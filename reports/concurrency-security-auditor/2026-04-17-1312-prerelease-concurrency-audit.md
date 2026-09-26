# Concurrency Security Audit — Pre-release v1.0.0

Date: 2026-04-17T12:12:59Z
Commit: 533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c
Go: 1.26.2 linux/amd64
GOMAXPROCS: 16 (16 cores available)
Race detector: enabled (`-race` flag)
Auditor: concurrency-security-auditor
Sprint: `2026-04-17-sprint.md`

---

## 0. Executive summary

The exhaustive `-race` audit of MuxMaster identified **4 Critical findings**, **3 High findings**, **1 Medium finding** and **2 Low findings**. Three classes of data race were confirmed by the race detector with full stack traces. The `sync.Pool` for `requestCtx` is correctly isolated against functional contamination via PathParam / ParamsFromContext — zero canaries leak. However, the hot-path design (lines `mux.go:464-480` and `mux.go:521-537`) assumes exclusive ownership of the `*http.Request` by the dispatcher goroutine; handlers that spawn goroutines and retain `r` produce an immediate data race, confirmed by the race detector.

**Recommendation:** The release must NOT be tagged as `v1.0.0` before:
1. CSA-001 is fixed OR the condition is documented normatively with a linter rule.
2. CSA-002 is fixed (trivial: move `Use()` and `Pre()` inside `m.mu`).
3. CSA-003 is fixed OR `Walk`/`Routes`/`Lookup` are explicitly protected by `m.mu.RLock()`.
4. CSA-004 and CSA-005 are remediated with a correct `defer` on the param-route path.

## 1. Shared state enumeration

Exhaustive enumeration of the entire shared-state surface reachable from the hot path or the public API.

