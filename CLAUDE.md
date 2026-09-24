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

These rules govern **every** interaction with this project. They take precedence over any default behaviour.

## 1. Core rules

1. **You are NOT authorised to take decisions on your own.** Whenever the instructions are insufficient, unclear, unspecific or not concrete, or whenever contradictions or ambiguities exist, you **MUST ALWAYS ASK** the user how to proceed.
   - When asking, always offer multiple options (a, b, c, …) and state which one you recommend.
   - When several questions need clarification, ask them **one at a time** (sequentially), never all at once.
   - **The boundary between acting and asking:** obvious, low-risk corrections (for example, a pre-existing bug with an unambiguous fix) proceed immediately; any decision that changes the scope, the expected behaviour, the architecture or the requirements demands that you ask the user first.
2. **Documentation in English.** All project documentation (including this `CLAUDE.md`) must be written in the most correct English possible, free of spelling, grammatical or syntactic errors. Use clear, simple, unambiguous technical language, written for human readers.
3. **Documentation faithful to the code.** Documentation must be accurate and must always reflect the real state of the code.
4. **Workflow.** Work always follows this order: **Specify → Implement → Test → Document.**

## 2. Self-contained development policy

- Every development cycle must be **self-contained**: each cycle produces a complete, usable result. You must **NEVER** deliver only part of a task.
- When new, unforeseen needs arise during a task, they must be resolved within the same development cycle, as quickly as possible (by creating new tasks and executing them straight afterwards).
- All code must be **full-fledged** (complete and ready to use). Tests with `skip` must **NOT** be created.
- Whenever you find a pre-existing bug, fix it on the spot and then resume the work you were doing when you found it.

## 3. Production-grade by default

**EVERY** action you take — development, fixes, evaluations, analyses, audits, and anything else — must be treated with the rigour expected of **production** work.

## 4. Planning and executing work

- For any operation involving Tasks or Sprints you must use the `roadmap-manager` skill.
- Use the `rmp` tool (the roadmap-management CLI available on the system) to plan and coordinate task execution.
- Treat `rmp` as the **single source of truth** for the planning and execution of this project's tasks. No other mechanism may be used for this purpose.
- Use the **Knowledge Graph** to understand the project, its components and the relationships between them, so that the scope and impact of each task can be identified more easily.

### 4.1. Planning

- Analyse the scope of the work proposed by the user and decide whether it warrants being split into several development phases. Each phase must correspond to a solid deliverable.
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
- Use the **Knowledge Graph** to identify the tasks with the greatest gain or impact, the foundational tasks, and the ones that unblock other tasks or features, so that the execution order can be optimised.
- **Prioritisation:** by default, always work from the highest-gain/highest-impact tasks down to the least essential ones. Foundational tasks and tasks that unblock others are always a priority.
- When a task is too large to be executed in one go by an AI agent (such as Claude Code), subdivide it into parts, respecting the principles already defined (notably that of the self-contained task).

### 4.2. Task execution

Execution is the step that follows planning. Always use `rmp` and follow this sequence:

1. Check whether there is an open, unfinished task to carry on with.
2. Identify the next task.
3. Understand the objective of the task about to start, based on its description and its functional and technical requirements.
4. Determine the most appropriate subagent and delegate execution to it.
5. Always validate the acceptance criteria before closing the task.
6. Close the task with a short summary of what was done.
7. After closing the task, and before moving on to the next one, make a `git commit` following best practice, explaining what was done.
8. Update the Knowledge Graph.

Execution notes:

- Whenever possible, adapt the model and its effort level to the requirements of each individual operation within the task.
- Task and sprint execution is **sequential**.
- Evaluations and audits may run in parallel, but such parallel execution must **ALWAYS be authorised by the user**.

## 5. Knowledge Graph

The KG (Knowledge Graph) must be managed with the help of the `knowledge-authority` skill.

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

## 6. Never guess

- Every interaction on this project must be based **EXCLUSIVELY** on verified knowledge. Never try to guess the intended answer.
- When the available information is not sufficient, look for answers in official or authoritative sources: specifications, RFCs, papers, books, or reference authors in the field.
- Use the **Knowledge Graph** as the primary source of information — both to query and to record the relationships you discover.

## 7. Measure to decide

Whenever you need to assess performance, completeness (whether something is complete) or correctness (whether something is right), **ALWAYS** gather evidence from the project to determine the needs. **ALWAYS** decide empirically.

## 8. Regression prevention

Whenever you identify a bug, create the regression tests needed to guarantee that the same bug cannot reappear as a consequence of future development.

## 9. Your team (subagents)

- You have at your disposal a team made up of every available subagent (global, user-level or project-level).
- Use them collaboratively and complementarily, so that each task is completed with the greatest possible confidence, effectiveness and assertiveness.
- Each subagent must contribute proactively with its own specialty.

