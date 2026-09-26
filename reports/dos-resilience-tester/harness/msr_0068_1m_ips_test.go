// Package harness — MSR-2026-0068 memory profile under 1,000,000 unique
// client IPs (closed-task audit #285, row #74).
//
// The closed-task audit found that the closest existing evidence
// (s8_dos_test.go::TestThrottlePerIP1MDistinctIPs) used only 100,000
// sequential IPs against ThrottlePerIP (the un-parameterised convenience
// wrapper), not the full 1,000,000 IPs against ThrottlePerIPCapped the
// acceptance criteria asked for. This file closes that gap: a genuine
// 1,000,000-unique-IP run against ThrottlePerIPCapped, recording heap
// in-use before, at its peak, and after, and asserting the heap stays
// bounded proportional to MaxTableSize — not to the 1,000,000 distinct
// keys seen.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestThrottlePerIPCapped1MDistinctIPsMemoryProfile drives 1,000,000
// sequential requests, each from a distinct spoofed client IP, through
// ThrottlePerIPCapped with an explicit MaxTableSize deliberately far
// smaller than the number of distinct IPs (1,000 vs 1,000,000), and
// records heap in-use before, at its sampled peak during the run, and
// after.
//
// The assertion is proportional-to-cap, not proportional-to-N: a single
// throttleEntry (a bounded channel plus a small struct and its map slot)
// costs on the order of a few hundred bytes, so MaxTableSize=1,000 entries
// should cost well under a low single-digit MB even generously bounded.
// If ThrottlePerIPCapped's cap enforcement (or its refs-decrement cleanup,
// MSR-2026-0068's original defect class) ever regressed such that the
// table instead grew with the number of DISTINCT IPs seen rather than the
// number of entries concurrently live, 1,000,000 leaked entries would cost
// tens to hundreds of MB — an order of magnitude the bound below cannot
// miss.
//
// Evidence (heap before/peak/after, in KB) is written to
// reports/dos-resilience-tester/evidence/2026-09-26/ alongside t.Logf, so
// the measurement survives outside `go test -v` output.
func TestThrottlePerIPCapped1MDistinctIPsMemoryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1M IP ThrottlePerIPCapped memory profile in short mode — use -run without -short")
	}

	const numIPs = 1_000_000
	const maxTableSize = 1_000 // deliberately << numIPs

	r := mm.New()
	r.Use(middleware.ThrottlePerIPCapped(100, 10*time.Millisecond, maxTableSize, nil))
	r.GET("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var before runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&before)

	peakHeapInUse := before.HeapInuse
	peakHeapAlloc := before.HeapAlloc

	const sampleEvery = 20_000
	start := time.Now()
	for i := range numIPs {
		req := httptest.NewRequest("GET", "/api", nil)
		req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", (i>>16)&0xff, (i>>8)&0xff, i&0xff)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if i%sampleEvery == 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peakHeapInUse {
				peakHeapInUse = m.HeapInuse
			}
			if m.HeapAlloc > peakHeapAlloc {
				peakHeapAlloc = m.HeapAlloc
			}
		}
	}
	elapsed := time.Since(start)

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapInuse > peakHeapInUse {
		peakHeapInUse = after.HeapInuse
	}
	if after.HeapAlloc > peakHeapAlloc {
		peakHeapAlloc = after.HeapAlloc
	}

	heapDeltaAfter := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	peakDeltaInUse := int64(peakHeapInUse) - int64(before.HeapInuse)
	peakDeltaAlloc := int64(peakHeapAlloc) - int64(before.HeapAlloc)

	// Bounds are proportional to MaxTableSize (1,000 entries), generously
	// padded for allocator/GC bookkeeping noise across a million
	// iterations — NOT proportional to numIPs (1,000,000). A genuine
	// per-key leak proportional to numIPs would blow past these bounds by
	// one to two orders of magnitude.
	const afterBound = 2 * 1024 * 1024   // 2 MB: table must have drained back close to baseline
	const peakBound = 20 * 1024 * 1024   // 20 MB: generous ceiling for the transient peak while churning
	const proportionalBound = 200 * 1024 // 200 KB per live entry, at maxTableSize=1000 that's already the 20MB peakBound

	t.Logf("MSR-2026-0068: ThrottlePerIPCapped(maxTableSize=%d) under %d sequential unique IPs, elapsed=%v",
		maxTableSize, numIPs, elapsed)
	t.Logf("  heap before:      HeapAlloc=%d KB HeapInuse=%d KB", before.HeapAlloc/1024, before.HeapInuse/1024)
	t.Logf("  heap peak:        HeapAlloc=%d KB HeapInuse=%d KB (delta alloc=%d KB, delta inuse=%d KB)",
		peakHeapAlloc/1024, peakHeapInUse/1024, peakDeltaAlloc/1024, peakDeltaInUse/1024)
	t.Logf("  heap after:       HeapAlloc=%d KB HeapInuse=%d KB (delta=%d KB)",
		after.HeapAlloc/1024, after.HeapInuse/1024, heapDeltaAfter/1024)

	failed := false
	if heapDeltaAfter > afterBound {
		t.Errorf("MSR-2026-0068 REGRESSION: heap delta after 1,000,000 unique IPs is %d KB (bound %d KB) — "+
			"table did not drain back to baseline; per-key entries are leaking", heapDeltaAfter/1024, afterBound/1024)
		failed = true
	}
	if peakDeltaInUse > peakBound {
		t.Errorf("MSR-2026-0068 REGRESSION: peak heap-in-use delta is %d KB (bound %d KB, proportional to "+
			"maxTableSize=%d, not to numIPs=%d) — memory grew with the number of distinct IPs seen, not with "+
			"the cap", peakDeltaInUse/1024, peakBound/1024, maxTableSize, numIPs)
		failed = true
	}
	// Cross-check: the proportional-to-cap bound must itself already be
	// far below what 1,000,000 leaked entries would cost (order-of-
	// magnitude separation), otherwise the bound above would not
	// distinguish "bounded by cap" from "bounded by N" at all.
	const perLeakedEntryLowBound = 100 // bytes; conservative floor for a real map entry + channel + struct
	worstCaseLeakBytes := int64(numIPs) * perLeakedEntryLowBound
	if proportionalBound*10 >= worstCaseLeakBytes {
		t.Fatalf("test design error: proportional bound (%d KB) is not separated by an order of magnitude from "+
			"a plausible full-leak size (%d KB) — the assertion above would not distinguish the two",
			proportionalBound/1024, worstCaseLeakBytes/1024)
	}

	verdict := "PASS"
	if failed {
		verdict = "FAIL"
	}
	evidence := fmt.Sprintf(
		"MSR-2026-0068 — ThrottlePerIPCapped memory profile under 1,000,000 unique client IPs\n"+
			"Run date: 2026-09-26 (rmp #285, closed-task audit #74)\n"+
			"MaxTableSize: %d\n"+
			"Distinct client IPs: %d (sequential, one request per IP, no overlap)\n"+
			"Elapsed: %v\n"+
			"\n"+
			"Heap before:  HeapAlloc=%d KB  HeapInuse=%d KB\n"+
			"Heap peak:    HeapAlloc=%d KB  HeapInuse=%d KB  (delta alloc=%d KB, delta inuse=%d KB)\n"+
			"Heap after:   HeapAlloc=%d KB  HeapInuse=%d KB  (delta=%d KB)\n"+
			"\n"+
			"Assertion: heap-after delta <= %d KB; heap-peak-inuse delta <= %d KB.\n"+
			"Both bounds are sized proportional to MaxTableSize (%d entries), not to the 1,000,000\n"+
			"distinct IPs presented — a genuine per-key leak proportional to N would be one to two\n"+
			"orders of magnitude larger than these bounds.\n"+
			"\n"+
			"Verdict: %s\n",
		maxTableSize, numIPs, elapsed,
		before.HeapAlloc/1024, before.HeapInuse/1024,
		peakHeapAlloc/1024, peakHeapInUse/1024, peakDeltaAlloc/1024, peakDeltaInUse/1024,
		after.HeapAlloc/1024, after.HeapInuse/1024, heapDeltaAfter/1024,
		afterBound/1024, peakBound/1024, maxTableSize,
		verdict,
	)

	evidenceDir := filepath.Join("..", "evidence", "2026-09-26")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatalf("failed to create evidence dir %s: %v", evidenceDir, err)
	}
	evidencePath := filepath.Join(evidenceDir, "MSR-2026-0068-throttlepercapped-1m-ips-memprofile.txt")
	if err := os.WriteFile(evidencePath, []byte(evidence), 0o644); err != nil {
		t.Fatalf("failed to write evidence file %s: %v", evidencePath, err)
	}
	t.Logf("evidence written to %s", evidencePath)
}
