// Harness — cors.go (H-005).
//
// Covered threats:
//   - CWE-942 overly permissive CORS (origin reflection without whitelist).
//   - CWE-942 wildcard + credentials (spec violation).
//   - CWE-113 CRLF injection via Origin reflection.
//   - "null" origin accepted.
//   - Subdomain wildcard interpretation (none — MuxMaster does exact match).
//   - Preflight bypass (OPTIONS not triggered).
//   - Case-sensitivity of method parsing.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func corsInner() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// -----------------------------------------------------------------------------
// MSR-CO-001 — Wildcard + AllowCredentials MUST panic
// -----------------------------------------------------------------------------

func TestSec_CORS_WildcardAndCredentialsPanics(t *testing.T) {
	defer func() {
		if rcv := recover(); rcv == nil {
			t.Fatal("CORS(['*']+credentials) did NOT panic — spec violation escape")
		}
	}()
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}

// -----------------------------------------------------------------------------
// MSR-CO-002 — wildcard + credentials via second entry
// Make sure panic check iterates over the whole list, not only [0].
// -----------------------------------------------------------------------------

func TestSec_CORS_WildcardLaterAndCredentialsPanics(t *testing.T) {
	defer func() {
		if rcv := recover(); rcv == nil {
			t.Fatal("CORS panic check must scan every element of AllowedOrigins")
		}
	}()
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://a.com", "*", "https://b.com"},
		AllowCredentials: true,
	})
}

// -----------------------------------------------------------------------------
// MSR-CO-003 — Origin reflection with `*` (no credentials) echoes attacker's Origin
// This is a FINDING: with AllowedOrigins=["*"], the middleware echoes
// the attacker's Origin (the common CORS wildcard convention is to reply
// `ACAO: *` — NOT to reflect). Combined with custom response shape, a caller
// may rely on the wildcard behaviour but the middleware actively turns
// wildcard mode into reflection mode.
//
// Current behaviour is the High-severity finding reported as MSR-CO-003.
// This test documents the behaviour and fails if it silently changes.
// -----------------------------------------------------------------------------

