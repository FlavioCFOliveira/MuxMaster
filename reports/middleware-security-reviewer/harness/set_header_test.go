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
// MSR-SH-001 — FIXED (MM-2026-0037): SetHeader now panics at construction time
// for values containing CR or LF. Non-CR/LF control chars pass through; Go
// stdlib sanitises them on the wire.
// -----------------------------------------------------------------------------

func TestSec_SetHeader_CRLFValue(t *testing.T) {
	// Payloads with CR or LF must panic at construction.
	shouldPanic := map[string]string{
		"crlf": "text/html\r\nX-Injected: yes",
		"lf":   "text/html\nX-Injected: yes",
		"cr":   "text/html\rInjected",
	}
	for name, p := range shouldPanic {
		t.Run(name, func(t *testing.T) {
			panicked := false
			func() {
				defer func() {
					if recover() != nil {
						panicked = true
					}
				}()
				middleware.SetHeader("Content-Type", p)
			}()
			if !panicked {
				t.Errorf("set_header[%s]: expected panic on CR/LF value %q", name, p)
			} else {
				t.Logf("MM-2026-0037 FIXED: set_header[%s] panics at construction", name)
			}
		})
	}

	// Payloads without CR/LF must not panic; wire is sanitised by Go stdlib.
	shouldPass := map[string]string{
		"nul":        "text/html\x00",
		"ansi":       "text/html\x1b[2J",
		"long_8k":    strings.Repeat("A", 8192),
		"tab":        "text/html\ttab",
		"unicode_ls": "text/html\u2028",
	}
	for name, p := range shouldPass {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("SetHeader panicked unexpectedly on value=%q: %v", p, rcv)
				}
			}()
			mw := middleware.SetHeader("Content-Type", p)
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			wire := rawHeaderDump(rec.Header())
			if strings.Count(wire, "\r\n\r\n") > 1 {
				t.Errorf("set_header[%s]: WIRE contains injected header block:\n%s", name, wire)
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
