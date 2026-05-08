package muxmaster_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// handler returns a HandlerFunc that writes code and body.
func handler(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body)) //nolint:errcheck
	}
}

// get performs a GET request against mux and returns the recorded response.
func get(mux http.Handler, path string) *httptest.ResponseRecorder {
	return do(mux, http.MethodGet, path)
}

func do(mux http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	mux.ServeHTTP(rec, req)
	return rec
}

// ── Static routes ────────────────────────────────────────────────────────────

func TestStaticRoutes(t *testing.T) {
	m := muxmaster.New()
	m.GET("/", handler(200, "root"))
	m.GET("/users", handler(200, "users"))
	m.GET("/users/list", handler(200, "list"))
	m.POST("/users", handler(201, "created"))

	tests := []struct {
		method string
		path   string
		code   int
		body   string
	}{
		{http.MethodGet, "/", 200, "root"},
		{http.MethodGet, "/users", 200, "users"},
		{http.MethodGet, "/users/list", 200, "list"},
		{http.MethodPost, "/users", 201, "created"},
	}

	for _, tc := range tests {
		rec := do(m, tc.method, tc.path)
		if rec.Code != tc.code {
			t.Errorf("%s %s: got status %d, want %d", tc.method, tc.path, rec.Code, tc.code)
		}
		if got := rec.Body.String(); got != tc.body {
			t.Errorf("%s %s: got body %q, want %q", tc.method, tc.path, got, tc.body)
		}
	}
}

// ── Param routes ─────────────────────────────────────────────────────────────

func TestParamRoutes(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(muxmaster.PathParam(r, "id"))) //nolint:errcheck
	})
	m.GET("/posts/:year/:month/:slug", func(w http.ResponseWriter, r *http.Request) {
		y := muxmaster.PathParam(r, "year")
		mo := muxmaster.PathParam(r, "month")
		sl := muxmaster.PathParam(r, "slug")
		w.Write([]byte(y + "-" + mo + "-" + sl)) //nolint:errcheck
	})

	rec := get(m, "/users/42")
	if rec.Body.String() != "42" {
		t.Fatalf("single param: got %q", rec.Body.String())
	}

	rec = get(m, "/posts/2024/04/hello-world")
	if rec.Body.String() != "2024-04-hello-world" {
		t.Fatalf("multi param: got %q", rec.Body.String())
	}
}

// TestParamRoutesOverflowInline exercises routes with more than bundleInlineMax (3) params.
// These use an overflow slice (make(Params, n)) instead of the inline array in requestCtx.small.
func TestParamRoutesOverflowInline(t *testing.T) {
	m := muxmaster.New()
	// 4 params — one beyond bundleInlineMax=3.
	m.GET("/a/:p1/b/:p2/c/:p3/d/:p4", func(w http.ResponseWriter, r *http.Request) {
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 4 {
			t.Errorf("want 4 params, got %d", len(ps))
			return
		}
		w.Write([]byte(ps[0].Value + "," + ps[1].Value + "," + ps[2].Value + "," + ps[3].Value)) //nolint:errcheck
	})

	rec := get(m, "/a/1/b/2/c/3/d/4")
	if want := "1,2,3,4"; rec.Body.String() != want {
		t.Fatalf("overflow params: got %q, want %q", rec.Body.String(), want)
	}
}

// ── Wildcard routes ───────────────────────────────────────────────────────────

func TestWildcardRoutes(t *testing.T) {
	m := muxmaster.New()
	m.GET("/static/*filepath", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(muxmaster.PathParam(r, "filepath"))) //nolint:errcheck
	})

	rec := get(m, "/static/js/app.js")
	if want := "/js/app.js"; rec.Body.String() != want {
		t.Fatalf("wildcard: got %q, want %q", rec.Body.String(), want)
	}
}

// ── Not Found ─────────────────────────────────────────────────────────────────

func TestNotFound(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.GET("/exists", handler(200, "ok"))

	rec := get(m, "/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestCustomNotFound(t *testing.T) {
	m := muxmaster.New()
	m.NotFound = handler(404, "custom not found")

	rec := get(m, "/nope")
	if rec.Body.String() != "custom not found" {
		t.Fatalf("got %q", rec.Body.String())
	}
}

// ── Trailing-slash redirect ───────────────────────────────────────────────────

func TestTrailingSlashRedirect(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/", handler(200, "ok"))

	rec := get(m, "/users")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/" {
		t.Fatalf("Location: got %q, want /users/", loc)
	}
}

func TestTrailingSlashRedirectRemove(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users", handler(200, "ok"))

	rec := get(m, "/users/")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
}

