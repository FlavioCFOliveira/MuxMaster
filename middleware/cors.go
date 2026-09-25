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

	// Opt L3: the header VALUES that are constant for this CORS() instance
	// are precomputed as STRINGS, so the handler skips both
	// textproto.CanonicalMIMEHeaderKey (the header names below are already
	// canonical compile-time constants) and, for allowedMethods/
	// allowedHeaders/exposedHeaders/maxAge, the strings.Join/strconv.Itoa
	// work on every request.
	//
	// MID-CORS-1 (sprint 18 waste-hunt, same class as MID-SETHEADER-1 in
	// set_header.go): an earlier version of this optimisation also hoisted
	// the single-element []string HEADER VALUES themselves into these
	// closure variables, so every request handled by this CORS() instance
	// shared the exact same slice for a given header. Any downstream code
	// indexing directly into the slice (w.Header()[k][0] = ...) mutated the
	// shared backing array in place, corrupting that header — commonly
	// Access-Control-Allow-Origin or -Credentials — for every other request
	// through this instance until process restart. Only the STRING is safe
	// to hoist; the slice wrapping it must be allocated fresh per request.
	maxAgeStr := ""
	if opts.MaxAge > 0 {
		maxAgeStr = strconv.Itoa(opts.MaxAge)
	}

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
			// MID-CORS-2 (waste-hunt gate follow-up to MID-CORS-1): every
			// single-value header this middleware may set below shares ONE
			// freshly allocated [7]string backing array per request — 1
			// allocation total for the whole request, not up to 7 — sized
			// for the worst case (preflight + credentials + exposed
			// headers + a non-allowAll Vary bump). Each assigned header's
			// slice is the FULL slice expression vals[i:i+1:i+1], capped
			// at length 1: an append to any ONE header (e.g. a later
			// Header().Add on the same key) grows into a fresh backing
			// array instead of silently overwriting an unrelated header's
			// slot in vals. Positions not used this request are simply
			// never referenced by any header key — their zero-value string
			// is harmless. Cross-REQUEST isolation (MID-CORS-1's original
			// concern) is unaffected: vals is allocated fresh here, every
			// call.
			vals := new([7]string)
			n := 0
			// When allowAll, emit the literal "*" — never reflect the request origin (MM-2026-0012).
			if allowAll {
				vals[n] = "*"
				h["Access-Control-Allow-Origin"] = vals[n : n+1 : n+1]
				n++
			} else {
				vals[n] = origin
				h["Access-Control-Allow-Origin"] = vals[n : n+1 : n+1]
				n++
				// MM-2026-0051: any per-origin response must carry Vary: Origin
				// so caches do not serve a response intended for origin A to a
				// client from origin B. Preserve Add semantics: if Vary already
				// exists, append; otherwise assign a fresh slot in vals
				// (MID-CORS-1: never a slice shared across requests).
				if existing, ok := h["Vary"]; ok {
					h["Vary"] = append(existing, "Origin")
				} else {
					vals[n] = "Origin"
					h["Vary"] = vals[n : n+1 : n+1]
					n++
				}
			}
			if opts.AllowCredentials {
				vals[n] = "true"
				h["Access-Control-Allow-Credentials"] = vals[n : n+1 : n+1]
				n++
			}
			if exposedHeaders != "" {
				vals[n] = exposedHeaders
				h["Access-Control-Expose-Headers"] = vals[n : n+1 : n+1]
				n++
			}
			if r.Method == http.MethodOptions {
				if allowedMethods != "" {
					vals[n] = allowedMethods
					h["Access-Control-Allow-Methods"] = vals[n : n+1 : n+1]
					n++
				}
				if allowedHeaders != "" {
					vals[n] = allowedHeaders
					h["Access-Control-Allow-Headers"] = vals[n : n+1 : n+1]
					n++
				}
				if maxAgeStr != "" {
					vals[n] = maxAgeStr
					h["Access-Control-Max-Age"] = vals[n : n+1 : n+1]
					// n is not incremented here: this is the last possible
					// write to vals in this function, so no code ever reads
					// n again (golangci-lint: ineffassign).
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
