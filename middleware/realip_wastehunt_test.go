// Regression tests for rmp task #252, sprint 18 (WH-11): RealIP's
// X-Forwarded-For selection now scans from the right with
// strings.LastIndexByte instead of strings.Split, allocating nothing. These
// tests pin the resolved IP to be identical to the pre-existing algorithm
// (reproduced here as an independent oracle, referenceSelectXFFRightmost)
// across a table of edge cases plus a large random corpus, and keep the
// documented rightmost-untrusted security semantics (MSR-2026-0065) intact.
package middleware_test

import (
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// referenceSelectXFFRightmost is the PRE-WH-11 algorithm (strings.Split,
// then walk), kept here purely as an independent equivalence oracle so the
// production implementation is not compared against itself.
func referenceSelectXFFRightmost(xff string, trusted []*netip.Prefix) (netip.Addr, bool) {
	const maxHops = 30
	parts := strings.Split(xff, ",")
	if len(parts) > maxHops {
		parts = parts[len(parts)-maxHops:]
	}
	parseAt := func(i int) (netip.Addr, bool) {
		addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		return addr, err == nil
	}
	if len(trusted) == 0 {
		for i := range parts {
			if addr, ok := parseAt(i); ok {
				return addr, true
			}
		}
		return netip.Addr{}, false
	}
	var lastValid netip.Addr
	var have bool
	for i := len(parts) - 1; i >= 0; i-- {
		addr, ok := parseAt(i)
		if !ok {
			continue
		}
		lastValid, have = addr, true
		trustedHop := false
		for _, c := range trusted {
			if c != nil && c.Contains(addr) {
				trustedHop = true
				break
			}
		}
		if !trustedHop {
			return addr, true
		}
	}
	return lastValid, have
}

func mustPrefixes(t *testing.T, ss []string) []*netip.Prefix {
	t.Helper()
	out := make([]*netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p := netip.MustParsePrefix(s)
		out = append(out, &p)
	}
	return out
}

const realIPProbePeer = "127.0.0.1:1"

// realIPResolve drives the public RealIP middleware and returns the
// resulting RemoteAddr, so the unexported selectXFFRightmost is exercised
// as a black box exactly as production code calls it.
func realIPResolve(trusted []*netip.Prefix, xff string) string {
	var got string
	h := middleware.RealIP(trusted...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = realIPProbePeer
	r.Header.Set("X-Forwarded-For", xff)
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestRealIP_SelectXFFRightmost_TableCases(t *testing.T) {
	trusted := mustPrefixes(t, []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})
	cases := []struct {
		name string
		xff  string
	}{
		{"single", "203.0.113.9"},
		{"three_hops", "198.51.100.23, 203.0.113.9, 10.0.0.7"},
		{"leading_space", " 203.0.113.9"},
		{"trailing_space", "203.0.113.9 "},
		{"internal_spaces", "198.51.100.23 , 203.0.113.9 , 10.0.0.7"},
		{"empty_entries", "198.51.100.23,,203.0.113.9,,10.0.0.7"},
		{"leading_comma", ",203.0.113.9"},
		{"trailing_comma", "203.0.113.9,"},
		{"only_commas", ",,,"},
		{"ipv6", "2001:db8::1, 203.0.113.9"},
		{"ipv6_zone", "fe80::1%eth0, 203.0.113.9"},
		{"ipv6_loopback_trusted_tail", "203.0.113.9, ::1"},
		{"ported_entry_invalid", "203.0.113.9:1234, 10.0.0.7"},
		{"bracketed_ipv6_invalid", "[2001:db8::1]:443, 203.0.113.9"},
		{"garbage_entries", "not-an-ip, 203.0.113.9, also-garbage"},
		{"all_trusted", "10.0.0.1, 172.16.0.1, 192.168.0.1, 127.0.0.1"},
		{"many_hops_exceeds_cap", strings.Repeat("10.0.0.1, ", 40) + "203.0.113.9"},
		{"exactly_maxhops", strings.Repeat("10.0.0.1, ", 29) + "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantAddr, wantOK := referenceSelectXFFRightmost(c.xff, trusted)
			got := realIPResolve(trusted, c.xff)
			if !wantOK {
				if got != realIPProbePeer {
					t.Fatalf("xff=%q: reference found no address but RealIP set RemoteAddr=%q", c.xff, got)
				}
				return
			}
			want := wantAddr.WithZone("").String()
			if got != want {
				t.Fatalf("xff=%q: RealIP set RemoteAddr=%q, want %q (reference)", c.xff, got, want)
			}
		})
	}
}

// TestRealIP_SelectXFFRightmost_FuzzEquivalence compares the production
// RealIP middleware against referenceSelectXFFRightmost over a large random
// corpus, with and without a trust list, covering headers well beyond the
// 30-hop cap.
func TestRealIP_SelectXFFRightmost_FuzzEquivalence(t *testing.T) {
	// The no-trusted-CIDR case (RealIP's documented misconfiguration
	// warning) fires on every construction below; silence it for the
	// duration of this test so 50000 iterations don't flood test output.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(prev)

	rng := rand.New(rand.NewPCG(5, 6))
	atoms := []string{
		"203.0.113.9", "10.0.0.7", "127.0.0.1", "192.168.1.1", "172.16.5.4",
		"2001:db8::1", "fe80::1%eth0", "::1", "garbage", "", " ", "1.2.3", "8.8.8.8",
	}
	var corpus []string
	for range 50000 {
		n := 1 + rng.IntN(40) // crosses the 30-hop bound
		parts := make([]string, n)
		for i := range parts {
			sp := strings.Repeat(" ", rng.IntN(2))
			parts[i] = sp + atoms[rng.IntN(len(atoms))] + sp
		}
		corpus = append(corpus, strings.Join(parts, ","))
	}
	corpus = append(corpus, ",", ",,", "203.0.113.9,", ",203.0.113.9", " 203.0.113.9 , 10.0.0.1 ")

	for _, trustedStrs := range [][]string{
		{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		{},
	} {
		trusted := mustPrefixes(t, trustedStrs)
		for _, xff := range corpus {
			wantAddr, wantOK := referenceSelectXFFRightmost(xff, trusted)
			got := realIPResolve(trusted, xff)
			if !wantOK {
				if got != realIPProbePeer {
					t.Fatalf("trusted=%v xff=%q: reference found nothing but got=%q", trustedStrs, xff, got)
				}
				continue
			}
			want := wantAddr.WithZone("").String()
			if got != want {
				t.Fatalf("trusted=%v xff=%q: got=%q want=%q", trustedStrs, xff, got, want)
			}
		}
	}
}
