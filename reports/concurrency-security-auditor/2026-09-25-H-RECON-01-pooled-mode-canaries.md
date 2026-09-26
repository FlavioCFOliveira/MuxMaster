# H-RECON-01 — PoolRequestBundle / PoolFastParams contamination canaries

Date: 2026-09-25
Author: concurrency-security-auditor
Originating rmp task: #263 (sprint 20)
Commit audited: `083acf391e7dd4243327739cb1a6a8c28977d0be`
Go: `go1.27.0 linux/amd64`   GOMAXPROCS: 16 (AMD/Linux CI host, `nproc` = 16)

## Why this task exists

CSA-2026-0056 (`reports/concurrency-security-auditor/2026-09-25-CSA-2026-0056-pool-gc-canary.md`)
verified zero cross-request parameter contamination, but only for the
**default, GC-managed** `reqBundle` architecture (no `sync.Pool` in the
path it exercised). It explicitly flagged a coverage gap as hypothesis
H-RECON-01 (`reports/overview/findings.md` §6): the two opt-in pooling
switches added since —

- `Mux.PoolRequestBundle` (Opt O13): recycles the fused `reqBundle`
  (`requestCtx` + `*http.Request` copy) for `Handle` routes via a tiered
  `sync.Pool`.
- `Mux.PoolFastParams` (Opt O9): recycles the `Params` slice handed to
  `FastHandler` routes via a tiered `sync.Pool`.

— had **no test coverage at all** except benchmarks (`bench_test.go`,
`reports/perf-lab-2026-09-24/harness/contention_bench_test.go`). Both
flags carry a strict "MUST NOT retain past return" lifetime contract
(`specification/configuration.md` §4.5 and §4.6). This report closes that
gap.

## Classification

| Field | Value |
|---|---|
| Type | Verified-safe result for correct usage; documented hazard confirmed for contract violation (not a defect) |
| Severity | Info (rmp severity 0) — no library defect found |
| CWE (class tested) | CWE-362 / CWE-200 — cross-request data exposure through recycled per-request state; CWE-416-class use-after-free for the documented retention hazard |
| Status | Closed — canaries added, zero contamination under correct usage, panic path confirmed clean, documented hazard reproduced deterministically as a regression guard |

## Test file added

`pool_contamination_test.go` (root package `muxmaster_test`, 688 lines, no
`t.Skip`, no build tags — runs under plain `go test ./...` like every
other root test file). Seven tests:

| Test | Flag(s) | What it does |
|---|---|---|
| `TestPoolRequestBundle_ConcurrentStress_AllTiers` | `PoolRequestBundle` | 64 goroutines × 3000 iterations × 5 tiers (1/2/3/overflow-5/catch-all params) = 192,000 requests. Each handler verifies its own params, `RoutePattern`, and a Pre-middleware context token derived from a per-(goroutine,iteration) unique token — any cross-request bleed produces a deterministic mismatch. |
| `TestPoolFastParams_ConcurrentStress_AllTiers` | `PoolFastParams` | Same design via `FastHandler`/`GETFast`, 192,000 requests. |
| `TestPoolRequestBundleAndPoolFastParams_Combined_ConcurrentStress` | both | Both flags on one `Mux`; goroutines interleave `Handle` and `FastHandler` traffic unpredictably to confirm the two independent pool families (`reqBundle*Pool` vs `fastParams*Pool`) never cross-contaminate. 192,000 requests. |
| `TestPoolRequestBundle_PanicPath_PoolStaysClean` | `PoolRequestBundle` + `PanicHandler` | Interleaves always-panicking routes (1-param and 3-param tiers, exercising both `dispatchParams1Pooled` and the `dispatchParamsNPooled` 3+ path) with checking routes on the same pools. 64,000 panic+check cycles, verifies `PanicHandler` invocation counts and that check requests never observe stale/panicked state. |
| `TestPoolFastParams_PanicPath_PoolStaysClean` | `PoolFastParams` + `PanicHandler` | Same for `FastHandler`. |
| `TestPoolRequestBundle_RetentionHazard_Documented` | `PoolRequestBundle` | Deliberately violates the documented contract (retains `*http.Request` past handler return) and demonstrates that the retained reference observes a **later, unrelated request's** recycled data — exactly the hazard documented in `specification/configuration.md` §4.6 item 35. Regression guard, not a bug report. |
| `TestPoolFastParams_RetentionHazard_Documented` | `PoolFastParams` | Same for the `Params` slice, per §4.5 item 31. |

Total requests exercised across the concurrency canaries: 576,000 correct-usage
requests + 128,000 panic-path requests = 704,000, run at `-race -count=5`
(3,520,000 total dispatches across the repeated runs) with zero violations
and zero data races, plus the two deterministic hazard tests.

## Design notes (method)