func TestNoTrailingSlashRedirectWhenDisabled(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.GET("/users/", handler(200, "ok"))

	rec := get(m, "/users")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ── Method Not Allowed ────────────────────────────────────────────────────────

func TestMethodNotAllowed(t *testing.T) {
	m := muxmaster.New()
	m.POST("/resource", handler(201, "ok"))

	rec := get(m, "/resource")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !contains(allow, http.MethodPost) {
		t.Fatalf("Allow header %q missing POST", allow)
	}
}

func TestCustomMethodNotAllowed(t *testing.T) {
	m := muxmaster.New()
	m.MethodNotAllowed = handler(405, "nope")
	m.POST("/resource", handler(201, "ok"))

	rec := get(m, "/resource")
	if rec.Body.String() != "nope" {
		t.Fatalf("got %q", rec.Body.String())
	}
}

// ── OPTIONS ───────────────────────────────────────────────────────────────────

func TestOPTIONS(t *testing.T) {
	m := muxmaster.New()
	m.GET("/resource", handler(200, "ok"))
	m.POST("/resource", handler(201, "ok"))

	rec := do(m, http.MethodOptions, "/resource")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !contains(allow, http.MethodGet) || !contains(allow, http.MethodPost) {
		t.Fatalf("Allow header missing methods: %q", allow)
	}
}

// ── Middleware ────────────────────────────────────────────────────────────────

func TestMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	m := muxmaster.New()
	m.Use(mw("a"), mw("b"))
	m.GET("/", handler(200, "ok"))

	get(m, "/")

	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("middleware order: got %v", order)
	}
}

// ── Groups ────────────────────────────────────────────────────────────────────

func TestGroup(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/v1")
	api.GET("/users", handler(200, "users"))
	api.POST("/users", handler(201, "created"))

	if rec := get(m, "/api/v1/users"); rec.Code != 200 {
		t.Fatalf("group GET: got %d", rec.Code)
	}
	if rec := do(m, http.MethodPost, "/api/v1/users"); rec.Code != 201 {
		t.Fatalf("group POST: got %d", rec.Code)
	}
}

func TestGroupMiddleware(t *testing.T) {
	called := false
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}

	m := muxmaster.New()
	api := m.Group("/api")
	api.Use(auth)
	api.GET("/secret", handler(200, "ok"))
	m.GET("/public", handler(200, "ok"))

	called = false
	get(m, "/public")
	if called {
		t.Fatal("auth middleware ran on public route")
	}

	called = false
	get(m, "/api/secret")
	if !called {
		t.Fatal("auth middleware did not run on secret route")
	}
}

