package harness

// Concurrency regression harness for the tiered reqBundle dispatch introduced
// in the 2026-04-18 performance optimisation.
//
// New code paths under test:
//   - dispatchParams1  → reqBundle1 (416 B, 1-param routes)
//   - dispatchParams2  → reqBundle2 (448 B, 2-param routes)
//   - default branch   → reqBundle  (480 B, 3-param routes) — unchanged; re-tested for regression
//   - routeCtxParams / routeCtxPattern  type-switch ordering
//
// Invariants asserted:
//   1. setReqCtxUnsafe is only called on freshly-allocated structs before any
//      goroutine sees them → no data race on bundle.req.ctx.
//   2. The original *http.Request r is never mutated.
//   3. Params values are stable across the full handler lifetime, including
//      goroutines spawned inside the handler.
//   4. Type-switch in routeCtxParams/routeCtxPattern covers all three types
//      without race.
//   5. Concurrent requests on 1-, 2-, 3-param routes do not cross-contaminate
//      param values.
//   6. PanicHandler path (dispatchWithRecover) leaves router in clean state
//      after panic in all three tiers.
//   7. Fallback (hasReqCtxField=false simulation) not exercised directly here —
//      tested via the standard -race run which exercises the live code path.
//
// Run with:
//   go test -race -count=10 -timeout=15m \
//     ./reports/concurrency-security-auditor/harness/ \
//     -run=TestTiered

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// ---------------------------------------------------------------------------
// Invariant 1 + 3: no data race; params stable after ServeHTTP returns
// (handlers spawn goroutines that read params AFTER the dispatch function has
// returned — the most adversarial scenario for any pool or unsafe trick).
// ---------------------------------------------------------------------------

func TestTiered1Param_Race_HandlerSpawnsGoroutine(t *testing.T) {
	r := mm.New()
	var mismatches int64

	r.GET("/a/:id", func(w http.ResponseWriter, req *http.Request) {
		wantID := req.URL.Path[len("/a/"):]
		// Snapshot INSIDE handler (correct usage — should be race-free).
		snapshotID := mm.PathParam(req, "id")
		// Spawn goroutine that reads the request's context after dispatch returns.
		go func(req *http.Request, want, snap string) {
			runtime.Gosched()
			time.Sleep(20 * time.Microsecond)
			// Both the snapshot and the live read must agree.
			liveID := mm.PathParam(req, "id")
			if snap != want || liveID != want {
				atomic.AddInt64(&mismatches, 1)
			}
		}(req, wantID, snapshotID)
	})

	const workers, perWorker = 64, 2000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				id := fmt.Sprintf("w%d-i%d", g, i)
				req := httptest.NewRequest(http.MethodGet, "/a/"+id, nil)
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(g)
	}
	wg.Wait()
	time.Sleep(30 * time.Millisecond) // let spawned goroutines drain

	if m := atomic.LoadInt64(&mismatches); m > 0 {
		t.Fatalf("1-param tiered: %d param mismatches (value corruption or race)", m)
	}
	t.Logf("1-param tiered goroutine-spawn: mismatches=0 (PASS)")
}

func TestTiered2Param_Race_HandlerSpawnsGoroutine(t *testing.T) {
	r := mm.New()
	var mismatches int64

	r.GET("/b/:user/:action", func(w http.ResponseWriter, req *http.Request) {
		wantUser := mm.PathParam(req, "user")
		wantAction := mm.PathParam(req, "action")
		go func(req *http.Request, wu, wa string) {
			runtime.Gosched()
			time.Sleep(20 * time.Microsecond)
			gotUser := mm.PathParam(req, "user")
			gotAction := mm.PathParam(req, "action")
			if gotUser != wu || gotAction != wa {
				atomic.AddInt64(&mismatches, 1)
			}
		}(req, wantUser, wantAction)
	})

	const workers, perWorker = 64, 2000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				path := fmt.Sprintf("/b/user%d/action%d", g, i)
				req := httptest.NewRequest(http.MethodGet, path, nil)
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(g)
	}
	wg.Wait()
	time.Sleep(30 * time.Millisecond)

	if m := atomic.LoadInt64(&mismatches); m > 0 {
		t.Fatalf("2-param tiered: %d param mismatches", m)
	}
	t.Logf("2-param tiered goroutine-spawn: mismatches=0 (PASS)")
}

