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
	status      int
	wroteHeader bool // true once status has been fixed by the first WriteHeader call
}

// WriteHeader forwards every call to the wrapped ResponseWriter unchanged —
// net/http's own *response already discards a call after the first one
// (logging "superfluous response.WriteHeader call" on the server's
// ErrorLog), so the client-visible status was always correct. Only the
// RECORDED status for the log line is fixed here: it must reflect the
// first call too, or a handler that writes an error status and then lets a
// helper like http.ServeContent call WriteHeader(200) afterwards would log
// 200 for a request net/http actually served as the first status
// (MM-2026-0254).
//
// 1xx informational codes (RFC 8297 Early Hints, 100 Continue) are exempt
// from the first-wins lock, exactly like net/http's own *response.WriteHeader
// (net/http/server.go: "if code >= 100 && code <= 199 && code !=
// StatusSwitchingProtocols"). Without this exemption, a handler emitting a
// 103 Early Hints response before its real final status (e.g. 401, 403)
// would have the LOGGED status permanently pinned to 103 — the client-visible
// response stays correct (every call is still forwarded to the real
// ResponseWriter, which applies the same net/http exemption), but the audit
// log would misreport a security-relevant final status as an informational
// one, hiding it from status-code-based log monitoring.
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader && (code < 100 || code > 199 || code == http.StatusSwitchingProtocols) {
		r.wroteHeader = true
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher by delegating to the wrapped ResponseWriter
// when it supports flushing (e.g. for Server-Sent Events). A direct type
// assertion w.(http.Flusher) needs this method to see through the wrapper;
// http.ResponseController would also work via Unwrap alone, but not every
// caller uses ResponseController.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the wrapped ResponseWriter so http.ResponseController (and
// any other Unwrap-aware caller) can reach optional interfaces this wrapper
// does not itself implement (http.Hijacker, http.Pusher).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// writerOnly strips every optional interface (io.ReaderFrom, http.Flusher,
// ...) from a wrapped io.Writer, exposing Write only. ReadFrom uses it to
// hand io.Copy a value that cannot re-discover io.ReaderFrom on the
// underlying writer and call back into itself, which would recurse forever
// when that writer does NOT implement io.ReaderFrom (the case ReadFrom
// falls back for).
type writerOnly struct{ io.Writer }

// ReadFrom delegates to the underlying http.ResponseWriter's io.ReaderFrom
// when it implements one (WH-07). net/http's *response implements
// io.ReaderFrom — http.ServeContent and http.ServeFile use io.CopyN, which
// prefers ReaderFrom, to reach the sendfile(2)/splice fast path. Without
// this method, embedding http.ResponseWriter only promotes Header, Write
// and WriteHeader, so that fast path was invisible behind statusRecorder
// and every file response fell back to a buffered read(2)+write(2) loop.
//
// Status capture is unchanged: exactly like Write, net/http implicitly
// records a 200 the first time bytes are written if WriteHeader was never
// called, and this method never touches r.status directly — the wrapped
// ResponseWriter's own WriteHeader (invoked by the stdlib when it decides
// to write headers before streaming the body) is what statusRecorder
// intercepts, same as on the Write path.
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(writerOnly{r.ResponseWriter}, src)
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

// appendSanitisedForLog is the allocation-free counterpart of
// sanitiseForLog (WH-02): it appends the sanitised bytes of s directly
// into buf instead of building a fresh quoted string just to strip its
// quotes and copy the remainder. Byte-for-byte identical to
// `append(buf, sanitiseForLog(s)...)` for every input (200 000-string
// fuzz-style corpus in logger_test.go, TestEquiv_AppendSanitisedForLog).
//
// The common case — every byte printable ASCII and neither `"` nor `\`,
// for which strconv.QuoteToASCII is the identity apart from the
// surrounding quotes — appends s unchanged with no allocation. Any other
// input falls back to strconv.AppendQuoteToASCII (which still allocates
// only when it must grow buf) and drops the two quote bytes it wrote.
func appendSanitisedForLog(buf []byte, s string) []byte {
	plain := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			plain = false
			break
		}
	}
	if plain {
		return append(buf, s...)
	}
	n := len(buf)
	buf = strconv.AppendQuoteToASCII(buf, s)
	// AppendQuoteToASCII wrote a leading and a trailing `"` around the
	// sanitised bytes; shift them out and shrink by those two bytes.
	copy(buf[n:], buf[n+1:len(buf)-1])
	return buf[:len(buf)-2]
}

// appendLogDuration appends the same text time.Duration.String() would
// produce for d, without allocating (WH-02). It is a byte-for-byte replica
// of the unexported time.Duration.format algorithm (Go 1.26/1.27),
// verified against the real d.String() for the full range of durations,
// including negative and extreme values, by TestEquiv_AppendLogDuration —
// that test is the ongoing guarantee that this replica tracks the
// standard library's format; a future stdlib change to Duration's textual
// format would fail it immediately.
func appendLogDuration(b []byte, d time.Duration) []byte {
	var arr [32]byte
	n := formatLogDuration(&arr, d)
	return append(b, arr[n:]...)
}

