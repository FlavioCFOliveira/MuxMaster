// Round 2: deeper investigation of specific new vectors
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ============================================================================
// R2-01: Allowed() exposes route existence even with RedirectFixedPath=false
// When HandleMethodNotAllowed=true (default), a 405 for a traversal-cleaned
// path reveals that a route exists there.
// ============================================================================

func TestR2_01_AllowedMethodLeaksRouteExistence(t *testing.T) {
	r := mm.New()
	r.RedirectFixedPath = false
	r.HandleMethodNotAllowed = true
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})

	// POST //admin: should get 404 (RFP=false), not 405.
	// But allowed() is called after 404 to check other methods.
	// allowed() calls root.hasHandler(urlPath) with the ORIGINAL (double-slash) path.
	// Does //admin in the GET tree have a handler? No — the tree stores /admin.
	// So 405 cannot fire for //admin if hasHandler uses the raw urlPath.
	code, handler := func() (int, string) {
		req := httptest.NewRequest("POST", "http://example.com//admin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("R2-01: POST //admin → code=%d handler=%q", code, handler)
	// 405 would reveal /admin exists. 404 is expected.
	if code == 405 {
		t.Logf("R2-01-NOTE: POST //admin → 405 (Allow header reveals /admin existence via path confusion)")
	}

	// The real test: POST /admin should 405 (method not allowed).
	code2, _ := func() (int, string) {
		req := httptest.NewRequest("POST", "http://example.com/admin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("Allow")
	}()
	if code2 != 405 {
		t.Errorf("R2-01: POST /admin expected 405, got %d", code2)
	}
	t.Logf("R2-01: POST /admin → code=%d (correct 405)", code2)
}

// ============================================================================
// R2-02: CleanPath interaction with double-slash — does //admin → /admin via
// path.Clean in cleanedPath() even with RFP=false?
// ============================================================================

func TestR2_02_CleanPath_DoubleSlashAdmin(t *testing.T) {
	// cleanedPath() is called ONLY when RedirectFixedPath=true.
	// With RFP=false, //admin goes through the tree with the literal double-slash.
	// The tree stores /admin (no //), so it correctly 404s.
	// With RFP=true (via cleanedPath): path.Clean("//admin") = "/admin" → redirect.
	// This is the documented PRF-004 disclosure.

	r := mm.New()
	r.RedirectFixedPath = false
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})

	// WITHOUT CleanPath middleware
	code1, handler1 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com//admin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("R2-02: RFP=false, no CleanPath, //admin → code=%d handler=%q", code1, handler1)
	if handler1 == "admin" {
		t.Errorf("R2-02-BYPASS: //admin reached /admin without RFP or CleanPath")
	}

	// WITH CleanPath via Pre — path.Clean("//admin") = "/admin" → then routes to /admin
	r2 := mm.New()
	r2.RedirectFixedPath = false
	r2.Pre(middleware.CleanPath())
	r2.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	code2, handler2 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com//admin", nil)
		w := httptest.NewRecorder()
		r2.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("R2-02: RFP=false, WITH CleanPath, //admin → code=%d handler=%q", code2, handler2)
	// CleanPath normalises // → / before dispatch. This is documented PRF-001 behaviour.
}

// ============================================================================
// R2-03: Mount + RawPath: can an attacker route to a mounted handler by
// providing a RawPath that matches the Mount prefix differently?
// ============================================================================

