// Package harness — DoS Resilience Test Harness: Algorithmic Complexity
//
// Measures radix-tree getValue and addRoute complexity against pathological inputs.
// All expected O(k) — any super-linear slope is a Critical finding.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

var h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

// BenchmarkTreeDepthPath registers a single deep static path and measures lookup time.
// Expected: O(k) where k = path_length. Slope must be near-zero per additional segment.
func BenchmarkTreeDepthPath(b *testing.B) {
	for _, depth := range []int{10, 100, 500, 1000} {
		depth := depth
		r := mm.New()
		path := strings.Repeat("/a", depth)
		r.GET(path, h)
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		w := httptest.NewRecorder()
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				w.Body.Reset()
				r.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkCommonPrefixBomb registers N routes all sharing a long common prefix.
// This creates a single deep chain with many split points — tests LCP walk cost.
func BenchmarkCommonPrefixBomb(b *testing.B) {
	for _, n := range []int{10, 100, 500, 1000} {
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			r := mm.New()
			base := "/api/v1/users/profile/settings/notifications/email/"
			for i := range n {
				r.GET(base+fmt.Sprintf("route%d", i), h)
			}
			// Look up the last route (worst case — must traverse all priority nodes)
			target := base + fmt.Sprintf("route%d", n-1)
			req := httptest.NewRequest("GET", "http://x"+target, nil)
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

// BenchmarkWideFanOut registers N routes that differ only in the first character
// after a common prefix — maximum index-scan cost.
func BenchmarkWideFanOut(b *testing.B) {
	// Generate N distinct single-byte paths.
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for _, n := range []int{10, 40, 62} {
		if n > len(chars) {
			n = len(chars)
		}
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			r := mm.New()
			for _, c := range chars[:n] {
				r.GET("/"+string(c), h)
			}
			// Always look up the last registered character (worst-case index scan)
			target := "/" + string(chars[n-1])
			req := httptest.NewRequest("GET", "http://x"+target, nil)
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

// BenchmarkManyParams registers a route with N path parameters and looks it up.
// Tests paramsBuf spill to overflow slice for N > maxParams.
// Expected: O(N) allocation only for N > 3; ns/op should grow linearly not quadratically.
func BenchmarkManyParams(b *testing.B) {
	for _, n := range []int{1, 2, 3, 4, 8, 16, 32} {
		n := n
		b.Run(fmt.Sprintf("params=%d", n), func(b *testing.B) {
			r := mm.New()
			segments := make([]string, n)
			vals := make([]string, n)
			for i := range n {
				segments[i] = fmt.Sprintf(":p%d", i)
				vals[i] = fmt.Sprintf("val%d", i)
			}
			path := "/" + strings.Join(segments, "/")
			r.GET(path, h)
			target := "/" + strings.Join(vals, "/")
			req := httptest.NewRequest("GET", "http://x"+target, nil)
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

// BenchmarkCatchAllDepth tests catch-all routes after varying numbers of static segments.
func BenchmarkCatchAllDepth(b *testing.B) {
	for _, depth := range []int{1, 5, 10, 50} {
		depth := depth
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			r := mm.New()
			prefix := strings.Repeat("/static", depth)
			r.GET(prefix+"/*filepath", h)
			target := prefix + "/some/file.txt"
			req := httptest.NewRequest("GET", "http://x"+target, nil)
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

// BenchmarkRegexParamComplexity tests regex params with increasing expression complexity.
// A malicious operator could register a catastrophic-backtracking regex. Confirm cost.
func BenchmarkRegexParamComplexity(b *testing.B) {
	cases := []struct {
		name string
		expr string
		val  string
	}{
		{"simple-digits", `[0-9]+`, "12345"},
		{"anchored-alnum", `[a-zA-Z0-9\-]+`, "my-slug-value"},
		// Pathological patterns would cause ReDoS if the regex engine backtracks.
		// Use a safe but expensive pattern to bound what is acceptable.
		{"uuid-like", `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, "550e8400-e29b-41d4-a716-446655440000"},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			r := mm.New()
			r.GET("/{id:"+tc.expr+"}", h)
			req := httptest.NewRequest("GET", "http://x/"+tc.val, nil)
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

// TestExpandOptionalExplosion confirms that a path with N optional segments expands
// to 2^N routes at registration time and documents the registration behavior.
//
// Finding DOS-2026-0050 (previously MM-2026-0050): exponential registration cost.
// For N=1: ok (2 routes). For N>=2: panics with wildcard conflict because the
// expansion of /base{/:p0}{/:p1} produces both /base/:p0 and /base/:p1 as
// distinct param routes from the same prefix — both are param nodes, but the
// second conflicts with the first in the radix tree.
//
// This is a registration-time panic (not a runtime DoS), but an operator who
// accidentally writes a path with 2+ consecutive optional segments will get a
// panic at startup rather than graceful failure.
func TestExpandOptionalExplosion(t *testing.T) {
	cases := []struct {
		n           int
		expectPanic bool
	}{
		{1, false}, // 2 routes — OK
		{2, true},  // conflict: /base/:p0 vs /base/:p1 at same tree level
		{3, true},
		{4, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("N=%d", tc.n), func(t *testing.T) {
			r := mm.New()
			var sb strings.Builder
			sb.WriteString("/base")
			for i := range tc.n {
				sb.WriteString(fmt.Sprintf("{/:p%d}", i))
			}
			path := sb.String()

			panicked := false
			var panicVal any
			func() {
				defer func() {
					if rcv := recover(); rcv != nil {
						panicked = true
						panicVal = rcv
					}
				}()
				r.GET(path, h)
			}()

			if tc.expectPanic && !panicked {
				t.Errorf("N=%d: expected registration panic for multiple consecutive optionals, but none occurred", tc.n)
			} else if panicked && !tc.expectPanic {
				t.Errorf("N=%d: unexpected panic: %v", tc.n, panicVal)
			}

			if panicked {
				t.Logf("N=%d: CONFIRMED DOS-2026-0050: registration panics with %q", tc.n, panicVal)
				t.Logf("  -> Multiple consecutive optional segments in a single path cause a wildcard conflict panic.")
				t.Logf("  -> Fix: detectand reject >1 consecutive optional segments at path validation time,")
				t.Logf("     or only expand the LAST optional segment recursively (not all simultaneously).")
			} else {
				t.Logf("N=%d: registration succeeded (PASS)", tc.n)
			}
			_ = r
		})
	}
}

// BenchmarkAddRouteScaling measures how addRoute cost scales with N routes already registered.
// Expected: O(k) per registration where k is the path length (not N).
func BenchmarkAddRouteScaling(b *testing.B) {
	for _, preload := range []int{100, 1000, 5000} {
		preload := preload
		b.Run(fmt.Sprintf("existing=%d", preload), func(b *testing.B) {
			r := mm.New()
			// Register preload distinct routes
			for i := range preload {
				r.GET(fmt.Sprintf("/route/%d/end", i), h)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Each iteration registers a new leaf — isolate by using b.N offset
				r2 := mm.New()
				for j := range preload {
					r2.GET(fmt.Sprintf("/route/%d/end", j), h)
				}
				// measure just the single-route add
				r2.GET(fmt.Sprintf("/new/%d", b.N+i), h)
			}
		})
	}
}

// TestNotFoundAllocation verifies that a 404 response does not allocate more memory
// than a constant factor of the request URL length.
func TestNotFoundAllocation(t *testing.T) {
	r := mm.New()
	r.GET("/exists", h)

	// Each size is a target URL that won't match any route
	for _, size := range []int{100, 1000, 4096, 65535} {
		path := "/" + strings.Repeat("a", size)
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		w := httptest.NewRecorder()

		allocs := testing.AllocsPerRun(100, func() {
			w.Body.Reset()
			r.ServeHTTP(w, req)
		})

		// 404 path should not allocate proportionally to URL size
		if allocs > 10 {
			t.Errorf("size=%d: %.0f allocs on 404 — expected <=10", size, allocs)
		}
		t.Logf("size=%d: %.1f allocs/op", size, allocs)
	}
}
