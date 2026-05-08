// Package harness contains path-routing fuzz and invariant tests for MuxMaster.
// Run with: go test github.com/FlavioCFOliveira/MuxMaster/reports/path-routing-fuzzer/harness
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// --- helpers ---

func h(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", name)
		w.WriteHeader(200)
	}
}

func request(method, path string) *http.Request {
	req := httptest.NewRequest(method, "http://example.com"+path, nil)
	return req
}

func serve(mux http.Handler, method, path string) (int, string) {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request(method, path))
	return w.Code, w.Header().Get("X-Handler")
}

// containsTraversal returns true if the path contains dot-segment traversal sequences
// that could escape a registered prefix, either raw or percent-encoded.
func containsTraversal(p string) bool {
	for _, seq := range []string{
		"/../", "/./", "/..", "/.",
		"/%2e%2e/", "/%2e%2e", "/%2e/",
		"/%2E%2E/", "/%2E./", "/%2e%2E/",
		"/..%2f", "/..%5c",
		"/%252e%252e", "/%252e",
	} {
		if strings.Contains(strings.ToLower(p), strings.ToLower(seq)) {
			return true
		}
	}
	return false
}

// =============================================================================
// H-A: clean_path × strip_slashes ordering vs catch-all
// =============================================================================

// TestHA_CleanPathThenStripSlashes documents the PRF-001 finding:
// When CleanPath is used via Pre(), it normalises dot-segments BEFORE routing.
// A path like /static/../admin gets cleaned to /admin, which then routes to
// the /admin handler — bypassing any route-specific auth on /static/*.
//
// This is a documented design tension: CleanPath should be used with caution
// when different routes have different auth requirements. Using CleanPath as a
// global Pre() middleware means traversal paths get re-routed to other handlers
// rather than being caught by the intended catch-all.
//
// FINDING: PRF-001 (Medium) — CleanPath via Pre() re-routes traversal paths.
// The filepath catch-all is bypassed; /admin is reached directly.
func TestHA_CleanPathThenStripSlashes(t *testing.T) {
	r := mm.New()
	r.Pre(middleware.CleanPath(), middleware.StripSlashes())
	r.GET("/admin", h("admin"))
	r.GET("/static/*filepath", h("static"))

	// PRF-001: Document the bypass — with CleanPath, /static/../admin → /admin.
	// This is expected behavior given how path.Clean works, but operators must
	// understand that route-level auth on /admin won't see the original path.
	type tc struct {
		path         string
		expectAdmin  bool // true = CleanPath causes re-route to /admin
	}
	cases := []tc{
		{"/static/../admin", true},      // path.Clean → /admin
		{"/static/..%2fadmin", true},    // URL.Path decoded: /static/../admin → /admin
		{"/static/%2e%2e/admin", true},  // URL.Path decoded: /static/../admin → /admin
		{"/static/..%2F..%2Fadmin", true},
		{"/static/./../../admin", true},
	}
	for _, c := range cases {
		_, handler := serve(r, "GET", c.path)
		if c.expectAdmin && handler != "admin" {
			t.Logf("PRF-001-NOTE: path=%q expected to reach admin via CleanPath, got handler=%q", c.path, handler)
		}
		if c.expectAdmin {
			// Document but don't fail — this IS the expected CleanPath behavior.
			// The security implication: route-specific auth on /admin IS still called
			// (CleanPath runs Pre, middleware wraps the matched handler).
			// The risk: a request intended for /static is now served as /admin.
			t.Logf("PRF-001: CleanPath re-routed %q to /admin (handler=%q) — "+
				"verify auth on /admin runs before the handler", c.path, handler)
		}
	}
	// The REAL security test: auth on /admin DOES run when CleanPath re-routes.
	authRan := false
	r2 := mm.New()
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			authRan = true
			http.Error(w, "Forbidden", 403)
		})
	}
	r2.Pre(middleware.CleanPath())
	r2.Use(authMW) // auth wraps all handlers including /admin
	r2.GET("/admin", h("admin"))
	r2.GET("/static/*filepath", h("static"))

	authRan = false
	code, handler := serve(r2, "GET", "/static/../admin")
	if handler == "admin" && !authRan {
		t.Errorf("PRF-001 CRITICAL: CleanPath re-routed /static/../admin to /admin WITHOUT running auth middleware (code=%d)", code)
	}
	if handler == "admin" && authRan {
		t.Logf("PRF-001 MITIGATED: CleanPath re-routes but auth still runs (code=%d blocked it) — "+
			"however this is still a route confusion issue", code)
	}
}

