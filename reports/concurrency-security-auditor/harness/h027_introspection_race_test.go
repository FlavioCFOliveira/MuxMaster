//go:build race

// h027_introspection_race_test.go — CSA harness for Hypothesis H-I
// Introspection (Lookup, Walk, WalkFast, Routes) vs concurrent ServeHTTP.
// The RWMutex in Lookup/Walk protects tree traversal while Handle COW-atomics
// protect ServeHTTP. Validates no torn reads under mixed introspection + serving.

package muxmaster_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestLookup_VsConcurrentServe confirms Lookup and ServeHTTP can run concurrently
// without data races. Lookup acquires RLock; Handle (during registration) acquires Lock.
// ServeHTTP reads via atomic load — they are orthogonal.
func TestLookup_VsConcurrentServe(t *testing.T) {
	t.Parallel()
	r := mm.New()
	for i := range 100 {
		i := i
		r.GET(fmt.Sprintf("/resource/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// ServeHTTP goroutines.
	// iters trimmed 20000 -> 4000 (rmp #271); still 4000*n*4 = 256,000
	// requests at GOMAXPROCS=16 (measured 2026-09-25: 68.1s of a 320s package
	// run at 20000).
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2400; i++ {
				path := fmt.Sprintf("/resource/%d/x", (g*i)%100)
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}

	// Lookup goroutines.
	// iters trimmed 10000 -> 3000 (rmp #271).
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1800; i++ {
				path := fmt.Sprintf("/resource/%d/val", (g*i)%100)
				_, _, _ = r.Lookup("GET", path)
			}
		}(g)
	}

	wg.Wait()
}

// TestWalk_VsConcurrentServe confirms Walk/WalkFast/Routes do not race with
// concurrent ServeHTTP calls.
func TestWalk_VsConcurrentServe(t *testing.T) {
	t.Parallel()
	r := mm.New()
	for i := range 50 {
		i := i
		r.GET(fmt.Sprintf("/a/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {})
		r.GETFast(fmt.Sprintf("/b/%d/:id", i), func(w http.ResponseWriter, req *http.Request, ps mm.Params) {})
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// iters trimmed 10000 -> 2500 (rmp #271); the Routes()/Walk()/WalkFast()
	// side below dominated this test's cost, not this ServeHTTP side.
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1500; i++ {
				path := fmt.Sprintf("/a/%d/v", (g*i)%50)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
				path2 := fmt.Sprintf("/b/%d/v", (g*i)%50)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path2, nil))
			}
		}(g)
	}

	// iters trimmed 2000 -> 500 (rmp #271): each iteration calls Routes(),
	// Walk() and WalkFast(), each of which walks/copies the full route table
	// — at n*2*2000 this was the dominant cost of this test (measured
	// 2026-09-25: 73.1s of a 320s package run at 2000 iters).
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = r.Routes()
				_ = r.Walk(func(method, pattern string, handler http.Handler) error {
					return nil
				})
				_ = r.WalkFast(func(method, pattern string, handler mm.FastHandler) error {
					return nil
				})
			}
		}()
	}

	wg.Wait()
}

// TestWalk_StopIteration confirms that Walk and WalkFast stop iteration on error.
func TestWalk_StopIteration(t *testing.T) {
	r := mm.New()
	for i := range 20 {
		r.GET(fmt.Sprintf("/x/%d", i), func(w http.ResponseWriter, req *http.Request) {})
	}

	sentinel := errors.New("stop")
	count := 0
	err := r.Walk(func(method, pattern string, handler http.Handler) error {
		count++
		if count >= 5 {
			return sentinel
		}
		return nil
	})
	if err != sentinel {
		t.Errorf("Walk did not stop on error: got %v, want sentinel", err)
	}
	if count != 5 {
		t.Errorf("Walk called fn %d times after error, want 5", count)
	}
}

// TestIntrospection_RouteSnapshot verifies Routes() returns a coherent snapshot
// even under concurrent Handle() registration (COW + atomic Store).
func TestIntrospection_RouteSnapshot(t *testing.T) {
	t.Parallel()
	r := mm.New()
	for i := range 50 {
		i := i
		r.GET(fmt.Sprintf("/init/%d", i), func(w http.ResponseWriter, req *http.Request) {})
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// Routes snapshot goroutines.
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// trimmed 1000 -> 400 (rmp #271): Routes() walks/copies the full
			// route table on every call (measured 2026-09-25: 61.1s of a
			// 320s package run at 1000 iters).
			for i := 0; i < 400; i++ {
				infos := r.Routes()
				if len(infos) < 50 {
					// Might see up to 50+extra from dynamic registrations below.
					// But never less than 50.
					t.Errorf("Routes snapshot has only %d routes (expected >= 50)", len(infos))
					return
				}
			}
		}()
	}

	// Dynamic registration goroutine (simulates late registration — documented unsupported,
	// but must not panic or corrupt; it must COW-safely).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 50; i < 100; i++ {
			i := i
			r.GET(fmt.Sprintf("/late/%d", i), func(w http.ResponseWriter, req *http.Request) {})
		}
	}()

	wg.Wait()
}