## 10. Decision framework

To decide what is expected as the outcome of the project — whether in evaluations and audits or during code implementation — follow this order of priority: **correct → secure → fast.**

1. **Is it correct?** Does the result meet the objective, the project specification, and the applicable authoritative sources (RFCs, standards, etc.)?
2. **Is it secure?** Does the decision or task avoid introducing any characteristic or behaviour that compromises the safe use of the deliverable?
3. **Is it fast?** Is it as fast as it can be without compromising correctness or security? What can be done to maximise the performance of the deliverable?

If these criteria conflict, or if you struggle to follow them, ask the user immediately how to proceed, presenting the possible options.

---

# Project reference

## Go version
**Go 1.26+** (`go.mod` declares `go 1.26`). Uses modern features:
- `for i := range n` (range over integer, Go 1.22+)
- `min`/`max` builtins (Go 1.21+)

## File layout

| File | Responsibility |
|---|---|
| `mux.go` | `Mux` struct, `ServeHTTP`, HTTP methods, global middleware |
| `tree.go` | Radix tree — `node`, `addRoute`, `getValue`, `findWildcard` |
| `params.go` | `Params` type, `sync.Pool`, `PathParam()`, `ParamsFromContext()` |
| `group.go` | `Group` — path prefix + group middleware |
| `mux_test.go` | 17 unit tests |
| `bench_test.go` | 8 benchmarks including concurrent scenarios |

## Public API

