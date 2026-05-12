# Hot-Path Performance Analysis — MuxMaster
**Date:** 2026-05-12  
**Branch:** perf/maximize-performance  
**Toolchain:** Go 1.26.2, amd64 (AMD Ryzen 9 5900HX)  
**Profile source:** `reports/perf-audit-2026-05-12/` (cpu.prof, mem.prof, 10-run baseline)

---

## Baseline (10-run median, this session)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| StaticRoute | 25.6 | 0 | 0 |
| ParamRoute1 | 121.0 | 416 | 1 |
| ParamRoute2 | 142.2 | 448 | 1 |
| ParamRoute3 | 147.6 | 480 | 1 |
| WildcardRoute | 123.4 | 416 | 1 |
| NotFound | 334.9 | 117 | 3 |
| FastStaticRoute | 26.8 | 0 | 0 |
| FastParamRoute1 | 52.5 | 32 | 1 |
| FastParamRoute2 | 95.0 ±25% | 64 | 1 |
| FastParamRoute3 | 78.0 ±63% | 96 | 1 |
| ParallelStaticRoute | 4.0 | 0 | 0 |
| ParallelParamRoute | 113.2 | 416 | 1 |
| FastParallelParamRoute | 15.9 | 32 | 1 |

---

## 1. `getValue` Line-by-Line Analysis (`tree.go:442–578`)

**CPU share:** 28.19% cumulative (4.08s flat / 6.70s cumulative in the profile).

### Inlining status

`getValue` has compiler cost **977** against a budget of **80** — it cannot be inlined. Every call from `dispatch` (two call sites) and from `hasHandler` (one call site) carries the full function-call frame setup overhead.

### Per-line cost breakdown (from `hot_path_listing.txt`)

| Line | Operation | Flat time | Observation |
|---|---|---|---|
| 445 | `prefix := n.path` | 290 ms | Load of two words (ptr+len) from CL0 of node. Cheap but appears in every iteration. |
| 447 | `len(path) > len(prefix)` | 280 ms | Length comparison — inexpensive, but unavoidable. |
| 448 | `prefixMatch(path[:len(prefix)], prefix, ci)` | **1.99s cum** | Dominates getValue. Detailed below. |
| 451 | `path = path[len(prefix):]` | 620 ms | Slice reslice: 2 words written. High count because it runs on every loop iteration including deep param paths. |
| 455 | `children := n.children[:len(n.indices)]` | 290 ms | Slice header construction — bounds check on indices length. |
| 457 | `foldEq(c, n.indices[j], ci)` | 280 ms | Called per child in the linear scan. Inlined but still a branch + load per child. |
| 459 | `continue walk` | 240 ms | Successful child match — sets `n` and restarts loop. |
| 469 | `n = n.children[len(n.children)-1]` | 110 ms | Wildchild dereference; only on routes with params. |
| 472 | `case param:` | 230 ms | Switch dispatch into param arm. |
| 482 | `params.add(...)` | 380 ms cum | Inlined `paramsBuf.add`: bounds check + struct field write. |
| 539 | `case wildcard: ... params.add(...)` | 280 ms | Wildcard arm. |
| 552 | `prefixMatch(path, prefix, ci)` (exact match) | 450 ms cum | Redundant call: same comparison as line 448 but for the terminal node where `len(path)==len(prefix)`. |

### Key structural observations

**Double `prefixMatch` call.** For a node where `len(path) == len(prefix)` (the terminal match), the code reaches line 552 and calls `prefixMatch` again after already having checked line 448. At line 448 the test is `len(path) > len(prefix)`, so when path equals prefix the `if` at line 447 is skipped, and the code falls to line 552. This second call re-compares the same strings. For the static route common case (one node, exact match), this is the dominant `prefixMatch` invocation.

**`path = path[len(prefix):]` as performance dominator.** At 620 ms flat, this reslice runs on every loop iteration. It is a two-word store and is unavoidable for correct traversal. No optimization opportunity.

**`children := n.children[:len(n.indices)]` slice construction.** At 290 ms flat, this builds a slice header to restrict the children scan to only indexed children. A minor optimization: use `n.indices` length directly in the loop bound without constructing the intermediate slice. The current code `for j := range len(n.indices)` already uses the integer bound, but `children := n.children[:len(n.indices)]` runs first and writes a slice header unnecessarily when `len(n.indices)` could be cached in a register.

