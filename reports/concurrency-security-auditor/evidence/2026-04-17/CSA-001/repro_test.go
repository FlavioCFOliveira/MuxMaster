// CSA-001 minimal reproducer: handler spawns goroutine that retains r.
// Triggers WARNING: DATA RACE in under 50ms on a single core.
//
// Run:
//
//	go test -race -run=TestCSA001 -count=1 ./reports/concurrency-security-auditor/evidence/2026-04-17/CSA-001/
package csa001

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestCSA001_HandlerRetainsRequest_Race(t *testing.T) {
	r := mm.New()
	var wg sync.WaitGroup

	r.GET("/u/:id", func(w http.ResponseWriter, req *http.Request) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Read r.Context() after the dispatcher has restored origCtx.
			time.Sleep(1 * time.Millisecond)
			_ = mm.PathParam(req, "id")
		}()
	})

	// Two requests suffice to trigger the race:
	//   request A: handler returns, dispatcher writes origCtx to r.ctx
	//   request B (same goroutine or parallel): writes rc to r.ctx
	//   A's child goroutine reads r.ctx concurrently
	var wg2 sync.WaitGroup
	for range 100 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			req := httptest.NewRequest(http.MethodGet, "/u/x", nil)
			r.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg2.Wait()
	wg.Wait()
}
