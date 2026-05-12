# MuxMaster — Auditoria exaustiva de performance — Findings preliminares

**Data:** 2026-05-12
**Branch:** `perf/maximize-performance`
**Hardware:** AMD Ryzen 9 5900HX (16 threads), Linux 6.8.0-111, Go 1.26.2
**Metodologia:** `go test -bench=. -benchmem -count=10 -benchtime=1s` + benchstat + pprof CPU/mem + objdump + gcflags=-m=2

---

## 1. Baseline confirmado (10 runs com benchstat)

| Benchmark | ns/op | B/op | allocs/op | Notas |
|---|---|---|---|---|
| `StaticRoute` | **25.57** ± 2% | 0 | 0 | Bate httprouter (33.8 ns) |
| `ParamRoute1` | 121.0 ± 1% | 416 | 1 | Vs httprouter 58 ns |
| `ParamRoute2` | 142.2 ± 1% | 448 | 1 | Vs httprouter 71 ns |
| `ParamRoute3` | 147.6 ± 1% | 480 | 1 | Vs httprouter 78 ns |
| `WildcardRoute` | 123.4 ± 3% | 416 | 1 | Vs httprouter 50 ns |
| `NotFound` (defaults) | 334.9 ± 150% | 117 | 3 | Variância enorme |
| `ParallelStaticRoute` | 4.047 ± 3% | 0 | 0 | Excelente |
| `ParallelParamRoute` | 113.2 ± 2% | 416 | 1 | Vs httprouter 22.5 ns ⚠️ |
| `FastStaticRoute` | 26.83 ± 1% | 0 | 0 | ≈ stdlib |
| `FastParamRoute1` | **52.55** ± 1% | 32 | 1 | **Bate httprouter (56 ns)** |
| `FastParamRoute2` | 95.03 ± 25% | 64 | 1 | Variância alta |
| `FastParamRoute3` | 77.99 ± 63% | 96 | 1 | Variância MUITO alta |
| `FastParallelParamRoute` | 15.95 ± 1% | 32 | 1 | Excelente |

### Casos extra medidos (5 runs)

| Bench | ns/op | B/op | allocs/op | Diagnóstico |
|---|---|---|---|---|
| `NotFoundCustomHandler` | 22 | 0 | 0 | **Confirma: 3 allocs do NotFound default vêm de `http.NotFound`** |
| `NotFoundWithMethodAllowedLookup` | 343 | 123 | 3 | `allowed()` faz string builder |
| **`MethodNotAllowed`** | **449** | **138** | **6** | **6 allocs! `http.Error()` + headers** |
| `OPTIONSAuto` | 161 | 40 | 3 | Razoável |
| **`RedirectTSL`** | **1554** | **1305** | **15** | **CRITÍCO. closure + url.URL{}.String() + http.Redirect alocam pesado** |
| `PathParamLookup` | 132–196 | 416 | 1 | OK (variância 50%) |
| `PathParamFast` | 46–52 | 32 | 1 | Excelente |
| `ParamsFromContext` | 152–182 | 448 | 1 | OK |

---

## 2. Profile CPU (5s runs em ParamRoute1/3 + Static + FastParam1)

**Top hotspots (flat%):**
| % flat | % cum | Função |
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

**Totais por categoria:**
- **Tree lookup (getValue + memeq + prefixMatch + foldEq + paramsBuf.add)**: ~37% cum
- **Bundle alloc (mallocgc + nextFreeFast + writeHeapBits + memclr + ...)**: ~16% cum
- **dispatch path scaffolding**: ~10% cum
- **HandlerFunc dispatch**: 2.72% cum

---

## 3. Análise hot path linha-a-linha

### `dispatch` (mux.go:865)
| Linha | ns acumulado | Operação | Optimização possível |
|---|---|---|---|
| 867: `urlPath := r.URL.Path` | 170ms | Load | nenhuma directa |
| 873: `trees := m.treesPtr.Load()` | 110ms | Atomic load | OK |
| 876: `methodIdx(r.Method)` | 460ms | Switch case (já optimizado para CMPW/CMPL pelo compilador) | Reordenar para GET primeiro pode poupar 1-2ns |
| 885: `var ps paramsBuf` | 190ms | Stack zeroing (128B) | Skip se `maxParams==0` (já feito) |
| 890: `root.getValue(...)` | 6.85s cum | **Lookup tree** (72% do dispatch) | Foco principal |
| 898: `fps := make(Params, ps.count)` | 1.49s cum | Alloc Params para FastHandler | Investigar pool seguro |
| 922: `dispatchWithParams(...)` | 5.87s cum | **Alloc reqBundle (>3 params) ou dispatch1/2** | Foco principal |