### Optimisation opportunities in getValue

**O1 — Split getValue into inline-eligible static-only fast path + `getValueFull` slow path.**

The static-route path through getValue is approximately:
1. Load `n.path`, compare lengths (2 ops).
2. `prefixMatch` with `ci=false` — reduces to `s == prefix` (1 string compare).
3. Reslice `path`.
4. Load `n.indices[0]`, compare with `path[0]` (1 byte compare).
5. Set `n = children[0]`, continue.
6. Eventually land on exact-match arm (line 552): `prefixMatch` again, load `n.handler`, return.

This path involves no wildchild, no switch, no param arm. Extracted to a separate function it would be ~30–40 AST nodes, within the inline budget of 80. Every static request (currently 47M op/s = 25ns) would avoid the function-call overhead of the current 977-node getValue.

**O2 — Merge the two `prefixMatch` calls for the terminal case.**

Currently `prefixMatch` is called at line 448 (condition `len(path) > len(prefix)`) and again at line 552 (condition `len(path) == len(prefix)`). These two branches are mutually exclusive. The code between them is only the wildchild/param arm (which returns early). The second call at line 552 is redundant when control reaches it from the `len(path) == len(prefix)` path: by definition `path == prefix` is the only remaining case that can succeed. The `prefixMatch` on line 552 with `ci=false` reduces to `path == prefix` which is already known to be the only case (since we already tested `len(path) > len(prefix)` is false). This second call costs 450 ms cumulative; it can be replaced with a direct `if ci` guard only calling `foldEq` byte-loop for the case-insensitive path, and elided entirely for the `ci=false` path.

**O3 — Avoid building `children` slice header inside the loop.**

Line 455: `children := n.children[:len(n.indices)]`. This writes a slice header (ptr+len+cap = 3 words) every loop iteration. Since the loop at line 456 already uses `range len(n.indices)` as its bound, `children[j]` is always in-bounds. The `children` slice header construction is redundant — the loop could read `n.children[j]` directly, eliminating 3 writes per loop iteration. The compiler may already optimise this away in some cases (it is inside the loop), but given 290 ms flat for line 455 it is not currently being eliminated.

---

## 2. `prefixMatch` and `foldEq` (`tree.go:583–610`)

### Current implementation

```go
func prefixMatch(s, prefix string, ci bool) bool {
    if !ci {
        return s == prefix  // fast path: direct string equality
    }
    // ci=true: byte-by-byte folding
    if len(s) != len(prefix) { return false }
    for i := range len(s) {
        if !foldEq(s[i], prefix[i], ci) { return false }
    }
    return true
}
```

### Analysis

**The `ci=false` branch is taken on >99% of requests** (CaseInsensitive defaults to false and is rarely enabled). The `if !ci { return s == prefix }` is a single branch (predicted-taken) plus the Go runtime string comparison which already uses SSE4.2 `pcmpestri` for strings of length ≥16 and word-level comparison for shorter strings. This is already near-optimal.

**`ci` is passed as a boolean parameter** on every call. Even though `ci` is constant for the lifetime of a request (it comes from `cfg.caseInsensitive`, frozen at first ServeHTTP call), the compiler cannot constant-fold it because `getValue` is not inlined and the boolean is passed through the call chain. If getValue were split into `getValueCI` / `getValueNonCI` variants dispatched once in `dispatch`, the `ci` branch would be compiled away entirely. This is speculative and would double the code size.

**uint64 comparison hack for 8-byte prefixes.** Replacing the string equality `s == prefix` with `*(*uint64)(unsafe.Pointer(&s))...` is NOT safe: Go string data backing arrays are not guaranteed to be NUL-padded to 8-byte boundaries, and out-of-bounds reads trigger undefined behaviour (and potential security issues). The compiler's existing string compare already uses word-level loads when length allows — an explicit uint64 trick offers no advantage and introduces unsafe code risk.

**Inlining:** `prefixMatch` (cost=69) and `foldEq` (cost=33) are both already inlined at their call sites. No further inlining gain available.

