//go:build timing

// jwt_alg_confusion_timing_test.go — Statistical timing analysis for JWTAuth middleware.
//
// PRIORITY HYPOTHESIS #1 (sprint 2026-05-07):
//   JWT algorithm-confusion timing oracle: when a server accepts multiple algorithms,
//   the latency of the rejection path REVEALS which algorithm code-path was executed,
//   allowing an attacker to infer which algorithm was used to sign a token.
//
// Attack scenario:
//   A server configured with Algorithms: ["HS256", "RS256"] will take different time
//   for HS256 (HMAC pool: ~1µs) vs RS256 (RSA verify: ~300µs) paths.
//   Even on rejection (wrong sig), the hash computation or RSA operation leaves a
//   measurable timing trace. An attacker submitting tokens with alg=HS256 vs alg=RS256
//   can distinguish these paths from timing alone.
//
// Additionally tested:
//   3. Empty token vs malformed token vs valid-format-wrong-sig timing.
//   4. HMAC pool hit (warm) vs pool cold (first call) — sync.Pool eviction.
//   5. JWT with alg=none vs alg=HS256 — should be rejected at allowedAlgs check,
//      both paths should have similar timing (both fail at header decode / alg check).
package harness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nJWT = 200_000

// ── JWT token construction helpers ───────────────────────────────────────────

func jwtHeader(alg string) string {
	b, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	return base64.RawURLEncoding.EncodeToString(b)
}

func jwtPayload(sub string) string {
	b, _ := json.Marshal(map[string]any{
		"sub": sub,
		"exp": int64(9999999999), // far future
		"iat": int64(1000000000),
	})
	return base64.RawURLEncoding.EncodeToString(b)
}

// makeHS256Token produces a token signed with the given HMAC key.
func makeHS256Token(secret []byte, sub string) string {
	h := jwtHeader("HS256")
	p := jwtPayload(sub)
	msg := h + "." + p
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return msg + "." + sig
}

// makeHS256TokenWrongSig produces a structurally valid HS256 token with a wrong signature.
func makeHS256TokenWrongSig(sub string) string {
	h := jwtHeader("HS256")
	p := jwtPayload(sub)
	fakeSig := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	return h + "." + p + "." + fakeSig
}

// makeRS256TokenWrongSig produces a structurally valid RS256 token with a wrong signature.
func makeRS256TokenWrongSig(sub string) string {
	h := jwtHeader("RS256")
	p := jwtPayload(sub)
	// A 256-byte fake sig (RSA-2048 size)
	fakeSig := base64.RawURLEncoding.EncodeToString(make([]byte, 256))
	return h + "." + p + "." + fakeSig
}

// makeES256TokenWrongSig produces a structurally valid ES256 token with a wrong signature.
func makeES256TokenWrongSig(sub string) string {
	h := jwtHeader("ES256")
	p := jwtPayload(sub)
	// 64-byte fake sig (P-256: r||s, each 32 bytes)
	fakeSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	return h + "." + p + "." + fakeSig
}

func jwtReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// ── Test: HS256 path vs RS256 path timing (HYPOTHESIS #1) ────────────────────

