# MuxMaster — Synthesis of the Exhaustive Performance Audit

**Date:** 2026-05-12 | **Branch:** `perf/maximize-performance`
**Hardware:** AMD Ryzen 9 5900HX | **Go:** 1.26.2

This document consolidates the findings of 3 specialised agents + a direct audit by the orchestrator into a prioritised list of **concrete, measurable changes, with no API change and without compromising security**, to extract the maximum performance the hardware can sustain.

---

## Guiding principle — CRITICAL REVISION AFTER THE AUDIT

> **Major discovery (competitor agent + apples-to-apples harness measurements):**
> The previous narrative "MuxMaster loses to httprouter on params (124ns vs 58ns)" compared **different APIs**. httprouter's 58ns uses its `Handle(w, r, Params)` (3 args, NOT compatible with `http.Handler`). Compared apples-to-apples (same stdlib `http.Handler` API):

| Router | API | Param1 ns/op | B/op | allocs |
|---|---|---|---|---|
| **MuxMaster Handle** | stdlib `http.Handler` | **118** | 416 | **1** |
| httprouter stdlib adapter | stdlib `http.Handler` | 179 | 456 | 4 |
| chi | stdlib `http.Handler` | 337 | 704 | 4 |
| MuxMaster Fast | 3-arg Params | **55** | 32 | 1 |
| httprouter native | 3-arg Params | **54** | 64 | 1 |
| bunrouter native | value-type Request (not stdlib) | 30 | 0 | 0 |

**Conclusion:** MuxMaster is ALREADY the fastest on both equivalent APIs. bunrouter's "0 allocs" is only achievable because it exposes a *value-type* `bunrouter.Request` (an API entirely distinct from `http.Handler`). When bunrouter is adapted to `http.Handler`, it costs 182ns / 3 allocs (SLOWER than MuxMaster).

The analysis concluded that **eliminating the reqBundle is structurally impossible** without reintroducing CSA-001 or breaking the API. Therefore, the strategy is:
1. **Optimise the path up to the reqBundle** (tree lookup, dispatch fan-out, redundancies)
2. **Optimise the content of the reqBundle** (eliminate 1 method call, reduce work)
3. **Eliminate costs on the non-hot paths** (default NotFound, MethodNotAllowed, RedirectTSL)
4. **Optimise middlewares** where safe (Logger is the largest target)

---

## TOP 12 — Prioritised optimisations (consolidated)

| # | ID | Description | Estimated gain | Affected path | Risk | Complexity | Unsafe |
|---|----|-----------|----------------|---------------|-------|--------------|--------|
| 1 | **O5** | Inline 1-param dispatch in `dispatch` (bypass `dispatchWithParams`) | **−5 to −10 ns/op** ParamRoute1/2 | stdlib param routes | Zero | S | NO |
| 2 | **O1** | Split `getValue` into an inlineable static fast path + `getValueFull` slow path | **−3 to −8 ns/op** all routes | Static routes (all) | Low | M | NO |
| 3 | **L1** | Logger: pooled buffer + no `fmt.Fprintf` + pooled statusRecorder | **−5500 ns/op, −7 allocs/op** | Logger middleware | Low | M | NO |
| 4 | **O9** | sync.Pool for `make(Params, n)` FastHandler | **−30 to −50 ns/op + eliminates ±63% variance** FastParam2/3 | FastHandler param routes | Medium (lifetime contract) | S | NO |
| 5 | **O5a** | Replace `r.Context()` with `*(*context.Context)(unsafe.Add(...))` in param dispatch | **−2 to −5 ns/op** param routes | All param dispatch | **SAFE** (validated) | S | YES (controlled) |
| 6 | **O2** | Eliminate the redundant `prefixMatch` at the terminal node of `getValue` | **−2 to −4 ns/op** all routes | Static + param | Zero | S | NO |
| 7 | **R1** | `RedirectTSL` rewrite: cached handler + manual builder without `url.URL{}.String()` | **−1000+ ns/op, −10 allocs** redirect path | Redirect TSL/Fixed | Low | M | NO |
| 8 | **O14**¹ | Move `var ps2 paramsBuf` into a separate `dispatchWildcard` function | **−2 to −4 ns/op** all routes (smaller stack frame) | All | Low | M | NO |
| 9 | **M1** | `MethodNotAllowed`: rebuild with fewer allocs (pre-cached allow string) | **−300 ns/op, −4 allocs** 405 path | 405 responses | Low | M | NO |
| 10 | **O3** | Remove the `children := n.children[:len(n.indices)]` slice header inside the `getValue` loop | **−1 to −2 ns/op** all routes | Static + param | Zero | S | NO |
| 11 | **L2** | RequestID: `hex.Encode` into a stack buffer instead of `hex.EncodeToString` | **−200 ns, −1 alloc** RequestID generate | RequestID middleware | Zero | S | NO |
| 12 | **L3** | NoCache + CORS: pre-canonicalise header keys; direct map assignment | **−150 ns, −2 allocs / −100 ns, −1 alloc** | NoCache + CORS middleware | Zero | S | NO |

