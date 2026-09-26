package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
)

// Recoverer recovers from panics and writes a 500 response.
// Logs via slog.Default() — use RecovererWithLogger for a custom logger.
//
// Deprecated: use RecovererWithLogger(slog.Default()) for explicit control.
func Recoverer() func(http.Handler) http.Handler {
	return RecovererWithLogger(slog.Default())
}

// recovererWriter tracks whether the response has already been committed —
// WriteHeader called with a final (non-1xx) status, or the first byte
// written, which implicitly sends 200 exactly like net/http's own
// *response — so RecovererWithLogger's panic handler can tell whether
// writing its own 500 is still possible.
//
// 1xx informational codes (RFC 8297 Early Hints, 100 Continue) do NOT mark
// the response as started, mirroring net/http's own *response.WriteHeader
// (net/http/server.go: "if code >= 100 && code <= 199 && code !=
// StatusSwitchingProtocols") and the same exemption already applied by
// statusRecorder (logger.go) and gzipResponseWriter (compress.go): the
// stdlib itself accepts a further WriteHeader call after a 1xx response, so
// a handler that emits Early Hints and then panics before writing its real
// status must still receive Recoverer's 500, not a connection left with no
// final status at all.
type recovererWriter struct {
	http.ResponseWriter
	started bool
}

func (w *recovererWriter) WriteHeader(code int) {
	if code < 100 || code > 199 || code == http.StatusSwitchingProtocols {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recovererWriter) Write(b []byte) (int, error) {
	// A Write with no prior WriteHeader implicitly sends 200, exactly like a
	// bare http.ResponseWriter.
	w.started = true
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher by delegating to the wrapped ResponseWriter
// when it supports flushing (e.g. for Server-Sent Events). Flushing commits
// any pending status (net/http sends an implicit 200 on first Flush if
// WriteHeader was never called), so it marks the response as started too.
func (w *recovererWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		w.started = true
		f.Flush()
	}
}

// Unwrap returns the wrapped ResponseWriter so http.ResponseController (and
// any other Unwrap-aware caller, notably http.Hijacker) can reach optional
// interfaces this wrapper does not itself implement.
func (w *recovererWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// recovererWriterPool reuses recovererWriter structs across requests so the
// `&recovererWriter{}` does not escape to the heap on every call — Recoverer
// is typically the outermost middleware and wraps every request in a
// deployment that uses it.
var recovererWriterPool = sync.Pool{
	New: func() any { return new(recovererWriter) },
}

// RecovererWithLogger recovers from panics, logs the panic value and stack
// trace at Error level via logger, and writes a plain 500 response — but
// only if the wrapped handler has not already committed a response (sent a
// final status or written a body byte). A handler that panics after
// writing its own response is a handler bug independent of Recoverer:
// net/http itself discards a WriteHeader call once the status line is on
// the wire (logging "superfluous response.WriteHeader call" to its own
// ErrorLog), and Recoverer now applies the same rule to the body, instead
// of unconditionally appending "Internal Server Error\n" after whatever
// the handler already streamed (O-14, rmp #276).
//
// The panic value is never written to the response body, preventing
// information leakage to clients (MM-2026-0023).
func RecovererWithLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := recovererWriterPool.Get().(*recovererWriter) //nolint:forcetypeassert // pool.New always returns *recovererWriter
			sw.ResponseWriter = w
			sw.started = false
			defer func() {
				if rcv := recover(); rcv != nil {
					// Sanitise method and path before logging — slog.TextHandler
					// emits raw bytes, so an attacker-controlled CRLF or ANSI
					// escape in r.URL.Path would otherwise reach the log
					// stream verbatim (log injection / terminal escape).
					// MSR-2026-0057.
					logger.Error("panic recovered",
						slog.Any("panic", rcv),
						slog.String("stack", string(debug.Stack())),
						slog.String("method", sanitiseForLog(r.Method)),
						slog.String("path", sanitiseForLog(r.URL.Path)),
					)
					if !sw.started {
						http.Error(sw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
				// Clear before returning to pool so the next caller does not
				// see a stale ResponseWriter pointer (pool hygiene, same
				// discipline as statusRecorderPool in logger.go).
				sw.ResponseWriter = nil
				sw.started = false
				recovererWriterPool.Put(sw)
			}()
			next.ServeHTTP(sw, r)
		})
	}
}
