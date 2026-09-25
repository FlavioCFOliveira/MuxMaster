# MuxMaster — System Model (DFD + Trust Boundaries)

**Date:** 2026-04-17
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Living document — updated at each architectural change

---

## 1. Propósito

Este documento descreve o modelo de sistema do MuxMaster: entidades externas, fronteiras de confiança, componentes internos, fluxos de dados e superfície de ataque. É a base do `threat-model.md`, `attack-trees.md` e `hypotheses.md`.

## 2. Âmbito

**In-scope:**
- Núcleo do router: `mux.go`, `tree.go`, `params.go`, `group.go`, `handler.go`, `introspection.go`, `response.go`
- Todos os 15 middlewares em `middleware/`
- Interacções com `net/http`, `context`, `sync.Pool`, `atomic.Pointer`, `unsafe`
- Comportamento protocolar HTTP/1.1 e HTTP/2 quando `net/http` entrega um `*http.Request` já parseado

**Out-of-scope:**
- HTTP/3 (não suportado pelo stdlib)
- Código das aplicações utilizadoras que registam handlers ou middlewares customizados — auditamos o módulo, não o consumo
- CVEs do Go runtime propriamente dito (serão confirmadas via `govulncheck`, mas não auditadas aqui)

## 3. Data Flow Diagram — Nível 0 (context diagram)

```
┌──────────────┐         HTTP(S)          ┌─────────────────────┐
│              │ ───────────────────────▶ │                     │
│   Attacker   │                          │  MuxMaster-backed   │
│   Client     │ ◀─────────────────────── │  Go HTTP server     │
│              │         responses        │                     │
└──────────────┘                          └─────────────────────┘
                                                   │
                                                   │ (optional)
                                                   ▼
                                          ┌─────────────────────┐
                                          │   Backend services  │
                                          │  DB / cache / RPC   │
                                          └─────────────────────┘
```

Entidades externas:
- **Cliente malicioso directo** — sem intermediários
- **Cliente através de reverse proxy** (nginx, Traefik, Caddy, envoy, cloud LB) — cenário típico em produção
- **Cliente com CDN entre si e o servidor** — cache pode ser oracle
- **Sistema de logs / SIEM** — consome stdout/stderr; vítima potencial de log injection
- **Sistema de observabilidade** — Prometheus scraper, tracer; consome `RoutePattern` / `Routes()`

## 4. Data Flow Diagram — Nível 1 (MuxMaster como caixa)

