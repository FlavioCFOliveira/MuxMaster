//go:build timing

// route_existence_timing_test.go — Route-existence timing oracle analysis.
//
// Reference: MM-2026-0026 (accepted: radix-tree depth correlates with lookup time).
//
// This test quantifies the ACTUAL magnitude of the timing difference between:
//   1. Registered route vs unregistered route (at same depth).
//   2. Short path (depth 1) vs long path (depth 5).
//   3. Static route vs parameterised route at same depth.
//   4. "Hidden admin" route vs random unregistered route.
//
// The goal is to confirm whether the oracle is exploitable at network latency
// (<1ms RTT LAN, ~10-50ms WAN) given the measured effect size.
//
// Expected: registered vs unregistered timing is measurable but small (~5-50ns).
// At WAN latency, this is below the noise floor. At LAN, it may be measurable
// with statistical averaging across many requests.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

const nRoute = 200_000

func nopHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func buildRoutingMux() *muxmaster.Mux {
	r := muxmaster.New()
	// Static routes at various depths.
	r.GET("/", nopHandler)
	r.GET("/users", nopHandler)
	r.GET("/users/list", nopHandler)
	r.GET("/users/detail/view", nopHandler)
	r.GET("/users/detail/view/extended/info", nopHandler)
	// Param routes.
	r.GET("/users/:id", nopHandler)
	r.GET("/users/:id/posts", nopHandler)
	r.GET("/users/:id/posts/:pid", nopHandler)
	// "Hidden" admin routes.
	r.GET("/admin", nopHandler)
	r.GET("/admin/users", nopHandler)
	r.GET("/admin/settings/security", nopHandler)
	// Catch-all.
	r.GET("/static/*filepath", nopHandler)
	return r
}

func routeReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

func measureRoute(handler http.Handler, path string, n int) []int64 {
	samples := make([]int64, n)
	for i := 0; i < n; i++ {
		req := routeReq(path)
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, req)
		samples[i] = time.Since(t0).Nanoseconds()
	}
	return samples
}

// TestTiming_Route_RegisteredVsUnregistered measures the timing oracle
// between a registered route and an unregistered route at the same depth.
func TestTiming_Route_RegisteredVsUnregistered(t *testing.T) {
	mux := buildRoutingMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Warmup.
	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, routeReq("/users"))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, routeReq("/totally-random-zzz"))
	}

	registered := measureRoute(mux, "/users", nRoute)
	unregistered := measureRoute(mux, "/totally-random-zzz", nRoute)

	result := RunTests(registered, unregistered)
	rs := Summarise(registered)
	us := Summarise(unregistered)

	t.Logf("Route existence oracle: /users (registered) vs /totally-random-zzz (unregistered)")
	t.Logf("  Registered:   mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", rs.Mean, rs.Std, rs.P50, rs.P99)
	t.Logf("  Unregistered: mean=%.1fns std=%.1fns p50=%.0fns p99=%.0fns", us.Mean, us.Std, us.P50, us.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		// This is the known MM-2026-0026 accepted finding. Classify by effect size.
		t.Logf("ROUTE ORACLE CONFIRMED (MM-2026-0026): diff=%.2fns", result.MeanDiffNs)
		assessNetworkExploitability(t, result.MeanDiffNs)
	} else {
		t.Logf("Route existence timing: NOT distinguishable at p<0.01 — acceptable")
	}
}