| # | Name | Kind | Access (R/W) | Current synchronisation | Lifetime | Status |
|---|---|---|---|---|---|---|
| 1 | `Mux.treesPtr` | `atomic.Pointer[methodTrees]` | R per req (dispatch), W per registration (Handle, mountAt) | atomic | module | **SAFE** — COW of the *array*; each Store publishes a new pointer |
| 2 | `*node` graph (children in each tree) | `[]*node`, string, http.Handler | R per req (getValue, walk, hasHandler), W per registration (addRoute) | **NONE** beyond `m.mu` in `Handle` | module | **UNSAFE vs introspection** — CSA-003 |
| 3 | `Mux.middleware` | `[]func(http.Handler) http.Handler` | R in `Handle`→`wrapMiddleware` (line 223, under `m.mu`), W in `Use` (line 176, **no lock**) | **partial** — only the reader holds the lock | module | **UNSAFE** — CSA-002 |
| 4 | `Mux.pre` | `[]func(http.Handler) http.Handler` | R+W in `Pre` (no lock, line 182) | **NONE** | module | **UNSAFE** — CSA-002 (variant) |
| 5 | `Mux.preHandler` | `http.Handler` | R per req (dispatchWithRecover, ServeHTTP), W in `Pre` (no lock) | **NONE** | module | **UNSAFE** — CSA-002 (variant) |
| 6 | `Mux.mu` | `sync.Mutex` | exclusive | — | module | **SAFE** — but does not cover Use/Pre/NotFound/etc. |
| 7 | `Mux.PanicHandler` | `func(w, r, any)` | R per req (ServeHTTP:407), W exclusive to the caller | **NONE** | module | **UNSAFE** if reassigned after start — CSA-006 |
| 8 | `Mux.NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `ErrorHandler` | `http.Handler` / func | R per req, W exclusive to the caller | **NONE** | module | **UNSAFE** if reassigned after start — CSA-006 |
| 9 | `Mux.RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS`, `CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode` | bool / int | R per req (dispatch), W exclusive to the caller | **NONE** | module | **UNSAFE** if reconfigured after start — CSA-006 |
| 10 | `reqCtxOffset` | `uintptr` | R per param-req (unsafe.Add), W in `init()` | init before the package is ready | module | **SAFE** — fixed at init |
| 11 | `rcPool` | `sync.Pool` | R+W per param-req | internal sync.Pool | module | **SAFE** — structure resistant to ghosts; returned objects de-synchronised with the GC |
| 12 | `*http.Request.ctx` (unexported field) | `context.Context` via `unsafe.Add` | W and restore in `dispatch:464-480` / `dispatch:521-537` | implicit (goroutine ownership) | request | **UNSAFE in the presence of spawn** — CSA-001 |
| 13 | `requestCtx` state (`rc.Context`, `rc.params`, `rc.pattern`, `rc.small[3]`) | struct | W in dispatch, R in the handler and in any goroutine that retains `r` | implicit | per-request until `releaseRC` | **UNSAFE in the presence of spawn** — combined CSA-001 |
| 14 | `throttle.tokens` | `chan struct{}` (buffered) | concurrent R+W per req | channel | middleware lifetime | **SAFE** — channel operations are atomic |
| 15 | `throttle.queue` | `chan struct{}` | same | channel | same | **SAFE** |
| 16 | `timeout.ctx` | `context.Context` | W in `context.WithTimeout`, R by any goroutine holding ctx | context package atomic | per-request | **SAFE** — but the handler may ignore cancel → CSA-008 |
| 17 | `recoverer.os.Stderr` | `*os.File` | W via `fmt.Fprintf` | kernel FD (atomic at line boundaries ≤ PIPE_BUF for pipes) | process | **SAFE** technically; **RISK** info leak (target of the middleware-reviewer) |

**Enumeration conclusion:** Of the 17 shared-state points, 6 are at risk of a race under legitimate or documented use, and a further 2 (17 boolean/handler fields) are at risk under any post-start reassignment.

## 2. Race detector results

Harnesses in `/reports/concurrency-security-auditor/harness/`. Each test was run with `-race -count=1` for initial race capture; the passing tests were additionally run with `-count=10` for stability.

### 2.1 Tests with zero races — `-count=10` suite of 54.78s

| Test | Duration (1×) | Iterations | DATA RACE | Status |
|---|---|---|---|---|
| TestH018_ReqCtxOffsetAgreement | 0.00s | trivial | 0 | **PASS** |
| TestH023_PoolCanaryNoCrossRequestLeak | 0.77s | 256 000 req | 0 | **PASS** |
| TestH023_RcSmallResidueInvariant | 0.20s | 64 000 req | 0 | **PASS** |
| TestH001_HandlerPropagatesContextToGoroutine (safe pattern) | 0.14s | 6 400 req (goroutine per req) | 0 | **PASS** — documents the safe pattern |
| TestH001_ServeHTTPMassiveParallel_Race (no spawn) | 1.53s | 320 000 req | 0 | **PASS** |
| TestPanicWithoutHandler_PoolRelease | 0.33s | 40 000 req | 0 | **PASS** functionally; CSA-004/005 are observable only with specialised inspection |
| TestPanicWithHandler_PoolRelease | 0.93s | 100 000 req | 0 | **PASS** |
| TestPanicConcurrent_NoCrossLeak | 0.42s | 128 000 req | 0 | **PASS** |
| TestThrottleCounterRace | 0.58s | 2 048 req | 0 | **PASS** — peak=8, limit=8 (zero off-by-one) |
| TestRecovererConcurrentPanics | 0.78s | 16 000 req | 0 | **PASS** (verbose stderr but recovery OK) |
| TestContextCancellationPropagation | 0.01s | 1 req | 0 | **PASS** |
| TestServeHTTPSteadyState_NoGoroutineLeak | 0.22s | 48 000 req | 0 | **PASS** (delta=0 goroutines) |
| TestTimeoutGoroutineLeak | 0.36s | 256 req | 0 | **PASS** — but reveals during=258 (confirms that Timeout does not preempt; **CSA-008**) |

### 2.2 Tests with detected races (Critical)

| Test | DATA RACE count | Locations | Status |
|---|---|---|---|
| TestH001_HandlerGoroutineReadsRequestContext | 3 | mux.go:473, 476, 478 vs params.go:160, 164 | **FAIL** → CSA-001 |
| TestMiddlewareChainMutationRace | 2 | mux.go:223 / mux.go:640 vs mux.go:176 | **FAIL** → CSA-002 |
| TestH027_WalkVsHandleRace | 4+ | tree.go:90, 91, 96 vs tree.go:465, 468, 469 | **FAIL** → CSA-003 |
| TestPublicFieldAssignment_PanicHandler_Race | ≥1 | mux.go:407 vs mux.go:407 (assignment) | **FAIL** → CSA-006 |
| TestPublicFieldAssignment_NotFound_Race | ≥1 | mux.go:568 vs mux.go:568 (assignment) | **FAIL** → CSA-006 |
| TestPublicFieldAssignment_BoolFlag_Race | ≥1 | mux.go:491 etc. vs direct assignment | **FAIL** → CSA-006 |

**Total unique data races identified by the race detector: 9+** (distributed across 4 distinct families).

## 3. Pool contamination canary results

Ran the test mandated by the prompt (Step 2) with 256 000 concurrent iterations.

| Test | Iterations | Canary leaks | Status |
|---|---|---|---|
| TestH023_PoolCanaryNoCrossRequestLeak | 256 000 | **0** | **PASS** |
| TestH023_RcSmallResidueInvariant | 64 000 | 0 badLen / 0 badVal | **PASS** |

**Analysis:** the re-slice `rc.params = Params(rc.small[:copy(rc.small[:], pslice)])` in `mux.go:473` produces a view that ALWAYS has `len == count`. Even if `rc.small[1]` and `rc.small[2]` retain data from the previous request (they are not zeroed on release), the public API (`PathParam`, `ParamsFromContext`, `Params.Get/Lookup`) iterates over `rc.params[:count]` and never reads the residual slots. **H-023 is REFUTED** at the functional level.

**Residual security caveat:** if someone obtains the `*requestCtx` through `Value`/unsafe access and uses reflect to read `rc.small[2]`, they will see residue. This attack requires access to the pointer inside the process — outside the threat model exploitable cross-request via HTTP. Catalogued as documentation/hardening: write `zeroSmall(rc)` before `releaseRC` for defence-in-depth.

## 4. Goroutine leak profile

```
TestServeHTTPSteadyState_NoGoroutineLeak:
  48 000 ServeHTTP invocations, 32 concurrent workers
  before GC: 2 goroutines
  after  GC: 2 goroutines
  delta    : 0
  verdict  : MuxMaster ServeHTTP does not spawn goroutines internally

TestTimeoutGoroutineLeak:
  256 concurrent /slow requests with Timeout(10ms) + handler sleep 200ms
  before : 2
  during : 258 (= 2 + 256 handlers blocked)
  after  : 2
  verdict : handlers are NOT preempted on timeout — goroutines drain when the handler returns naturally. Current documentation already flags this limitation (H-017 refined).