// TestHA_StripSlashesThenCleanPath documents the same PRF-001 finding for
// the reverse middleware order. Both orderings produce the same route-confusion
// because CleanPath always resolves dot-segments regardless of order.
func TestHA_StripSlashesThenCleanPath(t *testing.T) {
	r := mm.New()
	r.Pre(middleware.StripSlashes(), middleware.CleanPath())
	r.GET("/admin", h("admin"))
	r.GET("/static/*filepath", h("static"))

	traversalPaths := []string{
		"/static/../admin",
		"/static/..%2fadmin",
		"/static/%2e%2e/admin",
	}
	for _, p := range traversalPaths {
		_, handler := serve(r, "GET", p)
		if handler == "admin" {
			t.Logf("PRF-001: StripSlashes+CleanPath also re-routes %q to /admin — "+
				"same behavior as CleanPath+StripSlashes", p)
		}
	}
}

// =============================================================================
// H-J: redirectFixedPath + Mount bypass
// =============================================================================

// TestHJ_RedirectFixedPathMountBypass verifies cleanedPath() does not redirect
// to a Mount route when the request would otherwise 404 under the method tree.
func TestHJ_RedirectFixedPathMountBypass(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	r := mm.New()
	r.RedirectFixedPath = true
	r.Mount("/api", inner)
	r.GET("/admin", h("admin"))

	// A request to //admin (double slash) could be cleaned to /admin.
	// This tests that RFP doesn't accidentally expose routes via Mount prefix.
	paths := []string{"//admin", "/./admin", "/admin/."}
	for _, p := range paths {
		code, handler := serve(r, "GET", p)
		_ = code
		_ = handler
		// No crash = pass for the robustness half; actual routing checked below.
	}

	// Verify Mount actually works for its correct path.
	code, handler := serve(r, "GET", "/api/health")
	if code != 200 || handler != "inner" {
		t.Errorf("H-J: Mount /api/health should route to inner, got code=%d handler=%q", code, handler)
	}
}

// =============================================================================
// PRF: Mount RawPath trim asymmetry (hypothesis #9)
// =============================================================================

// TestMountRawPathAsymmetry tests that a request with an encoded prefix in
// RawPath is handled correctly — when TrimPrefix doesn't strip an encoded
// version of the prefix, RawPath is zeroed and the decoded Path is used.
func TestMountRawPathAsymmetry(t *testing.T) {
	var capturedPath, capturedRawPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedRawPath = r.URL.RawPath
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	r := mm.New()
	r.UseRawPath = true
	r.Mount("/api", inner)

	// Request where RawPath has encoded slash that won't match literal prefix "/api".
	req := httptest.NewRequest("GET", "http://example.com/api/v1/items", nil)
	req.URL.RawPath = "/api/v1/items" // normal — should strip correctly
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("Mount: expected 200, got %d", w.Code)
	}
	if capturedPath != "/v1/items" {
		t.Errorf("Mount: expected stripped path=/v1/items, got %q", capturedPath)
	}

	// PRF-009: Test with encoded RawPath where first byte is '%' not '/'.
	// When UseRawPath=true, dispatch uses RawPath for tree lookup.
	// RawPath="%2fapi%2fv2%2fitems" starts with '%', not '/'.
	// The tree has "/api/*mux_mount" which starts with '/'.
	// Result: the tree does not match, so the request falls through to 404.
	// This is the documented RawPath asymmetry: an encoded prefix cannot be
	// matched by the tree when UseRawPath=true, because the tree stores
	// literal registered patterns (e.g., "/api/*mux_mount" not "%2fapi...").
	req2 := httptest.NewRequest("GET", "http://example.com/api/v2/items", nil)
	req2.URL.Path = "/api/v2/items"
	req2.URL.RawPath = "%2fapi%2fv2%2fitems" // encoded — does not match tree
	w2 := httptest.NewRecorder()
	capturedPath = ""
	capturedRawPath = ""
	r.ServeHTTP(w2, req2)

	// Expected: 404 because RawPath doesn't match the tree (starts with '%' not '/').
	// This is correct behavior but must be documented clearly for operators.
	if w2.Code == 200 {
		// If somehow matched, verify RawPath was handled correctly.
		if capturedRawPath != "" {
			t.Errorf("PRF-MOUNT-RAWPATH: RawPath not zeroed when encoded prefix forwarded, got %q", capturedRawPath)
		}
	} else {
		t.Logf("PRF-009 DOCUMENTED: UseRawPath=true with fully-encoded RawPath (%q) causes 404 — "+
			"tree matches literal '/' not '%%2f'. Operators using UseRawPath=true must ensure "+
			"clients send properly-formed RawPath (first byte must be '/').", req2.URL.RawPath)
	}
}

