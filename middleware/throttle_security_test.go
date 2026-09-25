// Security-focused white-box regression tests for the sprint-18 rewrite of
// ThrottlePerIPCapped's key table (CH-01, sharded by hash/maphash) and
// ThrottleBacklog's semaphore (CH-02, atomic fast path + bounded channels).
//
// These tests target three adversarial hypotheses raised for this review
// that the existing concurrency-focused suite (throttle_internal_test.go,
// throttle_shard_test.go — already covered by
// reports/concurrency-security-auditor/2026-09-24-sprint18-contention-fixes.md)
// does not exercise:
//
//  1. Shard-targeting: can an attacker who knows the sharding scheme
//     concentrate keys onto a single shard and recreate the old
//     single-global-mutex bottleneck?
//  2. Seed secrecy/uniqueness: is the maphash seed actually randomised per
//     table instance, so shard assignment cannot be precomputed offline and
//     reused across deployments?
//  3. Bounded auxiliary channels: do the new queue/wake channels stay
//     exactly bounded to `backlog`, ruling out unbounded buffering as a new
//     memory-exhaustion vector?
//
// This file uses `package middleware` to reach the unexported
// throttleTable/throttleSem types directly.
package middleware

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// TestSec_ThrottleTable_SeedRandomisedPerInstance confirms newThrottleTable
// draws a fresh, non-degenerate maphash seed on every construction — the
// property CH-01's doc comment relies on ("an attacker who can influence key
// values ... cannot force every key onto the same shard by engineering hash
// collisions"). A fixed or zero seed would let an attacker precompute, once,
// a set of keys that all collide on one shard and reuse that set against
// every deployment; a per-instance random seed defeats that precomputation.
func TestSec_ThrottleTable_SeedRandomisedPerInstance(t *testing.T) {
	t1 := newThrottleTable(100)
	t2 := newThrottleTable(100)

	if t1.seed == t2.seed {
		t.Fatalf("two independently constructed throttleTables share the same maphash seed — " +
			"shard assignment would be identical (and precomputable offline) across deployments")
	}

	// The seed must actually influence the shard mapping observed for the
	// exact same key: if it did not, sharing a seed value would be harmless,
	// but a table that ignored its own seed field would defeat the whole
	// point of randomising it.
	key := "10.0.0.1"
	i1 := t1.shardFor(key)
	i2 := t2.shardFor(key)
	same := i1 == i2 // pointer identity would only coincidentally match by index
	// It's expected that different-seed tables MAY still land the same key on
	// the same shard index by chance (1/64 probability) — that alone is not a
	// defect. What matters is that this is not GUARANTEED by construction, so
	// we only assert that at least one of several distinct keys resolves to a
	// different shard-relative bucket between the two seeds, proving the seed
	// really is mixed into the hash rather than silently ignored.
	distinguished := false
	for i := 0; i < 32; i++ {
		k := fmt.Sprintf("10.0.%d.%d", i, i*7%256)
		if t1.shardFor(k) != t2.shardFor(k) {
			distinguished = true
			break
		}
	}
	if !distinguished {
		t.Fatalf("32 distinct keys resolved to identical shard buckets under two independently seeded "+
			"tables (first key same=%v) — the seed does not appear to influence shardFor's output", same)
	}
}

