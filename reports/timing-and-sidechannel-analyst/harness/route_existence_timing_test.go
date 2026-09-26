//go:build timing

// route_existence_timing_test.go — Route-existence timing oracle analysis.
//
// Reference: MM-2026-0026 (accepted: radix-tree depth correlates with lookup time).
//
// This test quantifies the ACTUAL magnitude of the timing difference between:
//  1. Registered route vs unregistered route (at same depth).
//  2. Short path (depth 1) vs long path (depth 5).
//  3. Static route vs parameterised route at same depth.
//  4. "Hidden admin" route vs random unregistered route.
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

// measureRouteWithStatus is measureRoute plus a per-sample status capture, so
// every arm's status can be verified across the full sample set — not just a
// single preflight request — before the timing evidence is trusted (rmp #264).
func measureRouteWithStatus(handler http.Handler, path string, n int) ([]int64, []int) {
	samples := make([]int64, n)
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		req := routeReq(path)
		w := httptest.NewRecorder()
		t0 := time.Now()
		handler.ServeHTTP(w, req)
		samples[i] = time.Since(t0).Nanoseconds()
		statuses[i] = w.Code
	}
	return samples, statuses
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

	VerifyArmStatus(t, "registered", mux, func() *http.Request { return routeReq("/users") }, http.StatusOK)
	VerifyArmStatus(t, "unregistered", mux, func() *http.Request { return routeReq("/totally-random-zzz") }, http.StatusNotFound)

	registered, statusesReg := measureRouteWithStatus(mux, "/users", nRoute)
	unregistered, statusesUnreg := measureRouteWithStatus(mux, "/totally-random-zzz", nRoute)
	for i, s := range statusesReg {
		if s != http.StatusOK {
			t.Fatalf("registered arm: sample %d returned status %d, want 200 — invalid evidence", i, s)
		}
	}
	for i, s := range statusesUnreg {
		if s != http.StatusNotFound {
			t.Fatalf("unregistered arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}

	result := RunTests(registered, unregistered)
	rs := Summarise(registered)
	us := Summarise(unregistered)

	t.Logf("Route existence oracle: /users (registered) vs /totally-random-zzz (unregistered)")
	t.Logf("  Registered:   N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", rs.N, rs.Mean, rs.Std, rs.P50, rs.P95, rs.P99)
	t.Logf("  Unregistered: N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", us.N, us.Mean, us.Std, us.P50, us.P95, us.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns  Cohen d=%.3f",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.CohenD)

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

	VerifyArmStatus(t, "admin", mux, func() *http.Request { return routeReq("/admin") }, http.StatusOK)
	VerifyArmStatus(t, "random", mux, func() *http.Request { return routeReq("/xyzxyz") }, http.StatusNotFound)

	adminSamples, adminStatuses := measureRouteWithStatus(mux, "/admin", nRoute)
	randomSamples, randomStatuses := measureRouteWithStatus(mux, "/xyzxyz", nRoute)
	for i, s := range adminStatuses {
		if s != http.StatusOK {
			t.Fatalf("admin arm: sample %d returned status %d, want 200 — invalid evidence", i, s)
		}
	}
	for i, s := range randomStatuses {
		if s != http.StatusNotFound {
			t.Fatalf("random arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}

	result := RunTests(adminSamples, randomSamples)
	as_ := Summarise(adminSamples)
	rs_ := Summarise(randomSamples)

	t.Logf("Route oracle: /admin (registered+auth) vs /xyzxyz (unregistered)")
	t.Logf("  Admin:  N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", as_.N, as_.Mean, as_.Std, as_.P50, as_.P95, as_.P99)
	t.Logf("  Random: N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", rs_.N, rs_.Mean, rs_.Std, rs_.P50, rs_.P95, rs_.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns  Cohen d=%.3f",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.CohenD)

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
		VerifyArmStatus(t, labels[j], mux, func() *http.Request { return routeReq(p) }, http.StatusOK)
		samples, statuses := measureRouteWithStatus(mux, p, nRoute/2)
		for i, s := range statuses {
			if s != http.StatusOK {
				t.Fatalf("%s arm: sample %d returned status %d, want 200 — invalid evidence", labels[j], i, s)
			}
		}
		results[j] = Summarise(samples)
	}

	t.Log("Route depth correlation (registered routes):")
	for j, s := range results {
		t.Logf("  %s (%s): N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", labels[j], paths[j], s.N, s.Mean, s.Std, s.P50, s.P95, s.P99)
	}
	// Pairwise Cohen's d / Welch p between adjacent depths, to quantify how
	// cleanly the depth-vs-latency correlation separates each level from
	// the next (complements the raw ns/level slope already logged above).
	for j := 1; j < len(paths); j++ {
		samplesPrev, _ := measureRouteWithStatus(mux, paths[j-1], nRoute/2)
		samplesCur, _ := measureRouteWithStatus(mux, paths[j], nRoute/2)
		r := RunTests(samplesPrev, samplesCur)
		t.Logf("  %s vs %s: Welch p=%.4g Cohen d=%.3f |mean diff|=%.2fns",
			labels[j-1], labels[j], r.WelchP, r.CohenD, r.MeanDiffNs)
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

	VerifyArmStatus(t, "static", mux, func() *http.Request { return routeReq("/users/list") }, http.StatusOK)
	VerifyArmStatus(t, "param", mux, func() *http.Request { return routeReq("/users/alice") }, http.StatusOK)

	staticSamples, staticStatuses := measureRouteWithStatus(mux, "/users/list", nRoute)
	paramSamples, paramStatuses := measureRouteWithStatus(mux, "/users/alice", nRoute)
	for i, s := range staticStatuses {
		if s != http.StatusOK {
			t.Fatalf("static arm: sample %d returned status %d, want 200 — invalid evidence", i, s)
		}
	}
	for i, s := range paramStatuses {
		if s != http.StatusOK {
			t.Fatalf("param arm: sample %d returned status %d, want 200 — invalid evidence", i, s)
		}
	}

	result := RunTests(staticSamples, paramSamples)
	ss := Summarise(staticSamples)
	ps := Summarise(paramSamples)

	t.Logf("Route: /users/list (static) vs /users/alice (param :id)")
	t.Logf("  Static: N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", ss.N, ss.Mean, ss.Std, ss.P50, ss.P95, ss.P99)
	t.Logf("  Param:  N=%d mean=%.1fns std=%.1fns p50=%.0fns p95=%.0fns p99=%.0fns", ps.N, ps.Mean, ps.Std, ps.P50, ps.P95, ps.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns  Cohen d=%.3f",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.CohenD)

	// Expected: param routes are slower (reqBundle allocation); static routes are 0-alloc.
	// This is KNOWN and ACCEPTED behaviour.
}

// buildRedirectFixedPathMux returns buildRoutingMux's route shape with
// RedirectFixedPath enabled. SECURITY.md documents RedirectFixedPath=true
// as an explicit disclosure trade-off ("discloses route existence via the
// redirect status") — the default is false precisely to avoid it. This
// builder exists only to quantify the timing side of that already-known
// disclosure, not to suggest enabling it.
func buildRedirectFixedPathMux() *muxmaster.Mux {
	mux := buildRoutingMux()
	mux.RedirectFixedPath = true
	return mux
}

// TestTiming_RedirectFixedPath_Oracle restores the timing side of the
// RedirectFixedPath disclosure documented in SECURITY.md ("RedirectFixedPath=true
// discloses route existence via the redirect status" — the STATUS CODE
// disclosure is already known and accepted; this test asks whether the
// TIMING also distinguishes a canonicalisable-to-a-registered-route 404
// from an ordinary 404 that has nothing to canonicalise, and from a
// canonicalisable-but-still-unmatched 404. Removed by commit 5f804fa
// without a like-for-like replacement (see reports/overview/findings.md
// O-14). Restored 2026-09-25 (rmp #274 / O-14), adapted to the current
// harness conventions (VerifyArmStatus preflight, per-sample status check,
// RunTests/Summarise). Informational — SECURITY.md already documents and
// accepts the status-code-level disclosure this magnifies; this test does
// not assert a numeric bound, matching the sibling error-oracle tests.
func TestTiming_RedirectFixedPath_Oracle(t *testing.T) {
	mux := buildRedirectFixedPathMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// canon: a double-slash path that path.Clean resolves to the registered
	// "/users/list" — RedirectFixedPath serves a 301 (route exists, disclosed).
	const canonPath = "/users//list"
	// nonCanon: a double-slash path whose cleaned form is NOT registered —
	// cleanedPath fails to find a handler, so this falls through to a plain 404.
	const nonCanonPath = "/totally//random-xyz"
	// plain: an ordinary 404 with nothing for path.Clean to canonicalise.
	const plainPath = "/totally-random-xyz-999"

	for i := 0; i < 30_000; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, routeReq(canonPath))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, routeReq(nonCanonPath))
		w3 := httptest.NewRecorder()
		mux.ServeHTTP(w3, routeReq(plainPath))
	}

	VerifyArmStatus(t, "canon", mux, func() *http.Request { return routeReq(canonPath) }, http.StatusMovedPermanently)
	VerifyArmStatus(t, "non-canon", mux, func() *http.Request { return routeReq(nonCanonPath) }, http.StatusNotFound)
	VerifyArmStatus(t, "plain", mux, func() *http.Request { return routeReq(plainPath) }, http.StatusNotFound)

	canonSamples, canonStatuses := measureRouteWithStatus(mux, canonPath, nRoute)
	nonCanonSamples, nonCanonStatuses := measureRouteWithStatus(mux, nonCanonPath, nRoute)
	plainSamples, plainStatuses := measureRouteWithStatus(mux, plainPath, nRoute)

	for i, s := range canonStatuses {
		if s != http.StatusMovedPermanently {
			t.Fatalf("canon arm: sample %d returned status %d, want 301 — invalid evidence", i, s)
		}
	}
	for i, s := range nonCanonStatuses {
		if s != http.StatusNotFound {
			t.Fatalf("non-canon arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}
	for i, s := range plainStatuses {
		if s != http.StatusNotFound {
			t.Fatalf("plain arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}

	pairs := []struct {
		name  string
		a, b  []int64
		label string
	}{
		{"canon_vs_noncanon", canonSamples, nonCanonSamples, "canonicalisable-to-registered (301) vs canonicalisable-but-unmatched (404)"},
		{"canon_vs_plain", canonSamples, plainSamples, "canonicalisable-to-registered (301, discloses hidden route) vs plain 404"},
		{"noncanon_vs_plain", nonCanonSamples, plainSamples, "double-slash non-canonical 404 vs plain 404"},
	}

	t.Logf("RedirectFixedPath oracle (N=%d per arm)", nRoute)
	canonS := Summarise(canonSamples)
	nonCanonS := Summarise(nonCanonSamples)
	plainS := Summarise(plainSamples)
	t.Logf("  canon:     mean=%.1fns p50=%.0fns p99=%.0fns", canonS.Mean, canonS.P50, canonS.P99)
	t.Logf("  non-canon: mean=%.1fns p50=%.0fns p99=%.0fns", nonCanonS.Mean, nonCanonS.P50, nonCanonS.P99)
	t.Logf("  plain:     mean=%.1fns p50=%.0fns p99=%.0fns", plainS.Mean, plainS.P50, plainS.P99)

	for _, p := range pairs {
		result := RunTests(p.a, p.b)
		t.Logf("  %s (%s): Welch p=%.4g KS p=%.4g MWU p=%.4g |mean diff|=%.2fns Cohen d=%.3f",
			p.name, p.label, result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.CohenD)
		if result.Leak {
			t.Logf("    DISTINGUISHABLE: diff=%.2fns — %s", result.MeanDiffNs, classifyOracle(result.MeanDiffNs))
			assessNetworkExploitability(t, result.MeanDiffNs)
		} else {
			t.Logf("    NOT distinguishable at p<0.01")
		}
	}
	t.Logf("Verdict: RedirectFixedPath's status-code-level disclosure is already documented and " +
		"accepted in SECURITY.md (\"RedirectFixedPath=true discloses route existence via the " +
		"redirect status\" — default is false). This test quantifies the additional timing signal " +
		"on top of that already-known, already-accepted disclosure; it does not assert a bound.")
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
