// Harness — real_ip.go (H-009).
//
// Threats covered:
//   - CWE-345 XFF unconditionally trusted (no trusted-proxy list).
//   - Chain pollution — how first/last pick affects downstream trust.
//   - CWE-20 CRLF in XFF / X-Real-IP.
//   - IPv6 parsing ([::1], 2001:db8::1).
//   - Precedence between XFF and X-Real-IP.
//   - Tab, space, empty, whitespace-only in XFF.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// realIPHandler captures the post-middleware r.RemoteAddr.
func realIPHandler(captured *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})
}

// -----------------------------------------------------------------------------
// MSR-RI-001 — Trust matrix (H-009)
// -----------------------------------------------------------------------------

func TestSec_RealIP_TrustMatrix(t *testing.T) {
	cases := []struct {
		name           string
		remoteAddr     string
		xff            string
		xri            string
		wantRemoteAddr string
	}{
		// No headers → RemoteAddr unchanged.
		{"no_headers", "192.0.2.1:12345", "", "", "192.0.2.1:12345"},

		// Single XFF → RemoteAddr = XFF (fully trusted).
		{"single_xff", "10.0.0.5:12345", "1.2.3.4", "", "1.2.3.4"},

		// Chain XFF "a, b, c" → RemoteAddr = first element.
		{"chain_xff_first", "10.0.0.5:12345", "1.2.3.4, 5.6.7.8, 10.0.0.5", "", "1.2.3.4"},

		// XFF with tab-separator — tab-separated value is not a valid IP; rejected.
		{"xff_tab_sep", "10.0.0.5:12345", "1.2.3.4 \t5.6.7.8", "", "10.0.0.5:12345"},

		// XFF with whitespace — TrimSpace runs.
		{"xff_whitespace", "10.0.0.5:12345", "   1.2.3.4   ", "", "1.2.3.4"},

		// Empty XFF with X-Real-IP present — XRI used.
		{"xri_only", "10.0.0.5:12345", "", "1.2.3.4", "1.2.3.4"},

		// Both present — XFF wins (matches code path).
		{"xff_and_xri", "10.0.0.5:12345", "9.9.9.9", "1.2.3.4", "9.9.9.9"},

		// IPv6 without brackets.
		{"ipv6_plain", "10.0.0.5:12345", "2001:db8::1", "", "2001:db8::1"},

		// IPv6 with brackets — netip.ParseAddr rejects bracketed form; RemoteAddr unchanged.
		{"ipv6_bracketed", "10.0.0.5:12345", "[2001:db8::1]", "", "10.0.0.5:12345"},

		// Private ranges — accepted without warning.
		{"private_xff", "10.0.0.5:12345", "192.168.1.1", "", "192.168.1.1"},
		{"loopback_xff", "10.0.0.5:12345", "127.0.0.1", "", "127.0.0.1"},

		// Obviously invalid IP — rejected by netip.ParseAddr; RemoteAddr unchanged.
		{"garbage_xff", "10.0.0.5:12345", "not-an-ip!!!", "", "10.0.0.5:12345"},

		// Empty XFF string (header present, value empty) → skipped.
		{"empty_xff", "192.0.2.1:12345", "", "", "192.0.2.1:12345"},

		// XFF with only whitespace — trimmed to empty, rejected; RemoteAddr unchanged.
		{"whitespace_only_xff", "192.0.2.1:12345", "   ", "", "192.0.2.1:12345"},

		// XFF with trailing comma.
		{"trailing_comma", "10.0.0.5:12345", "1.2.3.4,", "", "1.2.3.4"},

		// XFF leading comma — first element is empty after split; rejected; RemoteAddr unchanged.
		{"leading_comma", "10.0.0.5:12345", ",1.2.3.4", "", "10.0.0.5:12345"},
	}

	rows := [][]string{{"case", "remote_addr_in", "xff", "xri", "remote_addr_out", "expected"}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured string
			h := middleware.RealIP()(realIPHandler(&captured))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				req.Header.Set("X-Real-IP", tc.xri)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			rows = append(rows, []string{
				tc.name, tc.remoteAddr, tc.xff, tc.xri, captured, tc.wantRemoteAddr,
			})

			if captured != tc.wantRemoteAddr {
				t.Errorf("real_ip[%s]: got RemoteAddr=%q, want %q", tc.name, captured, tc.wantRemoteAddr)
			}
		})
	}

	writeCSV(t, "real-ip-matrix.csv", rows)
}

// -----------------------------------------------------------------------------
// MSR-RI-002 — CRLF in XFF is stripped by Go's Header.Set, but we test the
// runtime handling when the header arrived via direct map assignment.
// -----------------------------------------------------------------------------