func TestSubGroup(t *testing.T) {
	m := muxmaster.New()
	v1 := m.Group("/api/v1")
	admin := v1.Group("/admin")
	admin.GET("/dashboard", handler(200, "dash"))

	rec := get(m, "/api/v1/admin/dashboard")
	if rec.Code != 200 || rec.Body.String() != "dash" {
		t.Fatalf("sub-group: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// ── Panic handler ─────────────────────────────────────────────────────────────

func TestPanicHandler(t *testing.T) {
	recovered := false
	m := muxmaster.New()
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		recovered = true
		w.WriteHeader(http.StatusInternalServerError)
	}
	m.GET("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	rec := get(m, "/boom")
	if !recovered {
		t.Fatal("panic was not recovered")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ── ParamsFromContext ─────────────────────────────────────────────────────────

func TestParamsFromContext(t *testing.T) {
	m := muxmaster.New()
	m.GET("/items/:id/tags/:tag", func(w http.ResponseWriter, r *http.Request) {
		ps := muxmaster.ParamsFromContext(r.Context())
		w.Write([]byte(ps.Get("id") + ":" + ps.Get("tag"))) //nolint:errcheck
	})

	rec := get(m, "/items/99/tags/go")
	if rec.Body.String() != "99:go" {
		t.Fatalf("got %q", rec.Body.String())
	}
}

// ── Examples (compiled and verified by go test) ───────────────────────────────

func Example_helloWorld() {
	r := muxmaster.New()
	r.GET("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "Hello, World!")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(rec.Body.String())
	// Output: Hello, World!
}

func Example_pathParams() {
	r := muxmaster.New()
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		id := muxmaster.PathParam(req, "id")
		fmt.Fprint(w, "user="+id)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	fmt.Println(rec.Body.String())
	// Output: user=42
}

func Example_middleware() {
	var order []string
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			order = append(order, "mw")
			next.ServeHTTP(w, req)
		})
	}

	r := muxmaster.New()
	r.Use(mw)
	r.GET("/ping", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
		fmt.Fprint(w, "pong")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	fmt.Println(order)
	// Output: [mw handler]
}

func Example_groups() {
	r := muxmaster.New()

	api := r.Group("/api/v1")
	api.GET("/items", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "items")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/items", nil))
	fmt.Println(rec.Body.String())
	// Output: items
}

// helpers

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) && (stringContains(s, substr)))
}

func stringContains(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// CDX-S8-002 — ServeFiles MUST refuse to register when UseRawPath=true and
// UnescapePathValues=true would expose http.FileServer to %2f-decoded
// traversal in the captured filepath param.
func TestS8_CDX002_ServeFilesTraversal_Hardened(t *testing.T) {
	t.Run("registers cleanly with default options", func(t *testing.T) {
		r := muxmaster.New()
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ServeFiles panicked unexpectedly: %v", rec)
			}
		}()
		r.ServeFiles("/static/*filepath", http.Dir("."))
	})

	t.Run("panics with UseRawPath+UnescapePathValues both true", func(t *testing.T) {
		r := muxmaster.New()
		r.UseRawPath = true
		r.UnescapePathValues = true
		var got string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					got = fmt.Sprintf("%v", rec)
				}
			}()
			r.ServeFiles("/static/*filepath", http.Dir("."))
		}()
		if got == "" {
			t.Fatal("expected ServeFiles to panic with UseRawPath+UnescapePathValues both true")
		}
		if !strings.Contains(got, "CDX-S8-002") {
			t.Fatalf("expected message to cite CDX-S8-002, got %q", got)
		}
	})

	t.Run("safe pattern: path.Clean on captured filepath blocks traversal", func(t *testing.T) {
		// Custom handler that path.Clean's the value BEFORE filesystem dispatch.
		r := muxmaster.New()
		r.GET("/files/*filepath", func(w http.ResponseWriter, req *http.Request) {
			raw := muxmaster.PathParam(req, "filepath")
			cleaned := path.Clean("/" + raw)
			if strings.Contains(cleaned, "..") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("X-Cleaned", cleaned)
			w.WriteHeader(200)
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/files/../etc/passwd", nil))
		// path.Clean("/" + "../etc/passwd") = "/etc/passwd" — no ".." remains, so
		// the handler accepts; the operator-defined sanitisation is in effect.
		if rec.Header().Get("X-Cleaned") != "/etc/passwd" {
			t.Fatalf("expected /etc/passwd after Clean, got %q", rec.Header().Get("X-Cleaned"))
		}
	})
}

// PRF-2026-0003 — regex param name max length is 254 bytes; panic message
// must reflect that, not "exceeds 255 bytes".
func TestS8_PRF_RegexNameOffByOne(t *testing.T) {
	t.Run("254 bytes accepted", func(t *testing.T) {
		r := muxmaster.New()
		name := strings.Repeat("a", 254)
		pattern := "/v/{" + name + ":x}"
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("254-byte regex name panicked: %v", rec)
			}
		}()
		r.GET(pattern, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	})

	t.Run("255 bytes rejected with clear message", func(t *testing.T) {
		r := muxmaster.New()
		name := strings.Repeat("a", 255)
		pattern := "/v/{" + name + ":x}"
		var got string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					got = fmt.Sprintf("%v", rec)
				}
			}()
			r.GET(pattern, func(w http.ResponseWriter, _ *http.Request) {})
		}()
		if got == "" {
			t.Fatal("expected panic for 255-byte regex name")
		}
		if !strings.Contains(got, "at most 254 bytes") {
			t.Fatalf("expected message to cite 254-byte cap, got %q", got)
		}
	})
}

// CDX-S8-003 — Pre/Use × Handle/HandleFast policy matrix.
// Asserts each of the 4 cells documented in SECURITY.md
// "Pre vs Use security boundary".
func TestS8_CDX003_PrePolicyMatrix(t *testing.T) {
	t.Run("Pre wraps Handle", func(t *testing.T) {
		r := muxmaster.New()
		var hits int
		r.Pre(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				hits++
				next.ServeHTTP(w, req)
			})
		})
		r.GET("/slow", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/slow", nil))
		if hits != 1 || rec.Code != 200 {
			t.Fatalf("Pre+Handle: hits=%d code=%d, want hits=1 code=200", hits, rec.Code)
		}
	})

	t.Run("Pre wraps HandleFast", func(t *testing.T) {
		r := muxmaster.New()
		var hits int
		r.Pre(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				hits++
				next.ServeHTTP(w, req)
			})
		})
		r.GETFast("/fast", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) { w.WriteHeader(200) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/fast", nil))
		if hits != 1 || rec.Code != 200 {
			t.Fatalf("Pre+HandleFast: hits=%d code=%d, want hits=1 code=200", hits, rec.Code)
		}
	})

	t.Run("Use wraps Handle", func(t *testing.T) {
		r := muxmaster.New()
		var hits int
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				hits++
				next.ServeHTTP(w, req)
			})
		})
		r.GET("/slow", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/slow", nil))
		if hits != 1 || rec.Code != 200 {
			t.Fatalf("Use+Handle: hits=%d code=%d, want hits=1 code=200", hits, rec.Code)
		}
	})

	t.Run("Group.Use + Group.HandleFast panics at registration", func(t *testing.T) {
		r := muxmaster.New()
		g := r.Group("/api")
		g.Use(func(next http.Handler) http.Handler { return next })
		var got string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					got = fmt.Sprintf("%v", rec)
				}
			}()
			g.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {})
		}()
		if got == "" {
			t.Fatal("Group.Use + Group.HandleFast: expected panic at HandleFast registration (CSA-2026-0054)")
		}
		if !strings.Contains(got, "Use") {
			t.Fatalf("expected message to cite Use, got %q", got)
		}
	})

	t.Run("Mux.Use + Mux.HandleFast panics at registration (FPE-2026-010)", func(t *testing.T) {
		// Closes the asymmetry that previously allowed silent auth bypass:
		// HandleFast on a Mux carrying stdlib middleware is now refused at
		// registration time, mirroring Group.HandleFast (CSA-2026-0054).
		r := muxmaster.New()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req)
			})
		})
		var got string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					got = fmt.Sprintf("%v", rec)
				}
			}()
			r.GETFast("/fast", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {})
		}()
		if got == "" {
			t.Fatal("Mux.Use + Mux.HandleFast: expected panic at registration (FPE-2026-010)")
		}
		if !strings.Contains(got, "Use") || !strings.Contains(got, "HandleFast") {
			t.Fatalf("panic message = %q, want hint about HandleFast + Use", got)
		}
	})
}

