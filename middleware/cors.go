package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// isValidOrigin returns false if origin contains CR, LF, or NUL bytes.
func isValidOrigin(origin string) bool {
	for i := range len(origin) {
		c := origin[i]
		if c == '\r' || c == '\n' || c == 0 {
			return false
		}
	}
	return true
}

// CORSOptions configures CORS behaviour.
type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// CORS handles Cross-Origin Resource Sharing. Panics on invalid configuration.
func CORS(opts CORSOptions) func(http.Handler) http.Handler {
	for _, o := range opts.AllowedOrigins {
		if o == "*" && opts.AllowCredentials {
			panic(`middleware: CORS AllowCredentials must not be true when AllowedOrigins contains "*"`)
		}
	}
	allowedOrigins := make(map[string]bool, len(opts.AllowedOrigins))
	allowAll := false
	for _, o := range opts.AllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		allowedOrigins[o] = true
	}
	allowedMethods := strings.Join(opts.AllowedMethods, ", ")
	allowedHeaders := strings.Join(opts.AllowedHeaders, ", ")
	exposedHeaders := strings.Join(opts.ExposedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Reject CRLF/NUL in Origin before setting any response header (MM-2026-0028).
			if !isValidOrigin(origin) {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				return
			}
			if !allowAll && !allowedOrigins[origin] {
				if len(opts.AllowedOrigins) > 0 {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			// When allowAll, emit the literal "*" — never reflect the request origin (MM-2026-0012).
			if allowAll {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if opts.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if exposedHeaders != "" {
				h.Set("Access-Control-Expose-Headers", exposedHeaders)
			}
			if r.Method == http.MethodOptions {
				if allowedMethods != "" {
					h.Set("Access-Control-Allow-Methods", allowedMethods)
				}
				if allowedHeaders != "" {
					h.Set("Access-Control-Allow-Headers", allowedHeaders)
				}
				if opts.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", strconv.Itoa(opts.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
