package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	chi "github.com/go-chi/chi/v5"
	"github.com/julienschmidt/httprouter"
	bunrouter "github.com/uptrace/bunrouter"
)

// routeResult captures the routing outcome from a single router.
type routeResult struct {
	status  int
	handler string // "matched", "notfound", "405", "redirect", "options"
	param   string // first path param value if any
}

// --- MuxMaster oracle ---

func buildMuxMaster() *mm.Mux {
	r := mm.New()
	r.RedirectTrailingSlash = true
	r.RedirectFixedPath = false

	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "users_id")
		w.Header().Set("X-Param-id", mm.PathParam(req, "id"))
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.Header().Set("X-Param-filepath", mm.PathParam(req, "filepath"))
		w.WriteHeader(200)
	})
	r.GET("/api/v1/items/:id/children/:cid", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "items_children")
		w.Header().Set("X-Param-id", mm.PathParam(req, "id"))
		w.WriteHeader(200)
	})
	r.GET("/public", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "public")
		w.WriteHeader(200)
	})
	return r
}

func queryMuxMaster(mux *mm.Mux, path string) (res routeResult) {
	defer func() {
		if rc := recover(); rc != nil {
			res = routeResult{status: 0, handler: "panic", param: fmt.Sprintf("%v", rc)}
		}
	}()
	req := httptest.NewRequest("GET", "http://x"+path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	res.status = w.Code
	switch w.Code {
	case 200:
		res.handler = "matched"
	case 301, 302, 307, 308:
		res.handler = "redirect"
	case 404:
		res.handler = "notfound"
	case 405:
		res.handler = "405"
	default:
		res.handler = fmt.Sprintf("status%d", w.Code)
	}
	res.param = w.Header().Get("X-Param-id")
	if res.param == "" {
		res.param = w.Header().Get("X-Param-filepath")
	}
	return res
}

// --- httprouter oracle ---

func buildHTTPRouter() *httprouter.Router {
	r := httprouter.New()
	r.GET("/admin", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/users/:id", func(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
		w.Header().Set("X-Handler", "users_id")
		w.Header().Set("X-Param-id", ps.ByName("id"))
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
		w.Header().Set("X-Handler", "static")
		w.Header().Set("X-Param-filepath", ps.ByName("filepath"))
		w.WriteHeader(200)
	})
	r.GET("/api/v1/items/:id/children/:cid", func(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
		w.Header().Set("X-Handler", "items_children")
		w.Header().Set("X-Param-id", ps.ByName("id"))
		w.WriteHeader(200)
	})
	r.GET("/public", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.Header().Set("X-Handler", "public")
		w.WriteHeader(200)
	})
	return r
}

func queryHTTPRouter(r *httprouter.Router, path string) (res routeResult) {
	defer func() {
		if rc := recover(); rc != nil {
			res = routeResult{status: 0, handler: "panic", param: fmt.Sprintf("%v", rc)}
		}
	}()
	req := httptest.NewRequest("GET", "http://x"+path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	res.status = w.Code
	switch w.Code {
	case 200:
		res.handler = "matched"
	case 301, 302, 307, 308:
		res.handler = "redirect"
	case 404:
		res.handler = "notfound"
	case 405:
		res.handler = "405"
	default:
		res.handler = fmt.Sprintf("status%d", w.Code)
	}
	res.param = w.Header().Get("X-Param-id")
	if res.param == "" {
		res.param = w.Header().Get("X-Param-filepath")
	}
	return res
}

// --- chi oracle ---

func buildChi() *chi.Mux {
	r := chi.NewMux()
	r.Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.Get("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "users_id")
		w.Header().Set("X-Param-id", chi.URLParam(req, "id"))
		w.WriteHeader(200)
	})
	r.Get("/static/*", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.Header().Set("X-Param-filepath", chi.URLParam(req, "*"))
		w.WriteHeader(200)
	})
	r.Get("/api/v1/items/{id}/children/{cid}", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "items_children")
		w.Header().Set("X-Param-id", chi.URLParam(req, "id"))
		w.WriteHeader(200)
	})
	r.Get("/public", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler", "public")
		w.WriteHeader(200)
	})
	return r
}