### `getValue` (tree.go:442)
| Linha | ms | Operação | Optimização |
|---|---|---|---|
| 445: `prefix := n.path` | 290ms | Load string header | Layout cache line já optimizado (CL0) |
| 447: `if len(path) > len(prefix)` | 280ms | Length compare | OK |
| 448: `prefixMatch(...)` | 1.99s cum | **String compare (memequal)** | Inline byte compare para len≤16; uint64 reads via unsafe |
| 451: `path = path[len(prefix):]` | 620ms | Slice header construction | Inevitável |
| 454: `c := path[0]` | 130ms | Bounds check + load | gcassert directive |
| 455: `children := n.children[:len(n.indices)]` | 290ms | Slice header construction | Refactor: garantir len(children) == len(indices) (sem +wildchild misturado) |
| 456-457: `for j := range len(n.indices); foldEq(c, n.indices[j], ci)` | 470ms | Linear scan dos indices | Indices é tipicamente 1-3 chars; já é óptimo |
| 482: `params.add(name, value)` | 380ms cum | Store em buf (gc write barrier check) | unsafe store sem barrier se buf é stack-allocated |

### `dispatchParams1Fast` (params.go:246)
Sequência observada no assembly:
1. **`runtime.newobject` para `reqBundle1`** (1 alloc 416B class) — INEVITÁVEL com a actual arquitectura
2. **Set `Context` field** com `gcWriteBarrier4`
3. **Set `pattern`, `small[0]`, `params`** (várias stores, vários barrier checks)
4. **`*r` (304B) copy** via `MOVUPS X14` (SSE 16-byte) — already optimal
5. **`setReqCtxUnsafe`** — 1 unsafe.Add + 1 store + gcWriteBarrier2
6. **`h.ServeHTTP(&b.req)`** — virtual call

---

## 4. Escape analysis — todas as allocs no hot path

| Local | Allocação | Hot path? | Inevitável? |
|---|---|---|---|
| `params.go:247` | `&reqBundle1{}` | SIM (param routes) | Sim, sem `sync.Pool` arriscado |
| `params.go:269` | `&reqBundle2{}` | SIM | Sim |
| `params.go:308` | `&reqBundle{}` | SIM | Sim |
| `params.go:317/332` | `make(Params, n)` (overflow >3 params) | RARO | Sim (>3 params) |
| `mux.go:898` | `fps := make(Params, ps.count)` (FastHandler) | SIM (Fast routes) | **Não — pool é viável (FastHandler doc diz que params são válidos só durante o call)** |
| Pré-allocados (registo) | Vários `&node{}`, `append(...)` em `addRoute` | NÃO (registo) | N/A |

---

## 5. Análise paths não-hot (mas pesados)

### `MethodNotAllowed` — 449ns / 6 allocs / 138B
Provável composição (não verificado linha-a-linha):
1. `m.allowed(urlPath, r.Method)` — `strings.Builder` aloca pelo menos 2x
2. `m.lazyMethodNotAllowed(cfg, allow).ServeHTTP(...)` — cached, mas o handler dentro:
3. `w.Header().Set("Allow", allow)` — interno do http.ResponseWriter
4. `http.Error(w, http.StatusText(...), 405)` — aloca string

**Optimização:** pre-compute `Allow` strings comuns em registration time (com base na árvore final), mas isto requer freezing.

### `RedirectTSL` — 1554ns / 15 allocs / 1305B — 🚨 CRITICAL
Localizado em `mux.go:938-955`:
```go
target := (&url.URL{Path: newPath, RawQuery: r.URL.RawQuery}).String()
m.mu.RLock(); mw := m.middleware; m.mu.RUnlock()
wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    http.Redirect(w, r, target, code)
}), mw).ServeHTTP(w, r)
```

**Problemas:**
1. **`&url.URL{...}` aloca** (objeto url.URL ~104B)
2. **`.String()` aloca** o resultado da serialization
3. **`http.HandlerFunc(func...)` closure escape** — closure aloca
4. **`wrapMiddleware(...)` chamado A CADA REQUEST** — não cached
5. **`http.Redirect`** internamente aloca para a resposta

Esta path é raramente percorrida mas custa caro. Pode ser optimizada:
- Cache do redirect handler por (newPath, code) — mas explosão combinatorial
- Ou: construir o Location header sem url.URL — concatenação directa de bytes seguros
- Ou: pular `wrapMiddleware` (o redirect é apenas um header + status — não precisa wrap)

