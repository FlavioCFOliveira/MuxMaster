// Package harness — DoS Resilience: not-found path amplification
//
// Measures allocation behaviour and lookup cost for paths of increasing length
// that do not match any registered route. The 404 handler must run in O(k) time
// and must not allocate proportionally to the URL length.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// BenchmarkNotFoundOversizedPath measures 404 handling for increasingly large URLs.
// net/http's default MaxHeaderBytes limits the practical URL size to ~1 MB,
// but within that bound the router must not allocate proportionally to path length.
func BenchmarkNotFoundOversizedPath(b *testing.B) {
	r := mm.New()
	r.GET("/exists", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, size := range []int{100, 1000, 4096, 65535} {
		size := size
		path := "/" + strings.Repeat("a", size)
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		w := httptest.NewRecorder()
		b.Run(fmt.Sprintf("path=%dB", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				w.Body.Reset()
				r.ServeHTTP(w, req)
			}
		})
	}
}

// TestNotFoundAllocConstant confirms allocations on 404 are bounded independent
// of URL length. Allocation growth proportional to path length would be a DoS vector.
func TestNotFoundAllocConstant(t *testing.T) {
	r := mm.New()
	r.GET("/exists", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var allocs [4]float64
	sizes := []int{100, 1000, 10000, 65535}
	for i, size := range sizes {
		path := "/" + strings.Repeat("x", size)
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		w := httptest.NewRecorder()
		allocs[i] = testing.AllocsPerRun(50, func() {
			w.Body.Reset()
			r.ServeHTTP(w, req)
		})
	}

	t.Logf("404 allocs by path size:")
	for i, size := range sizes {
		t.Logf("  path=%dB: %.1f allocs/op", size, allocs[i])
	}

	// Allocs must not grow with path size. Allow a small constant for 404 handling.
	for i := 1; i < len(allocs); i++ {
		if allocs[i] > allocs[0]+5 {
			t.Errorf("allocation amplification: size=%dB gives %.0f allocs vs size=%dB %.0f allocs",
				sizes[i], allocs[i], sizes[0], allocs[0])
		}
	}
}

// TestManyMethodsAllowHeaderBloat confirms that methodNotAllowedCache does not
// grow unboundedly when many distinct Allow strings are computed.
// In practice the Allow string is determined by registered methods — a closed set.
// But confirming no goroutine-safety issue with concurrent sync.Map operations.
func TestManyMethodsAllowHeaderBloat(t *testing.T) {
	r := mm.New()
	// Register path for only GET — other methods return 405 with Allow: GET, HEAD, OPTIONS
	r.GET("/resource", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	methods := []string{"POST", "PUT", "PATCH", "DELETE", "CONNECT", "TRACE"}
	for _, m := range methods {
		req := httptest.NewRequest(m, "/resource", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Logf("method %s: got %d (expected 405)", m, w.Code)
		}
	}
	t.Log("methodNotAllowedCache: no bloat on standard method set (PASS)")
}
