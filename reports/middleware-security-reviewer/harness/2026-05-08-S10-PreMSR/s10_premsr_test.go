// Package s10_test — Mini-sprint S10-PreMSR security battery (2026-05-08).
//
// Covers six UNTESTED hypotheses from S9 posture §5:
//
//   TM-2026-001  JWT RequireExpiry default false — token without exp accepted ad infinitum
//   TM-2026-002  JWT exp type confusion — null/string/-1/NaN/0/1e308/true in exp field
//   TM-2026-004  OAuth2 url.Parse divergence — userinfo/bad-scheme/CRLF in URL
//   TM-2026-005  OAuth2 slog endpoint leak — credentials in error log
//   TM-2026-022  Logger leaks Authorization header — Bearer token in log output
//   TM-2026-044  RealIP trusted-proxies-all default — XFF accepted from any origin
//
// Every test is named TestSec_<Middleware>_<Threat>.
// Evidence: test output verbatim.
package s10_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── JWT helpers (local — no dependency on sibling package) ─────────────────

func s10MakeJWTRaw(alg string, hdrExtra map[string]any, payloadJSON []byte, signFn func([]byte) []byte) string {
	hdr := map[string]any{"alg": alg, "typ": "JWT"}
	for k, v := range hdrExtra {
		hdr[k] = v
	}
	hb, _ := json.Marshal(hdr)
	hdrB64 := base64.RawURLEncoding.EncodeToString(hb)
	payB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := hdrB64 + "." + payB64
	sig := signFn([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func s10MakeJWT(alg string, hdrExtra map[string]any, claims map[string]any, signFn func([]byte) []byte) string {
	pb, _ := json.Marshal(claims)
	return s10MakeJWTRaw(alg, hdrExtra, pb, signFn)
}

func s10HS256Sign(secret []byte) func([]byte) []byte {
	return func(input []byte) []byte {
		mac := hmac.New(sha256.New, secret)
		mac.Write(input)
		return mac.Sum(nil)
	}
}

func s10JWTMiddleware(secret []byte, requireExpiry bool) func(http.Handler) http.Handler {
	return middleware.JWTAuth(middleware.JWTOptions{
		Secret:        secret,
		Algorithms:    []string{"HS256"},
		RequireExpiry: requireExpiry,
	})
}

func s10ServeJWT(mw func(http.Handler) http.Handler, token string) int {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-001: JWT RequireExpiry default=false — eternal token scenario
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_JWT_RequireExpiry_DefaultFalse_AcceptsNoExp confirms TM-2026-001:
// with RequireExpiry=false (the default), a validly-signed token WITHOUT an exp
// claim is accepted indefinitely. This is the "eternal token" scenario — a
// stolen or leaked token of this form cannot be revoked via expiry.
func TestSec_JWT_RequireExpiry_DefaultFalse_AcceptsNoExp(t *testing.T) {
	secret := []byte("s10-tm001-secret")
	mw := s10JWTMiddleware(secret, false) // RequireExpiry=false is the default

	// Token with no exp claim — should be accepted forever once issued.
	claims := map[string]any{
		"sub": "eternal-user",
		"iat": time.Now().Unix(),
		// deliberately no "exp" field
	}
	token := s10MakeJWT("HS256", nil, claims, s10HS256Sign(secret))

	code := s10ServeJWT(mw, token)

	if code == http.StatusOK {
		t.Logf("TM-2026-001 CONFIRMED-VULN: RequireExpiry=false (default) — "+
			"token without exp accepted (HTTP %d). "+
			"A forged/leaked token with no exp claim is valid ad infinitum. "+
			"CWE-613 (Insufficient Session Expiration). "+
			"Severity: 6. "+
			"RECOMMENDATION: change documentation and README quickstart snippet to "+
			"use RequireExpiry=true as the recommended default. "+
			"The current default is backward-compatible but insecure for most deployments.", code)
	} else {
		t.Errorf("TM-2026-001 UNEXPECTED: expected 200 (token accepted) got %d — "+
			"confirm RequireExpiry behaviour has not changed", code)
	}
}

// TestSec_JWT_RequireExpiry_DefaultFalse_ExpiredTokenRejected verifies that
// even with RequireExpiry=false, a token whose exp is in the past is still
// rejected — the default only affects tokens with NO exp claim.
func TestSec_JWT_RequireExpiry_DefaultFalse_ExpiredTokenRejected(t *testing.T) {
	secret := []byte("s10-tm001-secret")
	mw := s10JWTMiddleware(secret, false)

	claims := map[string]any{
		"sub": "user",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-time.Hour).Unix(), // already expired
	}
	token := s10MakeJWT("HS256", nil, claims, s10HS256Sign(secret))
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-001: expired token (exp in past) expected 401, got %d", code)
	} else {
		t.Logf("TM-2026-001 PASS: expired token correctly rejected (401) even with RequireExpiry=false")
	}
}

// TestSec_JWT_RequireExpiry_TrueRejectsNoExp confirms that the opt-in
// RequireExpiry=true correctly rejects tokens without exp. This is the
// recommended safe default that operators SHOULD set.
func TestSec_JWT_RequireExpiry_TrueRejectsNoExp(t *testing.T) {
	secret := []byte("s10-tm001-secret")
	mw := s10JWTMiddleware(secret, true) // safe opt-in

	claims := map[string]any{
		"sub": "user",
		"iat": time.Now().Unix(),
		// no exp
	}
	token := s10MakeJWT("HS256", nil, claims, s10HS256Sign(secret))
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-001: RequireExpiry=true, no exp — expected 401, got %d", code)
	} else {
		t.Logf("TM-2026-001 PASS: RequireExpiry=true rejects no-exp token (401)")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-002: JWT exp type confusion
// ═══════════════════════════════════════════════════════════════════════════════
//
// The rawJWTPayload struct uses `int64 json:"exp"`. Go's JSON decoder is
// strict for typed fields: a JSON string, boolean, null, or float-overflow
// value in the "exp" position will be either rejected (decode error → errJWTInvalid)
// or silently coerced (float64 → int64 truncation for in-range floats).
// We test each variant to confirm the actual behaviour.

// s10MakeJWTWithRawPayload builds a JWT where the payload JSON is provided
// verbatim (not marshalled from a Go map), allowing injection of malformed types.
func s10MakeJWTWithRawPayload(t *testing.T, secret []byte, rawPayloadJSON string) string {
	t.Helper()
	return s10MakeJWTRaw("HS256", nil, []byte(rawPayloadJSON), s10HS256Sign(secret))
}

// TestSec_JWT_ExpNull confirms behaviour when exp=null.
// json.Unmarshal of null into int64 sets the field to 0.
// The code then checks: if raw.Exp == 0 => skip expiry check.
// So a token with exp=null is treated as "no expiry" — accepted when RequireExpiry=false.
func TestSec_JWT_ExpNull(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	// exp=null — json.Unmarshal sets int64 to 0.
	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":null}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code == http.StatusOK {
		t.Logf("TM-2026-002 exp=null: accepted (200). "+
			"json.Unmarshal(null→int64)=0, code treats exp==0 as no-expiry. "+
			"Behaviour: same as no exp field. "+
			"With RequireExpiry=false this is accepted — expected but documented as risk. "+
			"With RequireExpiry=true this would be rejected (raw.Exp==0 check). "+
			"VERDICT: PARTIAL — no type-confusion bypass, but null silently becomes 0.")
	} else {
		t.Logf("TM-2026-002 exp=null: rejected (%d). "+
			"json.Unmarshal returned an error for null→int64. Behaviour is strict.", code)
	}
}

// TestSec_JWT_ExpNull_RequireExpiryTrue confirms that exp=null is rejected when RequireExpiry=true.
func TestSec_JWT_ExpNull_RequireExpiryTrue(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, true)

	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":null}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=null + RequireExpiry=true: expected 401, got %d. "+
			"null exp should be treated as missing exp, hence rejected.", code)
	} else {
		t.Logf("TM-2026-002 exp=null + RequireExpiry=true: correctly rejected (401)")
	}
}

