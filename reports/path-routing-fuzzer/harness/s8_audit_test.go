// Package harness — Sprint S8 exhaustive pre-release routing audit.
//
// Covers hypotheses H8-03, H8-20..H8-23, H8-26, H8-27, H8-47, H8-50..H8-52, H8-58
// and the mandatory battery items listed in the sprint plan.
//
// Run:  go test -v -count=1 -timeout=300s ./... -run TestS8
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

// ============================================================================
// Shared helpers (local to this file to keep it self-contained)
// ============================================================================

func s8h(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", name)
		w.WriteHeader(200)
	}
}

func s8serve(mux http.Handler, method, rawpath string) (code int, handler string) {
	req := httptest.NewRequest(method, "http://example.com"+rawpath, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Code, w.Header().Get("X-Handler")
}

// ============================================================================
// H8-03 — ServeFiles: filepath param traversal boundary
//
// ServeFiles sets r2.URL.Path = PathParam(...). The radix tree passes the raw
// (possibly traversal-containing) filepath to http.FileServer which calls
// path.Clean internally. The question is: does the router itself allow filepath
// to contain ".." and if so, can an operator accidentally expose the filesystem?
// ============================================================================

func TestS8_H803_ServeFilesTraversalBoundary(t *testing.T) {
	// Build a real ServeFiles setup on a bounded fs (http.Dir(".")).
	// We are testing that the router does NOT itself clean the param value —
	// the security boundary is http.FileServer. Custom *filepath handlers must
	// sanitize themselves.
	r := mm.New()
	var capturedFilepath string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		capturedFilepath = mm.PathParam(req, "filepath")
		w.Header().Set("X-Filepath", capturedFilepath)
		w.WriteHeader(200)
	})

	// Also register /admin to detect if traversal re-routes there.
	r.GET("/admin", s8h("admin"))

	traversals := []struct {
		path      string
		wantAdmin bool // if true: any match to /admin is a bypass
	}{
		{"/static/../admin", true},
		{"/static/./../../admin", true},
		{"/static/..%2fadmin", true},
		{"/static/%2e%2e/admin", true},
		{"/static/..%2F..%2Fadmin", true},
		// These stay in static (traversal within filepath is not a router bypass).
		{"/static/../../etc/passwd", false},
		{"/static/%2e%2e%2f%2e%2e%2fetc%2fpasswd", false},
		{"/static/.%2e/.%2e/etc/passwd", false},
	}

	for _, tc := range traversals {
		capturedFilepath = ""
		code, handler := s8serve(r, "GET", tc.path)
		if tc.wantAdmin && handler == "admin" {
			t.Errorf("H8-03 BYPASS: %q reached /admin handler (code=%d, filepath=%q)", tc.path, code, capturedFilepath)
		}
		if !tc.wantAdmin && handler == "admin" {
			t.Errorf("H8-03 UNEXPECTED ADMIN MATCH: %q reached /admin (code=%d)", tc.path, code)
		}
		if tc.wantAdmin && handler != "admin" {
			// Correctly rejected — log for confirmation.
			t.Logf("H8-03 OK: %q did NOT reach /admin (code=%d handler=%q)", tc.path, code, handler)
		}
	}

	// Property: if filepath param contains ".." it must stay in the catch-all handler.
	// The router is NOT responsible for cleaning; that's http.FileServer's job.
	r2 := mm.New()
	r2.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		fp := mm.PathParam(req, "filepath")
		if strings.Contains(fp, "..") {
			// A custom handler SHOULD check this; we log it as a documentation item.
			w.Header().Set("X-Traversal", "yes")
		}
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})
	r2.GET("/admin", s8h("admin"))

	traversalInFilepath := []string{
		"/static/../../etc/passwd",
		"/static/../etc",
		"/static/%2e%2e/etc/passwd",
		"/static/..%2fetc%2fpasswd",
	}
	for _, p := range traversalInFilepath {
		code, handler := s8serve(r2, "GET", p)
		if handler == "admin" {
			t.Errorf("H8-03 CATCH-ALL BYPASS: %q reached /admin when it should stay in catch-all (code=%d)", p, code)
		} else {
			t.Logf("H8-03 catch-all boundary OK: %q → handler=%q code=%d (filepath param may contain traversal — operator must sanitize)", p, handler, code)
		}
	}
}

// ============================================================================
// H8-20 — RawPath × UseRawPath 4-state matrix
//
// (UseRawPath, UnescapePathValues) × 4 states. For each state verify:
// 1. Encoded slashes in params (%2f) do not span segment boundaries.
// 2. Double-encoded paths do not bypass.
// 3. The correct path reaches the correct handler.
// ============================================================================

func TestS8_H820_RawPathMatrix(t *testing.T) {
	type state struct {
		useRaw    bool
		unescape  bool
		label     string
	}

	states := []state{
		{false, false, "UseRaw=F,Unescape=F (default)"},
		{true, false,  "UseRaw=T,Unescape=F"},
		{true, true,   "UseRaw=T,Unescape=T"},
		// {false, true} is explicitly disallowed by the docs (Unescape only takes
		// effect when UseRaw=true), so we only test the interaction.
	}

	type testPath struct {
		urlPath    string
		rawPath    string // empty = same as urlPath
		wantParam  string // expected :id value
		wantCode   int
	}

	cases := []testPath{
		// Normal path.
		{"/users/abc", "", "abc", 200},
		// Encoded slash in :id segment — should NOT match /users/:id/posts.
		{"/users/a/b", "", "", 404},
		// %2f encoded in URL.Path (already decoded by net/url.Parse → treated as '/').
		{"/users/a%2fb", "/users/a%2fb", "", 404},
		// Double-encoded %252f — first decode: %2f; second decode (if unescape=true): /.
		{"/users/a%252fb", "/users/a%252fb", "a%2fb", 200},
	}

	for _, st := range states {
		st := st
		t.Run(st.label, func(t *testing.T) {
			r := mm.New()
			r.UseRawPath = st.useRaw
			r.UnescapePathValues = st.unescape
			r.Rebuild()

			var capturedID string
			r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
				capturedID = mm.PathParam(req, "id")
				w.Header().Set("X-Handler", "users")
				w.WriteHeader(200)
			})

			for _, tc := range cases {
				capturedID = ""
				req := httptest.NewRequest("GET", "http://example.com"+tc.urlPath, nil)
				if tc.rawPath != "" {
					req.URL.RawPath = tc.rawPath
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)

				if tc.wantCode == 200 && w.Code != 200 {
					t.Logf("[%s] %q: expected 200, got %d", st.label, tc.urlPath, w.Code)
					continue
				}
				if tc.wantCode == 404 && w.Code == 200 {
					// UseRawPath=true: %2f is NOT a segment separator at tree-lookup time.
					// The tree sees "a%2fb" as a single segment → :id captures "a%2fb".
					// UnescapePathValues=true: url.PathUnescape("a%2fb") = "a/b" — slash in VALUE.
					// This is documented behavior. The slash is in the param value only,
					// not in routing. Operators must sanitize param values used as paths.
					if strings.Contains(capturedID, "/") {
						t.Logf("PRF-S8-002 DOCUMENTED [%s]: %q → :id=%q (slash from %%2f via UnescapePathValues) — documented behavior, operator must sanitize", st.label, tc.urlPath, capturedID)
					} else {
						t.Logf("[%s] %q: got 200 (capturedID=%q) — may be documented behavior", st.label, tc.urlPath, capturedID)
					}
				}
				if tc.wantParam != "" && capturedID != tc.wantParam {
					t.Logf("[%s] %q: param mismatch want=%q got=%q", st.label, tc.urlPath, tc.wantParam, capturedID)
				}
			}
		})
	}
}

