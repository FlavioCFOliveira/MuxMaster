// Package harness — DOS-2026-0051 registration-cost re-measurement (rmp #266).
//
// Re-measures addRoute registration wall time, allocations, and complexity
// slope at HEAD, on this hardware (AMD Ryzen 9 5900HX, Go 1.27, Linux), to
// replace the stale numbers cited by rmp #166 (N=2000 -> 1.4s, N=5000 ->
// ~5s) that were never re-measured against the current copy-on-write
// path-copying implementation (tree.go addRouteInternal / incrementChildPrio,
// specification/performance.md SS36-37) and are the reason SECURITY.md's
// required startup-cost note is still missing (finding accepted AC-unmet).
package harness

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func regH(w http.ResponseWriter, r *http.Request) {}

// staticPrefixRoutes builds N distinct static routes sharing a common
// prefix: /api/v1/resource{i}.
func staticPrefixRoutes(n int) []string {
	routes := make([]string, n)
	for i := 0; i < n; i++ {
		routes[i] = fmt.Sprintf("/api/v1/resource%d", i)
	}
	return routes
}

// paramRoutes builds N distinct routes, each with a unique static segment
// followed by a shared param child: /r{i}/:id.
func paramRoutes(n int) []string {
	routes := make([]string, n)
	for i := 0; i < n; i++ {
		routes[i] = fmt.Sprintf("/r%d/:id", i)
	}
	return routes
}

// mixedRealisticRoutes builds a realistic REST-API-shaped route set: for
// N/5 resource collections, 5 routes each (list, detail, nested list,
// nested detail, action), mixing static and param segments the way a
// router generated from an OpenAPI document would — not an adversarial
// shape.
func mixedRealisticRoutes(n int) []string {
	count := n / 5
	if count < 1 {
		count = 1
	}
	routes := make([]string, 0, count*5)
	for i := 0; i < count; i++ {
		res := fmt.Sprintf("res%d", i)
		routes = append(routes,
			"/api/v1/"+res,
			"/api/v1/"+res+"/:id",
			"/api/v1/"+res+"/:id/children",
			"/api/v1/"+res+"/:id/children/:childID",
			"/api/v1/"+res+"/actions/:action",
		)
	}
	return routes
}

func registerAll(routes []string) *mm.Mux {
	r := mm.New()
	for _, p := range routes {
		r.GET(p, regH)
	}
	return r
}

func median(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

type shapeFn func(int) []string

var registrationShapes = map[string]shapeFn{
	"static-prefix": staticPrefixRoutes,
	"param":         paramRoutes,
	"mixed":         mixedRealisticRoutes,
}

// orderedShapeNames keeps output deterministic across runs (map iteration
// order is randomized in Go).
var orderedShapeNames = []string{"static-prefix", "param", "mixed"}

// TestDOS20260051_RegistrationCostScaling re-measures wall time and
// allocations for addRoute across N = 500..10000 for three route shapes,
// median of 5 runs each, and fits a log-log slope to classify the observed
// complexity: O(N) -> slope~=1, O(N log N) -> slope slightly >1 (approaches
// 1 as N grows since log N is sub-polynomial), O(N^2) -> slope~=2.
//
// Run: go test -run TestDOS20260051_RegistrationCostScaling -v ./...
func TestDOS20260051_RegistrationCostScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping registration-cost scaling in short mode")
	}

	sizes := []int{500, 1000, 2000, 5000, 10000}
	const runs = 5

	for _, shapeName := range orderedShapeNames {
		shapeFn := registrationShapes[shapeName]
		t.Run(shapeName, func(t *testing.T) {
			type point struct {
				n      int
				median time.Duration
				allocs uint64
			}
			var points []point

			for _, n := range sizes {
				routes := shapeFn(n)
				durs := make([]time.Duration, runs)
				var allocsSum uint64
				for run := 0; run < runs; run++ {
					var m0, m1 runtime.MemStats
					runtime.GC()
					runtime.ReadMemStats(&m0)
					start := time.Now()
					r := registerAll(routes)
					durs[run] = time.Since(start)
					runtime.ReadMemStats(&m1)
					allocsSum += m1.Mallocs - m0.Mallocs
					runtime.KeepAlive(r)
				}
				med := median(durs)
				points = append(points, point{n: n, median: med, allocs: allocsSum / runs})
				t.Logf("%s N=%d median=%v runs=%v avg-mallocs=%d",
					shapeName, n, med, durs, allocsSum/runs)
			}

			// Report ratios relative to the previous point (doubling-ish
			// steps) so a human reader can eyeball O(N) vs O(N^2) directly.
			for i := 1; i < len(points); i++ {
				nRatio := float64(points[i].n) / float64(points[i-1].n)
				if points[i-1].median <= 0 {
					continue
				}
				tRatio := float64(points[i].median) / float64(points[i-1].median)
				t.Logf("  %s N %d->%d: N-ratio=%.2fx time-ratio=%.2fx (O(N) predicts %.2fx, O(N^2) predicts %.2fx)",
					shapeName, points[i-1].n, points[i].n, nRatio, tRatio, nRatio, nRatio*nRatio)
			}

			// Fit a log-log slope via simple linear regression over
			// log(N) vs log(median-ns): median-ns ~= a * N^slope.
			var sumX, sumY, sumXY, sumX2 float64
			cnt := 0
			for _, p := range points {
				if p.median <= 0 {
					continue
				}
				x := math.Log(float64(p.n))
				y := math.Log(float64(p.median.Nanoseconds()))
				sumX += x
				sumY += y
				sumXY += x * y
				sumX2 += x * x
				cnt++
			}
			if cnt >= 2 {
				fx := float64(cnt)
				slope := (fx*sumXY - sumX*sumY) / (fx*sumX2 - sumX*sumX)
				t.Logf("%s: log-log slope = %.3f  (O(N)=1.0, O(N log N)~=1.05-1.15 over this range, O(N^2)=2.0)",
					shapeName, slope)
			}
		})
	}
}