// CSA-2026-0059 — Pre wraps BOTH stdlib and HandleFast routes; Use wraps
// only stdlib (and panics on HandleFast registration). This test asserts
// the documented matrix used in SECURITY.md "Pre vs Use security boundary".
func TestSec_PreVsUse_SecurityBoundary(t *testing.T) {
	r := muxmaster.New()

	// preCalls is bumped by a Pre middleware on every dispatch.
	var preCalls int
	r.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			preCalls++
			next.ServeHTTP(w, req)
		})
	})

	// Slow route (Handle/stdlib).
	r.GET("/slow", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	// Fast route (HandleFast).
	r.GETFast("/fast", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
		w.WriteHeader(200)
	})

	// One request to each.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/slow", nil))
	if rec.Code != 200 {
		t.Fatalf("/slow: got %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/fast", nil))
	if rec.Code != 200 {
		t.Fatalf("/fast: got %d, want 200", rec.Code)
	}
	if preCalls != 2 {
		t.Fatalf("Pre must wrap BOTH route types: preCalls=%d, want 2", preCalls)
	}
}

// FPE-2026-0001 — tree.go regex param parser must accept regex bodies that
// contain '}' literally (e.g. `(})`, `a{2,3}`, `[}]`, `\}`). The fix replaces
// the naive first-'}' scan with a "last '}' in segment" rule.
func TestS8_FPE_RegexBodyContainsBrace(t *testing.T) {
	patterns := []struct {
		pattern  string
		matchURL string
		wantOK   bool
	}{
		{"/foo/{id:(})}", "/foo/}", true},   // regex matches literal '}'
		{"/q/{n:a{2,3}}", "/q/aaa", true},   // quantifier
		{"/c/{id:[}]}", "/c/}", true},       // char class
		{"/e/{id:\\}}", "/e/}", true},       // escaped }
	}
	for _, tc := range patterns {
		tc := tc
		t.Run(tc.pattern, func(t *testing.T) {
			r := muxmaster.New()
			r.GET(tc.pattern, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", tc.matchURL, nil))
			if tc.wantOK && rec.Code != 200 {
				t.Fatalf("pattern %q match %q: got %d, want 200", tc.pattern, tc.matchURL, rec.Code)
			}
		})
	}
}

// PRF-2026-0001 — consecutive optional segments must panic at registration
// with a clear message (no opaque "wildcard conflict"), and non-consecutive
// optional segments must continue to expand correctly (4 routes).
func TestS8_PRF_ConsecutiveOptionals(t *testing.T) {
	t.Run("consecutive panics with clear message", func(t *testing.T) {
		r := muxmaster.New()
		var got string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					got = fmt.Sprintf("%v", rec)
				}
			}()
			r.GET("/a{/:p1}{/:p2}", func(w http.ResponseWriter, _ *http.Request) {})
		}()
		if got == "" {
			t.Fatal("expected panic for consecutive optional segments")
		}
		if !strings.Contains(got, "consecutive optional segments not supported") {
			t.Fatalf("expected clear message, got %q", got)
		}
	})

	t.Run("non-consecutive expand to 4 routes", func(t *testing.T) {
		r := muxmaster.New()
		hit := func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }
		r.GET("/users{/:id}/posts{/:post}", hit)

		// Expanded routes: /users/posts, /users/:id/posts, /users/posts/:post, /users/:id/posts/:post.
		for _, p := range []string{"/users/posts", "/users/42/posts", "/users/posts/13", "/users/42/posts/13"} {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			if rec.Code != 200 {
				t.Errorf("non-consecutive optionals: %q got %d, want 200", p, rec.Code)
			}
		}
	})
}