func formatLogDuration(buf *[32]byte, d time.Duration) int {
	w := len(buf)
	u := uint64(d) //nolint:gosec // two's-complement magnitude extraction, mirrors time.Duration.format exactly
	neg := d < 0
	if neg {
		u = -u
	}
	if u < uint64(time.Second) {
		var prec int
		w--
		buf[w] = 's'
		w--
		switch {
		case u == 0:
			buf[w] = '0'
			return w
		case u < uint64(time.Microsecond):
			prec = 0
			buf[w] = 'n'
		case u < uint64(time.Millisecond):
			prec = 3
			w--
			copy(buf[w:], "µ")
		default:
			prec = 6
			buf[w] = 'm'
		}
		w, u = fmtFracLogDuration(buf[:w], u, prec)
		w = fmtIntLogDuration(buf[:w], u)
	} else {
		w--
		buf[w] = 's'
		w, u = fmtFracLogDuration(buf[:w], u, 9)
		w = fmtIntLogDuration(buf[:w], u%60)
		u /= 60
		if u > 0 {
			w--
			buf[w] = 'm'
			w = fmtIntLogDuration(buf[:w], u%60)
			u /= 60
			if u > 0 {
				w--
				buf[w] = 'h'
				w = fmtIntLogDuration(buf[:w], u)
			}
		}
	}
	if neg {
		w--
		buf[w] = '-'
	}
	return w
}

func fmtFracLogDuration(buf []byte, v uint64, prec int) (nw int, nv uint64) {
	w := len(buf)
	print := false
	for range prec {
		digit := v % 10
		print = print || digit != 0
		if print {
			w--
			buf[w] = byte(digit) + '0'
		}
		v /= 10
	}
	if print {
		w--
		buf[w] = '.'
	}
	return w, v
}

func fmtIntLogDuration(buf []byte, v uint64) int {
	w := len(buf)
	if v == 0 {
		w--
		buf[w] = '0'
	} else {
		for v > 0 {
			w--
			buf[w] = byte(v%10) + '0'
			v /= 10
		}
	}
	return w
}

// Logger logs each request after it completes. Panics if out is nil.
//
// Opt L1: the original implementation used fmt.Fprintf(out, "%s %s %s %d %s\n"
// + 5 args) which boxes each argument as interface{} (5 allocs) and allocated
// a fresh *statusRecorder per request (1 alloc that escaped to heap). The new
// implementation pools the statusRecorder and assembles the log line into a
// pooled []byte buffer via direct strconv.Append*. Output bytes are identical:
// the format is "<RFC3339> <method> <path> <status> <duration>\n".
//
// WH-02: a single clock read at the end of the request (`end := time.Now()`)
// now serves both the logged timestamp (`end.AppendFormat`) and the logged
// duration (`end.Sub(start)`, monotonic exactly like time.Since) — the
// original implementation read the clock again for each. Sanitising method
// and path now appends straight into the pooled buffer
// (appendSanitisedForLog) instead of allocating a quoted string and copying
// it, and the duration is appended without allocating (appendLogDuration).
// The output format and every logged byte are unchanged; the request
// duration this logs now excludes the logger's own formatting work (it
// previously included the time spent reading and formatting the timestamp),
// which is closer to, not further from, the wrapped handler's true latency.
func Logger(out io.Writer) func(http.Handler) http.Handler {
	if out == nil {
		panic("middleware: Logger requires a non-nil writer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rec := statusRecorderPool.Get().(*statusRecorder) //nolint:forcetypeassert // pool.New always returns *statusRecorder
			rec.ResponseWriter = w
			rec.status = http.StatusOK
			rec.wroteHeader = false

			next.ServeHTTP(rec, r)

			status := rec.status
			// Clear before returning to pool so the next caller does not see a
			// stale ResponseWriter pointer or a wroteHeader flag that would
			// silently discard its own first WriteHeader call.
			rec.ResponseWriter = nil
			rec.status = 0
			rec.wroteHeader = false
			statusRecorderPool.Put(rec)

			end := time.Now()

			bufp := loggerBufPool.Get().(*[]byte) //nolint:forcetypeassert // pool.New always returns *[]byte
			buf := (*bufp)[:0]

			buf = end.AppendFormat(buf, time.RFC3339)
			buf = append(buf, ' ')
			buf = appendSanitisedForLog(buf, r.Method)
			buf = append(buf, ' ')
			buf = appendSanitisedForLog(buf, r.URL.Path)
			buf = append(buf, ' ')
			buf = strconv.AppendInt(buf, int64(status), 10)
			buf = append(buf, ' ')
			buf = appendLogDuration(buf, end.Sub(start))
			buf = append(buf, '\n')

			_, _ = out.Write(buf)

			*bufp = buf[:0]
			loggerBufPool.Put(bufp)
		})
	}
}
