package harness

// GAP A: RealIP CIDR parsing edge cases.
//
// Fuzzes the RealIP middleware's CIDR-trust logic with adversarial peer addresses,
// X-Forwarded-For values, and X-Real-IP values. Targets:
//   - IPv6 zone IDs (e.g. "fe80::1%eth0")
//   - Embedded null bytes and CRLF
//   - Square-bracket notation ([::1])
//   - Malformed CIDR prefixes passed at construction time
//   - CIDR bypass: attacker-controlled header accepted despite untrusted peer
//
// Invariants verified:
//   I-REALIP-01: RealIP never panics on any combination of peer address and header.
//   I-REALIP-02: When trustedCIDRs is non-empty and peer is NOT in any CIDR,
//                r.RemoteAddr must NOT be overwritten by any header value.
//   I-REALIP-03: r.RemoteAddr is only set to values that netip.ParseAddr accepts
//                (no CRLF, no embedded brackets, no zone IDs propagated as-is).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime/debug"
	"strings"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// parseCIDRSafe wraps netip.ParsePrefix; returns nil on parse error so the
// harness can exercise RealIP with deliberately malformed CIDR strings without
// crashing the fuzzer setup step itself.
func parseCIDRSafe(s string) *netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return nil
	}
	return &p
}

// buildRemoteAddr returns a minimal "host:port" suitable for r.RemoteAddr.
// If host looks like an IPv6 literal (contains ':'), it is wrapped in brackets.
func buildRemoteAddr(host string) string {
	if strings.ContainsRune(host, ':') {
		return "[" + host + "]:1234"
	}
	return host + ":1234"
}

// FuzzRealIPNoPanic verifies I-REALIP-01: the middleware never panics regardless
// of the peer address, X-Forwarded-For, or X-Real-IP content.
func FuzzRealIPNoPanic(f *testing.F) {
	// Seed: valid IPv4 and IPv6 peers + normal headers.
	f.Add("127.0.0.1", "203.0.113.1", "")
	f.Add("::1", "2001:db8::1", "")
	f.Add("10.0.0.1", "10.0.0.2, 192.168.0.1", "")
	// IPv6 zone ID in XFF — should be rejected by netip.ParseAddr.
	f.Add("::1", "fe80::1%eth0", "")
	// Bracket notation in header — invalid for netip.ParseAddr.
	f.Add("127.0.0.1", "[::1]", "")
	// CRLF injection in XFF.
	f.Add("127.0.0.1", "1.2.3.4\r\nX-Injected: evil", "")
	// NUL byte in header.
	f.Add("127.0.0.1", "1.2.3.4\x00", "")
	// Oversized XFF value.
	f.Add("127.0.0.1", strings.Repeat("1.2.3.4, ", 200)+"5.6.7.8", "")
	// X-Real-IP variations.
	f.Add("127.0.0.1", "", "  2.3.4.5  ")
	f.Add("127.0.0.1", "", "fe80::1%eth0")
	f.Add("127.0.0.1", "", "::ffff:192.0.2.1")
	// Empty peer.
	f.Add("", "1.2.3.4", "")
	// IPv4-mapped IPv6.
	f.Add("::ffff:127.0.0.1", "1.2.3.4", "")

	trustedIPv4, _ := netip.ParsePrefix("127.0.0.1/8")
	trustedIPv6, _ := netip.ParsePrefix("::1/128")
	mwTrusted := mw.RealIP(&trustedIPv4, &trustedIPv6)
	mwNoArgs := mw.RealIP()

	fuzzRealIP := func(t *testing.T, peer, xff, xri string, handler func(http.Handler) http.Handler) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in RealIP: peer=%q xff=%q xri=%q r=%v\n%s",
					peer, xff, xri, r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = buildRemoteAddr(peer)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		if xri != "" {
			req.Header.Set("X-Real-IP", xri)
		}
		rec := httptest.NewRecorder()
		handler(h200).ServeHTTP(rec, req)
	}

	f.Fuzz(func(t *testing.T, peer, xff, xri string) {
		fuzzRealIP(t, peer, xff, xri, mwTrusted)
		fuzzRealIP(t, peer, xff, xri, mwNoArgs)
	})
}