// TestTwoPhaseRegistrationPanic_LiveTreeIntact verifies that a panic during
// route registration leaves the previously-published tree intact — readers
// concurrently serving requests must not observe partial mutation
// (MM-2026-0033 / rmp #13). The test:
//   1. Registers a working route /ok and confirms it serves 200.
//   2. Attempts to register a pattern that panics during expansion (more
//      than maxOptionalSegments=8 optional segments).
//   3. Asserts the live tree still serves /ok and that the failed pattern
//      is NOT registered.
func TestTwoPhaseRegistrationPanic_LiveTreeIntact(t *testing.T) {
	r := muxmaster.New()
	r.GET("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Sanity: /ok serves before the panic.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-panic /ok status = %d, want 200", rec.Code)
	}

	// Attempt a pattern that panics inside addRoute (segment cap).
	bad := "/a"
	for i := 0; i < 12; i++ {
		bad += "{/:p}"
	}
	bad += "/end"
	panicked := false
	func() {
		defer func() {
			if recv := recover(); recv != nil {
				panicked = true
			}
		}()
		r.GET(bad, func(w http.ResponseWriter, _ *http.Request) {})
	}()
	if !panicked {
		t.Fatalf("expected panic registering oversized optional pattern")
	}

	// Live tree must still serve /ok and must NOT have registered the
	// partial bad pattern (e.g. /a or /a/end).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("post-panic /ok status = %d, want 200 (tree corruption)", rec.Code)
	}
	for _, p := range []string{"/a", "/a/end", "/a/x/end"} {
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("post-panic %q status = %d, want 404 (partial route registered — two-phase regression)", p, rec.Code)
		}
	}
}

// COV-2026-006 — Group shortcuts (all method aliases).
func TestGroup_AllMethodShortcuts(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	g := r.Group("/api")

	type route struct {
		method string
		path   string
		fn     func(string, http.HandlerFunc)
	}
	type eroute struct {
		method string
		path   string
		fn     func(string, muxmaster.HandlerFuncE)
	}

	hf := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	ef := func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusNoContent); return nil }

	routes := []route{
		{http.MethodGet, "/g", g.GET},
		{http.MethodHead, "/h", g.HEAD},
		{http.MethodPost, "/p", g.POST},
		{http.MethodPut, "/u", g.PUT},
		{http.MethodPatch, "/pa", g.PATCH},
		{http.MethodDelete, "/d", g.DELETE},
		{http.MethodOptions, "/o", g.OPTIONS},
		{http.MethodConnect, "/c", g.CONNECT},
		{http.MethodTrace, "/t", g.TRACE},
	}
	for _, rt := range routes {
		rt.fn(rt.path, hf)
	}
	eroutes := []eroute{
		{http.MethodGet, "/eg", g.GETE},
		{http.MethodHead, "/eh", g.HEADE},
		{http.MethodPost, "/ep", g.POSTE},
		{http.MethodPut, "/eu", g.PUTE},
		{http.MethodPatch, "/epa", g.PATCHE},
		{http.MethodDelete, "/ed", g.DELETEE},
		{http.MethodOptions, "/eo", g.OPTIONSE},
	}
	for _, rt := range eroutes {
		rt.fn(rt.path, ef)
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(rt.method, "/api"+rt.path, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s %s: status=%d", rt.method, rt.path, rec.Code)
		}
	}
	for _, rt := range eroutes {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(rt.method, "/api"+rt.path, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s %s: status=%d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestGroup_ANY_Match_With(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	g := r.Group("/api")

	g.ANY("/any", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	g.Match([]string{http.MethodGet, http.MethodPost}, "/match", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(m, "/api/any", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("ANY %s: status=%d", m, rec.Code)
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(m, "/api/match", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("Match %s: status=%d", m, rec.Code)
		}
	}
	// Method outside allowlist must NOT match Match.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/match", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("Match DELETE should not match")
	}

	// With creates a copy with extra middleware
	calls := 0
	scoped := g.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			calls++
			next.ServeHTTP(w, req)
		})
	})
	scoped.GET("/with", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/with", nil))
	if calls != 1 || rec.Code != http.StatusOK {
		t.Errorf("With middleware: calls=%d code=%d", calls, rec.Code)
	}
}

// COV-2026-005 — Mux.HandleE + all *E shortcuts.
func TestHandleE_DefaultErrorHandler(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	r.GETE("/boom", func(_ http.ResponseWriter, _ *http.Request) error {
		return fmt.Errorf("explode")
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("body=%q", rec.Body.String())
	}
}

func TestHandleE_CustomErrorHandler(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	var captured error
	r.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		captured = err
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("custom"))
	}
	r.GETE("/x", func(_ http.ResponseWriter, _ *http.Request) error {
		return fmt.Errorf("nope")
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if captured == nil {
		t.Fatal("error handler not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status=%d want 418", rec.Code)
	}
}

func TestHandleE_NilErrorPassesThrough(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	r.GETE("/ok", func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d", rec.Code)
	}
}

func TestHandleE_AllShortcuts(t *testing.T) {
	t.Parallel()
	methods := []struct {
		register func(*muxmaster.Mux, string, muxmaster.HandlerFuncE)
		method   string
	}{
		{(*muxmaster.Mux).GETE, http.MethodGet},
		{(*muxmaster.Mux).HEADE, http.MethodHead},
		{(*muxmaster.Mux).POSTE, http.MethodPost},
		{(*muxmaster.Mux).PUTE, http.MethodPut},
		{(*muxmaster.Mux).PATCHE, http.MethodPatch},
		{(*muxmaster.Mux).DELETEE, http.MethodDelete},
		{(*muxmaster.Mux).OPTIONSE, http.MethodOptions},
	}
	for _, m := range methods {
		t.Run(m.method, func(t *testing.T) {
			r := muxmaster.New()
			m.register(r, "/r", func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusNoContent)
				return nil
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(m.method, "/r", nil))
			if rec.Code != http.StatusNoContent {
				t.Errorf("%s: status=%d", m.method, rec.Code)
			}
		})
	}
}

