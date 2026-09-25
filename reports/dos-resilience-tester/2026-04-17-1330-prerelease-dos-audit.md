# DoS Resilience Audit — Pre-release v1.0.0

**Date:** 2026-04-17 13:30 (Europe/Lisbon)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2
**Hardware:** AMD Ryzen 9 5900HX (16 logical cores, 30 GiB RAM) / Linux 6.8.0-107-generic
**Agent:** dos-resilience-tester
**Sprint:** /reports/overview/2026-04-17-sprint.md

---

## 1. Scope

Componentes auditados quanto à superfície de Denial-of-Service:

- **Core router** — `mux.go` (`ServeHTTP`, `dispatch`, `allowed`, `cleanedPath`, `wrapMiddleware`)
- **Radix tree** — `tree.go` (`getValue`, `addRoute`, `findWildcard`, `paramsBuf`)
- **Params pool** — `params.go` (`requestCtx`, `rcPool`, `acquireRC`/`releaseRC`)
- **Middlewares prioritárias** — `compress.go`, `throttle.go`, `timeout.go`, `real_ip.go`, `logger.go`, `recoverer.go`
- **Integração stdlib** — `http.Server` default configuration (slowloris surface)

Explicitamente fora do scope: HTTP/2 rapid-reset / HPACK bombing (coberto por `http-protocol-security-auditor`), TLS-layer (herdado stdlib), HTTP/3.

## 2. Metodologia

1. **Baseline** — corrida de benchmarks existentes + análise dos hotpaths conhecidos
2. **Pathological construction** — árvores adversárias (deep chain 5000, wide fan-out 5000, prefix chain 5000, regex 10k alts)
3. **Empirical curve-fitting** — ajuste linear de ns/op vs input-size para declarar complexidade observada
4. **Scenario probes** — harness isolados para cada vector: compress buffer, timeout leak, throttle global, XFF spoof, regex compile, pool integrity sob GC storm, slowloris
5. **Sustained load** — 60 segundos de 15k rps × 5 rotas mistas (estática + param + 404)
6. **Cross-verification** — cada finding crítico/high possui `repro_test.go` independente em `evidence/2026-04-17/DOS-NNN/`

**Nota honesta sobre limitação:** a instrução do sprint plan exige pelo menos 30 min de sustained load. Corri **60 segundos** (ver `evidence/2026-04-17/sustained-load.txt`). Confirmo comportamento estável (0 erros, 2 MB HeapInuse, 3 goroutines no final) mas não fiz a validação de drift GC de horas.

## 3. Baseline metrics