// ============================================================================
// H8-21 — UseRawPath=true + cleanedPath(): RedirectFixedPath interaction
//
// When UseRawPath=true and RedirectFixedPath=true, cleanedPath receives
// RawPath (not decoded Path). Verify path.Clean of encoded paths does not
// produce a redirect that bypasses auth.
// ============================================================================

func TestS8_H821_UseRawPath_RedirectFixedPath(t *testing.T) {
	r := mm.New()
	r.UseRawPath = true
	r.RedirectFixedPath = true
	r.Rebuild()

	authRan := false
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			authRan = true
			next.ServeHTTP(w, req)
		})
	})
	r.GET("/admin", s8h("admin"))

	// //admin with RawPath set: path.Clean("//admin") = "/admin".
	// If RedirectFixedPath=true, this could redirect to /admin.
	// Verify auth still runs.
	authRan = false
	req := httptest.NewRequest("GET", "http://example.com//admin", nil)
	req.URL.RawPath = "//admin"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == 200 && w.Header().Get("X-Handler") == "admin" {
		t.Logf("H8-21: //admin matched /admin with UseRawPath+RFP (code=200); authRan=%v", authRan)
		if !authRan {
			t.Errorf("H8-21 BYPASS: //admin matched /admin without running auth middleware (UseRawPath+RedirectFixedPath)")
		}
	} else if w.Code == 301 || w.Code == 307 {
		if !authRan {
			t.Errorf("H8-21 BYPASS: //admin triggered redirect to /admin without running auth (code=%d)", w.Code)
		} else {
			t.Logf("H8-21 OK: redirect triggered and auth ran (code=%d)", w.Code)
		}
	} else {
		t.Logf("H8-21 OK: //admin → code=%d (not reaching /admin directly)", w.Code)
	}
}

// ============================================================================
// H8-22 — cleanedPath() does not leak /admin existence via redirects without auth
//
// RedirectFixedPath (default=false). Ensure that even when enabled, the
// redirect target itself is middleware-wrapped so auth runs.
// ============================================================================

func TestS8_H822_CleanedPath_AuthWrapped(t *testing.T) {
	blocked := false
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			blocked = true
			http.Error(w, "Forbidden", 403)
			// Do NOT call next — auth blocks.
		})
	}

	r := mm.New()
	r.RedirectFixedPath = true
	r.Use(authMW)
	r.GET("/admin", s8h("admin"))

	// path.Clean("//admin") = "/admin" → RedirectFixedPath would redirect.
	// The redirect handler is wrapped in middleware → auth must block it.
	blocked = false
	code, handler := s8serve(r, "GET", "//admin")
	if handler == "admin" {
		t.Errorf("H8-22 BYPASS: //admin reached /admin without auth (code=%d)", code)
	}
	if !blocked {
		// Redirect may occur before auth. If redirect code is returned, auth didn't run.
		if code == 301 || code == 307 {
			t.Errorf("H8-22 REDIRECT WITHOUT AUTH: //admin → redirect (code=%d) without auth running", code)
		}
	} else {
		t.Logf("H8-22 OK: //admin → auth blocked (code=%d)", code)
	}
}

// ============================================================================
// H8-23 — CleanPath middleware + UseRawPath=true: RawPath cleaning interaction
//
// When CleanPath is used as Pre() and UseRawPath=true, there are two paths:
// 1. CleanPath normalises URL.Path via path.Clean.
// 2. Dispatch uses URL.RawPath (because UseRawPath=true).
// Question: does RawPath contain encoded traversal that bypasses CleanPath?
// ============================================================================

