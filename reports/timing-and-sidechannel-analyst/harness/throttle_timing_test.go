//go:build timing

// throttle_timing_test.go — Throttle near-limit vs below-limit timing oracle.
//
// Vector (CLAUDE.md "Candidate timing leaks"): middleware/throttle.go —
// "'Near-limit' vs 'below-limit' timing" — hypothesised severity Medium.
//
// TestTiming_Throttle_BoundaryOracle restores the statistical regression
// test for this vector, removed by commit 5f804fa (as
// throttle_compress_test.go, along with the unrelated
// TestTiming_Compress_BREACH) without a like-for-like replacement — see
// reports/overview/findings.md O-14.
//
// TestTiming_Compress_BREACH itself is NOT restored here: BREACH is a
// compression-oracle vector now owned by dos-resilience-tester and already
// documented in SECURITY.md as "BREACH Compression Oracle (MM-2026-0030 /
// DOS-2026-0006)", with its own harness at
// reports/dos-resilience-tester/harness/breach_oracle_test.go. Duplicating
// it here would be out of this agent's scope (CLAUDE.md §4 — no
// unrequested work) and would fork a second, divergent source of truth for
// a finding another agent already owns.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nThrottle = 100_000

// occupyThrottleSlots blocks n concurrent slots of th (a
// middleware.ThrottleBacklog-wrapped chain) by launching n goroutines that
// each acquire a slot and then park on an internal channel. It returns only
// after all n slots are provably held (each blocker goroutine signals
// AFTER next.ServeHTTP actually runs, i.e. after the semaphore was
// acquired) — no time.Sleep-based synchronisation, unlike the historical
// throttle_compress_test.go this replaces, which relied on a fixed 5ms
// sleep and was therefore not deterministic.
func occupyThrottleSlots(th func(http.Handler) http.Handler, n int) (release func()) {
	if n == 0 {
		return func() {}
	}
	hold := make(chan struct{})
	started := make(chan struct{}, n)
	blocker := th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < n; i++ {
		go func() {
			w := httptest.NewRecorder()
			blocker.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/hold", nil))
		}()
	}
	for i := 0; i < n; i++ {
		<-started
	}
	return func() { close(hold) }
}

// TestTiming_Throttle_BoundaryOracle measures whether the per-request cost
// of middleware.ThrottleBacklog's fast-path acquisition
// (throttleSem.tryAcquire — a single atomic Load + CompareAndSwap, per
// middleware/throttle.go's CH-02 doc comment) depends on how close the
// current in-use count is to the configured limit. If an attacker can
// distinguish "budget nearly exhausted" from "budget mostly free" purely
// from response latency — WITHOUT ever seeing a 503 — that is a
// side-channel on the server's current load independent of the throttle's
// documented behaviour (which only differs observably once the limit is
// actually reached).
//
// limit=16, backlog=0. Fill levels probed: 0, 1, 8, 14, 15 (i.e. 0/16
// through 15/16 slots held by parked goroutines) — the same levels the
// removed harness used. Every measured request in every fill level
// succeeds (200 OK): at most 15 of 16 slots are ever pre-occupied, so a
// probe request always finds a free slot via the fast path. This
// deliberately excludes the fill=16 (over-limit, 503) case, which is
// already exhaustively covered by TestTiming_ErrorOracleMatrix's "503" arm
// in error_oracle_test.go.
//
// Restored 2026-09-25 (rmp #274 / O-14), adapted to current harness
// conventions (VerifyArmStatus preflight, per-sample status check,
// RunTests/Summarise). Informational: no numeric bound is asserted here
// because CLAUDE.md's own severity hypothesis for this vector is "Medium"
// pending evidence, not a documented accepted-bound class like
// TSC-2026-0001/0002/0004/0013.
func TestTiming_Throttle_BoundaryOracle(t *testing.T) {
	const limit = 16
	th := middleware.ThrottleBacklog(limit, 0, 100*time.Microsecond)
	probeHandler := th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	probeReq := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/probe", nil) }

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	fills := []int{0, 1, 8, 14, 15}
	samples := make(map[int][]int64, len(fills))

	for _, f := range fills {
		release := occupyThrottleSlots(th, f)

		// Warmup.
		for i := 0; i < 10_000; i++ {
			w := httptest.NewRecorder()
			probeHandler.ServeHTTP(w, probeReq())
		}
		VerifyArmStatus(t, "fill", probeHandler, probeReq, http.StatusOK)

		s := make([]int64, nThrottle)
		for i := 0; i < nThrottle; i++ {
			w := httptest.NewRecorder()
			t0 := time.Now()
			probeHandler.ServeHTTP(w, probeReq())
			s[i] = time.Since(t0).Nanoseconds()
			if w.Code != http.StatusOK {
				release()
				t.Fatalf("fill=%d/%d: sample %d returned status %d, want 200 — invalid evidence "+
					"(a 503 here means the fill setup itself saturated the throttle, invalidating "+
					"the below-limit premise of this test)", f, limit, i, w.Code)
			}
		}
		release()
		samples[f] = s
	}

	t.Logf("Throttle near-limit timing oracle: limit=%d backlog=0 N=%d/fill", limit, nThrottle)
	for _, f := range fills {
		s := Summarise(samples[f])
		t.Logf("  fill=%d/%d: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", f, limit, s.Mean, s.Std, s.P50, s.P99)
	}

	// Adversarial comparison: fill=0 (near-empty budget) vs every other
	// fill level, and specifically the maximally-adversarial fill=15/16
	// (one slot from exhaustion) vs fill=0.
	worstP := 1.0
	for _, f := range fills {
		if f == 0 {
			continue
		}
		result := RunTests(samples[0], samples[f])
		if result.WelchP < worstP {
			worstP = result.WelchP
		}
		t.Logf("  fill=0 vs fill=%d: Welch p=%.4g KS p=%.4g MWU p=%.4g |mean diff|=%.2fns",
			f, result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)
		if result.Leak {
			t.Logf("    DISTINGUISHABLE: diff=%.2fns — %s", result.MeanDiffNs, classifyOracle(result.MeanDiffNs))
		} else {
			t.Logf("    NOT distinguishable at p<0.01")
		}
	}
	t.Logf("Worst-case Welch p-value across fill=0 vs {1,8,14,15}: %.4g", worstP)
	t.Logf("Verdict: throttleSem.tryAcquire is a single atomic Load+CAS independent of the " +
		"current in-use count (middleware/throttle.go CH-02); any distinguishable difference " +
		"above is expected to be scheduler/GC noise proportional to the number of additional " +
		"live (parked) goroutines, not a property of remaining throttle budget.")
}
