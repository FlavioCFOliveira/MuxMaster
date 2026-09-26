# MuxMaster — Guide for Claude

## Roadmap

**Name:** muxmaster

> This is the `rmp` roadmap name. Every `rmp` command targets it with `-r muxmaster`.

## What this project is
A high-performance HTTP router / HTTP muxer for Go, implemented **in pure Go** (zero external dependencies). It uses a radix tree (Patricia trie) for O(k) lookup where k is the path length.

**This is an open-source project.** Every design decision, code change, documentation update, and tooling choice must follow the standard precepts of a quality open-source project:

### Mandatory open-source standards

#### Code quality
- **`go vet ./...`** — no warnings; run before any commit
- **`staticcheck ./...`** — advanced static analysis; fix every finding
- **`golangci-lint run`** — full linter suite (errcheck, gosimple, ineffassign, unused, etc.)
- **`go test -race ./...`** — zero race conditions; never relax this requirement
- **Test coverage** — every public function has a test; regressions get a test before the fix

#### Tests
- Every new feature requires unit tests in `mux_test.go` (or a dedicated file)
- Error and edge cases must be covered, not just the happy path
- Benchmarks in `bench_test.go` for any code on the hot path
- Integration tests when the interaction between components is non-trivial

#### Documentation
- **`README.md`** — installation, quickstart, usage examples, CI/coverage badges
- **GoDoc** — every exported type and function has a doc comment (`// TypeName ...`)
- **`CHANGELOG.md`** — change log per version (follow Keep a Changelog + SemVer)
- **`CONTRIBUTING.md`** — contributor guide: how to fork, branch, PR, and run tests
- **`LICENSE`** — license file present at the repository root

#### Versioning and releases
- **SemVer** (Semantic Versioning): `vMAJOR.MINOR.PATCH`
  - PATCH: backward-compatible bug fixes
  - MINOR: backward-compatible new features
  - MAJOR: breaking changes to the public API
- Git tags for every release: `git tag v1.2.3`
- Release notes on GitHub with the CHANGELOG diff

#### CI/CD
- A CI pipeline (GitHub Actions or equivalent) that runs on every PR:
  - `go build ./...`
  - `go test -race ./...`
  - `go vet ./...`
  - linters (golangci-lint)
- CI status badge in the README
- Branch protection on `main`: merge only after CI is green

#### Compatibility and public API
- No breaking changes in MINOR/PATCH releases
- Deprecate before removing: mark with `// Deprecated:` in GoDoc before deletion
- Maintain compatibility with the minimum Go version declared in `go.mod`
- Follow the Go API compatibility guidelines

---

# Working agreement

These rules govern **every** interaction with this project. They take precedence over any default behaviour and over every other section of this file. When another section appears to contradict them, these rules win.

## 1. Core rules

1. **You are NOT authorised to take decisions on your own.** Whenever the instructions are insufficient, unclear, unspecific or not concrete, or whenever contradictions or ambiguities exist, you **MUST ALWAYS ASK** the user how to proceed.
   - When asking, always offer multiple options (a, b, c, …) and state which one you recommend.
   - When several questions need clarification, ask them **one at a time** (sequentially), never all at once.
   - **The boundary between acting and asking:** inside the scope of the requested work, obvious, low-risk corrections (for example, a bug with an unambiguous fix in the code being changed) proceed immediately. Any decision that changes the scope, the expected behaviour, the architecture or the requirements demands that you ask the user first. Anything outside the scope of the requested work follows §4.
2. **Documentation faithful to the code.** Documentation must be accurate and must always reflect the real state of the code.
3. **Workflow.** Work always follows this order: **Specify → Implement → Test → Document.**

## 2. Completeness and self-contained development

- You are **FORBIDDEN** from executing work only partially. Every piece of work you start must be carried out in full. **NEVER leave a task half-done or partially done.**
- Every development cycle must be **self-contained**: each cycle produces a complete, usable result.
- When a new, unforeseen need arises during a task and it is **inside** the task's scope, resolve it within the same development cycle. When it is **outside** the task's scope, follow §4.
- All code must be **full-fledged** (complete and ready to use). Tests with `skip` must **NOT** be created.

## 3. Production-grade by default

**EVERY** action you take — development, fixes, evaluations, analyses, audits, and anything else — must be treated with the rigour expected of **production** work.

## 4. Scope discipline (no unrequested work)

- Your actions must be **strictly directed at the objective** of the work in progress.
- You are **FORBIDDEN** from voluntarily starting any work that was not **EXPLICITLY** requested.
- Whenever you identify a need outside the scope of the work in progress (including a pre-existing bug), report it to the user and ask how to proceed. **NEVER** start that work proactively.

## 5. Synergy and convergence

Motto: **"Make the effort pay off: deliver the most with the least work."** This is the default way of working on this project. The user does not need to ask for it, and it is **never** an `rmp` task.

- **Across tasks.** Whenever tasks (in `rmp`, or requested ad hoc by the user) show verifiable functional or technical proximity, complementary objectives, or converging objectives, **ALWAYS** merge them into a single development effort.
- **Within the work.** Group operations of the same kind:
  - write all the code (of every grouped task) in one pass;
  - test all the changes in one pass, instead of testing small pieces in isolation;
  - write all the documentation in one pass or, when the scope is too large, in a few well-defined blocks.
- **Goal.** Reach the objectives with the **fewest possible tasks and iterations**, making the most of the internal resources available, so that deliveries are faster and cheaper for the user.
- **Quality constraint.** Grouping must make delivery faster **without lowering quality**. The result must be at least as good as executing the tasks one by one. Never group work when that would compromise quality.

