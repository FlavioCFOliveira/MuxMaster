# Performance

## Scope

This file specifies performance targets for the MuxMaster router, the methodology for measuring and verifying those targets, and the design decisions that affect performance.

This file does not specify implementation details of the radix tree algorithm. It specifies observable performance requirements and the constraints that must be maintained to meet them.

---

## 1. Performance Targets

The following benchmarks must pass on a representative development machine. The reference hardware is AMD Ryzen 9 5900HX (or equivalent). Each benchmark runs `go test -bench=. -benchmem -count=5 ./...` to reduce variance.

| Benchmark case | Target ns/op | Target allocs/op | Measured HEAD (2026-09-26) | Measured httprouter (2026-09-26) |
|---|---|---|---|---|
| Static route | ≤ 150 | 0 | 28.63-29.6 ns, 0 allocs | 34.7 ns, 0 allocs |
| 1 named parameter | ≤ 200 | 0 (1 without pooling) | 116.9-118.5 ns, 1 alloc (46.7 ns, 0 allocs pooled) | 50.5 ns, 1 alloc |
| 5 named parameters | ≤ 300 | — | not separately benchmarked; see [params.md](params.md) section 5 for the allocation model beyond 3 parameters | — |
| Catch-all parameter | ≤ 150 | 0 (1 without pooling) | 116.4-132.4 ns, 1 alloc (46.5 ns, 0 allocs pooled) | 27.3 ns, 1 alloc |
| Static route (parallel) | ≤ 80 | 0 | 1.86-4.51 ns, 0 allocs | 2.35-4.91 ns, 0 allocs |

