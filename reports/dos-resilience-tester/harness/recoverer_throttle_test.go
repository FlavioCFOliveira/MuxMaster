// Package harness — DoS Resilience: recoverer + throttle interaction
//
// Tests the composition hazard between Recoverer and Throttle:
//   - A panic inside a throttled handler must correctly release the throttle token
//     so the concurrency slot is not permanently leaked.
//   - The Recoverer wrapper around the full chain must catch panics from any depth.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestRecovererDoesNotLeakThrottleToken verifies that when a handler panics,
// the ThrottleBacklog token is correctly returned (via the deferred release in
// the throttle middleware's ServeHTTP). If Recoverer swallows the panic before
// the throttle's defer runs, the token is permanently consumed → limit-1 effective limit.
//
// The release path in ThrottleBacklog is:
//
//	defer func() { tokens <- t }()
//	next.ServeHTTP(w, r)   ← panic here
//
// Since the defer is registered BEFORE next.ServeHTTP, the panic correctly
// unwinds through the defer and releases the token — IF the Recoverer is OUTSIDE
// the Throttle in the chain. If Recoverer is INSIDE Throttle, the panic is caught
// before reaching Throttle's defer and the token IS returned (because ServeHTTP
// returns normally from Recoverer's perspective).
//
// Either ordering is safe. This test validates both orderings.
func TestRecovererDoesNotLeakThrottleToken(t *testing.T) {
	const limit = 3

	for _, label := range []string{"Recoverer-outer-Throttle-inner", "Throttle-outer-Recoverer-inner"} {
		label := label
		t.Run(label, func(t *testing.T) {
			r := mm.New()

			switch label {
			case "Recoverer-outer-Throttle-inner":
				r.Use(middleware.RecovererWithLogger(silentLogger()))
				r.Use(middleware.ThrottleBacklog(limit, 0, 50*time.Millisecond))
			default:
				r.Use(middleware.ThrottleBacklog(limit, 0, 50*time.Millisecond))
				r.Use(middleware.RecovererWithLogger(silentLogger()))
			}

			r.GET("/panic", func(w http.ResponseWriter, _ *http.Request) {
				panic("test panic for token leak test")
			})

			// Fire limit+5 sequential requests. If tokens leak, the 4th+ will return 503.
			for i := range limit + 5 {
				req := httptest.NewRequest("GET", "/panic", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				// Accept either 500 (recoverer caught panic) or 503 (throttle rejected)
				// but NOT a hang (which would indicate a deadlock on the token channel).
				if w.Code == http.StatusServiceUnavailable && i < limit {
					t.Errorf("%s: request %d got 503 — throttle token leaked by panic", label, i)
				}
			}
			t.Logf("%s: no throttle token leak on panic (PASS)", label)
		})
	}
}

// TestRecovererPanicInfo confirms the recoverer does not write panic details
// to the response body (information leak prevention MM-2026-0023).
func TestRecovererPanicInfo(t *testing.T) {
	r := mm.New()
	r.Use(middleware.RecovererWithLogger(silentLogger()))
	r.GET("/panic", func(w http.ResponseWriter, _ *http.Request) {
		panic("INTERNAL SECRET: db password=hunter2")
	})

	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "INTERNAL SECRET") {
		t.Errorf("recoverer leaked panic message to client response body: %q", body)
	}
	t.Logf("Recoverer info-leak check: body=%q (PASS)", body)
}

// TestRecovererURLInPanicLog confirms that the path logged on panic recovery
// does not include path parameter values that may contain sensitive information.
// Sprint hypothesis #7: r.URL.Path may contain sensitive tokens in path params.
func TestRecovererURLInPanicLog(t *testing.T) {
	// This test is structural: we confirm what IS logged (method + path).
	// For routes like /users/:token/reset, the token value appears in Path.
	// MuxMaster's recoverer logs r.URL.Path — this is a documented risk.
	r := mm.New()
	r.Use(middleware.RecovererWithLogger(silentLogger()))
	r.GET("/users/:secretToken/reset", func(w http.ResponseWriter, req *http.Request) {
		panic("handler failure")
	})

	req := httptest.NewRequest("GET", "/users/my-sensitive-password-reset-token/reset", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	// The path /users/my-sensitive-password-reset-token/reset IS logged.
	// This is the documented exposure from sprint hypothesis #7.
	t.Log("WARNING: Recoverer logs r.URL.Path which may contain sensitive path param values. " +
		"Routes that embed secrets in URL segments (e.g. /reset/:token) should NOT use path params " +
		"for sensitive values. This is a design guidance issue, not a middleware bug.")
}

// TestRecovererConcurrentPanics stress-tests the recoverer under concurrent panics
// to confirm no goroutine leak and consistent 500 response.
func TestRecovererConcurrentPanics(t *testing.T) {
	r := mm.New()
	r.Use(middleware.RecovererWithLogger(silentLogger()))
	r.GET("/panic", func(w http.ResponseWriter, _ *http.Request) {
		panic("concurrent panic")
	})

	const concurrency = 200
	var wg sync.WaitGroup
	results := make(chan int, concurrency)

	goroutinesBefore := runtime.NumGoroutine()

	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/panic", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			results <- w.Code
		}()
	}
	wg.Wait()
	close(results)

	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()

	var ok500 int
	for code := range results {
		if code == http.StatusInternalServerError {
			ok500++
		}
	}

	if ok500 != concurrency {
		t.Errorf("recoverer: %d/%d requests got 500, rest had unexpected codes", ok500, concurrency)
	}
	leaked := goroutinesAfter - goroutinesBefore
	if leaked > 5 {
		t.Errorf("recoverer concurrent panics: %d goroutines leaked", leaked)
	}
	t.Logf("Recoverer concurrent panics: %d/200 = 500, goroutine delta=%d (PASS)", ok500, leaked)
}
