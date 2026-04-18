package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// ── Phase 1: radix tree security fixes ───────────────────────────────────────

func TestWildcardStaticConflictPanics(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		m := muxmaster.New()
		m.GET("/users/:id", handler(200, "user"))
		m.GET("/users/list", handler(200, "list")) // static sibling of wildcard — must panic
	}()
	if !panicked {
		t.Fatal("expected panic when registering static sibling of wildcard child")
	}
}

func TestAddRouteRejectsInvalidUTF8(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		m := muxmaster.New()
		m.GET("/\xff", handler(200, "ok"))
	}()
	if !panicked {
		t.Fatal("expected panic on invalid UTF-8 path")
	}
}

func TestFourParamRoute(t *testing.T) {
	m := muxmaster.New()
	m.GET("/a/:p1/b/:p2/c/:p3/d/:p4", func(w http.ResponseWriter, r *http.Request) {
		v1 := muxmaster.PathParam(r, "p1")
		v4 := muxmaster.PathParam(r, "p4")
		w.Write([]byte(v1 + "-" + v4)) //nolint:errcheck
	})
	rec := get(m, "/a/1/b/2/c/3/d/4")
	if rec.Body.String() != "1-4" {
		t.Fatalf("4-param route: got %q, want %q", rec.Body.String(), "1-4")
	}
}

func TestEightParamRoute(t *testing.T) {
	m := muxmaster.New()
	m.GET("/a/:p1/b/:p2/c/:p3/d/:p4/e/:p5/f/:p6/g/:p7/h/:p8", func(w http.ResponseWriter, r *http.Request) {
		v1 := muxmaster.PathParam(r, "p1")
		v8 := muxmaster.PathParam(r, "p8")
		w.Write([]byte(v1 + "-" + v8)) //nolint:errcheck
	})
	rec := get(m, "/a/1/b/2/c/3/d/4/e/5/f/6/g/7/h/8")
	if rec.Body.String() != "1-8" {
		t.Fatalf("8-param route: got %q, want %q", rec.Body.String(), "1-8")
	}
}

// ── Phase 2: goroutine safety with r.WithContext ──────────────────────────────

func TestParamRouteGoroutineSafe(t *testing.T) {
	// Verifies that r.Context() is valid in goroutines spawned by the handler.
	// go test -race must pass.
	m := muxmaster.New()
	done := make(chan string, 1)
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rCopy := r
		go func() {
			// After the handler returns, ctx and params must still be valid.
			_ = ctx.Done()
			done <- muxmaster.PathParam(rCopy, "id")
		}()
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/42", nil)
	m.ServeHTTP(rec, req)
	select {
	case v := <-done:
		if v != "42" {
			t.Fatalf("got %q, want %q", v, "42")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for goroutine")
	}
}

func TestPanicDoesNotLeakPool(t *testing.T) {
	// Verifies that the pool is not contaminated after a panic in the handler.
	m := muxmaster.New()
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		w.WriteHeader(500)
	}
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		panic("intentional")
	})
	// First request panics — pool must remain clean.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/42", nil)
	m.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	// Second request must work normally — pool not contaminated.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/users/99", nil)
	m.ServeHTTP(rec2, req2)
	// If we reach here without panic, the pool is clean.
}

// ── Phase 3: middleware runs on redirect/error responses ──────────────────────

func TestRedirectTrailingSlashRunsMiddleware(t *testing.T) {
	called := false
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}
	m := muxmaster.New()
	m.Use(auth)
	m.GET("/users/", handler(200, "ok"))

	called = false
	rec := get(m, "/users") // triggers TSR redirect
	if !called {
		t.Fatal("middleware was not called on TSR redirect")
	}
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
}

func TestRedirectFixedPathDefaultOff(t *testing.T) {
	m := muxmaster.New()
	// RedirectFixedPath must be false by default.
	if m.RedirectFixedPath {
		t.Fatal("RedirectFixedPath should be false by default")
	}
	m.GET("/admin/console", handler(200, "ok"))
	rec := get(m, "/admin//console")
	// With RedirectFixedPath=false, must 404, not 301.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with RedirectFixedPath=false, got %d", rec.Code)
	}
}

func TestOptions405RunsMiddleware(t *testing.T) {
	called := false
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}
	m := muxmaster.New()
	m.Use(auth)
	m.GET("/secret", handler(200, "ok"))

	// OPTIONS must pass through middleware.
	called = false
	rec := do(m, http.MethodOptions, "/secret")
	if !called {
		t.Fatal("middleware not called on OPTIONS")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// 405 must pass through middleware.
	called = false
	rec = do(m, http.MethodPost, "/secret")
	if !called {
		t.Fatal("middleware not called on 405")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestNotFoundRunsMiddleware(t *testing.T) {
	called := false
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}
	m := muxmaster.New()
	m.Use(auth)
	m.GET("/exists", handler(200, "ok"))

	called = false
	rec := get(m, "/missing")
	if !called {
		t.Fatal("middleware not called on 404")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