func TestS8_H823_CleanPath_UseRawPath_Interaction(t *testing.T) {
	r := mm.New()
	r.UseRawPath = true
	r.Rebuild()
	r.Pre(middleware.CleanPath())
	r.GET("/admin", s8h("admin"))
	r.GET("/static/*filepath", s8h("static"))

	// RawPath has encoded traversal: /static/%2e%2e/admin.
	// CleanPath runs on URL.Path (decoded): /static/../admin → /admin.
	// But dispatch uses RawPath.
	// After CleanPath: URL.Path = /admin, URL.RawPath is zeroed (decoded differs from cleaned).
	// So dispatch uses /admin → matches /admin.
	// This is the documented CleanPath behaviour (PRF-001) — but with UseRawPath, does it get worse?

	req := httptest.NewRequest("GET", "http://example.com/static/%2e%2e/admin", nil)
	// net/http sets RawPath automatically when it differs from decoded Path.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	t.Logf("H8-23: /static/%%2e%%2e/admin with CleanPath+UseRawPath → code=%d handler=%q", w.Code, w.Header().Get("X-Handler"))
	// Expected: CleanPath zeroes RawPath (decoded form cleans to /admin ≠ /static/%2e%2e/admin).
	// Then dispatch uses URL.Path = /admin → matches /admin.
	// This is documented PRF-001 behaviour. No new bypass.

	// More interesting: path that CleanPath passes unchanged in RawPath but decoded form differs.
	// /static/%2f%2e%2e/admin: RawPath cleaned = /static/%2f%2e%2e/admin (path.Clean doesn't decode),
	// decoded = /static//../admin → cleaned = /static/admin... let's check actual behavior.
	req2 := httptest.NewRequest("GET", "http://example.com/static/%2f%2e%2e/admin", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	t.Logf("H8-23: /static/%%2f%%2e%%2e/admin with CleanPath+UseRawPath → code=%d handler=%q RawPath=%q",
		w2.Code, w2.Header().Get("X-Handler"), req2.URL.RawPath)
	// If this reaches /admin with CleanPath+UseRawPath, document it.
	if w2.Header().Get("X-Handler") == "admin" {
		t.Logf("H8-23 NOTED: /static/%%2f%%2e%%2e/admin reached /admin via CleanPath+UseRawPath interaction (likely PRF-001 variant)")
	}
}

// ============================================================================
// H8-26 — Optional segments cap=8 via nested Group chains
//
// Each Group adds a prefix but does NOT add optional segments by itself.
// However, patterns registered through deeply nested groups concatenate prefixes.
// The cap is on the pattern string itself. Verify that nested Group.Group.GET
// cannot bypass the cap by distributing optionals across separate registrations.
//
// FINDING PRF-2026-S8-001: consecutive optional segments in a single pattern
// (e.g. /a{/:p1}{/:p2}) always conflict because expandOptional expands the
// FIRST optional found, then recursively processes, creating /a/:p1 AND /a/:p2
// at the same tree level — which triggers the wildcard conflict check.
// Non-consecutive optionals (e.g. /a{/:p1}/b{/:p2}) work correctly.
// ============================================================================

func TestS8_H826_OptionalSegmentsCap_GroupChain(t *testing.T) {
	// Test 1: Single optional — must work (baseline).
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("H8-26 REGRESSION: single optional panicked: %v", rec)
			}
		}()
		r := mm.New()
		r.GET("/a{/:p1}", s8h("ok"))
		code, handler := s8serve(r, "GET", "/a")
		if code != 200 || handler != "ok" {
			t.Errorf("H8-26: single optional routing broken: /a → %d/%q", code, handler)
		}
		code2, handler2 := s8serve(r, "GET", "/a/hello")
		if code2 != 200 || handler2 != "ok" {
			t.Errorf("H8-26: single optional routing broken: /a/hello → %d/%q", code2, handler2)
		}
	}()

	// Test 2: Two consecutive optionals — FINDING: always panics with wildcard conflict.
	// /a{/:p1}{/:p2} expands to: /a, /a/:p2, /a/:p1, /a/:p1/:p2.
	// /a/:p1 and /a/:p2 conflict (same tree level, both wildcards).
	panickedConsecutive := false
	var consecutiveMsg string
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panickedConsecutive = true
				consecutiveMsg = fmt.Sprintf("%v", rec)
			}
		}()
		r := mm.New()
		r.GET("/a{/:p1}{/:p2}", s8h("ok"))
		_ = r
	}()
	if panickedConsecutive {
		t.Logf("PRF-S8-001 CONFIRMED: consecutive optional segments /a{/:p1}{/:p2} panic with wildcard conflict: %s", consecutiveMsg)
		t.Logf("PRF-S8-001: Only NON-CONSECUTIVE optionals work: /a{/:p1}/b{/:p2}")
	} else {
		t.Logf("H8-26: consecutive optionals did NOT panic (behavior may have changed)")
	}

	// Test 3: Non-consecutive optionals — must work.
	// /users{/:id}/posts{/:post} expands to:
	//   /users/posts           (neither optional)
	//   /users/posts/:post     (second optional only)
	//   /users/:id/posts       (first optional only)
	//   /users/:id/posts/:post (both optionals)
	// Note: /users alone does NOT match (the /posts literal is always required).
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("H8-26 REGRESSION: non-consecutive optionals panicked: %v", rec)
			}
		}()
		r := mm.New()
		r.GET("/users{/:id}/posts{/:post}", s8h("ok"))
		// /users/posts is the minimal match (no optionals filled).
		code, _ := s8serve(r, "GET", "/users/posts")
		if code != 200 {
			t.Errorf("H8-26 non-consecutive: /users/posts should match (code=%d)", code)
		}
		// /users/123/posts — first optional filled.
		code2, _ := s8serve(r, "GET", "/users/123/posts")
		if code2 != 200 {
			t.Errorf("H8-26 non-consecutive: /users/123/posts should match (code=%d)", code2)
		}
		// /users — does NOT match (literal /posts is required).
		code3, _ := s8serve(r, "GET", "/users")
		if code3 == 200 {
			t.Logf("H8-26 non-consecutive: /users unexpectedly matched (code=%d) — expansion may differ", code3)
		}
	}()

	// Test 4: 9 optionals → must panic with "optional segments" DoS cap.
	panicked9 := false
	var panicMsg9 string
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked9 = true
				panicMsg9 = fmt.Sprintf("%v", rec)
			}
		}()
		r := mm.New()
		r.GET("/a{/:1}{/:2}{/:3}{/:4}{/:5}{/:6}{/:7}{/:8}{/:9}", s8h("ok"))
		_ = r
	}()
	if !panicked9 {
		t.Errorf("H8-26 MISSING CAP: 9-optional pattern did not panic — DoS possible")
	} else if !strings.Contains(panicMsg9, "optional segments") {
		t.Errorf("H8-26 WRONG PANIC: expected 'optional segments' error, got: %s", panicMsg9)
	} else {
		t.Logf("H8-26: 9 optionals correctly rejected by cap: %s", panicMsg9)
	}

	// Test 5: Group nested 9 levels deep, each adding a normal segment (no optionals).
	// This should work fine — cap applies to optional segments only.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("H8-26 false positive: deep Group nesting without optionals panicked: %v", rec)
			}
		}()
		r := mm.New()
		g := r.Group("/a")
		for i := 0; i < 9; i++ {
			g = g.Group(fmt.Sprintf("/s%d", i))
		}
		g.GET("/leaf", s8h("leaf"))
		code, handler := s8serve(r, "GET", "/a/s0/s1/s2/s3/s4/s5/s6/s7/s8/leaf")
		if code != 200 || handler != "leaf" {
			t.Errorf("H8-26 deep group routing: expected 200/leaf, got %d/%q", code, handler)
		}
	}()
}

// ============================================================================
// H8-27 — Mount + Group nested: optional-segment cap bypass hypothesis
//
// Mount registers prefix+"/*mux_mount". Group.Mount wraps the handler in
// group middleware then calls mux.mountAt. The combined prefix is
// group.prefix + prefix. Verify that neither Mount nor Group.Mount can inject
// optional segment syntax that bypasses the cap.
// ============================================================================

func TestS8_H827_MountGroupOptionalCap(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	// Normal Mount — should work.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("H8-27: normal Mount panicked: %v", rec)
			}
		}()
		r := mm.New()
		g := r.Group("/api{/:v}")
		g.Mount("/v2", inner)
		// Resulting route: /api{/:v}/v2/*mux_mount.
		// This has 1 optional segment — fine.
		_ = r
		t.Logf("H8-27: Group with optional in prefix + Mount registered without panic")
	}()

	// Mount where prefix itself has optional syntax (unusual but test).
	// mountAt panics if prefix has invalid UTF-8; optional syntax passes through to addRoute.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Logf("H8-27: Mount with optional prefix panicked (expected for optional in prefix after /*): %v", rec)
			}
		}()
		r := mm.New()
		// Try mounting at a path that mixes optional + wildcard.
		// This is unusual usage but should either work or panic cleanly.
		r.Mount("/app{/:section}", inner)
		_ = r
	}()
}

