// Package harness — DoS Resilience: OAuth2 cache stampede and stale-read analysis
//
// Sprint hypothesis #5: under high-read / low-write load with all entries expired,
// reads still return stale Active:true entries until next write triggers eviction.
// Additionally: no singleflight means concurrent misses fan-out to the introspection
// endpoint — N concurrent requests for the same uncached token fire N HTTP calls.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestOAuth2CacheStampedeNoCaching verifies that N concurrent requests for the
// same token (not yet cached) all independently hit the introspection endpoint.
// This is the stampede: no singleflight coalescing.
//
// Finding DOS-2026-0004: without singleflight, a token cache miss under N
// concurrent requests causes N upstream HTTP calls. An attacker can trigger
// this by sending N requests with the same valid-but-uncached token, exhausting
// the introspection endpoint's capacity.
func TestOAuth2CacheStampedeNoCaching(t *testing.T) {
	var callCount atomic.Int64

	// Mock introspection endpoint
	introspectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(5 * time.Millisecond) // simulate latency
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user1",
			"scope":  "read:all",
			"exp":    time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer introspectServer.Close()

	r := mm.New()
	r.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              introspectServer.URL,
		CacheTTL:              60 * time.Second,
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const concurrency = 20
	token := "valid-token-same-for-all-requests"

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}()
	}
	wg.Wait()

	introspectCalls := callCount.Load()
	t.Logf("Introspection calls for %d concurrent requests, same token: %d", concurrency, introspectCalls)

	if introspectCalls > 1 {
		t.Logf("WARNING DOS-2026-0004: Cache stampede confirmed: %d concurrent requests for uncached token "+
			"caused %d introspection calls. Without singleflight, this allows an attacker to amplify "+
			"load on the introspection endpoint by factor of %d with just %d concurrent requests. "+
			"Fix: wrap doIntrospect in singleflight.Group keyed by sha256(token).",
			concurrency, introspectCalls, introspectCalls, concurrency)
	}

	// After stampede: subsequent request must be served from cache (1 introspect call max)
	callCount.Store(0)
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if callCount.Load() > 0 {
		t.Errorf("cache miss after stampede: expected cache hit, got %d introspect calls", callCount.Load())
	} else {
		t.Log("Cache correctly populated after stampede (PASS)")
	}
}

// TestOAuth2StaleReadAfterExpiry verifies the stale-read window:
// Under sustained reads with no new writes, expired entries remain valid until
// a write triggers evictExpiredLocked.
//
// Sprint §2.1#5: cache.get() checks time.Now().After(e.expiry) on every read —
// confirm no off-by-one allows serving a stale entry.
func TestOAuth2StaleReadAfterExpiry(t *testing.T) {
	var callCount atomic.Int64

	introspectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user1",
			"exp":    time.Now().Add(100 * time.Millisecond).Unix(), // very short TTL
		})
	}))
	defer introspectServer.Close()

	r := mm.New()
	r.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              introspectServer.URL,
		CacheTTL:              100 * time.Millisecond, // 100ms TTL
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	token := "short-lived-token"
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	// First request: cache miss, populate cache
	r.ServeHTTP(w, req)
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 introspect call, got %d", callCount.Load())
	}

	// Wait for TTL to expire
	time.Sleep(200 * time.Millisecond)

	// Second request: cache should be stale → must trigger re-introspection
	callCount.Store(0)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if callCount.Load() == 0 {
		t.Errorf("STALE READ: cache served expired entry without re-introspecting. " +
			"Time.Now().After(e.expiry) check in cache.get() may have an off-by-one or is not evaluated.")
	} else {
		t.Logf("Stale read correctly handled: re-introspected after TTL expiry (PASS)")
	}
}