```
                                ╔═══════════════════════════════════════╗
                                ║          net/http server              ║
                                ║  (parsing HTTP/1.1, HTTP/2, framing,   ║
                                ║   TLS, connection management)          ║
                                ╚═══════════════╦═══════════════════════╝
                                                │
                                                │ *http.Request (parsed)
                                                ▼
           ╔═══════════════ TRUST BOUNDARY 1 — stdlib → muxmaster ═══════════════╗
           ║                                                                     ║
           ║                      ┌─────────────────┐                            ║
           ║                      │  Mux.ServeHTTP  │                            ║
           ║                      └────────┬────────┘                            ║
           ║                               │                                     ║
           ║                       ┌───────┴────────┐                            ║
           ║                       │ Pre-middleware │                            ║
           ║                       │  (m.preHandler)│                            ║
           ║                       └───────┬────────┘                            ║
           ║                               │                                     ║
           ║                      ┌────────┴────────┐                            ║
           ║                      │   Mux.dispatch  │                            ║
           ║                      │  (method → tree)│                            ║
           ║                      └────────┬────────┘                            ║
           ║                               │                                     ║
           ║                       ┌───────┴────────┐                            ║
           ║                       │ tree.getValue  │── walks radix              ║
           ║                       │   paramsBuf    │   fills params             ║
           ║                       └───────┬────────┘                            ║
           ║                               │                                     ║
           ║                       ┌───────┴────────┐                            ║
           ║                       │ rcPool acquire │── sync.Pool for requestCtx ║
           ║                       │ unsafe.Add ctx │── writes r.ctx via offset  ║
           ║                       └───────┬────────┘                            ║
           ║                               │                                     ║
           ╚═══════════════ TRUST BOUNDARY 2 — dispatch → middleware chain ════════╝
                                           │
                                           ▼
                              ┌──────────────────────────┐
                              │  Global middleware chain  │
                              │  (outermost first)        │
                              │                           │
                              │  Recoverer?  ───────┐    │
                              │  Logger?            │    │
                              │  RealIP?    ← XFF   │    │  ← USER-CONTROLLED
                              │  Throttle?          │    │     HEADERS
                              │  Compress?          │    │
                              │  CORS?      ← Origin│    │  ← USER-CONTROLLED
                              │  BasicAuth? ───◆     │    │  ← CRITICAL AUTH
                              │  WithValue? ├─┴───┐  │    │
                              │  RequestID? │     │  │    │
                              └─────┼───────┼─────┘    │    │
                                    │       │          │    │
           ╔════════════ TRUST BOUNDARY 3 — auth passed ═══════════════╗
           ║                  │       │                                 ║
           ║                  ▼       ▼                                 ║
           ║         ┌──────────────────┐                                ║
           ║         │   Group / Route  │                                ║
           ║         │   middleware     │                                ║
           ║         └────────┬─────────┘                                ║
           ║                  │                                          ║
           ║                  ▼                                          ║
           ║         ┌──────────────────┐                                ║
           ║         │  User handler    │ ← TRUSTED application code     ║
           ║         │  h(w, r)         │                                ║
           ║         └────────┬─────────┘                                ║
           ║                  │                                          ║
           ║                  ▼                                          ║
           ║         ┌──────────────────┐                                ║
           ║         │ ResponseWriter   │ ← write to socket               ║
           ║         │ (stdlib)         │                                ║
           ║         └──────────────────┘                                ║
           ╚═════════════════════════════════════════════════════════════╝

                       TRUST BOUNDARY 4 — response → network (stdlib responsibility)
```

## 5. Data Flow Diagram — Nível 2 (hot-path operations)

### 5.1 `ServeHTTP` → `dispatch` (todos os requests)

1. **Entry:** `m.ServeHTTP(w, r)` recebe `(w http.ResponseWriter, r *http.Request)` já parseados.
2. **Branch:** se `m.PanicHandler != nil` desvia para `dispatchWithRecover` (adiciona `defer m.recoverPanic`). Caso contrário, entra directamente no hot path (sem defer frame).
3. **Pre-middleware:** se `m.preHandler != nil`, executa a chain pre-routing (pode mutar path, criar novo `*http.Request`).
4. **URL path resolution:**
   - `urlPath = r.URL.Path` por defeito
   - se `m.UseRawPath && r.URL.RawPath != ""` → usa `RawPath`
5. **Tree load:** `trees := m.treesPtr.Load()` (atomic, lock-free).
6. **Method dispatch:** `idx := methodIdx(r.Method)`. **Case-sensitive** e mapeamento `switch` em 10 métodos. Métodos não-standard → `idx = -1` → fallback para `idxWild` tree (usada por `Mount`). Se nada responder → 404/405.

### 5.2 `tree.getValue` (lookup)

- Lê `paramsBuf` stack-allocated (fixed-size struct `[3]Param`), sem heap escape.
- Para cada nível da árvore: prefix compare (`prefixMatch`) com opção de case-fold ASCII (`foldEq`).
- Três tipos de nó wildcard: `param` (`:name`), `regexParam` (`{name:expr}`), `wildcard` (`*name`).
- Catch-all consome o resto do path **inteiro**, incluindo `/`.
- Se handler não encontrado mas existe TSR (trailing-slash-redirect), retorna `tsr=true`.

### 5.3 Params lifecycle

- `paramsBuf.add(key, value)` copia key/value strings para `[3]Param`. Se > 3 params: **silenciosamente descarta o excesso** (overflow silent drop).
- Se `handler != nil` e `ps.count > 0`:
  - `acquireRC()` obtém `*requestCtx` do `rcPool`
  - `rc.Context = origCtx` (ou `context.Background()` se `origCtx == nil`)
  - `rc.pattern = pattern`
  - `rc.params = Params(rc.small[:copy(rc.small[:], pslice)])` — copia do stack buffer para heap buffer do RC pool
  - **unsafe:** `*origCtxPtr = rc` onde `origCtxPtr` = `*(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))` — sobrescreve directamente o campo `ctx` não-exportado de `*http.Request`
  - Dispara `handler.ServeHTTP(w, r)`
  - **Pós-handler:** restaura `*origCtxPtr = origCtx`; limpa `rc.Context = nil; rc.params = nil; rc.pattern = ""`; devolve ao pool
