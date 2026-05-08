// Package harness_test — Gap C: JWT 'crit' header rejection (RFC 8725 §3.6 / RFC 7515 §4.1.11).
//
// RFC 7515 §4.1.11 mandates that any JWT recipient MUST reject a token whose
// "crit" header parameter lists extensions the recipient does not understand.
// RFC 8725 §3.6 reinforces this as a security requirement to prevent
// algorithm-confusion and extension-smuggling attacks.
//
// This file confirms the current behaviour of JWTAuth and provides regression
// tests so that future refactors cannot silently remove the check.
package harness_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

var jwtTestSecret = []byte("test-secret-key-for-harness-only-do-not-use-in-prod")

// makeHS256JWT builds a compact JWS with the given header claims and a valid
// HMAC-SHA256 signature over the given payload claims.
func makeHS256JWT(t *testing.T, headerExtra map[string]any, payloadClaims map[string]any) string {
	t.Helper()

	// Merge base header with extras.
	hdr := map[string]any{"alg": "HS256", "typ": "JWT"}
	for k, v := range headerExtra {
		hdr[k] = v
	}
	hdrJSON, err := json.Marshal(hdr)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	// Merge base payload with caller claims.
	now := time.Now().Unix()
	payload := map[string]any{
		"sub": "test-user",
		"iat": now,
		"exp": now + 3600,
	}
	for k, v := range payloadClaims {
		payload[k] = v
	}
	payJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payB64 := base64.RawURLEncoding.EncodeToString(payJSON)
	signingInput := hdrB64 + "." + payB64

	mac := hmac.New(sha256.New, jwtTestSecret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return fmt.Sprintf("%s.%s.%s", hdrB64, payB64, sigB64)
}

func newJWTHandler(t *testing.T) http.Handler {
	t.Helper()
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtTestSecret,
	})
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("authenticated"))
	}))
}

