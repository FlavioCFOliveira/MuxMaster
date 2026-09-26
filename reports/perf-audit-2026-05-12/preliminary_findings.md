# MuxMaster — Exhaustive performance audit — Preliminary findings

**Date:** 2026-05-12
**Branch:** `perf/maximize-performance`
**Hardware:** AMD Ryzen 9 5900HX (16 threads), Linux 6.8.0-111, Go 1.26.2
**Methodology:** `go test -bench=. -benchmem -count=10 -benchtime=1s` + benchstat + pprof CPU/mem + objdump + gcflags=-m=2

---

## 1. Confirmed baseline (10 runs with benchstat)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|---|---|---|---|---|
| `StaticRoute` | **25.57** ± 2% | 0 | 0 | Beats httprouter (33.8 ns) |
| `ParamRoute1` | 121.0 ± 1% | 416 | 1 | Vs httprouter 58 ns |
| `ParamRoute2` | 142.2 ± 1% | 448 | 1 | Vs httprouter 71 ns |
| `ParamRoute3` | 147.6 ± 1% | 480 | 1 | Vs httprouter 78 ns |
| `WildcardRoute` | 123.4 ± 3% | 416 | 1 | Vs httprouter 50 ns |
| `NotFound` (defaults) | 334.9 ± 150% | 117 | 3 | Huge variance |
| `ParallelStaticRoute` | 4.047 ± 3% | 0 | 0 | Excellent |
| `ParallelParamRoute` | 113.2 ± 2% | 416 | 1 | Vs httprouter 22.5 ns ⚠️ |
| `FastStaticRoute` | 26.83 ± 1% | 0 | 0 | ≈ stdlib |
| `FastParamRoute1` | **52.55** ± 1% | 32 | 1 | **Beats httprouter (56 ns)** |
| `FastParamRoute2` | 95.03 ± 25% | 64 | 1 | High variance |
| `FastParamRoute3` | 77.99 ± 63% | 96 | 1 | VERY high variance |
| `FastParallelParamRoute` | 15.95 ± 1% | 32 | 1 | Excellent |

### Extra cases measured (5 runs)

| Bench | ns/op | B/op | allocs/op | Diagnosis |
|---|---|---|---|---|
| `NotFoundCustomHandler` | 22 | 0 | 0 | **Confirms: the 3 allocs of the default NotFound come from `http.NotFound`** |
| `NotFoundWithMethodAllowedLookup` | 343 | 123 | 3 | `allowed()` uses a string builder |
| **`MethodNotAllowed`** | **449** | **138** | **6** | **6 allocs! `http.Error()` + headers** |
| `OPTIONSAuto` | 161 | 40 | 3 | Reasonable |
| **`RedirectTSL`** | **1554** | **1305** | **15** | **CRITICAL. closure + url.URL{}.String() + http.Redirect allocate heavily** |
| `PathParamLookup` | 132–196 | 416 | 1 | OK (50% variance) |
| `PathParamFast` | 46–52 | 32 | 1 | Excellent |
| `ParamsFromContext` | 152–182 | 448 | 1 | OK |

---

## 2. CPU profile (5s runs on ParamRoute1/3 + Static + FastParam1)

**Top hotspots (flat%):**
| % flat | % cum | Function |
|---|---|---|
| 20.48% | 34.37% | `(*node).getValue` |
| 8.99% | 8.99% | `memeqbody` (string compare) |
| 7.98% | 74.26% | `(*Mux).dispatch` |
| 3.11% | 78.13% | `(*Mux).ServeHTTP` |
| 2.72% | 2.72% | `runtime.memclrNoHeapPointers` (zeroing) |
| 2.72% | 3.11% | `http.HandlerFunc.ServeHTTP` |
| 2.58% | 20.24% | `dispatchWithParams` |
| 2.41% | 16.11% | `mallocgcSmallScanNoHeader` |
| 1.94% | 10.28% | `dispatchParams1Fast` |
| 1.84% | 1.84% | `nextFreeFast` (alloc) |
| 1.72% | 1.72% | `methodIdx` (inline) |
| 1.60% | 1.77% | `mspan.writeHeapBitsSmall` |
| 1.51% | 1.51% | `runtime.memequal` |
| 1.36% | 1.36% | `foldEq` (inline) |
| 1.15% | 1.15% | `paramsBuf.add` (inline) |
| 0.88% | 11.38% | `prefixMatch` (inline) |

**Totals per category:**
- **Tree lookup (getValue + memeq + prefixMatch + foldEq + paramsBuf.add)**: ~37% cum
- **Bundle alloc (mallocgc + nextFreeFast + writeHeapBits + memclr + ...)**: ~16% cum
- **dispatch path scaffolding**: ~10% cum
- **HandlerFunc dispatch**: 2.72% cum

