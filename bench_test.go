package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// nopHandler is a zero-cost http.Handler used in benchmarks.
var nopHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {}

// newBenchMux builds a realistic mux for benchmarks.
func newBenchMux() *muxmaster.Mux {
	m := muxmaster.New()

	// Static routes
	m.GET("/", nopHandler)
	m.GET("/users", nopHandler)
	m.GET("/users/list", nopHandler)
	m.GET("/users/search", nopHandler)
	m.POST("/users", nopHandler)
	m.GET("/products", nopHandler)
	m.GET("/products/featured", nopHandler)
	m.POST("/products", nopHandler)
	m.GET("/health", nopHandler)
	m.GET("/metrics", nopHandler)

	// Param routes
	m.GET("/users/:id", nopHandler)
	m.PUT("/users/:id", nopHandler)
	m.DELETE("/users/:id", nopHandler)
	m.GET("/users/:id/posts", nopHandler)
	m.GET("/users/:id/posts/:pid", nopHandler)
	m.GET("/products/:id", nopHandler)
	m.PUT("/products/:id", nopHandler)
	m.GET("/orgs/:org/repos/:repo/issues/:num", nopHandler)

	// Wildcard routes
	m.GET("/static/*filepath", nopHandler)
	m.GET("/docs/*path", nopHandler)

	return m
}