- Se `ps.count == 0`: chama `handler.ServeHTTP(w, r)` directamente (zero pool ops, zero allocs).

**Risco implícito:** a sobrescrita e restauro do `r.ctx` é síncrona ao handler. Se o handler criar uma goroutine que guarde `r` e a goroutine ler `r.Context()` depois do handler retornar, vê o **contexto restaurado** (original), não o routed context. E pior: se outra goroutine na mesma `*http.Request` tentar ler `r.Context()` concorrentemente durante o `*origCtxPtr = rc` ou `*origCtxPtr = origCtx`, **data race**.

### 5.4 `Mount` / `ServeFiles`

- Ambos usam `r.Clone(r.Context())` para criar `r2`. OK.
- `Mount` trim prefix de `RawPath` via `strings.TrimPrefix` — mismatch vs `Path` possível se ambos não forem normalizados da mesma forma.
- `ServeFiles` constrói `r2.URL.Path = PathParam(r, paramName)` — se o param value contiver `..` não-escaped, delega o path-traversal ao `http.FileServer` que normalmente lida bem — mas o boundary deve ser explicitamente documentado e testado.

## 6. Componentes e responsabilidades

| Componente | Ficheiro | Responsabilidade | Estado interno | Sync primitive |
|---|---|---|---|---|
| Mux root | `mux.go` | Dispatch, options, PanicHandler | `treesPtr atomic.Pointer[methodTrees]`, `middleware []`, `pre []`, `preHandler http.Handler`, `mu sync.Mutex` | `atomic.Pointer` (read path), `sync.Mutex` (write path) |
| Radix tree | `tree.go` | O(k) lookup, wildcard, regex, catch-all | per-node: `path`, `indices`, `children`, `handler`, `pattern`, `regexp` | imutável após registration (by design) |
| Params | `params.go` | Path param access, types, RC pool | `rcPool sync.Pool` | `sync.Pool` + `unsafe.Add` para reqCtxOffset |
| Group | `group.go` | Prefix + middleware composition | `prefix string`, `middleware []` | none (builder pattern) |
| Handler err | `handler.go` | `HandlerFuncE`, `HTTPError` | stateless | — |
| Introspection | `introspection.go` | `Lookup`, `Routes`, `Walk` | reads `treesPtr` | `atomic.Pointer` load |
| Response | `response.go` | JSON/XML/Text/Redirect helpers | stateless | — |
| Middleware | `middleware/` | 15 funcões retornando `func(http.Handler) http.Handler` | per-middleware | varies (see below) |

### 6.1 Estado mutável nos middlewares

| Middleware | Estado | Sync | Threat highlights |
|---|---|---|---|
| `basic_auth` | `creds map[string]string` (read-only após build) | none (config-time only) | user enumeration via map lookup before `subtle.ConstantTimeCompare` |
| `clean_path` | stateless | — | double-clean bypass candidate (single-pass `path.Clean`) |
| `compress` | `pool *sync.Pool` (gzip writers), per-request `gzipResponseWriter.buf` | `sync.Pool` | unbounded buffer growth; BREACH oracle |
| `cors` | `allowedOrigins map[string]bool`, `allowAll bool` | read-only | Origin reflection when `allowAll`; header injection |
| `logger` | `out io.Writer` (shared) | caller-owned | CRLF in path → log injection; no redaction |
| `no_cache` | stateless | — | — |
| `real_ip` | stateless; mutates `r.RemoteAddr` | — | unconditional trust of XFF |
| `recoverer` | stateless | `defer recover()` | prints stack + debug.Stack() to `os.Stderr` — potential info leak |
| `request_id` | stateless; `crypto/rand` | — | reflects client header without sanitisation |
| `set_header` | stateless | — | — |
| `strip_slashes` | stateless | — | double-slash semantics undocumented |
| `throttle` | `tokens chan`, `queue chan` (global, not per-IP) | channel | advertises per-IP but is **global**; counter race N/A (channel ops are atomic) but handler semantics per `defer` can leak tokens on panic |
| `timeout` | stateless; `context.WithTimeout` | context cancel | handler keeps running after timeout (goroutine leak) |
| `with_value` | `key, val any` | read-only | context-key collision if caller passes string |