// TestTiming_JWT_HS256vsRS256_PathLatency is the primary test for hypothesis #1.
// It measures the time to reject an HS256-labelled token vs an RS256-labelled token.
// A statistically significant difference CONFIRMS the alg-confusion timing oracle.
func TestTiming_JWT_HS256vsRS256_PathLatency(t *testing.T) {
	secret := []byte("timing-test-hmac-secret-key-32b!")

	// Generate RSA key for RS256 support.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}

	// Handler configured with BOTH HS256 and RS256.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		PublicKey:  &rsaKey.PublicKey,
		Algorithms: []string{"HS256", "RS256"},
	})(inner)

	// Use tokens with wrong signatures so both always fail at the verify step
	// (not at the alg-check step). This isolates the crypto path timing.
	hs256Token := makeHS256TokenWrongSig("user1")
	rs256Token := makeRS256TokenWrongSig("user1")

	var hs256Samples, rs256Samples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup — warm the HMAC pool and RSA verify path.
	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, jwtReq(hs256Token))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, jwtReq(rs256Token))
	}

	hs256Samples = make([]int64, nJWT)
	rs256Samples = make([]int64, nJWT)

	for i := 0; i < nJWT; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, jwtReq(hs256Token))
		hs256Samples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, jwtReq(rs256Token))
		rs256Samples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(hs256Samples, rs256Samples)
	hs := Summarise(hs256Samples)
	rs := Summarise(rs256Samples)

	t.Logf("JWT HS256 vs RS256 path timing (N=%d each)", nJWT)
	t.Logf("  HS256: mean=%.1fns (%.1fµs) std=%.1fns p50=%.0fns p99=%.0fns",
		hs.Mean, hs.Mean/1000, hs.Std, hs.P50, hs.P99)
	t.Logf("  RS256: mean=%.1fns (%.1fµs) std=%.1fns p50=%.0fns p99=%.0fns",
		rs.Mean, rs.Mean/1000, rs.Std, rs.P50, rs.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns (%.2fµs)",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.MeanDiffNs/1000)

	// Hypothesis #1 CONFIRMATION: if the paths are distinguishable, the oracle is real.
	// RSA verify takes ~200-400µs; HMAC takes ~1µs — these WILL be distinguishable.
	// We report this as a CONFIRMED finding (not a test failure per se, since the
	// behavior is architectural, but we classify severity).
	if result.Leak {
		t.Logf("HYPOTHESIS #1 CONFIRMED: JWT alg-confusion timing oracle detected.")
		t.Logf("  HS256 path: %.1fµs mean | RS256 path: %.1fµs mean | diff: %.1fµs",
			hs.Mean/1000, rs.Mean/1000, result.MeanDiffNs/1000)
		// Classify severity based on effect size.
		if result.MeanDiffNs > 10_000 { // 10µs threshold
			t.Logf("  SEVERITY: HIGH — %.0fµs difference is easily measurable from a network attacker",
				result.MeanDiffNs/1000)
		}
	} else {
		t.Logf("  Hypothesis #1: paths are NOT statistically distinguishable at p<0.01 threshold")
	}
}

// TestTiming_JWT_HS256vsES256_PathLatency tests ECDSA path vs HMAC path.
func TestTiming_JWT_HS256vsES256_PathLatency(t *testing.T) {
	secret := []byte("timing-test-hmac-secret-key-32b!")

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		PublicKey:  &ecKey.PublicKey,
		Algorithms: []string{"HS256", "ES256"},
	})(inner)

	hs256Token := makeHS256TokenWrongSig("user1")
	es256Token := makeES256TokenWrongSig("user1")

	var hs256Samples, es256Samples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, jwtReq(hs256Token))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, jwtReq(es256Token))
	}

	hs256Samples = make([]int64, nJWT)
	es256Samples = make([]int64, nJWT)

	for i := 0; i < nJWT; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, jwtReq(hs256Token))
		hs256Samples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, jwtReq(es256Token))
		es256Samples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(hs256Samples, es256Samples)
	hs := Summarise(hs256Samples)
	es := Summarise(es256Samples)

	t.Logf("JWT HS256 vs ES256 path timing (N=%d each)", nJWT)
	t.Logf("  HS256: mean=%.1fns (%.1fµs) p50=%.0fns", hs.Mean, hs.Mean/1000, hs.P50)
	t.Logf("  ES256: mean=%.1fns (%.1fµs) p50=%.0fns", es.Mean, es.Mean/1000, es.P50)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fµs",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs/1000)
	if result.Leak {
		t.Logf("JWT HS256 vs ES256 alg-path timing CONFIRMED distinguishable: diff=%.2fµs",
			result.MeanDiffNs/1000)
	}
}

// TestTiming_JWT_AlgNoneVsHS256 tests that alg=none rejection timing matches
// HS256 rejection timing (both fail at allowedAlgs check, before any crypto).
func TestTiming_JWT_AlgNoneVsHS256(t *testing.T) {
	secret := []byte("timing-test-hmac-secret-key-32b!")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(inner)

	// alg=none token — should be rejected at allowedAlgs check
	hdrNone := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	noneToken := hdrNone + "." + jwtPayload("user1") + "."

	hs256WrongSig := makeHS256TokenWrongSig("user1")

	var noneSamples, hs256Samples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 20_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, jwtReq(noneToken))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, jwtReq(hs256WrongSig))
	}

	noneSamples = make([]int64, nJWT)
	hs256Samples = make([]int64, nJWT)

	for i := 0; i < nJWT; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, jwtReq(noneToken))
		noneSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, jwtReq(hs256WrongSig))
		hs256Samples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(noneSamples, hs256Samples)
	ns := Summarise(noneSamples)
	hs := Summarise(hs256Samples)

	t.Logf("JWT alg=none vs HS256 timing (N=%d each)", nJWT)
	t.Logf("  none:  mean=%.1fns p50=%.0fns", ns.Mean, ns.P50)
	t.Logf("  HS256: mean=%.1fns p50=%.0fns", hs.Mean, hs.P50)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// alg=none fails at allowedAlgs[hdr.Alg] check BEFORE crypto; HS256-wrong-sig
	// proceeds through HMAC computation. These WILL differ — that is expected and
	// NOT an alg-confusion vulnerability.
	// The finding of interest is only the multi-alg server case (HS256 vs RS256 above).
}

