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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(key, value)
			next.ServeHTTP(w, r)
		})
	}
}
