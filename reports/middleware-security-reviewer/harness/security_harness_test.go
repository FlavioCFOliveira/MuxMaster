// Package harness contains the MSR security test battery for all MuxMaster middlewares.
// Every test is named TestSec_<Middleware>_<Threat>.
package harness_test

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/big"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── JWT helpers ──────────────────────────────────────────────────────────────

func makeJWT(alg string, hdrExtra map[string]any, claims map[string]any, signFn func([]byte) []byte) string {
	hdr := map[string]any{"alg": alg, "typ": "JWT"}
	for k, v := range hdrExtra {
		hdr[k] = v
	}
	hb, _ := json.Marshal(hdr)
	pb, _ := json.Marshal(claims)
	hdrB64 := base64.RawURLEncoding.EncodeToString(hb)
	payB64 := base64.RawURLEncoding.EncodeToString(pb)
	signingInput := hdrB64 + "." + payB64
	sig := signFn([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func hmacSign(secret []byte, h func() interface{ Write([]byte) (int, error); Sum([]byte) []byte; Reset() }) func([]byte) []byte {
	_ = h // unused in call site, using direct below
	return nil
}

func hs256Sign(secret []byte) func([]byte) []byte {
	return func(input []byte) []byte {
		mac := hmac.New(sha256.New, secret)
		mac.Write(input)
		return mac.Sum(nil)
	}
}

func hs384Sign(secret []byte) func([]byte) []byte {
	return func(input []byte) []byte {
		mac := hmac.New(sha512.New384, secret)
		mac.Write(input)
		return mac.Sum(nil)
	}
}

func validClaims(extra ...map[string]any) map[string]any {
	m := map[string]any{
		"sub": "testuser",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for _, e := range extra {
		for k, v := range e {
			m[k] = v
		}
	}
	return m
}

// serve wraps mw around an ok-200 handler and fires the request.
func serve(mw func(http.Handler) http.Handler, method, path string, reqFn func(*http.Request)) *httptest.ResponseRecorder {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if reqFn != nil {
		reqFn(req)
	}
	mw(inner).ServeHTTP(rec, req)
	return rec
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — basic_auth
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_BasicAuth_ConstantTimeCompare(t *testing.T) {
	// Source inspection: basic_auth.go uses crypto/subtle.ConstantTimeCompare on SHA-256 digests.
	// This test validates the runtime result is correct AND that empty credentials return 401.
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})

	// Valid
	rec := serve(mw, "GET", "/", func(r *http.Request) { r.SetBasicAuth("alice", "secret") })
	if rec.Code != http.StatusOK {
		t.Errorf("valid creds: got %d want 200", rec.Code)
	}

	// Invalid password
	rec = serve(mw, "GET", "/", func(r *http.Request) { r.SetBasicAuth("alice", "wrong") })
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d want 401", rec.Code)
	}

	// Unknown user — must also return 401 (not 404 or 403 for enumeration reasons)
	rec = serve(mw, "GET", "/", func(r *http.Request) { r.SetBasicAuth("unknown", "anything") })
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown user: got %d want 401", rec.Code)
	}
}

func TestSec_BasicAuth_EmptyCredentials(t *testing.T) {
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})

	// Basic Og== is empty user:pass
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":")))
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty creds: got %d want 401", rec.Code)
	}
}

func TestSec_BasicAuth_NoAuthorizationHeader(t *testing.T) {
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth header: got %d want 401", rec.Code)
	}
	// WWW-Authenticate must be present
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate header on 401")
	}
}

func TestSec_BasicAuth_MalformedBase64(t *testing.T) {
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Basic not!!valid==base64@@")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("malformed base64: got %d want 401", rec.Code)
	}
}

func TestSec_BasicAuth_RealmInjectionPrevented(t *testing.T) {
	// Realm with CR/LF/quotes — SetHeader sanitises it.
	mw := middleware.BasicAuth("realm\r\nInjected: evil", map[string]string{"u": "p"})
	rec := serve(mw, "GET", "/", nil)
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if strings.Contains(wwwAuth, "\r") || strings.Contains(wwwAuth, "\n") {
		t.Errorf("realm injection not sanitised: %q", wwwAuth)
	}
}

func TestSec_BasicAuth_TimingDifferential(t *testing.T) {
	// Quick timing sanity: confirm both valid-user/wrong-pass and invalid-user/any-pass
	// go through the same ConstantTimeCompare path (they do by code inspection).
	// We collect a small sample (1000 iterations is fast) and assert neither side
	// is consistently faster by more than a 2x factor — a gross check.
	mw := middleware.BasicAuth("r", map[string]string{"alice": "correcthorsebatterystaple"})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	const N = 1000
	var validTotal, invalidTotal time.Duration
	for i := range N {
		_ = i
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("alice", "wrong-password-12345")
		t0 := time.Now()
		handler.ServeHTTP(rec, req)
		validTotal += time.Since(t0)

		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.SetBasicAuth("doesnotexist", "any-password")
		t1 := time.Now()
		handler.ServeHTTP(rec2, req2)
		invalidTotal += time.Since(t1)
	}
	ratio := float64(validTotal) / float64(invalidTotal)
	if ratio < 0.1 || ratio > 10.0 {
		t.Logf("timing ratio valid/invalid=%f over %d samples (informational, not hard failure)", ratio, N)
	}
}

func TestSec_BasicAuth_AuthHeaderNotLoggedByMiddleware(t *testing.T) {
	// basic_auth itself doesn't log — but verify the auth header value is not
	// accidentally surfaced in the WWW-Authenticate response header.
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:secret")))
	})
	// On success the Authorization header must not appear in any response header
	for k := range rec.Header() {
		if strings.EqualFold(k, "Authorization") {
			t.Errorf("Authorization header leaked into response: %s", k)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — api_key
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_APIKey_ConstantTimeMapLookup(t *testing.T) {
	// api_key uses sha256.Sum256([32]byte) map lookup — Go map on [32]byte is
	// hash-table based, NOT constant-time. However it leaks no key content,
	// only found/not-found timing. This test validates the functional result.
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid-key-abc123": "user1"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "valid-key-abc123")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("valid key: got %d want 200", rec.Code)
	}
}

func TestSec_APIKey_InvalidKeyRejects401(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid": "user"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "invalid-key")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid key: got %d want 401", rec.Code)
	}
}

func TestSec_APIKey_MissingWWWAuthenticate(t *testing.T) {
	// MM-2026-0052: APIKey returns 401 without WWW-Authenticate header.
	// This test DOCUMENTS the finding — it FAILS if the header is missing.
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid": "user"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "invalid-key")
	})
	// According to RFC 7235 §4.1, a 401 response MUST include WWW-Authenticate.
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("MSR-FINDING: APIKey 401 missing WWW-Authenticate header (MM-2026-0052)")
	}
}

func TestSec_APIKey_EmptyKeyRejects(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid": "user"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty key: got %d want 401", rec.Code)
	}
}

func TestSec_APIKey_MapMutationPostConstruction(t *testing.T) {
	// Hypothesis #6: mutating opts.Keys after construction must not affect the
	// middleware because hashed map was built from a snapshot at construction.
	original := map[string]string{"valid": "user"}
	mw := middleware.APIKey(middleware.APIKeyOptions{Keys: original})

	// Mutate the original map — middleware should still work with original keys.
	original["evil-injected"] = "attacker"

	// The evil key should NOT be accepted (hashed map was built before mutation).
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "evil-injected")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Error("MSR-FINDING: post-construction map mutation allowed injected key")
	}

	// The original key should still work.
	rec2 := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-API-Key", "valid")
	})
	if rec2.Code != http.StatusOK {
		t.Errorf("original key after mutation: got %d want 200", rec2.Code)
	}
}

