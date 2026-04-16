# MuxMaster — Especificação Funcional

**Versão:** 1.0  
**Data:** 2026-04-16  
**Estado:** Rascunho

---

## 1. Visão Geral

MuxMaster é um router HTTP de alta performance para Go, implementado puramente em Go standard library (zero dependências externas). O algoritmo central é uma árvore radix (Patricia trie) com lookup O(k) onde k é o comprimento do path.

### 1.1 Princípios de Design

1. **Zero dependências externas** — apenas `stdlib`. Qualquer funcionalidade que requeira dependência externa é explicitamente excluída ou tornada plugável via interface.
2. **100% compatível com `net/http`** — middleware, handlers e qualquer código que aceite `http.Handler` funciona sem adaptadores.
3. **Performance first** — todas as decisões de design pesam o impacto em ns/op e allocs/op. O alvo é igualar ou superar `httprouter` e `bunrouter`.
4. **API idiomática** — segue as convenções Go (nomes, zero-values úteis, interfaces pequenas).
5. **Zero overhead em produção** — funcionalidades opcionais têm custo zero quando desactivadas.

### 1.2 Alvos de Performance

| Caso | Alvo ns/op | Alvo allocs/op | Referência |
|---|---|---|---|
| Rota estática | ≤ 150 | 0 | httprouter: ~150 |
| 1 parâmetro | ≤ 200 | 0 | bunrouter: ~200 |
| 5 parâmetros | ≤ 300 | 0 | bunrouter: ~280 |
| Catch-all | ≤ 150 | 0 | — |
| Paralelo (estático) | ≤ 80 | 0 | — |

---

## 2. Estado de Implementação

Legenda: ✅ Implementado · 🔲 Por implementar · ❌ Fora de scope

---

## 3. Routing

### 3.1 Sintaxe de Padrões de Path

| Sintaxe | Exemplo | Estado | Notas |
|---|---|---|---|
| Rota estática | `/users/list` | ✅ | |
| Parâmetro nomeado | `/users/:id` | ✅ | Captura um segmento de path |
| Catch-all wildcard | `/static/*filepath` | ✅ | Captura tudo incluindo `/` |
| Parâmetro com regex | `/users/{id:\d+}` | 🔲 | Valida e captura; falha a match se regex não casa |
| Parâmetro opcional | `/posts{/:slug}` | 🔲 | Equivale a registar `/posts` e `/posts/:slug` |
| Path vazio (root) | `/` | ✅ | |

**Regras de precedência de matching (por ordem):**

1. Rotas estáticas
2. Parâmetros nomeados (incluindo com regex)
3. Catch-all wildcard

**Validações em tempo de registo (panic):**

- Path não começa com `/`
- Handler nil
- Método HTTP vazio
- Rota duplicada
- Wildcard não nomeado (`*` sem nome)
- Catch-all não no fim do path
- Catch-all conflito com handler existente
- Múltiplos wildcards no mesmo segmento
- Regex inválida em parâmetro `{name:expr}`

### 3.2 Métodos HTTP

| Método | Método de conveniência | Estado |
|---|---|---|
| GET | `r.GET(path, h)` | ✅ |
| HEAD | `r.HEAD(path, h)` | ✅ |
| POST | `r.POST(path, h)` | ✅ |
| PUT | `r.PUT(path, h)` | ✅ |
| PATCH | `r.PATCH(path, h)` | ✅ |
| DELETE | `r.DELETE(path, h)` | ✅ |
| OPTIONS | `r.OPTIONS(path, h)` | ✅ |
| CONNECT | `r.CONNECT(path, h)` | 🔲 |
| TRACE | `r.TRACE(path, h)` | 🔲 |
| Qualquer método | `r.Handle(method, path, h)` | ✅ |
| Lista de métodos | `r.Match([]string, path, h)` | 🔲 |
| Todos os métodos | `r.ANY(path, h)` | 🔲 |
| Método customizado | `r.RegisterMethod(method)` | 🔲 |

### 3.3 Registo de Rotas

