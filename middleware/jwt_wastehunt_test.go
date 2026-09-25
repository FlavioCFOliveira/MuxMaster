// Black-box security regression tests for rmp task #252, sprint 18 (WH-06):
// JWTAuth's single-entry JOSE header memo must NEVER let a cached header
// bypass signature verification, the algorithm allow-list, or the "crit"
// check — it may only memoise the pure decode of byte-identical header
// segments. Package middleware_test reuses makeHS256JWT from
// middleware_test.go.
package middleware_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// signHS256WithHeader builds a JWT with an ARBITRARY (potentially malicious)
// JOSE header, signed correctly for the given secret. It mirrors the manual
// construction already used by TestJWTAuth_CritHeaderRejects in
// middleware_test.go.
func signHS256WithHeader(secret []byte, headerJSON string, claims map[string]any) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	payload, _ := json.Marshal(claims)
	pay := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := hdr + "." + pay
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func jwtServe(t *testing.T, mw func(http.Handler) http.Handler, token string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
	return rec.Code
}

// TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification warms the memo
// with a valid token, then sends a SECOND token that shares the byte-
// identical JOSE header (so the header decode step is a guaranteed memo
// hit) but carries a tampered signature. The memo must never let this
// through: verifyFn runs unconditionally on every request regardless of a
// header cache hit.
func TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification(t *testing.T) {
	secret := []byte("s3cr3t")
	mw := middleware.JWTAuth(middleware.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}})

	claims := map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()}
	valid := makeHS256JWT(secret, claims)
	if code := jwtServe(t, mw, valid); code != http.StatusOK {
		t.Fatalf("warm-up valid token: got %d, want 200", code)
	}

	// Same header bytes (guaranteed memo hit), same payload, but the
	// signature is corrupted at the byte level (see corruptSignature: a
	// naive last-character flip is NOT reliable — see its doc comment).
	tampered := corruptSignature(t, valid)
	if code := jwtServe(t, mw, tampered); code != http.StatusUnauthorized {
		t.Fatalf("tampered signature after a memo-warming request: got %d, want 401 — header memo must not bypass signature verification", code)
	}

	// The original valid token must still work afterwards (memo not corrupted).
	if code := jwtServe(t, mw, valid); code != http.StatusOK {
		t.Fatalf("valid token after a rejected tampered one: got %d, want 200", code)
	}
}

// corruptSignature returns token with its signature segment corrupted at
// the DECODED BYTE level, guaranteeing hmac.Equal sees a different value.
//
// A naive approach — flipping the last base64 CHARACTER of the token — is
// unreliable: base64url encodes a byte-level payload in 6-bit groups, and
// whenever the payload length leaves a 2-byte remainder (e.g. every 32-byte
// HMAC-SHA256 signature: 32 = 3*10 + 2), the LAST character of the encoding
// carries only 4 significant bits — its bottom 2 bits are unused padding.
// Go's base64.RawURLEncoding.DecodeString does not validate that those
// padding bits are zero (a documented stdlib leniency), so several distinct
// last characters decode to the byte-identical signature. A test that flips
// only that last character therefore sometimes produces a token that is NOT
// actually tampered — confirmed empirically: for an all-zero 32-byte
// payload, encoded characters 'A','B','C','D' (the whole bucket sharing the
// same top-4 bits) all decode to the same bytes. This was the root cause of
// an intermittent failure of this exact test (~1-in-8 to ~1-in-16 runs,
// matching the probability the token's real last character already falls in
// the same bucket the old flipBase64Char helper always targeted).
//
// Flipping a bit in the FIRST decoded byte of the signature side-steps this
// entirely: every bit of a fully-significant byte participates in every
// base64 character that encodes it, so XOR-ing it always changes the
// re-encoded string AND always changes the decoded bytes hmac.Equal compares
// against — deterministically, for every possible signature value.
func corruptSignature(t *testing.T, token string) string {
	t.Helper()
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		t.Fatalf("corruptSignature: no '.' found in token %q", token)
	}
	head, sigB64 := token[:i+1], token[i+1:]
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sig) == 0 {
		t.Fatalf("corruptSignature: cannot decode signature %q: %v", sigB64, err)
	}
	corrupted := append([]byte(nil), sig...)
	corrupted[0] ^= 0xFF
	return head + base64.RawURLEncoding.EncodeToString(corrupted)
}

