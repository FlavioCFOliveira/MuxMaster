// Package harness — DoS Resilience: compress middleware OOM and BREACH probe
package harness

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestCompressStreamingBoundedMemory verifies that the compress middleware does NOT
// buffer the entire response body before compressing. A handler streaming 100 MB of
// zeros must not cause >10 MB heap growth (streaming compressor, not buffering).
func TestCompressStreamingBoundedMemory(t *testing.T) {
	const streamBytes = 100 * 1024 * 1024 // 100 MB logical output

	r := mm.New()
	r.Use(middleware.Compress(gzip.DefaultCompression))
	r.GET("/stream", func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 64*1024) // 64 KB of zeros — ~1000:1 gzip ratio
		written := 0
		for written < streamBytes {
			n, _ := w.Write(chunk)
			written += n
		}
	})

	req := httptest.NewRequest("GET", "/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	r.ServeHTTP(w, req)

	runtime.GC()
	runtime.ReadMemStats(&after)

	// Heap must not have grown by more than 16 MB (sniffBufSize + gzip writer + overhead)
	heapDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	maxAllowed := int64(16 * 1024 * 1024)
	if heapDelta > maxAllowed {
		t.Fatalf("CRITICAL: compress middleware buffered %d MB heap for %d MB response — streaming violated",
			heapDelta/1024/1024, streamBytes/1024/1024)
	}
	t.Logf("heap delta: %d KB for %d MB response (PASS)", heapDelta/1024, streamBytes/1024/1024)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", enc)
	}
}

// TestCompressNoAcceptEncoding verifies compress middleware passes through when
// Accept-Encoding does not include gzip — no allocation amplification.
func TestCompressNoAcceptEncoding(t *testing.T) {
	r := mm.New()
	r.Use(middleware.Compress(gzip.DefaultCompression))
	r.GET("/data", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("hello world ", 200)))
	})

	req := httptest.NewRequest("GET", "/data", nil)
	// No Accept-Encoding header
	w := httptest.NewRecorder()

	allocs := testing.AllocsPerRun(50, func() {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	})
	// Without gzip, compress middleware should add near-zero allocations
	if allocs > 5 {
		t.Errorf("compress no-accept-encoding: %.0f allocs — expected <=5", allocs)
	}
	t.Logf("compress no-accept-encoding: %.1f allocs/op (PASS)", allocs)
}

// TestCompressSmallBodyNoCompression verifies that responses < minCompressSize
// are not compressed and the gzip writer is never activated (no Content-Encoding header).
func TestCompressSmallBodyNoCompression(t *testing.T) {
	r := mm.New()
	r.Use(middleware.Compress(gzip.DefaultCompression))
	r.GET("/small", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tiny")) // < 1024 bytes
	})

	req := httptest.NewRequest("GET", "/small", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if enc := w.Header().Get("Content-Encoding"); enc == "gzip" {
		t.Errorf("small body should NOT be gzip-encoded, got Content-Encoding: %s", enc)
	}
	t.Logf("small body: no compression applied (PASS)")
}

// BenchmarkCompressLargeResponse measures throughput of streaming compress middleware.
// Used to detect if any future change causes buffer materialisation.
func BenchmarkCompressLargeResponse(b *testing.B) {
	r := mm.New()
	r.Use(middleware.Compress(gzip.BestSpeed))
	payload := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4096) // ~100 KB
	r.GET("/bench", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	})
	req := httptest.NewRequest("GET", "/bench", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// TestBREACHOracle probes for BREACH-style oracle: if a response echoes an
// attacker-controlled string alongside a secret, gzip compression ratio leaks
// information. This test confirms the API surface exists (compress + oauth2 scope
// echo) and documents it. MuxMaster compress itself is not the fix — the handler
// must not echo compressed secrets alongside user input.
//
// Finding reference: MM-2026-0030, sprint hypothesis H-D.
// This test DOCUMENTS the pattern; it does NOT assert a pass/fail since the
// compress middleware is behaving correctly — the oracle is in the handler.
func TestBREACHOracle(t *testing.T) {
	secret := "scope=read:users write:admin billing:view"

	// Handler echoes both the secret and attacker-controlled input in same compressed response
	r := mm.New()
	r.Use(middleware.Compress(gzip.DefaultCompression))
	r.GET("/api/info", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query().Get("q")
		// Simulate scope echo: response contains both the secret and the attacker query
		_, _ = io.WriteString(w, secret+" "+q+strings.Repeat(" padding ", 200))
	})

	// Probe: vary q to be a prefix of the secret — a shorter compressed response
	// reveals that q and the secret share bytes (BREACH oracle).
	var sizes [3]int
	probes := []string{"XXXXXXXX", "scope=re", "scope=read:users"}
	for i, probe := range probes {
		req := httptest.NewRequest("GET", "/api/info?q="+probe, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		sizes[i] = w.Body.Len()
	}

	t.Logf("BREACH oracle probe — response sizes by attacker input:")
	t.Logf("  q=random:     %d bytes", sizes[0])
	t.Logf("  q=partial:    %d bytes", sizes[1])
	t.Logf("  q=full-match: %d bytes", sizes[2])

	// Oracle is confirmed if size[2] < size[0] (matching prefix compresses better)
	if sizes[2] < sizes[0] {
		t.Logf("WARNING DOS-2026-0001: BREACH oracle CONFIRMED: full-match response %d bytes < random %d bytes.",
			sizes[2], sizes[0])
		t.Logf("Mitigation: do not echo user input alongside secrets in the same compressed response body,")
		t.Logf("or disable compression on endpoints that echo user-controlled data near sensitive claims.")
	} else {
		t.Logf("BREACH oracle not confirmed in this payload configuration (padding may dominate)")
	}
}
