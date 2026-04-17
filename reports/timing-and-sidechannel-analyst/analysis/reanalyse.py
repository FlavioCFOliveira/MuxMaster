#!/usr/bin/env python3
"""
Re-analyse the CSVs emitted by the Go harness under
/reports/timing-and-sidechannel-analyst/evidence/<date>/*.csv

Requires: numpy, scipy, matplotlib.  Install via:

    python3 -m venv .venv && . .venv/bin/activate
    pip install numpy scipy matplotlib

Then:

    python analysis/reanalyse.py evidence/2026-04-17

Emits per-pair histograms + Q-Q plots + combined Welch / KS / MWU table.
"""
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

import numpy as np
import scipy.stats as st
import matplotlib.pyplot as plt


def load(path: Path) -> np.ndarray:
    return np.loadtxt(path)


def trim_p99(xs: np.ndarray) -> np.ndarray:
    cut = np.quantile(xs, 0.99)
    return xs[xs <= cut]


def pair_test(a: np.ndarray, b: np.ndarray) -> dict:
    welch = st.ttest_ind(a, b, equal_var=False)
    ks = st.ks_2samp(a, b)
    mwu = st.mannwhitneyu(a, b)
    return {
        "welch_t": welch.statistic,
        "welch_p": welch.pvalue,
        "ks_d": ks.statistic,
        "ks_p": ks.pvalue,
        "mwu_u": mwu.statistic,
        "mwu_p": mwu.pvalue,
        "mean_a": a.mean(),
        "mean_b": b.mean(),
        "mean_diff": a.mean() - b.mean(),
        "cohen_d": (a.mean() - b.mean()) / np.sqrt((a.var(ddof=1) + b.var(ddof=1)) / 2),
    }


def plot_pair(a: np.ndarray, b: np.ndarray, name: str, out_dir: Path) -> None:
    fig, axes = plt.subplots(1, 2, figsize=(12, 4))
    axes[0].hist(a, bins=80, alpha=0.5, label="A", density=True)
    axes[0].hist(b, bins=80, alpha=0.5, label="B", density=True)
    axes[0].set_xlabel("ns")
    axes[0].set_ylabel("density")
    axes[0].set_title(f"{name} — histogram")
    axes[0].legend()
    # Q-Q.
    qa = np.sort(a)
    qb = np.sort(b)
    n = min(len(qa), len(qb), 2000)
    ia = np.linspace(0, len(qa) - 1, n).astype(int)
    ib = np.linspace(0, len(qb) - 1, n).astype(int)
    axes[1].scatter(qa[ia], qb[ib], s=2)
    mn = min(qa[0], qb[0])
    mx = max(qa[-1], qb[-1])
    axes[1].plot([mn, mx], [mn, mx], "k--", lw=1, alpha=0.5)
    axes[1].set_xlabel("A quantile (ns)")
    axes[1].set_ylabel("B quantile (ns)")
    axes[1].set_title(f"{name} — Q-Q")
    fig.tight_layout()
    out = out_dir / f"{name}.png"
    fig.savefig(out, dpi=120)
    plt.close(fig)
    print(f"  wrote {out}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("evidence_dir", type=Path)
    args = parser.parse_args()
    ev = args.evidence_dir
    if not ev.is_dir():
        print(f"error: {ev} is not a directory", file=sys.stderr)
        return 1
    out = ev / "histograms"
    out.mkdir(exist_ok=True)

    # Enumerate _A.csv / _B.csv pairs.
    pairs: list[tuple[str, Path, Path]] = []
    for p in sorted(ev.glob("*_A.csv")):
        base = p.name[:-6]  # strip "_A.csv"
        partner = p.with_name(f"{base}_B.csv")
        if partner.exists():
            pairs.append((base, p, partner))
    # Specific top-level pairs.
    specifics = [
        ("h002_user", ev / "h002_user_exists_run1.csv", ev / "h002_user_absent_run1.csv"),
        ("h002b_pwlen", ev / "h002b_pwlen_same.csv", ev / "h002b_pwlen_diff.csv"),
    ]
    for n, a, b in specifics:
        if a.exists() and b.exists():
            pairs.append((n, a, b))

    print(f"{len(pairs)} pairs discovered")
    rows = []
    for name, a_path, b_path in pairs:
        a = trim_p99(load(a_path))
        b = trim_p99(load(b_path))
        r = pair_test(a, b)
        print(f"\n### {name}")
        print(f"  N_A={len(a)} N_B={len(b)}")
        print(f"  mean_A={r['mean_a']:.1f} mean_B={r['mean_b']:.1f} diff={r['mean_diff']:.1f}ns d={r['cohen_d']:.3f}")
        print(f"  welch t={r['welch_t']:.3f} p={r['welch_p']:g}")
        print(f"  KS    D={r['ks_d']:.3f} p={r['ks_p']:g}")
        print(f"  MWU   U={r['mwu_u']:.0f} p={r['mwu_p']:g}")
        plot_pair(a, b, name, out)
        rows.append((name, len(a), len(b), r))

    # Emit combined CSV.
    out_csv = ev / "combined_tests.csv"
    with out_csv.open("w") as f:
        f.write("pair,N_A,N_B,mean_A,mean_B,diff,cohen_d,welch_t,welch_p,KS_D,KS_p,MWU_U,MWU_p\n")
        for name, na, nb, r in rows:
            f.write(
                f"{name},{na},{nb},{r['mean_a']:.2f},{r['mean_b']:.2f},{r['mean_diff']:.2f},"
                f"{r['cohen_d']:.4f},{r['welch_t']:.4f},{r['welch_p']:g},"
                f"{r['ks_d']:.4f},{r['ks_p']:g},{r['mwu_u']:.0f},{r['mwu_p']:g}\n"
            )
    print(f"\nwrote {out_csv}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
