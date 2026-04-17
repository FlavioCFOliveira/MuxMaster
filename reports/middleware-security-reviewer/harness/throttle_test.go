// Harness — throttle.go (H-016, H-026).
//
// Threats covered:
//   - H-026: throttle is GLOBAL (not per-IP). One attacker exhausts budget.
//   - H-016: token leak on panic in handler (must recover cleanly).
//   - Counter race under concurrency.
//   - Window behaviour: queue + timeout.
package harness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-TH-001 — Throttle is GLOBAL: one attacker denies service to all (H-026)
//
// ThrottleBacklog(limit=2, backlog=0) means only 2 in-flight requests globally.
// Attacker opens 2 long-running requests → legit clients get 503.
// This test confirms the property and documents the finding.
// -----------------------------------------------------------------------------

func TestSec_Throttle_GlobalNotPerIP(t *testing.T) {
	mw := middleware.ThrottleBacklog(2, 0, 100*time.Millisecond)
	blockCh := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh // block until we unblock
		w.WriteHeader(http.StatusOK)
	})
	h := mw(slow)

	// Attacker fires 2 slow requests — both acquire tokens.
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/a%d", id), nil)
			req.RemoteAddr = "attacker.ip:9999"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("attacker req %d: got %d, want 200", id, rec.Code)
			}
		}(i)
	}

	// Give attackers time to occupy tokens.
	time.Sleep(50 * time.Millisecond)

	// Legit client from DIFFERENT IP should be throttled.
	req := httptest.NewRequest(http.MethodGet, "/legit", nil)
	req.RemoteAddr = "legit.ip:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("legit client: got %d, want 503 (attacker starved budget)", rec.Code)
	} else {
		t.Logf("MSR-TH-001 CONFIRMED: legit client from different IP got 503 because attacker exhausted GLOBAL budget")
	}

	close(blockCh)
	wg.Wait()
}

// -----------------------------------------------------------------------------
// MSR-TH-002 — Token leak on panic in handler (H-016)
// -----------------------------------------------------------------------------

func TestSec_Throttle_TokenReturnedOnPanic(t *testing.T) {
	// Silence stderr to avoid flooding with recoverer's debug.Stack output.
	origErr := os.Stderr
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stderr = devnull
	defer func() { os.Stderr = origErr; _ = devnull.Close() }()

	mw := middleware.ThrottleBacklog(2, 0, 100*time.Millisecond)

	var callCount atomic.Int32
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		panic("boom")
	})
	// Wrap with recoverer so the dispatcher survives.
	recovered := middleware.Recoverer()(panicking)
	h := mw(recovered)

	// Fire 10 requests sequentially; each panics, each recovers; tokens must
	// be returned via defer (inside throttle.go). If tokens leak, after 2
	// requests the channel is empty and all subsequent requests queue/503.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("req %d: got %d, want 500 (panic recovered)", i, rec.Code)
		}
	}
	if callCount.Load() != 10 {
		t.Errorf("expected 10 handler invocations; got %d (tokens leaked?)", callCount.Load())
	}
}

// -----------------------------------------------------------------------------
// MSR-TH-003 — Counter race under concurrency (property test)
// -----------------------------------------------------------------------------

func TestSec_Throttle_ConcurrentLimit(t *testing.T) {
	const limit = 4
	mw := middleware.ThrottleBacklog(limit, 10, 500*time.Millisecond)

	var active atomic.Int32
	var maxActive atomic.Int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	const goroutines = 32
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	if maxActive.Load() > limit {
		t.Errorf("throttle: concurrent active count exceeded limit=%d (got max=%d)", limit, maxActive.Load())
	}
}

// -----------------------------------------------------------------------------
// MSR-TH-004 — Backlog full → 503 (path: queue <- {} fails default branch)
// -----------------------------------------------------------------------------

func TestSec_Throttle_BacklogFullReturns503(t *testing.T) {
	mw := middleware.ThrottleBacklog(1, 1, 200*time.Millisecond)
	blockCh := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh
		w.WriteHeader(http.StatusOK)
	})
	h := mw(slow)

	var wg sync.WaitGroup
	// 1 request occupies the token.
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/r1", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}()
	time.Sleep(30 * time.Millisecond)

	// 1 request occupies the backlog, waiting.
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/r2", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}()
	time.Sleep(30 * time.Millisecond)

	// 1 more request: backlog full → immediate 503.
	req := httptest.NewRequest(http.MethodGet, "/r3", nil)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("throttle backlog full: got %d, want 503", rec.Code)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("throttle backlog full: request should have returned immediately (got %v)", elapsed)
	}

	close(blockCh)
	wg.Wait()
}

// -----------------------------------------------------------------------------
// MSR-TH-005 — Timeout in backlog triggers 503
// -----------------------------------------------------------------------------

func TestSec_Throttle_TimeoutInBacklog(t *testing.T) {
	mw := middleware.ThrottleBacklog(1, 5, 50*time.Millisecond)
	blockCh := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh
		w.WriteHeader(http.StatusOK)
	})
	h := mw(slow)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/r1", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}()
	time.Sleep(10 * time.Millisecond)

	// r2: enters backlog, waits 50ms for token → timeout → 503.
	req := httptest.NewRequest(http.MethodGet, "/r2", nil)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("throttle timeout: got %d, want 503", rec.Code)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("throttle timeout: should have waited ~50ms, got %v", elapsed)
	}

	close(blockCh)
	wg.Wait()
}

// -----------------------------------------------------------------------------
// MSR-TH-006 — Invalid config panics (limit ≤ 0, backlog < 0)
// -----------------------------------------------------------------------------

func TestSec_Throttle_InvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		call    func()
		wantPan bool
	}{
		{"zero_limit", func() { _ = middleware.ThrottleBacklog(0, 10, time.Second) }, true},
		{"negative_limit", func() { _ = middleware.ThrottleBacklog(-1, 10, time.Second) }, true},
		{"negative_backlog", func() { _ = middleware.ThrottleBacklog(1, -1, time.Second) }, true},
		{"zero_backlog_ok", func() { _ = middleware.ThrottleBacklog(1, 0, time.Second) }, false},
		{"zero_timeout_ok", func() { _ = middleware.ThrottleBacklog(1, 1, 0) }, false}, // no validation of timeout=0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				rcv := recover()
				if tc.wantPan && rcv == nil {
					t.Errorf("throttle[%s]: expected panic, got none", tc.name)
				}
				if !tc.wantPan && rcv != nil {
					t.Errorf("throttle[%s]: unexpected panic: %v", tc.name, rcv)
				}
			}()
			tc.call()
		})
	}
}

// Suppress unused.
var _ = context.TODO