func TestError_HTTPError(t *testing.T) {
	e := muxmaster.Error(http.StatusBadRequest, fmt.Errorf("bad input"))
	if e.StatusCode() != http.StatusBadRequest {
		t.Errorf("code=%d", e.StatusCode())
	}
	if e.Error() != "bad input" {
		t.Errorf("msg=%q", e.Error())
	}
}

func TestError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("root cause")
	e := muxmaster.Error(http.StatusBadRequest, inner)
	type unwrapper interface{ Unwrap() error }
	uw, ok := e.(unwrapper)
	if !ok {
		t.Fatal("Error does not implement Unwrap")
	}
	if uw.Unwrap() != inner {
		t.Errorf("Unwrap=%v want %v", uw.Unwrap(), inner)
	}
}

func TestError_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil err")
		}
	}()
	_ = muxmaster.Error(500, nil)
}

// TestRegression_TM_2026_008 — UseFast on a Group must not cause Pre-registered
// stdlib middleware on the parent Mux to be skipped on dispatch. Pre runs in
// ServeHTTP before tree lookup, so it must wrap both stdlib and fast routes
// regardless of registration order.
func TestRegression_TM_2026_008(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	preCalls := 0
	g := r.Group("/api")
	g.UseFast(func(next muxmaster.FastHandler) muxmaster.FastHandler {
		return func(w http.ResponseWriter, req *http.Request, ps muxmaster.Params) {
			next(w, req, ps)
		}
	})
	g.HandleFast(http.MethodGet, "/x", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})
	r.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			preCalls++
			next.ServeHTTP(w, req)
		})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if preCalls != 1 {
		t.Errorf("Pre middleware ran %d times for fast route, want 1 (TM-2026-008 bypass)", preCalls)
	}
}

// TestRegression_TM_2026_027 — PanicHandler operator-contract: when an
// operator installs a PanicHandler, the recovered value (which may include
// secrets passed to handlers) must NOT be propagated to the client body.
// We verify both the boundary and the safe recipe (write a constant 500).
func TestRegression_TM_2026_027_PanicHandlerNoLeak(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	gotRecovered := false
	r.PanicHandler = func(w http.ResponseWriter, _ *http.Request, recovered any) {
		gotRecovered = true
		// Safe pattern: do NOT echo recovered to body.
		_, secret := recovered.(string)
		_ = secret // suppress unused linter notice
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}
	r.GET("/boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("SECRET-API-KEY-12345")
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))
	if !gotRecovered {
		t.Fatalf("PanicHandler not invoked")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET-API-KEY") {
		t.Errorf("PanicHandler leaked recovered value to client: %q", rec.Body.String())
	}
}

// TestRegression_TM_2026_036 — Group-of-Group middleware ordering: a
// middleware applied to an outer Group must wrap routes registered on the
// inner Group as well as direct child routes.
func TestRegression_TM_2026_036(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	outerCalls, innerCalls := 0, 0
	outer := r.Group("/api")
	outer.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			outerCalls++
			next.ServeHTTP(w, req)
		})
	})
	inner := outer.Group("/v1")
	inner.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			innerCalls++
			next.ServeHTTP(w, req)
		})
	})
	inner.GET("/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	outer.GET("/y", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	for _, p := range []string{"/api/v1/x", "/api/y"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Errorf("%s status = %d", p, rec.Code)
		}
	}
	if outerCalls != 2 {
		t.Errorf("outer middleware ran %d times, want 2", outerCalls)
	}
	if innerCalls != 1 {
		t.Errorf("inner middleware ran %d times, want 1", innerCalls)
	}
}