// BenchmarkDOS20260051_Registration is the reusable, committed benchmark
// counterpart of the Test above, for benchstat / CI trend tracking. Run
// with a fixed iteration count (-benchtime=5x) so N=10000 registrations
// aren't multiplied by Go's adaptive b.N search.
//
// Run: go test -run '^$' -bench BenchmarkDOS20260051_Registration \
//        -benchtime=5x -benchmem -v ./...
func BenchmarkDOS20260051_Registration(b *testing.B) {
	sizes := []int{500, 1000, 2000, 5000, 10000}
	for _, shapeName := range orderedShapeNames {
		shapeFn := registrationShapes[shapeName]
		for _, n := range sizes {
			routes := shapeFn(n)
			b.Run(fmt.Sprintf("%s/N=%d", shapeName, n), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					r := registerAll(routes)
					runtime.KeepAlive(r)
				}
			})
		}
	}
}

// TestDOS20260051_CPUProfile captures a CPU profile of registering N routes
// of a chosen shape (default: param, the shape most likely to stress
// wildChild handling and incrementChildPrio), written under
// DOS0051_EVIDENCE_DIR (default: a per-test temporary directory) for
// `go tool pprof -top <file>` inspection of the dominant registration cost.
//
// Run: DOS0051_EVIDENCE_DIR=/path/to/evidence DOS0051_PROFILE_SHAPE=param \
//        DOS0051_PROFILE_N=10000 \
//        go test -run TestDOS20260051_CPUProfile -v ./...
func TestDOS20260051_CPUProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CPU profile capture in short mode")
	}
	n := 10000
	if v := os.Getenv("DOS0051_PROFILE_N"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	repeats := 100
	if v := os.Getenv("DOS0051_PROFILE_REPEATS"); v != "" {
		fmt.Sscanf(v, "%d", &repeats)
	}
	shape := os.Getenv("DOS0051_PROFILE_SHAPE")
	if shape == "" {
		shape = "param"
	}
	fn, ok := registrationShapes[shape]
	if !ok {
		t.Fatalf("unknown shape %q", shape)
	}
	routes := fn(n)

	outDir := os.Getenv("DOS0051_EVIDENCE_DIR")
	if outDir == "" {
		// Default to a per-test temporary directory so a plain test run never
		// writes a profile into the repository (rmp #290).
		outDir = t.TempDir()
	}
	profPath := outDir + "/cpuprofile-" + shape + fmt.Sprintf("-N%d.pprof", n)
	f, err := os.Create(profPath)
	if err != nil {
		t.Fatalf("create profile file: %v", err)
	}
	defer f.Close()

	// A single N=10000 registration completes in ~10ms — too short to
	// collect a statistically meaningful sample at the default 100Hz CPU
	// profiling rate. Repeat the same registration `repeats` times inside
	// the profiled window (each repeat builds a fresh *mm.Mux, so no
	// state leaks between repeats) to accumulate enough samples to
	// attribute the dominant cost.
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatalf("start profile: %v", err)
	}
	start := time.Now()
	for i := 0; i < repeats; i++ {
		r := registerAll(routes)
		runtime.KeepAlive(r)
	}
	elapsed := time.Since(start)
	pprof.StopCPUProfile()
	t.Logf("registered N=%d shape=%s x%d repeats in %v (%v/registration) -> profile written to %s",
		n, shape, repeats, elapsed, elapsed/time.Duration(repeats), profPath)
}