---

## 3. Line-by-line hot-path analysis

### `dispatch` (mux.go:865)
| Line | Cumulative ns | Operation | Possible optimisation |
|---|---|---|---|
| 867: `urlPath := r.URL.Path` | 170ms | Load | none directly |
| 873: `trees := m.treesPtr.Load()` | 110ms | Atomic load | OK |
| 876: `methodIdx(r.Method)` | 460ms | Switch case (already optimised to CMPW/CMPL by the compiler) | Reordering to put GET first may save 1-2ns |
| 885: `var ps paramsBuf` | 190ms | Stack zeroing (128B) | Skip if `maxParams==0` (already done) |
| 890: `root.getValue(...)` | 6.85s cum | **Tree lookup** (72% of dispatch) | Main focus |
| 898: `fps := make(Params, ps.count)` | 1.49s cum | Params alloc for FastHandler | Investigate a safe pool |
| 922: `dispatchWithParams(...)` | 5.87s cum | **reqBundle alloc (>3 params) or dispatch1/2** | Main focus |

### `getValue` (tree.go:442)
| Line | ms | Operation | Optimisation |
|---|---|---|---|
| 445: `prefix := n.path` | 290ms | Load string header | Cache-line layout already optimised (CL0) |
| 447: `if len(path) > len(prefix)` | 280ms | Length compare | OK |
| 448: `prefixMatch(...)` | 1.99s cum | **String compare (memequal)** | Inline byte compare for len≤16; uint64 reads via unsafe |
| 451: `path = path[len(prefix):]` | 620ms | Slice header construction | Unavoidable |
| 454: `c := path[0]` | 130ms | Bounds check + load | gcassert directive |
| 455: `children := n.children[:len(n.indices)]` | 290ms | Slice header construction | Refactor: guarantee len(children) == len(indices) (without the wildchild mixed in) |
| 456-457: `for j := range len(n.indices); foldEq(c, n.indices[j], ci)` | 470ms | Linear scan of the indices | Indices is typically 1-3 chars; already optimal |
| 482: `params.add(name, value)` | 380ms cum | Store into buf (gc write-barrier check) | unsafe store without a barrier if buf is stack-allocated |

### `dispatchParams1Fast` (params.go:246)
Sequence observed in the assembly:
1. **`runtime.newobject` for `reqBundle1`** (1 alloc, 416B class) — UNAVOIDABLE with the current architecture
2. **Set the `Context` field** with `gcWriteBarrier4`
3. **Set `pattern`, `small[0]`, `params`** (several stores, several barrier checks)
4. **`*r` (304B) copy** via `MOVUPS X14` (SSE 16-byte) — already optimal
5. **`setReqCtxUnsafe`** — 1 unsafe.Add + 1 store + gcWriteBarrier2
6. **`h.ServeHTTP(&b.req)`** — virtual call

---

## 4. Escape analysis — every alloc on the hot path

| Location | Allocation | Hot path? | Unavoidable? |
|---|---|---|---|
| `params.go:247` | `&reqBundle1{}` | YES (param routes) | Yes, without a risky `sync.Pool` |
| `params.go:269` | `&reqBundle2{}` | YES | Yes |
| `params.go:308` | `&reqBundle{}` | YES | Yes |
| `params.go:317/332` | `make(Params, n)` (overflow >3 params) | RARE | Yes (>3 params) |
| `mux.go:898` | `fps := make(Params, ps.count)` (FastHandler) | YES (Fast routes) | **No — a pool is viable (the FastHandler doc says params are valid only during the call)** |
| Pre-allocated (registration) | Several `&node{}`, `append(...)` in `addRoute` | NO (registration) | N/A |

---

## 5. Analysis of non-hot (but heavy) paths

### `MethodNotAllowed` — 449ns / 6 allocs / 138B
Probable composition (not verified line by line):
1. `m.allowed(urlPath, r.Method)` — `strings.Builder` allocates at least 2x
2. `m.lazyMethodNotAllowed(cfg, allow).ServeHTTP(...)` — cached, but the handler inside:
3. `w.Header().Set("Allow", allow)` — internal to the http.ResponseWriter
4. `http.Error(w, http.StatusText(...), 405)` — allocates a string

**Optimisation:** pre-compute common `Allow` strings at registration time (based on the final tree), but this requires freezing.

### `RedirectTSL` — 1554ns / 15 allocs / 1305B — 🚨 CRITICAL
Located at `mux.go:938-955`:
```go
target := (&url.URL{Path: newPath, RawQuery: r.URL.RawQuery}).String()
m.mu.RLock(); mw := m.middleware; m.mu.RUnlock()
wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    http.Redirect(w, r, target, code)
}), mw).ServeHTTP(w, r)
```

