# Waste Hunt — Final Gate Results

**rmp tasks closed by this gate:** #248 and #250–#260 (waste-hunt fixes and the defects they exposed; the round-2 follow-up below belongs to #250 and #259). The waste-hunt campaign itself is #249. Sprint 18 ("Performance and Efficiency Laboratory") remains open.
**Date:** 2026-09-25
**Agent:** go-perf-optimizer
**Scope:** final pre-commit gate on the uncommitted sprint-18 tree (baseline `d980583`) — lint, build/vet/staticcheck/lint/race across the main module and every nested module with tests, examples build+vet, same-session `benchstat` vs baseline, and an examples re-profile against the new code. **Round 2** (§7-§9) addresses three coordinator-directed follow-ups after the round-1 gate: making the DIV-001 backtracking cost lazy, cutting header-slice-isolation allocs to one per request per middleware, and fixing the two pre-existing test defects the round-1 gate surfaced but did not fix.
**Product code changed, round 1:** one line (`middleware/logger.go:38`, De Morgan simplification, no behaviour change) plus a mechanical `go mod vendor` re-sync of `reports/path-routing-fuzzer/harness/vendor/` (no source edits).
**Product code changed, round 2:** `tree.go` (`getValue` split into an inlined optimistic walk + a noinline `getValueBacktrack` retry), `middleware/no_cache.go`, `middleware/cors.go`, `mux.go` (405 default handler and `serveRedirect`) for one-allocation header fusion, plus one test-harness bug fix (`reports/dos-resilience-tester/harness/2026-05-08-loadtest/loadtest_test.go`) and one corrected test (`reports/fuzzing-and-property-engineer/harness/properties_test.go`).

## Summary

| Gate | Result |
|---|---|
| Lint (`golangci-lint run ./...`, main module) | **Clean** (2 findings fixed across both rounds: `middleware/logger.go:38`, `middleware/cors.go:175` ineffassign) |
| `go build ./...` / `go vet ./...` (main + 9 nested test modules + 13 examples) | **Clean** everywhere |
| `staticcheck ./...` | Clean in code changed this sprint. Pre-existing debt (unrelated files, confirmed via `git diff d980583` = empty) found in 6 modules — reported below, not fixed (out of scope). |
| `go test -race -count=1 ./...` (main + 9 nested test modules) | **No data races anywhere**, including the two round-1 findings, both now **fixed** in round 2 (see §9). |
| Same-session `benchstat` vs `d980583`, **final (post round 2)** | Net **-7.32%** geomean on the middleware audit suite (35 benchmarks, was -4.34% after round 1), **+5.23%** geomean on the root suite (18 benchmarks, was +6.19% after round 1) — see §7-§8 for the targeted fixes and remaining gap. |
| Examples re-profile (13/13, unpatched) | **13/13 built and ran; 0 crashes** (round 1; re-confirmed functionally after round 2 via `examples-smoke.sh`, still 13/13 PASS). All 5 previously-panicking examples and the reverse-proxy crash are confirmed fixed. Middleware CPU share dropped in every example that uses it. |

## 1. Lint

`golangci-lint run ./...` on the main module flagged one finding, introduced by the MID-LOGGER-1 fix:

```
middleware/logger.go:38:23: QF1001: could apply De Morgan's law (staticcheck)
	if !r.wroteHeader && !(code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols) {
```

Fixed by rewriting the negated conjunction as its De Morgan equivalent (same truth table, verified by inspection: `!(a && b && c) == !a || !b || !c`):

```go
if !r.wroteHeader && (code < 100 || code > 199 || code == http.StatusSwitchingProtocols) {
```

No other file changed since `d980583` produced a new lint finding. `golangci-lint run ./...` is clean (`0 issues`) in every module gated below.

## 2. Gate — build / vet / staticcheck / golangci-lint / race

### Main module

All five checks clean. `go test -race -count=1 ./...` — 14 packages, all `ok`.

`staticcheck ./...` reported 21 findings, all in files with **zero diff against `d980583`** (verified with `git diff --name-only d980583 -- <file>` = empty for each): `reports/concurrency-security-auditor/harness/2026-05-08-S10-PreCSA/s10_helpers_test.go` (1×`SA1019` deprecated `Recoverer`) and `reports/perf-audit-2026-05-12/middleware_bench_test.go` (2×`U1000` unused, 15×`SA1019` deprecated `Recoverer`/`ResponseRecorder.HeaderMap`). Pre-existing technical debt, not touched this sprint — not fixed, per scope discipline.

### Nested modules containing tests

| Module | build/vet | staticcheck | golangci-lint | race |
|---|---|---|---|---|
| `reports/dos-resilience-tester/harness` | clean | 2 pre-existing findings (unchanged files) | clean | **pass** (19.9s) |
| `reports/dos-resilience-tester/harness/2026-05-08-loadtest` | clean | 1 pre-existing finding | clean | **FAIL — pre-existing test-harness race, see below** |
| `reports/fuzzing-and-property-engineer/harness` | clean | 6 pre-existing findings | clean | no data race; **1 pre-existing test failure, see below** |
| `reports/http-protocol-security-auditor/harness/h2harness` | clean | clean | clean | **pass** (6.5s) |
| `reports/middleware-security-reviewer/harness` | clean | 3 pre-existing findings | clean | **pass** (3.0s) |
| `reports/middleware-security-reviewer/harness/2026-05-08-S10-PreMSR` | clean | clean | clean | **pass** (1.0s) |
| `reports/perf-lab-2026-09-24/harness` | clean | 21 pre-existing findings (deliberate mirror-struct fields in `layout_test.go`, unchanged) | clean | **pass** (1.0s) |
| `reports/perf-lab-2026-09-24/waste-hunt/bench` | clean | **clean** (includes the changed `headers_test.go`) | clean | **pass** (3.2s) |
| `reports/path-routing-fuzzer/harness` | clean (after vendor re-sync, see below) | 11 pre-existing findings (unchanged lines; the one changed hunk in `s8_audit_test.go` is clean) | clean | **pass** (1.1s) |