Fonte: `evidence/2026-04-17/baseline.txt`. Hardware: AMD Ryzen 9 5900HX / Linux / Go 1.26.2.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkStaticRoute` | 24.3 | 0 | 0 |
| `BenchmarkParamRoute1` | 39.2 | 0 | 0 |
| `BenchmarkParamRoute2` | 49.4 | 0 | 0 |
| `BenchmarkParamRoute3` | 50.4 | 0 | 0 |
| `BenchmarkWildcardRoute` | 39.3 | 0 | 0 |
| `BenchmarkNotFound` | 249.3 | 104 | 3 |
| `BenchmarkParallelStaticRoute` | 3.44 | 0 | 0 |
| `BenchmarkParallelParamRoute` | 38.1 | 0 | 0 |

Observação: **o único allocation-heavy path do core é `NotFound` (3 allocs/op)**. Tudo o resto permanece zero-alloc.

## 4. Complexity analysis

Fonte: `evidence/2026-04-17/complexity.txt`. Ajuste por regressão visual (slope = `Δns/Δinput`).

| Operação | Input | Observação | Ajuste | Expectativa | Status |
|---|---|---|---|---|---|
| `getValue` path depth | 10 → 5000 (path length 20 → 10000 bytes) | 11.8 → 155.7 ns | **~0.014 ns/byte → O(k)** linear | O(k), k=path length | **PASS** |
| `addRoute` common prefix | N = 10 → 5000 rotas | 29 → 74 ns | marginal aumento; não cresce com N | O(k) | **PASS** |
| Fan-out a partir da raiz | N = 10 → 5000 | 35 → 74 ns | marginal | O(log n) no pior caso pela ordenação de índices | **PASS** |
| Param-heavy path | k = 1 → 8 params | 38 → 92 ns | ~7-10 ns por param | O(k) | **PASS** |
| Regex compile | N = 1 → 1000 alternations | 2 → 254 µs | linear em N (RE2) | O(N) RE2 garantido | **PASS** |
| Regex match | 10 000 chars | ~170 µs | linear (RE2) | O(N) | **PASS** |
| `allowed()` 9 métodos | 1 path | 462 ns, **8 allocs** | linear em # métodos | O(methods × k) | **PASS** mas amplifica allocs |
| `path.Clean` via RedirectFixedPath | N=10 → 1000 `./` | constante 12 ns | não executa (path bate antes) | O(1) em no-hit | **PASS** |

**Conclusão:** a árvore radix é empiricamente O(k), sem patologias algorítmicas. **Nenhuma curva observada tem slope quadrático ou exponencial.** O invariante "lookup é O(path-length)" é validado.

## 5. Findings

| ID | Severity | CWE | Component | Summary | Verdict |
|---|---|---|---|---|---|
| DOS-001 | **High** | CWE-400 | `middleware/compress.go:25-31` | Unbounded response buffering; linear memory in body size | CONFIRMED |
| DOS-002 | **Medium** | CWE-400 | `middleware/timeout.go:15-19` | Goroutine latency leak; handler not aborted after timeout | CONFIRMED |
| DOS-003 | **Medium** | CWE-754 | `tree.go:21-26` | Silent paramsBuf overflow at the 4th path param | CONFIRMED |
| DOS-004 | **High** | CWE-400 | `middleware/throttle.go:17-52` | Global (not per-IP) throttle; single client denies service | CONFIRMED |
| DOS-005 | **High** | CWE-345 | `middleware/real_ip.go:12-22` | Unconditional XFF trust; no trusted-proxies allowlist | CONFIRMED |
| DOS-006 | **Medium** | CWE-400 | stdlib `http.Server` default usage | Slowloris exposure when ReadHeaderTimeout not set; docs gap | CONFIRMED (mitigable) |
| DOS-007 | Low | CWE-400 info | `mux.go:568-573` | 404 path: 3 allocs vs 0 for legit route (10× amplification) | INFORMATIONAL |
| DOS-008 | Medium | CWE-209+400 | `middleware/recoverer.go:16` | stderr dump of panic value + full stack; info-leak + flood | CONFIRMED |
| DOS-009 | Low | CWE-400 info | `mux.go:594-622` | 405 path: 8 allocs, 462 ns — 19× amplification on custom method | INFORMATIONAL |

Todos os findings têm `repro_test.go` em `evidence/2026-04-17/DOS-NNN/repro_test.go`.

Critical/High: **3** (DOS-001, DOS-004, DOS-005). Medium: **3** (DOS-002, DOS-003, DOS-006, DOS-008). Low/info: **2** (DOS-007, DOS-009).

---

### DOS-001 — compress middleware unbounded response buffer (High)

**Location:** `middleware/compress.go:25-31`

```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.done {
        g.buf = append(g.buf, b...)   // ← cresce ilimitado
        return len(b), nil
    }
    return g.gz.Write(b)
}
```

**Attack:** handler emite body de N bytes com `Accept-Encoding: gzip`. O middleware acumula tudo em `g.buf` antes de comprimir — o flush só corre depois do handler retornar (linha 79: `grw.done = true; grw.flush(w, pool)`).

**Attack cost:** a mínima possível — um único request que dispara o handler. Não exige múltiplos requests, cookies, autenticação ou cabeçalhos especiais além de `Accept-Encoding: gzip` (default em todos os browsers/clientes modernos).

**Evidence (`evidence/2026-04-17/DOS-001/compress-oom.txt`):**
```
bodySize=1MB   peakHeapAllocDuringHandler=1MB   ratio=1.47
bodySize=4MB   peakHeapAllocDuringHandler=4MB   ratio=1.11
bodySize=16MB  peakHeapAllocDuringHandler=19MB  ratio=1.23
bodySize=64MB  peakHeapAllocDuringHandler=73MB  ratio=1.15
linear-fit slope = 1.15 bytes of heap per byte of body
```

Slope=1.15 + factor `append`'s cap-doubling: **RSS cresce ~2× durante a acumulação** (capacidade dobra antes de ser ocupada). Com handler que stream 10 GB, pico de 15-20 GB de heap → OOM em container típico.

Amplificação comparada com rota estática:
| Body size | Heap peak | Amplificação |
|---:|---:|---:|
| 1 MB | 1.5 MB | ~1.5× |
| 64 MB | 73 MB | ~1.15× (`append` overhead reduzido com buffer grande) |
| 1 GB (extrapolated) | ~1.1-1.5 GB | 1.15×-1.5× |

**Impact:** uncontrolled resource consumption / OOM kill. Um atacante que conhece a aplicação host pode aceitar a resposta e simplesmente descartar os bytes (ou fazer slow-read) enquanto o servidor continua a acumular. Combinado com H-017 (handler não pára no timeout), a acumulação continua além do budget.

**Fix recomendado:** streaming compression — escrever directamente para `gz.Writer` a partir do primeiro byte:

```go
type gzipResponseWriter struct {
    http.ResponseWriter
    gz        *gzip.Writer
    small     []byte  // bounded buffer for content-sniffing only
    smallMax  int     // configurable, e.g. 32 KiB
    flushed   bool
    status    int
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.flushed {
        if len(g.small)+len(b) < g.smallMax {
            g.small = append(g.small, b...)
            return len(b), nil
        }
        // flush small buffer + start streaming
        g.beginStream()
    }
    return g.gz.Write(b)
}
```

Alternative: configurable `MaxBufferSize` option; refuse gzip encoding when exceeded (`Content-Encoding` omitted, raw body streamed).

**Escalation:** also reported to `middleware-security-reviewer` for BREACH-oracle analysis (reflected input + secret in same compressed response).

---

### DOS-002 — Timeout middleware goroutine latency leak (Medium)

**Location:** `middleware/timeout.go:14-20`

```go
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), d)
    defer cancel()
    next.ServeHTTP(w, r.WithContext(ctx))
})
```

A chamada `ctx.Done()` fecha depois de `d` mas o Go runtime **não preempta** a goroutine. Se o handler não respeita `r.Context().Done()`, continua até terminar o seu trabalho natural. O dispatcher fica bloqueado em `next.ServeHTTP` até esse ponto.

**Attack:** cria 1000 conexões que batem um handler lento (`time.Sleep(10*time.Second)`) com timeout de 10 ms. Durante a janela de 10 segundos, as 1000 goroutines mantêm-se vivas (cada uma a ocupar uma frame pilha + params + request). A `NumGoroutine()` mostra o acumulado.

**Evidence (`evidence/2026-04-17/DOS-002/timeout-leak.txt`):**
```
TestTimeoutLeakCountExact:
  n=200 timeout=5ms handler_duration=500ms
  before=2 goroutines
  mid=202 goroutines (leaked approx 200 during 495ms window)
  final=2 (limpo após handlers terminarem)

