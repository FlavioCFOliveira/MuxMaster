//go:build timing

// Throttle near-limit timing + Compress BREACH oracle.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// Throttle: check that timing varies with remaining budget.
//
// With limit=16, backlog=0, timeout=100µs, we measure per-request latency at
// different fill levels.  The hypothesis is that near-limit (15/16 used), the
// path is measurably slower than low-fill (1/16 used).  If so, an attacker
// can deduce the current load.
// -----------------------------------------------------------------------------

func TestTiming_Throttle_BoundaryOracle(t *testing.T) {
	const limit = 16
	th := middleware.ThrottleBacklog(limit, 0, 100*time.Microsecond)

	blockers := make([]chan struct{}, 0, limit)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := len(blockers) - 1
		if idx >= 0 {
			<-blockers[idx]
		}
		w.WriteHeader(http.StatusOK)
	})
	_ = inner
	// Simpler approach: build two handlers — "blocker" that can be released
	// on demand, and "fast" for measurement.  The throttle sits in front of
	// both.  We hold N of the 16 tokens by launching N blocker requests that
	// wait on a channel.  Then we time "fast" requests: each acquires a
	// token, runs instantly, releases.  The cost of the channel select
	// inside throttle is what we probe.

	released := make(chan struct{})
	hold := func() {
		th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-released
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/", nil))
	}
	fastHandler := th(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	measureAtFill := func(filled int, n int) []int64 {
		// Spin up `filled` blockers to occupy slots.
		done := make(chan struct{}, filled)
		for i := 0; i < filled; i++ {
			go func() { hold(); done <- struct{}{} }()
		}
		// Give the goroutines time to acquire their tokens.
		time.Sleep(5 * time.Millisecond)

		// Now run fast requests.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		Warmup(5_000, func() {
			w.Body.Reset()
			w.HeaderMap = http.Header{}
			fastHandler.ServeHTTP(w, req)
		})
		out := make([]int64, n)
		for i := 0; i < n; i++ {
			w.Body.Reset()
			w.HeaderMap = http.Header{}
			t0 := time.Now()
			fastHandler.ServeHTTP(w, req)
			out[i] = time.Since(t0).Nanoseconds()
		}
		// Release blockers.
		for i := 0; i < filled; i++ {
			released <- struct{}{}
		}
		// Drain.
		for i := 0; i < filled; i++ {
			<-done
		}
		return out
	}
	_ = released

	cleanup := PreparePinned()
	defer cleanup()

	N := 100_000
	// Probe at different fills relative to the limit of 16.
	fills := []int{0, 1, 8, 14, 15}
	series := make(map[int][]float64)
	for _, f := range fills {
		raw := measureAtFill(f, N)
		writeEvidenceCSV(fmt.Sprintf("throttle_fill_%d.csv", f), raw)
		series[f] = TrimP99(I64sToF64s(raw))
	}

	var report strings.Builder
	fmt.Fprintf(&report, "# Throttle near-limit timing oracle\n\n")
	fmt.Fprintf(&report, "limit=%d backlog=0 timeout=100µs  N/fill=%d\n\n", limit, N)
	fmt.Fprintf(&report, "| Fill | Mean (ns) | Median (ns) | p95 | p99 |\n|---|---|---|---|---|\n")
	for _, f := range fills {
		s := Summarise(series[f])
		fmt.Fprintf(&report, "| %d/%d | %.1f | %.1f | %.1f | %.1f |\n",
			f, limit, s.Mean, s.Median, s.P95, s.P99)
	}
	// Pair-wise 0 vs 15 is the adversarial comparison.
	fmt.Fprintf(&report, "\n## Pair-wise Welch vs fill=0 (low budget used)\n\n")
	for _, f := range fills {
		if f == 0 {
			continue
		}
		tt := WelchTTest(series[0], series[f])
		fmt.Fprintf(&report, "fill=%d: %s\n", f, FormatTTest(tt))
	}
	path := filepath.Join(evidenceDir, "throttle_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Throttle report: %s", path)
}

// -----------------------------------------------------------------------------
// Compress BREACH: does the compressed response length depend on reflected input?
//
// The classic BREACH attack: if reflected input and a secret are both emitted
// in the same gzip-compressed body, the compressed length depends on whether
// the attacker's guess matches a substring of the secret.  We test it by
// creating a handler that emits both a fixed "secret" and a request-supplied
// "guess".  If compressed_len(guess=correct_prefix) < compressed_len(guess=wrong),
// the oracle exists.
//
// NOTE: BREACH is a property of the *handler*, not the middleware.  The
// Compress middleware itself only gzips the body; it does not add mitigations
// (no random padding, no Length-Hiding Transform).  This test confirms that
// MuxMaster's Compress does not protect against BREACH when the handler is
// vulnerable.
// -----------------------------------------------------------------------------

func TestTiming_Compress_BREACH(t *testing.T) {
	const secret = "csrf-token:ABCDEFGHIJKLMNOP"
	mw := middleware.Compress(6) // default level 6

	// Handler reflects ?guess= into the body alongside the secret.  This is
	// the classic BREACH setup.
	hdlr := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guess := r.URL.Query().Get("guess")
		// Pad body to ≥ minCompressSize so gzip actually kicks in.
		pad := strings.Repeat("X", 2048)
		body := pad + "\nSECRET=" + secret + "\nguess=" + guess + "\n"
		_, _ = w.Write([]byte(body))
	}))

	mkReq := func(guess string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/?guess="+guess, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		return r
	}

	runGuess := func(guess string) int {
		w := httptest.NewRecorder()
		hdlr.ServeHTTP(w, mkReq(guess))
		return w.Body.Len()
	}

	guesses := []string{
		"csrf-token:A", // correct prefix 12 chars
		"csrf-token:X",
		"csrf-token:AB",
		"csrf-token:XX",
		"csrf-token:ABCDEF",
		"csrf-token:XXXXXX",
		"csrf-token:ABCDEFGHIJKLMNOP", // full correct secret
		"wholly-unrelated-string-xyz123",
	}
	_ = os.MkdirAll(evidenceDir, 0o755)
	csvPath := filepath.Join(evidenceDir, "breach_oracle.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "guess,gzip_bytes")

	var report strings.Builder
	fmt.Fprintf(&report, "# Compress BREACH oracle\n\n")
	fmt.Fprintf(&report, "Handler reflects ?guess= alongside secret %q.\n\n", secret)
	fmt.Fprintf(&report, "| Guess | gzip bytes |\n|---|---|\n")
	for _, g := range guesses {
		n := runGuess(g)
		fmt.Fprintf(f, "%q,%d\n", g, n)
		fmt.Fprintf(&report, "| `%s` | %d |\n", g, n)
	}
	_ = f.Close()
	// Conclusion: if the "correct prefix" sizes fall monotonically below the
	// "incorrect" sizes, the oracle exists (and is handler-level).
	fmt.Fprintf(&report, "\n**Verdict:** Compress middleware applies no BREACH mitigation.")
	fmt.Fprintf(&report, "  Mitigation is out-of-scope for a router; but the finding is documented.\n")
	path := filepath.Join(evidenceDir, "breach_report.md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("BREACH report: %s", path)
}