```go
mux := muxmaster.New()

// Middleware (must be registered BEFORE the routes it should wrap)
mux.Use(logger, auth)

// Routes
mux.GET("/users", listUsers)
mux.GET("/users/:id", getUser)       // path parameter
mux.GET("/static/*filepath", files)  // catch-all

// Groups
api := mux.Group("/api/v1")
api.Use(apiKeyCheck)
api.POST("/items", createItem)

// Sub-groups
admin := api.Group("/admin")

// Read parameters in the handler
id := muxmaster.PathParam(r, "id")
ps := muxmaster.ParamsFromContext(r.Context())

// Custom handlers
mux.NotFound = myHandler
mux.MethodNotAllowed = myHandler
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }

// Options and their defaults
mux.RedirectTrailingSlash  = true   // default: true
mux.RedirectFixedPath      = false  // default: false (security — canonicalisation can bypass middleware)
mux.HandleMethodNotAllowed = true   // default: true
mux.HandleOPTIONS          = true   // default: true
mux.PoolFastParams         = false  // default: false (opt-in: recycle FastHandler Params via sync.Pool)
mux.PoolRequestBundle      = false  // default: false (opt-in Opt O13: recycle the per-request reqBundle — −53% ns/op, ZERO alloc on http.Handler param routes; strict lifetime contract: handlers MUST NOT retain *http.Request past return)

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
- `paramsBuf` is a fixed-size struct allocated on the stack during `getValue` — no `sync.Pool`, no heap escape for static routes
- For routes with parameters: tiered bundles fuse `requestCtx` and the copy of `*http.Request` into a single allocation (1 alloc), sized to the parameter count:
  - 1 param → `reqBundle1` (392 B → size class 416 B): `requestCtx1` with `small [1]Param`
  - 2 params → `reqBundle2` (424 B → size class 448 B): `requestCtx2` with `small [2]Param`
  - 3+ params → `reqBundle` (456 B → size class 480 B): `requestCtx` with `small [3]Param` + heap overflow for >3
- Dispatch is performed by `dispatchParams1`/`dispatchParams2` for 1/2 params, and inlined for 3+ (avoids call overhead in the most common REST-API case)
- `bundle.req.ctx` is set via `setReqCtxUnsafe` (`unsafe.Add` over the reflected offset of the private `ctx` field of `http.Request`) — safe because: (1) the bundle is freshly allocated, no concurrent access yet; (2) the write happens-before any goroutine spawned in the handler (Go MM §goroutine creation); (3) the original `r` is NEVER modified; (4) no pool — lifetime managed by the GC
- Automatic fallback to 2 allocs (`r.WithContext`) if the `ctx` field is not found via reflect (future Go version)

### Concurrency
- `treesPtr atomic.Pointer[methodTrees]` — lock-free read on every request via `.Load()`; copy-on-write under `mu` during registration
- `methodTrees` is a `[methodCount]*node` array indexed by constant (0–9), not a `map[string]*node`
- `sync.RWMutex mu` protects Use/Pre/Handle and introspection; not needed for reads in `ServeHTTP`
- Tree nodes are read-only after the initial registration — dynamic route registration after the server starts serving is not supported

### `Param` type vs `PathParam` function
Go does not allow a type and a function with the same name in the same package. Hence:
- Type: `muxmaster.Param{Key, Value}`
- Function: `muxmaster.PathParam(r, "name")`

### Bug fixed in the radix tree
The condition `c != ':' && c != '*' && n.nType != param` was incorrect — it prevented the creation of static children on `param` nodes (e.g. `/users/:id/posts`), corrupting the tree by overwriting `n.path`. Fixed to `c != ':' && c != '*' && c != '{'` (the extra `{` covers regex params `{name:expr}`).

## Useful commands

```bash
go test ./...                          # all tests
go test -v ./...                       # verbose
go test -bench=. -benchmem ./...       # benchmarks with allocations
go test -race ./...                    # race-condition detector
go vet ./...                           # static analysis
```

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

Specialised agents are configured under `.claude/agents/`. Use them proactively — do not wait for the user to ask for them explicitly. Delegating the execution of a task to the most appropriate subagent is a mandatory step of the execution sequence (see "Working agreement" §4.2).

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
2. The 8 specialists run (in parallel only with prior user authorisation) and write to `/reports/<agent>/`
3. `threat-modeler` consolidates into `/reports/overview/<date>-posture.md` + updates `findings.md`, `threat-model.md`, `hypotheses.md`

**Escalation paths (security):**
- CRLF detected by any agent → `http-protocol-security-auditor`
- Race detected by any agent → `concurrency-security-auditor`
- Suspected timing leak → `timing-and-sidechannel-analyst`
- Routing bypass → `path-routing-fuzzer`
- Combination of findings → `threat-modeler-and-zero-day-researcher`

**Parallel execution:** evaluations and audits may run in parallel when the tasks are independent, but **parallel execution must always be authorised by the user first** (see "Working agreement" §4.2). Task and sprint execution is always sequential. The `threat-modeler` runs before (planning) and after (consolidation) any batch of specialist runs.

---

## Performance baseline (HEAD — Opt O10/O12/O13)

### Apple M4 — 10 cores (4P+6E), 32 GB, Go 1.26.2, macOS arm64 (2026-05-12)

Internal benchmarks (`bench_test.go`):

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static route | 14.1 | 0 | 0 |
| 1 parameter | 56.7 | 384 | 1 |
| 2 parameters | 64.2 | 416 | 1 |
| 3 parameters | 69.7 | 480 | 1 |
| Catch-all | 58.2 | 384 | 1 |
| Parallel static | 2.4 | 0 | 0 |
| Parallel 1 parameter | 82.2 | 384 | 1 |
| **Pooled** 1 parameter | **28.4** | **0** | **0** |
| **Pooled** 2 parameters | **36.4** | **0** | **0** |
| **Pooled** 3 parameters | **38.6** | **0** | **0** |
| **Pooled** catch-all | **28.5** | **0** | **0** |
| **Pooled** parallel param | **10.2** | **0** | **0** |
| Fast static | 14.2 | 0 | 0 |
| Fast 1 parameter | 28.4 | 32 | 1 |
| Fast 2 parameters | 36.9 | 64 | 1 |
| Fast 3 parameters | 45.9 | 96 | 1 |
| Fast parallel param | 13.1 | 32 | 1 |

Competitive benchmarks (`competitor/bench_test.go`, Apple M4, bunrouter using **native API**):

| Case | MuxMaster Pooled¹ | MuxMaster default | httprouter | bunrouter² | chi v5 |
|---|---|---|---|---|---|
| Static | **14 ns, 0 allocs** | 14 ns, 0 allocs | 14.7 ns, 0 allocs | 18.6 ns, 0 allocs | 114 ns, 2 allocs |
| 1 parameter | **28 ns, 0 allocs** | 61.7 ns, 1 alloc | 33.0 ns, 1 alloc | 21.9 ns, 0 allocs | 196 ns, 4 allocs |
| 2 parameters | **36 ns, 0 allocs** | 71.1 ns, 1 alloc | 39.6 ns, 1 alloc | 40.7 ns, 0 allocs | 226 ns, 4 allocs |
| 3 parameters | **39 ns, 0 allocs** | 83.2 ns, 1 alloc | 45.1 ns, 1 alloc | 29.3 ns, 0 allocs | 225 ns, 4 allocs |
| Catch-all | **29 ns, 0 allocs** | 57.4 ns, 1 alloc | 27.3 ns, 1 alloc | 11.5 ns, 0 allocs | 175 ns, 4 allocs |
| Parallel static | **1.86 ns, 0 allocs** | 1.86 ns, 0 allocs | 2.35 ns, 0 allocs | 2.12 ns, 0 allocs | 120 ns, 2 allocs |
| Parallel param | **10.2 ns, 0 allocs** | 112 ns, 1 alloc | 18.9 ns, 1 alloc | 3.97 ns, 0 allocs | 188 ns, 4 allocs |

¹ MuxMaster Pooled = `Mux.PoolRequestBundle = true` (Opt O13 opt-in) — pooled numbers from internal bench_test.go; competitor suite tests default mode only
² bunrouter uses native `bunrouter.HandlerFunc` API — not `net/http`-compatible; `http.Handler` adapter adds ~3 allocs (~180 ns/op)

---

### AMD Ryzen 9 5900HX — 16 cores, Go 1.26.2, Linux 6.8 (2026-05-08)

Internal benchmarks (`bench_test.go`):

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static route | 25.1 | 0 | 0 |
| 1 parameter | 105 | 384 | 1 |
| 2 parameters | 119 | 416 | 1 |
| 3 parameters | 135 | 480 | 1 |
| Catch-all | 108 | 384 | 1 |
| Parallel static | 3.6 | 0 | 0 |
| Parallel 1 parameter | 100 | 384 | 1 |
| **Pooled** 1 parameter | **49.6** | **0** | **0** |
| **Pooled** 2 parameters | **55.9** | **0** | **0** |
| **Pooled** 3 parameters | **58.6** | **0** | **0** |
| **Pooled** catch-all | **43.9** | **0** | **0** |
| **Pooled** parallel param | **6.3** | **0** | **0** |
| Fast static | 25.5 | 0 | 0 |
| Fast 1 parameter | 50.3 | 32 | 1 |
| Fast 2 parameters | 67.9 | 64 | 1 |
| Fast 3 parameters | 76.9 | 96 | 1 |
| Fast parallel param | 16.5 | 32 | 1 |

> Opt O10: eliminating the `doDispatch1`/`doDispatch2` function pointers gave −3% on all param routes. The indirect CALL was replaced by a direct call carrying the `if hasReqCtxField` branch internally.
> Opt O12: `requestCtx1`/`requestCtx2` dropped their `params Params` field — the slice header is derived from `small[:N]` on access. reqBundle1: 416→384 B, reqBundle2: 448→416 B. +4–9% ns/op gain via smaller GC size class.
> Opt O13 (opt-in via `Mux.PoolRequestBundle`): recycles `reqBundle` via `sync.Pool` — **−53% ns/op, −100% B/op (zero allocations)** on 1-param routes. Brings MuxMaster's stdlib `http.Handler` path on-par with the FastHandler one. Strict lifetime contract: handlers MUST NOT retain `*http.Request` past return. Failing this contract triggers use-after-free against the recycled bundle storage.

Interpretation notes (both platforms):
- **MuxMaster Pooled** is the fastest stdlib-compatible `http.Handler` HTTP router measured: 0 allocs on all param routes. On the M4, **faster than httprouter** (28 ns vs 33 ns, 1-param). Requires handler lifetime audit.
- **MuxMaster default** is the maximum-safety design: tiered reqBundle (384/416/480 B for 1/2/3 params) fuses requestCtx + *http.Request into a single allocation. GC-managed lifetime — handlers may retain `r` freely.
- **MuxMaster Fast** (`HandleFast`) bypasses the context overhead by passing `Params` as a 3rd argument — same model as httprouter's API but with stdlib `http.Handler` signature for static routes.
- **httprouter** uses a different API (3rd argument `Params`), not native `http.Handler`; its 1 alloc is just the 64 B `Params` slice, not a full copy of `*http.Request`.
- **bunrouter native** achieves 0 allocs via its own `bunrouter.HandlerFunc` signature — not `net/http`-compatible; the `http.Handler` adapter brings it to ~3 allocs.
- **MuxMaster parallel static** is the fastest of all tested routers on the M4 (1.86 ns vs httprouter 2.35 ns, bunrouter 2.12 ns) — the lock-free `atomic.Pointer` read path has zero contention.

---

## Development principles — Maximum performance

> Performance work is subordinate to the decision framework in "Working agreement" §10: **correct → secure → fast**. Never trade correctness or security for speed; when they conflict, ask the user.

### Mandatory multi-disciplinary approach
Before implementing any solution, you must consider **every possible approach** — alternative data structures, algorithms from other languages (C, C++, Rust, Zig, Java, etc.), operating-system techniques, and hardware patterns — and implement the most efficient one in idiomatic Go. Do not limit yourself to what is common in Go; draw inspiration from the best of every language and translate it to Go.

### Implementation selection criteria
1. **Measure first** — never optimise without a benchmark before and after (`benchstat`)
2. **Lower ns/op and allocs/op** — these are the primary success metrics
3. **Zero allocations on the hot path** is the goal; any allocation must be justified
4. **Idiomatic Go** — every implementation must respect Go principles: simplicity, readability, correct use of goroutines/channels/sync, and 100% compatibility with `net/http`

### Active use of specialised subagents
The subagents are not optional — they are part of the process. Activate them proactively (without waiting for the user to ask) whenever the conditions described in the "Available subagents" section are met. The goal is maximum performance, and the specialised agents are the mechanism for achieving it with rigour and evidence.