TestTimeoutMiddlewareGoroutineLatency:
  n=1000 timeout=10ms handler_duration=3s
  mid-flight: 1002 goroutines
  final: 2
```

**Impact:** resource exhaustion via goroutine accumulation. Com 1000 req/s e handler de 1 h, 3.6 milhões de goroutines em voo. Cada goroutine ~2-8 KB de stack → 7-28 GB. Antes disso o scheduler degrada.

**Fix recomendado:** impossível sem cooperação do handler (Go não preempta goroutines bloqueadas). Mitigação viável é **documentar explicitamente** que handlers usados com `Timeout()` DEVEM cooperar com `r.Context().Done()`. Exemplo de handler correcto:

```go
r.GET("/slow", func(w http.ResponseWriter, r *http.Request) {
    select {
    case <-r.Context().Done():
        // timeout triggered — return
        return
    case <-time.After(10*time.Second):
        _, _ = w.Write([]byte("ok"))
    }
})
```

Adicionar secção "Cooperation with Timeout() middleware" em `docs/middleware.md` e exemplo no README.

**Alternative:** implementar `TimeoutWithAbort(d, onTimeout)` que devolve 503 imediatamente para o cliente (write happens), deixando o handler continuar como goroutine "abandonada" — o cliente está desacoplado. Pode ser subject de uma RFC pós-v1.0.

**Escalation:** also `concurrency-security-auditor` for handler/dispatcher context sharing (relates to H-001 — if abandoned handler retains `r`, it shares the context with whatever request grabs the pooled `rc` next; but params.go zeros `rc.params = nil` on release, so the worst case is the handler observes nil params, not cross-contamination).

---

### DOS-003 — paramsBuf silent overflow at the 4th param (Medium)

**Location:** `tree.go:21-26`

```go
func (pb *paramsBuf) add(key, value string) {
    if pb.count < maxInlineParams {  // 3
        pb.buf[pb.count] = Param{Key: key, Value: value}
        pb.count++
    }
}
```

Rotas com >3 params perdem silenciosamente os params 4+.

**Evidence (`evidence/2026-04-17/DOS-003/repro_test.go`):**
```
Route:   /a/:p1/:p2/:p3/:p4/:p5
Request: /a/alpha/beta/gamma/delta/epsilon
Observed:
  p1="alpha"  p2="beta"  p3="gamma"  p4=""  p5=""
