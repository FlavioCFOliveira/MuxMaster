// Package dosharness contains the DoS-resilience harness for MuxMaster.
// Measures algorithmic complexity of the radix tree with pathological inputs.
package dosharness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

var emptyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

// BenchmarkTreeDepthPath fits ns/op vs tree depth.
// Expected: slope ~= k (constant per-byte cost), i.e. O(k) path length.
// A superlinear slope would indicate pathological worst-case.
func BenchmarkTreeDepthPath(b *testing.B) {
	for _, depth := range []int{10, 100, 1000, 3000, 5000} {
		depth := depth
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			r := mm.New()
			// Build a deep chain of a single literal segment repeated: /a/a/a/...
			// Every segment is the same — this exercises the worst-case
			// radix path walk (one child per node, sequential traversal).
			path := strings.Repeat("/a", depth)
			r.GET(path, emptyHandler)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkTreeCommonPrefixChain builds N routes sharing a long common
// prefix and measures lookup for the deepest.
func BenchmarkTreeCommonPrefixChain(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 5000} {
		n := n
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			r := mm.New()
			for i := range n {
				p := fmt.Sprintf("/api/common/prefix/chain/node%06d", i)
				r.GET(p, emptyHandler)
			}
			// Look up the last inserted (highest index — lowest priority in
			// the sorted index string because later insertions go to the end).
			target := fmt.Sprintf("/api/common/prefix/chain/node%06d", n-1)
			req := httptest.NewRequest(http.MethodGet, target, nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkTreeWideFanOut builds N children under a single parent.
// Measures lookup of the unluckiest (last inserted) child.
func BenchmarkTreeWideFanOut(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 5000} {
		n := n
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			r := mm.New()
			for i := range n {
				// Different first-char to force fan-out: /a000000 /b000001 ...
				// Use different first chars to guarantee unique entries in n.indices.
				// We need N unique leading chars — using /X{i:06d} where X varies.
				// With only 256 possible first bytes, N>256 reuses chars but
				// differentiates by the rest — that still creates a new child
				// since the radix tree branches on first differing byte.
				c := byte('a' + (i % 26))
				p := fmt.Sprintf("/%c%08d", c, i)
				r.GET(p, emptyHandler)
			}
			target := fmt.Sprintf("/%c%08d", byte('a'+((n-1)%26)), n-1)
			req := httptest.NewRequest(http.MethodGet, target, nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkTreeParamHeavy tests paths with many segments, each param-typed.
// Given maxInlineParams=3, paths with >3 params drop silently after the third.
// We still measure the lookup cost.
func BenchmarkTreeParamHeavy(b *testing.B) {
	for _, k := range []int{1, 2, 3, 4, 5, 8} {
		k := k
		b.Run(fmt.Sprintf("params=%d", k), func(b *testing.B) {
			r := mm.New()
			// Build pattern /:p1/:p2/.../:pk
			var sb strings.Builder
			for i := 1; i <= k; i++ {
				fmt.Fprintf(&sb, "/:p%d", i)
			}
			r.GET(sb.String(), emptyHandler)

			// Build matching path /v1/v2/...
			var sb2 strings.Builder
			for i := 1; i <= k; i++ {
				fmt.Fprintf(&sb2, "/v%dabc", i)
			}
			req := httptest.NewRequest(http.MethodGet, sb2.String(), nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkAllowedCostManyMethods measures the 405 / OPTIONS path which
// iterates all methodTrees. This is the method-dispatch pathology.
func BenchmarkAllowedCostManyMethods(b *testing.B) {
	r := mm.New()
	// Register /target under every method.
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace,
	}
	for _, m := range methods {
		r.Handle(m, "/target", emptyHandler)
	}
	// Send a method not registered at /target to miss at first, forcing
	// allowed() iteration across all trees.
	// All 9 methods are registered — use a custom "FROB" method instead to
	// trigger MethodNotAllowed which iterates each tree.
	req := httptest.NewRequest("FROB", "/target", nil)
	w := httptest.NewRecorder()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		r.ServeHTTP(w, req)
	}
}

// BenchmarkRegexCompile measures addRoute cost with progressively larger
// regex alternations. Tests H-019.
func BenchmarkRegexCompile(b *testing.B) {
	for _, n := range []int{1, 10, 100, 500, 1000} {
		n := n
		b.Run(fmt.Sprintf("alts=%d", n), func(b *testing.B) {
			// Build regex /users/{id:(aaa|aaa|...|aaa)}
			alts := make([]string, n)
			for i := range alts {
				alts[i] = "aaa"
			}
			pattern := "/users/{id:" + strings.Join(alts, "|") + "}"
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r := mm.New()
				r.GET(pattern, emptyHandler)
			}
		})
	}
}

// BenchmarkCleanPathDeep measures RedirectFixedPath cost.
func BenchmarkCleanPathDeep(b *testing.B) {
	r := mm.New()
	r.GET("/a/b/c", emptyHandler)

	// Request with many . segments that path.Clean must normalise.
	for _, depth := range []int{10, 100, 1000} {
		depth := depth
		b.Run(fmt.Sprintf("dots=%d", depth), func(b *testing.B) {
			var sb strings.Builder
			for range depth {
				sb.WriteString("/./.")
			}
			sb.WriteString("/a/b/c")
			req := httptest.NewRequest(http.MethodGet, sb.String(), nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}