```

## 5. Findings

### 5.1 Summary table

| ID | Severity | CWE | File:line | Title | Reproducer |
|---|---|---|---|---|---|
| CSA-001 | **Critical** | CWE-362 (Concurrent Execution using Shared Resource with Improper Synchronisation), CWE-367 (TOCTOU) | mux.go:464-480, 521-537 | Cross-goroutine race on `r.ctx` via `unsafe.Add` when a handler spawns a goroutine that retains `r` | `evidence/2026-04-17/CSA-001/repro_test.go` |
| CSA-002 | **Critical** | CWE-362, CWE-667 (Improper Locking) | mux.go:175-184 | `Use()` and `Pre()` mutate `m.middleware`/`m.pre`/`m.preHandler` without acquiring `m.mu`, racing against `Handle()`/`ServeHTTP` | `evidence/2026-04-17/CSA-002/repro_test.go` |
| CSA-003 | **Critical** | CWE-362, CWE-820 (Missing Synchronisation) | introspection.go:60-95 vs tree.go:63-155 | `Walk`/`Routes`/`Lookup` read nodes that `addRoute` mutates in place; no lock on the reader side | `evidence/2026-04-17/CSA-003/repro_test.go` |
| CSA-004 | **Critical** | CWE-404 (Improper Resource Shutdown), CWE-772 (Missing Release of Resource) | mux.go:466-480, 523-537 | Handler panic: `releaseRC(rc)` is never called → permanent rc leak; we observed ~5.4 KB/req allocation growth under panic storms | `evidence/2026-04-17/CSA-004/repro_test.go` |
| CSA-005 | **High** | CWE-662 (Improper Synchronisation), CWE-672 (Operation on a Resource after Expiration) | mux.go:474-476, 531-533 | Handler panic: `*origCtxPtr = origCtx` is never executed → `r.ctx` keeps pointing at the leaked rc after ServeHTTP returns via panic | `evidence/2026-04-17/CSA-005/repro_test.go` |
| CSA-006 | **High** | CWE-362 | mux.go:138-154, 109-136 | Public fields (`PanicHandler`, `NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `ErrorHandler`, 8 bool/int flags) are read per request without sync; any post-start reconfiguration is a race | `evidence/2026-04-17/public_fields_race_run1.txt` |
| CSA-007 | **High** | CWE-820 | mux.go:182-184 | `Pre()` rebuilds `m.preHandler` without a lock; if the program calls `Pre()` after another goroutine is already serving, the write publishes an `m.preHandler` without happens-before | (partial coverage by the `TestMiddlewareChainMutationRace` harness via `Use`) |
| CSA-008 | Medium | CWE-400 (Uncontrolled Resource Consumption) | middleware/timeout.go:14-20 | `Timeout` only cancels the context; the handler keeps running. 256 requests / 200ms handler produce 256 blocked goroutines. **H-017 confirmed**. | `evidence/2026-04-17/timeout_leak_run1.txt` |
| CSA-009 | Low | CWE-209 (Info Exposure via Error Message) | middleware/recoverer.go:16 | `fmt.Fprintf(os.Stderr, "panic: %v\n%s", rcv, debug.Stack())` — attacker-controlled panic value written raw to stderr. Cross-ref: owned by middleware-security-reviewer. | Stack logs in `middleware_throttle_recoverer_cancel_run1.txt` |
| CSA-010 | Low | CWE-453 (Insecure Default Variable Initialisation) | params.go:139-148 | `init()` in params.go iterates fields looking for `"ctx"` — if the field is renamed / removed, `reqCtxOffset = 0` silently. **H-018 partially refuted** (current value is correct on Go 1.26.2) **but latent**; explicit type validation is recommended. | `evidence/2026-04-17/h001_run1_full.txt` (H-018 passed) |

### 5.2 CSA-001 — Cross-goroutine race on `r.ctx` via `unsafe.Add`

**Severity:** Critical
**CWE:** CWE-362 (Concurrent Execution using Shared Resource with Improper Synchronisation), CWE-367 (TOCTOU Race Condition)
**Location:** `mux.go:464-480` (param-route path), `mux.go:521-537` (wildcard-method path)
**Relevant hypothesis:** H-001, H-024 (confirmed)

**Race window:**

```
goroutine A (dispatcher processing request R1):
  mux.go:464  origCtxPtr := (*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))
  mux.go:465  origCtx := *origCtxPtr
  mux.go:466  rc := acquireRC()
  ...
  mux.go:474  *origCtxPtr = rc                         // [WRITE r.ctx = rc]
  mux.go:475  handler.ServeHTTP(w, r)                  // handler spawns child goroutine B with r
  mux.go:476  *origCtxPtr = origCtx                    // [WRITE r.ctx = origCtx]
  mux.go:478  rc.params = nil                          // [WRITE rc.params]
  mux.go:480  releaseRC(rc)                            // rc returns to pool

goroutine B (spawned by handler, reads r.Context()):
  net/http/request.go:353  return r.ctx                // [READ r.ctx]
  params.go:160  rc, _ := r.Context().(*requestCtx)    // [READ r.ctx]
  params.go:164  return rc.params.Get(name)            // [READ rc.params]
```

Two classes of race observed:
1. **`r.ctx` pointer torn read vs write** — `mux.go:476` writes, goroutine B reads at `request.go:353`.
2. **rc object shared cross-goroutine** — if rc has returned to the pool and another request acquires it, `rc.params` is overwritten (line 473 of another dispatch) while goroutine B reads.

**Observed interleaving (3 races captured):**

```
WARNING: DATA RACE
Read at 0x00c0002964b8 by goroutine 60:
  net/http.(*Request).Context()      /usr/local/go/src/net/http/request.go:353
  PathParam()                         /data/dev/.../params.go:160
  handler's spawned goroutine         harness h001_r_ctx_goroutine_race_test.go:62

Previous write at 0x00c0002964b8 by goroutine 13:
  (*Mux).dispatch()                   mux.go:476      (*origCtxPtr = origCtx)
  (*Mux).ServeHTTP()                  mux.go:415

Read at 0x00c000610178 by goroutine 6003:
  Params.Get()                        params.go:25
  PathParam()                         params.go:164

Previous write at 0x00c000610178 by goroutine 28:
  runtime.slicecopy()                 runtime/slice.go:392
  (*Mux).dispatch()                   mux.go:473      (rc.params = copy(...))

Write at 0x00c000610150 by goroutine 44:
  (*Mux).dispatch()                   mux.go:478      (rc.params = nil)
```

