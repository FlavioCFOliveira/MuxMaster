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
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				path := fmt.Sprintf("/resource/%d/x", (g*i)%100)
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}

	// Lookup goroutines.
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
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

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				path := fmt.Sprintf("/a/%d/v", (g*i)%50)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
				path2 := fmt.Sprintf("/b/%d/v", (g*i)%50)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path2, nil))
			}
		}(g)
	}

	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
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
			for i := 0; i < 1000; i++ {
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
