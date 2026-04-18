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