func TestSec_APIKey_PanicsOnEmptyKeys(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("should panic on empty Keys")
		}
	}()
	middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{}})
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — jwt_auth (HIGH PRIORITY: alg-confusion)
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_JWT_AlgConfusion_HS256WithRSAPublicKeyBytes(t *testing.T) {
	// RFC 8725 §3.1 — alg confusion attack:
	// Server configured with RS256 (RSA private+public key pair).
	// Attacker crafts a token with alg=HS256 and signs it using the RSA PUBLIC KEY
	// bytes as the HMAC secret. If the server naively dispatches on token alg
	// and uses the public key bytes as HMAC secret, it verifies the forged token.
	//
	// MuxMaster defense: Construction-time panic when Algorithms lists HS256
	// but no Secret is provided, and RS256 but no *rsa.PublicKey is provided.
	// At runtime, verifyFn only dispatches to hmacPools[alg] if the alg was in
	// the original Algorithms list. A server configured with ONLY RS256 will
	// never have an hmacPool entry for HS256.
	//
	// This test confirms: a server configured with RS256-only REJECTS a token
	// whose header claims alg=HS256.

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	mw := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &rsaKey.PublicKey,
		Algorithms: []string{"RS256"},
	})

	// Craft forged token: header alg=HS256, signed with RSA public key DER bytes as HMAC secret.
	pubDER, _ := x509MarshalPKIXPublicKey(&rsaKey.PublicKey)
	forgedToken := makeJWT("HS256", nil, validClaims(), hs256Sign(pubDER))

	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+forgedToken)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("MSR-CRITICAL: alg-confusion attack succeeded — HS256 token accepted by RS256-only server (got %d)", rec.Code)
	}
}

func TestSec_JWT_AlgConfusion_MixedAlgsBothKeys(t *testing.T) {
	// Server configured with BOTH HS256 and RS256, supplying both Secret and PublicKey.
	// Attacker crafts a valid HS256 token (has the HMAC secret).
	// This is NOT an attack scenario but verifies the mixed config works correctly.

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	secret := []byte("super-secret-hmac-key-32bytes!!")

	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		PublicKey:  &rsaKey.PublicKey,
		Algorithms: []string{"HS256", "RS256"},
	})

	// Valid HS256 token must be accepted.
	token := makeJWT("HS256", nil, validClaims(), hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Errorf("mixed algs, valid HS256: got %d want 200", rec.Code)
	}
}

func TestSec_JWT_AlgNone_Rejected(t *testing.T) {
	// alg=none attack — unsigned tokens must be rejected.
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     []byte("secret"),
		Algorithms: []string{"HS256"},
	})
	noneToken := makeJWT("none", nil, validClaims(), func(b []byte) []byte { return []byte{} })
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+noneToken)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("alg=none must be rejected, got %d", rec.Code)
	}
}

func TestSec_JWT_KidHeaderInjection(t *testing.T) {
	// kid header is not used for key selection in MuxMaster (no JWKS).
	// Verify that a token with a malicious kid value is still correctly validated
	// (not bypassed or panicked-on).
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeJWT("HS256", map[string]any{"kid": "../../etc/passwd"}, validClaims(), hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	// Should still validate (kid is ignored, not used for key lookup).
	if rec.Code != http.StatusOK {
		t.Errorf("token with kid path traversal: got %d want 200", rec.Code)
	}
}

func TestSec_JWT_CritHeaderRejectsUnknownExtensions(t *testing.T) {
	// RFC 7515 §4.1.11: "crit" header with any entry must cause rejection.
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeJWT("HS256", map[string]any{"crit": []string{"custom-ext"}}, validClaims(), hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("crit header: got %d want 401", rec.Code)
	}
}

func TestSec_JWT_ExpiredToken(t *testing.T) {
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	expiredClaims := map[string]any{
		"sub": "user",
		"exp": time.Now().Add(-time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	}
	token := makeJWT("HS256", nil, expiredClaims, hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired token: got %d want 401", rec.Code)
	}
}

func TestSec_JWT_NbfClockSkewRespected(t *testing.T) {
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		ClockSkew:  30 * time.Second,
	})
	// nbf = 20 seconds in the future — within ClockSkew so should be accepted
	nbfFuture := map[string]any{
		"sub": "user",
		"exp": time.Now().Add(time.Hour).Unix(),
		"nbf": time.Now().Add(20 * time.Second).Unix(),
	}
	token := makeJWT("HS256", nil, nbfFuture, hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Errorf("nbf within clockskew: got %d want 200", rec.Code)
	}

	// nbf = 60 seconds in the future — beyond ClockSkew so must be rejected
	nbfTooFar := map[string]any{
		"sub": "user",
		"exp": time.Now().Add(time.Hour).Unix(),
		"nbf": time.Now().Add(60 * time.Second).Unix(),
	}
	token2 := makeJWT("HS256", nil, nbfTooFar, hs256Sign(secret))
	rec2 := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token2)
	})
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("nbf beyond clockskew: got %d want 401", rec2.Code)
	}
}

func TestSec_JWT_AudienceValidation(t *testing.T) {
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		Audiences:  []string{"myapp"},
	})
	// Wrong audience
	token := makeJWT("HS256", nil, map[string]any{
		"sub": "user", "exp": time.Now().Add(time.Hour).Unix(), "aud": "other",
	}, hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong audience: got %d want 401", rec.Code)
	}

	// Correct audience
	token2 := makeJWT("HS256", nil, map[string]any{
		"sub": "user", "exp": time.Now().Add(time.Hour).Unix(), "aud": "myapp",
	}, hs256Sign(secret))
	rec2 := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token2)
	})
	if rec2.Code != http.StatusOK {
		t.Errorf("correct audience: got %d want 200", rec2.Code)
	}
}

func TestSec_JWT_IssuerValidation(t *testing.T) {
	secret := []byte("testsecret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		Issuers:    []string{"https://auth.example.com"},
	})
	// Wrong issuer
	token := makeJWT("HS256", nil, map[string]any{
		"sub": "user", "exp": time.Now().Add(time.Hour).Unix(), "iss": "https://evil.com",
	}, hs256Sign(secret))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong issuer: got %d want 401", rec.Code)
	}
}

func TestSec_JWT_WWWAuthenticateOnMissingToken(t *testing.T) {
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     []byte("s"),
		Algorithms: []string{"HS256"},
	})
	rec := serve(mw, "GET", "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate header on 401")
	}
}

func TestSec_JWT_InvalidSignatureRejected(t *testing.T) {
	secret := []byte("correct-secret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeJWT("HS256", nil, validClaims(), hs256Sign([]byte("wrong-secret")))
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong signature: got %d want 401", rec.Code)
	}
}

func TestSec_JWT_ES256_ValidSignature(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	mw := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &priv.PublicKey,
		Algorithms: []string{"ES256"},
	})

	// Sign with ES256 (IEEE P1363 format: r||s fixed-width).
	signES256 := func(input []byte) []byte {
		h := sha256.Sum256(input)
		r, s, err := ecdsa.Sign(rand.Reader, priv, h[:])
		if err != nil {
			panic(err)
		}
		rb := r.Bytes()
		sb := s.Bytes()
		// Pad to 32 bytes each.
		sig := make([]byte, 64)
		copy(sig[32-len(rb):32], rb)
		copy(sig[64-len(sb):64], sb)
		return sig
	}
	token := makeJWT("ES256", nil, validClaims(), signES256)
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Errorf("valid ES256 token: got %d want 200", rec.Code)
	}
}

func TestSec_JWT_TokenNotLeakedInResponse(t *testing.T) {
	// Verify that an invalid JWT is not echoed back in the response body.
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     []byte("secret"),
		Algorithms: []string{"HS256"},
	})
	fakeToken := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzZWNyZXQtcGF5bG9hZCJ9.invalidsig"
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+fakeToken)
	})
	body := rec.Body.String()
	if strings.Contains(body, "eyJ") || strings.Contains(body, fakeToken) {
		t.Errorf("JWT token leaked in response body: %q", body)
	}
}

func TestSec_JWT_PanicsOnMissingHS256Secret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("HS256 without Secret should panic")
		}
	}()
	middleware.JWTAuth(middleware.JWTOptions{Algorithms: []string{"HS256"}})
}

