# rmp #271 — `go test -race -count=3 ./...` timeout in the CSA harness

Date: 2026-09-25   Commit: `2630763` (branch `feature/20-backlog-clearance`)
Go: go1.27.0 linux/amd64   GOMAXPROCS: 16 (AMD/Intel x86_64, 16 logical CPUs, Ubuntu 6.8)

## Defect

`go test -race -count=3 ./...` from the repo root failed 3/3: the package
`github.com/FlavioCFOliveira/MuxMaster/reports/concurrency-security-auditor/harness`
(and its two sub-packages `.../harness/2026-05-08-S10-PreCSA` and
`.../harness/mwharness` — all three carry the `//go:build race` tag, which Go
sets automatically whenever `-race` is passed, so they are always part of a
root `-race ./...` run) hit Go's default per-binary `-test.timeout=10m0s`
under `-count=3` full-suite contention.

## Root cause (measured, not guessed)

Isolated baseline (`go test -race -count=1 -v ./reports/concurrency-security-auditor/harness/...`,
this machine, no other load): the main `harness` package alone took
**319.8s** for a single pass — already using more than half the 600s budget
for `-count=1`; `-count=3` triples that to ~960s, and `-count=10` (task step 3)
would need ~3,200s. Both exceed the fixed 600s-per-binary timeout regardless
of contention from other packages.

Per-test timing (`--- PASS: Test (Ns)`) attributed the cost to four
independent, unbounded-growth anti-patterns, not to "the suite has a lot of
iterations":

1. **`runtime.GC()` forced on every request inside a hot handler.**
   `TestPool_NoCrossRequestLeak`, `TestPool_GCPressure_Canary`
   (`h023_pool_canary_test.go`) and `TestS8_PoolCanary_DeadBeef`
   (`s8_hypotheses_test.go`) called `runtime.GC()` — a synchronous,
   effectively stop-the-world cycle — on **every** one of up to
   1.28M canary-route hits (`n*8*10000` at GOMAXPROCS=16). Concurrently,
   ~128 goroutines each trying to force a GC serializes the entire
   stress fan-out. These three tests alone accounted for ~455s of a
   single 320s isolated pass (they overlap under `t.Parallel()`, so this is
   not purely additive, but each was individually 100–224s).

2. **`Use()`/`Pre()` called in a hot loop, growing the middleware chain
   without bound.** `Mux.Use` and `Mux.Pre` (`mux.go`) both `append` to a
   slice that every subsequent dispatch is re-wrapped with. Six tests
   (`TestH9_02_Pre_Rebuild_AtomicPtr_Race`,
   `TestH9_09_LazyNotFound_UseRace_PostFix_Regression`,
   `TestUse_LazyNotFound_Race`, `TestUse_LazyMethodNotAllowed_Race`,
   `TestUse_LazyOPTIONS_Race`, `TestH9_12_Use_Handle_Concurrent_Lock_Correctness`)
   called `Use()`/`Pre()` 500–80,000 times *inside* the stress loop, growing
   the pre-dispatch or middleware chain to thousands of layers deep by the
   end of the run — every concurrent `ServeHTTP` call from that point on paid
   for however deep the chain had grown. This was the single largest cost
   category: `TestH9_09` alone was 221s, `TestH9_02` was 195s,
   `TestUse_LazyMethodNotAllowed_Race` 111s, `TestUse_LazyOPTIONS_Race` 103s.
   A pre-existing, already-correct exemplar in the same file
   (`TestS8_LazyBuilders_AllThree_UseAndRebuild`, ~300 `Use()` calls total,
   GOMAXPROCS-independent, ~24s) shows the intended, bounded shape of this
   kind of test.

3. **Contended synchronous log I/O inside a panic-heavy stress loop.**
   `mw.Recoverer()` (`middleware/recoverer.go`) logs every recovered panic
   via `slog.Default()`, including a full `debug.Stack()` capture, by
   design (MSR-2026-0057: sanitised panic logging, not a defect). Under
   tens of thousands of *concurrent* panics
   (`TestRecovererMiddleware_WithPanic_Race`, `TestRecovererAndTimeout_H_C`,
   `TestH8_05_Compress_Recoverer_PanicMidWrite`), the shared stderr writer
   became a largely GOMAXPROCS-insensitive bottleneck: halving or
   quartering the iteration count did not proportionally cut wall time.
   Separately, `TestH8_30_PanicHandler_Panics_IsContained` drives a real
   `httptest.Server` where every request double-panics; `net/http`'s own
   built-in recovery *also* logs "http: panic serving ..." plus a stack
   trace to stderr on every one of those, with the same effect.