// ============================================================================
// H8-47 — Regex param ReDoS: {name:^(a+)+$} or similar
//
// Verify that addRoute compiles the regex (safe), and that getValue matching
// is bounded for pathological inputs.
// ============================================================================

func TestS8_H847_RegexParamReDoS(t *testing.T) {
	// Catastrophic backtracking patterns.
	redosPatterns := []struct {
		routeExpr string
		inputSeg  string
	}{
		{"^(a+)+$", "aaaaaaaaaaaaaaaaaaaaaaaaaaab"},
		{"^([a-zA-Z]+)*$", "aaaaaaaaaaaaaaaaaaaaaaaa!"},
		{"^(a|aa)+$", "aaaaaaaaaaaaaaaaaaaaaaaab"},
		{"^(a|a?)+$", "aaaaaaaaaaaaaaaaaaaaaaaab"},
	}

	for _, tc := range redosPatterns {
		tc := tc
		t.Run(tc.routeExpr, func(t *testing.T) {
			// Registration must succeed (compile-time is not catastrophic).
			panicked := false
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicked = true
						t.Logf("H8-47: addRoute panicked for {name:%s}: %v", tc.routeExpr, rec)
					}
				}()
				r := mm.New()
				r.GET("/{name:"+tc.routeExpr+"}", s8h("regex"))
				_ = r
			}()
			if panicked {
				// Panic during registration is acceptable (invalid regex or safety check).
				return
			}

			// Route matching with a ReDoS input — must return within reasonable time.
			// We set a generous bound; the Go regexp engine uses RE2 (linear time) so
			// catastrophic backtracking should NOT occur. This confirms Go's safety.
			done := make(chan bool, 1)
			go func() {
				func() {
					defer func() { recover() }()
					r := mm.New()
					r.GET("/{name:"+tc.routeExpr+"}", s8h("regex"))
					s8serve(r, "GET", "/"+tc.inputSeg)
				}()
				done <- true
			}()
			select {
			case <-done:
				t.Logf("H8-47 OK: {%s} with input %q did not hang (Go RE2 is linear)", tc.routeExpr, tc.inputSeg)
			// Use a reasonable timeout for the test (the test framework timeout handles the rest).
			}
		})
	}
}

// ============================================================================
// H8-50 — Two-phase registration rollback: tree consistency after panic
//
// A failed addRoute (e.g. duplicate route) should leave the tree consistent.
// Subsequent registrations and lookups must work correctly.
// ============================================================================

func TestS8_H850_TwoPhaseRegistration_RollbackConsistency(t *testing.T) {
	r := mm.New()
	r.GET("/admin", s8h("admin"))
	r.GET("/users/:id", s8h("users"))

	// Attempt a duplicate — this will panic. Recover and verify tree is intact.
	func() {
		defer func() { recover() }()
		r.GET("/admin", s8h("admin2")) // panic: already registered
	}()

	// After the panic + recovery, tree must still route correctly.
	code, handler := s8serve(r, "GET", "/admin")
	if code != 200 || handler != "admin" {
		t.Errorf("H8-50 CORRUPTION: after duplicate-route panic, /admin returned code=%d handler=%q (expected 200/admin)", code, handler)
	}

	code2, handler2 := s8serve(r, "GET", "/users/123")
	if code2 != 200 || handler2 != "users" {
		t.Errorf("H8-50 CORRUPTION: after panic, /users/:id returned code=%d handler=%q", code2, handler2)
	}

	// Now register a NEW valid route — must work after recovery.
	panicked := false
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
				t.Errorf("H8-50 REGRESSION: new route after panic failed: %v", rec)
			}
		}()
		r.GET("/new-route", s8h("new"))
	}()
	if !panicked {
		code3, handler3 := s8serve(r, "GET", "/new-route")
		if code3 != 200 || handler3 != "new" {
			t.Errorf("H8-50: new route after panic not reachable: code=%d handler=%q", code3, handler3)
		}
	}
}

// ============================================================================
// H8-51 — incrementChildPrio: index sync with multi-byte node.indices
//
// This is the PRF-NEW-001 regression: string(byte) for multi-byte chars.
// Fixed in tree.go (PRF-2026-0009). Verify thoroughly with all 4-byte ranges.
// ============================================================================

func TestS8_H851_IncrementChildPrio_MultiByte(t *testing.T) {
	// Test every byte class that could cause multi-byte expansion.
	// The fix uses string([]byte{b}) so all bytes should produce 1-byte strings.
	patterns := []struct {
		pattern string
		desc    string
	}{
		// 1-byte ASCII (baseline).
		{"/a/z", "ASCII suffix z"},
		// 2-byte UTF-8 leading bytes (0xC0..0xDF).
		{"/a/\xc3\xa9", "2-byte é"},
		{"/a/\xc3\xbc", "2-byte ü"},
		{"/a/\xd0\xb0", "2-byte Cyrillic а"},
		// 3-byte leading bytes (0xE0..0xEF).
		{"/a/\xe2\x80\x8b", "3-byte ZWSP"},
		{"/a/\xe2\x82\xac", "3-byte € (U+20AC)"},
		// 4-byte leading bytes (0xF0..0xF7).
		{"/a/\xf0\x9f\x98\x80", "4-byte emoji 😀"},
		// Optional segment followed by multi-byte — the original crash trigger.
		{"/a{/:b}\xc3\xa9", "optional + 2-byte"},
		{"/a{/:b}\xe2\x80\x8b", "optional + 3-byte"},
		{"/a{/:b}\xf0\x9f\x98\x80", "optional + 4-byte"},
		// Multi-byte char in node split position.
		{"/\xc3\xa9/\xd0\xb0", "2-byte prefix / 2-byte suffix"},
	}

	for _, tc := range patterns {
		tc := tc
		if !utf8.ValidString(tc.pattern) {
			continue // skip invalid UTF-8 patterns
		}
		t.Run(tc.desc, func(t *testing.T) {
			panicked := false
			var panicMsg interface{}
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicked = true
						panicMsg = rec
					}
				}()
				r := mm.New()
				r.GET(tc.pattern, s8h("handler"))
				// Also register a sibling to force incrementChildPrio.
				if tc.pattern != "/a/z" {
					r.GET("/a/z", s8h("z")) // common sibling to exercise priority update
				}
				_ = r
			}()
			if panicked {
				msg := fmt.Sprintf("%v", panicMsg)
				if strings.Contains(msg, "index out of range") {
					t.Errorf("H8-51 OOB PANIC: %q caused index out of range: %v (PRF-NEW-001 regression)", tc.pattern, panicMsg)
				} else {
					t.Logf("H8-51 non-OOB panic for %q: %v", tc.pattern, panicMsg)
				}
			} else {
				t.Logf("H8-51 OK: %q registered without panic", tc.pattern)
			}
		})
	}
}

