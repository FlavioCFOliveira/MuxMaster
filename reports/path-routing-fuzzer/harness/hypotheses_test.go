package fuzz

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mmmw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	chi "github.com/go-chi/chi/v5"
)

// --- H-010 -----------------------------------------------------------------
// clean_path is a single path.Clean pass. The question is whether the
// encoded traversal survives because path.Clean only normalises textual
// ".." segments. We build every meaningful combination and record the
// decision of the dispatcher.

func TestH010_CleanPathEncodedTraversal(t *testing.T) {
	cases := []struct {
		name      string
		inputPath string
	}{
		{"literal_dotdot", "/static/../admin"},
		{"encoded_slash_dotdot", "/static/..%2fadmin"},
		{"encoded_both", "/static/%2e%2e%2fadmin"},
		{"double_encoded_both", "/static/%252e%252e%252fadmin"},
		{"overlong_utf8", "/static/%c0%ae%c0%ae/admin"},
		{"fullwidth", "/static/%ef%bc%8e%ef%bc%8e%ef%bc%8fadmin"},
		{"semicolon", "/static/..;/admin"},
		{"triple_slash", "/static///../admin"},
	}

	matrix := []struct {
		name string
		cp   bool
		uev  bool
		rfp  bool
	}{
		{"baseline", false, false, false},
		{"cp_only", true, false, false},
		{"cp_unescape", true, true, false},
		{"cp_unescape_rfp", true, true, true},
		{"rfp_only", false, false, true},
		{"unescape_only", false, true, false},
	}

	for _, c := range cases {
		for _, mx := range matrix {
			t.Run(c.name+"_"+mx.name, func(t *testing.T) {
				r := buildMuxMaster(false, mx.rfp, false, mx.uev, false)
				var h http.Handler = r
				if mx.cp {
					h = mmmw.CleanPath()(h)
				}
				req := httptest.NewRequest(http.MethodGet,
					"http://example.test"+c.inputPath, nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				hv := rec.Header().Get("X-Handler")
				if hv == "admin" {
					t.Errorf("H-010 BYPASS: case=%s mx=%s → %s (expected 404 or catch-all)",
						c.name, mx.name, hv)
				}
			})
		}
	}
}

// --- H-012 -----------------------------------------------------------------
// paramsBuf is a fixed array of size 3. Registering a pattern with ≥4
// params must either be rejected at Handle time or the 4th param must be
// silently dropped. The latter would cause a silent security failure.

func TestH012_ParamsBufOverflow(t *testing.T) {
	r := mm.New()
	defer func() {
		if rc := recover(); rc != nil {
			t.Logf("H-012 addRoute panicked: %v (which would be the safer behaviour)", rc)
		}
	}()
	var gotP4 string
	var gotP5 string
	r.GET("/a/:p1/:p2/:p3/:p4/:p5", func(w http.ResponseWriter, req *http.Request) {
		gotP4 = mm.PathParam(req, "p4")
		gotP5 = mm.PathParam(req, "p5")
	})
	req := httptest.NewRequest(http.MethodGet,
		"http://example.test/a/w/x/y/dangerous/also", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	t.Logf("H-012: p4=%q p5=%q (expected: either addRoute panics OR both values present)",
		gotP4, gotP5)
	if gotP4 == "" && gotP5 == "" {
		t.Errorf("H-012 CONFIRMED: both :p4 and :p5 silently dropped (paramsBuf overflow)")
	}
}

// --- H-013 -----------------------------------------------------------------
// When Mount is used, RawPath trim diverges from Path trim. We send a
// request whose RawPath contains percent-encoded equivalents of the prefix
// and observe whether the inner handler sees a consistent URL.

func TestH013_MountRawPathPathDivergence(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Path", r.URL.Path)
		w.Header().Set("X-RawPath", r.URL.RawPath)
		w.WriteHeader(200)
	})
	r := mm.New()
	r.Mount("/api", inner)

	// Request with RawPath containing %61pi (= "api") — url.Parse tends to
	// normalise Path but keeps RawPath as-is. If muxmaster's prefix strip
	// is path-based (trims "/api"), RawPath ends up uncorrelated.
	req := httptest.NewRequest(http.MethodGet,
		"http://example.test/%61pi/foo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	resP := rec.Header().Get("X-Path")
	resR := rec.Header().Get("X-RawPath")
	t.Logf("H-013 Path=%q RawPath=%q", resP, resR)
	if resR != "" && !strings.HasPrefix(resR, "/") {
		t.Errorf("H-013 CONFIRMED: Mount inner handler receives RawPath without leading slash: %q", resR)
	}
	// A well-behaved Mount would leave both fields on matching paths, or both empty.
	if (resP == "" && resR != "") || (resP != "" && resR != "" && !strings.EqualFold(resP, "/foo")) {
		t.Logf("H-013 DIVERGENCE observed: Path=%q RawPath=%q", resP, resR)
	}
}

// --- H-025 -----------------------------------------------------------------
// RedirectTrailingSlash is emitted before any application middleware is
// reached. An unauthenticated caller can distinguish a 404 (route does not
// exist) from a 301 (route exists at the slashed variant). This is a
// pre-auth route disclosure oracle.

func TestH025_TSRPreAuthDisclosure(t *testing.T) {
	var authCalls int
	denyAll := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCalls++
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}

	// muxmaster: register /admin/ at a trailing-slash variant.
	r := mm.New()
	r.Use(denyAll)
	r.GET("/admin/", handlerTag("admin.ts"))

	req := httptest.NewRequest(http.MethodGet, "http://example.test/admin", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	res := rec.Result()
	loc := res.Header.Get("Location")
	if res.StatusCode == 301 || res.StatusCode == 307 || res.StatusCode == 308 {
		t.Errorf("H-025 CONFIRMED (muxmaster): GET /admin returned %d Location=%q BEFORE auth middleware ran (calls=%d)",
			res.StatusCode, loc, authCalls)
	}

	// Oracle: chi on the same scenario. Run-through for comparison.
	c := chi.NewRouter()
	var chiAuthCalls int
	c.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chiAuthCalls++
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	})
	c.Get("/admin/", handlerTag("admin.ts"))
	req2 := httptest.NewRequest(http.MethodGet, "http://example.test/admin", nil)
	rec2 := httptest.NewRecorder()
	c.ServeHTTP(rec2, req2)
	t.Logf("H-025 chi comparison: status=%d Location=%q authCalls=%d",
		rec2.Result().StatusCode, rec2.Result().Header.Get("Location"), chiAuthCalls)
}

