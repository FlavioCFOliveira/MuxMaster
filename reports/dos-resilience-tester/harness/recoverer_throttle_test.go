package dosharness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestThrottleTokenNotLeakedOnPanic verifies H-016:
// When a handler panics INSIDE a throttle-wrapped chain, the token MUST be
// returned to the pool (via defer) so subsequent requests aren't starved.
//
// This test stacks: Recoverer (outer) + Throttle(limit=2) + panic handler.
// After panic, the next non-panicking handler MUST acquire the token
// immediately (not after timeout).
func TestThrottleTokenNotLeakedOnPanic(t *testing.T) {
	r := mm.New()

	// Redirect recoverer's stderr to a buffer so test output stays clean.
	origStderr := os.Stderr
	rPipe, wPipe, _ := os.Pipe()
	os.Stderr = wPipe
	t.Cleanup(func() {
		os.Stderr = origStderr
		wPipe.Close()
		_, _ = io.Copy(io.Discard, rPipe)
	})

	r.Use(middleware.Recoverer())
	r.Use(middleware.ThrottleBacklog(2, 0, 1*time.Millisecond))
	var count int
	var mu sync.Mutex
	r.GET("/panic", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		panic("deliberate panic")
	})
	r.GET("/ok", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// 5 requests to /panic. If tokens leak on panic, the 3rd+ onwards
	// block and eventually 503.
	var panicStatuses [5]int
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 5; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/panic", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			panicStatuses[i] = w.Code
		}()
	}
	wg.Wait()
	// All 5 panic requests should have returned (after recoverer returned 500).
	// Next request should succeed immediately if tokens are returned.
	var finalStatuses [3]int
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ok", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		finalStatuses[i] = w.Code
	}

	t.Logf("panic statuses: %v; post-panic statuses: %v; count=%d", panicStatuses, finalStatuses, count)
	for _, s := range finalStatuses {
		if s != http.StatusOK {
			t.Errorf("post-panic request failed with status %d — token leaked on panic", s)
		}
	}
}

// TestRecovererDumpsSensitiveStackToStderr documents H-021:
// recoverer.go writes the panic value + full debug.Stack to os.Stderr.
// This is a security finding in its own right (CWE-209), and it is a DoS
// concern because an attacker that triggers many panics drives high I/O
// volume to stderr — potentially saturating syslog or disk throughput in
// production.
//
// This is not a repro of the DoS — it demonstrates the information leak
// for the middleware-security-reviewer agent to consume.
func TestRecovererStderrLeakInformational(t *testing.T) {
	// Capture stderr.
	origStderr := os.Stderr
	rPipe, wPipe, _ := os.Pipe()
	os.Stderr = wPipe
	defer func() { os.Stderr = origStderr }()

	r := mm.New()
	r.Use(middleware.Recoverer())
	r.GET("/panic", func(w http.ResponseWriter, req *http.Request) {
		// Inject secret-looking data.
		secret := "Bearer sk_live_AAAAAAAAAAAAAAAAAAAAAA"
		panic("ctx: user=" + secret)
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	wPipe.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, rPipe)
	os.Stderr = origStderr

	stderrContents := buf.String()
	if !bytes.Contains([]byte(stderrContents), []byte("Bearer sk_live_AAAAAAAAAAAAAAAAAAAAAA")) {
		t.Errorf("expected recoverer to dump panic value to stderr, got: %q", stderrContents)
	}
	t.Logf("recoverer wrote %d bytes to stderr; confirmed raw panic text present (secret-like pattern leaked)", len(stderrContents))
}
