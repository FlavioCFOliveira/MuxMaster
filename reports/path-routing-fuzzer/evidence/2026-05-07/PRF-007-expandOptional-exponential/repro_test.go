// Reproducer for MM-2026-0050 / PRF-2026-0007:
// expandOptional exponential time complexity (DoS at addRoute).
//
// Before the fix: a pattern with N optional {/:param} segments produced
// 2^N recursive expansion calls (12 segments → ~1.5s; 20 segments →
// minutes). An attacker controlling dynamic route registration could
// stall or hang the server.
//
// After the fix (rmp #3): addRouteInternal counts optional segments at
// the start of registration and panics with an explicit message if the
// count exceeds maxOptionalSegments (8). The panic must arrive in
// well under 10ms regardless of segment count, because the count step
// is O(len(path)) and never recurses.
package prf007repro_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestPRF007_OptionalSegmentCapPanicsFast(t *testing.T) {
	// 12 optional segments — would expand to 4096 routes pre-fix.
	pattern := "/a"
	for i := 0; i < 12; i++ {
		pattern += "{/:p}"
	}
	pattern += "/end"

	r := mm.New()
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		rec := recover()
		if rec == nil {
			t.Fatalf("expected panic on optional-segment overflow; got none")
		}
		msg, _ := rec.(string)
		for _, want := range []string{"optional segments", "exponential", "DoS", "maximum is 8"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic message missing %q: %s", want, msg)
			}
		}
		if elapsed > 10*time.Millisecond {
			t.Errorf("panic took %s; AC requires < 10ms", elapsed)
		}
		t.Logf("PRF-007 fix validated: %d optional segments rejected in %s with: %s", 12, elapsed, msg)
	}()
	r.GET(pattern, func(_ http.ResponseWriter, _ *http.Request) {})
}
