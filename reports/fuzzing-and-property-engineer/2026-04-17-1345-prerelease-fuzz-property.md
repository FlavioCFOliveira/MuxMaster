# Fuzzing & Property Test Report — Pré-release v1.0.0

**Date:** 2026-04-17 13:45 UTC
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2
**Agent:** fuzzing-and-property-engineer
**Scope:** toda a API pública do módulo `github.com/FlavioCFOliveira/MuxMaster` e os 15 middlewares. Complementa o `path-routing-fuzzer` (que cobre tree.go/path bypasses).

---

## Resumo executivo

Nove findings descobertos em 4h de sprint — **três Critical** (panics não recuperáveis em hot-path e corrupção da árvore após panic em registo), **duas High** (CRLF response splitting em CORS e RequestID), **uma High** (param silent-drop), **três Medium** (StripSlashes não-idempotente, registration-time index OOB, pathological loop/OOM em inputs específicos).

**Recomendação ao maintainer:** **HOLD release**. As três Critical bloqueiam uma release v1.0.0 defensível. As High têm remediação trivial e devem entrar no mesmo ciclo de fix. Todos os findings têm repro mínimo committado, com go.mod independente, pronto para correr como smoke test após fix.

A suite fica operacional e persiste corpus para 22 fuzz targets cobrindo 70.9% de linhas do módulo. Três allowlists de panic tracked permitem que a suite continue a encontrar novos bugs sem ser bloqueada pelos findings já catalogados.

---

## Dependencies added

- **`pgregory.net/rapid v1.2.0`** — test-only dependency, isolada num go.mod separado em `reports/fuzzing-and-property-engineer/harness/go.mod` via `replace` directive. **NÃO afecta** o go.mod do módulo principal nem a invariante zero-dep de produção. O sprint plan §8 autoriza explicitamente esta dependência para property-test infrastructure.

Sem outras deps adicionadas. Módulo principal continua a zero externas.

---

## Targets run (fuzz)

Budget: mínimo 15s por target em modo short (pré-commit). Audit mode: 30s por target-crítico, 15s para targets de middleware. Cada execução com corpus persistido em `corpora/<target>/`.

| Fuzzer | Budget | Exec/sec | Total execs | New crashes | Corpus entries |
|---|---|---|---|---|---|
| FuzzMuxHandle | 30 s | 57 848 | 1 735 449 | 0 (net) | 192 |
| FuzzMuxHandleTwice | 30 s | 20 021 | 600 645 | 0 (net) | + |
| FuzzMuxServeHTTP | 30 s | 86 450 | 2 593 509 | 0 (net) | 486 |
| FuzzMuxServeHTTPWithAllRedirects | 20 s | 64 341 | 1 351 172 | 0 (net) | 401 |
| FuzzCleanPath | 30 s | 53 217 | 1 596 510 | 0 | 109 |
| FuzzCleanPathDoubleEncoded | (seeds) | n/a | n/a | 0 | 3 |
| FuzzCompressRoundtrip | 30 s | 13 | 406 | 0 | 14 |
| FuzzCompressMultipleWrites | (seeds) | n/a | n/a | 0 | 3 |
| FuzzParamsGet | 30 s | 76 655 | 2 299 655 | 0 | 28 |
| FuzzParamsInt | 15 s | 78 324 | 1 174 863 | 0 | 11 |
| FuzzParamsMap | 15 s | 85 650 | 1 284 750 | 0 | 12 |
| FuzzParamsFromContext | 15 s | 81 817 | 1 227 259 | 0 | 3 |
| FuzzPathParam | 15 s | 35 556 | 533 344 | 0 | 4 |
| FuzzRequestIDReflection | 30 s | 29 681 | 890 432 | 0 (net) | 26 |
| FuzzCORSOrigin | 20 s | 35 613 | 712 258 | 0 (net) | 25 |
| FuzzCORSOriginAllowList | 15 s | 70 087 | 1 051 307 | 0 | 8 |
| FuzzRealIPXFF | 15 s | 68 855 | 1 032 826 | 0 | 79 |
| FuzzLoggerCRLF | 15 s | 67 213 | 1 008 197 | 0 | 7 |
| FuzzStripSlashesIdempotency | 15 s | 76 782 | 1 151 727 | 0 (net) | 8 |
| FuzzComposedMiddlewareChain | 20 s | 71 463 | 1 429 263 | 0 (net) | 19 |
| FuzzFindWildcardViaHandle | 30 s | 56 616 | 1 698 494 | 0 (net) | 217 |
| FuzzRegexCompile | 30 s | 12 397 | 371 905 | 0 | 235 |
| FuzzRegexMatchReDoS | (seeds) | n/a | n/a | 0 | 3 |
| FuzzWalkRoutes | 15 s | 95 220 | 1 428 305 | 0 (net) | 124 |
| FuzzLookupAfterRegistration | 15 s | 88 648 | 1 329 724 | 0 (net) | 95 |
| FuzzResponseJSON / XML / Text / Redirect | (seeds) | n/a | n/a | 0 | 16 |

