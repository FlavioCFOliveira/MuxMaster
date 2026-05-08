//go:build timing

// Package harness provides statistical helpers for timing side-channel analysis.
// All functions operate on raw nanosecond samples ([]int64).
package harness

import (
	"math"
	"sort"
)

// mean returns the arithmetic mean of samples.
func mean(samples []int64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		sum += float64(v)
	}
	return sum / float64(len(samples))
}

// variance returns the unbiased sample variance.
func variance(samples []int64) float64 {
	if len(samples) < 2 {
		return 0
	}
	m := mean(samples)
	var sum float64
	for _, v := range samples {
		d := float64(v) - m
		sum += d * d
	}
	return sum / float64(len(samples)-1)
}

// stddev returns the sample standard deviation.
func stddev(samples []int64) float64 {
	return math.Sqrt(variance(samples))
}

// percentile returns the p-th percentile (0–100) of samples.
// samples must be sorted.
func percentile(sorted []int64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100.0 * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return float64(sorted[lo])
	}
	return float64(sorted[lo])*(float64(hi)-idx) + float64(sorted[hi])*(idx-float64(lo))
}

// trimOutliers removes samples above the p99 quantile (reduces GC-pause contamination).
func trimOutliers(samples []int64) []int64 {
	s := make([]int64, len(samples))
	copy(s, samples)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	p99 := percentile(s, 99.0)
	var out []int64
	for _, v := range s {
		if float64(v) <= p99 {
			out = append(out, v)
		}
	}
	return out
}

// welchTTest returns the two-tailed p-value for Welch's t-test between a and b.
// Uses a t-distribution approximation via the regularised incomplete beta function.
func welchTTest(a, b []int64) float64 {
	na, nb := float64(len(a)), float64(len(b))
	if na < 2 || nb < 2 {
		return 1.0
	}
	va, vb := variance(a), variance(b)
	if va == 0 && vb == 0 {
		if mean(a) == mean(b) {
			return 1.0
		}
		return 0.0
	}
	se := math.Sqrt(va/na + vb/nb)
	if se == 0 {
		return 1.0
	}
	t := math.Abs((mean(a) - mean(b)) / se)

	// Welch–Satterthwaite degrees of freedom
	df := math.Pow(va/na+vb/nb, 2) /
		(math.Pow(va/na, 2)/(na-1) + math.Pow(vb/nb, 2)/(nb-1))

	// Two-tailed p-value approximation using the regularised incomplete beta function.
	// p = I(df/(df+t^2), df/2, 1/2)
	x := df / (df + t*t)
	p := regIncBeta(df/2.0, 0.5, x)
	return p
}

// ksTwoSample returns the p-value for the two-sample Kolmogorov-Smirnov test.
// Both slices must be sorted in ascending order.
func ksTwoSample(a, b []int64) float64 {
	na, nb := float64(len(a)), float64(len(b))
	if na == 0 || nb == 0 {
		return 1.0
	}
	// Compute maximum absolute difference in empirical CDFs.
	var maxD float64
	ia, ib := 0, 0
	for ia < len(a) || ib < len(b) {
		var av, bv int64 = math.MaxInt64, math.MaxInt64
		if ia < len(a) {
			av = a[ia]
		}
		if ib < len(b) {
			bv = b[ib]
		}
		var step int64
		if av <= bv {
			step = av
		} else {
			step = bv
		}
		for ia < len(a) && a[ia] == step {
			ia++
		}
		for ib < len(b) && b[ib] == step {
			ib++
		}
		d := math.Abs(float64(ia)/na - float64(ib)/nb)
		if d > maxD {
			maxD = d
		}
	}
	// Approximate p-value via the Kolmogorov distribution.
	en := math.Sqrt(na * nb / (na + nb))
	lambda := (en + 0.12 + 0.11/en) * maxD
	return kolmogorovP(lambda)
}

