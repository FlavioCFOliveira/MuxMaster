package middleware

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// sanitiseForLog removes control characters from s for safe log output.
// Uses QuoteToASCII and strips the surrounding double-quotes.
//
// FPE-2026-0002: this function is NOT idempotent. strconv.QuoteToASCII
// re-escapes already-escaped sequences, so calling sanitiseForLog twice
// on the same string produces a doubly-escaped result (e.g. `"` →
// `\"` → `\\\"`). All safety properties hold on every pass — there is
// no path that lets raw control bytes reach the output — but callers
// MUST apply the function exactly ONCE per log field. The Logger
// middleware applies it once per request line; new call sites should
// follow the same discipline.
func sanitiseForLog(s string) string {
	q := strconv.QuoteToASCII(s)
	return q[1 : len(q)-1]
}

// Logger logs each request after it completes. Panics if out is nil.
func Logger(out io.Writer) func(http.Handler) http.Handler {
	if out == nil {
		panic("middleware: Logger requires a non-nil writer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			fmt.Fprintf(out, "%s %s %s %d %s\n", //nolint:errcheck // log writes intentionally ignore I/O errors
				time.Now().Format(time.RFC3339),
				sanitiseForLog(r.Method),
				sanitiseForLog(r.URL.Path),
				rec.status,
				time.Since(start),
			)
		})
	}
}