## 6. Subagents

- **ALL** work on this project must be delegated to the subagent that is specialised in the objective of that work. **ALWAYS** choose the most appropriate subagent.
- Run **ONLY ONE** subagent at a time alongside the main Claude Code conversation. **NEVER** run more than one subagent in parallel.
- Use as many subagents as needed to reach the objective, but **in series**, never in parallel.
- **Exception:** the user may explicitly authorise more than one subagent in parallel. That authorisation covers only the current task and is **always revoked when the task ends**.
- Subagents contribute their specialty within the scope of the requested work (§4); a subagent's activation conditions never authorise unrequested work.

## 7. Skills

Use these Claude Code skills for the following needs:

- **`gitflow`** — **every** git write command, following the gitflow branching model adopted for this repository.
- **`roadmap-manager`** — coordination and management of tasks, sprints and comments, and **every** `rmp` command **except** `rmp graph *`.
- **`knowledge-authority`** — **exclusive** management of the project knowledge graph, including every `rmp graph *` command.

## 8. Planning and executing work

- Use `rmp` (through the `roadmap-manager` skill) to plan and coordinate task execution.
- Treat `rmp` as the **single source of truth** for the planning and execution of this project's tasks. No other mechanism may be used for this purpose.
- Use the **Knowledge Graph** to understand the project, its components and the relationships between them, so that the scope and impact of each task can be identified more easily.

### 8.1. Planning

- Analyse the scope of the work proposed by the user and decide whether it warrants being split into several development phases. Each phase must correspond to a solid deliverable.
- Plan with the **fewest possible tasks** (§5): merge work with verifiable functional or technical proximity instead of splitting it.
- Every task must have a clear and objective definition of:
  - objectives;
  - functional requirements;
  - technical requirements;
  - acceptance criteria (the conditions that confirm the task is complete).
- Phases map to **Sprints** in `rmp` and are used to group tasks.
- When the work requires several phases, planning happens in two distinct steps:
  1. define which phases (sprints) are needed and the scope/objective of each one;
  2. only then, sprint by sprint, define the tasks of each sprint.

  In both steps, use `rmp` as the single source of truth.
- Use the **Knowledge Graph** to identify the tasks with the greatest gain or impact, the foundational tasks, the ones that unblock other tasks or features, and the tasks that can be grouped into one effort, so that the execution order can be optimised.
- **Prioritisation:** by default, always work from the highest-gain/highest-impact tasks down to the least essential ones. Foundational tasks and tasks that unblock others are always a priority.
- When a task is too large to be executed in one go by an AI agent (such as Claude Code), subdivide it into parts, respecting the principles already defined (notably §2).

### 8.2. Task execution

Execution is the step that follows planning. Always use `rmp` and follow this sequence:

1. Check whether there is an open, unfinished task to carry on with.
2. Identify the next task, and every other task that can be grouped with it into a single effort (§5).
3. Understand the objective of each task about to start, based on its description and its functional and technical requirements.
4. Determine the most appropriate subagent and delegate execution to it (§6).
5. Always validate the acceptance criteria of every task before closing it.
6. Close each task with a short summary of what was done.
7. After closing the task (or the group of tasks executed as one effort), and before moving on, make a `git commit` through the `gitflow` skill, following best practice and explaining what was done and which tasks it closes.
8. Update the Knowledge Graph through the `knowledge-authority` skill.

Execution notes:

- Whenever possible, adapt the model and its effort level to the requirements of each individual operation within the task.
- Task and sprint execution is **sequential**. Grouping tasks into one effort (§5) is allowed and expected; running separate efforts in parallel is not.
- Evaluations and audits follow §6: one subagent at a time, unless the user explicitly authorises parallel execution for the current task.

## 9. Knowledge Graph

The KG (Knowledge Graph) is managed **exclusively** through the `knowledge-authority` skill.

- Use the "Graph" features of `rmp` (Groadmap) to create, maintain (update) and query a knowledge graph of the project.
- This graph **MUST CONTAIN EVERYTHING** worth knowing about the project. Examples:
  - which features exist, and where they are specified and implemented;
  - which tests exist and what they test;
  - which components exist, how they relate, and what the dependencies between them are;
  - in which `git commit` each feature was specified, implemented and tested;
  - the `rmp` tasks and their link to the components.
- The graph **MUST ALWAYS BE UPDATED on every `git commit`**, recording the changes to the graph's objects. Every node and edge update must identify the corresponding commit and its date.
- **This graph is the absolute truth about the project.** Keep it as up to date as possible, so that, before having to read files, you can query the graph and obtain what you need.
- Create the nodes and edges that make the most sense for the project. Use the graph together with the tasks and sprints to coordinate the work.

## 10. Never guess

- Every interaction on this project must be based **EXCLUSIVELY** on verified knowledge. Never try to guess the intended answer.
- When the available information is not sufficient, look for answers in official or authoritative sources: specifications, RFCs, papers, books, or reference authors in the field.
- Use the **Knowledge Graph** as the primary source of information — both to query and to record the relationships you discover.

## 11. Measure to decide

Whenever you need to assess performance, completeness (whether something is complete) or correctness (whether something is right), **ALWAYS** gather evidence from the project to determine the needs. **ALWAYS** decide empirically.

## 12. Regression prevention

Whenever you fix a bug, create the regression tests needed to guarantee that the same bug cannot reappear as a consequence of future development.

## 13. Language

### 13.1. Style

Write, and interpret, language that is always:

- **Explicit** — it is clear what is intended.
- **Objective** — it is always known what must be executed.
- **Closed** — it defines the scope of the work to be done.
- **Concise** — it uses few words to describe what is intended.