```go
// Já implementado
r.GET("/users", listUsers)
r.HandleFunc("GET", "/users", listUsers)
r.Handle("GET", "/users", handler)

// Por implementar
r.CONNECT("/tunnel", tunnelHandler)
r.TRACE("/debug", traceHandler)
r.Match([]string{"GET", "HEAD"}, "/users", listUsers)
r.ANY("/ping", pingHandler)
r.RegisterMethod("PURGE")  // após isto, r.Handle("PURGE", ...) é válido
```

### 3.4 Lookup Programático

```go
// Por implementar
// Devolve handler, params e bool sem side effects (sem redirect, sem logging)
handler, params, found := r.Lookup("GET", "/users/123")
```

Útil para: testes, proxies reversos, geração de URLs, ferramentas de introspection.

---

## 4. Parâmetros de Path

### 4.1 Tipo `Param` e `Params`

```go
// Já implementado
type Param struct {
    Key   string
    Value string
}

type Params []Param

func (ps Params) Get(name string) string  // devolve "" se não existe
```

```go
// Por implementar — distingue "não existe" de "valor vazio"
func (ps Params) Lookup(name string) (value string, ok bool)

// Conversões de tipo — devolvem (zero, err) se não existe ou conversão falha
func (ps Params) Int(name string) (int, error)
func (ps Params) Int64(name string) (int64, error)
func (ps Params) Uint64(name string) (uint64, error)
func (ps Params) Float64(name string) (float64, error)
func (ps Params) Bool(name string) (bool, error)

// Serialização
func (ps Params) Map() map[string]string
func (ps Params) Slice() []Param  // cópia (já é um slice, alias semântico)
```

### 4.2 Acesso aos Parâmetros

```go
// Já implementado
id := muxmaster.PathParam(r, "id")
ps := muxmaster.ParamsFromContext(r.Context())

// Por implementar
pattern := muxmaster.RoutePattern(r)  // devolve "/users/:id"
```

### 4.3 Pool de Parâmetros (interno)

- `sync.Pool` com capacidade pré-alocada de `maxParams = 16`
- `acquireParams()` / `releaseParams()` — já implementados
- Cópia para contexto antes de libertar o pool — já implementado

---

## 5. Middleware

### 5.1 Tipos de Middleware

O tipo de middleware é sempre `func(http.Handler) http.Handler` — compatível com qualquer ecosystem Go (alice, negroni, etc.).

```go
type MiddlewareFunc func(http.Handler) http.Handler
```

### 5.2 Scopes de Middleware

| Scope | API | Estado | Comportamento |
|---|---|---|---|
| Global | `r.Use(mw...)` | ✅ | Envolve todas as rotas registadas após a chamada |
| Por grupo | `g.Use(mw...)` | ✅ | Envolve rotas do grupo registadas após a chamada |
| Por rota | `r.With(mw...).GET(path, h)` | 🔲 | Cria router temporário com middleware extra; não afecta outras rotas |
| Pré-routing | `r.Pre(mw...)` | 🔲 | Executa antes do lookup de rota; pode alterar o path |

```go
// Por implementar — With()
r.With(authMiddleware, rateLimiter).GET("/admin", adminHandler)

// Por implementar — Pre()
r.Pre(muxmaster.CleanPath)      // normaliza path antes do lookup
r.Pre(muxmaster.StripSlashes)   // remove trailing slash antes do lookup
```

**Nota sobre ordem de execução:**

```
Pre middleware → [lookup de rota] → Global middleware → Group middleware → Per-route middleware → Handler
```

### 5.3 Middleware da Stdlib (pacote `muxmaster/middleware`)

Fornecidos no sub-pacote `middleware`. Zero dependências externas. Todos seguem a assinatura `func(http.Handler) http.Handler`.