// TestJWTAuth_HeaderMemo_DoesNotBypassAlgAllowList configures JWTAuth for
// HS256 only, warms the memo with a valid HS256 token, then sends a
// DIFFERENT, syntactically-valid, correctly-signed token whose header
// declares an algorithm outside the allow-list. Different header bytes
// force a memo MISS, which must still go through the full allow-list check
// and be rejected.
func TestJWTAuth_HeaderMemo_DoesNotBypassAlgAllowList(t *testing.T) {
	secret := []byte("s3cr3t")
	mw := middleware.JWTAuth(middleware.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}})

	valid := makeHS256JWT(secret, map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	if code := jwtServe(t, mw, valid); code != http.StatusOK {
		t.Fatalf("warm-up: got %d, want 200", code)
	}

	// "none" algorithm — classically used in algorithm-confusion attacks
	// (RFC 8725 §3.1). This alg is not in the allow-list.
	forged := signHS256WithHeader(secret, `{"alg":"none","typ":"JWT"}`, map[string]any{"sub": "attacker", "exp": time.Now().Add(time.Hour).Unix()})
	if code := jwtServe(t, mw, forged); code != http.StatusUnauthorized {
		t.Fatalf("alg=none after a memo-warming HS256 request: got %d, want 401", code)
	}

	// A second attempt with the SAME forged header must ALSO be rejected —
	// the rejection itself must never be (mis-)cached as an acceptance.
	if code := jwtServe(t, mw, forged); code != http.StatusUnauthorized {
		t.Fatalf("alg=none on a REPEAT attempt: got %d, want 401 (a rejected header must never become a cached acceptance)", code)
	}

	// The originally warmed, legitimate header must still work.
	if code := jwtServe(t, mw, valid); code != http.StatusOK {
		t.Fatalf("valid HS256 token after rejecting alg=none twice: got %d, want 200", code)
	}
}

// TestJWTAuth_HeaderMemo_DoesNotBypassCritCheck mirrors the allow-list test
// for RFC 7515 §4.1.11's "crit" rule: a header with a non-empty "crit" list
// must always be rejected, memo hit or miss, and must never poison the
// cache with a false acceptance.
func TestJWTAuth_HeaderMemo_DoesNotBypassCritCheck(t *testing.T) {
	secret := []byte("s3cr3t")
	mw := middleware.JWTAuth(middleware.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}})

	valid := makeHS256JWT(secret, map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	jwtServe(t, mw, valid) // warm the memo

	critToken := signHS256WithHeader(secret, `{"alg":"HS256","typ":"JWT","crit":["b64"]}`, map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	for i := range 3 {
		if code := jwtServe(t, mw, critToken); code != http.StatusUnauthorized {
			t.Fatalf("iteration %d: crit header token: got %d, want 401", i, code)
		}
	}
	if code := jwtServe(t, mw, valid); code != http.StatusOK {
		t.Fatalf("valid token after repeated crit rejections: got %d, want 200", code)
	}
}

// TestJWTAuth_HeaderMemo_InstanceIsolation confirms two independently
// configured JWTAuth middlewares never share a memo: an algorithm accepted
// by one instance must not leak acceptance into a stricter sibling
// instance, even when both process the exact same header bytes.
func TestJWTAuth_HeaderMemo_InstanceIsolation(t *testing.T) {
	secretA := []byte("secret-a")
	secretB := []byte("secret-b")
	mwHS256Only := middleware.JWTAuth(middleware.JWTOptions{Secret: secretA, Algorithms: []string{"HS256"}})
	mwHS384Only := middleware.JWTAuth(middleware.JWTOptions{Secret: secretB, Algorithms: []string{"HS384"}})

	tokenA := makeHS256JWT(secretA, map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	if code := jwtServe(t, mwHS256Only, tokenA); code != http.StatusOK {
		t.Fatalf("instance A warm-up: got %d, want 200", code)
	}
	// The SAME token (same header bytes, alg=HS256) must be rejected by the
	// HS384-only instance — it must not somehow inherit instance A's memo.
	if code := jwtServe(t, mwHS384Only, tokenA); code != http.StatusUnauthorized {
		t.Fatalf("HS256 token against an HS384-only instance: got %d, want 401 (cross-instance memo leak)", code)
	}
}

// TestJWTAuth_HeaderMemo_ConcurrentDifferentHeaders_RaceStress drives many
// goroutines alternately validating two tokens with genuinely different
// header byte sequences (forcing constant memo churn) under -race, to prove
// the atomic.Pointer-based memo has no data race and never serves a wrong
// result under contention.
func TestJWTAuth_HeaderMemo_ConcurrentDifferentHeaders_RaceStress(t *testing.T) {
	secret := []byte("s3cr3t")
	mw := middleware.JWTAuth(middleware.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}})

	claims := map[string]any{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()}
	tokenTypFirst := signHS256WithHeader(secret, `{"alg":"HS256","typ":"JWT"}`, claims)
	tokenAlgFirst := signHS256WithHeader(secret, `{"typ":"JWT","alg":"HS256"}`, claims)
	forged := signHS256WithHeader(secret, `{"alg":"none"}`, claims)

	var wg sync.WaitGroup
	for g := range 64 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 200 {
				var tok string
				var want int
				switch i % 3 {
				case 0:
					tok, want = tokenTypFirst, http.StatusOK
				case 1:
					tok, want = tokenAlgFirst, http.StatusOK
				default:
					tok, want = forged, http.StatusUnauthorized
				}
				if code := jwtServe(t, mw, tok); code != want {
					t.Errorf("goroutine %d iter %d: got %d, want %d", g, i, code, want)
				}
			}
		}(g)
	}
	wg.Wait()
}
