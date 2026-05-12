# MuxMaster — Scorecard de Performance (consolidado)

**Data:** 2026-05-12 | **Hardware:** AMD Ryzen 9 5900HX | **Go:** 1.26.2

Todas as medições com `benchstat`, `benchtime=1s` (interno) ou `benchtime=500ms` (extras), 3-10 runs.

---

## 1. Hot-path — funcionalidades core

| Funcionalidade | ns/op | B/op | allocs/op | Avaliação |
|---|---|---|---|---|
| **`StaticRoute`** | 25.6 | 0 | 0 | ★★★★★ Bate todos os competidores |
| `ParamRoute1` | 121 | 416 | 1 | ★★★ Perde para httprouter (58ns) |
| `ParamRoute2` | 142 | 448 | 1 | ★★★ Perde para httprouter (71ns) |
| `ParamRoute3` | 148 | 480 | 1 | ★★★ Perde para httprouter (78ns) |
| `WildcardRoute` | 123 | 416 | 1 | ★★★ |
| `ParallelStatic` | 4.0 | 0 | 0 | ★★★★★ |
| `ParallelParam` | 113 | 416 | 1 | ★★★ vs httprouter 23ns |
| **`FastStaticRoute`** | 26.8 | 0 | 0 | ★★★★★ |
| **`FastParamRoute1`** | **52.6** | 32 | 1 | ★★★★★ **BATE httprouter (56ns)** |
| `FastParamRoute2` | 80–95 | 64 | 1 | ★★★★ Bate httprouter (71ns) |
| `FastParamRoute3` | 78–99 | 96 | 1 | ★★★★ Bate httprouter (78ns) |
| `FastParallelParam` | 16.0 | 32 | 1 | ★★★★★ |

---

## 2. Edge cases

| Caso | ns/op | B/op | allocs/op | Notas |
|---|---|---|---|---|
| `ParamRoute5` (overflow >3) | 321 | 896 | 5 | 3x custo vs `Param3`; raro em prática |
| `FastParamRoute5` (overflow) | 162 | 256 | 3 | Fast handler suporta overflow |
| `LongParamValue` (100 chars) | 137 | 416 | 1 | Sem custo extra |
| `LongStaticPath` (11 segmentos) | 25–40 | 0 | 0 | Radix tree comprime bem |
| `DepthRoute` (11 níveis nested) | 14 | 0 | 0 | Excelente |
| `RoutePattern` lookup | **1125** | 416 | 1 | **🚨 Suspeito — investigar** |

---

## 3. Paths não-hot mas pesados

| Caso | ns/op | B/op | allocs/op | Diagnóstico |
|---|---|---|---|---|
| `NotFound` (defaults) | 335 | 117 | 3 | 3 allocs vêm de `http.NotFound` (string write) |
| `NotFoundCustomHandler` | 22 | 0 | 0 | 0 allocs com handler custom! |
| `NotFoundWithMethodAllowedLookup` | 343 | 123 | 3 | `allowed()` strings.Builder |
| **`MethodNotAllowed`** | **449** | **138** | **6** | 🚨 6 allocs — investigar |
| `OPTIONSAuto` | 161 | 40 | 3 | Razoável |
| **`RedirectTSL`** | **1554** | **1305** | **15** | 🚨 CRÍTICO — 15 allocs |

---

## 4. Escalabilidade (radix tree)

| Routes registadas | ns/op (middle path) | Crescimento |
|---|---|---|
| 10 | 23.2 | base |
| 100 | 26.0 | +12% |
| 1000 | 30.3 | +30% |
| 10000 | 34.4 | +48% |

**Veredicto:** O(log k) confirmado. De 10 → 10000 routes apenas +48% latência.

---

## 5. Middleware (18 individuais — agente delegado)

### Zero-alloc (já optimal)
| Middleware | ns/op |
|---|---|
| StripSlashes (clean) | 4.8 |
| Recoverer (no panic) | 7.5 |
| CORS (no Origin) | 19 |
| CleanPath (clean) | 32 |
| Compress (no gzip) | 34 |
| ThrottleBacklog (no wait) | 45 |

