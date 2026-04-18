package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP overwrites r.RemoteAddr with the client IP from X-Forwarded-For or
// X-Real-IP. Only mutates RemoteAddr when the direct peer is within one of
// the trusted CIDR prefixes. Call with no arguments to trust all peers
// (backward-compatible but insecure — only use behind a known single proxy).
//
// IP values are validated via netip.ParseAddr, which rejects CRLF injection
// and malformed addresses.
func RealIP(trustedCIDRs ...*netip.Prefix) func(http.Handler) http.Handler {
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
					r.RemoteAddr = addr.String()
				}
			} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
				candidate := strings.TrimSpace(xri)
				if addr, err := netip.ParseAddr(candidate); err == nil {
					r.RemoteAddr = addr.String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
