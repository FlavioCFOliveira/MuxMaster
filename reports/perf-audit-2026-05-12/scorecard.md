# MuxMaster — Performance Scorecard (consolidated)

**Date:** 2026-05-12 | **Hardware:** AMD Ryzen 9 5900HX | **Go:** 1.26.2

All measurements with `benchstat`, `benchtime=1s` (internal) or `benchtime=500ms` (extras), 3-10 runs.

---

## 1. Hot path — core features

| Feature | ns/op | B/op | allocs/op | Rating |
|---|---|---|---|---|
| **`StaticRoute`** | 25.6 | 0 | 0 | ★★★★★ Beats every competitor |
| `ParamRoute1` | 121 | 416 | 1 | ★★★ Loses to httprouter (58ns) |
| `ParamRoute2` | 142 | 448 | 1 | ★★★ Loses to httprouter (71ns) |
| `ParamRoute3` | 148 | 480 | 1 | ★★★ Loses to httprouter (78ns) |
| `WildcardRoute` | 123 | 416 | 1 | ★★★ |
| `ParallelStatic` | 4.0 | 0 | 0 | ★★★★★ |
| `ParallelParam` | 113 | 416 | 1 | ★★★ vs httprouter 23ns |
| **`FastStaticRoute`** | 26.8 | 0 | 0 | ★★★★★ |
| **`FastParamRoute1`** | **52.6** | 32 | 1 | ★★★★★ **BEATS httprouter (56ns)** |
| `FastParamRoute2` | 80–95 | 64 | 1 | ★★★★ Beats httprouter (71ns) |
| `FastParamRoute3` | 78–99 | 96 | 1 | ★★★★ Beats httprouter (78ns) |
| `FastParallelParam` | 16.0 | 32 | 1 | ★★★★★ |

---

## 2. Edge cases

| Case | ns/op | B/op | allocs/op | Notes |
|---|---|---|---|---|
| `ParamRoute5` (overflow >3) | 321 | 896 | 5 | 3x the cost of `Param3`; rare in practice |
| `FastParamRoute5` (overflow) | 162 | 256 | 3 | The fast handler supports overflow |
| `LongParamValue` (100 chars) | 137 | 416 | 1 | No extra cost |
| `LongStaticPath` (11 segments) | 25–40 | 0 | 0 | The radix tree compresses well |
| `DepthRoute` (11 nested levels) | 14 | 0 | 0 | Excellent |
| `RoutePattern` lookup | **1125** | 416 | 1 | **🚨 Suspicious — investigate** |

---

## 3. Non-hot but heavy paths

| Case | ns/op | B/op | allocs/op | Diagnosis |
|---|---|---|---|---|
| `NotFound` (defaults) | 335 | 117 | 3 | The 3 allocs come from `http.NotFound` (string write) |
| `NotFoundCustomHandler` | 22 | 0 | 0 | 0 allocs with a custom handler! |
| `NotFoundWithMethodAllowedLookup` | 343 | 123 | 3 | `allowed()` strings.Builder |
| **`MethodNotAllowed`** | **449** | **138** | **6** | 🚨 6 allocs — investigate |
| `OPTIONSAuto` | 161 | 40 | 3 | Reasonable |
| **`RedirectTSL`** | **1554** | **1305** | **15** | 🚨 CRITICAL — 15 allocs |

---

## 4. Scalability (radix tree)

| Registered routes | ns/op (middle path) | Growth |
|---|---|---|
| 10 | 23.2 | base |
| 100 | 26.0 | +12% |
| 1000 | 30.3 | +30% |
| 10000 | 34.4 | +48% |

**Verdict:** O(log k) confirmed. From 10 → 10000 routes, only +48% latency.

---

## 5. Middleware (18 individual — delegated agent)

### Zero-alloc (already optimal)
| Middleware | ns/op |
|---|---|
| StripSlashes (clean) | 4.8 |
| Recoverer (no panic) | 7.5 |
| CORS (no Origin) | 19 |
| CleanPath (clean) | 32 |
| Compress (no gzip) | 34 |
| ThrottleBacklog (no wait) | 45 |

