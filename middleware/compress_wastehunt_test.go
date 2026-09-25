// Regression tests for rmp task #251, sprint 18 (WH-03): compressAlt's pooled
// *gzipResponseWriter with a fixed 8 KiB sniff array, recycled through
// sync.Pool. These pin the pool-hygiene properties the task requires: no
// data leak between requests sharing a recycled writer, and correct cleanup
// (and no state leak to the NEXT request) when a handler panics.
package middleware_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func gzipRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	return r
}

func decodeGzipBody(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("bad gzip stream: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	return string(plain)
}

// TestCompress_PoolReuse_NoStateLeakAcrossManyRequests alternates a small
// (passthrough) and a large chunked (compressed) response over the SAME
// pooled *gzipResponseWriter hundreds of times and verifies every single
// response is byte-correct — a leaked sniff buffer or gzip writer state
// would corrupt or truncate the body of a later request.
func TestCompress_PoolReuse_NoStateLeakAcrossManyRequests(t *testing.T) {
	small := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	const chunkCount = 200
	big := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		for range chunkCount {
			_, _ = w.Write([]byte("<li>entry</li>"))
		}
	})
	mw := middleware.Compress(gzip.BestSpeed)

	for i := range 200 {
		h := small
		wantPlain := `{"ok":true}`
		if i%2 == 0 {
			h = big
			wantPlain = strings.Repeat("<li>entry</li>", chunkCount)
		}
		rec := httptest.NewRecorder()
		mw(h).ServeHTTP(rec, gzipRequest())

		body := rec.Body.Bytes()
		var gotPlain string
		if rec.Header().Get("Content-Encoding") == "gzip" {
			gotPlain = decodeGzipBody(t, body)
		} else {
			gotPlain = string(body)
		}
		if gotPlain != wantPlain {
			t.Fatalf("iteration %d: body = %q (len %d), want %q (len %d) — pooled writer leaked state between requests",
				i, truncate(gotPlain, 80), len(gotPlain), truncate(wantPlain, 80), len(wantPlain))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestCompress_PoolHygiene_PanicDoesNotLeakStateToNextRequest verifies the
// deferred cleanup (close + full struct reset + Put) still runs when the
// wrapped handler panics, and that the NEXT request through the same
// middleware instance is served correctly rather than inheriting the
// panicking request's partially-written sniff buffer or gzip writer.
func TestCompress_PoolHygiene_PanicDoesNotLeakStateToNextRequest(t *testing.T) {
	const n = 2000
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("x", n)))
		panic("boom")
	})
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("y", n)))
	})
	mw := middleware.Compress(gzip.BestSpeed)

	func() {
		defer func() { _ = recover() }()
		mw(panicking).ServeHTTP(httptest.NewRecorder(), gzipRequest())
	}()

	rec := httptest.NewRecorder()
	mw(ok).ServeHTTP(rec, gzipRequest())
	got := decodeGzipBody(t, rec.Body.Bytes())
	want := strings.Repeat("y", n)
	if got != want {
		t.Fatalf("body after a prior panic = %d bytes (want %d) — pool contamination across requests", len(got), len(want))
	}
}

// TestCompress_ChunkedWrite_IdenticalAcrossManySniffBufferSizes exercises
// the fixed 8 KiB sniff array's boundary: writes that land exactly at, just
// under, and just over sniffBufSize must all still produce a correct,
// decodable gzip stream (or correct passthrough) with no data loss from
// reusing the same backing array across the commit boundary.
func TestCompress_ChunkedWrite_IdenticalAcrossManySniffBufferSizes(t *testing.T) {
	sizes := []int{1023, 1024, 1025, 8191, 8192, 8193, 20000}
	mw := middleware.Compress(gzip.BestSpeed)
	for _, n := range sizes {
		want := strings.Repeat("a", n)
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			// Write in small pieces to exercise the accumulate-then-commit path.
			for i := 0; i < len(want); i += 37 {
				end := min(i+37, len(want))
				_, _ = w.Write([]byte(want[i:end]))
			}
		})
		rec := httptest.NewRecorder()
		mw(h).ServeHTTP(rec, gzipRequest())
		var got string
		if rec.Header().Get("Content-Encoding") == "gzip" {
			got = decodeGzipBody(t, rec.Body.Bytes())
		} else {
			got = rec.Body.String()
		}
		if got != want {
			t.Fatalf("size %d: body length = %d, want %d", n, len(got), len(want))
		}
	}
}
