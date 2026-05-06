//go:build timing

// Timing & side-channel measurements for the radix-tree route lookup.
//
// Vector H-011: route-existence oracle via getValue.
//   - /api/v1/users       — registered
//   - /api/v1/pictures    — similar-but-unregistered (shares prefix)
//   - /totally-random-xyz — totally unrelated
//
// Each pair is measured by calling Mux.ServeHTTP directly (no kernel TCP) —
// httptest.NewServer is deliberately avoided because the kernel stack adds
// µs-scale noise that dwarfs the expected nanosecond-scale leak.
//
// Also covers RedirectFixedPath: a path that can be canonicalised
// (/api//v1/users → /api/v1/users) follows a longer code path than a path
// that cannot be canonicalised.  We quantify that leak.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// buildMux returns a Mux pre-populated with a realistic shape.
func buildMux() *muxmaster.Mux {
	m := muxmaster.New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// A mix of static, parameter and catch-all routes.
	m.GET("/", h)
	m.GET("/health", h)
	m.GET("/api/v1/users", h)
	m.GET("/api/v1/users/:id", h)
	m.GET("/api/v1/users/:id/posts", h)
	m.GET("/api/v1/admin/secret", h) // "hidden" route
	m.GET("/api/v1/orders", h)
	m.GET("/static/*filepath", h)
	return m
}

// callFor serves a request with the given URL path.
func callFor(m http.Handler, path string) func() {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	return func() {
		w.Body.Reset()
		w.HeaderMap = http.Header{}
		m.ServeHTTP(w, req)
	}
}

// -----------------------------------------------------------------------------
// H-011: route-existence oracle
// -----------------------------------------------------------------------------

