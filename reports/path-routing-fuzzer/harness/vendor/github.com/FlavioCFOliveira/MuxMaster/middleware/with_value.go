package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"reflect"
)

// WithValue injects a value into the request context.
// To avoid context key collisions between packages, always use an unexported
// type as the key:
//
//	type ctxKey struct{}
//	mux.Use(middleware.WithValue(ctxKey{}, myValue))
//
// MSR-2026-0059: passing a string (or any built-in type) as the key is a
// CWE-1021 cross-package collision risk — any package that uses the same
// string literal can read or overwrite this value. WithValue emits a
// slog.Warn at construction time when called with a string-kind key.
func WithValue(key, val any) func(http.Handler) http.Handler {
	if key == nil {
		panic("middleware: WithValue key must not be nil")
	}
	if reflect.TypeOf(key).Kind() == reflect.String {
		slog.Default().Warn("WithValue: string context key is a cross-package collision risk (CWE-1021); "+
			"use an unexported type (e.g. type ctxKey struct{}) instead.",
			slog.Any("key", key))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), key, val)))
		})
	}
}