## 7. Trust boundaries — definições explícitas

### TB-1: Network / stdlib boundary
- **Quem controla:** atacante na rede (bytes arbitrários)
- **Quem valida:** `net/http` (parses request line, headers, body framing)
- **Quem confia:** MuxMaster confia que `r.Method`, `r.URL.Path`, `r.Header`, `r.URL.RawPath` sobreviveram à normalização stdlib
- **Risco:** divergência entre como `net/http` parseia e como MuxMaster reparseia `r.URL.Path` — base de request smuggling quando há proxy frontend

### TB-2: MuxMaster entry → middleware chain
- **Quem controla:** MuxMaster core
- **Quem valida:** o router dispatch (method+path lookup)
- **Quem confia:** os middlewares confiam que `r.URL.Path` e `r.Header` ainda contêm os bytes do atacante
- **Risco:** se o router sobrescrever `r.URL.Path` antes dos middlewares (e.g. `Mount`, `ServeFiles`, redirect), middleware vê path diferente do que viu o attacker

### TB-3: auth passed
- **Quem controla:** `basic_auth` (ou equivalente) assina a passagem
- **Quem valida:** o middleware
- **Quem confia:** o handler aplicacional assume que o request passou auth
- **Risco:** ordem de middlewares — se `basic_auth` for registado num grupo mas o path chegar via um grupo sibling sem auth (path aliasing), bypass

### TB-4: response to network
- **Quem controla:** stdlib (após `ResponseWriter.Write`)
- **Quem valida:** stdlib não re-valida headers; valores com CRLF podem sair
- **Quem confia:** o atacante recebe bytes
- **Risco:** CRLF injection em `Set-Cookie`, `Location`, `X-Request-ID`, `Access-Control-Allow-Origin`

## 8. Assets

| Asset | Confidencialidade | Integridade | Disponibilidade | Quem ameaça |
|---|---|---|---|---|
| Credenciais em `creds map` | Crítica | Crítica | Baixa | atacante via timing user-enum, brute-force, log leak |
| Password/token em Authorization header | Crítica | Crítica | Baixa | logger unescape, log file read |
| Session cookies (definidos pela app) | Crítica | Crítica | Média | CRLF em Set-Cookie, CORS reflection with credentials |
| Conteúdo de response bodies | Alta | Crítica | Alta | BREACH/CRIME (via compress), cross-origin via CORS misconfig |
| Topologia interna (rotas secretas) | Média | Baixa | N/A | `Routes()`/`Walk()` leak, route-existence timing oracle |
| Memória do processo | Alta | Crítica | Crítica | compression bomb, unbounded buf in compress, slowloris goroutine, throttle unlimited state |
| Log stream (stdout/stderr) | Média | Alta | Média | CRLF injection, ANSI escape, creds leak via logger |
| Handler execution slot (throttle) | N/A | Alta | Crítica | XFF spoof para exaurir throttle, token leak via panic |
| `sync.Pool` (RC pool) | Alta | Crítica | Alta | cross-request contamination (requires careful audit) |

## 9. Superfície de ataque enumerada

### 9.1 Inputs externos directos (todos untrusted)

| Input | Origem | Usado em |
|---|---|---|
| `r.Method` | request line | `methodIdx`, `allowed` |
| `r.URL.Path` | request line | `tree.getValue`, redirect building, logger |
| `r.URL.RawPath` | request line | `Mount`, opt `UseRawPath` |
| `r.URL.Query()` | query string | (caller handlers — não usado pelo router directamente) |
| `r.Header["Origin"]` | header | `cors.go` |
| `r.Header["Accept-Encoding"]` | header | `compress.go` |
| `r.Header["X-Forwarded-For"]` | header | `real_ip.go`, indirectamente `throttle.go` |
| `r.Header["X-Real-IP"]` | header | `real_ip.go` |
| `r.Header["X-Request-ID"]` | header | `request_id.go` |
| `r.Header["Authorization"]` | header | `basic_auth.go` (decoded via `r.BasicAuth()`) |
| `r.RemoteAddr` | TCP | `real_ip.go` overrides this |
| `r.Body` | body | caller handlers only (não usado pelo router) |

