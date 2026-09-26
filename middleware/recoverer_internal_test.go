// Internal (white-box) tests for recovererWriter (rmp #276, sprint 20,
// O-14 fix). This file uses `package middleware` so it can construct and
// drive recovererWriter directly, isolating its started-tracking state
// machine from the surrounding HTTP/panic-recovery machinery already
// covered end-to-end by middleware/gap_o14_test.go.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// bareResponseWriter implements only the three http.ResponseWriter methods
// — no http.Flusher, no http.Hijacker — so tests can verify recovererWriter
// degrades gracefully (a no-op, not a panic or a type-assertion crash) when
// the wrapped writer does not support an optional interface.
type bareResponseWriter struct {
	header http.Header
	status int
	body   []byte
}

func newBareResponseWriter() *bareResponseWriter {
	return &bareResponseWriter{header: make(http.Header)}
}

func (b *bareResponseWriter) Header() http.Header  { return b.header }
func (b *bareResponseWriter) WriteHeader(code int) { b.status = code }
func (b *bareResponseWriter) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return len(p), nil
}

func TestRecovererWriter_Unwrap_ReturnsWrappedWriter(t *testing.T) {
	inner := newBareResponseWriter()
	sw := &recovererWriter{ResponseWriter: inner}
	if unwrapped := sw.Unwrap(); unwrapped != http.ResponseWriter(inner) {
		t.Fatalf("Unwrap() = %v, want the exact wrapped ResponseWriter %v", unwrapped, inner)
	}
}

func TestRecovererWriter_WriteHeader_FinalStatusMarksStarted(t *testing.T) {
	inner := newBareResponseWriter()
	sw := &recovererWriter{ResponseWriter: inner}
	sw.WriteHeader(http.StatusNotFound)
	if !sw.started {
		t.Fatal("started = false after a final-status WriteHeader call, want true")
	}
	if inner.status != http.StatusNotFound {
		t.Fatalf("underlying status = %d, want %d (WriteHeader must still forward)", inner.status, http.StatusNotFound)
	}
}

func TestRecovererWriter_WriteHeader_1xxDoesNotMarkStarted(t *testing.T) {
	inner := newBareResponseWriter()
	sw := &recovererWriter{ResponseWriter: inner}
	sw.WriteHeader(http.StatusEarlyHints) // 103
	if sw.started {
		t.Fatal("started = true after a 1xx WriteHeader call, want false — net/http's own " +
			"*response.WriteHeader itself accepts a further call after a 1xx status")
	}
	sw.WriteHeader(http.StatusForbidden) // the real, final status
	if !sw.started {
		t.Fatal("started = false after the final WriteHeader following a 1xx, want true")
	}
	if inner.status != http.StatusForbidden {
		t.Fatalf("underlying status = %d, want %d", inner.status, http.StatusForbidden)
	}
}

func TestRecovererWriter_WriteHeader_SwitchingProtocolsMarksStarted(t *testing.T) {
	// 101 Switching Protocols is numerically inside the 1xx range but
	// net/http's own exemption explicitly excludes it — the connection is
	// about to be upgraded, not followed by another status line.
	inner := newBareResponseWriter()
	sw := &recovererWriter{ResponseWriter: inner}
	sw.WriteHeader(http.StatusSwitchingProtocols)
	if !sw.started {
		t.Fatal("started = false after 101 Switching Protocols, want true " +
			"(net/http treats 101 as final, not informational)")
	}
}

func TestRecovererWriter_Write_MarksStarted(t *testing.T) {
	inner := newBareResponseWriter()
	sw := &recovererWriter{ResponseWriter: inner}
	if _, err := sw.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !sw.started {
		t.Fatal("started = false after Write, want true — an unheadered Write implicitly sends 200")
	}
	if string(inner.body) != "hi" {
		t.Fatalf("underlying body = %q, want %q", inner.body, "hi")
	}
}

func TestRecovererWriter_Flush_MarksStartedWhenSupported(t *testing.T) {
	// httptest.ResponseRecorder implements http.Flusher.
	inner := httptest.NewRecorder()
	sw := &recovererWriter{ResponseWriter: inner}
	sw.Flush()
	if !sw.started {
		t.Fatal("started = false after Flush on a Flusher-supporting writer, want true")
	}
	if !inner.Flushed {
		t.Fatal("Flush did not delegate to the wrapped http.Flusher")
	}
}

func TestRecovererWriter_Flush_NoOpWhenUnsupported(t *testing.T) {
	inner := newBareResponseWriter() // does not implement http.Flusher
	sw := &recovererWriter{ResponseWriter: inner}
	sw.Flush() // must not panic
	if sw.started {
		t.Fatal("started = true after Flush on a non-Flusher writer, want false (Flush must be a no-op)")
	}
}

func TestRecovererWriterPool_ClearedBetweenUses(t *testing.T) {
	first := newBareResponseWriter()
	sw := recovererWriterPool.Get().(*recovererWriter) //nolint:forcetypeassert // pool.New always returns *recovererWriter
	sw.ResponseWriter = first
	sw.started = true
	sw.ResponseWriter = nil
	sw.started = false
	recovererWriterPool.Put(sw)

	reused := recovererWriterPool.Get().(*recovererWriter) //nolint:forcetypeassert // pool.New always returns *recovererWriter
	if reused.ResponseWriter != nil {
		t.Fatal("pooled recovererWriter retained a stale ResponseWriter across reuse")
	}
	if reused.started {
		t.Fatal("pooled recovererWriter retained a stale started flag across reuse")
	}
	recovererWriterPool.Put(reused)
}