// TestTiming_Route_AdminHiddenVsRandom specifically tests whether "admin" routes
// can be discovered via timing against a random path.
func TestTiming_Route_AdminHiddenVsRandom(t *testing.T) {
	mux := buildRoutingMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, routeReq("/admin"))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, routeReq("/xyzxyz"))
	}

	adminSamples := measureRoute(mux, "/admin", nRoute)
	randomSamples := measureRoute(mux, "/xyzxyz", nRoute)

	result := RunTests(adminSamples, randomSamples)
	as_ := Summarise(adminSamples)
	rs_ := Summarise(randomSamples)

	t.Logf("Route oracle: /admin (registered+auth) vs /xyzxyz (unregistered)")
	t.Logf("  Admin:  mean=%.1fns p50=%.0fns p99=%.0fns", as_.Mean, as_.P50, as_.P99)
	t.Logf("  Random: mean=%.1fns p50=%.0fns p99=%.0fns", rs_.Mean, rs_.P50, rs_.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("ADMIN ROUTE EXISTENCE ORACLE: diff=%.2fns — admin paths are timing-discoverable",
			result.MeanDiffNs)
		assessNetworkExploitability(t, result.MeanDiffNs)
	}
}

// TestTiming_Route_DepthCorrelation measures timing vs route depth to quantify
// whether the radix tree walk time increases with path depth (expected: yes).
func TestTiming_Route_DepthCorrelation(t *testing.T) {
	mux := buildRoutingMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	paths := []string{
		"/",
		"/users",
		"/users/list",
		"/users/detail/view",
		"/users/detail/view/extended/info",
	}
	labels := []string{"depth-1", "depth-2", "depth-3", "depth-4", "depth-5"}

	results := make([]SummaryStats, len(paths))
	for j, p := range paths {
		// Warmup.
		for i := 0; i < 20_000; i++ {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, routeReq(p))
		}
		samples := measureRoute(mux, p, nRoute/2)
		results[j] = Summarise(samples)
	}

	t.Log("Route depth correlation (registered routes):")
	for j, s := range results {
		t.Logf("  %s (%s): mean=%.1fns p50=%.0fns", labels[j], paths[j], s.Mean, s.P50)
	}
	// Check monotonic increase — radix tree walk should take longer for deeper routes.
	for j := 1; j < len(results); j++ {
		if results[j].Mean < results[0].Mean*0.5 {
			t.Logf("  NOTE: %s is faster than root — possible tree optimisation or measurement noise", labels[j])
		}
	}
}

// TestTiming_Route_Param_vs_Static measures whether parameterised routes
// show different timing from static routes at the same depth.
func TestTiming_Route_Param_vs_Static(t *testing.T) {
	mux := buildRoutingMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// /users/list is static; /users/alice matches /users/:id (parameterised).
	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, routeReq("/users/list"))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, routeReq("/users/alice"))
	}

	staticSamples := measureRoute(mux, "/users/list", nRoute)
	paramSamples := measureRoute(mux, "/users/alice", nRoute)

	result := RunTests(staticSamples, paramSamples)
	ss := Summarise(staticSamples)
	ps := Summarise(paramSamples)

	t.Logf("Route: /users/list (static) vs /users/alice (param :id)")
	t.Logf("  Static: mean=%.1fns p50=%.0fns", ss.Mean, ss.P50)
	t.Logf("  Param:  mean=%.1fns p50=%.0fns", ps.Mean, ps.P50)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// Expected: param routes are slower (reqBundle allocation); static routes are 0-alloc.
	// This is KNOWN and ACCEPTED behaviour.
}

// assessNetworkExploitability emits a severity log based on effect size.
func assessNetworkExploitability(t *testing.T, diffNs float64) {
	t.Helper()
	switch {
	case diffNs > 1_000_000: // 1ms
		t.Logf("  SEVERITY: CRITICAL — diff > 1ms, exploitable even over WAN")
	case diffNs > 100_000: // 100µs
		t.Logf("  SEVERITY: HIGH — diff > 100µs, exploitable over LAN with ~100 requests")
	case diffNs > 10_000: // 10µs
		t.Logf("  SEVERITY: MEDIUM — diff > 10µs, exploitable over LAN with ~1000 requests")
	case diffNs > 1_000: // 1µs
		t.Logf("  SEVERITY: LOW — diff > 1µs, exploitable over LAN with ~10000+ requests")
	default:
		t.Logf("  SEVERITY: INFORMATIONAL — diff < 1µs, below practical LAN exploitation threshold")
	}
}