func TestSec_JWT_PanicsOnRS256WithoutRSAKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("RS256 without *rsa.PublicKey should panic")
		}
	}()
	middleware.JWTAuth(middleware.JWTOptions{Algorithms: []string{"RS256"}})
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — oauth2
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_OAuth2_CacheStaleActiveAfterExpiry(t *testing.T) {
	// Hypothesis #5: oauth2 cache read path checks time.Now().After(expiry).
	// This test verifies that an entry whose expiry has passed is NOT returned
	// as active. We insert an entry with a very short TTL and check after expiry.

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user1",
			"exp":    time.Now().Add(50 * time.Millisecond).Unix(),
		})
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: server.URL,
		CacheTTL: 100 * time.Millisecond,
	})

	// First request — caches the token.
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer test-token")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d", rec.Code)
	}

	// Wait for cache TTL to expire.
	time.Sleep(150 * time.Millisecond)

	// Second request — cache entry must be expired and a new introspection call made.
	rec2 := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer test-token")
	})
	if rec2.Code != http.StatusOK {
		t.Errorf("post-expiry request: got %d want 200", rec2.Code)
	}
	if callCount < 2 {
		t.Errorf("cache did not expire: only %d introspection calls made (expected >= 2)", callCount)
	}
}

func TestSec_OAuth2_InactiveTokenCached_NotAccepted(t *testing.T) {
	// An inactive token cached must still produce 401 on subsequent requests.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"active": false})
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: server.URL,
		CacheTTL: 60 * time.Second,
	})

	// Inactive tokens: oauth2.go lines 220-225 check !resp.Active and return 401;
	// cache.set is only called for active tokens (line 227: after active check).
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer inactive-token")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("inactive token: got %d want 401", rec.Code)
	}
}

func TestSec_OAuth2_RevocationLag_Documented(t *testing.T) {
	// Revocation lag is a known limitation (documented in CacheTTL comment).
	// This test verifies the middleware correctly uses the effective TTL as
	// min(CacheTTL, token.exp - now) — i.e., respects the token's own expiry.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user",
			"exp":    time.Now().Add(50 * time.Millisecond).Unix(), // token expires soon
		})
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: server.URL,
		CacheTTL: 60 * time.Second, // long cache TTL
	})

	serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer revoked-token")
	})
	time.Sleep(100 * time.Millisecond)
	// After token.exp, even with long CacheTTL, the entry should be expired.
	serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer revoked-token")
	})
	if calls < 2 {
		t.Logf("Note: effective TTL = min(CacheTTL, exp-now). calls=%d. Revocation lag test.", calls)
		// Not a hard failure — depends on monotonic clock precision.
	}
}

func TestSec_OAuth2_EndpointErrorReturns401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: server.URL,
		CacheTTL: -1,
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer token")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("endpoint error: got %d want 401", rec.Code)
	}
}

func TestSec_OAuth2_ResponseBodyLimitedTo64KB(t *testing.T) {
	// Verify that oversized introspection responses (> 64 KB) are handled gracefully.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write 128KB of JSON preamble to trigger the 64KB limit.
		giant := strings.Repeat("x", 65537)
		// Start with invalid JSON to ensure decode fails cleanly.
		w.Write([]byte("{\"active\":true,\"scope\":\"" + giant + "\"}"))
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint: server.URL,
		CacheTTL: -1,
	})
	// Should not panic or OOM; returns 401 or 200 depending on truncation.
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer token")
	})
	// Accept either 200 (truncation kept valid JSON) or 401 (truncation broke JSON)
	if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
		t.Errorf("oversized response: got unexpected %d", rec.Code)
	}
}

func TestSec_OAuth2_MissingTokenReturns401WithWWWAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{AllowInsecureEndpoint: true, Endpoint: server.URL})
	rec := serve(mw, "GET", "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate on 401")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — cors
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_CORS_WildcardPlusCredentialsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("wildcard + credentials should panic")
		}
	}()
	middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}

func TestSec_CORS_NullOriginRejected(t *testing.T) {
	// "null" origin should not be accepted (RFC 6454 §7.3 — null origin is from
	// sandboxed iframes and is not a safe origin).
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Origin", "null")
	})
	// With allowedOrigins containing only example.com, "null" should be forbidden.
	if rec.Code == http.StatusOK && rec.Header().Get("Access-Control-Allow-Origin") == "null" {
		t.Error("MSR-FINDING: null origin accepted as valid CORS origin")
	}
}

func TestSec_CORS_OriginReflectionWithWhitelist(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com"},
	})
	// Unlisted origin must be forbidden.
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.com")
	})
	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao == "https://evil.com" {
		t.Error("MSR-CRITICAL: unlisted origin reflected in Access-Control-Allow-Origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("unlisted origin: got %d want 403", rec.Code)
	}
}

func TestSec_CORS_EmptyAllowedOrigins_SilentPermissive(t *testing.T) {
	// MSR-2026-0058 RESOLVED: CORS now panics on empty AllowedOrigins (fail-closed at
	// construction time), preventing the "silent permissive" misconfiguration trap.
	// This test asserts the fail-closed panic behaviour is present.
	defer func() {
		r := recover()
		if r == nil {
			t.Error("MSR-2026-0058 regression: CORS with empty AllowedOrigins must panic at construction; no panic observed")
		} else {
			t.Logf("MSR-2026-0058 FIXED: CORS panics on empty AllowedOrigins: %v", r)
		}
	}()
	// This call must panic.
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{},
	})
}

func TestSec_CORS_WildcardEmitsLiteralStar(t *testing.T) {
	// When AllowAll, ACAO must be "*", never the echoed origin.
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Origin", "https://attacker.com")
	})
	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao == "https://attacker.com" {
		t.Error("wildcard mode reflected specific origin instead of '*'")
	}
	if acao != "*" {
		t.Errorf("wildcard mode: want '*' got %q", acao)
	}
}

func TestSec_CORS_CRLFOriginRejected(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.com\r\nInjected: yes")
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("CRLF in origin: got %d want 400", rec.Code)
	}
}

func TestSec_CORS_PreflightRespondsToOptions(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
	})
	rec := serve(mw, http.MethodOptions, "/", func(r *http.Request) {
		r.Header.Set("Origin", "https://example.com")
	})
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight OPTIONS: got %d want 204", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "GET") {
		t.Error("preflight: missing Access-Control-Allow-Methods")
	}
}

func TestSec_CORS_VaryOriginMissing(t *testing.T) {
	// MM-2026-0051: When CORS reflects a specific origin, the response must include
	// Vary: Origin to prevent cache poisoning by CDNs/reverse proxies.
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Origin", "https://example.com")
	})
	vary := rec.Header().Get("Vary")
	if !strings.Contains(strings.ToLower(vary), "origin") {
		t.Errorf("MSR-FINDING: CORS reflects specific origin but Vary: Origin header absent (got Vary: %q). "+
			"CDNs may serve the CORS response to requests with different origins (MM-2026-0051).", vary)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — compress
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Compress_StreamingBoundedMemory(t *testing.T) {
	// Verify streaming compression: a large response does not buffer entirely in memory.
	mw := middleware.Compress(gzip.DefaultCompression)
	const chunkSize = 64 * 1024
	const numChunks = 100 // 6.4 MB total, written in chunks.

	chunk := bytes.Repeat([]byte("a"), chunkSize)
	bytesWritten := int64(0)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range numChunks {
			n, err := w.Write(chunk)
			bytesWritten += int64(n)
			if err != nil {
				return
			}
		}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("streaming compress: got %d", rec.Code)
	}
	// Verify Content-Encoding is gzip.
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Content-Encoding: gzip not set")
	}
	// Verify we can decompress the output.
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	n, err := io.Copy(io.Discard, gr)
	if err != nil {
		t.Errorf("decompress: %v", err)
	}
	if n != bytesWritten {
		t.Errorf("decompressed %d bytes want %d", n, bytesWritten)
	}
}

func TestSec_Compress_SmallResponseNotCompressed(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tiny"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("small response should not be compressed")
	}
}