// --- H-007/H-020 ----------------------------------------------------------
// RedirectFixedPath may produce a Location with a // prefix (open-redirect
// shape) if path.Clean retains it. Empirically path.Clean collapses "//"
// but we probe anyway and assert the actual Location value.

func TestH007_OpenRedirectViaFixedPath(t *testing.T) {
	r := mm.New()
	r.GET("/evil.com/foo", handlerTag("evil"))
	// RedirectFixedPath=true by default.
	cases := []string{
		"//evil.com/foo",
		"///evil.com/foo",
		"//evil.com/foo/",
		"/./evil.com/foo",
		"/./../evil.com/foo",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+p, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			loc := rec.Header().Get("Location")
			if strings.HasPrefix(loc, "//") {
				t.Errorf("H-007 CONFIRMED: path=%q returned Location=%q (protocol-relative)", p, loc)
			}
		})
	}
}

// --- Wildcard shadow ------------------------------------------------------
// When a :param and a static child co-exist at the same position, the
// registration order must not affect correctness — and the router must
// never panic at dispatch time.

func TestWildcardShadow_StaticAfterParamPanicsAtDispatch(t *testing.T) {
	// Register :param first, then a static sibling.
	r := mm.New()
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				t.Logf("addRoute /a/b after /a/:x panicked (safer): %v", rc)
			}
		}()
		r.GET("/a/:x", handlerTag("param"))
		r.GET("/a/b", handlerTag("static"))
	}()

	// Dispatch /a/b — if addRoute did NOT panic, the tree may be corrupt.
	req := httptest.NewRequest(http.MethodGet, "http://example.test/a/b", nil)
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() {
			panicked = recover()
		}()
		r.ServeHTTP(rec, req)
	}()
	if panicked != nil {
		t.Errorf("WILDCARD-SHADOW PANIC CONFIRMED: dispatching /a/b after registering /a/:x + /a/b panics with: %v", panicked)
	}
	if rec.Code == 200 && rec.Header().Get("X-Handler") != "static" {
		t.Errorf("wildcard shadow: /a/b routed to %q (expected static)", rec.Header().Get("X-Handler"))
	}
}

// Inverse order: static first, then :param.
func TestWildcardShadow_ParamAfterStatic(t *testing.T) {
	r := mm.New()
	r.GET("/a/b", handlerTag("static"))
	defer func() {
		if rc := recover(); rc != nil {
			t.Logf("addRoute /a/:x after /a/b panicked: %v", rc)
		}
	}()
	r.GET("/a/:x", handlerTag("param"))

	req2 := httptest.NewRequest(http.MethodGet, "http://example.test/a/c", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if h := rec2.Header().Get("X-Handler"); h != "param" {
		t.Errorf("wildcard shadow: /a/c routed to %q (expected param)", h)
	}
}

// --- Regex ReDoS compile cost (H-014/H-019) --------------------------------

func TestRegexCompilePanic(t *testing.T) {
	// Patterns that should produce a clean panic at addRoute, not hang.
	cases := []string{
		"/{x:[}", // unbalanced char class
		"/{x:}",  // empty regex → still valid empty expression actually
		"/{x:(a+)+$}",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			func() {
				defer func() { _ = recover() }()
				r := mm.New()
				r.GET(p, handlerTag("x"))
			}()
		})
	}
}