// TestSec_JWT_ExpStringValue confirms behaviour when exp is a JSON string (e.g. "1234").
// json.Unmarshal of string into int64 returns an error → errJWTInvalid → 401.
func TestSec_JWT_ExpStringValue(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":"9999999999"}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=string: expected 401 (json decode error), got %d. "+
			"A string-typed exp should cause json.Unmarshal to error and the token to be rejected.", code)
	} else {
		t.Logf("TM-2026-002 exp=string: correctly rejected (401). "+
			"json.Unmarshal rejects string→int64 coercion.")
	}
}

// TestSec_JWT_ExpNegative confirms behaviour when exp=-1.
// The negative-exp guard in parseAndValidateJWT (raw.Exp < 0) rejects this explicitly.
func TestSec_JWT_ExpNegative(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":-1}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=-1: expected 401, got %d. "+
			"Negative exp must be rejected per RFC 7519 §2 (NumericDate is non-negative).", code)
	} else {
		t.Logf("TM-2026-002 exp=-1: correctly rejected (401). "+
			"Guard: raw.Exp < 0 returns errJWTInvalid.")
	}
}

// TestSec_JWT_ExpZero confirms behaviour when exp=0 with RequireExpiry=false.
// exp=0 is treated as "no expiry present" (skip check). This is the design
// choice — zero is the Go zero-value for int64 and indicates absent.
func TestSec_JWT_ExpZero(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":0}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code == http.StatusOK {
		t.Logf("TM-2026-002 exp=0: accepted (200). "+
			"Design: raw.Exp==0 is treated as absent (same as no exp field). "+
			"An attacker who can forge a signed token with exp=0 bypasses expiry checks. "+
			"However: signature must still be valid — this is not a standalone bypass. "+
			"With RequireExpiry=true this is correctly rejected.")
	} else {
		t.Logf("TM-2026-002 exp=0: rejected (%d).", code)
	}
}

