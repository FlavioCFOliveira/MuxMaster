// Internal (white-box) tests and benchmark for rmp task #246 (sprint 18):
// bounding OAuth2Introspect's cache eviction cost under saturation (CH-09).
// This file uses `package middleware` (not middleware_test) so it can drive
// oauth2Cache directly — bypassing the HTTP layer and the real introspection
// round trip lets the saturation benchmark isolate exactly the eviction
// algorithm's cost, and lets the correctness tests inspect the heap/map
// invariants that the O(n)->O(log n) rewrite must preserve.
package middleware

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testOAuthKey builds a distinct 32-byte cache key from an integer index,
// standing in for sha256(token) without needing a real token.
func testOAuthKey(i int) [32]byte {
	var k [32]byte
	binary.LittleEndian.PutUint64(k[:8], uint64(i))
	return k
}

func newTestOAuth2Cache(maxSize int) *oauth2Cache {
	return &oauth2Cache{
		entries: make(map[[32]byte]*oauth2Entry, maxSize),
		maxSize: maxSize,
	}
}

// BenchmarkOAuth2CacheSetAtSaturation measures set() latency once the cache
// is already filled to MaxCacheSize with non-expired entries, under
// concurrent get() load — every set() call in this benchmark inserts a
// brand-new key, so it forces exactly one eviction every time (the
// worst-case, always-saturated regime CH-09 flagged as unmeasured).
//
// Before the O(log n) heap rewrite, every eviction here would perform a
// full O(maxSize) scan of the map WHILE HOLDING c.mu.Lock() (Go), blocking
// every concurrent get()/set() for the scan's duration — see
// reports/perf-lab-2026-09-24/results/fixes/246.txt for the measured
// before/after comparison.
func BenchmarkOAuth2CacheSetAtSaturation(b *testing.B) {
	const maxSize = 10_000
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()
	for i := 0; i < maxSize; i++ {
		c.set(testOAuthKey(i), &IntrospectResponse{Active: true}, now.Add(time.Hour))
	}

	var nextKey atomic.Int64
	nextKey.Store(maxSize)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// Concurrent read of an already-cached key.
			c.get(testOAuthKey(i % maxSize))
			i++
			// Concurrent write of a brand-new key — the cache is always at
			// or over capacity here, so this always triggers evictOneLocked.
			k := int(nextKey.Add(1))
			c.set(testOAuthKey(k), &IntrospectResponse{Active: true}, now.Add(time.Hour))
		}
	})
}

// TestOAuth2Cache_SizeNeverExceedsMaxSize inserts far more distinct keys
// than maxSize and asserts the map (and the heap backing it) never exceed
// maxSize entries, and stay in lock-step with each other, at every step.
func TestOAuth2Cache_SizeNeverExceedsMaxSize(t *testing.T) {
	const maxSize = 16
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()
	for i := 0; i < maxSize*4; i++ {
		c.set(testOAuthKey(i), &IntrospectResponse{Active: true}, now.Add(time.Hour))
		if len(c.entries) > maxSize {
			t.Fatalf("iteration %d: len(entries)=%d > maxSize=%d", i, len(c.entries), maxSize)
		}
		if c.heap.Len() != len(c.entries) {
			t.Fatalf("iteration %d: heap.Len()=%d != len(entries)=%d — heap/map desynced", i, c.heap.Len(), len(c.entries))
		}
	}
}

// TestOAuth2Cache_ExpiredEvictedFirst confirms that when the cache is full
// and at least one entry has already expired, eviction removes an EXPIRED
// entry rather than a live one — "expired entries evicted first".
func TestOAuth2Cache_ExpiredEvictedFirst(t *testing.T) {
	const maxSize = 4
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()

	expiredKey := testOAuthKey(0)
	c.set(expiredKey, &IntrospectResponse{Active: true}, now.Add(-time.Hour)) // already expired
	c.set(testOAuthKey(1), &IntrospectResponse{Active: true}, now.Add(2*time.Hour))
	c.set(testOAuthKey(2), &IntrospectResponse{Active: true}, now.Add(3*time.Hour))
	c.set(testOAuthKey(3), &IntrospectResponse{Active: true}, now.Add(4*time.Hour))

	// Cache is now at capacity (4/4) with one expired entry among 3 live
	// ones. A 5th distinct key must evict the EXPIRED one, not a live one.
	c.set(testOAuthKey(4), &IntrospectResponse{Active: true}, now.Add(5*time.Hour))

	if _, ok := c.entries[expiredKey]; ok {
		t.Fatalf("expired entry was not evicted")
	}
	for _, k := range []int{1, 2, 3, 4} {
		if _, ok := c.entries[testOAuthKey(k)]; !ok {
			t.Fatalf("live entry %d was unexpectedly evicted", k)
		}
	}
}

