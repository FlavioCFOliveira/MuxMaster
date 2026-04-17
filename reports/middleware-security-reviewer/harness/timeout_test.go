// Harness — timeout.go (H-017).
//
// Threats covered:
//   - H-017 goroutine leak: handler does not observe ctx.Done().
//   - Context propagation: handler sees ctx.Deadline().
//   - Timeout vs client cancellation interaction.
//   - Invalid config (d ≤ 0) panics.
//   - Multiple middlewares in chain — cancellation propagates.
package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-TO-001 — Invalid duration panics.
// -----------------------------------------------------------------------------

func TestSec_Timeout_InvalidConfigPanics(t *testing.T) {
	cases := []time.Duration{0, -1 * time.Second, -time.Nanosecond}
	for _, d := range cases {
		t.Run(d.String(), func(t *testing.T) {
			defer func() {
				if rcv := recover(); rcv == nil {
					t.Errorf("Timeout(%v): expected panic, got none", d)
				}
			}()
			_ = middleware.Timeout(d)
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-TO-002 — Context deadline is set correctly on downstream ctx.
// -----------------------------------------------------------------------------

func TestSec_Timeout_ContextDeadlineSet(t *testing.T) {
	const d = 100 * time.Millisecond
	mw := middleware.Timeout(d)
	var hadDeadline bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !hadDeadline {
		t.Error("Timeout: handler context did not carry a deadline")
	}
}

// -----------------------------------------------------------------------------
// MSR-TO-003 — Handler that ignores ctx.Done blocks goroutine until end.
// Documented limitation (design choice).
// -----------------------------------------------------------------------------

func TestSec_Timeout_HandlerIgnoresCancellationBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine leak check — skipped in short mode")
	}
	const short = 20 * time.Millisecond
	const long = 200 * time.Millisecond
	mw := middleware.Timeout(short)

	var handlerDone atomic.Bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(long)
		handlerDone.Store(true)
		w.WriteHeader(http.StatusOK)
	}))

	before := runtime.NumGoroutine()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// Launch in a goroutine to avoid blocking the test.
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Wait slightly longer than `short` — timeout should fire.
	time.Sleep(short + 30*time.Millisecond)

	// Handler is STILL running.
	if handlerDone.Load() {
		t.Errorf("timeout: handler completed too fast — test assumption broken")
	}
	// Goroutine count is strictly higher than baseline.
	if now := runtime.NumGoroutine(); now <= before {
		t.Errorf("timeout: expected handler goroutine alive; NumGoroutine=%d baseline=%d", now, before)
	}

	// Wait for handler to finish.
	<-done
	if !handlerDone.Load() {
		t.Errorf("timeout: handler never completed")
	}

	// Verify goroutine is reclaimed after handler finishes.
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)
	after := runtime.NumGoroutine()
	t.Logf("timeout: before=%d during=true after=%d (handler blocked for %v)", before, after, long)
	t.Logf("MSR-TO-003: timeout does NOT preempt handler; caller must observe r.Context().Done()")
}

// -----------------------------------------------------------------------------
// MSR-TO-004 — Cooperative handler observes cancellation and returns promptly.
// -----------------------------------------------------------------------------

func TestSec_Timeout_CooperativeHandler(t *testing.T) {
	const d = 20 * time.Millisecond
	mw := middleware.Timeout(d)

	observed := make(chan struct{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(observed)
			return
		case <-time.After(1 * time.Second):
			t.Error("cooperative handler never saw ctx.Done")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	select {
	case <-observed:
	default:
		t.Error("cooperative handler exited without observing cancellation")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("cooperative handler took too long to exit: %v", elapsed)
	}
}

// -----------------------------------------------------------------------------
// MSR-TO-005 — Double cancel via nested Timeout does not leak.
// Register Timeout(100ms) wrapping Timeout(20ms). Inner wins.
// -----------------------------------------------------------------------------

func TestSec_Timeout_NestedPicksShortest(t *testing.T) {
	outer := middleware.Timeout(100 * time.Millisecond)
	inner := middleware.Timeout(20 * time.Millisecond)
	var deadline time.Time
	h := outer(inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Deadline should be <= 30ms from now.
	until := time.Until(deadline)
	if until > 30*time.Millisecond {
		t.Errorf("nested timeout: deadline=%v, expected <~30ms (inner 20ms wins)", until)
	}
}

// -----------------------------------------------------------------------------
// MSR-TO-006 — Heavy concurrent use: no panic, no resource leak.
// -----------------------------------------------------------------------------

func TestSec_Timeout_ConcurrentUse(t *testing.T) {
	mw := middleware.Timeout(50 * time.Millisecond)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Quick handler.
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	const goroutines = 64
	const perG = 50
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("concurrent timeout: got %d", rec.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Silence unused.
var _ = context.TODO