### Proposed optimisation

**O4 — Dedicated `prefixEq(s, prefix string) bool` without the `ci` parameter** called from the hot static-route path.

```go
// prefixEq is a zero-overhead alias for the ci=false path.
// Avoids the branch `if !ci` on every call in the common case.
func prefixEq(s, prefix string) bool { return s == prefix }
```

This eliminates one branch per call, but the branch is already strongly predicted (taken). Estimated saving: **<1 ns/op**. Complexity: S. Worth doing as cleanup but not a meaningful performance win.

---

## 3. `methodIdx` (`mux.go:76–101`)

### Profile data

1.43% of total CPU time. Already inlined at call site (confirmed: `inlining call to methodIdx` in compiler output). Cost = 45 (well within budget).

### Assembly structure

The compiler generates length-first dispatch:
- `CMPQ BX, $4` → JGT (branches to len>4 cluster: PATCH, DELETE, OPTIONS, CONNECT, TRACE)
- len=3: `CMPW (AX), $17735` + `CMPB 2(AX), $84` → matches GET (0x47/0x45/0x54)
- len=4: word compare for HEAD (0x48/0x45/0x41/0x44) and POST (0x50/0x4F/0x53/0x54)
- len=3: PUT
- etc.

The compiler already uses multi-byte string comparison. For GET (most common, len=3): 3 comparison instructions. This is already close to the theoretical minimum.

### uint64 packing approach

Interpreting r.Method as a uint64 via `*(*uint64)(unsafe.Pointer(&m[0]))` and comparing to precomputed constants would require reading up to 8 bytes from a string that may be shorter than 8 bytes. The string backing array is allocated by net/http and is not guaranteed to have 8-byte padding. This approach is **INVIÁVEL** (unsafe without controlled string allocation).

### Verdict

**No actionable optimisation.** The 1.43% share at 16-core parallel execution is ~0.36 ns/op per physical core. The current implementation is already optimal for a string switch.

---

## 4. `dispatchParams1Fast` and `dispatchWithParams` (`params.go:246–339`)

### Allocation anatomy

Every param request allocates exactly **1 heap object** (the tiered reqBundle). The allocation breakdown per tier:

| Tier | Type | Size | GC size class | Benchmarks |
|---|---|---|---|---|
| 1 param | `reqBundle1` | 392 B | 416 B | ParamRoute1, WildcardRoute |
| 2 params | `reqBundle2` | 424 B | 448 B | ParamRoute2 |
| 3 params | `reqBundle` | 456 B | 480 B | ParamRoute3 |
| Fast 1 param | `make(Params, 1)` | 32 B | 32 B | FastParamRoute1 |
| Fast 2 params | `make(Params, 2)` | 64 B | 64 B | FastParamRoute2 |
| Fast 3 params | `make(Params, 3)` | 96 B | 96 B | FastParamRoute3 |

### Profile breakdown for `dispatchParams1Fast` (line-by-line)

| Line | Operation | Flat | Cumulative | Analysis |
|---|---|---|---|---|
| 247 | `b := &reqBundle1{}` | — | **1.95s** | `mallocgcSmallScanNoHeader` for 416B object. Includes zeroing (memclrNoHeapPointers). |
| 248 | `b.ctx.Context = r.Context()` | 290ms | 360ms | stdlib method call `r.Context()` (not inlined from our code). Includes nil check + potential `context.Background()` fallback. |
| 252 | `b.req = *r` | 210ms | **530ms** | 304B struct copy. GC `bulkBarrierPreWrite` (0.57s total in profile) is triggered here for the ~17 pointer fields in http.Request. |
| 253 | `setReqCtxUnsafe(&b.req, &b.ctx)` | — | 10ms | Single pointer write via pre-computed offset. Already inlined (cost=6). |
| 254 | `h.ServeHTTP(w, &b.req)` | — | 30ms | Interface dispatch to nopHandler in benchmarks. |

### Can `sync.Pool` be used for reqBundle?

**NO** for the current stdlib-handler path. Reasoning:

