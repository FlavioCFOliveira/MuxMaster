package dosharness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// readHeapAlloc returns HeapAlloc (currently-allocated, not-yet-GC'd bytes).
func readHeapAlloc() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestCompressBufferGrowsWithBody validates H-006: compress.go buffers the
// full response body in g.buf before writing any byte to the wire.
//
// Strategy: we pause the handler mid-write using a sync primitive, then
// sample HeapAlloc and observe g.buf must be at or near `bodySize`.
// We do NOT rely on naive before/after GC tricks — during a single
// running handler, HeapAlloc is the live delta.
func TestCompressBufferGrowsWithBody(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates large buffers; skipped in -short")
	}

	sizes := []int{1 << 20, 4 << 20, 16 << 20, 64 << 20}

	type result struct {
		size    int
		peakMem uint64
	}
	results := make([]result, 0, len(sizes))

	for _, size := range sizes {
		r := mm.New()
		r.Use(middleware.Compress(5))

		// Pause handler just before returning — this is the moment where
		// g.buf holds the full body; compress.go has not yet flushed.
		var handlerDone sync.WaitGroup
		handlerDone.Add(1)
		measureNow := make(chan uint64, 1)
		release := make(chan struct{})

		r.GET("/big", func(w http.ResponseWriter, req *http.Request) {
			chunk := make([]byte, 65536)
			remaining := size
			for remaining > 0 {
				n := len(chunk)
				if n > remaining {
					n = remaining
				}
				_, _ = w.Write(chunk[:n])
				remaining -= n
			}
			// Sample heap while g.buf is alive and not yet flushed.
			runtime.GC() // force housekeeping; g.buf still held by grw
			measureNow <- readHeapAlloc()
			<-release
			handlerDone.Done()
		})

		req := httptest.NewRequest(http.MethodGet, "/big", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		go func() {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			// Discard response body memory — not our concern.
			w.Body.Reset()
		}()

		peak := <-measureNow
		close(release)
		handlerDone.Wait()

		results = append(results, result{size: size, peakMem: peak})
		t.Logf("bodySize=%dMB peakHeapAllocDuringHandler=%dMB ratio=%.2f", size>>20, peak>>20, float64(peak)/float64(size))

		runtime.GC()
		runtime.GC()
	}

	// Validate the slope: peak memory growth should be >= bodySize.
	biggest := results[len(results)-1]
	smallest := results[0]
	deltaBody := biggest.size - smallest.size
	deltaMem := int64(biggest.peakMem) - int64(smallest.peakMem)
	slope := float64(deltaMem) / float64(deltaBody)

	t.Logf("linear-fit slope (bytes of heap per byte of body) = %.2f (expected >= 1.0 for buffering; ~0 for streaming)", slope)

	// Hard assert slope > 0.8: buffering confirms the OOM vector.
	if slope < 0.8 {
		t.Errorf("FAIL: expected slope >= 0.8 (linear buffering) but got %.2f", slope)
	}
}

// BenchmarkCompressBufferGrowth tracks allocations per MB of response body.
// A streaming compressor would be ~0 alloc/MB. MuxMaster's current
// implementation is O(body_size).
func BenchmarkCompressBufferGrowth(b *testing.B) {
	bodySize := 1 << 20 // 1MB per request
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = byte(i)
	}

	r := mm.New()
	r.Use(middleware.Compress(5))
	r.GET("/big", func(w http.ResponseWriter, req *http.Request) {
		remaining := bodySize
		for remaining > 0 {
			n := len(chunk)
			if n > remaining {
				n = remaining
			}
			_, _ = w.Write(chunk[:n])
			remaining -= n
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(bodySize))
	for range b.N {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
}

// TestCompressBufferNeverFlushedForSmallBody verifies the intended behavior:
// if the accumulated body is < minCompressSize (1024), the middleware writes
// it uncompressed. No security concern but documents the behaviour.
func TestCompressBufferSmallBodyBehavior(t *testing.T) {
	r := mm.New()
	r.Use(middleware.Compress(5))
	r.GET("/small", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("short response"))
	})

	req := httptest.NewRequest(http.MethodGet, "/small", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Encoding"); got == "gzip" {
		t.Errorf("expected no gzip encoding for small body, got %q", got)
	}
	t.Logf("small body response length=%d, Content-Encoding=%q", w.Body.Len(), w.Header().Get("Content-Encoding"))
}
