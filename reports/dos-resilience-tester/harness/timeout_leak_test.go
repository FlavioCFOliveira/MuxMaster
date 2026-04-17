package dosharness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestTimeoutMiddlewareGoroutineLeak validates H-017.
// The timeout middleware only cancels the context — it does NOT kill the
// handler goroutine. If the handler does not check ctx.Done(), it runs to
// completion while the dispatcher is blocked inside ServeHTTP.
//
// We fire N concurrent requests whose handler sleeps 10 * timeoutDuration.
// Before sending, we capture runtime.NumGoroutine(). We fire everything in
// parallel, then wait for handlers to finish, force GC, and measure again.
//
// The leak is NOT in the Go runtime's sense (goroutines return eventually),
// but in the *latency* sense: each in-flight request holds a goroutine for
// the full handler duration, no matter how short the timeout.
func TestTimeoutMiddlewareGoroutineLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running; skipped in -short")
	}

	const (
		nRequests       = 1000
		timeout         = 10 * time.Millisecond
		handlerDuration = 3 * time.Second
	)

	r := mm.New()
	r.Use(middleware.Timeout(timeout))
	var handlersStarted sync.WaitGroup
	handlersStarted.Add(nRequests)
	var handlersDone sync.WaitGroup
	handlersDone.Add(nRequests)
	r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
		handlersStarted.Done()
		// Does NOT check req.Context().Done() — simulates naive handler.
		time.Sleep(handlerDuration)
		handlersDone.Done()
		// Writing to w after ctx timeout may error but we ignore.
		_, _ = w.Write([]byte("late"))
	})

	runtime.GC()
	runtime.GC()
	before := runtime.NumGoroutine()
	t.Logf("goroutines before dispatch: %d", before)

	var startWG sync.WaitGroup
	startWG.Add(nRequests)
	for i := 0; i < nRequests; i++ {
		go func() {
			defer startWG.Done()
			req := httptest.NewRequest(http.MethodGet, "/slow", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}()
	}

	// Wait for all handlers to start.
	handlersStarted.Wait()
	// Give the timeout a chance to fire (timeout << handlerDuration).
	time.Sleep(timeout * 5)

	mid := runtime.NumGoroutine()
	t.Logf("goroutines mid-flight (after timeouts should have fired): %d", mid)

	// The leak proof: after timeouts have fired, the goroutines are still
	// stuck inside the handler sleep. We expect >= nRequests extra goroutines.
	if mid-before < nRequests/2 {
		t.Errorf("expected >= %d goroutines stuck in handler after timeout, got delta=%d", nRequests/2, mid-before)
	}

	// Clean up.
	handlersDone.Wait()
	startWG.Wait()
	runtime.GC()
	runtime.GC()
	after := runtime.NumGoroutine()
	t.Logf("goroutines after handlers finish: %d", after)
}

// TestTimeoutLeakCountExact is a smaller, faster version that pins an
// exact goroutine count during the leak window.
func TestTimeoutLeakCountExact(t *testing.T) {
	const (
		n               = 200
		timeout         = 5 * time.Millisecond
		handlerDuration = 500 * time.Millisecond
	)

	r := mm.New()
	r.Use(middleware.Timeout(timeout))
	handlerEnter := make(chan struct{}, n)
	r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
		handlerEnter <- struct{}{}
		time.Sleep(handlerDuration)
	})

	runtime.GC()
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < n; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/slow", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}()
	}

	// Wait for all n handlers to have entered.
	for i := 0; i < n; i++ {
		<-handlerEnter
	}
	// Timeout has already fired (5ms << 500ms).
	time.Sleep(10 * time.Millisecond)
	mid := runtime.NumGoroutine()

	leaked := mid - before
	t.Logf("After all %d handlers entered + timeout fired: %d goroutines (leaked approx %d)", n, mid, leaked)
	if leaked < n {
		t.Errorf("expected at least %d goroutines alive (n handler goroutines), got leaked=%d", n, leaked)
	}

	// Wait for cleanup.
	time.Sleep(handlerDuration + 100*time.Millisecond)
	runtime.GC()
	runtime.GC()
	after := runtime.NumGoroutine()
	t.Logf("final goroutines: %d (delta from before: %d)", after, after-before)
}
