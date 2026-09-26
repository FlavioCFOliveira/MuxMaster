// Regression tests for TM-2026-033 (rmp #291, specification/middleware-stdlib.md
// section 16, rules 71-75): CORS must add "Vary: Origin" to EVERY response it
// produces or forwards, unconditionally — with/without an Origin header,
// wildcard and allow-list AllowedOrigins, allowed and rejected origins,
// simple/preflight/error responses — without ever duplicating the "Origin"
// token or clobbering a Vary value some other middleware already set.
//
// See also:
//   - middleware/gap_o14_test.go: TestSec_CORS_NoOriginHeader_NoACAO (rule 72:
//     Access-Control-* stays absent for a no-Origin request; this file only
//     adds the Vary assertion there).
//   - reports/http-protocol-security-auditor/harness/tm_reclass_s20_test.go:
//     TestHPS_TM_2026_033_Regression_NonCORSResponseHasVaryOrigin (converted
//     from the pre-fix reproducer) and TestHPS_TM_2026_033_CORSCompress_VaryOnTheWire
//     (raw-wire framing: two separate "Vary:" field lines, not merged).
package middleware_test

import (
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// varyOriginTokenCount returns how many times "Origin" appears as a
// case-insensitive, comma-separated token across all of h's Vary values
// (RFC 9110 §5.1: field names are case-insensitive tokens; §12.5.5: Vary's
// values are themselves field names, and Vary is list-based).
func varyOriginTokenCount(h http.Header) int {
	count := 0
	for _, v := range h.Values("Vary") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "Origin") {
				count++
			}
		}
	}
	return count
}

// TestSec_CORS_VaryOrigin_Matrix drives every combination of CORS mode
// (wildcard / explicit allow-list), Origin-header presence, and
// simple/preflight/rejected request, and asserts exactly one "Origin" token
// appears across the response's Vary values (spec rules 71 and 74) while
// Access-Control-Allow-Origin keeps its pre-existing, unrelated behaviour
// (rule 72).
func TestSec_CORS_VaryOrigin_Matrix(t *testing.T) {
	type testCase struct {
		name          string
		opts          middleware.CORSOptions
		setOrigin     string // "" = no Origin header sent
		method        string
		wantStatus    int
		wantACAOEmpty bool
	}
	cases := []testCase{
		// Wildcard AllowedOrigins.
		{"wildcard/no-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"*"}}, "", http.MethodGet, http.StatusOK, true},
		{"wildcard/allowed-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"*"}}, "https://a.example", http.MethodGet, http.StatusOK, false},
		{"wildcard/allowed-origin/preflight", middleware.CORSOptions{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}}, "https://a.example", http.MethodOptions, http.StatusNoContent, false},

		// Explicit, non-wildcard allow-list.
		{"allowlist/no-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}}, "", http.MethodGet, http.StatusOK, true},
		{"allowlist/matched-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}}, "https://a.example", http.MethodGet, http.StatusOK, false},
		{"allowlist/matched-origin/preflight", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}, AllowedMethods: []string{"GET"}}, "https://a.example", http.MethodOptions, http.StatusNoContent, false},
		{"allowlist/disallowed-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}}, "https://evil.example", http.MethodGet, http.StatusForbidden, true},
		{"allowlist/disallowed-origin/preflight", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}, AllowedMethods: []string{"GET"}}, "https://evil.example", http.MethodOptions, http.StatusForbidden, true},

		// Multi-entry allow-list (exercises allowedOrigins map lookup, not just allowAll).
		{"multi-allowlist/no-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example", "https://b.example"}}, "", http.MethodGet, http.StatusOK, true},
		{"multi-allowlist/matched-origin/simple", middleware.CORSOptions{AllowedOrigins: []string{"https://a.example", "https://b.example"}}, "https://b.example", http.MethodGet, http.StatusOK, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := middleware.CORS(tc.opts)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/r", nil)
			if tc.setOrigin != "" {
				req.Header.Set("Origin", tc.setOrigin)
			}
			handlerRan := false
			mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handlerRan = true
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantACAOEmpty && rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("Access-Control-Allow-Origin = %q, want empty (rule 72)", rec.Header().Get("Access-Control-Allow-Origin"))
			}
			if tc.wantStatus == http.StatusForbidden && handlerRan {
				t.Fatalf("handler ran for a disallowed origin — CORS must reject before calling next()")
			}
			if got := varyOriginTokenCount(rec.Header()); got != 1 {
				t.Fatalf("Vary Origin token count = %d, want exactly 1 (Vary=%v)", got, rec.Header().Values("Vary"))
			}
		})
	}
}