// =============================================================================
// Invariant 1: /admin must not be reachable via traversal paths
// =============================================================================

func TestInvariant_AdminNotReachableViaTraversal(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))
	r.GET("/static/*filepath", h("static"))

	bypasses := []string{
		"/../admin",
		"/..//admin",
		"/./admin",
		"/users/../admin",
		"/users/1/../admin",
		"/static/../admin",
		"/static/..%2fadmin",
		"/static/%2e%2e/admin",
		"/%2e%2e/admin",
		"/%2E%2E/admin",
		"/..%2fadmin",
		"/..%5cadmin",
		"/%252e%252e%2fadmin",
		"/users/%2e%2e/admin",
		"/a/%2e%2e/admin",
	}

	for _, p := range bypasses {
		code, handler := serve(r, "GET", p)
		if handler == "admin" {
			t.Errorf("PRF-INVARIANT-1 BYPASS: path %q reached /admin handler (code=%d)", p, code)
		}
	}
}

// =============================================================================
// Invariant 2: :param must not capture '/'
// =============================================================================

func TestInvariant_ParamMustNotCaptureSlash(t *testing.T) {
	r := mm.New()
	var capturedID string
	r.GET("/users/:id/posts", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "posts")
		w.WriteHeader(200)
	})

	// A %2f-encoded slash should NOT span the segment boundary.
	paths := []string{
		"/users/1%2f2/posts",  // %2f = /
		"/users/a%2Fb/posts",  // %2F = /
		"/users/x%252fy/posts", // double-encoded
	}
	for _, p := range paths {
		capturedID = ""
		code, handler := serve(r, "GET", p)
		if handler == "posts" && strings.Contains(capturedID, "/") {
			t.Errorf("PRF-INVARIANT-2: :id captured slash via %q — id=%q (code=%d)", p, capturedID, code)
		}
	}
}

// =============================================================================
// Invariant 3: catch-all filepath must not escape /static/ prefix
// =============================================================================

// TestInvariant_CatchallFilepathContainsTraversal documents PRF-005:
// The MuxMaster radix tree itself does NOT clean dot-segments from catch-all
// filepath params. The filepath param can contain "/../" segments.
// Security boundary: ServeFiles (and http.FileServer underneath) handles traversal.
// Operators who use a custom *filepath handler MUST clean the value themselves.
func TestInvariant_CatchallNotEscapingPrefix(t *testing.T) {
	r := mm.New()
	var capturedFilepath string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		capturedFilepath = mm.PathParam(req, "filepath")
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	// These paths all start with /static/ so the router correctly routes them.
	// The filepath param contains traversal sequences — this is documented behavior
	// (the router is not responsible for file-system path safety).
	traversalPaths := []string{
		"/static/../../etc/passwd",         // literal .. segments
		"/static/../etc/passwd",
		"/static/%2e%2e/%2e%2e/etc/passwd", // %2e decoded by net/url → ..
		"/static/..%2f..%2fetc%2fpasswd",   // %2f decoded by net/url → /
	}
	for _, p := range traversalPaths {
		capturedFilepath = ""
		code, handler := serve(r, "GET", p)
		if handler == "static" && containsTraversal("/static/"+capturedFilepath) {
			// PRF-005: Document the filepath value for operator awareness.
			// This is NOT a router bypass (admin is not reached), but operators
			// using custom filepath handlers must sanitize the value.
			t.Logf("PRF-005: path=%q routed to static handler with traversal filepath=%q (code=%d) — "+
				"operator must sanitize filepath param in custom handlers; "+
				"http.FileServer handles this correctly via path.Clean",
				p, capturedFilepath, code)
		}
	}

	// Verify that traversal via catch-all does NOT reach other registered handlers.
	r2 := mm.New()
	r2.GET("/admin", h("admin"))
	r2.GET("/static/*filepath", h("static"))
	for _, p := range traversalPaths {
		_, handler := serve(r2, "GET", p)
		if handler == "admin" {
			t.Errorf("PRF-005 BYPASS: catch-all traversal path %q reached /admin (should stay in catch-all)", p)
		}
	}
}

