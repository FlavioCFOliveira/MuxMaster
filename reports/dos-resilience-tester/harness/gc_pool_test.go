package dosharness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/trace"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestPoolUnderGCStorm validates that the requestCtx pool in params.go
// remains correct under GC pressure. We intersperse requests with forced GC
// cycles (which clear sync.Pool) and assert no corruption.
func TestPoolUnderGCStorm(t *testing.T) {
	r := mm.New()
	r.GET("/u/:id", func(w http.ResponseWriter, req *http.Request) {
		id := mm.PathParam(req, "id")
		// Echo the id back so a caller can verify cross-contamination.
		_, _ = w.Write([]byte(id))
	})

	const iterations = 10000
	// 4 workers hitting distinct paths; racing with GC storms.
	var wg sync.WaitGroup
	errs := make(chan error, iterations)
	for w := 0; w < 4; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := intToStr(w*10000 + i)
				req := httptest.NewRequest(http.MethodGet, "/u/"+id, nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				if got := rec.Body.String(); got != id {
					errs <- fmt.Errorf("mismatch: want=%q got=%q", id, got)
					return
				}
			}
		}()
	}
	// Concurrent GC storm.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			runtime.GC()
		}
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("corruption detected: %v", e)
	}
}

// TestPoolContaminationConcurrent: concurrent requests MUST not leak prev
// request's params to the next.  If rc.small[2] contains stale data and is
// wrongly read by a later request, this test will catch it.
func TestPoolCrossGoroutineIntegrity(t *testing.T) {
	r := mm.New()
	r.GET("/a/:b/:c/:d", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		_, _ = fmt.Fprintf(w, "%s|%s|%s", ps.Get("b"), ps.Get("c"), ps.Get("d"))
	})

	const iterations = 2000
	const goroutines = 8

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				b := intToStr(g*1000000 + i)
				c := "X" + b
				d := "Y" + b
				path := "/a/" + b + "/" + c + "/" + d
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				want := b + "|" + c + "|" + d
				got := rec.Body.String()
				if got != want {
					t.Errorf("cross-contamination: want=%q got=%q", want, got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestTraceCapture dumps a runtime trace of a 2-second workload for later
// analysis with `go tool trace`. Useful for visualising goroutine
// scheduling under the sustained load test.
func TestTraceCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("trace emits a 10MB+ file; skipped in -short")
	}
	f, err := os.Create("/data/dev/github.com/FlavioCFOliveira/MuxMaster/reports/dos-resilience-tester/evidence/2026-04-17/trace.out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := trace.Start(f); err != nil {
		t.Fatal(err)
	}
	defer trace.Stop()

	r := mm.New()
	r.GET("/", func(w http.ResponseWriter, req *http.Request) {})
	r.GET("/x/:id", func(w http.ResponseWriter, req *http.Request) {
		_ = mm.PathParam(req, "id")
	})

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			req1 := httptest.NewRequest(http.MethodGet, "/", nil)
			req2 := httptest.NewRequest(http.MethodGet, "/x/42", nil)
			rec := httptest.NewRecorder()
			for i := 0; i < 50000; i++ {
				if (i+w)%2 == 0 {
					r.ServeHTTP(rec, req1)
				} else {
					r.ServeHTTP(rec, req2)
				}
			}
		}()
	}
	wg.Wait()
}
