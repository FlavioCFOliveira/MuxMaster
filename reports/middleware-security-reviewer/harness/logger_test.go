// Harness — logger.go (H-003).
//
// Threats covered:
//   - CWE-117 log injection via raw r.URL.Path (CRLF / ANSI).
//   - CWE-532 credential log leak (Authorization, Cookie, query-string secrets).
//   - Log format parsing ambiguity (unescaped bytes).
//   - Concurrent write safety (io.Writer synchronisation).
package harness

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-LG-001 — Injection corpus (CRLF / ANSI / control chars)
//
// net/http rejects CRLF on the request-line, so requests with raw \r\n in the
// *wire path* won't reach ServeHTTP. But the attack is also possible through:
//   - Percent-encoded bytes that get decoded into r.URL.Path (e.g. %0A).
//   - Direct r.URL.Path mutation by earlier middleware (clean_path, strip_slashes).
// We forge both paths: (1) synthetic *http.Request with control bytes directly
// in r.URL.Path (simulates middleware mutation); (2) URL with percent-decoded
// control bytes (simulates percent-decode).
// -----------------------------------------------------------------------------

func TestSec_Logger_InjectionCorpus(t *testing.T) {
	payloads := map[string]string{
		"raw_lf":         "/admin\nFAKE 2026-01-01T00:00:00Z GET /backdoor 200 1ms",
		"raw_crlf":       "/admin\r\nFAKE 2026-01-01 fake",
		"raw_cr":         "/admin\rFAKE",
		"ansi_clear":     "/admin\x1b[2J\x1b[H",
		"ansi_colour":    "/admin\x1b[31mRED",
		"null_byte":      "/admin\x00trailing",
		"bel":            "/admin\x07",
		"vertical_tab":   "/admin\x0bVT",
		"bom":            "/admin\uFEFFbom",
		"unicode_ls":     "/admin\u2028newlineLikely",
		"unicode_ps":     "/admin\u2029paragraphSep",
		"del":            "/admin\x7fDEL",
		"long_4k":        "/admin" + strings.Repeat("a", 4096),
		"mixed":          "/\r\n2026-01-01T00:00:00Z\tGET\t/fake\t200\t0s\n",
		"crlf_with_auth": "/\r\nAuthorization: Bearer leaked-secret",
	}

	var buf safeBuffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	corpus := bytes.Buffer{}

	for name, p := range payloads {
		t.Run(name, func(t *testing.T) {
			buf.Reset()
			// Use synthesised URL so that the raw bytes enter r.URL.Path.
			// We bypass URL parsing by constructing the URL directly.
			req := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req.URL = &url.URL{Path: p}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			out := buf.String()
			corpus.WriteString("---- " + name + " ----\n")
			corpus.WriteString("payload: " + fmt.Sprintf("%q", p) + "\n")
			corpus.WriteString("log:     " + fmt.Sprintf("%q", out) + "\n")

			// Record findings. The current logger implementation uses
			//   fmt.Fprintf(out, "...%s...", r.URL.Path, ...)
			// which prints raw bytes from the path. Any CR/LF/ANSI/NUL bytes
			// reach the output verbatim. Per the sprint threat model this is
			// MSR-LG-001.
			lines := strings.Count(out, "\n")
			if (strings.Contains(p, "\n") || strings.Contains(p, "\r")) && lines > 1 {
				t.Logf("MSR-LG-001 CONFIRMED: logger[%s] CR/LF in path produced %d lines (expected 1) — log injection\nout=%q",
					name, lines, out)
			}
			if strings.Contains(p, "\x1b") && strings.Contains(out, "\x1b") {
				t.Logf("MSR-LG-001 CONFIRMED: logger[%s] ANSI escape reached log — terminal smuggling\nout=%q",
					name, out)
			}
			if strings.Contains(p, "\x00") && strings.Contains(out, "\x00") {
				t.Logf("MSR-LG-001 CONFIRMED: logger[%s] NUL byte reached log — SIEM ingestion risk\nout=%q",
					name, out)
			}
		})
	}
	writeFile(t, "logger-injection-corpus.txt", corpus.Bytes())
}

// -----------------------------------------------------------------------------
// MSR-LG-002 — Query-string secrets leak via r.URL.Path
//
// logger prints r.URL.Path. Does it also print r.URL.RawQuery? We test that
// query strings (which commonly carry tokens) are NOT leaked by default.
// -----------------------------------------------------------------------------

func TestSec_Logger_QueryStringHandling(t *testing.T) {
	var buf safeBuffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/search?token=SECRET123&user=alice", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := buf.String()
	if strings.Contains(out, "SECRET123") {
		t.Errorf("logger: leaked query-string token into log:\n%s", out)
	}
	if !strings.Contains(out, "/search") {
		t.Errorf("logger: expected /search in log, got:\n%s", out)
	}
}

// -----------------------------------------------------------------------------
// MSR-LG-003 — Authorization / Cookie are NEVER logged (format only has path).
// Regression-lock the format.
// -----------------------------------------------------------------------------

func TestSec_Logger_NoAuthOrCookieLogging(t *testing.T) {
	var buf safeBuffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer SUPER-SECRET-TOKEN")
	req.Header.Set("Cookie", "session=SESSION-COOKIE-VALUE")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := buf.String()
	for _, bad := range []string{"SUPER-SECRET-TOKEN", "SESSION-COOKIE-VALUE", "Bearer ", "Cookie", "Authorization"} {
		if strings.Contains(out, bad) {
			t.Errorf("logger: leaked %q into log:\n%s", bad, out)
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-LG-004 — Concurrent writes — io.Writer must be safe under contention.
// -----------------------------------------------------------------------------

func TestSec_Logger_ConcurrentWrites(t *testing.T) {
	var buf safeBuffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	var wg sync.WaitGroup
	const goroutines = 32
	const perG = 200
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/g%d/i%d", id, i), nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
			}
		}(g)
	}
	wg.Wait()
	// Count lines — must equal total requests with no partial/interleaved.
	out := buf.String()
	lines := strings.Count(out, "\n")
	expected := goroutines * perG
	if lines != expected {
		t.Errorf("logger concurrent: got %d newlines, want %d — partial or interleaved writes", lines, expected)
	}
}

// -----------------------------------------------------------------------------
// MSR-LG-005 — Writer is closed during logging — panic / race handling.
// -----------------------------------------------------------------------------

func TestSec_Logger_WriterError(t *testing.T) {
	errWriter := &errorWriter{}
	mw := middleware.Logger(errWriter)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	// Must not panic even if the writer fails.
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Fatalf("logger panicked on write error: %v", rcv)
		}
	}()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("logger: got %d, want 200", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// Local support types.
// -----------------------------------------------------------------------------

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	// return a copy
	out := make([]byte, b.buf.Len())
	copy(out, b.buf.Bytes())
	return out
}

func (b *safeBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// Enforce io.Writer.
var _ io.Writer = (*safeBuffer)(nil)

type errorWriter struct{}

func (errorWriter) Write(p []byte) (int, error) { return 0, fmt.Errorf("write failed") }
