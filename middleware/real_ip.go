package middleware

import (
	"net/http"
	"strings"
)

// RealIP overwrites r.RemoteAddr with the client IP from X-Forwarded-For or X-Real-IP.
func RealIP() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				i := strings.IndexByte(xff, ',')
				if i < 0 {
					r.RemoteAddr = strings.TrimSpace(xff)
				} else {
					r.RemoteAddr = strings.TrimSpace(xff[:i])
				}
			} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
				r.RemoteAddr = xri
			}
			next.ServeHTTP(w, r)
		})
	}
}
