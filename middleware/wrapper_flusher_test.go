// Regression tests for rmp task #254, sprint 18 (waste-hunt defects
// compress-writeheader-overwrite.txt and wrappers-hide-flusher.txt):
//
//  1. Compress's gzipResponseWriter let a later WriteHeader call overwrite
//     the first one, so a handler that writes a real status (e.g. 404) and
//     is then handed to http.ServeContent — which calls WriteHeader(200)
//     internally — served 200 to any client sending Accept-Encoding: gzip.
//  2. Logger's statusRecorder and Compress's gzipResponseWriter hid every
//     optional ResponseWriter interface (http.Flusher, io.ReaderFrom) and
//     had no Unwrap(), so http.NewResponseController(w).Flush() failed
//     with "feature not supported" behind either middleware — breaking any
//     streaming handler (Server-Sent Events) placed behind them.
package middleware_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// doubleWriteHeaderHandler returns a handler that mimics what
// http.ServeContent does after a NotFound handler already wrote a real
// status: it calls WriteHeader a second time with 200, then writes body.
func doubleWriteHeaderHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.WriteHeader(http.StatusOK) // superfluous — must not win
		_, _ = w.Write([]byte(body))
	}
}

// TestCompress_FirstWriteHeaderWins_SmallBody reproduces
// compress-writeheader-overwrite.txt directly: a body under the 1024-byte
// compression threshold takes the "skip" path in commit(), which is where
// the pre-fix code unconditionally forwarded the LAST status recorded
// instead of the first.
func TestCompress_FirstWriteHeaderWins_SmallBody(t *testing.T) {
	const body = "not found page"
	for _, tc := range []struct {
		name           string
		acceptEncoding string
	}{
		{"plain", ""},
		{"gzip", "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := middleware.Compress(gzip.BestSpeed)(doubleWriteHeaderHandler(body))
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.acceptEncoding != "" {
				r.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (first WriteHeader must win)", rec.Code)
			}
			if got := rec.Body.String(); got != body {
				t.Fatalf("body = %q, want %q", got, body)
			}
			if enc := rec.Header().Get("Content-Encoding"); enc != "" {
				t.Fatalf("Content-Encoding = %q, want empty (below compression threshold)", enc)
			}
		})
	}
}

