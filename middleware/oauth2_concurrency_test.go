// Black-box concurrency regression tests for rmp task #246 (sprint 18):
// exercise OAuth2Introspect end-to-end (real HTTP dispatch, real
// singleflight coalescing, real heap-backed cache eviction) under heavy
// concurrent load, at once. Run with -race: these are designed to surface
// any interaction between the singleflight group and the heap/map rewrite
// that a purely internal, single-cache-instance test could miss (e.g. the
// cache being populated by MULTIPLE singleflight followers racing to call
// set() for the same key after the leader's call completes — see the
// MSR-2026-0071 comment in oauth2.go's set()).
package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestOAuth2Introspect_ConcurrentSameToken_NoRace drives many goroutines
// through OAuth2Introspect with the EXACT SAME bearer token simultaneously.
// This is the scenario that produced the real data race fixed in this
// sprint (get() reading e.resp/e.expiry after releasing its RLock, racing
// set()'s new in-place mutation): every request after the first should be
// served from cache or coalesced via singleflight, and every one of the
// N singleflight followers independently calls cache.set() with the shared
// response once do() returns, all racing to update (not duplicate) the
// SAME cache entry.
func TestOAuth2Introspect_ConcurrentSameToken_NoRace(t *testing.T) {
	var hits int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(2 * time.Millisecond) // widen the singleflight coalescing window
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer idp.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              idp.URL,
		AllowInsecureEndpoint: true,
		MaxCacheSize:          16,
		CacheTTL:              time.Hour,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const goroutines = 300
	const waves = 20

	var wg sync.WaitGroup
	var failures int64
	for wave := 0; wave < waves; wave++ {
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer same-token-for-all")
				rec := httptest.NewRecorder()
				wrapped.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
				}
			}()
		}
		wg.Wait()
	}

	if failures != 0 {
		t.Fatalf("%d/%d requests failed for the same token", failures, goroutines*waves)
	}
}

// TestOAuth2Introspect_ConcurrentManyTokens_HeapEvictionUnderLoad drives
// concurrent requests across far more distinct tokens than MaxCacheSize —
// forcing continuous heap-backed eviction (evictOneLocked / heap.Pop) on
// every cache-full set() — interleaved with repeated re-requests of an
// already-cached token (heap.Fix) and concurrent reads, at high goroutine
// counts, to catch any heap corruption or map/cache desync under real HTTP
// dispatch (as opposed to the direct internal-API churn already covered by
// the internal test file).
func TestOAuth2Introspect_ConcurrentManyTokens_HeapEvictionUnderLoad(t *testing.T) {
	var hits int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer idp.Close()

	const maxCacheSize = 32
	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              idp.URL,
		AllowInsecureEndpoint: true,
		MaxCacheSize:          maxCacheSize,
		CacheTTL:              time.Hour,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const goroutines = 800
	const distinctTokens = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var failures int64
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				tok := fmt.Sprintf("tok-%d", (g*37+i*13)%distinctTokens)
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				rec := httptest.NewRecorder()
				wrapped.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if failures != 0 {
		t.Fatalf("%d requests failed under heap-eviction load", failures)
	}
}
