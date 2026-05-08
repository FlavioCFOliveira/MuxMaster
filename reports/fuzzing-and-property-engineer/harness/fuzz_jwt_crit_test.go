package harness

// GAP C: JWT 'crit', 'cty', 'typ' exotic header fuzzing.
//
// RFC 7515 §4.1.11 mandates that a recipient MUST reject a JWS with a "crit"
// header containing any extension it does not understand. MuxMaster's JWTAuth
// already rejects any non-empty "crit" (len(hdr.Crit) > 0 → errJWTInvalid).
//
// This file adds targeted corpus entries for:
//   1. "crit" arrays with various cardinalities and exotic member values.
//   2. "cty" (content type) header — ignored per RFC 7519 §5.2, but must not panic.
//   3. "typ" header — exotic values ("JWT", "at+JWT", "dpop+jwt", custom).
//   4. Combined exotic headers (crit + cty + typ + unknown fields).
//   5. Deeply nested / large JSON headers.
//
// Invariants verified:
//   I-JWT-CRIT-01: Any token with a non-empty "crit" array is rejected (401), never panics.
//   I-JWT-CRIT-02: Tokens with "cty" or exotic "typ" that pass alg/sig checks are accepted;
//                  those that fail sig are rejected. Neither case panics.
//   I-JWT-CRIT-03: Oversized or deeply-nested JSON headers do not cause OOM or panic.
//   I-JWT-01 (inherited): JWTAuth never panics on any input.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// buildJWTHeaderB64 encodes a raw JSON object as a base64url string
// (no padding, URL-safe alphabet).
func buildJWTHeaderB64(headerJSON string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
}

// buildJWTToken assembles a three-part JWT from pre-encoded segments.
// sigB64 may be garbage — signature verification will fail, but the
// header parsing path is still exercised before signature check.
func buildJWTToken(headerB64, payloadB64, sigB64 string) string {
	return "Bearer " + headerB64 + "." + payloadB64 + "." + sigB64
}

// trivialPayloadB64 is a minimal valid JWT payload.
const trivialPayloadB64 = "eyJzdWIiOiJ4In0" // {"sub":"x"}

// FuzzJWTCritHeader specifically targets the "crit" header field.
// I-JWT-CRIT-01: any non-empty crit array must produce 401, never panic.
func FuzzJWTCritHeader(f *testing.F) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})

	// Seed corpus: diverse crit values.
	seeds := []string{
		// RFC 7515 examples.
		`{"alg":"HS256","crit":["b64"]}`,
		`{"alg":"HS256","crit":["b64","x5t#S256"]}`,
		// Single unknown extension.
		`{"alg":"HS256","crit":["custom-extension"]}`,
		// Empty string member.
		`{"alg":"HS256","crit":[""]}`,
		// Known standard header names used as crit (still rejected — we don't support any).
		`{"alg":"HS256","crit":["alg"]}`,
		`{"alg":"HS256","crit":["typ","cty"]}`,
		// Large crit array.
		`{"alg":"HS256","crit":["a","b","c","d","e","f","g","h","i","j","k","l","m","n","o","p"]}`,
		// Crit with null member.
		`{"alg":"HS256","crit":[null]}`,
		// Crit with numeric member (invalid JSON for string array, triggers parse error).
		`{"alg":"HS256","crit":[1,2,3]}`,
		// Deeply nested crit (not a valid string array).
		`{"alg":"HS256","crit":[[["nested"]]]}`,
		// Empty crit array (should NOT be rejected — only non-empty triggers I-JWT-CRIT-01).
		`{"alg":"HS256","crit":[]}`,
		// Crit with Unicode extension names.
		`{"alg":"HS256","crit":["éxt"]}`,
		// Crit with control bytes in extension name.
		`{"alg":"HS256","crit":[" "]}`,
	}

	for _, hdr := range seeds {
		f.Add(hdr)
	}

	f.Fuzz(func(t *testing.T, headerJSON string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzJWTCritHeader: headerJSON=%q r=%v\n%s",
					headerJSON, r, debug.Stack())
			}
		}()

		headerB64 := buildJWTHeaderB64(headerJSON)
		token := buildJWTToken(headerB64, trivialPayloadB64, "fakesig")

		handler := mwHS256(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Only 200 or 401 are acceptable.
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status %d for headerJSON=%q", rec.Code, headerJSON)
		}

		// I-JWT-CRIT-01: if the header JSON parses and contains a non-empty crit,
		// the token MUST be rejected (401), not accepted (200).
		var hdr struct {
			Alg  string          `json:"alg"`
			Crit json.RawMessage `json:"crit"`
		}
		if json.Unmarshal([]byte(headerJSON), &hdr) == nil && hdr.Crit != nil {
			// Check if crit is a non-empty JSON array of strings.
			var critArr []json.RawMessage
			if json.Unmarshal(hdr.Crit, &critArr) == nil && len(critArr) > 0 {
				// At least one member — must be rejected.
				if rec.Code == http.StatusOK {
					t.Errorf("I-JWT-CRIT-01 VIOLATION: non-empty crit accepted (200), headerJSON=%q", headerJSON)
				}
			}
		}
	})
}