// TestOAuth2Cache_SoonestExpiryFallback confirms the DOS-2026-0005 fallback:
// when the cache is full and NOTHING has expired yet, eviction removes the
// entry with the soonest expiry.
func TestOAuth2Cache_SoonestExpiryFallback(t *testing.T) {
	const maxSize = 4
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()

	soonestKey := testOAuthKey(0)
	c.set(soonestKey, &IntrospectResponse{Active: true}, now.Add(1*time.Hour))
	c.set(testOAuthKey(1), &IntrospectResponse{Active: true}, now.Add(2*time.Hour))
	c.set(testOAuthKey(2), &IntrospectResponse{Active: true}, now.Add(3*time.Hour))
	c.set(testOAuthKey(3), &IntrospectResponse{Active: true}, now.Add(4*time.Hour))

	c.set(testOAuthKey(4), &IntrospectResponse{Active: true}, now.Add(5*time.Hour))

	if _, ok := c.entries[soonestKey]; ok {
		t.Fatalf("soonest-expiry entry should have been evicted (DOS-2026-0005 fallback)")
	}
	for _, k := range []int{1, 2, 3, 4} {
		if _, ok := c.entries[testOAuthKey(k)]; !ok {
			t.Fatalf("entry %d was unexpectedly evicted", k)
		}
	}
}

// TestOAuth2Cache_UpdateExistingKeyRepositionsHeap confirms that re-caching
// an already-present key updates it in place (via heap.Fix) instead of
// growing the heap/map with a duplicate entry.
func TestOAuth2Cache_UpdateExistingKeyRepositionsHeap(t *testing.T) {
	const maxSize = 8
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()
	key := testOAuthKey(0)

	c.set(key, &IntrospectResponse{Active: true, Subject: "first"}, now.Add(time.Hour))
	c.set(key, &IntrospectResponse{Active: true, Subject: "second"}, now.Add(2*time.Hour))

	if len(c.entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1 (overwrite must not duplicate)", len(c.entries))
	}
	if c.heap.Len() != 1 {
		t.Fatalf("heap.Len()=%d, want 1 (overwrite must reposition, not duplicate)", c.heap.Len())
	}
	resp, ok := c.get(key)
	if !ok || resp.Subject != "second" {
		t.Fatalf("get() = (%+v, %v), want Subject=\"second\", ok=true", resp, ok)
	}
}

// TestOAuth2Cache_MaxSizeZero_NoInsertion preserves the defence-in-depth
// behaviour: a cache constructed with maxSize<=0 never inserts anything.
func TestOAuth2Cache_MaxSizeZero_NoInsertion(t *testing.T) {
	c := newTestOAuth2Cache(0)
	c.set(testOAuthKey(0), &IntrospectResponse{Active: true}, time.Now().Add(time.Hour))
	if len(c.entries) != 0 {
		t.Fatalf("len(entries)=%d, want 0 (maxSize=0 must never insert)", len(c.entries))
	}
}

// TestOAuth2Cache_ConcurrentGetSetAtSaturation_NoRace hammers a saturated
// cache with concurrent get()/set() calls (run with -race) to validate the
// heap-backed eviction path under real contention, and confirms the size
// invariant survives concurrent churn.
func TestOAuth2Cache_ConcurrentGetSetAtSaturation_NoRace(t *testing.T) {
	const maxSize = 200
	const goroutines = 32
	const itersPerGoroutine = 500

	c := newTestOAuth2Cache(maxSize)
	now := time.Now()
	for i := 0; i < maxSize; i++ {
		c.set(testOAuthKey(i), &IntrospectResponse{Active: true}, now.Add(time.Hour))
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				c.get(testOAuthKey(i % maxSize))
				k := maxSize + g*itersPerGoroutine + i
				c.set(testOAuthKey(k), &IntrospectResponse{Active: true}, now.Add(time.Hour))
			}
		}(g)
	}
	wg.Wait()

	if len(c.entries) > maxSize {
		t.Fatalf("len(entries)=%d > maxSize=%d after concurrent churn", len(c.entries), maxSize)
	}
	if c.heap.Len() != len(c.entries) {
		t.Fatalf("heap.Len()=%d != len(entries)=%d after concurrent churn", c.heap.Len(), len(c.entries))
	}
}

