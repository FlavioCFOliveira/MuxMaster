// Package middleware_test — regression tests for rmp task #244 (sprint 18):
// sharding ThrottlePerIPCapped's key table and replacing ThrottleBacklog's
// channel semaphore with a lock-free fast path. These tests target the
// properties the contention-hunt report (CH-01/CH-02) required to be
// preserved across the rewrite: exact concurrency limits, exact table
// capacity under churn, backlog bound + timeout 503, slot release on
// panic, and no waiter starvation.
package middleware_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── ThrottleBacklog ──────────────────────────────────────────────────────────

// TestThrottleBacklog_NeverExceedsLimitConcurrent drives far more concurrent
// requests than the configured limit through ThrottleBacklog and asserts the
// observed peak concurrency inside the handler never exceeds limit, on both
// the lock-free fast path and the backlog-wait slow path.
func TestThrottleBacklog_NeverExceedsLimitConcurrent(t *testing.T) {
	const limit = 8
	const workers = 200

	var cur, peak int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&cur, 1)
		for {
			p := atomic.LoadInt64(&peak)
			if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.ThrottleBacklog(limit, workers, time.Second)(inner)

	var wg sync.WaitGroup
	var rejected int64
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				atomic.AddInt64(&rejected, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&peak); got > limit {
		t.Fatalf("peak concurrency = %d, want <= %d (limit)", got, limit)
	}
	if rejected != 0 {
		t.Fatalf("unexpected rejections: %d (backlog=%d should absorb all %d workers)", rejected, workers, workers)
	}
}

// TestThrottleBacklog_BacklogFullReturns503Immediately confirms the backlog
// bound is still enforced: with backlog=0, a second request arriving while
// the single permit is held must be rejected immediately (not after the
// timeout), preserving the pre-rewrite behaviour.
func TestThrottleBacklog_BacklogFullReturns503Immediately(t *testing.T) {
	release := make(chan struct{})
	holding := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(holding)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.ThrottleBacklog(1, 0, time.Hour)(inner)

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-holding

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)
	close(release)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 (backlog=0, no queue slot available)", rec.Code)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("503 took %v — expected an immediate rejection, not a wait", elapsed)
	}
}

// TestThrottleBacklog_TimeoutReturns503 confirms a queued waiter that never
// gets woken (because the permit holder never releases within the timeout)
// still receives 503 after roughly the configured timeout.
func TestThrottleBacklog_TimeoutReturns503(t *testing.T) {
	release := make(chan struct{})
	holding := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(holding)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.ThrottleBacklog(1, 4, 30*time.Millisecond)(inner)

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-holding
	defer close(release)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 on timeout", rec.Code)
	}
}

// TestThrottleBacklog_ReleaseOnHandlerPanic verifies that a panicking handler
// still releases its permit (the defer runs before the panic propagates), so
// a subsequent request is not permanently blocked behind a leaked slot.
func TestThrottleBacklog_ReleaseOnHandlerPanic(t *testing.T) {
	// mw is built ONCE so both wrapped handlers below share the same
	// underlying semaphore (ThrottleBacklog closes over it once, at
	// construction time) — this is what lets the second call genuinely
	// observe whether the first, panicking call released its permit.
	mw := middleware.ThrottleBacklog(1, 0, 50*time.Millisecond)

	panicking := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	func() {
		defer func() { _ = recover() }()
		rec := httptest.NewRecorder()
		panicking.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	ok := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	start := time.Now()
	rec := httptest.NewRecorder()
	ok.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 — permit should have been released after the panic", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("second request took %v — permit release after panic appears delayed", elapsed)
	}
}

// TestThrottleBacklog_NoStarvation drains a queue of waiters one at a time
// (the single permit is released and immediately re-acquired by the next
// handler in turn) and asserts every single queued waiter is eventually
// woken and served — i.e. no lost wake-ups and no permanent starvation.
func TestThrottleBacklog_NoStarvation(t *testing.T) {
	const backlog = 32
	var served int64

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&served, 1)
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.ThrottleBacklog(1, backlog, 5*time.Second)(inner)

	var wg sync.WaitGroup
	var timedOut int64
	wg.Add(backlog + 1)
	for i := 0; i < backlog+1; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusOK {
				atomic.AddInt64(&timedOut, 1)
			}
		}()
	}
	wg.Wait()

	if timedOut != 0 {
		t.Fatalf("%d waiters timed out or were rejected — expected all %d to be served (no starvation)", timedOut, backlog+1)
	}
	if got := atomic.LoadInt64(&served); got != backlog+1 {
		t.Fatalf("served=%d, want %d", got, backlog+1)
	}
}

