# MuxMaster — Guia para Claude

## O que é este projecto
Router HTTP / HTTP muxer de alta performance para Go, implementado **puramente em Go** (zero dependências externas). Usa uma árvore radix (Patricia trie) para lookup O(k) onde k é o comprimento do path.

**Este é um projecto open-source.** Toda a decisão de design, código, documentação e tooling deve seguir os preceitos típicos de um projecto open-source de qualidade:

### Padrões obrigatórios de projecto open-source

#### Qualidade de código
- **`go vet ./...`** — sem avisos; executar antes de qualquer commit
- **`staticcheck ./...`** — análise estática avançada; corrigir todos os findings
- **`golangci-lint run`** — suite completa de linters (errcheck, gosimple, ineffassign, unused, etc.)
- **`go test -race ./...`** — zero race conditions; nunca relaxar este requisito
- **Cobertura de testes** — toda a funcionalidade pública tem teste; regressões têm teste antes do fix

#### Testes
- Cada feature nova requer testes unitários em `mux_test.go` (ou ficheiro dedicado)
- Casos de erro e edge cases devem ser cobertos, não apenas o happy path
- Benchmarks em `bench_test.go` para qualquer código no hot path
- Testes de integração quando a interacção entre componentes for não-trivial

#### Documentação
- **`README.md`** — instalação, quickstart, exemplos de uso, badges de CI/cobertura
- **GoDoc** — todos os tipos e funções exportados têm doc comment (`// TypeName ...`)
- **`CHANGELOG.md`** — registo de mudanças por versão (seguir Keep a Changelog + SemVer)
- **`CONTRIBUTING.md`** — guia para contribuidores: como fazer fork, branch, PR, e correr testes
- **`LICENSE`** — ficheiro de licença presente na raiz

#### Gestão de versões e releases
- **SemVer** (Semantic Versioning): `vMAJOR.MINOR.PATCH`
  - PATCH: bug fixes retrocompatíveis
  - MINOR: features novas retrocompatíveis
  - MAJOR: breaking changes na API pública
- Tags Git para cada release: `git tag v1.2.3`
- Release notes no GitHub com o diff de CHANGELOG

#### CI/CD
- Pipeline de CI (GitHub Actions ou equivalente) que corre em cada PR:
  - `go build ./...`
  - `go test -race ./...`
  - `go vet ./...`
  - linters (golangci-lint)
- Badge de estado de CI no README
- Protecção da branch `main`: merge só após CI verde

#### Compatibilidade e API pública
- Não introduzir breaking changes em MINOR/PATCH releases
- Deprecar antes de remover: marcar com `// Deprecated:` no GoDoc antes de eliminar
- Manter compatibilidade com a versão mínima de Go declarada no `go.mod`
- Seguir as Go API compatibility guidelines

## Versão de Go
**Go 1.26+** (go.mod declara `go 1.26`). Usa funcionalidades modernas:
- `for i := range n` (range sobre inteiro, Go 1.22+)
- `min`/`max` builtins (Go 1.21+)

## Estrutura de ficheiros

| Ficheiro | Responsabilidade |
|---|---|
| `mux.go` | `Mux` struct, `ServeHTTP`, métodos HTTP, middleware global |
| `tree.go` | Árvore radix — `node`, `addRoute`, `getValue`, `findWildcard` |
| `params.go` | Tipo `Params`, `sync.Pool`, `PathParam()`, `ParamsFromContext()` |
| `group.go` | `Group` — prefixo de path + middleware de grupo |
| `mux_test.go` | 17 testes unitários |
| `bench_test.go` | 8 benchmarks incluindo concorrência |

## API pública

```go
r := muxmaster.New()

// Middleware (deve ser registado ANTES das rotas que deve envolver)
r.Use(logger, auth)

// Rotas
r.GET("/users", listUsers)
r.GET("/users/:id", getUser)       // parâmetro de path
r.GET("/static/*filepath", files)  // catch-all

// Grupos
api := r.Group("/api/v1")
api.Use(apiKeyCheck)
api.POST("/items", createItem)

// Sub-grupos
admin := api.Group("/admin")

// Ler parâmetros no handler
id := muxmaster.PathParam(r, "id")
ps := muxmaster.ParamsFromContext(r.Context())

// Handlers customizados
r.NotFound = myHandler
r.MethodNotAllowed = myHandler
r.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }

// Opções (todas true por defeito)
r.RedirectTrailingSlash  = true
r.RedirectFixedPath      = true
r.HandleMethodNotAllowed = true
r.HandleOPTIONS          = true

http.ListenAndServe(":8080", r)
```