func TestR2_03_Mount_RawPath_MatchBypass(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})
	restricted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "restricted")
		w.WriteHeader(200)
	})

	r := mm.New()
	r.UseRawPath = true
	r.Mount("/api", inner)
	r.GET("/admin", restricted)

	// Normal request: /api/v1/data should hit inner
	req1 := httptest.NewRequest("GET", "http://example.com/api/v1/data", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	t.Logf("R2-03: /api/v1/data → code=%d handler=%q", w1.Code, w1.Header().Get("X-Handler"))

	// Attack: can we use a specially crafted RawPath to bypass mount lookup?
	// The wildcard tree stores /api/*mux_mount. UseRawPath=true uses RawPath for lookup.
	// Try: RawPath = /api/%61dmin (encoded 'a' → makes API prefix match but value contains admin)
	req2 := httptest.NewRequest("GET", "http://example.com/api/admin", nil)
	req2.URL.Path = "/api/admin"
	req2.URL.RawPath = "/api/%61dmin"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	t.Logf("R2-03: RawPath=/api/%%61dmin → code=%d handler=%q", w2.Code, w2.Header().Get("X-Handler"))

	// Try: crafted RawPath that ends with encoded characters to see if mux_mount param contains traversal
	req3 := httptest.NewRequest("GET", "http://example.com/api/v1/../admin", nil)
	req3.URL.Path = "/api/v1/../admin"
	req3.URL.RawPath = "/api/v1/%2e%2e/admin"
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	t.Logf("R2-03: RawPath=/api/v1/%%2e%%2e/admin → code=%d handler=%q", w3.Code, w3.Header().Get("X-Handler"))
	if w3.Header().Get("X-Handler") == "restricted" {
		t.Errorf("R2-03-BYPASS: RawPath traversal via Mount reached /admin")
	}
}

// ============================================================================
// R2-04: Tree traversal under concurrent registration + read — no data race
// (Two-phase registration robustness: concurrent ServeHTTP + Handle)
// ============================================================================

func TestR2_04_ConcurrentRegistrationAndRead(t *testing.T) {
	r := mm.New()
	r.GET("/static", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func() {
			// Concurrent readers
			req := httptest.NewRequest("GET", "http://example.com/static", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			done <- true
		}()
	}
	// Concurrent writers (some will panic on conflict — recover gracefully)
	for i := 0; i < 5; i++ {
		go func(n int) {
			defer func() { recover() }()
			r.GET("/dynamic-"+strings.Repeat("x", n), func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(200)
			})
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// After concurrent ops, original route must still work
	code, handler := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/static", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	if code != 200 || handler != "static" {
		t.Errorf("R2-04: concurrent registration corrupted tree: code=%d handler=%q", code, handler)
	}
	t.Logf("R2-04: concurrent registration+read: tree intact (code=%d handler=%q)", code, handler)
}

// ============================================================================
// R2-05: Wildcard shadow — /admin/:sub and /admin/profile — registration order
// ============================================================================

func TestR2_05_WildcardShadow_StaticVsParam(t *testing.T) {
	// Static FIRST, then param — expected: static wins
	r1 := mm.New()
	r1.GET("/admin/profile", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "profile")
		w.WriteHeader(200)
	})
	r1.GET("/admin/:sub", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "param")
		w.WriteHeader(200)
	})

	code1, handler1 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/admin/profile", nil)
		w := httptest.NewRecorder()
		r1.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	if handler1 != "profile" {
		t.Errorf("R2-05: static-first, /admin/profile → handler=%q (want 'profile') code=%d", handler1, code1)
	}
	t.Logf("R2-05: static-first: /admin/profile → %q (correct)", handler1)

	// Test /admin/other goes to :sub
	code2, handler2 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/admin/settings", nil)
		w := httptest.NewRecorder()
		r1.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	if handler2 != "param" {
		t.Errorf("R2-05: /admin/settings → handler=%q (want 'param') code=%d", handler2, code2)
	}
	t.Logf("R2-05: /admin/settings → %q (correct)", handler2)
}

// ============================================================================
// R2-06: Overlong UTF-8 encoding attacks — %C0%AE%C0%AE (overlong ..)
// net/url rejects malformed percent sequences but accepts valid ones.
// These are valid UTF-8 or invalid? %C0%AE = 0xC0 0xAE = overlong encoding of '.'
// net/url.PathUnescape returns them as raw bytes — the router sees them.
// ============================================================================