¹ Labelled **O10** in this original draft. The id **O10** was later reassigned (audit of the same afternoon, commit `6cc0686`) to the applied optimisation "eliminate the `doDispatch1`/`doDispatch2` function-pointer indirection". This idea (dispatchWildcard) was implemented and empirically **rejected** — a regression on every benchmark, with no net gain — commit `943a1d1`. Renumbered to **O14** to eliminate the identifier collision. `O14` (no hyphen) names this optimisation; it is unrelated to `O-14`, the open item in reports/overview/findings.md, after which sprint-20 artefacts such as `FPE-O14-002` and `*_o14_*` test files are named (see findings.md B.5).

### Total potential gain (cumulative, estimates)

| Case | Baseline | After O5+O1+O2+O3+O5a | Gain |
|---|---|---|---|
| StaticRoute | 25.6 ns | ~16–20 ns | −20–35% |
| ParamRoute1 | 121.0 ns | ~100–108 ns | −10–17% |
| ParamRoute2 | 142.2 ns | ~120–128 ns | −10–15% |
| FastParamRoute1 | 52.5 ns | ~45–50 ns | −5–15% |
| FastParamRoute2 ±63% var | 95 ns (mean) | ~80 ns ±5% (with O9 pool) | stability + −15% |
| Production middleware chain | 8595 ns | ~3400 ns | **−60%** (Logger is dominant) |