### `paramsBuf.size`
`unsafe.Sizeof(paramsBuf{})` actual:
- `count int`: 8 B
- `buf [3]Param`: 3 * 32 = 96 B
- `overflow []Param`: 24 B
- Padding/alignment: 0
- **Total: 128 B**

(O comentário no código menciona 264 B — talvez de iteração anterior `maxParams=8`. Verificar e actualizar comentário.)

O **zeroing dos 128B** é feito a cada call que entra o hot path (não está limitado a `maxParams>0`). Linha 885 actual:
```go
var ps paramsBuf  // sempre zera 128B
var psBuf *paramsBuf
if root.maxParams > 0 { psBuf = &ps }
```

Pode-se eliminar o zeroing quando não há params? Não exactamente — o compilador zera porque o struct contém pointers (slice header). Mas: se sempre fizermos `psBuf = &ps` ou `psBuf = nil` conforme `root.maxParams`, é mais simples e potencialmente mais rápido (sem ramo).

---

## 6. Comparação directa com httprouter (apples-to-apples)

| Caso | MuxMaster | httprouter | Δ | Causa |
|---|---|---|---|---|
| Static | 30 ns / 0 allocs | 34 ns / 0 allocs | **+14% MuxMaster** | Tree implementation |
| Param1 | 124 ns / 416B / 1 alloc | 58 ns / 64B / 1 alloc | -53% MuxMaster | **httprouter aloca apenas Params slice; MuxMaster aloca todo o request bundle para suportar `r.Context()`** |
| Param2 | 148 ns / 448B / 1 alloc | 71 ns / 64B / 1 alloc | -52% | mesmo motivo |
| Param3 | 150 ns / 480B / 1 alloc | 78 ns / 96B / 1 alloc | -48% | mesmo motivo |
| ParallelParam | 107 ns / 416B / 1 alloc | 23 ns / 64B / 1 alloc | -78% | bundle é GC-pressure source |
| MuxMaster FastParam1 | 52 ns / 32B / 1 alloc | 58 ns / 64B / 1 alloc | **+10% MuxMaster** | FastHandler bypass do context |

**Conclusão central:** O custo do bundle (392-456B vs 64-96B do httprouter) é a diferença estrutural. Se conseguíssemos:
1. **Eliminar** o bundle alloc (pool seguro), ou
2. **Reduzir** dramaticamente o tamanho do bundle (não copiar o *http.Request completo)

...estaríamos competitivos com httprouter.

---

## 7. Hipóteses de optimização para análise pelos agentes especializados

### H1 — `sync.Pool` para reqBundle1/2/3
**Risco identificado:** CSA-001 (Concurrency Security Audit, 2026) determinou que `r.WithContext`-style mutation do request original tem race condition (middleware goroutines que ainda referenciam o `r` antigo).

**MAS:** o nosso bundle CONTÉM uma cópia fresca do request. Se o pool guardar o bundle até final do handler chain e depois reciclar... o risco é apenas se um handler/middleware spawn uma goroutine com referência ao bundle (raro mas possível — pense em `go log(r.Context())`).

**Mitigação possível:** o pool só recicla bundles cujo refcount cai a 0 — mas isso adiciona overhead de refcounting que provavelmente anula o ganho.

**Alternativa segura:** pool com release explícito apenas após `handler.ServeHTTP` retornar SEM panic. Goroutines spawned dentro do handler que captem `r` ficam com referência ao bundle ANTES do release — porque o pool reset esvazia campos e marca-os como "stale". Soluções para invalidar referências do request copy nos handlers que escaparam: a) imutabilidade — bundle.req nunca é mutado depois de set; b) lifetime — pool só recicla após end-of-handler.

→ **Veredicto: requer análise rigorosa do concurrency-security-auditor antes de avançar.**

### H2 — Reduzir/eliminar zeroing do `paramsBuf` na stack
Se `paramsBuf` for menor (e.g. 64B sem o slice header de overflow para o caso comum), o memclr é mais rápido. Mas o overflow é necessário para >3 params.

**Alternativa:** Cair para um path lento dedicado quando >3 params, usando alocação externa do overflow. O `paramsBuf` fica `count int + buf [3]Param = 104 B`.