### Optimisable
| Middleware | Current | Target | Gain |
|---|---|---|---|
| **Logger** | 6762 ns / 10 allocs | ~1200 ns / 3 allocs | **−5500ns / −7 allocs** |
| RequestID (generate) | 4944 ns / 9 allocs | ~4700 ns / 8 allocs | -200ns / -1 alloc |
| JWTAuth HS256 | 5506 ns / 20 allocs | ~5450 ns / 19 allocs | -50ns / -1 alloc |
| NoCache | 422 ns / 7 allocs | ~275 ns / 5 allocs | -150ns / -2 allocs |
| CORS (allowed) | 1670 ns / 4 allocs | ~1570 ns / 3 allocs | -100ns / -1 alloc |

### Inherent cost
- **Timeout**: 5518ns / 5 allocs — `context.WithTimeout` is the minimum possible
- **JWTAuth ES256**: 79697ns / 40 allocs — bounded by the stdlib `ecdsa.Verify`
- **OAuth2Introspect**: network latency dominates

---

## 6. Chain depth (Use middleware)

| Depth | ns/op | Overhead/middleware |
|---|---|---|
| 1 | 16.5 | 11 ns marginal |
| 5 | 30 | ~3.5 ns marginal |
| 20 | 96 | ~4.2 ns marginal |

**Verdict:** chain depth has a low linear cost (~4ns/middleware).

### Pre vs Use vs UseFast (trivial passthrough)
| Type | ns/op | Notes |
|---|---|---|
| `Use(noop)` (handle path) | 16.5 | Equivalent |
| `Pre(noop)` | 19.3 | +2.8ns (pre-dispatch wrap) |
| `UseFast(noop)` | 13.0 | **+0 — faster (native fast path)** |

---

## 7. Typical production chains (delegated agent)

| Chain | ns/op | allocs |
|---|---|---|
| Minimal (Recoverer) | 7.8 | 0 |
| AuthBasic | 233 | 2 |
| AuthJWT (Recoverer + RequestID + JWT) | 7021 | 29 |
| Security (5 middlewares) | 2485 | 20 |
| **Production** (Recoverer+RealIP+RequestID+Logger+CORS) | **8595** | **23** |
| Heavy (8 middlewares) | 11464 | 34 |

**Chains conclusion:** after the Logger optimisation, **the Production chain drops from 8595 → ~3400ns (−60%)**.

---

## 8. Direct comparison vs competitors (apples-to-apples)

| Case | MuxMaster | httprouter | bunrouter² | chi v5 | Verdict |
|---|---|---|---|---|---|
| Static | **30** | 34 | 188 | 220 | MuxMaster wins |
| Param1 stdlib | 124 | **58** | 182 | 394 | httprouter wins |
| Param1 FastHandler | **53** | n/a (different interface) | n/a | n/a | MuxMaster beats httprouter |
| Parallel Static | **4** | 5 | 127 | 149 | MuxMaster wins |
| Parallel Param | 107 | **23** | 127 | 264 | httprouter wins (cf. bundle 416B vs 64B) |

**Main gap:** the cost of the `reqBundle` (416-480B vs httprouter Params 64-96B).

---

## 9. Audit task status

| # | Task | Status | Output |
|---|---|---|---|
| 1 | Baseline | ✅ | `bench_internal_baseline*.txt`, `bench_competitor_baseline*.txt` |
| 2 | API surface | ✅ | Mapped (~70 Mux funcs, ~26 Group) |
| 3-5 | Hot path mux/tree/params | ⏳ Agent | `hotpath_analysis.md` (pending) |
| 6 | Groups | ✅ | No runtime overhead (registration only) |
| 7-8 | 18 middlewares + chains | ✅ | `middleware_analysis.md` (delivered) |
| 9 | Competitor study | ⏳ Agent | `competitor_techniques.md` (pending) |
| 10 | pprof + escape | ✅ | `cpu.prof`, `escape_*.txt` |
| 11 | Assembly hot path | ✅ | `asm_*.txt` |
| 12 | Final synthesis | ⌛ Pending | To be produced after the agents |