1. "0 allocs/op" means the benchmark reports `0 allocs/op` when run without the race detector. For a `Handle` route with one or more path parameters, this requires `Mux.PoolRequestBundle == true` (see [configuration.md](configuration.md) section 4.6); without it, the documented, deliberate cost is exactly one allocation per request (section 3.1).
2. The targets apply to the route lookup and handler dispatch path only. Response writing, middleware execution, and application logic are outside the measurement window.
3. Benchmarks are defined in `bench_test.go`. New benchmarks that verify targets for new features must be added alongside the implementation.
4. The "Measured" columns are sourced from `reports/perf-lab-2026-09-26-docs/README.md` (AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`, measured 2026-09-26; ranges cover the root-package and competitor-suite benchmarks, which use different request-construction helpers and so do not always agree to the last decimal). At `-count=3`, `benchstat` cannot compute a significance test (it requires `n >= 4`); every ns/op figure here is a point estimate, not a statistically confirmed value — allocation counts (`allocs/op`), by contrast, agreed exactly (`p=1.000`) across all runs in that report and are treated as solid. All figures remain comfortably within the ns/op targets in this table regardless.

---

## 2. Measurement Methodology

5. All benchmarks use `testing.B` from the standard library.
6. Benchmark handlers write nothing to the response writer. They exist only to confirm that the route was matched.
7. Benchmarks use `httptest.NewRecorder()` or a no-op `http.ResponseWriter` implementation to avoid measuring response-writing overhead.
8. Parallel benchmarks use `b.RunParallel` to simulate concurrent request handling.
9. Benchmark functions are named with the prefix `Benchmark` followed by the scenario name. Naming convention: `BenchmarkStaticRoute`, `BenchmarkOneParam`, `BenchmarkParallelStatic`.
10. The race detector (`-race`) must not be active during performance measurement. The race detector adds overhead that is not representative of production performance.

---

## 3. What Affects Performance

### 3.1 Zero Allocs Goal

11. The 0 allocs/op target for route lookup requires:
    - No allocation during tree traversal. Captured parameters are held in a stack-allocated buffer (see [params.md](params.md) section 5) that does not escape to the heap by itself.
    - No allocation for a static route (no path parameters), whether registered via `Handle` or `HandleFast`.
    - For a `Handle` route with path parameters and the default configuration (`PoolRequestBundle == false`), exactly one allocation is made: the tiered request bundle that fuses the parameter-carrying context with a copy of `*http.Request` (see section 8, Tiered Request Bundle). This single allocation replaces what would otherwise be two separate allocations (a context wrapper plus a copy of `*http.Request` from `r.WithContext`).
    - For a `HandleFast` route with path parameters and the default configuration (`PoolFastParams == false`), exactly one allocation is made: the `Params` slice passed as the handler's third argument. There is no context or request-copy allocation at all, because `FastHandler` dispatch never wraps the request context (see section 6, FastHandler Dispatch).
    - Enabling `PoolRequestBundle` or `PoolFastParams` (configuration.md sections 4.6 and 4.5) eliminates the one remaining allocation described above for the route types and parameter counts they cover, achieving 0 allocs/op on parameterized routes at the cost of the lifetime contracts documented for those flags.
12. Without pooling, one allocation per parameterized request is the practical floor for both `Handle` and `HandleFast` routes, because a value that must outlive the request-scoped stack frame — and remain valid if a handler passes it to a goroutine — has to live on the heap. This is a deliberate MuxMaster design trade-off, not an unavoidable `net/http` cost; `PoolRequestBundle` and `PoolFastParams` exist specifically to remove it, in exchange for the stricter handler lifetime contracts in configuration.md sections 4.5 and 4.6.

### 3.2 Middleware Overhead

13. Middleware applied at registration time (not at request time) contributes zero per-request overhead beyond the cost of the closure call chain. There is no slice iteration or middleware chain assembly at request time.
14. Pre-routing middleware (if registered) adds one additional function call per request regardless of whether a route is matched.

### 3.3 Feature Flag Overhead

15. Boolean feature flags (`RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS`) add a single conditional check per request when enabled. When the route is matched on the first lookup (the common case), these checks are only reached in the fallback path.
16. When all flags are `false`, the router's fast path (match found on first lookup) executes the minimum possible code.

### 3.4 Radix Tree Complexity

17. Route lookup is O(k) where k is the length of the request path in bytes. It does not depend on the number of registered routes.
18. Static routes are dispatched via byte comparison on compressed tree edges, which is cache-friendly.
19. Named parameter nodes add one `strings.IndexByte` call per parameter to find the segment boundary.
20. Regex parameter nodes add one `regexp.Regexp.MatchString` call per parameter. This is significantly more expensive than named parameters. Regex parameters should not be used on high-traffic routes if performance is a concern.

### 3.5 Case-Insensitive Matching

21. When `CaseInsensitive` is `true`, every byte comparison in tree traversal is replaced with a case-folded comparison. This roughly doubles the CPU cost of the static-segment matching phase. Case-insensitive matching must not be used when the static route target (≤ 150 ns/op) is required.

---

## 4. Performance Regression Policy

22. A change that causes any benchmark listed in section 1 to exceed its target ns/op or allocs/op is considered a performance regression.
23. Performance regressions must be resolved before a change is merged, unless the user explicitly accepts the regression and updates the targets in this file.
24. Benchmark results are sensitive to system load. A result that exceeds the target by less than 10% on a loaded machine is not automatically a regression. Run benchmarks at least 3 times on an idle machine before concluding there is a regression.

---

## 5. Comparison with Competitors

The primary performance competitors are `httprouter` and `bunrouter`. The table below distinguishes two kinds of data and must not conflate them:

- **Measured** rows are MuxMaster's own `competitor/bench_test.go` suite, run against the real competitor source. Source: `reports/perf-lab-2026-09-26-docs/README.md` §3, AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`, measured 2026-09-26. `bunrouter` is measured through its `http.Handler` adapter (not its native, zero-alloc API — see the note below the table).
- **External** rows are the competitor project's own published claim, from its documentation or the third-party `go-http-routing-benchmark` suite, which uses a different methodology (matching against a large, realistic route table rather than a single isolated route) and is not directly comparable, row for row, to MuxMaster's own single-route benchmarks.

| Router | Static ns/op | Static allocs | 1 param ns/op | 1 param allocs | Basis |
|---|---|---|---|---|---|
| MuxMaster (default) | 28.6-29.6 | 0 | 116.9-118.5 | 1 | Measured |
| MuxMaster (Pooled, `PoolRequestBundle=true`) | 28.6-30.7 | 0 | 46.7-48.5 | 0 | Measured |
| MuxMaster (Fast, `HandleFast`) | 25.5-30.6 | 0 | 43.3-47.8 | 1 | Measured |
| httprouter | 34.7 | 0 | 50.5 | 1 | Measured |
| bunrouter (`http.Handler` adapter) | 168.1 | 3 | 160.3 | 3 | Measured |
| bunrouter (native `bunrouter.HandlerFunc` API) | < httprouter | 0 | ~200 | 0 | External (bunrouter docs) |
| chi v5 | 217.9 | 2 | 360.7 | 4 | Measured |
| gorilla/mux | 578.9 | — | 954.7 | — | Measured |
| gorilla/mux | ~3 444 278 | 156 015 | — | — | External (go-http-routing-benchmark) |
| net/http (Go 1.22+) | ~706 222 | 96 | — | — | External (go-http-routing-benchmark) |

`bunrouter`'s native API achieves 0 allocations by using its own `bunrouter.HandlerFunc` handler signature rather than `net/http`'s; MuxMaster's competitor suite benchmarks `bunrouter` through the `http.Handler` adapter its own `go.mod` pulls in, which is why the measured row shows 3 allocations even on the static route. This is a known, documented difference in what is being measured, not a discrepancy between MuxMaster's numbers and bunrouter's claim.

25. The comparison benchmarks in `bench_test.go` must include direct comparisons against at least `httprouter` and `bunrouter` in a separate benchmark file or build tag that imports those packages. These comparison benchmarks are not part of the zero-dependency build and must be excluded from `go build` and `go test ./...` by default. They are enabled only when explicitly requested.

---

## 6. FastHandler Dispatch

26. `HandleFast(method, pattern string, h FastHandler)` registers a `FastHandler` — a handler with the signature `func(http.ResponseWriter, *http.Request, Params)` — for the given method and pattern on `*Mux`. `(*Group).HandleFast` is the group-scoped equivalent; the full pattern is `group.prefix + pattern`.
27. Dedicated convenience registration methods exist for `FastHandler` on `*Mux`: `GETFast`, `HEADFast`, `POSTFast`, `PUTFast`, `PATCHFast`, `DELETEFast`, `OPTIONSFast`, `CONNECTFast`, `TRACEFast`, and `QUERYFast`. Each delegates to `HandleFast`. `QUERY` is a standard HTTP method (RFC 10008); see [routing.md](routing.md) section 9. Unlike the other `...Fast` methods, `QUERYFast` has no `*Group` equivalent: `*Group` does not expose any `...Fast` convenience methods (see [groups.md](groups.md) section 3); `(*Group).HandleFast(muxmaster.MethodQuery, pattern, h)` remains available for that purpose.
28. `FastHandler` routes receive path parameters as the third argument (`Params`) instead of through the request context. This avoids the context-wrapping allocation that `Handle` routes pay on parameterized routes (see section 8, Tiered Request Bundle).
29. For a static `FastHandler` route (no path parameters), the `Params` argument passed to the handler is `nil`.
30. `PathParam` and `ParamsFromContext` never observe parameters captured by a `FastHandler` route, because those parameters are never placed in the request context. `FastHandler` code must read the third argument directly.
31. The original `*http.Request` passed to `ServeHTTP` is never mutated by `FastHandler` dispatch: its context, URL, and all other fields are left exactly as received. `Handle` and `HandleFast` routes may coexist on the same `*Mux` or `*Group` without interfering with each other.
32. The lifetime of the `Params` slice passed to a `FastHandler` depends on `Mux.PoolFastParams` (see configuration.md section 4.5):
    - When `false` (the default), the dispatcher allocates a fresh `Params` slice per request. The slice remains valid after the handler returns; a goroutine spawned from the handler may safely retain and read it.
    - When `true`, the `Params` slice for 1-, 2-, or 3-parameter routes is drawn from a `sync.Pool` tier and is cleared and returned to the pool the instant the handler returns. A handler in this mode MUST NOT retain the slice, or any element of it, past return.
33. `Mux.PanicHandler`, when set, recovers panics raised inside a `FastHandler` exactly as it does for `Handle` routes.
34. Stdlib middleware registered via `Use` does NOT wrap `FastHandler` routes. `FastMiddleware`, registered via `UseFast`, is the dedicated `FastHandler` middleware mechanism, applied at registration time with the same zero-per-request-overhead model as stdlib middleware (see middleware.md sections 6 and 7 for composition rules and the full route-type coverage matrix).
35. Registering a route via `HandleFast` on a `*Mux` or `*Group` that already has one or more middleware registered via `Use` (or `Group.Use`) causes a panic at that `HandleFast` call. This guard only checks middleware registered earlier via `Use`; see middleware.md section 7, requirement 43, for the exact evaluation-order caveat this implies.

---

## 7. Lock-Free Dispatch

36. The route trees for all HTTP methods are held in a single `atomic.Pointer` value, read via `Load()` on every request without acquiring a lock. The request-time read path used by `ServeHTTP` is fully lock-free and non-blocking regardless of how many requests are being served concurrently.
37. Route registration (`Handle`, `HandleFast`) is serialized by an internal mutex and uses a copy-on-write strategy based on path copying: for the affected method's tree, only the nodes on the path from the root to the point of insertion are copied; every node not on that insertion path is shared, unmodified, between the tree snapshot currently published and the new tree snapshot being built. Sharing untouched subtrees is safe because a node that belongs to a published tree is never mutated after publication — only the nodes newly copied for the current registration, which are not reachable from any published tree until the registration completes, are written to. `maxParams` is tracked only on the tree's root node, never on any other node: it records whether any route registered anywhere in that method's tree captures at least one path parameter (named, regex, or catch-all), and is used to skip parameter-buffer setup entirely when a tree is purely static. Each registration folds the newly inserted pattern's own parameter count into the root's existing `maxParams` value in a single, constant-time step — never by walking the tree, and never by maintaining a separate value per node. Once the copied path has been fully built and mutated, the updated set of trees is published with a single atomic store. Requests in flight during a registration continue to observe the previous, unmodified tree snapshot until the new one is published; they never observe a partially mutated tree, and they never observe a mix of nodes belonging to two different registrations.
38. If a registration call panics partway through mutating the copied insertion path (for example, on a duplicate route or an invalid wildcard), every node copied for that registration is discarded without ever being published, and the previously published tree snapshot is left completely intact — including every subtree that was shared, unmodified, with the failed registration. This holds regardless of how much of the tree is shared, because a shared subtree is only ever read, never mutated, by the registration that shares it.
39. This mechanism exists to keep the request-time read path lock-free and to make registration-time panics safe; it does not change the policy stated in out-of-scope.md section 3.1: registering or removing routes after the server has begun serving requests remains unsupported and is not a use case the router is designed, tested, or documented for.

---

## 8. Tiered Request Bundle

40. When a `Handle` (stdlib `http.Handler`) route matches a request with one or more path parameters, the router allocates exactly one object that fuses the parameter-carrying request context together with a copy of the `*http.Request`, instead of allocating the context and the request copy as two separate objects. This single-allocation design is the tiered request bundle.
41. The bundle is tiered by parameter count so that each allocation fits the smallest Go runtime GC size class that can hold it:

    | Parameter count | Bundle | Logical size | GC size class |
    |---|---|---|---|
    | 1 | `reqBundle1` | 368 B | 384 B |
    | 2 | `reqBundle2` | 400 B | 416 B |
    | 3 or more | `reqBundle` | 456 B | 480 B |

42. Routes with more than 3 parameters store the first 3 inline in the bundle and allocate one additional, separate `Params` slice for the remaining parameters. This is an extra allocation limited to the rare case of routes with more than 3 path parameters.
43. The tiered request bundle is used only for `Handle` routes that have at least one path parameter. Static `Handle` routes and all `FastHandler` routes never allocate it.
44. By default (`Mux.PoolRequestBundle == false`), each matching request allocates a fresh bundle. Its lifetime is managed by the garbage collector like any other heap object: the handler, and any goroutine it spawns, may retain the `*http.Request` after the handler returns.
45. When `Mux.PoolRequestBundle == true`, the bundle is drawn from a `sync.Pool` tier matching the parameter count and is zeroed and returned to the pool the instant the handler's `ServeHTTP` call returns. See configuration.md section 4.6 for the full lifetime contract this places on handlers.
46. If the private context field of `http.Request` cannot be located via reflection during package initialization — a forward-compatibility guard against a future Go version that renames or removes it — the router falls back to building the context wrapper and calling `r.WithContext`, which allocates the context and the request copy as two separate objects. This fallback applies uniformly whether `PoolRequestBundle` is enabled or not, and is transparent to handler code; it does not change any documented behavior, only the allocation count.
