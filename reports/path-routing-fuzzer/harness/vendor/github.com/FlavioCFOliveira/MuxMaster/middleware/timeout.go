package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout applies a context deadline to each request. Panics if d <= 0.
//
// SECURITY (DOS-2026-0003): Timeout cancels the request context after d, but
// it does NOT preempt the handler goroutine — Go has no preemption primitive
// for blocked syscalls. Handlers MUST observe ctx.Done() on every blocking
// call (DB, network, file I/O); a handler that ignores ctx.Done() will run
// to completion regardless of the timeout, accumulating goroutines under
// load and exhausting memory or upstream connections. Co-design Timeout
// with handler-level cooperation (use the *Context variants of the stdlib
// — sql.DB.QueryContext, net/http with http.Request, etc.).
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	if d <= 0 {
		panic("middleware: Timeout duration must be positive")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
