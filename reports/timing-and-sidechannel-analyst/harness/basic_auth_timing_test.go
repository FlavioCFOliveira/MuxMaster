//go:build timing

// basic_auth_timing_test.go — Statistical timing analysis for BasicAuth middleware.
//
// Hypotheses tested:
//  1. valid-password vs invalid-password (constant-time MUST hold)
//  2. existing-user vs non-existing-user (user-enumeration oracle MUST NOT exist)
//
// Methodology: pinned OS thread, GC disabled, 1 M samples, p99 outlier trim,
// Welch t-test + KS 2-sample + Mann-Whitney U.
// Significance threshold: reject constant-time claim if any p < 0.01.
package harness

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nBasicAuth = 200_000 // reduced from 1 M for CI; increase for production audit

func buildBasicAuthHandler(realm string, creds map[string]string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return middleware.BasicAuth(realm, creds)(inner)
}

func basicAuthReq(user, pass string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	return req
}

// TestTiming_BasicAuth_ValidVsInvalid tests that valid and invalid password timing
// is statistically indistinguishable.
func TestTiming_BasicAuth_ValidVsInvalid(t *testing.T) {
	handler := buildBasicAuthHandler("test", map[string]string{
		"alice": "correct-password-for-timing-test",
	})

	var validSamples, invalidSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup
	for i := 0; i < 50_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, basicAuthReq("alice", "correct-password-for-timing-test"))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, basicAuthReq("alice", "wrong-password"))
	}

	validSamples = make([]int64, nBasicAuth)
	invalidSamples = make([]int64, nBasicAuth)

	for i := 0; i < nBasicAuth; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, basicAuthReq("alice", "correct-password-for-timing-test"))
		validSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, basicAuthReq("alice", "wrong-password"))
		invalidSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(validSamples, invalidSamples)
	vs := Summarise(validSamples)
	is := Summarise(invalidSamples)

	t.Logf("BasicAuth valid/invalid timing comparison (N=%d each)", nBasicAuth)
	t.Logf("  Valid:   mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", vs.Mean, vs.Std, vs.P50, vs.P99)
	t.Logf("  Invalid: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", is.Mean, is.Std, is.P50, is.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Errorf("TIMING LEAK DETECTED: valid/invalid password timing is distinguishable "+
			"(Welch p=%.4g, KS p=%.4g, MWU p=%.4g, mean-diff=%.2fns)",
			result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)
	}
}

// TestTiming_BasicAuth_UserExistsVsNotExists tests that existing-user and
// non-existing-user timing is statistically indistinguishable.
func TestTiming_BasicAuth_UserExistsVsNotExists(t *testing.T) {
	handler := buildBasicAuthHandler("test", map[string]string{
		"alice": "password-1234",
	})

	var existsSamples, missSamples []int64

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup
	for i := 0; i < 50_000; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, basicAuthReq("alice", "wrong"))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, basicAuthReq("nonexistent-user", "wrong"))
	}

	existsSamples = make([]int64, nBasicAuth)
	missSamples = make([]int64, nBasicAuth)

	for i := 0; i < nBasicAuth; i++ {
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, basicAuthReq("alice", fmt.Sprintf("wrong-%d", i)))
		existsSamples[i] = time.Since(t0).Nanoseconds()

		w2 := httptest.NewRecorder()
		t1 := time.Now()
		handler.ServeHTTP(w2, basicAuthReq("nonexistent-user", fmt.Sprintf("wrong-%d", i)))
		missSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(existsSamples, missSamples)
	es := Summarise(existsSamples)
	ms := Summarise(missSamples)

	t.Logf("BasicAuth user-exists vs not-exists timing comparison (N=%d each)", nBasicAuth)
	t.Logf("  Exists:   mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", es.Mean, es.Std, es.P50, es.P99)
	t.Logf("  NotExist: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", ms.Mean, ms.Std, ms.P50, ms.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Errorf("TIMING LEAK DETECTED: user-exists vs not-exists is distinguishable "+
			"(Welch p=%.4g, KS p=%.4g, MWU p=%.4g, mean-diff=%.2fns)",
			result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)
	}
}