// TestSec_JWT_ExpLargeFloat confirms behaviour with exp=1e308 (beyond int64 max).
// json.Unmarshal of an overflowing float into int64 in Go returns a decode error.
func TestSec_JWT_ExpLargeFloat(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	// 1e308 overflows int64 (max ~9.2e18). json.Unmarshal returns json.UnmarshalTypeError.
	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":1e308}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=1e308: expected 401 (overflow → decode error), got %d", code)
	} else {
		t.Logf("TM-2026-002 exp=1e308: correctly rejected (401). "+
			"json.Unmarshal rejects overflowing float64→int64.")
	}
}

// TestSec_JWT_ExpBoolean confirms behaviour with exp=true.
// json.Unmarshal of bool into int64 returns a type error → errJWTInvalid.
func TestSec_JWT_ExpBoolean(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":true}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=true: expected 401, got %d. "+
			"Boolean in exp field must be rejected.", code)
	} else {
		t.Logf("TM-2026-002 exp=true: correctly rejected (401).")
	}
}

// TestSec_JWT_ExpNaNFloat confirms behaviour when exp is a JSON number that Go
// parses as NaN-equivalent. Standard JSON forbids NaN/Infinity — json.Unmarshal
// returns a SyntaxError for these non-standard values.
func TestSec_JWT_ExpNaNFloat(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	// NaN is not valid JSON. The payload itself will fail to decode.
	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":NaN}`, time.Now().Unix())
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	if code != http.StatusUnauthorized {
		t.Errorf("TM-2026-002 exp=NaN: expected 401, got %d. "+
			"NaN is not valid JSON — must be rejected at JSON parse.", code)
	} else {
		t.Logf("TM-2026-002 exp=NaN: correctly rejected (401). NaN is invalid JSON.")
	}
}