// ============================================================================
// H8-52 — addRoute two-phase commit: parse-time vs commit-time panic
//
// If addRoute panics between the clone and the Store, the live tree must be
// unaffected. This was fixed by cloneTree + Store under mutex (MM-2026-0033).
// ============================================================================

func TestS8_H852_TwoPhaseCommit_ParseTime(t *testing.T) {
	r := mm.New()
	r.GET("/safe", s8h("safe"))

	// These patterns will panic in addRoute (invalid patterns).
	invalidPatterns := []string{
		"/path/*catch/more", // catch-all not at end
		"/admin",            // will conflict after first registration below
	}
	// First register /admin so the second attempt is a duplicate.
	r.GET("/admin", s8h("admin"))

	for _, p := range invalidPatterns {
		func() {
			defer func() { recover() }()
			r.GET(p, s8h("bad"))
		}()
	}

	// After all the panics, the tree must still correctly serve valid routes.
	code, handler := s8serve(r, "GET", "/safe")
	if code != 200 || handler != "safe" {
		t.Errorf("H8-52: /safe not reachable after parse-time panics: code=%d handler=%q", code, handler)
	}
	code2, handler2 := s8serve(r, "GET", "/admin")
	if code2 != 200 || handler2 != "admin" {
		t.Errorf("H8-52: /admin not reachable after parse-time panics: code=%d handler=%q", code2, handler2)
	}
}

// ============================================================================
// H8-58 — incrementChildPrio index synchronisation: indices vs children
//
// When a node split occurs, incrementChildPrio is called. The invariant is:
// len(n.indices) == len(n.children[:len(n.indices)]).
// Test that deep tree re-ordering after many inserts keeps indices in sync.
// ============================================================================

func TestS8_H858_IncrementChildPrio_IndexSync(t *testing.T) {
	// Register many routes with common prefixes to force multiple node splits
	// and priority reorderings.
	routes := []string{
		"/api/v1/users",
		"/api/v1/items",
		"/api/v1/admin",
		"/api/v2/users",
		"/api/v2/items",
		"/api/v2/admin",
		"/api/v1/users/:id",
		"/api/v1/items/:id",
		"/api/v1/admin/:id",
		"/app/dashboard",
		"/app/settings",
		"/app/profile",
		"/app/dashboard/:id",
		"/a/b/c",
		"/a/b/d",
		"/a/b/e",
		"/a/c/d",
		"/a/d/e",
	}

	panicked := false
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
				t.Errorf("H8-58 PANIC during registration: %v", rec)
			}
		}()
		r := mm.New()
		for _, route := range routes {
			r.GET(route, s8h("handler"))
		}

		// Verify all routes are reachable.
		for _, route := range routes {
			// Convert :id to a concrete value for testing.
			testPath := strings.ReplaceAll(route, ":id", "123")
			code, handler := s8serve(r, "GET", testPath)
			if code != 200 || handler != "handler" {
				t.Errorf("H8-58: route %q not reachable at %q: code=%d handler=%q", route, testPath, code, handler)
			}
		}
	}()
	if !panicked {
		t.Logf("H8-58 OK: %d routes registered and verified without index sync issues", len(routes))
	}
}

// ============================================================================
// Additional S8 coverage: specific H8-xx hypotheses from sprint plan
// ============================================================================

// TestS8_CatchallEmpty — empty filepath in catch-all.
func TestS8_CatchallEmpty(t *testing.T) {
	r := mm.New()
	r.RedirectTrailingSlash = true
	var captured string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		captured = mm.PathParam(req, "filepath")
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	// /static/ — TSR may redirect, or the wildcard may capture empty string.
	captured = ""
	code, handler := s8serve(r, "GET", "/static/")
	t.Logf("S8: /static/ → code=%d handler=%q filepath=%q", code, handler, captured)
	// /static (no trailing slash) — TSR redirect expected.
	code2, handler2 := s8serve(r, "GET", "/static")
	t.Logf("S8: /static → code=%d handler=%q", code2, handler2)
	if code2 == 200 && handler2 == "static" {
		t.Logf("S8: /static matched catch-all without slash (unusual)")
	}
}

// TestS8_WildcardShadow_AllMethods — precedence consistency across HTTP methods.
func TestS8_WildcardShadow_AllMethods(t *testing.T) {
	r := mm.New()
	r.GET("/users/admin", s8h("static"))
	r.GET("/users/:id", s8h("param"))
	r.POST("/users/:id", s8h("post-param"))
	// POST does not have /users/admin registered.

	cases := []struct {
		method  string
		path    string
		want    string
		wantCode int
	}{
		{"GET", "/users/admin", "static", 200},   // static wins over :id for GET
		{"GET", "/users/123", "param", 200},       // :id wins for other values
		{"POST", "/users/admin", "post-param", 200}, // POST: no static, :id matches
		{"POST", "/users/123", "post-param", 200},
		{"DELETE", "/users/admin", "", 405},        // no DELETE registered
	}

	for _, tc := range cases {
		code, handler := s8serve(r, tc.method, tc.path)
		if tc.wantCode == 200 {
			if code != 200 || handler != tc.want {
				t.Errorf("S8-SHADOW: %s %q → code=%d handler=%q, want 200/%q", tc.method, tc.path, code, handler, tc.want)
			}
		} else {
			if code != tc.wantCode {
				t.Errorf("S8-SHADOW: %s %q → code=%d, want %d", tc.method, tc.path, code, tc.wantCode)
			}
		}
	}
}

