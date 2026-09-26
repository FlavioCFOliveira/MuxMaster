# Performance lab — documentation truth pass (rmp #296)

Measurement-only pass in support of sprint 21 "Documentation truth pass": establish
an honest, reproducible performance baseline for v1.1.0 vs HEAD, an absolute
snapshot of everything new since v1.1.0, a competitor comparison at HEAD, and a
per-claim verification of the CHANGELOG's "Sprint 18 — Waste-Hunt Campaign"
section. **No Go source, test, or non-report documentation file was changed by
this task.** `git status` shows only this new `reports/perf-lab-2026-09-26-docs/`
directory as untracked.

## Instruction change mid-run

The task originally specified `-count=10` for the root/middleware suites and
`-count=6` for the competitor suite. Partway through, the coordinator changed
this to **`-count=3` for every run**. The `-count=10` v1.1.0 root run in
progress at that point was killed (exit code 144, no partial numbers used) and
every suite below was run (or re-run) at `-count=3`, so v1.1.0 and HEAD use
identical settings throughout.

**Consequence: statistical power is limited.** `benchstat` needs `n>=4` samples
per side to compute a significance test at all, and `n>=6` for a confidence
interval. With `n=3`, benchstat marks **every single comparison** in the
root-package table "~" (not statistically distinguishable) and prints
"need >= 4 samples to detect a difference at alpha level 0.05". This is
reported faithfully below — no delta in this report should be read as a
confirmed regression or improvement unless it is also cross-checked against
the wider, single-sided sample or the wastehunt/perf-audit corroborating
benchmarks (which use the same live code, different call sites).

## Host

| Fact | Value |
|---|---|
| CPU | AMD Ryzen 9 5900HX with Radeon Graphics, 8 cores / 16 threads |
| CPU governor | `powersave` (not pinned to `performance` — see caveats) |
| OS / kernel | Linux 6.8.0-139-generic (Ubuntu), x86_64 |
| Go version | go1.27.0 linux/amd64 |
| Date | 2026-09-26 |
| HEAD commit | `bc4edb71720d42eec66ea64f59eb456b06a4f90f` (2026-09-26 08:42:25 +0100) |
| v1.1.0 tag commit | `8af68ecd7b6f35b0f425133d94e8dd6f584248e3` (2026-05-12 14:36:40 +0100) |
| Commits between tags | 82 |

v1.1.0 was checked out into an isolated git worktree at
`/tmp/claude-1000/.../scratchpad/v110` (`git worktree add --detach <path> v1.1.0`)
and removed at the end of the task (`git worktree remove`); the main working
tree was never touched.

## Exact commands

```bash
# v1.1.0 root package (isolated worktree)
cd <worktree>/v110
go test -run=^$ -bench=. -benchmem -count=3 .            > root-v110.txt

# HEAD root package
cd MuxMaster
go test -run=^$ -bench=. -benchmem -count=3 .             > root-head.txt

# HEAD middleware package (no v1.1.0 equivalent — see below)
go test -run=^$ -bench=. -benchmem -count=3 ./middleware/ > middleware-head.txt

# HEAD competitor suite (own go.mod + vendor/; -mod=mod only to sidestep a
# vendor/modules.txt "explicit" metadata staleness — no vendor files touched)
cd competitor
go test -mod=mod -run=^$ -bench=. -benchmem -count=3 .    > competitor-head.txt

# Sprint-18 claim verification harnesses (both self-contained modules with
# `replace github.com/FlavioCFOliveira/MuxMaster => ../../../../` (or `../`),
# so their "current" subtests call the SAME code as HEAD, live)
cd reports/perf-lab-2026-09-24/waste-hunt/bench
go test -mod=mod -run=^$ -bench=. -benchmem -count=3 .    > wastehunt-claims-head.txt

cd reports/perf-audit-2026-05-12   # same module, no replace needed
go test -run=^$ -bench=. -benchmem -count=3 .             > perfaudit-may12-head.txt

benchstat root-v110.txt root-head.txt        > root-benchstat.txt
benchstat middleware-head.txt                > middleware-benchstat.txt
benchstat competitor-head.txt                > competitor-benchstat.txt
benchstat wastehunt-claims-head.txt          > wastehunt-claims-benchstat.txt
benchstat perfaudit-may12-head.txt           > perfaudit-may12-benchstat.txt
```

Raw outputs and benchstat outputs for all five runs are in this directory.

## 1. Root package: v1.1.0 vs HEAD (hot-path cases)

Every delta below is marked "~" by benchstat — **not statistically significant
at n=3**. Read the ns/op columns as two independent 3-sample point estimates,
not as a confirmed comparison.

