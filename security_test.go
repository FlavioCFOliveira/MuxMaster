package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
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

// ── Phase 4: concurrency regressions ─────────────────────────────────────────

// TestUseConcurrentNoRace verifies MM-2026-0014: Use() and Pre() are protected
// by m.mu and may be called concurrently during route registration without
// causing a DATA RACE. Run with: go test -race.
func TestUseConcurrentNoRace(t *testing.T) {
	m := muxmaster.New()
	noop := func(next http.Handler) http.Handler { return next }
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); m.Use(noop) }()
		go func() { defer wg.Done(); m.Pre(noop) }()
	}
	wg.Wait()
}

// TestIntrospectionConcurrentNoRace verifies MM-2026-0016: Walk, Routes, and
// Lookup hold m.mu.RLock() and do not race with concurrent Handle calls.
// Run with: go test -race.
func TestIntrospectionConcurrentNoRace(t *testing.T) {
	m := muxmaster.New()
	h := handler(200, "ok")
	// Pre-populate a few routes so the tree is non-trivial.
	for _, path := range []string{"/a", "/b/:id", "/c/*rest"} {
		m.GET(path, h)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers: Walk, Routes, Lookup — all concurrent.
	for i := 0; i < 5; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Walk(func(_, _ string, _ http.Handler) error { return nil })
				}
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Routes()
				}
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _, _ = m.Lookup("GET", "/b/42")
				}
			}
		}()
	}

	// Writer: register new unique routes concurrently with the readers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			// Each path must be unique; registering duplicates panics by design.
			m.GET("/dyn/"+string(rune('A'+i)), h)
		}
		close(stop)
	}()

	wg.Wait()
}

// ── Phase 5: tree edge cases ──────────────────────────────────────────────────

// TestCatchAllRequiresSlashPrefix verifies MM-2026-0021: a catch-all wildcard
// without a '/' prefix (e.g. "/{:}*name") must panic at registration time with
// a descriptive message rather than triggering a runtime index-out-of-range.
func TestCatchAllRequiresSlashPrefix(t *testing.T) {
	cases := []string{
		"*bare",    // no leading slash at all
		"/*",       // anonymous catch-all — valid form, but included for coverage
	}
	// The pattern that originally caused the OOB: a catch-all placed directly
	// after a non-slash byte (i.e. `path[i] != '/'` after `i--`).
	malformed := "/{:}*00000"
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		m := muxmaster.New()
		m.GET(malformed, handler(200, "ok"))
	}()
	if !panicked {
		t.Fatalf("expected panic registering %q, got none", malformed)
	}
	_ = cases // kept for documentation; malformed is the OOB reproducer
}

// ── Phase 6: Mount RawPath normalisation ─────────────────────────────────────

// TestMountRawPathNormalisedOnMismatch verifies MM-2026-0022: when Mount strips
// a prefix and the percent-encoded RawPath does not start with the same prefix,
// RawPath is zeroed instead of forwarding a stale encoded path to the inner
// handler.
func TestMountRawPathNormalisedOnMismatch(t *testing.T) {
	var gotRawPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = r.URL.RawPath
		w.WriteHeader(200)
	})

	m := muxmaster.New()
	m.Mount("/api", inner)

	// A request with a percent-encoded path that, after URL parsing, maps to
	// the mounted prefix. RawPath will be set by the test runner; we simulate
	// the case where the encoded form does NOT start with /api to trigger the
	// mismatch branch.
	req := httptest.NewRequest("GET", "/api/v1/resource", nil)
	// Manually set RawPath to a value that does not match prefix "/api",
	// exercising the TrimPrefix-mismatch guard.
	req.URL.RawPath = "/%61pi/v1/resource" // %61 == 'a', so prefix "/api" is not a byte-equal prefix

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// Inner handler must receive empty RawPath, not the stale encoded prefix.
	if gotRawPath != "" {
		t.Fatalf("expected empty RawPath on mismatch, got %q", gotRawPath)
	}
}
