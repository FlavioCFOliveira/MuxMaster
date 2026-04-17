//go:build timing

// Error-oracle audit: compare status / body-length / header / timing across
// 404 (not found), 405 (method not allowed), 401 (unauthorised),
// 429 (throttled) and 500 (recovered panic).
//
// The matrix is emitted as a CSV for re-analysis and reproduced in the report.
package harness

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// buildErrorOracleMux builds a mux that exposes each of the error categories.
func buildErrorOracleMux() http.Handler {
	m := muxmaster.New()
	// Must install a panic handler; otherwise dispatchWithRecover is not used.
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("oracle-induced")
	})
	m.GET("/ok", okHandler)
	m.POST("/onlyPOST", okHandler) // used for 405
	m.GET("/panicking", panicHandler)

	// Protected routes — basic_auth fails for every request.
	bauth := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	protected := bauth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	m.GET("/protected", protected.ServeHTTP)

	// Throttled path — limit 1, backlog 0, timeout 1ns to force 429.
	th := middleware.ThrottleBacklog(1, 0, time.Nanosecond)
	// Occupy the single slot up front by calling a handler that spins.  For a
	// deterministic benchmark we rely on the fact that *nobody* releases a
	// slot between pre-occupation and our measurement — to keep it simple
	// instead we point this route at the throttle alone with limit=1 and
	// measure from a goroutine that holds the token.  In practice the
	// shortest deterministic path is to set limit=0-equivalent via backlog=0
	// and timeout=1ns: after a single request fills the budget, all further
	// requests complete the "enqueue → timeout → 503" path synchronously.
	//
	// We instead just use backlog=0 with pre-consumed token.
	throttled := th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	m.GET("/throttled", throttled.ServeHTTP)

	// Warm the throttle by consuming the single token (will respond 200 once;
	// subsequent requests will time out → 503).  Done in Test* function.

	return m
}

type oracleProbe struct {
	name     string
	req      *http.Request
	expected int
}

func buildOracleProbes() []oracleProbe {
	return []oracleProbe{
		{"200_OK", httptest.NewRequest(http.MethodGet, "/ok", nil), 200},
		{"404_NotFound", httptest.NewRequest(http.MethodGet, "/does-not-exist-xyz", nil), 404},
		{"405_MethodNotAllowed", httptest.NewRequest(http.MethodGet, "/onlyPOST", nil), 405},
		{"401_Unauthorised", httptest.NewRequest(http.MethodGet, "/protected", nil), 401},
		{"500_Panic", httptest.NewRequest(http.MethodGet, "/panicking", nil), 500},
		// 429/503 is measured separately: the throttle's internal state means
		// the first request succeeds (200).
	}
}