The reqBundle lifetime extends beyond `dispatchParams1Fast` — `h.ServeHTTP` receives `&b.req`, which has `b.ctx` embedded in it. The handler (and any middleware it calls) may spawn goroutines that access `r.Context()`, which reads `b.ctx`. A goroutine created in the handler outlives `ServeHTTP`. If the bundle were pooled and returned on `ServeHTTP` return, and then reused for another request, the goroutine from the first request would observe the new request's context — a severe data race.

The CSA-001 audit confirmed: any pooling that puts the bundle back before all goroutines spawned in the handler have completed is unsafe. The GC-managed allocation (current design) is the correct tradeoff.

**For the FastHandler path only (`fps make(Params, n)`):** A pool IS theoretically safe IF the FastHandler lifetime contract (documented in handler.go) is enforced — `ps Params` must not be retained after the handler returns. However, enforcing this at runtime is impossible. The documentation exists but is not compile-time checked. A pool here would work in practice for well-written handlers but is a correctness footgun.

### Can we read `r.ctx` directly via unsafe instead of calling `r.Context()`?

**YES — SAFE, with caveats.**

`r.Context()` does:
```go
func (r *Request) Context() context.Context {
    if r.ctx != nil { return r.ctx }
    return context.Background()
}
```

The net/http server sets `req.ctx = ctx` at `server.go:1041` (confirmed) before calling `ServeHTTP`. `httptest.NewRequest` uses `NewRequestWithContext` which also sets ctx. Therefore `r.ctx` is **never nil** when MuxMaster's ServeHTTP is called from a real HTTP server or test.

The equivalent unsafe read:
```go
parentCtx := *(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxFieldOffset))
```

This uses the same `reqCtxFieldOffset` already computed in `params.go:init()` for `setReqCtxUnsafe`. The read is from the original `r` (not yet modified), before any concurrent access begins. Safety classification: **SEGURO**.

Estimated saving: **2–5 ns/op** (eliminates the `r.Context()` method call overhead: 1 indirect call + nil check + potential `context.Background()` path).

**Risk:** If `hasReqCtxField == false` (future Go version removed the field), the fallback would be absent. Mitigation: keep the existing `r.Context()` call in the `hasReqCtxField == false` branch (already present in dispatchParams1Safe).

### The `b.req = *r` copy cost

The 304B `http.Request` copy triggers `bulkBarrierPreWrite` for ~17 pointer fields. This costs ~2.4% of total CPU (0.57s in the profile).

**Can the copy be avoided?** No — the bundle must own an independent `*http.Request` so handlers can access all fields (Header, Body, URL, etc.) while the router's ctx override is invisible to them. Any approach that avoids copying (e.g., storing only `*r` plus a ctx delta) would require overriding the `Context()` method, which is only possible via an interface — breaking the `*http.Request` contract.

**Can the GC write barrier cost be reduced?** Not without unsafe surgery on the GC metadata or restructuring `http.Request` itself (impossible for a library). The current approach is already the minimum possible for stdlib-compatible param dispatch.

### `doDispatch1` function pointer overhead

`doDispatch1` is an `init()`-selected function pointer dispatched indirectly (indirect CALL). The indirect branch is always predicted correctly after the first ~10 calls (branch target predictor learns the constant target). Overhead: negligible (<0.5 ns/op in steady state). **No actionable optimisation.**

### `dispatchWithParams` as a non-inlineable wrapper

`dispatchWithParams` (cost=457) cannot be inlined. It exists to route the 1/2/3+ param cases to the appropriate bundle tier. For the 1-param case (most common REST case), the call chain is:

```
dispatch → dispatchWithParams → doDispatch1 → dispatchParams1Fast
```

Three function calls for the common case. An inline-eligible fast path for the 1-param case in `dispatch` (eliminating the `dispatchWithParams` wrapper) would save ~5–10 ns/op.

**O5 — Inline the 1-param dispatch directly in `dispatch`.**

When `ps.count == 1`, instead of calling `dispatchWithParams` (which switches on `len(pslice)` and calls `doDispatch1`), call `doDispatch1` directly from `dispatch`:

```go
if ps.count == 1 {
    doDispatch1(w, r, handler, pattern, ps.buf[0])
    return
}
```

This saves one function call (dispatchWithParams wrapper) and one switch. Estimated saving: **3–7 ns/op** for ParamRoute1. Complexity: S. Unsafe: NO.