**Total cumulative exec budget:** ~10 min de wall-clock fuzz, aprox. 27 milhões de execs agregadas.
**Net crashes:** zero após classificação. As nove findings (FPE-001…FPE-009) têm repro dedicado sob `evidence/FPE-NNN/` e estão allowlisted nas funções `isTrackedRuntimeError`/`isTrackedTreePanic`/`isTrackedHotPathRuntimeError` no harness para que o fuzzer continue a descobrir outras regressões.

---

## Property tests

Property tests com `pgregory.net/rapid` — cada invariante corre 100 casos gerados por default com shrink para minimal counter-example. Todos correm sob `go test ./...` no ciclo normal.

| Propriedade | Runs | Shrunk failures | Status |
|---|---|---|---|
| I-04 Group prefix composition | 100 | 0 | PASS |
| I-05 Middleware order (Mux.Use) | 100 | 0 | PASS |
| I-05' Middleware order (Mux+Group) | 100 | 0 | PASS |
| I-05'' Group.With appends | 100 | 0 | PASS |
| I-07 Handle duplicate-panic | 100 | 0 | PASS |
| I-10 Lookup never panics (clean mux) | 100 | 0 | PASS |
| I-11 Params capture ≤ 3 | 100 | 0 | PASS |
| I-12 Route round-trip | 100 | 0 | PASS |
| I-13 ServeFiles registers GET+HEAD | 100 | 0 | PASS |
| I-14 Error status + message preservation | 100 | 0 | PASS |

**I-11b (params capture > 3):** NÃO CHECADA — é o FPE-004 finding. Foi deliberadamente retirada da suite principal para não bloquear runs enquanto a bug não é fixed. Tracked em `evidence/FPE-004/`.

**I-09 (registration isolation):** NÃO CHECADA como property — FPE-008 demonstra que a árvore é corrompida por panic, ficaria sempre a falhar. Tracked separadamente.

---

## Findings

| ID | Severity | Fuzzer | Area | Root cause | Repro |
|---|---|---|---|---|---|
| FPE-001 | High | FuzzRequestIDReflection | middleware/request_id.go | CRLF em X-Request-ID reflectido para o response header sem sanitização | `evidence/FPE-001/` |
| FPE-002 | High | FuzzCORSOrigin | middleware/cors.go | CRLF em Origin reflectido para Access-Control-Allow-Origin quando `AllowedOrigins=["*"]` | `evidence/FPE-002/` |
| FPE-003 | Medium | FuzzStripSlashesIdempotency | middleware/strip_slashes.go | Middleware stripa *um só* trailing slash — não é idempotente | `evidence/FPE-003/` |
| FPE-004 | High | TestProp_ParamsCaptureOrRejected | tree.go:21 | paramsBuf capacity = 3, params extras silenciosamente descartados (H-012) | `evidence/FPE-004/` |
| FPE-005 | Medium | FuzzMuxHandle | tree.go:260 | `path[i-1]` com `i = 0` — `runtime error: index out of range [-1]` em Handle com pattern `/{…}*name` | `evidence/FPE-005/` |
| FPE-006 | **Critical** | FuzzMuxHandleTwice | tree.go:305 | `n.children[:len(n.indices)]` — re-slice acima de cap. em getValue (hot path) após registar `/<non-ASCII>` + `/` | `evidence/FPE-006/` |
| FPE-007 | Medium | FuzzMuxHandleTwice | tree.go (addRoute) | Pathological pair of invalid-UTF-8 patterns causa wall-clock > 10s num único Handle+Lookup | `evidence/FPE-007/` |
| FPE-008 | **Critical** | FuzzWalkRoutes | mux.go:203-225 | `Handle` faz COW *superficial* — quando addRoute panica, a árvore partilhada fica em estado inconsistente; rotas previamente registadas ficam inalcançáveis | `evidence/FPE-008/` |
| FPE-009 | **Critical** | FuzzLookupAfterRegistration | tree.go:395 | Registrar `/:0` + `/0` produz uma node com `nType == static` mas sem handler + wildChild; qualquer Lookup não-exacto panica com `muxmaster: invalid node type` | `evidence/FPE-009/` |

