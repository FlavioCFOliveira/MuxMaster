# Performance

MuxMaster is designed to add negligible overhead to the standard `net/http` stack. This document explains the design decisions behind its performance, how to measure it, and how it compares to other Go HTTP routers.

For a hands-on guide to configuring MuxMaster for maximum throughput and **zero per-request allocations** on the routing layer, see **[Maximum Performance Guide](max-performance.md)**.

## Table of Contents

- [Design Goals](#design-goals)
- [How Allocations Are Minimised](#how-allocations-are-minimised)
- [Benchmarks](#benchmarks)
- [Zero-allocation opt-ins](#zero-allocation-opt-ins)
- [Running Benchmarks Locally](#running-benchmarks-locally)
- [What Affects Performance](#what-affects-performance)
- [Comparison Notes](#comparison-notes)

---

## Design Goals

1. **Zero allocations for static routes; one fused tiered allocation for parameterised routes (`Handle`)** — the parameter bundle is sized to match the GC size class (416 / 448 / 480 B for 1 / 2 / 3 parameters), and `HandleFast` further reduces the allocation footprint to 32–96 B for the same parameter counts.
2. **Sub-microsecond dispatch** — route lookup completes in tens of nanoseconds, not hundreds.
3. **Linear scalability** — throughput per core scales linearly with the number of CPUs (~4 200 RPS per vCPU on a 16-core box at 1 000 concurrent goroutines).
4. **Strict `net/http` compatibility** — no fasthttp; no breaking surface; the fused allocation is the safest design that avoids the race conditions detected on the previous experimental zero-alloc approach (CSA-001).

---

## How Allocations Are Minimised

MuxMaster delivers **zero allocations on static routes** and a **single tiered allocation on parameterised routes** in the `Handle` path. The `HandleFast` path uses a smaller exact-sized allocation. Both paths achieve O(k) lookup and lock-free reads through the same set of techniques.

### Radix tree

Routes are stored in a radix (compressed prefix) tree — one tree per HTTP method. Lookup is O(k) in the path length, not O(n) in the number of routes. The tree is built at startup and never mutated during request processing, so no locks are needed on the read path. The active method-trees pointer is loaded lock-free via `treesPtr atomic.Pointer[methodTrees]`; registration uses a copy-on-write swap under a writer mutex.

### Stack-allocated parameter buffer

During tree traversal, path parameters are written into a fixed-size `paramsBuf` struct allocated on the stack inside `getValue`. There is no `sync.Pool` — the buffer never escapes the goroutine, so the GC never sees it. For static routes (no parameters), nothing further is allocated and the static-route allocation count is zero.

### Tiered request bundle

For routes with parameters, MuxMaster fuses the request context and the copy of `*http.Request` into a single GC-class-aligned struct — the tiered `reqBundle`:

| Parameters | Bundle type   | Size  | GC size class |
|------------|---------------|-------|---------------|
| 1          | `reqBundle1`  | 368 B | 384 B         |
| 2          | `reqBundle2`  | 400 B | 416 B         |
| 3+         | `reqBundle`   | 456 B | 480 B         |

Each tier is sized to the exact GC bucket so there is no internal fragmentation. Opt O12 (May 2026) trimmed `requestCtx1` and `requestCtx2` by dropping the redundant `params Params` field — the slice header is now derived from `small[:N]` on access, dropping `reqBundle1` from 416 B to 384 B and `reqBundle2` from 448 B to 416 B (next-smaller GC class).

The bundle's request-context field is set via `setReqCtxUnsafe` (an `unsafe.Add` over the reflected offset of the private `ctx` field of `http.Request`). This is safe because the bundle is freshly allocated and is not visible to any other goroutine until after the write; the original `r` is never mutated. If a future Go release moves the `ctx` field, the router automatically falls back to a 2-allocation `r.WithContext(ctx)` path through a runtime-detected `hasReqCtxField` flag — the previous, unsafe approach of mutating the original `r` was rejected after the `concurrency-security-auditor` confirmed CSA-001 race conditions.

For the `HandleFast` path (`FastHandler`), the allocation is even smaller: a 32–96 B exact-sized `Params` slice bounded by `maxParams = 3`.

### Opt-in pools (Opt O9 / O13) — zero allocations on the hot path

MuxMaster offers two opt-in `sync.Pool` tiers that recycle the per-request objects so the param-route path also drops to **zero allocations**. They are opt-in because they tighten the handler lifetime contract: handlers must not retain `*http.Request` (for `PoolRequestBundle`) or the `Params` slice (for `PoolFastParams`) past return.

| Field | Recycles | Effect on 1-param route |
|---|---|---|
| `Mux.PoolFastParams` | `Params` slice for `HandleFast` | 50 ns / 32 B / 1 alloc → 44 ns / 0 B / 0 allocs |
| `Mux.PoolRequestBundle` | `reqBundle1/2/3` for `Handle` | 105 ns / 384 B / 1 alloc → 45 ns / 0 B / 0 allocs |

See [Maximum Performance Guide](max-performance.md) for the lifetime contract, audit checklist, and worked recipes.

### Middleware applied at registration time

Middleware is applied at **route registration** time via `wrapMiddleware`, not at request dispatch time. The router stores the fully-wrapped handler directly. At request time, the router calls a single function pointer — there is no middleware chain to iterate. This means `Use` must be called before the routes it should wrap.

### Method dispatch via array index

Standard HTTP methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE) are mapped to array indices at compile time. Method dispatch during a request is an array access — O(1) and branch-free.

### Frozen configuration snapshot

On the first `ServeHTTP` call, the Mux flags (`RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS`, `CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode`) are frozen into a `muxConfig` snapshot. Subsequent requests load the snapshot via a single atomic pointer instead of reading 6–8 struct fields. Tests that need to change Mux flags after first use call `Mux.Rebuild()` to reset the snapshot.

---

## Benchmarks

Measured on AMD Ryzen 9 5900HX (16 logical cores), Linux 6.8, Go 1.26.2. Numbers are medians over `count=10` runs against the same route set, captured immediately before the v1.0.0 release. The full evidence is archived under `reports/overview/2026-05-08-perf-validation.md` and `reports/overview/2026-05-08-final-maturity-verdict.md`.

### Serial (single goroutine)

| Route type   | `Handle` (default) | `Handle` + Pool¹ | `HandleFast` | httprouter |
|--------------|--------------------|------------------|--------------|------------|
| Static       | **25 ns, 0 allocs** | 25 ns, 0 allocs | 25 ns, 0 allocs | 33.8 ns, 0 allocs |
| 1 parameter  | 105 ns, 1 alloc    | **45 ns, 0 allocs** | 50 ns, 1 alloc | 56.4 ns, 1 alloc |
| 2 parameters | 119 ns, 1 alloc    | **57 ns, 0 allocs** | 68 ns, 1 alloc | 66.5 ns, 1 alloc |
| 3 parameters | 135 ns, 1 alloc    | **59 ns, 0 allocs** | 77 ns, 1 alloc | 78.4 ns, 1 alloc |
| Catch-all    | 108 ns, 1 alloc    | **44 ns, 0 allocs** | 50 ns, 1 alloc | 51.3 ns, 1 alloc |

¹ Opt-in: `Mux.PoolRequestBundle = true`. Handlers MUST NOT retain `*http.Request` past return — see [Maximum Performance Guide](max-performance.md).

### Parallel (GOMAXPROCS cores)

| Route type   | `Handle` (default) | `Handle` + Pool   | `HandleFast`     | httprouter       |
|--------------|--------------------|-------------------|------------------|------------------|
| Static       | **3.6 ns, 0 allocs** | 3.6 ns, 0 allocs | 3.6 ns, 0 allocs | 4.9 ns, 0 allocs |
| 1 parameter  | 100 ns, 1 alloc    | **6.3 ns, 0 allocs** | 17 ns, 1 alloc | 22.2 ns, 1 alloc |

The parallel static benchmark shows near-linear CPU scaling on the 16-core box: 25 ns serial → 3.6 ns parallel (~7× speed-up). With `PoolRequestBundle = true` the parallel 1-param case drops to **6.3 ns**, beating `httprouter`'s 22.2 ns by **3.5×** on the same hardware. Sustained-load testing with a four-middleware stack and 1 000 concurrent goroutines reaches **67 275 RPS at 0.00 % error rate** with a maximum GC pause of 2.95 ms (`reports/dos-resilience-tester/2026-05-08-production-loadtest.md`).

### Why one allocation on parameterised `Handle` routes (default)

The single allocation on parameterised routes is a **tiered `reqBundle`** (384 / 416 / 480 B for 1 / 2 / 3 parameters) that fuses the `requestCtx` and the copy of `*http.Request` into one GC-class-aligned object. This is a deliberate trade-off:

- `Handle` returns a 100 % `net/http`-compatible `http.Handler` chain. The single fused allocation is the safest available implementation against the previous race conditions detected by the `concurrency-security-auditor` (CSA-001) on the experimental zero-alloc design.
- `HandleFast` provides a fast-path `FastHandler` type that bypasses the standard wrapper and beats `httprouter` on every parameterised case while keeping a 1 alloc / 32–96 B footprint. Use `HandleFast` for trusted internal routes where stdlib middleware overhead is unacceptable.
- **`PoolRequestBundle = true`** further recycles the `reqBundle` via `sync.Pool`, dropping the `Handle` path to 0 allocations as well. It requires the handler lifetime contract — see [Maximum Performance Guide](max-performance.md).

See `CLAUDE.md` for the design rationale and `SECURITY.md` for the concurrency analysis behind the trade-off.

---

## Zero-allocation opt-ins

MuxMaster offers two opt-in switches that drop the param-route path to zero allocations:

```go
mux := muxmaster.New()
mux.PoolRequestBundle = true   // Opt O13 — recycles reqBundle for Handle routes
mux.PoolFastParams    = true   // Opt O9  — recycles Params slice for HandleFast
```

These eliminate the per-request allocation on every route with path parameters. The price is a tighter handler lifetime contract: handlers MUST NOT retain `*http.Request` (or the `Params` slice on `HandleFast`) past return. A goroutine that captures `r` would observe a recycled bundle belonging to a future, unrelated request — effectively a use-after-free against the pool storage.

Worked recipes, the lifetime audit checklist, and a runnable example are in the **[Maximum Performance Guide](max-performance.md)** and `examples/max-performance/`.

---

## Running Benchmarks Locally

```
# All benchmarks with allocation counts
go test -bench=. -benchmem ./...

# Repeat 3 times and use benchstat for statistical comparison
go test -bench=. -benchmem -count=3 ./... | tee results.txt
benchstat results.txt
```

To compare before and after a code change:

```
go test -bench=. -benchmem -count=5 ./... > before.txt
# make your change
go test -bench=. -benchmem -count=5 ./... > after.txt
benchstat before.txt after.txt
```

---

## What Affects Performance

### Number of path parameters

Each additional parameter requires one extra comparison during tree traversal. This is linear and very fast — the difference between 1 and 3 parameters is approximately 20 ns.

### Regex-constrained parameters

Regex parameters compile the expression at startup and execute it during lookup. The overhead depends on the complexity of the pattern. A simple `[0-9]+` adds roughly 10–20 ns compared to an unconstrained `:name` parameter.

### Middleware

Middleware is applied at registration time, so it has no effect on the routing overhead itself. However, each middleware layer adds function-call overhead during the request. A chain of 5 middleware functions typically adds 50–200 ns depending on what they do.

### Route tree depth

Routes registered with longer paths require more tree traversal steps. In practice, paths are short enough that this is not measurable.

### Number of registered routes

Because the radix tree compresses shared prefixes, the number of routes has almost no effect on lookup time. A router with 1000 routes and a router with 10 routes perform identically on a given path.

---

## Comparison Notes

### vs httprouter

httprouter is the historical performance reference for Go HTTP routers. MuxMaster `Handle` beats httprouter on static routes and on `Not found`; on parameterised routes it trails httprouter by 1.5–2× because `Handle` preserves a strict `net/http`-compatible chain (1 fused 416–480 B allocation per request). MuxMaster `HandleFast` removes the stdlib wrapper and beats httprouter on every parameterised case (50 ns vs 59 ns at 1 parameter; 17 ns vs 24 ns in the parallel benchmark).

### vs bunrouter

bunrouter claims zero allocations through **lazy parameter extraction** in its native API. The benchmarks in this repository measure bunrouter through the `HTTPHandlerFunc` adapter, which adds a `context.WithValue` and is therefore not representative of upstream native usage. In adapter mode, MuxMaster `Handle` is faster across the board; in native mode bunrouter is competitive, but parameter reads become O(n) per read instead of O(1).

### vs chi

chi uses a patricia radix trie and focuses on idiomatic API design over raw performance. MuxMaster `Handle` is approximately 8–25× faster in ns/op on the parameterised cases (115 ns vs 3 449 ns at 1 parameter) and allocates fewer bytes per request, while maintaining a compatible API surface.

### vs gorilla/mux

gorilla/mux uses regular-expression matching and was archived in 2022. It is typically 200–1 000× slower than MuxMaster for the same route set. MuxMaster is a drop-in replacement for the routing layer in gorilla/mux applications — see the [Migration Guide](migration.md).

---

## See Also

- [Migration Guide](migration.md) — replacing httprouter, chi, or gorilla/mux
- [Routing](routing.md) — how the radix tree resolves patterns