func TestR2_06_OverlongUTF8_PathTraversal(t *testing.T) {
	r := mm.New()
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	// %C0%AE = overlong encoding of '.', %C0%AF = overlong encoding of '/'
	// When percent-decoded by net/url, they become bytes 0xC0 0xAE (invalid UTF-8).
	// Go's url.PathUnescape decodes %XX sequences unconditionally.
	// But httptest.NewRequest uses url.ParseRequestURI which validates the URL.
	// Use direct URL manipulation instead.
	overlongPaths := []struct {
		path string
		desc string
	}{
		{"/static/%c0%ae%c0%ae/admin", "overlong .. = C0 AE C0 AE"},
		{"/static/%c0%af%c0%ae%c0%ae", "overlong /.. = C0AF C0AE C0AE"},
		{"/%c0%ae%c0%ae/admin", "overlong ../admin"},
	}

	for _, tc := range overlongPaths {
		func() {
			defer func() {
				if rc := recover(); rc != nil {
					t.Logf("R2-06: %s panic (expected invalid UTF-8): %v", tc.desc, rc)
				}
			}()
			u, err := url.Parse("http://example.com" + tc.path)
			if err != nil {
				t.Logf("R2-06: %s → url.Parse error: %v (blocked at HTTP layer)", tc.desc, err)
				return
			}
			req := &http.Request{
				Method: "GET",
				URL:    u,
				Header: make(http.Header),
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			t.Logf("R2-06: %s → code=%d handler=%q path=%q", tc.desc, w.Code, w.Header().Get("X-Handler"), u.Path)
			if w.Header().Get("X-Handler") == "admin" {
				t.Errorf("R2-06-BYPASS: overlong encoding %q reached /admin", tc.path)
			}
		}()
	}
}

// ============================================================================
// R2-07: Regex param — empty match / empty capture group
// ============================================================================

func TestR2_07_RegexParam_EmptyCapture(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				panicked = true
				t.Logf("R2-07: registration panic: %v", rc)
			}
		}()
		r := mm.New()
		// Regex that matches empty string — param captures empty value
		r.GET("/{id:[a-z]*}/profile", func(w http.ResponseWriter, req *http.Request) {
			id := mm.PathParam(req, "id")
			w.Header().Set("X-Param-id", id)
			w.Header().Set("X-Handler", "profile")
			w.WriteHeader(200)
		})

		// Test with empty segment: /x//profile where x is empty
		// Actually router sees //<something> as double-slash
		// Test: /<empty match>/profile
		cases := []struct{ path string }{
			{"/abc/profile"},       // normal match
			{"/123/profile"},       // digits — should not match [a-z]*? Actually [a-z]* matches empty
			{"//profile"},          // empty segment
		}
		for _, tc := range cases {
			req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			t.Logf("R2-07: %q → code=%d handler=%q id=%q", tc.path, w.Code, w.Header().Get("X-Handler"), w.Header().Get("X-Param-id"))
		}
	}()
	_ = panicked
}

// ============================================================================
// R2-08: path.Clean behaviour for specific edge cases — does it handle
// Windows-style backslash paths?
// ============================================================================