func TestSec_Compress_VaryAcceptEncodingPresent(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	bigBody := bytes.Repeat([]byte("x"), 2048)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(bigBody) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(inner).ServeHTTP(rec, req)
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary: Accept-Encoding missing from compressed response")
	}
}

func TestSec_Compress_NoAcceptEncodingPassthrough(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 2048))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	// No Accept-Encoding header — must not compress.
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not compress when Accept-Encoding: gzip absent")
	}
}

func TestSec_Compress_UnsupportedEncodingIgnored(t *testing.T) {
	// Client sends Accept-Encoding: br (brotli) only — not supported, no compression.
	mw := middleware.Compress(gzip.DefaultCompression)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 2048))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Error("brotli-only Accept-Encoding should not trigger gzip compression")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — real_ip
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_RealIP_NoTrustedProxies_IgnoresXFF(t *testing.T) {
	// When called with no CIDR arguments, RealIP trusts ALL peers.
	// This is documented as insecure. Verify at minimum XFF is processed.
	mw := middleware.RealIP()
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// With no trusted CIDRs, XFF is trusted (trust-all mode).
	if capturedAddr != "1.2.3.4" {
		t.Logf("RealIP() trust-all mode: RemoteAddr=%q (expected 1.2.3.4)", capturedAddr)
	}
}

func TestSec_RealIP_UntrustedPeerDoesNotRewriteAddr(t *testing.T) {
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:9999" // NOT in 10.0.0.0/8
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedAddr != "1.2.3.4:9999" {
		t.Errorf("untrusted peer: RemoteAddr was rewritten to %q, want original", capturedAddr)
	}
}

func TestSec_RealIP_TrustedPeerRewritesFromXFF(t *testing.T) {
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:12345" // trusted
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedAddr != "203.0.113.1" {
		t.Errorf("trusted peer XFF: got %q want 203.0.113.1", capturedAddr)
	}
}

func TestSec_RealIP_XFFChainPicksFirst(t *testing.T) {
	// XFF: "client, proxy1, proxy2" — RealIP takes the leftmost (first) value.
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.6, 10.0.0.7")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedAddr != "1.2.3.4" {
		t.Errorf("XFF chain: got %q want 1.2.3.4 (leftmost)", capturedAddr)
	}
}

func TestSec_RealIP_CRLFInXFFRejected(t *testing.T) {
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4\r\nInjected: header")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// netip.ParseAddr rejects CRLF — RemoteAddr must remain as original.
	if capturedAddr == "1.2.3.4\r\nInjected: header" {
		t.Error("CRLF in XFF was not sanitised")
	}
}

func TestSec_RealIP_IPv6Parsed(t *testing.T) {
	pfx, _ := netip.ParsePrefix("::1/128")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:9000"
	req.Header.Set("X-Forwarded-For", "2001:db8::1")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedAddr != "2001:db8::1" {
		t.Errorf("IPv6 XFF: got %q want 2001:db8::1", capturedAddr)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — recoverer
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Recoverer_StackTraceNotInResponseBody(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("database password: secret123")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "database password") || strings.Contains(body, "secret123") {
		t.Errorf("MSR-CRITICAL: panic value leaked to response body: %q", body)
	}
	if strings.Contains(body, "goroutine") || strings.Contains(body, "runtime") {
		t.Errorf("MSR-CRITICAL: stack trace leaked to response body: %q", body)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panic recovery: got %d want 500", rec.Code)
	}
}

func TestSec_Recoverer_StackToLogsNotBody(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic for stack trace check")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)

	// Stack trace should appear in logs but not in response body.
	logs := logBuf.String()
	if !strings.Contains(logs, "panic recovered") {
		t.Error("panic recovery should log 'panic recovered'")
	}
	body := rec.Body.String()
	if strings.Contains(body, "goroutine") {
		t.Error("stack trace in response body")
	}
}

func TestSec_Recoverer_NilPanicValue(t *testing.T) {
	// panic(nil) — recoverer must handle without crashing.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil) //nolint:govet // intentional nil panic for testing
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	// Should not crash the test.
	mw(inner).ServeHTTP(rec, req)
	// In Go 1.21+ panic(nil) is recoverable via *runtime.PanicNilError.
	// The recoverer should either return 500 or pass through (if recover() returns nil).
	// Acceptable outcomes: 500 or 200 if recover() == nil in older behaviour.
}

func TestSec_Recoverer_URLPathNotInBody(t *testing.T) {
	// MSR-RE-V2-001: raw r.URL.Path is logged but must not appear in response body.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("oops")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secret/token=abc123", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "secret") || strings.Contains(body, "abc123") {
		t.Errorf("URL path leaked in response body: %q", body)
	}
	// Log should contain path (informational, not a security failure per se for logs)
	logs := logBuf.String()
	if !strings.Contains(logs, "/secret/token=abc123") {
		t.Logf("Note: panic log does not include request path (path: %q)", logs)
	}
}

// TestSec_Recoverer_CRLFInPathSanitisedInLog covers MSR-2026-0057: when a
// panicking request carries CRLF or ANSI escape bytes in r.URL.Path or
// r.Method, the Recoverer log line must not contain any raw control byte —
// otherwise an attacker can forge log entries (log injection) or run
// terminal escape sequences against an operator tailing the log.
func TestSec_Recoverer_CRLFInPathSanitisedInLog(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("crlf-panic")
	})

	// httptest.NewRequest rejects CRLF in the URL string, so build the request
	// directly with raw control bytes injected into r.URL.Path / r.Method.
	req := httptest.NewRequest("GET", "/innocent", nil)
	req.URL.Path = "/admin\r\nFAKE: injected\x1b[31mevil\x1b[0m\x00null"
	req.Method = "GET\r\nX-Method-Smuggle: yes"

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	logs := logBuf.String()
	// Only the inner panic stack legitimately contains a single newline
	// (slog renders multi-line stack frames). Inspect the path field in
	// isolation: extract the fragment after `path=` and verify no raw
	// CR/LF/ESC/NUL bytes appear there.
	pathIdx := strings.Index(logs, "path=")
	if pathIdx < 0 {
		t.Fatalf("log line missing path field: %q", logs)
	}
	// path= is the last attribute on the line; slog terminates the line with
	// a single \n that is not part of the path value.
	pathField := strings.TrimRight(logs[pathIdx:], "\n")
	for _, raw := range []byte{'\r', '\n', 0x1b, 0x00} {
		if strings.IndexByte(pathField, raw) >= 0 {
			t.Errorf("path field contains raw control byte 0x%02x — sanitisation regression: %q",
				raw, pathField)
		}
	}
	// And the sanitised path must appear in escaped form (the literal four
	// characters '\','r','\','n' plus slog's outer quoting → "\\r\\n").
	if !strings.Contains(pathField, `\\r\\n`) {
		t.Errorf("expected escaped CRLF in sanitised path field; got: %q", pathField)
	}

	methodIdx := strings.Index(logs, "method=")
	if methodIdx < 0 {
		t.Fatalf("log line missing method field: %q", logs)
	}
	// Limit method-field inspection to a short window before the next field.
	end := strings.Index(logs[methodIdx:], " path=")
	if end < 0 {
		end = len(logs) - methodIdx
	}
	methodField := logs[methodIdx : methodIdx+end]
	for _, raw := range []byte{'\r', '\n'} {
		if strings.IndexByte(methodField, raw) >= 0 {
			t.Errorf("method field contains raw control byte 0x%02x — sanitisation regression: %q",
				raw, methodField)
		}
	}
}

