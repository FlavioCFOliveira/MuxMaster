// FuzzCompressRoundtrip — invariant I-03: if the response was compressed,
// gunzip(output) must equal the original bytes that the handler wrote.
//
// This catches corruption introduced by the compression middleware's buffering
// and the gzip writer lifecycle. It also flags the case where the middleware
// silently swallows bytes.
//
// Note: middleware.Compress uses a minCompressSize threshold (1024 bytes) —
// below that, it writes uncompressed. The invariant is still the same: the
// client observes the original bytes, whether via gzip or raw.
package harness

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func FuzzCompressRoundtrip(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("hello"))
	f.Add(bytes.Repeat([]byte("abc"), 500))
	f.Add(bytes.Repeat([]byte{0x00}, 2048))
	f.Add(bytes.Repeat([]byte{0xff}, 2048))
	f.Add([]byte("\x1f\x8b\x08"))               // gzip magic — double-compression
	f.Add(bytes.Repeat([]byte("ABC\r\n"), 500)) // CRLF in body
	f.Add(bytes.Repeat([]byte("<html></html>"), 200))

	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 8<<20 { // cap at 8MB per fuzz iteration
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic body.len=%d: %v\n%s", len(body), r, debug.Stack())
			}
		}()

		mw := middleware.Compress(5)
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
		h := mw(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		enc := rec.Header().Get("Content-Encoding")
		out := rec.Body.Bytes()

		switch enc {
		case "gzip":
			gr, err := gzip.NewReader(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("gzip.NewReader failed with body.len=%d: %v", len(body), err)
			}
			decoded, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("gzip read failed with body.len=%d: %v", len(body), err)
			}
			if !bytes.Equal(decoded, body) {
				t.Fatalf("roundtrip mismatch: body.len=%d decoded.len=%d firstDiff=%d",
					len(body), len(decoded), firstDiff(body, decoded))
			}
		case "":
			// No gzip — the middleware decided the body was too small.
			// Still, the raw bytes seen by the client must equal body.
			if !bytes.Equal(out, body) {
				t.Fatalf("uncompressed passthrough mismatch: body.len=%d out.len=%d",
					len(body), len(out))
			}
		default:
			t.Fatalf("unexpected Content-Encoding %q", enc)
		}
	})
}

// firstDiff returns the index of the first byte where a and b differ, or
// -1 if they are equal. Used for concise fuzz failure messages.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// FuzzCompressMultipleWrites — the handler writes in multiple chunks; the
// roundtrip invariant must still hold regardless of chunk boundary.
func FuzzCompressMultipleWrites(f *testing.F) {
	f.Add([]byte("hello"), []byte(" world"))
	f.Add(bytes.Repeat([]byte("A"), 600), bytes.Repeat([]byte("B"), 600))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, a, b []byte) {
		if len(a)+len(b) > 8<<20 {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v\n%s", r, debug.Stack())
			}
		}()

		mw := middleware.Compress(5)
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(a)
			_, _ = w.Write(b)
		})
		h := mw(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		enc := rec.Header().Get("Content-Encoding")
		out := rec.Body.Bytes()
		expected := append(append([]byte{}, a...), b...)

		if enc == "gzip" {
			gr, err := gzip.NewReader(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("gzip reader error: %v", err)
			}
			decoded, _ := io.ReadAll(gr)
			if !bytes.Equal(decoded, expected) {
				t.Fatalf("mismatch with two writes: expected.len=%d decoded.len=%d",
					len(expected), len(decoded))
			}
		} else {
			if !bytes.Equal(out, expected) {
				t.Fatalf("uncompressed mismatch: expected.len=%d out.len=%d",
					len(expected), len(out))
			}
		}
	})
}