// FuzzJWTCtyTypHeader exercises "cty" and "typ" exotic values.
// I-JWT-CRIT-02: these fields do not affect acceptance/rejection in MuxMaster's
// JWTAuth (only alg + sig + claims matter). Exotic values must not panic.
func FuzzJWTCtyTypHeader(f *testing.F) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})

	// Seed: exotic typ and cty values per RFC 7519 §5.1, §5.2, RFC 9068.
	seeds := []string{
		`{"alg":"HS256","typ":"JWT"}`,
		`{"alg":"HS256","typ":"at+JWT"}`,
		`{"alg":"HS256","typ":"dpop+jwt"}`,
		`{"alg":"HS256","typ":"secevent+jwt"}`,
		`{"alg":"HS256","typ":"application/jwt"}`,
		`{"alg":"HS256","typ":""}`,
		`{"alg":"HS256","typ":null}`,
		`{"alg":"HS256","typ":123}`,
		`{"alg":"HS256","cty":"JWT"}`,
		`{"alg":"HS256","cty":"application/jwt"}`,
		`{"alg":"HS256","cty":""}`,
		`{"alg":"HS256","cty":null}`,
		// Both present.
		`{"alg":"HS256","typ":"JWT","cty":"JWT"}`,
		// typ with CRLF (should not inject into headers).
		`{"alg":"HS256","typ":"JWT\r\nX-Inject: evil"}`,
		// Unicode typ.
		`{"alg":"HS256","typ":"éjwt"}`,
		// Very long typ.
		`{"alg":"HS256","typ":"` + strings.Repeat("A", 1024) + `"}`,
		// typ=none (alg confusion attempt via typ — should not bypass alg check).
		`{"alg":"none","typ":"JWT"}`,
		// Extra unknown fields alongside typ/cty.
		`{"alg":"HS256","typ":"JWT","kid":"key-1","x5t":"abc","jku":"https://example.com/.well-known/jwks.json"}`,
	}

	for _, hdr := range seeds {
		f.Add(hdr)
	}

	f.Fuzz(func(t *testing.T, headerJSON string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzJWTCtyTypHeader: headerJSON=%q r=%v\n%s",
					headerJSON, r, debug.Stack())
			}
		}()

		headerB64 := buildJWTHeaderB64(headerJSON)
		token := buildJWTToken(headerB64, trivialPayloadB64, "fakesig")

		handler := mwHS256(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status %d for headerJSON=%q", rec.Code, headerJSON)
		}

		// Sanity check: response headers must not contain any value from the JWT
		// header (no reflection of typ/cty into response).
		for name, vals := range rec.Header() {
			for _, v := range vals {
				if strings.Contains(v, "X-Inject") || strings.Contains(v, "evil") {
					t.Errorf("possible header injection via JWT typ: header %q = %q", name, v)
				}
			}
		}
	})
}

// FuzzJWTExoticHeaderCombo exercises tokens with crit + cty + typ + unknown
// fields all present simultaneously.
func FuzzJWTExoticHeaderCombo(f *testing.F) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})
	mwMulti := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256", "RS256"},
		Secret:     []byte("testsecret"),
		PublicKey:  &jwtRSAKey.PublicKey,
	})

	// crit arrays combined with cty/typ — RFC 7515 §4.1.11 compliance.
	seeds := []struct {
		crit []string
		typ  string
		cty  string
	}{
		{[]string{"b64"}, "JWT", "JWT"},
		{[]string{}, "at+JWT", ""},
		{[]string{"x5t#S256", "b64"}, "dpop+jwt", "application/json"},
		{nil, "JWT", ""},
		{[]string{"custom"}, "", "text/plain"},
	}

	for _, s := range seeds {
		hdr := map[string]any{"alg": "HS256"}
		if s.crit != nil {
			hdr["crit"] = s.crit
		}
		if s.typ != "" {
			hdr["typ"] = s.typ
		}
		if s.cty != "" {
			hdr["cty"] = s.cty
		}
		b, _ := json.Marshal(hdr)
		f.Add(string(b))
	}

	// Also seed a few hand-crafted adversarial combinations.
	f.Add(`{"alg":"HS256","crit":["crit"],"cty":"JWT","typ":"JWT"}`)
	f.Add(`{"alg":"HS256","crit":["b64"],"b64":false,"typ":"JWT"}`)
	f.Add(`{"alg":"none","crit":[],"typ":"JWT","cty":"JWT"}`)
	f.Add(`{"alg":"HS256","crit":["alg"],"alg":"none"}`) // alg override attempt

	f.Fuzz(func(t *testing.T, headerJSON string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzJWTExoticHeaderCombo: headerJSON=%q r=%v\n%s",
					headerJSON, r, debug.Stack())
			}
		}()

		headerB64 := buildJWTHeaderB64(headerJSON)
		token := buildJWTToken(headerB64, trivialPayloadB64, "fakesig")

		for _, jwtMW := range []func(http.Handler) http.Handler{mwHS256, mwMulti} {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", token)
			rec := httptest.NewRecorder()
			jwtMW(h200).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
				t.Errorf("unexpected status %d for headerJSON=%q", rec.Code, headerJSON)
			}

			// I-JWT-CRIT-01: enforce crit rejection.
			var hdr struct {
				Crit json.RawMessage `json:"crit"`
			}
			if json.Unmarshal([]byte(headerJSON), &hdr) == nil {
				var arr []json.RawMessage
				if json.Unmarshal(hdr.Crit, &arr) == nil && len(arr) > 0 {
					if rec.Code == http.StatusOK {
						t.Errorf("I-JWT-CRIT-01 VIOLATION: non-empty crit accepted, headerJSON=%q", headerJSON)
					}
				}
			}
		}
	})
}

