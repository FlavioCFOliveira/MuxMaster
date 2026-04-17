// Harness — request_id.go (H-004).
//
// Threats covered:
//   - crypto/rand is used (not math/rand).
//   - Client-supplied X-Request-ID is propagated unconditionally.
//   - CRLF / control chars in client X-Request-ID.
//   - Length bounds (10KB+).
//   - Collision rate in generated IDs.
//   - Context key uses typed struct (not string).
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-RQ-001 — Static: crypto/rand is the source (not math/rand).
// -----------------------------------------------------------------------------

func TestSec_RequestID_UsesCryptoRand(t *testing.T) {
	src, err := readSource("/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/request_id.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `"crypto/rand"`) {
		t.Error("request_id: crypto/rand import missing; ID generation is not cryptographically secure")
	}
	if strings.Contains(src, `"math/rand"`) {
		t.Error("request_id: math/rand import present; must not be used for ID generation")
	}
}

// -----------------------------------------------------------------------------
// MSR-RQ-002 — Collision rate in 100k generated IDs.
// -----------------------------------------------------------------------------

func TestSec_RequestID_NoCollisions(t *testing.T) {
	if testing.Short() {
		t.Skip("collision test — skipped in short mode")
	}
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	const n = 100_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		id := rec.Header().Get("X-Request-ID")
		if id == "" {
			t.Fatalf("iteration %d: X-Request-ID empty", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: duplicate ID %q", i, id)
		}
		seen[id] = struct{}{}
	}
	t.Logf("request_id: %d unique IDs, no collisions", n)
}

// -----------------------------------------------------------------------------
// MSR-RQ-003 — Client-supplied X-Request-ID is reflected verbatim.
// This is the H-004 finding — request_id does not sanitise the client input.
// -----------------------------------------------------------------------------

func TestSec_RequestID_ClientReflection(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	// Non-malicious baseline.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-trace-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "client-trace-123" {
		t.Errorf("request_id reflection: got %q, want %q", got, "client-trace-123")
	}
}

// -----------------------------------------------------------------------------
// MSR-RQ-004 — CRLF / ANSI / control chars in client X-Request-ID.
//
// Go's Header.Set validates and drops CRLF. We test both:
//   - The value is NOT smuggled into the response headers as a line break.
//   - Control chars still reach the response (Go drops only CR/LF).
// -----------------------------------------------------------------------------

func TestSec_RequestID_ControlCharReflection(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	payloads := map[string]string{
		"crlf":        "abc\r\nX-Smuggled: yes",
		"lf":          "abc\nX-Smuggled: yes",
		"cr":          "abc\rsmuggled",
		"tab":         "abc\tdef",
		"nul":         "abc\x00def",
		"bel":         "abc\x07",
		"ansi":        "abc\x1b[2Jcls",
		"long_4k":     strings.Repeat("a", 4096),
		"long_64k":    strings.Repeat("a", 64*1024),
		"long_1mb":    strings.Repeat("a", 1024*1024),
		"vertical":    "abc\x0bdef",
		"multi_crlf":  "a\r\nb\r\nc\r\nd",
		"utf8":        "abc\u202eunicode",
	}

	for name, p := range payloads {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			// Use direct map assignment to bypass Go's set-side validation.
			req.Header["X-Request-Id"] = []string{p}
			rec := httptest.NewRecorder()
			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("request_id panicked on id=%q: %v", truncate(p, 64), rcv)
				}
			}()
			h.ServeHTTP(rec, req)

			// rec.Header() preserves the raw value (in-memory map).
			// Go's Header.Write (serialisation to wire) converts CR/LF to space.
			// Test both views to fully characterise the behaviour.
			got := rec.Header().Get("X-Request-Id")
			wireView := rawHeaderDump(rec.Header())

			// The WIRE representation must NOT contain a spliced header.
			if strings.Count(wireView, "\r\n\r\n") > 1 {
				t.Errorf("request_id[%s]: wire serialisation contains spliced header block:\n%s",
					name, wireView)
			}
			// CR/LF in the in-memory Get() value — finding MSR-RQ-004.
			if strings.ContainsAny(got, "\r\n") {
				t.Logf("MSR-RQ-004 CONFIRMED: request_id[%s] in-memory header value retains raw CR/LF: %q (wire sanitised by stdlib to: %s)",
					name, got, wireView)
			}

			// No length cap — arbitrary-size IDs reach the response.
			if len(p) > 10*1024 && len(got) > 0 {
				t.Logf("MSR-RQ-004 CONFIRMED: request_id accepted %d-byte client ID; output %d bytes (DoS bloat of response)",
					len(p), len(got))
			}
			if strings.Contains(got, "\x1b") {
				t.Logf("MSR-RQ-004 CONFIRMED: request_id[%s] reflected ANSI escape into response header — log/terminal risk",
					name)
			}
			if strings.Contains(got, "\x00") {
				t.Logf("MSR-RQ-004 CONFIRMED: request_id[%s] reflected NUL byte into response header", name)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-RQ-005 — Typed key in context — string key would collide.
// Static scan: the code uses `type requestIDKey struct{}`.
// -----------------------------------------------------------------------------

func TestSec_RequestID_TypedContextKey(t *testing.T) {
	src, err := readSource("/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/request_id.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "type requestIDKey struct") {
		t.Error("request_id: context key is not a typed struct — collision risk")
	}
	// Forbid string literals as keys.
	bad := []string{
		`context.WithValue(r.Context(), "request_id"`,
		`context.WithValue(ctx, "request_id"`,
		`context.WithValue(r.Context(), "requestID"`,
	}
	for _, b := range bad {
		if strings.Contains(src, b) {
			t.Errorf("request_id: string key %q found — collision risk", b)
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-RQ-006 — Propagation to context verifies roundtrip.
// -----------------------------------------------------------------------------

func TestSec_RequestID_Propagation(t *testing.T) {
	mw := middleware.RequestID()
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "propagate-me")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if captured != "propagate-me" {
		t.Errorf("request_id: context value=%q, want propagate-me", captured)
	}
}

// -----------------------------------------------------------------------------
// MSR-RQ-007 — Concurrent IDs — no races.
// -----------------------------------------------------------------------------

func TestSec_RequestID_Concurrent(t *testing.T) {
	mw := middleware.RequestID()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	var wg sync.WaitGroup
	const g = 16
	wg.Add(g)
	for i := 0; i < g; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/g%d/j%d", id, j), nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if v := rec.Header().Get("X-Request-ID"); v == "" {
					t.Error("empty request id")
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
