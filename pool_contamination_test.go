package muxmaster_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// pool_contamination_test.go — H-RECON-01 (reports/overview/findings.md §6).
//
// CSA-2026-0056 (reports/concurrency-security-auditor/2026-09-25-CSA-2026-0056-
// pool-gc-canary.md) verified zero cross-request parameter contamination for
// the DEFAULT (GC-managed, non-pooled) reqBundle architecture. That verdict
// never covered the two opt-in pooling switches added since:
//
//   - Mux.PoolRequestBundle (Opt O13): recycles the fused reqBundle
//     (requestCtx + *http.Request copy) for Handle (stdlib http.Handler)
//     routes with path parameters via a tiered sync.Pool.
//   - Mux.PoolFastParams (Opt O9): recycles the Params slice handed to
//     FastHandler routes via a tiered sync.Pool.
//
// Both flags carry a strict "MUST NOT retain past return" lifetime contract
// (specification/configuration.md §4.5-4.6). This file exercises both flags,
// separately and together, under massive concurrency to confirm that
// CORRECT usage (reading params only within the handler, never retaining
// them) never observes cross-request contamination, that a panicking
// handler never returns a dirty object to the pool, and that the documented
// retention hazard is real and observable when the contract is violated on
// purpose (as a deterministic demonstration of the documented behaviour,
// not a bug).

// poolTestTokenKey is the context key used by poolTestTokenMiddleware to
// smuggle a per-request unique token through Pre-middleware context
// wrapping, so tests can verify that PoolRequestBundle's re-derivation of
// bundle.ctx.Context from the CURRENT request on every dispatch (not a
// stale pooled value) actually holds under concurrent reuse.
type poolTestTokenKey struct{}

// poolTestTokenMiddleware copies the X-Test-Token request header into the
// request context under poolTestTokenKey. Registered via Mux.Pre, which
// wraps ServeHTTP outside the dispatch/pooling boundary (CDX-S8-003) and
// therefore uniformly covers both Handle and HandleFast routes.
func poolTestTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := r.Header.Get("X-Test-Token"); tok != "" {
			r = r.WithContext(context.WithValue(r.Context(), poolTestTokenKey{}, tok))
		}
		next.ServeHTTP(w, r)
	})
}

// poolToken builds a per-request token that is unique across every
// (goroutine, iteration) pair in a stress loop. Because every check below
// re-derives the EXPECTED param/context values purely from this token, any
// cross-request contamination (a value belonging to a different token)
// produces a deterministic mismatch instead of relying on a shared map.
func poolToken(g, i int) string {
	return fmt.Sprintf("g%d-i%d", g, i)
}

// --- Handle / PoolRequestBundle tiers -------------------------------------

const (
	patH1      = "/h1/:id"
	patH2      = "/h2/:a/:b"
	patH3      = "/h3/:a/:b/:c"
	patH5      = "/h5/:a/:b/:c/:d/:e" // overflow tier: maxParams inline is 3
	patHStatic = "/hstatic/*filepath" // catch-all: 1-param tier
)

// checkTokenContext verifies the context token planted by
// poolTestTokenMiddleware belongs to THIS request (non-empty, matches tok).
func checkTokenContext(r *http.Request, tok string, violations *int64) {
	got, _ := r.Context().Value(poolTestTokenKey{}).(string)
	if got == "" || got != tok {
		atomic.AddInt64(violations, 1)
	}
}

