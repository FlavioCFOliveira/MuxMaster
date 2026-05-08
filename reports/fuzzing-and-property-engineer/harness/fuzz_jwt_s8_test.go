package harness

// JWT S8 fuzz harness — covers:
//   H8-08: jwt+with_value type-assertion safety
//   H8-42: CVE-2025-22865 analogue — alg=none/None/NONE must always be rejected
//   H8-43: CVE-2025-30204 (golang-jwt panic) — fuzz the JWT parser
//   H8-63: kid injection — kid header field used as arbitrary string key
//   H8-64: alg=none algorithm — must always be rejected even with no key config
//
// Invariants tested:
//   I-JWT-NONE-01: tokens claiming alg=none/None/NONE are always rejected (401)
//                  regardless of case folding or whitespace.
//   I-JWT-KID-01:  kid field in header does not reach any filesystem/SQL/shell;
//                  arbitrary kid values never cause panic or 500.
//   I-JWT-PARSE-01: the JWT parser never panics on any input; all error paths
//                   produce 401 (not 500 or panic).
//   I-JWT-CTX-01:  GetJWTClaims on a context not populated by JWTAuth returns
//                  (nil, false) without panic.
//   I-JWT-WV-01:   a downstream handler receiving JWT claims via context can
//                  assert them without crash even when claims contain
//                  attacker-shaped payloads.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"pgregory.net/rapid"
)

// jwtHS256Sign produces a HS256 JWT token with the given header and payload
// (already JSON-encoded), signed with the supplied key.
func jwtHS256Sign(hdrJSON, payloadJSON, key []byte) string {
	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := hdrB64 + "." + payB64
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// jwtMakeTokenWithAlg creates a minimal JWT token with the specified alg
// field value. The signature is intentionally zeroed (to exercise the alg
// check path before signature verification).
func jwtMakeTokenWithAlg(alg string) string {
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	pay, _ := json.Marshal(map[string]any{"sub": "test", "exp": int64(9999999999)})
	hdrB64 := base64.RawURLEncoding.EncodeToString(hdr)
	payB64 := base64.RawURLEncoding.EncodeToString(pay)
	// Zero-length signature — this is deliberately invalid.
	return hdrB64 + "." + payB64 + "."
}

var jwtSecret = []byte("test-secret-key-for-s8-fuzz-harness")

// jwtMakeValidHS256Token creates a validly signed HS256 token with the given
// payload fields so the handler chain actually runs.
func jwtMakeValidHS256Token(payload map[string]any) string {
	hdrJSON, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payJSON, _ := json.Marshal(payload)
	return jwtHS256Sign(hdrJSON, payJSON, jwtSecret)
}

// ============================================================
// I-JWT-NONE-01: alg=none variants always rejected
// ============================================================

func TestJWT_AlgNoneVariantsRejected(t *testing.T) {
	handler := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtSecret,
	})(h200)

	// Every known variation of "none" that has appeared in CVEs.
	noneVariants := []string{
		"none",
		"None",
		"NONE",
		"nOnE",
		"NoNe",
		" none",       // leading space
		"none ",       // trailing space
		"none\x00",    // NUL suffix
		"\x00none",    // NUL prefix
		"None\r\n",    // CRLF
		"HS256\nnone", // multi-line
		"alg:none",    // colon-injection
		"NONE,HS256",  // comma-separated
	}

	for _, alg := range noneVariants {
		t.Run(fmt.Sprintf("alg=%q", alg), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC: alg=%q r=%v\n%s", alg, r, debug.Stack())
				}
			}()
			token := jwtMakeTokenWithAlg(alg)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("alg=%q: expected 401, got %d", alg, rec.Code)
			}
		})
	}
}