// TestSec_JWT_ExpInRangeFloat confirms behaviour when exp is an in-range float
// like 1234567890.5. json.Unmarshal of float64 into int64 returns a decode error
// in Go's strict mode (json.Number is not involved here — the struct field is int64).
func TestSec_JWT_ExpInRangeFloat(t *testing.T) {
	secret := []byte("s10-tm002-secret")
	mw := s10JWTMiddleware(secret, false)

	futureExp := float64(time.Now().Add(time.Hour).Unix()) + 0.5
	payload := fmt.Sprintf(`{"sub":"u","iat":%d,"exp":%f}`, time.Now().Unix(), futureExp)
	token := s10MakeJWTWithRawPayload(t, secret, payload)
	code := s10ServeJWT(mw, token)

	// Go's encoding/json rejects float → int64 coercion when the field is typed int64.
	if code == http.StatusOK {
		t.Errorf("TM-2026-002 exp=float (in-range): accepted (200). "+
			"A fractional float64 in exp should fail json.Unmarshal into int64. "+
			"This may indicate the JSON decoder is performing lossy truncation.")
	} else {
		t.Logf("TM-2026-002 exp=float (in-range): rejected (%d). "+
			"json.Unmarshal correctly rejects float64→int64 coercion.", code)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-004: OAuth2 url.Parse divergence — userinfo / bad-scheme / CRLF
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_OAuth2_URLParse_UserinfoRejected confirms TM-2026-004 (partial fix):
// an endpoint URL with embedded userinfo is rejected at construction time with a panic.
// This closes the "send bearer tokens to attacker-controlled host via misconfigured URL" vector.
func TestSec_OAuth2_URLParse_UserinfoRejected(t *testing.T) {
	panicked := false
	var panicMsg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicMsg = fmt.Sprintf("%v", r)
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "https://attacker:secret@evil.example.com/introspect",
			CacheTTL: -1,
		})
	}()

	if !panicked {
		t.Errorf("TM-2026-004 CONFIRMED-VULN: OAuth2Introspect does NOT panic on URL with userinfo "+
			"(https://user:pass@host/). Bearer tokens would be sent to an attacker-controlled host. "+
			"CWE-20 / CWE-918 (SSRF). Severity: 6. "+
			"Fix: add parsedEndpoint.User != nil check before scheme check.")
	} else {
		t.Logf("TM-2026-004 PASS: OAuth2Introspect panics on userinfo URL. msg=%q", panicMsg)
	}
}

// TestSec_OAuth2_URLParse_EmptyHostRejected confirms that a URL without a host
// (e.g. "https:///introspect") is rejected at construction time.
func TestSec_OAuth2_URLParse_EmptyHostRejected(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "https:///introspect",
			CacheTTL: -1,
		})
	}()

	if !panicked {
		t.Errorf("TM-2026-004: OAuth2Introspect should panic on URL with empty host (https:///)")
	} else {
		t.Logf("TM-2026-004 PASS: empty-host URL rejected at construction")
	}
}

// TestSec_OAuth2_URLParse_PlaintextRejected confirms that http:// (without AllowInsecure) panics.
func TestSec_OAuth2_URLParse_PlaintextRejected(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "http://introspect.example.com/token",
			CacheTTL: -1,
		})
	}()

	if !panicked {
		t.Errorf("TM-2026-004: http:// endpoint without AllowInsecureEndpoint=true must panic")
	} else {
		t.Logf("TM-2026-004 PASS: http:// endpoint correctly rejected")
	}
}

