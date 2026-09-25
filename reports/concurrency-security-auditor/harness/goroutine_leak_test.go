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

// stableGoroutineBaseline returns a runtime.NumGoroutine() reading that has
// stayed constant across a few consecutive polls, so the "before" snapshot
// a leak test measures against is not itself mid-settle from warm-up work
// (pool population, a prior GC cycle, etc.). Bounded by timeout: if the
// count never stabilizes within it, the last observed value is returned
// rather than blocking forever.
func stableGoroutineBaseline(timeout time.Duration) int {
	const pollInterval = 5 * time.Millisecond
	const requiredStreak = 3
	deadline := time.Now().Add(timeout)
	runtime.GC()
	prev := runtime.NumGoroutine()
	streak := 1
	for streak < requiredStreak && time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		cur := runtime.NumGoroutine()
		if cur == prev {
			streak++
		} else {
			prev = cur
			streak = 1
		}
	}
	return prev
}

// waitForGoroutineCeiling polls runtime.NumGoroutine() until it drops to at
// most want, or timeout elapses. Polling replaces a single fixed sleep: on a
// loaded host, a fixed sleep is either too short (a flaky failure against a
// perfectly healthy implementation whose goroutines simply hadn't drained
// yet) or wastefully long in the common case. Polling settles as soon as
// goroutines actually drain, and only spends the full budget when they
// genuinely never do — which is exactly the leak this is meant to catch.
func waitForGoroutineCeiling(want int, timeout time.Duration) (last int, settled bool) {
	const pollInterval = 5 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for {
		runtime.GC()
		last = runtime.NumGoroutine()
		if last <= want {
			return last, true
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(pollInterval)
	}
}

// TestTimeoutMW_GoroutineLeak verifies that the Timeout middleware does not leak
// goroutines when the handler finishes (normally, cancel() is deferred so the
// context is always cancelled — no goroutine leak from WithTimeout itself).
//
// Deterministic by construction (rmp #257): runtime.NumGoroutine() is a
// process-global counter, so this test deliberately does NOT call
// t.Parallel() — every other test in this package that does is queued into
// a single parallel batch which only starts running after every serial
// (non-Parallel) top-level test, including this one, has returned. Staying
// serial means no other test's goroutines can ever be alive concurrently
// with this one's before/after measurements, in any run order and under
// any -count. The remaining source of flakiness — the OS/scheduler taking
// longer than expected to actually drain goroutines under load — is
// handled by bounded polling (waitForGoroutineCeiling) instead of a single
// fixed sleep.
func TestTimeoutMW_GoroutineLeak(t *testing.T) {
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

	before := stableGoroutineBaseline(500 * time.Millisecond)

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

	// Allow up to n*4 goroutines above baseline (test runtime overhead),
	// polling up to 2s for the count to settle instead of trusting one
	// fixed sleep.
	threshold := before + n*4
	after, settled := waitForGoroutineCeiling(threshold, 2*time.Second)
	t.Logf("goroutine count after %d requests: %d (before=%d threshold=%d)", n*4*5000, after, before, threshold)
	if !settled {
		t.Errorf("goroutine leak: count=%d exceeds threshold %d and did not settle within budget", after, threshold)
	}
}

// TestTimeoutMW_SlowHandler_ContextCancelled verifies that when a handler takes
// longer than the timeout, the request context is cancelled and the response is
// written by the middleware (503/504-style). The Timeout implementation here
// simply cancels the context; the handler is responsible for observing it.
//
// We verify: no goroutine leak even when handlers run longer than timeout.
//
// Deterministic by construction (rmp #257, widened) — see
// TestTimeoutMW_GoroutineLeak for why t.Parallel() is deliberately absent
// and bounded polling is used instead of a fixed sleep. This test showed the
// exact same flakiness pattern (t.Parallel + fixed sleep + global counter)
// as the three originally-scoped tests when run inside the package's full
// parallel batch — same root cause, same fix.
func TestTimeoutMW_SlowHandler_ContextCancelled(t *testing.T) {
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

	before := stableGoroutineBaseline(500 * time.Millisecond)

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

	threshold := before + n*4
	after, settled := waitForGoroutineCeiling(threshold, 2*time.Second)
	t.Logf("handlerRan=%d goroutine count: %d (before=%d threshold=%d)", atomic.LoadInt64(&handlerRan), after, before, threshold)
	if !settled {
		t.Errorf("goroutine leak after slow handler + timeout: count=%d exceeds threshold %d and did not settle within budget", after, threshold)
	}
}

// TestThrottlePerIP_NoLeak verifies ThrottlePerIP cleans up per-IP entries
// (refs == 0 triggers delete) with no goroutine leaks.
//
// Deterministic by construction (rmp #257) — see TestTimeoutMW_GoroutineLeak
// for why t.Parallel() is deliberately absent and bounded polling is used
// instead of a fixed sleep.
func TestThrottlePerIP_NoLeak(t *testing.T) {
	r := mm.New()
	r.Use(mw.ThrottlePerIP(10, 50*time.Millisecond, nil))
	r.GET("/api/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	before := stableGoroutineBaseline(500 * time.Millisecond)

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

	threshold := before + n*2
	after, settled := waitForGoroutineCeiling(threshold, 2*time.Second)
	t.Logf("ThrottlePerIP goroutine count: %d (before=%d threshold=%d)", after, before, threshold)
	if !settled {
		t.Errorf("goroutine leak in ThrottlePerIP: count=%d exceeds threshold %d and did not settle within budget", after, threshold)
	}
}

// TestThrottleBacklog_ChannelLeak verifies that the channel-based ThrottleBacklog
// does not leak goroutines when requests are queued and timeout out.
//
// Deterministic by construction (rmp #257) — see TestTimeoutMW_GoroutineLeak
// for why t.Parallel() is deliberately absent and bounded polling is used
// instead of a fixed sleep.
func TestThrottleBacklog_ChannelLeak(t *testing.T) {
	r := mm.New()
	// 1 concurrent, 0 backlog — most requests get 503 immediately.
	r.Use(mw.ThrottleBacklog(1, 0, 5*time.Millisecond))
	r.GET("/limited/:id", func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	before := stableGoroutineBaseline(500 * time.Millisecond)

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

	threshold := before + n*2
	after, settled := waitForGoroutineCeiling(threshold, 2*time.Second)
	t.Logf("ThrottleBacklog goroutine count: %d (before=%d threshold=%d)", after, before, threshold)
	if !settled {
		t.Errorf("goroutine leak in ThrottleBacklog: count=%d exceeds threshold %d and did not settle within budget", after, threshold)
	}
}