### 9.2 Outputs controlados pelo módulo (podem transportar injecção)

| Output | Construído em | Bytes do atacante |
|---|---|---|
| Response `Location` header | `mux.go` (redirect), `response.Redirect` | `r.URL.Path` inteiro (sem sanitização manual — delega a stdlib) |
| Response `Allow` header | `mux.go:allowed` | — (método strings fixas) |
| Response `Access-Control-Allow-Origin` | `cors.go` | **reflecte origin directo** |
| Response `Access-Control-Allow-Credentials` | `cors.go` | — |
| Response `Access-Control-Expose-Headers` | `cors.go` | config-time strings |
| Response `Access-Control-Allow-Methods` | `cors.go` | config-time |
| Response `Access-Control-Allow-Headers` | `cors.go` | config-time |
| Response `WWW-Authenticate` | `basic_auth.go` | **`realm` string** (config-time, mas se caller passar user input → CRLF) |
| Response `X-Request-ID` | `request_id.go` | **header reflectido do cliente** |
| Response `Cache-Control`, `Pragma`, `Expires` | `no_cache.go` | — |
| Response `Vary`, `Content-Encoding` | `compress.go` | — |
| Response body (500) | `recoverer.go`, `Mux.PanicHandler` default | stdlib `http.Error` — safe |
| `os.Stderr` panic dump | `recoverer.go` | `rcv any` + `debug.Stack()` — **includes local variables in stack** |
| Log line (stdout) | `logger.go` | **`r.URL.Path` raw** via `fmt.Fprintf` |

### 9.3 Inputs de configuração (trusted at build time)

| Config | Fonte | Risco se atacante controlar |
|---|---|---|
| `creds map` em basic_auth | aplicação | — (não controlável remotamente) |
| `realm` em basic_auth | aplicação | se caller o expõe via config file / env mal-validado, CRLF injection |
| `CORSOptions.AllowedOrigins` | aplicação | — |
| `Compress(level)` | aplicação | invalid level → panic ao build (pre-serve) |
| `Logger(out)` | aplicação | se `out` for um arquivo sem rotação, log flood DoS |
| `BasicAuth(realm, creds)` | aplicação | creds hardcoded é um smell, mas fora do scope |

### 9.4 Pontos de panic em runtime (cada um é um D contingente se recoverer não cobre)

- `Mux.Handle` — panic em: método vazio, path não abs, handler nil, método não-suportado, rota duplicada, catch-all mal formado, wildcard conflito
- `Mux.Mount` — nil handler, prefix não começa com `/`
- `Mux.ServeFiles` — nil root, prefix sem `/*name`
- `tree.addRoute` — wildcards inválidos, regex inválida, conflito
- `middleware.BasicAuth` — creds nil
- `middleware.Compress` — level inválido
- `middleware.Logger` — writer nil
- `middleware.Throttle` — limit ≤ 0 ou backlog < 0
- `middleware.Timeout` — duration ≤ 0
- `middleware.WithValue` — key nil

Todas estas panics são **registration-time**; correctamente aceitáveis. Mas: nenhuma é recuperável num handler via `recoverer` — elas correm **antes** da chain existir. Se uma aplicação registar rotas dinamicamente (não suportado), panics aqui colapsam o processo.

## 10. Modelo de concorrência

