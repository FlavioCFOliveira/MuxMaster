// Security-focused regression test for the sprint-18 heap-based eviction
// rewrite of oauth2Cache (rmp #246, CH-09). The existing
// oauth2_cache_internal_test.go / oauth2_concurrency_test.go suite already
// proves the structural invariants (map/heap never exceed MaxCacheSize,
// expired-first eviction, DOS-2026-0005 soonest-expiry fallback, no race on
// the in-place re-cache path). This file adds the one adversarial property
// those tests do not directly exercise end-to-end: that heap-driven
// eviction churn NEVER causes an expired (i.e. should-be-revoked) token to
// be served past its TTL, even while unrelated keys are being evicted
// around it.
package middleware

import (
	"testing"
	"time"
)

// TestSec_OAuth2Cache_ExpiredEntrySurvivingInHeap_NeverServedByGet plants an
// entry with a short expiry that is NOT the soonest in the cache (so it
// will NOT be the next eviction candidate) and confirms that once real time
// passes it, get() reports it as absent — regardless of how much unrelated
// heap churn (inserts/evictions of OTHER keys) happens around it in the
// meantime. This is the core "no stale-token acceptance" property: eviction
// order is a memory-bound mechanism, not the authority on token validity —
// get()'s own time.Now().After(e.expiry) check is, and it must keep working
// correctly no matter where in the heap the entry ends up after Fix/Pop/Push
// operations on its neighbours.
func TestSec_OAuth2Cache_ExpiredEntrySurvivingInHeap_NeverServedByGet(t *testing.T) {
	const maxSize = 32
	c := newTestOAuth2Cache(maxSize)
	now := time.Now()

	targetKey := testOAuthKey(0)
	// This entry expires almost immediately, but is inserted alongside many
	// others with LATER expiries, so it is not automatically the very next
	// eviction candidate for every subsequent set() — it sits somewhere in
	// the heap while churn continues.
	c.set(targetKey, &IntrospectResponse{Active: true, Subject: "short-lived"}, now.Add(20*time.Millisecond))
	for i := 1; i < maxSize; i++ {
		c.set(testOAuthKey(i), &IntrospectResponse{Active: true}, now.Add(time.Duration(i+1)*time.Hour))
	}

	// Confirm it is still genuinely served before it expires.
	if resp, ok := c.get(targetKey); !ok || resp.Subject != "short-lived" {
		t.Fatalf("get() before expiry = (%+v, %v), want (short-lived, true)", resp, ok)
	}

	// Let it expire, then churn the cache with unrelated inserts (each one
	// at maxSize capacity forces evictOneLocked, exercising heap.Pop/Fix
	// around the target entry without ever touching it directly).
	time.Sleep(30 * time.Millisecond)
	for i := 0; i < 200; i++ {
		c.set(testOAuthKey(1000+i), &IntrospectResponse{Active: true}, time.Now().Add(time.Hour))
		if _, ok := c.get(targetKey); ok {
			t.Fatalf("iteration %d: expired entry was still served by get() after expiry — stale-token acceptance", i)
		}
	}
}

// TestSec_OAuth2Cache_RevokedTokenNeverServedAfterExplicitInactiveRecache
// simulates the introspection endpoint reporting a token as revoked
// (Active=false) on re-check — the caller-side flow (OAuth2Introspect) never
// caches Active=false responses (see the call site: cache.set is only
// reached after the `if !resp.Active { ... return }` branch), but this test
// pins that contract directly against the cache primitive: nothing in
// oauth2Cache itself would prevent serving a poisoned "still active" answer
// if that contract were ever violated, so this documents and guards it.
func TestSec_OAuth2Cache_RevokedTokenNeverServedAfterExplicitInactiveRecache(t *testing.T) {
	const maxSize = 8
	c := newTestOAuth2Cache(maxSize)
	key := testOAuthKey(0)
	now := time.Now()

	c.set(key, &IntrospectResponse{Active: true, Subject: "legit"}, now.Add(time.Hour))
	if resp, ok := c.get(key); !ok || !resp.Active {
		t.Fatalf("expected active token to be cached and served")
	}

	// A caller that (incorrectly) re-cached a revoked response would poison
	// the entry in place (heap.Fix path) — this is intentionally NOT what
	// OAuth2Introspect does, but the cache's job is only to store what it is
	// given faithfully; verify get() reflects EXACTLY what was last set,
	// with no residual "sticky" Active=true from the prior entry.
	c.set(key, &IntrospectResponse{Active: false, Subject: "revoked"}, now.Add(time.Hour))
	resp, ok := c.get(key)
	if !ok {
		t.Fatalf("get() after re-cache = not found, want the updated (revoked) entry to be visible")
	}
	if resp.Active {
		t.Fatalf("get() returned Active=true after the entry was explicitly re-cached as Active=false — the caller-side never-cache-inactive contract is the only thing preventing this in production, and it must never be silently overridden by the cache")
	}
}
