// Regression tests for rmp task #251, sprint 18 (WH-01): ThrottlePerIP's
// non-blocking fast path (no timer allocated when a token is immediately
// available) and throttleEntry recycling through throttleTable.entryPool.
// These target exactly the properties reports/perf-lab-2026-09-24/waste-
// hunt.md's WH-01 fix relies on: a recycled entry's token channel is always
// full when handed to a new key, a capacity mismatch never reuses a
// mismatched entry, and the fast/slow path split is race-free under heavy
// concurrency. The CH-01/CH-02 contention properties (sharding, exact
// cap, no lost wake-up) are covered by throttle_internal_test.go and
// throttle_shard_test.go, unchanged by this task.
package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestThrottleTable_EntryPool_ReusedEntryIsFullyUsable drives many
// sequential, non-overlapping first-time acquisitions (each key's single
// holder always releases before the next key's acquire runs) so every
// acquire after the first is guaranteed to receive a recycled entry from
// entryPool (WH-01(b)). It then verifies each entry's token channel holds
// EXACTLY `limit` items — as if freshly built — before exercising it
// through one more full drain/refill/release cycle.
func TestThrottleTable_EntryPool_ReusedEntryIsFullyUsable(t *testing.T) {
	const limit = 3
	table := newThrottleTable(0)

	for i := range 50 {
		key := fmt.Sprintf("k-%d", i)
		ch, e, full := table.acquire(key, limit)
		if full {
			t.Fatalf("iteration %d: unexpectedly reported full", i)
		}
		if cap(ch) != limit {
			t.Fatalf("iteration %d: channel capacity = %d, want %d", i, cap(ch), limit)
		}
		got := 0
		for range limit + 1 {
			select {
			case <-ch:
				got++
			default:
			}
		}
		if got != limit {
			t.Fatalf("iteration %d: drained %d tokens, want %d — entry was not full when handed out (WH-01 recycling invariant broken)", i, got, limit)
		}
		for range got {
			ch <- struct{}{}
		}
		table.decRefs(key, e)
	}
}

// TestThrottleTable_EntryPool_CapacityMismatch_FallsBackToFreshEntry proves
// the defensive cap(e.tokens) == limit check in acquire(): a pooled entry
// built for one limit must never be handed to a key acquired with a
// different limit.
func TestThrottleTable_EntryPool_CapacityMismatch_FallsBackToFreshEntry(t *testing.T) {
	table := newThrottleTable(0)

	ch1, e1, full := table.acquire("a", 2)
	if full {
		t.Fatal("unexpected full on empty table")
	}
	<-ch1
	ch1 <- struct{}{}
	table.decRefs("a", e1) // refs -> 0: entry recycled into entryPool with cap 2

	ch2, e2, full := table.acquire("b", 5)
	if full {
		t.Fatal("unexpected full on empty table")
	}
	if cap(ch2) != 5 {
		t.Fatalf("cap(ch2) = %d, want 5 — a cap-2 pooled entry must never be reused for limit 5", cap(ch2))
	}
	got := 0
	for range 6 {
		select {
		case <-ch2:
			got++
		default:
		}
	}
	if got != 5 {
		t.Fatalf("drained %d tokens from the fresh cap-5 entry, want 5", got)
	}
	for range got {
		ch2 <- struct{}{}
	}
	table.decRefs("b", e2)
}

// TestThrottlePerIPCapped_FastPath_RaceStress hammers ThrottlePerIPCapped
// with far more concurrent requests than its limit, for a SINGLE shared key
// (maximising both the non-blocking fast-path and the timer-based slow-path
// code under -race) plus a churn of distinct keys (exercising entry
// recycling under -race). Every accepted request must observe the
// configured concurrency limit; no request should time out given the
// generous per-request hold time and timeout budget.
func TestThrottlePerIPCapped_FastPath_RaceStress(t *testing.T) {
	const limit = 4
	const goroutines = 100
	const itersPerGoroutine = 30

	keyFn := func(r *http.Request) string { return r.Header.Get("X-Test-Key") }
	h := ThrottlePerIPCapped(limit, 2*time.Second, 0, keyFn)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := h(ok)

	var wg sync.WaitGroup
	var rejected int64
	var mu sync.Mutex
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range itersPerGoroutine {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				// A mix of one hot, shared key (forces contention onto both
				// the fast and slow path) and per-goroutine distinct keys
				// (forces first-time acquires — entry pool churn).
				if i%3 == 0 {
					req.Header.Set("X-Test-Key", "shared")
				} else {
					req.Header.Set("X-Test-Key", fmt.Sprintf("g%d", g))
				}
				wrapped.ServeHTTP(rec, req)
				if rec.Code == http.StatusServiceUnavailable {
					mu.Lock()
					rejected++
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()

	if rejected != 0 {
		t.Fatalf("%d requests were rejected (503) — with a 2s timeout and immediate handler completion, none should ever time out", rejected)
	}
}

// TestThrottlePerIPCapped_EntryPoolRecycling_NeverExceedsLimit is the
// concurrency-bound counterpart WH-01(b) needs: heavy key churn (so every
// acquire after the first is very likely served by a recycled entryPool
// entry) combined with slow-ish handlers, asserting the observed IN-FLIGHT
// concurrency for EVERY key never exceeds its configured limit. A pool-reuse
// bug that handed out an entry with fewer than `limit` tokens actually
// present in its channel (e.g. a stale/partially-drained recycled entry)
// would manifest here as the limit being silently exceeded, not as a 503 —
// TestThrottlePerIPCapped_FastPath_RaceStress alone would not catch that
// class of defect.
func TestThrottlePerIPCapped_EntryPoolRecycling_NeverExceedsLimit(t *testing.T) {
	const limit = 3
	const keys = 40
	const requestsPerKey = 25

	keyFn := func(r *http.Request) string { return r.Header.Get("X-Test-Key") }
	h := ThrottlePerIPCapped(limit, 2*time.Second, 0, keyFn)

	inFlight := make(map[string]*atomic.Int64)
	var mapMu sync.Mutex
	getCounter := func(key string) *atomic.Int64 {
		mapMu.Lock()
		defer mapMu.Unlock()
		c, ok := inFlight[key]
		if !ok {
			c = new(atomic.Int64)
			inFlight[key] = c
		}
		return c
	}

	var violated atomic.Bool
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := getCounter(r.Header.Get("X-Test-Key"))
		cur := c.Add(1)
		defer c.Add(-1)
		if cur > limit {
			violated.Store(true)
		}
		// A short, variable hold time maximises overlap between requests for
		// the same key without making the test slow.
		time.Sleep(time.Millisecond)
	})
	wrapped := h(ok)

	var wg sync.WaitGroup
	for k := range keys {
		for range requestsPerKey {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("X-Test-Key", fmt.Sprintf("k-%d", k))
				wrapped.ServeHTTP(rec, req)
			}(k)
		}
	}
	wg.Wait()

	if violated.Load() {
		t.Fatal("observed concurrent in-flight count exceeded the configured limit for some key — entry pool recycling handed out a corrupted token count (WH-01 invariant broken)")
	}
}