func TestTiered3Param_Race_HandlerSpawnsGoroutine(t *testing.T) {
	r := mm.New()
	var mismatches int64

	r.GET("/c/:org/:repo/:ref", func(w http.ResponseWriter, req *http.Request) {
		wantOrg := mm.PathParam(req, "org")
		wantRepo := mm.PathParam(req, "repo")
		wantRef := mm.PathParam(req, "ref")
		go func(req *http.Request, wo, wr, wref string) {
			runtime.Gosched()
			time.Sleep(20 * time.Microsecond)
			gotOrg := mm.PathParam(req, "org")
			gotRepo := mm.PathParam(req, "repo")
			gotRef := mm.PathParam(req, "ref")
			if gotOrg != wo || gotRepo != wr || gotRef != wref {
				atomic.AddInt64(&mismatches, 1)
			}
		}(req, wantOrg, wantRepo, wantRef)
	})

	const workers, perWorker = 64, 2000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				path := fmt.Sprintf("/c/org%d/repo%d/ref%d", g, i, g+i)
				req := httptest.NewRequest(http.MethodGet, path, nil)
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(g)
	}
	wg.Wait()
	time.Sleep(30 * time.Millisecond)

	if m := atomic.LoadInt64(&mismatches); m > 0 {
		t.Fatalf("3-param tiered: %d param mismatches", m)
	}
	t.Logf("3-param tiered goroutine-spawn: mismatches=0 (PASS)")
}

// ---------------------------------------------------------------------------
// Invariant 2: original *http.Request is never mutated.
//
// We read r.Context() BEFORE and AFTER ServeHTTP returns. The original r's
// context must be unchanged: it must NOT be the requestCtx1/2/requestCtx —
// those belong to the bundle, not the original.
// ---------------------------------------------------------------------------

func TestTiered_OriginalRequestUnmutated(t *testing.T) {
	r := mm.New()

	for _, path := range []string{"/d/:id", "/e/:a/:b", "/f/:x/:y/:z"} {
		p := path
		r.GET(p, func(w http.ResponseWriter, req *http.Request) {
			// Handler receives the bundle's req, not the original.
			// This test verifies from the caller's side.
		})
	}

	origCtxKey := struct{ name string }{"orig"}

	const workers, perWorker = 32, 1000
	var mutated int64

	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				// Tag the original request context with a recognisable value.
				origCtx := context.WithValue(context.Background(), origCtxKey, fmt.Sprintf("orig-%d-%d", g, i))

				// 1-param
				req1 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/d/v%d", i), nil).WithContext(origCtx)
				origCtx1 := req1.Context()
				r.ServeHTTP(httptest.NewRecorder(), req1)
				if req1.Context() != origCtx1 {
					atomic.AddInt64(&mutated, 1)
				}

				// 2-param
				req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/e/a%d/b%d", g, i), nil).WithContext(origCtx)
				origCtx2 := req2.Context()
				r.ServeHTTP(httptest.NewRecorder(), req2)
				if req2.Context() != origCtx2 {
					atomic.AddInt64(&mutated, 1)
				}

				// 3-param
				req3 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/f/x%d/y%d/z%d", g, i, g+i), nil).WithContext(origCtx)
				origCtx3 := req3.Context()
				r.ServeHTTP(httptest.NewRecorder(), req3)
				if req3.Context() != origCtx3 {
					atomic.AddInt64(&mutated, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if m := atomic.LoadInt64(&mutated); m > 0 {
		t.Fatalf("original r.Context() was mutated in %d cases — setReqCtxUnsafe wrote to original r", m)
	}
	t.Logf("original request unmutated: PASS")
}

// ---------------------------------------------------------------------------
// Invariant 4: routeCtxParams/routeCtxPattern type switch under concurrency.
//
// All three bundle types hit the switch simultaneously. Any missed type arm
// would silently return nil/empty. We verify correctness and absence of races.
// ---------------------------------------------------------------------------

func TestTiered_TypeSwitch_AllThreeTiers_Concurrent(t *testing.T) {
	r := mm.New()
	var bad int64

	r.GET("/ts1/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 || ps[0].Key != "id" {
			atomic.AddInt64(&bad, 1)
		}
		if mm.RoutePattern(req) != "/ts1/:id" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/ts2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 || ps[0].Key != "a" || ps[1].Key != "b" {
			atomic.AddInt64(&bad, 1)
		}
		if mm.RoutePattern(req) != "/ts2/:a/:b" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/ts3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 || ps[0].Key != "x" || ps[1].Key != "y" || ps[2].Key != "z" {
			atomic.AddInt64(&bad, 1)
		}
		if mm.RoutePattern(req) != "/ts3/:x/:y/:z" {
			atomic.AddInt64(&bad, 1)
		}
	})

	const workers, perWorker = 64, 5000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				switch i % 3 {
				case 0:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ts1/v%d", i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 1:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ts2/a%d/b%d", g, i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 2:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ts3/x%d/y%d/z%d", g, i, g+i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				}
			}
		}(g)
	}
	wg.Wait()

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("type-switch concurrent: %d wrong Params or RoutePattern reads across all three tiers", b)
	}
	t.Logf("type-switch concurrent: PASS")
}