### 13.2. Documentation language

**All documentation** — from the main `README.md` to the specification, including code documentation and this `CLAUDE.md` — must be written in flawless English, in a professional tone, free of spelling, grammatical or syntactic errors, and must follow §13.1.

## 14. Decision framework

To decide what is expected as the outcome of the project — whether in evaluations and audits or during code implementation — follow this order of priority: **correct → secure → fast.**

1. **Is it correct?** Does the result meet the objective, the project specification, and the applicable authoritative sources (RFCs, standards, etc.)?
2. **Is it secure?** Does the decision or task avoid introducing any characteristic or behaviour that compromises the safe use of the deliverable?
3. **Is it fast?** Is it as fast as it can be without compromising correctness or security? What can be done to maximise the performance of the deliverable?

If these criteria conflict, or if you struggle to follow them, ask the user immediately how to proceed, presenting the possible options.

---

# Project reference

## Go version
**Go 1.26+** (`go.mod` declares `go 1.26`; the 2026-09-26 measurements used Go 1.27.0). Uses modern features:
- `for i := range n` (range over integer, Go 1.22+)
- `min`/`max` builtins (Go 1.21+)

## File layout

| File | Responsibility |
|---|---|
| `doc.go` | Package documentation |
| `mux.go` | `Mux` struct and options, `New`, `MethodQuery`, `Use`/`Pre`/`UseFast`, `Handle*` and per-method helpers, `Mount`, `ServeFiles`, `ServeHTTP`/`dispatch`, 405/OPTIONS/redirect handling, `Rebuild` |
| `tree.go` | Radix tree — `node`, `addRoute`, `getValue` (bounded backtracking), `findWildcard`, regex and optional parameters |
| `params.go` | `Param`/`Params` and typed helpers, tiered `requestCtx*`/`reqBundle*`, `reqBundle` and `fastParams` pools, `dispatchParams*`, `PathParam`, `ParamsFromContext`, `RoutePattern` |
| `group.go` | `Group` — prefix joining, group middleware, per-method helpers, `Group.Mount`, `Group.ServeFiles` |
| `handler.go` | `HandlerFuncE`, `HTTPError`, `Error`, `FastHandler`, `FastMiddleware` |
| `response.go` | Response helpers — `JSON`, `XML`, `Text`, `Redirect`, `NoContent` |
| `introspection.go` | `RouteInfo`, `Lookup`, `Routes`, `Walk`, `WalkFast` |
| `middleware/` | 17 middleware source files plus `doc.go`, exporting 21 constructors (`APIKey`, `BasicAuth`, `CleanPath`, `Compress`, `CORS`, `JWTAuth`, `Logger`, `NoCache`, `OAuth2Introspect`, `RealIP`, `Recoverer`, `RecovererWithLogger`, `RequestID`, `SetHeader`, `StripSlashes`, `ThrottleBacklog`, `ThrottleAllBacklog`, `ThrottlePerIP`, `ThrottlePerIPCapped`, `Timeout`, `WithValue`) |

**Tests and benchmarks:** 525 `func Test…` declarations in tracked `*_test.go` files outside `reports/` and `competitor/` — 256 in the root package, 269 in `middleware/` (counted with `git ls-files '*_test.go' | grep -v '^reports/\|^competitor/' | xargs grep -h '^func Test' | wc -l`). Benchmarks: 26 in `bench_test.go`, 9 in `middleware/bench_test.go`. Competitor benchmarks live in the separate `competitor/` module; audit harnesses under `reports/` belong to the root module.

## Public API

```go
mux := muxmaster.New()

// Middleware — Use and UseFast wrap only routes registered AFTER the call
mux.Pre(cleanPath)           // before routing; covers Handle and HandleFast routes
mux.Use(logger, auth)        // stdlib middleware; Handle routes only
mux.UseFast(fastLogger)      // FastMiddleware; HandleFast routes only

// Routes — ten methods: GET HEAD POST PUT PATCH DELETE OPTIONS CONNECT TRACE QUERY
mux.GET("/users", listUsers)
mux.GET("/users/:id", getUser)            // named parameter
mux.GET("/users/{id:[0-9]+}", getUser)    // regex parameter
mux.GET("/users{/:id}", getUser)          // optional parameter
mux.GET("/static/*filepath", files)       // catch-all
mux.QUERY("/search", search)              // RFC 10008; muxmaster.MethodQuery == "QUERY"
mux.ANY("/health", health)                // all ten methods
mux.GETE("/items/:id", getItemE)          // HandlerFuncE (error-returning)
mux.GETFast("/fast/:id", fastHandler)     // FastHandler: params as 3rd argument

// Groups, sub-groups, mounting, static files
api := mux.Group("/api/v1")
api.Use(apiKeyCheck)
api.POST("/items", createItem)
admin := api.Group("/admin")
mux.Mount("/v2", v2Mux)                                      // registered as "*" /v2/*mux_mount
mux.ServeFiles("/static/*filepath", http.Dir("./public"))

// Read parameters in the handler
id := muxmaster.PathParam(r, "id")
ps := muxmaster.ParamsFromContext(r.Context())
pattern := muxmaster.RoutePattern(r)  // "" for static routes (no context allocation)

// Introspection (registration read lock; never blocks dispatch)
h, params, found := mux.Lookup("GET", "/users/42") // h == nil for a HandleFast route
routes := mux.Routes()                             // includes fast routes and mount points
mux.Walk(fn)                                       // skips fast routes
mux.WalkFast(fastFn)                               // fast routes only

// Custom handlers
mux.NotFound = myHandler
mux.MethodNotAllowed = myHandler
mux.GlobalOPTIONS = myHandler
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) { ... }
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }

// Options and the defaults set by New()
mux.RedirectTrailingSlash  = true   // default: true
mux.RedirectFixedPath      = false  // default: false (security — canonicalisation can bypass middleware)
mux.HandleMethodNotAllowed = true   // default: true
mux.HandleOPTIONS          = true   // default: true (204 + Allow)
mux.CaseInsensitive        = false  // default: false
mux.UseRawPath             = false  // default: false
mux.UnescapePathValues     = false  // default: false (effective only with UseRawPath)
mux.RedirectCode           = 0      // default: 0 → 301 for GET/HEAD, 307 otherwise
mux.PoolFastParams         = false  // default: false (Opt O9: recycle FastHandler Params; handlers MUST NOT retain ps)
mux.PoolRequestBundle      = false  // default: false (Opt O13: recycle reqBundle; 0 allocs on Handle param routes; handlers MUST NOT retain *http.Request)
mux.Rebuild()                       // re-snapshot the options after the first request

http.ListenAndServe(":8080", mux)
```