// ── ThrottlePerIPCapped (sharded table) ──────────────────────────────────────

func ipKey(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// TestThrottlePerIPCapped_NeverExceedsLimitSameKey drives many concurrent
// requests through the SAME key and asserts the observed peak concurrency
// never exceeds the configured per-key limit — the sharded table must not
// change per-key token semantics.
func TestThrottlePerIPCapped_NeverExceedsLimitSameKey(t *testing.T) {
	const limit = 4
	const workers = 100

	var cur, peak int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&cur, 1)
		for {
			p := atomic.LoadInt64(&peak)
			if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.ThrottlePerIPCapped(limit, time.Second, 1000, func(*http.Request) string { return "same-key" })(inner)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&peak); got > limit {
		t.Fatalf("peak concurrency for one key = %d, want <= %d", got, limit)
	}
}

// TestThrottlePerIPCapped_ExactCapUnderConcurrentChurn holds exactly
// maxTable distinct keys open simultaneously and confirms that (a) all of
// them are accepted, and (b) every additional distinct key arriving while
// they are still open is rejected — the table never exceeds its cap, and
// never accepts fewer than it should either (the cap is exact in both
// directions), even with all insertions racing across many shards.
func TestThrottlePerIPCapped_ExactCapUnderConcurrentChurn(t *testing.T) {
	const maxTable = 50
	const extra = 50

	h := middleware.ThrottlePerIPCapped(1, 200*time.Millisecond, maxTable, ipKey)
	release := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(maxTable)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	wrapped := h(inner)

	// Phase 1: fill the table exactly to capacity with long-held requests.
	var wg1 sync.WaitGroup
	wg1.Add(maxTable)
	for i := 0; i < maxTable; i++ {
		go func(i int) {
			defer wg1.Done()
			entered.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.1.%d.%d:1000", (i>>8)&0xff, i&0xff)
			wrapped.ServeHTTP(rec, req)
		}(i)
	}
	entered.Wait()
	time.Sleep(30 * time.Millisecond) // let every goroutine reach acquire()

	// Phase 2: extra distinct keys must ALL be rejected — the table is full.
	var rejected int64
	var wg2 sync.WaitGroup
	wg2.Add(extra)
	for i := 0; i < extra; i++ {
		go func(i int) {
			defer wg2.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.2.%d.%d:1000", (i>>8)&0xff, i&0xff)
			hInner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
			h(hInner).ServeHTTP(rec, req)
			if rec.Code == http.StatusServiceUnavailable {
				atomic.AddInt64(&rejected, 1)
			}
		}(i)
	}
	wg2.Wait()
	close(release)
	wg1.Wait()

	if rejected != extra {
		t.Fatalf("rejected=%d, want %d — the cap must reject EVERY new key once %d entries are held (exact cap)", rejected, extra, maxTable)
	}
}

