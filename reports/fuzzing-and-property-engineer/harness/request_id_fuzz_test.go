// FuzzRequestIDReflection — the RequestID middleware copies an incoming
// X-Request-ID header to the response header verbatim.
//
// Invariants:
//   1. Never panics on any input header value.
//   2. When the output is NOT empty, it must equal the input (pin of current
//      behaviour).
//
// FPE-001 / H-004 tracked separately: CR/LF in the client-supplied X-Request-ID
// is reflected verbatim by the middleware. The stdlib http.Header.Set refuses
// CR/LF, but this middleware uses Set with whatever the client sent. The
// response writer's internal header map therefore contains raw CR/LF, and if
// the ResponseWriter implementation is permissive (or test double), the
// attacker controls response bytes — classic response splitting.
//
// This is pinned in TestFPE001_RequestIDCRLFReflection (evidence/FPE-001/)
// rather than as a failing assertion here, so that this fuzz target can keep
// running and discover OTHER regressions without being blocked on the
// long-standing H-004 issue.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func FuzzRequestIDReflection(f *testing.F) {
	f.Add("simple-id")
	f.Add("")
	f.Add(strings.Repeat("a", 65536))
	f.Add("id\x00null")
	f.Add("id\x1b[31m")
	f.Add("\ttab-prefixed")

	f.Fuzz(func(t *testing.T, clientID string) {
		if len(clientID) > 1<<20 {
			t.Skip()
		}
		// Skip CRLF inputs — those are pinned separately in the FPE-001 repro.
		if strings.ContainsAny(clientID, "\r\n") {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RequestID panicked on %q: %v\n%s", clientID, r, debug.Stack())
			}
		}()

		mw := middleware.RequestID()
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		h := mw(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header["X-Request-Id"] = []string{clientID}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		out := rec.Header().Get("X-Request-ID")

		// Reflection invariant (non-CRLF inputs): either output equals input,
		// or the middleware generated a fresh ID (empty input path).
		if clientID != "" && out != clientID {
			t.Fatalf("non-empty input not reflected verbatim\ninput=%q\noutput=%q",
				clientID, out)
		}
	})
}

// FuzzRequestIDGeneration — if client sends no X-Request-ID, the middleware
// generates one with crypto/rand. Check: never empty, hex-encoded, fixed length.
func FuzzRequestIDGeneration(f *testing.F) {
	f.Add(true)
	f.Add(false)

	f.Fuzz(func(t *testing.T, withClientID bool) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v\n%s", r, debug.Stack())
			}
		}()

		mw := middleware.RequestID()
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		h := mw(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if withClientID {
			req.Header.Set("X-Request-ID", "client-id")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		out := rec.Header().Get("X-Request-ID")
		if !withClientID {
			if len(out) != 32 {
				t.Fatalf("generated ID has wrong length: got %d, want 32", len(out))
			}
			for _, c := range out {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Fatalf("generated ID contains non-hex byte: %q", out)
				}
			}
		} else if out != "client-id" {
			t.Fatalf("did not propagate client ID: got %q", out)
		}
	})
}