func TestR2_08_BackslashInPath(t *testing.T) {
	r := mm.New()
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	// Backslash is NOT a path separator in HTTP/RFC 3986.
	// Go's path.Clean does NOT treat backslash as separator.
	backslashPaths := []struct {
		path string
		desc string
	}{
		{"/static/..\\admin", "backslash in literal path"},
		{"/static/%5c..%5cadmin", "%5c = backslash"},      // %5c = '\'
		{"/static/%5c%2e%2e%5cadmin", "%5c%2e%2e%5c"},
	}
	for _, tc := range backslashPaths {
		func() {
			defer func() {
				if rc := recover(); rc != nil {
					t.Logf("R2-08: %s panic: %v", tc.desc, rc)
				}
			}()
			u, err := url.Parse("http://example.com" + tc.path)
			if err != nil {
				t.Logf("R2-08: %s url.Parse error: %v", tc.desc, err)
				return
			}
			req := &http.Request{
				Method: "GET",
				URL:    u,
				Header: make(http.Header),
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			t.Logf("R2-08: %s → code=%d handler=%q path=%q rawpath=%q", tc.desc, w.Code, w.Header().Get("X-Handler"), u.Path, u.RawPath)
			if w.Header().Get("X-Handler") == "admin" {
				t.Errorf("R2-08-BYPASS: backslash traversal %q reached /admin", tc.path)
			}
		}()
	}
}

// ============================================================================
// R2-09: Mount + double-slash in prefix — does //api/*mux_mount match?
// ============================================================================

func TestR2_09_Mount_DoubleSlashPrefix(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	// Mount at /api — creates /api/*mux_mount in wildcard tree.
	r := mm.New()
	r.Mount("/api", inner)

	// Can we reach Mount via //api?
	req1 := httptest.NewRequest("GET", "http://example.com//api/v1", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	t.Logf("R2-09: //api/v1 → code=%d handler=%q", w1.Code, w1.Header().Get("X-Handler"))
	// Expected: 404 (double-slash not in wildcard tree)

	// Via CleanPath
	r2 := mm.New()
	r2.Pre(middleware.CleanPath())
	r2.Mount("/api", inner)

	req2 := httptest.NewRequest("GET", "http://example.com//api/v1", nil)
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, req2)
	t.Logf("R2-09: CleanPath, //api/v1 → code=%d handler=%q", w2.Code, w2.Header().Get("X-Handler"))
	// With CleanPath: //api/v1 → path.Clean → /api/v1 → matches Mount → inner
}

// ============================================================================
// R2-10: ServeFiles + traversal in URL.Path (not via RawPath)
// When UseRawPath=false (default), URL.Path is decoded by net/url.
// Test that traversal via literal .. segments stays in catch-all.
// ============================================================================

func TestR2_10_ServeFiles_LiteralTraversalStaysInCatchall(t *testing.T) {
	r := mm.New()
	var capturedFP string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		capturedFP = mm.PathParam(req, "filepath")
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})
	r.GET("/etc/passwd", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "sensitive")
		w.WriteHeader(200)
	})

	// /static/../../etc/passwd: net/url decodes the path to literal /../../../etc/passwd
	// The router then does getValue("/static/../../etc/passwd").
	// The static catch-all consumes /static/ as prefix, *filepath = /../../etc/passwd.
	// /etc/passwd is registered separately — can traversal reach it via /static/*?
	req := httptest.NewRequest("GET", "http://example.com/static/../../etc/passwd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	t.Logf("R2-10: /static/../../etc/passwd → code=%d handler=%q fp=%q", w.Code, w.Header().Get("X-Handler"), capturedFP)

	if w.Header().Get("X-Handler") == "sensitive" {
		t.Errorf("R2-10-BYPASS: /static/../../etc/passwd reached /etc/passwd handler — catch-all traversal escape")
	}
	// If handler=static, filepath shows ../../etc/passwd — operator must clean it.
	// This is documented PRF-005 behaviour.
}

// ============================================================================
// R2-11: Pre() middleware modifying r.URL.Path — does tree see the modified path?
// ============================================================================

func TestR2_11_Pre_Middleware_PathModification(t *testing.T) {
	// A Pre() middleware that MODIFIES r.URL.Path before dispatch
	// The dispatch() function uses r.URL.Path (from the potentially modified request).
	// This is the intended mechanism for CleanPath and StripSlashes.
	// Test that Pre() modifications are seen by the tree lookup.

	r := mm.New()
	// Pre() middleware that rewrites /v1/ prefix to /api/v1/
	prefixRewrite := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/") {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/api" + r.URL.Path // /v1/items → /api/v1/items
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	r.Pre(prefixRewrite)
	r.GET("/api/v1/items", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "items")
		w.WriteHeader(200)
	})
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})

	code1, handler1 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/v1/items", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("R2-11: /v1/items → code=%d handler=%q (should rewrite to /api/v1/items)", code1, handler1)
	if handler1 != "items" {
		t.Logf("R2-11-NOTE: Pre() path rewrite not seen by dispatch (expected 'items', got %q)", handler1)
	}

	// Security: prefix-rewrite must not create unintended routes
	// /v1/../admin → /api/v1/../admin (if not cleaned) → NOT /api/admin
	code2, handler2 := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/v1/../admin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("R2-11: /v1/../admin → code=%d handler=%q (traversal after pre-rewrite)", code2, handler2)
	// net/url resolves /v1/../admin to /admin BEFORE Pre() sees it
	// So Pre() sees r.URL.Path=/admin, doesn't rewrite (no /v1/ prefix)
	// → routes to /admin directly
	// This is expected (net/url normalises before Pre) but operator-visible.
}