// mannWhitneyU returns the two-tailed p-value for the Mann–Whitney U test.
// Uses the rank-sum formula (O(n log n)) with normal approximation — valid for n > 20.
// Both a and b must already be sorted in ascending order (trimOutliers sorts them).
func mannWhitneyU(a, b []int64) float64 {
	na, nb := len(a), len(b)
	if na == 0 || nb == 0 {
		return 1.0
	}
	// Compute rank sum R1 for sample a by merging the two sorted slices.
	// Rank of a value is its 1-based position in the combined sorted sequence.
	// Ties are handled by assigning average ranks.
	n := na + nb
	combined := make([]int64, n)
	copy(combined[:na], a)
	copy(combined[na:], b)
	// combined is NOT sorted yet — sort it.
	sort.Slice(combined, func(i, j int) bool { return combined[i] < combined[j] })

	// Assign ranks with average tie-breaking.
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i + 1
		for j < n && combined[j] == combined[i] {
			j++
		}
		avgRank := float64(i+1+j) / 2.0
		for k := i; k < j; k++ {
			ranks[k] = avgRank
		}
		i = j
	}

	// Build rank map: value → average rank (for ties, same value → same avg rank).
	// Since combined is sorted, we can use binary search to assign ranks to a.
	rankOf := func(v int64) float64 {
		// Find any occurrence of v in combined (sorted), return its pre-assigned rank.
		lo, hi := 0, n-1
		for lo <= hi {
			mid := (lo + hi) / 2
			if combined[mid] == v {
				return ranks[mid]
			} else if combined[mid] < v {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		return 0
	}

	var r1 float64
	for _, v := range a {
		r1 += rankOf(v)
	}

	u1 := r1 - float64(na)*float64(na+1)/2.0
	mu := float64(na) * float64(nb) / 2.0

	// Tie-correction for variance.
	// Compute sum of (t^3 - t) over all tied groups.
	var tieSum float64
	for i := 0; i < n; {
		j := i + 1
		for j < n && combined[j] == combined[i] {
			j++
		}
		t := float64(j - i)
		if t > 1 {
			tieSum += t*t*t - t
		}
		i = j
	}
	nf := float64(n)
	sigma2 := float64(na) * float64(nb) / 12.0 * (nf + 1.0 - tieSum/(nf*(nf-1.0)))
	if sigma2 <= 0 {
		return 1.0
	}
	sigma := math.Sqrt(sigma2)
	z := math.Abs(u1-mu) / sigma
	// Two-tailed p = 2 * (1 - Phi(z))
	return 2.0 * (1.0 - phi(z))
}

// ── math helpers ──────────────────────────────────────────────────────────────

// phi returns the standard normal CDF Phi(z) using Abramowitz & Stegun 26.2.17.
func phi(z float64) float64 {
	if z < 0 {
		return 1 - phi(-z)
	}
	t := 1.0 / (1.0 + 0.2316419*z)
	poly := t * (0.319381530 +
		t*(-0.356563782+
			t*(1.781477937+
				t*(-1.821255978+
					t*1.330274429))))
	return 1.0 - (1.0/math.Sqrt(2*math.Pi))*math.Exp(-0.5*z*z)*poly
}

// kolmogorovP returns the p-value for the Kolmogorov distribution.
func kolmogorovP(lambda float64) float64 {
	if lambda <= 0 {
		return 1.0
	}
	var sum float64
	for j := 1; j <= 50; j++ {
		sign := 1.0
		if j%2 == 0 {
			sign = -1.0
		}
		sum += sign * math.Exp(-2.0*float64(j)*float64(j)*lambda*lambda)
	}
	p := 2.0 * sum
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// regIncBeta approximates I_x(a,b) — the regularised incomplete beta function —
// used as the CDF of the t-distribution for the Welch p-value.
// Implemented via the continued-fraction representation (Lentz algorithm).
func regIncBeta(a, b, x float64) float64 {
	if x < 0 || x > 1 {
		return 0
	}
	if x == 0 {
		return 0
	}
	if x == 1 {
		return 1
	}
	// Use symmetry relation when x > (a+1)/(a+b+2).
	if x > (a+1)/(a+b+2) {
		return 1 - regIncBeta(b, a, 1-x)
	}
	lbeta := lgamma(a+b) - lgamma(a) - lgamma(b)
	front := math.Exp(lbeta + a*math.Log(x) + b*math.Log(1-x))
	return front * betaCF(a, b, x) / a
}

// betaCF evaluates the continued fraction for the incomplete beta function.
func betaCF(a, b, x float64) float64 {
	const maxIter = 200
	const eps = 3e-7
	const tiny = 1e-30

	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1.0 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1.0 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		mf := float64(m)
		m2 := 2 * mf
		// Even step.
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1.0 / d
		h *= d * c
		// Odd step.
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1.0 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// lgamma is a thin wrapper around math.Lgamma to keep code readable.
func lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// SummaryStats holds descriptive statistics for a sample set.
type SummaryStats struct {
	N    int
	Mean float64
	Std  float64
	P50  float64
	P95  float64
	P99  float64
}

// Summarise computes descriptive statistics for raw samples.
func Summarise(samples []int64) SummaryStats {
	s := make([]int64, len(samples))
	copy(s, samples)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return SummaryStats{
		N:    len(s),
		Mean: mean(s),
		Std:  stddev(s),
		P50:  percentile(s, 50),
		P95:  percentile(s, 95),
		P99:  percentile(s, 99),
	}
}

// StatResult holds results from all three hypothesis tests.
type StatResult struct {
	WelchP     float64
	KSP        float64
	MWUP       float64
	MeanDiffNs float64
	Leak       bool
}

// RunTests runs Welch, KS, and MWU on trimmed samples.
// Reports a leak when any p-value < 0.01.
func RunTests(a, b []int64) StatResult {
	at := trimOutliers(a)
	bt := trimOutliers(b)
	wp := welchTTest(at, bt)
	// KS requires sorted input — trimOutliers already sorts.
	kp := ksTwoSample(at, bt)
	mp := mannWhitneyU(at, bt)
	diff := math.Abs(mean(at) - mean(bt))
	return StatResult{
		WelchP:     wp,
		KSP:        kp,
		MWUP:       mp,
		MeanDiffNs: diff,
		Leak:       wp < 0.01 || kp < 0.01 || mp < 0.01,
	}
}
