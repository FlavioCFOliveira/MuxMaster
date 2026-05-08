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
//
// SECURITY: AllowedOrigins must be set explicitly. Passing nil or an empty
// slice is a misconfiguration trap (HPS-2026-0003): the middleware would
// silently let cross-origin requests through with no ACAO header, hiding
// the issue from the operator. We panic at construction time so the
// misconfiguration is caught at boot.
//
// ORDERING (MSR-2026-0070): CORS sets `Access-Control-Allow-Origin` (and
// related Access-Control-* + Vary headers) when its frame runs. If another
// middleware that calls `Header().Set(...)` runs AFTER CORS in the request
// flow (innermost in the Use() chain), the late Set will OVERWRITE the
// CORS-managed values, silently bypassing the configured whitelist. To
// keep CORS authoritative, register CORS as the INNERMOST middleware that
// touches these headers (i.e. last in the Use() chain that handles them)
// or avoid calling SetHeader on CORS-managed names. See SetHeader for the
// composition rule.
func CORS(opts CORSOptions) func(http.Handler) http.Handler {
	if len(opts.AllowedOrigins) == 0 {
		panic("middleware: CORS requires a non-empty AllowedOrigins (use []string{\"*\"} for wildcard, " +
			"or an explicit allowlist; nil silently allows traffic with no Access-Control-Allow-Origin)")
	}
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
				// MM-2026-0051: any per-origin response must carry Vary: Origin
				// so caches do not serve a response intended for origin A to a
				// client from origin B.
				h.Add("Vary", "Origin")
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