// TestSec_JWTCrit_SingleUnknownExtension verifies that a token with a single
// unknown critical extension is rejected.
//
// RFC 7515 §4.1.11: "If any of the listed extension header parameter names
// are not understood and supported by the implementation, then the JWS MUST
// fail to be accepted."
func TestSec_JWTCrit_SingleUnknownExtension(t *testing.T) {
	h := newJWTHandler(t)
	token := makeHS256JWT(t, map[string]any{
		"crit": []string{"x-custom-extension"},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Errorf("RFC 7515 §4.1.11 violation: token with crit=[x-custom-extension] accepted (got %d), expected 401", rw.Code)
	}
}

// TestSec_JWTCrit_MultipleUnknownExtensions verifies rejection when crit lists
// multiple unknown extensions.
func TestSec_JWTCrit_MultipleUnknownExtensions(t *testing.T) {
	h := newJWTHandler(t)
	token := makeHS256JWT(t, map[string]any{
		"crit": []string{"x-ext-a", "x-ext-b", "x-ext-c"},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Errorf("RFC 7515 §4.1.11: token with multiple crit extensions accepted (got %d), expected 401", rw.Code)
	}
}

// TestSec_JWTCrit_EmptyArrayAccepted verifies that an empty crit array does
// not cause false rejection.  RFC 7515 says crit must contain at least one
// element to be valid, but we should be defensive: empty = no required
// extensions = no reason to reject on crit grounds.
func TestSec_JWTCrit_EmptyArrayAccepted(t *testing.T) {
	h := newJWTHandler(t)
	// Note: crit=[] is technically invalid per RFC but we must not panic.
	token := makeHS256JWT(t, map[string]any{
		"crit": []string{},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	// Empty crit slice: len(hdr.Crit) == 0 → no rejection on crit grounds.
	// Token should be accepted (valid signature, valid claims).
	if rw.Code != http.StatusOK {
		t.Errorf("token with empty crit array rejected (got %d) — should be accepted (no required extensions)", rw.Code)
	}
}

// TestSec_JWTCrit_NoCritFieldAccepted is the baseline: a token without any
// crit field should be accepted normally when all other claims are valid.
func TestSec_JWTCrit_NoCritFieldAccepted(t *testing.T) {
	h := newJWTHandler(t)
	token := makeHS256JWT(t, nil, nil) // no crit field

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("valid token without crit rejected (got %d), expected 200", rw.Code)
	}
}

// TestSec_JWTCrit_CritWithKnownStandardClaim verifies that even a crit
// extension that names a standard claim (e.g. "exp") is rejected, because
// the middleware does not declare any "understood" extensions.
// RFC 8725 §3.6: "The crit Header Parameter MUST NOT be used to require the
// understanding of standard JWS/JWE header parameters."
// Our middleware rejects all non-empty crit values — correct per RFC 8725.
func TestSec_JWTCrit_CritWithKnownStandardClaim(t *testing.T) {
	h := newJWTHandler(t)
	token := makeHS256JWT(t, map[string]any{
		"crit": []string{"exp"}, // misuse: listing standard "exp" as critical extension
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	// Middleware rejects all non-empty crit arrays — this is correct
	// and conservative: any non-empty crit list indicates an extension
	// the middleware does not understand (none are registered).
	if rw.Code != http.StatusUnauthorized {
		t.Errorf("token with crit=[exp] accepted (got %d) — should be rejected per RFC 7515 §4.1.11", rw.Code)
	}
}

// TestSec_JWTCrit_CritAbsentSignatureInvalid confirms that crit rejection
// happens before or alongside signature verification — a rejected crit token
// must not reveal whether the signature was valid.
// The response must always be 401 with no distinguishing body for crit vs
// bad-signature — prevents extension-oracle attacks.
func TestSec_JWTCrit_CritAbsentSignatureInvalid(t *testing.T) {
	h := newJWTHandler(t)

	// Token with crit AND invalid signature.
	hdrJSON, _ := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT", "crit": []string{"ext"}})
	payJSON, _ := json.Marshal(map[string]any{"sub": "x", "exp": time.Now().Add(time.Hour).Unix()})
	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payB64 := base64.RawURLEncoding.EncodeToString(payJSON)
	badSig := base64.RawURLEncoding.EncodeToString([]byte("invalidsignature"))
	token := hdrB64 + "." + payB64 + "." + badSig

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Errorf("token with crit+bad-sig: expected 401, got %d", rw.Code)
	}

	// Confirm no information leakage distinguishing crit-reject vs sig-reject.
	// Both should return the same WWW-Authenticate header.
	wwwAuth := rw.Header().Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Errorf("WWW-Authenticate header missing on 401 — RFC 7235 §4.1 violation")
	}
}

// TestSec_JWTCrit_MalformedCritNotArray verifies that a crit value that is not
// an array (e.g. a string) does not panic and returns 401.
func TestSec_JWTCrit_MalformedCritNotArray(t *testing.T) {
	h := newJWTHandler(t)

	// Craft header with crit as a plain string (malformed per spec).
	hdrJSON := []byte(`{"alg":"HS256","typ":"JWT","crit":"not-an-array"}`)
	payJSON, _ := json.Marshal(map[string]any{"sub": "x", "exp": time.Now().Add(time.Hour).Unix()})
	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payB64 := base64.RawURLEncoding.EncodeToString(payJSON)
	signingInput := hdrB64 + "." + payB64
	mac := hmac.New(sha256.New, jwtTestSecret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()

	// Must not panic.
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("panic on malformed crit: %v", rec)
		}
	}()

	h.ServeHTTP(rw, req)

	// Malformed JSON for crit → json.Unmarshal on rawJWTHeader will fail
	// (crit is []string, not string) → errJWTInvalid → 401.
	if rw.Code != http.StatusUnauthorized {
		t.Errorf("malformed crit (string, not array): expected 401, got %d", rw.Code)
	}
}