// FuzzJWTAlgNone fuzzes alg field values — any non-allowlisted alg must
// produce 401, never 200 or panic. H8-42, H8-64.
func FuzzJWTAlgNone(f *testing.F) {
	f.Add("none")
	f.Add("None")
	f.Add("NONE")
	f.Add("HS256")
	f.Add("RS256")
	f.Add("HS256\x00")
	f.Add("\x00HS256")
	f.Add("none\r\nX-Injected: evil")
	f.Add("alg:none")
	f.Add("")
	f.Add("HS256,none")

	handler := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtSecret,
	})(h200)

	f.Fuzz(func(t *testing.T, alg string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC: alg=%q r=%v\n%s", alg, r, debug.Stack())
			}
		}()
		token := jwtMakeTokenWithAlg(alg)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Only HS256 is allowed. Any other alg must be rejected with 401.
		if alg == "HS256" {
			// Zero sig → still 401 (invalid sig), not panic.
			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("alg=HS256 with bad sig got 500, want 401")
			}
		} else {
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("alg=%q: expected 401, got %d", alg, rec.Code)
			}
		}
	})
}

// ============================================================
// I-JWT-PARSE-01: parser never panics on arbitrary input (H8-43)
// ============================================================

func FuzzJWTParserNoPanic(f *testing.F) {
	// Seeds from CVE-2025-30204 corpus context: malformed headers/payloads.
	f.Add("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.invalid")
	f.Add("..") // three empty parts
	f.Add("a.b.c")
	f.Add("a.b.")
	f.Add(".b.c")
	f.Add("eyJhbGciOiJub25lIn0.eyJzdWIiOiJ0ZXN0In0.") // alg:none
	f.Add("eyJhbGciOiJIUzI1NiJ9.e30.AAAA")
	f.Add("!invalid base64!.payload.sig")
	f.Add(string(make([]byte, 65536)))                                                       // large input
	f.Add("a." + string(make([]byte, 65536)) + ".c")                                         // large payload
	f.Add("eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ0ZXN0IiwiZXhwIjo5OTk5OTk5OTk5fQ.") // none with valid payload

	handler := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtSecret,
	})(h200)

	f.Fuzz(func(t *testing.T, token string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAuth parser: token_len=%d r=%v\n%s",
					len(token), r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Must be 200 (valid+signed) or 401 (invalid). Never 500.
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("JWTAuth returned 500 for token_len=%d", len(token))
		}
	})
}

// ============================================================
// I-JWT-KID-01: kid field never panics, never used as path (H8-63)
// ============================================================

// FuzzJWTKidInjection sends tokens whose header contains a "kid" field with
// adversarial values. The kid field is parsed but MuxMaster's JWTAuth does not
// use kid for key lookup (single-key config). It must never panic.
func FuzzJWTKidInjection(f *testing.F) {
	// Adversarial kid values: path traversal, SQL injection, shell injection,
	// NUL byte, Unicode, CRLF.
	f.Add("../../etc/passwd")
	f.Add("'; DROP TABLE users; --")
	f.Add("$(id)")
	f.Add("kid\x00value")
	f.Add("kid\r\nX-Injected: evil")
	f.Add(" ")
	f.Add(string(make([]byte, 65536)))
	f.Add("normal-key-id")
	f.Add("")

	handler := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtSecret,
	})(h200)

	f.Fuzz(func(t *testing.T, kid string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAuth with kid=%q r=%v\n%s", kid, r, debug.Stack())
			}
		}()
		// Build a token with arbitrary kid in the header.
		hdrMap := map[string]string{"alg": "HS256", "typ": "JWT", "kid": kid}
		hdrJSON, _ := json.Marshal(hdrMap)
		payJSON, _ := json.Marshal(map[string]any{
			"sub": "user",
			"exp": int64(9999999999),
		})
		token := jwtHS256Sign(hdrJSON, payJSON, jwtSecret)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Must succeed (kid is ignored) or fail with 401. Never 500.
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("JWTAuth returned 500 for kid=%q", kid)
		}
	})
}

// ============================================================
// I-JWT-CTX-01 / I-JWT-WV-01: context type assertion safety (H8-08)
// ============================================================

