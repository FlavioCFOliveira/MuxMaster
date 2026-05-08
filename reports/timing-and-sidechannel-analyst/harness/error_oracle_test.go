//go:build timing

// error_oracle_test.go — Error oracle timing analysis.
//
// Tests whether different error conditions produce distinguishable timing:
//
//	404 Not Found vs 405 Method Not Allowed vs 401 Unauthorized vs 200 OK
//
// An error oracle exists when an attacker can determine which error occurred
// by measuring response latency. This leaks:
//   - 404 vs 405: whether the path exists (route enumeration)
//   - 401 vs 404: whether the path exists but requires auth
//   - Timing patterns during 429 throttle: remaining budget
//
// For each scenario we record: status code, response body length, header set,
// and median timing. Distinguishability is tested with all 3 hypothesis tests.
package harness

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nError = 150_000

func buildErrorOracleMux() *muxmaster.Mux {
	r := muxmaster.New()
	r.Use(middleware.BasicAuth("test", map[string]string{"user": "password"}))
	r.GET("/exists", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.POST("/exists", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func measureError(mux *muxmaster.Mux, method, path string, authHeader string, n int) ([]int64, []int) {
	samples := make([]int64, n)
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(method, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		t0 := time.Now()
		mux.ServeHTTP(w, req)
		samples[i] = time.Since(t0).Nanoseconds()
		statuses[i] = w.Code
	}
	return samples, statuses
}

func validAuthHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
}

// TestTiming_ErrorOracle_404vs405 tests whether 404 (no route) and 405
// (route exists, wrong method) timing is distinguishable.
// This is the primary route-existence oracle via error codes.
func TestTiming_ErrorOracle_404vs405(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		req404 := httptest.NewRequest(http.MethodDelete, "/nonexistent", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req404)
		req405 := httptest.NewRequest(http.MethodDelete, "/exists", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req405)
	}

	// DELETE /nonexistent → 404 (no route, no auth check because BasicAuth wraps handler)
	// DELETE /exists → 405 (route exists for GET/POST, not DELETE)
	// NOTE: BasicAuth middleware is applied at registration time, wrapping only the route handlers.
	// For 404/405, the mux returns these before reaching the handler, so BasicAuth does NOT run.
	// This means 404 and 405 are BOTH unauthenticated code paths — good for comparison.
	s404, statuses404 := measureError(mux, http.MethodDelete, "/nonexistent-path", "", nError)
	s405, statuses405 := measureError(mux, http.MethodDelete, "/exists", "", nError)

	// Verify status codes.
	for i, s := range statuses404[:10] {
		if s != 404 {
			t.Logf("iteration %d: expected 404, got %d", i, s)
		}
	}
	for i, s := range statuses405[:10] {
		if s != 405 {
			t.Logf("iteration %d: expected 405, got %d", i, s)
		}
	}

	result := RunTests(s404, s405)
	r4 := Summarise(s404)
	r5 := Summarise(s405)

	t.Logf("Error oracle: 404 vs 405 timing (N=%d each)", nError)
	t.Logf("  404: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  405: mean=%.1fns p50=%.0fns p99=%.0fns", r5.Mean, r5.P50, r5.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("ERROR ORACLE: 404 vs 405 timing is distinguishable — route existence leaks")
		t.Logf("  Effect size: %.2fns — %s", result.MeanDiffNs,
			classifyOracle(result.MeanDiffNs))
	} else {
		t.Logf("404 vs 405 timing: NOT distinguishable — no route-existence oracle via timing")
	}
}

// TestTiming_ErrorOracle_404vs401 tests whether 404 (no route, auth not applied)
// and 401 (route exists, auth rejected) timing is distinguishable.
func TestTiming_ErrorOracle_404vs401(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		req404 := httptest.NewRequest(http.MethodGet, "/no-such-route", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req404)
		req401 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req401)
	}

	// GET /no-such-route → 404, no auth
	// GET /exists (no auth header) → 401, auth runs
	s404, _ := measureError(mux, http.MethodGet, "/no-such-route", "", nError)
	s401, _ := measureError(mux, http.MethodGet, "/exists", "", nError)

	result := RunTests(s404, s401)
	r4 := Summarise(s404)
	r1 := Summarise(s401)

	t.Logf("Error oracle: 404 vs 401 timing (N=%d each)", nError)
	t.Logf("  404: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  401: mean=%.1fns p50=%.0fns p99=%.0fns", r1.Mean, r1.P50, r1.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("ERROR ORACLE (404 vs 401): timing distinguishable — confirms route existence when auth required")
		t.Logf("  Effect size: %.2fns — %s", result.MeanDiffNs, classifyOracle(result.MeanDiffNs))
		t.Logf("  NOTE: 401 includes SHA-256 hash overhead from BasicAuth — expected difference")
	}
}