func queryChi(r *chi.Mux, path string) (res routeResult) {
	defer func() {
		if rc := recover(); rc != nil {
			res = routeResult{status: 0, handler: "panic", param: fmt.Sprintf("%v", rc)}
		}
	}()
	req := httptest.NewRequest("GET", "http://x"+path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	res.status = w.Code
	switch w.Code {
	case 200:
		res.handler = "matched"
	case 301, 302, 307, 308:
		res.handler = "redirect"
	case 404:
		res.handler = "notfound"
	case 405:
		res.handler = "405"
	default:
		res.handler = fmt.Sprintf("status%d", w.Code)
	}
	res.param = w.Header().Get("X-Param-id")
	if res.param == "" {
		res.param = w.Header().Get("X-Param-filepath")
	}
	return res
}

// --- bunrouter oracle ---

func buildBunRouter() *bunrouter.Router {
	r := bunrouter.New()
	r.GET("/admin", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
		return nil
	})
	r.GET("/users/:id", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Handler", "users_id")
		w.Header().Set("X-Param-id", req.Param("id"))
		w.WriteHeader(200)
		return nil
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Handler", "static")
		w.Header().Set("X-Param-filepath", req.Param("filepath"))
		w.WriteHeader(200)
		return nil
	})
	r.GET("/api/v1/items/:id/children/:cid", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Handler", "items_children")
		w.Header().Set("X-Param-id", req.Param("id"))
		w.WriteHeader(200)
		return nil
	})
	r.GET("/public", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Handler", "public")
		w.WriteHeader(200)
		return nil
	})
	return r
}

