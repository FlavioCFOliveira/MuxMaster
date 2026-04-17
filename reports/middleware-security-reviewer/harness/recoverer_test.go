// Harness — recoverer.go (H-021).
//
// Threats covered:
//   - CWE-209 information exposure via stack trace in response body.
//   - CWE-532 stack trace to stderr with local-variable content.
//   - CWE-117 ANSI / CRLF in panic message smuggles into stderr.
//   - Nested panics / panic(nil) / panic with complex object.
//   - Panic after WriteHeader — ensures no second WriteHeader crash.
package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// captureStderr runs fn with os.Stderr redirected to a buffer and returns it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// -----------------------------------------------------------------------------
// MSR-RE-001 — Stack trace MUST NOT appear in response body.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_NoStackInResponse(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("DB password: secret123")
	})
	h := mw(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	stderr := captureStderr(t, func() {
		h.ServeHTTP(rec, req)
	})

	body := rec.Body.String()
	// Response body MUST only be the generic 500 text — NO stack, NO panic value.
	if strings.Contains(body, "secret123") {
		t.Errorf("recoverer: panic secret leaked into response body:\n%s", body)
	}
	if strings.Contains(body, "goroutine ") || strings.Contains(body, ".go:") {
		t.Errorf("recoverer: stack trace leaked into response body:\n%s", body)
	}

	// Stderr SHOULD have the stack (it's the observability path).
	if !strings.Contains(stderr, "panic:") {
		t.Errorf("recoverer: stderr missing 'panic:' marker:\n%s", stderr)
	}
	if !strings.Contains(stderr, "goroutine") {
		t.Errorf("recoverer: stderr missing stack:\n%s", stderr)
	}
	// Finding: the secret IS printed on stderr because %v dumps rcv verbatim.
	// This is MSR-RE-002.
	writeFile(t, "recoverer-leak-check.txt", []byte("STDERR CAPTURE\n\n"+stderr))
}

// -----------------------------------------------------------------------------
// MSR-RE-002 — Secret in panic value flows into stderr via %v.
// This is a Medium finding — stderr is often aggregated into log systems
// with weaker ACL than the binary.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_SecretInPanicValue(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("token=SECRET_SHOULD_NOT_BE_LOGGED_abcdef")
	})
	h := mw(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	stderr := captureStderr(t, func() {
		h.ServeHTTP(rec, req)
	})

	if strings.Contains(stderr, "SECRET_SHOULD_NOT_BE_LOGGED_abcdef") {
		// This is the CURRENT behaviour of recoverer.go — confirmed finding.
		t.Logf("MSR-RE-002 CONFIRMED: secret in panic() value flows verbatim to stderr")
	} else {
		t.Errorf("recoverer: expected secret in stderr (confirming current behaviour); got:\n%s", stderr)
	}
}

// -----------------------------------------------------------------------------
// MSR-RE-003 — ANSI / CRLF in panic message smuggles into stderr.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_ANSICRLFInPanicMessage(t *testing.T) {
	mw := middleware.Recoverer()

	payloads := []string{
		"\r\nFAKE LOG ENTRY\r\n",
		"\x1b[2J\x1b[HCLEARED",
		"\x1b[31mred\x1b[0m",
		"\x00null-byte",
	}
	for _, p := range payloads {
		t.Run(truncate(p, 16), func(t *testing.T) {
			panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic(p)
			})
			h := mw(panicking)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			stderr := captureStderr(t, func() {
				h.ServeHTTP(rec, req)
			})

			// Exactly the same failure mode as logger: %v prints raw bytes.
			if strings.Contains(p, "\x1b") && strings.Contains(stderr, "\x1b") {
				t.Logf("MSR-RE-003 CONFIRMED: ANSI escape reached stderr via panic value")
			}
			if strings.Contains(p, "\r\n") && strings.Count(stderr, "\n") > 2 {
				t.Logf("MSR-RE-003 CONFIRMED: CRLF in panic value spreads across lines on stderr")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-RE-004 — panic(nil) is not recovered by Go 1.21+ semantics — must fail safely.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_PanicNil(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil)
	})
	h := mw(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	stderr := captureStderr(t, func() {
		defer func() {
			// Go ≥1.21 re-panics with runtime.PanicNilError, which is recoverable.
			if rcv := recover(); rcv != nil {
				t.Logf("outer recovered from nil panic: %v", rcv)
			}
		}()
		h.ServeHTTP(rec, req)
	})

	// If recoverer handled panic(nil), rec.Code should be 500.
	if rec.Code != http.StatusInternalServerError && rec.Code != 0 {
		t.Logf("recoverer: panic(nil) produced status %d (Go %s-era behaviour)", rec.Code, "1.21+")
	}
	_ = stderr
}

// -----------------------------------------------------------------------------
// MSR-RE-005 — Panic AFTER WriteHeader — w.WriteHeader called twice would panic.
// http.Error after writer started must not cause a second panic.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_PanicAfterWriteHeader(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late panic")
	})
	h := mw(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// Must not crash the test.
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Fatalf("recoverer: late panic re-escaped: %v", rcv)
		}
	}()
	stderr := captureStderr(t, func() {
		h.ServeHTTP(rec, req)
	})
	// Expect: 200 (already written) + stderr has panic. http.Error in recoverer
	// tries to WriteHeader(500) which is ignored.
	if rec.Code != http.StatusOK {
		t.Logf("recoverer: late panic got status %d (header already committed)", rec.Code)
	}
	if !strings.Contains(stderr, "late panic") {
		t.Errorf("recoverer: stderr missing late panic record:\n%s", stderr)
	}
}

// -----------------------------------------------------------------------------
// MSR-RE-006 — Concurrent panics — no shared-state corruption.
// -----------------------------------------------------------------------------

func TestSec_Recoverer_Concurrent(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("concurrent panic")
	})
	h := mw(panicking)

	var wg sync.WaitGroup
	const goroutines = 64
	const perG = 50
	// Swallow stderr — we only care about HTTP code correctness.
	origErr := os.Stderr
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stderr = devnull
	defer func() { os.Stderr = origErr; _ = devnull.Close() }()

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusInternalServerError {
					t.Errorf("recoverer concurrent: got %d, want 500", rec.Code)
				}
			}
		}()
	}
	wg.Wait()
}

// -----------------------------------------------------------------------------
// MSR-RE-007 — Panic with complex structured object — %v may invoke String()
// method that itself panics — should not cascade.
// -----------------------------------------------------------------------------

type evilStringer struct{}

func (evilStringer) String() string {
	panic("String() also panics")
}

func TestSec_Recoverer_EvilStringer(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(evilStringer{})
	})
	h := mw(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	// Ensure no double-panic escape.
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Errorf("recoverer: double-panic escaped: %v", rcv)
		}
	}()
	_ = captureStderr(t, func() {
		h.ServeHTTP(rec, req)
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("recoverer evil-stringer: got %d, want 500", rec.Code)
	}
}

// silence unused import warnings.
var _ = bytes.Buffer{}