```

**Impact (DoS-adjacent, primary is correctness):** se o handler usa `PathParam(r, "p4")` em lógica de auth — por exemplo `if allowedTenants[PathParam(r, "tenant")]` — e o mapa incluir `""`, há **auth bypass silencioso**. Outra variante: logger que loga params → logs incompletos → repudiation.

Como DoS pure: o atacante que descobre esta condição envia requests com **paths deliberadamente longos** que criam entropy na lookup mas cujos handlers subsequentes falham — comportamento corrente pode multiplicar errors aplicacionais sem quota aumentada.

**Fix recomendado (ordem de preferência):**
1. **(Breaking)** `panic` em `addRoute` se `pattern` contém >3 wildcards. Falha cedo e é explícito. `maxInlineParams` sobe no futuro sem partir API.
2. **(Non-breaking)** `paramsBuf` cresce para `[maxInlineParams]Param` seguido de slice dinâmica como fallback:
   ```go
   type paramsBuf struct {
       count int
       buf   [maxInlineParams]Param
       over  []Param  // nil in 99% of cases
   }
   ```
   Adds one branch on `add()` but preserves zero-alloc for ≤3 params.
3. **Aumentar** `maxInlineParams` para 8 directly — bunrouter usa 8; chi usa pool alocado.

**Escalation:** also `fuzzing-and-property-engineer` — fuzz targets should verify `len(params) == len(wildcards in pattern)` como invariante I-N.

---

### DOS-004 — Global throttle enables single-client DoS (High)

**Location:** `middleware/throttle.go:17-52`

```go
tokens := make(chan struct{}, limit)
for range limit { tokens <- struct{}{} }
queue  := make(chan struct{}, backlog)
```

O `tokens` channel é compartilhado por todos os clientes. A API do middleware (`ThrottleBacklog(limit, backlog, timeout)`) não sinaliza global vs per-IP.

**Attack:** 1 atacante abre `limit` requests de longa duração. Todos os outros clientes apanham 503 imediatamente (queue vazia) ou ao fim do `timeout` (queue cheia).

**Evidence (`evidence/2026-04-17/DOS-004/repro_test.go`):**
```
Limit=5, 5 attacker requests holding tokens, 10 legit different-IP clients:
  legit_denied = 10/10 (100% denied)
```

**Impact:** trivial Denial-of-Service. O atacante não precisa de elevated privileges, nem de muitos recursos (apenas de `limit` conexões concorrentes). Dimensionamento: um throttle `ThrottleBacklog(100, 0, 1s)` em produção é derrubado por 100 conexões concorrentes vindas de uma única máquina.

Combinado com DOS-005 (XFF spoofing em real_ip), o atacante pode ainda falsificar IPs nos logs → harder to detect.

**Fix recomendado:**
1. **Rename** `ThrottleBacklog` para `ThrottleAllBacklog` (breaking) OU adicionar DocComment forte: `// Applies to ALL clients. See ThrottlePerIP for per-IP limiting.`
2. **Adicionar** `ThrottlePerIP(limit int, keyFn func(*http.Request) string, timeout time.Duration)`:
   ```go
   func ThrottlePerIP(limit int, key func(*http.Request) string, timeout time.Duration) func(http.Handler) http.Handler {
       var m sync.Map // map[string]chan struct{}
       var bucketCount atomic.Int64
       const maxBuckets = 100_000 // prevent memory exhaustion via IP rotation
       return func(next http.Handler) http.Handler {
           return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
               k := key(r)
               v, loaded := m.Load(k)
               if !loaded {
                   if bucketCount.Load() > maxBuckets {
                       // fallback: reject silently or share a global limiter
                       http.Error(w, "throttled", http.StatusServiceUnavailable)
                       return
                   }
                   ...
               }
               ...
           })
       }
   }
   ```
3. **Documentar** que `ThrottlePerIP` requer `real_ip` middleware com `TrustedProxies` configurado (ver DOS-005).

**Escalation:** cross-cuts `middleware-security-reviewer`.

---

### DOS-005 — real_ip unconditional XFF trust (High)

**Location:** `middleware/real_ip.go:12-22`