// TestRegression_TM_2026_039 — transposition of Caddy CVE-2022-0653
// (auth bypass via path-normalisation order). MuxMaster's clean_path middleware
// applies path.Clean BEFORE dispatch and zeroes RawPath when its decoded form
// would clean differently — so a guard middleware sees the same canonical
// path the router will dispatch on. This test exercises a percent-encoded
// traversal payload against /admin to confirm there's no bypass.
func TestRegression_TM_2026_039_CaddyTransposition(t *testing.T) {
	t.Parallel()

	innerCalls := 0
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Inspect raw RawPath so encoded dot segments are visible —
			// equivalent to the WAF that Caddy bypassed.
			if strings.Contains(req.URL.Path, "..") ||
				strings.Contains(req.URL.RawPath, "%2e") ||
				strings.Contains(req.URL.RawPath, "%2E") ||
				strings.Contains(req.URL.RawPath, "%2f") ||
				strings.Contains(req.URL.RawPath, "%2F") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	}

	// Guard registered BEFORE CleanPath in Pre order so it observes the raw
	// (pre-canonical) path — modelling a typical operator setup where a WAF
	// or auth middleware does its own validation first.
	r := muxmaster.New()
	r.UseRawPath = true
	r.Pre(guard)
	r.Pre(middleware.CleanPath())
	r.GET("/admin", func(w http.ResponseWriter, _ *http.Request) {
		innerCalls++
		w.WriteHeader(http.StatusOK)
	})

	payloads := []string{
		"/foo/%2e%2e/admin",
		"/admin/%2e",
		"/foo/..%2fadmin",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			innerBefore := innerCalls
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			if innerCalls != innerBefore {
				t.Errorf("inner /admin handler executed for %q (TM-2026-039 bypass: guard saw raw, dispatch saw clean)", p)
			}
			if rec.Code == http.StatusOK {
				t.Errorf("status 200 for %q: traversal reached protected handler", p)
			}
		})
	}
}

// TestRegression_TM_2026_009 documents the operator-boundary contract for
// handlers that use path-parameter values as filesystem paths under
// UseRawPath=true + UnescapePathValues=true. The router intentionally does
// NOT clean param values; the handler MUST do so before any os.Open call.
// SECURITY.md "UseRawPath traversal" describes the contract — this test
// asserts that:
//   1. A handler that DOES NOT call path.Clean is exposed to traversal.
//   2. A handler that DOES call path.Clean is safe.
// The test does not write to disk; it only inspects the captured param value.
func TestRegression_TM_2026_009_UseRawPath_HandlerBoundary(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	r.UseRawPath = true
	r.UnescapePathValues = true

	var captured string
	r.GET("/files/*filepath", func(w http.ResponseWriter, req *http.Request) {
		captured = muxmaster.PathParam(req, "filepath")
		w.WriteHeader(http.StatusOK)
	})

	// /files/..%2fetc%2fpasswd → captured filepath includes traversal.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/files/..%2fetc%2fpasswd", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// The router preserves the raw param so the handler can decide. A handler
	// calling path.Clean reduces the value to a safe relative form.
	if captured == "" {
		t.Errorf("filepath param was not captured")
	}
	cleaned := path.Clean("/" + captured)
	if strings.Contains(cleaned, "..") {
		t.Errorf("path.Clean failed to neutralise traversal in %q (cleaned=%q) — operator MUST clean", captured, cleaned)
	}
}

// TestRegression_TM_2026_007 verifies that a Pre()-registered middleware on
// the parent Mux wraps a Group.Mount-attached raw http.Handler. Without this,
// auth-gating Pre middleware would not run on traffic that lands on the
// mounted handler — a silent bypass.
func TestRegression_TM_2026_007(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	preCalls := 0
	r.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			preCalls++
			next.ServeHTTP(w, req)
		})
	})

	innerCalls := 0
	external := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalls++
		w.WriteHeader(http.StatusOK)
	})

	g := r.Group("/admin")
	g.Mount("/legacy", external)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/legacy/x", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if preCalls != 1 {
		t.Errorf("Pre middleware ran %d times on Group.Mount handler, want 1 (TM-2026-007: silent auth bypass)", preCalls)
	}
	if innerCalls != 1 {
		t.Errorf("inner handler ran %d times, want 1", innerCalls)
	}
}

// TestRegression_FPE_2026_010 verifies that calling HandleFast on a Mux that
// already has Use()-registered stdlib middleware panics. Before the fix only
// Group.HandleFast had this guard (CSA-2026-0054); the root Mux silently
// accepted the registration, leaving auth middleware unattached to the fast
// route — an unannounced auth bypass.
func TestRegression_FPE_2026_010(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
		})
	}
	r.Use(authMW)

	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatalf("FPE-2026-010: Mux.HandleFast did NOT panic when Use() middleware is present (silent auth bypass)")
		}
		msg, _ := rec.(string)
		if !strings.Contains(msg, "HandleFast") || !strings.Contains(msg, "Use") {
			t.Errorf("panic message = %q, want hint about HandleFast + Use boundary", msg)
		}
	}()
	// Should panic.
	r.HandleFast(http.MethodGet, "/api/users/:id", func(_ http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {})
}

