package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
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
