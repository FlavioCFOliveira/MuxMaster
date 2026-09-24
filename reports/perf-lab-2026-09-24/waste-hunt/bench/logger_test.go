package bench

import (
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: Logger per-request work ──────────────────────────────────────
//
// middleware/logger.go reads the clock three times per request (time.Now at
// start, time.Now for the timestamp, time.Since for the duration) and
// sanitises method and path through strconv.QuoteToASCII, which allocates a
// fresh quoted string per field, then slices off the quotes and copies the
// bytes again into the pooled buffer. time.Duration.String allocates too.
//
// loggerAlt keeps the exact output format and pools, and changes only:
//   (a) one end-of-request clock read serves both the timestamp and the
//       duration (end.Sub(start) uses the monotonic reading, like Since);
//   (b) sanitisation appends straight into the pooled buffer with
//       strconv.AppendQuoteToASCII (+ a fast path that skips quoting when
//       every byte is printable ASCII other than '"' and '\\', for which
//       QuoteToASCII is the identity);
//   (c) the duration is appended with appendDuration, a byte-identical
//       replica of time.Duration.String (verified in equiv_test.go).

type statusRecorderAlt struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorderAlt) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

var statusRecorderAltPool = sync.Pool{New: func() any { return new(statusRecorderAlt) }}
var loggerAltBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 192); return &b }}

// appendSanitised appends QuoteToASCII(s) without its surrounding quotes.
func appendSanitised(buf []byte, s string) []byte {
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
	copy(buf[n:], buf[n+1:len(buf)-1])
	return buf[:len(buf)-2]
}

// appendDuration is a replica of time.Duration.String appending into buf.
func appendDuration(b []byte, d time.Duration) []byte {
	var arr [32]byte
	n := formatDuration(&arr, d)
	return append(b, arr[n:]...)
}

// formatDuration mirrors the unexported time.Duration.format (Go 1.26/1.27).
func formatDuration(buf *[32]byte, d time.Duration) int {
	w := len(buf)
	u := uint64(d)
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
		w, u = fmtFrac(buf[:w], u, prec)
		w = fmtInt(buf[:w], u)
	} else {
		w--
		buf[w] = 's'
		w, u = fmtFrac(buf[:w], u, 9)
		w = fmtInt(buf[:w], u%60)
		u /= 60
		if u > 0 {
			w--
			buf[w] = 'm'
			w = fmtInt(buf[:w], u%60)
			u /= 60
			if u > 0 {
				w--
				buf[w] = 'h'
				w = fmtInt(buf[:w], u)
			}
		}
	}
	if neg {
		w--
		buf[w] = '-'
	}
	return w
}

func fmtFrac(buf []byte, v uint64, prec int) (nw int, nv uint64) {
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

func fmtInt(buf []byte, v uint64) int {
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

func loggerAlt(out io.Writer) func(http.Handler) http.Handler { return loggerVariant(out, true, true) }

// loggerVariant: singleClock applies change (a); noAlloc applies (b)+(c).
func loggerVariant(out io.Writer, singleClock, noAlloc bool) func(http.Handler) http.Handler {
	sanitise := func(s string) string { q := strconv.QuoteToASCII(s); return q[1 : len(q)-1] }
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := statusRecorderAltPool.Get().(*statusRecorderAlt)
			rec.ResponseWriter = w
			rec.status = http.StatusOK
			next.ServeHTTP(rec, r)
			status := rec.status
			rec.ResponseWriter = nil
			rec.status = 0
			statusRecorderAltPool.Put(rec)

			bufp := loggerAltBufPool.Get().(*[]byte)
			buf := (*bufp)[:0]
			var end time.Time
			if singleClock {
				end = time.Now()
				buf = end.AppendFormat(buf, time.RFC3339)
			} else {
				buf = time.Now().AppendFormat(buf, time.RFC3339)
			}
			buf = append(buf, ' ')
			if noAlloc {
				buf = appendSanitised(buf, r.Method)
				buf = append(buf, ' ')
				buf = appendSanitised(buf, r.URL.Path)
			} else {
				buf = append(buf, sanitise(r.Method)...)
				buf = append(buf, ' ')
				buf = append(buf, sanitise(r.URL.Path)...)
			}
			buf = append(buf, ' ')
			buf = strconv.AppendInt(buf, int64(status), 10)
			buf = append(buf, ' ')
			var d time.Duration
			if singleClock {
				d = end.Sub(start)
			} else {
				d = time.Since(start)
			}
			if noAlloc {
				buf = appendDuration(buf, d)
			} else {
				buf = append(buf, d.String()...)
			}
			buf = append(buf, '\n')
			_, _ = out.Write(buf)
			*bufp = buf[:0]
			loggerAltBufPool.Put(bufp)
		})
	}
}

func benchLogger(b *testing.B, h http.Handler) {
	r := realisticRequest(http.MethodGet, "/api/v1/books/42/reviews")
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

func BenchmarkLogger(b *testing.B) {
	b.Run("current", func(b *testing.B) { benchLogger(b, mw.Logger(io.Discard)(nop)) })
	b.Run("alternative", func(b *testing.B) { benchLogger(b, loggerAlt(io.Discard)(nop)) })
	b.Run("alt-single-clock-only", func(b *testing.B) { benchLogger(b, loggerVariant(io.Discard, true, false)(nop)) })
	b.Run("alt-no-alloc-only", func(b *testing.B) { benchLogger(b, loggerVariant(io.Discard, false, true)(nop)) })
}

// Cost of a single clock read on THIS host (clocksource matters: HPET vs TSC).
var sinkTime time.Time
var sinkDur time.Duration

func BenchmarkClock(b *testing.B) {
	b.Run("time.Now", func(b *testing.B) {
		for range b.N {
			sinkTime = time.Now()
		}
	})
	b.Run("time.Since", func(b *testing.B) {
		t0 := time.Now()
		for range b.N {
			sinkDur = time.Since(t0)
		}
	})
}

// Isolated sub-costs of the current Logger, for attribution.
var sinkStr string

func BenchmarkLoggerParts(b *testing.B) {
	b.Run("QuoteToASCII-path", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			q := strconv.QuoteToASCII("/api/v1/books/42/reviews")
			sinkStr = q[1 : len(q)-1]
		}
	})
	b.Run("appendSanitised-path", func(b *testing.B) {
		b.ReportAllocs()
		buf := make([]byte, 0, 64)
		for range b.N {
			buf = appendSanitised(buf[:0], "/api/v1/books/42/reviews")
		}
	})
	b.Run("Duration.String", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			sinkStr = (1234567 * time.Nanosecond).String()
		}
	})
	b.Run("appendDuration", func(b *testing.B) {
		b.ReportAllocs()
		buf := make([]byte, 0, 64)
		for range b.N {
			buf = appendDuration(buf[:0], 1234567*time.Nanosecond)
		}
	})
}