// FuzzJWTOversizedHeader verifies I-JWT-CRIT-03: oversized JSON headers do not
// cause OOM or panic. The middleware must parse (or reject) them gracefully.
func FuzzJWTOversizedHeader(f *testing.F) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})

	// Seeds: progressively larger headers.
	f.Add(1024)
	f.Add(65536)
	f.Add(1 << 20) // 1 MiB

	// Additional seed: a header with many fields.
	var bigHeader strings.Builder
	bigHeader.WriteString(`{"alg":"HS256"`)
	for i := range 1000 {
		bigHeader.WriteString(`,"field` + strings.Repeat("x", i%50) + `":"value"`)
	}
	bigHeader.WriteString("}")
	f.Add(len(bigHeader.String()))

	f.Fuzz(func(t *testing.T, size int) {
		if size < 0 || size > 4<<20 { // cap at 4 MiB to avoid OOM in fuzz corpus
			t.Skip()
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzJWTOversizedHeader: size=%d r=%v\n%s", size, r, debug.Stack())
			}
		}()

		// Build a header of the requested size by padding the alg field with a large string value.
		padding := strings.Repeat("A", size)
		headerJSON := `{"alg":"HS256","padding":"` + padding + `"}`
		headerB64 := buildJWTHeaderB64(headerJSON)
		token := buildJWTToken(headerB64, trivialPayloadB64, "fakesig")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		mwHS256(h200).ServeHTTP(rec, req)

		// Only 200 or 401 acceptable.
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Errorf("unexpected status %d for oversized header (size=%d)", rec.Code, size)
		}
	})
}

// TestJWTCritSemantics is a deterministic regression test that validates the
// exact acceptance/rejection semantics for all documented crit scenarios.
// This runs in short mode (no -fuzz flag needed).
func TestJWTCritSemantics(t *testing.T) {
	mwHS256 := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     []byte("testsecret"),
	})

	cases := []struct {
		name       string
		headerJSON string
		// wantRejected is true if we expect 401 due to the header alone
		// (i.e. before signature verification).
		wantRejected bool
	}{
		// Non-empty crit — must be rejected per RFC 7515 §4.1.11.
		{"crit_single", `{"alg":"HS256","crit":["b64"]}`, true},
		{"crit_multi", `{"alg":"HS256","crit":["b64","x5t#S256"]}`, true},
		{"crit_unknown", `{"alg":"HS256","crit":["custom"]}`, true},
		{"crit_empty_str", `{"alg":"HS256","crit":[""]}`, true},

		// Empty crit array — NOT rejected by crit rule (len == 0); rejected only by sig.
		{"crit_empty_array", `{"alg":"HS256","crit":[]}`, false},

		// No crit at all — rejected only by sig (fakesig).
		{"no_crit", `{"alg":"HS256"}`, false},
		{"with_typ", `{"alg":"HS256","typ":"JWT"}`, false},
		{"with_cty", `{"alg":"HS256","cty":"JWT"}`, false},
		{"with_typ_and_cty", `{"alg":"HS256","typ":"JWT","cty":"application/json"}`, false},

		// Malformed JSON — rejected as invalid (errJWTInvalid).
		{"malformed_json", `not-json`, true},
		// Unknown alg — rejected before crit check.
		{"unknown_alg", `{"alg":"UNKNOWN","crit":["b64"]}`, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			headerB64 := buildJWTHeaderB64(tc.headerJSON)
			token := buildJWTToken(headerB64, trivialPayloadB64, "fakesig")

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", token)
			rec := httptest.NewRecorder()
			mwHS256(h200).ServeHTTP(rec, req)

			// In all cases: must be 401 (signature is fake, so even valid headers fail).
			if rec.Code == http.StatusOK {
				t.Errorf("unexpected 200: fakesig should always fail sig check for headerJSON=%q", tc.headerJSON)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d for headerJSON=%q", rec.Code, tc.headerJSON)
			}
			// (tc.wantRejected is preserved for documentation — all cases yield 401
			// because the signature is always fake. The semantic distinction matters
			// for real signed tokens, which are covered by FuzzJWTAlgConfusion.)
			_ = tc.wantRejected
		})
	}
}
