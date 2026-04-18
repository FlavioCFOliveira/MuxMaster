package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type requestIDKey struct{}

// validRequestID returns true if id is safe to propagate as a request ID.
// Allows ASCII alphanumeric characters plus hyphen, underscore, and dot,
// with a maximum length of 128 characters.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// RequestID generates or propagates a request ID via X-Request-ID header.
// Incoming X-Request-ID values are validated; invalid or oversized values
// are replaced with a freshly generated random ID (MM-2026-0011).
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if !validRequestID(id) {
				var b [16]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetRequestID returns the request ID stored in ctx, or "" if absent.
func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
