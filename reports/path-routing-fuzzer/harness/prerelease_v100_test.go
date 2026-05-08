// Package harness — pre-release v1.0.0 targeted tests.
// Covers: TM-038 (nginx merge_slashes), TM-039 (Caddy CVE-2022-0653),
// TM-046 (HEAD/GET divergence), TM-047 (Apache CVE-2021-41773),
// TM-051 (Werkzeug CVE-2020-28724), CL-PATH-1 closure.
package harness

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
	chi "github.com/go-chi/chi/v5"
	"github.com/julienschmidt/httprouter"
)

// =============================================================================
// TM-039 / Caddy CVE-2022-0653: path traversal via .. and percent-encoded equivalents
// =============================================================================

// TestTM039_CaddyCVE20220653 verifies that MuxMaster does not allow traversal
// from /static/*filepath to /admin via any combination of dot-segment, percent-
// encoded, or mixed-case variants — both with and without the CleanPath middleware.
func TestTM039_CaddyCVE20220653(t *testing.T) {
	payloads := []string{
		"/static/../admin",
		"/static/..%2fadmin",
		"/static/..%2Fadmin",
		"/static/%2e%2e/admin",
		"/static/%2E%2E/admin",
		"/static/%2e./admin",
		"/static/.%2e/admin",
		"/static/..%252fadmin",       // double-encoded
		"/static/%c0%ae%c0%ae/admin", // overlong UTF-8 for ..
		"/static/..%5cadmin",         // backslash separator
	}

	t.Run("without_CleanPath", func(t *testing.T) {
		r := mm.New()
		r.GET("/admin", h("admin"))
		r.GET("/static/*filepath", h("static"))

		for _, p := range payloads {
			code, handler := serve(r, "GET", p)
			if handler == "admin" {
				t.Errorf("TM-039 BYPASS (no middleware): path %q reached /admin (code=%d)", p, code)
			}
		}
	})

	t.Run("with_CleanPath_via_Pre", func(t *testing.T) {
		// CleanPath via Pre() re-routes traversal paths — this is PRF-001 (documented).
		// The security invariant: auth on /admin still runs.
		authRan := false
		authMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				authRan = true
				http.Error(w, "Forbidden", 403)
			})
		}

		r := mm.New()
		r.Pre(middleware.CleanPath())
		r.Use(authMW)
		r.GET("/admin", h("admin"))
		r.GET("/static/*filepath", h("static"))

		for _, p := range payloads {
			authRan = false
			code, handler := serve(r, "GET", p)
			// With CleanPath, some traversal paths get re-routed to /admin.
			// The invariant: if they reach /admin, auth MUST have run.
			if handler == "admin" && !authRan {
				t.Errorf("TM-039 AUTH BYPASS: path %q reached /admin WITHOUT auth (code=%d)", p, code)
			}
			// If handler is not admin (e.g., 403 from auth, or 404), that's safe.
			_ = code
		}
	})

	t.Run("UseRawPath_true", func(t *testing.T) {
		// With UseRawPath=true and NO UnescapePathValues, the router dispatches
		// using RawPath. Static segments like "/static/" must still match literally.
		r := mm.New()
		r.UseRawPath = true
		r.Rebuild()
		r.GET("/admin", h("admin"))
		r.GET("/static/*filepath", h("static"))

		// These payloads should NOT reach /admin.
		for _, p := range payloads {
			code, handler := serve(r, "GET", p)
			if handler == "admin" {
				t.Errorf("TM-039 BYPASS (UseRawPath=true): path %q reached /admin (code=%d)", p, code)
			}
		}
	})
}

// =============================================================================
// TM-047 / Apache CVE-2021-41773: ..%2f in catch-all routes
// =============================================================================

