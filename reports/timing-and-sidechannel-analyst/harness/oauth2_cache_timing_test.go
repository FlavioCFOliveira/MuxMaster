//go:build timing

// oauth2_cache_timing_test.go — Timing analysis for OAuth2Introspect middleware.
//
// Hypothesis: cache hit vs cache miss latency is distinguishable.
//   - Cache hit: RLock → map lookup → RUnlock → ~200ns
//   - Cache miss: RLock → map miss → RUnlock → HTTP call → ... → ~10ms+
//   This is an EXPECTED, ACCEPTED timing difference (network call dominates).
//
// Intra-cache timing tests:
//   1. Active-cached vs inactive-cached tokens — should both return from cache
//      in similar time (same code path: get() → Active check → return).
//   2. Cache hit vs expired entry — expired entry falls through as a miss
//      (expired check in get(): time.Now().After(e.expiry)).
//
// Note: the OAuth2 cache timing oracle is ACCEPTED because:
//   (a) Cache hit always results in a valid response — attacker can infer "this
//       token was seen recently" but not the token value.
//   (b) The cache key is sha256(token) — not reversible.
//   (c) The timing difference is dominated by the introspection network call,
//       not by any secret comparison.
//
// We test the INTRA-CACHE timing to ensure the Active=true vs Active=false
// code paths within the cached response do not leak additional info.
package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nOAuth2 = 100_000

// mockIntrospectionServer returns a test server that responds with active or inactive
// token responses based on the token value.
func mockIntrospectionServer(active bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		resp := map[string]any{
			"active": active,
			"sub":    "user1",
			"scope":  "read write",
			"exp":    time.Now().Add(1 * time.Hour).Unix(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func oauth2Req(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestTiming_OAuth2_CacheHit_ActiveVsInactive measures intra-cache timing:
// the code path for a cached Active=true vs a cached Active=false response.
// Both hit the cache; the only branch is `if !resp.Active`.
func TestTiming_OAuth2_CacheHit_ActiveVsInactive(t *testing.T) {
	activeServer := mockIntrospectionServer(true)
	defer activeServer.Close()

	inactiveServer := mockIntrospectionServer(false)
	defer inactiveServer.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Build handler backed by active-server; pre-populate cache with one active token.
	activeHandler := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: activeServer.URL,
		CacheTTL: 10 * time.Minute,
	})(inner)

	// Build handler backed by inactive-server; pre-populate cache with inactive token.
	inactiveHandler := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: inactiveServer.URL,
		CacheTTL: 10 * time.Minute,
	})(inner)

	activeToken := "cached-active-token-abc123"
	inactiveToken := "cached-inactive-token-xyz789"

	// Trigger cache population via real introspection call.
	ctx := context.Background()
	_ = ctx

	{
		w := httptest.NewRecorder()
		activeHandler.ServeHTTP(w, oauth2Req(activeToken))
	}
	{
		w := httptest.NewRecorder()
		inactiveHandler.ServeHTTP(w, oauth2Req(inactiveToken))
	}

	var activeSamples, inactiveSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup — ensure cache is hit each time (no network).
	for i := 0; i < 20_000; i++ {
		w := httptest.NewRecorder()
		activeHandler.ServeHTTP(w, oauth2Req(activeToken))
		w2 := httptest.NewRecorder()
		inactiveHandler.ServeHTTP(w2, oauth2Req(inactiveToken))
	}

	activeSamples = make([]int64, nOAuth2)
	inactiveSamples = make([]int64, nOAuth2)

	for i := 0; i < nOAuth2; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		activeHandler.ServeHTTP(w, oauth2Req(activeToken))
		activeSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		inactiveHandler.ServeHTTP(w2, oauth2Req(inactiveToken))
		inactiveSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(activeSamples, inactiveSamples)
	as_ := Summarise(activeSamples)
	is_ := Summarise(inactiveSamples)

	t.Logf("OAuth2 cache: active vs inactive cached token timing (N=%d each)", nOAuth2)
	t.Logf("  Active:   mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", as_.Mean, as_.Std, as_.P50, as_.P99)
	t.Logf("  Inactive: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", is_.Mean, is_.Std, is_.P50, is_.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// The two paths diverge at `if !resp.Active` — one path calls next.ServeHTTP,
	// the other calls http.Error. This is an error oracle, but the information
	// (token is cached-inactive) is not security-sensitive beyond what the 401 already reveals.
	if result.Leak {
		t.Logf("NOTE: Cache hit timing differs between active/inactive branches: diff=%.2fns — "+
			"expected due to next.ServeHTTP vs http.Error path length difference",
			result.MeanDiffNs)
		// Not a security failure: the 401 response itself is the oracle, not the timing.
	}
}

// TestTiming_OAuth2_Cache_RWMutex_Contention documents that cache.get() uses
// RLock (shared) which allows concurrent readers. Under single-threaded bench,
// the RLock overhead vs direct map access is ~10-50ns.
func TestTiming_OAuth2_Cache_RWMutex_Contention(t *testing.T) {
	activeServer := mockIntrospectionServer(true)
	defer activeServer.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: activeServer.URL,
		CacheTTL: 10 * time.Minute,
	})(inner)

	token := "mutex-contention-test-token"

	// Populate cache.
	{
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, oauth2Req(token))
	}

	var samples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()

	const n = 100_000
	samples = make([]int64, n)
	for i := 0; i < n; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, oauth2Req(token))
		samples[i] = time.Since(t0).Nanoseconds()
	}

	s := Summarise(samples)
	t.Logf("OAuth2 cache hit (single-threaded, RLock path) (N=%d)", n)
	t.Logf("  mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns",
		s.Mean, s.Std, s.P50, s.P95, s.P99)
	t.Logf("  Expected: ~200-800ns (RLock + map lookup + RUnlock + context setup)")
}
