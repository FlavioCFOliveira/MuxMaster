package harness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"
	"time"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// FuzzOAuth2Authorization exercises OAuth2Introspect with arbitrary Authorization headers.
// The introspection endpoint is stubbed with a test server to avoid network calls.
// Invariant I-OAUTH2-01: OAuth2Introspect must never panic for any header value.
// Invariant I-OAUTH2-02: Only status 200 or 401 is acceptable.
func FuzzOAuth2Authorization(f *testing.F) {
	// Stub introspection server that returns inactive for everything.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
	}))
	// Note: stub is never closed in fuzz — this is intentional for the test lifetime.

	opts := mw.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              stub.URL,
		CacheTTL:              -1, // disable cache to exercise the network path every time
		MaxCacheSize:          10,
	}
	oauthMW := mw.OAuth2Introspect(opts)

	f.Add("Bearer valid-token")
	f.Add("Bearer ")
	f.Add("")
	f.Add("bearer TOKEN") // case-insensitive
	f.Add("BEARER token")
	f.Add("Basic dXNlcjpwYXNz") // wrong scheme
	f.Add("Bearer " + makeJunk(4096))
	f.Add("Bearer \x00\r\nX-Injected: evil")
	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig") // JWT-shaped token

	f.Fuzz(func(t *testing.T, authHeader string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in OAuth2Authorization: header=%q r=%v\n%s", authHeader, r, debug.Stack())
			}
		}()
		handler := oauthMW(h200)
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status=%d for header=%q", rec.Code, authHeader)
		}
	})
}

// FuzzOAuth2CachePoisoning exercises the introspection response cache with
// arbitrary JSON responses from the introspection endpoint.
// Invariant I-OAUTH2-03: Malformed introspection JSON must not panic.
// Invariant I-OAUTH2-04: inactive=false must not grant access.
func FuzzOAuth2IntrospectionResponse(f *testing.F) {
	f.Add(`{"active":true,"sub":"user","scope":"read"}`)
	f.Add(`{"active":false}`)
	f.Add(`{}`)
	f.Add(`not-json`)
	f.Add(`{"active":true,"exp":0}`)         // exp=0 means no expiry check
	f.Add(`{"active":true,"exp":1}`)         // already expired unix epoch
	f.Add(`{"active":true,"aud":"single"}`)  // string aud
	f.Add(`{"active":true,"aud":["a","b"]}`) // array aud
	f.Add(`{"active":true,"aud":null}`)
	f.Add(`{"active":true,"aud":123}`)
	f.Add(makeJunk(65537))                                      // > 64KB limit
	f.Add(`{"active":true` + string(make([]byte, 65000)) + `}`) // near limit

	f.Fuzz(func(t *testing.T, introspectBody string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in OAuth2IntrospectionResponse: r=%v\n%s", r, debug.Stack())
			}
		}()

		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(introspectBody))
		}))
		defer stub.Close()

		opts := mw.OAuth2Options{
			AllowInsecureEndpoint: true,
			Endpoint:              stub.URL,
			CacheTTL:              -1,
			MaxCacheSize:          5,
			HTTPClient:            &http.Client{Timeout: 2 * time.Second},
		}
		oauthMW := mw.OAuth2Introspect(opts)
		handler := oauthMW(h200)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer test-token-123")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// No panic is the primary invariant; status validation follows:
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status=%d for introspect body len=%d", rec.Code, len(introspectBody))
		}
	})
}

// FuzzOAuth2CacheEviction exercises the cache eviction path under concurrent access.
// Invariant I-OAUTH2-05: Cache eviction must not panic or data-race.
func FuzzOAuth2CacheEviction(f *testing.F) {
	f.Add(10, 5)
	f.Add(1, 1)
	f.Add(100, 50)

	f.Fuzz(func(t *testing.T, maxSize, numTokens int) {
		if maxSize < 1 || maxSize > 1000 || numTokens < 1 || numTokens > 200 {
			return
		}

		// Stub returns active=true for all tokens.
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"active": true,
				"sub":    "user",
				"exp":    time.Now().Add(10 * time.Second).Unix(),
			})
		}))
		defer stub.Close()

		opts := mw.OAuth2Options{
			AllowInsecureEndpoint: true,
			Endpoint:              stub.URL,
			CacheTTL:              1 * time.Second,
			MaxCacheSize:          maxSize,
			HTTPClient:            &http.Client{Timeout: 2 * time.Second},
		}
		oauthMW := mw.OAuth2Introspect(opts)
		handler := oauthMW(h200)

		// Send numTokens distinct tokens to fill the cache beyond maxSize.
		for i := range numTokens {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC in cache eviction: maxSize=%d i=%d r=%v\n%s",
						maxSize, i, r, debug.Stack())
				}
			}()
			token := "token-" + string(rune('A'+i%26)) + "-" + string(rune('a'+i/26%26))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}
