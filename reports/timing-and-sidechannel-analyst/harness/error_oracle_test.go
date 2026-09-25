//go:build timing

// error_oracle_test.go — Error oracle timing analysis.
//
// Tests whether different error conditions produce distinguishable timing:
//
//	404 Not Found vs 405 Method Not Allowed vs 401 Unauthorized vs 200 OK
//
// An error oracle exists when an attacker can determine which error occurred
// by measuring response latency. This leaks:
//   - 404 vs 405: whether the path exists (route enumeration)
//   - 401 vs 404: whether the path exists but requires auth
//   - Timing patterns during 429 throttle: remaining budget
//
// For each scenario we record: status code, response body length, header set,
// and median timing. Distinguishability is tested with all 3 hypothesis tests.
package harness

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const nError = 150_000

// buildErrorOracleMux registers BasicAuth on a Group, NOT on the root Mux via
// Use(). This matters for the validity of the error-oracle comparisons below:
//
// Mux.Use() wraps every handler registered after the call AND the mux's
// shared, lazily-built NotFound/MethodNotAllowed handlers (see mux.go Use()
// doc comment and the lazyNotFoundPtr/methodNotAllowedCache invalidation it
// performs) — that is documented, intended behaviour for cross-cutting
// middleware. Group.Use()/Group.Handle(), by contrast, wraps only the
// specific handler registered through that group (see group.go Handle:
// "g.mux.Handle(method, g.prefix+path, wrapMiddleware(handler, g.middleware))")
// and never touches the mux-level NotFound/MethodNotAllowed handlers.
//
// The original harness used r.Use(...), which meant every unauthenticated
// request — including ones that should 404 (no route) or 405 (wrong method)
// — was intercepted by BasicAuth before the router's own NotFound/
// MethodNotAllowed logic ever ran, and ALL of them observably returned 401.
// See TSC-2026-0009 for the measured evidence of that defect (both the
// "404" and "405" arms returned 401, invalidating the reported timing
// figure). Using a Group here keeps /exists authenticated while leaving the
// mux's genuine 404/405 dispatch paths — which is what these tests are
// actually meant to measure — unauthenticated, exactly as intended.
func buildErrorOracleMux() *muxmaster.Mux {
	r := muxmaster.New()
	g := r.Group("")
	g.Use(middleware.BasicAuth("test", map[string]string{"user": "password"}))
	g.GET("/exists", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	g.POST("/exists", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func measureError(mux *muxmaster.Mux, method, path string, authHeader string, n int) ([]int64, []int) {
	samples := make([]int64, n)
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(method, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		t0 := time.Now()
		mux.ServeHTTP(w, req)
		samples[i] = time.Since(t0).Nanoseconds()
		statuses[i] = w.Code
	}
	return samples, statuses
}

func validAuthHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
}

// TestTiming_ErrorOracle_404vs405 tests whether 404 (no route) and 405
// (route exists, wrong method) timing is distinguishable.
// This is the primary route-existence oracle via error codes.
func TestTiming_ErrorOracle_404vs405(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		req404 := httptest.NewRequest(http.MethodDelete, "/nonexistent", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req404)
		req405 := httptest.NewRequest(http.MethodDelete, "/exists", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req405)
	}

	// DELETE /nonexistent-path → 404 (no route at all)
	// DELETE /exists → 405 (route exists for GET/POST, not DELETE)
	// BasicAuth is registered on a Group (see buildErrorOracleMux), so it wraps
	// only the /exists GET/POST handlers — NOT the mux's shared NotFound/
	// MethodNotAllowed handlers. Both 404 and 405 dispatch paths are therefore
	// genuinely unauthenticated, as the comparison requires.
	//
	// Mandatory preflight (rmp #264 / TSC-2026-0009): confirm each arm really
	// produces its intended status BEFORE collecting any timing sample. A
	// mismatch here means the harness setup is broken and any timing evidence
	// would not measure what it claims to.
	VerifyArmStatus(t, "404", mux, func() *http.Request {
		return httptest.NewRequest(http.MethodDelete, "/nonexistent-path", nil)
	}, http.StatusNotFound)
	VerifyArmStatus(t, "405", mux, func() *http.Request {
		return httptest.NewRequest(http.MethodDelete, "/exists", nil)
	}, http.StatusMethodNotAllowed)

	s404, statuses404 := measureError(mux, http.MethodDelete, "/nonexistent-path", "", nError)
	s405, statuses405 := measureError(mux, http.MethodDelete, "/exists", "", nError)

	// Full-sample status confirmation — every sample must match, not just a
	// prefix, since the whole point is to guarantee the statistical evidence
	// below actually reflects the 404 and 405 dispatch paths.
	for i, s := range statuses404 {
		if s != http.StatusNotFound {
			t.Fatalf("404 arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}
	for i, s := range statuses405 {
		if s != http.StatusMethodNotAllowed {
			t.Fatalf("405 arm: sample %d returned status %d, want 405 — invalid evidence", i, s)
		}
	}

	result := RunTests(s404, s405)
	r4 := Summarise(s404)
	r5 := Summarise(s405)

	t.Logf("Error oracle: 404 vs 405 timing (N=%d each)", nError)
	t.Logf("  404: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  405: mean=%.1fns p50=%.0fns p99=%.0fns", r5.Mean, r5.P50, r5.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("ERROR ORACLE: 404 vs 405 timing is distinguishable — route existence leaks")
		t.Logf("  Effect size: %.2fns — %s", result.MeanDiffNs,
			classifyOracle(result.MeanDiffNs))
	} else {
		t.Logf("404 vs 405 timing: NOT distinguishable — no route-existence oracle via timing")
	}
}

// TestTiming_ErrorOracle_404vs401 tests whether 404 (no route, auth not applied)
// and 401 (route exists, auth rejected) timing is distinguishable.
func TestTiming_ErrorOracle_404vs401(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	for i := 0; i < 30_000; i++ {
		req404 := httptest.NewRequest(http.MethodGet, "/no-such-route", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req404)
		req401 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req401)
	}

	// GET /no-such-route → 404, no auth (mux-level NotFound, unaffected by
	// the Group-scoped BasicAuth — see buildErrorOracleMux)
	// GET /exists (no auth header) → 401, auth runs
	VerifyArmStatus(t, "404", mux, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/no-such-route", nil)
	}, http.StatusNotFound)
	VerifyArmStatus(t, "401", mux, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/exists", nil)
	}, http.StatusUnauthorized)

	s404, statuses404 := measureError(mux, http.MethodGet, "/no-such-route", "", nError)
	s401, statuses401 := measureError(mux, http.MethodGet, "/exists", "", nError)

	for i, s := range statuses404 {
		if s != http.StatusNotFound {
			t.Fatalf("404 arm: sample %d returned status %d, want 404 — invalid evidence", i, s)
		}
	}
	for i, s := range statuses401 {
		if s != http.StatusUnauthorized {
			t.Fatalf("401 arm: sample %d returned status %d, want 401 — invalid evidence", i, s)
		}
	}

	result := RunTests(s404, s401)
	r4 := Summarise(s404)
	r1 := Summarise(s401)

	t.Logf("Error oracle: 404 vs 401 timing (N=%d each)", nError)
	t.Logf("  404: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  401: mean=%.1fns p50=%.0fns p99=%.0fns", r1.Mean, r1.P50, r1.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	if result.Leak {
		t.Logf("ERROR ORACLE (404 vs 401): timing distinguishable — confirms route existence when auth required")
		t.Logf("  Effect size: %.2fns — %s", result.MeanDiffNs, classifyOracle(result.MeanDiffNs))
		t.Logf("  NOTE: 401 includes SHA-256 hash overhead from BasicAuth — expected difference")
	}
}

// TestTiming_ErrorOracle_200vs401 tests authenticated success vs auth failure.
// For BasicAuth specifically, this should be constant-time (already tested in basic_auth_timing_test.go).
func TestTiming_ErrorOracle_200vs401(t *testing.T) {
	mux := buildErrorOracleMux()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	authOK := validAuthHeader()

	for i := 0; i < 30_000; i++ {
		req200 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req200.Header.Set("Authorization", authOK)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req200)
		req401 := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req401.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:wrongpass")))
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req401)
	}

	badAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:wrongpass"))

	VerifyArmStatus(t, "200", mux, func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req.Header.Set("Authorization", authOK)
		return req
	}, http.StatusOK)
	VerifyArmStatus(t, "401", mux, func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req.Header.Set("Authorization", badAuth)
		return req
	}, http.StatusUnauthorized)

	s200, statuses200 := measureError(mux, http.MethodGet, "/exists", authOK, nError)
	s401bad := make([]int64, nError)
	statuses401bad := make([]int, nError)
	for i := 0; i < nError; i++ {
		req := httptest.NewRequest(http.MethodGet, "/exists", nil)
		req.Header.Set("Authorization", badAuth)
		w := httptest.NewRecorder()
		t0 := time.Now()
		mux.ServeHTTP(w, req)
		s401bad[i] = time.Since(t0).Nanoseconds()
		statuses401bad[i] = w.Code
	}

	for i, s := range statuses200 {
		if s != http.StatusOK {
			t.Fatalf("200 arm: sample %d returned status %d, want 200 — invalid evidence", i, s)
		}
	}
	for i, s := range statuses401bad {
		if s != http.StatusUnauthorized {
			t.Fatalf("401 arm: sample %d returned status %d, want 401 — invalid evidence", i, s)
		}
	}

	result := RunTests(s200, s401bad)
	r2 := Summarise(s200)
	r4 := Summarise(s401bad)

	t.Logf("Error oracle: 200 (valid auth) vs 401 (wrong password) timing (N=%d each)", nError)
	t.Logf("  200: mean=%.1fns p50=%.0fns p99=%.0fns", r2.Mean, r2.P50, r2.P99)
	t.Logf("  401: mean=%.1fns p50=%.0fns p99=%.0fns", r4.Mean, r4.P50, r4.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs)

	// NOTE: 200 calls next.ServeHTTP (trivial nopHandler); 401 calls http.Error.
	// The difference here is the handler path length, not auth timing.
	// BasicAuth itself is constant-time (confirmed separately).
}