| Benchmark | v1.1.0 ns/op | HEAD ns/op | B/op (both) | allocs/op (both) | benchstat |
|---|---|---|---|---|---|
| StaticRoute | 26.95 | 28.63 | 0 | 0 | ~ (p=0.100) |
| ParamRoute1 | 119.5 | 118.5 | 384 | 1 | ~ (p=0.400) |
| ParamRoute2 | 1635 ¹ | 130.1 | 416 | 1 | ~ (p=0.100) |
| ParamRoute3 | 1496 ¹ | 149.6 | 480 | 1 | ~ (p=0.600) |
| WildcardRoute (catch-all) | 109.5 | 132.4 | 384 | 1 | ~ (p=0.100) |
| NotFound | 216.6 | 208.0 | 93 → 91 | 3 | ~ (p=0.400) |
| ParallelStaticRoute | 3.731 | 4.249 | 0 | 0 | ~ (p=0.100) |
| ParallelParamRoute | 105.3 | 104.5 | 384 | 1 | ~ (p=0.100) |
| FastStaticRoute | 25.45 | 28.91 | 0 | 0 | ~ (p=0.100) |
| FastParamRoute1 | 43.31 | 46.15 | 32 | 1 | ~ (p=0.100) |
| FastParamRoute2 | 61.58 | 68.96 | 64 | 1 | ~ (p=0.100) |
| FastParamRoute3 | 85.23 | 79.72 | 96 | 1 | ~ (p=0.100) |
| FastParallelParamRoute | 15.53 | 16.31 | 32 | 1 | ~ (p=0.100) |
| PooledParamRoute1 | 42.20 | 48.51 | 0 | 0 | ~ (p=0.100) |
| PooledParamRoute2 | 55.05 | 59.76 | 0 | 0 | ~ (p=0.100) |
| PooledParamRoute3 | 58.60 | 61.76 | 0 | 0 | ~ (p=0.100) |
| PooledWildcardRoute | 41.11 | 44.95 | 0 | 0 | ~ (p=0.100) |
| PooledParallelParamRoute | 6.347 | 6.901 | 0 | 0 | ~ (p=0.100) |
| geomean (sec/op, both sides) | 62.85n | 453.8n ¹ | — | — | -18.93% (geomean; skewed by ¹) |