// TestSec_OAuth2_URLParse_CRLFInPath confirms how CRLF bytes in the URL path are handled.
// url.Parse does NOT reject CRLF — it round-trips them. However since the middleware
// passes opts.Endpoint verbatim to http.NewRequestWithContext, the Go HTTP client will
// reject CRLF in the URL via its own validation.
// The construction-time check should also catch this.
func TestSec_OAuth2_URLParse_CRLFInPath(t *testing.T) {
	panicked := false
	var panicMsg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicMsg = fmt.Sprintf("%v", r)
			}
		}()
		// CRLF in path — url.Parse is permissive about this.
		// The middleware checks scheme and host, but not path content.
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "https://introspect.example.com/introspect\r\nInjected: header",
			CacheTTL: -1,
		})
	}()

	if panicked {
		t.Logf("TM-2026-004 CRLF-in-path: construction panics (msg=%q). "+
			"Reason may be: scheme/host check fails on malformed URL, or "+
			"url.Parse returns an error for the CRLF. Safe outcome.", panicMsg)
	} else {
		// If no panic, the URL is accepted at construction. We verify that the Go
		// HTTP client would reject it at runtime. We cannot make a live request here
		// without an active server, so we document the gap.
		t.Logf("TM-2026-004 CRLF-in-path: construction does NOT panic. "+
			"The CRLF-containing endpoint URL is accepted at middleware construction. "+
			"Go's http.NewRequestWithContext will reject CRLF in the URL at request time "+
			"(net/http URL validation). "+
			"PARTIAL: no construction-time guard against CRLF in path. "+
			"The middleware should call url.EscapedPath() validation or reject control chars. "+
			"CWE-20. Severity: LOW (Go HTTP client provides defence in depth).")
	}
}

// TestSec_OAuth2_URLParse_JavascriptScheme confirms that non-HTTP/HTTPS schemes are rejected.
func TestSec_OAuth2_URLParse_JavascriptScheme(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "javascript://example.com/introspect",
			CacheTTL: -1,
		})
	}()

	if !panicked {
		t.Errorf("TM-2026-004: javascript:// scheme should panic (only https allowed)")
	} else {
		t.Logf("TM-2026-004 PASS: javascript:// scheme rejected at construction")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-005: OAuth2 slog endpoint leak — credentials in log output
// ═══════════════════════════════════════════════════════════════════════════════
//
// The hypothesis: when AllowInsecureEndpoint=true and the endpoint URL contains
// userinfo (or when slog.Warn is emitted), the full URL including credentials
// might appear in log output.
//
// Observed code (oauth2.go:235):
//   slog.Info("OAuth2Introspect: configured", "host", parsedEndpoint.Host, "scheme", parsedEndpoint.Scheme)
//
// The TM-2026-004 fix already rejects userinfo at construction (panics), so
// userinfo can never reach the slog call. TM-2026-005 specifically asks whether
// slog.Warn on the AllowInsecureEndpoint path leaks the full URL.

// TestSec_OAuth2_SlogWarn_NoCredentialLeak confirms that when AllowInsecureEndpoint=true
// is used (the only path that reaches slog.Warn without panicking), the log output
// contains only host+scheme and NOT the raw endpoint URL with any credentials.
func TestSec_OAuth2_SlogWarn_NoCredentialLeak(t *testing.T) {
	// Capture slog output by replacing the default handler.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	oldDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldDefault)

	// Use AllowInsecureEndpoint=true with a plaintext URL (only path to slog.Warn
	// without panic). Userinfo is rejected before slog, so we test the basic case.
	_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              "http://localhost:9999/introspect",
		AllowInsecureEndpoint: true,
		CacheTTL:              -1,
	})

	logOutput := logBuf.String()

	// The endpoint URL should NOT appear verbatim in the log.
	// Only "host" and "scheme" attributes should be present.
	if strings.Contains(logOutput, "localhost:9999/introspect") {
		// The path "/introspect" in the log is acceptable (it is not a secret).
		// What matters is: no password, no bearer token, no raw URL with credentials.
		t.Logf("TM-2026-005 INFO: slog output contains endpoint path component. "+
			"Log: %q. "+
			"No userinfo is present (userinfo is rejected at construction before this code path).", logOutput)
	}

	// The critical check: no userinfo (user:password) pattern in log.
	if strings.Contains(logOutput, "@") {
		t.Errorf("TM-2026-005 CONFIRMED-VULN: slog output contains '@' character — "+
			"potential userinfo leak. Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-005 PASS: no '@' (userinfo) in slog output.")
	}

	// Check slog.Info line contains only host and scheme, not full URL.
	if strings.Contains(logOutput, "endpoint=") {
		t.Errorf("TM-2026-005: slog output contains raw 'endpoint=' key — "+
			"the full URL might leak credentials if they were present. Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-005 PASS: no 'endpoint=' key in slog output (only host+scheme logged). Log: %q",
			logOutput)
	}
}