// TestTiming_ErrorOracle_Panic measures panic recovery overhead.
func TestTiming_ErrorOracle_Panic(t *testing.T) {
	r := muxmaster.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
	r.GET("/clean", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/panic", func(w http.ResponseWriter, req *http.Request) {
		panic("test panic for timing analysis")
	})

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	VerifyArmStatus(t, "clean", r, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/clean", nil)
	}, http.StatusOK)
	VerifyArmStatus(t, "panic", r, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/panic", nil)
	}, http.StatusInternalServerError)

	for i := 0; i < 10_000; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clean", nil))
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/panic", nil))
	}

	const n = 50_000
	cleanSamples := make([]int64, n)
	panicSamples := make([]int64, n)

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/clean", nil)
		w := httptest.NewRecorder()
		t0 := time.Now()
		r.ServeHTTP(w, req)
		cleanSamples[i] = time.Since(t0).Nanoseconds()
		if w.Code != http.StatusOK {
			t.Fatalf("clean arm: sample %d returned status %d, want 200 — invalid evidence", i, w.Code)
		}

		req2 := httptest.NewRequest(http.MethodGet, "/panic", nil)
		w2 := httptest.NewRecorder()
		t1 := time.Now()
		r.ServeHTTP(w2, req2)
		panicSamples[i] = time.Since(t1).Nanoseconds()
		if w2.Code != http.StatusInternalServerError {
			t.Fatalf("panic arm: sample %d returned status %d, want 500 — invalid evidence", i, w2.Code)
		}
	}

	result := RunTests(cleanSamples, panicSamples)
	cs := Summarise(cleanSamples)
	ps := Summarise(panicSamples)

	t.Logf("Panic recovery: clean path vs panic path timing (N=%d each)", n)
	t.Logf("  Clean: mean=%.1fns p50=%.0fns p99=%.0fns", cs.Mean, cs.P50, cs.P99)
	t.Logf("  Panic: mean=%.1fns p50=%.0fns p99=%.0fns", ps.Mean, ps.P50, ps.P99)
	t.Logf("  Welch p=%.4g  KS p=%.4g  MWU p=%.4g  |mean diff|=%.2fns (%.2fµs)",
		result.WelchP, result.KSP, result.MWUP, result.MeanDiffNs, result.MeanDiffNs/1000)

	// Panic path ALWAYS takes longer (runtime.panic overhead + defer + recover ~= 2-10µs).
	// This is a KNOWN, ACCEPTED difference — informational severity.
	if result.Leak {
		t.Logf("Panic path distinguishable from clean path: diff=%.2fµs — INFORMATIONAL",
			result.MeanDiffNs/1000)
	}
}

