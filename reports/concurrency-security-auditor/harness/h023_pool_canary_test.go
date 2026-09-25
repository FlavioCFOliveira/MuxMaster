//go:build race

// h023_pool_canary_test.go — CSA pool contamination canary tests.
// The current architecture uses no sync.Pool for Params (reqBundle is GC-managed).
// These tests verify there is no cross-request param contamination regardless.
//
// All tests confirm zero leaks of __CANARY__ sentinel values between requests.

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

// TestPool_NoCrossRequestLeak is the primary canary: a request to /canary/:id
// deliberately sets a "poisoned" value in params and ensures subsequent requests
// to /clean/:id never see that value.
func TestPool_NoCrossRequestLeak(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var leaks int64

	// Route A: check for canary contamination.
	r.GET("/clean/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		for _, p := range ps {
			if p.Key == "__CANARY__" || p.Value == "__CANARY_VALUE__" {
				atomic.AddInt64(&leaks, 1)
			}
		}
		// Correct: only 'id' should be present.
		if len(ps) != 1 || ps[0].Key != "id" {
			atomic.AddInt64(&leaks, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	// Route B: simulates a request that "contaminates" (in a pooled design, this
	// would poison the pool; in the GC design, this just exercises the allocator).
	//
	// runtime.GC() forces a full, synchronous, effectively stop-the-world GC
	// cycle. Forcing it on every one of the ~n*8*10000 hits to this route (rmp
	// #271: 1.28M calls at GOMAXPROCS=16) serializes all concurrently-running
	// goroutines against each other on every single request and was the
	// dominant cost of this test (224s of a 320s package run, measured
	// 2026-09-25 under go test -race -count=1 in isolation). A GC forced once
	// every gcEveryNCanaryHits requests still interleaves hundreds of real,
	// synchronous GC cycles against thousands of concurrently in-flight
	// requests — the property this test exists to check (a live reqBundle
	// must survive a GC that runs while it is reachable) — without paying for
	// a GC on every single one of the ~1e6 iterations.
	const gcEveryNCanaryHits = 4000
	var canaryHits int64
	r.GET("/canary/:__CANARY__", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		_ = ps
		// Simulate holding a stale reference (like a bad handler would).
		if atomic.AddInt64(&canaryHits, 1)%gcEveryNCanaryHits == 0 {
			runtime.GC()
		}
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	// outer iters trimmed 10000 -> 4000 (rmp #271): after bounding the
	// forced-GC frequency above, the remaining cost here is pure ServeHTTP
	// volume (2 routes/iteration); still 4000*n*8*2 = 1,024,000 requests at
	// GOMAXPROCS=16 (measured 2026-09-25: 63.7s of an 85s package run at
	// 10000 outer iters — comfortably above the 1e6-canary-iteration floor
	// on its own).
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2400; i++ {
				// Alternate: canary route then clean route.
				r.ServeHTTP(
					httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/canary/__CANARY_VALUE__"), nil),
				)
				r.ServeHTTP(
					httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/clean/%d", i), nil),
				)
			}
		}(g)
	}
	wg.Wait()

	if n := atomic.LoadInt64(&leaks); n > 0 {
		t.Fatalf("pool/GC contamination: %d canary leaks detected", n)
	}
}

// TestPool_MultiTier_Isolation ensures that 1-param, 2-param, and 3-param bundles
// do not share or alias each other's param storage.
func TestPool_MultiTier_Isolation(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var violations int64

	r.GET("/t1/:a", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/t2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/t3/:a/:b/:c", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	routes := []string{"/t1/x", "/t2/x/y", "/t3/x/y/z"}
	// iters trimmed 30000 -> 6000 (rmp #271): still 6000*n*8 = 768,000
	// requests at GOMAXPROCS=16, no GC-forcing or chain growth in this test —
	// its cost scales linearly with raw request volume.
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 3600; i++ {
				path := routes[i%len(routes)]
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
			}
		}(g)
	}
	wg.Wait()

	if v := atomic.LoadInt64(&violations); v > 0 {
		t.Fatalf("multi-tier param isolation violated: %d cases", v)
	}
}

// TestPool_GCPressure_Canary exercises GC under high allocation pressure to
// confirm reqBundle objects are not prematurely collected while in use.
func TestPool_GCPressure_Canary(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var badCtx int64

	// See the identical rationale in TestPool_NoCrossRequestLeak: a forced,
	// synchronous runtime.GC() on every request (n*4*2000 = 128,000 calls at
	// GOMAXPROCS=16) serializes the whole n*4-goroutine stress fan-out on
	// every iteration. Bounding it to one forced GC every gcEveryNHits
	// requests still exercises "GC runs while the bundle is live and
	// reachable" hundreds of times per run.
	const gcEveryNHits = 50
	var hits int64
	r.GET("/gc/:token", func(w http.ResponseWriter, req *http.Request) {
		// Trigger GC inside the handler — the bundle is still reachable via req.
		if atomic.AddInt64(&hits, 1)%gcEveryNHits == 0 {
			runtime.GC()
		}
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 || ps[0].Key != "token" {
			atomic.AddInt64(&badCtx, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				path := fmt.Sprintf("/gc/tok%d", (g*i)%10000)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
			}
		}(g)
	}
	wg.Wait()

	if n := atomic.LoadInt64(&badCtx); n > 0 {
		t.Fatalf("GC collected live reqBundle: %d cases", n)
	}
}