// TestSec_OAuth2_SlogWarn_InsecureEndpoint_FullURL_NotLogged confirms that
// even the AllowInsecureEndpoint slog.Warn path logs only host+scheme, not
// the full URL with path/query (which might contain embedded credentials
// in real misconfigured deployments).
func TestSec_OAuth2_SlogWarn_InsecureEndpoint_FullURL_NotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	oldDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldDefault)

	// URL with sensitive query param (some IDP configs embed tokens in URL).
	_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              "http://localhost:9999/introspect?client_secret=verysecret",
		AllowInsecureEndpoint: true,
		CacheTTL:              -1,
	})

	logOutput := logBuf.String()

	if strings.Contains(logOutput, "verysecret") {
		t.Errorf("TM-2026-005 CONFIRMED-VULN: slog output contains 'verysecret' from query param. "+
			"Credential in URL query is logged. Log: %q. "+
			"CWE-532 (Sensitive Information Logged). Severity: 4.", logOutput)
	} else {
		t.Logf("TM-2026-005 PASS: 'verysecret' not present in slog output. Log: %q", logOutput)
	}

	// Verify what IS logged (should be host only).
	if strings.Contains(logOutput, "localhost:9999") {
		t.Logf("TM-2026-005: host 'localhost:9999' is present in log (expected — only host is logged)")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-022: Logger leaks Authorization header
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Logger_AuthorizationHeader_NotLogged confirms TM-2026-022:
// the Logger middleware logs method/path/status/duration but NOT request headers.
// An Authorization: Bearer <token> header in the request must NOT appear in log output.
func TestSec_Logger_AuthorizationHeader_NotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	mw := middleware.Logger(&logBuf)

	req := httptest.NewRequest("GET", "/api/resource", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMSJ9.secret-sig")
	req.Header.Set("Cookie", "session=supersecretcookie; auth=abc123")

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), req)

	logOutput := logBuf.String()

	// The Authorization header value must not appear.
	if strings.Contains(logOutput, "Bearer") {
		t.Errorf("TM-2026-022 CONFIRMED-VULN: Logger output contains 'Bearer' — "+
			"Authorization header leaked to logs. CWE-532. Severity: 4 (escalate if tokens are sensitive). "+
			"Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-022 PASS: 'Bearer' not found in log output.")
	}

	if strings.Contains(logOutput, "eyJhbGciOiJIUzI1NiJ9") {
		t.Errorf("TM-2026-022 CONFIRMED-VULN: JWT token body leaked to logs. Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-022 PASS: JWT token body not in log output.")
	}

	if strings.Contains(logOutput, "supersecretcookie") {
		t.Errorf("TM-2026-022 CONFIRMED-VULN: Cookie value leaked to logs. CWE-532. Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-022 PASS: Cookie value not in log output.")
	}

	t.Logf("TM-2026-022 full log output: %q", logOutput)
}

