//go:build timing

// Package harness contains the statistical tests used by the
// timing-and-sidechannel-analyst.  Pure Go, no external dependencies — scipy is
// not available in the audit environment, so we implement Welch's t-test,
// Kolmogorov–Smirnov and Mann–Whitney U ourselves.
//
// All tests are two-sided. p-values are computed from asymptotic
// approximations adequate for N ≥ 1e4 (Welch → Student's-t via regularised
// incomplete beta; KS → asymptotic series; MWU → normal approximation with
// continuity correction and tie adjustment).
package harness

import (
	"fmt"
	"math"
	"os"
	"sort"
)

// -----------------------------------------------------------------------------
// Descriptive statistics
// -----------------------------------------------------------------------------

type Summary struct {
	N        int
	Mean     float64
	Variance float64
	StdDev   float64
	Min      float64
	Max      float64
	Median   float64
	P01, P05 float64
	P95, P99 float64
}

// Summarise returns basic descriptive stats over a copy of xs.
func Summarise(xs []float64) Summary {
	s := Summary{N: len(xs)}
	if len(xs) == 0 {
		return s
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	s.Min = cp[0]
	s.Max = cp[len(cp)-1]
	s.Median = quantileSorted(cp, 0.50)
	s.P01 = quantileSorted(cp, 0.01)
	s.P05 = quantileSorted(cp, 0.05)
	s.P95 = quantileSorted(cp, 0.95)
	s.P99 = quantileSorted(cp, 0.99)
	var sum float64
	for _, v := range xs {
		sum += v
	}
	s.Mean = sum / float64(s.N)
	var ss float64
	for _, v := range xs {
		d := v - s.Mean
		ss += d * d
	}
	if s.N > 1 {
		s.Variance = ss / float64(s.N-1)
	}
	s.StdDev = math.Sqrt(s.Variance)
	return s
}

func quantileSorted(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

// TrimP99 returns a copy of xs with values above the 99th percentile removed.
// This is the "outlier trim" recommended by dudect — GC pauses and preemption
// produce a heavy right tail that dominates Welch's estimate of variance.
func TrimP99(xs []float64) []float64 {
	if len(xs) == 0 {
		return xs
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	cut := quantileSorted(cp, 0.99)
	out := make([]float64, 0, len(xs))
	for _, v := range xs {
		if v <= cut {
			out = append(out, v)
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Welch's t-test (two-sample, unequal variances)
// -----------------------------------------------------------------------------

type TTestResult struct {
	T          float64
	DF         float64
	PValue     float64
	MeanA      float64
	MeanB      float64
	MeanDiff   float64
	EffectSize float64 // |mean_a-mean_b| / pooled stddev (Cohen's d)
}

// WelchTTest computes Welch's two-sided t-test for a ≠ b.
func WelchTTest(a, b []float64) TTestResult {
	sa := Summarise(a)
	sb := Summarise(b)
	r := TTestResult{MeanA: sa.Mean, MeanB: sb.Mean, MeanDiff: sa.Mean - sb.Mean}
	if sa.N < 2 || sb.N < 2 {
		r.PValue = math.NaN()
		return r
	}
	va := sa.Variance / float64(sa.N)
	vb := sb.Variance / float64(sb.N)
	denom := math.Sqrt(va + vb)
	if denom == 0 {
		if r.MeanDiff == 0 {
			r.PValue = 1
		} else {
			r.PValue = 0
		}
		return r
	}
	r.T = r.MeanDiff / denom
	// Welch–Satterthwaite df approximation.
	num := (va + vb) * (va + vb)
	den := va*va/float64(sa.N-1) + vb*vb/float64(sb.N-1)
	r.DF = num / den
	// Two-sided p-value via survival function of Student's t.
	r.PValue = 2 * studentTSF(math.Abs(r.T), r.DF)
	if r.PValue > 1 {
		r.PValue = 1
	}
	// Pooled SD for Cohen's d.
	pooled := math.Sqrt((sa.Variance*float64(sa.N-1) + sb.Variance*float64(sb.N-1)) /
		float64(sa.N+sb.N-2))
	if pooled > 0 {
		r.EffectSize = math.Abs(r.MeanDiff) / pooled
	}
	return r
}

// studentTSF returns the right-tail probability P(T > t) for a Student-t with
// v degrees of freedom, using the incomplete beta function.
//
//	P(T > t) = 0.5 * I(v/(v+t^2); v/2, 1/2)  for t ≥ 0
func studentTSF(t, v float64) float64 {
	if t < 0 {
		return 1 - studentTSF(-t, v)
	}
	x := v / (v + t*t)
	return 0.5 * betainc(x, v/2, 0.5)
}

// betainc returns the regularised incomplete beta function I_x(a, b).
func betainc(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lnBeta := lgamma(a) + lgamma(b) - lgamma(a+b)
	front := math.Exp(math.Log(x)*a + math.Log(1-x)*b - lnBeta)
	// Use symmetry for faster convergence of the continued fraction.
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}
	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

func betaContinuedFraction(x, a, b float64) float64 {
	const maxIter = 200
	const eps = 3e-16
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < 1e-30 {
		d = 1e-30
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		m2 := 2 * m
		aa := float64(m) * (b - float64(m)) * x / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		h *= d * c
		aa = -(a + float64(m)) * (qab + float64(m)) * x /
			((a + float64(m2)) * (qap + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			return h
		}
	}
	return h
}

func lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// -----------------------------------------------------------------------------
// Kolmogorov–Smirnov two-sample test
// -----------------------------------------------------------------------------

type KSResult struct {
	D      float64
	PValue float64
}

// KS2 computes the two-sample KS statistic and an asymptotic p-value.
func KS2(a, b []float64) KSResult {
	sa := append([]float64(nil), a...)
	sb := append([]float64(nil), b...)
	sort.Float64s(sa)
	sort.Float64s(sb)
	i, j := 0, 0
	n1 := float64(len(sa))
	n2 := float64(len(sb))
	var d float64
	for i < len(sa) && j < len(sb) {
		if sa[i] <= sb[j] {
			i++
		} else {
			j++
		}
		diff := math.Abs(float64(i)/n1 - float64(j)/n2)
		if diff > d {
			d = diff
		}
	}
	// Asymptotic p-value (Smirnov 1948).
	en := math.Sqrt(n1 * n2 / (n1 + n2))
	lambda := (en + 0.12 + 0.11/en) * d
	return KSResult{D: d, PValue: ksProb(lambda)}
}

func ksProb(lambda float64) float64 {
	if lambda <= 0 {
		return 1
	}
	const tol = 1e-12
	var sum float64
	prev := 0.0
	sign := 1.0
	for j := 1; j <= 150; j++ {
		term := 2 * sign * math.Exp(-2*lambda*lambda*float64(j*j))
		sum += term
		if math.Abs(term) < tol*math.Abs(sum) || math.Abs(term) < 1e-300 {
			break
		}
		sign = -sign
		prev = sum
	}
	_ = prev
	if sum < 0 {
		return 0
	}
	if sum > 1 {
		return 1
	}
	return sum
}

// -----------------------------------------------------------------------------
// Mann–Whitney U test (Wilcoxon rank-sum)
// -----------------------------------------------------------------------------

type MWUResult struct {
	U      float64
	Z      float64
	PValue float64
}

// MannWhitneyU computes the two-sided MWU test with normal approximation and
// tie correction.  Suitable for N ≥ 20; we use N ≥ 1e4 in this audit.
func MannWhitneyU(a, b []float64) MWUResult {
	n1 := len(a)
	n2 := len(b)
	all := make([]float64, 0, n1+n2)
	all = append(all, a...)
	all = append(all, b...)
	// Rank with tie correction.
	idx := make([]int, len(all))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return all[idx[i]] < all[idx[j]] })
	ranks := make([]float64, len(all))
	i := 0
	var tieCorrection float64
	for i < len(idx) {
		j := i
		for j+1 < len(idx) && all[idx[j+1]] == all[idx[i]] {
			j++
		}
		avg := (float64(i) + float64(j) + 2) / 2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		t := float64(j - i + 1)
		tieCorrection += t*t*t - t
		i = j + 1
	}
	var r1 float64
	for k := 0; k < n1; k++ {
		r1 += ranks[k]
	}
	u1 := r1 - float64(n1)*float64(n1+1)/2
	u2 := float64(n1)*float64(n2) - u1
	u := math.Min(u1, u2)
	n := float64(n1 + n2)
	mu := float64(n1) * float64(n2) / 2
	varU := float64(n1) * float64(n2) * (n + 1) / 12
	if tieCorrection > 0 {
		varU -= float64(n1) * float64(n2) * tieCorrection / (12 * n * (n - 1))
	}
	if varU <= 0 {
		return MWUResult{U: u, Z: 0, PValue: 1}
	}
	// Continuity-corrected z.
	z := (math.Abs(u-mu) - 0.5) / math.Sqrt(varU)
	p := 2 * (1 - normalCDF(z))
	if p > 1 {
		p = 1
	}
	return MWUResult{U: u, Z: z, PValue: p}
}

func normalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// WriteCSV writes a single-column CSV of float64s.  No header; one value per line.
func WriteCSV(path string, xs []float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, v := range xs {
		if _, err := fmt.Fprintf(f, "%.3f\n", v); err != nil {
			return err
		}
	}
	return nil
}

// WriteTwoColumnCSV writes two float columns side-by-side.
func WriteTwoColumnCSV(path string, a, b []float64, headerA, headerB string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "%s,%s\n", headerA, headerB)
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var va, vb string
		if i < len(a) {
			va = fmt.Sprintf("%.3f", a[i])
		}
		if i < len(b) {
			vb = fmt.Sprintf("%.3f", b[i])
		}
		fmt.Fprintf(f, "%s,%s\n", va, vb)
	}
	return nil
}

// I64sToF64s converts a slice of int64 nanoseconds to float64 nanoseconds.
func I64sToF64s(xs []int64) []float64 {
	out := make([]float64, len(xs))
	for i, v := range xs {
		out[i] = float64(v)
	}
	return out
}
