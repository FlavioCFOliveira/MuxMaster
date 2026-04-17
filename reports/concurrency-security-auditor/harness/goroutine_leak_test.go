package harness

// Goroutine-leak probe (prompt Step 4) + baseline NumGoroutine snapshot for
// the steady-state serving path (no spawned goroutines expected).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestServeHTTPSteadyState_NoGoroutineLeak asserts that serving 50k requests
// does NOT leak any goroutines. MuxMaster is expected to run each request
// entirely on the caller's goroutine — no fan-out.
func TestServeHTTPSteadyState_NoGoroutineLeak(t *testing.T) {
	r := mm.New()
	r.GET("/a/:id", func(w http.ResponseWriter, req *http.Request) {
		_ = mm.PathParam(req, "id")
	})
	r.GET("/b", func(w http.ResponseWriter, req *http.Request) {})

	// Warm up and settle.
	for i := range 1000 {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/a/warm%d", i), nil))
	}
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	before := runtime.NumGoroutine()

	const workers, per = 32, 1500
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range per {
				path := fmt.Sprintf("/a/w%d-i%d", g, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
			}
		}(g)
	}
	wg.Wait()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	delta := after - before
	t.Logf("goroutines before=%d after=%d delta=%d", before, after, delta)
	// Small delta permitted (GC assist goroutines).
	if delta > 4 {
		t.Fatalf("unexpected goroutine leak: +%d goroutines", delta)
	}
}