---

## 5. `paramsBuf` Zeroing Cost

### Size verification

```
sizeof(paramsBuf) = 128 bytes
  count     int      = 8B
  buf       [3]Param = 96B (3 × 32B)
  overflow  []Param  = 24B (slice header)
```

Note: The comment in `mux.go:884` says "264 B of stack" — this is stale from a previous implementation. The actual size is **128 bytes**.

### Current behaviour

`var ps paramsBuf` at line 885 is declared inside `if root != nil {}` but the Go compiler zeroes all local variables at entry of the enclosing function (via `memclrNoHeapPointers` or inline zeroing). The zeroing is **unconditional at function entry** even for static routes, because:

1. The `paramsBuf` struct is declared inside the `if root != nil {}` block.
2. However, `root` is derived from the atomically loaded `trees` pointer — the compiler cannot prove at compile time that `root` is nil, so it must keep the variable live from its declaration point.
3. `var ps2 paramsBuf` at line 980 (inside `if starRoot != nil {}`) has the same issue.

**For mixed static+param trees** (the common case in newBenchMux which has both `/users/list` and `/users/:id`): `root.maxParams > 0`, so `psBuf = &ps` is set and the zeroing is both unavoidable and necessary. The zeroing cost of 128B is part of the static route's 25.6 ns/op.

**`memclrNoHeapPointers` at 3.74%** in the profile: ~0.89s of the 23.77s profile. Averaged across all benchmarks (most of which have param routes), this represents ~4–6 ns/op for each request on mixed trees.

### Can we reduce zeroing cost?

**O6 — Use `runtime.KeepAlive` pattern to defer zeroing past the fast path (NOT applicable to struct vars).**

This pattern does not apply here. Stack variable zeroing cannot be deferred.

**O7 — Split the tree into "static-only" and "has-params" subtrees.**

If every route tree were statically known to be param-free (maxParams == 0), `var ps paramsBuf` would never be instantiated. But in the common case (mixed API routes), the entire tree has `maxParams > 0`.

**More realistic:** The `paramsBuf` struct could be shrunk. The `overflow []Param` slice header (24B) is only needed for routes with >3 params, which cover a negligible fraction. However, removing it would require a different overflow strategy.

**O8 — `[3]Param` array in `paramsBuf` does not need to be zeroed when `psBuf == nil`.**

The current code: `var ps paramsBuf; if root.maxParams > 0 { psBuf = &ps }`. When `root.maxParams == 0`, `psBuf` stays nil and `ps` is never written by `getValue`. Therefore zeroing `ps` before the check is unnecessary for static-only trees.

Possible: restructure to `var ps paramsBuf` inside the `if root.maxParams > 0 {}` block to hint to the compiler that it's only needed conditionally. BUT: `ps.count` is read at line 893 (`if ps.count > 0`) even when `psBuf` was nil — so `ps` must stay in scope after the `getValue` call. The compiler keeps it live regardless.

**Verdict:** The 128B zeroing is largely unavoidable for the current design on mixed trees. The only structural solution would be to move `var ps paramsBuf` into a sub-function that is only called for param routes, returning a pointer. This would cause heap escape. Not worth it.

---

## 6. NotFound Path (117 B, 3 allocs)

### Root cause analysis

From pprof `-alloc_objects`:
```
65.69%  net/textproto.MIMEHeader.Set   (30.8M allocs)
34.00%  net/http.Error                 (16.0M allocs)
```

The 3 allocations are **entirely from `net/http.Error`** (the default `http.NotFound` handler), NOT from MuxMaster:

1. `h.Set("Content-Type", "text/plain; charset=utf-8")` → allocates `[]string{"text/plain; charset=utf-8"}` (1 alloc, ~48B)
2. `h.Set("X-Content-Type-Options", "nosniff")` → allocates `[]string{"nosniff"}` (1 alloc, ~32B)  
3. `fmt.Fprintln(w, error)` → allocates variadic `[]interface{}` + string escape (1 alloc, ~32B)

**MuxMaster's contribution to NotFound allocs: 0.**

Verification: `BenchmarkNotFoundCustomHandler` (custom nopHandler as NotFound) shows **0 allocs, ~25 ns/op** — the same as static routes.