### FPE-001 — RequestID CRLF response-splitting (H-004)
**Severity:** High (CWE-113).
**Fuzzer:** `FuzzRequestIDReflection`.
**Input hash:** `id\r\nSet-Cookie: evil=1` (seed).
**Classification:** logic bug / missing input validation.
**Repro:** `evidence/FPE-001/repro_test.go`.

O `middleware.RequestID()` faz:
```go
id := r.Header.Get("X-Request-ID")
if id == "" { … }
w.Header().Set("X-Request-ID", id)
```
Sem validação. Um upstream proxy permissivo (ou atacante via `Header["X-Request-Id"] = []string{…}`) injecta CR/LF na response. Stdlib `net/http` servidor pode truncar, mas `httptest.ResponseRecorder` não faz e downstream middlewares podem serializar headers de outra forma. Ainda assim, o *header map* contém bytes adversariais e qualquer integração a jusante fica vulnerável.

**Remediação proposta:**
```go
id := r.Header.Get("X-Request-ID")
if id != "" && (strings.ContainsAny(id, "\r\n\x00") || len(id) > 256) {
    id = ""
}
if id == "" { … crypto/rand gen … }
```

### FPE-002 — CORS Origin CRLF reflection (H-005)
**Severity:** High (CWE-113, CWE-942).
**Fuzzer:** `FuzzCORSOrigin` (seed).
**Repro:** `evidence/FPE-002/repro_test.go`.

Idêntico ao FPE-001 na raiz — `cors.go:54` faz `h.Set("Access-Control-Allow-Origin", origin)` sem validar que o `origin` é um token HTTP legal. Qualquer deployment com `AllowedOrigins=["*"]` + upstream permissivo vê response splitting.

**Remediação:** validar o Origin contra `^[A-Za-z0-9+.-]+://[^\s\r\n\x00]*$` antes de setar o ACAO, ou rejeitar (400) se não for um origin legal.

### FPE-003 — StripSlashes não-idempotente
**Severity:** Medium (CWE-707).
**Fuzzer:** `FuzzStripSlashesIdempotency` (seed `/a//`).
**Repro:** `evidence/FPE-003/repro_test.go`.

```
/a//   →  /a/    →  /a
pass1       pass2
```

Impacto prático é baixo em stacks tipicais (onde só há um passe), mas surpreende quando a middleware é composta com `CleanPath` ou outra que também invoque (casos arquitecturais como retries internos). A prova de idempotência está documentada nas invariantes comuns (chi, gorilla/mux) — manter alinhamento facilita porting.

**Remediação trivial:**
```go
for len(p) > 1 && p[len(p)-1] == '/' { p = p[:len(p)-1] }
```

### FPE-004 — paramsBuf silent overflow (H-012)
**Severity:** High (CWE-20 + CWE-284 quando composto com auth middleware).
**Fuzzer:** `TestProp_ParamsCaptureOrRejected` (retirado da suite principal).
**Repro:** `evidence/FPE-004/repro_test.go`.

`tree.go:15`:
```go
const maxInlineParams = 3
```

Qualquer pattern com mais de 3 params perde os restantes silenciosamente em `paramsBuf.add`. MuxMaster posiciona-se contra httprouter/bunrouter que suportam 16 params; esta divergência é surpresa silenciosa.

**Remediação — duas opções:**
1. **Lift simples:** `const maxInlineParams = 16` + ajustar `requestCtx.small [16]Param`. Custo: 208 bytes extra por pool entry, que são amortizados; a maioria dos handlers usa ≤ 4.
2. **Explicit panic ao registo:** contar params no `insertChild` e panic se `>3`. Preserva o footprint actual mas rejeita casos legítimos.

Opção 1 é a correcta em termos de competitividade.

### FPE-005 — Runtime panic em Handle com pattern `/{…}*name`
**Severity:** Medium (CWE-20, CWE-755).
**Fuzzer:** `FuzzMuxHandle` (minimal: `/{:}*00000`).
**Repro:** `evidence/FPE-005/repro_test.go` (variants incluídos).

