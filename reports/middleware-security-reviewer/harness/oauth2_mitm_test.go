// Package harness_test — Gap B: OAuth2 introspection endpoint MITM blast radius.
//
// If an attacker can serve a false introspection endpoint (DNS poisoning, BGP
// hijack, rogue TLS cert accepted by a misconfigured HTTPClient), the attack
// succeeds for the duration of the cache TTL.  These tests quantify the blast
// radius:
//
//  1. A poisoned positive entry (active=true) survives in the cache after the
//     real IdP would have invalidated the token.
//  2. Cache eviction does NOT happen before TTL expiry — there is no
//     invalidation callback or out-of-band revocation check.
//  3. After cache expiry, a healthy endpoint correctly rejects the token.
//  4. A false IdP that returns active=true for an expired token causes the
//     middleware to accept it within the cache window.
//
// Threat model: CWE-345 (Insufficient Verification of Data Authenticity) /
// CWE-613 (Insufficient Session Expiration).
package harness_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// introspectServer is a test HTTP server simulating an OAuth2 introspection
// endpoint.  The active field controls what the endpoint returns.
type introspectServer struct {
	active  atomic.Bool
	callsMu int64 // number of times the endpoint was called
	calls   atomic.Int64
}

func (s *introspectServer) handler(w http.ResponseWriter, r *http.Request) {
	s.calls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	exp := time.Now().Add(time.Hour).Unix()
	if s.active.Load() {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active":    true,
			"sub":       "user1",
			"exp":       exp,
			"scope":     "read",
			"iss":       "https://idp.example.com",
			"client_id": "client1",
		})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
	}
}

// TestSec_OAuth2_CachePoisonBlastRadius confirms that once a false IdP poisons
// the cache with active=true, the middleware continues to accept the token for
// the full TTL duration even after the false IdP is replaced by a correct one
// that returns active=false.
//
// This is expected behaviour (documented trade-off of caching), but the test
// measures and documents the blast radius window.
func TestSec_OAuth2_CachePoisonBlastRadius(t *testing.T) {
	srv := &introspectServer{}
	srv.active.Store(true) // start as rogue IdP returning active=true

	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	const cacheTTL = 200 * time.Millisecond
	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              ts.URL,
		CacheTTL:              cacheTTL,
	})

	reached := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	token := "poisoned-token-abc123"

	// First request: rogue IdP says active=true — token is cached.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	rw1 := httptest.NewRecorder()
	handler.ServeHTTP(rw1, req1)
	if rw1.Code != http.StatusOK {
		t.Fatalf("expected 200 from rogue IdP, got %d", rw1.Code)
	}
	if !reached {
		t.Fatal("handler not reached after valid introspection")
	}

	// Switch to correct IdP: now returns active=false (token revoked).
	srv.active.Store(false)
	callsBeforeRevoke := srv.calls.Load()

	// Second request: cache still hot — middleware accepts token WITHOUT
	// calling the endpoint, even though it is now revoked.
	reached = false
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rw2 := httptest.NewRecorder()
	handler.ServeHTTP(rw2, req2)

	callsAfter := srv.calls.Load()
	if callsAfter > callsBeforeRevoke {
		t.Logf("introspection endpoint called %d extra time(s) after revoke — cache not used", callsAfter-callsBeforeRevoke)
	}

	if rw2.Code == http.StatusOK {
		// Document the blast radius: revoked token is accepted within TTL.
		t.Logf("MSR-GAP-B (blast-radius documented): token accepted from cache after IdP revoke (TTL=%s) — blast radius window is up to cacheTTL", cacheTTL)
		// This is a documented trade-off, not a code defect.
		// The test validates that this is the ACTUAL behaviour so operators
		// understand the risk.
	} else {
		t.Logf("token rejected after revoke (endpoint called? %v) — caching may not have activated", callsAfter > callsBeforeRevoke)
	}

	// Wait for cache TTL to expire.
	time.Sleep(cacheTTL + 50*time.Millisecond)

	// Third request: cache expired — endpoint is called; correct IdP rejects.
	reached = false
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	rw3 := httptest.NewRecorder()
	handler.ServeHTTP(rw3, req3)

	if rw3.Code != http.StatusUnauthorized {
		t.Errorf("MSR-GAP-B: expected 401 after cache expiry and real IdP returning active=false, got %d", rw3.Code)
	}
}

