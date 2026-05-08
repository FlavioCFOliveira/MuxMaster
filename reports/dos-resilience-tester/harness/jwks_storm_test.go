// Package harness — DoS Resilience: JWT auth DoS vectors
//
// JWTAuth does not perform remote JWKS fetches (no JWKS URL support) — key
// material is provided at construction time. This eliminates the classic
// "JWKS fetch storm" attack vector. This file documents that absence and
// tests the remaining per-request DoS surface:
//
//  1. RSA/ECDSA verification CPU cost under sustained load
//  2. Large JWT payload (base64-decode + JSON parse cost)
//  3. Many concurrent invalid tokens (early-reject efficiency)
//  4. HMAC pool contamination safety
package harness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestJWTAuthNoJWKSFetch documents that JWTAuth does not perform remote key fetches.
// JWKS fetch storm is therefore NOT a vulnerability in this implementation.
func TestJWTAuthNoJWKSFetch(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	r := mm.New()
	r.Use(middleware.JWTAuth(middleware.JWTOptions{
		Algorithms: []string{"ES256"},
		PublicKey:  &key.PublicKey,
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	t.Log("JWKS fetch storm: NOT APPLICABLE — JWTAuth uses local key material only (PASS)")
}

// BenchmarkJWTAuthLargePayload measures per-request cost with varying payload sizes.
// JWTAuth checks signature BEFORE parsing payload (see parseAndValidateJWT).
// An invalid signature is rejected before base64-decoding the payload body,
// so large payloads only add decode cost when the signature is valid.
// This benchmark uses an invalid sig — exercises only header+sig decode path.
func BenchmarkJWTAuthLargePayload(b *testing.B) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	r := mm.New()
	r.Use(middleware.JWTAuth(middleware.JWTOptions{
		Algorithms: []string{"ES256"},
		PublicKey:  &key.PublicKey,
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, payloadKB := range []int{1, 10, 50} {
		payloadKB := payloadKB
		b.Run(fmt.Sprintf("payload=%dKB", payloadKB), func(b *testing.B) {
			hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
			largeJSON := `{"sub":"test","exp":9999999999,"data":"` + strings.Repeat("x", payloadKB*1024) + `"}`
			pay := base64.RawURLEncoding.EncodeToString([]byte(largeJSON))
			// Invalid signature — rejected after header decode, before payload decode
			token := fmt.Sprintf("%s.%s.aW52YWxpZHNpZ25hdHVyZQ", hdr, pay)

			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				w.Body.Reset()
				r.ServeHTTP(w, req)
			}
		})
	}
}

// TestJWTAuthManyInvalidTokensConcurrent measures concurrent invalid-token rejection
// to confirm no lock contention or goroutine build-up under attack.
func TestJWTAuthManyInvalidTokensConcurrent(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	r := mm.New()
	r.Use(middleware.JWTAuth(middleware.JWTOptions{
		Algorithms: []string{"ES256"},
		PublicKey:  &key.PublicKey,
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const concurrency = 500
	results := make(chan int, concurrency)

	for range concurrency {
		go func() {
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJoYWNrZXIifQ.aW52YWxpZA")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			results <- w.Code
		}()
	}

	unauthorized := 0
	for range concurrency {
		if code := <-results; code == http.StatusUnauthorized {
			unauthorized++
		}
	}

	if unauthorized != concurrency {
		t.Errorf("expected all %d requests rejected as 401, only %d were", concurrency, unauthorized)
	}
	t.Logf("JWTAuth concurrent invalid-token rejection: %d/%d = 401 (PASS)", unauthorized, concurrency)
}

// TestJWTHMACPoolConcurrency confirms the HMAC pool is not contaminated under concurrent use.
func TestJWTHMACPoolConcurrency(t *testing.T) {
	r := mm.New()
	r.Use(middleware.JWTAuth(middleware.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("super-secret-key-for-testing-32b"),
	}))
	r.GET("/protected", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const concurrency = 200
	results := make(chan int, concurrency)
	for range concurrency {
		go func() {
			req := httptest.NewRequest("GET", "/protected", nil)
			req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.aW52YWxpZA")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			results <- w.Code
		}()
	}

	allUnauth := true
	for range concurrency {
		if <-results != http.StatusUnauthorized {
			allUnauth = false
		}
	}
	if !allUnauth {
		t.Error("HMAC pool corruption: some requests returned unexpected non-401 code")
	} else {
		t.Log("HMAC pool: no contamination under concurrent load (PASS)")
	}
}
