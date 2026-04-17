//go:build timing

package harness

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// MeasureLoop returns the elapsed time in nanoseconds for calling `fn` once,
// repeated N times.  The caller must have already set up GC, CPU pinning and
// warm-up.  We take the sample using time.Now so the cost is comparable
// between runs (no CPU-cycle counter).
func MeasureLoop(n int, fn func()) []int64 {
	out := make([]int64, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		fn()
		out[i] = time.Since(t0).Nanoseconds()
	}
	return out
}

// InterleavedMeasure alternates `fnA` and `fnB` in pairs.  Interleaving
// cancels drift caused by dynamic frequency scaling and CPU warm-up: any
// slow-down that affects one call equally affects the next, so systematic
// bias between A and B cancels.  Returns (a, b) where a[i] is a sample of fnA
// and b[i] is a sample of fnB collected immediately after.
func InterleavedMeasure(n int, fnA, fnB func()) (a, b []int64) {
	a = make([]int64, n)
	b = make([]int64, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		fnA()
		a[i] = time.Since(t0).Nanoseconds()
		t1 := time.Now()
		fnB()
		b[i] = time.Since(t1).Nanoseconds()
	}
	return a, b
}

// PreparePinned pins the current goroutine to a single OS thread and disables
// GC.  It returns a cleanup function.
func PreparePinned() func() {
	runtime.GC()
	runtime.GC()
	prevPct := debug.SetGCPercent(-1)
	runtime.LockOSThread()
	return func() {
		runtime.UnlockOSThread()
		debug.SetGCPercent(prevPct)
	}
}

// Warmup calls fn `n` times to prime CPU caches, branch predictors and
// any sync.Pool slots.
func Warmup(n int, fn func()) {
	for i := 0; i < n; i++ {
		fn()
	}
}

// AsciiHistogram returns a human-readable textual histogram of xs with `bins`
// buckets spanning [p01, p99], plus a tail summary.
func AsciiHistogram(xs []float64, bins int, width int) string {
	if len(xs) == 0 {
		return "<empty>"
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	lo := quantileSorted(cp, 0.01)
	hi := quantileSorted(cp, 0.99)
	if hi == lo {
		hi = lo + 1
	}
	counts := make([]int, bins)
	var below, above int
	step := (hi - lo) / float64(bins)
	for _, v := range xs {
		if v < lo {
			below++
			continue
		}
		if v > hi {
			above++
			continue
		}
		k := int((v - lo) / step)
		if k >= bins {
			k = bins - 1
		}
		counts[k]++
	}
	maxC := 0
	for _, c := range counts {
		if c > maxC {
			maxC = c
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("N=%d  lo(p01)=%.1fns hi(p99)=%.1fns step=%.2fns\n",
		len(xs), lo, hi, step))
	sb.WriteString(fmt.Sprintf("below p01: %d (%.3f%%)  above p99: %d (%.3f%%)\n",
		below, 100*float64(below)/float64(len(xs)),
		above, 100*float64(above)/float64(len(xs))))
	for i, c := range counts {
		bar := strings.Repeat("#", (c*width)/max1(maxC))
		left := lo + float64(i)*step
		sb.WriteString(fmt.Sprintf("[%6.1f..%6.1f) %7d | %s\n", left, left+step, c, bar))
	}
	return sb.String()
}

func max1(x int) int {
	if x < 1 {
		return 1
	}
	return x
}

// QQSample returns up to `k` points sampled uniformly from each distribution
// in rank order, suitable for an ASCII Q-Q scatter plot.
func QQSample(a, b []float64, k int) (qa, qb []float64) {
	sa := append([]float64(nil), a...)
	sb := append([]float64(nil), b...)
	sort.Float64s(sa)
	sort.Float64s(sb)
	if k > len(sa) {
		k = len(sa)
	}
	if k > len(sb) {
		k = len(sb)
	}
	qa = make([]float64, k)
	qb = make([]float64, k)
	for i := 0; i < k; i++ {
		q := float64(i+1) / float64(k+1)
		qa[i] = quantileSorted(sa, q)
		qb[i] = quantileSorted(sb, q)
	}
	return qa, qb
}

// QQAscii renders a lightweight scatter from two equal-size quantile slices.
func QQAscii(qa, qb []float64, size int) string {
	if len(qa) == 0 {
		return "<empty>"
	}
	lo, hi := qa[0], qa[0]
	for _, v := range qa {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	for _, v := range qb {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi == lo {
		hi = lo + 1
	}
	grid := make([][]byte, size)
	for i := range grid {
		grid[i] = make([]byte, size)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}
	// Diagonal (reference y=x).
	for i := 0; i < size; i++ {
		grid[size-1-i][i] = '.'
	}
	for i := 0; i < len(qa); i++ {
		x := int((qa[i] - lo) / (hi - lo) * float64(size-1))
		y := int((qb[i] - lo) / (hi - lo) * float64(size-1))
		if x >= 0 && x < size && y >= 0 && y < size {
			grid[size-1-y][x] = '#'
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Q-Q plot  lo=%.1fns hi=%.1fns\n", lo, hi))
	for _, row := range grid {
		sb.Write(row)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// WriteText writes a single string to a file, creating parent dirs as needed.
func WriteText(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// FormatTTest / FormatKS / FormatMWU return single-line result strings.
func FormatTTest(r TTestResult) string {
	return fmt.Sprintf("Welch t=%.4f df=%.1f p=%g meanA=%.2fns meanB=%.2fns diff=%.3fns d=%.4f",
		r.T, r.DF, r.PValue, r.MeanA, r.MeanB, r.MeanDiff, r.EffectSize)
}

func FormatKS(r KSResult) string {
	return fmt.Sprintf("KS    D=%.4f p=%g", r.D, r.PValue)
}

func FormatMWU(r MWUResult) string {
	return fmt.Sprintf("MWU   U=%.0f z=%.3f p=%g", r.U, r.Z, r.PValue)
}

// FormatSummary returns a short descriptive stats summary.
func FormatSummary(name string, s Summary) string {
	return fmt.Sprintf("%s: N=%d  mean=%.3f sd=%.3f  p01=%.1f p05=%.1f med=%.1f p95=%.1f p99=%.1f",
		name, s.N, s.Mean, s.StdDev, s.P01, s.P05, s.Median, s.P95, s.P99)
}

// -- safeguard to avoid NaN log messages
var _ = math.NaN