var benchReq = func(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

// BenchmarkStaticRoute measures lookup of a route with no parameters.
func BenchmarkStaticRoute(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/list")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute1 measures lookup with one path parameter.
func BenchmarkParamRoute1(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute2 measures lookup with two path parameters.
func BenchmarkParamRoute2(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42/posts/7")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute3 measures lookup with three path parameters.
func BenchmarkParamRoute3(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/orgs/acme/repos/api/issues/123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkWildcardRoute measures lookup with a catch-all parameter.
func BenchmarkWildcardRoute(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/static/css/main.min.css")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkNotFound measures 404 path.
func BenchmarkNotFound(b *testing.B) {
	m := newBenchMux()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = false
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/this/path/does/not/exist")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParallelStaticRoute measures concurrent static route lookup.
func BenchmarkParallelStaticRoute(b *testing.B) {
	m := newBenchMux()
	r := benchReq(http.MethodGet, "/products/featured")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// BenchmarkParallelParamRoute measures concurrent param route lookup.
func BenchmarkParallelParamRoute(b *testing.B) {
	m := newBenchMux()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// newFastBenchMux builds a Mux using HandleFast for the benchmarked routes.
func newFastBenchMux() *muxmaster.Mux {
	nopFast := func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {}
	m := muxmaster.New()

	// Static routes — same as newBenchMux
	m.GET("/", nopHandler)
	m.GET("/users", nopHandler)
	m.GET("/users/list", nopHandler)
	m.GET("/users/search", nopHandler)
	m.POST("/users", nopHandler)
	m.GET("/products", nopHandler)
	m.GET("/products/featured", nopHandler)
	m.POST("/products", nopHandler)
	m.GET("/health", nopHandler)
	m.GET("/metrics", nopHandler)

	// Param routes registered as FastHandler
	m.GETFast("/users/:id", nopFast)
	m.PUTFast("/users/:id", nopFast)
	m.DELETEFast("/users/:id", nopFast)
	m.GETFast("/users/:id/posts", nopFast)
	m.GETFast("/users/:id/posts/:pid", nopFast)
	m.GETFast("/products/:id", nopFast)
	m.PUTFast("/products/:id", nopFast)
	m.GETFast("/orgs/:org/repos/:repo/issues/:num", nopFast)

	// Wildcard routes as FastHandler
	m.GETFast("/static/*filepath", nopFast)
	m.GETFast("/docs/*path", nopFast)

	return m
}

// BenchmarkFastStaticRoute measures ServeHTTP for a static fast route.
// Static routes use the same path as regular Handle routes — 0 allocs.
func BenchmarkFastStaticRoute(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/list")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute1 measures HandleFast dispatch with one path parameter.
// Target: 0 allocs, ≤42 ns.
func BenchmarkFastParamRoute1(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute2 measures HandleFast dispatch with two path parameters.
func BenchmarkFastParamRoute2(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42/posts/7")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute3 measures HandleFast dispatch with three path parameters.
func BenchmarkFastParamRoute3(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/orgs/acme/repos/api/issues/123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParallelParamRoute measures concurrent HandleFast param dispatch.
func BenchmarkFastParallelParamRoute(b *testing.B) {
	m := newFastBenchMux()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// newPooledBenchMux is newBenchMux with PoolRequestBundle enabled (Opt O13).
func newPooledBenchMux() *muxmaster.Mux {
	m := newBenchMux()
	m.PoolRequestBundle = true
	return m
}

// BenchmarkPooledParamRoute1 measures Opt O13 (PoolRequestBundle) with 1 param.
func BenchmarkPooledParamRoute1(b *testing.B) {
	m := newPooledBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkPooledParamRoute2 measures Opt O13 with 2 params.
func BenchmarkPooledParamRoute2(b *testing.B) {
	m := newPooledBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42/posts/7")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkPooledParamRoute3 measures Opt O13 with 3 params.
func BenchmarkPooledParamRoute3(b *testing.B) {
	m := newPooledBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/orgs/acme/repos/api/issues/123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkPooledWildcardRoute measures Opt O13 with a catch-all param.
func BenchmarkPooledWildcardRoute(b *testing.B) {
	m := newPooledBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/static/css/main.min.css")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkPooledParallelParamRoute measures Opt O13 concurrently.
func BenchmarkPooledParallelParamRoute(b *testing.B) {
	m := newPooledBenchMux()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// BenchmarkRegisterRoutes measures route registration cost — the path
// copying strategy of performance.md §36 makes each Handle() call O(depth)
// instead of O(tree size) ([waste-hunt WH-08]), so total registration cost
// for N routes should scale close to linearly in N rather than quadratically.
// ns/route is reported explicitly so N=100/1000/5000 are directly comparable.
func BenchmarkRegisterRoutes(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run("N="+strconv.Itoa(n), func(b *testing.B) {
			paths := make([]string, n)
			for i := range n {
				switch i % 3 {
				case 0:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items"
				case 1:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items/:id"
				default:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items/:id/sub/:sid"
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m := muxmaster.New()
				for _, p := range paths {
					m.GET(p, nopHandler)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/route")
		})
	}
}

// benchFS is a tiny in-memory filesystem used by the ServeFiles benchmarks
// below, so they measure (*Mux).ServeFiles / (*Group).ServeFiles directly
// (not a hand-written replica) without touching the real filesystem.
var benchFS = fstest.MapFS{
	"css/style.css": {Data: []byte("body{color:red}")},
}

// BenchmarkServeFiles measures (*Mux).ServeFiles directly — the shallow
// request copy replacing r.Clone ([waste-hunt WH-04]).
func BenchmarkServeFiles(b *testing.B) {
	m := muxmaster.New()
	m.ServeFiles("/assets/*filepath", http.FS(benchFS))
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/assets/css/style.css")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkGroupServeFiles measures (*Group).ServeFiles directly — the same
// shallow-copy fix applied in group.go ([waste-hunt WH-04]).
func BenchmarkGroupServeFiles(b *testing.B) {
	m := muxmaster.New()
	g := m.Group("/static")
	g.ServeFiles("/*filepath", http.FS(benchFS))
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/static/css/style.css")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// buildAdversarialBacktrackMux builds a tree engineered so a lookup for
// "/a" + "/x"*depth + "/REALEND" (or, when the target is unreachable — see
// buildAdversarialBacktrackMux's beyondCap use in the correctness test —
// "/a" + "/x"*depth) forces a genuine NESTED static-vs-param fork at every
// one of `depth` levels along a single all-static descent (rmp #259 /
// DIV-001 DoS bound):
//
//   - A pure-static "dead end" spine "/a/x/x/.../x/STATICEND" (depth copies
//     of the literal segment "x") forces indices="x" — a real static
//     child — at every one of the depth tree levels along the path the
//     request walks.
//   - At every depth k (0..depth-1), a param alternative lives at the SAME
//     node as the k-th static "x" child: reusing the name "p1" at k=0 (the
//     position genuinely shared with the real, all-param target chain) and
//     a fresh name "rK" for k>=1 (a position only reachable through the
//     static spine, so no name collision with the real chain). Each of
//     these param alternatives itself requires one more literal segment
//     ("/NEVERMATCHk") the request never supplies, so it's a dead end too.
//
// A request of `depth` literal "x" segments matches the static child at
// every level, so getValue commits to static depth times in a row, pushing
// a backtrack frame at every level — only discovering the dead end at the
// very bottom (STATICEND doesn't match), then unwinding: each of the depth
// abandoned param alternatives ("/:rK/NEVERMATCHk") dead-ends one segment
// below its own fork, so exploring it costs O(1), not O(depth-k). Total
// cost is therefore O(depth): the initial descent (O(depth)) plus depth
// O(1) dead-end retries plus the final O(depth) walk down the real
// all-param chain once the shallowest (level-0) fork is reached. See
// TestDeepForkBacktracking_LinearNotExponential (tree_backtrack_bound_test.go)
// for the correctness side of this construction — every registered depth
// must now be FOUND (no cap silently drops fallback frames any more, see
// backtrackStack's "why no cap is needed" proof in tree.go) — and
// reports/perf-lab-2026-09-24/waste-hunt/results/fixes/260.txt for the
// measured ns/op growth (linear in depth).
//
// buildQuadraticBacktrackMux below is a harsher construction where each
// abandoned branch itself costs O(depth-k) instead of O(1), to empirically
// exercise the general (tree-size-bounded, polynomial-not-exponential)
// case rather than this construction's best-case-linear one.
func buildAdversarialBacktrackMux(depth int) (*muxmaster.Mux, string) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false

	spine := "/a" + strings.Repeat("/x", depth) + "/STATICEND"
	m.GET(spine, nopHandler)

	for k := 0; k < depth; k++ {
		paramName := fmt.Sprintf("r%d", k)
		if k == 0 {
			paramName = "p1" // shared position with the real chain's first param
		}
		pattern := "/a" + strings.Repeat("/x", k) + fmt.Sprintf("/:%s/NEVERMATCH%d", paramName, k)
		m.GET(pattern, nopHandler)
	}

	var real strings.Builder
	real.WriteString("/a")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&real, "/:p%d", i)
	}
	m.GET(real.String(), nopHandler)

	return m, "/a" + strings.Repeat("/x", depth)
}

// BenchmarkAdversarialBacktracking measures lookup cost on the adversarial
// tree above across a range of depths, now well past the old (removed)
// backtrackDepth=8 ceiling. ns/op must grow linearly in depth, never
// exponentially — the DoS bound backtrackStack's "why no cap is needed"
// proof (tree.go) establishes structurally, not via a depth cap.
// Run with: go test -bench=BenchmarkAdversarialBacktracking -benchmem .
func BenchmarkAdversarialBacktracking(b *testing.B) {
	for _, depth := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			m, reqPath := buildAdversarialBacktrackMux(depth)
			w := httptest.NewRecorder()
			r := benchReq(http.MethodGet, reqPath)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.ServeHTTP(w, r)
			}
		})
	}
}

// buildQuadraticBacktrackMux is a harsher variant of
// buildAdversarialBacktrackMux: instead of every abandoned param
// alternative dead-ending one segment below its own fork (O(1) to
// disprove), each abandoned alternative at fork k requires walking
// (depth-k) more literal "y" segments before dead-ending.
//
// A naive first attempt at this (a single ".../y/y/.../y/NEVERMATCHk"
// pattern per fork, with no other route touching the "y" run) does NOT
// force O(depth-k) work: the radix tree compresses any unbranched run of
// literal segments into a SINGLE edge regardless of its length, so the
// whole "/y/y/.../y/NEVERMATCHk" tail collapses to one node, and the
// request's next byte ('x', continuing the real spine, never 'y') fails
// the prefix match on that ONE node immediately — back to O(1) per fork,
// exactly like buildAdversarialBacktrackMux. This is itself a finding
// worth recording: forcing a genuinely expensive abandoned branch requires
// registering genuine BRANCH POINTS along it, not merely a long literal
// tail — so a large TOTAL PATH LENGTH is not, by itself, a cost driver;
// only actual tree structure (registered branch points) is.
//
// To force real per-level nodes, a sibling route ".../y*m/YSTOP" (capital
// Y, diverging from the lowercase "/y" continuation in its very first
// byte) is registered at every one of the (depth-k) y-levels below fork
// k, splitting what would otherwise be one compressed edge into
// depth-k distinct nodes. Exploring fork k's abandoned branch now
// genuinely visits depth-k additional nodes before dead-ending. Summed
// over all depth forks (each popped at most once, per backtrackStack's
// "why no cap is needed" proof in tree.go), total node-visits is
// O(depth^2): still polynomial, never exponential, because no fork is
// ever retried more than once and no node is ever visited twice — it is
// the empirical counterpart to that proof's claim that the bound is
// "linear in TOTAL TREE NODES" (here deliberately built up to O(depth^2)
// nodes), not "linear in matched-prefix length alone" (still O(depth)).
func buildQuadraticBacktrackMux(depth int) (*muxmaster.Mux, string) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false

	spine := "/a" + strings.Repeat("/x", depth) + "/STATICEND"
	m.GET(spine, nopHandler)

	for k := 0; k < depth; k++ {
		paramName := fmt.Sprintf("r%d", k)
		if k == 0 {
			paramName = "p1"
		}
		prefix := "/a" + strings.Repeat("/x", k) + "/:" + paramName

		// Force a genuine branch node at every y-level below this fork —
		// see the doc comment above for why this is necessary to avoid
		// radix-tree edge compression collapsing the whole run to O(1).
		for level := 0; level < depth-k; level++ {
			branch := prefix + strings.Repeat("/y", level) + "/YSTOP"
			m.GET(branch, nopHandler)
		}

		deadEnd := prefix + strings.Repeat("/y", depth-k) + fmt.Sprintf("/NEVERMATCH%d", k)
		m.GET(deadEnd, nopHandler)
	}

	var real strings.Builder
	real.WriteString("/a")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&real, "/:p%d", i)
	}
	m.GET(real.String(), nopHandler)

	return m, "/a" + strings.Repeat("/x", depth)
}

// BenchmarkQuadraticBacktracking measures lookup cost on the harsher
// buildQuadraticBacktrackMux construction. ns/op is expected to grow
// quadratically in depth (each of the depth doublings in the table below
// should roughly quadruple ns/op) — polynomial, never exponential — per
// backtrackStack's "why no cap is needed" proof (tree.go). Depths are kept
// smaller than BenchmarkAdversarialBacktracking's since the tree itself is
// O(depth^2) nodes here (registration cost, not just lookup cost, grows
// quadratically too).
func BenchmarkQuadraticBacktracking(b *testing.B) {
	for _, depth := range []int{2, 4, 8, 16, 32, 64} {
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			m, reqPath := buildQuadraticBacktrackMux(depth)
			w := httptest.NewRecorder()
			r := benchReq(http.MethodGet, reqPath)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.ServeHTTP(w, r)
			}
		})
	}
}

// ── Mount ─────────────────────────────────────────────────────────────────

// newBenchMountMux builds an outer *Mux that mounts an inner *Mux at a
// static prefix, mirroring newBenchMux's realistic route mix on the inner
// side. Used to measure the cost mountAt's forwarding closure adds on top
// of a plain dispatch — the mountBundle allocation, the composed-prefix
// bookkeeping (specification/groups.md §8), and the RawPath derivation
// (§10) all run on every request that reaches this path, matched or not.
func newBenchMountMux() *muxmaster.Mux {
	inner := newBenchMux()
	outer := muxmaster.New()
	outer.Mount("/api", inner)
	return outer
}

// BenchmarkMount_Static measures a static-route lookup reached through one
// level of Mount (outer dispatch → mountAt's forwarding closure → inner
// dispatch), the baseline every mounted request pays regardless of route
// shape.
func BenchmarkMount_Static(b *testing.B) {
	m := newBenchMountMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/api/users/list")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkMount_Param measures a one-path-parameter route reached through
// Mount — the shape most affected by this change: mountAt now additionally
// computes matchedPrefix, composes the Mount-prefix chain, and (when
// r.URL.RawPath is set) runs the RawPath derivation, on top of the
// pre-existing shallow request copy.
func BenchmarkMount_Param(b *testing.B) {
	m := newBenchMountMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/api/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkMount_TSRRedirect measures a trailing-slash redirect issued by
// the INNER *Mux and rewritten by the outer Mux through the mount-prefix
// chain (specification/groups.md §8) — the new Location-rewriting code
// path added by this change.
func BenchmarkMount_TSRRedirect(b *testing.B) {
	inner := muxmaster.New()
	inner.GET("/users/", nopHandler)
	outer := muxmaster.New()
	outer.Mount("/api", inner)

	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/api/users")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		outer.ServeHTTP(w, r)
	}
}