// TestSec_ThrottleTable_ShardDistributionResistsAdversarialKeys feeds a large
// number of attacker-controlled, highly regular keys (sequential dotted-quad
// IPv4 strings, the exact shape a real X-Forwarded-For spoofing campaign
// would produce) into shardFor and asserts the resulting shard population is
// close to uniform. Before CH-01's sharding, ALL keys funnelled through one
// global mutex regardless of value, so a maldistribution here would recreate
// (on a smaller, per-shard scale) the very bottleneck the rewrite exists to
// remove, and would let an attacker who can enumerate/guess the seed (e.g.
// via a side channel) concentrate load on a minority of shards.
func TestSec_ThrottleTable_ShardDistributionResistsAdversarialKeys(t *testing.T) {
	table := newThrottleTable(1_000_000)

	const n = 200_000
	counts := make([]int, throttleShardCount)
	for i := 0; i < n; i++ {
		// Sequential, highly structured, fully attacker-predictable keys —
		// the worst case for a naive or low-quality hash.
		key := fmt.Sprintf("10.%d.%d.%d", (i>>16)&0xff, (i>>8)&0xff, i&0xff)
		sh := table.shardFor(key)
		for idx := range table.shards {
			if sh == &table.shards[idx] {
				counts[idx]++
				break
			}
		}
	}

	mean := float64(n) / float64(throttleShardCount)
	maxCount, minCount := 0, math.MaxInt
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
		if c < minCount {
			minCount = c
		}
	}
	// A well-distributed hash over 64 shards with 200k samples should keep
	// every shard within roughly +/-15% of the mean (~3125). Allow a generous
	// 2x band before failing, since this test only needs to catch a
	// pathologically bad distribution (e.g. all keys landing on shard 0),
	// not to certify statistical hash quality — that is maphash's own
	// documented job.
	if float64(maxCount) > mean*2 {
		t.Fatalf("shard population is skewed under adversarial sequential keys: max=%d min=%d mean=%.1f "+
			"(max/mean=%.2fx) — a single shard is absorbing disproportionate load", maxCount, minCount, mean, float64(maxCount)/mean)
	}
	if minCount == 0 {
		t.Fatalf("at least one of the %d shards received zero keys out of %d adversarial samples — "+
			"suspiciously non-uniform distribution", throttleShardCount, n)
	}
}

// TestSec_ThrottleTable_SaturatedTable_ExistingKeyStillServed_OnlyNewKeysRejected
// is the black-box-adjacent regression for the documented saturation
// semantics (TM-2026-013, DOS-2026-0057, SECURITY.md "ThrottlePerIPCapped
// saturation"): when the table is at maxTableSize, an ALREADY-TRACKED key
// must keep being served (no bypass of its own per-key limit, no spurious
// rejection either), while every BRAND-NEW key is rejected immediately. The
// sharded rewrite must not change which keys this applies to.
func TestSec_ThrottleTable_SaturatedTable_ExistingKeyStillServed_OnlyNewKeysRejected(t *testing.T) {
	const maxTableSize = 8
	table := newThrottleTable(maxTableSize)

	// Fill the table to exactly capacity, keeping every entry's token held
	// (refs=1, token not returned) so it stays "in active use".
	type acquired struct {
		key string
		e   *throttleEntry
	}
	var held []acquired
	for i := 0; i < maxTableSize; i++ {
		key := fmt.Sprintf("existing-%d", i)
		ch, e, full := table.acquire(key, 2)
		if full {
			t.Fatalf("table reported full while filling to exactly maxTableSize (%d/%d)", i, maxTableSize)
		}
		<-ch // consume one of the 2 tokens, leaving the entry "in use"
		held = append(held, acquired{key: key, e: e})
	}

	// A brand-new key must be rejected — the table is exactly full.
	if _, _, full := table.acquire("brand-new-key", 2); !full {
		t.Fatalf("new key was accepted while the table was already at maxTableSize=%d — cap not enforced", maxTableSize)
	}

	// Every EXISTING key must still be acquirable (refs++, no new table slot
	// consumed) — the per-key limit must not degrade to a blanket rejection
	// just because the table itself is saturated.
	for _, h := range held {
		ch, e, full := table.acquire(h.key, 2)
		if full {
			t.Fatalf("existing key %q was rejected as if it were new — saturation must never affect already-tracked keys", h.key)
		}
		if e != h.e {
			t.Fatalf("existing key %q resolved to a different *throttleEntry — re-acquire must reuse the same entry, not create a duplicate", h.key)
		}
		select {
		case <-ch:
			// second token consumed fine; put it back for cleanup below.
			ch <- struct{}{}
		default:
			t.Fatalf("existing key %q had no token available on its second concurrent acquire (limit=2)", h.key)
		}
		table.decRefs(h.key, e) // release the re-acquire's refs
	}

	// Clean up the original holds.
	for _, h := range held {
		h.e.tokens <- struct{}{}
		table.decRefs(h.key, h.e)
	}
	if got := table.size.Load(); got != 0 {
		t.Fatalf("table.size = %d after releasing every held key, want 0", got)
	}
}

