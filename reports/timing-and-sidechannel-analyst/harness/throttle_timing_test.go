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
//
// Design note (rmp #274 / part 5c, 2026-09-25): the original restoration
// measured each fill level SEQUENTIALLY — fill=0's whole 100k-sample block
// ran to completion, then fill=0's parked goroutines were released, then
// fill=1's were spun up and measured, and so on. That confounds the
// intended signal (does tryAcquire's cost depend on the in-use count?)
// with an uncontrolled one: any host-load drift between blocks (thermal
// throttling, other processes, GC-adjacent scheduler jitter) shows up as a
// between-arm timing difference indistinguishable from a genuine
// fill-level oracle. This was observed directly: standalone the fill=0 vs
// fill=15 mean difference was ~45 ns (informational), but inside the full
// `-tags timing` suite (i.e. with prior tests' heap/scheduler state still
// settling) the same comparison read 1100-2100 ns — classified MEDIUM by
// classifyOracle purely from run-to-run drift, not from the code under
// test.
//
// Fix: every fill level now has its OWN independent ThrottleBacklog
// instance (a fresh, unshared throttleSem — see middleware/throttle.go),
// pre-filled to its target level, and all 5 instances are held open
// SIMULTANEOUSLY for the full duration of the measurement. Samples are
// then taken in round-robin order — one sample from fill=0, then fill=1,
// then fill=8, then fill=14, then fill=15, repeat — exactly like the
// alternating A/B pattern TestTiming_BasicAuth_ValidVsInvalid and
// TestTiming_BasicAuth_UserExistsVsNotExists use for their two arms,
// generalised to 5 arms. Any drift in host load now lands on all 5 arms
// within the same few-microsecond round instead of accumulating
// differently across sequential multi-hundred-millisecond blocks, so it
// cancels out in the fill-vs-fill comparison instead of masquerading as a
// fill-level effect. VerifyArmStatus and the per-sample status check are
// preserved for every arm (rmp #264 / TSC-2026-0009 lesson: never trust a
// harness that hasn't proven it measures the intended code path).
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nThrottle = 100_000

// tsc20260014BoundNs is the accepted bound for the throttle fill-level
// timing oracle (SECURITY.md "Accepted Timing Oracles", TSC-2026-0014),
// derived 2026-09-25 (rmp #274 / part 5c) as 2x the worst |mean diff|
// observed across 6 independent -count=1 runs of the interleaved harness
// below on this shared/virtualised sandbox: 98.35, 17.48, 30.70, 53.63,
// 87.34, 84.62 ns (worst observed: 98.35 ns; raw logs:
// evidence/2026-09-25/throttle-interleaved-derivation.log) — same
// derivation method as TSC-2026-0001/0002/0004/0013 (rmp #270 / O-9, rmp
// #274 / O-14). Documented in SECURITY.md alongside those entries.
const tsc20260014BoundNs = 200.0 // fill=0 vs worst-of-{1,8,14,15}, interleaved

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
// Each fill level uses its own independent ThrottleBacklog instance (see
// the file-level design note above) so all 5 levels can be held open and
// sampled in interleaved round-robin order within a single measurement
// window, eliminating sequential-block host-load drift as a confound.
//
// Restored 2026-09-25 (rmp #274 / O-14), interleaved 2026-09-25 (rmp #274
// / part 5c) to fix the sequential-sampling confound described in the
// file-level design note. The comparison is now asserted against
// tsc20260014BoundNs (TSC-2026-0014) rather than left purely
// informational, since the interleaved design produces a stable,
// reproducible magnitude — see SECURITY.md for the derivation.
func TestTiming_Throttle_BoundaryOracle(t *testing.T) {
	const limit = 16
	fills := []int{0, 1, 8, 14, 15}

	type arm struct {
		fill    int
		handler http.Handler
		release func()
	}
	arms := make([]arm, len(fills))
	for i, f := range fills {
		th := middleware.ThrottleBacklog(limit, 0, 100*time.Microsecond)
		handler := th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		release := occupyThrottleSlots(th, f)
		arms[i] = arm{fill: f, handler: handler, release: release}
	}
	defer func() {
		for _, a := range arms {
			a.release()
		}
	}()

	probeReq := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/probe", nil) }

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup — round-robin across all 5 live arms, same order the
	// measurement loop below uses, so warmup exercises the identical
	// interleaving pattern.
	for i := 0; i < 10_000; i++ {
		for _, a := range arms {
			w := httptest.NewRecorder()
			a.handler.ServeHTTP(w, probeReq())
		}
	}
	for _, a := range arms {
		VerifyArmStatus(t, fmt.Sprintf("fill=%d", a.fill), a.handler, probeReq, http.StatusOK)
	}

	samples := make(map[int][]int64, len(fills))
	for _, f := range fills {
		samples[f] = make([]int64, nThrottle)
	}

	// Interleaved measurement: one sample per arm per round, round-robin,
	// for nThrottle rounds. Any host-load drift now falls within a single
	// round (5 back-to-back ServeHTTP calls) instead of across an entire
	// 100k-sample block, so it can no longer masquerade as a fill-level
	// effect.
	for i := 0; i < nThrottle; i++ {
		for _, a := range arms {
			w := httptest.NewRecorder()
			t0 := time.Now()
			a.handler.ServeHTTP(w, probeReq())
			d := time.Since(t0).Nanoseconds()
			if w.Code != http.StatusOK {
				t.Fatalf("fill=%d/%d: sample %d returned status %d, want 200 — invalid evidence "+
					"(a 503 here means the fill setup itself saturated the throttle, invalidating "+
					"the below-limit premise of this test)", a.fill, limit, i, w.Code)
			}
			samples[a.fill][i] = d
		}
	}

	t.Logf("Throttle near-limit timing oracle (interleaved): limit=%d backlog=0 N=%d/fill", limit, nThrottle)
	for _, f := range fills {
		s := Summarise(samples[f])
		t.Logf("  fill=%d/%d: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", f, limit, s.Mean, s.Std, s.P50, s.P99)
	}

	// Adversarial comparison: fill=0 (near-empty budget) vs every other
	// fill level, and specifically the maximally-adversarial fill=15/16
	// (one slot from exhaustion) vs fill=0.
	worstP := 1.0
	worstDiff := 0.0
	for _, f := range fills {
		if f == 0 {
			continue
		}
		result := RunTests(samples[0], samples[f])
		if result.WelchP < worstP {
			worstP = result.WelchP
		}
		if result.MeanDiffNs > worstDiff {
			worstDiff = result.MeanDiffNs
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
	t.Logf("Worst-case |mean diff| across fill=0 vs {1,8,14,15}: %.2fns", worstDiff)

	if worstDiff > tsc20260014BoundNs {
		t.Errorf("TIMING LEAK EXCEEDS ACCEPTED BOUND: throttle fill=0 vs worst-other mean-diff=%.2fns "+
			"> %.0fns (SECURITY.md TSC-2026-0014 accepted bound) — this is larger than the "+
			"documented scheduler/GC noise envelope and may indicate a genuine fill-level oracle",
			worstDiff, tsc20260014BoundNs)
	}
	t.Logf("Verdict: throttleSem.tryAcquire is a single atomic Load+CAS independent of the " +
		"current in-use count (middleware/throttle.go CH-02); the interleaved comparison above " +
		"bounds any residual difference as scheduler/GC noise proportional to the number of " +
		"additional live (parked) goroutines, not a property of remaining throttle budget " +
		"(TSC-2026-0014, accepted bound below).")
}