// TestOAuth2Cache_ConcurrentGetSetSameKey_NoRace is a regression test for a
// real data race caught by the sprint-18 -race suite (originally surfaced
// via reports/concurrency-security-auditor/harness's
// TestH8_06_Timeout_OAuth2_SingleflightCancel): set() re-caching an
// ALREADY-PRESENT key (e.g. two concurrent singleflight leaders racing for
// the same token, MSR-2026-0071) used to mutate the existing *oauth2Entry
// in place (e.resp = resp; e.expiry = expiry) while get() read those same
// fields AFTER releasing its read lock — safe only as long as entries were
// immutable post-construction, which stopped being true once set() started
// updating existing entries in place for the O(log n) heap.Fix rewrite.
// get() now holds the read lock across the field reads (see oauth2.go);
// this test drives many goroutines to concurrently get()/set() the exact
// SAME key — unlike TestOAuth2Cache_ConcurrentGetSetAtSaturation_NoRace,
// which only ever set()s brand-new keys and therefore never exercised the
// re-cache-in-place path — so a regression here is caught under -race.
func TestOAuth2Cache_ConcurrentGetSetSameKey_NoRace(t *testing.T) {
	const goroutines = 32
	const itersPerGoroutine = 2000

	c := newTestOAuth2Cache(16)
	key := testOAuthKey(0)
	now := time.Now()
	c.set(key, &IntrospectResponse{Active: true, Subject: "initial"}, now.Add(time.Hour))

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				if resp, ok := c.get(key); ok && resp.Subject == "" {
					t.Errorf("get() returned a zero-value Subject while ok=true — torn read")
				}
				c.set(key, &IntrospectResponse{Active: true, Subject: "updated"}, now.Add(time.Hour))
			}
		}(g)
	}
	wg.Wait()

	if resp, ok := c.get(key); !ok || resp.Subject != "updated" {
		t.Fatalf("final get() = (%+v, %v), want Subject=\"updated\", ok=true", resp, ok)
	}
	if len(c.entries) != 1 || c.heap.Len() != 1 {
		t.Fatalf("len(entries)=%d heap.Len()=%d, want 1/1 (re-caching the same key must never duplicate)", len(c.entries), c.heap.Len())
	}
}

// assertHeapInvariants validates, for the current (settled — caller must
// hold no concurrent writers) state of c.heap and c.entries:
//   - every entry's idx field is a valid, in-bounds position in c.heap;
//   - c.heap[e.idx] == e for every entry (idx bookkeeping is positionally
//     correct, not just numerically in range — catches a Swap/Push/Pop
//     that updates idx incorrectly without breaking Len());
//   - the array satisfies the min-heap property (every parent's expiry is
//     <= both children's expiry) — catches a Fix/Push/Pop that leaves the
//     heap size correct but its ordering corrupted, which would silently
//     break "expired/soonest entries evicted first" without ever showing
//     up as a map/heap length mismatch.
func assertHeapInvariants(t *testing.T, c *oauth2Cache) {
	t.Helper()
	n := c.heap.Len()
	if len(c.entries) != n {
		t.Fatalf("len(entries)=%d != heap.Len()=%d", len(c.entries), n)
	}
	for i, e := range c.heap {
		if e.idx != i {
			t.Fatalf("heap[%d].idx=%d — idx bookkeeping is out of sync with actual position", i, e.idx)
		}
		if got, ok := c.entries[e.key]; !ok || got != e {
			t.Fatalf("heap[%d] (key=%x) is not the same object as c.entries[key] (or is missing from the map)", i, e.key)
		}
		left, right := 2*i+1, 2*i+2
		if left < n && c.heap[left].expiry.Before(e.expiry) {
			t.Fatalf("min-heap property violated: heap[%d] (child, expiry=%v) < heap[%d] (parent, expiry=%v)",
				left, c.heap[left].expiry, i, e.expiry)
		}
		if right < n && c.heap[right].expiry.Before(e.expiry) {
			t.Fatalf("min-heap property violated: heap[%d] (child, expiry=%v) < heap[%d] (parent, expiry=%v)",
				right, c.heap[right].expiry, i, e.expiry)
		}
	}
}

// TestOAuth2Cache_HeapInvariants_UnderConcurrentChurn drives a large number
// of goroutines through a mix of new-key inserts (forcing evictOneLocked),
// same-key re-caches (forcing heap.Fix, the in-place-mutation path this
// sprint introduced), and gets, at a scale and duration intended to shake
// out any idx bookkeeping bug in Push/Pop/Swap/Fix that a mere
// heap.Len()==len(entries) check (already covered by the saturation test
// above) would miss — e.g. a Swap that updates the wrong index, or a Fix
// called with a stale idx. After the churn settles, the full positional
// and min-heap-ordering invariant is checked via assertHeapInvariants.
func TestOAuth2Cache_HeapInvariants_UnderConcurrentChurn(t *testing.T) {
	const maxSize = 64
	const goroutines = 500
	const itersPerGoroutine = 200

	c := newTestOAuth2Cache(maxSize)
	now := time.Now()
	for i := 0; i < maxSize; i++ {
		c.set(testOAuthKey(i), &IntrospectResponse{Active: true}, now.Add(time.Duration(i+1)*time.Minute))
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				switch i % 3 {
				case 0:
					// Re-cache an existing key with a fresh expiry — exercises
					// heap.Fix on an entry that may be anywhere in the heap.
					k := testOAuthKey(i % maxSize)
					c.set(k, &IntrospectResponse{Active: true}, time.Now().Add(time.Duration(g%97+1)*time.Second))
				case 1:
					// Brand-new key — forces evictOneLocked (heap.Pop) since
					// the cache is already at maxSize.
					k := testOAuthKey(maxSize + g*itersPerGoroutine + i)
					c.set(k, &IntrospectResponse{Active: true}, now.Add(time.Hour))
				default:
					c.get(testOAuthKey(i % maxSize))
				}
			}
		}(g)
	}
	wg.Wait()

	assertHeapInvariants(t, c)
}