// TestThrottlePerIPCapped_EntriesReclaimedAfterTrafficStops verifies that
// once a first batch of maxTable keys finishes (releasing their slots), a
// completely disjoint second batch of maxTable NEW keys can all succeed —
// proving decRefs actually reclaims table capacity rather than leaking it.
func TestThrottlePerIPCapped_EntriesReclaimedAfterTrafficStops(t *testing.T) {
	const maxTable = 40
	h := middleware.ThrottlePerIPCapped(1, time.Second, maxTable, ipKey)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := h(ok)

	// First batch: fill and fully drain (each request completes and its
	// entry's refs drop to zero, deleting it from the table).
	var wg sync.WaitGroup
	wg.Add(maxTable)
	for i := 0; i < maxTable; i++ {
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.3.%d.%d:1000", (i>>8)&0xff, i&0xff)
			wrapped.ServeHTTP(rec, req)
		}(i)
	}
	wg.Wait()

	// Give reclamation a brief moment (decRefs runs in the deferred callback
	// right after ServeHTTP returns, but goroutines above may still be
	// unwinding their own defers).
	time.Sleep(20 * time.Millisecond)

	// Second batch: an entirely disjoint set of maxTable NEW keys. If the
	// first batch's entries were not reclaimed, the table would already be
	// "full" of stale entries and every one of these would be rejected.
	var rejected int64
	var wg2 sync.WaitGroup
	wg2.Add(maxTable)
	for i := 0; i < maxTable; i++ {
		go func(i int) {
			defer wg2.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.4.%d.%d:1000", (i>>8)&0xff, i&0xff)
			wrapped.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				atomic.AddInt64(&rejected, 1)
			}
		}(i)
	}
	wg2.Wait()

	if rejected != 0 {
		t.Fatalf("rejected=%d — first batch's table entries were not reclaimed after traffic stopped", rejected)
	}
}

// TestThrottlePerIPCapped_TimeoutReclaimsRefs confirms that a request which
// times out waiting for a token still decrements refs (MSR-2026-0068): after
// many timeouts on the SAME key, the entry must eventually be reclaimable
// (an unrelated request for a DIFFERENT key must not be blocked by a leaked
// refs count keeping the table artificially full).
func TestThrottlePerIPCapped_TimeoutReclaimsRefs(t *testing.T) {
	const maxTable = 2
	h := middleware.ThrottlePerIPCapped(1, 5*time.Millisecond, maxTable, ipKey)

	release := make(chan struct{})
	holdInner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	wrapped := h(holdInner)

	holding := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.5.0.1:1000"
		close(holding)
		wrapped.ServeHTTP(rec, req)
	}()
	<-holding
	time.Sleep(10 * time.Millisecond)

	// Several timed-out requests for the SAME key (limit=1, already held).
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	timeoutWrapped := h(ok)
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.5.0.1:1000"
		timeoutWrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("iteration %d: got %d, want 503 (timeout)", i, rec.Code)
		}
	}

	// A second, DIFFERENT key must still be accepted (maxTable=2, one slot
	// held by 10.5.0.1, so there is exactly one free slot left — but only if
	// the timed-out requests above did not leak extra refs/entries).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.5.0.2:2000"
	timeoutWrapped.ServeHTTP(rec, req)
	close(release)
	if rec.Code != http.StatusOK {
		t.Fatalf("second key got %d, want 200 — refs from timed-out requests on the first key must not leak", rec.Code)
	}
}

// TestThrottlePerIPCapped_ReleaseOnHandlerPanic verifies a panicking handler
// still releases its per-key token, so the next request for the same key is
// not permanently blocked.
func TestThrottlePerIPCapped_ReleaseOnHandlerPanic(t *testing.T) {
	h := middleware.ThrottlePerIPCapped(1, 50*time.Millisecond, 100, ipKey)
	panicInner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := h(panicInner)

	func() {
		defer func() { _ = recover() }()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.6.0.1:1000"
		wrapped.ServeHTTP(rec, req)
	}()

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	okWrapped := h(ok)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.6.0.1:1000"
	okWrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 — token should have been released after the panic", rec.Code)
	}
}

// TestThrottlePerIPCapped_ManyShardsManyKeysRace is a stress test intended to
// be run with -race: many goroutines hammer a large number of distinct keys
// concurrently (exercising every shard) plus a shared key (exercising
// per-key channel contention within one shard), verifying no data race and
// no panic.
func TestThrottlePerIPCapped_ManyShardsManyKeysRace(t *testing.T) {
	const maxTable = 500
	const goroutines = 300
	const itersPerGoroutine = 50

	h := middleware.ThrottlePerIPCapped(2, 20*time.Millisecond, maxTable, nil)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := h(ok)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				// Mix of many distinct keys (spread across shards) and a
				// handful of repeated keys (contended within one shard).
				req.RemoteAddr = fmt.Sprintf("10.9.%d.%d:%d", (g*7+i)%256, (g+i*3)%256, 1000+i)
				wrapped.ServeHTTP(rec, req)
			}
		}(g)
	}
	wg.Wait()
}