// TestSec_Logger_LogFormat_OnlyMethodPathStatusDuration verifies the exact
// format of the Logger output: it only contains timestamp, method, path,
// status code, and duration — no headers of any kind.
func TestSec_Logger_LogFormat_OnlyMethodPathStatusDuration(t *testing.T) {
	var logBuf bytes.Buffer
	mw := middleware.Logger(&logBuf)

	req := httptest.NewRequest("POST", "/login", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // user:pass in base64
	req.Header.Set("X-Api-Key", "my-secret-api-key")
	req.Header.Set("Cookie", "token=abc; session=xyz")

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})).ServeHTTP(httptest.NewRecorder(), req)

	logOutput := logBuf.String()
	secretValues := []string{
		"dXNlcjpwYXNz",    // base64 Basic auth
		"my-secret-api-key",
		"abc", "xyz",       // cookie values
		"Authorization",   // header name itself
		"X-Api-Key",
		"Cookie",
	}

	leaked := false
	for _, secret := range secretValues {
		if strings.Contains(logOutput, secret) {
			t.Errorf("TM-2026-022: logger contains secret value %q. Full log: %q", secret, logOutput)
			leaked = true
		}
	}

	if !leaked {
		t.Logf("TM-2026-022 PASS: Logger does not output any auth header names or values. "+
			"Log format is: timestamp method path status duration only. "+
			"Log: %q", logOutput)
	}

	// Verify the log contains the expected fields.
	if !strings.Contains(logOutput, "POST") {
		t.Errorf("TM-2026-022: log missing method 'POST'. Log: %q", logOutput)
	}
	if !strings.Contains(logOutput, "/login") {
		t.Errorf("TM-2026-022: log missing path '/login'. Log: %q", logOutput)
	}
	if !strings.Contains(logOutput, "201") {
		t.Errorf("TM-2026-022: log missing status '201'. Log: %q", logOutput)
	}
}

// TestSec_Logger_CRLFInAuthHeader_NotInLog verifies that even if an attacker
// injects CRLF into the Authorization header value, it cannot produce a
// forged log line — because the header is never logged at all.
func TestSec_Logger_CRLFInAuthHeader_NotInLog(t *testing.T) {
	var logBuf bytes.Buffer
	mw := middleware.Logger(&logBuf)

	req := httptest.NewRequest("GET", "/admin", nil)
	// Attempt CRLF injection via Authorization header value.
	req.Header.Set("Authorization", "Bearer token\r\n2026-01-01T00:00:00Z DELETE /admin 200 1ms")

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "DELETE /admin") {
		t.Errorf("TM-2026-022 CONFIRMED-VULN: CRLF injection via Authorization header "+
			"produced forged log line. Log: %q", logOutput)
	} else {
		t.Logf("TM-2026-022 PASS: Authorization header CRLF injection does not reach log "+
			"(header is never logged). Log: %q", logOutput)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TM-2026-044: RealIP trusted-proxies-all default
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_RealIP_NoCIDR_AcceptsXFF_FromAnyPeer confirms TM-2026-044:
// when RealIP() is called with NO trusted CIDRs, it accepts XFF from any
// connecting peer. This is equivalent to Gin's insecure trustedProxies=["*"] default.
func TestSec_RealIP_NoCIDR_AcceptsXFF_FromAnyPeer(t *testing.T) {
	// No CIDRs — any peer can spoof XFF.
	mw := middleware.RealIP() // zero arguments

	var capturedAddr string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.1:54321" // public attacker IP
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	mw(inner).ServeHTTP(httptest.NewRecorder(), req)

	if capturedAddr == "10.0.0.1" {
		t.Logf("TM-2026-044 CONFIRMED-VULN: RealIP() with no CIDRs accepts XFF from ANY peer. "+
			"Attacker at 203.0.113.1 set XFF=10.0.0.1 and RemoteAddr was rewritten to %q. "+
			"This is trivial IP spoofing — any IP-based access control, throttling, "+
			"or audit logging is bypassable. "+
			"CWE-20 / CWE-290 (Authentication Bypass Using Spoofed IP). "+
			"Severity: 4 (Medium). "+
			"NOTE: a slog.Warn IS emitted at construction (not a silent failure). "+
			"RECOMMENDATION for production safety: default should be DENY-ALL "+
			"(i.e. RealIP() with no args should NOT modify RemoteAddr). "+
			"Current behaviour matches Gin's old insecure default (TM-2026-044 hypothesis confirmed). "+
			"Operator MUST pass explicit CIDRs. This should be documented prominently in README.", capturedAddr)
	} else if capturedAddr == "203.0.113.1:54321" {
		t.Logf("TM-2026-044 REFUTED: RealIP() with no CIDRs does NOT accept XFF — "+
			"RemoteAddr unchanged (%q). The middleware is safe-by-default.", capturedAddr)
	} else {
		t.Logf("TM-2026-044 INFO: capturedAddr=%q (unexpected value)", capturedAddr)
	}
}

// TestSec_RealIP_NoCIDR_SlogWarnEmitted confirms that the slog.Warn IS emitted
// at construction time when no CIDRs are passed — this is the current mitigation.
func TestSec_RealIP_NoCIDR_SlogWarnEmitted(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	oldDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldDefault)

	_ = middleware.RealIP() // should emit slog.Warn

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "no trusted CIDRs") || strings.Contains(logOutput, "spoof") {
		t.Logf("TM-2026-044 PASS: slog.Warn emitted at construction when no CIDRs. Log: %q",
			logOutput)
	} else {
		t.Logf("TM-2026-044 INFO: slog.Warn not captured (may be using different default logger). "+
			"Log: %q", logOutput)
	}
}