// TestOAuth2MaxCacheSizeExhaustion verifies that when the cache is full of
// unexpired entries, new tokens are DROPPED (not cached) rather than evicting
// valid entries. This tests the DoS angle: attacker floods with distinct tokens
// to fill the cache, then legitimate tokens are never cached.
func TestOAuth2MaxCacheSizeExhaustion(t *testing.T) {
	const maxSize = 10
	var callCount atomic.Int64

	introspectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "attacker",
			"exp":    time.Now().Add(time.Hour).Unix(), // long TTL — fill cache permanently
		})
	}))
	defer introspectServer.Close()

	r := mm.New()
	r.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              introspectServer.URL,
		CacheTTL:              time.Hour,
		MaxCacheSize:          maxSize,
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Fill cache to capacity with distinct attacker tokens
	for i := range maxSize {
		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer attacker-token-%d", i))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	callCount.Store(0)

	// Now legitimate user token arrives — cache is full
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer legit-user-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	firstCall := callCount.Load()
	callCount.Store(0)

	// Second request with same legit token — should be in cache (if cache accepted it)
	// or requires another introspect call (if cache rejected it — DoS condition)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	secondCall := callCount.Load()

	t.Logf("Cache full: legit token first=%d introspect calls, second=%d", firstCall, secondCall)

	if secondCall > 0 {
		t.Logf("WARNING DOS-2026-0005: Cache-full DoS: when cache is at MaxCacheSize with unexpired "+
			"attacker tokens, legitimate tokens are never cached → every request requires introspection. "+
			"Attack: flood with %d distinct long-lived tokens → legitimate users pay O(1) introspect/req. "+
			"Fix: implement LRU/LFU eviction or a periodic background expiry goroutine.", maxSize)
	} else {
		t.Logf("Legit token correctly cached after eviction on full cache (PASS)")
	}
}

// TestOAuth2CacheSingleflightHypothesis documents the singleflight fix recommendation.
// TestDOS_OAuth2CacheStampede is the regression guard for DOS-OAUTH2-001
// (rmp #8): N concurrent requests for the same uncached token must coalesce
// into exactly 1 upstream introspection call (singleflight). After the
// cache is populated subsequent reads stay at 0 new calls, and N concurrent
// requests for N distinct tokens fan out to N calls (proving the
// coalescing key is per-token, not global).
func TestDOS_OAuth2CacheStampede(t *testing.T) {
	var callCount atomic.Int64
	introspectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    r.FormValue("token"),
			"scope":  "read:all",
			"exp":    time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer introspectServer.Close()

	r := mm.New()
	r.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              introspectServer.URL,
		CacheTTL:              60 * time.Second,
	}))
	var hitCount atomic.Int64
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		hitCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	// Phase 1: 100 concurrent goroutines, single token, cold cache.
	const concurrency = 100
	token := "stampede-token-1"
	callCount.Store(0)
	hitCount.Store(0)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if got := callCount.Load(); got != 1 {
		t.Fatalf("cold-cache singleflight: expected exactly 1 introspect call for %d concurrent requests, got %d",
			concurrency, got)
	}
	if got := hitCount.Load(); got != int64(concurrency) {
		t.Errorf("expected %d successful handler hits, got %d", concurrency, got)
	}

	// Phase 2: subsequent reads must hit the cache, no new upstream calls.
	callCount.Store(0)
	for range 50 {
		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	if got := callCount.Load(); got != 0 {
		t.Errorf("warm-cache: expected 0 introspect calls, got %d", got)
	}

	// Phase 3: 100 distinct tokens, cold cache → exactly 100 calls.
	callCount.Store(0)
	hitCount.Store(0)
	wg = sync.WaitGroup{}
	for i := range concurrency {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer distinct-token-%d", i))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}(i)
	}
	wg.Wait()
	if got := callCount.Load(); got != int64(concurrency) {
		t.Fatalf("distinct-tokens fan-out: expected %d introspect calls, got %d",
			concurrency, got)
	}

	_ = context.Background() // suppress unused import (used by other tests)
}