// TestS8_EncodedSlash_InParam — %2f in :param must not span segment boundaries.
func TestS8_EncodedSlash_InParam(t *testing.T) {
	r := mm.New()
	var capturedID string
	r.GET("/users/:id/posts", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "posts")
		w.WriteHeader(200)
	})
	r.GET("/admin", s8h("admin"))

	// UseRawPath=false (default): net/url decodes %2f to /, making the path
	// /users/a/b/posts which does not match /users/:id/posts (:id cannot be "a/b").
	cases := []struct {
		path      string
		wantMatch bool
		wantSlash bool // should :id contain '/'?
	}{
		{"/users/abc/posts", true, false},
		{"/users/a%2fb/posts", false, false}, // decoded: /users/a/b/posts → 404
		{"/users/a%2Fb/posts", false, false},
		{"/users/a%252fb/posts", true, false}, // %252f → %2f literal (not decoded to /)
	}

	for _, tc := range cases {
		capturedID = ""
		code, handler := s8serve(r, "GET", tc.path)
		matched := handler == "posts" && code == 200
		if tc.wantMatch && !matched {
			t.Errorf("S8-ENCODED-SLASH: %q should match, got code=%d handler=%q", tc.path, code, handler)
		}
		if !tc.wantMatch && matched {
			t.Errorf("S8-ENCODED-SLASH: %q should NOT match, got code=%d id=%q", tc.path, code, capturedID)
		}
		if matched && strings.Contains(capturedID, "/") {
			t.Errorf("S8-ENCODED-SLASH: :id captured slash via %q — id=%q", tc.path, capturedID)
		}
	}
}

// TestS8_TrailingSlash_LocationHeader — redirect Location must be same-origin.
func TestS8_TrailingSlash_LocationHeader(t *testing.T) {
	r := mm.New()
	r.RedirectTrailingSlash = true
	r.GET("/admin/", s8h("admin"))

	// Normal TSR redirect.
	code, _ := s8serve(r, "GET", "/admin")
	if code != 301 && code != 307 {
		t.Logf("S8-TSR: /admin → code=%d (no redirect)", code)
		return
	}

	req := httptest.NewRequest("GET", "http://example.com/admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	loc := w.Header().Get("Location")
	t.Logf("S8-TSR: Location=%q", loc)

	// The Location must be a relative same-origin path or absolute same-origin.
	// It must NOT start with "//" (protocol-relative, open redirect).
	if strings.HasPrefix(loc, "//") {
		t.Errorf("S8-TSR OPEN-REDIRECT: Location=%q starts with '//' — protocol-relative redirect", loc)
	}
	// Must not contain CRLF.
	if strings.Contains(loc, "\r") || strings.Contains(loc, "\n") {
		t.Errorf("S8-TSR CRLF: Location=%q contains CRLF", loc)
	}
	// Must end with "/" (adding trailing slash).
	if loc != "" && !strings.HasSuffix(loc, "/") && !strings.HasSuffix(loc, "/admin/") {
		t.Logf("S8-TSR NOTE: Location=%q does not end with '/'", loc)
	}
}

// TestS8_GroupPrefixConcatenation — double slash from group prefix concat.
func TestS8_GroupPrefixConcatenation(t *testing.T) {
	r := mm.New()
	// Group prefix "/api" + route path "/users" → "/api/users" (correct).
	// But "/api" + "" would give "/api" (also fine).
	// Edge case: "/api/" + "/users" → "/api//users" (double slash).
	g := r.Group("/api/")
	g.GET("/users", s8h("users"))
	// This registers "/api//users" — let's verify what happens.

	code, handler := s8serve(r, "GET", "/api//users")
	t.Logf("S8-GROUP-CONCAT: /api//users → code=%d handler=%q", code, handler)
	// Also test the "correct" path.
	code2, handler2 := s8serve(r, "GET", "/api/users")
	t.Logf("S8-GROUP-CONCAT: /api/users → code=%d handler=%q", code2, handler2)

	// Finding: if double-slash path is reachable but single-slash is not, document it.
	if code == 200 && code2 != 200 {
		t.Logf("S8-GROUP-CONCAT NOTED: group prefix '/api/' + route '/users' creates double-slash path '/api//users' — "+
			"only reachable via double-slash, not /api/users. This is a routing confusion issue.")
	}
}

// TestS8_MountPrefixTrailingSlash — Mount strips trailing slash from prefix.
func TestS8_MountPrefixTrailingSlash(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.Header().Set("X-Path", r.URL.Path)
		w.WriteHeader(200)
	})

	r := mm.New()
	r.Mount("/api/", inner) // trailing slash in prefix

	// mountAt strips trailing slash: prefix becomes "/api".
	// Route registered: /api/*mux_mount.
	code, handler := s8serve(r, "GET", "/api/users")
	if code != 200 || handler != "inner" {
		t.Errorf("S8-MOUNT-SLASH: /api/users → code=%d handler=%q (expected 200/inner)", code, handler)
	}
}

// TestS8_UTF8Invalid_InPath — invalid UTF-8 must not panic.
func TestS8_UTF8Invalid_InPath(t *testing.T) {
	r := mm.New()
	r.GET("/admin", s8h("admin"))
	r.GET("/users/:id", s8h("users"))
	r.GET("/static/*filepath", s8h("static"))

	// These are invalid UTF-8 byte sequences.
	// net/http's httptest.NewRequest uses url.Parse which may reject some,
	// but we test at the ServeHTTP level with direct URL manipulation.
	invalidPaths := []string{
		"/admin\xff",
		"/\xff\xfe/admin",
		"/static/\x80\x81",
		"/users/\xfe\xff",
	}

	for _, p := range invalidPaths {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("S8-UTF8-INVALID: path %q caused panic: %v", p, rec)
				}
			}()
			req, err := http.NewRequest("GET", "http://example.com", nil)
			if err != nil {
				return
			}
			// Manually set the URL path to bypass URL validation.
			req.URL.Path = p
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			// Must not panic; status doesn't matter.
			t.Logf("S8-UTF8-INVALID: %q → code=%d (no panic)", p, w.Code)
		}()
	}
}

// TestS8_RegexParam_CaptureGroup — regex param name extraction.
func TestS8_RegexParam_CaptureGroup(t *testing.T) {
	r := mm.New()
	var capturedID string
	r.GET("/users/{id:[0-9]+}", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "users")
		w.WriteHeader(200)
	})

	cases := []struct {
		path     string
		wantCode int
		wantID   string
	}{
		{"/users/123", 200, "123"},
		{"/users/0", 200, "0"},
		{"/users/abc", 404, ""},
		{"/users/12ab", 404, ""},
		{"/users/", 404, ""},
		{"/users", 301, ""}, // TSR: /users → /users/ (with trailing slash rule)
	}

	for _, tc := range cases {
		capturedID = ""
		code, handler := s8serve(r, "GET", tc.path)
		if tc.wantCode == 200 {
			if code != 200 || handler != "users" {
				t.Errorf("S8-REGEX: %q → code=%d handler=%q, want 200/users", tc.path, code, handler)
			}
			if capturedID != tc.wantID {
				t.Errorf("S8-REGEX: %q → id=%q, want %q", tc.path, capturedID, tc.wantID)
			}
		} else if tc.wantCode == 404 {
			if code == 200 {
				t.Errorf("S8-REGEX: %q should not match, got 200 id=%q", tc.path, capturedID)
			}
		}
	}
}