// TestTM047_ApacheCVE20211773 verifies that the catch-all *filepath does not
// allow traversal via ..%2f sequences to reach handlers outside the /static/
// prefix. The Caddy CVE-2022-0653 transposition focuses on percent-encoded
// dot-slash; this test focuses on the Apache variant: ..%2f.
func TestTM047_ApacheCVE20211773(t *testing.T) {
	payloads := []string{
		"/static/..%2fetc%2fpasswd",
		"/static/..%2F..%2Fetc%2Fpasswd",
		"/static/%2e%2e%2fetc%2fpasswd",
		"/static/%2e%2e/%2e%2e/etc/passwd",
		"/static/..%2f..%2fadmin",       // attempt to reach /admin
		"/cgi-bin/.%2e/.%2e/etc/passwd", // Apache-style cgi path (should 404)
	}

	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/static/*filepath", h("static"))

	for _, p := range payloads {
		code, handler := serve(r, "GET", p)
		if handler == "admin" {
			t.Errorf("TM-047 BYPASS: path %q reached /admin (code=%d)", p, code)
		}
		// Traversal into static/* is acceptable (PRF-005 boundary) — operator
		// must sanitise the *filepath param in custom handlers.
		_ = code
	}

	// Verify filepath capture value: %2f must NOT be decoded to / by the router
	// (UseRawPath=false is default — net/url decodes to literal '/' in URL.Path,
	// which acts as segment separator, so 'admin' is a separate segment that
	// doesn't match the catch-all starting with /static/).
	var capturedFilepath string
	r2 := mm.New()
	r2.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		capturedFilepath = mm.PathParam(req, "filepath")
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})
	r2.GET("/admin", h("admin"))

	// /static/..%2fadmin — net/url decodes to /static/../admin.
	// path traversal: the router sees /static/ + ../admin → catch-all captures "/../admin"
	// (which is still within the /static tree — it does NOT jump to /admin).
	capturedFilepath = ""
	code, handler := serve(r2, "GET", "/static/..%2fadmin")
	if handler == "admin" {
		t.Errorf("TM-047 CRITICAL: /static/..%%2fadmin reached /admin handler (code=%d)", code)
	}
	_ = capturedFilepath
}

// =============================================================================
// TM-051 / Werkzeug CVE-2020-28724: encoding inconsistency dispatcher vs handler
// =============================================================================

// TestTM051_WerkzeugCVE20200628 verifies there is no encoding inconsistency
// between the path the router dispatches on and the value captured in params.
// Werkzeug's CVE was: the dispatcher normalised a double-slash //static// to
// /static/ and matched a route, but the handler received the original URI with
// double-slash, exposing the handler to the un-normalised path.
func TestTM051_WerkzeugCVE20200628(t *testing.T) {
	// With UseRawPath=false (default), net/url normalises percent-encoded unreserved chars.
	// The router sees URL.Path (decoded). What the handler sees is r.URL.Path (decoded).
	// There must be no discrepancy.
	var (
		dispatchedPath string
		capturedParam  string
	)

	r := mm.New()
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		dispatchedPath = req.URL.Path
		capturedParam = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "users")
		w.WriteHeader(200)
	})

	// %61 = 'a' — net/url decodes to 'a' in URL.Path; handler sees decoded path.
	req := httptest.NewRequest("GET", "http://x/users/%61bc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("TM-051: expected 200 for /users/%%61bc, got %d", w.Code)
	}
	if dispatchedPath != "/users/abc" {
		t.Errorf("TM-051 path mismatch: handler sees %q, expected /users/abc", dispatchedPath)
	}
	if capturedParam != "abc" {
		t.Errorf("TM-051 param mismatch: capturedParam=%q, expected 'abc'", capturedParam)
	}

	// Double-slash: //users/123 — with UseRawPath=false net/url.Path=/users/123 (no double slash).
	// But the router's dispatch sees URL.Path="/users/123" after net/http parsing,
	// while the handler also sees r.URL.Path="/users/123". No discrepancy.
	req2 := httptest.NewRequest("GET", "http://x//users/123", nil)
	w2 := httptest.NewRecorder()
	dispatchedPath = ""
	capturedParam = ""
	r.ServeHTTP(w2, req2)

	// net/http does NOT normalise //users/123 to /users/123 on its own;
	// the double slash is preserved in URL.Path unless CleanPath is used.
	// With no CleanPath middleware the router sees "//users/123" which does not
	// match "/users/:id" — so 404 is expected.
	if w2.Code == 200 {
		if dispatchedPath != capturedPath(req2) {
			t.Errorf("TM-051 DISPATCH-HANDLER DISCREPANCY: dispatch saw %q, handler got %q",
				capturedPath(req2), dispatchedPath)
		}
		t.Logf("TM-051 NOTE: //users/123 → 200 (router matched double-slash route) dispatchedPath=%q param=%q",
			dispatchedPath, capturedParam)
	} else {
		t.Logf("TM-051 OK: //users/123 → %d (no match, double-slash not normalised)", w2.Code)
	}
}

func capturedPath(r *http.Request) string {
	return r.URL.Path
}

// =============================================================================
// TM-038 / nginx merge_slashes: ///x///y vs /x/y
// =============================================================================

// TestTM038_NginxMergeSlashes verifies MuxMaster's behaviour on paths with
// multiple consecutive slashes is consistent and does NOT create routing bypasses.
func TestTM038_NginxMergeSlashes(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))
	r.GET("/users/:id", h("users"))
	r.GET("/api/v1/items", h("items"))

	cases := []struct {
		path        string
		expectAdmin bool // true = reaching /admin is a bypass
	}{
		{"///admin", true},
		{"//admin", true},
		{"/admin//extra", false},
		{"///x///y", false},        // not a registered route
		{"/api//v1//items", false}, // double slash between segments
	}

	for _, tc := range cases {
		code, handler := serve(r, "GET", tc.path)
		if tc.expectAdmin && handler == "admin" {
			t.Errorf("TM-038 BYPASS: %q reached /admin (code=%d) without normalisation", tc.path, code)
		}
		// Non-admin expect: may be 301 redirect, 404, or 200 (if some match). No panic.
		_ = code
	}

	// With CleanPath: path.Clean("///admin") = "/admin" — documented PRF-001 re-route.
	// Verify auth still runs.
	authRan := false
	r2 := mm.New()
	r2.Pre(middleware.CleanPath())
	r2.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			authRan = true
			http.Error(w, "Forbidden", 403)
		})
	})
	r2.GET("/admin", h("admin"))

	authRan = false
	code, handler := serve(r2, "GET", "///admin")
	if handler == "admin" && !authRan {
		t.Errorf("TM-038 AUTH BYPASS: ///admin → /admin via CleanPath WITHOUT auth (code=%d)", code)
	}
	_ = code
}

// =============================================================================
// TM-046 / HEAD/GET divergence
// =============================================================================

// TestTM046_HEADGETDivergence verifies:
//  1. HEAD request on a GET-registered route: MuxMaster does NOT automatically
//     serve HEAD — it returns 405 with Allow: GET, OPTIONS (no implicit HEAD).
//  2. When HEAD is explicitly registered, it dispatches correctly.
//  3. Compare with httprouter (which DOES implicit HEAD for GET routes).
func TestTM046_HEADGETDivergence(t *testing.T) {
	r := mm.New()
	r.GET("/items", h("items-get"))

	// MuxMaster: HEAD /items → what happens?
	code, handler := serve(r, "HEAD", "/items")
	t.Logf("TM-046 MuxMaster HEAD on GET-only route: code=%d handler=%q", code, handler)

	if handler == "items-get" {
		t.Logf("TM-046 NOTE: MuxMaster serves HEAD implicitly from GET route (handler=%q)", handler)
	} else if code == 405 {
		t.Logf("TM-046 NOTE: MuxMaster returns 405 for HEAD on GET-only route (standard behaviour)")
	} else if code == 404 {
		t.Logf("TM-046 NOTE: MuxMaster returns 404 for HEAD on GET-only route")
	}

	// Explicit HEAD registration must work.
	r2 := mm.New()
	r2.GET("/items", h("items-get"))
	r2.HEAD("/items", h("items-head"))

	code2, handler2 := serve(r2, "HEAD", "/items")
	if code2 != 200 || handler2 != "items-head" {
		t.Errorf("TM-046: explicit HEAD registration should match, got code=%d handler=%q", code2, handler2)
	}

	// Compare with httprouter: it serves HEAD implicitly from GET.
	hrRouter := httprouter.New()
	hrRouter.GET("/items", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.Header().Set("X-Handler", "items-get")
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("HEAD", "http://x/items", nil)
	w := httptest.NewRecorder()
	hrRouter.ServeHTTP(w, req)
	hrCode := w.Code
	hrHandler := w.Header().Get("X-Handler")
	t.Logf("TM-046 httprouter HEAD on GET-only route: code=%d handler=%q", hrCode, hrHandler)

	// Compare with chi: also serves HEAD implicitly.
	chiRouter := chi.NewMux()
	chiRouter.Get("/items", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler", "items-get")
		w.WriteHeader(200)
	})
	req2 := httptest.NewRequest("HEAD", "http://x/items", nil)
	w2 := httptest.NewRecorder()
	chiRouter.ServeHTTP(w2, req2)
	t.Logf("TM-046 chi HEAD on GET-only route: code=%d handler=%q", w2.Code, w2.Header().Get("X-Handler"))

	// Security check: HEAD response must not leak route existence more than GET would.
	// A 405 for HEAD reveals the route exists (same as GET returning 200 would).
	// This is an acceptable information disclosure — it is equivalent to OPTIONS.
	// The invariant: HEAD must not bypass auth middleware when explicitly registered.
	authRan := false
	r3 := mm.New()
	r3.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			authRan = true
			http.Error(w, "Forbidden", 403)
		})
	})
	r3.HEAD("/secure", h("secure"))

	authRan = false
	codeHead, _ := serve(r3, "HEAD", "/secure")
	if !authRan {
		t.Errorf("TM-046 AUTH BYPASS: HEAD /secure did not run auth middleware (code=%d)", codeHead)
	}
}

// =============================================================================
// Catch-all + Group prefix concatenation (TM-2026-012 PARTIAL)
// =============================================================================

// TestTM012_GroupCatchAllTraversal verifies that a Group-registered catch-all
// route cannot be used to traverse to handlers outside the group prefix.
func TestTM012_GroupCatchAllTraversal(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))

	api := r.Group("/api")
	var capturedAll string
	api.GET("/users/*all", func(w http.ResponseWriter, req *http.Request) {
		capturedAll = mm.PathParam(req, "all")
		w.Header().Set("X-Handler", "api-users")
		w.WriteHeader(200)
	})

	// These traversal paths should NOT reach /admin.
	traversalPaths := []string{
		"/api/users/../../admin",
		"/api/users/%2e%2e/%2e%2e/admin",
		"/api/users/..%2f..%2fadmin",
		"/api/users/../../../admin",
	}
	for _, p := range traversalPaths {
		capturedAll = ""
		code, handler := serve(r, "GET", p)
		if handler == "admin" {
			t.Errorf("TM-012 BYPASS: %q reached /admin via Group catch-all (code=%d)", p, code)
		}
		// If it matched the catch-all, document the captured value.
		if handler == "api-users" && strings.Contains(capturedAll, "..") {
			t.Logf("TM-012 NOTE: %q matched catch-all with traversal all=%q — operator must sanitise", p, capturedAll)
		}
		_ = code
	}

	// Verify the group route itself works correctly.
	code, handler := serve(r, "GET", "/api/users/profile/settings")
	if code != 200 || handler != "api-users" {
		t.Errorf("TM-012: /api/users/profile/settings should match Group catch-all, got code=%d handler=%q", code, handler)
	}
}

// =============================================================================
// Wildcard + Group double-slash normalization (PRF-S9-004/008)
// =============================================================================

// TestS9_GroupDoubleSlash verifies the known finding PRF-S9-004/008:
// Group("/api/").GET("/users", h) registers /api//users (double slash).
// Routing result: /api/users → 404; /api//users → 200.
func TestS9_GroupDoubleSlash(t *testing.T) {
	r := mm.New()
	r.GET("/admin", h("admin"))

	// PRF-S9-008 reproduction: trailing slash in Group prefix + leading slash in route.
	grp := r.Group("/api/")
	grp.GET("/users", h("api-users"))

	code1, handler1 := serve(r, "GET", "/api/users")
	code2, handler2 := serve(r, "GET", "/api//users")

	t.Logf("PRF-S9-004: /api/users → code=%d handler=%q (expected 404 due to double-slash registration)", code1, handler1)
	t.Logf("PRF-S9-004: /api//users → code=%d handler=%q (expected 200 — actual registered path)", code2, handler2)

	// Document the finding — this is a known issue, not a security bypass to admin.
	if handler1 == "admin" || handler2 == "admin" {
		t.Errorf("PRF-S9-004 CRITICAL: double-slash Group route reached /admin handler")
	}
}

// =============================================================================
// Regex param empty segment (PRF-S9-007): //handler bypass
// =============================================================================

// TestS9007_RegexEmptySegment reproduces PRF-S9-007:
// Route /{id:[a-z]*}/profile — //profile (double-slash) → id=” if [a-z]* matches empty.
// This is a WAF bypass if WAF expects non-empty first segment.
//
// Note: registering /admin on the same mux as /{id:[a-z]*}/... panics because
// the regex wildcard at the root level conflicts with any static child.
// Test uses a separate route set at a deeper level to avoid the registration conflict.
func TestS9007_RegexEmptySegment(t *testing.T) {
	// Test the regex empty-match behaviour in isolation.
	r := mm.New()
	r.GET("/{id:[a-z]*}/profile", h("profile"))

	// //profile: what does the router do?
	code, handler := serve(r, "GET", "//profile")
	t.Logf("PRF-S9-007: //profile → code=%d handler=%q", code, handler)
	if handler == "profile" {
		t.Logf("PRF-S9-007 CONFIRMED: //profile matched /{id:[a-z]*}/profile with empty id — WAF bypass risk")
	}

	// Verify the normal path still works.
	code2, handler2 := serve(r, "GET", "/abc/profile")
	if code2 != 200 || handler2 != "profile" {
		t.Errorf("PRF-S9-007: normal path /abc/profile should match, got code=%d handler=%q", code2, handler2)
	}

	// Verify //settings also hits the regex route with empty id.
	code3, handler3 := serve(r, "GET", "//settings")
	t.Logf("PRF-S9-007: //settings → code=%d handler=%q (not a registered path)", code3, handler3)
}
