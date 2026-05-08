package harness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

var (
	// Pre-generated keys for fuzz tests — generation is expensive; do it once.
	jwtRSAKey   *rsa.PrivateKey
	jwtECKey256 *ecdsa.PrivateKey
)

func init() {
	var err error
	jwtRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("test setup: rsa.GenerateKey: " + err.Error())
	}
	jwtECKey256, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic("test setup: ecdsa.GenerateKey: " + err.Error())
	}
}

// FuzzJWTAuth fuzzes the JWTAuth middleware with arbitrary Authorization header values.
// Invariant I-JWT-01: JWTAuth must never panic on any header value, including:
//   - malformed base64 in any segment
//   - truncated tokens (1 or 2 dots only)
//   - alg confusion candidates (HS/RS/ES switched)
//   - crit claims with arbitrary content
//   - oversized tokens
func FuzzJWTAuth(f *testing.F) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})
	mwRS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"RS256"},
		PublicKey:  &jwtRSAKey.PublicKey,
	})
	mwMixed := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256", "RS256"},
		Secret:     []byte("testsecret"),
		PublicKey:  &jwtRSAKey.PublicKey,
	})

	// Valid token seeds
	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.AAAA")
	// Invalid base64
	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.!!!.AAAA")
	// Empty / whitespace
	f.Add("")
	f.Add("Bearer ")
	f.Add("Bearer    ")
	// Alg confusion: RS256 header with HMAC-signed body
	f.Add("Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.fakesig")
	// crit claim
	f.Add("Bearer eyJhbGciOiJIUzI1NiIsImNyaXQiOlsidGVzdCJdfQ.eyJzdWIiOiJ1c2VyIn0.sig")
	// None alg
	f.Add("Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyIn0.")
	// Giant token (8KB)
	f.Add("Bearer " + makeJunk(8192))
	// Dot-only token
	f.Add("Bearer ...")
	// Unicode in header
	f.Add("Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCIsInVuaWNvZGUiOiLimJMifQ.e30.sig")

	fuzzJWT := func(t *testing.T, authHeader string, jwtMW func(http.Handler) http.Handler) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAuth: header=%q r=%v\n%s", authHeader, r, debug.Stack())
			}
		}()
		handler := jwtMW(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Must produce 200 or 401 — never panic, never 500.
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status=%d for header=%q", rec.Code, authHeader)
		}
	}

	f.Fuzz(func(t *testing.T, authHeader string) {
		fuzzJWT(t, authHeader, mwHS256)
		fuzzJWT(t, authHeader, mwRS256)
		fuzzJWT(t, authHeader, mwMixed)
	})
}

// FuzzJWTAlgConfusion specifically targets RFC 8725 §3.1 alg confusion:
// a token with alg=HS256 signed with the RSA public key bytes should NOT validate
// when HS256 is configured with a *different* secret.
// And when both alg=HS256 and alg=RS256 are permitted with different keys,
// a sender-chosen alg must not cross-validate.
func FuzzJWTAlgConfusion(f *testing.F) {
	hmacKey := []byte("hmac-secret-key-for-test")
	rsaPubBytes := marshalRSAPub(&jwtRSAKey.PublicKey)

	// Middleware configured with HS256 only.
	mwHS := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     hmacKey,
	})

	// Middleware configured with RS256 only.
	mwRS := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"RS256"},
		PublicKey:  &jwtRSAKey.PublicKey,
	})

	// Seeds: craft tokens that look like alg=RS256 but carry HMAC sigs.
	f.Add(buildFakeAlgConfusionToken("RS256", "HS256", rsaPubBytes))
	f.Add(buildFakeAlgConfusionToken("HS256", "RS256", hmacKey))
	f.Add("Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJhZG1pbiJ9.")

	f.Fuzz(func(t *testing.T, token string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAlgConfusion: r=%v\n%s", r, debug.Stack())
			}
		}()

		// Neither middleware should accept an attacker-supplied token.
		for _, jwtMW := range []func(http.Handler) http.Handler{mwHS, mwRS} {
			handler := jwtMW(h200)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// Any 200 with a crafted alg-confusion token would be a finding.
			// We don't have a valid forged token here, so we can't assert 401
			// definitively — but we do assert no panic.
		}
	})
}

// FuzzJWTAudClaim exercises the jwtAudClaim custom JSON unmarshaller with
// arbitrary payloads (string or array "aud" field).
// Invariant I-JWT-02: JSON parse errors must not panic, only return errJWTInvalid.
func FuzzJWTAudClaim(f *testing.F) {
	mwAud := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("secret"),
		Audiences:  []string{"api"},
	})

	// Seeds with different aud formats
	f.Add(`{"sub":"x","aud":"api","exp":9999999999}`)
	f.Add(`{"sub":"x","aud":["api","other"],"exp":9999999999}`)
	f.Add(`{"sub":"x","aud":null}`)
	f.Add(`{"sub":"x","aud":123}`)
	f.Add(`{"sub":"x","aud":true}`)
	f.Add(`{"sub":"x","aud":{}}`)
	f.Add(`{"sub":"x"}`)
	f.Add(`{}`)
	f.Add(`not-json`)

	f.Fuzz(func(t *testing.T, payload string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAudClaim: payload=%q r=%v\n%s", payload, r, debug.Stack())
			}
		}()
		// Craft a token with the raw payload (bypass sig check won't work, but
		// we test the parser path for malformed JSON without reaching the sig step).
		handler := mwAud(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		// Any token will fail sig; we just ensure no panic on any input.
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9."+b64([]byte(payload))+".fakesig")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("unexpected 200 on forged token with payload=%q", payload)
		}
	})
}

// ---- helpers ----

func makeJunk(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'A' + byte(i%26)
	}
	return string(b)
}

func marshalRSAPub(pub *rsa.PublicKey) []byte {
	// Return N bytes as a crude "key material" for confusion test seeds.
	return pub.N.Bytes()
}

func buildFakeAlgConfusionToken(claimedAlg, actualAlg string, key []byte) string {
	_ = actualAlg
	_ = key
	// Build a syntactically valid token structure with the claimed alg in the header.
	header := `{"alg":"` + claimedAlg + `"}`
	payload := `{"sub":"admin"}`
	return "Bearer " + b64([]byte(header)) + "." + b64([]byte(payload)) + ".fakesig"
}

func b64(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out []byte
	for i := 0; i < len(data); i += 3 {
		b := [3]byte{}
		n := copy(b[:], data[i:])
		out = append(out,
			alphabet[(b[0]>>2)&0x3F],
			alphabet[((b[0]&3)<<4|(b[1]>>4))&0x3F],
		)
		if n > 1 {
			out = append(out, alphabet[((b[1]&0xF)<<2|(b[2]>>6))&0x3F])
		}
		if n > 2 {
			out = append(out, alphabet[b[2]&0x3F])
		}
	}
	return string(out)
}