func TestTiming_H011_RouteExistence(t *testing.T) {
	m := buildMux()
	registered := callFor(m, "/api/v1/users")
	similar := callFor(m, "/api/v1/pictures")       // shares /api/v1/ prefix
	random := callFor(m, "/totally-random-xyz-999") // unrelated
	hidden := callFor(m, "/api/v1/admin/secret")    // registered, likely undisclosed

	cleanup := PreparePinned()
	defer cleanup()

	Warmup(20_000, registered)
	Warmup(20_000, similar)
	Warmup(20_000, random)
	Warmup(20_000, hidden)

	var report strings.Builder
	fmt.Fprintf(&report, "# H-011 — route-existence timing oracle\n\n")
	fmt.Fprintf(&report, "Host: %s  Go: %s  N/run: %d  Runs: %d\n\n",
		runtime.GOOS+"/"+runtime.GOARCH, runtime.Version(), nSamples, nRuns)

	// Matrix of pairs to compare.
	pairs := []struct {
		name  string
		fA    func()
		fB    func()
		label string
	}{
		{"reg_vs_similar", registered, similar, "registered vs similar-unregistered"},
		{"reg_vs_random", registered, random, "registered vs totally-random"},
		{"hidden_vs_similar", hidden, similar, "hidden-admin vs similar-unregistered"},
		{"hidden_vs_random", hidden, random, "hidden-admin vs totally-random"},
		{"similar_vs_random", similar, random, "two different non-existent paths"},
	}
	var worstPs []float64
	for _, p := range pairs {
		fmt.Fprintf(&report, "## %s\n", p.label)
		worst := 1.0
		for run := 1; run <= nRuns; run++ {
			a, b := InterleavedMeasure(nSamples, p.fA, p.fB)
			if run == 1 {
				writeEvidenceCSV(fmt.Sprintf("h011_%s_A.csv", p.name), a)
				writeEvidenceCSV(fmt.Sprintf("h011_%s_B.csv", p.name), b)
			}
			af := TrimP99(I64sToF64s(a))
			bf := TrimP99(I64sToF64s(b))
			sa := Summarise(af)
			sb := Summarise(bf)
			tt := WelchTTest(af, bf)
			ks := KS2(af, bf)
			mwu := MannWhitneyU(af, bf)
			fmt.Fprintf(&report, "Run %d\n", run)
			fmt.Fprintf(&report, "  %s\n", FormatSummary("A", sa))
			fmt.Fprintf(&report, "  %s\n", FormatSummary("B", sb))
			fmt.Fprintf(&report, "  %s\n", FormatTTest(tt))
			fmt.Fprintf(&report, "  %s\n", FormatKS(ks))
			fmt.Fprintf(&report, "  %s\n", FormatMWU(mwu))
			if tt.PValue < worst {
				worst = tt.PValue
			}
		}
		fmt.Fprintf(&report, "  worst p=%g\n\n", worst)
		worstPs = append(worstPs, worst)
	}
	path := filepath.Join(evidenceDir, "h011_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("H-011 report written to %s; worst p-values: %v", path, worstPs)
}

// -----------------------------------------------------------------------------
// RedirectFixedPath oracle: canonicalisable vs not.
// -----------------------------------------------------------------------------

func TestTiming_RedirectFixedPath_Oracle(t *testing.T) {
	m := buildMux()
	// A path that path.Clean will canonicalise to a registered route.
	// /api//v1/users → /api/v1/users (registered)
	canon := callFor(m, "/api//v1/users")
	// A path with a double-slash that does NOT resolve to a registered route.
	nonCanon := callFor(m, "/totally//random-xyz")
	// Plain 404 with no canonicalisation possible.
	plain := callFor(m, "/totally-random-xyz-999")

	cleanup := PreparePinned()
	defer cleanup()

	Warmup(20_000, canon)
	Warmup(20_000, nonCanon)
	Warmup(20_000, plain)

	var report strings.Builder
	fmt.Fprintf(&report, "# RedirectFixedPath oracle\n\n")
	fmt.Fprintf(&report, "Host: %s  Go: %s  N/run: %d  Runs: %d\n\n",
		runtime.GOOS+"/"+runtime.GOARCH, runtime.Version(), nSamples, nRuns)

	pairs := []struct {
		name   string
		fA, fB func()
		label  string
	}{
		{"canon_vs_noncanon", canon, nonCanon, "canonicalisable 404 vs double-slash 404 that doesn't match"},
		{"canon_vs_plain", canon, plain, "canonicalisable 404 (reveals hidden route) vs plain 404"},
		{"noncanon_vs_plain", nonCanon, plain, "double-slash non-canonical vs plain"},
	}
	for _, p := range pairs {
		fmt.Fprintf(&report, "## %s\n", p.label)
		worst := 1.0
		for run := 1; run <= nRuns; run++ {
			a, b := InterleavedMeasure(nSamples/2, p.fA, p.fB) // half samples – redirect path is ~60ns
			if run == 1 {
				writeEvidenceCSV(fmt.Sprintf("rfp_%s_A.csv", p.name), a)
				writeEvidenceCSV(fmt.Sprintf("rfp_%s_B.csv", p.name), b)
			}
			af := TrimP99(I64sToF64s(a))
			bf := TrimP99(I64sToF64s(b))
			sa := Summarise(af)
			sb := Summarise(bf)
			tt := WelchTTest(af, bf)
			ks := KS2(af, bf)
			mwu := MannWhitneyU(af, bf)
			fmt.Fprintf(&report, "Run %d\n", run)
			fmt.Fprintf(&report, "  %s\n", FormatSummary("A", sa))
			fmt.Fprintf(&report, "  %s\n", FormatSummary("B", sb))
			fmt.Fprintf(&report, "  %s\n", FormatTTest(tt))
			fmt.Fprintf(&report, "  %s\n", FormatKS(ks))
			fmt.Fprintf(&report, "  %s\n", FormatMWU(mwu))
			if tt.PValue < worst {
				worst = tt.PValue
			}
		}
		fmt.Fprintf(&report, "  worst p=%g\n\n", worst)
	}
	path := filepath.Join(evidenceDir, "rfp_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("RedirectFixedPath report written to %s", path)
}