| Middleware | Função | Estado | Notas |
|---|---|---|---|
| `Logger` | Log de cada request (método, path, status, duração) | 🔲 | `Logger(out io.Writer)` |
| `Recoverer` | Recover de panic com stack trace | 🔲 | Substitui `PanicHandler` com middleware idiomático |
| `RequestID` | Injeta UUID/random no contexto e header `X-Request-ID` | 🔲 | `RequestID()` |
| `RealIP` | Extrai IP real de `X-Forwarded-For` / `X-Real-IP` | 🔲 | `RealIP()` |
| `Timeout` | Aplica `context.WithTimeout` ao request | 🔲 | `Timeout(d time.Duration)` |
| `NoCache` | Define headers `Cache-Control: no-store` etc. | 🔲 | `NoCache()` |
| `Compress` | Compressão gzip do body de resposta | 🔲 | `Compress(level int)` |
| `BasicAuth` | HTTP Basic Authentication | 🔲 | `BasicAuth(realm string, creds map[string]string)` |
| `CORS` | Cross-Origin Resource Sharing | 🔲 | `CORS(opts CORSOptions)` |
| `ThrottleBacklog` | Limita concorrência com backlog | 🔲 | `ThrottleBacklog(limit, backlog int, timeout time.Duration)` |
| `StripSlashes` | Remove trailing slash antes de passar ao handler | 🔲 | Para usar com `Pre()` |
| `CleanPath` | Normaliza `//`, `.`, `..` no path | 🔲 | Para usar com `Pre()` |
| `SetHeader` | Define header fixo na resposta | 🔲 | `SetHeader(key, value string)` |
| `WithValue` | Injeta valor no contexto do request | 🔲 | `WithValue(key, val any)` |

---

## 6. Grupos e Subrouters

### 6.1 Grupos (prefixo + middleware)

```go
// Já implementado
api := r.Group("/api/v1")
api.Use(apiKeyCheck)
api.GET("/users", listUsers)

admin := api.Group("/admin")
admin.Use(adminOnly)
```

### 6.2 Inline Group (functional style)

```go
// Por implementar
r.Route("/api/v1", func(r *muxmaster.Group) {
    r.Use(apiKeyCheck)
    r.GET("/users", listUsers)
    r.POST("/users", createUser)

    r.Route("/admin", func(r *muxmaster.Group) {
        r.Use(adminOnly)
        r.DELETE("/users/:id", deleteUser)
    })
})
```

### 6.3 Mount (subrouter externo)

```go
// Por implementar
// Monta qualquer http.Handler num prefixo
// Strip do prefixo antes de passar ao handler
r.Mount("/legacy", legacyRouter)
r.Mount("/docs", http.FileServer(http.Dir("./docs")))

// Equivalente para grupos
api.Mount("/v2", v2Router)
```

**Comportamento do Mount:**
- Strip do prefixo do path antes de delegar ao handler montado
- Trailing slash no prefixo é normalizado
- Handler montado pode ser qualquer `http.Handler` (outro `*Mux`, `chi.Router`, `http.ServeMux`, etc.)

---

## 7. Handlers Especiais e Configuração

### 7.1 Configuração do Router

```go
type Mux struct {
    // Já implementados
    RedirectTrailingSlash  bool  // default: true
    RedirectFixedPath      bool  // default: true
    HandleMethodNotAllowed bool  // default: true
    HandleOPTIONS          bool  // default: true

    NotFound         http.Handler
    MethodNotAllowed http.Handler
    PanicHandler     func(http.ResponseWriter, *http.Request, any)

    // Por implementar
    RedirectCode         int   // default: 301 para GET, 307 para outros
    CaseInsensitive      bool  // default: false; matching case-insensitive
    UseRawPath           bool  // default: false; usa r.URL.RawPath para params
    UnescapePathValues   bool  // default: true; URL-decode dos param values
}
```

### 7.2 Handlers de Erro

| Handler | Estado | Assinatura |
|---|---|---|
| `NotFound` | ✅ | `http.Handler` |
| `MethodNotAllowed` | ✅ | `http.Handler` |
| `PanicHandler` | ✅ | `func(http.ResponseWriter, *http.Request, any)` |
| `GlobalOPTIONS` | 🔲 | `http.Handler` — resposta custom a OPTIONS globais |

### 7.3 Códigos de Status Emitidos

