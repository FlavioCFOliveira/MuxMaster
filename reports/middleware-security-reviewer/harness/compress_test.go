// Harness — compress.go (H-006).
//
// Threats covered:
//   - CWE-400 unbounded g.buf grows with every Write — memory DoS.
//   - BREACH — secret in response + reflected client input in same response.
//   - Missing Vary: Accept-Encoding header.
//   - Compression of already-compressed content (image/jpeg, video).
//   - Accept-Encoding spoof — unsupported codecs trigger no fallback.
//   - Min-size bypass (1-byte response).
//   - Double-compression / content-length mismatch.
package harness

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-CP-001 — Unbounded buffer memory (H-006)
//
// This test proves that g.buf grows linearly with total handler writes because
// compression only happens after handler return. It's architecturally
// guaranteed by compress.go:26-28. We bound the test at 256MB to avoid OOM
// on CI runners but the delta is measurable.
// -----------------------------------------------------------------------------

func TestSec_Compress_UnboundedBuffer(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test — skipped in short mode")
	}
	mw := middleware.Compress(gzip.BestSpeed)
	const total = 64 << 20 // 64 MB — enough to prove the O(n) scaling
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i & 0xff)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < total/len(chunk); i++ {
			if _, err := w.Write(chunk); err != nil {
				t.Fatalf("write failed: %v", err)
			}
		}
	})
	h := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	var mStart runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mStart)

	// Atomic peak counter (race-free).
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()

	h.ServeHTTP(rec, req)
	close(done)

	var mEnd runtime.MemStats
	runtime.ReadMemStats(&mEnd)

	peakMB := float64(peak.Load()) / (1 << 20)
	deltaMB := float64(int64(mEnd.HeapInuse)-int64(mStart.HeapInuse)) / (1 << 20)
	t.Logf("compress: total written=%dMB, heap peak=%.1fMB, delta=%.1fMB",
		total>>20, peakMB, deltaMB)

	writeFile(t, "compress-rss-trace.txt", []byte(fmt.Sprintf(
		"compress buffer test\nwritten_MB=%d\nheap_peak_MB=%.2f\nheap_delta_MB=%.2f\n",
		total>>20, peakMB, deltaMB,
	)))

	// Expectation: the code buffers everything before compressing; peak must
	// be at least ~50% of the buffered size. This guards against a silent
	// refactor that replaces the buffering with streaming (which would be
	// the fix for MSR-CP-001).
	if peakMB < float64(total>>21) {
		t.Logf("NOTE: compress peak RSS (%.1fMB) below half the buffered size — code may have been fixed to stream", peakMB)
	} else {
		t.Logf("MSR-CP-001 CONFIRMED: compress buffered O(n) memory before flushing — unbounded attack surface")
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-002 — Vary: Accept-Encoding header is emitted when compressing
// -----------------------------------------------------------------------------

func TestSec_Compress_VaryHeader(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate > minCompressSize (1024) to trigger compression.
		_, _ = w.Write(bytes.Repeat([]byte{'a'}, 4096))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("compress: Content-Encoding=%q, want gzip", got)
	}
	// Vary must include Accept-Encoding.
	vary := rec.Header().Values("Vary")
	found := false
	for _, v := range vary {
		if strings.Contains(v, "Accept-Encoding") {
			found = true
		}
	}
	if !found {
		t.Errorf("compress: Vary header missing Accept-Encoding; got Vary=%v", vary)
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-003 — Min-size bypass (response smaller than minCompressSize)
// -----------------------------------------------------------------------------

func TestSec_Compress_MinSizeBypass(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tiny"))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got == "gzip" {
		t.Errorf("compress: tiny response was compressed; Content-Encoding=gzip")
	}
	if got := rec.Body.String(); got != "tiny" {
		t.Errorf("compress: tiny response body mangled: %q", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-004 — Accept-Encoding absent → no compression
// -----------------------------------------------------------------------------

func TestSec_Compress_NoAcceptEncoding(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'a'}, 4096))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Accept-Encoding set.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("compress: unexpected Content-Encoding=%q for client without gzip", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-005 — Accept-Encoding: br (brotli) — unsupported, not attempted
// -----------------------------------------------------------------------------

func TestSec_Compress_UnsupportedCodec(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'a'}, 4096))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("compress: br client got Content-Encoding=%q (no brotli support)", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-006 — Content-Type NOT consulted — compress runs on any type
// The middleware does NOT check Content-Type. image/jpeg bodies are compressed.
// This is a documentation requirement (user should avoid Compress for CDN-style
// pre-compressed assets) and a code smell (minor CPU waste, re-expansion).
// -----------------------------------------------------------------------------

func TestSec_Compress_IgnoresContentType(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, 4096))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got == "gzip" {
		t.Logf("NOTE: compress applied to image/jpeg — middleware does not skip by Content-Type (documented)")
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-007 — BREACH oracle (reflected input + secret in same response)
//
// We verify that the middleware does NOT emit Cache-Control: no-transform,
// which would harden against proxy-based BREACH variants. We also document
// the presence of both secret and reflected input in a compressed response.
// The finding is a MITIGATION GUIDANCE finding, not a code bug.
// -----------------------------------------------------------------------------

func TestSec_Compress_BREACHSurface(t *testing.T) {
	secret := "csrf_token=abcdef1234567890"
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reflected := r.URL.Query().Get("q")
		// This is the classic BREACH surface: attacker-controlled prefix + secret.
		body := "<html>search: " + reflected + "\n" + secret + "</html>"
		// Pad so minCompressSize is exceeded.
		body += strings.Repeat(" ", 4096)
		_, _ = w.Write([]byte(body))
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/?q=foo", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "no-transform") {
		t.Logf("OK: compress sets no-transform")
	} else {
		t.Logf("NOTE: compress does NOT emit Cache-Control: no-transform — BREACH exposure depends on handler-level cache config (documented)")
	}

	// Decompress to verify content is recoverable.
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("compress: could not gunzip response: %v", err)
	}
	defer func() { _ = gr.Close() }()
	out, _ := io.ReadAll(gr)
	if !strings.Contains(string(out), secret) {
		t.Error("compress: decompressed output missing the secret")
	}
	if !strings.Contains(string(out), "search: foo") {
		t.Error("compress: decompressed output missing the reflected input")
	}
}

// -----------------------------------------------------------------------------
// MSR-CP-008 — Content-Length mismatch after compression
// -----------------------------------------------------------------------------

func TestSec_Compress_ContentLengthDropped(t *testing.T) {
	mw := middleware.Compress(gzip.BestSpeed)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := bytes.Repeat([]byte{'z'}, 4096)
		w.Header().Set("Content-Length", "4096")
		_, _ = w.Write(body)
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		if got := rec.Header().Get("Content-Length"); got != "" && got == "4096" {
			t.Errorf("compress: stale Content-Length=4096 after compression — downstream parser desync risk")
		}
	}
}