- **Leitura da árvore:** `m.treesPtr.Load()` lock-free em cada request.
- **Escrita da árvore:** `Handle` tira `m.mu.Lock`, clona o array de trees (copy-on-write), muta a raiz do método relevante, `treesPtr.Store`. Correct.
- **Nota crítica:** a **mutação dos nós individuais** dentro de `root.addRoute` **não** é copy-on-write. Se um método for re-registrado concorrentemente com um serve, o serve pode ler nós parcialmente mutados. O design actual assume registration completo antes de iniciar serving. **Isto é uma TOCTOU latente se a aplicação violar o contrato** — documentação existe mas o código não guarda invariance.
- **`rcPool`:** `sync.Pool` com `New` criando `*requestCtx`. `Get/Put` race-safe, mas o padrão `*origCtxPtr = rc` cria uma janela onde duas goroutines poderiam ver o mesmo `rc` se partilhassem o mesmo `*http.Request` (e.g. middleware que spawn goroutines com o mesmo `r`). Vector para pool contamination.
- **Middleware modification em runtime:** `m.Use`, `m.Pre` alteram `m.middleware`/`m.pre`. **Não há sync**. Se chamados concorrentemente com serving, data race garantida. Documentação diz "antes das rotas"; recomendar enforcement via `atomic.Bool` flag "has_served".

## 11. Notable unsafe / reflective constructs

| Local | Construct | Propósito | Auditoria necessária |
|---|---|---|---|
| `params.go:140` | `reflect.TypeOf(http.Request{})` em `init()` | Encontrar offset do campo `ctx` | Se stdlib mudar layout, `reqCtxOffset` fica errado silenciosamente em novas versões Go — **regressão latente** |
| `params.go:155`, `mux.go:464`, `mux.go:521` | `unsafe.Add(unsafe.Pointer(r), reqCtxOffset)` | Escrever directamente `r.ctx` | Bypassa `r.WithContext` (que clona) — partilha de request entre goroutines fica insegura |
| `introspection.go:99` | `reflect.ValueOf(h)` + `runtime.FuncForPC` | Nome do handler para display | Info leak se `Routes()` exposto externamente |

## 12. Dependências externas

```
$ grep -E 'require\s+github' go.mod
(empty)
```

**Zero external dependencies.** Invariante confirmado.

Go stdlib usage (hot path): `net/http`, `net/url`, `context`, `sync`, `sync/atomic`, `unsafe`, `reflect`, `strings`, `strconv`, `path`, `regexp`, `crypto/subtle` (basic_auth), `crypto/rand` (request_id), `encoding/hex`, `compress/gzip`, `io`, `fmt`, `os`, `runtime/debug`, `time`, `encoding/json`, `encoding/xml`.

## 13. Superfície de ataque priorizada (input para o sprint plan)

Top 10 superfícies por risco estimado (antes de investigação):

1. **`unsafe.Add` em `mux.go:464/521`** — escrita em campo não-exportado de `*http.Request` partilhável entre goroutines → potencial data race em handlers que escapem o request para goroutines filhas. **Único uso de `unsafe` no módulo**, portanto crítico para auditar.
2. **`logger.go` CRLF injection** — `fmt.Fprintf(out, "%s %s %s ...", ..., r.URL.Path, ...)` com `r.URL.Path` raw. Trivialmente exploitável se atacante enviar path com `\r\n`.
3. **`basic_auth.go` user enumeration** — map lookup `creds[user]` antes de `subtle.ConstantTimeCompare`; não-constant-time; timing discrepancy entre user existente e não-existente é garantida arquitecturalmente.
4. **`cors.go` Origin reflection com `allowAll`** — se qualquer origem é echoed e o atacante enviar `Origin: evil.com\r\nSet-Cookie: x=1`, o `allowedOrigins[origin]` (string map) não contém o `\r\n` → cairia na condição "não listado". MAS se `allowAll=true` a verificação é pulada e reflecte-se directamente. Requer verificação empírica.
5. **`request_id.go` CRLF + unbounded length** — `r.Header.Get("X-Request-ID")` directo para `w.Header().Set("X-Request-ID", id)`. Aceita qualquer tamanho e qualquer byte.
6. **`real_ip.go` unconditional XFF trust** — sem config de trusted proxies. Trivial spoof.
7. **`compress.go` unbounded buffer + BREACH** — `g.buf = append(g.buf, b...)` cresce ilimitadamente; BREACH oracle quando input reflectido está na resposta.
8. **`RedirectTrailingSlash`/`RedirectFixedPath`** — constrói Location a partir de `r.URL.Path`. Se path contém `//evil.com`, `path.Clean` remove o `/` duplicado transformando em `/evil.com`. `r.URL.String()` pode serializar como URL cross-host? Requer verificação rigorosa.
9. **`tree.go` radix: bypass via case-fold + RedirectFixedPath** — `RedirectFixedPath` chama `getValue(cleaned, nil, false)` com ci=false, mas se `CaseInsensitive=true` o matching é fold; a combinação pode permitir `/ADMIN/` → canonical `/admin` → redirect expõe a rota oculta.
10. **`timeout.go` goroutine leak** — classic; bem conhecido. Documentar SLA.

