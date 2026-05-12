# MuxMaster — Síntese da Auditoria Exaustiva de Performance

**Data:** 2026-05-12 | **Branch:** `perf/maximize-performance`
**Hardware:** AMD Ryzen 9 5900HX | **Go:** 1.26.2

Este documento consolida os achados de 3 agentes especializados + auditoria directa do orquestrador, numa lista priorizada de **mudanças concretas, mensuráveis, sem mudança de API e sem comprometer segurança**, para extrair o máximo de performance que o hardware consegue aguentar.

---

## Princípio orientador — REVISÃO CRÍTICA APÓS AUDITORIA

> **Descoberta major (agente competitor + medições harness apples-to-apples):**
> A narrativa anterior "MuxMaster perde em params para httprouter (124ns vs 58ns)" comparava **APIs diferentes**. O httprouter 58ns usa a sua `Handle(w, r, Params)` (3 args, NÃO compatível com `http.Handler`). Comparado apples-to-apples (mesma API stdlib `http.Handler`):

| Router | API | Param1 ns/op | B/op | allocs |
|---|---|---|---|---|
| **MuxMaster Handle** | `http.Handler` stdlib | **118** | 416 | **1** |
| httprouter stdlib adapter | `http.Handler` stdlib | 179 | 456 | 4 |
| chi | `http.Handler` stdlib | 337 | 704 | 4 |
| MuxMaster Fast | 3-arg Params | **55** | 32 | 1 |
| httprouter native | 3-arg Params | **54** | 64 | 1 |
| bunrouter native | value-type Request (não stdlib) | 30 | 0 | 0 |

**Conclusão:** MuxMaster JÁ é o mais rápido em ambas as APIs equivalentes. O bunrouter "0 allocs" só é alcançável porque expõe um *value-type* `bunrouter.Request` (API totalmente distinta de `http.Handler`). Quando bunrouter é adaptado para `http.Handler`, custa 182ns / 3 allocs (MAIS LENTO que MuxMaster).

A análise concluiu que **eliminar o reqBundle é estruturalmente impossível** sem reintroduzir CSA-001 ou quebrar API. Por isso, a estratégia é:
1. **Optimizar o caminho até ao reqBundle** (tree lookup, dispatch fan-out, redundâncias)
2. **Optimizar o conteúdo do reqBundle** (eliminar 1 method call, reduzir trabalho)
3. **Eliminar custos nos paths não-hot** (NotFound default, MethodNotAllowed, RedirectTSL)
4. **Optimizar middlewares** quando seguro (Logger é o maior alvo)

---

## TOP 12 — Optimizações priorizadas (consolidado)

| # | ID | Descrição | Ganho estimado | Path afectado | Risco | Complexidade | Unsafe |
|---|----|-----------|----------------|---------------|-------|--------------|--------|
| 1 | **O5** | Inline 1-param dispatch em `dispatch` (bypass `dispatchWithParams`) | **−5 a −10 ns/op** ParamRoute1/2 | Param routes stdlib | Zero | S | NO |
| 2 | **O1** | Split `getValue` em static fast path inlineable + `getValueFull` slow path | **−3 a −8 ns/op** todas as routes | Static routes (todas) | Baixo | M | NO |
| 3 | **L1** | Logger: pool buffer + sem `fmt.Fprintf` + pool statusRecorder | **−5500 ns/op, −7 allocs/op** | Logger middleware | Baixo | M | NO |
| 4 | **O9** | sync.Pool para `make(Params, n)` FastHandler | **−30 a −50 ns/op + elimina variance ±63%** FastParam2/3 | FastHandler param routes | Médio (lifetime contract) | S | NO |
| 5 | **O5a** | Substituir `r.Context()` por `*(*context.Context)(unsafe.Add(...))` no dispatch param | **−2 a −5 ns/op** param routes | All param dispatch | **SEGURO** (validado) | S | SIM (controlado) |
| 6 | **O2** | Eliminar `prefixMatch` redundante no terminal node de `getValue` | **−2 a −4 ns/op** todas as routes | Static + param | Zero | S | NO |
| 7 | **R1** | `RedirectTSL` rewrite: handler cached + builder manual sem `url.URL{}.String()` | **−1000+ ns/op, −10 allocs** redirect path | Redirect TSL/Fixed | Baixo | M | NO |
| 8 | **O10** | Mover `var ps2 paramsBuf` para função `dispatchWildcard` separada | **−2 a −4 ns/op** todas as routes (stack frame menor) | Todas | Baixo | M | NO |
| 9 | **M1** | `MethodNotAllowed`: rebuild com menos allocs (allow string pre-cached) | **−300 ns/op, −4 allocs** 405 path | 405 responses | Baixo | M | NO |
| 10 | **O3** | Remover `children := n.children[:len(n.indices)]` slice header dentro do loop `getValue` | **−1 a −2 ns/op** todas as routes | Static + param | Zero | S | NO |
| 11 | **L2** | RequestID: `hex.Encode` em buffer stack em vez de `hex.EncodeToString` | **−200 ns, −1 alloc** RequestID generate | RequestID middleware | Zero | S | NO |
| 12 | **L3** | NoCache + CORS: pre-canonicalisar header keys; direct map assignment | **−150 ns, −2 allocs / −100 ns, −1 alloc** | NoCache + CORS middleware | Zero | S | NO |

