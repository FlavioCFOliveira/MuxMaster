package middleware

import (
	"io"
	"net/http"
	"strconv"
	"sync"
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

// statusRecorderPool reuses statusRecorder structs across requests so the
// `&statusRecorder{}` does not escape to the heap on every call.
var statusRecorderPool = sync.Pool{
	New: func() any { return new(statusRecorder) },
}

// loggerBufPool reuses []byte slices for the log line so fmt.Fprintf's
// interface boxing (one alloc per %s/%d argument) is avoided. The buffer
// starts at 192B, which covers a typical RFC3339 timestamp + method +
// path + status + duration in one allocation.
var loggerBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 192)
		return &b
	},
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
//
// Opt L1: the original implementation used fmt.Fprintf(out, "%s %s %s %d %s\n"
// + 5 args) which boxes each argument as interface{} (5 allocs) and allocated
// a fresh *statusRecorder per request (1 alloc that escaped to heap). The new
// implementation pools the statusRecorder and assembles the log line into a
// pooled []byte buffer via direct strconv.Append*. Output bytes are identical:
// the format is "<RFC3339> <method> <path> <status> <duration>\n".
func Logger(out io.Writer) func(http.Handler) http.Handler {
	if out == nil {
		panic("middleware: Logger requires a non-nil writer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rec := statusRecorderPool.Get().(*statusRecorder)
			rec.ResponseWriter = w
			rec.status = http.StatusOK

			next.ServeHTTP(rec, r)

			status := rec.status
			// Clear before returning to pool so the next caller does not see a
			// stale ResponseWriter pointer.
			rec.ResponseWriter = nil
			rec.status = 0
			statusRecorderPool.Put(rec)

			bufp := loggerBufPool.Get().(*[]byte)
			buf := (*bufp)[:0]

			buf = time.Now().AppendFormat(buf, time.RFC3339)
			buf = append(buf, ' ')
			buf = append(buf, sanitiseForLog(r.Method)...)
			buf = append(buf, ' ')
			buf = append(buf, sanitiseForLog(r.URL.Path)...)
			buf = append(buf, ' ')
			buf = strconv.AppendInt(buf, int64(status), 10)
			buf = append(buf, ' ')
			buf = append(buf, time.Since(start).String()...)
			buf = append(buf, '\n')

			_, _ = out.Write(buf)

			*bufp = buf[:0]
			loggerBufPool.Put(bufp)
		})
	}
}