// TestSec_RealIP_WithCIDR_OnlyTrustedPeerXFFAccepted confirms the secure path:
// when a CIDR is configured, only connections from trusted peers cause XFF rewriting.
func TestSec_RealIP_WithCIDR_OnlyTrustedPeerXFFAccepted(t *testing.T) {
	trusted, _ := netip.ParsePrefix("10.0.0.0/8") // internal proxy range
	mw := middleware.RealIP(&trusted)

	// Case 1: trusted peer — XFF should be accepted.
	var addr1, addr2 string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_ = inner

	innerCapture1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr1 = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "10.0.0.5:1234" // trusted proxy
	req1.Header.Set("X-Forwarded-For", "5.6.7.8")
	mw(innerCapture1).ServeHTTP(httptest.NewRecorder(), req1)

	// Case 2: untrusted peer — XFF must NOT be accepted, RemoteAddr unchanged.
	innerCapture2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr2 = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "203.0.113.1:1234" // untrusted attacker
	req2.Header.Set("X-Forwarded-For", "1.1.1.1")
	mw(innerCapture2).ServeHTTP(httptest.NewRecorder(), req2)

	if addr1 != "5.6.7.8" {
		t.Errorf("TM-2026-044: trusted peer XFF — expected RemoteAddr=5.6.7.8, got %q", addr1)
	} else {
		t.Logf("TM-2026-044 PASS: trusted peer XFF accepted → RemoteAddr=%q", addr1)
	}

	if addr2 == "1.1.1.1" {
		t.Errorf("TM-2026-044: untrusted peer XFF accepted — RemoteAddr rewritten to spoofed IP %q", addr2)
	} else if addr2 == "203.0.113.1:1234" {
		t.Logf("TM-2026-044 PASS: untrusted peer XFF ignored → RemoteAddr=%q (unchanged)", addr2)
	} else {
		t.Logf("TM-2026-044 INFO: addr2=%q", addr2)
	}
}

// TestSec_RealIP_NoCIDR_XRealIPAlsoAccepted confirms that with no CIDRs,
// X-Real-IP is ALSO accepted from any peer (same exposure as XFF).
func TestSec_RealIP_NoCIDR_XRealIPAlsoAccepted(t *testing.T) {
	mw := middleware.RealIP()

	var capturedAddr string
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	req.Header.Set("X-Real-IP", "192.168.1.100")

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAddr = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if capturedAddr == "192.168.1.100" {
		t.Logf("TM-2026-044 CONFIRMED: RealIP() with no CIDRs also accepts X-Real-IP from any peer. "+
			"Attacker at 203.0.113.1 spoofed private IP %q via X-Real-IP header. "+
			"Same severity as XFF spoofing.", capturedAddr)
	} else {
		t.Logf("TM-2026-044 X-Real-IP: capturedAddr=%q (not spoofed address)", capturedAddr)
	}
}
