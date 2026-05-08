#!/usr/bin/env python3
"""
reanalyse.py — Offline statistical re-analysis of timing CSVs.

Usage:
    python3 reanalyse.py evidence/2026-05-07/basic_auth_valid.csv \
                         evidence/2026-05-07/basic_auth_invalid.csv

Requires: numpy, scipy (pip install numpy scipy)
"""
import sys
import numpy as np

try:
    import scipy.stats as st
    HAS_SCIPY = True
except ImportError:
    HAS_SCIPY = False
    print("WARNING: scipy not available — KS and MWU tests skipped")


def load(path):
    return np.loadtxt(path, dtype=np.float64)


def analyse(a, b, label_a="A", label_b="B"):
    # p99 trim
    a = a[a < np.quantile(a, 0.99)]
    b = b[b < np.quantile(b, 0.99)]

    print(f"\n{'='*60}")
    print(f"Comparison: {label_a} vs {label_b}")
    print(f"  N: {len(a)} vs {len(b)}")
    print(f"  {label_a}: mean={np.mean(a):.1f}ns  std={np.std(a, ddof=1):.1f}ns  "
          f"p50={np.percentile(a, 50):.0f}ns  p99={np.percentile(a, 99):.0f}ns")
    print(f"  {label_b}: mean={np.mean(b):.1f}ns  std={np.std(b, ddof=1):.1f}ns  "
          f"p50={np.percentile(b, 50):.0f}ns  p99={np.percentile(b, 99):.0f}ns")
    print(f"  |mean diff|: {abs(np.mean(a) - np.mean(b)):.2f}ns")

    if HAS_SCIPY:
        t_stat, p_welch = st.ttest_ind(a, b, equal_var=False)
        ks_stat, p_ks = st.ks_2samp(a, b)
        u_stat, p_mwu = st.mannwhitneyu(a, b, alternative='two-sided')
        print(f"  Welch t-test: t={t_stat:.4f}  p={p_welch:.4g}")
        print(f"  KS 2-sample:  D={ks_stat:.4f}  p={p_ks:.4g}")
        print(f"  Mann-Whitney: U={u_stat:.1f}  p={p_mwu:.4g}")
        leak = p_welch < 0.01 or p_ks < 0.01 or p_mwu < 0.01
        print(f"  VERDICT: {'TIMING LEAK DETECTED' if leak else 'PASS — no significant timing difference'}")
        return leak
    return None


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: reanalyse.py <file_a.csv> <file_b.csv> [label_a] [label_b]")
        sys.exit(1)

    a = load(sys.argv[1])
    b = load(sys.argv[2])
    label_a = sys.argv[3] if len(sys.argv) > 3 else sys.argv[1]
    label_b = sys.argv[4] if len(sys.argv) > 4 else sys.argv[2]
    analyse(a, b, label_a, label_b)