// registerPoolHandleTiers registers the five Handle tiers (1/2/3/overflow/
// catch-all params) used by the PoolRequestBundle stress tests. Each
// handler verifies, from context alone, that its params and context token
// belong to the SAME request — never a request-response pair a wildly
// different request.
func registerPoolHandleTiers(m *muxmaster.Mux, violations *int64) {
	m.GET(patH1, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 1 || ps.Get("id") != tok {
			atomic.AddInt64(violations, 1)
		}
		if muxmaster.RoutePattern(r) != patH1 {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GET(patH2, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 2 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" {
			atomic.AddInt64(violations, 1)
		}
		if muxmaster.RoutePattern(r) != patH2 {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GET(patH3, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 3 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" {
			atomic.AddInt64(violations, 1)
		}
		if muxmaster.RoutePattern(r) != patH3 {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GET(patH5, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 5 ||
			ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" ||
			ps.Get("d") != tok+"D" || ps.Get("e") != tok+"E" {
			atomic.AddInt64(violations, 1)
		}
		if muxmaster.RoutePattern(r) != patH5 {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GET(patHStatic, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		// Catch-all captures include the leading '/' of the matched suffix.
		if len(ps) != 1 || ps.Get("filepath") != "/"+tok {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
}

// buildHandleRequest constructs the tier-th (0..4) request for token tok,
// carrying the token in both the path parameters and the X-Test-Token
// header consumed by poolTestTokenMiddleware.
func buildHandleRequest(tier int, tok string) *http.Request {
	var path string
	switch tier {
	case 0:
		path = "/h1/" + tok
	case 1:
		path = "/h2/" + tok + "A/" + tok + "B"
	case 2:
		path = "/h3/" + tok + "A/" + tok + "B/" + tok + "C"
	case 3:
		path = "/h5/" + tok + "A/" + tok + "B/" + tok + "C/" + tok + "D/" + tok + "E"
	default:
		path = "/hstatic/" + tok
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-Token", tok)
	return req
}

// --- FastHandler / PoolFastParams tiers -----------------------------------

const (
	patF1      = "/f1/:id"
	patF2      = "/f2/:a/:b"
	patF3      = "/f3/:a/:b/:c"
	patF5      = "/f5/:a/:b/:c/:d/:e" // overflow tier: never pooled (spec §4.5 item 32), tested for correctness
	patFStatic = "/fstatic/*filepath"
)

// registerPoolFastTiers mirrors registerPoolHandleTiers for FastHandler
// routes. FastHandler dispatch never wraps the request context
// (performance.md §6), so RoutePattern(r) is not meaningful here and is not
// checked; the context token check still holds because Pre middleware
// wraps r before dispatch regardless of Handle vs HandleFast.
func registerPoolFastTiers(m *muxmaster.Mux, violations *int64) {
	m.GETFast(patF1, func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		if len(ps) != 1 || ps.Get("id") != tok {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast(patF2, func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		if len(ps) != 2 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast(patF3, func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		if len(ps) != 3 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast(patF5, func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		if len(ps) != 5 ||
			ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" ||
			ps.Get("d") != tok+"D" || ps.Get("e") != tok+"E" {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast(patFStatic, func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, violations)
		// Catch-all captures include the leading '/' of the matched suffix.
		if len(ps) != 1 || ps.Get("filepath") != "/"+tok {
			atomic.AddInt64(violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func buildFastRequest(tier int, tok string) *http.Request {
	var path string
	switch tier {
	case 0:
		path = "/f1/" + tok
	case 1:
		path = "/f2/" + tok + "A/" + tok + "B"
	case 2:
		path = "/f3/" + tok + "A/" + tok + "B/" + tok + "C"
	case 3:
		path = "/f5/" + tok + "A/" + tok + "B/" + tok + "C/" + tok + "D/" + tok + "E"
	default:
		path = "/fstatic/" + tok
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-Token", tok)
	return req
}

// stressWorkers returns a goroutine count for stress loops: at least 64 (as
// specified by the audit method), scaled with GOMAXPROCS on larger
// machines, and the iteration count per goroutine. GOMAXPROCS is logged so
// runs are reproducible.
func stressWorkers(t *testing.T) (goroutines, itersPerGoroutine int) {
	t.Helper()
	procs := runtime.GOMAXPROCS(0)
	t.Logf("GOMAXPROCS=%d", procs)
	goroutines = max(64, procs*4)
	itersPerGoroutine = 3000
	return
}

// TestPoolRequestBundle_ConcurrentStress_AllTiers is the primary canary for
// Mux.PoolRequestBundle: many goroutines hammer all five param tiers
// (1/2/3/overflow/catch-all) concurrently, each request reading its own
// params and context token immediately (correct usage — never retained
// past return). Zero violations is required.
func TestPoolRequestBundle_ConcurrentStress_AllTiers(t *testing.T) {
	m := muxmaster.New()
	m.PoolRequestBundle = true
	m.Pre(poolTestTokenMiddleware)

	var violations int64
	registerPoolHandleTiers(m, &violations)

	goroutines, iters := stressWorkers(t)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tok := poolToken(g, i)
				tier := i % 5
				req := buildHandleRequest(tier, tok)
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&violations, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * iters
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("PoolRequestBundle contamination: %d violations out of %d requests", v, total)
	}
	t.Logf("PoolRequestBundle: %d requests across %d goroutines, 0 violations", total, goroutines)
}

// TestPoolFastParams_ConcurrentStress_AllTiers mirrors the above for
// Mux.PoolFastParams and FastHandler routes.
func TestPoolFastParams_ConcurrentStress_AllTiers(t *testing.T) {
	m := muxmaster.New()
	m.PoolFastParams = true
	m.Pre(poolTestTokenMiddleware)

	var violations int64
	registerPoolFastTiers(m, &violations)

	goroutines, iters := stressWorkers(t)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tok := poolToken(g, i)
				tier := i % 5
				req := buildFastRequest(tier, tok)
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&violations, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * iters
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("PoolFastParams contamination: %d violations out of %d requests", v, total)
	}
	t.Logf("PoolFastParams: %d requests across %d goroutines, 0 violations", total, goroutines)
}

// TestPoolRequestBundleAndPoolFastParams_Combined_ConcurrentStress enables
// both opt-in pooling flags on the SAME Mux and interleaves Handle and
// FastHandler traffic unpredictably across goroutines, verifying the two
// independent pool families (reqBundle*Pool vs fastParams*Pool) never cross
// contaminate each other or leak between request types.
func TestPoolRequestBundleAndPoolFastParams_Combined_ConcurrentStress(t *testing.T) {
	m := muxmaster.New()
	m.PoolRequestBundle = true
	m.PoolFastParams = true
	m.Pre(poolTestTokenMiddleware)

	var violations int64
	registerPoolHandleTiers(m, &violations)
	registerPoolFastTiers(m, &violations)

	goroutines, iters := stressWorkers(t)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tok := poolToken(g, i)
				tier := i % 5
				var req *http.Request
				if (g+i)%2 == 0 {
					req = buildHandleRequest(tier, tok)
				} else {
					req = buildFastRequest(tier, tok)
				}
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&violations, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * iters
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("combined PoolRequestBundle+PoolFastParams contamination: %d violations out of %d requests", v, total)
	}
	t.Logf("combined pooling: %d requests across %d goroutines, 0 violations", total, goroutines)
}

// --- Panic path ------------------------------------------------------------

// TestPoolRequestBundle_PanicPath_PoolStaysClean verifies that a handler
// panicking mid-request under PoolRequestBundle never returns a dirty
// bundle to the pool: interleaved "poison" requests that always panic
// (across the 1-param and 3-param tiers, i.e. dispatchParams1Pooled and the
// dispatchParamsNPooled 3+ path) must never corrupt what a concurrently
// running "check" request observes.
func TestPoolRequestBundle_PanicPath_PoolStaysClean(t *testing.T) {
	m := muxmaster.New()
	m.PoolRequestBundle = true
	m.Pre(poolTestTokenMiddleware)

	var panicHandlerCalls int64
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		atomic.AddInt64(&panicHandlerCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}

	m.GET("/p1/:id", func(w http.ResponseWriter, r *http.Request) {
		panic("boom-1")
	})
	m.GET("/p3/:a/:b/:c", func(w http.ResponseWriter, r *http.Request) {
		panic("boom-3")
	})

	var violations int64
	m.GET("/c1/:id", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, &violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 1 || ps.Get("id") != tok {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GET("/c3/:a/:b/:c", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, &violations)
		ps := muxmaster.ParamsFromContext(r.Context())
		if len(ps) != 3 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	goroutines, iters := stressWorkers(t)
	iters /= 3 // panic-path requests are heavier (defer/recover on every call)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tok := poolToken(g, i)

				pr1 := httptest.NewRequest(http.MethodGet, "/p1/"+tok, nil)
				pr1.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), pr1)

				pr3 := httptest.NewRequest(http.MethodGet, "/p3/"+tok+"X/"+tok+"Y/"+tok+"Z", nil)
				pr3.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), pr3)

				cr1 := httptest.NewRequest(http.MethodGet, "/c1/"+tok, nil)
				cr1.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), cr1)

				cr3 := httptest.NewRequest(http.MethodGet, "/c3/"+tok+"A/"+tok+"B/"+tok+"C", nil)
				cr3.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), cr3)
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * iters
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("PoolRequestBundle panic-path contamination: %d violations out of %d check requests", v, total*2)
	}
	if want := int64(total * 2); atomic.LoadInt64(&panicHandlerCalls) != want {
		t.Fatalf("expected %d PanicHandler invocations, got %d", want, panicHandlerCalls)
	}
	t.Logf("PoolRequestBundle panic path: %d panic+check cycles, 0 violations", total)
}

// TestPoolFastParams_PanicPath_PoolStaysClean mirrors the above for
// FastHandler routes under PoolFastParams.
func TestPoolFastParams_PanicPath_PoolStaysClean(t *testing.T) {
	m := muxmaster.New()
	m.PoolFastParams = true
	m.Pre(poolTestTokenMiddleware)

	var panicHandlerCalls int64
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		atomic.AddInt64(&panicHandlerCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}

	m.GETFast("/p1/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		panic("boom-1-fast")
	})
	m.GETFast("/p3/:a/:b/:c", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		panic("boom-3-fast")
	})

	var violations int64
	m.GETFast("/c1/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, &violations)
		if len(ps) != 1 || ps.Get("id") != tok {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast("/c3/:a/:b/:c", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		tok, _ := r.Context().Value(poolTestTokenKey{}).(string)
		checkTokenContext(r, tok, &violations)
		if len(ps) != 3 || ps.Get("a") != tok+"A" || ps.Get("b") != tok+"B" || ps.Get("c") != tok+"C" {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	goroutines, iters := stressWorkers(t)
	iters /= 3
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tok := poolToken(g, i)

				pr1 := httptest.NewRequest(http.MethodGet, "/p1/"+tok, nil)
				pr1.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), pr1)

				pr3 := httptest.NewRequest(http.MethodGet, "/p3/"+tok+"X/"+tok+"Y/"+tok+"Z", nil)
				pr3.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), pr3)

				cr1 := httptest.NewRequest(http.MethodGet, "/c1/"+tok, nil)
				cr1.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), cr1)

				cr3 := httptest.NewRequest(http.MethodGet, "/c3/"+tok+"A/"+tok+"B/"+tok+"C", nil)
				cr3.Header.Set("X-Test-Token", tok)
				m.ServeHTTP(httptest.NewRecorder(), cr3)
			}
		}(g)
	}
	wg.Wait()

	total := goroutines * iters
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("PoolFastParams panic-path contamination: %d violations out of %d check requests", v, total*2)
	}
	if want := int64(total * 2); atomic.LoadInt64(&panicHandlerCalls) != want {
		t.Fatalf("expected %d PanicHandler invocations, got %d", want, panicHandlerCalls)
	}
	t.Logf("PoolFastParams panic path: %d panic+check cycles, 0 violations", total)
}

// --- Documented retention hazard (not a defect — a specification demo) ----

// hazardRetryAttempts bounds the retry loop used by the two
// RetentionHazard_Documented tests below. Reuse of the exact same pooled
// object across two back-to-back Get/Put cycles on one goroutine is the
// pool's fast, allocation-free path (the same one every "0 allocs/op"
// pooled benchmark relies on) and is reliable with GC disabled — but it is
// not a language guarantee, and under `-race` the runtime's heavier
// per-goroutine bookkeeping occasionally preempts between the two calls or
// (rarely) still triggers a pool-clearing GC despite SetGCPercent(-1) (a
// manually or memory-limit-triggered collection is still possible). A
// bounded retry keeps the test honest — it fails if the hazard is NEVER
// observed in this many attempts — while absorbing that scheduling jitter
// instead of being flaky in either direction.
const hazardRetryAttempts = 200

// TestPoolRequestBundle_RetentionHazard_Documented demonstrates the exact
// hazard documented in specification/configuration.md §4.6 item 35: a
// handler that retains the *http.Request past its return, under
// PoolRequestBundle, observes a LATER, unrelated request's data once the
// pooled bundle is recycled — a use-after-free against the pooled storage,
// not a supported pattern.
//
// The retained reference is read from INSIDE the second handler, on the
// SAME goroutine that drives both requests sequentially — there is no
// cross-goroutine access and therefore nothing for the race detector to
// flag; the hazard is purely logical (stale-looking data), which is
// exactly the failure mode a handler violating the contract would hit
// synchronously if it read `r` again later in its own control flow (e.g.
// from a deferred cleanup), or asynchronously if it handed `r` to a
// goroutine (not exercised here to keep this a deterministic, race-clean
// test).
//
// GC is disabled for the critical section so sync.Pool's GC-triggered
// poolCleanup cannot evict the bundle between the two calls; see
// hazardRetryAttempts for why a bounded retry still wraps this.
func TestPoolRequestBundle_RetentionHazard_Documented(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	m := muxmaster.New()
	m.PoolRequestBundle = true

	var retained *http.Request // captured past return — the documented violation
	var sanityID string        // read WITHIN handler 1, before any pooling recycling
	var observedDuringNext string

	m.GET("/hazard1/:id", func(w http.ResponseWriter, r *http.Request) {
		// Correct usage: read within the handler, before returning.
		sanityID = muxmaster.PathParam(r, "id")
		retained = r // VIOLATION: retaining *http.Request past the handler's return
		w.WriteHeader(http.StatusOK)
	})
	m.GET("/hazard2/:id", func(w http.ResponseWriter, r *http.Request) {
		// `retained` was captured by the UNRELATED /hazard1 request above. By
		// the time this (second, same-tier) request's handler runs, dispatch
		// has already overwritten the recycled bundle's fields with THIS
		// request's data but has not yet zeroed/returned it to the pool
		// (that happens only after this handler returns) — so reading
		// `retained` right now observes request 2's data through a reference
		// that, per the documented contract, should never have been kept.
		observedDuringNext = muxmaster.PathParam(retained, "id")
		w.WriteHeader(http.StatusOK)
	})

	for attempt := 0; attempt < hazardRetryAttempts; attempt++ {
		first := fmt.Sprintf("first%d", attempt)
		second := fmt.Sprintf("second%d", attempt)

		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hazard1/"+first, nil))
		if sanityID != first {
			t.Fatalf("sanity check failed: want %q, got %q", first, sanityID)
		}
		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hazard2/"+second, nil))

		if observedDuringNext == second {
			return // hazard observed — recycled storage leaked request 2's data
		}
	}

	t.Fatalf("expected the reference retained past request 1's return to observe a "+
		"later request's recycled data per configuration.md §4.6 item 35, but it was never "+
		"observed in %d attempts — if this fails consistently, sync.Pool's reuse pattern "+
		"changed and this hazard demonstration needs updating, NOT that the hazard stopped "+
		"existing", hazardRetryAttempts)
}

// TestPoolFastParams_RetentionHazard_Documented mirrors the above for
// FastHandler routes under PoolFastParams (configuration.md §4.5 item 31).
func TestPoolFastParams_RetentionHazard_Documented(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	m := muxmaster.New()
	m.PoolFastParams = true

	var retained muxmaster.Params // captured past return — the documented violation
	var sanityID string           // read WITHIN handler 1, before any pooling recycling
	var observedDuringNext string

	m.GETFast("/hazardfast1/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		// Correct usage: read within the handler, before returning.
		sanityID = ps.Get("id")
		retained = ps // VIOLATION: retaining the Params slice past the handler's return
		w.WriteHeader(http.StatusOK)
	})
	m.GETFast("/hazardfast2/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		observedDuringNext = retained.Get("id")
		w.WriteHeader(http.StatusOK)
	})

	for attempt := 0; attempt < hazardRetryAttempts; attempt++ {
		first := fmt.Sprintf("first%d", attempt)
		second := fmt.Sprintf("second%d", attempt)

		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hazardfast1/"+first, nil))
		if sanityID != first {
			t.Fatalf("sanity check failed: want %q, got %q", first, sanityID)
		}
		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hazardfast2/"+second, nil))

		if observedDuringNext == second {
			return // hazard observed — recycled storage leaked request 2's data
		}
	}

	t.Fatalf("expected the Params slice retained past request 1's return to observe a "+
		"later request's recycled data per configuration.md §4.5 item 31, but it was never "+
		"observed in %d attempts — if this fails consistently, sync.Pool's reuse pattern "+
		"changed and this hazard demonstration needs updating, NOT that the hazard stopped "+
		"existing", hazardRetryAttempts)
}