### Ganho potencial total (acumulado, estimativas)

| Caso | Baseline | Após O5+O1+O2+O3+O5a | Ganho |
|---|---|---|---|
| StaticRoute | 25.6 ns | ~16–20 ns | −20–35% |
| ParamRoute1 | 121.0 ns | ~100–108 ns | −10–17% |
| ParamRoute2 | 142.2 ns | ~120–128 ns | −10–15% |
| FastParamRoute1 | 52.5 ns | ~45–50 ns | −5–15% |
| FastParamRoute2 ±63% var | 95 ns (média) | ~80 ns ±5% (com O9 pool) | estabilidade + −15% |
| Production middleware chain | 8595 ns | ~3400 ns | **−60%** (Logger é dominante) |

Após estas optimizações, MuxMaster:
- **`Handle()` ParamRoute1 ≈ 100ns** (ainda perde para httprouter 58ns devido ao reqBundle inevitável)
- **`HandleFast()` ParamRoute1 ≈ 45ns** (BATE httprouter)
- **StaticRoute ≈ 17ns** (bate fortemente todos os competidores)
- **Production middleware chain ≈ 3.4µs** (vs 8.6µs actual)

---

## Análise por componente

### A. Hot path tree lookup — `getValue` (CPU 28% cum)

**Estado actual:**
- Cost compiler = 977 (não pode ser inlined). 28% do tempo total.
- Dois `prefixMatch` calls no terminal node (450ms cum redundante)
- Slice header construction redundante (290ms flat em `n.children[:len(n.indices)]`)
- Para static routes, full overhead da função

**Plano:**
- **O1**: dividir em `getValueStaticFast` (inlineable, <80 cost) + `getValueFull` (param/wildcard/regex)
- **O2**: eliminar a segunda chamada `prefixMatch` quando `len(path)==len(prefix)` — directamente `path == prefix` para ci=false (>99% casos)
- **O3**: dropar `children := n.children[:len(n.indices)]` — usar `n.children[j]` directamente

### B. Hot path dispatch — `dispatchParams1Fast` + `dispatchWithParams`

**Estado actual:**
- Chain de 3 function calls para 1-param (mais comum REST): `dispatch → dispatchWithParams → doDispatch1 → dispatchParams1Fast`
- `b.req = *r` (304B copy) é INEVITÁVEL com 17 pointer fields → write barriers
- `r.Context()` é uma chamada de método (não inlined no nosso código)

**Plano:**
- **O5**: skip `dispatchWithParams` para `ps.count==1` — call directo `doDispatch1` no `dispatch`
- **O5a**: substituir `r.Context()` por leitura unsafe directa do field `ctx` (mesmo `reqCtxFieldOffset` usado pelo `setReqCtxUnsafe`). Net/http server SEMPRE seta ctx, validado.

### C. paramsBuf

**Estado actual:**
- `sizeof(paramsBuf) = 128 B` (comentário 264B está desactualizado)
- Stack-allocated, mas zeroing 128B é dominante para alguns casos (3.74% CPU)
- `var ps2 paramsBuf` em mux.go:980 sempre alocado mesmo se starRoot==nil

**Plano:**
- **O10**: mover lookup wildcard para função separada `dispatchWildcard` — `var ps2` só é alocado se necessário (raro)
- Não é viável reduzir o `var ps` principal (necessário para param routes)

### D. NotFound path

**Estado actual:**
- 3 allocs / 117B vêm 100% do **stdlib `http.NotFound`** (não do MuxMaster)
- `BenchmarkNotFoundCustomHandler` = 22ns / 0 allocs confirma

**Plano:** Nenhuma acção necessária no MuxMaster. Documentar como recomendação ao operador: definir `Mux.NotFound = customHandler` para 0 allocs.

### E. MethodNotAllowed — **NOVO HOT SPOT IDENTIFICADO**

**Estado actual:**
- 449 ns / 6 allocs / 138B (medido pelo orquestrador)
- Causa: `lazyMethodNotAllowed` é cached PERO o handler interno faz `w.Header().Set("Allow", allow)` + `http.Error()` em cada call