## Key design decisions

### Middleware applied at registration, not per request
`wrapMiddleware` is invoked in `Handle()` at registration time. This means **zero per-request middleware overhead** — but `Use()` must be called before the routes it should wrap.

### Pre vs Use × Handle vs HandleFast — the policy matrix (CDX-S8-003)
| middleware family | wraps `Handle`? | wraps `HandleFast`? |
|---|---|---|
| `mux.Pre(...)` | YES | YES |
| `mux.Use(...)` (stdlib `http.Handler`) | YES | NO — panics at `HandleFast` registration (CSA-2026-0054) |
| `mux.UseFast(...)` (`FastMiddleware`) | NO | YES |

`Pre` runs OUTSIDE the dispatch (in `ServeHTTP` before tree lookup) and is the only middleware family that uniformly covers BOTH route types. Auth gates that must apply to fast routes MUST go through `Pre`, not `Use`. See SECURITY.md "Pre vs Use security boundary" / CDX-S8-003.

### Param accumulation — tiered reqBundle (1 alloc) + paramsBuf (stack)
- `paramsBuf` is a fixed-size struct allocated on the stack during `getValue`; static routes pass the original `r` to the handler and allocate nothing
- For routes with parameters: tiered bundles fuse `requestCtx` and the copy of `*http.Request` into a single allocation (1 alloc), sized to the parameter count:
  - 1 param → `reqBundle1` (368 B → size class 384 B): `requestCtx1` with `small [1]Param`
  - 2 params → `reqBundle2` (400 B → size class 416 B): `requestCtx2` with `small [2]Param`
  - 3+ params → `reqBundle` (456 B → size class 480 B): `requestCtx` with `small [3]Param` + heap overflow slice for >3
