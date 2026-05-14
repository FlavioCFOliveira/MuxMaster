package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP overwrites r.RemoteAddr with the client IP derived from the
// X-Forwarded-For or X-Real-IP header. Only mutates RemoteAddr when the
// direct peer is within one of the trusted CIDR prefixes.
//
// XFF selection (MSR-2026-0065): the header is parsed as a comma-separated
// list and walked from RIGHTMOST toward leftmost, skipping entries that lie
// inside any trustedCIDRs. The first entry NOT inside a trusted CIDR is the
// real client IP. This rejects attacker-injected leftmost values: in a
// multi-hop chain (proxy1 + proxy2), if only proxy2 is trusted, the
// leftmost-XFF approach would pick a forged `client_ip` injected by the
// attacker, but the rightmost walk stops at proxy1 (the first untrusted
// hop) and falls back to its address. When the entire chain consists of
// trusted CIDRs, the leftmost entry is used as a last resort. With a single
// trusted proxy stripping inbound XFF (the documented baseline) the
// behaviour is identical to picking the leftmost.
//
// SECURITY (MSR-2026-0055): calling RealIP() with no CIDRs trusts every
// peer — any client can spoof the X-Forwarded-For / X-Real-IP header and
// the router will accept it as the real client IP. This is only safe behind
// a single trusted proxy that strips inbound XFF; in any other deployment
// it is a trivial spoofing primitive that defeats ThrottlePerIP and
// IP-based access controls. The middleware emits a slog.Warn at
// construction time when called without CIDRs so the misconfiguration is
// visible in startup logs. ALWAYS pass the proxy CIDR list explicitly in
// production.
//
// Proxy depth: each additional trusted proxy in the forwarding chain MUST be
// covered by a trustedCIDRs entry, otherwise the rightmost-walk stops too
// early and the proxy IP (rather than the real client) becomes RemoteAddr.
//
// IP values are validated via netip.ParseAddr, which rejects CRLF injection
// and malformed addresses, and IPv6 zone IDs are stripped via WithZone("")
// (FPE-2026-003 / FPE-2026-003b).
func RealIP(trustedCIDRs ...*netip.Prefix) func(http.Handler) http.Handler {
	if len(trustedCIDRs) == 0 {
		slog.Default().Warn("RealIP: called with no trusted CIDRs — every peer can spoof " +
			"X-Forwarded-For / X-Real-IP. This is only safe behind a single trusted " +
			"proxy that strips inbound XFF. See SECURITY.md \"RealIP misconfiguration\".")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedCIDRs) > 0 {
				peer, _, _ := net.SplitHostPort(r.RemoteAddr)
				peerAddr, err := netip.ParseAddr(peer)
				if err != nil {
					next.ServeHTTP(w, r)
					return
				}
				trusted := false
				for _, cidr := range trustedCIDRs {
					if cidr != nil && cidr.Contains(peerAddr) {
						trusted = true
						break
					}
				}
				if !trusted {
					next.ServeHTTP(w, r)
					return
				}
			}

			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if addr, ok := selectXFFRightmost(xff, trustedCIDRs); ok {
					r.RemoteAddr = addr.WithZone("").String()
				}
			} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
				candidate := strings.TrimSpace(xri)
				if addr, err := netip.ParseAddr(candidate); err == nil {
					r.RemoteAddr = addr.WithZone("").String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// selectXFFRightmost walks an X-Forwarded-For value from the rightmost entry
// leftward, skipping any entry whose address lies inside trustedCIDRs. It
// returns the first untrusted address (the real client IP). When the chain
// consists entirely of trusted hops, the leftmost valid address is returned
// as a last resort. When trustedCIDRs is empty the leftmost entry is
// returned (legacy single-proxy semantics).
// maxXFFHops bounds the number of X-Forwarded-For entries we consider.
// RFC 7239 / typical deployments rarely exceed 10 hops; we cap at 30 to
// prevent O(N×M) CPU exhaustion on adversarial XFF headers (DOS-2026-0059,
// TM-2026-021). When the header exceeds this cap, we keep the rightmost
// maxXFFHops entries — the leftmost (attacker-controlled) entries are
// dropped, which is the correct security posture: those entries are the
// least trustworthy in any case.
const maxXFFHops = 30

func selectXFFRightmost(xff string, trustedCIDRs []*netip.Prefix) (netip.Addr, bool) {
	parts := strings.Split(xff, ",")
	if len(parts) > maxXFFHops {
		parts = parts[len(parts)-maxXFFHops:]
	}

	parseAt := func(i int) (netip.Addr, bool) {
		candidate := strings.TrimSpace(parts[i])
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			return netip.Addr{}, false
		}
		return addr, true
	}

	if len(trustedCIDRs) == 0 {
		// No trust list — preserve legacy leftmost behaviour.
		for i := range parts {
			if addr, ok := parseAt(i); ok {
				return addr, true
			}
		}
		return netip.Addr{}, false
	}

	// Walk from rightmost; the first untrusted hop is the real client.
	var lastValid netip.Addr
	var haveLastValid bool
	for i := len(parts) - 1; i >= 0; i-- {
		addr, ok := parseAt(i)
		if !ok {
			continue
		}
		lastValid = addr
		haveLastValid = true
		trusted := false
		for _, cidr := range trustedCIDRs {
			if cidr != nil && cidr.Contains(addr) {
				trusted = true
				break
			}
		}
		if !trusted {
			return addr, true
		}
	}
	// Entire chain trusted — use the leftmost valid address.
	return lastValid, haveLastValid
}