E atenção secundária a:
- `Mount` com RawPath prefix trim (divergência Path vs RawPath)
- `ServeFiles` + catch-all + traversal via `http.FileServer`
- `throttle.go` semântica global vs per-IP (docs dizem per-IP; código é global)
- Introspection `Routes()/Walk()` concorrente com registo dinâmico (fora do contrato mas testável)

## 14. Perguntas abertas para os especialistas resolverem

1. (http-protocol) `http.Redirect(w, r, r.URL.String(), code)` quando `r.URL.Path` começa com `//` produz Location cross-origin? Se sim, open redirect.
2. (path-routing) Diferencial: `httprouter`, `chi`, `bunrouter` todos aceitam `/a%2Fb` como dois segmentos? Se diferem, MuxMaster pode ser outlier.
3. (concurrency) Se um handler `go func(){ fmt.Println(r.Context()) }()` o que vê a goroutine quando o main handler retorna e `*origCtxPtr = origCtx` dispara? Race detector deve flag-ar.
4. (middleware) `basic_auth` timing: qual é a diferença empírica user-existente vs não-existente em 1M samples?
5. (dos) `compress.go` buf: alocação linear confirmada até que tamanho de response body? Existe um limite efectivo (heap available)?
6. (sast) `reflect.TypeOf(http.Request{})` em `init()` — se futura versão Go renomear `ctx` o offset fica errado. Existe assert post-init a validar?
7. (timing) `RedirectFixedPath` timing — medir se é distinguível vs NotFound (route-existence oracle).
8. (fuzz) `findWildcard` + `expandOptional` + regex compile dentro de `insertChild` — fuzzer que gere padrões `{name:(.*){0,1000}}` provoca ReDoS na compilação?

## 15. Diagrama consolidado — superfície & TBs

```
              ATTACKER
                │
                │ HTTP request (arbitrary bytes)
                ▼
        ┌────────────┐
        │  net/http  │  ← TB1: stdlib HTTP parsing
        └──────┬─────┘
               │ *http.Request (Method, URL, Header normalised)
               ▼
┌────────────────────────────────────────┐
│ MUX: ServeHTTP                          │ ← TB2: module entry
│  ├ preHandler chain                     │
│  ├ dispatch                             │
│  │   ├ treesPtr.Load (atomic)           │ ← read-only after reg
│  │   ├ tree.getValue (stack paramsBuf)  │
│  │   └ unsafe.Add(r, reqCtxOffset)      │ ← UNSAFE: req-owned invariant
│  └ middleware chain                     │
│      ├ outer → inner                    │
│      │                                  │
│      │  ▼ per middleware threats:       │
│      │    CRLF / CORS / bomb / timing   │
│      │                                  │
│      └ handler                          │ ← TB3: auth-passed
│        └ user code                      │
└────────────────────────────────────────┘
               │
               │ ResponseWriter.Write
               ▼ (TB4: back to stdlib → network)
        ┌────────────┐
        │  net/http  │
        └──────┬─────┘
               │
               ▼
           NETWORK ← attacker-observed response + side channels
```

## 16. Próximas revisões

Este `system-model.md` é reavaliado quando:
- É adicionado / removido middleware em `middleware/`
- É mudada a assinatura de `ServeHTTP`, `dispatch`, ou o formato de `*requestCtx`
- É alterado o padrão de uso de `unsafe.Pointer` / `reflect` no hot path
- É introduzido novo transporte (HTTP/3, gRPC, WebSocket)
- É adicionado qualquer mecanismo de mutação de estado **após** início de serving
