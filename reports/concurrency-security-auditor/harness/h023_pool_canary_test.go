package harness

// H-023 + general sync.Pool canary test (auditor prompt Step 2).
//
// Two distinct leak shapes are covered:
//
//   1. Cross-request CANARY: a handler for /plant/:id injects a sentinel Param
//      into the params slice observable by the next request. Because Params
//      escape via context in handlers, any aliasing would let the next request
//      at /check/:id observe the canary. MuxMaster's hot path writes
//      `rc.params = rc.small[:copy(rc.small[:], pslice)]` which re-slices the
//      fixed-size array — we assert no residue leaks across requests.
//
//   2. rc.small RESIDUE: after releaseRC, rc.small may still contain the
//      previous request's Param values (Key + Value strings hold references to
//      the previous URL). The current handler path re-slices via `copy`
//      before exposing to the context, so the handler cannot observe residue
//      through PathParam. We verify this invariant under heavy concurrency.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestH023_PoolCanaryNoCrossRequestLeak is the canary test mandated by the
// auditor prompt. It runs 100k+ iterations concurrently, expecting zero
// canaries to surface in requests that never had one planted.
func TestH023_PoolCanaryNoCrossRequestLeak(t *testing.T) {
	r := mm.New()

	var leaks int64

	// Route A: injects sentinel by mutating the Params slice it obtains.
	// In MuxMaster's design, mutating params is IMMEDIATELY visible via
	// rc.small because rc.params aliases rc.small[:n]. Any persistence of
	// that mutation to a subsequent request is pool contamination.
	r.GET("/plant/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		for i := range ps {
			// Overwrite the Key to a canary sentinel.
			if ps[i].Key == "id" {
				ps[i].Key = "__CANARY__"
			}
		}
	})

	// Route B: scans for the canary; if the pool carries residue, the check
	// handler would observe "__CANARY__" on a request that never planted.
	r.GET("/check/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		for i := range ps {
			if ps[i].Key == "__CANARY__" {
				atomic.AddInt64(&leaks, 1)
			}
		}
	})

	const (
		workers   = 64
		perWorker = 2000 // 64 * 2000 * 2 paths = 256k requests
	)
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				// Alternate plant and check.
				p := fmt.Sprintf("/plant/v-%d-%d", g, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
				c := fmt.Sprintf("/check/v-%d-%d", g, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c, nil))
			}
		}(g)
	}
	wg.Wait()

	if n := atomic.LoadInt64(&leaks); n > 0 {
		t.Fatalf("H-023: pool contamination — %d canary leaks observed", n)
	}
	t.Logf("H-023: zero canary leaks after %d iterations", workers*perWorker*2)
}

// TestH023_RcSmallResidueInvariant validates the invariant that
// rc.params=rc.small[:n] with n==1 never exposes rc.small[1] / rc.small[2].
//
// Concretely: route /one/:a has 1 param, route /three/:x/:y/:z has 3 params.
// We alternate them on the same goroutines so the pool may return the same rc.
// /one must always see len(Params)==1 and rc.small[1..2] must not be reachable
// via the public API.
func TestH023_RcSmallResidueInvariant(t *testing.T) {
	r := mm.New()

	var badLen int64
	var badVal int64

	r.GET("/one/:a", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 {
			atomic.AddInt64(&badLen, 1)
			return
		}
		if ps[0].Key != "a" {
			atomic.AddInt64(&badVal, 1)
		}
	})
	r.GET("/three/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 3 {
			atomic.AddInt64(&badLen, 1)
		}
	})

	const workers, perWorker = 32, 1000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				a := fmt.Sprintf("/one/a%d-%d", g, i)
				b := fmt.Sprintf("/three/x%d/y%d/z%d", g, i, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, a, nil))
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, b, nil))
			}
		}(g)
	}
	wg.Wait()

	if bl := atomic.LoadInt64(&badLen); bl > 0 {
		t.Fatalf("H-023: rc.small residue — %d requests saw wrong len(Params)", bl)
	}
	if bv := atomic.LoadInt64(&badVal); bv > 0 {
		t.Fatalf("H-023: rc.small residue — %d requests saw wrong Param.Key", bv)
	}
}
