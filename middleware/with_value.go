package middleware

import (
	"context"
	"net/http"
)

// WithValue injects a value into the request context.
// To avoid context key collisions between packages, always use an unexported
// type as the key:
//
//	type ctxKey struct{}
//	mux.Use(middleware.WithValue(ctxKey{}, myValue))
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
