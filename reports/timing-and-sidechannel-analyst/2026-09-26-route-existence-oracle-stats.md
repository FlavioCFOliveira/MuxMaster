# Route-Existence Timing Oracle — Full Statistics & Cross-Router Evidence

Date: 2026-09-26   Commit: b44a001 (branch `feature/20-backlog-clearance`)
Go: 1.26.2 (module `go 1.26`)   Methodology: pinned OS thread, GC disabled
during the measurement window, N=200,000 samples/arm, ≥3 independent
`-count=1` runs, Welch t-test + Kolmogorov-Smirnov + Mann-Whitney U +
Cohen's d on p99-trimmed samples (harness convention).

rmp: task #11 (MM-2026-0026, closed 2026-05-07 as a duplicate spike) /
task #290 (audit-remediation chore), row #11 of
`reports/overview/2026-09-26-closed-task-audit.md`. That audit found task
#11's original report (`2026-04-17-1320-prerelease-timing-audit.md`) had
no std/percentiles for the route pairs and only a one-line, uncommitted
competitor claim ("Full comparative data is outside this agent's scope").
This report supplies both.

## Scope

1. Re-run MuxMaster's existing `TestTiming_Route_*` suite
   (`reports/timing-and-sidechannel-analyst/harness/route_existence_timing_test.go`)
   at HEAD, 3 independent runs, with the harness extended (this task) to
   log std/p95/Cohen's d alongside the mean/p50/p99 it already logged.
2. Build an equivalent harness for httprouter, chi and bunrouter — the
   three competitor modules already vendored in `competitor/go.mod` (no
   new dependency added) — measuring the *same* pairs with the *same*
   method, to give the SECURITY.md claim "intrinsic to every radix
   router... httprouter, chi, and bunrouter as well" real, committed data.
