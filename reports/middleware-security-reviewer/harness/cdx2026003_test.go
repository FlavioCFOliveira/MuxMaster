// Package harness_test — CDX-2026-003 (rmp #58): RealIP + ThrottlePerIP
// composite safe-configuration test.
//
// CDX-2026-003 is a COMPOSITE finding (see reports/overview/findings.md and
// SECURITY.md "RealIP + ThrottlePerIP ordering (DOS-2026-0002)"): when
// ThrottlePerIP's keyFn is nil, it keys on r.RemoteAddr, which RealIP only
// rewrites when the direct TCP peer is inside an explicit trusted-CIDR
// list. The rmp #58 acceptance criterion, as originally written, asked for
// two things:
//
//  1. ThrottlePerIP should "detect RealIP's presence" and reject requests
//     with a spoofed IP (401) when RealIP is missing/misconfigured.
//  2. A test proving the safe composition actually rate-limits correctly
//     and resists XFF spoofing.
//
// Per reports/overview/2026-09-26-closed-task-audit.md (#58) and
// reports/overview/findings.md (CDX-2026-003 row: "Accepted
// (construction-time slog.Warn + docs)"), requirement (1) is NOT
// implementable as literal runtime detection: ThrottlePerIP has no
// reliable way to distinguish "RealIP ran and left RemoteAddr as the raw
// peer address because the peer IS the real client" from "RealIP was never
// registered" — both look identical at the point ThrottlePerIP reads
// r.RemoteAddr. There is also no cross-middleware registration-order
// introspection API in MuxMaster (Pre/Use chains are plain
// func(http.Handler) http.Handler values with no identifying metadata).
// The project's accepted mitigation is exactly what throttle.go already
// does: emit a slog.Warn at ThrottlePerIP CONSTRUCTION time whenever keyFn
// is nil, telling the operator to register RealIP (with explicit
// trusted-proxy CIDRs) first — see throttle.go's ThrottlePerIPCapped
// construction warning and SECURITY.md. This test file does NOT add a
// "detect RealIP" mechanism; #58's requirement (1), as literally worded,
// should be recorded as superseded by that construction-time warning plus
// SECURITY.md/README.md documentation, not implemented.
//
// What IS specified, and what this file tests, is requirement (2): the
// SAFE composition (RealIP registered with explicit trusted CIDRs, BEFORE
// ThrottlePerIP) both keys correctly per real client AND is not defeated by
// an X-Forwarded-For spoof attempt from a peer outside the trusted CIDR
// list — because RealIP only trusts XFF/X-Real-IP from a peer inside
// trustedCIDRs (real_ip.go), an attacker who is not that trusted proxy
// cannot pick an arbitrary ThrottlePerIP bucket by lying in XFF: RealIP
// leaves RemoteAddr as the attacker's own (real) peer address, so
// ThrottlePerIP keys on THAT — not on the forged value.
package harness_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestSec_CDX_2026_003_RealIP_Before_ThrottlePerIP_KeysCorrectly pins the
// SPECIFIED safe composition: with RealIP(trustedCIDR) registered before
// ThrottlePerIP, requests arriving THROUGH the trusted proxy with distinct
// X-Forwarded-For client IPs are keyed on those distinct real client IPs —
// not on the shared proxy address — so they do not contend for the same
// per-IP throttle slot.
func TestSec_CDX_2026_003_RealIP_Before_ThrottlePerIP_KeysCorrectly(t *testing.T) {
	trustedProxy := netip.MustParsePrefix("10.0.0.1/32")
	chain := middleware.RealIP(&trustedProxy)(
		middleware.ThrottlePerIP(1, 100*time.Millisecond, nil)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		),
	)

	// Two distinct "real" clients behind the SAME trusted proxy, fired
	// concurrently. If ThrottlePerIP (limit=1) correctly keys on the
	// RealIP-rewritten address, neither should ever see 503 from
	// contending with the OTHER client's slot (each has its own bucket).
	var wg sync.WaitGroup
	codes := make([]int, 2)
	clientIPs := []string{"203.0.113.10", "203.0.113.20"}
	for i, ip := range clientIPs {
		wg.Add(1)
		go func(i int, ip string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.1:8080" // the trusted proxy's own peer address
			req.Header.Set("X-Forwarded-For", ip)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i, ip)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("CDX-2026-003: client %s got status %d, want 200 — distinct RealIP-rewritten "+
				"clients must not contend for the same ThrottlePerIP(limit=1) bucket", clientIPs[i], code)
		}
	}
}