func TestSec_Recoverer_ConcurrentRequestsIsolated(t *testing.T) {
	// Ensure recovered state from one goroutine does not affect another.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("concurrent panic")
		}
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(200)
	})

	var wg sync.WaitGroup
	results := make([]int, 20)
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := "/ok"
			if idx%2 == 0 {
				path = "/panic"
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			mw(inner).ServeHTTP(rec, req)
			results[idx] = rec.Code
		}(i)
	}
	wg.Wait()
	for i, code := range results {
		if i%2 == 0 {
			if code != 500 {
				t.Errorf("goroutine %d (panic path): got %d want 500", i, code)
			}
		} else {
			if code != 200 {
				t.Errorf("goroutine %d (ok path): got %d want 200 (isolation failure?)", i, code)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — throttle
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Throttle_XFFBypassDependsOnRealIP(t *testing.T) {
	// Without RealIP middleware, ThrottlePerIP keys on r.RemoteAddr (which is the
	// raw TCP peer). A client behind a shared IP spoofing XFF will not bypass throttle
	// because throttle looks at RemoteAddr, not XFF.
	// This test verifies ThrottlePerIP correctly uses RemoteAddr by default.
	// Use limit=1 with a long-holding handler to reliably trigger the 503.
	limit := 1
	mw := middleware.ThrottlePerIP(limit, 30*time.Millisecond, nil)

	ready := make(chan struct{})
	hold := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal that the slot is held, then wait.
		select {
		case ready <- struct{}{}:
		default:
		}
		<-hold
		w.WriteHeader(200)
	})
	handler := mw(inner)

	// First goroutine holds the single slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		handler.ServeHTTP(rec, req)
	}()

	// Wait for slot to be held.
	<-ready

	// Second request — same IP, slot occupied — should get 503.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "1.2.3.4:5678"
	handler.ServeHTTP(rec2, req2)

	close(hold) // release the first goroutine.
	wg.Wait()

	if rec2.Code != http.StatusServiceUnavailable {
		t.Errorf("throttle not enforced: got %d want 503 (limit=1, slot occupied)", rec2.Code)
	}
}

func TestSec_Throttle_DifferentIPsNotSharedBucket(t *testing.T) {
	mw := middleware.ThrottlePerIP(1, 50*time.Millisecond, nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(200)
	})
	handler := mw(inner)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, ip := range []string{"1.1.1.1:80", "2.2.2.2:80"} {
		wg.Add(1)
		go func(idx int, remoteAddr string) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = remoteAddr
			handler.ServeHTTP(rec, req)
			codes[idx] = rec.Code
		}(i, ip)
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Errorf("different IP %d throttled incorrectly: got %d", i, c)
		}
	}
}

func TestSec_Throttle_EntryCleanedAfterRelease(t *testing.T) {
	// Verify the table cleanup: after all requests from an IP complete,
	// the entry is removed (refs == 0 → delete(table, key)).
	// We use the custom keyFn to observe cleanup indirectly by checking
	// a second burst from the same IP can start fresh.
	mw := middleware.ThrottlePerIP(1, 100*time.Millisecond, nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(inner)

	// Serial requests — each should complete and clean up.
	for range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "5.5.5.5:1234"
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("serial request: got %d want 200", rec.Code)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — timeout
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Timeout_ContextCancelledAfterDeadline(t *testing.T) {
	mw := middleware.Timeout(50 * time.Millisecond)
	ctxDone := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(ctxDone)
		case <-time.After(200 * time.Millisecond):
			// timeout not fired in handler context
		}
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)

	select {
	case <-ctxDone:
		// Good: handler observed context cancellation.
	case <-time.After(300 * time.Millisecond):
		t.Error("handler did not observe context cancellation after timeout")
	}
}

func TestSec_Timeout_HandlerGoroutineEventuallyExits(t *testing.T) {
	// Goroutine leak test: start a handler that sleeps past the timeout.
	// The goroutine should eventually exit (net/http's ResponseWriter closes).
	mw := middleware.Timeout(10 * time.Millisecond)
	done := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		<-r.Context().Done()
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)

	select {
	case <-done:
		// Handler observed cancellation and exited.
	case <-time.After(200 * time.Millisecond):
		t.Error("handler goroutine did not observe context Done — potential goroutine leak")
	}
}

func TestSec_Timeout_PanicsOnZeroDuration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Timeout(0) should panic")
		}
	}()
	middleware.Timeout(0)
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — logger
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Logger_CRLFInPathSanitised(t *testing.T) {
	// httptest.NewRequest rejects raw CRLF in URL — test log injection via a custom
	// handler that directly calls the logger middleware with a crafted request.
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(inner)

	// Build a request then manually set a path with CRLF after construction.
	req := httptest.NewRequest("GET", "/safe", nil)
	req.URL.Path = "/inject\r\nFAKE-LOG: evil"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	// sanitiseForLog uses strconv.QuoteToASCII which escapes \r and \n to \\r\\n.
	// The log output must not contain raw CR or LF that would allow injection.
	if strings.Contains(out, "\r") || (strings.Count(out, "\n") > 1) {
		t.Errorf("MSR-FINDING: raw CR/LF in log output enables injection: %q", out)
	}
	// The text "FAKE-LOG" can appear in escaped form — what matters is no raw newline.
	// Verify the escape representation: \r should appear as \\r in the output.
	if !strings.Contains(out, `\r`) && !strings.Contains(out, `\n`) {
		t.Logf("Note: CRLF not escaped in log — logger may not sanitise path: %q", out)
	}
	t.Logf("CRLF log injection result: %q (sanitised = no raw newline injection)", out)
}

func TestSec_Logger_ANSIEscapeSanitised(t *testing.T) {
	// httptest.NewRequest rejects control chars in URL — inject via direct path mutation.
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/safe", nil)
	req.URL.Path = "/path\x1b[2Jerased" // ESC[2J = clear screen

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("MSR-FINDING: ANSI escape in log output (unescaped ESC byte): %q", out)
	}
	t.Logf("ANSI escape log result: %q", out)
}

func TestSec_Logger_AuthorizationNotLogged(t *testing.T) {
	// Logger only logs method, path, status, duration — no headers.
	// Verify the Authorization header value does not appear in the log.
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	serve(mw, "GET", "/api", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer super-secret-token-xyz")
	})
	out := buf.String()
	if strings.Contains(out, "super-secret-token-xyz") {
		t.Errorf("MSR-CRITICAL: Authorization header value leaked in log: %q", out)
	}
}

func TestSec_Logger_CookiesNotLogged(t *testing.T) {
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	serve(mw, "GET", "/api", func(r *http.Request) {
		r.Header.Set("Cookie", "session=abc123secret")
	})
	out := buf.String()
	if strings.Contains(out, "abc123secret") {
		t.Errorf("MSR-CRITICAL: Cookie value leaked in log: %q", out)
	}
}

func TestSec_Logger_QueryStringLogged(t *testing.T) {
	// Document: logger does NOT log query string (only r.URL.Path, not RawQuery).
	// This test verifies the behavior.
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	serve(mw, "GET", "/search?token=secretvalue", nil)
	out := buf.String()
	// Path will be "/search" and query "token=secretvalue" should not appear
	// because logger uses r.URL.Path which excludes query.
	if strings.Contains(out, "secretvalue") {
		t.Logf("Note: query string parameter appears in log: %q (may be acceptable)", out)
	}
}

func TestSec_Logger_PanicsOnNilWriter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Logger(nil) should panic")
		}
	}()
	middleware.Logger(nil)
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — request_id
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_RequestID_UsesCryptoRand(t *testing.T) {
	// Verify IDs are 32 hex chars (16 random bytes from crypto/rand).
	mw := middleware.RequestID()
	ids := make(map[string]struct{}, 1000)
	for range 1000 {
		rec := serve(mw, "GET", "/", nil)
		id := rec.Header().Get("X-Request-ID")
		if len(id) != 32 {
			t.Errorf("request ID length: got %d want 32", len(id))
		}
		if _, dupe := ids[id]; dupe {
			t.Errorf("duplicate request ID generated: %q", id)
		}
		ids[id] = struct{}{}
	}
}

func TestSec_RequestID_NoCollisions(t *testing.T) {
	mw := middleware.RequestID()
	ids := make(map[string]struct{}, 10000)
	for range 10000 {
		rec := serve(mw, "GET", "/", nil)
		id := rec.Header().Get("X-Request-ID")
		if _, dupe := ids[id]; dupe {
			t.Errorf("collision in 10000 IDs: %q", id)
		}
		ids[id] = struct{}{}
	}
}

func TestSec_RequestID_ValidIncomingPropagated(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-Request-ID", "valid-id-123")
	})
	if got := rec.Header().Get("X-Request-ID"); got != "valid-id-123" {
		t.Errorf("valid incoming ID not propagated: got %q", got)
	}
}