**Problems:**
1. **`&url.URL{...}` allocates** (url.URL object ~104B)
2. **`.String()` allocates** the serialisation result
3. **`http.HandlerFunc(func...)` closure escape** — the closure allocates
4. **`wrapMiddleware(...)` called ON EVERY REQUEST** — not cached
5. **`http.Redirect`** allocates internally for the response

This path is rarely taken but is expensive. It can be optimised:
- Cache the redirect handler per (newPath, code) — but combinatorial explosion
- Or: build the Location header without url.URL — direct concatenation of safe bytes
- Or: skip `wrapMiddleware` (the redirect is only a header + status — it does not need wrapping)

### `paramsBuf.size`
Current `unsafe.Sizeof(paramsBuf{})`:
- `count int`: 8 B
- `buf [3]Param`: 3 * 32 = 96 B
- `overflow []Param`: 24 B
- Padding/alignment: 0
- **Total: 128 B**

(The comment in the code mentions 264 B — perhaps from an earlier `maxParams=8` iteration. Verify and update the comment.)

The **zeroing of the 128B** is done on every call that enters the hot path (it is not limited to `maxParams>0`). Current line 885:
```go
var ps paramsBuf  // always zeroes 128B
var psBuf *paramsBuf
if root.maxParams > 0 { psBuf = &ps }
```

Can the zeroing be eliminated when there are no params? Not exactly — the compiler zeroes it because the struct contains pointers (slice header). But: if we always set `psBuf = &ps` or `psBuf = nil` according to `root.maxParams`, it is simpler and potentially faster (no branch).

---

## 6. Direct comparison with httprouter (apples-to-apples)

| Case | MuxMaster | httprouter | Δ | Cause |
|---|---|---|---|---|
| Static | 30 ns / 0 allocs | 34 ns / 0 allocs | **+14% MuxMaster** | Tree implementation |
| Param1 | 124 ns / 416B / 1 alloc | 58 ns / 64B / 1 alloc | -53% MuxMaster | **httprouter allocates only the Params slice; MuxMaster allocates the whole request bundle to support `r.Context()`** |
| Param2 | 148 ns / 448B / 1 alloc | 71 ns / 64B / 1 alloc | -52% | same reason |
| Param3 | 150 ns / 480B / 1 alloc | 78 ns / 96B / 1 alloc | -48% | same reason |
| ParallelParam | 107 ns / 416B / 1 alloc | 23 ns / 64B / 1 alloc | -78% | the bundle is a source of GC pressure |
| MuxMaster FastParam1 | 52 ns / 32B / 1 alloc | 58 ns / 64B / 1 alloc | **+10% MuxMaster** | FastHandler bypasses the context |

**Central conclusion:** The cost of the bundle (392-456B vs httprouter's 64-96B) is the structural difference. If we could:
1. **Eliminate** the bundle alloc (safe pool), or
2. **Reduce** the bundle size dramatically (not copy the whole *http.Request)

...we would be competitive with httprouter.

---

## 7. Optimisation hypotheses for analysis by the specialised agents

### H1 — `sync.Pool` for reqBundle1/2/3
**Identified risk:** CSA-001 (Concurrency Security Audit, 2026) determined that `r.WithContext`-style mutation of the original request has a race condition (middleware goroutines that still reference the old `r`).

**BUT:** our bundle CONTAINS a fresh copy of the request. If the pool keeps the bundle until the end of the handler chain and then recycles it... the risk exists only if a handler/middleware spawns a goroutine with a reference to the bundle (rare but possible — think of `go log(r.Context())`).

**Possible mitigation:** the pool recycles only bundles whose refcount drops to 0 — but that adds refcounting overhead that probably cancels out the gain.

**Safe alternative:** a pool with explicit release only after `handler.ServeHTTP` returns WITHOUT a panic. Goroutines spawned inside the handler that capture `r` keep a reference to the bundle BEFORE the release — because the pool reset empties the fields and marks them as "stale". Solutions to invalidate references to the request copy in handlers that escaped: a) immutability — bundle.req is never mutated after it is set; b) lifetime — the pool recycles only after end-of-handler.

→ **Verdict: requires rigorous analysis by the concurrency-security-auditor before proceeding.**

### H2 — Reduce/eliminate the zeroing of `paramsBuf` on the stack
If `paramsBuf` were smaller (e.g. 64B without the overflow slice header for the common case), the memclr would be faster. But the overflow is needed for >3 params.

**Alternative:** Fall back to a dedicated slow path when >3 params, using external allocation for the overflow. `paramsBuf` becomes `count int + buf [3]Param = 104 B`.