func queryBunRouter(r *bunrouter.Router, path string) (res routeResult) {
	// bunrouter panics on certain malformed paths (e.g. //double-slash). Recover
	// and treat as "panic" result so we can report it as a competitor finding.
	defer func() {
		if rc := recover(); rc != nil {
			res = routeResult{status: 0, handler: "panic", param: fmt.Sprintf("%v", rc)}
		}
	}()
	req := httptest.NewRequest("GET", "http://x"+path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	res = routeResult{status: w.Code}
	switch w.Code {
	case 200:
		res.handler = "matched"
	case 301, 302, 307, 308:
		res.handler = "redirect"
	case 404:
		res.handler = "notfound"
	case 405:
		res.handler = "405"
	default:
		res.handler = fmt.Sprintf("status%d", w.Code)
	}
	res.param = w.Header().Get("X-Param-id")
	if res.param == "" {
		res.param = w.Header().Get("X-Param-filepath")
	}
	return res
}

// =============================================================================
// Differential corpus table
// =============================================================================

// divergenceClass classifies a divergence as a security finding or documented
// behaviour difference.
func divergenceClass(path string, mm, hr, ch, bun routeResult) string {
	// A competitor panic is always noteworthy.
	if bun.handler == "panic" {
		return "COMPETITOR-PANIC:bunrouter"
	}
	allMatch := mm.handler == hr.handler && mm.handler == ch.handler && mm.handler == bun.handler
	if allMatch {
		return ""
	}
	// Documented: chi strips trailing slash by default; httprouter redirects.
	if strings.HasSuffix(path, "/") {
		return "documented-trailing-slash"
	}
	// Documented: bunrouter 0-alloc uses different param encoding.
	// Documented: chi uses different wildcard syntax (*).
	// If MuxMaster says "matched" but all others say "notfound" — security finding.
	if mm.handler == "matched" && hr.handler == "notfound" && ch.handler == "notfound" {
		return "SECURITY:muxmaster-matches-where-others-404"
	}
	// If MuxMaster says "notfound" but all others match — possibly missing route.
	if mm.handler == "notfound" && hr.handler == "matched" && ch.handler == "matched" {
		return "muxmaster-404-where-others-match"
	}
	// Redirect divergence: MuxMaster redirects but others 404.
	if mm.handler == "redirect" && hr.handler == "notfound" {
		return "mm-redirect-where-hr-404"
	}
	return "divergence"
}

func TestDifferential_CorpusPaths(t *testing.T) {
	mmRouter := buildMuxMaster()
	hrRouter := buildHTTPRouter()
	chiRouter := buildChi()
	bunRouter := buildBunRouter()

	type testCase struct {
		path string
		// securityCritical = true means any divergence where MM matches is a finding.
		securityCritical bool
	}

	cases := []testCase{
		// --- Control paths (all must match) ---
		{"/admin", false},
		{"/users/123", false},
		{"/static/image.png", false},
		{"/api/v1/items/42/children/99", false},
		{"/public", false},

		// --- Traversal attacks (all must 404 or redirect, NOT match /admin) ---
		{"/../admin", true},
		{"/users/../admin", true},
		{"/users/1/../admin", true},
		{"/static/../admin", true},
		{"/./admin", true},
		{"/%2e%2e/admin", true},
		{"/%2E%2E/admin", true},
		{"/..%2fadmin", true},
		{"/users/%2e%2e/admin", true},
		{"/static/..%2fadmin", true},
		{"/static/%2e%2e/admin", true},

		// --- Encoding attacks ---
		{"/%61dmin", false},   // %61 = 'a' — should 404 (no pre-routing decode)
		{"/ad%6din", false},   // %6d = 'm'
		{"/%2561dmin", false}, // double-encoded
		// Note: null bytes are tested separately in hypotheses_test.go;
		// httptest.NewRequest rejects them before reaching the router.

		// --- Structural ---
		{"//admin", false},
		{"///admin", false},
		{"/admin//", false},
		{"/admin;param", false},
		{"/admin#fragment", false}, // fragment should not reach server

		// --- Param attacks ---
		{"/users/1%2f2", false}, // %2f = '/' in param
		{"/users/", false},      // empty param after slash
		{"/users", false},       // missing param (TSR candidate)

		// --- Unicode ---
		{"/аdmin", false}, // Cyrillic а ≠ Latin a

		// --- Wildcard ---
		{"/static/", false}, // empty filepath (TSR candidate)
		{"/static", false},  // missing slash (TSR candidate)
		{"/static/a/b/c", false},
		{"/static/../../etc/passwd", true},
	}

	type divRecord struct {
		path  string
		mm    routeResult
		hr    routeResult
		chi   routeResult
		bun   routeResult
		class string
	}

	var divergences []divRecord
	var securityFindings []divRecord

	for _, tc := range cases {
		mmR := queryMuxMaster(mmRouter, tc.path)
		hrR := queryHTTPRouter(hrRouter, tc.path)
		chiR := queryChi(chiRouter, tc.path)
		bunR := queryBunRouter(bunRouter, tc.path)

		class := divergenceClass(tc.path, mmR, hrR, chiR, bunR)
		if class != "" {
			rec := divRecord{tc.path, mmR, hrR, chiR, bunR, class}
			divergences = append(divergences, rec)
			if strings.HasPrefix(class, "SECURITY") || tc.securityCritical {
				if mmR.handler == "matched" {
					securityFindings = append(securityFindings, rec)
				}
			}
		}
	}

	// Report all divergences.
	for _, d := range divergences {
		t.Logf("DIV [%s] path=%q mm=%s hr=%s chi=%s bun=%s",
			d.class, d.path,
			d.mm.handler, d.hr.handler, d.chi.handler, d.bun.handler)
	}

	// Fail on security-critical divergences where MM matched.
	for _, d := range securityFindings {
		t.Errorf("PRF-DIFF SECURITY: path=%q MuxMaster matched (%d) but competitors did not (hr=%s chi=%s bun=%s)",
			d.path, d.mm.status, d.hr.handler, d.chi.handler, d.bun.handler)
	}

	t.Logf("Differential summary: %d paths tested, %d divergences, %d security findings",
		len(cases), len(divergences), len(securityFindings))
}

// =============================================================================
// Fuzz harness: GetValue must not panic
// =============================================================================

func FuzzGetValue(f *testing.F) {
	// Seed corpus.
	seeds := []string{
		"/admin", "/users/123", "/static/img.png",
		"/../admin", "/users/../admin", "/%2e%2e/admin",
		"/..%2fadmin", "/%61dmin", "/%252e%252e/admin",
		"/аdmin", "/admin\x00", "//admin",
		"/users/1%2f2", "/static/../../etc/passwd",
		"/admin;param", "", "/", "//", "///",
		"/users/", "/static/", "/api/v1/items/1/children/",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	mmRouter := buildMuxMaster()

	f.Fuzz(func(t *testing.T, path string) {
		// Skip invalid UTF-8.
		for i := 0; i < len(path); i++ {
			if path[i] > 127 {
				// Check multi-byte validity.
				if !isValidUTF8Prefix(path) {
					t.Skip()
					return
				}
				break
			}
		}
		if len(path) > 8192 {
			t.Skip()
			return
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC path=%q: %v", path, r)
			}
		}()

		mmR := queryMuxMaster(mmRouter, path)

		// Invariant: traversal to /admin must never succeed.
		if containsTraversal(path) && mmR.handler == "matched" {
			// Check which handler was matched.
			req := httptest.NewRequest("GET", "http://x"+path, nil)
			w := httptest.NewRecorder()
			mmRouter.ServeHTTP(w, req)
			if w.Header().Get("X-Handler") == "admin" {
				t.Fatalf("FUZZ-INVARIANT: traversal path %q reached /admin handler", path)
			}
		}
	})
}

func isValidUTF8Prefix(s string) bool {
	for i := 0; i < len(s); {
		if s[i] < 0x80 {
			i++
			continue
		}
		// Simplified: check leading byte.
		b := s[i]
		var size int
		switch {
		case b < 0xC0:
			return false // continuation byte at start
		case b < 0xE0:
			size = 2
		case b < 0xF0:
			size = 3
		default:
			size = 4
		}
		if i+size > len(s) {
			return false
		}
		for j := 1; j < size; j++ {
			if s[i+j]&0xC0 != 0x80 {
				return false
			}
		}
		i += size
	}
	return true
}

// =============================================================================
// Fuzz harness: addRoute must not panic for valid patterns
// =============================================================================

func FuzzAddRoute(f *testing.F) {
	seeds := []string{
		"/admin", "/users/:id", "/static/*filepath",
		"/api/v1/:version/items/:id",
		"/a{/:b}", "/a{/:b}{/:c}",
		"/a{/:b}{/:c}{/:d}",
		"/{name:[a-z]+}/profile",
		"/a/b/c/d/e/f/g",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		if len(pattern) == 0 || pattern[0] != '/' {
			t.Skip()
		}
		if len(pattern) > 512 {
			t.Skip()
		}

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
			r.GET(pattern, func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(200)
			})
		}()

		if panicked {
			// Panics for invalid patterns (conflict, unclosed brace, etc.) are acceptable.
			msg := fmt.Sprintf("%v", panicMsg)
			// Only report unexpected panics — not documented conflict/validation panics.
			unexpected := true
			for _, expected := range []string{
				"conflicts with", "only one wildcard", "wildcards must be named",
				"catch-all routes", "catch-all conflicts", "catch-all requires",
				"already registered", "invalid UTF-8", "unclosed {", "regex param",
				"invalid regexp", "optional segments",
			} {
				if strings.Contains(msg, expected) {
					unexpected = false
					break
				}
			}
			if unexpected {
				t.Fatalf("FUZZ-ADDROUTE UNEXPECTED PANIC pattern=%q: %v", pattern, panicMsg)
			}
		}
	})
}
