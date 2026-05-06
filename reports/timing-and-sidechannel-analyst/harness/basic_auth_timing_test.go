//go:build timing

// Timing & side-channel measurements for middleware/basic_auth.
//
// Vector H-002: User enumeration via map lookup before subtle.ConstantTimeCompare.
//
// Two adversarial comparisons are made:
//
//  1. "user exists, wrong password"  vs  "user does not exist"
//     - "alice" is in the credentials map; "charlie" is not.
//     - The middleware path for "alice" runs the map lookup, then
//     subtle.ConstantTimeCompare, then the WWW-Authenticate header write.
//     - The middleware path for "charlie" skips subtle.ConstantTimeCompare
//     (map lookup misses) and goes directly to the 401 path.
//     - Hypothesis H_1 (leak): the two paths are distinguishable in mean or
//     distribution at N = 5e5 samples, tripled.
//
//  2. "wrong password, same length"  vs  "wrong password, different length"
//     - Both users exist; both passwords miss subtle.ConstantTimeCompare; we
//     verify that the compare itself is constant-time.
//
// Interleaving is used so that CPU-freq drift cancels across A/B pairs.
// p99 trim is applied before Welch to reduce GC/preemption noise.
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

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const evidenceDir = "../evidence/2026-04-17"

// nSamples controls the per-run sample size.  1e6 is the doctrine; 5e5 is used
// to keep the audit within wall-clock budget on a shared 16-core Ryzen 9
// 5900HX (not an isolated core).  We document the limitation in the report.
const nSamples = 500_000

// nRuns is the number of triplicated independent runs.
const nRuns = 3

func writeEvidenceCSV(name string, ns []int64) string {
	_ = os.MkdirAll(evidenceDir, 0o755)
	p := filepath.Join(evidenceDir, name)
	xs := I64sToF64s(ns)
	if err := WriteCSV(p, xs); err != nil {
		panic(err)
	}
	return p
}

// buildAuth returns a handler chain equivalent to what a production service
// would use: Middleware.BasicAuth(realm, creds) → inner 200-OK handler.
func buildAuth(creds map[string]string) http.Handler {
	mw := middleware.BasicAuth("test", creds)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mw(inner)
}

// callAuth performs a single BasicAuth request with (user, pass).
// The request is constructed outside the timed region.
func mkReq(user, pass string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth(user, pass)
	return r
}

// -----------------------------------------------------------------------------
// H-002: user-exists vs user-does-not-exist
// -----------------------------------------------------------------------------