**Real-world attack surface:**

The pattern that triggers this race is extremely common and **entirely legitimate** in real applications:

```go
r.GET("/api/user/:id", func(w http.ResponseWriter, r *http.Request) {
    // Async audit log — pattern seen in Gin, Echo, chi apps
    go func() { logAsync(r.Context(), "user_fetched", r.URL.Path) }()
    // ... synchronous handler work
})
```

Gin, Echo, chi and httprouter all treat the request context immutably (they use `r.WithContext`, which returns a new `*Request`). MuxMaster differs: it mutates `r.ctx` in place via `unsafe.Add`. Migrating from any of these routers to MuxMaster **silently breaks** apps that use `go func() { ... r.Context() ... }()`.

**Evidence:**
- `evidence/2026-04-17/h001_run1_full.txt` (139 lines, 3 DATA RACE warnings)
- `evidence/2026-04-17/CSA-001/repro_test.go` (minimal reproducer with 100 requests)

**Detection mechanism:** race detector (`-race`) with full stack traces.

**Recommended fix (preferred):** Use `r.WithContext(rc)` instead of `unsafe.Add` on the param-route path. This reintroduces 1 alloc per param-req but ELIMINATES the race. Zero-alloc alternative: document the constraint normatively + add static validation in CI.

```go
// Current (mux.go:464-476) — UNSAFE:
origCtxPtr := (*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))
origCtx := *origCtxPtr
rc := acquireRC()
rc.Context = origCtx
// ...
*origCtxPtr = rc
handler.ServeHTTP(w, r)
*origCtxPtr = origCtx

// Safe variant A (1 alloc per param-req):
rc := acquireRC()
rc.Context = r.Context()
// ...
handler.ServeHTTP(w, r.WithContext(rc))

// Safe variant B (zero-alloc, requires API change):
// Return Params directly to handler via second argument; drop r.ctx mutation.
```

If variant A is considered an unacceptable performance regression (benchstat before/after), document imperatively that **handlers MUST NOT retain `r` cross-goroutine**. The current documentation in `mux.go:20` only says `"middleware must be registered before the routes it should wrap"` — it does not mention this restriction.

---

### 5.3 CSA-002 — `Use()` / `Pre()` / `preHandler` not synchronised with `Handle()`

**Severity:** Critical
**CWE:** CWE-362, CWE-667 (Improper Locking)
**Location:** `mux.go:175-184`

**Race window:**

```
goroutine A (calls r.Use(mw)):
  mux.go:176  m.middleware = append(m.middleware, middleware...)   // [WRITE slice header + backing]

goroutine B (calls r.GET("/p/42", h)):
  mux.go:223  root.addRoute(pattern, wrapMiddleware(handler, m.middleware))  // [READ slice header]
              wrapMiddleware iterates m.middleware                            // [READ backing]
```

`Handle()` acquires `m.mu` at line 203 — but `Use()` does NOT.

**Observed (2 races captured):**

```
WARNING: DATA RACE
Read at 0x00c00012e698 by goroutine 11:
  (*Mux).Handle()       mux.go:223
  (*Mux).HandleFunc()   mux.go:229
  (*Mux).GET()          mux.go:247

Previous write at 0x00c00012e698 by goroutine 10:
  (*Mux).Use()          mux.go:176

Read at 0x00c0000c92c8 by goroutine 11:
  wrapMiddleware()      mux.go:640    (range over m.middleware)

Previous write at 0x00c0000c92c8 by goroutine 10:
  runtime.slicecopy()   runtime/slice.go:392  (append grew backing)
  (*Mux).Use()          mux.go:176
```

**Real-world trigger:** applications that register routes concurrently (plugin loading, parallel unit tests sharing a Mux, etc.) or that call `Use()` after `Handle()` — combined with CSA-008 / H-008 this is a silent auth-bypass, because routes registered before `Use(auth)` do NOT have auth, and the concurrent read may still see a mixed state.

**Evidence:** `evidence/2026-04-17/middleware_chain_mutation_run1.txt`, `evidence/2026-04-17/CSA-002/repro_test.go`.

**Recommended fix:**

```go
func (m *Mux) Use(middleware ...func(http.Handler) http.Handler) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.middleware = append(m.middleware, middleware...)
}

func (m *Mux) Pre(mw ...func(http.Handler) http.Handler) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.pre = append(m.pre, mw...)
    m.preHandler = wrapMiddleware(http.HandlerFunc(m.dispatch), m.pre)
}
```

Note that `ServeHTTP` reads `m.preHandler` without a lock (lines 411, 422). Even with `Pre()` under the lock, publishing the pointer via a plain write provides no happens-before for another goroutine that reads it. Complete solution: use `atomic.Pointer[http.Handler]` for `preHandler`.

Secondary fix: apply the same pattern to all public-field writers if the contract documents them as configurable at any time, or document explicitly "set before ListenAndServe; do not reconfigure".

---

### 5.4 CSA-003 — Introspection (Walk/Routes/Lookup) races against `addRoute` without synchronisation

**Severity:** Critical (if dynamic registration is contemplated for v1.0.0; Medium if it remains UB)
**CWE:** CWE-362, CWE-820
**Location:** `introspection.go:60-95` vs `tree.go:63-155`
**Relevant hypothesis:** H-027 (confirmed)

**Race window:** `treesPtr.Load()` returns the array pointer atomically, but the *nodes* are MUTATED IN PLACE by `addRoute`. Any goroutine traversing a node via `walk` observes concurrent writes to `n.children`, `n.indices`, `n.path`, `n.handler`, `n.pattern`.

**Observed (4+ races in the first 200ms):**