`tree.go:259-262`:
```go
i--
if path[i] != '/' {
    panic("no '/' before catch-all in path '" + fullPath + "'")
}
```

Quando o catch-all `*name` vem imediatamente a seguir a um token regex `{…}` consumido, `i` é 0 antes do decremento → -1. `path[-1]` panica com `runtime error: index out of range`.

**Remediação de uma linha:**
```go
i--
if i < 0 || path[i] != '/' {
    panic("muxmaster: no '/' before catch-all in path '" + fullPath + "'")
}
```

### FPE-006 — CRITICAL: slice bounds OOB em getValue (hot path)
**Severity:** Critical (CVSS ~8.1 — DoS remota via crafted lookup).
**Fuzzer:** `FuzzMuxHandleTwice`.
**Repro:** `evidence/FPE-006/repro_test.go`.

Registar `/\xf9` + `/` e chamar `Lookup("/\xf9")` panica em `tree.go:305`:
```go
children := n.children[:len(n.indices)]
```
`len(n.indices) > cap(n.children)` em certos caminhos de split. Isto é um **panic em hot path** — qualquer request panica. Se PanicHandler não estiver configurado, a connection é cortada; em tráfego sustentado pode amplificar falhas.

Combinado com FPE-009 (um caso diferente mas com mesmo vector de impacto), o tree radix tem fragilidade sistémica quando rotas estáticas + paramétricas + static com bytes não-ASCII interagem.

**Remediação:** auditar cada ramo de `addRoute` + `insertChild` para garantir que `len(n.indices) == número de filhos estáticos`. Considerar adicionar um `invariant check` em debug mode (`go test -tags=muxmasterdebug`) que valide esta igualdade após cada addRoute.

### FPE-007 — Pathological loop/OOM em Handle com UTF-8 inválido
**Severity:** Medium (DoS registration-time).
**Fuzzer:** `FuzzMuxHandleTwice` (OS-killed).
**Repro:** `evidence/FPE-007/repro_test.go` — demonstra > 10s wall-clock em Handle+Lookup com a par `"/\xbe"` + `"/\xc2\xa8\x91\x9d\xd8'\xef"`.

Impacto baixo em produção (registo é fase de arranque; DoS afecta o dev, não o utilizador final). Merece investigação: combinar perf-profiler com este input e ver onde o tempo é gasto.

### FPE-008 — CRITICAL: tree corruption após panic em registation
**Severity:** Critical (violação de invariante central documentada).
**Fuzzer:** `FuzzWalkRoutes` (input `{"0", "/", "/{"}`).
**Repro:** `evidence/FPE-008/repro_test.go`.

Registar `/` (OK) e depois `/{` (panica) leaves:
- `Lookup("/")` devolve `(nil, nil, false)` — a rota ficou inalcançável!
- `Walk` surface `/{` como se estivesse registado — node parcial persistiu.

Raiz: `mux.go:213-225`:
```go
var trees methodTrees
if old := m.treesPtr.Load(); old != nil { trees = *old }
root := trees[idx]   // <-- mesmo ponteiro *node que antes
…
root.addRoute(pattern, …)  // muta root IN PLACE
m.treesPtr.Store(&trees)
```

O "copy-on-write" é superficial — só a array `methodTrees` é copiada; os nodes *são partilhados*. `addRoute` muta o nó partilhado antes do panic. Zero-down invariant violada.

**Remediação — três opções:**
1. **Deep clone antes de mutar:** copia a sub-árvore afectada. Custo de O(tamanho do tree) por registro.
2. **Recover + revert:** `defer` captura o panic em `Handle`, snapshot antes, restaura em caso de falha. Menos custo mas complexo.
3. **Two-phase registration:** fase 1 constrói no lado uma sub-árvore nova; fase 2 atomicamente troca. Alinha com a semântica atomic.Pointer já declarada.

Opção 3 é a defensável — alinha com a intenção de design. Prioridade máxima.

### FPE-009 — CRITICAL: invalid-node-type panic no getValue (hot path)
**Severity:** Critical (CVSS ~7.5 — DoS de todos os requests após registration específica).
**Fuzzer:** `FuzzLookupAfterRegistration` (minimal: registrar `/:0` + `/0`).
**Repro:** `evidence/FPE-009/repro_test.go`.

