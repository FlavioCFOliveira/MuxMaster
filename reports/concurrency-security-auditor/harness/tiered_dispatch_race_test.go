//go:build race

// tiered_dispatch_race_test.go — CSA harness for tiered reqBundle dispatch.
// Stress-tests dispatchParams1Fast, dispatchParams2Fast, and the 3+ path.
// Also tests the Use() / lazyNotFound MM-2026-0049 race.

package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestTieredDispatch_AllPaths_Race exercises all three dispatch tiers and
// the static-route path simultaneously under maximum concurrency.
func TestTieredDispatch_AllPaths_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var counts [4]int64 // [static, 1-param, 2-param, 3-param]

	// Static.
	r.GET("/static/route", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&counts[0], 1)
		w.WriteHeader(http.StatusOK)
	})
	// 1-param (reqBundle1, dispatchParams1Fast).
	r.GET("/p1/:a", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&counts[1], 1)
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 || ps[0].Key != "a" {
			t.Errorf("1-param: wrong params: %+v", ps)
		}
		w.WriteHeader(http.StatusOK)
	})
	// 2-param (reqBundle2, dispatchParams2Fast).
	r.GET("/p2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&counts[2], 1)
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 {
			t.Errorf("2-param: wrong params: %+v", ps)
		}
		w.WriteHeader(http.StatusOK)
	})
	// 3-param (reqBundle, inline path).
	r.GET("/p3/:a/:b/:c", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&counts[3], 1)
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			t.Errorf("3-param: wrong params: %+v", ps)
		}
		w.WriteHeader(http.StatusOK)
	})

	routes := []string{
		"/static/route",
		"/p1/val",
		"/p2/x/y",
		"/p3/x/y/z",
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 30000

	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				path := routes[(g*i)%len(routes)]
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("unexpected status %d for path %s", w.Code, path)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(n * 8 * iters)
	sum := counts[0] + counts[1] + counts[2] + counts[3]
	if sum != total {
		t.Errorf("dispatch count mismatch: sum=%d want=%d", sum, total)
	}
}

// TestUse_LazyNotFound_Race is the primary harness for MM-2026-0049.
// Use() writes m.middleware while lazyNotFound() reads it without lock.
// The race detector MUST fire on the current (unfixed) code.
func TestUse_LazyNotFound_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.GET("/found", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// ServeHTTP goroutines hit the not-found path (calls lazyNotFound).
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				req := httptest.NewRequest("GET", "/missing", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}

	// Concurrent Use() writes m.middleware.
	wg.Add(1)
	go func() {
		defer wg.Done()
		noop := func(next http.Handler) http.Handler { return next }
		for i := 0; i < 5000; i++ {
			r.Use(noop)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

// TestUse_LazyMethodNotAllowed_Race tests the same race for lazyMethodNotAllowed.
func TestUse_LazyMethodNotAllowed_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.POST("/item", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// Requests that trigger 405 (hit lazyMethodNotAllowed).
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				req := httptest.NewRequest("DELETE", "/item", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}

	// Concurrent Use() writes m.middleware.
	wg.Add(1)
	go func() {
		defer wg.Done()
		noop := func(next http.Handler) http.Handler { return next }
		for i := 0; i < 5000; i++ {
			r.Use(noop)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

// TestUse_LazyOPTIONS_Race tests the same race for lazyOPTIONS.
func TestUse_LazyOPTIONS_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.GET("/endpoint", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.POST("/endpoint", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// OPTIONS requests trigger lazyOPTIONS.
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				req := httptest.NewRequest("OPTIONS", "/endpoint", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}

	// Concurrent Use() writes m.middleware.
	wg.Add(1)
	go func() {
		defer wg.Done()
		noop := func(next http.Handler) http.Handler { return next }
		for i := 0; i < 5000; i++ {
			r.Use(noop)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

// TestTieredDispatch_FastHandler_NoMW verifies that FastHandler routes are served
// correctly under concurrent load. No stdlib middleware should wrap them.
func TestTieredDispatch_FastHandler_NoMW(t *testing.T) {
	t.Parallel()
	r := mm.New()

	// FPE-2026-010: Use() now panics if HandleFast routes already exist on
	// the same mux when Use is called too. Register the fast route FIRST,
	// then add Use for the stdlib route. This still validates the
	// "fast handler runs without stdlib MW" invariant the test was written for.
	var fastCalled int64
	r.GETFast("/fast/:id", func(w http.ResponseWriter, req *http.Request, ps mm.Params) {
		atomic.AddInt64(&fastCalled, 1)
		w.WriteHeader(http.StatusOK)
	})

	var mwCalled int64
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			atomic.AddInt64(&mwCalled, 1)
			next.ServeHTTP(w, req)
		})
	})
	r.GET("/slow/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 10000

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/fast/%d", i), nil))
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/slow/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	expectedFast := int64(n * 4 * iters)
	expectedSlow := int64(n * 4 * iters)

	if atomic.LoadInt64(&fastCalled) != expectedFast {
		t.Errorf("fast handler called %d times, want %d", fastCalled, expectedFast)
	}
	// Middleware must NOT have been called for fast routes.
	if atomic.LoadInt64(&mwCalled) != expectedSlow {
		t.Errorf("middleware called %d times for fast routes; want exactly %d (slow only) — HandleFast bypass confirmed",
			mwCalled, expectedSlow)
	}
}
