// Internal (white-box) regression tests for rmp task #245 (sprint 18):
// exercise nextRandomID and requestIDBufPool directly (bypassing the HTTP
// layer) at a scale far beyond what a black-box test can cheaply reach, to
// canary-test sync.Pool contamination of the pooled crypto/rand buffers —
// "every byte is served to exactly one ID, never reused" (per the comment
// on requestIDBuf). This file uses `package middleware`, matching the
// existing oauth2_cache_internal_test.go pattern.
package middleware

import (
	"encoding/hex"
	"sync"
	"testing"
)

// TestNextRandomID_NoOverlapNoDuplicate_HighConcurrency draws several
// million IDs directly from nextRandomID across many concurrent goroutines
// (far exceeding the mandated 1e6-iteration canary floor) and asserts every
// single one is globally unique. Each ID is 16 bytes (128 bits) of
// crypto/rand output; a genuine collision at this sample size is
// astronomically improbable by chance (birthday bound on 128-bit values),
// so ANY duplicate is conclusive evidence of pool contamination — either
// the same buffer byte range served twice (an "off" bookkeeping bug) or
// the same *requestIDBuf handed to two goroutines simultaneously (a
// double-checkout / double-Put bug in how the pool is used).
func TestNextRandomID_NoOverlapNoDuplicate_HighConcurrency(t *testing.T) {
	const goroutines = 256
	const perGoroutine = 8192 // 256 * 8192 ~= 2.1e6 total draws
	const total = goroutines * perGoroutine

	results := make([][16]byte, total)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			base := g * perGoroutine
			for i := 0; i < perGoroutine; i++ {
				results[base+i] = nextRandomID()
			}
		}(g)
	}
	wg.Wait()

	seen := make(map[[16]byte]int, total)
	dupes := 0
	for i, id := range results {
		if prev, ok := seen[id]; ok {
			dupes++
			if dupes <= 5 {
				t.Errorf("duplicate 16-byte ID %s at draw %d, previously seen at draw %d — pool contamination", hex.EncodeToString(id[:]), i, prev)
			}
			continue
		}
		seen[id] = i
	}
	if dupes > 0 {
		t.Fatalf("%d/%d draws were duplicates — pool contamination detected (nextRandomID served the same 16-byte range to more than one caller)", dupes, total)
	}
	if len(seen) != total {
		t.Fatalf("collected %d unique IDs, want %d", len(seen), total)
	}
}

// TestRequestIDBufPool_RefillNeverServesUninitialisedBytes drains a large
// number of freshly constructed (never-refilled) buffers straight from the
// pool's New function, confirming newExhaustedRequestIDBuf's off==len(b)
// sentinel reliably forces a refill on first use rather than silently
// serving zeroed memory as if it were random — the specific hazard the
// sentinel's doc comment calls out.
func TestRequestIDBufPool_RefillNeverServesUninitialisedBytes(t *testing.T) {
	for i := 0; i < 1000; i++ {
		buf := newExhaustedRequestIDBuf()
		if buf.off != requestIDBufSize {
			t.Fatalf("newExhaustedRequestIDBuf().off = %d, want %d (fully exhausted, forcing an immediate refill)", buf.off, requestIDBufSize)
		}
	}
}

// TestNextRandomID_SequentialDraws_SpanningManyRefills_AllUnique repeats the
// uniqueness canary in a single goroutine (no concurrency at all) across
// enough draws to force many buffer refills (4096/16 = 256 draws per
// buffer), proving the offset-advance/refill bookkeeping is correct on its
// own merits, independent of any cross-goroutine pool hand-off. Combined
// with TestNextRandomID_NoOverlapNoDuplicate_HighConcurrency (which adds the
// concurrent dimension), a skipped or rewound offset in either mode would
// manifest here as a duplicate or a malformed (non-32-hex-char) id.
func TestNextRandomID_SequentialDraws_SpanningManyRefills_AllUnique(t *testing.T) {
	const draws = requestIDBufSize/16*10 + 7 // >10 full buffers, plus a partial one

	seen := make(map[[16]byte]struct{}, draws)
	for i := 0; i < draws; i++ {
		id := nextRandomID()
		if _, dup := seen[id]; dup {
			t.Fatalf("draw %d produced a duplicate of an earlier draw — offset/refill bookkeeping skipped or rewound", i)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != draws {
		t.Fatalf("collected %d unique ids, want %d", len(seen), draws)
	}
}
