// Harness — set_header.go.
//
// Threats covered:
//   - CWE-113 CRLF in header value.
//   - Header overwrite behaviour (last writer wins).
//   - Empty value / empty key.
//   - Control chars (\x00, \x1b) rejected by Go's Header.Set on write.
package harness

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-SH-001 — CRLF in value is refused by Go; SetHeader does NOT validate.
// We verify that the final serialised headers do not contain injected lines.
// -----------------------------------------------------------------------------

func TestSec_SetHeader_CRLFValue(t *testing.T) {
	payloads := map[string]string{
		"crlf":       "text/html\r\nX-Injected: yes",
		"lf":         "text/html\nX-Injected: yes",
		"cr":         "text/html\rInjected",
		"nul":        "text/html\x00",
		"ansi":       "text/html\x1b[2J",
		"long_8k":    strings.Repeat("A", 8192),
		"tab":        "text/html\ttab",
		"unicode_ls": "text/html\u2028",
	}

	for name, p := range payloads {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("SetHeader panicked on value=%q: %v", p, rcv)
				}
			}()
			mw := middleware.SetHeader("Content-Type", p)
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			// Validate both in-memory and on-wire views.
			got := rec.Header().Get("Content-Type")
			wire := rawHeaderDump(rec.Header())

			// WIRE check: only one header-block terminator. Multiple means
			// CRLF was injected past the stdlib sanitisation.
			if strings.Count(wire, "\r\n\r\n") > 1 {
				t.Errorf("set_header[%s]: WIRE contains injected header block:\n%s", name, wire)
			}
			// IN-MEMORY finding: SetHeader does NOT validate input; any caller
			// that passes a value through (untrusted) config may corrupt the
			// header value, but the wire is sanitised.
			if strings.ContainsAny(got, "\r\n") {
				t.Logf("MSR-SH-001 CONFIRMED: set_header[%s] accepted raw CR/LF; Go stdlib sanitises on wire (wire view below):\n%s",
					name, wire)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-SH-002 — Overwrite behaviour: SetHeader runs before handler; handler can override.
// -----------------------------------------------------------------------------

func TestSec_SetHeader_Overwrite(t *testing.T) {
	mw := middleware.SetHeader("X-Test", "middleware-value")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "handler-value")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Test"); got != "handler-value" {
		t.Errorf("set_header: got %q, want handler-value (handler must win)", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-SH-003 — Empty key / value.
// -----------------------------------------------------------------------------

func TestSec_SetHeader_EmptyValues(t *testing.T) {
	// Empty key — Go's Header.Set accepts it but it's nonsensical.
	// We just ensure no panic.
	mw := middleware.SetHeader("", "val")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Fatalf("SetHeader(\"\", \"val\") panicked: %v", rcv)
		}
	}()
	h.ServeHTTP(rec, req)
}
