// Sprint 20, rmp #286 — TM-2026-015 "oauth2Cache TTL eviction race vs
// introspect storm" (reports/overview/2026-05-07-sprint-S9.md, B1.3).
//
// Premise: when a hot token's cache entry expires, every concurrent request
// misses the cache at once and each one calls the introspection endpoint —
// an IdP amplification of N calls per TTL period.
//
// At HEAD, oauth2.go:453-483 routes every miss through the per-token
// singleflight group, so a storm that arrives while the refresh is in
// flight costs exactly one upstream call.
//
// Run with:
//
//	cd reports/dos-resilience-tester/harness && go test -race -count=1 -run TestDOS_TM_2026_015 .
package harness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func TestDOS_TM_2026_015_TTLExpiryStorm_SingleUpstreamCall(t *testing.T) {
	const (
		storm    = 256
		cacheTTL = 50 * time.Millisecond
		token    = "tm-2026-015-hot-token"
	)

	var calls atomic.Int64
	started := make(chan struct{}, 8)
	var gateMu sync.Mutex
	gate := make(chan struct{}) // closed to release the in-flight introspection
	close(gate)                 // the warm-up call is not held

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		gateMu.Lock()
		g := gate
		gateMu.Unlock()
		<-g
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true, "sub": "u1", "exp": time.Now().Add(time.Hour).Unix(),
		})
	}))
	defer idp.Close()

	var entered atomic.Int64
	m := mm.New()
	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered.Add(1)
			next.ServeHTTP(w, r)
		})
	})
	m.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              idp.URL,
		AllowInsecureEndpoint: true,
		CacheTTL:              cacheTTL,
	}))
	m.GET("/p", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/p", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)
		return rec.Code
	}

	// Warm the cache: exactly one upstream call.
	if code := do(); code != http.StatusOK {
		t.Fatalf("warm-up status %d", code)
	}
	<-started
	if c := calls.Load(); c != 1 {
		t.Fatalf("warm-up upstream calls = %d, want 1", c)
	}
	// Cache hit while fresh: no upstream call.
	if code := do(); code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("fresh-hit: status %d, upstream calls %d", code, calls.Load())
	}

	// Let the entry expire, then hold the next introspection in flight.
	time.Sleep(cacheTTL + 30*time.Millisecond)
	gateMu.Lock()
	gate = make(chan struct{})
	gateMu.Unlock()
	entered.Store(0)

	codes := make(chan int, storm)
	var wg sync.WaitGroup
	for range storm {
		wg.Go(func() { codes <- do() })
	}
	// The first miss becomes the singleflight leader and blocks in the IdP.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no introspection call after expiry")
	}
	// Wait until every storm request has entered the router, then give the
	// followers time to reach the singleflight wait (a few µs each) before
	// releasing the leader.
	deadline := time.Now().Add(5 * time.Second)
	for entered.Load() < storm {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d storm requests entered", entered.Load(), storm)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	gateMu.Lock()
	close(gate)
	gateMu.Unlock()
	wg.Wait()
	close(codes)

	for c := range codes {
		if c != http.StatusOK {
			t.Fatalf("storm request status %d, want 200", c)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d after a %d-request storm at TTL expiry, want 2 (warm-up + one coalesced refresh)", got, storm)
	}
	t.Logf("TM-2026-015: %d concurrent requests at TTL expiry -> 1 upstream refresh", storm)
}