The `lazyNotFoundPtr` cache is working correctly: the handler is built once and served atomically on every subsequent call. The 334.9 ns/op for NotFound vs. 25.6 ns/op for static routes is entirely the cost of `http.Error` (2 header Set calls + fmt.Fprintln + ResponseRecorder.WriteHeader).

**No MuxMaster optimisation possible here.** The 3 allocs and ~310 ns overhead are intrinsic to `http.NotFound`. If the benchmark used a custom NotFound handler that writes a pre-built response, it would be 0 allocs.

---

## 7. FastParamRoute2/3 Variance Analysis

### Observed data

```
BenchmarkFastParamRoute2 (default -cpu=16):  72 ns – 488 ns/op  (±63%)
BenchmarkFastParamRoute3 (default -cpu=16):  90 ns – 638 ns/op  (bimodal)
With GOGC=off, -cpu=16:                      93 – 99 ns/op       (±3%, stable)
With -cpu=1:                                 89 – 96 ns/op       (±5%, stable)
```

### Root cause: GC-triggered stop-the-world pauses

The FastHandler path allocates `make(Params, n)` on every request:
- FastParamRoute2: 64 B/op × 14–17M ops/s = **~960–1088 MB/s** allocation rate (16 goroutines)
- FastParamRoute3: 96 B/op × 12–15M ops/s = **~1152–1440 MB/s** allocation rate

The default GOGC=100 triggers a GC cycle when the live heap doubles. With a typical initial heap of ~8 MB (tests binary), GC triggers roughly every ~8 MB of allocation. At 1 GB/s allocation rate, that is a GC trigger every ~8 ms. A stop-the-world mark-phase pause of even 100–500 μs every 8 ms inflates 6 consecutive benchmark samples from ~80 ns to ~480 ns.

This is NOT a branch misprediction, NOT false sharing, NOT cache thrashing. The bimodal distribution (fast mode + slow mode) is the classical GC pause signature.

### Impact on benchstat reliability

The high coefficient of variation makes `-count=10` insufficient for `benchstat` significance testing at 5% confidence. For these specific benchmarks, `-count=30` or using `GOGC=200` during benchmarking would produce statistically reliable baselines.

### Fix options

**For benchmarking:** Run with `GOGC=200` or `GOGC=off` and note it in the benchmark header. The "true" performance (no GC) is ~93 ns (FastParamRoute2) and ~107 ns (FastParamRoute3) at 16 cores.

**For production:** The allocation is unavoidable with the current `make(Params, n)` design. A `sync.Pool` for Params slices would eliminate GC pressure on the FastHandler path — at the documented safety cost described in §4. Alternatively, a pre-sized `[maxParams]Param` array could be passed by value (avoiding heap allocation entirely), but this requires changing the FastHandler signature (`func(w, r, [3]Param, int)`) — a public API break.

**O9 — Optional sync.Pool for FastHandler Params** (conditional, with clear documentation):

```go
// fastParamsPool recycles Params backing arrays for FastHandler routes.
// SAFETY: handlers MUST NOT retain ps after the handler returns.
var fastParamsPool sync.Pool

func getFastParams(n int) Params {
    if p, ok := fastParamsPool.Get().(Params); ok && cap(p) >= n {
        return p[:n]
    }
    return make(Params, n)
}
func putFastParams(ps Params) { fastParamsPool.Put(ps[:0]) }
```

This would reduce allocs/op for FastParam routes to 0 and eliminate GC-triggered variance entirely. Complexity: S. Risk: SEGURO for handlers that respect the lifetime contract; a footgun for those that don't.

---

## 8. `dispatch` Stack Frame Size

`dispatch` has a **1008-byte stack frame** (`locals=0x3f0`). Dominant contributors:

| Variable | Size | Notes |
|---|---|---|
| `var ps paramsBuf` | 128 B | Always live; see §5. |
| `var ps2 paramsBuf` | 128 B | Live only when `starRoot != nil` (wildcard tree). |
| Spilled registers, outgoing args, temporaries | ~752 B | Compiler-generated for the 5009-byte function body. |