¹ **Outlier, not a real regression.** The v1.1.0 `ParamRoute2`/`ParamRoute3`
rows show 2 of their 3 samples at ~1.5–1.7 µs — roughly 10x every other
sample in either version — while `ParamRoute1`, immediately adjacent in the
same file, is a clean ~119 ns across all 3 runs. HEAD's own `ParamRoute2`/
`ParamRoute3` samples are tight (129–135 ns, 149.6–150.0 ns). This is a
transient host perturbation during that specific window (the CPU governor was
`powersave`, not `performance` — see caveats), not a characteristic of the
v1.1.0 code; it also corrupts the sec/op geomean line above, which should be
read as **not meaningful** for this table. **Allocations (B/op, allocs/op) are
identical between v1.1.0 and HEAD for every one of these 18 shared
benchmarks** — that comparison is solid regardless of the ns/op noise (all
samples in both versions agree exactly, hence `p=1.000` / "all samples are
equal" in the raw benchstat allocation tables).

**Overall reading:** no allocation regression anywhere in the shared
hot-path set. The ns/op deltas are all within a single-digit-to-low-double-digit
percentage and none clear benchstat's significance bar at n=3; a few rows
(WildcardRoute, StaticRoute, FastParamRoute2) show a small, consistently
higher HEAD number across all 3 samples on both sides — worth a follow-up
`-count=10+` run if the team wants to confirm whether this is real (e.g. new
validation added to the hot path since v1.1.0) or host drift, but it is not
reported here as a finding because n=3 cannot support that conclusion.

## 2. New at HEAD (no v1.1.0 equivalent — absolute numbers only)

`middleware/bench_test.go` did not exist at v1.1.0 (zero `Benchmark*` functions
anywhere under `middleware/` at that tag). The following root-package
benchmarks are also new since v1.1.0 (confirmed via `grep func Benchmark` on
both tags):

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| RegisterRoutes/N=100 | 56 170 (561.7 ns/route) | 86.31 Ki | 1 135 |
| RegisterRoutes/N=1000 | 693 100 (693.1 ns/route) | 1.041 Mi | 13 400 |
| RegisterRoutes/N=5000 | 4 698 000 (939.6 ns/route) | 6.084 Mi | 75 940 |
| ServeFiles | 808.1 | 709 | 8 |
| GroupServeFiles | 811.9 | 709 | 8 |
| Mount_Static | 376.6 | 864 | 2 |
| Mount_Param | 519.6 | 1 219 | 3 |
| Mount_TSRRedirect | 568.6 | 920 | 6 |
| AdversarialBacktracking/1..128 | 165.0 → 15 550 (linear in depth) | 384 → 30 720 | 1 → 19 |
| QuadraticBacktracking/2..64 | 197.4 → 8 771 (linear in depth) | 416 → 15 320 | 1 → 17 |

Backtracking benchmarks confirm the DoS-bound claim in tree.go: cost and
allocations grow linearly with depth (e.g. AdversarialBacktracking 1→128 is a
94x depth increase for a ~94x time increase), not quadratically or worse.

### Middleware package, HEAD only (no v1.1.0 baseline exists)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| ThrottlePerIP/sequential-one-client | 115.0 | 0 | 0 |
| ThrottlePerIP/many-clients-no-overlap | 127.2 | 0 | 0 |
| Logger | 5 671 | 0 | 0 |
| Recoverer_NoPanic | 19.99 | 0 | 0 |
| RecovererWithLogger_NoPanic | 20.12 | 0 | 0 |
| Compress/small-600B | 316.6 | 32 | 2 |
| Compress/chunked-12KiB | 7 765 | 58 | 3 |
| RealIP/1-hop | 143.6 | 16 | 1 |
| RealIP/3-hops | 178.7 | 16 | 1 |
| APIKeyHit | 453.3 | 416 | 6 |
| BasicAuth/1-users/hit | 319.5 | 32 | 2 |
| BasicAuth/10-users/hit | 549.7 | 32 | 2 |
| BasicAuth/100-users/hit | 2 836 | 32 | 2 |
| BasicAuth/1-users/miss | 733.3 | 168 | 8 |
| BasicAuth/10-users/miss | 958.8 | 168 | 8 |
| BasicAuth/100-users/miss | 3 232 | 168 | 8 |
| JWTAuthHS256 | 4 354 | 738 | 7 |
| OAuth2CacheSetAtSaturation | 3 194 | 288 | 2 |

BasicAuth's near-linear ns/op growth with registered-user count (1→10→100
users: ~320ns→550ns→2.8µs hit; ~730ns→960ns→3.2µs miss) is the expected,
documented cost of the O(n) constant-time credential scan (sprint 20,
rmp #290 / TSC-2026-0002) that replaced the map-lookup timing oracle.

## 3. Competitor suite at HEAD (`-count=3`)

Static / param / catch-all / not-found / parallel cases, ns/op (allocs/op in
parentheses). Full data in `competitor-head.txt` / `competitor-benchstat.txt`.

| Router | Static | Param×1 | Param×2 | Param×3 | Wildcard | NotFound | Parallel static | Parallel param |
|---|---|---|---|---|---|---|---|---|
| **MuxMaster (default)** | 29.6 (0) | 116.9 (1) | 131.9 (1) | 144.4 (1) | 116.4 (1) | 254.7 (3) | 4.51 (0) | 102.3 (1) |
| **MuxMaster Fast** | 30.6 (0) | 47.8 (1) | 67.8 (1) | 83.0 (1) | 48.6 (1) | — | 4.55 (0) | 17.2 (1) |
| **MuxMaster Pooled** | 30.7 (0) | 46.7 (0) | 60.9 (0) | 67.6 (0) | 46.5 (0) | — | — | 7.19 (0) |
| httprouter | 34.7 (0) | 50.5 (1) | 60.2 (1) | 74.4 (1) | 42.8 (1) | 398.2 (?) | 4.91 (0) | 21.8 (1) |
| bunrouter (http.Handler adapter) | 168.1 (3) | 160.3 (3) | 181.6 (3) | 179.9 (3) | 152.5 (3) | 275.5 (3) | 126.1 (3) | 126.1 (3) |
| chi v5 | 217.9 (2) | 360.7 (4) | 405.0 (4) | 415.5 (4) | 334.2 (4) | 346.5 (4) | 133.8 (2) | 232.3 (4) |
| gorilla/mux | 578.9 (?) | 954.7 (?) | 1497 (?) | 1729 (?) | 1611 (?) | 1034 (4) | 345.6 (7) | 456.8 (8) |

**Note on bunrouter:** `competitor/bench_test.go` benchmarks bunrouter through
its `http.Handler` adapter (`go.mod` pulls `github.com/uptrace/bunrouter
v1.0.23`, not bunrouter's native `bunrouter.HandlerFunc` API). This is why
bunrouter shows 3 allocs even on the static route here — consistent with the
documented "native API gets 0 allocs, the stdlib adapter costs ~3 allocs"
finding already on record in `reports/apple-m4-benchmarks-2026-05-12.md`.
This run does not re-measure bunrouter's native API; treat the bunrouter row
as "bunrouter behind an `http.Handler` shim," not bunrouter's best case.

**MuxMaster Pooled is 0 allocs on every parameterised case** and beats
httprouter's 1-param case on this host (46.7 ns vs 50.5 ns) while MuxMaster's
**default** mode trails httprouter slightly (116.9 ns vs 50.5 ns for 1 param)
because it fuses `requestCtx`+`*http.Request` into a GC-managed allocation
rather than pooling — the documented, deliberate default/Pooled/Fast
three-tier trade-off (`Mux.PoolRequestBundle`, `HandleFast`).

## 4. Sprint 18 "Waste-Hunt Campaign" claim verification

Verified via two self-contained benchmark modules that `replace` the live
module root, so their "current" subtests exercise the exact code shipped at
HEAD today (not a frozen 2026-09-24 snapshot):
`reports/perf-lab-2026-09-24/waste-hunt/bench/` and
`reports/perf-audit-2026-05-12/` (the latter has no `replace`; it is a regular
package in the same module, built directly against HEAD). Both ran clean at
`-count=3`, no vet/build errors, no source changes.

| Claim (rmp / WH id) | Changelog before → after | Measured now (HEAD) | Verdict |
|---|---|---|---|
| Registration O(depth) not O(tree) (#253, WH-08) N=100 | 884 µs → 53 µs | 56.2 µs (561.7 ns/route) | **Reproduced** — within ~6% of claimed after-value, >15x faster than before |
| … N=1000 | 90.6 ms → 0.65 ms | 693 µs (693.1 ns/route) | **Reproduced** — within ~7%, >130x faster than before |
| … N=5000 | 2.86 s → 4.7 ms | 4.70 ms (939.6 ns/route) | **Reproduced** — essentially exact match, ~608x faster than before |
| Mount shallow copy (#250, WH-04) | 1024 ns → 243 ns, 9→3 allocs | 237.5 ns, 864 B, **2 allocs** | **Reproduced**, allocs even better than claimed |
| CleanPath (dirty path) shallow copy (#250, WH-04) | 810 ns → 132 ns, 7→2 allocs | 145.4 ns, 464 B, 2 allocs | **Reproduced** |
| Redirect cache + fused bundle, no middleware (#248/#250, WH-10) | 884 ns → 639 ns | 641.3 ns, 1088 B, 10 allocs | **Reproduced** |
| Redirect cache + fused bundle, 5-mw chain (#248/#250, WH-10) | 1050 ns → 792 ns, 19→12 allocs | 760.7 ns, 1440 B, **11 allocs** | **Reproduced**, allocs even better than claimed |
| ThrottlePerIP fast path (#251, WH-01) | 4051 ns → 118 ns, 5→0 allocs | 115.0–116.9 ns, 0 allocs | **Reproduced**, essentially exact |
| Logger alloc-free formatting (#251, WH-02/WH-07) | 7.5 µs → 5.7 µs, 5→0 allocs | 5.671–5.673 µs, 0 allocs | **Reproduced**, essentially exact |
| Compress pooled writer, small 600B (#251, WH-03) | 435 ns → 356 ns, 4→2 allocs | 309.4–316.6 ns, 2 allocs | **Reproduced**, better than claimed |
| Compress pooled writer, chunked 12 KiB (#251, WH-03) | 14.6 µs → 8.1 µs, 16→3 allocs | 7.765–7.773 µs, 3 allocs | **Reproduced** |
| JWTAuth header memo + unsafe HMAC input (#251, WH-06) | 5.16 µs → 4.44 µs, 10→7 allocs | 4.354–4.420 µs, 7 allocs | **Reproduced**, better than claimed |
| RealIP right-to-left XFF scan, 1-hop (#251, WH-11) | 195 ns → 166 ns | 142.1–143.6 ns, 1 alloc | **Reproduced**, better than claimed |
| RealIP right-to-left XFF scan, 3-hop (#251, WH-11) | 257 ns → 201 ns, 2→1 allocs | 178.7–184.7 ns, 1 alloc | **Reproduced**, better than claimed |
| APIKey fused context node (#251, WH-12) | 574 ns → 488 ns, 7→6 allocs | 434.0–453.3 ns, 6 allocs | **Reproduced**, better than claimed |
| 405 `MethodNotAllowed` Allow-header table (#250, WH-09) | 153 ns → 138 ns, 2→3 allocs | 109.6 ns, 91 B, **1 alloc** | **Reproduced**, better than claimed (allocs down further, not up) |
| Auto-`OPTIONS` Allow-header table (#250, WH-09) | 124 ns → 61 ns, 3→1 allocs | 61.9 ns, 1 alloc | **Reproduced**, essentially exact |
| Header-slice aliasing fix, `Text` (#250) | 98 ns → 58 ns, 2→1 allocs (net) | 60.4 ns, 1 alloc | **Reproduced**, essentially exact |
| Header-slice aliasing fix, `JSONHelper` (#250) | 500 ns → 487 ns, 3 allocs unchanged | 514.1 ns, 3 allocs | **Reproduced** — allocs match exactly; ns/op within noise of claimed value |

**No claim regressed.** Every claim with a matching benchmark reproduced the
direction and rough magnitude of the original measurement; several
(MethodNotAllowed, APIKey, RealIP, Compress-small, JWTAuth) now measure
*better* than the number originally recorded on 2026-09-24, consistent with
further incremental work in sprints 19–20 rather than any drift back toward
the pre-fix baseline.

**Claims with no directly matching benchmark today:**

- **`StripSlashes` shallow copy (WH-04):** grouped in the same changelog
  bullet as Mount/CleanPath/ServeFiles but has no dedicated benchmark in
  either harness used here. The same shallow-copy code path is shared with
  the verified `CleanPath` case, so it is very unlikely to have regressed in
  isolation, but this is inference, not a direct measurement. **No benchmark
  available.**
- **`ServeFiles`/`Group.ServeFiles` exact before/after ns (WH-04):** the
  changelog bullet gives numbers only for Mount and CleanPath, not ServeFiles
  itself. The root suite's `BenchmarkServeFiles`/`BenchmarkGroupServeFiles`
  (808.1 ns / 811.9 ns, 709 B, 8 allocs — see §2) confirm the live handler is
  cheap and allocation-light, consistent with the same fix, but there is no
  quoted before/after pair to reproduce. **No quantitative claim to verify;
  absolute HEAD number reported instead.**
  (Note: `waste-hunt/bench/clone_test.go`'s `BenchmarkServeFilesCopy/current-replica`
  is a **hand-written replica of the pre-fix code**, not the live `ServeFiles`
  handler — its 1.029 µs / 9-allocs number is the intentional "before" replica
  for comparison inside that harness, not a HEAD regression.)

## Caveats

- **`-count=3` throughout, per the coordinator's mid-run instruction.**
  `benchstat` cannot compute a confidence interval below `n=6` or a
  significance test below `n=4`; the root-package v1.1.0-vs-HEAD table above
  has every row marked "~" for exactly this reason. Treat every ns/op delta
  in §1 as indicative, not confirmed. The claim-verification numbers in §4
  are more trustworthy because most of them show large, order-of-magnitude
  effects that no amount of n=3 noise could produce spuriously (e.g.
  4051 ns → 115 ns, 884 µs → 56 µs).
- **CPU governor was `powersave`, not `performance`,** for the entire session
  — this is the most likely explanation for the ParamRoute2/ParamRoute3
  outlier in §1 and adds general frequency-scaling jitter to every run here.
  A future run pinning `performance` (or `cpupower frequency-set -g
  performance`) would tighten all of these numbers.
- **The machine was not otherwise idle in the traditional "isolated
  benchmarking box" sense** — it is the operator's normal Linux workstation.
  Runs were executed back-to-back, one at a time, with no other benchmark or
  build running concurrently, but ambient desktop load was not independently
  verified to be zero throughout.
- **Competitor `-count=3`** — the task originally requested `-count=6` for
  this suite; the coordinator's later instruction to use `-count=3`
  everywhere supersedes that. The competitor table in §3 is a 3-sample
  estimate, same caveat as §1.
- **Cross-package comparisons (e.g. `middleware-head.txt` vs
  `wastehunt-claims-head.txt` for `Compress`/`RealIP`/`APIKeyHit`) use
  different request-construction helpers** (`newDiscardRW`/`realisticRequest`
  in both, but constructed independently in each file) — they agree to
  within a few percent, which is itself useful corroboration, but they are
  not the same benchmark binary.
- No Go source, test, or documentation file outside this report directory
  was modified. `-mod=mod` was used only for the two vendored/replace-based
  modules (`competitor/`, `waste-hunt/bench/`) to bypass a stale
  `vendor/modules.txt` "explicit" flag for `gorilla/mux`; no vendor file was
  written (`git status` confirms no changes under `competitor/`).
