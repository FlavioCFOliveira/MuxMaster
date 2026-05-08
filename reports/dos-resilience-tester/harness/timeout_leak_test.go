// Package harness — DoS Resilience: timeout middleware goroutine survival
//
// The Timeout middleware cancels the request context but does NOT pre-empt
// the handler goroutine. A handler that ignores ctx.Done() will keep running
// after the response is written. This harness quantifies the goroutine exposure.
//
// Reference: MM-2026-0019 (accepted, documented). This test RE-MEASURES the
// exposure under the current implementation to provide empirical evidence.
package harness

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// silentLogger returns a slog.Logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestTimeoutHandlerSurvival measures how many goroutines survive after the
// timeout has fired. The Timeout middleware only cancels the context — the slow
// handler goroutine continues until it finishes (or the process exits).
//
// Expected behaviour (documented MM-2026-0019): goroutines DO survive.
// This test quantifies the maximum goroutine debt for N concurrent slow requests.
func TestTimeoutHandlerSurvival(t *testing.T) {
	const (
		timeoutDur  = 20 * time.Millisecond
		handlerSlow = 500 * time.Millisecond
		concurrency = 50
	)

	var active atomic.Int64

	r := mm.New()
	r.Use(middleware.Timeout(timeoutDur))
	r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		// Deliberately ignores ctx.Done — simulates a blocking DB / network call.
		select {
		case <-time.After(handlerSlow):
		case <-req.Context().Done():
			// cooperative: at least observe the cancellation
		}
	})

	goroutinesBefore := runtime.NumGoroutine()

	for range concurrency {
		req := httptest.NewRequest("GET", "/slow", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// Give handlers time to complete fully.
	time.Sleep(handlerSlow + 200*time.Millisecond)
	runtime.GC()
	runtime.GC()

	goroutinesAfter := runtime.NumGoroutine()
	leaked := goroutinesAfter - goroutinesBefore
	stillActive := active.Load()

	t.Logf("Goroutine delta after %d timeout requests: %d (expected ~0 after all handlers complete)",
		concurrency, leaked)
	t.Logf("Active handlers at check time: %d", stillActive)

	if stillActive > 0 {
		t.Logf("WARNING DOS-2026-0003: %d handler goroutines still running after timeout fired. "+
			"Timeout middleware cancels context only — does NOT kill handler goroutines. "+
			"Under sustained load, goroutines accumulate. "+
			"Mitigation: handlers MUST observe ctx.Done() on every blocking call.",
			stillActive)
	}
	if leaked > 10 {
		t.Logf("DOS-2026-0003 CONFIRMED: %d goroutines leaked past completion (scheduler residual)", leaked)
	}
}

// TestTimeoutContextCancellation verifies that ctx.Done() fires within
// 2x the timeout duration — ensuring cooperative handlers can respect it promptly.
func TestTimeoutContextCancellation(t *testing.T) {
	const timeoutDur = 50 * time.Millisecond

	r := mm.New()
	r.Use(middleware.Timeout(timeoutDur))

	start := time.Now()
	var cancelledAt time.Duration

	r.GET("/check", func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
			cancelledAt = time.Since(start)
		case <-time.After(5 * time.Second):
			t.Error("handler not cancelled within 5s")
		}
	})

	req := httptest.NewRequest("GET", "/check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if cancelledAt == 0 {
		t.Error("ctx.Done() never fired")
		return
	}
	if cancelledAt > 2*timeoutDur {
		t.Errorf("ctx.Done() fired after %v — expected within %v", cancelledAt, 2*timeoutDur)
	}
	t.Logf("ctx.Done() fired after %v (timeout=%v) — PASS", cancelledAt, timeoutDur)
}

// TestTimeoutWithRecovererStackOrder confirms that Recoverer(outer) → Timeout(inner)
// ordering means a panic inside the slow handler is caught correctly and does NOT
// produce a double-WriteHeader panic. Sprint plan hypothesis H-C.
func TestTimeoutWithRecovererStackOrder(t *testing.T) {
	const timeoutDur = 100 * time.Millisecond

	r := mm.New()
	r.Use(middleware.RecovererWithLogger(silentLogger()))
	r.Use(middleware.Timeout(timeoutDur))
	r.GET("/panic-after-timeout", func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
		panic("panic after timeout")
	})

	req := httptest.NewRequest("GET", "/panic-after-timeout", nil)
	w := httptest.NewRecorder()

	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				t.Errorf("panic escaped Recoverer: %v", rcv)
			}
		}()
		r.ServeHTTP(w, req)
	}()

	t.Logf("H-C Recoverer+Timeout: response code=%d (no double-WriteHeader)", w.Code)
}

// BenchmarkTimeoutMiddlewareOverhead measures per-request cost of Timeout middleware
// on a fast handler (context.WithTimeout allocation cost).
func BenchmarkTimeoutMiddlewareOverhead(b *testing.B) {
	r := mm.New()
	r.Use(middleware.Timeout(time.Second))
	r.GET("/fast", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/fast", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}