A 1008-byte frame costs a stack growth check at entry (`morestack` call if < 1008 B remaining). On Linux with GOMAXPROCS=16, each goroutine gets a 2KB initial stack. A 1008-byte frame is feasible without growth, but in practice the benchmark goroutines have calling frames above dispatch (testing.B, ServeHTTP), leaving <800 B free. This can trigger `morestack` on some goroutines.

**O10 — Move `var ps2 paramsBuf` into a separate function `dispatchWildcard`.**

```go
// Only called when the method tree missed and a wildcard tree exists.
//go:noinline
func (m *Mux) dispatchWildcard(w http.ResponseWriter, r *http.Request,
    urlPath string, trees *methodTrees, cfg *muxConfig) bool {
    starRoot := trees[idxWild]
    if starRoot == nil { return false }
    var ps2 paramsBuf
    ...
}
```

This moves 128 B off the dispatch frame for the common path (method tree hit). Estimated saving: **2–4 ns/op** on static and param routes (smaller stack = fewer stack-growth checks). Complexity: M. Unsafe: NO.

---

## 9. Node Struct Cache Line Layout

### Current layout (confirmed from assembly)

```
sizeof(node) = 112 bytes (1.75 cache lines)

CL0 (bytes 0–63):
  path     string       [0..15]  — hot: read on every node visit
  handler  http.Handler [16..31] — hot: checked on leaf match
  indices  string       [32..47] — hot: read for child scan
  children []*node      [48..71] — crosses CL0/CL1 boundary!

CL1 (bytes 64–127):
  children ptr part     [64..71] — (length word of children slice)
  fast     FastHandler  [72..87] — cold: only for fast routes
  pattern  string       [88..103] — cold: only on match
  priority uint32       [104..107] — cold: registration only
  nType    nodeType     [108]    — hot: switch dispatch!
  wildChild bool        [109]    — hot: wildchild check!
  ...
```

**Critical issue:** `nType` (line 471 switch) and `wildChild` (line 463 check) are in **CL1**, accessed after loading CL0 fields. For static routes this is a second cache miss. For the `case param` branch (230 ms flat in profile), the CL1 load is required.

**O11 — Move `nType` and `wildChild` into CL0.**

To bring `nType` and `wildChild` into CL0, we need to make room. Options:
1. Move `handler http.Handler` to CL1: saves 16B in CL0. Then `nType`(1B) + `wildChild`(1B) fit in CL0 at bytes 16–17. But `handler` is read on every leaf match — moving it to CL1 costs a cache miss on the match arm.
2. Replace `handler http.Handler` (16B interface) with a pointer-only field if `fast` is always nil for Handle routes and `handler` is always nil for HandleFast routes — but both coexist in the current struct.
3. Use a packed uint8 flags field at byte 63 (end of CL0): `flags = nType | (wildChild<<4)`. Saves 1 byte for nType and packs wildChild alongside priority.

The current layout was deliberately tuned for "successful static route match reads only path + handler — both in CL0" (comment at line 69). Moving `nType` into CL0 would require evicting either `handler` or part of `children`, both of which are read on fast paths. This is a layout tradeoff with no clear winner.

**Verdict:** Current layout is a deliberate optimisation for static routes. The `nType` CL1 access is only on param/wildcard paths, which already allocate (1 alloc). The cold-miss cost (~5 ns for a DRAM miss) is already baked into the 121 ns/op for ParamRoute1. Moving `nType` to CL0 would require evicting `handler` to CL1, increasing the static route miss rate. **No change recommended** without measurement on a cache-hierarchy simulator.

---

## Prioritised Optimisation Table

