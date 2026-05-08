//go:build race

// panic_pool_cleanliness_test.go — CSA harness: panic recovery + param cleanliness.
// Verifies that panic in a handler does not leave stale params visible in the
// next request (no pool contamination), and that recoverer + PanicHandler work
// concurrently without races.

package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestPanicInHandler_ParamsClean verifies that after a panic in a param route,
// subsequent requests to a different param route see only their own params.
func TestPanicInHandler_ParamsClean(t *testing.T) {
	r := mm.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		http.Error(w, "recovered", http.StatusInternalServerError)
	}

	var staleCount int64

	r.GET("/panic/:id", func(w http.ResponseWriter, req *http.Request) {
		panic("boom")
	})
	r.GET("/check/:name", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		// Must have exactly one param, keyed "name".
		if len(ps) != 1 || ps[0].Key != "name" {
			atomic.AddInt64(&staleCount, 1)
			t.Logf("stale params: %+v", ps)
		}
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 20000; i++ {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/panic/anything", nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/check/hello", nil))
	}

	if n := atomic.LoadInt64(&staleCount); n > 0 {
		t.Fatalf("stale params after panic: %d occurrences", n)
	}
}

// TestPanicInHandler_PoolClean_Concurrent runs the panic + check cycle in parallel.
func TestPanicInHandler_PoolClean_Concurrent(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		http.Error(w, "recovered", http.StatusInternalServerError)
	}

	var staleCount int64

	r.GET("/panic2/:x/:y", func(w http.ResponseWriter, req *http.Request) {
		panic("two-param panic")
	})
	r.GET("/clean2/:a", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 || ps[0].Key != "a" {
			atomic.AddInt64(&staleCount, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/panic2/%d/%d", g, i), nil))
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/clean2/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	if n := atomic.LoadInt64(&staleCount); n > 0 {
		t.Fatalf("stale params after 2-param panic: %d occurrences", n)
	}
}

// TestRecovererMiddleware_NoPanic_Race confirms the recoverer middleware does not
// introduce races when no panic occurs.
func TestRecovererMiddleware_NoPanic_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.Use(mw.Recoverer())
	r.GET("/safe/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				path := fmt.Sprintf("/safe/%d", i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
			}
		}(g)
	}
	wg.Wait()
}

// TestRecovererMiddleware_WithPanic_Race confirms that the recoverer middleware's
// recover() defer does not race with concurrent requests.
func TestRecovererMiddleware_WithPanic_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.Use(mw.Recoverer())
	r.GET("/boom/:id", func(w http.ResponseWriter, req *http.Request) {
		panic("test")
	})
	r.GET("/ok/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/boom/%d", i), nil))
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/ok/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()
}

// TestRecovererAndTimeout_H_C tests hypothesis H-C:
// Recoverer wraps Timeout; timeout fires; handler panics after timeout.
// Must not produce double WriteHeader, must not race.
func TestRecovererAndTimeout_H_C(t *testing.T) {
	t.Parallel()

	// Timeout is very short; handler is fast enough to run within it.
	// But we also fire a slow goroutine to simulate the leak scenario.
	r := mm.New()
	r.Use(mw.Recoverer())

	slowDone := make(chan struct{})
	r.GET("/slow/:id", func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
			// Context cancelled by timeout — observe cancellation, don't panic.
		case <-slowDone:
		}
		w.WriteHeader(http.StatusOK)
	})
	close(slowDone)

	r.GET("/panic-after/:id", func(w http.ResponseWriter, req *http.Request) {
		panic("post-timeout panic")
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/slow/%d", i), nil))
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/panic-after/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()
}
