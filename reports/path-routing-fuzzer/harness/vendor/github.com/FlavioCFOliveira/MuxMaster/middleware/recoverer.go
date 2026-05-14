package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recoverer recovers from panics and writes a 500 response.
// Logs via slog.Default() — use RecovererWithLogger for a custom logger.
//
// Deprecated: use RecovererWithLogger(slog.Default()) for explicit control.
func Recoverer() func(http.Handler) http.Handler {
	return RecovererWithLogger(slog.Default())
}

// RecovererWithLogger recovers from panics, logs the panic value and stack
// trace at Error level via logger, and writes a plain 500 response.
// The panic value is never written to the response body, preventing
// information leakage to clients (MM-2026-0023).
func RecovererWithLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