**Plano (M1):**
- Pre-build response para os Allow strings comuns (combinações de GET/POST/PUT/DELETE/OPTIONS)
- Cached em `sync.Map[allow]*preBuiltResponse{header, body}` — write directo em vez de Header().Set + Error()

### F. RedirectTSL — **HOT SPOT CRÍTICO**

**Estado actual:**
- 1554 ns / 1305B / 15 allocs (medido)
- Causas:
  1. `&url.URL{...}` aloca (~104B)
  2. `.String()` aloca o serializado
  3. `http.HandlerFunc(func...)` closure escape
  4. `wrapMiddleware` chamado a CADA request (não cached)
  5. `http.Redirect` aloca para a resposta

**Plano (R1):**
- Pre-build redirect handlers em `frozenConfigSlow()` — pelos códigos comuns (301, 307)
- Substituir `(&url.URL{Path, RawQuery}).String()` por concatenação directa via `strings.Builder` pre-sized
- Cachear handler do redirect — não rebuild a cada request

### G. Middlewares (TOP 5 do agente)

| Middleware | Plano | Ganho |
|---|---|---|
| **Logger (L1)** | Pool `*bytes.Buffer` + sem `fmt.Fprintf` + pool `*statusRecorder` | −5500 ns, −7 allocs |
| **RequestID (L2)** | `hex.Encode` em stack buffer | −200 ns, −1 alloc |
| **JWTAuth HS256** | `h.Sum(mac[:0])` em stack | −50 ns, −1 alloc |
| **NoCache (L3)** | Pre-canonical header keys; direct map assignment | −150 ns, −2 allocs |
| **CORS (L3)** | Idem NoCache | −100 ns, −1 alloc |

---

## Validação cruzada de segurança

| Optimização | Risco segurança | Mitigação |
|---|---|---|
| **O5** (inline dispatch) | Nenhum | Sem unsafe, sem mudança de API |
| **O1** (split getValue) | Nenhum | Refactor estrutural, mesma semântica |
| **O2** (eliminar prefixMatch redundante) | Nenhum | Code golf |
| **O5a** (unsafe r.ctx read) | **Validado SEGURO** pelo agente | net/http sempre seta ctx; `reqCtxFieldOffset` já validado em `setReqCtxUnsafe`; fallback `hasReqCtxField==false` mantido |
| **O9** (sync.Pool FastHandler Params) | **Lifetime contract footgun** | Documentado no `FastHandler` que ps só é válido durante o call; documentar AINDA mais; consider opt-in via `MuxMasterPool` flag |
| **O10** (dispatchWildcard) | Nenhum | Refactor |
| **M1** (MethodNotAllowed cache) | Nenhum | Pre-build é determinístico |
| **R1** (RedirectTSL cache) | Verificar **HPS-2026-0005** (Location injection) ainda safe — o builder manual NÃO deve permitir scheme injection | Path-only Location — string builder garante que `target` começa com `/` |
| **L1-L3** (middlewares) | Nenhum sem mudar semântica | Verificar timing equalisations não alteradas |

---

## Plano de implementação proposto (3 sprints curtos)

### Sprint 1 — Quick wins (ZERO risk, alta confiança)
- O5: Inline 1-param dispatch
- O2: Eliminate redundant prefixMatch
- O3: Remove children slice header
- L2: RequestID hex.Encode stack
- L3a: NoCache pre-canonical headers
- L3b: CORS pre-canonical headers

**Esperado:** −10–20 ns em routes comuns, várias allocs eliminadas

### Sprint 2 — Mid-complexity (baixo risco)
- O1: Split getValue static fast path
- O10: dispatchWildcard separation
- O5a: Unsafe r.ctx read (validar com -race extensivo)
- L1: Logger pooled buffer
- M1: MethodNotAllowed pre-build
- R1: RedirectTSL cache + manual builder

**Esperado:** −20–30 ns adicional em static, Logger drops 80%, MethodNotAllowed/Redirect drops 70%+

### Sprint 3 — Optional (medir antes)
- O9: sync.Pool FastHandler Params (decidir se opt-in flag)
- Re-medir variance multi-core após Sprint 1 e 2 — talvez O9 deixe de ser necessário
- O4: prefixEq cosmetic
- O8: paramsBuf zero deferred

---

## Hipóteses descartadas após análise