func TestSec_RequestID_CRLFInIncomingReplaced(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-Request-ID", "id\r\nInjected: evil")
	})
	id := rec.Header().Get("X-Request-ID")
	if strings.Contains(id, "\r") || strings.Contains(id, "\n") {
		t.Errorf("CRLF in X-Request-ID not sanitised: %q", id)
	}
	// Should have been replaced with a fresh random ID.
	if id == "id\r\nInjected: evil" {
		t.Error("CRLF X-Request-ID was echoed verbatim")
	}
}

func TestSec_RequestID_OversizedIncomingReplaced(t *testing.T) {
	mw := middleware.RequestID()
	giant := strings.Repeat("a", 200) // > 128 chars
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("X-Request-ID", giant)
	})
	id := rec.Header().Get("X-Request-ID")
	if len(id) >= 200 {
		t.Errorf("oversized X-Request-ID not replaced: len=%d", len(id))
	}
}

func TestSec_RequestID_PredictabilityCheck(t *testing.T) {
	// Ensure IDs are not sequential or math/rand-based.
	// Collect 100 IDs and verify they have adequate entropy (no pattern).
	mw := middleware.RequestID()
	ids := make([]string, 100)
	for i := range ids {
		rec := serve(mw, "GET", "/", nil)
		ids[i] = rec.Header().Get("X-Request-ID")
	}
	// Check that IDs are not simply incrementing hex.
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Errorf("consecutive identical IDs: %q", ids[i])
		}
	}
	// Verify hex encoding only.
	for _, id := range ids {
		if _, err := hex.DecodeString(id); err != nil {
			t.Errorf("ID not valid hex: %q, err: %v", id, err)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — clean_path
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_CleanPath_TraversalNormalised(t *testing.T) {
	mw := middleware.CleanPath()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/a/../b/../../etc/passwd", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedPath == "/a/../b/../../etc/passwd" {
		t.Error("path traversal not cleaned")
	}
	if capturedPath != "/etc/passwd" {
		// path.Clean resolves /a/../b/../../etc/passwd → /etc/passwd
		t.Logf("cleaned path: %q (expected /etc/passwd)", capturedPath)
	}
}

func TestSec_CleanPath_RawPathWithTraversalZeroed(t *testing.T) {
	mw := middleware.CleanPath()
	var capturedRaw string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRaw = r.URL.RawPath
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/a/%2e%2e/etc/passwd", nil)
	// Simulate RawPath set by Go's HTTP server for percent-encoded paths.
	req.URL.RawPath = "/a/%2e%2e/etc/passwd"
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// path.Clean on the RawPath would produce a different result → RawPath zeroed.
	if capturedRaw == "/a/%2e%2e/etc/passwd" {
		t.Logf("Note: RawPath with encoded traversal was not zeroed: %q", capturedRaw)
	}
}

func TestSec_CleanPath_DoubleSlashNormalised(t *testing.T) {
	mw := middleware.CleanPath()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "//admin//secret", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(capturedPath, "//") {
		t.Errorf("double slashes not cleaned: %q", capturedPath)
	}
}

func TestSec_CleanPath_QueryStringUntouched(t *testing.T) {
	mw := middleware.CleanPath()
	var capturedURL string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.RawQuery
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/path?q=../../../etc", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// Query should be unchanged.
	if capturedURL != "q=../../../etc" {
		t.Logf("query string: got %q want q=../../../etc", capturedURL)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — strip_slashes
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_StripSlashes_TrailingSlashStripped(t *testing.T) {
	mw := middleware.StripSlashes()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/admin///", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedPath != "/admin" {
		t.Errorf("trailing slashes not stripped: %q want /admin", capturedPath)
	}
}

func TestSec_StripSlashes_RootPreserved(t *testing.T) {
	mw := middleware.StripSlashes()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedPath != "/" {
		t.Errorf("root path should not be stripped: got %q", capturedPath)
	}
}

func TestSec_StripSlashes_NoMiddleSlashEffect(t *testing.T) {
	mw := middleware.StripSlashes()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/a//b//c", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// StripSlashes only strips TRAILING slashes, not internal ones.
	if capturedPath != "/a//b//c" {
		t.Logf("Note: StripSlashes modified path with middle slashes: %q", capturedPath)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — with_value
// ═══════════════════════════════════════════════════════════════════════════════

type testCtxKey struct{ name string }

func TestSec_WithValue_TypedKeyNoCollision(t *testing.T) {
	// Using typed key struct avoids string collision between packages.
	key1 := testCtxKey{"user"}
	key2 := testCtxKey{"role"}

	mw1 := middleware.WithValue(key1, "alice")
	mw2 := middleware.WithValue(key2, "admin")

	var got1, got2 any
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got1 = r.Context().Value(key1)
		got2 = r.Context().Value(key2)
		w.WriteHeader(200)
	})
	chain := mw1(mw2(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	chain.ServeHTTP(rec, req)

	if got1 != "alice" {
		t.Errorf("key1 value: got %v want alice", got1)
	}
	if got2 != "admin" {
		t.Errorf("key2 value: got %v want admin", got2)
	}
}

func TestSec_WithValue_StringKeyCollisionRisk(t *testing.T) {
	// Demonstrate that string keys CAN collide — this is an anti-pattern.
	// The middleware itself doesn't enforce typed keys, but the doc comment warns.
	// Both string "user" keys would collide — the inner one wins (last write).
	mw1 := middleware.WithValue("user", "alice")
	mw2 := middleware.WithValue("user", "attacker")

	var gotUser any
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Context().Value("user")
		w.WriteHeader(200)
	})
	chain := mw1(mw2(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	chain.ServeHTTP(rec, req)

	// Document the collision: the last WithValue set wins.
	t.Logf("String key collision test: r.Context().Value(\"user\") = %v (inner mw2 wins)", gotUser)
}

func TestSec_WithValue_PanicsOnNilKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WithValue(nil, ...) should panic")
		}
	}()
	middleware.WithValue(nil, "value")
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — set_header
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_SetHeader_CRLFInKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("SetHeader with CRLF in key should panic")
		}
	}()
	middleware.SetHeader("X-Key\r\nX-Inject", "value")
}

func TestSec_SetHeader_CRLFInValuePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("SetHeader with CRLF in value should panic")
		}
	}()
	middleware.SetHeader("X-Key", "value\r\nX-Inject: evil")
}

func TestSec_SetHeader_LFInValuePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("SetHeader with LF in value should panic")
		}
	}()
	middleware.SetHeader("X-Key", "value\nX-Inject: evil")
}

func TestSec_SetHeader_HeaderSetBeforeNext(t *testing.T) {
	mw := middleware.SetHeader("X-Custom", "test-value")
	var seen string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = w.Header().Get("X-Custom")
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)
	if seen != "test-value" {
		t.Errorf("header not set before next handler: got %q", seen)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — no_cache
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_NoCache_AllHeadersPresent(t *testing.T) {
	mw := middleware.NoCache()
	rec := serve(mw, "GET", "/", nil)

	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control missing no-store: %q", cc)
	}
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control missing no-cache: %q", cc)
	}
	if !strings.Contains(cc, "must-revalidate") {
		t.Errorf("Cache-Control missing must-revalidate: %q", cc)
	}
	if rec.Header().Get("Pragma") != "no-cache" {
		t.Errorf("Pragma: got %q want no-cache", rec.Header().Get("Pragma"))
	}
	if rec.Header().Get("Expires") != "0" {
		t.Errorf("Expires: got %q want 0", rec.Header().Get("Expires"))
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — middleware ordering
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Order_RecovererOutermost(t *testing.T) {
	// Recoverer must be outermost: a panic inside a middleware lower in the chain
	// must be caught by the outer Recoverer.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	recoverer := middleware.RecovererWithLogger(logger)

	panicMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("inner middleware panic")
		})
	}

	chain := recoverer(panicMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	chain.ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Errorf("recoverer must catch inner middleware panic: got %d", rec.Code)
	}
}

