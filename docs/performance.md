# Performance

MuxMaster is a Go HTTP router built to add as little as possible to the standard `net/http` stack: zero allocations on static routes, one fused allocation on parameterised routes by default, and zero allocations with the opt-in pools. This document explains how the router achieves that, what the measurements show, and how to reproduce them.

For configuring the zero-allocation mode and its handler lifetime contract, see the **[Maximum Performance Guide](max-performance.md)**.

## Table of Contents

- [Design Goals](#design-goals)
- [How Allocations Are Minimised](#how-allocations-are-minimised)
- [Current Results (2026-09-26)](#current-results-2026-09-26)
- [Changes Since v1.1.0](#changes-since-v110)
- [Historical Results](#historical-results)
- [Running Benchmarks Locally](#running-benchmarks-locally)
- [What Affects Performance](#what-affects-performance)
- [Comparison Notes](#comparison-notes)

---

## Design Goals

1. **Zero allocations on static routes; one fused, size-class-aligned allocation on parameterised `Handle` routes** — 384, 416 or 480 B for 1, 2 or 3+ parameters. `HandleFast` routes allocate only the parameter slice (32, 64 or 96 B).
2. **Zero allocations on parameterised routes when the application accepts a stricter lifetime contract** — the opt-in `PoolRequestBundle` and `PoolFastParams` pools.
3. **Lock-free request dispatch** — no mutex on the steady-state request path.
4. **Strict `net/http` compatibility** — `*Mux` is an `http.Handler`; handlers keep the standard signature, and the default mode lets handlers retain `*http.Request` indefinitely.

---

## How Allocations Are Minimised

### Radix tree

Routes are stored in a radix (compressed prefix) tree, one per HTTP method. Lookup cost is O(k) in the path length, not O(n) in the number of routes. The trees are published through `treesPtr atomic.Pointer[methodTrees]`: requests load the pointer without a lock, and registration builds a copy-on-write replacement under a mutex. Registration copies only the nodes on the path being modified, so its cost grows with the depth of the new route, not the size of the tree.

When a static branch fails further down the path, the lookup backtracks to a parameter sibling. Backtracking is bounded: its cost grows linearly with path depth (see [What Affects Performance](#route-tree-shape-and-depth)).

### Stack-allocated parameter buffer

During lookup, path parameters are written into a fixed-size `paramsBuf` on the stack. A static route passes the original `*http.Request` straight to the handler, so it allocates nothing.

### Tiered request bundle

For a `Handle` route with parameters, MuxMaster copies `*http.Request` and fuses the copy with the parameter context (`requestCtx`) into one struct — the tiered `reqBundle`:

| Parameters | Bundle type  | Struct size | GC size class |
|------------|--------------|-------------|---------------|
| 1          | `reqBundle1` | 368 B       | 384 B         |
| 2          | `reqBundle2` | 400 B       | 416 B         |
| 3+         | `reqBundle`  | 456 B       | 480 B         |

More than three parameters add a separate overflow slice. Opt O12 (v1.1.0) removed a redundant `params Params` field from `requestCtx1` and `requestCtx2`; the slice is now derived from `small[:N]`, which moved `reqBundle1` from the 416 B to the 384 B size class and `reqBundle2` from 448 B to 416 B.

The copy's context is set with `setReqCtxUnsafe`, an `unsafe.Add` write at the reflected offset of the private `ctx` field of `http.Request`. This is safe because the bundle is not visible to any other goroutine until after the write, and the original `r` is never modified. If a future Go release renames or removes that field, the router detects it at start-up (`hasReqCtxField`) and falls back to `r.WithContext`, which costs a second allocation. An earlier design that wrote to the original `r` was rejected after the `concurrency-security-auditor` demonstrated a data race (CSA-001, recorded as MM-2026-0003 in `reports/overview/threat-model.md`).

A request whose internal context is `nil` — for example a struct literal passed to `ServeHTTP` in a test — is dispatched with `context.Background()` as the parent context.

### FastHandler routes

`HandleFast` passes the parameters as a third argument instead of through the request context, so it does not copy the request. By default it allocates only the `Params` slice (32–96 B for 1–3 parameters).

### Opt-in pools (Opt O13 / O9)

| Field | Recycles | Lifetime contract |
|---|---|---|
| `Mux.PoolRequestBundle` | The `reqBundle` of `Handle` routes (three tiers) | Handlers must not retain `*http.Request` after returning |
| `Mux.PoolFastParams` | The `Params` slice of `HandleFast` routes with 1–3 parameters | Handlers must not retain `ps` after returning |

Both default to `false`. A pooled bundle is zeroed (`*b = reqBundle1{}`) before it returns to the pool, so a later request never sees stale fields. Zeroing does not make a retained reference safe: a goroutine that keeps `r` sees a zeroed or reissued bundle. The pooled path is used only when `hasReqCtxField` is true. See the [Maximum Performance Guide](max-performance.md).

### Middleware applied at registration time

`Use` middleware is applied when a route is registered (`wrapMiddleware`), and the tree stores the wrapped handler. At request time the router makes one call; there is no chain to iterate. Consequently `Use` must be called before the routes it should wrap. `Pre` middleware is wrapped once around the dispatcher and runs on every request.

### Method dispatch via array index

`methodIdx` maps the method string to an array index with a `switch` over the ten supported methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY) plus the internal `"*"` token used by `Mount`; the tree root is then an array access. Unsupported methods are rejected at registration.

### Frozen configuration snapshot

On the first `ServeHTTP` call, the option fields (`RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS`, `CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode`, `PoolFastParams`, `PoolRequestBundle`) and the handler fields (`NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `ErrorHandler`, `PanicHandler`) are copied into a `muxConfig` snapshot. Requests read the snapshot through one atomic pointer load. `Rebuild()` discards it; the next request takes a new one.

### Cached error, OPTIONS and redirect handlers

The middleware-wrapped 405 and automatic-OPTIONS handlers are built once per `Allow` value and cached; the `Allow` string itself comes from a precomputed table indexed by a method bitmask. Redirects use a cached middleware-wrapped handler and read the `Use` middleware from an atomic snapshot, so no redirect takes a lock.

---

## Current Results (2026-09-26)

**Host and method:** AMD Ryzen 9 5900HX (8 cores / 16 threads), Linux 6.8, Go 1.27.0, `-count=3`, CPU governor `powersave`, 2026-09-26. With three samples, `benchstat` cannot run a significance test (it needs at least 4) or compute confidence intervals (at least 6). Treat differences of a few percent as noise. Raw data, exact commands and caveats: [`reports/perf-lab-2026-09-26-docs/`](../reports/perf-lab-2026-09-26-docs/README.md).

### Routing versus other routers

`competitor/` suite; ns/op, allocs/op in parentheses. "Pooled" is `PoolRequestBundle = true`; "Fast" is `HandleFast` without `PoolFastParams`.

| Route type        | MuxMaster default | MuxMaster Pooled | MuxMaster Fast | httprouter | bunrouter¹ | chi v5    | gorilla/mux |
|-------------------|-------------------|------------------|----------------|------------|------------|-----------|-------------|
| Static            | 29.6 (0)          | 30.7 (0)         | 30.6 (0)       | 34.7 (0)   | 168.1 (3)  | 217.9 (2) | 578.9 (7)   |
| 1 parameter       | 116.9 (1)         | 46.7 (0)         | 47.8 (1)       | 50.5 (1)   | 160.3 (3)  | 360.7 (4) | 954.7 (8)   |
| 2 parameters      | 131.9 (1)         | 60.9 (0)         | 67.8 (1)       | 60.2 (1)   | 181.6 (3)  | 405.0 (4) | 1 497 (8)   |
| 3 parameters      | 144.4 (1)         | 67.6 (0)         | 83.0 (1)       | 74.4 (1)   | 179.9 (3)  | 415.5 (4) | 1 729 (8)   |
| Catch-all         | 116.4 (1)         | 46.5 (0)         | 48.6 (1)       | 42.8 (1)   | 152.5 (3)  | 334.2 (4) | 1 611 (8)   |
| Not found         | 254.7 (3)         | —                | —              | 398.2 (3)  | 275.5 (4)  | 346.5 (5) | 1 034 (4)   |
| Parallel static   | 4.51 (0)          | —²               | 4.55 (0)       | 4.91 (0)   | 126.1 (3)  | 133.8 (2) | 345.6 (7)   |
| Parallel 1 param  | 102.3 (1)         | 7.19 (0)         | 17.2 (1)       | 21.8 (1)   | 126.1 (3)  | 232.3 (4) | 456.8 (8)   |

¹ bunrouter v1.0.23 through its `http.Handler` adapter (`bunrouter.HTTPHandlerFunc`), which stores parameters with `context.WithValue`. Its native `bunrouter.HandlerFunc` API is not `net/http`-compatible and was not measured in this run.
² Not measured. Static routes never allocate a request bundle, so pooling does not change them.

Bytes per operation for the parameterised cases: MuxMaster default 384/416/480 B, MuxMaster Fast 32/64/96 B, MuxMaster Pooled 0 B, httprouter 64/64/96 B (32 B for the catch-all).

### Root package (`bench_test.go`)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| StaticRoute | 28.63 | 0 | 0 |
| ParamRoute1 / 2 / 3 | 118.5 / 130.1 / 149.6 | 384 / 416 / 480 | 1 |
| WildcardRoute (catch-all) | 132.4 | 384 | 1 |
| NotFound | 208.0 | 91 | 3 |
| ParallelStaticRoute | 4.249 | 0 | 0 |
| ParallelParamRoute | 104.5 | 384 | 1 |
| FastStaticRoute | 28.91 | 0 | 0 |
| FastParamRoute1 / 2 / 3 | 46.15 / 68.96 / 79.72 | 32 / 64 / 96 | 1 |
| FastParallelParamRoute | 16.31 | 32 | 1 |
| PooledParamRoute1 / 2 / 3 | 48.51 / 59.76 / 61.76 | 0 | 0 |
| PooledWildcardRoute | 44.95 | 0 | 0 |
| PooledParallelParamRoute | 6.901 | 0 | 0 |
| Mount_Static / Mount_Param / Mount_TSRRedirect | 376.6 / 519.6 / 568.6 | 864 / 1 219 / 920 | 2 / 3 / 6 |
| ServeFiles / GroupServeFiles | 808.1 / 811.9 | 709 | 8 |
| RegisterRoutes N=100 / 1 000 / 5 000 (per route) | 561.7 / 693.1 / 939.6 ns | — | — |

`bench_test.go` has no benchmark for `PoolFastParams`.

### Middleware (`middleware/bench_test.go`)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| ThrottlePerIP, one client / many clients | 115.0 / 127.2 | 0 | 0 |
| Logger | 5 671 | 0 | 0 |
| Recoverer / RecovererWithLogger, no panic | 19.99 / 20.12 | 0 | 0 |
| Compress, 600 B / chunked 12 KiB | 316.6 / 7 765 | 32 / 58 | 2 / 3 |
| RealIP, 1 hop / 3 hops | 143.6 / 178.7 | 16 | 1 |
| APIKey, hit | 453.3 | 416 | 6 |
| BasicAuth hit, 1 / 10 / 100 users | 319.5 / 549.7 / 2 836 | 32 | 2 |
| BasicAuth miss, 1 / 10 / 100 users | 733.3 / 958.8 / 3 232 | 168 | 8 |
| JWTAuth, HS256 | 4 354 | 738 | 7 |
| OAuth2Introspect cache insert at saturation | 3 194 | 288 | 2 |

---

## Changes Since v1.1.0

### Hot path: no regression

The 18 routing benchmarks present in both v1.1.0 and HEAD were run on the same host with the same settings. **B/op and allocs/op are identical for all 18.** No ns/op delta is statistically significant at n=3.

In v1.1.0's `ParamRoute2` and `ParamRoute3`, two of the three samples read 1.5–1.7 µs, roughly ten times every other sample in either version, while the adjacent `ParamRoute1` was a steady ~119 ns. This is a host perturbation during that window (the CPU governor was `powersave`), not a property of v1.1.0; it also distorts the geometric-mean line of that comparison, which should be ignored. A few rows (`StaticRoute`, `WildcardRoute`, `FastParamRoute2`) read slightly higher at HEAD in all three samples; three samples cannot establish whether that is real. Full table: [report §1](../reports/perf-lab-2026-09-26-docs/README.md#1-root-package-v110-vs-head-hot-path-cases).

### Improvements re-verified on 2026-09-26

The sprint 18 waste-hunt campaign recorded before/after figures on 2026-09-24. All 18 claims that have a matching benchmark were re-run against HEAD on 2026-09-26 and reproduced; several now measure better than first recorded ([report §4](../reports/perf-lab-2026-09-26-docs/README.md#4-sprint-18-waste-hunt-campaign-claim-verification)).

| Change | Before (2026-09-24) | HEAD (2026-09-26) |
|---|---|---|
| Registration, O(depth) copy-on-write: 100 / 1 000 / 5 000 routes | 884 µs / 90.6 ms / 2.86 s | 56.2 µs / 693 µs / 4.70 ms |
| `Mount`, static prefix (shallow request copy) | 1 024 ns, 9 allocs | 237.5 ns, 2 allocs |
| `CleanPath`, dirty path (shallow request copy) | 810 ns, 7 allocs | 145.4 ns, 2 allocs |
| Trailing-slash redirect, no middleware | 884 ns | 641.3 ns, 10 allocs |
| Trailing-slash redirect, 5 middleware | 1 050 ns, 19 allocs | 760.7 ns, 11 allocs |
| `ThrottlePerIP`, one client | 4 051 ns, 5 allocs | 115.0 ns, 0 allocs |
| `Logger` | 7.5 µs, 5 allocs | 5.67 µs, 0 allocs |
| `Compress`, 600 B / chunked 12 KiB | 435 ns, 4 allocs / 14.6 µs, 16 allocs | 316.6 ns, 2 allocs / 7.77 µs, 3 allocs |
| `JWTAuth`, HS256 | 5.16 µs, 10 allocs | 4.35 µs, 7 allocs |
| `RealIP`, 1 hop / 3 hops | 195 ns / 257 ns, 2 allocs (3 hops) | 143.6 ns, 1 alloc / 178.7 ns, 1 alloc |
| `APIKey`, hit | 574 ns, 7 allocs | 453.3 ns, 6 allocs |
| 405 Method Not Allowed | 153 ns, 2 allocs | 109.6 ns, 1 alloc |
| Automatic OPTIONS | 124 ns, 3 allocs | 61.9 ns, 1 alloc |
| `Text` response helper | 98 ns, 2 allocs | 60.4 ns, 1 alloc |
| `JSON` response helper | 500 ns, 3 allocs | 514.1 ns, 3 allocs (unchanged within noise) |

`StripSlashes` shares the shallow-copy change but has no benchmark; `ServeFiles` has no recorded before/after pair.

### Improvements measured on 2026-09-24, not re-measured on 2026-09-26

The sprint 18 contention hunt measured scaling with `-cpu 1,4,16` ([`reports/perf-lab-2026-09-24/contention-hunt.md`](../reports/perf-lab-2026-09-24/contention-hunt.md)). The 2026-09-26 run did not repeat these benchmarks.

| Change | Before | After |
|---|---|---|
| `ThrottlePerIP` table sharded 64 ways, many clients, 16 CPUs | 2 114 ns | 451 ns (4.68×); no longer slows down above 4 CPUs |
| `ThrottleBacklog` lock-free CAS fast path, 1 CPU / 16 CPUs | 39.12 ns / 76.15 ns | 22.76 ns (−42%) / 66.23 ns (−13%) |
| `RequestID` batched `crypto/rand` and fused allocation | 7 allocs; 1 092 / 291.4 / 253.1 ns at 1 / 4 / 16 CPUs | 2 allocs; 231.5 / 102.0 / 131.6 ns (~4.7× / 2.8× / 1.95×) |
| `OAuth2Introspect` eviction at a full cache (min-heap) | 247.7 / 257.2 / 257.4 µs at 1 / 4 / 16 CPUs | 3.27 / 1.53 / 2.15 µs |
| Redirects read the `Use` middleware snapshot lock-free | `RWMutex` read lock per redirect | no lock; no measurable ns/op change |

### Costs added by security fixes

| Fix | Effect | Source |
|---|---|---|
| `BasicAuth` scans all users in constant time (TSC-2026-0002, rmp #290) | Cost grows with the number of users: hit ≈ 320 ns / 550 ns / 2.8 µs, miss ≈ 730 ns / 960 ns / 3.2 µs for 1 / 10 / 100 users | 2026-09-26 run |
| `CORS` adds `Vary: Origin` to every response (TM-2026-033, rmp #291) | Request without `Origin`: 25.7 ns, 0 allocs → 71.0 ns, 1 alloc (112 B); other paths unchanged | `-count=10` when the fix landed (CHANGELOG) |
| `Recoverer` tracks whether the response has started (O-14, rmp #276) | No-panic path: 8.4 ns → 19.4 ns, 0 allocs before and after | `-count=10` when the fix landed (CHANGELOG) |

### High-concurrency scaling (2026-09-24)

`b.RunParallel` with `-cpu 1,4,16`, `-count=6`, Go 1.27.0, same host ([contention hunt](../reports/perf-lab-2026-09-24/contention-hunt.md)):

| Benchmark | cpu=1 | cpu=4 | cpu=16 |
|---|---:|---:|---:|
| 1 parameter, `Handle` default (1 alloc) | 143.2 ns | 94.7 ns | 104.4 ns |
| 1 parameter, `Handle` + `PoolRequestBundle` (0 allocs) | 41.5 ns | 11.0 ns | 7.3 ns |
| 1 parameter, `HandleFast` default (1 alloc) | 49.0 ns | 13.2 ns | 16.0 ns |
| 1 parameter, `HandleFast` + `PoolFastParams` (0 allocs) | 39.3 ns | 10.3 ns | 5.0 ns |

The allocating variants stop improving, and slightly regress, beyond 4 CPUs because of allocator and GC contention; the pooled variants keep scaling. This is the most recent measurement of `PoolFastParams`; the 2026-09-26 run has no pooled fast-route benchmark.

---

## Historical Results

The following figures were measured on v1.1.0-era code and have not been repeated on current code. They remain useful for comparing hardware, not for describing HEAD.

### AMD Ryzen 9 5900HX, Go 1.26.2, 2026-05-08

Source: [`reports/overview/2026-05-08-perf-validation.md`](../reports/overview/2026-05-08-perf-validation.md) and the v1.1.0 CHANGELOG entry.

| Route type   | `Handle` default | `Handle` + Pool | `HandleFast` | httprouter |
|--------------|------------------|-----------------|--------------|------------|
| Static       | 25 ns, 0 allocs  | 25 ns, 0 allocs | 25 ns, 0 allocs | 33.8 ns, 0 allocs |
| 1 parameter  | 105 ns, 1 alloc  | 45 ns, 0 allocs | 50 ns, 1 alloc | 56.4 ns, 1 alloc |
| 2 parameters | 119 ns, 1 alloc  | 57 ns, 0 allocs | 68 ns, 1 alloc | 66.5 ns, 1 alloc |
| 3 parameters | 135 ns, 1 alloc  | 59 ns, 0 allocs | 77 ns, 1 alloc | 78.4 ns, 1 alloc |
| Catch-all    | 108 ns, 1 alloc  | 44 ns, 0 allocs | 50 ns, 1 alloc | 51.3 ns, 1 alloc |

A sustained-load test on the same host (four middleware, 1 000 concurrent goroutines, 30 s) reached 67 275 requests per second with no errors and a maximum GC pause of 2.95 ms ([`reports/dos-resilience-tester/2026-05-08-production-loadtest.md`](../reports/dos-resilience-tester/2026-05-08-production-loadtest.md)).

### Raspberry Pi 5 (ARM64 Cortex-A76, 4 cores), Go 1.26.3, 2026-05-12

Source: [`reports/rpi5-benchmarks-2026-05-12.md`](../reports/rpi5-benchmarks-2026-05-12.md).

| Route type   | `Handle` default | `Handle` + Pool | `HandleFast` |
|--------------|------------------|-----------------|--------------|
| Static       | 52 ns, 0 allocs  | 52 ns, 0 allocs | 52 ns, 0 allocs |
| 1 parameter  | 287 ns, 1 alloc  | 99 ns, 0 allocs | 149 ns, 1 alloc |
| 2 parameters | 335 ns, 1 alloc  | 128 ns, 0 allocs | 218 ns, 1 alloc |
| 3 parameters | 351 ns, 1 alloc  | 138 ns, 0 allocs | 242 ns, 1 alloc |
| Parallel 1 parameter (4 CPUs) | 184 ns, 1 alloc | 25 ns, 0 allocs | 45 ns, 1 alloc |

### Apple M4 (10 cores), Go 1.26.2, 2026-05-12

Source: [`reports/apple-m4-benchmarks-2026-05-12.md`](../reports/apple-m4-benchmarks-2026-05-12.md). bunrouter was measured through its **native** API here, unlike the 2026-09-26 run.

| Route type   | `Handle` default | `Handle` + Pool | `HandleFast` | httprouter | bunrouter (native) |
|--------------|------------------|-----------------|--------------|------------|--------------------|
| Static       | 14 ns, 0 allocs  | 14 ns, 0 allocs | 14 ns, 0 allocs | 14.7 ns, 0 allocs | 18.6 ns, 0 allocs |
| 1 parameter  | 57 ns, 1 alloc   | 28 ns, 0 allocs | 28 ns, 1 alloc | 33.0 ns, 1 alloc | 21.9 ns, 0 allocs |
| 2 parameters | 64 ns, 1 alloc   | 36 ns, 0 allocs | 37 ns, 1 alloc | 39.6 ns, 1 alloc | 40.7 ns, 0 allocs |
| 3 parameters | 70 ns, 1 alloc   | 39 ns, 0 allocs | 46 ns, 1 alloc | 45.1 ns, 1 alloc | 29.3 ns, 0 allocs |
| Catch-all    | 58 ns, 1 alloc   | 29 ns, 0 allocs | 28 ns, 1 alloc | 27.3 ns, 1 alloc | 11.5 ns, 0 allocs |
| Parallel static | 1.86 ns, 0 allocs | 1.86 ns, 0 allocs | — | 2.35 ns, 0 allocs | 2.12 ns, 0 allocs |
| Parallel 1 parameter | 112 ns, 1 alloc | 10.2 ns, 0 allocs | — | 18.9 ns, 1 alloc | 3.97 ns, 0 allocs |

Allocation counts were identical on all three platforms.

---

## Running Benchmarks Locally

```bash
# Root and middleware packages (excludes reports/ and competitor/)
make bench

# The same, three samples, for benchstat
go test -run='^$' -bench=. -benchmem -count=3 . ./middleware/ | tee results.txt
benchstat results.txt

# Competitor suite (separate module with vendored dependencies)
cd competitor && go test -mod=mod -run='^$' -bench=. -benchmem -count=3 .
```

`go test -bench=. ./...` from the repository root also runs the audit harnesses under `reports/`, which belong to the same module.

To compare before and after a change, use at least `-count=6` so `benchstat` can report confidence intervals, and pin the CPU governor to `performance` if you can:

```bash
go test -run='^$' -bench=. -benchmem -count=10 . > before.txt
# make your change
go test -run='^$' -bench=. -benchmem -count=10 . > after.txt
benchstat before.txt after.txt
```

---

## What Affects Performance

### Number of path parameters

In the default mode each extra parameter moves the request into a larger bundle tier and adds lookup work: 1 → 3 parameters measured 118.5 → 149.6 ns (384 → 480 B). With `PoolRequestBundle` the same range is 48.51 → 61.76 ns with no allocation.

### Regex-constrained parameters

A regex parameter is compiled at registration and evaluated against the candidate segment during lookup. Its cost depends on the expression; the benchmark suites contain no regex-route benchmark, so measure your own patterns.

### Middleware

`Use` middleware adds no routing overhead because it is applied at registration; each request pays only for what the middleware itself does. The [middleware table](#middleware-middlewarebench_testgo) shows those costs, from about 20 ns (`Recoverer`, no panic) to several microseconds (`Logger`, `JWTAuth`).

### Route tree shape and depth

Lookup cost depends on the length of the path and on how much backtracking the tree forces, not on the total number of routes. In the adversarial benchmarks, cost and allocations grow linearly with depth: `AdversarialBacktracking` goes from 165 ns at depth 1 to 15.55 µs at depth 128, and `QuadraticBacktracking` from 197.4 ns at depth 2 to 8.77 µs at depth 64.

### Registration

Registration copies only the nodes along the new route's path. Measured per route: 561.7 ns with 100 routes, 693.1 ns with 1 000, 939.6 ns with 5 000.

---

## Comparison Notes

All figures in this section are from the [2026-09-26 run](#routing-versus-other-routers).

### vs httprouter

httprouter is the usual performance reference. MuxMaster is faster on static routes (29.6 vs 34.7 ns) and on not-found (254.7 vs 398.2 ns). In the default mode, MuxMaster's parameterised routes are about 2–2.7× slower serially and 4.7× slower on the parallel benchmark, because it copies `*http.Request` into a 384–480 B bundle so handlers keep the standard signature and may retain `r`; httprouter allocates only its `Params` slice and uses a three-argument handler.

With `PoolRequestBundle`, MuxMaster allocates nothing on parameterised routes and is faster than httprouter on 1 parameter (46.7 vs 50.5 ns), 3 parameters (67.6 vs 74.4 ns) and the parallel parameter benchmark (7.19 vs 21.8 ns), level on 2 parameters (60.9 vs 60.2 ns), and slower on catch-all (46.5 vs 42.8 ns). `HandleFast` without pooling is faster on 1 parameter and the parallel benchmark, and slower on 2 and 3 parameters and catch-all.

### vs bunrouter

The suite measures bunrouter through its `http.Handler` adapter, which stores parameters with `context.WithValue`; MuxMaster's default mode is faster in every case (for example 116.9 vs 160.3 ns on 1 parameter). bunrouter's native API allocates nothing; on the 2026-05-12 Apple M4 run it was faster than pooled MuxMaster on 1 and 3 parameters, catch-all and the parallel benchmark, and slower on static and 2-parameter routes. Its native handlers do not use the `net/http` signature.

### vs chi

chi v5 allocates on every route type, including static routes. MuxMaster's default mode is about 3× faster on parameterised routes (116.9 vs 360.7 ns on 1 parameter) and about 7× faster on static routes.

### vs gorilla/mux

gorilla/mux matches with regular expressions and allocates 7–8 times per matched request. MuxMaster's default mode is about 4× faster on not-found, 8–14× faster on parameterised routes and about 20× faster on static routes. See the [Migration Guide](migration.md).

---

## See Also

- [Maximum Performance Guide](max-performance.md) — the zero-allocation configuration and its lifetime contract
- [Migration Guide](migration.md) — replacing httprouter, chi, or gorilla/mux
- [Routing](routing.md) — how the radix tree resolves patterns
- [SECURITY.md](../SECURITY.md) — the concurrency analysis behind the lifetime contracts
