package middleware_test

// Minimal repro for FPE-2026-003b:
// NUL byte propagation via IPv6 zone ID in X-Forwarded-For.
//
// Root cause: netip.ParseAddr("::%\x00") succeeds — the zone ID "\x00" is
// accepted. addr.String() returns "::%\x00". RealIP then sets
// r.RemoteAddr = addr.String(), propagating the NUL byte downstream.
//
// Severity: High
// Impact: NUL bytes in r.RemoteAddr corrupt logging, HTTP/2 header frames
//         (forbidden by RFC 9113 §8.2), and any C/cgo binding that
//         interprets RemoteAddr as a C string.
// Fix: strip zone ID from parsed addr before storing — use addr.WithZone("").
//
// Recommended patch for real_ip.go line ~47:
//   if addr, err := netip.ParseAddr(candidate); err == nil {
//       r.RemoteAddr = addr.WithZone("").String()  // strip zone
//   }

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func TestFPE2026003b_NULViaZoneID(t *testing.T) {
	trusted, _ := netip.ParsePrefix("127.0.0.0/8")
	middleware := mw.RealIP(&trusted)

	var gotRemote string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRemote = r.RemoteAddr
		w.WriteHeader(200)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234" // trusted peer
	req.Header.Set("X-Forwarded-For", "::%\x00")

	rec := httptest.NewRecorder()
	middleware(inner).ServeHTTP(rec, req)

	// Before fix: gotRemote == "::%\x00" (NUL propagated)
	// After fix:  gotRemote == "127.0.0.1:1234" (rejected) or "::" (zone stripped)
	if strings.ContainsAny(gotRemote, "\x00\r\n") {
		t.Fatalf("FPE-2026-003b CONFIRMED: NUL in r.RemoteAddr=%q", gotRemote)
	}
}
