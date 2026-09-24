// Regression tests for rmp task #247 (sprint 18): serveRedirect must keep
// applying Use()-registered middleware after the m.mu.RLock() removal
// (CH-06) — middleware application now reads a lock-free snapshot
// (redirectMWPtr) refreshed by Use(), instead of locking m.mu on every
// redirect.
package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestRedirect_Use_MiddlewareBeforeRoute_WrapsRedirectResponse confirms a
// middleware registered via Use() BEFORE the route still wraps the
// trailing-slash redirect response (e.g. it can observe/modify the
// response written by http.Redirect).
func TestRedirect_Use_MiddlewareBeforeRoute_WrapsRedirectResponse(t *testing.T) {
	m := muxmaster.New()
	var wrapped bool
	m.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "1")
			wrapped = true
			next.ServeHTTP(w, r)
		})
	})
	m.GET("/users/", handler(200, "ok"))

	rec := get(m, "/users")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if !wrapped {
		t.Fatalf("middleware registered before the route did not run on the redirect")
	}
	if rec.Header().Get("X-Wrapped") != "1" {
		t.Fatalf("X-Wrapped header not set — middleware did not wrap the redirect response")
	}
}

// TestRedirect_Use_MiddlewareAfterRoute_StillWrapsRedirect confirms a
// middleware registered via Use() AFTER routes were already added is still
// observed by later redirects — the lock-free redirectMWPtr snapshot must
// be refreshed by every Use() call, exactly like the lazy NotFound /
// MethodNotAllowed / OPTIONS caches already are.
func TestRedirect_Use_MiddlewareAfterRoute_StillWrapsRedirect(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/", handler(200, "ok"))

	// First redirect: no middleware registered yet.
	rec1 := get(m, "/users")
	if rec1.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec1.Code)
	}
	if rec1.Header().Get("X-Wrapped") == "1" {
		t.Fatalf("X-Wrapped should not be set before Use() is called")
	}

	// Register middleware AFTER the route.
	m.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "1")
			next.ServeHTTP(w, r)
		})
	})

	// Second redirect: must now be wrapped.
	rec2 := get(m, "/users")
	if rec2.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Wrapped") != "1" {
		t.Fatalf("X-Wrapped header not set after Use() — redirectMWPtr snapshot was not refreshed")
	}
}

// TestRedirect_NoMiddleware_FastPathStillRedirects confirms the common,
// no-middleware case still redirects correctly through the lock-free
// nil-snapshot fast path.
func TestRedirect_NoMiddleware_FastPathStillRedirects(t *testing.T) {
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

// TestRedirect_MultipleMiddleware_OrderPreserved confirms multiple Use()
// calls compose in the expected outermost-first order on the redirect path,
// not just on the normal dispatch path.
func TestRedirect_MultipleMiddleware_OrderPreserved(t *testing.T) {
	m := muxmaster.New()
	var order []string
	mkMW := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	m.Use(mkMW("first"), mkMW("second"))
	m.GET("/users/", handler(200, "ok"))

	_ = get(m, "/users")
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("middleware order = %v, want [first second]", order)
	}
}

// TestRedirect_ConcurrentUseVsConcurrentRedirects_NoRace is the adversarial
// stress test for CH-06's publication-ordering claim: many goroutines call
// Use() to keep appending middleware while many OTHER goroutines
// concurrently fire trailing-slash-redirect requests, at high concurrency
// with -race enabled. This must never race (redirectMWPtr is read via a
// lock-free atomic.Pointer.Load() with no synchronisation against m.mu on
// the reader side) and every redirect response must be internally
// consistent: EITHER it reflects no middleware, OR it reflects some
// prefix of the middleware chain built up to that point (which is always a
// no-op chain here — each middleware just increments a counter — so the
// response is always a 301, and the per-middleware counter is bumped by
// exactly the length of whichever snapshot that particular request's
// serveRedirect call observed).
func TestRedirect_ConcurrentUseVsConcurrentRedirects_NoRace(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/", handler(200, "ok"))

	const useGoroutines = 20
	const useCallsPerGoroutine = 15
	const redirectGoroutines = 200
	const redirectsPerGoroutine = 150

	var mwInvocations int64

	var wg sync.WaitGroup
	wg.Add(useGoroutines + redirectGoroutines)

	for g := 0; g < useGoroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < useCallsPerGoroutine; i++ {
				m.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						atomic.AddInt64(&mwInvocations, 1)
						next.ServeHTTP(w, r)
					})
				})
			}
		}(g)
	}

	var badStatus int64
	for g := 0; g < redirectGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < redirectsPerGoroutine; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "http://example.com/users", nil)
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusMovedPermanently {
					atomic.AddInt64(&badStatus, 1)
				}
			}
		}()
	}

	wg.Wait()

	if got := atomic.LoadInt64(&badStatus); got != 0 {
		t.Fatalf("%d redirect responses had an unexpected status code under concurrent Use()", got)
	}

	// Final sanity: after all Use() calls have settled, a fresh redirect
	// must be wrapped by the FULL, final middleware chain — proving the
	// lock-free snapshot converges to the fully-published state once the
	// writers are done, with no middleware call permanently lost.
	before := atomic.LoadInt64(&mwInvocations)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/users", nil)
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("final redirect: got %d, want 301", rec.Code)
	}
	after := atomic.LoadInt64(&mwInvocations)
	wantDelta := int64(useGoroutines * useCallsPerGoroutine)
	if delta := after - before; delta != wantDelta {
		t.Fatalf("final redirect invoked %d middleware layers, want exactly %d (the full, settled chain)", delta, wantDelta)
	}
}