```
WARNING: DATA RACE
Write at 0x00c000170260 by goroutine 10:
  (*node).addRoute()    tree.go:96       (n.path = path[:i])
  (*Mux).Handle()       mux.go:223

Previous read at 0x00c000170260 by goroutine 11:
  (*node).walk()        tree.go:468      (if n.handler != nil && n.pattern != "")
  (*Mux).Walk()         introspection.go:84

Write at 0x00c0000ac080 by goroutine 10:
  (*node).addRoute()    tree.go:91       (n.children = ...)
Previous read at 0x00c0000ac080 by goroutine 11:
  (*node).walk()        tree.go:465
```

**Evidence:** `evidence/2026-04-17/h027_walk_vs_handle_run1.txt` (63 race warnings in 2s), `evidence/2026-04-17/CSA-003/repro_test.go`.

**Assessment:** CLAUDE.md line 37 of the threat model says "dynamic route registration after the server starts serving is not supported". The public documentation in `mux.go:20-21` repeats this rule. **But** introspection (`Walk`, `Routes`, `Lookup`) is a public API that operators DO USE for debug/health endpoints **at runtime** — restricting it to pre-serve makes no sense.

**Recommended fix:** 

Option A (preferred, keeps zero-dep and a zero-overhead hot path):
- Document explicitly that `Walk`/`Routes`/`Lookup` must be called only after the last `Handle`/`GET`/etc. (i.e. treat registration as a "fixed phase"). Add to each doc comment: `Safe to call from multiple goroutines provided no route registration is in progress`.
- Add a linter rule in CI that detects calls to `Walk`/`Routes`/`Lookup` after start.

Option B (correctness-first, COW cost regression in Handle):
- Perform a full COW of the tree (not only of the `*methodTrees` array). Each `addRoute` produces a deep copy of the affected tree root. Cost: O(N) bytes per registration instead of O(1). Benefit: `Walk`/`Routes`/`Lookup` become naturally race-free because they read immutable snapshots.

Option C (middle-ground):
- Add `m.mu.RLock()` to `Walk`/`Routes`/`Lookup`. Contained within the lock path that `Handle` already uses. Cost: blocks writers for the duration of the walk, but walkers are rare.

---

### 5.5 CSA-004 — `releaseRC(rc)` skipped on handler panic: pool leak

**Severity:** Critical (long-running processes with periodic panics)
**CWE:** CWE-404, CWE-772
**Location:** `mux.go:466-480` (param path), `mux.go:523-537` (wildcard-method path)

**Race/leak path:**

```go
rc := acquireRC()                // line 466
// ...
handler.ServeHTTP(w, r)          // line 475 — PANIC HERE
*origCtxPtr = origCtx            // SKIPPED
rc.Context = nil                 // SKIPPED
rc.params = nil                  // SKIPPED
rc.pattern = ""                  // SKIPPED
releaseRC(rc)                    // SKIPPED — rc LEAKS
```

Neither the `Recoverer` middleware (which acts inside the handler) nor the `PanicHandler` field (which acts via `defer recover()` in `dispatchWithRecover`) restores the cleanup sequence. Both swallow the panic, but the rc cleanup never runs.

**Observed:** 50 000 consecutive panics produce +271 990 424 bytes allocated (5 439 bytes/request).

```
CSA-004: 50000 panic requests caused allocDelta=271990424 bytes (5439.8 bytes/req)
```

The GC eventually reclaims the objects (because nothing retains them), but under panic storms the residual pressure is significant and the `sync.Pool` loses its recycling advantage.

**Evidence:** `evidence/2026-04-17/CSA-004/repro_test.go`.

**Recommended fix:** Use `defer` for the cleanup, not inline statements:

```go
if ps.count > 0 {
    pslice := ps.buf[:ps.count]
    if m.UnescapePathValues {
        for i := range pslice {
            if v, err := url.QueryUnescape(pslice[i].Value); err == nil {
                pslice[i].Value = v
            }
        }
    }
    origCtxPtr := (*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))
    origCtx := *origCtxPtr
    rc := acquireRC()
    if origCtx != nil {
        rc.Context = origCtx
    } else {
        rc.Context = context.Background()
    }
    rc.pattern = pattern
    rc.params = Params(rc.small[:copy(rc.small[:], pslice)])
    *origCtxPtr = rc
    defer func() {
        *origCtxPtr = origCtx
        rc.Context = nil
        rc.params = nil
        rc.pattern = ""
        releaseRC(rc)
    }()
    handler.ServeHTTP(w, r)
}
```

**Cost:** the `defer` adds ~20ns per param-req (frame setup). It can be mitigated with `defer inline` via Go 1.24+ compiler hints, or moved into an inlineable helper. But the correctness gain outweighs the performance delta.

**Cross-ref with CSA-001:** the same `defer` would solve the `origCtx` restore problem on the panic path (CSA-005), but it does **not** solve the cross-goroutine race of CSA-001 (which is architectural in `unsafe.Add`).

---

### 5.6 CSA-005 — Handler panic leaves `r.ctx` pointing at the leaked rc

**Severity:** High
**CWE:** CWE-662 (Improper Synchronisation), CWE-672 (Operation on a Resource after Expiration)
**Location:** `mux.go:474-476`, `mux.go:531-533`

**Observed behaviour:**

```
CSA-005: after panic, req.Context sentinel=origin PathParam(id)="x"
CSA-005 confirmed: req.Context still points at the leaked *requestCtx (PathParam returned "x")
```

After a panic on `/p/:id` (the handler panics), `req.Context()` still returns the original `*requestCtx` (not the parent `origCtx` that the caller passed). This means:

1. If the caller (HTTP server, test harness, tested middleware) reuses `r` after a recovered panic, it reads data from the aborted request.
2. If the rc is ever returned to the pool by some future path and reassigned to another request, confusion may arise (mitigated by rc poll, but mutations via reflect could leak).

`rc.Context` still points at the valid context chain (so `Value(sentinel)=="origin"` passes), which partially masks the problem — but `PathParam` returns a value from the aborted request.

