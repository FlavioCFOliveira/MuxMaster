//go:build race

// goroutine_leak_test.go — CSA harness for goroutine leak detection.
// Tests: timeout middleware goroutine lifecycle, ThrottlePerIP ref counting.
// Uses runtime.NumGoroutine() as a bounded-delta assertion.

package muxmaster_test

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
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestTimeoutMW_GoroutineLeak verifies that the Timeout middleware does not leak
// goroutines when the handler finishes (normally, cancel() is deferred so the
// context is always cancelled — no goroutine leak from WithTimeout itself).
func TestTimeoutMW_GoroutineLeak(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.Use(mw.Timeout(50 * time.Millisecond))

	r.GET("/fast/:id", func(w http.ResponseWriter, req *http.Request) {
		// Completes well within timeout.
		w.WriteHeader(http.StatusOK)
	})

	// Warm up (fill pools, stabilize goroutine count).
	for i := 0; i < 100; i++ {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/fast/0", nil))
	}
	runtime.GC()
	time.Sleep(20 * time.Millisecond)

	before := runtime.NumGoroutine()

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				path := fmt.Sprintf("/fast/%d", i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
			}
		}(g)
	}
	wg.Wait()

	// Give goroutines time to drain.
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()
	// Allow up to n*2 goroutines delta (test runtime overhead).
	delta := after - before
	t.Logf("goroutine delta after %d requests: %d (before=%d after=%d)", n*4*5000, delta, before, after)
	if delta > n*4 {
		t.Errorf("goroutine leak: delta=%d exceeds threshold %d", delta, n*4)
	}
}

// TestTimeoutMW_SlowHandler_ContextCancelled verifies that when a handler takes
// longer than the timeout, the request context is cancelled and the response is
// written by the middleware (503/504-style). The Timeout implementation here
// simply cancels the context; the handler is responsible for observing it.
//
// We verify: no goroutine leak even when handlers run longer than timeout.
func TestTimeoutMW_SlowHandler_ContextCancelled(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.Use(mw.Timeout(5 * time.Millisecond))

	var handlerRan int64
	r.GET("/slow/:id", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&handlerRan, 1)
		// Deliberately take longer than timeout — context will be Done.
		select {
		case <-req.Context().Done():
			// Correctly observed cancellation.
		case <-time.After(200 * time.Millisecond):
			// Should not reach here in a normal run.
		}
		w.WriteHeader(http.StatusOK)
	})

	before := runtime.NumGoroutine()

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/slow/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	after := runtime.NumGoroutine()

	delta := after - before
	t.Logf("handlerRan=%d goroutine_delta=%d", atomic.LoadInt64(&handlerRan), delta)
	if delta > n*4 {
		t.Errorf("goroutine leak after slow handler + timeout: delta=%d", delta)
	}
}

// TestThrottlePerIP_NoLeak verifies ThrottlePerIP cleans up per-IP entries
// (refs == 0 triggers delete) with no goroutine leaks.
func TestThrottlePerIP_NoLeak(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.Use(mw.ThrottlePerIP(10, 50*time.Millisecond, nil))
	r.GET("/api/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	before := runtime.NumGoroutine()

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/api/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()
	delta := after - before
	t.Logf("ThrottlePerIP goroutine delta: %d", delta)
	if delta > n*2 {
		t.Errorf("goroutine leak in ThrottlePerIP: delta=%d", delta)
	}
}

// TestThrottleBacklog_ChannelLeak verifies that the channel-based ThrottleBacklog
// does not leak goroutines when requests are queued and timeout out.
func TestThrottleBacklog_ChannelLeak(t *testing.T) {
	t.Parallel()
	r := mm.New()
	// 1 concurrent, 0 backlog — most requests get 503 immediately.
	r.Use(mw.ThrottleBacklog(1, 0, 5*time.Millisecond))
	r.GET("/limited/:id", func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	before := runtime.NumGoroutine()

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/limited/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()
	delta := after - before
	t.Logf("ThrottleBacklog goroutine delta: %d", delta)
	if delta > n*2 {
		t.Errorf("goroutine leak in ThrottleBacklog: delta=%d", delta)
	}
}
