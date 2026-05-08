//go:build timing

// api_key_timing_test.go — Statistical timing analysis for APIKey middleware.
//
// Key finding to investigate:
//
//	The APIKey middleware uses sha256.Sum256(raw) as a Go map key ([32]byte).
//	A map lookup on [32]byte is NOT constant-time: Go maps use a hash function
//	that may short-circuit on miss vs hit (different code paths). Furthermore,
//	map miss returns immediately with ok=false while map hit traverses the
//	bucket chain. This is an accepted "statistical" timing difference but not
//	a cryptographic constant-time guarantee.
//
// Hypotheses tested:
//  1. Valid key (hit) vs invalid key (miss) — is the map lookup timing distinguishable?
//  2. Multiple valid keys (different identities) — identity does not affect timing.
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

const nAPIKey = 200_000

func buildAPIKeyHandler(keys map[string]string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return middleware.APIKey(middleware.APIKeyOptions{Keys: keys})(inner)
}

func apiKeyReq(key string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", key)
	return req
}

// TestTiming_APIKey_HitVsMiss tests whether a valid key lookup is
// timing-distinguishable from an invalid key lookup.
// Note: map[K]V in Go is not constant-time; this test documents the observable
// timing difference and quantifies its magnitude.
func TestTiming_APIKey_HitVsMiss(t *testing.T) {
	handler := buildAPIKeyHandler(map[string]string{
		"valid-api-key-for-timing-test": "identity-a",
	})

	var hitSamples, missSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup
	for i := 0; i < 50_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, apiKeyReq("valid-api-key-for-timing-test"))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, apiKeyReq("invalid-key"))
	}

	hitSamples = make([]int64, nAPIKey)
	missSamples = make([]int64, nAPIKey)

	for i := 0; i < nAPIKey; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, apiKeyReq("valid-api-key-for-timing-test"))
		hitSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, apiKeyReq("invalid-key-that-is-not-registered"))
		missSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(hitSamples, missSamples)
	hs := Summarise(hitSamples)
	ms := Summarise(missSamples)

	t.Logf("APIKey hit/miss timing comparison (N=%d each)", nAPIKey)
	t.Logf("  Hit:  mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", hs.Mean, hs.Std, hs.P50, hs.P99)
	t.Logf("  Miss: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", ms.Mean, ms.Std, ms.P50, ms.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("TIMING DIFFERENCE DETECTED (map lookup is not constant-time by design): "+
			"Welch p=%.4g, KS p=%.4g, MWU p=%.4g, mean-diff=%.2fns",
			result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)
		// Not a test failure: map-based lookup is a known, accepted timing side-channel.
		// The question is whether the effect size is exploitable.
		if result.MeanDiffNs > 100 {
			t.Errorf("Effect size too large: mean timing diff %.2fns > 100ns threshold — exploitable oracle", result.MeanDiffNs)
		}
	}
}

// TestTiming_APIKey_EmptyVsPresent tests timing when the X-API-Key header is
// absent vs when it is present but invalid.
func TestTiming_APIKey_EmptyVsPresent(t *testing.T) {
	handler := buildAPIKeyHandler(map[string]string{
		"valid-api-key-for-timing-test": "identity-a",
	})

	var emptySamples, presentSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 20_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, apiKeyReq(""))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, apiKeyReq("invalid"))
	}

	emptySamples = make([]int64, nAPIKey)
	presentSamples = make([]int64, nAPIKey)

	for i := 0; i < nAPIKey; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, apiKeyReq(""))
		emptySamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, apiKeyReq("invalid-not-registered"))
		presentSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(emptySamples, presentSamples)
	es := Summarise(emptySamples)
	ps := Summarise(presentSamples)

	t.Logf("APIKey empty-header vs invalid-key timing (N=%d each)", nAPIKey)
	t.Logf("  Empty:   mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", es.Mean, es.Std, es.P50, es.P99)
	t.Logf("  Present: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", ps.Mean, ps.Std, ps.P50, ps.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// Empty header skips sha256 computation entirely — this WILL show a timing difference.
	// The difference should be attributable to one SHA-256 hash (~70-100ns).
	if result.MeanDiffNs > 500 {
		t.Errorf("Unexpectedly large timing difference for empty vs present key: %.2fns", result.MeanDiffNs)
	}
}