- `dispatch` calls `dispatchParams1`/`dispatchParams2` directly for 1/2 params (Opt O10) and `dispatchWithParams` for 3+; with `PoolRequestBundle` it calls the `dispatchParams1Pooled`/`dispatchParams2Pooled`/`dispatchParamsNPooled` variants instead
- `bundle.req.ctx` is set via `setReqCtxUnsafe` (`unsafe.Add` over the reflected offset of the private `ctx` field of `http.Request`) — safe because: (1) the bundle is not yet visible to any other goroutine; (2) the write happens-before any goroutine spawned in the handler (Go MM §goroutine creation); (3) the original `r` is NEVER modified; (4) by default there is no pool and the GC manages the lifetime — with `PoolRequestBundle` the bundle comes from a per-tier `sync.Pool` and is zeroed (`*b = reqBundle1{}`) before `Put`
- Automatic fallback to 2 allocs (`r.WithContext`) if the `ctx` field is not found via reflect (future Go version); the pooled path is then not used
- A request whose internal context is `nil` (struct literal) is dispatched with `context.Background()` as parent (rmp #292)

### Concurrency
- `treesPtr atomic.Pointer[methodTrees]` — lock-free read on every request via `.Load()`; copy-on-write under `mu` during registration
- `methodTrees` is a `[methodCount]*node` array (methodCount = 11: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY, plus the `"*"` slot used by `Mount`), indexed by constant, not a `map[string]*node`
- `sync.RWMutex mu` guards Use/Pre/Handle and introspection (`Lookup`, `Routes`, `Walk`, `WalkFast`). Request dispatch does not take it, except once per `Allow` value when a 405 or automatic-OPTIONS handler is first built and cached; redirects read an atomic middleware snapshot (`redirectMWPtr`, CH-06)
- Options are frozen into `muxConfig` on the first `ServeHTTP`; `Rebuild()` resets the snapshot atomically and is safe during serving
- Tree nodes are read-only after the initial registration — dynamic route registration after the server starts serving is not supported

### `Param` type vs `PathParam` function
Go does not allow a type and a function with the same name in the same package. Hence:
- Type: `muxmaster.Param{Key, Value}`
- Function: `muxmaster.PathParam(r, "name")`

### Bug fixed in the radix tree
The condition `c != ':' && c != '*' && n.nType != param` was incorrect — it prevented the creation of static children on `param` nodes (e.g. `/users/:id/posts`), corrupting the tree by overwriting `n.path`. Fixed to `c != ':' && c != '*' && c != '{'` (the extra `{` covers regex params `{name:expr}`).

## Useful commands

```bash
go test ./...                          # all tests (includes the audit harnesses under reports/)
go test -v ./...                       # verbose
go test -race ./...                    # race-condition detector
go vet ./...                           # static analysis
make bench                             # benchmarks of the root and middleware packages only
cd competitor && go test -mod=mod -run='^$' -bench=. -benchmem -count=3 .   # competitor suite
```

`go test -bench=. ./...` also runs every benchmark under `reports/`; use `make bench` for the product packages.

## Performance competitors to beat

The most well-known and widely adopted Go HTTP routers, in order of relevance as a performance target.

### Pure routers (performance-focused — direct competition)

| Module | Import path | Algorithm | GitHub stars | Notes |
|---|---|---|---|---|
| **httprouter** | `github.com/julienschmidt/httprouter` | Radix tree (per method) | ~16 000 | Historical performance reference; basis of Gin; ~0 allocs on static routes |
| **bunrouter** | `github.com/uptrace/bunrouter` | Zero-alloc radix tree | ~3 800 | Claims 0 allocs even with parameters; httprouter-compatible |
| **chi** | `github.com/go-chi/chi/v5` | Patricia radix trie | ~19 000 | 100% stdlib-compatible; very popular for modular APIs |
| **httptreemux** | `github.com/dimfeld/httptreemux/v5` | Radix tree | ~1 900 | Performance close to httprouter; supports case-insensitive |
| **bone** | `github.com/go-zoo/bone` | Tree-based | ~1 300 | ~118 ns/op reported; supports regex and wildcards |

### Frameworks with integrated routers (indirect competition)

| Framework | Import path | Internal router | GitHub stars | Notes |
|---|---|---|---|---|
| **Gin** | `github.com/gin-gonic/gin` | httprouter (fork) | ~81 000 | The most popular; uses the httprouter radix tree |
| **Echo** | `github.com/labstack/echo/v5` | Custom radix tree + sync.Pool | ~30 000 | Reported as the fastest in 2025 hello-world benchmarks |

### Alternative-stack frameworks (raw-routing comparison)

| Framework | Import path | HTTP stack | GitHub stars | Notes |
|---|---|---|---|---|
| **Fiber** | `github.com/gofiber/fiber/v3` | fasthttp (not net/http) | ~35 000 | Express-style API; incompatible with stdlib; raw-routing benchmarks are comparable but the stacks differ |

> **Note on Fiber:** the comparison with Fiber is for *raw routing throughput*, not full-stack. Fiber uses `fasthttp` which avoids `net/http` allocations — a structural advantage independent of the router. Benchmarks measure only the route-dispatch logic.

### Slower routers (not the target, but widely used)

| Module | Algorithm | Notes |
|---|---|---|
| **gorilla/mux** | Regex | ~21 000 ★; archived in 2022; ~3 444 278 ns/op on static routes |
| **net/http.ServeMux** | Linear (improved in Go 1.22+) | Stdlib; ~706 222 ns/op; sufficient for most cases |

### Known reference benchmarks (static routes, single lookup)

| Router | ns/op | allocs/op | Source |
|---|---|---|---|
| httprouter | ~15 010 | 0 | go-http-routing-benchmark |
| httptreemux | ~15 123 | 0 | go-http-routing-benchmark |
| bunrouter | < httprouter | 0 | bunrouter docs |
| chi | competitive | low | go-http-routing-benchmark |
| gorilla/mux | ~3 444 278 | 156 015 | go-http-routing-benchmark |
| net/http | ~706 222 | 96 | go-http-routing-benchmark |

> **Primary target:** match or surpass `httprouter` and `bunrouter` on ns/op and allocs/op while keeping an idiomatic API and 100% compatibility with `net/http`.

### Benchmark references
- https://github.com/julienschmidt/go-http-routing-benchmark
- https://github.com/smallnest/go-web-framework-benchmark

---

## Available subagents

Specialised agents are configured under `.claude/agents/`. Use them without waiting for the user to name them: delegating the execution of the requested work to the most appropriate subagent is mandatory (see "Working agreement" §6 and §8.2). The activation conditions below select **which** subagent handles work that is in scope; they never authorise starting unrequested work (§4). Subagents run one at a time, in series (§6).

### Performance agents

#### `go-perf-optimizer`
Specialty: measure, diagnose, and optimise the Go performance of this project (benchmarks, pprof, escape analysis, assembly).

**Activate automatically when:**
- Any hot-path file is modified: `mux.go`, `tree.go`, `params.go`
- `allocs/op` or `ns/op` increases on any benchmark after a change
- A new call to `context.WithValue`, `r.WithContext`, `sync.RWMutex`, or `sync.Pool` is added to the hot path
- The user asks for benchmarks, profiling, or performance analysis
- Before a release tag (full audit)
- A new code path is added to `ServeHTTP` or `getValue`

**Do not activate when:** the change is only in `group.go`, tests, documentation, or comments.

#### `benchmark-elite-tester`
Specialty: build and run competitor benchmarks (under `/competitor/<name>/`), analyse competitor source code, and produce evidence-backed objective comparisons.

**Activate automatically when:**
- The user asks *why* MuxMaster is slower than a specific competitor
- A direct comparison with httprouter, bunrouter, chi, Echo, or Gin is requested
- It is necessary to study how a competitor eliminates allocations (implementation technique)
- `go-perf-optimizer` has identified a performance gap and needs evidence on how competitors solve it
- A benchmark-environment setup for a new competitor is requested

**Do not activate when:** the question is only about MuxMaster code itself, with no external comparison.

### Security agents

9 agents cover the totality of attack vectors applicable to a Go HTTP router. They all produce reports under `/reports/<name>/` (and `/reports/overview/` for the threat-modeler). See `/reports/README.md` for the full activation matrix.

#### `http-protocol-security-auditor`
HTTP/1.1 + HTTP/2 framing, request smuggling (CL.TE/TE.CL/TE.TE), HPP, Rapid Reset (CVE-2023-44487), HPACK bombing, CONTINUATION flood, CRLF injection, response splitting, open redirect via RedirectTrailingSlash/RedirectFixedPath, method override abuse.
**Activate when:** `mux.go` / `response.go` / `handler.go` / header middlewares are modified; questions about protocol-level security; pre-release.

#### `path-routing-fuzzer`
Native Go fuzzing + differential testing against httprouter/chi/bunrouter on `tree.go` (getValue/addRoute/findWildcard). Covers path traversal, Unicode (NFC/NFKC/NFD), double/overlong encoding, null bytes, catch-all escape, wildcard shadow, case-folding bypass.
**Activate when:** `tree.go` is modified; new catch-all routes are registered; any question about routing bypass.

#### `dos-resilience-tester`
Algorithmic complexity of the radix tree, memory exhaustion, slowloris, compression bomb, throttle bypass, hash-flood, GC pressure. Empirically measures complexity slopes and resource profile under sustained load.
**Activate when:** new `throttle` / `timeout` / `compress` middlewares are added; questions about worst-case behaviour; pre-release.

#### `concurrency-security-auditor`
Data races (-race stress), `sync.Pool` contamination (canary tests), TOCTOU during registration, goroutine leaks, panic recovery + pool cleanliness, context propagation, introspection thread-safety.
**Activate when:** `sync.Pool` or `RWMutex` is touched; dynamic registration is considered; `recoverer` is modified; `-race` reports.

#### `middleware-security-reviewer`
Audits the 13 middlewares individually with their own threat model: basic_auth (timing, constant-time), cors (wildcard+credentials, reflection), compress (BREACH/gzip bomb), real_ip (XFF trust), recoverer (info leak), throttle, timeout, logger (CRLF/secret leak), request_id, clean_path, strip_slashes, with_value, set_header.
**Activate when:** any file under `/middleware/` is modified or added.

#### `go-sast-and-memory-auditor`
SAST: `gosec`, `staticcheck`, `golangci-lint`, `govulncheck`, `semgrep`, `CodeQL`, `errcheck`, `ineffassign`. Supply chain: `osv-scanner`, SBOM (CycloneDX), licences. Memory safety: escape analysis, `unsafe` detection, type-assertion audit.
**Activate when:** any Go file is added/changed; in CI; pre-release.

#### `timing-and-sidechannel-analyst`
Statistical validation of constant-time (Welch t-test, KS, Mann-Whitney U) with N ≥ 1e6 samples. Detection of error oracles, route-existence leaks, PRNG audit (`math/rand` vs `crypto/rand`).
**Activate when:** auth primitives (basic_auth, HMAC, signatures) are changed; `crypto/subtle` is no longer used; questions about "does this leak information?".

#### `fuzzing-and-property-engineer`
Native fuzzing of the entire public API (`Mux.Handle`, `Mux.Group`, `Params`, individual middlewares) and property tests with `pgregory.net/rapid`. Maintains `invariants.md` and a continuous corpus. Complements `path-routing-fuzzer` by covering the rest of the module.
**Activate when:** the public API gains a new exported symbol; nightly / pre-release; requires fuzz ≥ 30 s per target in short mode.

#### `threat-modeler-and-zero-day-researcher` (orchestrator)
STRIDE, attack trees, cross-ecosystem transposition (nginx / Rails / Express / Spring / Traefik / Caddy → Go), zero-day hypothesis generation by combining findings. Sole author of `/reports/overview/`. Coordinates the 8 specialists.
**Activate when:** a full audit is requested; pre-release; new architectural component; two agents produce findings that may combine.

### Coordination between agents

**Performance flow:**
1. `go-perf-optimizer` → identifies an opportunity
2. `benchmark-elite-tester` → competitor evidence
3. `go-perf-optimizer` → implements and validates

**Security flow (sprint):**
1. `threat-modeler` → writes `/reports/overview/<date>-sprint.md`
2. The 8 specialists run one at a time, in series (in parallel only with explicit user authorisation for the current task — §6), and write to `/reports/<agent>/`
3. `threat-modeler` consolidates into `/reports/overview/<date>-posture.md` + updates `findings.md`, `threat-model.md`, `hypotheses.md`

**Escalation paths (security):**
- CRLF detected by any agent → `http-protocol-security-auditor`
- Race detected by any agent → `concurrency-security-auditor`
- Suspected timing leak → `timing-and-sidechannel-analyst`
- Routing bypass → `path-routing-fuzzer`
- Combination of findings → `threat-modeler-and-zero-day-researcher`

**Parallel execution:** only one subagent runs at a time. Evaluations and audits may run in parallel only when the user explicitly authorises it; that authorisation covers only the current task and is revoked when the task ends (see "Working agreement" §6). Task and sprint execution is always sequential. The `threat-modeler` runs before (planning) and after (consolidation) any batch of specialist runs.

---

## Performance baseline (HEAD, measured 2026-09-26)

Source: `reports/perf-lab-2026-09-26-docs/` (rmp #296). One host: AMD Ryzen 9 5900HX (8 cores / 16 threads), Linux 6.8, Go 1.27.0, `-count=3`, CPU governor `powersave`. At n=3, `benchstat` cannot test significance (it needs n≥4) or give confidence intervals (n≥6): treat ns/op differences below ~10% as noise.

### v1.1.0 → HEAD
- **Allocations identical** on all 18 shared root benchmarks (B/op and allocs/op equal in every sample of both versions).
- **ns/op: no statistically significant change** — every row is "~". v1.1.0's `ParamRoute2`/`ParamRoute3` had 2 of 3 samples at ~1.5–1.7 µs (host perturbation, ~10× every other sample); this corrupts that table's geomean line, which must be ignored.

### Internal benchmarks at HEAD (`bench_test.go`)

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static route | 28.63 | 0 | 0 |
| 1 parameter | 118.5 | 384 | 1 |
| 2 parameters | 130.1 | 416 | 1 |
| 3 parameters | 149.6 | 480 | 1 |
| Catch-all | 132.4 | 384 | 1 |
| Not found | 208.0 | 91 | 3 |
| Parallel static | 4.249 | 0 | 0 |
| Parallel 1 parameter | 104.5 | 384 | 1 |
| **Pooled** 1 parameter | **48.51** | **0** | **0** |
| **Pooled** 2 parameters | **59.76** | **0** | **0** |
| **Pooled** 3 parameters | **61.76** | **0** | **0** |
| **Pooled** catch-all | **44.95** | **0** | **0** |
| **Pooled** parallel param | **6.901** | **0** | **0** |
| Fast static | 28.91 | 0 | 0 |
| Fast 1 parameter | 46.15 | 32 | 1 |
| Fast 2 parameters | 68.96 | 64 | 1 |
| Fast 3 parameters | 79.72 | 96 | 1 |
| Fast parallel param | 16.31 | 32 | 1 |
| Mount, static prefix | 376.6 | 864 | 2 |
| Mount, param prefix | 519.6 | 1 219 | 3 |
| ServeFiles | 808.1 | 709 | 8 |
| RegisterRoutes N=5000 | 4.70 ms (939.6 ns/route) | 6.08 MiB | 75 940 |

`AdversarialBacktracking` (depth 1→128) and `QuadraticBacktracking` (2→64) grow linearly with depth. No pooled fast-route (`PoolFastParams`) benchmark exists in `bench_test.go`.

### Middleware at HEAD (`middleware/bench_test.go`; no v1.1.0 baseline exists)

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| ThrottlePerIP, one client | 115.0 | 0 | 0 |
| ThrottlePerIP, many clients | 127.2 | 0 | 0 |
| Logger | 5 671 | 0 | 0 |
| Recoverer, no panic | 19.99 | 0 | 0 |
| Compress, 600 B | 316.6 | 32 | 2 |
| Compress, chunked 12 KiB | 7 765 | 58 | 3 |
| RealIP, 1 hop / 3 hops | 143.6 / 178.7 | 16 | 1 |
| APIKey, hit | 453.3 | 416 | 6 |
| BasicAuth hit, 1 / 10 / 100 users | 319.5 / 549.7 / 2 836 | 32 | 2 |
| BasicAuth miss, 1 / 10 / 100 users | 733.3 / 958.8 / 3 232 | 168 | 8 |
| JWTAuth HS256 | 4 354 | 738 | 7 |
| OAuth2 cache set at saturation | 3 194 | 288 | 2 |

`RequestID` and `CORS` have no benchmark in this suite and were not part of the report's middleware table.

### Competitor suite at HEAD (`competitor/bench_test.go`) — ns/op (allocs/op)

| Router | Static | Param×1 | Param×2 | Param×3 | Catch-all | Not found | Parallel static | Parallel param |
|---|---|---|---|---|---|---|---|---|
| **MuxMaster default** | 29.6 (0) | 116.9 (1) | 131.9 (1) | 144.4 (1) | 116.4 (1) | 254.7 (3) | 4.51 (0) | 102.3 (1) |
| **MuxMaster Pooled** | 30.7 (0) | 46.7 (0) | 60.9 (0) | 67.6 (0) | 46.5 (0) | — | —¹ | 7.19 (0) |
| **MuxMaster Fast** | 30.6 (0) | 47.8 (1) | 67.8 (1) | 83.0 (1) | 48.6 (1) | — | 4.55 (0) | 17.2 (1) |
| httprouter | 34.7 (0) | 50.5 (1) | 60.2 (1) | 74.4 (1) | 42.8 (1) | 398.2 (3) | 4.91 (0) | 21.8 (1) |
| bunrouter (`http.Handler` adapter)² | 168.1 (3) | 160.3 (3) | 181.6 (3) | 179.9 (3) | 152.5 (3) | 275.5 (4) | 126.1 (3) | 126.1 (3) |
| chi v5 | 217.9 (2) | 360.7 (4) | 405.0 (4) | 415.5 (4) | 334.2 (4) | 346.5 (5) | 133.8 (2) | 232.3 (4) |
| gorilla/mux | 578.9 (7) | 954.7 (8) | 1 497 (8) | 1 729 (8) | 1 611 (8) | 1 034 (4) | 345.6 (7) | 456.8 (8) |

¹ Not measured; static routes never allocate a bundle, so pooling does not change them.
² Vendored `github.com/uptrace/bunrouter v1.0.23` through `bunrouter.HTTPHandlerFunc` (params via `context.WithValue`). Its native API was not measured in this run.
Not-found allocs for bunrouter (4) and chi (5) are taken from the raw `competitor-head.txt`; the report README's §3 table lists 3 and 4.

Interpretation:
- MuxMaster is the fastest of the measured routers on static, not-found and parallel-static routes.
- Default mode trails httprouter ~2–2.7× on serial param routes (4.7× parallel): it copies `*http.Request` into a 384–480 B GC-managed bundle, so handlers may retain `r`. httprouter allocates only its `Params` slice (32–96 B) and uses a 3-argument handler.
- Pooled mode is 0 allocs on every param route; vs httprouter it is faster on Param×1, Param×3 and parallel param, level on Param×2, slower on catch-all.

### Sprint 18 waste-hunt claims — reproduced on 2026-09-26 (report §4)
All 18 verifiable claims reproduced (none regressed): registration O(depth) (N=5000: 2.86 s → 4.70 ms); Mount 1 024 → 237.5 ns, 9 → 2 allocs; CleanPath dirty 810 → 145.4 ns, 7 → 2 allocs; redirect 884 → 641.3 ns, and with 5 middleware 1 050 → 760.7 ns, 19 → 11 allocs; ThrottlePerIP 4 051 → 115 ns, 5 → 0 allocs; Logger 7.5 → 5.67 µs, 0 allocs; Compress 600 B 435 → 316.6 ns, chunked 14.6 → 7.77 µs; JWTAuth 5.16 → 4.35 µs, 7 allocs; RealIP 3-hop 257 → 178.7 ns; APIKey 574 → 453.3 ns; 405 153 → 109.6 ns, 1 alloc; auto-OPTIONS 124 → 61.9 ns, 1 alloc; `Text` 98 → 60.4 ns, 1 alloc. No benchmark exists for `StripSlashes` (WH-04); `ServeFiles` has no quoted before/after pair.

### Sprint 18 contention hunt — measured 2026-09-24, NOT re-measured on 2026-09-26
Source: `reports/perf-lab-2026-09-24/contention-hunt.md` and the CHANGELOG. ThrottlePerIP 64-way sharding 2 114 → 451 ns at 16 CPUs (4.68×); ThrottleBacklog CAS fast path −42% at 1 CPU (39.12 → 22.76 ns), −13% at 16 CPUs (76.15 → 66.23 ns); RequestID 7 → 2 allocs, ~4.7×/2.8×/1.95× at 1/4/16 CPUs; OAuth2Introspect eviction 257.4 µs → 2.15 µs at 16 CPUs; redirect path reads a lock-free `Use()` snapshot (no measurable ns/op change).

### Costs added by security fixes (sprint 20)
- BasicAuth constant-time scan over all users (rmp #290, TSC-2026-0002): O(users) — hit ≈ 320 ns / 550 ns / 2.8 µs for 1 / 10 / 100 users (2026-09-26).
- CORS `Vary: Origin` on every response (rmp #291): no-`Origin` path 25.7 → 71.0 ns, 0 → 1 alloc (`-count=10`, measured when the fix landed).
- Recoverer response-state tracking (rmp #276): no-panic path 8.4 → 19.4 ns, 0 allocs (`-count=10`, measured when the fix landed).

### Optimisation history (v1.1.0)
- **O10**: removed the `doDispatch1`/`doDispatch2` function pointers; direct calls (−3% on param routes, measured 2026-05).
- **O12**: `requestCtx1`/`requestCtx2` dropped their `params Params` field (slice derived from `small[:N]`); `reqBundle1` 416 → 384 B, `reqBundle2` 448 → 416 B size class.
- **O13**: `PoolRequestBundle` recycles the bundle via `sync.Pool` — 0 allocs on param routes (1 param at HEAD: 118.5 → 48.51 ns). Handlers MUST NOT retain `*http.Request` past return.
- **O9**: `PoolFastParams` recycles the `FastHandler` `Params` slice. Handlers MUST NOT retain `ps` past return.

---

### Historical: Apple M4 — 10 cores (4P+6E), 32 GB, Go 1.26.2, macOS arm64 (2026-05-12, v1.1.0-era code)

Not re-measured on current code. Source: `reports/apple-m4-benchmarks-2026-05-12.md`.

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static route | 14.1 | 0 | 0 |
| 1 parameter | 56.7 | 384 | 1 |
| Parallel static | 2.4 | 0 | 0 |
| **Pooled** 1 parameter | **28.4** | **0** | **0** |

Competitor suite on that date (bunrouter measured through its **native** API, which is not `net/http`-compatible):

| Case | MuxMaster Pooled | httprouter | bunrouter (native) |
|---|---|---|---|
| Static | 14 ns, 0 allocs | 14.7 ns, 0 allocs | 18.6 ns, 0 allocs |
| 1 parameter | 28 ns, 0 allocs | 33.0 ns, 1 alloc | 21.9 ns, 0 allocs |

---

## Development principles — Maximum performance

> Performance work is subordinate to the decision framework in "Working agreement" §14: **correct → secure → fast**. Never trade correctness or security for speed; when they conflict, ask the user.

### Mandatory multi-disciplinary approach
Before implementing any solution, you must consider **every possible approach** — alternative data structures, algorithms from other languages (C, C++, Rust, Zig, Java, etc.), operating-system techniques, and hardware patterns — and implement the most efficient one in idiomatic Go. Do not limit yourself to what is common in Go; draw inspiration from the best of every language and translate it to Go.

### Implementation selection criteria
1. **Measure first** — never optimise without a benchmark before and after (`benchstat`)
2. **Lower ns/op and allocs/op** — these are the primary success metrics
3. **Zero allocations on the hot path** is the goal; any allocation must be justified
4. **Idiomatic Go** — every implementation must respect Go principles: simplicity, readability, correct use of goroutines/channels/sync, and 100% compatibility with `net/http`

### Active use of specialised subagents
The subagents are not optional — they are part of the process. Within the scope of the requested work (§4), delegate to them without waiting for the user to ask whenever the conditions described in the "Available subagents" section are met, one at a time (§6). The goal is maximum performance, and the specialised agents are the mechanism for achieving it with rigour and evidence.