### H3 — Eliminar a alocação no `make(Params, ps.count)` para FastHandler
Actualmente em `mux.go:898` faz-se uma cópia explícita do stack-allocated paramsBuf para uma heap-allocated Params (32-96B). Se o pool dos FastHandler params for SEGURO (o doc do FastHandler diz que params só são válidos durante o call), pode usar-se um sync.Pool sized 3 (1/2/3 params) e libertar no `defer`. Mas: o `fast(w, r, fps)` é uma function pointer que pode escapar tudo. Se o handler spawn goroutine com `ps`, a pool entrega `ps` a outro goroutine.

**Mitigação:** documentar que ps são válidos só para o call (já feito) + zerar `fps` no pool put. O caller pode `copy()` se quiser persistir.

### H4 — `getValue` SIMD-style com uint64 reads
Para strings ≤ 8 bytes, podemos comparar com 1 single `uint64` load via `unsafe`. Strings ≤ 16 bytes podem usar 2 loads. Para o caso comum (segmentos curtos como "users", "list", etc.), isto pode ser dramaticamente mais rápido que `memequal`.

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

**Complicação:** strings com len < 8 podem ter "lixo" nos bytes não-string (não — em Go strings têm trailing zero/garbage não-determinístico). Precisa de máscara baseada em len.

**Viabilidade:** alta para strings de comprimento conhecido (segmentos comuns: 1-15 chars). Risco: usar `unsafe.StringData` é estável no Go 1.20+.

### H5 — Pre-compute path segment hashes
Cada nodo na tree poderia ter um `hash uint64` calculado em registration time. Em runtime, fazemos hash do segmento sendo procurado e comparamos hashes primeiro; só se igual, fazemos string compare (para falsos positivos).

**Trade-off:** hash compute custa CPU; só vale se evitar muitos `memequal` calls.

**Viabilidade:** moderada. Provavelmente não vale na maioria dos casos porque `memequal` para strings ≤ 16 bytes já é muito rápido.

### H6 — `RedirectTSL` cache estático
A `target := (&url.URL{...}).String()` aloca 2x. Pode-se construir manualmente:
```go
// Sem allocs intermediárias
var sb strings.Builder
sb.Grow(len(newPath) + 1 + len(r.URL.RawQuery))
sb.WriteString(newPath)
if r.URL.RawQuery != "" {
    sb.WriteByte('?')
    sb.WriteString(r.URL.RawQuery)
}
target := sb.String()
```

Ainda há 1 alloc do string mas elimina o `url.URL{}` struct. Ganho ~50%.

Outro ponto: **`wrapMiddleware` a cada redirect** — pode ser cached. Construir o `redirectHandler` uma vez no `frozenConfigSlow()`. Substancialmente cheaper.

### H7 — Inline `paramsBuf.add`
Já é inline. Skip.

### H8 — Eliminar o branch `cfg.hasPanicHandler` na ServeHTTP
Já é zero-cost porque `cfg.hasPanicHandler` é um bool inline em `cfg`. O branch é predicted-static (sempre false na maioria das configs).

---

## 8. Prioridade preliminar (a refinar com input dos agentes)

| ID | Optimização | Ganho estimado | Risco | Complexidade |
|---|---|---|---|---|
| **A** | `RedirectTSL` rewrite (cache + manual builder) | -1000+ ns / -10 allocs em redirect path | Baixo | S |
| **B** | `make(Params, n)` pool para FastHandler | -5ns / -1 alloc (32-96B) em Fast routes | Médio (doc já diz) | S |
| **C** | `MethodNotAllowed` rewrite (less allocs no handler) | -300 ns / -4 allocs | Baixo | S |
| **D** | `sync.Pool` reqBundle1/2/3 (PENDENTE security review) | -50ns / -1 alloc em stdlib param routes | **ALTO** | M |
| **E** | uint64 string compare em prefixMatch | -10ns em param routes | Médio (unsafe) | M |
| **F** | Pre-build redirect handler no `frozenConfigSlow` | -200ns no redirect path | Baixo | S |
| **G** | Eliminar `make(Params)` em mux.go:898 reusando ps.buf via stack-aware path | -5ns / -1 alloc em FastParam | Baixo (já há análise) | M |
| **H** | Reordenar `methodIdx` para GET primeiro | -1ns geral | Baixo | XS |

---

## 9. Próximos passos

1. ⏳ Aguardar relatórios dos 3 agentes especializados:
   - `hotpath_analysis.md` (go-perf-optimizer #1 — getValue + paramsBuf + bundles)
   - `competitor_techniques.md` (benchmark-elite-tester — técnicas dos competidores)
   - `middleware_analysis.md` (go-perf-optimizer #3 — 18 middlewares + chains)
2. Consolidar tudo na **matriz prioritizada final** (task #12)
3. Apresentar plano ao utilizador para implementação