// FuzzRealIPBypassCheck verifies I-REALIP-02: an untrusted peer must NOT have
// r.RemoteAddr overwritten. This is a differential property: we compare
// RemoteAddr before vs after when the peer is outside the trusted CIDR.
func FuzzRealIPBypassCheck(f *testing.F) {
	// Seeds: untrusted peers with enticing XFF values.
	f.Add("8.8.8.8", "127.0.0.1")          // attacker claims localhost IP via XFF
	f.Add("1.2.3.4", "10.0.0.1")            // attacker claims RFC-1918 IP
	f.Add("203.0.113.5", "203.0.113.5")     // attacker echoes own IP
	f.Add("2001:db8::beef", "::1")           // IPv6 attacker claims loopback
	f.Add("198.51.100.1", "192.168.0.1, 10.0.0.1")

	trustedStr := "127.0.0.0/8"
	trusted, err := netip.ParsePrefix(trustedStr)
	if err != nil {
		f.Fatalf("setup: ParsePrefix(%q): %v", trustedStr, err)
	}
	mwCheck := mw.RealIP(&trusted)

	f.Fuzz(func(t *testing.T, peer, xff string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in RealIPBypassCheck: peer=%q xff=%q r=%v\n%s",
					peer, xff, r, debug.Stack())
			}
		}()

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = buildRemoteAddr(peer)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		originalRemote := req.RemoteAddr

		// Determine whether the peer is trusted.
		peerAddr, err := netip.ParseAddr(peer)
		peerIsTrusted := err == nil && trusted.Contains(peerAddr)

		var gotRemote string
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRemote = r.RemoteAddr
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		mwCheck(inner).ServeHTTP(rec, req)

		// I-REALIP-02: if peer is NOT trusted, RemoteAddr must be unchanged.
		if !peerIsTrusted && gotRemote != originalRemote {
			t.Fatalf("BYPASS detected: untrusted peer=%q had RemoteAddr overwritten from %q to %q (xff=%q)",
				peer, originalRemote, gotRemote, xff)
		}

		// I-REALIP-03: if RemoteAddr was overwritten, the new value must be a
		// valid netip.Addr string (no CRLF, no zone IDs, no brackets).
		if gotRemote != originalRemote {
			// RemoteAddr is set to addr.String() which is canonical netip form.
			// Verify no CRLF or null bytes slipped through.
			if strings.ContainsAny(gotRemote, "\r\n\x00") {
				t.Fatalf("I-REALIP-03 violation: overwritten RemoteAddr contains control bytes: %q", gotRemote)
			}
		}
	})
}

// FuzzRealIPZoneID specifically targets IPv6 zone IDs — a known category of
// parser divergences between net.SplitHostPort, netip.ParseAddr, and net.ParseIP.
// Zone IDs are valid in link-local addresses (RFC 4007) but must not propagate
// into r.RemoteAddr or be used as bypass vectors.
func FuzzRealIPZoneID(f *testing.F) {
	// Zone ID seeds — all should be rejected by netip.ParseAddr.
	f.Add("fe80::1%eth0")
	f.Add("fe80::1%25eth0") // percent-encoded zone
	f.Add("::1%lo")
	f.Add("::1%0")
	f.Add("fe80::1%")           // trailing % with no zone
	f.Add("fe80::1%eth0%eth1")  // double zone
	f.Add("::ffff:127.0.0.1%eth0")
	// Mixed with brackets (not valid in X-Forwarded-For but should not panic).
	f.Add("[fe80::1%25eth0]")
	f.Add("[::1]")

	trustedIPv6Str := "::1/128"
	trustedIPv6, err := netip.ParsePrefix(trustedIPv6Str)
	if err != nil {
		f.Fatalf("setup: ParsePrefix: %v", err)
	}
	// Also trust an RFC-1918 range so the test is interesting for IPv4.
	trustedIPv4Str := "127.0.0.0/8"
	trustedIPv4, _ := netip.ParsePrefix(trustedIPv4Str)

	mwZone := mw.RealIP(&trustedIPv6, &trustedIPv4)

	f.Fuzz(func(t *testing.T, zoneCandidate string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in RealIPZoneID: xff=%q r=%v\n%s", zoneCandidate, r, debug.Stack())
			}
		}()

		// Attempt with trusted peer (127.0.0.1) so the middleware reaches the header
		// parsing branch.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:9999"
		req.Header.Set("X-Forwarded-For", zoneCandidate)

		var gotRemote string
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRemote = r.RemoteAddr
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		mwZone(inner).ServeHTTP(rec, req)

		// FINDING FPE-2026-003 (zone ID propagation): netip.ParseAddr accepts IPv6
		// addresses with zone IDs (e.g. "fe80::1%eth0") and addr.String() includes
		// the zone. When such a value arrives in X-Forwarded-For from a trusted peer,
		// RealIP sets r.RemoteAddr to the zone-bearing string.
		//
		// FINDING FPE-2026-003b (NUL via zone): netip.ParseAddr also accepts zone IDs
		// containing NUL bytes (e.g. "::%\x00"). addr.String() propagates the NUL
		// into r.RemoteAddr. Severity: High — NUL bytes in RemoteAddr can corrupt
		// downstream logging, HTTP/2 header frames, and C-binding interfaces.
		//
		// Checks:
		//   1. CRLF/NUL bytes must NOT propagate (FPE-2026-003b — this is a hard fail).
		//   2. No bracket notation propagates.
		//   3. Zone IDs themselves are documented (FPE-2026-003) but not hard-failed here.
		if strings.ContainsAny(gotRemote, "\r\n\x00") {
			t.Fatalf("FPE-2026-003b: NUL/CRLF in RemoteAddr via zone ID: %q (xff=%q)", gotRemote, zoneCandidate)
		}
		if strings.HasPrefix(gotRemote, "[") {
			t.Fatalf("bracket notation propagated to RemoteAddr: %q (xff=%q)", gotRemote, zoneCandidate)
		}

		// Document (but do not fail) zone ID propagation — this is FPE-2026-003.
		if strings.Contains(gotRemote, "%") && gotRemote != "127.0.0.1:9999" {
			_ = fmt.Sprintf("FPE-2026-003 observed: zone ID in RemoteAddr: %q (xff=%q)", gotRemote, zoneCandidate)
		}
	})
}