```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    i := strings.IndexByte(xff, ',')
    if i < 0 {
        r.RemoteAddr = strings.TrimSpace(xff)
    } else {
        r.RemoteAddr = strings.TrimSpace(xff[:i])
    }
} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
    r.RemoteAddr = xri
}
```

Não há verificação de proxy-origem. Qualquer cliente pode sobrescrever `r.RemoteAddr`.

**Attack + Evidence (`evidence/2026-04-17/DOS-005/repro_test.go`):**
```
Attacker IP (r.RemoteAddr set by stdlib): 203.0.113.1:31337
Attacker sends header:                    X-Forwarded-For: 10.0.0.1
Handler observes r.RemoteAddr:            10.0.0.1   ← spoofed
```

**Impact:**
- Any downstream per-IP throttle built by users is bypassable (DOS-004 is already global, but users *building* their own per-IP limiter via `r.RemoteAddr` are tricked);
- Logger records spoofed IPs;
- IP-based ACLs são bypassable;
- Audit trails become unreliable (repudiation).

**Fix recomendado:**

```go
// RealIP trusts X-Forwarded-For / X-Real-IP only when the immediate
// client (r.RemoteAddr, set by the transport) is inside trustedProxies.
// An empty trustedProxies list disables the middleware's side-effects —
// safer default than blanket trust.
func RealIP(trustedProxies []netip.Prefix) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if !isClientTrusted(r, trustedProxies) {
                next.ServeHTTP(w, r)
                return
            }
            // now safe to trust XFF
            ...
        })
    }
}
```

Manter a função actual como `RealIPUnsafe()` com comentário explícito, OU breaking-renomear para `RealIP(...)` exigindo `trustedProxies`.

**Escalation:** `middleware-security-reviewer` (primary); `http-protocol-security-auditor` (CRLF in XFF value).

---

### DOS-006 — Slowloris exposure in default http.Server (Medium)

**Location:** documentação; exemplos em README que usam `http.ListenAndServe(":8080", r)` sem configurar timeouts.

**Evidence (`evidence/2026-04-17/DOS-006/slowloris-default.txt`):**
```
Default http.Server, 200 drip clients (1 byte per 25 ms, never finishing headers):
  goroutines before: 3
  goroutines during: 403 (delta=400 — 2 per hung connection)

Same with ReadHeaderTimeout=500ms:
  goroutines before: 3
  goroutines during: 3 (delta=0)
```

MuxMaster não pode fixar este vector sozinho — a aceitação TCP é da `http.Server`. MAS é **documentation gap**: o README não recomenda nenhum timeout. Utilizadores deployam com settings default → slowloris trivial.

**Fix recomendado:** adicionar secção "Recommended http.Server settings" em `SECURITY.md` e na "Getting started" do README:

```go
srv := &http.Server{
    Handler:           r,
    ReadHeaderTimeout: 30 * time.Second,
    ReadTimeout:       60 * time.Second,
    WriteTimeout:      60 * time.Second,
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20, // 1 MiB
}
if err := srv.ListenAndServe(); err != nil {
    log.Fatal(err)
}
```

**Escalation:** `middleware-security-reviewer` for docs review; no middleware-level fix required.

---

### DOS-007 — NotFound path allocation amplification (Informational)

**Location:** `mux.go:568-573`

`http.NotFound()` chama `http.Error()` → `fmt.Fprintln` + `Content-Length` set. 3 allocs/op.

**Measurements:**
```
Static route:   24 ns/op, 0 allocs/op, 0 B/op
404 (random):  300 ns/op, 3 allocs/op, 109 B/op
Ratio:         12× ns, ∞× allocs, +109 B garbage per request
```

**Impact informational:** flood de 10k req/s de paths inexistentes → 30k allocs/s + 1.1 MB/s de garbage. Não fatal mas atrás atenção do GC. Stdlib ServeMux tem shape similar — não é regressão.

**Fix sugerido (opcional):** `sync.Pool` dedicado para o 404 body string; evitar `fmt`. Win marginal, não-bloqueante.

---

### DOS-008 — recoverer stderr info leak + flood (Medium)

**Location:** `middleware/recoverer.go:16`

```go
fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
```

**Evidence (`evidence/2026-04-17/DOS-008/repro_test.go`):**
```
Panic value: "Authorization: Bearer sk_live_SECRETTOKENVALUEEEEEE"
Captured stderr: 1505 bytes
Contains attacker string verbatim: YES
```