## Decisões de design importantes

### Middleware aplicado no registo, não no request
`wrapMiddleware` é chamado em `Handle()` no momento do registo. Isto significa **zero overhead de middleware por request** — mas o `Use()` deve ser chamado antes das rotas que deve envolver.

### sync.Pool para Params
- `acquireParams()` / `releaseParams()` evitam alocação na pesquisa de rotas sem parâmetros
- Quando há parâmetros: faz-se um `make(Params, n)` + `copy` antes de armazenar no contexto (necessário para não partilhar o array do pool entre goroutines)

### Concorrência
- `sync.RWMutex` protege o mapa `m.trees` (adição de métodos novos)
- Os nós da árvore são apenas lidos após o registo inicial — não suporta registo dinâmico de rotas após iniciar a servir

### Tipo `Param` vs função `PathParam`
Go não permite um tipo e uma função com o mesmo nome no mesmo pacote. Por isso:
- Tipo: `muxmaster.Param{Key, Value}`
- Função: `muxmaster.PathParam(r, "name")`

### Bug corrigido na árvore radix
A condição `c != ':' && c != '*' && n.nType != param` era incorrecta — impedia a criação de filhos estáticos em nós `param` (ex: `/users/:id/posts`), corrompendo a árvore ao sobrescrever `n.path`. Corrigido para `c != ':' && c != '*'`.

## Comandos úteis

```bash
go test ./...                          # todos os testes
go test -v ./...                       # verbose
go test -bench=. -benchmem ./...       # benchmarks com alocações
go test -race ./...                    # detector de race conditions
go vet ./...                           # análise estática
```

## Competidores a bater em performance

Routers HTTP Go mais conhecidos e adoptados pela comunidade, por ordem de relevância como alvo de performance.

### Routers puros (foco em performance — concorrência directa)

| Módulo | Import path | Algoritmo | Estrelas GitHub | Notas |
|---|---|---|---|---|
| **httprouter** | `github.com/julienschmidt/httprouter` | Radix tree (por método) | ~16 000 | Referência histórica de performance; base do Gin; ~0 allocs em rotas estáticas |
| **bunrouter** | `github.com/uptrace/bunrouter` | Radix tree zero-alloc | ~3 800 | Reclama 0 allocs mesmo com parâmetros; compatível com httprouter |
| **chi** | `github.com/go-chi/chi/v5` | Patricia radix trie | ~19 000 | 100% stdlib-compatible; muito popular em APIs modulares |
| **httptreemux** | `github.com/dimfeld/httptreemux/v5` | Radix tree | ~1 900 | Performance próxima do httprouter; suporta case-insensitive |
| **bone** | `github.com/go-zoo/bone` | Tree-based | ~1 300 | ~118 ns/op reportado; suporta regex e wildcard |

### Frameworks com router integrado (concorrência indirecta)

| Framework | Import path | Router interno | Estrelas GitHub | Notas |
|---|---|---|---|---|
| **Gin** | `github.com/gin-gonic/gin` | httprouter (fork) | ~81 000 | O mais popular; usa radix tree do httprouter |
| **Echo** | `github.com/labstack/echo/v5` | Radix tree próprio + sync.Pool | ~30 000 | Reportado como mais rápido em 2025 em hello-world |

### Frameworks de stack alternativa (comparação de raw routing)

| Framework | Import path | Stack HTTP | Estrelas GitHub | Notas |
|---|---|---|---|---|
| **Fiber** | `github.com/gofiber/fiber/v3` | fasthttp (não net/http) | ~35 000 | API estilo Express; incompatível com stdlib; benchmarks de raw routing são comparáveis mas stacks diferentes |