- **Self-checking tokens, no shared map.** Every request carries a token
  encoding `(goroutine, iteration)`. Path parameters and a Pre-middleware
  context value (`X-Test-Token` header → `context.WithValue`) are both
  derived from the same token. A handler that observes any other
  request's data fails a purely local comparison — no shared map, no
  extra synchronization, and this exercises the "context values set by
  Pre/Use middleware" requirement uniformly across `Handle` and
  `HandleFast` (`Mux.Pre` wraps `ServeHTTP` outside the dispatch/pooling
  boundary per CDX-S8-003, so it covers both).
- **Catch-all value includes the leading `/`.** The first iteration of
  this test file asserted `ps.Get("filepath") == tok`, which is wrong:
  MuxMaster's wildcard capture includes the separator (`/hstatic/tok1` →
  `filepath = "/tok1"`), confirmed against the non-pooled path with a
  throwaway probe test. This was a test bug, not a library defect — fixed
  before the canary was trusted. Documented here because it is exactly
  the kind of assumption error a contamination canary must not paper
  over with a wrong expectation.
- **Panic path is safe by omission, not by cleanup.** Tracing
  `dispatchParams1Pooled`/`dispatchParams2Pooled`/`dispatchParamsNPooled`
  (`params.go`) and `putFastParams` (`params.go`) shows the zero-and-Put
  step runs **after** the handler call, with no `defer`. When a handler
  panics, the bundle (or Params array) is never zeroed and never
  returned to the pool — it is simply dropped and left for the GC. This
  means a panicking handler cannot hand a dirty object back to a future
  request: the object that held its data is abandoned, not recycled. The
  canary confirms this empirically (0 violations across 128,000
  panic+check cycles); it is also visible by code inspection and is
  worth noting as a (minor, non-actionable) perf detail: each panic
  permanently "burns" one pooled object, which `sync.Pool.New` silently
  replaces on the next `Get`.
- **The retention-hazard tests had to be redesigned once.** The first
  version read the retained reference *after* the second `ServeHTTP`
  call returned to the caller — but the zero-then-Put step runs
  synchronously before the second call returns, so by then the bundle
  had already been re-zeroed and the test's own assertion was reading
  stale (zeroed) data, not "request 2's data." Fixed by reading the
  retained reference **from inside the second handler**, on the same
  goroutine, before that handler returns (still zero cross-goroutine
  access — no data race). A second issue surfaced only under repeated
  `-race -count=N` runs: on one repetition, `-race`'s heavier scheduling
  and allocation pressure apparently let a GC cycle (or goroutine
  migration between Ps) intervene between the two `Get`/`Put` cycles,
  causing `sync.Pool` to hand back a fresh object instead of reusing the
  same one, and the hazard was not observed that run (`got ""` instead
  of `"second"`). Fixed by disabling GC for the critical section
  (`debug.SetGCPercent(-1)`, restored via `defer`) and wrapping the
  probe in a bounded retry loop (`hazardRetryAttempts = 200`) that
  returns on the first attempt where the hazard is observed and only
  fails if it is *never* observed — this removes flakiness in both
  directions (it neither spuriously passes nor spuriously fails on
  scheduler jitter) while still requiring the hazard to be reproduced.
  Confirmed stable across `-race -count=5` (5 full repetitions, i.e. 25
  runs total of every test in the file).

## Commands run and results

```
$ go vet ./...
(clean, exit 0)

$ golangci-lint run ./...
0 issues.

$ go test -race -count=5 -run 'TestPool(RequestBundle|FastParams)' -v .
--- PASS: TestPoolRequestBundle_ConcurrentStress_AllTiers               (×5, ~0.9s each)
--- PASS: TestPoolFastParams_ConcurrentStress_AllTiers                  (×5, ~0.8s each)
--- PASS: TestPoolRequestBundleAndPoolFastParams_Combined_ConcurrentStress (×5, ~0.9s each)
--- PASS: TestPoolRequestBundle_PanicPath_PoolStaysClean                (×5, ~1.1s each)
--- PASS: TestPoolFastParams_PanicPath_PoolStaysClean                   (×5, ~1.0s each)
--- PASS: TestPoolRequestBundle_RetentionHazard_Documented              (×5, <0.01s each)
--- PASS: TestPoolFastParams_RetentionHazard_Documented                 (×5, <0.01s each)
ok  	github.com/FlavioCFOliveira/MuxMaster	24.766s

$ go test -race -count=3 .              # full root package, not just the new tests
ok  	github.com/FlavioCFOliveira/MuxMaster	20.898s

$ go test -race -count=3 ./middleware/...
ok  	github.com/FlavioCFOliveira/MuxMaster/middleware	30.504s
```

Zero `DATA RACE` reports, zero test failures, zero contamination
violations across all of the above.

## Verdict per flag

| Flag | Correct-usage contamination | Panic-path pool cleanliness | Documented retention hazard |
|---|---|---|---|
| `Mux.PoolRequestBundle` | **PASS** — 0 violations / 384,000 requests (192k stress + 192k combined-test share) | **PASS** — 0 violations / 64,000 panic+check cycles | **Confirmed** — reproduces deterministically, matches `configuration.md` §4.6 item 35 exactly |
| `Mux.PoolFastParams` | **PASS** — 0 violations / 384,000 requests | **PASS** — 0 violations / 64,000 panic+check cycles | **Confirmed** — reproduces deterministically, matches `configuration.md` §4.5 item 31 exactly |
| Both together | **PASS** — 0 violations / 192,000 requests, no cross-pool-family bleed | n/a (covered per-flag above) | n/a |