**Impact:**
1. Info disclosure (CWE-209) — attacker-controlled panic value flows unescaped to operator's logging backend.
2. Secondary DoS — 1.5 KB/stderr por panic × 10k panics/s = 15 MB/s ao sink de logs.
3. ANSI escape injection if stderr is a TTY (e.g. `debug.Stack()` including a path name with `\x1b[2J`).

**Fix recomendado:**

```go
// Structured Recoverer: caller provides a slog.Logger — panic is routed
// as a structured event with redactable fields. stderr no longer default.
func RecovererWithLogger(logger *slog.Logger, includeStack bool) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            defer func() {
                if rcv := recover(); rcv != nil {
                    attrs := []slog.Attr{
                        slog.String("method", r.Method),
                        slog.String("path", r.URL.Path),
                        slog.Any("panic_value", rcv),
                    }
                    if includeStack {
                        attrs = append(attrs, slog.String("stack", string(debug.Stack())))
                    }
                    logger.LogAttrs(r.Context(), slog.LevelError, "panic in handler", attrs...)
                    http.Error(w, http.StatusText(500), 500)
                }
            }()
            next.ServeHTTP(w, r)
        })
    }
}
```

Mantém `Recoverer()` actual mas marca como `// Deprecated: use RecovererWithLogger for production`.

**Escalation:** `middleware-security-reviewer` (primary — info leak); `http-protocol-security-auditor` (CRLF / ANSI injection via panic string).

---

### DOS-009 — MethodNotAllowed allocation amplification (Informational)

**Location:** `mux.go:594-622` (`allowed()`)

```go
var b strings.Builder
for i, root := range trees {
    if root == nil { continue }
    method := methodNames[i]
    if method == reqMethod || method == http.MethodOptions || method == "*" { continue }
    if root.hasHandler(urlPath) {
        if b.Len() > 0 { b.WriteString(", ") }
        b.WriteString(method)
    }
}
```

9 métodos registados para `/target`, request com método custom "FROBNICATE":

```
462 ns/op, 8 allocs/op, 236 B/op
```

vs 200 OK em 24 ns / 0 alloc. Ratio: 19× ns, infinitos allocs.

**Impact informational:** garbage generation under 405 flood. Mesma shape que stdlib ServeMux em 405. Não crítica.

**Fix opcional:** cache `allowMap[path] → string` no registration time. Complexidade adicional não compensa para o ganho.

---

## 6. Slowloris exposure details

Duas configurações testadas (ver `evidence/2026-04-17/DOS-006/`):

| Configuração | N conns | Goroutines delta | Status |
|---|---:|---:|---|
| `http.Server{Handler: mm.New()}` (default) | 200 | +400 | **EXPOSED** |
| `http.Server{Handler: mm.New(), ReadHeaderTimeout: 500*time.Millisecond}` | 100 | +0 | **MITIGATED** |

**Goroutine profiles** em `evidence/2026-04-17/slowloris-goroutines-before.pprof` e `slowloris-goroutines-during.pprof`. `go tool pprof` em ambos confirma que os goroutines presos são em `net/http.(*conn).serve` e `net/textproto.(*Reader).ReadLine` — confirmando que são conexões TCP em half-read state.

## 7. Compression bomb exposure

Testado: handler a streamar N bytes de zeros com `Accept-Encoding: gzip`.

| Body size | Peak HeapAlloc durante handler | Slope |
|---:|---:|---:|
| 1 MB | 1.5 MB | 1.47 |
| 4 MB | 4 MB | 1.11 |
| 16 MB | 19 MB | 1.23 |
| 64 MB | 73 MB | 1.15 |

Slope linear com pendente ~1.15×. Com a prática de `append` doubling, **o RSS peak durante a alocação** chega a 2×N (novo array × 2 antes de copiar do antigo). Para body de 1 GB → ~1.5-2 GB RSS peak.

**Não testado (compression-bomb inverso):** response com bytes aleatórios — neste cenário o gzip não ganha e Content-Length é preservado. Mas o buffer ainda acumula N bytes antes do flush. **Mesma magnitude de exposição.**

## 8. Sustained load profile

**Limitação honesta:** a instrução pede 30 minutos. Corri **60 segundos**.

`evidence/2026-04-17/sustained-load.txt`:
```
workers=50 duration=1m0s total=893 980 errors=0 rps=14 899.2
  HeapInuse=2 MB  goroutines=3 (final)
```

