package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP overwrites r.RemoteAddr with the client IP from X-Forwarded-For or
// X-Real-IP. Only mutates RemoteAddr when the direct peer is within one of
// the trusted CIDR prefixes.
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
				candidate := xff
				if i := strings.IndexByte(xff, ','); i >= 0 {
					candidate = xff[:i]
				}
				candidate = strings.TrimSpace(candidate)
				// netip.ParseAddr rejects CRLF, spaces, and garbage.
				if addr, err := netip.ParseAddr(candidate); err == nil {
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