// ---------------------------------------------------------------------------
// Invariant 5: cross-tier contamination.
//
// 1-param and 2-param requests interleaved at high concurrency. Neither tier
// must see the other tier's params. The key test: a 1-param handler must never
// see 2 params, and a 2-param handler must never see 1 param.
// ---------------------------------------------------------------------------

func TestTiered_CrossTier_NoContamination(t *testing.T) {
	r := mm.New()
	var bad int64

	r.GET("/ct1/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/ct2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/ct3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			atomic.AddInt64(&bad, 1)
		}
	})
	// Static route: must have 0 params.
	r.GET("/ct0/static", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 0 {
			atomic.AddInt64(&bad, 1)
		}
	})

	const workers, perWorker = 64, 5000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				switch i % 4 {
				case 0:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ct1/id%d", i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 1:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ct2/a%d/b%d", g, i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 2:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ct3/x%d/y%d/z%d", g, i, g+i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 3:
					req := httptest.NewRequest(http.MethodGet, "/ct0/static", nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				}
			}
		}(g)
	}
	wg.Wait()

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("cross-tier contamination: %d wrong param counts", b)
	}
	t.Logf("cross-tier contamination: PASS")
}

// ---------------------------------------------------------------------------
// Invariant 6: PanicHandler + tiered dispatch — all three tiers must leave
// the router in clean state after a handler panic.
// ---------------------------------------------------------------------------

func TestTiered_PanicHandler_AllTiers_CleanState(t *testing.T) {
	r := mm.New()
	var recovered int64
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		atomic.AddInt64(&recovered, 1)
	}

	var bad int64

	// Panic routes for all three tiers.
	r.GET("/panic1/:id", func(w http.ResponseWriter, req *http.Request) {
		panic("boom1")
	})
	r.GET("/panic2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		panic("boom2")
	})
	r.GET("/panic3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		panic("boom3")
	})

	// Check routes — params must be correct after panic.
	r.GET("/check1/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 || ps[0].Key != "id" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/check2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 || ps[0].Key != "a" || ps[1].Key != "b" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/check3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			atomic.AddInt64(&bad, 1)
		}
	})

	const iters = 10000
	for i := range iters {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/panic1/p%d", i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/check1/c%d", i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/panic2/a%d/b%d", i, i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/check2/a%d/b%d", i, i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/panic3/x%d/y%d/z%d", i, i, i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/check3/x%d/y%d/z%d", i, i, i), nil))
	}

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("panic+check: %d bad param reads after panic across all tiers", b)
	}
	// 3 panic routes × iters = 3*iters expected recoveries.
	if rec := atomic.LoadInt64(&recovered); rec != int64(3*iters) {
		t.Fatalf("expected %d recoveries, got %d", 3*iters, rec)
	}
	t.Logf("panic+handler all tiers: recovered=%d bad=%d PASS", recovered, bad)
}

// ---------------------------------------------------------------------------
// Invariant 6 (concurrent variant): panic + check under concurrency.
// ---------------------------------------------------------------------------

func TestTiered_PanicConcurrent_AllTiers(t *testing.T) {
	r := mm.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {}
	var bad int64

	r.GET("/pp1/:id", func(w http.ResponseWriter, req *http.Request) { panic("1") })
	r.GET("/pp2/:a/:b", func(w http.ResponseWriter, req *http.Request) { panic("2") })
	r.GET("/pp3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) { panic("3") })
	r.GET("/pc1/:id", func(w http.ResponseWriter, req *http.Request) {
		if mm.PathParam(req, "id") == "" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/pc2/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		if mm.PathParam(req, "a") == "" || mm.PathParam(req, "b") == "" {
			atomic.AddInt64(&bad, 1)
		}
	})
	r.GET("/pc3/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			atomic.AddInt64(&bad, 1)
		}
	})

	const workers, perWorker = 32, 500
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pp1/w%d-i%d", g, i), nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pc1/w%d-i%d", g, i), nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pp2/a%d/b%d", g, i), nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pc2/a%d/b%d", g, i), nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pp3/x%d/y%d/z%d", g, i, g+i), nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/pc3/x%d/y%d/z%d", g, i, g+i), nil))
			}
		}(g)
	}
	wg.Wait()

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("panic concurrent all tiers: %d bad param reads", b)
	}
	t.Logf("panic concurrent all tiers: PASS")
}