3. Assess the feasibility of a stochastic-padding countermeasure (named
   in task #11's technical requirements) using the measured signal and
   noise magnitudes.

New/changed files:

- `reports/timing-and-sidechannel-analyst/harness/stats.go` — added
  `StatResult.CohenD` (pooled-SD Cohen's d on the same p99-trimmed samples
  Welch/KS/MWU already use).
- `reports/timing-and-sidechannel-analyst/harness/route_existence_timing_test.go`
  — all four `TestTiming_Route_*` tests now log N/mean/std/p50/p95/p99 per
  arm and Welch p / KS p / MWU p / mean diff / Cohen's d per pair (previously
  some tests logged only mean/p50/p99, no std, no p95, no Cohen's d).
- `competitor/route_existence_timing_test.go` (new, build-tag `timing`) —
  cross-router harness for httprouter/chi/bunrouter (+ an in-process
  MuxMaster control built on the same matched topology), importing the
  shared `harness` package cross-module via `competitor/go.mod`'s existing
  `replace github.com/FlavioCFOliveira/MuxMaster => ../` (run with
  `GOFLAGS=-mod=mod`, since `competitor/vendor` doesn't mirror the
  `reports/` subtree — see the file's doc comment for the exact command).

## Method note on the two MuxMaster measurements

Two different MuxMaster mux instances appear below:

- **"MuxMaster (own harness)"** — the pre-existing, SECURITY.md-cited mux
  (`buildRoutingMux`, full topology: depth 1-5 static chain, admin chain,
  param routes mixed with static siblings, a catch-all).
- **"MuxMaster (matched control)"** — a smaller mux built in
  `competitor/route_existence_timing_test.go` with EXACTLY the same static
  topology as the httprouter/chi/bunrouter instances in that file, run in
  the same process/suite for a same-conditions comparison. Absolute values
  differ slightly between the two (different tree size → different
  baseline lookup cost) but the qualitative story (registered faster than
  unregistered, admin faster than random) is identical and cross-validates
  both harnesses.

All routers were verified with `VerifyArmStatus` (or the equivalent local
per-sample status check) before any timing evidence was trusted — every
run in every log file passed 100% status verification (0 `t.Fatalf`s).

## 1. Registered (200) vs Unregistered (404), same depth

3 independent runs, N=200,000/arm. Welch p, KS p, MWU p were **0** (i.e.
below float64 precision) in all 12 (4 routers × 3 runs) measurements — no
run for any router failed to detect the difference.

| Router | Run | Reg. mean±std (ns) | Reg. p50/p95/p99 | Unreg. mean±std (ns) | Unreg. p50/p95/p99 | Mean diff (ns) | Cohen's d |
|---|---|---|---|---|---|---|---|
| MuxMaster (own harness) | 1 | 1627.0 ± 3090.1 | 1467/1956/3353 | 2629.4 ± 7455.7 | 1956/6076/8102 | 950.93 | 1.022 |
| MuxMaster (own harness) | 2 | 1611.5 ± 4029.0 | 1466/1955/3352 | 2627.7 ± 7136.8 | 1956/5796/7264 | 917.87 | 1.044 |
| MuxMaster (own harness) | 3 | 1626.4 ± 5911.4 | 1466/1955/3352 | 2608.8 ± 7083.9 | 1955/5727/7543 | 923.15 | 1.047 |
| MuxMaster (matched ctrl) | 1 | 1601.5 ± 4991.1 | 1466/1886/3352 | 2565.8 ± 3462.5 | 1956/5797/7613 | 951.90 | 1.073 |
| MuxMaster (matched ctrl) | 2 | 1611.6 ± 4528.2 | 1466/1955/3352 | 2593.9 ± 6814.5 | 1955/5727/7473 | 915.39 | 1.051 |
| MuxMaster (matched ctrl) | 3 | 1592.9 ± 4367.2 | 1466/1955/3352 | 2624.0 ± 7374.6 | 1956/5727/7473 | 928.85 | 1.074 |
| httprouter | 1 | 1565.7 ± 3206.8 | 1466/1955/2794 | 2676.0 ± 4860.2 | 2305/5797/7543 | 1039.17 | 1.183 |
| httprouter | 2 | 1544.7 ± 3867.8 | 1466/1886/1956 | 2644.0 ± 7079.5 | 2305/5797/7264 | 977.01 | 1.236 |
| httprouter | 3 | 1606.2 ± 3815.9 | 1466/1955/2864 | 2766.5 ± 8565.9 | 2305/5867/7543 | 1026.26 | 1.158 |
| chi | 1 | 1924.1 ± 3354.5 | 1886/2375/5727 | 2916.4 ± 7346.5 | 2305/6216/9568 | 887.87 | 0.779 |
| chi | 2 | 2021.9 ± 4711.6 | 1886/5099/6635 | 2919.0 ± 5948.4 | 2305/6216/9498 | 841.88 | 0.692 |
| chi | 3 | 1938.6 ± 4445.2 | 1886/2375/5727 | 3031.4 ± 8405.6 | 2305/6216/9568 | 961.86 | 0.817 |
| bunrouter | 1 | 1835.5 ± 2439.5 | 1885/2304/5727 | 2689.0 ± 6188.3 | 1956/6076/7613 | 762.62 | 0.790 |
| bunrouter | 2 | 1867.4 ± 4339.3 | 1885/1956/5727 | 2743.1 ± 7986.1 | 1956/6076/7612 | 768.61 | 0.781 |
| bunrouter | 3 | 1815.3 ± 3711.3 | 1885/1956/5238 | 2734.0 ± 8003.3 | 1956/6076/7613 | 799.43 | 0.860 |

**3-run averages:** MuxMaster (own harness) 930.65 ns (d≈1.038) · MuxMaster
(matched ctrl) 932.05 ns (d≈1.066) · httprouter 1014.15 ns (d≈1.192) · chi
897.20 ns (d≈0.763) · bunrouter 776.89 ns (d≈0.810).

**Verdict:** all four routers land in the same 750–1050 ns band. MuxMaster
is squarely in the middle — not the largest (httprouter is ~9% larger) and
not the smallest (bunrouter is ~17% smaller). Effect sizes are
medium-to-large by Cohen's convention (0.69–1.24) for all four. The
"intrinsic to every radix router, comparable magnitude" claim in
SECURITY.md is **confirmed empirically**, previously it was asserted
without committed data.

## 2. Admin (hidden, 200) vs Random (404)

Same pattern; Welch/KS/MWU p = 0 in all 12 measurements.

| Router | 3-run mean diff (ns) | 3-run Cohen's d range |
|---|---|---|
| MuxMaster (own harness) | 926.13 (921.36/918.38/938.86) | 1.035–1.060 |
| MuxMaster (matched ctrl) | 951.43 (964.22/944.89/945.18) | 1.060–1.077 |
| httprouter | 1029.41 (1042.31/1020.66/1025.25) | 1.145–1.163 |
| chi | 888.97 (879.36/840.87/946.67) | 0.754–0.889 |
| bunrouter | 750.09 (776.15/755.46/718.66) | 0.743–0.853 |

Same conclusion as §1: MuxMaster is mid-pack; the hidden-admin-route class
of the oracle is not worse for MuxMaster than for any competitor.

## 3. Depth correlation (registered routes, depth 1→5)

This is where the four routers **diverge materially**. The per-level mean
diff (ns) and Cohen's d, summed across the 4 adjacent-depth comparisons
(depth1→2, 2→3, 3→4, 4→5), 3-run average:

| Router | Avg. cumulative |mean diff| across 4 levels (ns) | Largest single-step Cohen's d |
|---|---|---|
| MuxMaster (own harness) | ~79 (80.10/50.30/107.66) | 0.21 (run 3, depth2→3) |
| MuxMaster (matched ctrl) | ~58 (50.18/84.38/40.24) | 0.26 (run 1, depth1→2) |
| httprouter | ~76 (90.29/71.01/68.23) | 0.26 |
| chi | **~796** (1199.4/891.2/298.4) | **0.53** (medium) |
| bunrouter | **~443** (446.5/378.1/503.5) | 0.30 |

**Finding:** MuxMaster and httprouter have a nearly flat depth-vs-latency
signature (small effect, d ≤ 0.26 at every step, ~40–110 ns of *total*
signal across 4 tree levels). chi's cumulative depth signal is **~10×**
larger (up to 509 ns at a single step, d up to 0.53 — a medium effect by
Cohen's convention) and bunrouter's is **~6×** larger than MuxMaster's.
This is consistent with chi's documented architecture (pattern/regex-based
routing, ~225 ns/op vs MuxMaster's ~14–70 ns/op per the project's own
competitive benchmark table) — a slower dispatch path amplifies whatever
depth-dependent cost exists.

**Correction to the blanket "comparable magnitude" claim:** true for the
binary registered-vs-unregistered / admin-vs-random oracle (§1, §2); NOT
uniformly true for the finer-grained *depth-correlation* signal, where chi
and bunrouter leak measurably more route-topology depth information than
MuxMaster or httprouter. SECURITY.md should not extend the "comparable
magnitude" language to depth correlation without this caveat.

## 4. Static vs param route at matched depth (MuxMaster, chi, bunrouter — httprouter skipped, see below)

httprouter v1.3.0 does not support registering a static and a
parameterised child at the same tree node (`/users/list` + `/users/:id`);
`bench_test.go` already documents and works around this same limitation
for the performance benchmarks. Extending the shared topology to force
this pairing would have broken parity with pairs 1-3, so this pair is
measured for the three routers that support it.

| Router | 3-run mean diff (ns) | 3-run Cohen's d |
|---|---|---|
| MuxMaster (own harness) | 158.19 (142.06/158.41/174.09) | 0.540–0.734 |
| MuxMaster (matched ctrl) | 309.82 (319.87/305.90/303.69) | 0.490–0.521 |
| chi | 388.69 (355.64/499.78/310.66) | 0.353–0.518 |
| bunrouter | 139.11 (28.07/209.13/180.13) — high run-to-run variance | 0.043–0.299 |

This is an already-accepted, expected class (param-route dispatch costs
more — one allocation for the param bundle vs zero for a static route) and
is not new; included here for completeness against the AC's "the other
pairs" requirement. bunrouter's very low, noisy diff in run 1 (28 ns,
d=0.043) is consistent with its documented zero-alloc param design.

## 5. Signal-to-noise analysis and stochastic-padding feasibility

Per-request jitter (`std`, §1 table) is **3,000–8,600 ns** — 3× to 9× the
~750–1050 ns systematic mean difference being measured. A single request
is not a reliable oracle; distinguishing the two arms requires averaging.
Using the Welch statistic `t = diff / (std·√(2/N))`, reaching a
conventionally-decisive `|t| ≈ 4` at this signal/noise ratio requires
roughly **N ≈ 900–1,000 samples per candidate path**, measured
locally with no network stack (`ServeHTTP` called directly). Real network
jitter (LAN: tens of µs; WAN: ms-scale) would push the required sample
count one to several orders of magnitude higher — consistent with the
existing `assessNetworkExploitability` classification of this oracle as
INFORMATIONAL/LOW at these magnitudes (all 12 runs in §1 and §2 classified
below the 1 µs "informational" threshold).

**Stochastic padding (add random per-request delay) is NOT a viable fix:**

1. By the Central Limit Theorem, adding independent, zero-mean random
   delay to both arms only requires an attacker to average over
   proportionally more samples (`N_new ≈ N_old · (σ_new/σ_old)²`) — it does
   not remove the systematic mean-shift that constitutes the oracle. An
   adversary with sample budget is still guaranteed to detect it
   eventually; padding raises the cost, it does not close the channel.
2. To meaningfully raise that cost (e.g. 10×) would require roughly
   tripling the per-request jitter, i.e. injecting on the order of a
   microsecond or more of *mean* extra latency per request — on **every**
   request, hit or miss. Against MuxMaster's own measured static-route
   dispatch cost of ~14 ns/op (CLAUDE.md performance baseline), this is a
   50-100× latency tax on the fast path to make a currently
   INFORMATIONAL/LOW-severity, statistically-inconclusive-without-heavy-
   averaging oracle marginally harder to exploit.
3. The only mechanism that removes the oracle rather than taxing its cost
   is a constant-time tree descent (always walk to the tree's maximum
   depth regardless of match) — already evaluated and declined in
   `2026-04-17-1320-prerelease-timing-audit.md` §TSC-002 for the same
   reason: it erases the radix tree's asymptotic performance advantage,
   which is this project's primary design goal (CLAUDE.md "Performance
   competitors to beat"). This data reconfirms that decision was correct:
   the cost/benefit ratio of any padding-class mitigation is strongly
   unfavourable given the already-large natural jitter and already-low
   practical exploitability.

## Verdict

**ACCEPT.** Reconfirms the existing SECURITY.md decision for MM-2026-0026
/ TSC-2026-0005, now with:

- Full descriptive statistics (mean, std, p50, p95, p99) for every arm,
  triplicated, all four hypothesis tests (Welch, KS, MWU) plus Cohen's d.
- Committed, reproducible cross-router evidence (httprouter/chi/bunrouter)
  confirming MuxMaster is not an outlier on the registered-vs-unregistered
  and admin-vs-random pairs (mid-pack of a 750–1050 ns band).
- A quantitative rejection of stochastic padding as a mitigation (§5),
  closing the "évaluate feasibility" item in task #11's technical
  requirements.
- One genuine refinement: the depth-correlation signal is NOT uniform
  across routers — chi and bunrouter leak more route-depth information
  than MuxMaster/httprouter. This does not change the Accept verdict (the
  absolute magnitudes are still in the same INFORMATIONAL/LOW severity
  band) but should be reflected in SECURITY.md's wording (see below) so
  the "comparable magnitude" claim is scoped to the pair it was actually
  measured on.

## Exact sentence for SECURITY.md (task #287)

Replace the unsupported clause in the "Route-Existence Timing Oracle
(MM-2026-0026)" section — currently:

> This is intrinsic to radix tree lookup and is present in httprouter,
> chi, and bunrouter as well.

with (data-backed, cites this report):

> This is intrinsic to radix tree lookup: measured with the identical
> harness against the vendored httprouter, chi, and bunrouter modules
> (N=200,000 samples × 3 independent runs, 2026-09-26,
> `competitor/route_existence_timing_test.go`), the same
> registered-vs-unregistered pair yields ~1014 ns (httprouter), ~897 ns
> (chi), and ~777 ns (bunrouter), against MuxMaster's ~932 ns — all four
> land within the same 750–1050 ns band (Cohen's d 0.69–1.24, medium-to-
> large) and MuxMaster is not the largest. The finer-grained
> depth-correlation signal is not uniform across routers: chi and
> bunrouter show roughly 6–10× more cumulative depth-vs-latency signal
> than MuxMaster or httprouter (see
> `timing-and-sidechannel-analyst/2026-09-26-route-existence-oracle-stats.md`
> §3), though still within the same informational severity band. See that
> report for the full per-run statistics, hypothesis tests, and the
> stochastic-padding feasibility analysis (rejected — cost/benefit
> strongly unfavourable).

## Evidence

All raw `go test -v` stdout (3 independent runs each, `-count=1`):

- `evidence/2026-09-26/muxmaster_route_oracle_run{1,2,3}.log` — MuxMaster,
  own harness, `go test -tags timing -run 'TestTiming_Route_' -v -count=1 .`
  from `reports/timing-and-sidechannel-analyst/harness/`.
- `evidence/2026-09-26/competitor_route_oracle_run{1,2,3}.log` —
  httprouter/chi/bunrouter + matched-control MuxMaster, from
  `GOFLAGS=-mod=mod go test -tags timing -run 'TestTiming_Route_Competitor_' -v -count=1 .`
  in `competitor/`.

All 6 log files show 0 `--- FAIL` and 0 status-verification `t.Fatalf`s.

## Coverage gaps (declared)

- CPU cache-timing (FLUSH+RELOAD, PRIME+PROBE) and branch-predictor timing:
  out of scope, no cryptographic secret is involved in routing dispatch.
- Real network-stack jitter (kernel TCP, NIC, proxy hops): not measured —
  §5's SNR analysis is a theoretical extrapolation from local
  `ServeHTTP`-direct jitter, consistent with the existing harness
  convention of never using `httptest.NewServer` for timing.
- gorilla/mux and Fiber were not included — task scope named
  httprouter/chi/bunrouter specifically (SECURITY.md's existing claim),
  and gorilla/mux's ~1000× slower dispatch (CLAUDE.md competitive table)
  would dominate any comparison without adding evidentiary value for this
  specific oracle.
