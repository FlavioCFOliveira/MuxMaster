//go:build timing

// Package harness provides timing measurement helpers for side-channel analysis.
// All measurements are taken directly on ServeHTTP — no kernel TCP overhead.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
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
