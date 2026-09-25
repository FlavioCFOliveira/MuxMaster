package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// SetHeader sets a fixed response header before calling the next handler.
// Panics at construction time if key or value contains CR or LF, as those
// bytes can reach downstream middleware in raw form even though Go's wire
// serialiser strips them before writing to the network.
//
// ORDERING (MSR-2026-0070): SetHeader runs Header().Set on the response
// at the START of its frame, BEFORE calling next. Middleware composition
// in MuxMaster wraps from outermost to innermost — the first Use() call
// is outermost. So if Use(CORS, SetHeader(...)) is registered, SetHeader
// runs LAST in the request flow (innermost) and its Set() will OVERWRITE
// any header CORS already wrote. Specifically, Use(CORS, SetHeader(
// "Access-Control-Allow-Origin", "*")) silently bypasses the CORS allowed
// origins whitelist for every request.
//
// To preserve CORS guarantees, register SetHeader BEFORE CORS in the Use()
// chain (so SetHeader is the outermost frame and CORS overwrites it for
// the headers it manages), or omit any SetHeader call that targets a
// CORS-managed header (Access-Control-Allow-Origin, -Methods, -Headers,
// -Credentials, -Max-Age, -Expose-Headers, Vary).
func SetHeader(key, value string) func(http.Handler) http.Handler {
	if strings.ContainsAny(key, "\r\n") {
		panic("middleware: SetHeader key contains CR or LF: " + strconv.QuoteToASCII(key))
	}
	if strings.ContainsAny(value, "\r\n") {
		panic("middleware: SetHeader value contains CR or LF: " + strconv.QuoteToASCII(value))
	}
	// [waste-hunt WH-05] key is fixed for the lifetime of this middleware, so
	// canonicalise it once here instead of paying Header().Set's
	// canonicalisation cost on every request. Direct map assignment with a
	// PER-REQUEST []string{value} produces the identical header as
	// Set(key, value) (see specification/middleware-stdlib.md §59).
	//
	// MID-SETHEADER-1: the single-element slice backing the header value
	// MUST be allocated fresh per request, not hoisted alongside canonKey.
	// A hoisted, shared slice is reachable from every request's Header map
	// simultaneously; any downstream code that indexes into it directly
	// (w.Header()[key][0] = ...) — rather than going through Set/Add/Del,
	// which always install a brand new slice — mutates that ONE shared
	// backing array in place, corrupting the header value for every other
	// request (past and future) still holding the same pooled middleware
	// instance, until process restart. This is a real cross-request
	// contamination vector for exactly the security headers SetHeader is
	// typically used for (CSP, HSTS, X-Frame-Options, CORS). Allocating the
	// slice per request restores normal http.Header isolation.
	canonKey := http.CanonicalHeaderKey(key)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header()[canonKey] = []string{value}
			next.ServeHTTP(w, r)
		})
	}
}
