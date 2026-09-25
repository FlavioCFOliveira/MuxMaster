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
	// iters trimmed 30000 -> 3000 (rmp #271; further trimmed from an
	// intermediate 6000 on 2026-09-25): pure ServeHTTP volume, no GC-forcing
	// or chain growth — still 3000*n*8 = 384,000 requests at GOMAXPROCS=16.
	const iters = 1800

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
	// iters trimmed 20000 -> 4000 (rmp #271); see the Use() comment below for
	// why the mutator side, not this reader side, was the dominant cost.
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2500; i++ {
				req := httptest.NewRequest("GET", "/missing", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}

	// Concurrent Use() writes m.middleware.
	//
	// Use() appends unboundedly to m.middleware and every lazyNotFound
	// rebuild re-wraps the not-found handler with the whole current chain.
	// 5000 Use() calls on a single goroutine grew the chain to 5000 layers by
	// the end of the run, and every not-found request from the goroutines
	// above paid for however deep the chain was at that moment — this test
	// took 98s of a 320s package run (rmp #271, measured 2026-09-25 under go
	// test -race -count=1 in isolation). 300 calls is the same budget
	// validated in TestS8_LazyBuilders_AllThree_UseAndRebuild
	// (s8_hypotheses_test.go), which races this same invalidation path in
	// 24s.
	wg.Add(1)
	go func() {
		defer wg.Done()
		noop := func(next http.Handler) http.Handler { return next }
		for i := 0; i < 300; i++ {
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
	// iters trimmed 20000 -> 4000; Use() below (not this reader side) trimmed
	// 5000 -> 300 was the dominant cost — see TestUse_LazyNotFound_Race above
	// for the full rationale (rmp #271, measured 2026-09-25).
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2500; i++ {
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
		for i := 0; i < 300; i++ {
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
	// iters trimmed 20000 -> 4000; Use() below (not this reader side) trimmed
	// 5000 -> 300 was the dominant cost — see TestUse_LazyNotFound_Race above
	// for the full rationale (rmp #271, measured 2026-09-25).
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2500; i++ {
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
		for i := 0; i < 300; i++ {
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
	// iters trimmed 10000 -> 2500 (rmp #271): still 2500*n*4*2 = 320,000
	// requests at GOMAXPROCS=16 (measured 2026-09-25: 39.3s of a 320s package
	// run at 10000 iters).
	const iters = 1500

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