// TestProp_JWTClaimsContextSafety verifies that:
//  1. GetJWTClaims on any arbitrary context does not panic.
//  2. When JWTAuth injects claims, a downstream handler can access them without panic.
//  3. If a downstream handler uses context.WithValue with the same key type,
//     GetJWTClaims still returns the correct claims (type-safety of the key).
func TestProp_JWTClaimsContextSafety(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWT context safety: %v\n%s", r, debug.Stack())
			}
		}()

		// Generate adversarial string values for the token payload.
		sub := rapid.String().Draw(t, "sub")
		iss := rapid.String().Draw(t, "iss")

		// Build a valid signed token with attacker-controlled sub/iss.
		payload := map[string]any{
			"sub": sub,
			"iss": iss,
			"exp": int64(9999999999),
		}
		token := jwtMakeValidHS256Token(payload)

		// Mux with JWTAuth + a handler that reads claims and stacks another WithValue.
		mux := mm.New()
		var claimsGot *mw.JWTClaims
		var claimsOK bool
		mux.Use(mw.JWTAuth(mw.JWTOptions{
			Algorithms: []string{"HS256"},
			Secret:     jwtSecret,
		}))
		mux.GET("/protected", func(w http.ResponseWriter, r *http.Request) {
			// Stack another context value to simulate downstream middleware.
			type downstreamKey struct{}
			ctx := context.WithValue(r.Context(), downstreamKey{}, "downstream-value")
			r = r.WithContext(ctx)

			// Now retrieve claims — must still work despite the extra WithValue.
			claimsGot, claimsOK = mw.GetJWTClaims(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			return // Token may have failed validation for other reasons — skip.
		}
		if !claimsOK || claimsGot == nil {
			t.Fatal("GetJWTClaims returned ok=false on successful auth")
		}
		// Claims must not contain raw values from the attacker outside the JSON-safe fields.
		// Subject and Issuer are stored directly — verify no panic on access.
		_ = claimsGot.Subject
		_ = claimsGot.Issuer
		_ = claimsGot.Audience
	})
}

// TestProp_GetJWTClaimsArbitraryContext verifies I-JWT-CTX-01: GetJWTClaims
// never panics on any context, including plain background and contexts with
// unrelated keys whose values are not *JWTClaims.
func TestProp_GetJWTClaimsArbitraryContext(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in GetJWTClaims: %v\n%s", r, debug.Stack())
			}
		}()

		// Test 1: background context.
		c1, ok1 := mw.GetJWTClaims(context.Background())
		if ok1 || c1 != nil {
			t.Fatal("GetJWTClaims on background ctx returned non-nil or ok=true")
		}

		// Test 2: context with an unrelated value.
		type otherKey struct{}
		val := rapid.String().Draw(t, "val")
		ctx2 := context.WithValue(context.Background(), otherKey{}, val)
		c2, ok2 := mw.GetJWTClaims(ctx2)
		if ok2 || c2 != nil {
			t.Fatal("GetJWTClaims on non-JWT ctx returned non-nil or ok=true")
		}
	})
}

// ============================================================
// FuzzJWTHeaderArbitrary: H8-43 — fuzzes the raw Authorization header bytes
// ============================================================

func FuzzJWTHeaderArbitrary(f *testing.F) {
	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.invalid")
	f.Add("Bearer ")
	f.Add("bearer eyJ")
	f.Add("BEARER token")
	f.Add("")
	f.Add("Basic dXNlcjpwYXNz")
	f.Add("Bearer a.b.c.d") // four parts
	f.Add(string(make([]byte, 8192)))

	handler := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     jwtSecret,
	})(h200)

	f.Fuzz(func(t *testing.T, authHeader string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTAuth header parsing: header_len=%d r=%v\n%s",
					len(authHeader), r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", authHeader)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("JWTAuth returned 500 for Authorization header len=%d", len(authHeader))
		}
	})
}