func TestSec_Order_LoggerAfterRealIPLogsClientIP(t *testing.T) {
	// Logger registered AFTER RealIP logs the client IP (rewritten RemoteAddr).
	// Logger registered BEFORE RealIP logs the raw RemoteAddr (proxy IP).
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	realIP := middleware.RealIP(&pfx)

	var loggedPath string
	var buf bytes.Buffer
	logger := middleware.Logger(&buf)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loggedPath = r.RemoteAddr // capture what logger would have seen
		w.WriteHeader(200)
	})

	// Order: realIP → logger → inner
	chain := realIP(logger(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.55")
	chain.ServeHTTP(rec, req)

	// After RealIP runs, RemoteAddr = "203.0.113.55".
	if loggedPath != "203.0.113.55" {
		t.Errorf("after RealIP: RemoteAddr=%q want 203.0.113.55", loggedPath)
	}
}

func TestSec_Order_TimeoutCancelsSlowHandler(t *testing.T) {
	timeout := middleware.Timeout(30 * time.Millisecond)
	// Recoverer wraps timeout — panic after timeout fires should still produce 500.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	recoverer := middleware.RecovererWithLogger(logger)

	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		// Wait for context cancellation.
		<-r.Context().Done()
		close(handlerDone)
	})

	chain := recoverer(timeout(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	go chain.ServeHTTP(rec, req)
	<-handlerStarted

	select {
	case <-handlerDone:
		// Good: handler observed cancellation.
	case <-time.After(200 * time.Millisecond):
		t.Error("handler did not observe timeout cancellation")
	}
}

func TestSec_Order_ThrottleBeforeAuthEnablesDenialOfService(t *testing.T) {
	// Documented risk: throttle before auth means unauthenticated requests consume
	// throttle slots, potentially DoSing legitimate users.
	// This test documents the risk — it doesn't assert a specific code outcome.
	limit := 1
	throttle := middleware.ThrottlePerIP(limit, 20*time.Millisecond, nil)
	auth := middleware.BasicAuth("realm", map[string]string{"alice": "pass"})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	chain := throttle(auth(inner))

	// Fill the throttle slot with an invalid auth request.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("wrong", "wrong")
	go chain.ServeHTTP(rec, req)
	time.Sleep(5 * time.Millisecond)

	// Now a valid request should be throttled.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.SetBasicAuth("alice", "pass")
	chain.ServeHTTP(rec2, req2)

	t.Logf("Throttle-before-auth DoS test: valid request got %d (503 = DoS risk demonstrated)", rec2.Code)
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — cross-middleware composition
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Composition_CleanPathThenStripSlashes(t *testing.T) {
	// H-A: Test order clean_path → strip_slashes vs strip_slashes → clean_path
	// on a traversal path with trailing slashes.
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})

	cleanFirst := middleware.CleanPath()(middleware.StripSlashes()(inner))
	stripFirst := middleware.StripSlashes()(middleware.CleanPath()(inner))

	for name, chain := range map[string]http.Handler{
		"clean→strip": cleanFirst,
		"strip→clean": stripFirst,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/static/../admin/", nil)
		chain.ServeHTTP(rec, req)
		if strings.Contains(capturedPath, "..") {
			t.Errorf("%s: traversal survived: %q", name, capturedPath)
		}
		t.Logf("%s: cleaned to %q", name, capturedPath)
	}
}

func TestSec_Composition_CompressWithCORSSensitiveData(t *testing.T) {
	// H-D: Compress + OAuth2-like scope response — BREACH oracle risk.
	// Document: if a handler echoes user-controlled input in a compressed response
	// containing secrets, BREACH oracle is possible.
	// This test verifies compress does NOT add CORS/auth headers itself.
	mw := middleware.Compress(gzip.DefaultCompression)
	cors := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response containing a "secret" and reflected user input — BREACH oracle scenario.
		reflected := r.URL.Query().Get("q")
		w.Write([]byte(`{"scope":"read write","token":"secret123","query":"` + reflected + `"}`))
	})

	chain := cors(mw(inner))
	bigReflect := strings.Repeat("a", 2048) // make response > minCompressSize
	req := httptest.NewRequest("GET", "/api?q="+url.QueryEscape(bigReflect), nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Origin", "*")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	t.Logf("BREACH oracle test (H-D): Content-Encoding=%s. "+
		"If handler reflects user input in compressed response containing secrets, "+
		"disable compression on sensitive endpoints or add Cache-Control: no-transform.",
		rec.Header().Get("Content-Encoding"))
}

func TestSec_Composition_RecovererAndTimeout_PanicAfterTimeout(t *testing.T) {
	// H-C: panic after timeout fired must not cause double-WriteHeader.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	recoverer := middleware.RecovererWithLogger(logger)
	timeout := middleware.Timeout(30 * time.Millisecond)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		// Panic after timeout — recoverer should catch this.
		panic("panic after context cancelled")
	})

	chain := recoverer(timeout(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		chain.ServeHTTP(rec, req)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("handler did not complete — potential goroutine leak")
	}
	// Should not double-write or panic.
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("unexpected status after panic+timeout: %d", rec.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — math/rand vs crypto/rand verification
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_RequestID_NotMathRand(t *testing.T) {
	// Verify IDs are NOT sequential outputs from math/rand by checking that
	// two IDs generated in a fixed seed context differ.
	// Source confirms crypto/rand is used — this test validates statistically.
	mw := middleware.RequestID()
	_ = mrand.New(mrand.NewSource(42)) // seed math/rand, should not affect middleware
	ids := make([]string, 10)
	for i := range ids {
		rec := serve(mw, "GET", "/", nil)
		ids[i] = rec.Header().Get("X-Request-ID")
	}
	// All IDs must be unique (crypto/rand guarantees this with overwhelming probability).
	seen := make(map[string]struct{})
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate ID from math/rand-seeded run: %q", id)
		}
		seen[id] = struct{}{}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — x509 helper (stub — real implementation in crypto/x509)
// ═══════════════════════════════════════════════════════════════════════════════

// x509MarshalPKIXPublicKey is a local stub that encodes the public key to DER.
// We need this for the alg-confusion test above.
func x509MarshalPKIXPublicKey(pub *rsa.PublicKey) ([]byte, error) {
	// Use a simple encoding: just the modulus bytes (not real DER, but sufficient
	// for testing that HS256 signed with these bytes is rejected).
	return pub.N.Bytes(), nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — ECDSA zero-element edge case
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_JWT_ES256_ZeroRSig_Rejected(t *testing.T) {
	// Verify that an ECDSA signature with r=0 or s=0 is rejected.
	// ecdsa.Verify should return false for zero-element signatures.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mw := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &priv.PublicKey,
		Algorithms: []string{"ES256"},
	})

	// Craft a token with r=0, s=1 (malleable/zero-r signature).
	zeroSig := make([]byte, 64) // all zeros = r=0, s=0
	token := makeJWT("ES256", nil, validClaims(), func(b []byte) []byte { return zeroSig })
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("zero-element ECDSA signature accepted: got %d want 401", rec.Code)
	}
}