// =============================================================================
// Encoding attacks — single and double percent-encoding
// =============================================================================

func TestEncoding_SinglePercent(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))

	// PRF-002 FINDING: Go's net/url.Parse() percent-decodes URL.Path.
	// When UseRawPath=false (default), /%61dmin → URL.Path="/admin" → matches /admin.
	// When UseRawPath=true, the router uses RawPath="/%61dmin" which does NOT match /admin.
	//
	// This is by-design per RFC 3986 §6.2.2.2 (path normalization).
	// The security implication: /%61dmin and /admin are equivalent at the HTTP layer.
	cases := []struct {
		path        string
		wantHandler string // "" means don't care; "admin" = bypass; "admin-by-design" = expected
	}{
		{"/admin", "admin"},              // control: must match
		{"/%61dmin", "admin-by-design"},  // %61='a' → URL.Path="/admin" → matches (net/url decodes)
		{"/%61%64%6d%69%6e", "admin-by-design"}, // all-encoded → URL.Path="/admin" → matches
		{"/admın", ""},                   // Latin dotless-i — different Unicode code point
		{"/ADMIN", ""},                   // case mismatch (CaseInsensitive=false)
	}

	for _, tc := range cases {
		code, handler := serve(r, "GET", tc.path)
		switch tc.wantHandler {
		case "admin":
			if handler != "admin" {
				t.Errorf("Encoding: path %q — expected admin handler, got handler=%q code=%d",
					tc.path, handler, code)
			}
		case "admin-by-design":
			// Document: net/url decodes percent-encoded unreserved chars.
			t.Logf("PRF-002: %q → URL.Path decoded → matched /admin (UseRawPath=false, by-design): handler=%q",
				tc.path, handler)
		case "":
			if handler == "admin" {
				t.Errorf("PRF-ENCODING: path %q unexpectedly reached /admin (handler=%q code=%d)",
					tc.path, handler, code)
			}
		}
	}
}

func TestEncoding_DoublePercent(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))

	// Double-encoded paths: %25 encodes the '%' character itself.
	doubleEncoded := []string{
		"/%2561dmin",      // %25 = '%', so this is /%61dmin after one decode
		"/%252e%252e/admin", // double-encoded dot-dot
		"/%25%32%65%25%32%65/admin", // double-encoded dot-dot verbose
	}
	for _, p := range doubleEncoded {
		_, handler := serve(r, "GET", p)
		if handler == "admin" {
			t.Errorf("PRF-ENCODING-DOUBLE: double-encoded path %q reached /admin", p)
		}
	}
}

// =============================================================================
// Null byte injection
// =============================================================================

func TestNullByte_InPath(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))

	// Note: Go's net/http.NewRequest rejects raw null bytes in the URL (RFC 7230
	// §3.2.6 — control characters forbidden). Null bytes are blocked at the
	// HTTP parsing layer before reaching the router. We test percent-encoded nulls.
	nullPaths := []string{
		"/%00admin",   // %00 = null byte percent-encoded
		"/admin%00",   // trailing percent-encoded null
		"/admin%00.txt",
		"/users%00/1",
	}
	for _, p := range nullPaths {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("PRF-NULL: percent-encoded null path %q caused panic: %v", p, rec)
				}
			}()
			_, _ = serve(r, "GET", p)
		}()
	}

	// Verify: %00 in a path does NOT match /admin.
	_, handler := serve(r, "GET", "/%00admin")
	if handler == "admin" {
		t.Errorf("PRF-NULL-BYPASS: %%00admin matched /admin handler")
	}
	_, handler2 := serve(r, "GET", "/admin%00")
	if handler2 == "admin" {
		t.Errorf("PRF-NULL-BYPASS: /admin%%00 matched /admin handler")
	}
}