// TestCompress_FirstWriteHeaderWins_LargeBody exercises the SAME defect on
// the "compress" path (body large enough to clear minCompressSize), so the
// fix is verified on both branches commit() can take.
func TestCompress_FirstWriteHeaderWins_LargeBody(t *testing.T) {
	body := strings.Repeat("not found - ", 200) // > 1024 bytes
	h := middleware.Compress(gzip.BestSpeed)(doubleWriteHeaderHandler(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (first WriteHeader must win)", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	if got := decodeGzipBody(t, rec.Body.Bytes()); got != body {
		t.Fatalf("decoded body mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

// TestLogger_FirstWriteHeaderWins verifies the RECORDED status used for the
// log line reflects the first WriteHeader call. net/http's real
// ResponseWriter already discards the client-visible effect of the second
// call on its own; the bug was that statusRecorder.status still captured
// the superfluous 200, so the log line lied about what was actually served.
func TestLogger_FirstWriteHeaderWins(t *testing.T) {
	var logBuf bytes.Buffer
	h := middleware.Logger(&logBuf)(doubleWriteHeaderHandler("nf"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("client status = %d, want 404", rec.Code)
	}
	logLine := logBuf.String()
	fields := strings.Fields(logLine)
	if len(fields) < 4 {
		t.Fatalf("unexpected log line shape: %q", logLine)
	}
	// Format is "<RFC3339> <method> <path> <status> <duration>".
	if loggedStatus := fields[3]; loggedStatus != "404" {
		t.Fatalf("logged status = %q, want %q (log line: %q)", loggedStatus, "404", logLine)
	}
}

// TestCompress_1xxInformational_DoesNotBlockFinalStatus reproduces
// MID-COMPRESS-1 (found during the sprint 18 waste-hunt security review of
// WH-03): gzipResponseWriter's first-WriteHeader-wins guard did not exempt
// 1xx informational codes the way net/http's own *response.WriteHeader does
// (net/http/server.go: "if code >= 100 && code <= 199 && code !=
// StatusSwitchingProtocols"). A handler that sends a 103 Early Hints
// response before its real final status (401, 403, ...) had that final
// status silently discarded: the ONE status commit() ever forwards to the
// wrapped ResponseWriter was the 103, and because net/http itself does not
// count a 1xx WriteHeader as "headers sent", the subsequent body write then
// triggered net/http's own implicit 200 OK — an access-control decision
// downgraded to success purely because the client advertised gzip support.
// This MUST run against a real net/http server: httptest.ResponseRecorder
// does not itself implement net/http's 1xx exemption, so it cannot
// distinguish a correct fix from the bug.
func TestCompress_1xxInformational_DoesNotBlockFinalStatus(t *testing.T) {
	body := strings.Repeat("A", 2000) // clears minCompressSize
	h := middleware.Compress(gzip.BestSpeed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints) // 103
		w.WriteHeader(http.StatusForbidden)  // the real, final status
		_, _ = w.Write([]byte(body))
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("client-visible status = %d, want %d — a 403 must survive a preceding 103 Early Hints call (MID-COMPRESS-1)", resp.StatusCode, http.StatusForbidden)
	}
	var r io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		r = zr
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("body mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

// TestLogger_1xxInformational_LogsFinalStatusNotInformational is the Logger
// counterpart of TestCompress_1xxInformational_DoesNotBlockFinalStatus.
// statusRecorder always forwards every WriteHeader call to the real
// ResponseWriter, so the CLIENT-visible status was already correct even
// before the fix; the bug was that the LOGGED status stayed pinned to the
// first (1xx) call forever, so a security-relevant final status (403 here)
// never appeared in the access log — it would read "103" instead, hiding
// the event from any status-code-based log monitoring or alerting.
func TestLogger_1xxInformational_LogsFinalStatusNotInformational(t *testing.T) {
	var logBuf bytes.Buffer
	h := middleware.Logger(&logBuf)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints) // 103
		w.WriteHeader(http.StatusForbidden)  // the real, final status
		_, _ = w.Write([]byte("forbidden"))
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("client-visible status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	fields := strings.Fields(logBuf.String())
	if len(fields) < 4 {
		t.Fatalf("unexpected log line shape: %q", logBuf.String())
	}
	if fields[3] != "403" {
		t.Fatalf("logged status = %q, want \"403\" (log line: %q) — a preceding 103 Early Hints call must not pin the logged status", fields[3], logBuf.String())
	}
}

// TestResponseController_Flush_ThroughWrappers reproduces
// wrappers-hide-flusher.txt: http.NewResponseController(w).Flush() must
// succeed behind Logger, Compress, and the two composed, exactly as it does
// against a bare http.ResponseWriter. It also proves the flushed bytes
// actually reach the client before the handler returns — a channel blocks
// the handler right after Flush so a bounded partial read on the client can
// only succeed if the data was truly delivered, not merely buffered
// internally.
func TestResponseController_Flush_ThroughWrappers(t *testing.T) {
	const firstChunk = "data: first\n\n"
	const secondChunk = "data: second\n\n"

	tests := []struct {
		name string
		wrap func(http.Handler) http.Handler
	}{
		{"bare", func(h http.Handler) http.Handler { return h }},
		{"logger", middleware.Logger(io.Discard)},
		{"compress", middleware.Compress(gzip.BestSpeed)},
		{"logger+compress", func(h http.Handler) http.Handler {
			return middleware.Logger(io.Discard)(middleware.Compress(gzip.BestSpeed)(h))
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proceed := make(chan struct{})
			flushErrCh := make(chan error, 1)

			srv := httptest.NewServer(tc.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(firstChunk))
				flushErrCh <- http.NewResponseController(w).Flush()
				// Bounded wait, not an unconditional <-proceed: if Flush
				// failed (the pre-fix defect) the test fails fast on the
				// error below and never reaches close(proceed), and this
				// handler must still return so the deferred srv.Close()
				// cannot deadlock waiting for it.
				select {
				case <-proceed:
				case <-time.After(3 * time.Second):
				}
				_, _ = w.Write([]byte(secondChunk))
			})))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			// Below the compression threshold: Flush forces an early
			// compress/skip decision (commit) that will pick "skip" for
			// firstChunk's few bytes, so the wire bytes equal firstChunk
			// verbatim even with Accept-Encoding: gzip — exercising the
			// exact defect scenario without needing a streaming gzip
			// decoder in the test.
			req.Header.Set("Accept-Encoding", "gzip")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if err := <-flushErrCh; err != nil {
				t.Fatalf("ResponseController.Flush error: %v", err)
			}

			got := make([]byte, len(firstChunk))
			readDone := make(chan error, 1)
			go func() {
				_, err := io.ReadFull(resp.Body, got)
				readDone <- err
			}()
			select {
			case err := <-readDone:
				if err != nil {
					t.Fatalf("reading flushed chunk: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("first chunk did not arrive before timeout — Flush did not reach the client")
			}
			if string(got) != firstChunk {
				t.Fatalf("first chunk = %q, want %q", got, firstChunk)
			}

			close(proceed)

			rest, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading remainder: %v", err)
			}
			if string(rest) != secondChunk {
				t.Fatalf("second chunk = %q, want %q", rest, secondChunk)
			}
		})
	}
}

// TestSSEStreaming_ThroughLoggerAndCompress drives a multi-event
// Server-Sent-Events stream through Logger(Compress(handler)) — the
// composition explicitly named in the defect report — flushing after each
// event, and verifies every event is individually delivered and the
// overall response completes cleanly.
func TestSSEStreaming_ThroughLoggerAndCompress(t *testing.T) {
	const events = 5
	var logBuf bytes.Buffer

	h := middleware.Logger(&logBuf)(middleware.Compress(gzip.BestSpeed)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			rc := http.NewResponseController(w)
			for i := range events {
				if _, err := fmt.Fprintf(w, "data: event-%d\n\n", i); err != nil {
					t.Errorf("write event %d: %v", i, err)
					return
				}
				if err := rc.Flush(); err != nil {
					t.Errorf("flush event %d: %v", i, err)
					return
				}
			}
		}),
	))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	for i := range events {
		want := fmt.Sprintf("data: event-%d\n\n", i)
		if !strings.Contains(string(body), want) {
			t.Errorf("stream missing %q; got %q", want, body)
		}
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(logBuf.String(), " 200 ") {
		t.Errorf("log line = %q, want status 200 recorded", logBuf.String())
	}
}