// TestSec_CORS_VaryOrigin_InvalidOriginCRLF_400 covers CORS's own 400
// response (CRLF/NUL in the Origin header, rejected by isValidOrigin before
// any Access-Control-* header is set): spec rule 71 requires Vary: Origin
// here too, even though the response body is just "Bad Request".
func TestSec_CORS_VaryOrigin_InvalidOriginCRLF_400(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.Header.Set("Origin", "https://evil.example\r\nX-Injected: yes")

	handlerRan := false
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a CRLF-bearing Origin header", rec.Code)
	}
	if handlerRan {
		t.Fatalf("handler ran despite an invalid (CRLF) Origin header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty on the 400 response", got)
	}
	if got := varyOriginTokenCount(rec.Header()); got != 1 {
		t.Fatalf("Vary Origin token count = %d, want exactly 1 on the 400 response (Vary=%v)", got, rec.Header().Values("Vary"))
	}
}

// TestSec_CORS_VaryOrigin_ExistingUppercase_NotDuplicated covers the case
// that was already correct before this fix (a matched, non-wildcard
// origin): a second CORS-adjacent header-setter contributing "Vary: Origin"
// again must not produce a duplicate token, and TestSec_TM_2026_031_CORS_VaryOrigin
// / TestSec_CORS_VaryOrigin_WhenSpecificOriginReflected already cover the
// single-writer case.
func TestSec_CORS_VaryOrigin_ExistingUppercase_NotDuplicated(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}})
	rec := httptest.NewRecorder()
	// Simulate a prior write (e.g. by a wrapping test harness, or a
	// ResponseWriter that starts with headers pre-populated) that already
	// declared Vary: Origin before CORS's own handler ran.
	rec.Header().Add("Vary", "Origin")
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.Header.Set("Origin", "https://a.example")

	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if got := varyOriginTokenCount(rec.Header()); got != 1 {
		t.Fatalf("Vary Origin token count = %d, want exactly 1 (Vary=%v) — CORS must not add a duplicate", got, rec.Header().Values("Vary"))
	}
}

// TestSec_CORS_VaryOrigin_ExistingLowercaseFromOuterMiddleware_NotDuplicated
// covers spec rule 74's case-insensitivity requirement directly: an outer
// middleware in the Use() chain (running before CORS, and so writing to the
// shared ResponseWriter's headers before CORS's handler executes) sets
// "Vary: origin" — all lowercase, a token match per RFC 9110 §5.1 even
// though it is not the canonical "Origin" spelling CORS itself would write.
// CORS must recognise it as the same token and must not add a second one.
func TestSec_CORS_VaryOrigin_ExistingLowercaseFromOuterMiddleware_NotDuplicated(t *testing.T) {
	cors := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// outerMiddleware runs BEFORE cors in the Use() chain: it sets its own
	// lowercase "Vary: origin" and then calls next (cors(inner)).
	outerMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "origin")
			next.ServeHTTP(w, r)
		})
	}
	chain := outerMiddleware(cors(inner))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.Header.Set("Origin", "https://a.example")
	chain.ServeHTTP(rec, req)

	if got := varyOriginTokenCount(rec.Header()); got != 1 {
		t.Fatalf("Vary Origin token count = %d, want exactly 1 (Vary=%v) — the outer middleware's lowercase "+
			"\"origin\" token must be recognised and not duplicated", got, rec.Header().Values("Vary"))
	}
	// The pre-existing value's exact casing is preserved (rule 73: CORS
	// never overwrites an existing Vary value).
	found := false
	for _, v := range rec.Header().Values("Vary") {
		if v == "origin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Vary=%v, want the outer middleware's original lowercase \"origin\" value preserved unchanged", rec.Header().Values("Vary"))
	}
}

// TestSec_CORS_VaryOrigin_ExistingCombinedTokenList_NotDuplicated covers a
// single Vary value that already lists multiple comma-separated field
// names, one of which is "Origin" — e.g. "Accept-Encoding, Origin" set as
// ONE value rather than two Header.Add calls. CORS's token scan must still
// find "Origin" inside that combined list and skip adding a second one.
func TestSec_CORS_VaryOrigin_ExistingCombinedTokenList_NotDuplicated(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})
	rec := httptest.NewRecorder()
	rec.Header().Set("Vary", "Accept-Encoding, Origin")
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.Header.Set("Origin", "https://a.example")

	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if got := varyOriginTokenCount(rec.Header()); got != 1 {
		t.Fatalf("Vary Origin token count = %d, want exactly 1 (Vary=%v)", got, rec.Header().Values("Vary"))
	}
	if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding, Origin" {
		t.Fatalf("Vary = %v, want the original single combined value left untouched (rule 73)", got)
	}
}