// TestSec_CDX_2026_003_UntrustedPeer_XFFSpoof_DoesNotChangeKey pins the
// other half of the safe composition: an X-Forwarded-For value sent by a
// peer that is NOT in RealIP's trusted-CIDR list must NOT be honoured.
// ThrottlePerIP therefore keys on the peer's OWN (real) address regardless
// of how many different XFF values that same untrusted peer claims — an
// attacker cannot evade the per-IP limit by rotating a spoofed
// X-Forwarded-For header, because RealIP never rewrites RemoteAddr for an
// untrusted peer in the first place (real_ip.go: "if !trusted {
// next.ServeHTTP(w, r); return }").
func TestSec_CDX_2026_003_UntrustedPeer_XFFSpoof_DoesNotChangeKey(t *testing.T) {
	// Only 10.0.0.0/8 is trusted; the attacker's peer address (9.9.9.9) is
	// deliberately outside that range — it never satisfies RealIP's
	// trusted-CIDR check.
	trustedProxy := netip.MustParsePrefix("10.0.0.0/8")

	var mu sync.Mutex
	var observedKeys []string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observedKeys = append(observedKeys, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// keyFn=nil: ThrottlePerIP keys on r.RemoteAddr, exactly the
	// documented default composition.
	chain := middleware.RealIP(&trustedProxy)(
		middleware.ThrottlePerIP(100, 100*time.Millisecond, nil)(inner),
	)

	spoofedIPs := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	for _, spoof := range spoofedIPs {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "9.9.9.9:4444" // untrusted peer — outside 10.0.0.0/8
		req.Header.Set("X-Forwarded-For", spoof)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request with spoofed XFF=%q from untrusted peer: got status %d, want 200", spoof, rec.Code)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observedKeys) != len(spoofedIPs) {
		t.Fatalf("handler observed %d requests, want %d", len(observedKeys), len(spoofedIPs))
	}
	for i, key := range observedKeys {
		if key != "9.9.9.9:4444" {
			t.Errorf("CDX-2026-003: request %d (spoofed XFF=%q from untrusted peer 9.9.9.9) resulted in "+
				"RemoteAddr=%q — RealIP must NOT honour X-Forwarded-For from an untrusted peer, and "+
				"ThrottlePerIP's key must therefore stay pinned to the real peer address",
				i, spoofedIPs[i], key)
		}
	}

	// Now demonstrate the actual security property end-to-end: because
	// every spoofed request above keyed on the SAME real peer address
	// (9.9.9.9:4444), they all share ONE ThrottlePerIP bucket — rotating
	// the spoofed XFF value does not grant the attacker additional
	// buckets. With limit=1 and one slot already held, a concurrent
	// request from the SAME untrusted peer (with yet another spoofed XFF)
	// must be throttled (503), proving the spoof cannot be used to evade
	// the limit by claiming a "new" identity per request.
	release := make(chan struct{})
	started := make(chan struct{})
	holder := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	limitedChain := middleware.RealIP(&trustedProxy)(
		middleware.ThrottlePerIP(1, 200*time.Millisecond, nil)(holder),
	)

	var wg sync.WaitGroup
	var holderCode, spoofedCode int
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "9.9.9.9:4444"
		req.Header.Set("X-Forwarded-For", "1.1.1.1")
		rec := httptest.NewRecorder()
		limitedChain.ServeHTTP(rec, req)
		holderCode = rec.Code
	}()
	<-started // the first request now holds the single slot

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "9.9.9.9:4444"
	req2.Header.Set("X-Forwarded-For", "8.8.8.8") // a DIFFERENT spoofed identity
	rec2 := httptest.NewRecorder()
	limitedChain.ServeHTTP(rec2, req2)
	spoofedCode = rec2.Code

	close(release)
	wg.Wait()

	if holderCode != http.StatusOK {
		t.Errorf("holder request: got status %d, want 200", holderCode)
	}
	if spoofedCode != http.StatusServiceUnavailable {
		t.Errorf("CDX-2026-003: second request from the SAME untrusted peer with a DIFFERENT spoofed "+
			"X-Forwarded-For got status %d, want 503 — rotating the spoofed XFF value must not grant a "+
			"fresh ThrottlePerIP bucket", spoofedCode)
	}
}