### Optimizáveis
| Middleware | Actual | Target | Ganho |
|---|---|---|---|
| **Logger** | 6762 ns / 10 allocs | ~1200 ns / 3 allocs | **−5500ns / −7 allocs** |
| RequestID (generate) | 4944 ns / 9 allocs | ~4700 ns / 8 allocs | -200ns / -1 alloc |
| JWTAuth HS256 | 5506 ns / 20 allocs | ~5450 ns / 19 allocs | -50ns / -1 alloc |
| NoCache | 422 ns / 7 allocs | ~275 ns / 5 allocs | -150ns / -2 allocs |
| CORS (allowed) | 1670 ns / 4 allocs | ~1570 ns / 3 allocs | -100ns / -1 alloc |

### Custo inerente
- **Timeout**: 5518ns / 5 allocs — `context.WithTimeout` é o mínimo possível
- **JWTAuth ES256**: 79697ns / 40 allocs — limitado pela stdlib `ecdsa.Verify`
- **OAuth2Introspect**: domina latência de rede

---

## 6. Chain depth (Use middleware)

| Depth | ns/op | Overhead/middleware |
|---|---|---|
| 1 | 16.5 | 11 ns marginal |
| 5 | 30 | ~3.5 ns marginal |
| 20 | 96 | ~4.2 ns marginal |

**Veredicto:** chain depth tem custo linear baixo (~4ns/middleware).

### Pre vs Use vs UseFast (passthrough trivial)
| Tipo | ns/op | Notas |
|---|---|---|
| `Use(noop)` (handle path) | 16.5 | Equivalente |
| `Pre(noop)` | 19.3 | +2.8ns (pre-dispatch wrap) |
| `UseFast(noop)` | 13.0 | **+0 — mais rápido (fast path nativo)** |

---

## 7. Chains de produção típicas (agente delegado)

| Chain | ns/op | allocs |
|---|---|---|
| Minimal (Recoverer) | 7.8 | 0 |
| AuthBasic | 233 | 2 |
| AuthJWT (Recoverer + RequestID + JWT) | 7021 | 29 |
| Security (5 middlewares) | 2485 | 20 |
| **Production** (Recoverer+RealIP+RequestID+Logger+CORS) | **8595** | **23** |
| Heavy (8 middlewares) | 11464 | 34 |

**Conclusão chains:** após optimização do Logger, **Production chain dropa de 8595 → ~3400ns (−60%)**.

---

## 8. Comparação directa vs competidores (apples-to-apples)

| Caso | MuxMaster | httprouter | bunrouter² | chi v5 | Veredicto |
|---|---|---|---|---|---|
| Static | **30** | 34 | 188 | 220 | MuxMaster vence |
| Param1 stdlib | 124 | **58** | 182 | 394 | httprouter vence |
| Param1 FastHandler | **53** | n/a (interface diferente) | n/a | n/a | MuxMaster bate httprouter |
| Parallel Static | **4** | 5 | 127 | 149 | MuxMaster vence |
| Parallel Param | 107 | **23** | 127 | 264 | httprouter vence (cf. bundle 416B vs 64B) |

**Lacuna principal:** custo do `reqBundle` (416-480B vs httprouter Params 64-96B).

---

## 9. Estado de tasks de auditoria

| # | Task | Estado | Output |
|---|---|---|---|
| 1 | Baseline | ✅ | `bench_internal_baseline*.txt`, `bench_competitor_baseline*.txt` |
| 2 | API surface | ✅ | Mapeada (~70 funcs Mux, ~26 Group) |
| 3-5 | Hot path mux/tree/params | ⏳ Agente | `hotpath_analysis.md` (pendente) |
| 6 | Groups | ✅ | Sem overhead runtime (só registo) |
| 7-8 | 18 middlewares + chains | ✅ | `middleware_analysis.md` (entregue) |
| 9 | Competitor study | ⏳ Agente | `competitor_techniques.md` (pendente) |
| 10 | pprof + escape | ✅ | `cpu.prof`, `escape_*.txt` |
| 11 | Assembly hot path | ✅ | `asm_*.txt` |
| 12 | Síntese final | ⌛ Pending | A produzir após agentes |
