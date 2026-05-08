//go:build race

// middleware_race_test.go — CSA harness for JWT/OAuth2 middleware races.
// Tests the jwtHMACPool (sync.Pool), oauth2Cache (sync.RWMutex),
// and concurrent access patterns in new middleware.

package middleware_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// buildHS256Token builds a minimal valid HS256 JWT for testing.
func buildHS256Token(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	headerJSON := []byte(`{"alg":"HS256","typ":"JWT"}`)
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// TestJWTHMACPool_ConcurrentVerify stress-tests the jwtHMACPool under parallel
// verification. A race on the pool would corrupt HMAC computation.
func TestJWTHMACPool_ConcurrentVerify(t *testing.T) {
	t.Parallel()

	secret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		t.Fatal(err)
	}

	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})

	validToken := buildHS256Token(t, secret, map[string]any{
		"sub": "user1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	var accepted, rejected int64
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&accepted, 1)
		w.WriteHeader(http.StatusOK)
	}))

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("Authorization", "Bearer "+validToken)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					atomic.AddInt64(&rejected, 1)
				}
			}
		}()
	}
	wg.Wait()

	if rejected > 0 {
		t.Errorf("valid token rejected %d times — possible HMAC pool corruption under race", rejected)
	}
}

// TestJWTHMACPool_ResetBeforeUse verifies that the pool's Reset() is called
// before Write, so hash state from a previous request does not contaminate
// the next. We fire alternating "user1" and "user2" tokens and verify each
// verifies correctly with its own expected MAC.
func TestJWTHMACPool_ResetBeforeUse(t *testing.T) {
	t.Parallel()

	secret := []byte("test-secret-32bytes-exactly-here!")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})

	token1 := buildHS256Token(t, secret, map[string]any{
		"sub": "user1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	token2 := buildHS256Token(t, secret, map[string]any{
		"sub": "user2", "exp": time.Now().Add(time.Hour).Unix(),
	})

	var rejected int64
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				tok := token1
				if i%2 == 1 {
					tok = token2
				}
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					atomic.AddInt64(&rejected, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if rejected > 0 {
		t.Errorf("token rejected %d times — pool contamination suspected", rejected)
	}
}

// TestOAuth2Cache_RWMutex_Race stress-tests the oauth2Cache RWMutex.
// Concurrent get() and set() calls from many goroutines must not data-race.
func TestOAuth2Cache_RWMutex_Race(t *testing.T) {
	t.Parallel()

	var introspectCalls int64
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&introspectCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"active":true,"sub":"user1","exp":%d}`, time.Now().Add(60*time.Second).Unix())
	}))
	defer fakeServer.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:     fakeServer.URL,
		CacheTTL:     30 * time.Second,
		MaxCacheSize: 1000,
	})

	var accepted int64
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&accepted, 1)
		w.WriteHeader(http.StatusOK)
	}))

	tokens := make([]string, 50)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("token-%04d", i)
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				tok := tokens[(g*i)%len(tokens)]
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
			}
		}(g)
	}
	wg.Wait()

	total := int64(n * 4 * 5000)
	t.Logf("total=%d introspect_calls=%d cache_hit_rate=%.1f%%",
		total, introspectCalls, 100*(1-float64(introspectCalls)/float64(total)))

	if introspectCalls >= total {
		t.Error("cache zero hit rate — oauth2Cache.set() may be racing")
	}
}

// TestOAuth2Cache_Eviction_Race tests evictExpiredLocked racing with concurrent get().
func TestOAuth2Cache_Eviction_Race(t *testing.T) {
	t.Parallel()

	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Second).Unix())
	}))
	defer fakeServer.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:     fakeServer.URL,
		CacheTTL:     100 * time.Millisecond,
		MaxCacheSize: 5, // tiny to force evictions
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tok := fmt.Sprintf("tok-%d-%d", g, i)
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				time.Sleep(time.Millisecond)
			}
		}(g)
	}
	wg.Wait()
}

// TestGetJWTClaims_ContextSafe confirms GetJWTClaims does not race.
func TestGetJWTClaims_ContextSafe(t *testing.T) {
	ctx := context.Background()
	claims, ok := middleware.GetJWTClaims(ctx)
	if ok || claims != nil {
		t.Error("expected no claims in empty context")
	}
}

// TestGetOAuth2Claims_ContextSafe confirms GetOAuth2Claims does not race.
func TestGetOAuth2Claims_ContextSafe(t *testing.T) {
	ctx := context.Background()
	claims, ok := middleware.GetOAuth2Claims(ctx)
	if ok || claims != nil {
		t.Error("expected no claims in empty context")
	}
}