> **Nota sobre Fiber:** A comparação com Fiber é de *raw routing throughput*, não de stack completa. Fiber usa `fasthttp` que evita alocações de `net/http` — vantagem estrutural independente do router. Os benchmarks medem apenas a lógica de dispatch de rotas.

### Routers mais lentos (não são o alvo, mas são muito usados)

| Módulo | Algoritmo | Notas |
|---|---|---|
| **gorilla/mux** | Regex | ~21 000 ★; archivado em 2022; ~3 444 278 ns/op em rotas estáticas |
| **net/http.ServeMux** | Linear (Go 1.22+ melhorado) | Stdlib; ~706 222 ns/op; suficiente para a maioria dos casos |

### Benchmarks de referência conhecidos (rotas estáticas, lookup único)

| Router | ns/op | allocs/op | Fonte |
|---|---|---|---|
| httprouter | ~15 010 | 0 | go-http-routing-benchmark |
| httptreemux | ~15 123 | 0 | go-http-routing-benchmark |
| bunrouter | < httprouter | 0 | bunrouter docs |
| chi | competitivo | baixo | go-http-routing-benchmark |
| gorilla/mux | ~3 444 278 | 156 015 | go-http-routing-benchmark |
| net/http | ~706 222 | 96 | go-http-routing-benchmark |

> **Alvo principal:** igualar ou superar `httprouter` e `bunrouter` em ns/op e allocs/op, mantendo API idiomática e compatibilidade 100% com `net/http`.

### Referências de benchmarks
- https://github.com/julienschmidt/go-http-routing-benchmark
- https://github.com/smallnest/go-web-framework-benchmark

---

## Subagentes disponíveis

Dois agentes especializados estão configurados em `.claude/agents/`. Usa-os proactivamente — não esperes que o utilizador os peça explicitamente.

### `go-perf-optimizer`
Especialidade: medir, diagnosticar e optimizar performance do código Go deste projecto (benchmarks, pprof, escape analysis, assembly).

**Activa automaticamente quando:**
- Qualquer ficheiro do hot path é modificado: `mux.go`, `tree.go`, `params.go`
- `allocs/op` ou `ns/op` aumentam em qualquer benchmark após uma mudança
- Uma nova chamada a `context.WithValue`, `r.WithContext`, `sync.RWMutex`, ou `sync.Pool` é adicionada ao hot path
- O utilizador pede benchmarks, profiling, ou análise de performance
- Antes de um tag de release (auditoria completa)
- Um novo código path é adicionado a `ServeHTTP` ou `getValue`

**Não actives quando:** a mudança é apenas em `group.go`, testes, documentação, ou comentários.

### `benchmark-elite-tester`
Especialidade: criar e correr benchmarks de competidores (em `/competitor/<nome>/`), analisar código fonte dos competidores, e produzir comparações objectivas com evidência de código.

**Activa automaticamente quando:**
- O utilizador questiona *porquê* MuxMaster é mais lento que um competidor específico
- É pedida uma comparação directa com httprouter, bunrouter, chi, Echo, ou Gin
- É necessário estudar como um competidor elimina alocações (técnica de implementação)
- O `go-perf-optimizer` identificou uma gap de performance e precisa de evidência sobre como os competidores a resolvem
- É pedido setup de ambiente de benchmark para um novo competidor

**Não actives quando:** a questão é apenas sobre o código MuxMaster em si, sem comparação com externos.

### Coordenação entre agentes

Fluxo típico de optimização:
1. **go-perf-optimizer** → identifica regressão ou oportunidade (ex: "4 allocs/op em rotas estáticas")
2. **benchmark-elite-tester** → produz evidência de como competidores resolvem o mesmo problema
3. **go-perf-optimizer** → implementa e valida a optimização com benchstat

Os dois agentes podem correr em paralelo quando as tarefas são independentes (ex: profiling do MuxMaster em paralelo com setup do ambiente httprouter).

---

## Performance baseline (Apple M4, Go 1.26)

Benchmarks internos (bench_test.go):

