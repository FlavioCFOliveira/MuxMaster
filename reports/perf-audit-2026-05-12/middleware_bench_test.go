// Isolated benchmarks for every MuxMaster middleware.
// Run with: go test -bench=. -benchmem -count=5 ./reports/perf-audit-2026-05-12/
package muxmaster_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// nopHandler is a zero-cost sink handler.
var nopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

// sink discards all writes, keeping the interface satisfied without I/O cost.
type discardWriter struct{ io.Writer }

var discardBuf = &bytes.Buffer{}

// makeHS256Token produces a valid HS256 JWT for benchmarking purposes.
func makeHS256Token(secret []byte, expOffset time.Duration) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	exp := time.Now().Add(expOffset).Unix()
	payload, _ := json.Marshal(map[string]any{"sub": "user1", "exp": exp})
	pay := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := hdr + "." + pay
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func makeES256Token(key *ecdsa.PrivateKey, expOffset time.Duration) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	exp := time.Now().Add(expOffset).Unix()
	payload, _ := json.Marshal(map[string]any{"sub": "user1", "exp": exp})
	pay := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := hdr + "." + pay
	h := sha256.Sum256([]byte(sigInput))
	r, s, _ := ecdsa.Sign(rand.Reader, key, h[:])
	keySize := 32
	sig := make([]byte, 2*keySize)
	rb := r.Bytes()
	sb := s.Bytes()
	copy(sig[keySize-len(rb):keySize], rb)
	copy(sig[2*keySize-len(sb):], sb)
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ── APIKey ────────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_APIKey_Hit measures the hot path (valid key, success).
func BenchmarkMiddleware_APIKey_Hit(b *testing.B) {
	const rawKey = "supersecretapikey123"
	h := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{rawKey: "identity-1"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	r.Header.Set("X-API-Key", rawKey)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkMiddleware_APIKey_Miss measures the rejection path (invalid key).
func BenchmarkMiddleware_APIKey_Miss(b *testing.B) {
	h := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"goodkey": "identity-1"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	r.Header.Set("X-API-Key", "wrongkey")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// ── BasicAuth ─────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_BasicAuth_Hit measures a successful auth (constant-time SHA256).
func BenchmarkMiddleware_BasicAuth_Hit(b *testing.B) {
	h := middleware.BasicAuth("Test", map[string]string{"alice": "password123"})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secret", nil)
	r.SetBasicAuth("alice", "password123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkMiddleware_BasicAuth_Miss measures a rejected auth.
func BenchmarkMiddleware_BasicAuth_Miss(b *testing.B) {
	h := middleware.BasicAuth("Test", map[string]string{"alice": "password123"})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secret", nil)
	r.SetBasicAuth("alice", "wrongpassword")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// ── CleanPath ─────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_CleanPath_Clean measures the fast path (path already clean).
func BenchmarkMiddleware_CleanPath_Clean(b *testing.B) {
	h := middleware.CleanPath()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/list", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_CleanPath_Dirty measures the slow path (path needs cleaning, triggers r.Clone).
func BenchmarkMiddleware_CleanPath_Dirty(b *testing.B) {
	h := middleware.CleanPath()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users//list/../list", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── Compress ──────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_Compress_NoGzip measures overhead when client does NOT accept gzip.
func BenchmarkMiddleware_Compress_NoGzip(b *testing.B) {
	h := middleware.Compress(-1)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data", nil)
	// no Accept-Encoding: gzip

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_Compress_SmallBody measures overhead when gzip is accepted but body < 1 KB (skip).
func BenchmarkMiddleware_Compress_SmallBody(b *testing.B) {
	smallPayload := []byte(strings.Repeat("x", 512))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(smallPayload)
	})
	h := middleware.Compress(-1)(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkMiddleware_Compress_LargeBody measures cost with a body large enough to compress.
func BenchmarkMiddleware_Compress_LargeBody(b *testing.B) {
	largePayload := []byte(strings.Repeat("Hello World! ", 200)) // ~2.6 KB
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(largePayload)
	})
	h := middleware.Compress(-1)(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// ── CORS ──────────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_CORS_NoOrigin measures the fast path (no Origin header).
func BenchmarkMiddleware_CORS_NoOrigin(b *testing.B) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_CORS_AllowedOrigin measures the path with a matching origin.
func BenchmarkMiddleware_CORS_AllowedOrigin(b *testing.B) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api", nil)
	r.Header.Set("Origin", "https://example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// BenchmarkMiddleware_CORS_Preflight measures OPTIONS preflight handling.
func BenchmarkMiddleware_CORS_Preflight(b *testing.B) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/api", nil)
	r.Header.Set("Origin", "https://example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// ── JWTAuth ───────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_JWTAuth_HS256_Hit measures valid HS256 token verification.
func BenchmarkMiddleware_JWTAuth_HS256_Hit(b *testing.B) {
	secret := []byte("very-secret-key-for-benchmarks-1234567890")
	token := makeHS256Token(secret, time.Hour)
	h := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkMiddleware_JWTAuth_HS256_Miss measures an invalid HS256 token (wrong signature).
func BenchmarkMiddleware_JWTAuth_HS256_Miss(b *testing.B) {
	secret := []byte("very-secret-key-for-benchmarks-1234567890")
	token := makeHS256Token([]byte("different-secret"), time.Hour)
	h := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkMiddleware_JWTAuth_ES256_Hit measures valid ES256 token verification (more expensive).
func BenchmarkMiddleware_JWTAuth_ES256_Hit(b *testing.B) {
	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	token := makeES256Token(privKey, time.Hour)
	h := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &privKey.PublicKey,
		Algorithms: []string{"ES256"},
	})(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// ── Logger ────────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_Logger measures the fmt.Fprintf + statusRecorder alloc overhead.
func BenchmarkMiddleware_Logger(b *testing.B) {
	h := middleware.Logger(io.Discard)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/list", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// ── NoCache ───────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_NoCache measures the 5 Header().Set calls.
func BenchmarkMiddleware_NoCache(b *testing.B) {
	h := middleware.NoCache()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// ── RealIP ────────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_RealIP_NoHeader measures when no XFF/X-Real-IP header is present.
func BenchmarkMiddleware_RealIP_NoHeader(b *testing.B) {
	cidr, _ := netip.ParsePrefix("10.0.0.0/8")
	h := middleware.RealIP(&cidr)(nopHandler)
	w := httptest.NewRecorder()
	// Use an IP in the trusted range as RemoteAddr.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_RealIP_XFF measures XFF header parsing + IP selection.
func BenchmarkMiddleware_RealIP_XFF(b *testing.B) {
	cidr, _ := netip.ParsePrefix("10.0.0.0/8")
	h := middleware.RealIP(&cidr)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── Recoverer ─────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_Recoverer_NoPanic measures the happy path (no panic — just defer overhead).
func BenchmarkMiddleware_Recoverer_NoPanic(b *testing.B) {
	h := middleware.Recoverer()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── RequestID ─────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_RequestID_Generate measures the path that generates a new UUID (crypto/rand).
func BenchmarkMiddleware_RequestID_Generate(b *testing.B) {
	h := middleware.RequestID()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No X-Request-ID → will generate one.

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// BenchmarkMiddleware_RequestID_Propagate measures the path that reuses a valid incoming ID.
func BenchmarkMiddleware_RequestID_Propagate(b *testing.B) {
	h := middleware.RequestID()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "abc123-valid-id")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// ── SetHeader ─────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_SetHeader measures the single Header().Set call.
func BenchmarkMiddleware_SetHeader(b *testing.B) {
	h := middleware.SetHeader("X-Frame-Options", "DENY")(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
	}
}

// ── StripSlashes ──────────────────────────────────────────────────────────────

// BenchmarkMiddleware_StripSlashes_Clean measures the fast path (no trailing slash).
func BenchmarkMiddleware_StripSlashes_Clean(b *testing.B) {
	h := middleware.StripSlashes()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_StripSlashes_Dirty measures the r.Clone slow path.
func BenchmarkMiddleware_StripSlashes_Dirty(b *testing.B) {
	h := middleware.StripSlashes()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users///", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── Throttle ──────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_ThrottleBacklog_NoWait measures the hot path where a token is immediately available.
func BenchmarkMiddleware_ThrottleBacklog_NoWait(b *testing.B) {
	h := middleware.ThrottleBacklog(100, 0, time.Second)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkMiddleware_ThrottlePerIP_Hit measures per-IP throttle (key already in table).
func BenchmarkMiddleware_ThrottlePerIP_Hit(b *testing.B) {
	h := middleware.ThrottlePerIP(100, time.Second, nil)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:8080"

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── Timeout ───────────────────────────────────────────────────────────────────

// BenchmarkMiddleware_Timeout measures context.WithTimeout + r.WithContext alloc overhead.
func BenchmarkMiddleware_Timeout(b *testing.B) {
	h := middleware.Timeout(time.Second)(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── WithValue ─────────────────────────────────────────────────────────────────

type benchCtxKey struct{}

// BenchmarkMiddleware_WithValue measures context.WithValue + r.WithContext overhead.
func BenchmarkMiddleware_WithValue(b *testing.B) {
	h := middleware.WithValue(benchCtxKey{}, "some-config-value")(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// ── CHAIN BENCHMARKS ──────────────────────────────────────────────────────────

// BenchmarkChain_Production simulates a realistic production middleware stack:
// Recoverer → RealIP → RequestID → Logger → CORS
func BenchmarkChain_Production(b *testing.B) {
	cidr, _ := netip.ParsePrefix("10.0.0.0/8")

	// Build chain innermost-last (Use semantics: last registered = innermost).
	var h http.Handler = nopHandler
	h = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})(h)
	h = middleware.Logger(io.Discard)(h)
	h = middleware.RequestID()(h)
	h = middleware.RealIP(&cidr)(h)
	h = middleware.Recoverer()(h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
		w.Body.Reset()
	}
}

// BenchmarkChain_AuthBasic simulates a protected API route: Recoverer → BasicAuth.
func BenchmarkChain_AuthBasic(b *testing.B) {
	var h http.Handler = nopHandler
	h = middleware.BasicAuth("Realm", map[string]string{"bob": "hunter2"})(h)
	h = middleware.Recoverer()(h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r.SetBasicAuth("bob", "hunter2")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.Body.Reset()
	}
}

// BenchmarkChain_AuthJWT simulates a JWT-protected route: Recoverer → RequestID → JWTAuth.
func BenchmarkChain_AuthJWT(b *testing.B) {
	secret := []byte("very-secret-key-for-chain-bench-1234567890")
	token := makeHS256Token(secret, time.Hour)

	var h http.Handler = nopHandler
	h = middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(h)
	h = middleware.RequestID()(h)
	h = middleware.Recoverer()(h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
		w.Body.Reset()
	}
}

// BenchmarkChain_Security simulates full security stack:
// Recoverer → NoCache → SetHeader → CORS → APIKey.
func BenchmarkChain_Security(b *testing.B) {
	const rawKey = "api-key-for-security-chain-bench"
	var h http.Handler = nopHandler
	h = middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{rawKey: "client-1"},
	})(h)
	h = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})(h)
	h = middleware.SetHeader("X-Frame-Options", "DENY")(h)
	h = middleware.NoCache()(h)
	h = middleware.Recoverer()(h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	r.Header.Set("X-API-Key", rawKey)
	r.Header.Set("Origin", "https://example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
		w.Body.Reset()
	}
}

// BenchmarkChain_Heavy simulates a maximal stack with all non-network middlewares:
// Recoverer → RealIP → RequestID → Logger → NoCache → SetHeader → CORS → BasicAuth
func BenchmarkChain_Heavy(b *testing.B) {
	cidr, _ := netip.ParsePrefix("10.0.0.0/8")
	var h http.Handler = nopHandler
	h = middleware.BasicAuth("Heavy", map[string]string{"alice": "password"})(h)
	h = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
	})(h)
	h = middleware.SetHeader("X-Frame-Options", "DENY")(h)
	h = middleware.NoCache()(h)
	h = middleware.Logger(io.Discard)(h)
	h = middleware.RequestID()(h)
	h = middleware.RealIP(&cidr)(h)
	h = middleware.Recoverer()(h)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/heavy", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("X-Forwarded-For", "203.0.113.42")
	r.SetBasicAuth("alice", "password")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.HeaderMap = nil
		w.Body.Reset()
	}
}

// BenchmarkChain_Minimal simulates the absolute minimal stack: just Recoverer + nop.
// This establishes the floor cost that every other stack adds on top of.
func BenchmarkChain_Minimal(b *testing.B) {
	h := middleware.Recoverer()(nopHandler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}
