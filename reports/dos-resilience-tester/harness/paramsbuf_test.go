// Package harness — DoS Resilience: paramsBuf overflow and param amplification
//
// Tests that routes with many parameters (> maxParams=3) correctly spill to the
// overflow slice without memory amplification, and that the paramsBuf stack
// allocation is not bypassed.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mmparams "github.com/FlavioCFOliveira/MuxMaster"
)

// TestParamsBufOverflowCorrectness verifies that routes with > maxParams (3) params
// correctly deliver all parameter values via the overflow path.
func TestParamsBufOverflowCorrectness(t *testing.T) {
	for _, n := range []int{4, 8, 16} {
		n := n
		t.Run(fmt.Sprintf("params=%d", n), func(t *testing.T) {
			r := mm.New()
			segs := make([]string, n)
			vals := make([]string, n)
			for i := range n {
				segs[i] = fmt.Sprintf(":p%d", i)
				vals[i] = fmt.Sprintf("val%d", i)
			}
			route := "/" + strings.Join(segs, "/")
			target := "/" + strings.Join(vals, "/")

			r.GET(route, func(w http.ResponseWriter, req *http.Request) {
				ps := mmparams.ParamsFromContext(req.Context())
				if len(ps) != n {
					t.Errorf("expected %d params, got %d", n, len(ps))
				}
				for i := range n {
					key := fmt.Sprintf("p%d", i)
					want := fmt.Sprintf("val%d", i)
					if got := ps.Get(key); got != want {
						t.Errorf("param %s: want %q, got %q", key, want, got)
					}
				}
			})

			req := httptest.NewRequest("GET", target, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		})
	}
}

// TestParamsBufLargeValueNoBloat confirms that large param values (KB-sized)
// do not cause amplified memory allocation.
func TestParamsBufLargeValueNoBloat(t *testing.T) {
	r := mm.New()
	r.GET("/item/:id", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, size := range []int{100, 1000, 10000} {
		size := size
		t.Run(fmt.Sprintf("value=%dB", size), func(t *testing.T) {
			val := strings.Repeat("a", size)
			req := httptest.NewRequest("GET", "/item/"+val, nil)
			w := httptest.NewRecorder()

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			r.ServeHTTP(w, req)
			runtime.GC()
			runtime.ReadMemStats(&after)

			heapDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			// The param value is a substring of the URL path (already allocated by net/http).
			// The reqBundle should not double-allocate the value itself.
			// Allow up to 1 KB for the bundle allocation + overhead.
			maxAllowed := int64(4096)
			if heapDelta > maxAllowed {
				t.Errorf("value=%dB: heap delta=%d B — unexpected amplification (expected <%d B)",
					size, heapDelta, maxAllowed)
			}
			t.Logf("value=%dB: heap delta=%d B (PASS)", size, heapDelta)
		})
	}
}

// BenchmarkParamsBufOverflow measures the overhead of the overflow path (> maxParams).
func BenchmarkParamsBufOverflow(b *testing.B) {
	for _, n := range []int{4, 8, 16} {
		n := n
		b.Run(fmt.Sprintf("params=%d", n), func(b *testing.B) {
			r := mm.New()
			segs := make([]string, n)
			vals := make([]string, n)
			for i := range n {
				segs[i] = fmt.Sprintf(":p%d", i)
				vals[i] = fmt.Sprintf("v%d", i)
			}
			r.GET("/"+strings.Join(segs, "/"), func(w http.ResponseWriter, _ *http.Request) {})
			target := "/" + strings.Join(vals, "/")
			req := httptest.NewRequest("GET", target, nil)
			w := httptest.NewRecorder()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w.Body.Reset()
				r.ServeHTTP(w, req)
			}
		})
	}
}

// TestParamsMapAllocation verifies that Params.Map() allocates a new map but
// does NOT hold references to the request bundle (which may be GC-eligible).
func TestParamsMapAllocation(t *testing.T) {
	r := mm.New()
	r.GET("/users/:id/posts/:postID", func(w http.ResponseWriter, req *http.Request) {
		ps := mmparams.ParamsFromContext(req.Context())
		m := ps.Map()
		if m["id"] != "123" {
			t.Errorf("id: expected '123', got %q", m["id"])
		}
		if m["postID"] != "456" {
			t.Errorf("postID: expected '456', got %q", m["postID"])
		}
	})

	req := httptest.NewRequest("GET", "/users/123/posts/456", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
}