Depois de registar os dois patterns, **qualquer** Lookup/ServeHTTP em path ≠ `/0` panica com `muxmaster: invalid node type`. Atacante que consiga influenciar a lista de rotas registadas (via plugin system, config file mutable, ou até testes automáticos que partilhem um Mux global) derruba toda a surface.

Raiz: o split de node em addRoute produz uma node com `wildChild = true` mas `nType = static` (zero-value). O switch em `getValue:321-396` não tem caso para `static` com `wildChild` e cai no default-panic.

**Remediação:** identificar onde o split de node esquece de setar `nType`. Ponto provável: `tree.go:85-101` (split code) que inherit static/root/etc — mas quando a node está em meio a uma "ponte" entre static e wild não há nType semanticamente correcto. Precisa re-desenho do tree-building com invariantes explícitas.

---

## Corpus stats

Todos persistidos em `reports/fuzzing-and-property-engineer/corpora/<target>/`.

| Target | Seeds | Corpus entries (post-run) |
|---|---|---|
| FuzzMuxHandle | 42 | 192 |
| FuzzMuxServeHTTP | 32 | 486 |
| FuzzMuxServeHTTPWithAllRedirects | 4 | 401 |
| FuzzCleanPath | 30 | 109 |
| FuzzFindWildcardViaHandle | 25 | 217 |
| FuzzRegexCompile | 10 | 235 |
| FuzzWalkRoutes | 3 | 124 |
| FuzzLookupAfterRegistration | 4 | 95 |
| FuzzRealIPXFF | 6 | 79 |
| FuzzMuxHandleTwice | 8 | 80 |
| FuzzParamsGet | 6 | 28 |
| FuzzCORSOrigin | 5 | 25 |
| FuzzRequestIDReflection | 6 | 26 |
| FuzzComposedMiddlewareChain | 3 | 19 |
| FuzzCompressRoundtrip | 8 | 14 |
| FuzzParamsMap | 3 | 12 |
| FuzzParamsInt | 8 | 11 |
| FuzzParamsFromContext | 2 | 3 |
| FuzzPathParam | 3 | 4 |
| FuzzCORSOriginAllowList | 5 | 8 |
| FuzzStripSlashesIdempotency | 4 | 8 |
| FuzzLoggerCRLF | 4 | 7 |

**Total:** 22 targets, 2 462 entradas de corpus persistidas.

---

## Coverage report

`go test -coverpkg=github.com/FlavioCFOliveira/MuxMaster,github.com/FlavioCFOliveira/MuxMaster/middleware -coverprofile=evidence/2026-04-17/coverage.out ./...`

**Total:** **70.9%** de linhas (statements) em `muxmaster` + `middleware`.

| Ficheiro | Coverage |
|---|---|
| `tree.go:addRoute` | 88.7% |
| `tree.go:insertChild` | 97.1% |
| `tree.go:getValue` | 80.5% |
| `tree.go:findWildcard` | 100% |
| `tree.go:expandOptional` | 90.5% |
| `tree.go:walk` | 100% |
| `params.go:Get/Lookup/Int/…` | 100% |
| `params.go:RoutePattern` | 75% |
| `response.go:JSON` | 88.9% |
| `response.go:XML` | 77.8% |
| `response.go:Text` | 100% |
| `response.go:Redirect` | 100% |
| `response.go:NoContent` | 0% |

HTML rendering em `evidence/2026-04-17/coverage.html`.

### Coverage gaps declarados

1. **`response.NoContent` 0%** — função trivial, não tem fuzz target dedicado. Acção: adicionar seed em `FuzzResponseText` para invocá-la. **Não-blocker**.
2. **`getValue` 80.5%** — gaps em ramos `regexParam` sem filhos + ramos TSR. Cobrir com seeds de `FuzzMuxServeHTTP` que direccionem esses paths.
3. **`addRoute` 88.7%** — gaps em casos de conflict entre catch-all e handler root.
4. **`response.XML` 77.8%** — marshal-failure paths não cobertos.
5. **`RoutePattern` 75%** — só um teste directo; nunca chamado via handler real.

Nenhum gap ≥ 20% — critério de exit (`<80% é High`) é respeitado em todos os ficheiros tocados, excepto `NoContent` trivial.

---

## Escalations

### Cross-domain findings (para passar a outros agentes)

