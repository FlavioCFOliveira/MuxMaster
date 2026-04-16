# Performance

## Scope

This file specifies performance targets for the MuxMaster router, the methodology for measuring and verifying those targets, and the design decisions that affect performance.

This file does not specify implementation details of the radix tree algorithm. It specifies observable performance requirements and the constraints that must be maintained to meet them.

---

## 1. Performance Targets

The following benchmarks must pass on a representative development machine. The reference hardware is AMD Ryzen 9 5900HX (or equivalent). Each benchmark runs `go test -bench=. -benchmem -count=5 ./...` to reduce variance.

| Benchmark case | Target ns/op | Target allocs/op | Reference |
|---|---|---|---|
| Static route | ≤ 150 | 0 | httprouter: ~150 ns/op, 0 allocs |
| 1 named parameter | ≤ 200 | 0 | bunrouter: ~200 ns/op, 0 allocs |
| 5 named parameters | ≤ 300 | 0 | bunrouter: ~280 ns/op, 0 allocs |
| Catch-all parameter | ≤ 150 | 0 | — |
| Static route (parallel) | ≤ 80 | 0 | — |

1. "0 allocs/op" means the benchmark reports `0 allocs/op` when run without the race detector.
2. The targets apply to the route lookup and handler dispatch path only. Response writing, middleware execution, and application logic are outside the measurement window.
3. Benchmarks are defined in `bench_test.go`. New benchmarks that verify targets for new features must be added alongside the implementation.

---

## 2. Measurement Methodology

4. All benchmarks use `testing.B` from the standard library.
5. Benchmark handlers write nothing to the response writer. They exist only to confirm that the route was matched.
6. Benchmarks use `httptest.NewRecorder()` or a no-op `http.ResponseWriter` implementation to avoid measuring response-writing overhead.
7. Parallel benchmarks use `b.RunParallel` to simulate concurrent request handling.
8. Benchmark functions are named with the prefix `Benchmark` followed by the scenario name. Naming convention: `BenchmarkStaticRoute`, `BenchmarkOneParam`, `BenchmarkParallelStatic`.
9. The race detector (`-race`) must not be active during performance measurement. The race detector adds overhead that is not representative of production performance.

---

## 3. What Affects Performance

### 3.1 Zero Allocs Goal

10. The 0 allocs/op target for route lookup requires:
    - No allocation during tree traversal.
    - No allocation when the route has no parameters (`Params` from the pool is released without copying).
    - When the route has parameters, exactly one allocation is made: the `make(Params, n)` copy placed in the context. This one allocation is unavoidable because the copy must outlive the pool slice.
11. Allocation of the `*http.Request` by `r.WithContext` is counted by the benchmarks. This allocation is unavoidable in standard `net/http` and is not a MuxMaster failure.

### 3.2 Middleware Overhead

12. Middleware applied at registration time (not at request time) contributes zero per-request overhead beyond the cost of the closure call chain. There is no slice iteration or middleware chain assembly at request time.
13. Pre-routing middleware (if registered) adds one additional function call per request regardless of whether a route is matched.

### 3.3 Feature Flag Overhead

14. Boolean feature flags (`RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS`) add a single conditional check per request when enabled. When the route is matched on the first lookup (the common case), these checks are only reached in the fallback path.
15. When all flags are `false`, the router's fast path (match found on first lookup) executes the minimum possible code.

### 3.4 Radix Tree Complexity

16. Route lookup is O(k) where k is the length of the request path in bytes. It does not depend on the number of registered routes.
17. Static routes are dispatched via byte comparison on compressed tree edges, which is cache-friendly.
18. Named parameter nodes add one `strings.IndexByte` call per parameter to find the segment boundary.
19. Regex parameter nodes add one `regexp.Regexp.MatchString` call per parameter. This is significantly more expensive than named parameters. Regex parameters should not be used on high-traffic routes if performance is a concern.

### 3.5 Case-Insensitive Matching

20. When `CaseInsensitive` is `true`, every byte comparison in tree traversal is replaced with a case-folded comparison. This roughly doubles the CPU cost of the static-segment matching phase. Case-insensitive matching must not be used when the static route target (≤ 150 ns/op) is required.

---

## 4. Performance Regression Policy

21. A change that causes any benchmark listed in section 1 to exceed its target ns/op or allocs/op is considered a performance regression.
22. Performance regressions must be resolved before a change is merged, unless the user explicitly accepts the regression and updates the targets in this file.
23. Benchmark results are sensitive to system load. A result that exceeds the target by less than 10% on a loaded machine is not automatically a regression. Run benchmarks at least 3 times on an idle machine before concluding there is a regression.

---

## 5. Comparison with Competitors

The primary performance competitors are `httprouter` and `bunrouter`. Reference data:

| Router | Static ns/op | Static allocs | 1 param ns/op | 1 param allocs | Source |
|---|---|---|---|---|---|
| httprouter | ~150 | 0 | ~200 | 0 | go-http-routing-benchmark |
| bunrouter | < httprouter | 0 | ~200 | 0 | bunrouter docs |
| chi | competitive | low | competitive | low | go-http-routing-benchmark |
| gorilla/mux | ~3 444 278 | 156 015 | — | — | go-http-routing-benchmark |
| net/http (Go 1.22+) | ~706 222 | 96 | — | — | go-http-routing-benchmark |

24. The comparison benchmarks in `bench_test.go` must include direct comparisons against at least `httprouter` and `bunrouter` in a separate benchmark file or build tag that imports those packages. These comparison benchmarks are not part of the zero-dependency build and must be excluded from `go build` and `go test ./...` by default. They are enabled only when explicitly requested.