**Evidence:** `evidence/2026-04-17/CSA-005/repro_test.go`.

**Recommended fix:** The same solution as CSA-004 (`defer`) resolves this case.

---

### 5.7 CSA-006 — Public fields read unsafely on hot path

**Severity:** High
**CWE:** CWE-362
**Location:** `mux.go` — every public field of `Mux`

**Affected fields:**
- `NotFound` (line 139) — read at `mux.go:568`
- `MethodNotAllowed` (line 142) — read at `mux.go:559`
- `GlobalOPTIONS` (line 146) — read at `mux.go:549`
- `ErrorHandler` (line 150) — read in generated HandleE (line 237)
- `PanicHandler` (line 154) — read at `mux.go:407, 577`
- `RedirectTrailingSlash` (line 111) — read at `mux.go:491`
- `RedirectFixedPath` (line 114) — read at `mux.go:501`
- `HandleMethodNotAllowed` (line 118) — read at `mux.go:556`
- `HandleOPTIONS` (line 122) — read at `mux.go:546`
- `CaseInsensitive` (line 125) — read at `mux.go:450, 518`
- `UseRawPath` (line 128) — read at `mux.go:433`
- `UnescapePathValues` (line 132) — read at `mux.go:456`
- `RedirectCode` (line 136) — read at `mux.go:583`

**Race window:** `ServeHTTP` reads each field per request without sync. If the caller CHANGES any of these fields after the server has accepted the first request, it is a race.

**Observed (3 tests failed with WARNING: DATA RACE):**

```
TestPublicFieldAssignment_PanicHandler_Race: FAIL (race detected)
TestPublicFieldAssignment_NotFound_Race:     FAIL (race detected)
TestPublicFieldAssignment_BoolFlag_Race:     FAIL (race detected)
```

**Assessment:** It is common practice in Go applications to configure boolean flags in the constructor and not touch them again. But `NotFound`, `MethodNotAllowed` and `PanicHandler` are types more prone to runtime reconfiguration (feature flags, runtime config reload). The public API does not document the restriction.

**Evidence:** `evidence/2026-04-17/public_fields_race_run1.txt`.

**Recommended fix:**

Option A (preferred — zero-overhead, correctness through documentation):
- Document in each field's GoDoc: `// Set before starting to serve; do not reassign after the first request.`
- Add a "Thread-safety contract" section to README.md.

Option B (robust — performance cost):
- Wrap each sensitive field in `atomic.Pointer` or `atomic.Value`. Major API change (breaking).

Option C (middle-ground):
- Expose `SetNotFound(h)`, `SetPanicHandler(f)` etc. that swap the field atomically. Keep the public field as read-only legacy. Non-breaking.

---

### 5.8 CSA-007 — `Pre()` publishes `preHandler` without happens-before

**Severity:** High
**CWE:** CWE-820 (Missing Synchronisation)
**Location:** `mux.go:181-184`

`Pre()` assigns `m.preHandler = wrapMiddleware(...)` without a lock. `ServeHTTP` reads `m.preHandler` at `mux.go:411` and `mux.go:422` without a lock. Even if the write is atomic on the current platform (word-size pointer on amd64), the Go memory model **does not guarantee visibility** without synchronisation. The receiving goroutine may see a stale pointer indefinitely.

**Evidence:** partially covered by the `TestMiddlewareChainMutationRace` harness, which exercises `Use` (analogous). A `Pre`-specific harness would produce an identical result.

**Recommended fix:** See CSA-002 — reuse `m.mu`; for the `preHandler` field itself, use `atomic.Pointer[http.Handler]`.

---

### 5.9 CSA-008 — Timeout middleware does not preempt the handler (confirmation of a design limitation)

**Severity:** Medium
**CWE:** CWE-400
**Location:** `middleware/timeout.go:14-20`
**Relevant hypothesis:** H-017 (confirmed)

**Observed:** 256 concurrent requests to a handler that sleeps 200ms, with `Timeout(10ms)`. Result:

```
goroutines: before=2 during=258 after=2 (N=256) started=256 ended=256
```

During the period between the timeout (10ms) and the handler's natural completion (200ms), 256 goroutines remain blocked — the middleware only cancels the context; the handler **does not cooperate** (it does not call `<-ctx.Done()`).

**Assessment:** This is an accepted design choice (documented in H-017). It is Medium because slowloris attack + slow handler = goroutine exhaustion. MuxMaster CANNOT solve it unilaterally — it requires handler cooperation or the stdlib's http.Server.ReadHeaderTimeout / WriteTimeout. **Escalation:** `dos-resilience-tester` must confirm the slowloris scenario; CSA-008 is only the concrete evidence of the limitation.

**Recommended mitigation:**
- The GoDoc of `Timeout(d)` must explain `Handlers must check r.Context().Done() to terminate early; otherwise the goroutine blocks until the handler returns on its own.`
- Add an example in docs/middleware.md.

---

### 5.10 CSA-009 — Recoverer writes the raw panic value to stderr

**Severity:** Low
**CWE:** CWE-209
**Location:** `middleware/recoverer.go:16`

**Code:**
```go
fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
```

If `rcv` is attacker-controllable (e.g. `panic(string(untrustedInput))`), control bytes escape to stderr: ANSI sequences, CRLF (log injection in log aggregators), info disclosure via `debug.Stack()` (internal file names).

**Cross-ref:** H-021, H-003 (CRLF). Primary owner: `middleware-security-reviewer`. The escalation is mentioned here because the concurrent test exposed the volume of stderr output under panic storms.

**Recommended fix (delegated to middleware-reviewer):**
- Escape `%v` to `%q` or use a dedicated sanitiser.
- Redact file paths via a build flag or the environment variable `MUXMASTER_REDACT_STACK=1`.

---