All pre-existing `staticcheck` findings above were verified with `git diff --name-only d980583 -- <file>` returning empty output, i.e. present at the sprint's starting commit and untouched by this sprint's work.

**Vendor re-sync (`reports/path-routing-fuzzer/harness`).** This module vendors MuxMaster via a `replace ... => ../../../` directive plus a checked-in `vendor/` tree (Go's automatic `-mod=vendor` applies whenever `vendor/modules.txt` is present and consistent, so this harness compiles against the **vendored copy**, not the live source, unless re-vendored). The vendor copy present in the uncommitted tree was already mid-sync with this sprint's fixes but had drifted behind the latest edits to `mux.go`, `tree.go`, `response.go`, `middleware/compress.go`, `middleware/logger.go` (including the De Morgan fix above) and `middleware/set_header.go`. Running `go mod vendor` in that module brought it current — a mechanical, non-behavioural sync (confirmed: `go.mod`/`go.sum` unchanged, only vendored file contents updated) required so the gate actually exercises the sprint's real code instead of a stale snapshot. This is now part of the uncommitted tree.

### Two pre-existing failures surfaced by `-race` (neither caused by this sprint)

**1. `reports/fuzzing-and-property-engineer/harness`: `TestProp_HandleFastVsHandleIsolation` fails (no data race — a logic assertion).**
The property test asserts `GETFast` after `Use()` must not panic, but current (and baseline) `mux.go` intentionally panics in that case (CDX-S8-003 / CSA-2026-0054 policy documented in `CLAUDE.md`: `Use()` wraps `Handle` but never `HandleFast`, and registering a fast route on a mux with stdlib middleware panics at registration). Confirmed pre-existing by running the same test against a clean worktree of `d980583`: **identical failure**. The panic policy was introduced in commit `825c623` (S9 audit closure), long before this sprint; `properties_test.go` was last touched in `995d563` (gofmt) and has had this failing assertion since. Not fixed — out of scope (test file untouched this sprint).

**2. `reports/dos-resilience-tester/harness/2026-05-08-loadtest`: `TestThrottlePerIPCappedSaturationRevalidation` — real data race, in the test, not in MuxMaster.**
`-race` reports a genuine race, but it is the test harness sharing one `httptest.NewRecorder()` (`loadtest_test.go:774`) across 100 concurrent goroutines that each call `r.ServeHTTP(w, req)` on it (`:781`) and later `w.WriteHeader` (`:762`). `httptest.ResponseRecorder` is not safe for concurrent use by design — this would race with any router. Confirmed via `git log`: this file is unchanged since the `v1.0.0` release (`e9fd648`), i.e. long pre-existing. Not fixed — out of scope (test file untouched this sprint). The functional assertions the test makes (503 during saturation, 200 after drain) pass; only the concurrent-writer-on-one-recorder pattern is racy.

### Examples

All 13 examples (`authn`, `cache`, `graceful-shutdown`, `jwt`, `max-performance`, `oauth2`, `rest-api`, `reverse-proxy`, `server-sent-events`, `server-side-render`, `static-site`, `upload-file`, `versioning`) build and `go vet` cleanly, unchanged from before.

`examples-smoke.sh` (run **unpatched**, exactly as shipped) confirms rmp #255's fixes: **ALL EXAMPLES PASS**, including the reverse-proxy 32-conn/8s load burst that used to crash the process under `PoolRequestBundle=true`.

## 3. Same-session `benchstat` vs baseline (`d980583`) — round 1

**Superseded by §7-§8 below**, which re-measure after the round-2 fixes. Kept here as the historical record of what round 1 shipped, since it is what the round-2 root-cause analysis (§7) compares against.

Method: a detached git worktree of `d980583` was created in the scratchpad; `bench_test.go` (root package) and `reports/perf-audit-2026-05-12/middleware_bench_test.go` (unchanged file present in both trees — 35 identical benchmark names, ideal for a clean before/after) were run back-to-back, `count=10`, same host (AMD Ryzen 9 5900HX, 16 cores, load average 1.5–2.5/16 before and during the run), same `GOMAXPROCS`. `middleware/bench_test.go` is new this sprint (no baseline counterpart) and is reported standalone. Raw files and `benchstat` outputs are under `results/final/`.

### Headline table — root package (`bench_test.go`, 18 benchmarks in common)

| Benchmark | baseline | new | Δ |
|---|---|---|---|
| StaticRoute | 24.37 ns | 28.81 ns | **+18.22%** (p<0.001) |
| FastStaticRoute | 25.61 ns | 30.74 ns | **+19.99%** (p<0.001) |
| ParallelStaticRoute | 3.693 ns | 4.501 ns | **+21.86%** (p<0.001) |
| PooledParallelParamRoute | 6.483 ns | 7.451 ns | **+14.93%** (p<0.001) |
| PooledParamRoute2 | 54.66 ns | 60.64 ns | **+10.95%** (p<0.001) |
| PooledWildcardRoute | 41.32 ns | 44.94 ns | +8.77% (p<0.001) |
| PooledParamRoute1 | 42.48 ns | 46.38 ns | +9.16% (p<0.001) |
| FastParamRoute2 | 61.69 ns | 66.96 ns | +8.54% (p<0.001) |
| FastParamRoute1 | 43.56 ns | 47.16 ns | +8.28% (p<0.001) |
| PooledParamRoute3 | 58.81 ns | 61.78 ns | +5.06% (p<0.001) |
| FastParallelParamRoute | 16.57 ns | 17.23 ns | +3.98% (p<0.001) |
| WildcardRoute | 108.6 ns | 111.9 ns | +2.99% (p<0.001) |
| ParamRoute1 | 118.0 ns | 111.7 ns | -5.38% (p<0.001) |
| NotFound | 220.7 ns | 213.7 ns | -3.15% (p<0.001) |
| FastParamRoute3 | 86.26 ns | 83.66 ns | -3.02% (p<0.001) |
| ParallelParamRoute | 107.8 ns | 105.9 ns | -1.76% (p=0.013) |
| ParamRoute3 | 145.5 ns | 143.2 ns | -1.62% (p=0.003) |
| ParamRoute2 | 130.4 ns | 129.4 ns | -0.84% (p=0.020) |
| **geomean** | **48.06 ns** | **461.4 ns\*** | **+6.19%** |

\* the printed geomean mixes units across the table (`benchstat` computed it over the full set, including sub-second `AdversarialBacktracking`/`QuadraticBacktracking` sizes reported in the same column) — treat the **+6.19%** ratio as the meaningful number, not the absolute geomean value. B/op and allocs/op are **identical** (all `~` / "all samples are equal") for all 18 common benchmarks — this is a pure ns/op regression, no new allocations.

**New-only benchmarks (no baseline; reported standalone):** `RegisterRoutes/N=100` 54.72 µs, `N=1000` 676.4 µs, `N=5000` 4.672 ms (this is the WH-08 registration-complexity fix's own regression suite — see finding table below for the *actual* before/after, measured separately by the fix's own A/B harness since the old O(N²) `RegisterRoutes/N=5000` baseline take minutes and was not re-run here); `ServeFiles` 779.5 ns; `GroupServeFiles` 783.3 ns; `AdversarialBacktracking`/`QuadraticBacktracking` (regression-guard benchmarks for DIV-001, no baseline equivalent existed).

### Regression root cause — confirmed: DIV-001 backtracking bookkeeping (rmp #259)

`StaticRoute`, `FastStaticRoute` and `ParallelStaticRoute` all register a **mixed** tree (static + param + wildcard siblings, via `newBenchMux()`), so they never qualify for the `getValueStatic` fast path (Opt O1) — every lookup goes through the general `getValue`. The DIV-001 fix (rmp #259, "static branch falls back to the param sibling instead of the tree committing to a dead-end 404") added an unconditional `var stack backtrackStack` declaration at the top of `getValue` (`tree.go`, ~line 399), with a `[2]backtrackFrame` inline array, checked on every static-child match attempt even when no fork is ever taken. This is confirmed by:
1. `git diff d980583 -- tree.go` showing the new `backtrackStack`/`backtrackFrame` machinery declared unconditionally in `getValue`.
2. The uniform ~3–5 ns cost appearing across every benchmark that exercises the general `getValue` (pure-static, param, wildcard, pooled, fast — everything except the pure single-route trees that still hit `getValueStatic`).
3. `AdversarialBacktracking`/`QuadraticBacktracking`, the new regression-guard benchmarks added alongside DIV-001, exist specifically to bound this stack's worst case.

This is a deliberate, documented **correctness-for-speed trade-off**: routing.md rule 49 ("static routes always outrank named parameters" implies the param sibling must still be tried when the static branch dead-ends) was previously violated (a real routing bug, diverging from chi/bunrouter behaviour), and fixing it costs a small constant per lookup. No allocation regression accompanies it (0 B/op unchanged everywhere).

### Headline table — middleware audit suite (`reports/perf-audit-2026-05-12/middleware_bench_test.go`, 35 unchanged-file benchmarks in common)

Net: **-4.34% geomean** on sec/op. Selected wins (the sprint's actual targets):

| Benchmark | baseline | new | Δ |
|---|---|---|---|
| Middleware_Compress_LargeBody | 770.6 ns, 2858 B, 2 allocs | 311.3 ns, 85 B, 0 allocs | **-59.59% time, -97.0% B, -100% allocs** |
| Chain_Production | 8.109 µs, 939 B, 11 allocs | 6.491 µs, 915 B, 8 allocs | -19.95% time |
| Middleware_Logger | 7.284 µs, 40 B, 4 allocs | 5.646 µs, 0 B, 0 allocs | **-22.49% time, -100% allocs** |
| Chain_Heavy | 9.025 µs, 1686 B, 17 allocs | 7.581 µs, 1748 B, 19 allocs | -16.00% time |
| RedirectTSL | 846.4 ns, 1219 B, 13 allocs | 652.4 ns, 1062 B, 11 allocs | -22.93% time |
| OPTIONSAuto | 115.8 ns, 40 B, 3 allocs | 62.02 ns, 16 B, 1 alloc | **-46.44% time, -66.7% allocs** |
| Middleware_CleanPath_Dirty | 279.3 ns, 552 B, 5 allocs | 231.1 ns, 504 B, 4 allocs | -17.28% time |
| Middleware_StripSlashes_Dirty | 178.2 ns, 512 B, 3 allocs | 141.0 ns, 464 B, 2 allocs | -20.90% time |
| Middleware_Compress_SmallBody | 396.9 ns, 684 B, 2 allocs | 288.4 ns, 93.5 B, 0 allocs | -27.35% time, -86.3% B |

Selected regressions, with attributed cause:

| Benchmark | baseline | new | Δ | Cause |
|---|---|---|---|---|
| Middleware_NoCache | 162.8 ns, 400 B, 2 allocs | 231.3 ns, 480 B, 7 allocs | **+42.15% time, +250% allocs** | Header-slice aliasing fix (see below) |
| Middleware_CORS_Preflight | 219.9 ns, 416 B, 3 allocs | 267.2 ns, 464 B, 6 allocs | +21.51% time, +100% allocs | Header-slice aliasing fix |
| Middleware_CORS_AllowedOrigin | 198.7 ns, 416 B, 3 allocs | 223.8 ns, 432 B, 4 allocs | +12.66% time, +33% allocs | Header-slice aliasing fix |
| MethodNotAllowed | 151.9 ns, 83 B, 2 allocs | 141.2 ns, 101.5 B, 3 allocs | -7.05% time, **+50% allocs** | Header-slice aliasing fix (faster overall from the bitmask table, but +1 alloc for per-request `Allow` slice isolation) |
| PathParamLookup | 107.1 ns | 125.1 ns | +16.80% | DIV-001 (general `getValue`) |
| ParamsFromContext | 118.7 ns | 132.7 ns | +11.80% | DIV-001 |
| GroupDispatch / NestedGroupDispatch | 95.6 / 95.9 ns | 110.0 / 110.6 ns | +15.0% / +15.3% | DIV-001 |
| Middleware_RequestID_Generate/Propagate | 285.7 / 246.7 ns | 318.8 / 276.6 ns | +11.6% / +12.1% | not fully isolated — allocs unchanged (4/4, 800B/800B); likely a small constant-time cost shared with the DIV-001 dispatch path, not confirmed at the instruction level in this pass |
| Middleware_WithValue, Middleware_BasicAuth_Hit, Chain_AuthBasic, Chain_Security, Middleware_APIKey_* | +2–11% each, no allocs change | | | Small, consistent overhead of the same order as DIV-001's per-lookup cost |

**Header-slice aliasing fix (security-motivated, not a regression to reverse).** The CHANGELOG documents a real defect closed this sprint: WH-05 and WH-09 originally hoisted the header **value** (`[]string`, not just the string) to package/instance scope to save an allocation. That is unsafe — any code indexing directly into the header map's slice (`w.Header()["X"][0] = ...`) would mutate the *shared* backing array, corrupting that header for every other concurrent/future request through the same cached path until process restart. Five call sites were fixed (`response.go`, `mux.go`'s 405/OPTIONS cache, `middleware/set_header.go`, `middleware/cors.go`, `middleware/no_cache.go`): the string is still hoisted, but the one-element `[]string` wrapping it is now allocated fresh per request. `Middleware_NoCache`'s +5 allocs and `Middleware_CORS_*`'s +1–3 allocs are the direct, necessary cost of that correctness fix — the alternative (the old numbers) was a live cross-request header-corruption vulnerability. Not something to optimize away without reintroducing the bug.

New-only (`middleware/bench_test.go`, no baseline): `BenchmarkThrottlePerIP`, `BenchmarkLogger`, `BenchmarkCompress`, `BenchmarkRealIP`, `BenchmarkAPIKeyHit` (442–446 ns, 416 B, 6 allocs), `BenchmarkJWTAuthHS256` (4.40–4.44 µs, 738 B, 7 allocs) — these are the fix-validation benchmarks written alongside WH-01/02/03/06/11/12; raw numbers in `results/final/middleware-new-only.txt`.

## 4. Examples re-profile against the new code

Re-ran the waste-hunt `examples`+`attribute` methodology (13 examples, real sockets, 32 keep-alive connections, 12 s load window, 10 s CPU + alloc profile, nearest-owner attribution) **without** applying `patches/*.diff` — the panics/crashes those patches worked around are now fixed in `examples/` itself (confirmed by `examples-smoke.sh` above), so patching would apply stale workarounds to already-fixed source. A copy of the harness's `profile_example`/`attribute_example` logic with the patch step removed is in `reports/perf-lab-2026-09-24/waste-hunt/results/final/examples/` alongside its output; raw profiles and tops are under each example's subdirectory.

**Result: 13/13 examples built, started, and ran without crashing.** 11/13 report `STATUS-OK`. The 2 apparent mismatches are **not regressions** — both are stale scenario-fixture expectations written against the pre-fix behaviour, and both independently confirm a fix:

- `max-performance`: `GET /debug/pprof/cmdline` — scenario hardcodes `expect=404` with the comment *"404 = example defect"* (the original campaign's own diagnosis). It now returns `200`: the pprof `Mount` is reachable, as rmp #255 fixed and the CHANGELOG documents.
- `static-site`: two lines. `GET /nope` — scenario hardcodes `expect=200` with the comment *"200 = Compress WriteHeader defect"*; it now correctly returns `404` — the exact Compress 1xx/first-wins fix (MID-COMPRESS-1) closes this. `GET /assets/assets/style.css` (a doubled-prefix workaround for the old routing quirk) — the underlying quirk is fixed, so the doubled path now legitimately 404s; verified manually that the correct single path `GET /assets/style.css` returns `200` (also confirmed by `examples-smoke.sh`, which uses the correct path and passes). The scenario JSON files (`scenarios/max-performance.json`, `scenarios/static-site.json`) should be updated to the new expected values — flagged for the user's decision, not changed here (out of scope for this gate).

### MuxMaster CPU/bytes share, old campaign (patched, pre-fix) vs new (unpatched, post-fix)

| Example | CPU mw/root — old | CPU mw/root — new | bytes mw/root — old | bytes mw/root — new |
|---|---|---|---|---|
| authn | 11.1% / 2.4% | 8.3% / 2.7% | 30.4% / 6.2% | 26.0% / 6.7% |
| cache | 9.0% / 2.2% | 6.7% / 2.4% | 22.6% / 7.8% | 22.1% / 9.8% |
| graceful-shutdown | 16.8% / 0.3% | 15.5% / 0.5% | 47.9% / 0.0% | 47.5% / 0.0% |
| jwt | 14.6% / 2.9% | 12.1% / 2.9% | 34.2% / 4.3% | 31.6% / 4.3% |
| max-performance | 6.5% / 0.8% | 5.5% / 0.9% | 22.1% / 5.3% | 22.6% / 2.4% |
| oauth2 | 11.9% / 5.1% | 6.8% / 5.3% | 20.5% / 14.4% | 14.3% / 15.4% |
| rest-api | 15.8% / 4.3% | 13.2% / 4.8% | 39.5% / 10.7% | 40.9% / 11.5% |
| reverse-proxy | 1.5% / 2.1% | 1.2% / 1.9% | 3.3% / 16.1% | 3.4% / 16.3% |
| server-sent-events | 0.3% / 0.1% | 0.4% / 0.2% | 16.9% / 0.0% | 16.9% / 0.0% |
| server-side-render | 8.6% / 0.7% | 7.2% / 0.6% | 14.1% / 3.4% | 13.1% / 1.5% |
| static-site | 9.9% / 2.0% | 8.5% / 1.9% | 28.0% / 5.2% | 28.4% / 3.8% |
| upload-file | 0.3% / 0.1% | 0.3% / 0.2% | 0.3% / 0.0% | 0.2% / 0.0% |
| versioning | 1.4% / 0.7% | 1.3% / 0.7% | 22.3% / 0.3% | 22.8% / 0.3% |

Middleware CPU share dropped in every example that uses the fixed middlewares (largest: oauth2 11.9%→6.8%, cache 9.0%→6.7%, jwt 14.6%→12.1%, rest-api 15.8%→13.2%) — consistent with the WH-01/02/03/06/11/12 middleware fixes. Root-package CPU share is flat to marginally up (rest-api 4.3%→4.8%, oauth2 5.1%→5.3%) — consistent with the small, uniform DIV-001 cost identified above; it does not dominate any example's profile. Throughput held or improved in 11/13 examples (e.g. oauth2 +2.5% req/s with p50 latency 339µs→272µs, static-site +3.4%, jwt +2.3%); `max-performance` (-0.8%) and `upload-file` (-1.4%) are within noise for workloads where MuxMaster's own share is ≤0.9%.

## 5. WH-01..WH-13 finding-by-finding outcome

| ID | Finding | Outcome | Final numbers (this gate / CHANGELOG) |
|---|---|---|---|
| WH-01 | `ThrottlePerIP` timer + entry re-creation | **Fixed** (rmp #251) | -97.09% (4051 ns → 118 ns), 5→0 allocs, 2.2→0.07 `clock_gettime`/req |
| WH-02 | `Logger` alloc + clock reads | **Fixed** (rmp #251) | -24.03% (this gate: -22.49% on the audit suite), 5→0 allocs |
| WH-03 | `Compress` sniff buffer | **Fixed** (rmp #251) | chunked/large body -59.59% time, -97.0% B, -100% allocs (this gate) |
| WH-04 | `Mount`/`CleanPath`/`ServeFiles` deep `Clone` | **Fixed** (rmp #250) | `CleanPath` dirty -17.28% (this gate); Mount/ServeFiles per CHANGELOG -76% |
| WH-05 | `SetHeader`/`Text` per-call header alloc | **Fixed, with a defect found and fixed in the same pass** | see "Header-slice aliasing fix" above — the first version of this optimization introduced cross-request header corruption; corrected before this gate |
| WH-06 | `JWTAuth` header decode + HMAC copy | **Fixed** (rmp #251) | -14.03% per CHANGELOG; `BenchmarkJWTAuthHS256` new baseline 4.40–4.44 µs, 738 B, 7 allocs |
| WH-07 | `Logger` blocks `sendfile` | **Fixed** (folded into WH-02 — `io.ReaderFrom` delegation) | `sendfile` syscall count preserved per CHANGELOG |
| WH-08 | O(N²) registration | **Fixed** (rmp #253) | `RegisterRoutes/N=5000` 2.86 s → 4.672 ms (this gate; CHANGELOG reports 4.7 ms, -99.84%) |
| WH-09 | 405/OPTIONS `Allow` rebuild | **Fixed, with a defect found and fixed in the same pass** | `OPTIONSAuto` -46.44% time, -66.7% allocs (this gate); see WH-05 note — same aliasing class |
| WH-10 | Redirect chain rebuilt per request | **Fixed** (rmp #248/#250) | `RedirectTSL` -22.93% (this gate); CHANGELOG also reports the ABA-race fix via generation-tagged cache |
| WH-11 | `RealIP` `strings.Split` | **Fixed** (rmp #251) | -21.66%/-14.79% per CHANGELOG |
| WH-12 | `APIKey` boxed identity | **Fixed** (rmp #251) | -14.96% per CHANGELOG; `BenchmarkAPIKeyHit` new baseline 442–446 ns, 416 B, 6 allocs |
| WH-13 | Stale comment (static path "skips zeroing") | **Fixed** (comment corrected) | `tree.go` comment now explains `ps` is always zeroed by the compiler; no code change |

## 6. Known trade-offs (carry forward, do not "fix" without a design decision) — round 1 status

1. ~~DIV-001 backtracking bookkeeping~~ — **addressed in round 2, §7**: reduced from +18-22% to +5-14% on the affected benchmarks via a lazy, inlined design. Not fully eliminated; see §7 for the measured residual and why it appears to be a hard floor for these specific benchmarks, not an implementation gap.
2. ~~Header-slice aliasing fix allocs~~ — **addressed in round 2, §8**: `NoCache`/`CORS`/`MethodNotAllowed`/`serveRedirect` now pay at most one allocation per request for however many header values they set, instead of one allocation per header.
3. ~~Two pre-existing, unrelated test issues~~ — **both fixed in round 2, §9**, per the coordinator's explicit ruling that pre-existing defects are in scope for this sprint.
4. **Two stale scenario-fixture expectations** (`scenarios/max-performance.json`, `scenarios/static-site.json`) encode pre-fix statuses and now read as "mismatches" that are actually confirmations of fixes. Still flagged for the user; not edited (unchanged from round 1 — out of scope for round 2, which did not touch the waste-hunt harness's own scenario fixtures).

## 7. Round 2, item 1 — making the DIV-001 backtracking cost lazy

**Target (coordinator):** no statistically significant regression vs `d980583` on `StaticRoute`, `FastStaticRoute`, `ParallelStaticRoute`; no DUFFZERO/extra zeroing on the success path (`-gcflags=-S`); the three named fuzz targets clean for 60 s each; the deep-fork tests passing.

### What shipped

`tree.go`'s `getValue` is now a single function containing the **exact pre-DIV-001 walk** (byte-for-byte, from before rmp #259), plus one extra `bool` local, `couldBacktrack`, set at the single point a static child is chosen over an available wildchild sibling. If the walk finds a handler, or fails with `couldBacktrack` still `false` (no fork was ever skipped — the common `NotFound` shape too), `getValue` returns directly: identical instructions to the pre-DIV-001 code, no backtracking state at all. Only when the walk fails **and** a fork was skipped does `getValue` discard that result, rewind `params`, and call `getValueBacktrack` — the full bounded-backtracking walk (frame0\*/frame1\* named scalars + a rare heap-backed overflow slice, `//go:noinline`) — from the root. `getValueBacktrack` is entered only on that already-rare path.

### Two rejected designs, measured and discarded

| Design | StaticRoute Δ vs `d980583` | Why rejected |
|---|---|---|
| `var stack backtrackStack` (round 1's shipped version): one address-taken struct, unconditional | +18.22% | Baseline this task started from |
| Same one-pass shape, backtrackInline frames as independent named scalars instead of an array field | **+28.38%** (worse) | More branches/spill traffic for a route that forks on every call regardless of how cheap the recording is; forking here is unavoidable, not avoidable |
| Split `getValueFast` (separate, un-inlined function) called unconditionally by a thin `getValue` wrapper | +10.50% | Backtracking cost genuinely removed, but the mandatory function-call boundary on **every** lookup made already-fine param/pooled/fast paths measurably worse (`PooledParamRoute1` +9%→+28%) — net loss |
| **Shipped: inlined optimistic walk + noinline retry (no split, no struct)** | **+12.58% to +13.29%** (final, count=10) | Best result found: static-path regression cut roughly in half vs round 1, **and** every param/pooled/fast benchmark improved or stayed flat vs the split design |

### Root-cause finding: the target is not fully reachable for these three benchmarks

`StaticRoute`/`FastStaticRoute`/`ParallelStaticRoute` request `/users/list`, and `newBenchMux()` registers `/users/list` as a static sibling of the wildchild `/users/:id`. DIV-001 correctness requires recording a fallback **before** committing to the static child at that exact node, on **every** call to that route — this is not a probabilistic or avoidable cost, it is what "static routes always outrank named parameters, but the param sibling must still be tried if the static branch dead-ends" (routing.md rule 49) means for a tree with that shape. `d980583` never recorded anything there because it did not implement that rule at all (the DIV-001 bug). So "no regression vs `d980583`" and "DIV-001 is enforced" are in direct tension for a route that forks on every call — one of them has to give, and the sprint's decision (confirmed correct, not revisited here) was to keep the correctness fix.

The lazy design **does** deliver "pays nothing" for lookups that never fork, exactly as asked: `NotFound-16` (which fails at the root, a purely static branch point with no wildchild sibling) shows -1.29% to -3.06% across **all three** designs tried, including the shipped one — a consistent, small **improvement**, never a regression. `ParamRoute2` similarly shows `~` (no significant change, p=0.926) in the final measurement. The residual +12-14% on the three named benchmarks is the cost of the "minimal store" the coordinator's own framing anticipated ("a lookup that forks must pay only the minimal store"), for a route that is guaranteed to fork on every single call by construction.

### Verification

- **`-gcflags=-S`:** `go build -gcflags=-S .` shows `getValue`'s compiled frame at **208 bytes** (down from **440 bytes** in round 1's `var stack backtrackStack` version; `d980583`'s pre-DIV-001 `getValue` was 216 bytes) with **zero DUFFZERO occurrences** anywhere in its disassembly. `getValueBacktrack` (440 bytes, carries all the backtracking state) is a separate, `//go:noinline` function never reached on the success/non-forking path.
- **Correctness — full suite:** `go test -race -count=1 .` — all tests pass, including `TestDeepForkBacktracking_LinearNotExponential` (depths 1, 2, 3, 8, 9, 50) and `TestDeepForkBacktracking_NoPanicAtExtremeDepth` (depth 500).
- **Fuzzing, 60 s each, `-race` off (native Go fuzzing does not support `-race` concurrently with corpus workers on this Go version the way `-fuzz` invokes them; the full suite above already covers `-race`):**

  | Target | Result |
  |---|---|
  | `FuzzRoutingDifferentialAgainstReferenceMatcher` | PASS — 5,133,725 execs, 8 new corpus entries, 0 failures |
  | `FuzzRouteRegistrationOrderIndependence` | PASS — 2,061,704 execs, 4 new corpus entries, 0 failures |
  | `FuzzPathCopySnapshotImmutability` | PASS — 2,371,023 execs, 2 new corpus entries, 0 failures |

- **benchstat, count=10, same session, final:** see §8's headline table (root suite) — `StaticRoute` +13.03%, `FastStaticRoute` +13.21%, `ParallelStaticRoute` +12.86%, all `p=0.000`; 0 B/op and 0 allocs/op change everywhere (confirmed pure ns/op trade, no new allocations).

**Disposition:** shipped as the best available result. The residual regression on these three specific benchmarks is reported, not hidden, with the root cause traced to their tree topology rather than an implementation shortfall. If the user wants the residual recovered further, the only remaining lever identified is changing the benchmarks' route topology (they are free to fork or not — `bench_test.go` is test code) or accepting a design that moves the retry decision to the caller (mux.go), which would require changing `getValue`'s signature and touching all 5 call sites; not attempted here as it is a more invasive, higher-risk change for an uncertain further gain.

## 8. Round 2, item 2 — one allocation per request per middleware for header-slice isolation

**Target (coordinator):** keep per-request header-slice isolation (specification/README.md design principle 6) but cut to one allocation per request per middleware, via a shared backing array with capped (`vals[i:i+1:i+1]`) sub-slices, applied wherever a middleware or handler writes more than one prebuilt header.

### Sites found and fixed

| Site | Headers fused | Before (safe, unoptimised) | After |
|---|---|---|---|
| `middleware/no_cache.go` `NoCache()` | Cache-Control, Pragma, Expires, Surrogate-Control, X-Accel-Expires (always all 5) | 5 allocs | **1 alloc** (`&[5]string{...}`) |
| `middleware/cors.go` `CORS()` | Up to 7: Access-Control-Allow-Origin, Vary, -Credentials, -Expose-Headers, -Allow-Methods, -Allow-Headers, -Max-Age (conditional) | 1-7 allocs depending on config/method | **1 alloc** (`new([7]string)`, sized for the worst case; unused slots are simply never referenced by any header key) |
| `mux.go` `lazyMethodNotAllowed` default 405 handler | Allow, Content-Type, X-Content-Type-Options (always all 3) | 3 allocs | **1 alloc** (`&[3]string{...}`) |
| `mux.go` `serveRedirect` | Location + Content-Type (GET/HEAD with no caller-set Content-Type) | 2 allocs (when both set) | **1 alloc** when both are set; Location alone still gets its own 1-element slice when Content-Type is not set (no `[2]string` allocated for a header that will not be used) |

`middleware/set_header.go` and `response.go`'s `JSON`/`XML`/`Text` were checked and found to already be at 1 allocation per call (single header each) — no change needed there.

### Correctness technique

Each site allocates one `[N]string` array per request (`&[N]string{...}` for a fixed, always-set set of headers; `new([N]string)` + a running index for a conditional set), and assigns each header as the **full slice expression** `vals[i:i+1:i+1]` — capping capacity at 1. `http.Header.Add` grows a header via `append(h[key], value)`; because each header's slice already has `cap == len == 1`, that `append` always allocates a **new** backing array rather than writing into `vals[i+1]`, so one header's later growth cannot corrupt an adjacent header sharing the same array. Cross-**request** isolation (the original MID-*-1 concern) is unaffected: `vals` is allocated fresh on every call, exactly as the separate slices were before.

### New tests

All the existing aliasing regression tests (`header_aliasing_wastehunt_test.go`, `middleware/nocache_wastehunt_test.go`, `middleware/cors_wastehunt_test.go`, and the rest of the `*_wastehunt_test.go` suite) still pass unmodified — they check a **different** property (mutating one request's header does not leak into another request) that this change does not affect. Four new tests check the property the coordinator asked for directly — that **appending** to one header's slice does not corrupt a **sibling** header sharing the same backing array, within the same response:

- `TestMethodNotAllowed_DefaultHandler_AppendToOneHeader_DoesNotCorruptSiblingHeaders` (`header_aliasing_wastehunt_test.go`)
- `TestRedirect_AppendToLocation_DoesNotCorruptContentType` (`header_aliasing_wastehunt_test.go`)
- `TestNoCache_AppendToOneHeader_DoesNotCorruptSiblingHeaders` (`middleware/shared_array_sibling_isolation_wastehunt_test.go`, new file)
- `TestCORS_AppendToOneHeader_DoesNotCorruptSiblingHeaders` (same new file)

All four pass; `go vet`, `staticcheck` and `golangci-lint` are clean on every touched file (one `ineffassign` finding in the CORS rewrite — a final, never-read `n++` — was fixed by removing that last increment).

### Measured result (`reports/perf-audit-2026-05-12/middleware_bench_test.go`, count=10, vs `d980583`)

| Benchmark | round-1 (safe, unoptimised) | round-2 (final) | vs `d980583` baseline |
|---|---|---|---|
| `Middleware_NoCache` | 231.3 ns, 480 B, **7 allocs** (+250%) | 201.1 ns, 480 B, **3 allocs** | +23.56% time, **+50% allocs** (2→3; down from +250%) |
| `Middleware_CORS_AllowedOrigin` | 223.8 ns, 432 B, **4 allocs** (+33%) | 233.4 ns, 512 B, **3 allocs** | +17.52% time, **allocs unchanged** (3→3) |
| `Middleware_CORS_Preflight` | 267.2 ns, 464 B, **6 allocs** (+100%) | 245.9 ns, 512 B, **3 allocs** | +11.83% time, **allocs unchanged** (3→3) |
| `MethodNotAllowed` | 141.2 ns, 101.5 B, **3 allocs** (+50%) | 118.6 ns, 94 B, **1 alloc** | **-21.90% time, -50% allocs** (2→1 — net *better* than the original pre-optimisation baseline) |
| `RedirectTSL` | 652.4 ns, 1062 B, 11 allocs | 618.0 ns, 1062 B, **10 allocs** | -26.99% time, -23.08% allocs (was -22.93%/-15.38% in round 1) |

`Middleware_CORS_AllowedOrigin` and `Middleware_CORS_Preflight` allocs are now **exactly equal** to `d980583`'s original, pre-optimisation, pre-aliasing-bug baseline (3 allocs each) — the fix fully absorbed the cost of the earlier security correction. `MethodNotAllowed` and `RedirectTSL` both beat the original baseline outright. `NoCache` still carries 1 net extra allocation over the original baseline (2→3) — 4 allocations recovered out of the 5 the safety fix had added, the `[5]string` array itself being the unavoidable 1.

Overall middleware-suite geomean improved from **-4.34%** (round 1) to **-7.32%** (round 2, final) with these fixes layered on top of round 1's other wins.

## 9. Round 2, item 3 — the two pre-existing test defects, ruled in scope

The coordinator ruled that pre-existing defects the round-1 gate surfaced are in scope for this sprint. Both are now fixed.

### 9.1 `TestThrottlePerIPCappedSaturationRevalidation` — real data race, fixed

**Diagnosis (unchanged from round 1):** the test shared one `httptest.NewRecorder()` across 100 concurrent goroutines, each calling `r.ServeHTTP(w, req)` on it. `httptest.ResponseRecorder` has no internal synchronisation, so this raced regardless of which router served the request — a test-harness bug, not a MuxMaster defect.

**Fix:** each goroutine now gets its own `httptest.NewRecorder()` (`reports/dos-resilience-tester/harness/2026-05-08-loadtest/loadtest_test.go`). The test's actual assertions (503 during saturation, 200 after drain, measured via the separate `victimW`/`victimW2` recorders that were never part of the race) are unchanged. Verified: `go test -race -count=1 -run TestThrottlePerIPCappedSaturationRevalidation -v .` passes with no race report; the module's full `go test -race -count=1 ./...` (62.5 s) passes.

### 9.2 `TestProp_HandleFastVsHandleIsolation` — the test was wrong, not the policy

**Determination, against the specification:** `CLAUDE.md`'s "Pre vs Use × Handle vs HandleFast" policy matrix (CDX-S8-003) states `mux.Use(...)` "wraps `HandleFast`? NO — panics at `HandleFast` registration (CSA-2026-0054)". `mux.go`'s `HandleFast` implementation carries this exact panic today, with a comment reading `// FPE-2026-010: panic if stdlib middleware (registered via Use) is present.` `git log -S` on that panic string shows it was introduced in commit `825c623` ("fix(security,middleware): close S9 audit findings"), which also closes `FPE-010` — the same identifier the coordinator cited. The failing property test asserted the **opposite**: that `GETFast` registered after `Use()` middleware must succeed **silently**, with the fast route simply bypassing that middleware, and its own removed comment called this "by design." That "silent bypass" is precisely hypothesis H-10 the test's own doc comment names — the exact shape the security team later closed by making it panic instead. The test predates that fix and was never updated; confirmed by running it against a clean worktree of `d980583` (the sprint's own starting commit) — **identical failure**, proving this is not a sprint-18 regression either.

**Conclusion: the test was wrong, not the code.** No specified behaviour was changed. Fixed by rewriting `TestProp_HandleFastVsHandleIsolation` (`reports/fuzzing-and-property-engineer/harness/properties_test.go`) to verify the actual specified policy: (a) `GETFast` on a `Mux` that already has `Use()` middleware must panic, with a message identifying `HandleFast` and "stdlib middleware"; (b) the two safe compositions still isolate correctly and do not panic — `Handle`+`Use` (stdlib middleware runs on the stdlib route) and `HandleFast`+`UseFast` (fast middleware runs on the fast route, with no stdlib middleware present to bypass). Verified: `go test -run TestProp_HandleFastVsHandleIsolation -v .` passes (100 rapid-check iterations); the module's full `go test -race -count=1 ./...` passes; `staticcheck`/`golangci-lint` show no new findings in the modified file.

## Reproduce

```bash
# Lint / build / vet / staticcheck / race — main module
cd /data/dev/github.com/FlavioCFOliveira/MuxMaster
go build ./... && go vet ./... && staticcheck ./... ; golangci-lint run ./... ; go test -race -count=1 ./...

# Same-session benchstat vs baseline (recreates the worktree used for this report)
git worktree add --detach /tmp/baseline-d980583 d980583
(cd /tmp/baseline-d980583 && go test -run '^$' -bench . -benchmem -count=10 .) > /tmp/root-baseline.txt
go test -run '^$' -bench . -benchmem -count=10 . > /tmp/root-final.txt
benchstat /tmp/root-baseline.txt /tmp/root-final.txt
git worktree remove --force /tmp/baseline-d980583

# Same-session benchstat, middleware audit suite (round 2 final)
(cd /tmp/baseline-d980583 && go test -run '^$' -bench . -benchmem -count=10 ./reports/perf-audit-2026-05-12) > /tmp/mw-baseline.txt
go test -run '^$' -bench . -benchmem -count=10 ./reports/perf-audit-2026-05-12 > /tmp/mw-final.txt
benchstat /tmp/mw-baseline.txt /tmp/mw-final.txt

# -gcflags=-S check for DUFFZERO in getValue (round 2, item 1)
go build -gcflags=-S . 2>/tmp/asm.txt
awk '/TEXT.*getValue\(SB\)/,/TEXT.*getValueBacktrack\(SB\)/' /tmp/asm.txt | grep -c DUFFZERO   # expect 0

# The three named fuzz targets, 60 s each (round 2, item 1)
go test -run '^$' -fuzz '^FuzzRoutingDifferentialAgainstReferenceMatcher$' -fuzztime 60s .
go test -run '^$' -fuzz '^FuzzRouteRegistrationOrderIndependence$' -fuzztime 60s .
go test -run '^$' -fuzz '^FuzzPathCopySnapshotImmutability$' -fuzztime 60s .
go test -run 'TestDeepForkBacktracking' -v .

# Sibling-header isolation tests (round 2, item 2)
go test -run 'AppendToOneHeader|AppendToLocation' -v . ./middleware/...

# Examples re-profile (unpatched, against current code)
reports/perf-lab-2026-09-24/waste-hunt/examples-smoke.sh   # functional smoke, all 13
# CPU/alloc profiling: reuse run.sh's build_tools/profile_example/attribute_example
# functions with the `patch` step removed (see the rationale in §4 above).

# The two round-2 defect fixes
go test -race -count=1 -run TestThrottlePerIPCappedSaturationRevalidation -v \
  ./reports/dos-resilience-tester/harness/2026-05-08-loadtest/...
go test -run TestProp_HandleFastVsHandleIsolation -v \
  ./reports/fuzzing-and-property-engineer/harness/...
```

## Raw files

All under `reports/perf-lab-2026-09-24/waste-hunt/results/final/`:

- `root-baseline-d980583.txt` — root `bench_test.go` on `d980583`, count=10 (shared baseline for both rounds)
- `root-new.txt`, `root-benchstat.txt` — round-1 result (the `var stack backtrackStack` design) and its benchstat vs baseline
- `root-new-lazystack.txt`, `root-benchstat-lazystack.txt` — round-2 rejected design #1 (named scalars, one-pass, still worse)
- `root-new-split.txt`, `root-benchstat-split.txt` — round-2 rejected design #2 (split `getValueFast`/`getValueBacktrack`, unconditional call)
- `root-new-merged.txt`, `root-benchstat-merged.txt` — round-2 shipped design, first measurement
- `root-final.txt`, `root-benchstat-final.txt` — round-2 shipped design, **final** measurement (after item 2's changes too; this is the number quoted in §7-§8)
- `middleware-audit-baseline-d980583.txt` — `reports/perf-audit-2026-05-12/middleware_bench_test.go` on `d980583`, count=10 (shared baseline for both rounds)
- `middleware-audit-new.txt`, `middleware-audit-benchstat.txt` — round-1 result and its benchstat vs baseline
- `middleware-audit-final.txt`, `middleware-audit-benchstat-final.txt` — round-2 **final** result and benchstat vs baseline (quoted in §8)
- `middleware-new-only.txt` — new-tree-only `middleware/bench_test.go`, count=10 (unaffected by round 2's items 1-3, not re-run)
- `examples/<name>/` — `load.txt`, `warmup.txt`, `rss.txt`, `attribution.txt`, `cpu-top.txt`, `alloc_space-top.txt`, `alloc_objects-top.txt`, `*-muxmaster-focus.txt`, `server-stderr.log`, `profiles/{cpu,allocs}.pb.gz`, per example; `environment.txt` at the top level (round 1 only — not re-profiled for CPU/alloc shares in round 2, since items 1-3 were not requested to be re-profiled at the example level; `examples-smoke.sh` was re-run functionally after round 2 and is 13/13 PASS)