| # | Hotspot | Current | Opportunity | Est. gain | Unsafe | Complexity | Priority |
|---|---|---|---|---|---|---|---|
| O5 | Inline 1-param dispatch in `dispatch` (bypass `dispatchWithParams` call) | 121 ns | Direct `doDispatch1` call | **5–10 ns/op** ParamRoute1 | NO | S | **P0** |
| O1 | Split `getValue` into inline-eligible static fast path + `getValueFull` | getValue=977 cost, not inlined | ~30-node static path, inlineable | **3–8 ns/op** static | NO | M | **P0** |
| O2 | Merge double `prefixMatch` calls (terminal node) | 2×prefixMatch per terminal | 1×prefixMatch or direct compare | **2–4 ns/op** all routes | NO | S | **P1** |
| O9 | `sync.Pool` for FastHandler Params slices | 1 alloc/op, ±63% variance | 0 allocs, stable variance | **30–50 ns/op** FastParam2/3, eliminates GC pauses | NO | S | **P1** |
| O5a | Read `r.ctx` via unsafe instead of `r.Context()` | 1 method call per param req | Direct unsafe load | **2–5 ns/op** all param routes | SEGURO | S | **P1** |
| O10 | Move `var ps2 paramsBuf` to `dispatchWildcard` function | 1008B frame | ~880B frame | **2–4 ns/op** all routes | NO | M | **P2** |
| O3 | Remove `children := n.children[:...]` slice header inside `getValue` loop | 3 writes/iter | Direct `n.children[j]` | **1–2 ns/op** all routes | NO | S | **P2** |
| O4 | `prefixEq(s, prefix string)` without ci parameter on static path | 1 branch/call | 0 branches | **<1 ns/op** | NO | S | **P3** |
| O8 | Defer `paramsBuf` zeroing past `maxParams==0` check | 128B zero on every req | Conditional zero | **1–3 ns/op** static-only trees only | NO | S | **P3** |
| O11 | Node struct cache line layout: `nType`/`wildChild` into CL0 | CL1 access for nType | CL0 access, but evicts handler | **ambiguous** | NO | L | MEASURE FIRST |

---

## Starting Point Recommendation

**Implement O5 first** (inline 1-param dispatch in `dispatch`). It is:
- Smallest change: a single `if ps.count == 1 { doDispatch1(...); return }` before the existing `dispatchWithParams` call, using `ps.buf[0]` directly (no intermediate `ps.params()` call needed).
- Zero risk: no unsafe, no API change, no new dependencies.
- Highest expected gain for the most common REST API pattern (single :id parameter).

**Implement O1 second** (split `getValue` fast path). This is more complex but addresses the largest single CPU contributor (28% cumulative). The static route path through getValue is well-understood and the split would also allow the compiler to inline the fast path into `dispatch`, eliminating the function call overhead entirely for static routes.

**Implement O9 third** (FastHandler Params pool). Addresses the extreme variance in FastParam benchmarks and would bring FastParamRoute2/3 allocs to 0, matching the theoretical minimum. The documentation and lifetime contract for FastHandler already exists; the pool adds the implementation.

---

## Unsafe Usage Summary

| Technique | Current usage | Proposed new usage | Safety classification |
|---|---|---|---|
| `setReqCtxUnsafe`: write r.ctx via reflect-computed offset | In use (dispatchParams1Fast) | No change | SEGURO |
| Read r.ctx via same offset (O5a) | Not in use | Add parallel read path | SEGURO — same field, same offset, r always has ctx set |
| uint64 prefix compare for methodIdx | Not proposed | Rejected | INVIÁVEL — bounds safety |
| uint64 prefix compare in prefixMatch | Not proposed | Rejected | INVIÁVEL — bounds safety |
| `sync.Pool` for FastHandler Params | Not in use | O9 — optional | RISCO — handler lifetime contract violation possible; document clearly |

---

## Appendix: Key Compiler Decisions Confirmed

```
getValue:             cannot inline, cost=977 >> budget=80
dispatchWithParams:   cannot inline, cost=457 >> budget=80
dispatchParams1Fast:  cannot inline, cost=130 >> budget=80
ServeHTTP:            cannot inline, cost=296 >> budget=80
dispatch:             cannot inline, cost=2872 >> budget=80

prefixMatch:          can inline, cost=69   ✓  (inlined at 3 call sites)
foldEq:               can inline, cost=33   ✓  (inlined at 4 call sites)
methodIdx:            can inline, cost=45   ✓  (inlined at dispatch call site)
paramsBuf.add:        can inline, cost=31   ✓  (inlined at 3 call sites in getValue)
paramsBuf.params:     can inline, cost=35   ✓  (inlined at dispatch and introspection)
setReqCtxUnsafe:      can inline, cost=6    ✓  (inlined at dispatchWithParams)
config:               can inline, cost=N/A  ✓  (fast path: load non-nil atomic)
```
