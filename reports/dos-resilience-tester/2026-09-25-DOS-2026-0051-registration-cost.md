# DoS Resilience Audit — DOS-2026-0051 Registration-Cost Re-measurement

**Date:** 2026-09-25
**Commit:** `4849ce8` (branch `feature/20-backlog-clearance`; no change to `tree.go` or `mux.go`)
**Go:** 1.27.0 linux/amd64
**Hardware:** AMD Ryzen 9 5900HX, 16 logical CPUs, 30 GiB RAM, Linux 6.8.0-139-generic, bare metal
**Harness:** `reports/dos-resilience-tester/harness/registration_cost_test.go` — `TestDOS20260051_RegistrationCostScaling`, `BenchmarkDOS20260051_Registration`, `TestDOS20260051_CPUProfile`
**Evidence:** `reports/dos-resilience-tester/evidence/2026-09-25/` (`DOS-2026-0051-scaling-run.txt`, `-bench-run.txt`, `-pprof-top.txt`, `-pprof-cum.txt`, `-tail-check.txt`, `cpuprofile-param-N10000.pprof`)
**rmp:** #266 (O-3 in `reports/overview/findings.md`)

---

## Verdict

**Not a DoS vector; registration cost is linear, not quadratic.** Registering 10 000 routes takes 6.6–9.9 ms and 100 000 routes about 104 ms. The figures cited by rmp #166 (N = 2 000 → 1.4 s, N = 5 000 → ~5 s) are stale by roughly 500–1 700× and must not be repeated. Severity: Informational — a startup-cost note for operators, not a mitigation requirement.

---

## Commands

```
cd reports/dos-resilience-tester/harness
go test -run TestDOS20260051_RegistrationCostScaling -v -timeout 600s ./...
go test -run '^$' -bench BenchmarkDOS20260051_Registration -benchtime=5x -benchmem -v ./...
DOS0051_EVIDENCE_DIR=<evidence-dir> DOS0051_PROFILE_SHAPE=param DOS0051_PROFILE_N=10000 \
  DOS0051_PROFILE_REPEATS=200 go test -run TestDOS20260051_CPUProfile -v ./...
go tool pprof -top -cum -nodecount=25 <evidence-dir>/cpuprofile-param-N10000.pprof
```

All runs without `-race`.

## 1. Wall time and allocations (median of 5 runs)

| Shape | N | Median wall time | Avg mallocs | Benchmark ns/op (×5) | B/op | allocs/op |
|---|---|---|---|---|---|---|
| static-prefix (`/api/v1/resource{i}`) | 500 | 240 µs | 5 038 | 351 097 | 429 259 | 5 038 |
| | 1 000 | 376 µs | 10 288 | 782 470 | 879 262 | 10 288 |
| | 2 000 | 910 µs | 22 788 | 1 220 632 | 1 971 275 | 22 788 |
| | 5 000 | 2.92 ms | 60 290 | 2 879 704 | 5 248 438 | 60 289 |
| | 10 000 | 6.61 ms | 122 789 | 6 590 739 | 10 709 636 | 122 791 |
| param (`/r{i}/:id`) | 500 | 253 µs | 6 275 | 466 811 | 514 339 | 6 275 |
| | 1 000 | 469 µs | 12 775 | 802 654 | 1 051 542 | 12 775 |
| | 2 000 | 1.07 ms | 27 775 | 1 410 365 | 2 333 952 | 27 775 |
| | 5 000 | 3.86 ms | 72 779 | 4 502 836 | 6 181 270 | 72 775 |
| | 10 000 | 9.59 ms | 147 779 | 8 884 607 | 12 596 744 | 147 779 |
| mixed (REST-shaped, N/5 resources × 5 routes) | 500 | 246 µs | 6 359 | 324 850 | 493 131 | 6 359 |
| | 1 000 | 587 µs | 13 829 | 731 638 | 1 100 651 | 13 829 |
| | 2 000 | 1.02 ms | 28 769 | 1 440 215 | 2 315 694 | 28 769 |
| | 5 000 | 3.85 ms | 73 589 | 3 829 013 | 5 960 872 | 73 589 |
| | 10 000 | 9.68 ms | 158 289 | 9 887 554 | 13 076 116 | 158 289 |

## 2. Complexity slope

Log-log linear regression of median wall time against N, N = 500 → 10 000:

| Shape | Fitted slope |
|---|---|
| static-prefix | 1.146 |
| param | 1.237 |
| mixed | 1.217 |

O(N) = 1.0, O(N²) = 2.0. All three shapes are mildly super-linear in this range and far from quadratic.

Extended check beyond the requested range (param shape, single run, `DOS-2026-0051-tail-check.txt`, not part of the committed harness):

```
N=10,000  → 1,097 ns/route
N=20,000  → 1,056 ns/route
N=50,000  → 1,070 ns/route
N=100,000 → 1,038 ns/route  (total: 103.8 ms)
```

Per-route cost is flat from N = 10 000 to N = 100 000. The mild super-linear slope at small N is a warm-up and GC artefact, not a per-insertion cost that grows with N: asymptotically, registration is O(N).

## 3. CPU profile (param shape, N = 10 000 × 200 repeats; 1.66 s wall, 4.01 s CPU samples)

Cumulative share:

- `addRouteInternal`: 36.4 %
- GC (`gcBgMarkWorker`, `mallocgc`, scan workers): ~35–40 % — allocation-driven, not tree-algorithm cost
- `copyNode` (copy-on-write path copying, `specification/performance.md` §36–37): 21.5 % — proportional to path depth × registrations
- `incrementChildPrio` (child-priority reorder): 17.2 % — bounded by per-node fan-out, not by N
- `growslice` (children-slice growth): 14.0 %
- `runtime.nanotime`: 13.5 % flat — scheduler and GC bookkeeping amplified by allocation volume

The dominant cost is allocation volume (~14.8 mallocs per route on the param shape) plus the intentional copy-on-write path copying that provides the rollback guarantee pinned by `TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched` (`tree_rollback_test.go`). Both scale with N × bounded path depth. No quadratic hot spot was found, so no fix is proposed.

## 4. Threat assessment

- Registration (`Handle`, `GET`, `HandleFast`, …) runs only from the application's own code at startup. The tree is read-only after registration, and registration after serving begins is unsupported by design (`specification/out-of-scope.md` §3.1); no HTTP request can reach `addRoute`.
- At N = 100 000 routes registration takes ~104 ms; a one-second startup delay from registration alone would need roughly 1 000 000 routes.
- Residual exposure is architectural only: an application that rebuilds its route table from an untrusted, attacker-influenced source on every start could have its route count, and therefore its startup time, inflated roughly linearly. This is an integration risk, not a MuxMaster defect.
- The rmp #166 figures probably predate the two-phase copy-on-write registration (commit `32d3c77`, 2026-05-07) and later hot-path work. This is a plausible explanation that cannot be verified, because the old implementation no longer exists to re-measure.

## 5. Recommended `SECURITY.md` note

Handed to the documentation step of rmp #266: a "Startup-time route registration cost" section stating the measured figures (1 000 → ~0.4–0.8 ms, 10 000 → ~6.6–9.9 ms, 100 000 → ~104 ms), the linear scaling, that registration is not reachable from requests, and the guidance to bound route counts derived from untrusted configuration.