4. **Expensive reflection-based introspection called in a tight loop.**
   `Mux.Routes()` (`introspection.go`) calls `reflect.ValueOf` +
   `runtime.FuncForPC` per registered route to resolve a human-readable
   handler name. `TestWalk_VsConcurrentServe`, `TestIntrospection_RouteSnapshot`
   and `TestH8_28_WalkFast_VsConcurrentHandle` called `Routes()`/`Walk()`/
   `WalkFast()` thousands of times per goroutine, each call walking and
   copying the full route table — legitimate, documented behaviour, not a
   defect, but far more expensive per call than a plain `ServeHTTP`.

None of (1)–(4) are library defects: `runtime.GC()` behaves as documented,
`Use()`/`Pre()` are correctly append-only registration APIs never intended to
be called thousands of times per process lifetime, `Recoverer`'s logging is
an intentional security feature (MSR-2026-0057), and `Routes()`'s reflection
cost is inherent to producing human-readable handler names. **No library code
was changed.**

## Fix

All changes are confined to `reports/concurrency-security-auditor/harness/*_test.go`
(10 files, 338 insertions / 60 deletions; `go build/vet -tags=race` and
`gofmt -l` clean). Applied in three passes, remeasuring after each:

- **Bound forced-GC frequency, don't remove it.** Replaced `runtime.GC()` on
  every request with `runtime.GC()` every Nth request via a per-test atomic
  counter (N chosen so each test still forces 75–320+ real, synchronous GC
  cycles interleaved against thousands of concurrently in-flight requests —
  the exact property these canaries exist to check).
- **Bound `Use()`/`Pre()` call counts, independent of `GOMAXPROCS`.** Where a
  test spawns `n` mutator goroutines (`n := runtime.GOMAXPROCS(0)`), the
  per-goroutine call count is now `max(5, totalBudget/n)` with
  `totalBudget` = 300, matching the budget already validated safe and fast
  (~24s) by `TestS8_LazyBuilders_AllThree_UseAndRebuild`. Single-goroutine
  mutators got a flat bound (300, then 400 for the two heaviest). This still
  races the chain-invalidation path against concurrent `ServeHTTP` hundreds
  of times per run — real production `Use()`/`Pre()` call volume across a
  process lifetime is orders of magnitude below even the reduced bound.
- **Redirect contended log output for panic-heavy stress tests, not just cut
  iterations.** Added `discardRecoverer()` (`panic_pool_cleanliness_test.go`)
  — `mw.RecovererWithLogger` pointed at `io.Discard` instead of
  `slog.Default()` — used wherever a test intentionally triggers many
  concurrent panics. `debug.Stack()` still runs on every panic (the same
  recover()/dispatch code path under test is unchanged); only the contended
  stderr write is removed. Likewise, `TestH8_30` now builds its
  `httptest.Server` via `NewUnstartedServer` + `Config.ErrorLog = log.New(io.Discard, "", 0)`
  before `Start()`, so `net/http`'s own panic-recovery logging doesn't
  serialize the test either.
- **Proportional iteration-count reduction everywhere else**, applied in two
  rounds (guided by remeasurement, not a blanket single pass): raw
  `ServeHTTP`-volume loops, real-crypto loops (JWT HMAC verification),
  real-panic loops, and `Routes()`/`Walk()`/`WalkFast()` loops were each cut
  by roughly 2–8x depending on their per-operation cost, informed by the
  ~14–28µs/request baseline measured on this environment (via
  `TestPool_MultiTier_Isolation`, the cleanest pure-volume test with neither
  GC-forcing nor chain growth).
- Two genuinely real-time-bound tests (`TestH9_10_ContextPropagation_ReqBundle_Done`,
  which asserts real `context.WithTimeout` cancellation behaviour, and
  `TestH9_05_ThrottlePerIP_NoSendOnClosedChannel`, which holds a real rate-limit
  slot for `time.Sleep(2ms)`) were identified as *not* CPU-bound anti-patterns —
  their cost is proportional to real wall-clock waits, not iteration count —
  and were only lightly trimmed, preserving the specific timing behaviour
  under test.

No `-timeout` flag was added anywhere, and no `t.Skip` was introduced.
`testing.Short()` was not adopted as a gate: the full (non-`-short`) run is
the one the CI/local mandate cares about, so the default iteration counts
themselves had to be bounded, per the task's requirement.