func classifyOracle(diffNs float64) string {
	switch {
	case diffNs > 10_000:
		return "HIGH — exploitable at LAN with <1000 requests"
	case diffNs > 1_000:
		return "MEDIUM — exploitable at LAN with ~10k requests"
	case diffNs > 200:
		return "LOW — exploitable at LAN with ~100k requests"
	default:
		return "INFORMATIONAL — below practical LAN exploitation threshold"
	}
}

// TestTiming_ErrorOracleMatrix is the restored 15-pair error-oracle matrix
// (MM-2026-0046), which commit 5f804fa removed without a like-for-like
// replacement (see reports/overview/findings.md O-14). The 4 sibling tests
// above (404vs405, 404vs401, 200vs401, Panic) each cover exactly one pair
// out of the 6-scenario matrix {200, 401, 404, 405, 500, 503}; this test
// restores full pairwise coverage (C(6,2) = 15 pairs) so that no
// scenario-pair combination is left unmeasured.
//
// MM-2026-0046 in SECURITY.md ("Error Oracle") already documents differing
// error responses (404/405/401/...) as intentional, accepted HTTP
// semantics — this test is therefore informational (matching the sibling
// tests' style): it logs distinguishability and effect size per pair via
// classifyOracle, it does not assert a numeric bound. Each of the 6 arms
// is preflight-verified with VerifyArmStatus AND has every sample's status
// checked (rmp #264 / TSC-2026-0009 lesson: a harness that silently
// measures the wrong code path produces invalid evidence).
//
// Restored 2026-09-25 (rmp #274 / O-14).
func TestTiming_ErrorOracleMatrix(t *testing.T) {
	const nMatrix = 100_000

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	runtime.GC()
	runtime.GC()

	// Arms sharing the Group-scoped BasicAuth mux (see buildErrorOracleMux
	// doc comment for why Group, not Mux.Use, is required for 404/405 to be
	// genuinely unauthenticated).
	mux1 := buildErrorOracleMux()
	authOK := validAuthHeader()

	// 500: a dedicated mux with a PanicHandler — panic recovery is a
	// mux-level concern independent of BasicAuth/routing.
	mux2 := func() *muxmaster.Mux {
		r := muxmaster.New()
		r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		r.GET("/panicking", func(w http.ResponseWriter, req *http.Request) {
			panic("error-oracle-matrix induced panic")
		})
		return r
	}()

	// 503: a dedicated mux where a single ThrottleBacklog(1, 0, ...) slot is
	// held for the duration of the measurement by a goroutine blocked on
	// <-hold, forcing every /probe request to fail acquisition immediately
	// (backlog=0 means the queue send has no waiting receiver and rejects
	// on the spot — no timeout wait needed, so this arm is deterministic
	// and fast). See the historical throttle_compress_test.go for the same
	// technique (removed by 5f804fa).
	mux3 := muxmaster.New()
	sem := middleware.ThrottleBacklog(1, 0, 50*time.Microsecond)
	hold := make(chan struct{})
	started := make(chan struct{})
	blocker := sem(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	probe := sem(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux3.GET("/probe", probe.ServeHTTP)
	go func() {
		w := httptest.NewRecorder()
		blocker.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/hold", nil))
	}()
	<-started // deterministic: the semaphore slot is provably held before we proceed
	defer close(hold)

	badAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:wrongpass"))

	type arm struct {
		name   string
		mux    *muxmaster.Mux
		method string
		path   string
		auth   string
		want   int
	}
	arms := []arm{
		{"200", mux1, http.MethodGet, "/exists", authOK, http.StatusOK},
		{"401", mux1, http.MethodGet, "/exists", badAuth, http.StatusUnauthorized},
		{"404", mux1, http.MethodGet, "/no-such-route-xyz", "", http.StatusNotFound},
		{"405", mux1, http.MethodDelete, "/exists", "", http.StatusMethodNotAllowed},
		{"500", mux2, http.MethodGet, "/panicking", "", http.StatusInternalServerError},
		{"503", mux3, http.MethodGet, "/probe", "", http.StatusServiceUnavailable},
	}

	// Warmup + preflight + measure, one arm at a time.
	samples := make(map[string][]int64, len(arms))
	for _, a := range arms {
		for i := 0; i < 20_000; i++ {
			req := httptest.NewRequest(a.method, a.path, nil)
			if a.auth != "" {
				req.Header.Set("Authorization", a.auth)
			}
			w := httptest.NewRecorder()
			a.mux.ServeHTTP(w, req)
		}
		VerifyArmStatus(t, a.name, a.mux, func() *http.Request {
			req := httptest.NewRequest(a.method, a.path, nil)
			if a.auth != "" {
				req.Header.Set("Authorization", a.auth)
			}
			return req
		}, a.want)

		s, statuses := measureError(a.mux, a.method, a.path, a.auth, nMatrix)
		for i, code := range statuses {
			if code != a.want {
				t.Fatalf("arm %q: sample %d returned status %d, want %d — invalid evidence", a.name, i, code, a.want)
			}
		}
		samples[a.name] = s
	}

	// Full C(6,2) = 15 pairwise comparison.
	names := []string{"200", "401", "404", "405", "500", "503"}
	t.Logf("Error-oracle matrix (MM-2026-0046): N=%d per arm, %d pairs", nMatrix, len(names)*(len(names)-1)/2)
	for _, n := range names {
		s := Summarise(samples[n])
		t.Logf("  arm %s: mean=%.1fns p50=%.0fns p99=%.0fns", n, s.Mean, s.P50, s.P99)
	}
	worstP := 1.0
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := names[i], names[j]
			result := RunTests(samples[a], samples[b])
			if result.WelchP < worstP {
				worstP = result.WelchP
			}
			if result.Leak {
				t.Logf("  %s vs %s: distinguishable — diff=%.2fns (%s) Welch p=%.4g KS p=%.4g MWU p=%.4g",
					a, b, result.MeanDiffNs, classifyOracle(result.MeanDiffNs), result.WelchP, result.KSP, result.MWUP)
			} else {
				t.Logf("  %s vs %s: NOT distinguishable at p<0.01", a, b)
			}
		}
	}
	t.Logf("Worst-case Welch p-value across all 15 pairs: %.4g", worstP)
	t.Logf("Verdict: MM-2026-0046 accepted class — differential error responses are intentional " +
		"HTTP semantics (see SECURITY.md \"Error Oracle\"); this matrix documents current magnitudes, " +
		"it does not gate the build on any pair's significance.")
}