// TestRegression_HPS_2026_0005 verifies that the TSR + RedirectFixedPath
// redirects emit a same-origin path-only Location even when the incoming
// request was sent in absolute-form URI (RFC 7230 §5.3.2). Before the fix,
// a request like "GET http://evil.com/admin/ HTTP/1.1" would cause net/http
// to populate r.URL.Scheme / r.URL.Host with attacker-controlled values, and
// r.URL.String() would return the absolute URL → 301 Location: http://evil.com/admin
// → navigable open redirect.
func TestRegression_HPS_2026_0005(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	r.RedirectTrailingSlash = true
	r.RedirectFixedPath = true
	r.GET("/admin", handler(http.StatusOK, "ok"))

	cases := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "TSR_AbsoluteFormURI",
			req: func() *http.Request {
				// Programmatic equivalent of "GET http://evil.com/admin/ HTTP/1.1".
				req := httptest.NewRequest("GET", "http://evil.com/admin/", nil)
				return req
			}(),
		},
		{
			name: "FixedPath_AbsoluteFormURI",
			req: func() *http.Request {
				// /admin/. canonicalises to /admin via path.Clean.
				req := httptest.NewRequest("GET", "http://evil.com/admin/.", nil)
				return req
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusPermanentRedirect && rec.Code != http.StatusFound && rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("status = %d, expected 3xx redirect", rec.Code)
			}
			loc := rec.Header().Get("Location")
			if loc == "" {
				t.Fatalf("missing Location header")
			}
			if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") || strings.HasPrefix(loc, "//") {
				t.Errorf("HPS-2026-0005: Location is absolute (open redirect): %q", loc)
			}
			if strings.Contains(loc, "evil.com") {
				t.Errorf("HPS-2026-0005: Location leaks attacker host: %q", loc)
			}
			// The Location must still point at the correct same-origin path.
			if !strings.HasPrefix(loc, "/admin") {
				t.Errorf("Location = %q, expected to start with /admin", loc)
			}
		})
	}
}

// TestRegression_CSA_2026_0060 verifies that route params remain accessible
// inside a handler even when a Use()-registered middleware wraps the request
// context (e.g. via context.WithTimeout, context.WithValue, context.WithCancel).
// Before the fix, routeCtxParams did a direct type switch on the outermost
// context, which missed when a middleware wrapped it — causing PathParam,
// ParamsFromContext, and RoutePattern to silently return empty values. The
// slow-path fallback now traverses the context chain via ctx.Value(contextKey{}).
func TestRegression_CSA_2026_0060(t *testing.T) {
	t.Parallel()

	// timeoutMiddleware is the canonical case: stdlib http middleware that
	// returns r.WithContext(ctx) wraps the *requestCtx with a *timerCtx,
	// which is exactly the type-switch miss that the slow-path fallback fixes.
	timeoutMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			defer cancel()
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
	type valueKey struct{}
	valueMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), valueKey{}, "x")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}

	cases := []struct {
		name string
		mw   func(http.Handler) http.Handler
	}{
		{"WithTimeout", timeoutMiddleware},
		{"WithValue", valueMiddleware},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				idValue   string
				paramsLen int
				pattern   string
			}
			r := muxmaster.New()
			r.Use(tc.mw)
			r.GET("/users/:userID", func(w http.ResponseWriter, req *http.Request) {
				got.idValue = muxmaster.PathParam(req, "userID")
				got.paramsLen = len(muxmaster.ParamsFromContext(req.Context()))
				got.pattern = muxmaster.RoutePattern(req)
				w.WriteHeader(http.StatusOK)
			})

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/abc123", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got.paramsLen != 1 {
				t.Errorf("ParamsFromContext len = %d, want 1 (CSA-2026-0060: params dropped through %s wrapper)", got.paramsLen, tc.name)
			}
			if got.idValue != "abc123" {
				t.Errorf("PathParam(userID) = %q, want %q (CSA-2026-0060)", got.idValue, "abc123")
			}
			if got.pattern != "/users/:userID" {
				t.Errorf("RoutePattern = %q, want %q (CSA-2026-0060)", got.pattern, "/users/:userID")
			}
		})
	}
}

// TestRegression_CSA_2026_0060_DeepWrap stacks 3 stdlib middlewares (timeout +
// value + cancel) before reaching the handler. Each layer wraps the context
// once — the slow-path fallback must still traverse all the way to the
// requestCtx layer.
func TestRegression_CSA_2026_0060_DeepWrap(t *testing.T) {
	t.Parallel()
	r := muxmaster.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, cancel := context.WithTimeout(req.Context(), time.Second)
			defer cancel()
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	type kA struct{}
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), kA{}, "a")))
		})
	})
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	var firstID, secondID, pattern string
	var paramsN int
	r.GET("/a/:first/b/:second", func(w http.ResponseWriter, req *http.Request) {
		firstID = muxmaster.PathParam(req, "first")
		secondID = muxmaster.PathParam(req, "second")
		paramsN = len(muxmaster.ParamsFromContext(req.Context()))
		pattern = muxmaster.RoutePattern(req)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/a/x/b/y", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if paramsN != 2 {
		t.Errorf("paramsLen = %d, want 2 (CSA-2026-0060 deep wrap)", paramsN)
	}
	if firstID != "x" || secondID != "y" {
		t.Errorf("params = (%q,%q), want (\"x\",\"y\")", firstID, secondID)
	}
	if pattern != "/a/:first/b/:second" {
		t.Errorf("RoutePattern = %q, want %q", pattern, "/a/:first/b/:second")
	}
}