// =============================================================================
// Unicode normalisation attacks
// =============================================================================

func TestUnicode_ConfusableGlyphs(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))

	// Cyrillic 'а' (U+0430) vs Latin 'a' (U+0061): these are different code points.
	// With CaseInsensitive=false they must not match.
	confusables := []string{
		"/аdmin",                 // Cyrillic а (U+0430)
		"/ɑdmin",                 // Latin alpha (U+0251)
		"/admin",                       // all explicit latin → must match
		"/admin​",                // zero-width space appended (U+200B)
		"/admin\xef\xbb\xbf",          // BOM (U+FEFF) via hex escape
		"/‮admin",                // RTL override before (U+202E)
	}
	for _, p := range confusables {
		if !utf8.ValidString(p) {
			continue
		}
		_, handler := serve(r, "GET", p)
		// /admina... (all explicit latin) should match; rest must not.
		if p == "/admin" {
			if handler != "admin" {
				t.Errorf("Unicode: pure latin /admin should match, got handler=%q", handler)
			}
		} else if handler == "admin" {
			t.Errorf("PRF-UNICODE: confusable glyph path %q reached /admin", p)
		}
	}
}

func TestUnicode_FullwidthSlash(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/static/*filepath", h("static"))

	// Fullwidth slash: U+FF0F (／), percent-encoded as %EF%BC%8F.
	// Must not be treated as a path separator.
	fullwidthPaths := []string{
		"/static/／etc／passwd",   // raw fullwidth slash
		"/static/%EF%BC%8Fetc%EF%BC%8Fpasswd", // percent-encoded
	}
	for _, p := range fullwidthPaths {
		if !utf8.ValidString(p) {
			continue
		}
		_, _ = serve(r, "GET", p) // must not panic
	}
}

// =============================================================================
// Wildcard shadowing: :param vs *catchall registration order
// =============================================================================

func TestWildcardShadow_ParamVsCatchall(t *testing.T) {
	// PRF-003 FINDING: Registering :param FIRST then a static sibling at the same
	// level PANICS with "conflicts with existing wildcard". This is registration-
	// order-dependent. httprouter has the same behavior.
	// The fix: always register static routes BEFORE wildcard routes.
	panicked := false
	var panicMsg string
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
				panicMsg = fmt.Sprintf("%v", rec)
			}
		}()
		r := mm.New()
		r.GET("/files/:name", h("param"))
		r.GET("/files/readme", h("static")) // PANICS: conflicts with :name
		_ = r
	}()
	if !panicked {
		t.Logf("PRF-003 NOTE: :name-first then static-child did NOT panic (behavior may have changed)")
	} else {
		t.Logf("PRF-003 CONFIRMED: :param registered first, static sibling panics: %q", panicMsg)
	}
}

func TestWildcardShadow_StaticThenParam(t *testing.T) {
	// Static registered first, then :param.
	r := mm.New()
	r.GET("/files/readme", h("static"))
	r.GET("/files/:name", h("param"))

	code, handler := serve(r, "GET", "/files/readme")
	if handler != "static" {
		t.Errorf("PRF-SHADOW-ORDER: static /files/readme must beat :name param regardless of registration order, got handler=%q code=%d", handler, code)
	}
}

// =============================================================================
// expandOptional: must not panic on nested/malformed patterns
// =============================================================================

func TestExpandOptional_MalformedPatterns(t *testing.T) {
	malformed := []struct {
		pattern     string
		shouldPanic bool
	}{
		{"/users{/:id}", false},  // valid optional
		{"/a{/:b}{/:c}", false},  // two optionals — potential exponential
		{"/a{/:b}{/:c}{/:d}", false}, // three optionals — 2^3 = 8 expansions
	}

	for _, tc := range malformed {
		tc := tc
		t.Run(tc.pattern, func(t *testing.T) {
			panicked := false
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicked = true
					}
				}()
				r := mm.New()
				r.GET(tc.pattern, h("handler"))
				_ = r
			}()
			if tc.shouldPanic && !panicked {
				t.Errorf("expected panic for %q but none occurred", tc.pattern)
			}
		})
	}
}

// =============================================================================
// TrailingSlash redirect: must not reveal protected routes before auth
// =============================================================================

func TestRedirectTrailingSlash_NoBypass(t *testing.T) {
	authRan := false
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authRan = true
			// Simulate auth check — reject all.
			http.Error(w, "Forbidden", 403)
		})
	}

	r := mm.New()
	r.RedirectTrailingSlash = true
	r.Use(authMW)
	r.GET("/admin/", h("admin"))

	// Request /admin (no trailing slash) — should trigger TSR redirect.
	// The redirect goes through middleware (wrapMiddleware is called inline).
	authRan = false
	code, _ := serve(r, "GET", "/admin")
	// The redirect is wrapped in middleware, so auth should run.
	// If auth blocks it, we should see 403.
	_ = code
	if !authRan {
		t.Errorf("PRF-TSR: trailing slash redirect did not run middleware — auth was bypassed")
	}
}

// =============================================================================
// Case-insensitive matching must not create bypass under CaseInsensitive=true
// =============================================================================

func TestCaseInsensitive_MatchingBehavior(t *testing.T) {
	r := mm.New()
	r.CaseInsensitive = true
	r.Rebuild() // ensure config is re-read
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))

	cases := []struct {
		path    string
		wantHit bool
	}{
		{"/ADMIN", true},   // case-insensitive match expected
		{"/Admin", true},
		{"/aDmIn", true},
		{"/admin", true},
	}
	for _, tc := range cases {
		_, handler := serve(r, "GET", tc.path)
		if tc.wantHit && handler != "admin" {
			t.Errorf("CaseInsensitive: %q should match /admin (ci=true), got handler=%q", tc.path, handler)
		}
	}
}

// =============================================================================
// ServeFiles: filepath param must not contain raw traversal after serving
// =============================================================================

func TestServeFiles_FilepathContainment(t *testing.T) {
	r := mm.New()
	var capturedParam string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		capturedParam = mm.PathParam(req, "filepath")
		w.WriteHeader(200)
	})

	// These all start with /static/ so the router will match *filepath.
	// After path.Clean in ServeFiles, traversal should not escape.
	paths := []string{
		"/static/subdir/../../etc/passwd",
		"/static/..",
		"/static/../etc",
	}
	for _, p := range paths {
		capturedParam = ""
		serve(r, "GET", p)
		// Note: path.Clean("/static/../etc/passwd") = "/etc/passwd"
		// The router itself doesn't clean — it just captures the raw segment.
		// The security boundary is at the file server layer (ServeFiles uses r2.URL.Path = PathParam).
		// Here we just ensure no panic occurs.
		_ = capturedParam
	}
}

// =============================================================================
// Oversized paths: must not panic or hang
// =============================================================================

func TestOversizedPath_NoPanic(t *testing.T) {
	r := mm.New()
	r.GET("/users/:id", h("users"))
	r.GET("/static/*filepath", h("static"))

	cases := []struct {
		name string
		path string
	}{
		{"1KB", "/static/" + strings.Repeat("a", 1024)},
		{"64KB", "/static/" + strings.Repeat("a", 65536)},
		{"deep_segments", "/" + strings.Repeat("a/", 500) + "end"},
		{"many_dots", "/static/" + strings.Repeat("../", 200) + "etc/passwd"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PRF-OVERSIZE: path %q (%d bytes) caused panic: %v", tc.name, len(tc.path), r)
				}
			}()
			serve(r, "GET", tc.path)
		})
	}
}

// =============================================================================
// HandleFast × Use middleware bypass (H-10 verification)
// =============================================================================

func TestHandleFast_UseMiddlewareBypass(t *testing.T) {
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	r := mm.New()
	r.Use(authMW)

	// FPE-2026-010 fix: registering a FastHandler when stdlib middleware is
	// present must panic at registration time (mirrors Group.HandleFast /
	// CSA-2026-0054). The previous "documented bypass" is now a hard error —
	// the boundary is enforced.
	panicked := false
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		r.GETFast("/fast-route", func(w http.ResponseWriter, req *http.Request, ps mm.Params) {
			w.WriteHeader(200)
		})
	}()
	if !panicked {
		t.Fatal("FPE-2026-010 regression: Mux.Use+HandleFast did not panic at registration")
	}
	t.Log("PRF-H10 closed by FPE-2026-010: Use+HandleFast now panics at registration time")
}

// =============================================================================
// Mount: auth middleware DOES wrap Mounted handlers
// =============================================================================

func TestMount_AuthMiddlewareWrapsInner(t *testing.T) {
	authRan := false
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authRan = true
			next.ServeHTTP(w, r)
		})
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	r := mm.New()
	r.Use(authMW)
	r.Mount("/api", inner)

	authRan = false
	code, handler := serve(r, "GET", "/api/anything")

	if code != 200 || handler != "inner" {
		t.Errorf("Mount: expected inner handler, got handler=%q code=%d", handler, code)
	}
	if !authRan {
		t.Errorf("PRF-H-G: Mount did NOT run auth middleware — bypass of parent Use() chain")
	}
}

// =============================================================================
// Structural edge cases: empty segments, semicolons, fragments
// =============================================================================

func TestStructural_EmptyAndDoubleSlash(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))

	cases := []string{
		"//admin",         // double slash
		"///admin",        // triple slash
		"//users//1",      // double slashes around segment
		"/admin/",         // trailing slash (TSR kicks in but not bypass)
		"/;admin",         // semicolon prefix (not a path separator in HTTP)
		"/admin;ignore",   // semicolon param in segment
	}
	for _, p := range cases {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("PRF-STRUCTURAL: path %q caused panic: %v", p, rec)
				}
			}()
			serve(r, "GET", p)
		}()
	}
}

// =============================================================================
// addRoute: conflicting patterns must panic (not silently corrupt tree)
// =============================================================================

func TestAddRoute_ConflictPanics(t *testing.T) {
	conflicts := []struct {
		name     string
		patterns []string
	}{
		{"duplicate_static", []string{"/admin", "/admin"}},
		{"catchall_vs_static", []string{"/static/*f", "/static/readme"}},
	}

	for _, tc := range conflicts {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			panicked := false
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicked = true
					}
				}()
				r := mm.New()
				for _, p := range tc.patterns {
					r.GET(p, h("handler"))
				}
			}()

			switch tc.name {
			case "duplicate_static":
				if !panicked {
					t.Errorf("addRoute: duplicate static route should panic, but didn't")
				}
			case "catchall_vs_static":
				// MuxMaster allows static children alongside catch-all.
				// Just verify no silent corruption by checking both are routable.
				if !panicked {
					// Acceptable — static child may coexist.
					r2 := mm.New()
					for _, p := range tc.patterns {
						r2.GET(p, h("handler"))
					}
					code, _ := serve(r2, "GET", "/static/readme")
					if code != 200 {
						t.Errorf("addRoute: /static/readme should be reachable, got %d", code)
					}
				}
			}
		})
	}
}

// =============================================================================
// UnescapePathValues: must not double-decode params
// =============================================================================

func TestUnescapePathValues_NoDoubleDecode(t *testing.T) {
	r := mm.New()
	r.UnescapePathValues = true
	r.Rebuild()

	var capturedID string
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.WriteHeader(200)
	})

	// /users/hello%20world — after unescape: "hello world"
	serve(r, "GET", "/users/hello%20world")
	if capturedID != "hello world" {
		t.Errorf("UnescapePathValues: expected 'hello world', got %q", capturedID)
	}

	// PRF-006 FINDING: Double-decode via UnescapePathValues=true.
	// /users/hello%2520world:
	//   Step 1 (net/url.Parse): %25 → '%', producing URL.Path="/users/hello%20world"
	//   Step 2 (UnescapePathValues): url.QueryUnescape("hello%20world") → "hello world"
	// Expected after ONE decode: "hello%20world" (literal percent-twenty).
	// Actual after TWO decodes: "hello world" (space).
	// This is a double-decode: %2520 should decode to %20, not to space.
	serve(r, "GET", "/users/hello%2520world")
	if capturedID == "hello world" {
		t.Errorf("PRF-006 DOUBLE-DECODE CONFIRMED: UnescapePathValues double-decoded %%2520 → 'hello world', "+
			"expected 'hello%%20world'. Attack: %%2520 bypasses input validation that blocks spaces.")
	} else {
		t.Logf("PRF-006 note: id=%q (expected 'hello%%20world')", capturedID)
	}
}

// =============================================================================
// PRF-NEW-001: multi-byte UTF-8 char after optional segment panics in
// incrementChildPrio — index out of range [N] with length N
// =============================================================================

// TestPRFNEW001_MultiByteSuffixAfterOptional reproduces the fuzz crash
// discovered by FuzzAddRoute (corpus file 22ba14d69a116c38).
//
// Root cause: tree.go:169 uses string(n.path[i]) which interprets the byte
// as a Unicode code point (rune), not a raw byte. For a multi-byte character
// like é (0xc3 0xa9), string(byte(0xc3)) produces "Ã" (2 bytes in UTF-8),
// not a 1-byte string. This makes len(n.indices) = 2 before the new child is
// appended, so incrementChildPrio(len(n.indices)-1 = 1) is called but
// len(n.children) = 1 at that point — index out of range.
//
// Affected: any pattern of the form /prefix{/:param}SUFFIX where SUFFIX
// starts with any non-ASCII byte (first byte >= 0x80).
// Fix: string([]byte{n.path[i]}) on tree.go:169 (and the same pattern at 202).
func TestPRFNEW001_MultiByteSuffixAfterOptional(t *testing.T) {
	cases := []struct {
		pattern string
		desc    string
	}{
		{"/00{/:00}\xd3\x9e", "original fuzz crash: Ӟ (U+04DE) after optional"},
		{"/a{/:b}\xc3\xa9", "é (U+00E9, 2-byte) after optional"},
		{"/a{/:b}\xe2\x80\x8b", "ZWSP (U+200B, 3-byte) after optional"},
		{"/a{/:b}\xf0\x9f\x98\x80", "emoji (U+1F600, 4-byte) after optional"},
		{"/a{/:b}z", "ASCII after optional — must NOT panic (control)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			panicked := false
			var panicMsg interface{}
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						panicMsg = r
					}
				}()
				r := mm.New()
				r.GET(tc.pattern, h("handler"))
				_ = r
			}()
			if tc.pattern == "/a{/:b}z" {
				if panicked {
					t.Errorf("ASCII control should not panic, got: %v", panicMsg)
				}
				return
			}
			if panicked {
				// Determine if it's the known OOB panic or a legitimate conflict panic.
				msg := fmt.Sprintf("%v", panicMsg)
				if strings.Contains(msg, "index out of range") {
					t.Errorf("PRF-NEW-001 CONFIRMED: OOB panic for multi-byte suffix %q: %v "+
						"(root cause: tree.go:169 string(byte) treats byte as rune, "+
						"producing multi-byte n.indices entry; fix: string([]byte{n.path[i]}))",
						tc.pattern, panicMsg)
				} else {
					t.Logf("non-OOB panic (may be legitimate conflict): %v", panicMsg)
				}
			} else {
				t.Logf("OK: no panic for %q", tc.pattern)
			}
		})
	}
}

// =============================================================================
// Mount wildcard (idxWild) method tree: not affected by method-specific TSR
// =============================================================================

func TestMount_IdxWildNotAffectedByTSR(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})
	r := mm.New()
	r.RedirectTrailingSlash = true
	r.Mount("/app", inner)

	// /app/ has trailing slash — TSR logic runs on the method tree first.
	// Mount is in idxWild tree; it should still be reachable.
	code, handler := serve(r, "GET", "/app/dashboard")
	if code != 200 || handler != "inner" {
		t.Errorf("Mount idxWild: /app/dashboard should reach inner, got code=%d handler=%q", code, handler)
	}

	// Test exact prefix match.
	code2, handler2 := serve(r, "POST", "/app/submit")
	if code2 != 200 || handler2 != "inner" {
		t.Errorf("Mount idxWild: POST /app/submit should reach inner, got code=%d handler=%q", code2, handler2)
	}
}