### 5.11 CSA-010 — `reqCtxOffset` latent failure modes in future Go versions

**Severity:** Low (latent, not exploitable on the current Go 1.26.2)
**CWE:** CWE-453 (Insecure Default Variable Initialisation)
**Location:** `params.go:139-148`

**Current code:**
```go
func init() {
    t := reflect.TypeOf(http.Request{})
    for i := 0; i < t.NumField(); i++ {
        f := t.Field(i)
        if f.Name == "ctx" {
            reqCtxOffset = f.Offset
            break
        }
    }
}
```

If a future Go version:
1. Renames `ctx` (→ `context`, `cctx`, etc.) → `reqCtxOffset = 0`
2. Removes the field (→ backing via an opaque struct) → same
3. Changes its type (→ atomic, or wrapper interface) → valid offset, but writes corrupt the layout

Consequence: `unsafe.Add(r, 0)` would write to the first field of `http.Request` (currently `Method string`) — silent corruption with unpredictable behaviour.

**Assessment:** H-018 passes on Go 1.26.2 (the `ctx` field is still present, `TestH018_ReqCtxOffsetAgreement` PASS). This is a latent maintenance hazard, not an active vulnerability.

**Recommended fix:**

```go
func init() {
    t := reflect.TypeOf(http.Request{})
    ctxT := reflect.TypeOf((*context.Context)(nil)).Elem()
    for i := 0; i < t.NumField(); i++ {
        f := t.Field(i)
        if f.Name == "ctx" {
            if f.Type != ctxT {
                panic(fmt.Sprintf(
                    "muxmaster: http.Request.ctx has unexpected type %s (expected context.Context); MuxMaster is incompatible with this Go version",
                    f.Type.String()))
            }
            reqCtxOffset = f.Offset
            return
        }
    }
    panic("muxmaster: http.Request has no 'ctx' field; MuxMaster is incompatible with this Go version")
}
```

**Cross-ref:** `go-sast-and-memory-auditor` should include this check in custom `govulncheck` / `staticcheck` rules.

## 6. Documented safe patterns

Tested and validated as SAFE:

1. **ServeHTTP without spawn**: `r.GET(...)` + handlers that only use `r` synchronously. 256 000 concurrent reqs, 0 races, 0 goroutines leaked.
2. **Handler spawns with snapshot**: `id := mm.PathParam(r, "id"); go func(id string) { ... }(id)` — the snapshot is copied before the spawn; the child goroutine does not retain `r`. TestH001_HandlerPropagatesContextToGoroutine PASS.
3. **Pool canary**: `rc.params = rc.small[:copy(...)]` + API iteration restricted to `[:count]` is functionally safe; 256 000 iter, 0 leaks.
4. **Throttle counter**: the channel-based limit is correctly atomic; 2048 reqs, peak==limit==8, zero off-by-one.
5. **Recoverer recovery**: 16 000 concurrent panics, all recovered; pool NOT contaminated via the public API.
6. **Context cancellation**: cancel() of the parent context propagates to the handler within <2s (observed 5ms).
7. **Current reqCtxOffset**: the offset on Go 1.26.2 is correct and stable; reads/writes via `unsafe.Add` write to the correct field.

## 7. Coverage gaps

Not tested in this audit (responsibility of other agents or out of scope):

1. **HTTP/2 multiplexing**: stream-level concurrency under HTTP/2 frames — escalated to `http-protocol-security-auditor`.
2. **net/http.Server integration**: the harness uses `httptest` directly; the interaction with the `http.Server` connections-per-goroutine model was not exercised. The goroutine leak on timeout (CSA-008) is tested in isolation; in production `http.Server.WriteTimeout` may abort the connection even with a blocked handler, partially mitigating it.
3. **Real Network slowloris**: tested only synthetically; full slowloris escalated to `dos-resilience-tester`.
4. **TSR pre-auth route disclosure** (H-025): behavioural; outside the concurrency scope.
5. **Pool cross-P memory model**: `sync.Pool` has per-P semantics with stealing; we did not test whether rc fields are guaranteed to be zero after `New()` (we assume so — the Go spec guarantees it). A specific canary for this race would be `rc.pattern` observing a value from another P — inspect whether PathParam/pattern reads see stale values after `Pool.Get`.
6. **GOMAXPROCS=1 scenario**: all runs were on 16 cores. Under GOMAXPROCS=1 the scheduler serialises more, potentially masking races. Not re-tested here; low marginal value for this module.
7. **ARM64 / weak memory model platforms**: tests run on amd64 (TSO); ARM64 may expose races that the race detector records, but production behaviour may differ. A re-run of the harness on arm64 before the release is recommended.

## 8. Escalations (cross-agent)

| Finding | Escalate to | Reason |
|---|---|---|
| CSA-009 (Recoverer stderr) | `middleware-security-reviewer` | Primary owner of middleware auditing; decides the escaping policy |
| CSA-008 (Timeout leak) | `dos-resilience-tester` | Needs a real slowloris + http.Server harness |
| CSA-010 (reqCtxOffset latent) | `go-sast-and-memory-auditor` | Add a validation rule in CI |
| CSA-001 composite with H-015 | `threat-modeler-and-zero-day-researcher` | Potential multi-channel exfil if combined with async logging middleware |
| CSA-006 (public fields) | `go-sast-and-memory-auditor` | `golangci-lint` possibly catches it via `govet -copylocks`? Investigate |

## 9. Next actions (for maintainer)

### 9.1 Pre-release (must fix before v1.0.0)