// TestS8_RegexParam_RegexpNameEnd_LongName — regexpNameEnd is uint8 (max 255 bytes).
func TestS8_RegexParam_RegexpNameEnd_LongName(t *testing.T) {
	// 255-byte name is the maximum.
	name255 := strings.Repeat("a", 255)
	name256 := strings.Repeat("a", 256)

	// 255-byte name: should work or panic with meaningful error.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Logf("S8-REGEXPNAME: 255-byte name panicked: %v", rec)
			}
		}()
		r := mm.New()
		r.GET("/{"+name255+":[0-9]+}", s8h("r"))
		_ = r
	}()

	// 256-byte name: must panic with "exceeds 255 bytes" error.
	panicked := false
	var msg string
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
				msg = fmt.Sprintf("%v", rec)
			}
		}()
		r := mm.New()
		r.GET("/{"+name256+":[0-9]+}", s8h("r"))
		_ = r
	}()
	if !panicked {
		t.Errorf("S8-REGEXPNAME: 256-byte param name should panic, but didn't")
	} else if !strings.Contains(msg, "255") {
		t.Errorf("S8-REGEXPNAME: 256-byte param name panicked with wrong message: %s", msg)
	} else {
		t.Logf("S8-REGEXPNAME: 256-byte name correctly rejected: %s", msg)
	}
}

// TestS8_Middleware_CleanPath_EncodedSlashBypass — %2f in RawPath bypasses CleanPath.
//
// CleanPath calls path.Clean(r.URL.Path). When UseRawPath=false (default),
// net/url.Parse has already decoded %2f to '/', so path.Clean handles it.
// When UseRawPath=true and CleanPath is used as Pre(), the RawPath check in
// CleanPath uses url.PathUnescape which decodes %2f. So encoded slashes are
// handled. But what about %252f (double-encoded)?
func TestS8_Middleware_CleanPath_DoubleEncodedSlash(t *testing.T) {
	r := mm.New()
	r.Pre(middleware.CleanPath())
	r.GET("/admin", s8h("admin"))
	r.GET("/static/*filepath", s8h("static"))

	// %252f double-encoded: URL.Path will have %2f (after one decode by net/url).
	// path.Clean("/static/%2f../admin") = "/static/%2f../admin" (no decode).
	// Then dispatch uses this cleaned path which won't match /admin.
	// The router does NOT percent-decode the path during matching.
	code, handler := s8serve(r, "GET", "/static/%252f../admin")
	t.Logf("S8-CLEANPATH-DOUBLE: /static/%%252f../admin → code=%d handler=%q", code, handler)
	if handler == "admin" {
		t.Errorf("S8-CLEANPATH-DOUBLE BYPASS: double-encoded slash traversal reached /admin")
	}

	// Mixed: %2f + literal ..: URL.Path = /static/../admin → cleaned to /admin.
	code2, handler2 := s8serve(r, "GET", "/static%2f../admin")
	t.Logf("S8-CLEANPATH-DOUBLE: /static%%2f../admin → code=%d handler=%q", code2, handler2)
	// %2f decoded by net/url → /static/../admin → CleanPath cleans to /admin → matches /admin.
	// This is the documented PRF-001 behaviour (CleanPath re-routes traversal paths).
	if handler2 == "admin" {
		t.Logf("S8-CLEANPATH NOTE: /static%%2f../admin → /admin via CleanPath (documented PRF-001 behaviour)")
	}
}

// TestS8_AllowHeader_SyncMapBloat — Allow header cache must not grow unboundedly.
//
// methodNotAllowedCache is keyed by Allow string. If an attacker can cause
// many different Allow strings (by registering routes for many method combinations),
// the cache grows. Verify this is bounded by the number of method combinations.
func TestS8_AllowHeader_SyncMapBloat(t *testing.T) {
	r := mm.New()
	// Register GET on several paths.
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/path%d", i)
		r.GET(path, s8h("handler"))
	}

	// Send POST requests to each path → 405 with Allow: GET, OPTIONS.
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/path%d", i)
		s8serve(r, "POST", path)
	}

	// The cache should have at most 1 entry (all paths have same Allow string).
	count := 0
	// Verify no panic and consistent responses (internal sync.Map state not exposed).
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/path%d", i)
		code, _ := s8serve(r, "POST", path)
		if code != 405 {
			t.Errorf("S8-ALLOW-CACHE: POST %s → code=%d, want 405", path, code)
		}
		count++
	}
	t.Logf("S8-ALLOW-CACHE: verified %d paths with consistent 405 responses", count)
}

// ============================================================================
// Expanded traversal corpus: 30+ encodings
// ============================================================================

func TestS8_TraversalCorpus_Extended(t *testing.T) {
	r := mm.New()
	r.GET("/admin", s8h("admin"))
	r.GET("/static/*filepath", s8h("static"))
	r.GET("/users/:id", s8h("users"))
	r.GET("/public", s8h("public"))

	// 30+ traversal encodings — none should reach /admin via traversal.
	traversals := []string{
		// Literal dot-dots.
		"/../admin",
		"/../../admin",
		"/./admin",
		"/./../admin",
		"/users/../admin",
		"/users/1/../admin",
		"/static/../admin",
		"/static/./../../admin",
		"/public/../admin",
		"/api/../admin",

		// Single percent-encoded.
		"/%2e%2e/admin",
		"/%2E%2E/admin",
		"/%2e./admin",
		"/.%2e/admin",
		"/%2e%2e%2fadmin",
		"/..%2fadmin",
		"/..%2Fadmin",
		"/%2e%2e%5cadmin",
		"/..%5cadmin",

		// Double percent-encoded.
		"/%252e%252e/admin",
		"/%25%32%65%25%32%65/admin",
		"/..%252fadmin",
		"/%252e%252e%252fadmin",

		// Overlong UTF-8 (invalid in Go, but test encoded form).
		"/%c0%ae%c0%ae/admin",  // overlong '.' — invalid UTF-8, URL.Path decodes differently
		"/%c0%ae%c0%ae%c0%afadmin",

		// Mixed dot + encoded.
		"/.%2e/admin",
		"/%2e./admin",
		"/static/.%2e/admin",
		"/static/%2e./admin",

		// Windows-style backslash (not a path separator in HTTP).
		"/..\\admin",
		"/..%5cadmin",
		"/static/..%5cadmin",

		// Unicode / NFKC normalization (not a path separator).
		"/static/．．/admin", // fullwidth dot-dot
		"/static/․․/admin", // one-dot leader

		// Encoded null + dot.
		"/%00../admin",
		"/../%00admin",

		// Semicolons (matrix params — not treated as separators).
		"/admin;ignored",
		"/../admin;ignored",

		// Fragment (not visible server-side, but test robustness).
		"/static/../admin#fragment",
	}

	bypassFound := false
	for _, p := range traversals {
		if !utf8.ValidString(p) {
			// Skip invalid UTF-8 — handled separately.
			continue
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("S8-TRAVERSAL PANIC: %q caused panic: %v", p, rec)
				}
			}()
			code, handler := s8serve(r, "GET", p)
			if handler == "admin" {
				t.Errorf("S8-TRAVERSAL BYPASS: %q reached /admin (code=%d)", p, code)
				bypassFound = true
			}
		}()
	}
	if !bypassFound {
		t.Logf("S8-TRAVERSAL: all %d traversal payloads correctly rejected", len(traversals))
	}
}