// ---------------------------------------------------------------------------
// Massive parallel stress test — exercises all three tiers simultaneously
// under the highest concurrency load (128 goroutines × 50 000 iterations).
// The race detector will catch any torn read/write on bundle fields.
// ---------------------------------------------------------------------------

func TestTiered_MassiveParallel_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short")
	}
	r := mm.New()

	// Register a spread of 1-, 2-, and 3-param routes.
	for i := range 50 {
		i := i
		r.GET(fmt.Sprintf("/mp1/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			_ = mm.PathParam(req, "id")
		})
		r.GET(fmt.Sprintf("/mp2/%d/:a/:b", i), func(w http.ResponseWriter, req *http.Request) {
			_ = mm.PathParam(req, "a")
			_ = mm.PathParam(req, "b")
		})
		r.GET(fmt.Sprintf("/mp3/%d/:x/:y/:z", i), func(w http.ResponseWriter, req *http.Request) {
			_ = mm.PathParam(req, "x")
			_ = mm.PathParam(req, "y")
			_ = mm.PathParam(req, "z")
		})
	}

	const workers, perWorker = 128, 50000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				slot := (g + i) % 50
				switch i % 3 {
				case 0:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mp1/%d/%d", slot, i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 1:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mp2/%d/%d/%d", slot, g, i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				case 2:
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mp3/%d/%d/%d/%d", slot, g, i, g+i), nil)
					r.ServeHTTP(httptest.NewRecorder(), req)
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("MassiveParallel all tiers: PASS")
}

// ---------------------------------------------------------------------------
// Context cancellation: parent context cancellation must propagate through
// all three bundle types.
// ---------------------------------------------------------------------------

func TestTiered_ContextCancellation_AllTiers(t *testing.T) {
	r := mm.New()
	type routeSpec struct {
		path    string
		paramN  int
		pattern string
	}
	routes := []routeSpec{
		{"/canc1/:id", 1, "/canc1/:id"},
		{"/canc2/:a/:b", 2, "/canc2/:a/:b"},
		{"/canc3/:x/:y/:z", 3, "/canc3/:x/:y/:z"},
	}

	var bad int64
	for _, spec := range routes {
		spec := spec
		r.GET(spec.path, func(w http.ResponseWriter, req *http.Request) {
			select {
			case <-req.Context().Done():
				// correct — cancellation propagated
			case <-time.After(2 * time.Second):
				atomic.AddInt64(&bad, 1)
			}
		})
	}

	for _, spec := range routes {
		spec := spec
		t.Run(spec.pattern, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var path string
			switch spec.paramN {
			case 1:
				path = spec.path[:len(spec.path)-3] + "/v1"
			case 2:
				path = spec.path[:len(spec.path)-6] + "/v1/v2"
			case 3:
				path = spec.path[:len(spec.path)-9] + "/v1/v2/v3"
			}
			req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			done := make(chan struct{})
			go func() {
				r.ServeHTTP(httptest.NewRecorder(), req)
				close(done)
			}()
			time.Sleep(5 * time.Millisecond)
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatalf("handler %s did not return after context cancel", spec.pattern)
			}
		})
	}

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("context cancellation: %d handlers timed out — cancellation not propagated through bundle", b)
	}
	t.Logf("context cancellation all tiers: PASS")
}

// ---------------------------------------------------------------------------
// RoutePattern correctness: all three tiers must store and return the correct
// pattern string, verified concurrently.
// ---------------------------------------------------------------------------

func TestTiered_RoutePattern_AllTiers_Concurrent(t *testing.T) {
	r := mm.New()
	var bad int64

	patterns := map[string]string{
		"/rp1/:id":      "1",
		"/rp2/:a/:b":    "2",
		"/rp3/:x/:y/:z": "3",
	}
	for pat := range patterns {
		pat := pat
		r.GET(pat, func(w http.ResponseWriter, req *http.Request) {
			got := mm.RoutePattern(req)
			if got != pat {
				atomic.AddInt64(&bad, 1)
			}
		})
	}

	const workers, perWorker = 32, 5000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				switch i % 3 {
				case 0:
					r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/rp1/v%d", i), nil))
				case 1:
					r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/rp2/a%d/b%d", g, i), nil))
				case 2:
					r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/rp3/x%d/y%d/z%d", g, i, g+i), nil))
				}
			}
		}(g)
	}
	wg.Wait()

	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("RoutePattern: %d wrong pattern reads across tiers", b)
	}
	t.Logf("RoutePattern all tiers concurrent: PASS")
}
