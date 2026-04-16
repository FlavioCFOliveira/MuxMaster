package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout applies a context deadline to each request. Panics if d <= 0.
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