func TestErrorOracleMatrix(t *testing.T) {
	h := buildErrorOracleMux()

	// Drain the throttle's one token so /throttled returns 503 deterministically.
	// Don't return the token — we want /throttled to 503.
	// The throttle allocates its `tokens` channel at middleware-construction
	// time; by sending a single request we consume the token.  Because
	// `defer tokens <- t` runs, the token returns.  To keep it deterministic
	// we time a SINGLE pre-consumed request at each sampling.
	_ = h

	// Collect per-probe timing series.
	cleanup := PreparePinned()
	defer cleanup()

	probes := buildOracleProbes()
	N := nSamples / 5 // 1e5 per probe is plenty for median comparison
	series := make(map[string][]float64, len(probes))
	last := make(map[string]*httptest.ResponseRecorder, len(probes))
	for _, p := range probes {
		Warmup(5_000, func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, p.req)
		})
		samples := make([]int64, N)
		var rec *httptest.ResponseRecorder
		for i := 0; i < N; i++ {
			rec = httptest.NewRecorder()
			t0 := time.Now()
			h.ServeHTTP(rec, p.req)
			samples[i] = time.Since(t0).Nanoseconds()
		}
		series[p.name] = TrimP99(I64sToF64s(samples))
		last[p.name] = rec
	}
	// Throttled probe: separate because its state is mutated by traffic.
	th := h
	throttleReq := httptest.NewRequest(http.MethodGet, "/throttled", nil)
	// Consume the single token; because of `defer`, it returns after each call
	// so 503 only happens when the *in-flight* handler is slow.  To force 503,
	// we wrap the probe in a synchronous handler that holds the token.
	// Instead, we rebuild the throttle with a limit of 0 — not allowed.  So we
	// construct a fresh mux where the target handler blocks on a channel; we
	// never release it and time requests that time out.
	{
		m := muxmaster.New()
		hold := make(chan struct{})
		defer close(hold)
		th2 := middleware.ThrottleBacklog(1, 0, 100*time.Microsecond)
		blocking := th2(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-hold
		}))
		m.GET("/t", blocking.ServeHTTP)
		th = m
		// Start one request that holds the single slot.
		go func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/t", nil)
			th.ServeHTTP(w, r)
		}()
		time.Sleep(5 * time.Millisecond)
		Warmup(2_000, func() {
			w := httptest.NewRecorder()
			th.ServeHTTP(w, throttleReq)
		})
		samples := make([]int64, N)
		var rec *httptest.ResponseRecorder
		for i := 0; i < N; i++ {
			rec = httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/t", nil)
			t0 := time.Now()
			th.ServeHTTP(rec, r)
			samples[i] = time.Since(t0).Nanoseconds()
		}
		series["503_Throttled"] = TrimP99(I64sToF64s(samples))
		last["503_Throttled"] = rec
	}

	// Emit matrix.
	_ = os.MkdirAll(evidenceDir, 0o755)
	csvPath := filepath.Join(evidenceDir, "error_oracle_matrix.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	cw := csv.NewWriter(f)
	_ = cw.Write([]string{"scenario", "status", "body_len", "header_set", "mean_ns", "median_ns", "p95_ns", "p99_ns"})

	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var report strings.Builder
	fmt.Fprintf(&report, "# Error-oracle matrix\n\n")
	fmt.Fprintf(&report, "N per probe: %d\n\n", N)
	fmt.Fprintf(&report, "| Scenario | Status | BodyLen | Headers | Mean (ns) | Median (ns) | p95 | p99 |\n")
	fmt.Fprintf(&report, "|---|---|---|---|---|---|---|---|\n")
	for _, k := range keys {
		rec := last[k]
		body := rec.Body.String()
		bodyLen := len(body)
		// Header summary: comma-joined keys.
		hdrs := make([]string, 0, len(rec.HeaderMap))
		for h := range rec.HeaderMap {
			hdrs = append(hdrs, h)
		}
		sort.Strings(hdrs)
		hdrList := strings.Join(hdrs, ";")
		s := Summarise(series[k])
		_ = cw.Write([]string{
			k,
			fmt.Sprintf("%d", rec.Code),
			fmt.Sprintf("%d", bodyLen),
			hdrList,
			fmt.Sprintf("%.1f", s.Mean),
			fmt.Sprintf("%.1f", s.Median),
			fmt.Sprintf("%.1f", s.P95),
			fmt.Sprintf("%.1f", s.P99),
		})
		fmt.Fprintf(&report, "| %s | %d | %d | %s | %.1f | %.1f | %.1f | %.1f |\n",
			k, rec.Code, bodyLen, hdrList, s.Mean, s.Median, s.P95, s.P99)
	}
	cw.Flush()
	_ = f.Close()

	// Pair-wise Welch tests — any distinguishable pair is an oracle.
	fmt.Fprintf(&report, "\n## Pair-wise Welch t-test (p-values < 0.01 mark distinguishable pairs)\n\n")
	fmt.Fprintf(&report, "|  | %s |\n", strings.Join(keys, " | "))
	fmt.Fprintf(&report, "|---|%s\n", strings.Repeat("---|", len(keys)))
	for _, a := range keys {
		fmt.Fprintf(&report, "| **%s** |", a)
		for _, b := range keys {
			if a == b {
				fmt.Fprintf(&report, " - |")
				continue
			}
			tt := WelchTTest(series[a], series[b])
			fmt.Fprintf(&report, " p=%.2g d=%.1fns |", tt.PValue, tt.MeanDiff)
		}
		fmt.Fprintf(&report, "\n")
	}
	path := filepath.Join(evidenceDir, "error_oracle_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Error-oracle report: %s\nCSV: %s", path, csvPath)
}