func TestTiming_H002_UserEnumeration(t *testing.T) {
	creds := map[string]string{
		"alice":   "correct-horse-battery-staple-2026",
		"bob":     "correct-horse-battery-staple-2026",
		"daniela": "correct-horse-battery-staple-2026",
	}
	handler := buildAuth(creds)

	// Interleave to cancel drift.
	w := httptest.NewRecorder()
	existReq := mkReq("alice", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")      // user exists, wrong pw
	nonexistReq := mkReq("charlie", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx") // user absent

	// Precompute and reuse one recorder to avoid allocating in the hot loop.
	callExist := func() {
		w.Body.Reset()
		w.HeaderMap = http.Header{}
		handler.ServeHTTP(w, existReq)
	}
	callNonexist := func() {
		w.Body.Reset()
		w.HeaderMap = http.Header{}
		handler.ServeHTTP(w, nonexistReq)
	}

	cleanup := PreparePinned()
	defer cleanup()

	// Warm-up — discard 20 000 iterations of each.
	Warmup(20_000, callExist)
	Warmup(20_000, callNonexist)

	var report strings.Builder
	fmt.Fprintf(&report, "# H-002 — basic_auth user enumeration timing\n\n")
	fmt.Fprintf(&report, "Host: %s  Go: %s  N/run: %d  Runs: %d\n\n",
		runtime.GOOS+"/"+runtime.GOARCH, runtime.Version(), nSamples, nRuns)

	var tResults []TTestResult
	var ksResults []KSResult
	var mwuResults []MWUResult
	for run := 1; run <= nRuns; run++ {
		fmt.Fprintf(&report, "## Run %d\n", run)
		eRaw, nRaw := InterleavedMeasure(nSamples, callExist, callNonexist)

		// Write raw CSVs for first run only to save disk.
		if run == 1 {
			writeEvidenceCSV("h002_user_exists_run1.csv", eRaw)
			writeEvidenceCSV("h002_user_absent_run1.csv", nRaw)
		}

		e := TrimP99(I64sToF64s(eRaw))
		nF := TrimP99(I64sToF64s(nRaw))
		sa := Summarise(e)
		sb := Summarise(nF)
		tt := WelchTTest(e, nF)
		ks := KS2(e, nF)
		mwu := MannWhitneyU(e, nF)
		tResults = append(tResults, tt)
		ksResults = append(ksResults, ks)
		mwuResults = append(mwuResults, mwu)
		fmt.Fprintf(&report, "%s\n", FormatSummary("exist   ", sa))
		fmt.Fprintf(&report, "%s\n", FormatSummary("absent  ", sb))
		fmt.Fprintf(&report, "%s\n", FormatTTest(tt))
		fmt.Fprintf(&report, "%s\n", FormatKS(ks))
		fmt.Fprintf(&report, "%s\n\n", FormatMWU(mwu))
	}
	// Histogram & Q-Q from last run only (still representative).
	// Compute once more to keep the sample fresh.
	eRaw, nRaw := InterleavedMeasure(nSamples, callExist, callNonexist)
	e := TrimP99(I64sToF64s(eRaw))
	nF := TrimP99(I64sToF64s(nRaw))
	fmt.Fprintf(&report, "## Histogram — user exists (wrong password)\n```\n%s\n```\n",
		AsciiHistogram(e, 20, 60))
	fmt.Fprintf(&report, "## Histogram — user absent\n```\n%s\n```\n",
		AsciiHistogram(nF, 20, 60))
	qa, qb := QQSample(e, nF, 40)
	fmt.Fprintf(&report, "## Q-Q plot (exists vs absent)\n```\n%s\n```\n",
		QQAscii(qa, qb, 30))

	// Verdict logic.
	worstP := 1.0
	for _, r := range tResults {
		if r.PValue < worstP {
			worstP = r.PValue
		}
	}
	fmt.Fprintf(&report, "\n## Worst-case Welch p-value across %d runs: %g\n", nRuns, worstP)

	path := filepath.Join(evidenceDir, "h002_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("H-002 report written to %s", path)
	t.Logf("Worst-case p-value: %g", worstP)
}

// -----------------------------------------------------------------------------
// H-002b: wrong-password equal-length vs different-length
//         (constant-time-compare sanity check)
// -----------------------------------------------------------------------------

func TestTiming_H002b_PasswordLengthOracle(t *testing.T) {
	creds := map[string]string{
		"alice": "correct-horse-battery-staple-2026",
	}
	handler := buildAuth(creds)

	w := httptest.NewRecorder()
	// Same length as the stored password (33 chars).
	sameLenReq := mkReq("alice", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	// Very different length.
	diffLenReq := mkReq("alice", "x")
	callSame := func() {
		w.Body.Reset()
		w.HeaderMap = http.Header{}
		handler.ServeHTTP(w, sameLenReq)
	}
	callDiff := func() {
		w.Body.Reset()
		w.HeaderMap = http.Header{}
		handler.ServeHTTP(w, diffLenReq)
	}

	cleanup := PreparePinned()
	defer cleanup()

	Warmup(20_000, callSame)
	Warmup(20_000, callDiff)

	var report strings.Builder
	fmt.Fprintf(&report, "# H-002b — basic_auth password-length timing\n\n")
	fmt.Fprintf(&report, "Host: %s  Go: %s  N/run: %d  Runs: %d\n\n",
		runtime.GOOS+"/"+runtime.GOARCH, runtime.Version(), nSamples, nRuns)

	var tResults []TTestResult
	for run := 1; run <= nRuns; run++ {
		fmt.Fprintf(&report, "## Run %d\n", run)
		sRaw, dRaw := InterleavedMeasure(nSamples, callSame, callDiff)
		if run == 1 {
			writeEvidenceCSV("h002b_pwlen_same.csv", sRaw)
			writeEvidenceCSV("h002b_pwlen_diff.csv", dRaw)
		}
		s := TrimP99(I64sToF64s(sRaw))
		d := TrimP99(I64sToF64s(dRaw))
		sa := Summarise(s)
		sb := Summarise(d)
		tt := WelchTTest(s, d)
		tResults = append(tResults, tt)
		fmt.Fprintf(&report, "%s\n", FormatSummary("samelen ", sa))
		fmt.Fprintf(&report, "%s\n", FormatSummary("difflen ", sb))
		fmt.Fprintf(&report, "%s\n", FormatTTest(tt))
		fmt.Fprintf(&report, "%s\n", FormatKS(KS2(s, d)))
		fmt.Fprintf(&report, "%s\n\n", FormatMWU(MannWhitneyU(s, d)))
	}
	worstP := 1.0
	for _, r := range tResults {
		if r.PValue < worstP {
			worstP = r.PValue
		}
	}
	fmt.Fprintf(&report, "\n## Worst-case Welch p-value across %d runs: %g\n", nRuns, worstP)
	path := filepath.Join(evidenceDir, "h002b_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("H-002b report written to %s", path)
	t.Logf("Worst-case p-value: %g", worstP)
}