// TestTiming_JWT_ECDSA_ZeroSig tests ECDSA verify with zeroed r,s values.
// Per the sprint sprint: "verifyECDSAJWT uses ecdsa.Verify directly with raw r,s
// from sig — confirm no zero-element / p-1 edge cases bypass".
// Zero r,s should return false quickly (big.Int.SetBytes([]byte{0...}) == 0,
// ecdsa.Verify returns false for zero). This test verifies no timing anomaly.
func TestTiming_JWT_ECDSA_ZeroSig(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &ecKey.PublicKey,
		Algorithms: []string{"ES256"},
	})(inner)

	// Zero sig: r=0||s=0 (64 zero bytes).
	zeroSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	zeroToken := jwtHeader("ES256") + "." + jwtPayload("user1") + "." + zeroSig

	// Max r/s (p-1 values for P-256).
	// P-256 order: 0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551
	pMinus1 := make([]byte, 32)
	for i := range pMinus1 {
		pMinus1[i] = 0xff
	}
	maxSig := base64.RawURLEncoding.EncodeToString(append(pMinus1, pMinus1...))
	maxToken := jwtHeader("ES256") + "." + jwtPayload("user1") + "." + maxSig

	var zeroSamples, maxSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 20_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, jwtReq(zeroToken))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, jwtReq(maxToken))
	}

	const n = 100_000
	zeroSamples = make([]int64, n)
	maxSamples = make([]int64, n)

	for i := 0; i < n; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, jwtReq(zeroToken))
		zeroSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, jwtReq(maxToken))
		maxSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(zeroSamples, maxSamples)
	zs := Summarise(zeroSamples)
	ms := Summarise(maxSamples)

	t.Logf("ECDSA zero-sig vs max-sig timing (N=%d each)", n)
	t.Logf("  Zero sig: mean=%.1fns p50=%.0fns", zs.Mean, zs.P50)
	t.Logf("  Max  sig: mean=%.1fns p50=%.0fns", ms.Mean, ms.P50)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// ECDSA verify on invalid/degenerate inputs should not take meaningfully longer
	// than on random invalid inputs. A large timing difference here could indicate
	// a precomputation or early-exit shortcut leaking the validity of r,s.
	if result.MeanDiffNs > 50_000 { // 50µs
		t.Logf("NOTE: Large timing difference between zero-sig and max-sig (%.2fµs) — "+
			"investigate ECDSA verify implementation for edge-case timing variation",
			result.MeanDiffNs/1000)
	}
}

// TestTiming_JWT_HMACPool_WarmVsCold tests that the jwtHMACPool sync.Pool
// does not introduce a cold-start timing signal (first call significantly slower).
func TestTiming_JWT_HMACPool_WarmVsCold(t *testing.T) {
	secret := []byte("timing-test-hmac-secret-key-32b!")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(inner)

	validToken := makeHS256Token(secret, "user1")

	// Measure "warm" (after many calls) vs ensure the pool is in a known state.
	// We test this by comparing first-N vs last-N calls in a long sequence.
	const n = 100_000

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	allSamples := make([]int64, n)
	for i := 0; i < n; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, jwtReq(validToken))
		allSamples[i] = time.Since(t0).Nanoseconds()
	}

	firstQuarter := allSamples[:n/4]
	lastQuarter := allSamples[3*n/4:]

	result := RunTests(firstQuarter, lastQuarter)
	fs := Summarise(firstQuarter)
	ls := Summarise(lastQuarter)

	t.Logf("JWT HMAC pool warm vs cold (first/last quarter, N=%d each)", n/4)
	t.Logf("  First 25%%: mean=%.1fns p50=%.0fns", fs.Mean, fs.P50)
	t.Logf("  Last  25%%: mean=%.1fns p50=%.0fns", ls.Mean, ls.P50)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// Pool warm-up should not produce a statistically significant difference.
	if result.Leak && result.MeanDiffNs > 500 {
		t.Logf("NOTE: HMAC pool shows temporal drift of %.2fns — "+
			"may indicate pool eviction or CPU frequency scaling artefact",
			result.MeanDiffNs)
	}
}

// bigIntFromBytes is a helper for constructing big.Int from bytes.
func bigIntFromBytes(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}