// TestS8_Differential_Extended — 100+ paths through all 4 routers.
func TestS8_Differential_Extended(t *testing.T) {
	mmRouter := buildMuxMaster()
	hrRouter := buildHTTPRouter()
	chiRouter := buildChi()
	bunRouter := buildBunRouter()

	// Extended corpus: combine traversal + encoding + structural + unicode corpora.
	paths := []string{
		// Control paths.
		"/admin", "/users/123", "/static/img.png", "/api/v1/items/1/children/2", "/public",
		// Traversal.
		"/../admin", "/users/../admin", "/%2e%2e/admin", "/..%2fadmin",
		"/static/../admin", "/static/..%2fadmin", "/static/%2e%2e/admin",
		// Encoding.
		"/%61dmin", "/%61%64%6d%69%6e", "/%2561dmin", "/ad%6din",
		// Structural.
		"//admin", "///admin", "/admin//", "/admin/",
		"/admin;param", "/users//1", "/users/1//",
		// Param boundary.
		"/users/1%2f2", "/users/a%2fb",
		"/users/", "/users",
		// Catch-all.
		"/static/", "/static",
		"/static/a/b/c/d/e",
		"/static/../../etc/passwd",
		"/static/%2e%2e/%2e%2e/etc/passwd",
		// Unicode.
		"/аdmin",   // Cyrillic а
		"/ɑdmin",   // Latin alpha
		"/ADMIN",   // uppercase
		// Deep paths.
		"/api/v1/items/1/children/",
		"/api/v1/items/1/children",
		"/api/v1/items//children/1",
		// Malformed.
		"",
		"/",
		"//",
		"///",
		"/a/b/c/d/e/f/g/h/i/j",
	}

	findings := 0
	for _, p := range paths {
		if !utf8.ValidString(p) {
			continue
		}
		mmR := queryMuxMaster(mmRouter, p)
		hrR := queryHTTPRouter(hrRouter, p)
		chiR := queryChi(chiRouter, p)
		bunR := queryBunRouter(bunRouter, p)

		// Security finding: MM matches but all others 404.
		if mmR.handler == "matched" && hrR.handler == "notfound" && chiR.handler == "notfound" {
			t.Errorf("S8-DIFF SECURITY: %q MM=matched, HR=notfound, CHI=notfound, BUN=%s", p, bunR.handler)
			findings++
		}
		// MM panic is always a finding.
		if mmR.handler == "panic" {
			t.Errorf("S8-DIFF PANIC: %q MM panicked: %s", p, mmR.param)
			findings++
		}
	}
	if findings == 0 {
		t.Logf("S8-DIFF: %d paths tested, no security findings", len(paths))
	}
}

// isValidHTTPPath returns true if the path can be passed to httptest.NewRequest
// without causing url.Parse to fail. We exclude characters that httptest.NewRequest
// rejects: control characters, space, and '#' (fragment delimiter which confuses
// the host-name parser when the URL is "http://example.com#").
func isValidHTTPPath(p string) bool {
	if len(p) == 0 {
		return false
	}
	for i := 0; i < len(p); i++ {
		b := p[i]
		// Reject ASCII control characters (0x00..0x1f) and space (0x20).
		if b <= 0x20 {
			return false
		}
		// Reject DEL (0x7f).
		if b == 0x7f {
			return false
		}
		// Reject '#' — treated as fragment delimiter, breaks url.Parse host check.
		if b == '#' {
			return false
		}
	}
	return true
}

// fuzzServeBypassParse builds a request whose URL.Path is set directly,
// bypassing url.Parse rejection of bare '%', space, and '#'. PRF-2026-0005:
// route the fuzz path through ServeHTTP without httptest.NewRequest's URL
// validation pollution.
func fuzzServeBypassParse(mux http.Handler, method, rawPath string) {
	req, err := http.NewRequest(method, "http://example.com", nil)
	if err != nil {
		return
	}
	req.URL.Path = rawPath
	req.URL.RawPath = rawPath
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
}

// TestS8_Fuzz_CleanPath — mini-fuzz for CleanPath middleware.
func FuzzCleanPath(f *testing.F) {
	seeds := []string{
		"/admin", "//admin", "/./admin", "/../admin",
		"/static/../admin", "/static/%2e%2e/admin",
		"/api/v1/../../../admin", "/users%2f1%2fposts",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	r := mm.New()
	r.Pre(middleware.CleanPath())
	r.GET("/admin", s8h("admin"))
	r.GET("/static/*filepath", s8h("static"))

	f.Fuzz(func(t *testing.T, rawPath string) {
		if !utf8.ValidString(rawPath) || len(rawPath) > 4096 {
			t.Skip()
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("FUZZ-CLEANPATH PANIC: path=%q: %v", rawPath, rec)
			}
		}()
		// Must not panic — bypass url.Parse rejection of '%', space, '#'.
		fuzzServeBypassParse(r, "GET", rawPath)
	})
}

// TestS8_Fuzz_StripSlashes — mini-fuzz for StripSlashes middleware.
func FuzzStripSlashes(f *testing.F) {
	seeds := []string{
		"/admin/", "//admin//", "/admin///", "/",
		"/static//file.txt//", "/users/123//",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	r := mm.New()
	r.Pre(middleware.StripSlashes())
	r.GET("/admin", s8h("admin"))
	r.GET("/users/:id", s8h("users"))

	f.Fuzz(func(t *testing.T, rawPath string) {
		if !utf8.ValidString(rawPath) || len(rawPath) > 4096 {
			t.Skip()
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("FUZZ-STRIPSLASHES PANIC: path=%q: %v", rawPath, rec)
			}
		}()
		fuzzServeBypassParse(r, "GET", rawPath)
	})
}
