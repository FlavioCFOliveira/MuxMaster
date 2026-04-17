// Package harness exercises the concurrency surface of MuxMaster to validate
// that no data races, pool contamination or goroutine leaks exist under the
// harshest conditions the runtime permits.
//
// This file targets hypotheses H-001 / H-024 in hypotheses.md:
//
//	Cross-goroutine race on r.ctx via unsafe.Add.
//
// Root cause under investigation:
//
//	mux.go:464-480 and 521-537 mutate the unexported ctx field of
//	*http.Request via `unsafe.Add(unsafe.Pointer(r), reqCtxOffset)` and
//	restore the original pointer before returning. A handler that spawns
//	a child goroutine and retains `r` can observe the pointer being
//	rewritten on the parent (dispatcher) goroutine -> data race and
//	possible cross-request leak because rc is returned to rcPool and
//	may be reused by a concurrent request.
//
// Run with:
//
//	go test -race -run=TestH001 -count=10 ./reports/concurrency-security-auditor/harness/...
package harness

import (
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

// TestH001_HandlerGoroutineReadsRequestContext launches a handler that spawns
// a background goroutine which reads r.Context() AFTER the handler returned.
// The dispatcher goroutine has by then restored origCtx and released rc to the
// pool. Any concurrent /a/:id request may acquire the same rc and reuse it.
//
// Expected outcome:
//   - With -race: a DATA RACE on the unsafe write/read at reqCtxOffset.
//   - Observational: non-nil cross-request value or stale param value.
func TestH001_HandlerGoroutineReadsRequestContext(t *testing.T) {
	r := mm.New()

	var (
		observedEmpty  int64
		observedMismatch int64
		observed       sync.Map
	)

	r.GET("/a/:id", func(w http.ResponseWriter, req *http.Request) {
		wantID := req.URL.Path[len("/a/"):]
		go func(req *http.Request, want string) {
			// Deliberately read the request's context AFTER the dispatcher
			// has likely restored origCtx and released the rc struct.
			// This models `go logAsync(r.Context(), ...)` in handlers.
			runtime.Gosched()
			time.Sleep(50 * time.Microsecond)
			got := mm.PathParam(req, "id")
			if got == "" {
				atomic.AddInt64(&observedEmpty, 1)
			} else if got != want {
				atomic.AddInt64(&observedMismatch, 1)
				observed.Store(got+"::"+want, struct{}{})
			}
		}(req, wantID)
	})

	const (
		workers = 64
		perWorker = 500
	)

	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				id := fmt.Sprintf("w%d-i%d", g, i)
				req := httptest.NewRequest(http.MethodGet, "/a/"+id, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}
	wg.Wait()

	// Let background goroutines finish reading stale contexts.
	time.Sleep(50 * time.Millisecond)

	empty := atomic.LoadInt64(&observedEmpty)
	mism := atomic.LoadInt64(&observedMismatch)
	t.Logf("H-001 async reads: empty=%d mismatch=%d", empty, mism)
	if mism > 0 {
		observed.Range(func(k, _ any) bool {
			t.Logf("  mismatch got::want = %v", k)
			return true
		})
	}
	// The behavioural assertion is a supplement to -race; we DO NOT fail the
	// test on observed==0 because goroutines may race benignly into the
	// restored origCtx. -race remains authoritative.
}

// TestH001_HandlerPropagatesContextToGoroutine exercises the documented
// "safe" pattern where the handler snapshots the context via r.Context()
// INSIDE the handler (before returning) and passes that to the spawned
// goroutine. This should be race-free.
func TestH001_HandlerPropagatesContextToGoroutine(t *testing.T) {
	r := mm.New()
	var seen int64

	r.GET("/b/:id", func(w http.ResponseWriter, req *http.Request) {
		// SAFE pattern: resolve param BEFORE returning and pass the value.
		id := mm.PathParam(req, "id")
		go func(id string) {
			runtime.Gosched()
			if id != "" {
				atomic.AddInt64(&seen, 1)
			}
		}(id)
	})

	const workers, perWorker = 32, 200
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				id := fmt.Sprintf("%d-%d", g, i)
				req := httptest.NewRequest(http.MethodGet, "/b/"+id, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}
	wg.Wait()
	time.Sleep(20 * time.Millisecond)
	t.Logf("H-001 safe pattern: seen=%d", atomic.LoadInt64(&seen))
}

// TestH001_ServeHTTPMassiveParallel_Race is the massive-parallel race harness
// from the auditor prompt (Step 1). Any DATA RACE line is a Critical finding.
func TestH001_ServeHTTPMassiveParallel_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short")
	}

	r := mm.New()
	for i := range 64 {
		r.GET(fmt.Sprintf("/p/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			_ = mm.PathParam(req, "id")
		})
		r.GET(fmt.Sprintf("/s/%d", i), func(w http.ResponseWriter, req *http.Request) {})
	}

	const workers, perWorker = 64, 5000
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perWorker {
				var path string
				if i%2 == 0 {
					path = fmt.Sprintf("/p/%d/%d", g%64, i)
				} else {
					path = fmt.Sprintf("/s/%d", g%64)
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}
	wg.Wait()
}