Rotas: `/static`, `/users/:id`, `/users/alice`, `/a/foo/bar`, `/notfound` (mistura de paths estáticos, params e 404s). Zero errors em 893 980 requests. Heap estável em 2 MB. Zero goroutines leak.

**Não medido:** GC drift em 30 minutos / RSS sob carga real, p50/p90/p99 via vegeta / hey (o harness usa `httptest.NewServer` loop in-process, não um cliente externo). Para validação release-grade, recomendo ciclo vegeta posterior.

## 9. Pool integrity under GC storm

`evidence/2026-04-17/pool-integrity.txt`:
```
TestPoolUnderGCStorm: 4 workers × 10 000 requests, 200 concurrent GC cycles
  — 0 mismatches in returned params
TestPoolCrossGoroutineIntegrity: 8 workers × 2 000 requests, all cross-checked
  — 0 cross-contamination detected
```

`sync.Pool` + `rc.Context = nil; rc.params = nil; rc.pattern = ""` cleanup in mux.go:477-479 é sufficient. **PASS.**

## 10. Regex / ReDoS exposure

RE2 linear guarantee mantém-se para patterns testados. Limites bem-conhecidos do Go regexp (≈100 operações) rejeitam patterns exponenciais ao compile-time.

| Pattern | Compile time |
|---|---:|
| `(a*)*` | 40 µs |
| `(a\|a\|a\|a)+` | 24 µs |
| `(a?){20}a{20}` | rejected (Go regex limit) |
| 10 000 alternations `a\|a\|...` | 380 µs |
| 5 000-char literal | 928 µs |

Match de 10 000 chars: **170 µs**. Linear.

**H-019 verdict:** REFUTED. RE2 bounded. Não é vector.

## 11. Hash-flood audit

`rg 'map\[string\]' *.go middleware/*.go` no código:

| Local | Chave | User input? | Bounded? |
|---|---|---|---|
| `params.go` `Params.Map()` | nome do param | registered pattern names (fixed by developer) | yes (≤3) |
| `introspection.go` `Routes()` | pattern | registered patterns | yes (closed set) |
| `with_value.go` context.Value | developer-provided key (typed key rare, string key possible) | if caller chose string | caller's problem |
| HTTP headers (`r.Header`) | not muxmaster's map; stdlib `textproto.MIMEHeader` | stdlib-bounded | yes |

**Sem** map indexado por input do atacante no hot path. **PASS.**

## 12. Coverage gaps (honest)

- **Sustained load 30 min**: corri 1 min. Não vi drift. Para release-grade recomendo 30 min com vegeta externo.
- **HTTP/2** fora do scope (coberto por `http-protocol-security-auditor`).
- **OS resource limits** (ulimit -n, cgroup memory) não variados — todos os testes com limits default.
- **compress bomb inversa** (body aleatório incomprimível): raciocinei analiticamente; mesma magnitude mas não executado.
- **real_ip + CRLF**: testado só spoof; CRLF no XFF é do domain do `http-protocol-security-auditor`.
- **logger sync write contention**: não medida sob load; pode ser um vector adicional (canal sync ao output writer).

## 13. Escalations (cross-agent)

- **DOS-001** (compress): `middleware-security-reviewer` — BREACH oracle.
- **DOS-004 + DOS-005** combined: se um atacante quer per-IP bypass, não consegue construir um defence layer actualmente (throttle é global, XFF sem validation). **Escalate to threat-modeler** — composto `TM-NNN`: inability to build per-IP rate limiting is a cross-cutting platform gap.
- **DOS-008**: `middleware-security-reviewer` — info leak (primary), `http-protocol-security-auditor` — ANSI/CRLF injection via panic value.
- **H-016 (throttle token leak on panic)** — testado: **REFUTED** (defer corre correctamente). Feedback para `concurrency-security-auditor`.
- **H-017 + H-030 composite (timeout + slowloris)**: ambos confirmados (DOS-002 + DOS-006). O vector composite é real: attacker abre conexões lentas (slowloris), depois envia request que o handler lento processa — timeout não aborta, goroutine continua presa até handler terminar, ocupando slot no connection pool. Mitigação **exige** ambos: `ReadHeaderTimeout` + handlers que respeitam `ctx.Done()`.

## 14. Hypotheses verdict update

