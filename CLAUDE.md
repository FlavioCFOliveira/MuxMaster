# MuxMaster — Guide for Claude

## Roadmap

**Name:** muxmaster

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
r := muxmaster.New()

// Middleware (must be registered BEFORE the routes it should wrap)
r.Use(logger, auth)

// Routes
r.GET("/users", listUsers)
r.GET("/users/:id", getUser)       // path parameter
r.GET("/static/*filepath", files)  // catch-all

// Groups
api := r.Group("/api/v1")
api.Use(apiKeyCheck)
api.POST("/items", createItem)

// Sub-groups
admin := api.Group("/admin")

// Read parameters in the handler
id := muxmaster.PathParam(r, "id")
ps := muxmaster.ParamsFromContext(r.Context())

// Custom handlers
r.NotFound = myHandler
r.MethodNotAllowed = myHandler
r.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }

// Options and their defaults
r.RedirectTrailingSlash  = true   // default: true
r.RedirectFixedPath      = false  // default: false (security — canonicalisation can bypass middleware)
r.HandleMethodNotAllowed = true   // default: true
r.HandleOPTIONS          = true   // default: true

http.ListenAndServe(":8080", r)
```

## Key design decisions

### Middleware applied at registration, not per request
`wrapMiddleware` is invoked in `Handle()` at registration time. This means **zero per-request middleware overhead** — but `Use()` must be called before the routes it should wrap.

### Pre vs Use × Handle vs HandleFast — the policy matrix (CDX-S8-003)
| middleware family | wraps `Handle`? | wraps `HandleFast`? |
|---|---|---|
| `r.Pre(...)` | YES | YES |
| `r.Use(...)` (stdlib `http.Handler`) | YES | NO — panics at `HandleFast` registration (CSA-2026-0054) |
| `r.UseFast(...)` (`FastMiddleware`) | NO | YES |

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

Specialised agents are configured under `.claude/agents/`. Use them proactively — do not wait for the user to ask for them explicitly.

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
2. The 8 specialists run in parallel and write to `/reports/<agent>/`
3. `threat-modeler` consolidates into `/reports/overview/<date>-posture.md` + updates `findings.md`, `threat-model.md`, `hypotheses.md`

**Escalation paths (security):**
- CRLF detected by any agent → `http-protocol-security-auditor`
- Race detected by any agent → `concurrency-security-auditor`
- Suspected timing leak → `timing-and-sidechannel-analyst`
- Routing bypass → `path-routing-fuzzer`
- Combination of findings → `threat-modeler-and-zero-day-researcher`

All agents may run in parallel when the tasks are independent. The `threat-modeler` runs before (planning) and after (consolidation) the parallel batch.

---

## Performance baseline (AMD Ryzen 9 5900HX, Go 1.26.2 — HEAD with tiered reqBundle)

Internal benchmarks (`bench_test.go`), after the tiered-bundles optimisation (reqBundle1/2/3 by param count):

| Case | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static route | 25.3 | 0 | 0 |
| 1 parameter | 112 | 416 | 1 |
| 2 parameters | 130 | 448 | 1 |
| 3 parameters | 141 | 480 | 1 |
| Catch-all | 109 | 416 | 1 |
| Parallel static | 3.7 | 0 | 0 |
| Parallel 1 parameter | 108 | 416 | 1 |

> Previous baseline (single 480 B reqBundle): 1 param=135 ns/640 B, 2=150 ns/640 B, 3=153 ns/640 B, catch-all=139 ns/640 B, parallel=165 ns/640 B.
> Tiered optimisation: reqBundle1 (416 B), reqBundle2 (448 B), reqBundle (480 B) — each tier matches the exact GC size class. Average gain: −17% ns/op, −35% B/op on 1-param routes; −13% and −30% on 2-param routes.

Competitive benchmarks (`competitor/bench_test.go`, AMD Ryzen 9 5900HX):

| Case | MuxMaster (HEAD) | MuxMaster (vendored¹) | httprouter | bunrouter² | chi v5 | Fiber v3³ |
|---|---|---|---|---|---|---|
| Static | **25 ns, 0 allocs** | 27.5 ns, 0 allocs | 33.8 ns, 0 allocs | 188.4 ns, 3 allocs | 213.5 ns, 2 allocs | 188.7 ns, 0 allocs |
| 1 parameter | **112 ns, 1 alloc** | 43.7 ns, 0 allocs | 56.4 ns, 1 alloc | 182.9 ns, 3 allocs | 354.1 ns, 4 allocs | 212.2 ns, 0 allocs |
| 2 parameters | **130 ns, 1 alloc** | 55.7 ns, 0 allocs | 66.5 ns, 1 alloc | 204.9 ns, 3 allocs | 402.2 ns, 4 allocs | 287.4 ns, 0 allocs |
| 3 parameters | **141 ns, 1 alloc** | 56.1 ns, 0 allocs | 78.4 ns, 1 alloc | 204.4 ns, 3 allocs | 410.2 ns, 4 allocs | 270.9 ns, 0 allocs |
| Catch-all | **109 ns, 1 alloc** | 41.5 ns, 0 allocs | 51.3 ns, 1 alloc | 173.5 ns, 3 allocs | 330.2 ns, 4 allocs | 212.4 ns, 0 allocs |
| Parallel static | **3.7 ns, 0 allocs** | 3.83 ns, 0 allocs | 4.92 ns, 0 allocs | 124.6 ns, 3 allocs | 128.2 ns, 2 allocs | 25.9 ns, 0 allocs |
| Parallel param | **108 ns, 1 alloc** | 37.6 ns, 0 allocs | 22.2 ns, 1 alloc | 124.4 ns, 3 allocs | 223.9 ns, 4 allocs | 28.7 ns, 0 allocs |

¹ Vendored version: uses `rcPool` + `setReqCtx` on the original `r` — 0 allocs but with race conditions confirmed by the concurrency-security-auditor (CSA-001)
² bunrouter vendored: a customised fork with an `HTTPHandlerFunc` adapter — does not represent the upstream 0-alloc reality
³ Fiber uses fasthttp (different stack from net/http)

Interpretation notes:
- **MuxMaster vendored (0 allocs)** was the previous approach, audited and rejected for confirmed race conditions (CSA-001): `setReqCtx` modified the original `r` that middleware goroutines already held a reference to
- **MuxMaster HEAD (1 alloc)** is the maximum-safety design with stdlib `net/http`: tiered reqBundle (1/2/3 params → 416/448/480 B) fuses requestCtx + *http.Request into a single allocation, using `unsafe` ONLY on freshly allocated structs before any concurrent access
- **httprouter** uses a different API (3rd argument `Params`), not native `http.Handler`; the 1 alloc is just the params slice (64 B), not a full copy of `*http.Request`
- **bunrouter** vendored is not representative of upstream — uses `context.WithValue` via the adapter → 3 allocs
- **Fiber** has fasthttp URI-parsing overhead (~55% CPU) that is part of the real production cost

---

## Development principles — Maximum performance

### Mandatory multi-disciplinary approach
Before implementing any solution, you must consider **every possible approach** — alternative data structures, algorithms from other languages (C, C++, Rust, Zig, Java, etc.), operating-system techniques, and hardware patterns — and implement the most efficient one in idiomatic Go. Do not limit yourself to what is common in Go; draw inspiration from the best of every language and translate it to Go.

### Implementation selection criteria
1. **Measure first** — never optimise without a benchmark before and after (`benchstat`)
2. **Lower ns/op and allocs/op** — these are the primary success metrics
3. **Zero allocations on the hot path** is the goal; any allocation must be justified
4. **Idiomatic Go** — every implementation must respect Go principles: simplicity, readability, correct use of goroutines/channels/sync, and 100% compatibility with `net/http`

### Active use of specialised subagents
The subagents are not optional — they are part of the process. Activate them proactively (without waiting for the user to ask) whenever the conditions described in the "Available subagents" section are met. The goal is maximum performance, and the specialised agents are the mechanism for achieving it with rigour and evidence.
