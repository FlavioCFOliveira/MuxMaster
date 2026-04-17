# Performance

MuxMaster is designed to add negligible overhead to the standard `net/http` stack. This document explains the design decisions behind its performance, how to measure it, and how it compares to other Go HTTP routers.

## Table of Contents

- [Design Goals](#design-goals)
- [How Zero Allocations Are Achieved](#how-zero-allocations-are-achieved)
- [Benchmarks](#benchmarks)
- [Running Benchmarks Locally](#running-benchmarks-locally)
- [What Affects Performance](#what-affects-performance)
- [Comparison Notes](#comparison-notes)

---

## Design Goals

1. **Zero allocations on the hot path** — every request that serves a static or parameterized route (up to three parameters) must allocate 0 bytes.
2. **Sub-microsecond dispatch** — route lookup must complete in tens of nanoseconds, not hundreds.
3. **Linear scalability** — throughput per core must scale linearly with the number of CPUs.

---

## How Zero Allocations Are Achieved

### Radix tree

Routes are stored in a radix (compressed prefix) tree — one tree per HTTP method. Lookup is O(k) in the path length, not O(n) in the number of routes. The tree is built at startup and never mutated during request processing, so no locks are needed on the read path.

### Pooled request context

Path parameters and the matched route pattern are stored in a `requestCtx` struct that is retrieved from a `sync.Pool` on each request. `sync.Pool` has per-P (per-OS-thread) free lists, which means under concurrent load, pool gets and puts are nearly always local to the current CPU and require no atomic operations.

The struct has an inline array for up to three parameters (`[3]Param`). For routes with one to three parameters, no separate heap allocation is needed — the slice header in the struct points into this inline array.

For routes with more than three parameters (uncommon in practice), a small heap allocation is made.

### Direct context injection

Storing the `requestCtx` in the request context normally requires calling `r.WithContext(ctx)`, which allocates a new `http.Request`. MuxMaster avoids this by writing the context pointer directly into the unexported `ctx` field of the existing `http.Request` using `unsafe.Add`. The offset is determined once at `init` time via reflection.

This technique eliminates one `http.Request` allocation per parameterized request.

### Middleware at registration time

Middleware is applied at **route registration** time, not at request dispatch time. The router stores the fully-wrapped handler directly. At request time, the router calls a single function pointer — there is no middleware chain to iterate.

### Method dispatch via array index

Standard HTTP methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE) are mapped to array indices at compile time. Method dispatch during a request is an array access — O(1) and branch-free.

---

## Benchmarks

Measured on Apple M4, Go 1.26. All routers use the route set `/api/v1/...`.

### Serial (single goroutine)

| Route type      | MuxMaster               | httprouter              | bunrouter               |
|-----------------|-------------------------|-------------------------|-------------------------|
| Static          | **13.5 ns, 0 allocs**   | 15.9 ns, 0 allocs       | 14.0 ns, 0 allocs       |
| 1 parameter     | 27 ns, 0 allocs         | 32.8 ns, 1 alloc        | 22.4 ns, 0 allocs       |
| 2 parameters    | **38.7 ns, 0 allocs**   | 40.0 ns, 1 alloc        | 41.7 ns, 0 allocs       |
| 3 parameters    | 46.7 ns, 0 allocs       | 44.5 ns, 1 alloc        | **29.8 ns, 0 allocs**   |
| Catch-all       | **23.2 ns, 0 allocs**   | 28.0 ns, 1 alloc        | 11.9 ns, 0 allocs       |

### Parallel (GOMAXPROCS cores)

| Route type      | MuxMaster               | httprouter              | bunrouter               |
|-----------------|-------------------------|-------------------------|-------------------------|
| Static          | **1.55 ns, 0 allocs**   | 1.98 ns, 0 allocs       | 1.77 ns, 0 allocs       |
| 1 parameter     | ~10 ns, 0 allocs        | 15.5 ns, 1 alloc        | 3.6 ns, 0 allocs        |

The parallel static benchmark shows near-linear CPU scaling: 13.5 ns serial → 1.55 ns parallel on a 10-core M4 (8.7× speedup).

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

httprouter is the historical performance reference for Go HTTP routers. MuxMaster equals or exceeds httprouter in ns/op across all route types, while allocating zero bytes per request instead of one `Params` slice per parameterized request.

### vs bunrouter

bunrouter claims zero allocations through **lazy parameter extraction** — it does not copy parameter values during tree traversal; instead it records offsets into the URL string. This makes traversal fast, but parameter reads become O(n) per read rather than O(1).

For routes that read all parameters (the common case), MuxMaster is faster from ≥ 3 parameters upward, because eager extraction pays the cost once at dispatch time.

### vs chi

chi uses a patricia radix trie and focuses on idiomatic API design over raw performance. MuxMaster is generally 2–4× faster in ns/op and allocates fewer bytes, while maintaining a compatible API surface.

### vs gorilla/mux

gorilla/mux uses regular expression matching. It is typically 200–1000× slower than MuxMaster for the same route set. MuxMaster is a drop-in replacement for the routing layer in gorilla/mux applications — see the [Migration Guide](migration.md).

---

## See Also

- [Migration Guide](migration.md) — replacing httprouter, chi, or gorilla/mux
- [Routing](routing.md) — how the radix tree resolves patterns