func TestSec_RealIP_CRLFInXFF(t *testing.T) {
	payloads := map[string]string{
		"crlf":      "1.2.3.4\r\nInjected: yes",
		"lf":        "1.2.3.4\nInjected: yes",
		"cr":        "1.2.3.4\rX: y",
		"null":      "1.2.3.4\x00",
		"tab":       "1.2.3.4\t",
		"long":      strings.Repeat("a", 16*1024),
		"ansi":      "1.2.3.4\x1b[2J",
		"comma_sep": "1.2.3.4, 5.6.7.8\r\nBad: yes",
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			var captured string
			h := middleware.RealIP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.RemoteAddr
				// Force Go to serialise response — if CRLF crept in we'd see it.
				w.Header().Set("Echo-Addr", r.RemoteAddr)
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.5:12345"
			// Assign directly via the map to simulate wire-level injection
			// (which will not happen through http.Server in practice — Go's
			// net/http rejects it — but we exercise the middleware's own
			// sanitisation).
			req.Header["X-Forwarded-For"] = []string{payload}
			rec := httptest.NewRecorder()

			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("real_ip panicked on XFF=%q: %v", payload, rcv)
				}
			}()
			h.ServeHTTP(rec, req)

			// Go's http.Header.Set (used when downstream echoes addr via Set)
			// replaces raw CR/LF with space, so the wire-level header block
			// cannot contain injected lines. However the RemoteAddr STRING
			// itself keeps the CR/LF; any downstream code that concatenates
			// RemoteAddr into raw HTTP/log output WITHOUT Header.Set is
			// vulnerable. This is the MSR-RI-002 finding.
			raw := rawHeaderDump(rec.Header())
			if strings.Contains(raw, "\r\n\r\n") ||
				(strings.Contains(raw, "Injected: yes") && !strings.Contains(raw, "Echo-Addr: 1.2.3.4")) {
				// If Echo-Addr contains "Injected: yes" as substring but the
				// CR/LF was sanitised to space, that's Go stdlib's protection.
				// A true smuggle would have "\r\nInjected: yes\r\n" on a new line.
				rawHeader := rec.Header().Get("Echo-Addr")
				if strings.ContainsAny(rawHeader, "\r\n") {
					t.Errorf("real_ip[%s]: CRLF survived Go's Header.Set sanitisation:\n%s",
						name, raw)
				}
			}

			// Document what ends up in RemoteAddr.
			t.Logf("real_ip[%s]: payload=%q → RemoteAddr=%q", name, truncate(payload, 64), captured)

			// Document: real_ip does NOT sanitise CRLF. Downstream middlewares
			// that bypass http.Header.Set (e.g. log writer concatenation) can
			// smuggle. Record as finding MSR-RI-002.
			if strings.Contains(captured, "\r") || strings.Contains(captured, "\n") {
				t.Logf("MSR-RI-002 CONFIRMED: RemoteAddr retained raw CR/LF bytes; downstream risk if addr is used in logs/raw I/O: %q",
					captured)
			}
		})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("...(%d more)", len(s)-n)
}

// -----------------------------------------------------------------------------
// MSR-RI-003 — Repro for downstream bypass: spoofed XFF lets attacker dodge a
// per-IP throttle built on top of real_ip.
// -----------------------------------------------------------------------------

func TestSec_RealIP_DownstreamPerIPBypass(t *testing.T) {
	// Simulate a tiny per-IP counter that a naive user might put after real_ip.
	counters := make(map[string]int)
	perIP := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counters[r.RemoteAddr]++
			next.ServeHTTP(w, r)
		})
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.RealIP()(perIP(inner))

	// Attacker varies XFF per request.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:12345" // the actual attacker
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("1.2.3.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}

	// Confirm the real attacker's address never reaches the counter.
	if counters["10.0.0.5:12345"] != 0 {
		t.Errorf("real_ip: attacker address leaked into per-IP counter: %v", counters["10.0.0.5:12345"])
	}
	if len(counters) < 50 {
		t.Errorf("real_ip: attacker only managed %d distinct IPs in 100 requests — expected close to 100", len(counters))
	}
	t.Logf("real_ip: attacker achieved %d unique counter entries from a single real IP (documented DoS bypass)",
		len(counters))
}

// -----------------------------------------------------------------------------
// MSR-RI-004 — XFF precedence: when both headers set, XFF wins.
// Verify deterministic behaviour.
// -----------------------------------------------------------------------------

func TestSec_RealIP_PrecedenceDeterministic(t *testing.T) {
	for i := 0; i < 10; i++ {
		var captured string
		h := middleware.RealIP()(realIPHandler(&captured))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:12345"
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		req.Header.Set("X-Real-IP", "4.4.4.4")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if captured != "8.8.8.8" {
			t.Errorf("iteration %d: RemoteAddr=%q, want 8.8.8.8 (XFF wins)", i, captured)
		}
	}
}
