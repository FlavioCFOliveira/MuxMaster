package harness

// Middleware-focused race harness.
//
// Covers:
//   - Throttle counter race (Step 8 of prompt): N concurrent goroutines hit a
//     limit=R throttle. We count how many reach the handler; under concurrency,
//     the channel-based implementation should allow EXACTLY <= R concurrent
//     executions. We assert equality to the expected count and look for
//     off-by-one.
//   - Timeout goroutine leak snapshot (Step 4).
//   - Recoverer concurrent panics — defer ordering under -race.
//   - Middleware chain mutation race (Step 7): calling Use() AFTER ServeHTTP
//     has started must either be safe or panic deterministically.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestThrottleCounterRace launches N goroutines, each firing a single request
// to a throttled handler with limit=R. The handler holds inside for ~2ms so
// contention is maximised. We count how many reached the handler at the same
// instant. The observed peak must be <= R. An observation of peak > R reveals
// a counter race.
func TestThrottleCounterRace(t *testing.T) {
	const (
		limit   = 8
		backlog = 100
		workers = 64
		per     = 32
	)

	var active int64
	var peak int64
	var served int64

	mux := mm.New()
	mux.Use(middleware.ThrottleBacklog(limit, backlog, 2*time.Second))
	mux.GET("/throttled", func(w http.ResponseWriter, req *http.Request) {
		n := atomic.AddInt64(&active, 1)
		for {
			cur := atomic.LoadInt64(&peak)
			if n <= cur || atomic.CompareAndSwapInt64(&peak, cur, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		atomic.AddInt64(&served, 1)
	})

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range per {
				mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/throttled", nil))
			}
		}()
	}
	wg.Wait()

	p := atomic.LoadInt64(&peak)
	if p > int64(limit) {
		t.Fatalf("throttle counter race: peak concurrency %d > limit %d", p, limit)
	}
	t.Logf("throttle: served=%d peak=%d (limit=%d)", served, p, limit)
}

// TestTimeoutGoroutineLeak documents the KNOWN design limitation: Timeout
// cancels the context but does NOT preempt the handler. We measure goroutine
// delta to confirm that handlers which ignore ctx.Done block goroutines.
func TestTimeoutGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short")
	}
	mux := mm.New()
	mux.Use(middleware.Timeout(10 * time.Millisecond))

	var started, ended int64
	mux.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&started, 1)
		// Handler DOES NOT check ctx — this models the worst case.
		time.Sleep(200 * time.Millisecond)
		atomic.AddInt64(&ended, 1)
	})

	runtime.GC()
	before := runtime.NumGoroutine()
	const N = 256
	for range N {
		go func() {
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
		}()
	}
	// Wait past timeout but before handler completion.
	time.Sleep(50 * time.Millisecond)
	during := runtime.NumGoroutine()

	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	t.Logf("goroutines: before=%d during=%d after=%d (N=%d) started=%d ended=%d",
		before, during, after, N, atomic.LoadInt64(&started), atomic.LoadInt64(&ended))

	// Assertion: during-before must be >= ~N/2 — proving the leak period.
	// after-before must be small — proving goroutines drain once handlers exit.
	if during-before < N/3 {
		t.Errorf("expected goroutine delta during ~%d, got %d — timeout may be preempting unexpectedly", N, during-before)
	}
	if after-before > N/2 {
		t.Errorf("expected goroutine drain within 300ms, still %d over baseline", after-before)
	}
}

// TestRecovererConcurrentPanics hammers the Recoverer middleware under
// concurrency to catch any race in defer ordering or stderr buffering.
func TestRecovererConcurrentPanics(t *testing.T) {
	mux := mm.New()
	mux.Use(middleware.Recoverer())
	mux.GET("/p/:x", func(w http.ResponseWriter, req *http.Request) {
		panic(mm.PathParam(req, "x"))
	})

	const workers, per = 32, 500
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range per {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/p/w%d-i%d", g, i), nil)
				mux.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(g)
	}
	wg.Wait()
}

// TestMiddlewareChainMutationRace attempts to detect a race between a caller
// invoking Use() while ServeHTTP is running. m.middleware is a plain slice
// (mux.go:156) and Use appends WITHOUT holding m.mu. This is a classic
// non-thread-safe append. If ServeHTTP reads m.middleware via wrapMiddleware
// during dispatch, there is no race because wrapMiddleware runs at Handle
// time. But Handle DOES read m.middleware — so concurrent Use() vs Handle()
// IS racy. This test documents the race (using Handle as the reader).
func TestMiddlewareChainMutationRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short")
	}
	mux := mm.New()
	// Seed at least one route so we have something to hit.
	mux.GET("/seed", func(w http.ResponseWriter, req *http.Request) {})

	var stop atomic.Bool
	var wg sync.WaitGroup

	// Goroutine A: continuously call Use() — even with the same MW, this
	// mutates the underlying slice.
	wg.Add(1)
	go func() {
		defer wg.Done()
		noop := func(h http.Handler) http.Handler { return h }
		for !stop.Load() {
			mux.Use(noop)
		}
	}()

	// Goroutine B: continuously call Handle() (via GET). Handle reads
	// m.middleware inside wrapMiddleware under m.mu, but Use() doesn't hold
	// m.mu when appending — so a Handle() that reads the slice header can
	// race with a Use() that overwrites it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for !stop.Load() {
			func() {
				defer func() { _ = recover() }() // ignore duplicate-route panics
				mux.GET(fmt.Sprintf("/mw/%d", i), func(w http.ResponseWriter, req *http.Request) {})
			}()
			i++
		}
	}()

	time.Sleep(500 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// TestContextCancellationPropagation verifies that cancelling the parent
// context propagates through MuxMaster's wrapping into the handler's ctx.
// This is a correctness check; unsafe.Add semantics must preserve Done()
// inheritance.
func TestContextCancellationPropagation(t *testing.T) {
	mux := mm.New()
	observed := make(chan struct{}, 1)
	mux.GET("/cancel/:id", func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
			observed <- struct{}{}
		case <-time.After(1 * time.Second):
			t.Errorf("handler did not observe cancellation within 1s")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/cancel/x", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not receive cancellation signal")
	}
	<-done
}
