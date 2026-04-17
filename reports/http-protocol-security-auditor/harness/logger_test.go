package harness

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestLoggerInjection exercises H-003: the default Logger middleware
// formats r.URL.Path with %s — so whatever bytes end up in Path are
// written to the out stream. We want to know:
//   - Does stdlib strip/reject CR/LF on the request target before Path?
//   - Are ANSI escape sequences accepted (RFC 3986 reserved set does not
//     forbid them; they pass through)?
//   - Is NUL allowed?
//   - What about UTF-8 / bidi overrides?
//
// Key empirical finding from harness/url_path_surface_test.go:
// stdlib REJECTS literal control bytes at the request line (400), but
// PERCENT-ENCODED control bytes are accepted and DECODED into r.URL.Path,
// so an attacker can inject raw \r\n/ANSI/NUL bytes into any downstream
// consumer of r.URL.Path.
func TestLoggerInjection(t *testing.T) {
	buf := &bytes.Buffer{}
	mux := muxmaster.New()
	mux.Use(middleware.Logger(buf))
	mux.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	// Each target is sent raw over TCP, exactly as an attacker would.
	cases := []struct {
		name    string
		target  string
		comment string
	}{
		{"clean", "/plain", "baseline"},
		{"percent_encoded_crlf", "/abc%0D%0A2099-01-01T00:00:00Z+GET+/fake+200+0s", "attacker-forged second log line"},
		{"percent_encoded_lf", "/abc%0Amalicious", "LF only"},
		{"percent_encoded_cr", "/abc%0Dmalicious", "CR only"},
		{"percent_encoded_ansi_clear", "/%1B%5B2J%1B%5BHfake-log", "clear-screen + home"},
		{"percent_encoded_ansi_red", "/abc%1B%5B31mRED%1B%5B0m", "red coloured log line"},
		{"percent_encoded_nul", "/abc%00xyz", "NUL"},
		{"percent_encoded_bidi", "/abc%E2%80%AEuser", "right-to-left override"},
		{"percent_encoded_combo", "/abc%1B%5B31m%0D%0Abright-red-fake", "ANSI+CRLF combo"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h003-logger-injection.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		buf.Reset()
		raw := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			c.target, host)
		resp, rerr := rawHTTPExchange(srv, raw)
		_ = resp
		status := ""
		if len(resp) > 0 {
			if idx := strings.Index(string(resp), "\r\n"); idx > 0 {
				status = string(resp)[:idx]
			}
		}

		logLine := buf.String()
		lineCount := strings.Count(logLine, "\n")
		fmt.Fprintf(f, "[%s] %s\n  target=%q raw_err=%v\n  log_bytes=%q\n  has_ctl=%t lines=%d status=%q\n\n",
			c.name, c.comment, c.target, rerr,
			logLine, containsCtl(logLine), lineCount, status)

		// A successful log injection produces >1 newline in the log string.
		if strings.Contains(c.target, "%0D%0A") && lineCount > 1 {
			t.Logf("[%s] CONFIRMED log injection — %d lines", c.name, lineCount)
		}
		if strings.Contains(c.target, "%1B%5B") && strings.Contains(logLine, "\x1b[") {
			t.Logf("[%s] CONFIRMED ANSI injection via percent-encoded escape", c.name)
		}
	}
}

// TestLoggerCRLFDirectServeHTTP bypasses the stdlib client to drive the
// mux directly, so we can place ANY bytes — including literal CR, LF, NUL —
// into r.URL.Path. This tests the *middleware* behaviour assuming an
// upstream bug let the bytes through.
func TestLoggerCRLFDirectServeHTTP(t *testing.T) {
	buf := &bytes.Buffer{}
	mux := muxmaster.New()
	mux.Use(middleware.Logger(buf))
	mux.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	cases := []struct {
		name string
		path string
	}{
		{"literal_crlf", "/abc\r\n2026-04-17T00:00:00Z GET /fake 200 0s"},
		{"literal_lf", "/abc\nmalicious"},
		{"literal_cr", "/abc\rmalicious"},
		{"ansi", "/\x1b[2JFAKE"},
		{"nul", "/abc\x00def"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h003-logger-direct.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		buf.Reset()
		req := httptest.NewRequest("GET", "http://localhost/x", nil)
		// Override Path directly — stdlib normally can't parse CR/LF into
		// Path, but an upstream proxy that forwards raw bytes could.
		req.URL.Path = c.path
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		log := buf.String()
		fmt.Fprintf(f, "[%s] path=%q log_bytes=%q has_ctl=%t lines=%d\n",
			c.name, c.path, log, containsCtl(log), strings.Count(log, "\n"))

		// CONFIRM: if path contained CR/LF, log has >1 line.
		if strings.ContainsAny(c.path, "\r\n") && strings.Count(log, "\n") > 1 {
			t.Logf("[%s] CONFIRMED log injection — %d lines in log",
				c.name, strings.Count(log, "\n"))
		}
		if strings.Contains(c.path, "\x1b") && strings.Contains(log, "\x1b") {
			t.Logf("[%s] CONFIRMED ANSI injection in log output", c.name)
		}
	}
}

// TestSetHeaderCRLF exercises middleware/set_header.go: if a caller passes
// a value containing CR/LF, what does Go's Header().Set do?
func TestSetHeaderCRLF(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.SetHeader("X-Custom", "val\r\nSet-Cookie: evil=1"))
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := rawHTTPExchange(srv, "GET /x HTTP/1.1\r\nHost: "+stripSchemeHost(srv.URL)+"\r\nConnection: close\r\n\r\n")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	recordTranscript(t, "set-header-crlf.txt",
		"GET /x with SetHeader('X-Custom', 'val\\r\\nSet-Cookie: evil=1')",
		resp)
	// Validate that the stdlib response writer does NOT produce a real
	// Set-Cookie line. On the wire the serialiser replaces CR/LF with
	// spaces. So "Set-Cookie:" survives only as part of the X-Custom
	// value, with two spaces (where \r\n was).
	cookie := ""
	for _, line := range strings.Split(string(resp), "\r\n") {
		if strings.HasPrefix(line, "Set-Cookie:") {
			cookie = line
			break
		}
	}
	if cookie != "" {
		t.Errorf("actual Set-Cookie header appeared: %q", cookie)
	}
	t.Logf("wire-level response: %q", string(resp))
}