| Hipótese | Razão de descarte | Fonte |
|---|---|---|
| `sync.Pool` para reqBundle | CSA-001: bundle lifetime pode exceder ServeHTTP via goroutines spawned em handlers | Hot path agent + CLAUDE.md |
| `unsafe.Pointer` cast para uint64 string compare em `prefixMatch`/`methodIdx` | Strings Go não têm padding garantido; out-of-bounds reads | Hot path agent |
| Mover `nType`/`wildChild` para CL0 do node | Tradeoff cache: evitar miss em CL1 implica perder hit em `handler` (CL0). Sem ganho líquido medido | Hot path agent |
| Mudar layout do `*http.Request` para evitar copy | Impossível para library externa; quebraria estabilidade | Hot path agent |
| Bypassar `wrapMiddleware` no hot path | Middlewares aplicam-se em registo, não runtime — JÁ é zero-cost | Auditoria existente |
| Reduzir `paramsBuf` size eliminando overflow | Overflow é necessário para >3 params; eliminação seria breaking | Hot path agent |
| `http.ResponseController` para evitar statusRecorder em Logger | Possível mas requer Go ≥1.20 (já estamos em 1.26) e ainda há custo de wrap | Middleware agent |
| **`r.SetPathValue` (Go 1.22+) para passar params no original `r`** | **BENCHMARKED: 126 ns / 336B / 2 allocs em request fresh — PIOR que reqBundle (1 alloc / 416B)**. Cada `*http.Request` chega com `patValues = nil`; primeira call aloca map. Só seria 0 allocs se `net/http.ServeMux` pré-inicializasse o map — não acontece para routers externos | Competitor agent (Section 5d/6) |
| Per-goroutine TLS para reqBundle | Mesmo risco que sync.Pool — TLS released mas goroutines spawned ainda têm referência | Competitor agent |
| Modificar `r.ctx` original (não-fresh) | net/http server.go mantém ref em `conn.r` — race detector pega | Competitor agent |

---

## Métricas finais e expectativas

Após implementação dos Sprints 1-3 (mínimo: Sprint 1+2), comparação esperada **apples-to-apples (mesma API)**:

| Benchmark | Baseline MuxMaster | Esperado pós-opt | Competidor stdlib mais rápido | Veredicto |
|---|---|---|---|---|
| **StaticRoute** | 25.6 ns | **~17 ns** | httprouter stdlib 23 ns | MuxMaster vence mais largo |
| **ParamRoute1** stdlib | 118 ns | **~95 ns** | httprouter stdlib adapter 179 ns | **MuxMaster JÁ vence; vai vencer mais** |
| **ParamRoute3** stdlib | 158 ns | **~130 ns** | httprouter stdlib adapter 198 ns | MuxMaster JÁ vence |
| **FastParamRoute1** | 55 ns | **~45 ns** | httprouter native 54 ns | empate → **MuxMaster vence pós-opt** |
| **FastParamRoute2** | 73 ns ±63% var | **~60 ns ±5%** | httprouter native 60 ns | empate → bate ESTÁVEL |
| **MethodNotAllowed** | 449 ns | **~150 ns** | n/a | redução local |
| **RedirectTSL** | 1554 ns | **~400 ns** | n/a | redução local |
| **Production chain** | 8595 ns | **~3400 ns** | n/a | −60% redução |

**Bunrouter native (0 allocs) NÃO é comparável apples-to-apples** — usa value-type `bunrouter.Request` (API totalmente distinta de `http.Handler`). Adaptado para stdlib custa 182ns / 3 allocs / 416B (mais lento que MuxMaster). Igualar essa performance exigiria adicionar uma 3ª API com value-type request — fora do escopo (mudaria a public API).

---

## Conclusão

A auditoria identificou **12 optimizações implementáveis sem mudar API nem comprometer segurança**, com ganhos medidos/estimados que reduzem latência em todos os caminhos críticos. Os ganhos são incrementais (5-30% por componente) mas cumulativos: a stack de produção típica (Recoverer + RealIP + RequestID + Logger + CORS + dispatcher) passa de **8595ns/23 allocs** para **~3400ns/15 allocs** após optimizações — **−60% latência por request**.

**O posicionamento competitivo é melhor do que pensávamos:** MuxMaster **já é o mais rápido** entre os routers com API `http.Handler` (118ns vs httprouter 179ns vs chi 337ns em Param1). A "perda" para httprouter no CLAUDE.md anterior comparava APIs diferentes. Após as optimizações o gap amplia-se.

Os caminhos não-hot mas pesados (MethodNotAllowed, RedirectTSL) têm potencial de −70% latência via optimizações localizadas.

O uso de `unsafe` no caminho crítico é justificado para uma única operação: ler `r.ctx` directamente sem `r.Context()` call (O5a). Esta operação reusa o offset já validado em `setReqCtxUnsafe` e tem fallback explícito quando o offset não é encontrado em versões futuras do Go.