| Caso | ns/op | B/op | allocs/op |
|---|---|---|---|
| Rota estática | 13.5 | 0 | 0 |
| 1 parâmetro | 24 | 0 | 0 |
| 2 parâmetros | 36 | 0 | 0 |
| 3 parâmetros | 42 | 0 | 0 |
| Catch-all | 24 | 0 | 0 |
| Paralelo estático | 2.0 | 0 | 0 |
| Paralelo 1 parâmetro | 6.8 | 0 | 0 |

Benchmarks competitivos (competitor/bench_test.go, rota `/api/v1/...`):

| Caso | MuxMaster | httprouter | bunrouter | Fiber v3 |
|---|---|---|---|---|
| Estático | **13.5 ns, 0 allocs** | 15.9 ns, 0 allocs | 14.0 ns, 0 allocs | 187 ns, 0 allocs |
| 1 parâmetro | 27 ns, 0 allocs | 32.8 ns, 1 alloc | **22.4 ns**, 0 allocs | 212 ns, 0 allocs |
| 2 parâmetros | **38.7 ns, 0 allocs** | 40.0 ns, 1 alloc | 41.7 ns, 0 allocs | 286 ns, 0 allocs |
| 3 parâmetros | 46.7 ns, 0 allocs | 44.5 ns, 1 alloc | **29.8 ns**, 0 allocs | 267 ns, 0 allocs |
| Catch-all | **23.2 ns, 0 allocs** | 28.0 ns, 1 alloc | 11.9 ns, 0 allocs | 211 ns, 0 allocs |
| Paralelo estático | **1.55 ns, 0 allocs** | 1.98 ns, 0 allocs | 1.77 ns, 0 allocs | 27 ns, 0 allocs |
| Paralelo 1 parâmetro | ~10 ns, 0 allocs | 15.5 ns, 1 alloc | 3.6 ns, 0 allocs | **32 ns**, 0 allocs |

> Benchmarks Fiber medidos em AMD Ryzen 9 5900HX / Go 1.26.2 (`competitor/fiber/bench_test.go`). Os restantes em Apple M4. Fiber inclui overhead de URI parsing do fasthttp (~55% do CPU) que faz parte do custo real de produção.

Notas de interpretação:
- **bunrouter** usa extracção lazy de parâmetros — não copia os valores durante o tree walk. Em handlers reais que lêem todos os parâmetros, MuxMaster é mais rápido a partir de ≥3 parâmetros (eager é O(1) por leitura; lazy é O(N))
- **Fiber** usa linear scan de rotas (não radix trie) + URI parsing fasthttp por cada request; MuxMaster é 5–8x mais rápido em rotas seriais. Fiber não é compatível com `net/http` (usa `fasthttp`)
- **Paralelo Fiber (param1):** Fiber ganha por 1.2x porque agrupa params e contexto num único pool.Get/Put; MuxMaster faz dois — investigar fusão

---

## Princípios de desenvolvimento — Desempenho Máximo

### Abordagem multi-disciplinar obrigatória
Antes de implementar qualquer solução, deves considerar **todas as abordagens possíveis** — estruturas de dados alternativas, algoritmos de outras linguagens (C, C++, Rust, Zig, Java, etc.), técnicas de sistemas operativos, e padrões de hardware — e implementar a mais eficiente em Go idiomático. Não te limites ao que é comum em Go; inspira-te no melhor de todas as linguagens e transpõe para Go.

### Critérios de selecção de implementação
1. **Medir primeiro** — nunca optimizar sem benchmark antes e depois (`benchstat`)
2. **Menor ns/op e allocs/op** — estas são as métricas primárias de sucesso
3. **Zero alocações no hot path** é o objectivo; qualquer alocação deve ser justificada
4. **Idiomático Go** — toda a implementação deve respeitar os princípios Go: simplicidade, legibilidade, uso correcto de goroutines/channels/sync, e compatibilidade 100% com `net/http`

### Uso activo dos sub-agentes especializados
Os sub-agentes não são opcionais — são parte do processo. Deves activá-los proactivamente (sem esperar que o utilizador peça) sempre que as condições descritas na secção "Subagentes disponíveis" se verificarem. O objectivo é desempenho máximo, e os agentes especializados são o mecanismo para o atingir com rigor e evidência.