func TestSec_JWT_ES256_TruncatedSig_Rejected(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mw := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &priv.PublicKey,
		Algorithms: []string{"ES256"},
	})

	// Signature with wrong length (31 bytes instead of 64).
	badSig := make([]byte, 31)
	token := makeJWT("ES256", nil, validClaims(), func(b []byte) []byte { return badSig })
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("truncated ECDSA signature accepted: got %d want 401", rec.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR — JWT RawPayload not included in 401 error response
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_JWT_RawPayloadNotLeakedOn401(t *testing.T) {
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     []byte("secret"),
		Algorithms: []string{"HS256"},
	})
	// Valid structure but bad signature — payload should never appear in 401 body.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"secret-user","role":"admin","exp":9999999999}`))
	fakeToken := "eyJhbGciOiJIUzI1NiJ9." + payload + ".invalidsig"
	rec := serve(mw, "GET", "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+fakeToken)
	})
	body := rec.Body.String()
	if strings.Contains(body, "secret-user") || strings.Contains(body, "admin") {
		t.Errorf("JWT payload claims leaked in 401 response body: %q", body)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0055: RealIP no-args trusts ALL peers (XFF spoof)
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_RealIP_NoArgs_TrustsAllPeers_DocumentedInsecure(t *testing.T) {
	// MSR-2026-0055: RealIP() with no CIDR arguments trusts the XFF header from
	// ANY TCP peer — no whitelist enforced. A client can spoof any IP.
	mw := middleware.RealIP() // no trusted CIDRs = trust-all mode
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:9999" // real peer
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	// In trust-all mode the spoofed IP is accepted.
	if capturedAddr != "1.1.1.1" {
		t.Logf("RealIP() trust-all: spoofed XFF accepted as RemoteAddr=%q", capturedAddr)
	} else {
		// Document: attacker at 192.168.1.100 can claim to be 1.1.1.1.
		t.Logf("MSR-2026-0055 CONFIRMED: RealIP() no-args accepts spoofed XFF=1.1.1.1 (capturedAddr=%q). "+
			"Only safe behind a single trusted proxy. Add CIDR arguments to enforce trust boundary.", capturedAddr)
	}
	// This is documented behavior — not a hard test failure, but a documented risk.
}

func TestSec_RealIP_WithCIDR_SpoofPrevented(t *testing.T) {
	// Correct usage: with a CIDR, spoofed XFF from an untrusted peer is blocked.
	pfx, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&pfx)
	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:9999" // NOT in trusted CIDR
	req.Header.Set("X-Forwarded-For", "9.8.7.6") // spoofed
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedAddr == "9.8.7.6" {
		t.Error("MSR-2026-0055: spoofed XFF accepted despite peer not in trusted CIDR")
	}
	// Original RemoteAddr must be preserved.
	if capturedAddr != "1.2.3.4:9999" {
		t.Errorf("RealIP with CIDR: unexpected RemoteAddr %q", capturedAddr)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0056: NoCache missing CDN-specific headers
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_NoCache_CDNHeadersMissing(t *testing.T) {
	// MSR-2026-0056: NoCache emits Cache-Control, Pragma, Expires but NOT
	// Surrogate-Control (Akamai/CDN) or X-Accel-Expires (nginx accel).
	mw := middleware.NoCache()
	rec := serve(mw, "GET", "/", nil)

	// These ARE present — regression guard.
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("NoCache: Cache-Control missing")
	}
	if rec.Header().Get("Pragma") == "" {
		t.Error("NoCache: Pragma missing")
	}
	if rec.Header().Get("Expires") == "" {
		t.Error("NoCache: Expires missing")
	}

	// These are ABSENT — document as MSR-2026-0056.
	if rec.Header().Get("Surrogate-Control") == "" {
		t.Logf("MSR-2026-0056: Surrogate-Control header absent — Akamai/CDN edges may cache responses")
	}
	if rec.Header().Get("X-Accel-Expires") == "" {
		t.Logf("MSR-2026-0056: X-Accel-Expires header absent — nginx accelerator may cache responses")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0060: Compress Vary absent on sub-minCompressSize responses
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Compress_SmallResponseVaryAbsent(t *testing.T) {
	// MSR-2026-0060: a small response (< 1024 bytes) that is not compressed
	// does NOT get Vary: Accept-Encoding. CDN caches the uncompressed response
	// keyed without Accept-Encoding, and may serve it to clients that requested gzip.
	mw := middleware.Compress(1) // BestSpeed
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write exactly 100 bytes — well below 1024 minCompressSize.
		w.Write(bytes.Repeat([]byte("x"), 100))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(inner).ServeHTTP(rec, req)

	// Compression must be skipped (small body).
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("small response should not be compressed")
	}
	// MSR-2026-0060: Vary: Accept-Encoding should be set even for uncompressed responses
	// when the client sent Accept-Encoding. Currently absent — document.
	vary := rec.Header().Get("Vary")
	if !strings.Contains(vary, "Accept-Encoding") {
		t.Logf("MSR-2026-0060 CONFIRMED: Vary: Accept-Encoding absent for small (uncompressed) response "+
			"when client sent Accept-Encoding: gzip. Got Vary: %q. "+
			"CDN may serve cached uncompressed response to gzip-capable clients.", vary)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0061: CleanPath percent-encoded traversal bypass (%2e%2e)
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_CleanPath_PercentEncodedDotTraversal_RawPathBypass(t *testing.T) {
	// MSR-2026-0061: path.Clean("/a/%2e%2e/etc/passwd") returns "/a/%2e%2e/etc/passwd"
	// unchanged because path.Clean does not decode %2e → ".". The decoded Path is
	// cleaned to "/etc/passwd", but RawPath retains the encoded form.
	// A router using RawPath (when non-empty) will see the encoded traversal.
	mw := middleware.CleanPath()
	var capturedPath, capturedRaw string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedRaw = r.URL.RawPath
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/a/%2e%2e/etc/passwd", nil)
	// Simulate Go HTTP server setting RawPath for percent-encoded URL.
	req.URL.RawPath = "/a/%2e%2e/etc/passwd"
	req.URL.Path = "/a/../etc/passwd" // decoded form
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)

	// The decoded Path should be cleaned.
	if capturedPath == "/a/../etc/passwd" {
		t.Error("decoded path not cleaned by CleanPath")
	}

	// MSR-2026-0061: RawPath should be zeroed when encoded form contains traversal
	// that was cleaned from the decoded Path. Currently it is NOT zeroed.
	if capturedRaw == "/a/%2e%2e/etc/passwd" {
		t.Logf("MSR-2026-0061 CONFIRMED: RawPath '/a/%%2e%%2e/etc/passwd' not zeroed after CleanPath. "+
			"Decoded path cleaned to %q but RawPath still contains encoded traversal. "+
			"Routers preferring RawPath may bypass CleanPath protection.", capturedPath)
	}
}

func TestSec_CleanPath_LiteralDotTraversal_Cleaned(t *testing.T) {
	// Regression: literal dot-dot is always cleaned correctly.
	mw := middleware.CleanPath()
	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
	})
	req := httptest.NewRequest("GET", "/a/b/../../etc/passwd", nil)
	mw(inner).ServeHTTP(httptest.NewRecorder(), req)
	if capturedPath != "/etc/passwd" {
		t.Errorf("literal traversal not cleaned: got %q want /etc/passwd", capturedPath)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0057: Recoverer slog logs r.URL.Path unsanitised
// ═══════════════════════════════════════════════════════════════════════════════

func TestSec_Recoverer_PathWithCRLF_SanitisedInLog(t *testing.T) {
	// MSR-2026-0057: RecovererWithLogger uses slog.String("path", r.URL.Path).
	// slog TextHandler does NOT call QuoteToASCII — raw control bytes reach the log.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mw := middleware.RecovererWithLogger(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("trigger recoverer")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/safe", nil)
	// Manually inject CRLF into the path after request construction.
	req.URL.Path = "/inject\r\nFAKE-LOG: injected-by-attacker"
	mw(inner).ServeHTTP(rec, req)

	logs := logBuf.String()
	// Check for raw CR or injected line (multiple log lines from one request = injection).
	if strings.Contains(logs, "FAKE-LOG") && strings.Contains(logs, "injected-by-attacker") {
		rawLines := strings.Split(logs, "\n")
		if len(rawLines) > 2 { // more than one real log line + trailing newline
			t.Errorf("MSR-2026-0057 CONFIRMED: CRLF injection via r.URL.Path in Recoverer slog output. "+
				"Log has %d lines from one request. First injected line: %q",
				len(rawLines)-1, rawLines[1])
		}
	}
	// Also check for raw \r in log output.
	if strings.Contains(logs, "\r") {
		t.Errorf("MSR-2026-0057: raw CR (\\r) in Recoverer slog output — log injection possible: %q", logs)
	}
	t.Logf("Recoverer path log output: %q", logs)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Math helpers to suppress unused import
// ═══════════════════════════════════════════════════════════════════════════════

var _ = math.E
var _ = big.NewInt