func TestSec_CORS_WildcardEchoesOriginNotStar(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})
	h := mw(corsInner())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	switch got {
	case "*":
		t.Logf("OK (future): wildcard stays wildcard — behaviour has been fixed")
	case "https://evil.example":
		t.Logf("MSR-CO-003 CONFIRMED (High): wildcard reflects attacker Origin %q — should emit '*' or explicit whitelist. Documented.", got)
	default:
		t.Errorf("CORS: unexpected ACAO=%q for wildcard", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-004 — Origin whitelist strict match (baseline)
// -----------------------------------------------------------------------------

func TestSec_CORS_WhitelistStrict(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})
	h := mw(corsInner())

	for _, origin := range []string{
		"https://attacker.com",
		"https://example.com.attacker.com",
		"https://example.comEVIL",
		"http://example.com",  // scheme mismatch
		"https://Example.Com", // case
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("CORS: origin=%q got status %d, want 403", origin, rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("CORS: origin=%q leaked ACAO=%q", origin, got)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-005 — Empty AllowedOrigins = pass-through without ACAO
//
// If AllowedOrigins is empty, the middleware calls next without setting ACAO.
// This is the current behaviour (arguably surprising, but documented).
// Lock it with a regression test.
// -----------------------------------------------------------------------------

func TestSec_CORS_EmptyWhitelistPassThrough(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{})
	h := mw(corsInner())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://attacker.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("CORS empty whitelist: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS empty whitelist: got unexpected ACAO=%q", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-006 — "null" origin (sandboxed iframe / data: / file:) accepted if whitelisted
// -----------------------------------------------------------------------------

func TestSec_CORS_NullOriginBehaviour(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com"},
	})
	h := mw(corsInner())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "null")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// MuxMaster rejects "null" here because it's not on the whitelist — good.
	if rec.Code != http.StatusForbidden {
		t.Errorf("CORS: null origin with strict whitelist got %d, want 403", rec.Code)
	}

	// Variant: whitelist explicitly includes "null" (opt-in).
	mw2 := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"null"},
	})
	h2 := mw2(corsInner())
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "null")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("CORS: null origin with explicit whitelist got %d, want 200", rec2.Code)
	}
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Errorf("CORS: null opt-in ACAO=%q, want null", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-007 — CRLF in Origin — Go's Header().Set drops these.
// We verify empirically that CRLF cannot smuggle through ACAO.
// -----------------------------------------------------------------------------

func TestSec_CORS_CRLFInOrigin(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})
	h := mw(corsInner())

	payloads := map[string]string{
		"crlf":      "https://evil.com\r\nSet-Cookie: evil=1",
		"lf":        "https://evil.com\nSet-Cookie: evil=1",
		"cr":        "https://evil.com\rX: y",
		"null_byte": "https://evil.com\x00X-Zero: y",
		"tab":       "https://evil.com\tTab",
		"long_16k":  "https://" + strings.Repeat("a", 16*1024),
		"bom":       "\uFEFFhttps://evil.com",
		"utf8_high": "https://evil.com\u2028",
		"space":     "https://evil .com",
		"ansi":      "https://evil.com\x1b[2J",
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			// Header.Set sanitises \r\n on *set*. Assign directly via map, which is
			// the attack surface for a custom client. But we use Set to mirror
			// production callers who use Header.Set. The net/http server is what
			// would reject on the wire.
			req.Header.Set("Origin", payload)
			rec := httptest.NewRecorder()

			func() {
				defer func() {
					if rcv := recover(); rcv != nil {
						t.Fatalf("CORS panicked on origin=%q: %v", payload, rcv)
					}
				}()
				h.ServeHTTP(rec, req)
			}()

			raw := rawHeaderDump(rec.Header())
			// Go 1.26 http.Header.Write replaces raw CR/LF with space during
			// serialisation, so no real injection reaches the wire. A true
			// CRLF injection survives only if the dump has >1 header-block
			// terminator.
			if strings.Count(raw, "\r\n\r\n") > 1 {
				t.Errorf("CORS[%s]: multiple header-block terminators on wire — CRLF injection succeeded:\n%s",
					name, raw)
			}
			// In-memory finding (CORS reflects Origin verbatim into ACAO):
			acao := rec.Header().Get("Access-Control-Allow-Origin")
			if strings.ContainsAny(acao, "\r\n") {
				t.Logf("MSR-CO-007 CONFIRMED: CORS[%s] raw CR/LF survived in in-memory ACAO (wire sanitised): %q",
					name, acao)
			}
			// Additional note: ACAO value retained "Set-Cookie:" substring
			// because stdlib sanitisation turns CR/LF to space — the attacker
			// payload becomes an ugly value but does not break out of the
			// header. Document and continue.
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-008 — Preflight OPTIONS behaviour
// -----------------------------------------------------------------------------

func TestSec_CORS_Preflight(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://ok.com"},
		AllowedMethods: []string{"GET", "POST", "DELETE"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         3600,
	})
	// Inner handler must NOT be called for preflight.
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://ok.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Error("CORS preflight reached inner handler — must short-circuit with 204")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("CORS preflight status=%d, want 204", rec.Code)
	}
	for _, want := range []string{"GET", "POST", "DELETE"} {
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), want) {
			t.Errorf("CORS preflight ACAM missing %s", want)
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-009 — No-Origin request bypasses CORS path entirely (pass-through)
// -----------------------------------------------------------------------------

func TestSec_CORS_NoOriginPasses(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://ok.com"},
	})
	h := mw(corsInner())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("CORS without Origin: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS without Origin: leaked ACAO=%q", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CO-010 — 5-dimensional matrix (Origin × Method × Credentials × ReqHdr × Mode)
//
// This exhaustive matrix is the primary evidence. We drive every cell, log
// the outcome, and emit the result as cors-matrix.csv.
// -----------------------------------------------------------------------------

func TestSec_CORS_Matrix(t *testing.T) {
	origins := []string{
		"https://trusted.com",  // whitelisted
		"https://attacker.com", // not whitelisted
		"null",                 // null origin
		"",                     // no origin
	}
	methods := []string{"GET", "POST", "OPTIONS"}
	credentials := []bool{false, true}
	reqHeaders := []string{"", "Authorization", "Content-Type"}
	modes := []string{"strict", "wildcard"}

	rows := [][]string{{"origin", "method", "credentials", "request_headers", "mode", "status", "acao", "acac"}}

	for _, mode := range modes {
		opts := middleware.CORSOptions{
			AllowedOrigins: []string{"https://trusted.com"},
			AllowedMethods: []string{"GET", "POST"},
			AllowedHeaders: []string{"Authorization", "Content-Type"},
		}
		if mode == "wildcard" {
			opts.AllowedOrigins = []string{"*"}
		}

		for _, creds := range credentials {
			// Skip illegal combination (wildcard + credentials panics by design).
			if mode == "wildcard" && creds {
				continue
			}
			opts.AllowCredentials = creds
			mw := middleware.CORS(opts)
			h := mw(corsInner())

			for _, origin := range origins {
				for _, method := range methods {
					for _, reqH := range reqHeaders {
						req := httptest.NewRequest(method, "/", nil)
						if origin != "" {
							req.Header.Set("Origin", origin)
						}
						if method == "OPTIONS" {
							req.Header.Set("Access-Control-Request-Method", "POST")
						}
						if reqH != "" {
							req.Header.Set("Access-Control-Request-Headers", reqH)
						}
						rec := httptest.NewRecorder()
						h.ServeHTTP(rec, req)
						rows = append(rows, []string{
							origin,
							method,
							fmt.Sprintf("%v", creds),
							reqH,
							mode,
							fmt.Sprintf("%d", rec.Code),
							rec.Header().Get("Access-Control-Allow-Origin"),
							rec.Header().Get("Access-Control-Allow-Credentials"),
						})
					}
				}
			}
		}
	}
	writeCSV(t, "cors-matrix.csv", rows)
	t.Logf("CORS matrix: %d rows written to evidence/cors-matrix.csv", len(rows)-1)
}
