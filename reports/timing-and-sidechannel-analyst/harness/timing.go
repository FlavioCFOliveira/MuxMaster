//go:build timing

// Package harness provides timing measurement helpers for side-channel analysis.
// All measurements are taken directly on ServeHTTP — no kernel TCP overhead.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// MeasureHandler measures N calls to handler using the given request builder.
// Returns raw nanosecond samples.
// Caller must have already run warmup iterations.
func MeasureHandler(handler http.Handler, buildReq func() *http.Request, n int) []int64 {
	samples := make([]int64, n)
	for i := 0; i < n; i++ {
		req := buildReq()
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, req)
		samples[i] = time.Since(t0).Nanoseconds()
	}
	return samples
}

// VerifyArmStatus is the mandatory pre-measurement invariant check for every
// TestTiming_* harness in this package (rmp #264 / O-1 / H-RECON-02).
//
// It sends one request, built by buildReq, through handler and fails the test
// immediately via t.Fatalf if the resulting status code does not equal want.
// This guards against silently measuring the wrong code path: the original
// TestTiming_ErrorOracle_404vs405 harness built a mux where BasicAuth was
// registered via the mux-level Use() (which — by design — also wraps the
// shared NotFound/MethodNotAllowed handlers), so BOTH the intended "404" and
// "405" arms actually returned 401. The statistical test still ran and still
// passed, producing a 404-vs-405 timing figure that never measured 404 or 405
// at all. See TSC-2026-0009.
//
// Call this once per arm, BEFORE any warmup or sample loop, with a handler
// and request builder that exercise exactly the same request the measurement
// loop itself will send for that arm (same method, path, headers, body).
// arm is a short label identifying the arm in the failure message.
func VerifyArmStatus(t *testing.T, arm string, handler http.Handler, buildReq func() *http.Request, want int) {
	t.Helper()
	req := buildReq()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("preflight failed for arm %q: want status %d, got %d — "+
			"harness setup does not exercise the intended code path; "+
			"any timing evidence collected from this arm would be invalid",
			arm, want, w.Code)
	}
}

// WithQuietEnv pins the goroutine to the current OS thread, disables GC,
// runs warmup calls, then calls measure(), and finally restores GC.
// This is the recommended wrapper for all timing measurements.
func WithQuietEnv(warmup int, warmupFn func(), measure func()) {
	// Pin to OS thread to reduce scheduler noise.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Disable GC for the measurement window.
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)

	// Force a GC cycle before starting so the heap is clean.
	runtime.GC()
	runtime.GC()

	// Warmup — populates instruction cache and JIT-equivalent CPU state.
	for i := 0; i < warmup; i++ {
		warmupFn()
	}

	// Measurement.
	measure()
}