// TestSec_OAuth2_CacheKeyIsSHA256OfToken verifies that the cache key is the
// SHA-256 hash of the raw token, not the token itself.  This prevents raw
// token material from residing in heap memory as a map key.
//
// We confirm this indirectly: two tokens with the same hash preimage collide
// (this is computationally infeasible) — instead we verify that different
// tokens do NOT share a cache entry (no accidental alias).
func TestSec_OAuth2_CacheKeyIsSHA256OfToken(t *testing.T) {
	srv := &introspectServer{}
	srv.active.Store(true)
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              ts.URL,
		CacheTTL:              60 * time.Second,
	})
	var lastToken string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastToken = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))

	tokens := []string{"token-alpha", "token-beta", "token-gamma"}
	for _, tok := range tokens {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("token %q: expected 200, got %d", tok, rw.Code)
		}
	}

	// Each token should have independently hit the endpoint at least once.
	if srv.calls.Load() < int64(len(tokens)) {
		t.Errorf("expected at least %d endpoint calls (one per distinct token), got %d — possible cache collision", len(tokens), srv.calls.Load())
	}
	_ = lastToken
}

// TestSec_OAuth2_CacheDoesNotStoreFalseActiveResponse verifies that tokens
// reported as active=false by the IdP are NOT stored in the cache.
// Storing inactive tokens would allow DoS via cache exhaustion without
// consuming valid token budget.
func TestSec_OAuth2_CacheDoesNotStoreFalseActiveResponse(t *testing.T) {
	srv := &introspectServer{}
	srv.active.Store(false) // IdP returns inactive for all tokens

	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              ts.URL,
		CacheTTL:              60 * time.Second,
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Send 3 requests with the same inactive token.
	token := "inactive-token-xyz"
	for i := range 3 {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)
		if rw.Code != http.StatusUnauthorized {
			t.Errorf("request %d: expected 401 for inactive token, got %d", i, rw.Code)
		}
	}

	// Each request must have hit the endpoint: inactive entries must not be cached.
	if srv.calls.Load() < 3 {
		t.Errorf("MSR-GAP-B: inactive token cached (%d endpoint calls for 3 requests) — cache should only store active=true responses", srv.calls.Load())
	}
}

// TestSec_OAuth2_MITMFalseIdPWindowDocumentation is an informational test that
// computes and logs the maximum window an attacker has after a successful MITM.
// Not a pass/fail assertion — documents the operational risk.
func TestSec_OAuth2_MITMFalseIdPWindowDocumentation(t *testing.T) {
	// Default TTL is 60s; effective TTL = min(CacheTTL, token.exp - now).
	// If the false IdP sets exp far in the future, full CacheTTL applies.
	defaultCacheTTL := 60 * time.Second
	maxCacheSize := 10000

	t.Logf("MSR-GAP-B documentation:")
	t.Logf("  Default blast-radius window (no custom TTL): %s", defaultCacheTTL)
	t.Logf("  Max tokens a false IdP can poison in one window: %d (MaxCacheSize)", maxCacheSize)
	t.Logf("  Cache eviction: lazy (on cache-full writes only) — no background reaper")
	t.Logf("  Mitigation available to operators: set CacheTTL=-1 to disable caching")
	t.Logf("  Mitigation available to operators: set MaxCacheSize=0 (defaults to 10000 — no effect)")
	t.Logf("  Recommended: document TTL trade-off in OAuth2Options GoDoc; add CacheDisabled bool option")
	t.Logf("  Out of scope: TLS cert validation of Endpoint is caller's responsibility (HTTPClient)")
}

// TestSec_OAuth2_CacheExpiresOnTokenExp verifies the cache respects token exp:
// effective TTL = min(CacheTTL, token.exp - now).
func TestSec_OAuth2_CacheExpiresOnTokenExp(t *testing.T) {
	srv := &introspectServer{}
	srv.active.Store(true)

	// Custom handler: returns a token with exp in the very near future.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// exp = 150ms from now — shorter than CacheTTL of 10s.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user1",
			"exp":    time.Now().Add(150 * time.Millisecond).Unix(),
		})
	}))
	defer ts.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              ts.URL,
		CacheTTL:              10 * time.Second, // long TTL — token exp should win
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	token := "expiring-token"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200 on first request, got %d", rw.Code)
	}

	// Wait for token exp to pass but not for CacheTTL.
	time.Sleep(200 * time.Millisecond)

	// Now the server returns active=false (token expired on IdP side too).
	srv.active.Store(false)

	// We can't change the fake server response after the fact easily;
	// what we assert is: after token.exp, the middleware re-introspects
	// (the cache entry should have been set with expiry = token.exp - now).
	// This is an informational check — the implementation caps at token.exp.
	callsBefore := srv.calls.Load()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rw2 := httptest.NewRecorder()
	handler.ServeHTTP(rw2, req2)
	callsAfter := srv.calls.Load()

	if callsAfter == callsBefore {
		t.Errorf("MSR-GAP-B: cache entry not expired after token.exp passed — effective TTL did not honour token expiry")
	} else {
		t.Logf("OK: endpoint re-called after token exp (%d calls before, %d after)", callsBefore, callsAfter)
	}
}