// TestSec_CORS_VaryOrigin_CoexistWithCompress_BothOrders drives CORS and
// Compress together, in both Use()-chain orders, and asserts: (1) Origin
// appears exactly once across Vary, (2) Accept-Encoding appears exactly
// once, and (3) the two are carried as separate Vary values (Header.Add
// semantics — see TestHPS_TM_2026_033_CORSCompress_VaryOnTheWire for the
// on-the-wire confirmation that these serialise as two distinct "Vary:"
// field lines), regardless of which middleware runs first (spec rule 75).
func TestSec_CORS_VaryOrigin_CoexistWithCompress_BothOrders(t *testing.T) {
	cors := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}})
	compress := middleware.Compress(gzip.DefaultCompression)
	body := strings.Repeat("x", 2048) // above Compress's 1 KB threshold

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	})

	for _, tc := range []struct {
		name  string
		chain http.Handler
	}{
		{"cors-outer-compress-inner", cors(compress(inner))},
		{"compress-outer-cors-inner", compress(cors(inner))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/r", nil)
			req.Header.Set("Origin", "https://a.example")
			req.Header.Set("Accept-Encoding", "gzip")
			tc.chain.ServeHTTP(rec, req)

			if rec.Header().Get("Access-Control-Allow-Origin") != "https://a.example" {
				t.Fatalf("ACAO = %q, want the allowed origin reflected", rec.Header().Get("Access-Control-Allow-Origin"))
			}
			if rec.Header().Get("Content-Encoding") != "gzip" {
				t.Fatalf("Content-Encoding = %q, want gzip (body exceeds the compression threshold)", rec.Header().Get("Content-Encoding"))
			}

			vary := rec.Header().Values("Vary")
			originCount, aeCount := 0, 0
			for _, v := range vary {
				for _, tok := range strings.Split(v, ",") {
					switch strings.TrimSpace(strings.ToLower(tok)) {
					case "origin":
						originCount++
					case "accept-encoding":
						aeCount++
					}
				}
			}
			if originCount != 1 {
				t.Fatalf("%s: Origin token count = %d, want 1 (Vary=%v)", tc.name, originCount, vary)
			}
			if aeCount != 1 {
				t.Fatalf("%s: Accept-Encoding token count = %d, want 1 (Vary=%v)", tc.name, aeCount, vary)
			}
			if len(vary) != 2 {
				t.Fatalf("%s: %d Vary values, want 2 separate values (one per middleware, Header.Add semantics), got %v", tc.name, len(vary), vary)
			}
		})
	}
}

// TestSec_CORS_VaryOrigin_CoexistWithCompress_NoOrigin_BothOrders is the
// no-CORS-relevance counterpart of the test above: without an Origin
// header, CORS still contributes its own Vary: Origin (rule 71) alongside
// Compress's unconditional Vary: Accept-Encoding, in both orders, with no
// Access-Control-* headers set (rule 72).
func TestSec_CORS_VaryOrigin_CoexistWithCompress_NoOrigin_BothOrders(t *testing.T) {
	cors := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example"}})
	compress := middleware.Compress(gzip.DefaultCompression)
	body := strings.Repeat("x", 2048)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	})

	for _, tc := range []struct {
		name  string
		chain http.Handler
	}{
		{"cors-outer-compress-inner", cors(compress(inner))},
		{"compress-outer-cors-inner", compress(cors(inner))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/r", nil) // no Origin header
			req.Header.Set("Accept-Encoding", "gzip")
			tc.chain.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("%s: ACAO = %q, want empty (no Origin header)", tc.name, got)
			}
			if got := varyOriginTokenCount(rec.Header()); got != 1 {
				t.Fatalf("%s: Vary Origin token count = %d, want 1 (Vary=%v)", tc.name, got, rec.Header().Values("Vary"))
			}
			aeCount := 0
			for _, v := range rec.Header().Values("Vary") {
				for _, tok := range strings.Split(v, ",") {
					if strings.EqualFold(strings.TrimSpace(tok), "Accept-Encoding") {
						aeCount++
					}
				}
			}
			if aeCount != 1 {
				t.Fatalf("%s: Accept-Encoding token count = %d, want 1 (Vary=%v)", tc.name, aeCount, rec.Header().Values("Vary"))
			}
		})
	}
}
