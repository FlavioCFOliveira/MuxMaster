package bench

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: RealIP splits the whole X-Forwarded-For header into a fresh
// []string (strings.Split) on every request, although the trusted-CIDR walk
// only ever needs the entries from the RIGHT until the first untrusted one.
// selectXFFRightmostAlt walks the header backwards with strings.LastIndexByte,
// with no allocation, and keeps the maxXFFHops=30 bound and every result
// identical (equiv_test.go compares both on a corpus + random inputs).

const altMaxXFFHops = 30

func selectXFFRightmostAlt(xff string, trusted []*netip.Prefix) (netip.Addr, bool) {
	// Bound to the rightmost altMaxXFFHops comma-separated parts, exactly as
	// parts[len(parts)-maxXFFHops:] does in the current implementation.
	start := 0
	commas := 0
	for i := len(xff) - 1; i >= 0; i-- {
		if xff[i] == ',' {
			commas++
			if commas == altMaxXFFHops {
				start = i + 1
				break
			}
		}
	}
	xff = xff[start:]

	if len(trusted) == 0 {
		// Leftmost valid entry of the (bounded) list.
		rest := xff
		for {
			part, tail, found := strings.Cut(rest, ",")
			if a, err := netip.ParseAddr(strings.TrimSpace(part)); err == nil {
				return a, true
			}
			if !found {
				return netip.Addr{}, false
			}
			rest = tail
		}
	}
	var lastValid netip.Addr
	var haveLastValid bool
	end := len(xff)
	for end >= 0 {
		i := strings.LastIndexByte(xff[:end], ',')
		part := xff[i+1 : end]
		end = i
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err == nil {
			lastValid, haveLastValid = addr, true
			isTrusted := false
			for _, c := range trusted {
				if c != nil && c.Contains(addr) {
					isTrusted = true
					break
				}
			}
			if !isTrusted {
				return addr, true
			}
		}
		if i < 0 {
			break
		}
	}
	return lastValid, haveLastValid
}

func realIPAlt(trusted ...*netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) > 0 {
				peer, _, _ := net.SplitHostPort(r.RemoteAddr)
				peerAddr, err := netip.ParseAddr(peer)
				if err != nil {
					next.ServeHTTP(w, r)
					return
				}
				ok := false
				for _, c := range trusted {
					if c != nil && c.Contains(peerAddr) {
						ok = true
						break
					}
				}
				if !ok {
					next.ServeHTTP(w, r)
					return
				}
			}
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if addr, ok := selectXFFRightmostAlt(xff, trusted); ok {
					r.RemoteAddr = addr.WithZone("").String()
				}
			} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
				if addr, err := netip.ParseAddr(strings.TrimSpace(xri)); err == nil {
					r.RemoteAddr = addr.WithZone("").String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func toPrefixes(ss []string) []*netip.Prefix {
	out := make([]*netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p := netip.MustParsePrefix(s)
		out = append(out, &p)
	}
	return out
}

func restAPITrusted() []*netip.Prefix {
	var out []*netip.Prefix
	for _, s := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		p := netip.MustParsePrefix(s)
		out = append(out, &p)
	}
	return out
}

func BenchmarkRealIP(b *testing.B) {
	trusted := restAPITrusted()
	for _, xff := range []struct{ name, v string }{
		{"1-hop", "203.0.113.9"},
		{"3-hops", "198.51.100.23, 203.0.113.9, 10.0.0.7"},
	} {
		run := func(b *testing.B, h http.Handler) {
			r := realisticRequest(http.MethodGet, "/api/v1/books/1")
			r.Header.Set("X-Forwarded-For", xff.v)
			w := newDiscardRW()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				r.RemoteAddr = "127.0.0.1:54321" // RealIP overwrites it; restore each time
				h.ServeHTTP(w, r)
			}
		}
		b.Run(xff.name+"/current", func(b *testing.B) { run(b, mw.RealIP(trusted...)(nop)) })
		b.Run(xff.name+"/alternative", func(b *testing.B) { run(b, realIPAlt(trusted...)(nop)) })
	}
}