1. **CSA-002 fix** (trivial, 5-line patch): move `Use()` / `Pre()` inside `m.mu`. Add `atomic.Pointer[http.Handler]` for `preHandler`.
2. **CSA-004 + CSA-005 fix** (moderate, ~10-line diff): use `defer` in `mux.go:466-480` and `mux.go:523-537` to guarantee cleanup on the panic path. Measure the regression (benchstat baseline vs fix) — expected <5% on param routes.
3. **CSA-003 decision**: choose between option A (documentation), B (full COW), C (`m.mu.RLock()` in introspection). Minimum: add `m.mu.RLock()` to `Walk`/`Routes`/`Lookup` (option C) — zero hot-path impact, race closed.
4. **CSA-001 decision**: choose between:
   - (A) Remove `unsafe.Add` and use `r.WithContext(rc)` — 1 alloc/req, race closed definitively.
   - (B) Keep `unsafe.Add` + normative documentation + a CI linter rule that detects spawns holding `r`.
   Auditor recommendation: **(A)**. The performance regression is acceptable given the architectural correctness guarantee. Performance remains superior to the non-zero-alloc competitors (chi, gin).

### 9.2 High-severity follow-up

5. **CSA-006**: document the Thread-safety contract in README.md + the GoDoc of each public field. Evaluate adding atomic setters for `PanicHandler` / `NotFound` (non-breaking).
6. **CSA-010**: strengthen init() validation — 6-line diff.

### 9.3 Medium-severity (can ship as-is with documentation)

7. **CSA-008**: add a section to docs/middleware.md explaining that handlers must cooperate with `ctx.Done()` for the timeout to have real effect.
8. **CSA-009**: delegated to `middleware-security-reviewer`.

### 9.4 Re-run requirements

After fixes 1-4 are merged:
- Re-run the whole harness suite with `-race -count=10` — **zero** DATA RACE warnings.
- Re-run benchmarks: `go test -bench=. -benchmem -count=10 > new.txt && benchstat baseline.txt new.txt`. A regression ≤ 10% on param routes is acceptable if justified by correctness.
- Update this report, marking every CSA with status `Fixed` → `Verified`.
- Update `/reports/overview/findings.md` with verdicts.
- Update `/reports/overview/hypotheses.md`:
  - H-001: `open` → `confirmed`
  - H-018: `open` → `partial` (latent, mitigated by proposed init validation)
  - H-023: `open` → `refuted` (functional)
  - H-027: `open` → `confirmed`
  - H-017: `open` → `confirmed` (documentation sufficient)
  - H-016: `open` → `refuted` (token does not leak, but rc leaks — cross-ref CSA-004)

## 10. Evidence layout

```
/reports/concurrency-security-auditor/
├── 2026-04-17-1312-prerelease-concurrency-audit.md   ← this file
├── harness/
│   ├── h001_r_ctx_goroutine_race_test.go             ← CSA-001
│   ├── h018_reqctx_offset_test.go                    ← CSA-010 (H-018)
│   ├── h023_pool_canary_test.go                      ← H-023 canary (mandatory)
│   ├── h027_introspection_race_test.go               ← CSA-003
│   ├── middleware_race_test.go                       ← throttle, timeout, recoverer, chain mutation, cancellation
│   ├── panic_pool_cleanliness_test.go                ← CSA-004/005
│   ├── public_fields_race_test.go                    ← CSA-006
│   └── goroutine_leak_test.go                        ← baseline leak detection
└── evidence/2026-04-17/
    ├── h001_run1_full.txt                            ← 139 lines, 3 DATA RACE
    ├── h001_massive_parallel_run1.txt                ← PASS
    ├── h023_canary_run1.txt                          ← 0 leaks
    ├── h027_walk_vs_handle_run1.txt                  ← 63 DATA RACE
    ├── middleware_chain_mutation_run1.txt            ← 2 DATA RACE (CSA-002)
    ├── middleware_throttle_recoverer_cancel_run1.txt ← PASS + stderr dump
    ├── panic_pool_run1.txt                           ← functional PASS
    ├── public_fields_race_run1.txt                   ← 3 DATA RACE (CSA-006)
    ├── timeout_leak_run1.txt                         ← 256 goroutines during timeout
    ├── goroutine_leak_run1.txt                       ← delta=0
    ├── suite_count10_run1.txt                        ← 54.78s, zero DATA RACE in the passing subset
    ├── CSA-001/repro_test.go
    ├── CSA-002/repro_test.go
    ├── CSA-003/repro_test.go
    ├── CSA-004/repro_test.go
    └── CSA-005/repro_test.go
```

## 11. Sprint retrospective

**What went well:**
- 3 race classes confirmed in <5 minutes of harness execution.
- The mandatory pool canary test produced strong evidence that the functional API is shielded (0 leaks in 256k iter).
- Minimal reproducers (<60 lines each) isolate each finding for iterative patching.

**What could improve:**
- I did not cover stream-level HTTP/2 concurrency — it depended on `http-protocol-security-auditor` providing a harness; it remains a documented gap.
- Benchstat before/after the proposed fixes was not executed in this sprint — responsibility of `go-perf-optimizer` in the follow-up.

**New hypotheses generated:**
- **H-033** (new, candidate): `m.preHandler` reassignment via `atomic.Pointer` to close CSA-007 without breaking the API — calls for performance experimentation.
- **H-034** (new, candidate): `unsafe.Add` vs `r.WithContext` — differential benchmark to justify the CSA-001 fix. Cross-team with `go-perf-optimizer`.

**Conclusion:** MuxMaster has a mature radix tree and a well-designed hot path, but the `unsafe.Add`-based optimisation creates an architectural trap for legitimate handler patterns. Release v1.0.0 must wait for the remediation of the 4 Critical findings; the inferior alternative (documentation only) is acceptable only if accompanied by a linter rule enforced in CI.

---

**Auditor signature:** concurrency-security-auditor
**Reproducibility:** all findings are reproducible in <2 minutes on commodity hardware (Linux amd64 / GOMAXPROCS ≥ 4). Reproducers are versioned in `evidence/2026-04-17/CSA-NNN/repro_test.go`.