| Condição | Código | Estado |
|---|---|---|
| Rota não encontrada | 404 Not Found | ✅ |
| Método não permitido | 405 Method Not Allowed | ✅ |
| OPTIONS auto-resposta | 204 No Content | ✅ |
| Trailing slash (GET) | 301 Moved Permanently | ✅ |
| Trailing slash (outros) | 307 Temporary Redirect | ✅ |
| Fixed path | 307 Temporary Redirect | ✅ |
| Fixed path (GET) | 301 Moved Permanently | 🔲 |

---

## 8. Introspection e Navegação

### 8.1 RoutePattern

```go
// Por implementar
// Devolve o padrão da rota matched — disponível dentro de handlers e middleware
pattern := muxmaster.RoutePattern(r)  // ex: "/users/:id"
```

Útil para: logging estruturado, métricas Prometheus (evita high cardinality), tracing (span name), geração de OpenAPI.

### 8.2 Listagem de Rotas

```go
// Por implementar
type RouteInfo struct {
    Method  string
    Pattern string
    Handler string  // nome da função (runtime.FuncForPC)
}

routes := r.Routes() []RouteInfo

// Walking da árvore — para geração de documentação
err := r.Walk(func(method, pattern string, handler http.Handler) error {
    fmt.Printf("%s %s\n", method, pattern)
    return nil
})
```

### 8.3 Lookup Programático

```go
// Por implementar
handler, params, found := r.Lookup("GET", "/users/123")
// handler: http.Handler (já com middleware aplicado)
// params: Params{{"id", "123"}}
// found: true
```

---

## 9. Ficheiros Estáticos

```go
// Por implementar
// Serve ficheiros estáticos de um FileSystem
// path deve terminar em /*filepath
r.ServeFiles("/static/*filepath", http.Dir("./public"))
r.ServeFiles("/static/*filepath", http.FS(embeddedFS))  // embed.FS

// Variante num grupo
api.ServeFiles("/assets/*filepath", http.Dir("./assets"))
```

**Comportamento:**
- Usa `http.FileServer` internamente — zero lógica custom
- Regista automaticamente HEAD além de GET
- Protege contra path traversal (comportamento do `http.FileServer`)

---

## 10. Padrões de Handler

MuxMaster suporta dois padrões de handler. A assinatura principal (`http.HandlerFunc`) nunca muda — o padrão de erro é additive.

### 10.1 Padrão Standard (já implementado)

```go
func listUsers(w http.ResponseWriter, r *http.Request) {
    // ...
}
```

### 10.2 Padrão com Retorno de Erro (por implementar)

```go
// Tipo alternativo
type HandlerFuncE func(http.ResponseWriter, *http.Request) error

// Adaptador — converte HandlerFuncE para http.Handler
// O tratamento do erro é delegado ao ErrorHandler configurado
r.HandleE("GET", "/users", func(w http.ResponseWriter, r *http.Request) error {
    users, err := db.List(r.Context())
    if err != nil {
        return muxmaster.Error(500, err)  // ou qualquer error com StatusCode() int
    }
    return muxmaster.JSON(w, 200, users)
})

// Conveniência
r.GETE("/users", listUsersE)

// Handler de erro global
r.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    var httpErr muxmaster.HTTPError
    if errors.As(err, &httpErr) {
        http.Error(w, httpErr.Error(), httpErr.StatusCode())
        return
    }
    http.Error(w, "internal error", 500)
}
```

**Nota:** Este padrão é completamente opcional. A API standard `http.HandlerFunc` continua a ser a principal.

---

## 11. Utilitários de Request/Response

Fornecidos como funções standalone (não métodos de um context customizado — mantém compatibilidade total com `net/http`).

### 11.1 Leitura de Request

```go
// Por implementar — pacote muxmaster ou muxmaster/requtil

// Query parameters
muxmaster.QueryParam(r, "page")                     // string
muxmaster.QueryParamDefault(r, "page", "1")         // string com default
muxmaster.QueryParamInt(r, "page") (int, error)     // typed

// Form data
muxmaster.FormValue(r, "name") string

// Cookies
muxmaster.GetCookie(r, "session") (*http.Cookie, error)
```