// TestSec_ThrottleSem_AuxiliaryChannelsStayBounded confirms the queue and
// wake channels introduced by CH-02's rewrite are allocated with EXACTLY the
// configured backlog capacity — neither channel can be grown at runtime, so
// there is no way for a request stream to make ThrottleBacklog buffer more
// waiter state than the operator configured (ruling out an unbounded
// memory-growth DoS vector introduced by the new channels).
func TestSec_ThrottleSem_AuxiliaryChannelsStayBounded(t *testing.T) {
	const backlog = 37
	sem := newThrottleSem(4, backlog)

	if cap(sem.queue) != backlog {
		t.Fatalf("cap(sem.queue) = %d, want exactly %d (backlog)", cap(sem.queue), backlog)
	}
	if cap(sem.wake) != backlog {
		t.Fatalf("cap(sem.wake) = %d, want exactly %d (backlog)", cap(sem.wake), backlog)
	}

	// Saturate both channels deliberately (more waiters than backlog) and
	// confirm the queue channel — the one that actually bounds concurrent
	// waiters — rejects any waiter beyond `backlog`, never blocking the
	// caller and never growing past its fixed capacity.
	var reserved int
	for i := 0; i < backlog+50; i++ {
		select {
		case sem.queue <- struct{}{}:
			reserved++
		default:
			// expected once the channel is full
		}
	}
	if reserved != backlog {
		t.Fatalf("reserved %d queue slots, want exactly %d (backlog) — queue channel is not bounded correctly", reserved, backlog)
	}
}

// TestSec_ThrottleSem_NoBypassOfConfiguredLimit_UnderRapidChurn is a tight,
// short-timeout adversarial stress: many more acquirers than the configured
// limit hammer tryAcquire/acquireWait/release back-to-back with a very small
// timeout (forcing frequent 503s and frequent re-tries), checking that
// inUse never exceeds limit even under the churn pattern most likely to
// expose a race between the atomic fast path and the channel-based slow
// path introduced by CH-02.
func TestSec_ThrottleSem_NoBypassOfConfiguredLimit_UnderRapidChurn(t *testing.T) {
	const limit = 3
	const backlog = 16
	const attackers = 500
	const rounds = 20

	sem := newThrottleSem(limit, backlog)
	done := make(chan struct{})
	violation := make(chan int64, 1)

	go func() {
		defer close(done)
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			if v := sem.inUse.Load(); v > int64(limit) {
				select {
				case violation <- v:
				default:
				}
				return
			}
		}
	}()

	results := make(chan bool, attackers*rounds)
	for a := 0; a < attackers; a++ {
		go func() {
			for r := 0; r < rounds; r++ {
				if sem.tryAcquire() {
					sem.release()
					results <- true
					continue
				}
				ok := sem.acquireWait(2 * time.Millisecond)
				if ok {
					sem.release()
				}
				results <- ok
			}
		}()
	}
	for i := 0; i < attackers*rounds; i++ {
		<-results
	}
	<-done

	select {
	case v := <-violation:
		t.Fatalf("observed inUse=%d > limit=%d during rapid-churn adversarial load — the configured limit was bypassed", v, limit)
	default:
	}
	if final := sem.inUse.Load(); final != 0 {
		t.Fatalf("inUse=%d after all attackers finished, want 0", final)
	}
}