## Canary/race strength — before vs after

| Test | Iterations (before) | Iterations (after) | Goroutines | Forced GC cycles (before → after) |
|---|---|---|---|---|
| `TestPool_NoCrossRequestLeak` | 10,000/goroutine (n·8 = 1.28M canary hits) | 2,500/goroutine (n·8 = 320K canary hits) | n·8 = 128 | every hit (1.28M) → every 4,000th (~80) |
| `TestPool_GCPressure_Canary` | 2,000/goroutine | 2,000/goroutine (unchanged) | n·4 = 64 | every hit (128K) → every 50th (~2,560) |
| `TestS8_PoolCanary_DeadBeef` | 10,000/goroutine (n·8 = 1.28M canary hits) | 2,500/goroutine (n·8 = 320K canary hits) | n·8 = 128 | every hit (1.28M) → every 4,000th (~80) |
| `TestH9_09` Use() mutator | 5,000/goroutine × n = 80,000 total | `max(5, 300/n)` ≈ 18/goroutine × n ≈ 300 total | n = 16 | n/a |
| `TestH9_02` Pre() mutator | 500/goroutine × n = 8,000 total | `max(5, 300/n)` ≈ 18/goroutine × n ≈ 300 total | n = 16 | n/a |
| `TestUse_Lazy{NotFound,MethodNotAllowed,OPTIONS}_Race` Use() mutator | 5,000 (single goroutine) | 300 (single goroutine) | 1 | n/a |
| `TestRecovererMiddleware_WithPanic_Race` | 5,000/goroutine (n·4 = 320K panics) | 400/goroutine (n·4 = 25,600 panics) | n·4 = 64 | n/a |
| Package total, isolated `-count=1` | 319.8s | 51.3s (typical; 51–85s observed range) | — | — |

The floor chosen throughout (≥300 total mutation events for chain-growth
tests, ≥75 forced GC cycles for GC-canary tests, ≥25,000 total requests for
pure-volume tests) is well above what any single interleaving needs to
expose a memory-model violation empirically: every one of these tests
previously caught a real, documented finding (e.g. MM-2026-0048,
MM-2026-0049) at iteration counts far below even the reduced values here.

## Verification

1. **Package alone, `-race -count=10`, isolated** (no other load):
   ```
   ok  .../harness                        541.860s
   ok  .../harness/2026-05-08-S10-PreCSA   245.749s
   ok  .../harness/mwharness               386.388s
   ```
   PASS. Zero `DATA RACE`, zero panics. `harness` fits in 541.9s of the
   600s-per-binary budget (58.1s margin).

2. **Three consecutive full-suite runs from the repo root**,
   `go test -race -count=3 ./...` (14 packages under `-race`, default
   `-timeout`):

   | Run | Wall time | Result |
   |---|---|---|
   | 1 | 188.4s | PASS — all 14 packages `ok`, zero FAIL/panic/DATA RACE |
   | 2 | 184.2s | PASS — all 14 packages `ok`, zero FAIL/panic/DATA RACE |
   | 3 | 182.1s | PASS — all 14 packages `ok`, zero FAIL/panic/DATA RACE |

   No other package failed or came close to its timeout in any of the three
   runs. (`go test`'s per-binary `-test.timeout=10m0s` applies independently
   to each package, confirmed via `ps` inspection of the running test
   binaries' argv during the original failing repro.)

## Files changed

```
reports/concurrency-security-auditor/harness/h001_r_ctx_goroutine_race_test.go
reports/concurrency-security-auditor/harness/h023_pool_canary_test.go
reports/concurrency-security-auditor/harness/h027_introspection_race_test.go
reports/concurrency-security-auditor/harness/handlefast_panic_test.go
reports/concurrency-security-auditor/harness/panic_pool_cleanliness_test.go
reports/concurrency-security-auditor/harness/public_fields_race_test.go
reports/concurrency-security-auditor/harness/rebuild_race_test.go
reports/concurrency-security-auditor/harness/s8_hypotheses_test.go
reports/concurrency-security-auditor/harness/s9_hypotheses_test.go
reports/concurrency-security-auditor/harness/tiered_dispatch_race_test.go
```

10 files, 338 insertions(+), 60 deletions(-). No `mux.go`, `tree.go`,
`params.go`, or `middleware/*.go` changes — this was purely a test-harness
scaling defect, not a library defect.

## Not committed

Per instruction, these changes were left uncommitted in the working tree for
the user/CI to review and commit via the `gitflow` skill.