After these optimisations, MuxMaster:
- **`Handle()` ParamRoute1 ≈ 100ns** (still loses to httprouter's 58ns due to the unavoidable reqBundle)
- **`HandleFast()` ParamRoute1 ≈ 45ns** (BEATS httprouter)
- **StaticRoute ≈ 17ns** (strongly beats every competitor)
- **Production middleware chain ≈ 3.4µs** (vs 8.6µs currently)

---

## Analysis per component

### A. Hot-path tree lookup — `getValue` (CPU 28% cum)

**Current state:**
- Compiler cost = 977 (cannot be inlined). 28% of total time.
- Two `prefixMatch` calls at the terminal node (450ms redundant cum)
- Redundant slice-header construction (290ms flat in `n.children[:len(n.indices)]`)
- For static routes, the function's full overhead

**Plan:**
- **O1**: split into `getValueStaticFast` (inlineable, <80 cost) + `getValueFull` (param/wildcard/regex)
- **O2**: eliminate the second `prefixMatch` call when `len(path)==len(prefix)` — directly `path == prefix` for ci=false (>99% of cases)
- **O3**: drop `children := n.children[:len(n.indices)]` — use `n.children[j]` directly

### B. Hot-path dispatch — `dispatchParams1Fast` + `dispatchWithParams`

**Current state:**
- Chain of 3 function calls for 1-param (most common REST case): `dispatch → dispatchWithParams → doDispatch1 → dispatchParams1Fast`
- `b.req = *r` (304B copy) is UNAVOIDABLE with 17 pointer fields → write barriers
- `r.Context()` is a method call (not inlined in our code)

**Plan:**
- **O5**: skip `dispatchWithParams` for `ps.count==1` — call `doDispatch1` directly from `dispatch`
- **O5a**: replace `r.Context()` with a direct unsafe read of the `ctx` field (the same `reqCtxFieldOffset` used by `setReqCtxUnsafe`). The net/http server ALWAYS sets ctx, validated.

### C. paramsBuf

**Current state:**
- `sizeof(paramsBuf) = 128 B` (the 264B comment is outdated)
- Stack-allocated, but zeroing 128B is dominant in some cases (3.74% CPU)
- `var ps2 paramsBuf` at mux.go:980 is always allocated even if starRoot==nil

**Plan:**
- **O14** (renumbered id; see note¹ above): move the wildcard lookup into a separate `dispatchWildcard` function — `var ps2` is only allocated when needed (rare)
- Reducing the main `var ps` is not viable (needed for param routes)

### D. NotFound path

**Current state:**
- 3 allocs / 117B come 100% from the **stdlib `http.NotFound`** (not from MuxMaster)
- `BenchmarkNotFoundCustomHandler` = 22ns / 0 allocs confirms it

**Plan:** No action needed in MuxMaster. Document it as a recommendation to the operator: set `Mux.NotFound = customHandler` for 0 allocs.

### E. MethodNotAllowed — **NEW HOT SPOT IDENTIFIED**

**Current state:**
- 449 ns / 6 allocs / 138B (measured by the orchestrator)
- Cause: `lazyMethodNotAllowed` is cached, BUT the inner handler performs `w.Header().Set("Allow", allow)` + `http.Error()` on every call

**Plan (M1):**
- Pre-build the response for the common Allow strings (combinations of GET/POST/PUT/DELETE/OPTIONS)
- Cached in `sync.Map[allow]*preBuiltResponse{header, body}` — direct write instead of Header().Set + Error()

### F. RedirectTSL — **CRITICAL HOT SPOT**

**Current state:**
- 1554 ns / 1305B / 15 allocs (measured)
- Causes:
  1. `&url.URL{...}` allocates (~104B)
  2. `.String()` allocates the serialised form
  3. `http.HandlerFunc(func...)` closure escape
  4. `wrapMiddleware` called on EVERY request (not cached)
  5. `http.Redirect` allocates for the response

**Plan (R1):**
- Pre-build redirect handlers in `frozenConfigSlow()` — for the common codes (301, 307)
- Replace `(&url.URL{Path, RawQuery}).String()` with direct concatenation via a pre-sized `strings.Builder`
- Cache the redirect handler — do not rebuild it on every request

### G. Middlewares (the agent's TOP 5)

| Middleware | Plan | Gain |
|---|---|---|
| **Logger (L1)** | Pooled `*bytes.Buffer` + no `fmt.Fprintf` + pooled `*statusRecorder` | −5500 ns, −7 allocs |
| **RequestID (L2)** | `hex.Encode` into a stack buffer | −200 ns, −1 alloc |
| **JWTAuth HS256** | `h.Sum(mac[:0])` on the stack | −50 ns, −1 alloc |
| **NoCache (L3)** | Pre-canonical header keys; direct map assignment | −150 ns, −2 allocs |
| **CORS (L3)** | Same as NoCache | −100 ns, −1 alloc |

---

## Security cross-validation

| Optimisation | Security risk | Mitigation |
|---|---|---|
| **O5** (inline dispatch) | None | No unsafe, no API change |
| **O1** (split getValue) | None | Structural refactor, same semantics |
| **O2** (eliminate redundant prefixMatch) | None | Code golf |
| **O5a** (unsafe r.ctx read) | **Validated SAFE** by the agent | net/http always sets ctx; `reqCtxFieldOffset` already validated in `setReqCtxUnsafe`; `hasReqCtxField==false` fallback kept |
| **O9** (sync.Pool FastHandler Params) | **Lifetime contract footgun** | Documented in `FastHandler` that ps is only valid during the call; document it EVEN more; consider opt-in via a `MuxMasterPool` flag |
| **O14** (dispatchWildcard; see note¹) | None | Refactor |
| **M1** (MethodNotAllowed cache) | None | The pre-build is deterministic |
| **R1** (RedirectTSL cache) | Verify **HPS-2026-0005** (Location injection) is still safe — the manual builder must NOT allow scheme injection | Path-only Location — the string builder guarantees that `target` starts with `/` |
| **L1-L3** (middlewares) | None without changing semantics | Verify that timing equalisations are unchanged |

---

## Proposed implementation plan (3 short sprints)

### Sprint 1 — Quick wins (ZERO risk, high confidence)
- O5: Inline 1-param dispatch
- O2: Eliminate redundant prefixMatch
- O3: Remove children slice header
- L2: RequestID hex.Encode stack
- L3a: NoCache pre-canonical headers
- L3b: CORS pre-canonical headers

**Expected:** −10–20 ns on common routes, several allocs eliminated

### Sprint 2 — Mid-complexity (low risk)
- O1: Split getValue static fast path
- O14: dispatchWildcard separation (renumbered id; see note¹)
- O5a: Unsafe r.ctx read (validate with extensive -race)
- L1: Logger pooled buffer
- M1: MethodNotAllowed pre-build
- R1: RedirectTSL cache + manual builder

**Expected:** a further −20–30 ns on static, Logger drops 80%, MethodNotAllowed/Redirect drop 70%+

### Sprint 3 — Optional (measure first)
- O9: sync.Pool FastHandler Params (decide whether it is an opt-in flag)
- Re-measure multi-core variance after Sprints 1 and 2 — O9 may no longer be necessary
- O4: prefixEq cosmetic
- O8: paramsBuf zero deferred

---

## Hypotheses discarded after analysis

| Hypothesis | Reason for discarding | Source |
|---|---|---|
| `sync.Pool` for reqBundle | CSA-001: the bundle lifetime may exceed ServeHTTP via goroutines spawned in handlers | Hot path agent + CLAUDE.md |
| `unsafe.Pointer` cast to uint64 for string compare in `prefixMatch`/`methodIdx` | Go strings have no guaranteed padding; out-of-bounds reads | Hot path agent |
| Move `nType`/`wildChild` to the node's CL0 | Cache trade-off: avoiding a miss on CL1 implies losing a hit on `handler` (CL0). No measured net gain | Hot path agent |
| Change the layout of `*http.Request` to avoid the copy | Impossible for an external library; would break stability | Hot path agent |
| Bypass `wrapMiddleware` on the hot path | Middlewares are applied at registration, not at runtime — it is ALREADY zero-cost | Existing audit |
| Reduce `paramsBuf` size by eliminating overflow | Overflow is needed for >3 params; eliminating it would be breaking | Hot path agent |
| `http.ResponseController` to avoid statusRecorder in Logger | Possible, but requires Go ≥1.20 (we are already on 1.26) and there is still a wrap cost | Middleware agent |
| **`r.SetPathValue` (Go 1.22+) to pass params on the original `r`** | **BENCHMARKED: 126 ns / 336B / 2 allocs on a fresh request — WORSE than reqBundle (1 alloc / 416B)**. Every `*http.Request` arrives with `patValues = nil`; the first call allocates the map. It would only be 0 allocs if `net/http.ServeMux` pre-initialised the map — which does not happen for external routers | Competitor agent (Section 5d/6) |
| Per-goroutine TLS for reqBundle | Same risk as sync.Pool — TLS released but spawned goroutines still hold a reference | Competitor agent |
| Modify the original `r.ctx` (not fresh) | net/http server.go keeps a ref in `conn.r` — the race detector catches it | Competitor agent |

---

## Final metrics and expectations

After implementing Sprints 1-3 (minimum: Sprint 1+2), the expected **apples-to-apples (same API)** comparison:

| Benchmark | MuxMaster baseline | Expected post-opt | Fastest stdlib competitor | Verdict |
|---|---|---|---|---|
| **StaticRoute** | 25.6 ns | **~17 ns** | httprouter stdlib 23 ns | MuxMaster wins by a wider margin |
| **ParamRoute1** stdlib | 118 ns | **~95 ns** | httprouter stdlib adapter 179 ns | **MuxMaster ALREADY wins; will win by more** |
| **ParamRoute3** stdlib | 158 ns | **~130 ns** | httprouter stdlib adapter 198 ns | MuxMaster ALREADY wins |
| **FastParamRoute1** | 55 ns | **~45 ns** | httprouter native 54 ns | tie → **MuxMaster wins post-opt** |
| **FastParamRoute2** | 73 ns ±63% var | **~60 ns ±5%** | httprouter native 60 ns | tie → beats it STABLY |
| **MethodNotAllowed** | 449 ns | **~150 ns** | n/a | local reduction |
| **RedirectTSL** | 1554 ns | **~400 ns** | n/a | local reduction |
| **Production chain** | 8595 ns | **~3400 ns** | n/a | −60% reduction |

**Native bunrouter (0 allocs) is NOT comparable apples-to-apples** — it uses a value-type `bunrouter.Request` (an API entirely distinct from `http.Handler`). Adapted to the stdlib it costs 182ns / 3 allocs / 416B (slower than MuxMaster). Matching that performance would require adding a 3rd API with a value-type request — out of scope (it would change the public API).

---

## Conclusion

The audit identified **12 optimisations implementable without changing the API or compromising security**, with measured/estimated gains that reduce latency on every critical path. The gains are incremental (5-30% per component) but cumulative: the typical production stack (Recoverer + RealIP + RequestID + Logger + CORS + dispatcher) goes from **8595ns/23 allocs** to **~3400ns/15 allocs** after the optimisations — **−60% latency per request**.

**The competitive positioning is better than we thought:** MuxMaster **is already the fastest** among routers with an `http.Handler` API (118ns vs httprouter 179ns vs chi 337ns on Param1). The "loss" to httprouter in the previous CLAUDE.md compared different APIs. After the optimisations the gap widens.

The non-hot but heavy paths (MethodNotAllowed, RedirectTSL) have a −70% latency potential via localised optimisations.

The use of `unsafe` on the critical path is justified for a single operation: reading `r.ctx` directly without the `r.Context()` call (O5a). This operation reuses the offset already validated in `setReqCtxUnsafe` and has an explicit fallback when the offset is not found in future Go versions.