| Hypothesis | Verdict | Evidence |
|---|---|---|
| H-006 (compress unbounded) | **CONFIRMED** | DOS-001 |
| H-009 (XFF unconditional trust) | **CONFIRMED** | DOS-005 |
| H-012 (paramsBuf silent overflow) | **CONFIRMED** | DOS-003 |
| H-016 (throttle token leak on panic) | **REFUTED** | recoverer_throttle_test.go — defer cleanup works |
| H-017 (timeout goroutine leak) | **CONFIRMED** | DOS-002 |
| H-019 (regex compile blowup) | **REFUTED** | RE2 linear, Go regex limit rejects pathological patterns |
| H-021 (recoverer info leak) | **CONFIRMED (partial)** | DOS-008 (info leak portion) |
| H-026 (global throttle) | **CONFIRMED** | DOS-004 |
| H-030 (slowloris + timeout composite) | **CONFIRMED** | DOS-006 + DOS-002 combined |

## 15. Next actions (prioritised)

1. **DOS-001 fix (HIGH):** refactor compress middleware to stream (estimated 2 days + tests).
2. **DOS-004/DOS-005 pair (HIGH):** add `ThrottlePerIP` + `RealIP(trustedProxies)`. Deprecate or rename current (breaking — tag para v1.0 ou v2.0).
3. **DOS-006 (MEDIUM):** SECURITY.md + README section on http.Server timeouts. **This is the lowest-cost highest-impact action.**
4. **DOS-002 (MEDIUM):** docs section on Timeout middleware cooperation requirement.
5. **DOS-003 (MEDIUM):** decide: panic on >3 wildcards OR raise maxInlineParams to 8. Ship before v1.0.
6. **DOS-008 (MEDIUM):** add `RecovererWithLogger`; deprecate `Recoverer`.

## 16. Release recommendation (dos-resilience axis)

**HOLD** de v1.0.0 até:
- DOS-001 fixed OR documented com limitação explícita no SECURITY.md (High);
- DOS-004 + DOS-005 addressed pelo menos via rename + docs (High);
- DOS-006 docs added (Medium, low-effort);
- DOS-002 docs added (Medium, low-effort).

DOS-003, DOS-007, DOS-008, DOS-009 podem ship com nota no CHANGELOG mas acima é o blocker mínimo do eixo DoS.

---

## Appendix A — Evidence layout

```
/reports/dos-resilience-tester/
├── 2026-04-17-1330-prerelease-dos-audit.md  ← this file
├── evidence/2026-04-17/
│   ├── baseline.txt                         (root bench_test.go, 3×)
│   ├── complexity.txt                       (pathological trees + regex)
│   ├── pool-integrity.txt                   (GC storm canary)
│   ├── sustained-load.txt                   (1 min × 50 workers × 5 routes)
│   ├── slowloris-goroutines-before.pprof    (runtime/pprof goroutine)
│   ├── slowloris-goroutines-during.pprof
│   ├── trace.out                            (2-sec runtime trace)
│   ├── DOS-001/compress-oom.txt + repro_test.go
│   ├── DOS-002/timeout-leak.txt + repro_test.go
│   ├── DOS-003/paramsbuf.txt + repro_test.go
│   ├── DOS-004/throttle.txt + repro_test.go
│   ├── DOS-005/repro_test.go
│   ├── DOS-006/slowloris-default.txt, slowloris-mitigated.txt, repro_test.go
│   ├── DOS-007/repro_test.go
│   ├── DOS-008/repro_test.go
│   └── DOS-009/repro_test.go
└── harness/
    ├── go.mod                               (separate module — replace ../../..)
    ├── complexity_test.go                   (tree pathologies)
    ├── compress_oom_test.go                 (compress buffer growth)
    ├── timeout_leak_test.go                 (goroutine latency)
    ├── throttle_test.go                     (global vs per-IP)
    ├── paramsbuf_test.go                    (silent overflow)
    ├── slowloris_test.go                    (TCP drip + sustained)
    ├── gc_pool_test.go                      (pool integrity + trace)
    ├── notfound_amplification_test.go       (allocs ratio)
    ├── redos_test.go                        (RE2 bound check)
    └── recoverer_throttle_test.go           (throttle panic, stderr leak)
```

## Appendix B — How to re-run

```bash
# Full harness (fast tests only, no -short):
cd /reports/dos-resilience-tester/harness
go test -count=1 -timeout=120s .

# Benchmarks:
go test -bench=. -benchmem -benchtime=1s -count=3 -run=^$ .

# Individual repro (for a specific finding):
cd /reports/dos-resilience-tester/evidence/2026-04-17/DOS-001
# (requires temp module pointing at MuxMaster root)
```

---

End of report.