### H3 — Eliminate the allocation in `make(Params, ps.count)` for FastHandler
Currently `mux.go:898` makes an explicit copy of the stack-allocated paramsBuf into a heap-allocated Params (32-96B). If pooling the FastHandler params is SAFE (the FastHandler doc says params are only valid during the call), a sync.Pool sized 3 (1/2/3 params) can be used, releasing in a `defer`. But: `fast(w, r, fps)` is a function pointer that can let anything escape. If the handler spawns a goroutine holding `ps`, the pool hands `ps` to another goroutine.

**Mitigation:** document that ps are valid only for the call (already done) + zero `fps` on pool put. The caller can `copy()` if it wants to persist them.

### H4 — SIMD-style `getValue` with uint64 reads
For strings ≤ 8 bytes, we can compare with a single `uint64` load via `unsafe`. Strings ≤ 16 bytes can use 2 loads. For the common case (short segments such as "users", "list", etc.), this can be dramatically faster than `memequal`.

```go
//go:nosplit
//go:nocheckptr
func stringEq8(a, b string) bool {
    // Both same len (cheaper to check first), and len<=8
    if *(*uint64)(unsafe.Pointer(unsafe.StringData(a))) == *(*uint64)(unsafe.Pointer(unsafe.StringData(b))) {
        // BUT we need to mask out beyond-len bits
    }
}
```

**Complication:** strings with len < 8 may have "garbage" in the non-string bytes (no — in Go, strings have non-deterministic trailing zero/garbage). A len-based mask is needed.

**Viability:** high for strings of known length (common segments: 1-15 chars). Risk: using `unsafe.StringData` is stable since Go 1.20.

### H5 — Pre-compute path segment hashes
Each node in the tree could hold a `hash uint64` computed at registration time. At runtime, we hash the segment being looked up and compare hashes first; only if they are equal do we perform the string compare (for false positives).

**Trade-off:** computing the hash costs CPU; it only pays off if it avoids many `memequal` calls.

**Viability:** moderate. Probably not worth it in most cases because `memequal` for strings ≤ 16 bytes is already very fast.

### H6 — Static `RedirectTSL` cache
`target := (&url.URL{...}).String()` allocates 2x. It can be built manually:
```go
// No intermediate allocs
var sb strings.Builder
sb.Grow(len(newPath) + 1 + len(r.URL.RawQuery))
sb.WriteString(newPath)
if r.URL.RawQuery != "" {
    sb.WriteByte('?')
    sb.WriteString(r.URL.RawQuery)
}
target := sb.String()
```

There is still 1 alloc for the string, but it eliminates the `url.URL{}` struct. Gain ~50%.

Another point: **`wrapMiddleware` on every redirect** — it can be cached. Build the `redirectHandler` once in `frozenConfigSlow()`. Substantially cheaper.

### H7 — Inline `paramsBuf.add`
Already inlined. Skip.

### H8 — Eliminate the `cfg.hasPanicHandler` branch in ServeHTTP
Already zero-cost because `cfg.hasPanicHandler` is an inline bool in `cfg`. The branch is predicted-static (always false in most configs).

---

## 8. Preliminary priority (to be refined with the agents' input)

| ID | Optimisation | Estimated gain | Risk | Complexity |
|---|---|---|---|---|
| **A** | `RedirectTSL` rewrite (cache + manual builder) | -1000+ ns / -10 allocs on the redirect path | Low | S |
| **B** | `make(Params, n)` pool for FastHandler | -5ns / -1 alloc (32-96B) on Fast routes | Medium (the doc already says so) | S |
| **C** | `MethodNotAllowed` rewrite (fewer allocs in the handler) | -300 ns / -4 allocs | Low | S |
| **D** | `sync.Pool` reqBundle1/2/3 (PENDING security review) | -50ns / -1 alloc on stdlib param routes | **HIGH** | M |
| **E** | uint64 string compare in prefixMatch | -10ns on param routes | Medium (unsafe) | M |
| **F** | Pre-build the redirect handler in `frozenConfigSlow` | -200ns on the redirect path | Low | S |
| **G** | Eliminate `make(Params)` at mux.go:898 by reusing ps.buf via a stack-aware path | -5ns / -1 alloc on FastParam | Low (analysis already exists) | M |
| **H** | Reorder `methodIdx` to put GET first | -1ns overall | Low | XS |

---

## 9. Next steps

1. ⏳ Wait for the reports from the 3 specialised agents:
   - `hotpath_analysis.md` (go-perf-optimizer #1 — getValue + paramsBuf + bundles)
   - `competitor_techniques.md` (benchmark-elite-tester — competitors' techniques)
   - `middleware_analysis.md` (go-perf-optimizer #3 — 18 middlewares + chains)
2. Consolidate everything into the **final prioritised matrix** (task #12)
3. Present the plan to the user for implementation