// TestTiming_ErrorOracle_200vs401 tests authenticated success vs auth failure.
// For BasicAuth specifically, this should be constant-time (already tested in basic_auth_timing_test.go).
func TestTiming_ErrorOracle_200vs401(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	authOK := validAuthHeader()

	for i := 0; i < 30_000; i++ {
		req200 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req200.Header.Set("Authorization", authOK)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req200)
		req401 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req401.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:wrongpass")))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req401)
	}

	s200, _ := measureError(mux, http.MethodGet, "/exists", authOK, nError)
	s401bad := make([]int64, nError)
	badAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:wrongpass"))
	for i := 0; i < nError; i++ {
		req := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req.Header.Set("Authorization", badAuth)
		w := httptest.NewRecorder()
		t0 := time.Now()
		mux.ServeHTTP(w, req)
		s401bad[i] = time.Since(t0).Nanoseconds()
	}

	result := RunTests(s200, s401bad)
	r2 := Summarise(s200)
	r4 := Summarise(s401bad)

	t.Logf("Error oracle: 200 (valid auth) vs 401 (wrong password) timing (N=%d each)", nError)
	t.Logf("  200: mean=%.1fns p50=%.0fns p99=%.0fns", r2.Mean, r2.P50, r2.P99)
	t.Logf("  401: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// NOTE: 200 calls next.ServeHTTP (trivial nopHandler); 401 calls http.Error.
	// The difference here is the handler path length, not auth timing.
	// BasicAuth itself is constant-time (confirmed separately).
}

// TestTiming_ErrorOracle_Panic measures panic recovery overhead.
func TestTiming_ErrorOracle_Panic(t *testing.T) {
	r := muxmaster.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
	r.GET("/clean", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/panic", func(w http.ResponseWriter, req *http.Request) {
		panic("test panic for timing analysis")
	})

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 10_000; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clean", nil))
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/panic", nil))
	}

	const n = 50_000
	cleanSamples := make([]int64, n)
	panicSamples := make([]int64, n)

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/clean", nil)
		w := httptest.NewRecorder()
		t0 := time.Now()
		r.ServeHTTP(w, req)
		cleanSamples[i] = time.Since(t0).Nanoseconds()

		req2 := httptest.NewRequest(http.MethodGet, "/panic", nil)
		w2 := httptest.NewRecorder()
		t1 := time.Now()
		r.ServeHTTP(w2, req2)
		panicSamples[i] = time.Since(t1).Nanoseconds()
	}

	result := RunTests(cleanSamples, panicSamples)
	cs := Summarise(cleanSamples)
	ps := Summarise(panicSamples)

	t.Logf("Panic recovery: clean path vs panic path timing (N=%d each)", n)
	t.Logf("  Clean: mean=%.1fns p50=%.0fns p99=%.0fns", cs.Mean, cs.P50, cs.P99)
	t.Logf("  Panic: mean=%.1fns p50=%.0fns p99=%.0fns", ps.Mean, ps.P50, ps.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns (%.2fµs)",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.MeanDiffNs/1000)

	// Panic path ALWAYS takes longer (runtime.panic overhead + defer + recover ~= 2-10µs).
	// This is a KNOWN, ACCEPTED difference — informational severity.
	if result.Leak {
		t.Logf("Panic path distinguishable from clean path: diff=%.2fµs — INFORMATIONAL",
			result.MeanDiffNs/1000)
	}
}

func classifyOracle(diffNs float64) string {
	switch {
	case diffNs > 10_000:
		return "HIGH — exploitable at LAN with <1000 requests"
	case diffNs > 1_000:
		return "MEDIUM — exploitable at LAN with ~10k requests"
	case diffNs > 200:
		return "LOW — exploitable at LAN with ~100k requests"
	default:
		return "INFORMATIONAL — below practical LAN exploitation threshold"
	}
}