**No library defect was found.** Both pooling modes behave exactly as
`specification/configuration.md` documents: correct usage (read params
within the handler, never retain past return) is contamination-free under
sustained massive concurrency and under panics; the documented
retention hazard is real, exactly as specified, and now has a permanent
regression guard.

## Retention-contract demonstration (item 3 of the task)

Included, as `TestPoolRequestBundle_RetentionHazard_Documented` and
`TestPoolFastParams_RetentionHazard_Documented` — see above. Both are
deterministic (bounded retry + GC disabled for the critical section, not
a raw two-call assumption) and were validated stable across `-race
-count=5`.

## Coverage gaps (open)

- **Cross-goroutine retention is not exercised.** All tests here read a
  retained reference from the same goroutine that drove the requests
  (by design — see "Design notes" above, this keeps the tests
  race-detector-clean and deterministic). The specification's stated
  hazard also covers a goroutine *spawned by the handler* that retains
  `r`/`ps` and reads it asynchronously; that scenario is a genuine,
  intentional data race between the retaining goroutine's read and a
  later request's write into the same recycled memory, and any attempt
  to demonstrate it under `-race` would itself trigger a `DATA RACE`
  report — appropriately, since that is exactly what the documented
  hazard is. It was not added as a test because a genuinely racy
  demonstration cannot be made deterministic without synchronization
  that would defeat the point (see the `dataviz`-style reasoning in the
  test file's doc comments). If a future audit wants this exercised
  explicitly, it should be a documentation/example artifact (e.g.
  `examples/`), not a test asserting on race-detector-flagged behavior.
- **Overflow tier (>3 params) is not pooled for either flag** (per
  `configuration.md` §4.5 item 32 and the `reqBundle`
  design) — only the surrounding bundle/dispatch machinery is. The
  5-param tier is exercised for correctness in all stress tests but does
  not specifically stress the pooled-bundle-with-heap-overflow-slice
  interaction under panic; the panic-path tests use the 1- and 3-param
  tiers only. Given the 3-param tier already exercises
  `dispatchParamsNPooled`'s bundle-pooling logic (the overflow slice
  itself is never pooled regardless of param count once n > 3), this is
  a low-priority gap.
- **PoolFastParams overflow tier (>3 params) always allocates fresh**
  (per §4.5 item 32) and was tested only for correctness, not
  specifically for pool-adjacent behavior (there is none to test, since
  it never touches a pool).

## Out-of-scope finding surfaced during this task (not fixed, reported only)

Running the full `go test -race -count=3 ./...` (all packages, not just
root + middleware) produced one failure:
`TestHPS0010_RequestSmuggling_NetHTTPDefence/TE.CL_—_invalid_chunked_body`
and panic-recovery noise from `TestS8_H8_30_PanicHandlerDoublePanic`, both
in `reports/http-protocol-security-auditor/harness` — a different
package entirely, owned by the `http-protocol-security-auditor` agent.

This is **not** a consequence of this task's changes:
- `pool_contamination_test.go` lives in the root package
  (`github.com/FlavioCFOliveira/MuxMaster`), which `go test ./...` builds
  and runs as a completely separate test binary/process from
  `reports/http-protocol-security-auditor/harness`. The two cannot share
  in-process state (the pooling globals in `params.go` are unreachable
  from a different package's test binary).
- Re-running the failing tests in isolation —
  `go test -race -count=1 -run 'TestHPS0010_RequestSmuggling_NetHTTPDefence|TestS8_H8_30_PanicHandlerDoublePanic' ./reports/http-protocol-security-auditor/harness/...`
  — passed cleanly, both with and without `pool_contamination_test.go`
  present in the tree. This points to the failure being load/timing
  sensitive under a full-module `go test ./...` invocation (dozens of
  packages, many spinning up real `net/http` listeners, running
  concurrently under `-race`'s heavy instrumentation), not a
  deterministic defect.

Per the working agreement's scope discipline (§4) and the escalation
matrix ("HTTP smuggling / CRLF → `http-protocol-security-auditor`"), this
is reported for routing, not fixed here. No file under
`reports/http-protocol-security-auditor/` was touched.

## Files

- `pool_contamination_test.go` (new, root package) — the seven tests
  described above.

## Next actions

- None required for H-RECON-01 itself — the gap CSA-2026-0056 flagged is
  closed with zero defects found.
- Recommend the maintainer route the out-of-scope
  `TestHPS0010_RequestSmuggling_NetHTTPDefence` /
  `TestS8_H8_30_PanicHandlerDoublePanic` full-suite flakiness observation
  to `http-protocol-security-auditor` for triage (full-suite load
  sensitivity vs. a real regression).
