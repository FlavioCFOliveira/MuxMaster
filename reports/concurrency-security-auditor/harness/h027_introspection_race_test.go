package harness

// H-027 — Introspection (Walk / Routes / Lookup) concurrent with Handle.
//
// introspection.go loads `trees := m.treesPtr.Load()` atomically, but the
// tree roots themselves are MUTATED in place by addRoute (children slice,
// indices string, handler field). A concurrent Walk can therefore observe
// torn state; under -race, this should surface as a data race.
//
// The docs state that dynamic registration after serving is UB. This test
// does NOT imply MuxMaster must support dynamic reg — it simply proves
// whether introspection is safe against it. If unsafe, the report MUST
// document the constraint and/or propose a guard.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestH027_WalkVsHandleRace interleaves Handle with Walk and Lookup. Any
// concurrent access to tree fields will be caught by -race as a Write vs Read
// race on node.children, node.indices, or node.handler.
func TestH027_WalkVsHandleRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short")
	}

	r := mm.New()
	// Seed with a few routes so Walk has something to iterate.
	for i := range 8 {
		r.GET(fmt.Sprintf("/seed/%d", i), func(w http.ResponseWriter, req *http.Request) {})
	}

	var stop atomic.Bool
	var wg sync.WaitGroup

	// Goroutine A: continuous registration of new routes (dynamic).
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for !stop.Load() {
			// Use a fresh, non-conflicting path each iteration.
			path := fmt.Sprintf("/dyn/%d/sub/%d", i%64, i)
			// Defensive: addRoute panics on conflict. We use a unique i so no
			// conflict occurs. If unique path collides, rethrow as failure.
			func() {
				defer func() {
					if rcv := recover(); rcv != nil {
						t.Errorf("H-027: addRoute panicked on unique path %q: %v", path, rcv)
					}
				}()
				r.GET(path, func(w http.ResponseWriter, req *http.Request) {})
			}()
			i++
			if i%64 == 0 {
				time.Sleep(time.Microsecond)
			}
		}
	}()

	// Goroutine B: continuous Walk.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = r.Walk(func(method, pattern string, h http.Handler) error {
				_ = method
				_ = pattern
				_ = h
				return nil
			})
		}
	}()

	// Goroutine C: continuous Routes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = r.Routes()
		}
	}()

	// Goroutine D: continuous Lookup.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_, _, _ = r.Lookup(http.MethodGet, "/seed/0")
		}
	}()

	// Goroutine E: continuous ServeHTTP.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			req := httptest.NewRequest(http.MethodGet, "/seed/0", nil)
			r.ServeHTTP(httptest.NewRecorder(), req)
		}
	}()

	time.Sleep(2 * time.Second)
	stop.Store(true)
	wg.Wait()
}