### 11.2 Escrita de Response

```go
// Por implementar — funções puras, não métodos

// JSON — usa encoding/json (stdlib)
muxmaster.JSON(w, 200, payload)         // Content-Type: application/json
muxmaster.JSONError(w, 400, "bad request")

// XML — usa encoding/xml (stdlib)
muxmaster.XML(w, 200, payload)

// Texto
muxmaster.Text(w, 200, "ok")

// Redirect
muxmaster.Redirect(w, r, 301, "/new-path")

// No content
muxmaster.NoContent(w)

// Cookies
muxmaster.SetCookie(w, &http.Cookie{Name: "session", Value: "xyz"})
```

**Nota de design:** Estas são funções puras sobre `http.ResponseWriter` — não há context type customizado. A performance não é afectada porque estas funções só são chamadas na escrita da resposta (fim do handler).

---

## 12. Servidor HTTP (helpers opcionais)

MuxMaster não é um framework — não substitui `http.Server`. Os helpers abaixo são açúcar sintáctico sobre o stdlib.

```go
// Por implementar — opcional, pode ser um sub-pacote muxmaster/server

// Iniciar servidor
err := muxmaster.ListenAndServe(":8080", r)

// HTTPS com certificados
err := muxmaster.ListenAndServeTLS(":443", "cert.pem", "key.pem", r)

// Graceful shutdown — wrapping de http.Server.Shutdown
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
defer cancel()
err := muxmaster.ListenAndServeContext(ctx, ":8080", r)
```

---

## 13. Testes

### 13.1 Cobertura de Testes Unitários

| Funcionalidade | Estado |
|---|---|
| Rotas estáticas (múltiplos métodos) | ✅ |
| Parâmetros de path (1, 2, N) | ✅ |
| Catch-all wildcard | ✅ |
| Custom NotFound | ✅ |
| Trailing slash redirect | ✅ |
| Method Not Allowed (405 + Allow) | ✅ |
| OPTIONS automático | ✅ |
| Ordem de execução do middleware | ✅ |
| Groups — prefixo e métodos | ✅ |
| Groups — middleware scoped | ✅ |
| Sub-grupos aninhados | ✅ |
| PanicHandler | ✅ |
| ParamsFromContext | ✅ |
| Regex params | 🔲 |
| Parâmetros opcionais | 🔲 |
| With() per-route middleware | 🔲 |
| Mount() subrouter | 🔲 |
| Pre() middleware | 🔲 |
| Lookup() | 🔲 |
| ANY() / Match() | 🔲 |
| Routes() / Walk() | 🔲 |
| RoutePattern() | 🔲 |
| ServeFiles() | 🔲 |
| Case-insensitive | 🔲 |
| Cada middleware do stdlib | 🔲 |

### 13.2 Cobertura de Benchmarks

| Caso | Estado |
|---|---|
| Rota estática | ✅ |
| 1 parâmetro | ✅ |
| 2 parâmetros | ✅ |
| 3 parâmetros | ✅ |
| Catch-all | ✅ |
| 404 (rota não encontrada) | ✅ |
| Paralelo estático | ✅ |
| Paralelo 1 param | ✅ |
| Regex param | 🔲 |
| Middleware chain (N layers) | 🔲 |
| Lookup() | 🔲 |
| Mount() | 🔲 |
| Comparação directa com httprouter | 🔲 |
| Comparação directa com bunrouter | 🔲 |
| Comparação directa com chi | 🔲 |

---

## 14. Race Conditions e Concorrência

| Garantia | Estado |
|---|---|
| Registo de rotas thread-safe (RWMutex) | ✅ |
| ServeHTTP concorrente thread-safe | ✅ |
| sync.Pool para Params | ✅ |
| Registo de rotas após início de serving | ❌ Não suportado (by design) |
| Detector de race: `go test -race ./...` | ✅ Passa |

---

## 15. Não-Objectivos

As seguintes funcionalidades estão explicitamente **fora de scope** para manter o foco e zero dependências:

| Funcionalidade | Razão |
|---|---|
| HTTP/3 (QUIC) | Requer dependência externa (`quic-go`) |
| WebSocket | Fora do scope de routing |
| Template engine | Responsabilidade da aplicação; Go tem `html/template` |
| ORM / base de dados | Framework territory |
| Injecção de dependências | Framework territory |
| Hot reload de rotas | Modelo de concorrência diferente; aumenta complexidade |
| gRPC | Protocolo diferente de HTTP/REST |
| Validação de structs | Responsabilidade da aplicação |

---

## 16. Estrutura de Pacotes (alvo)

```
muxmaster/
├── mux.go          # Mux, ServeHTTP, métodos HTTP, configuração
├── tree.go         # Árvore radix — addRoute, getValue, regex
├── params.go       # Param, Params, pool, type helpers
├── group.go        # Group, Route, Mount, With
├── handler.go      # HandlerFuncE, HTTPError, adaptadores
├── response.go     # JSON, XML, Text, Redirect, helpers de response
├── middleware/
│   ├── logger.go
│   ├── recoverer.go
│   ├── request_id.go
│   ├── real_ip.go
│   ├── timeout.go
│   ├── compress.go
│   ├── basic_auth.go
│   ├── cors.go
│   ├── throttle.go
│   ├── no_cache.go
│   ├── strip_slashes.go
│   ├── clean_path.go
│   └── set_header.go
├── mux_test.go
├── bench_test.go
├── go.mod
└── README.md
```

---

## 17. Compatibilidade

| Versão Go | Estado |
|---|---|
| Go 1.26+ | ✅ Suportado (versão mínima) |
| Go 1.22–1.25 | ❌ — usa `range n` e `min`/`max` builtins |

### Compatibilidade com ecosystem

| Ecosystem | Compatibilidade |
|---|---|
| `net/http` middleware (`alice`, `negroni`, etc.) | ✅ Total — middleware type é `func(http.Handler) http.Handler` |
| `http.Handler` interface | ✅ Total — `*Mux` implementa `http.Handler` |
| `http.FileServer` | ✅ via `Mount()` e `ServeFiles()` |
| Gin handlers | ❌ — assinatura diferente |
| Echo handlers | ❌ — assinatura diferente |
| httprouter handlers | ❌ — assinatura diferente (Params no handler) |

---

## 18. Roadmap de Implementação (ordenado por prioridade)

### Fase 1 — Completar o core router

1. Métodos `CONNECT` e `TRACE`
2. `ANY()` e `Match()` multi-método
3. `Lookup(method, path)`
4. `With()` per-route middleware
5. `Pre()` pre-routing middleware
6. `Mount()` subrouter
7. Inline `Route()` / `r.Group(prefix, func(r){})`
8. `RoutePattern(r)` e `Routes()` / `Walk()`

### Fase 2 — Parâmetros e routing avançado

9. Parâmetros com regex `{id:\d+}`
10. Parâmetros opcionais `{/:slug}`
11. `Params.Lookup()`, `Params.Int()`, `.Int64()`, `.Bool()`, `.Float64()`, `.Map()`
12. `ServeFiles()`
13. `CaseInsensitive` matching
14. `GlobalOPTIONS` handler

### Fase 3 — Utilitários e middleware stdlib

15. Response helpers: `JSON()`, `XML()`, `Text()`, `Redirect()`, `NoContent()`
16. Middleware: `Logger`, `Recoverer`, `RequestID`, `RealIP`, `Timeout`
17. Middleware: `NoCache`, `SetHeader`, `WithValue`, `StripSlashes`, `CleanPath`
18. Middleware: `Compress`, `BasicAuth`, `CORS`, `ThrottleBacklog`

### Fase 4 — Padrões avançados e polish

19. Handler com retorno de erro (`HandlerFuncE`, `GETE()`, etc.)
20. Query/form helpers, cookie helpers
21. Benchmarks de comparação directa com competidores
22. `RegisterMethod()` para métodos HTTP customizados
23. `UseRawPath`, `UnescapePathValues` para edge cases de encoding
