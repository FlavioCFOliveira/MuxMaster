package middleware

import (
	"context"
	"net/http"
)

// WithValue injects a value into the request context.
func WithValue(key, val any) func(http.Handler) http.Handler {
	if key == nil {
		panic("middleware: WithValue key must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), key, val)))
		})
	}
}