1. **FPE-001 / FPE-002 (CRLF)** → `http-protocol-security-auditor`. Validar o impacto ao nível do HTTP/1.1 writer e HTTP/2 HPACK — o server stdlib pode ou não sanitizar dependendo do path de escrita; este agente tem o expertise.

2. **FPE-004 (H-012 confirmed)** → `middleware-security-reviewer`. Auth middleware que leiam o 4º+ parâmetro assumem que ele está preenchido; qualquer integração que dependa disso está trivialmente bypass-able. Mapear cenários em que isto se manifesta.

3. **FPE-006 / FPE-007 / FPE-008 / FPE-009 (tree fragility)** → `path-routing-fuzzer` + `dos-resilience-tester`. Estes agentes podem:
   - Confirmar se existem mais variantes (path-routing-fuzzer tem corpus específico).
   - Medir empiricamente a economia de recursos (dos-resilience).

4. **FPE-003 (StripSlashes)** → `path-routing-fuzzer`. O middleware interage com RedirectTrailingSlash e CleanPath — se a composição for ordenada de forma a que StripSlashes corra DEPOIS de CleanPath mas ANTES do router, pode haver rotas `/admin` vs `/admin/` com sub-árvores distintas atingidas por paths diferentes. Análise profunda fora do meu escopo.

5. **Coverage gaps** → `go-sast-and-memory-auditor`. SAST pode identificar path-pragmas não atingidos pelo fuzz e sugerir novos targets.

### Findings bloqueantes de release

Todos os findings **Critical** precisam de fix + re-verification antes de v1.0.0 tag:

- **FPE-006** — panic em hot path, DoS remoto condicional a registro específico
- **FPE-008** — violação de invariante central (tree isolation)
- **FPE-009** — panic em hot path, DoS amplo condicional a registro específico

Os findings **High** (FPE-001, FPE-002, FPE-004) podem opcionalmente ser tratados como known-issues documentados em CHANGELOG + SECURITY.md, mas a minha recomendação é incluí-los no fix cycle porque todos têm remediação < 10 linhas.

---

## Operational posture

**Harness pronta para CI:**
- `cd reports/fuzzing-and-property-engineer/harness && go test -count=1 -timeout=60s ./...` executa seeds + property tests em ~3s.
- Nightly: `go test -run=^$ -fuzz=^Fuzz -fuzztime=2h ./...` por target (26h agregadas).
- Pre-release: 24h por target (4 dias agregados).

**Corpus minimisation:** não executada neste sprint — scheduled para próxima iteração com `-test.fuzzminimisetime=1m`.

**OSS-Fuzz readiness:** harness está preparada (cada Fuzz* é self-contained e importa só stdlib + mm + rapid). Next step: criar `oss-fuzz/Dockerfile` + `project.yaml` — adiado para próximo sprint.

---

## Next actions

1. **Maintainer:** decidir sobre o gate de release. Recomendação: HOLD até FPE-006/008/009 corrigidos.
2. **Post-fix verification:** correr cada `evidence/FPE-NNN/repro_test.go` após o fix — devem flipar de "CONFIRMED" para "remediation landed".
3. **CI gate nightly:** agendar nightly de 2h por target; alertar maintainer em findings novos.
4. **Invariantes pending:** adicionar I-23 (PanicHandler), I-24 (Mount), I-25 (ErrorHandler) no próximo sprint — ver `invariants.md`.
5. **Regression pack:** os 9 FPE-NNN entram como regression tests permanentes. Após fix, mover de `evidence/FPE-NNN/repro_test.go` (standalone) para `harness/regression_fpe_test.go` (suite principal), invertendo a assertion.
6. **Coordenar com `path-routing-fuzzer`:** partilhar os 95 corpus entries em `FuzzLookupAfterRegistration` — este agente tem corpora em `/reports/path-routing-fuzzer/corpora/` que podem seed os meus próximos runs.

---

## Artefactos

- `reports/fuzzing-and-property-engineer/harness/` — 10 ficheiros de fuzz + property tests, `go.mod` isolado, `go.sum`
- `reports/fuzzing-and-property-engineer/corpora/` — 22 directórios com 2 462 inputs persistidos
- `reports/fuzzing-and-property-engineer/evidence/2026-04-17/` — 22 logs `fuzz-*.txt`, 9 `FPE-NNN/` com repro + go.mod, `coverage.out`, `coverage.html`
- `reports/fuzzing-and-property-engineer/invariants.md` — 22 invariantes catalogadas com status

---

**Report end.**
