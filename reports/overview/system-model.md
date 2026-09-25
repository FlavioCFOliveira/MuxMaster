# MuxMaster — System Model (DFD + Trust Boundaries)

**Date:** 2026-04-17
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Living document — updated at each architectural change

---

## 1. Purpose

This document describes the system model of MuxMaster: external entities, trust boundaries, internal components, data flows and attack surface. It is the foundation of `threat-model.md`, `attack-trees.md` and `hypotheses.md`.

## 2. Scope

**In-scope:**
- Core router: `mux.go`, `tree.go`, `params.go`, `group.go`, `handler.go`, `introspection.go`, `response.go`
- All 15 middlewares in `middleware/`
- Interactions with `net/http`, `context`, `sync.Pool`, `atomic.Pointer`, `unsafe`
- HTTP/1.1 and HTTP/2 behavior when `net/http` delivers a parsed `*http.Request`

**Out-of-scope:**
- HTTP/3 (not supported by stdlib)
- Code of applications that register handlers or custom middlewares — we audit the module, not consumption
- Go runtime CVEs themselves (will be confirmed via `govulncheck`, but not re-audited here)

## 3. Data Flow Diagram — Level 0 (context diagram)

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

External entities:
- **Direct malicious client** — no intermediaries
- **Client through reverse proxy** (nginx, Traefik, Caddy, envoy, cloud LB) — typical production scenario
- **Client with CDN between itself and the server** — cache can be oracle
- **Log / SIEM system** — consumes stdout/stderr; potential victim of log injection
- **Observability system** — Prometheus scraper, tracer; consumes `RoutePattern` / `Routes()`

## 4. Data Flow Diagram — Level 1 (MuxMaster as a box)

```
                                ╔═══════════════════════════════════════╗
                                ║          net/http server              ║
                                ║  (parsing HTTP/1.1, HTTP/2, framing,   ║
                                ║   TLS, connection management)          ║
                                ╚═══════════════════════╦═══════════════╝
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

## 5. Data Flow Diagram — Level 2 (hot-path operations)

### 5.1 `ServeHTTP` → `dispatch` (all requests)

1. **Entry:** `m.ServeHTTP(w, r)` receives `(w http.ResponseWriter, r *http.Request)` already parsed.
2. **Branch:** if `m.PanicHandler != nil` deviates to `dispatchWithRecover` (adds `defer m.recoverPanic`). Otherwise, enters directly in hot path (no defer frame).
3. **Pre-middleware:** if `m.preHandler != nil`, executes the pre-routing chain (may mutate path, create new `*http.Request`).
4. **URL path resolution:**
   - `urlPath = r.URL.Path` by default
   - if `m.UseRawPath && r.URL.RawPath != ""` → uses `RawPath`
5. **Tree load:** `trees := m.treesPtr.Load()` (atomic, lock-free).
6. **Method dispatch:** `idx := methodIdx(r.Method)`. **Case-sensitive** and switch mapping on 10 methods. Non-standard methods → `idx = -1` → fallback to `idxWild` tree (used by `Mount`). If nothing responds → 404/405.

### 5.2 `tree.getValue` (lookup)

- Reads `paramsBuf` stack-allocated (fixed-size struct `[3]Param`), no heap escape.
- For each tree level: prefix compare (`prefixMatch`) with optional ASCII case-fold (`foldEq`).
- Three types of wildcard node: `param` (`:name`), `regexParam` (`{name:expr}`), `wildcard` (`*name`).
- Catch-all consumes the entire rest of path, including `/`.
- If handler not found but TSR exists (trailing-slash-redirect), returns `tsr=true`.

### 5.3 Params lifecycle

- `paramsBuf.add(key, value)` copies key/value strings to `[3]Param`. If > 3 params: **silently discards excess** (silent overflow drop).
- If `handler != nil` and `ps.count > 0`:
  - `acquireRC()` obtains `*requestCtx` from `rcPool`
  - `rc.Context = origCtx` (or `context.Background()` if `origCtx == nil`)
  - `rc.pattern = pattern`
  - `rc.params = Params(rc.small[:copy(rc.small[:], pslice)])` — copies from stack buffer to heap buffer of RC pool
  - **unsafe:** `*origCtxPtr = rc` where `origCtxPtr` = `*(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))` — directly overwrites non-exported `ctx` field of `*http.Request`
  - Dispatches `handler.ServeHTTP(w, r)`
  - **Post-handler:** restores `*origCtxPtr = origCtx`; cleans `rc.Context = nil; rc.params = nil; rc.pattern = ""`; returns to pool
- If `ps.count == 0`: calls `handler.ServeHTTP(w, r)` directly (zero pool ops, zero allocs).

**Implicit risk:** the overwrite and restore of `r.ctx` is synchronous to handler. If handler creates a goroutine that retains `r` and the goroutine reads `r.Context()` after handler returns, sees the **restored** context (original), not the routed context. And worse: if another goroutine on the same `*http.Request` tries to read `r.Context()` concurrently during `*origCtxPtr = rc` or `*origCtxPtr = origCtx`, **data race**.

### 5.4 `Mount` / `ServeFiles`

- Both use `r.Clone(r.Context())` to create `r2`. OK.
- `Mount` trim prefix of `RawPath` via `strings.TrimPrefix` — mismatch vs `Path` possible if both not normalized the same way.
- `ServeFiles` constructs `r2.URL.Path = PathParam(r, paramName)` — if param value contains unescaped `..`, delegates path-traversal to `http.FileServer` which normally handles well — but the boundary should be explicitly documented and tested.

## 6. Components and responsibilities

| Component | File | Responsibility | Internal state | Sync primitive |
|---|---|---|---|---|
| Mux root | `mux.go` | Dispatch, options, PanicHandler | `treesPtr atomic.Pointer[methodTrees]`, `middleware []`, `pre []`, `preHandler http.Handler`, `mu sync.Mutex` | `atomic.Pointer` (read path), `sync.Mutex` (write path) |
| Radix tree | `tree.go` | O(k) lookup, wildcard, regex, catch-all | per-node: `path`, `indices`, `children`, `handler`, `pattern`, `regexp` | immutable after registration (by design) |
| Params | `params.go` | Path param access, types, RC pool | `rcPool sync.Pool` | `sync.Pool` + `unsafe.Add` for reqCtxOffset |
| Group | `group.go` | Prefix + middleware composition | `prefix string`, `middleware []` | none (builder pattern) |
| Handler err | `handler.go` | `HandlerFuncE`, `HTTPError` | stateless | — |
| Introspection | `introspection.go` | `Lookup`, `Routes`, `Walk` | reads `treesPtr` | `atomic.Pointer` load |
| Response | `response.go` | JSON/XML/Text/Redirect helpers | stateless | — |
| Middleware | `middleware/` | 15 functions returning `func(http.Handler) http.Handler` | per-middleware | varies (see below) |

### 6.1 Mutable state in middlewares

| Middleware | State | Sync | Threat highlights |
|---|---|---|---|
| `basic_auth` | `creds map[string]string` (read-only after build) | none (config-time only) | user enumeration via map lookup before `subtle.ConstantTimeCompare` |
| `clean_path` | stateless | — | double-clean bypass candidate (single-pass `path.Clean`) |
| `compress` | `pool *sync.Pool` (gzip writers), per-request `gzipResponseWriter.buf` | `sync.Pool` | unbounded buffer growth; BREACH oracle |
| `cors` | `allowedOrigins map[string]bool`, `allowAll bool` | read-only | Origin reflection when `allowAll`; header injection |
| `logger` | `out io.Writer` (shared) | caller-owned | CRLF in path → log injection; no redaction |
| `no_cache` | stateless | — | — |
| `real_ip` | stateless; mutates `r.RemoteAddr` | — | unconditional trust of XFF |
| `recoverer` | stateless | `defer recover()` | prints stack + debug.Stack() to `os.Stderr` — potential info leak |
| `request_id` | stateless; `crypto/rand` | — | reflects client header without sanitization |
| `set_header` | stateless | — | — |
| `strip_slashes` | stateless | — | double-slash semantics undocumented |
| `throttle` | `tokens chan`, `queue chan` (global, not per-IP) | channel | advertises per-IP but is **global**; counter race N/A (channel ops atomic) but handler semantics per `defer` may leak tokens on panic |
| `timeout` | stateless; `context.WithTimeout` | context cancel | handler keeps running after timeout (goroutine leak) |
| `with_value` | `key, val any` | read-only | context-key collision if caller passes string |

## 7. Trust boundaries — explicit definitions

### TB-1: Network / stdlib boundary
- **Who controls:** attacker on network (arbitrary bytes)
- **Who validates:** `net/http` (parses request line, headers, body framing)
- **Who trusts:** MuxMaster trusts that `r.Method`, `r.URL.Path`, `r.Header`, `r.URL.RawPath` survived stdlib normalization
- **Risk:** divergence between how `net/http` parses and how MuxMaster re-parses `r.URL.Path` — basis of request smuggling when front proxy present

### TB-2: MuxMaster entry → middleware chain
- **Who controls:** MuxMaster core
- **Who validates:** router dispatch (method+path lookup)
- **Who trusts:** middlewares trust that `r.URL.Path` and `r.Header` still contain attacker's bytes
- **Risk:** if router overwrites `r.URL.Path` before middlewares (e.g. `Mount`, `ServeFiles`, redirect), middleware sees different path than attacker sent

### TB-3: auth passed
- **Who controls:** `basic_auth` (or equivalent) signs passage
- **Who validates:** the middleware
- **Who trusts:** application handler assumes request passed auth
- **Risk:** middleware ordering — if `basic_auth` registered in a group but path arrives via sibling group without auth (path aliasing), bypass

### TB-4: response to network
- **Who controls:** stdlib (after `ResponseWriter.Write`)
- **Who validates:** stdlib does not re-validate headers; values with CRLF may exit
- **Who trusts:** attacker receives bytes
- **Risk:** CRLF injection in `Set-Cookie`, `Location`, `X-Request-ID`, `Access-Control-Allow-Origin`

## 8. Assets

| Asset | Confidentiality | Integrity | Availability | Who threatens |
|---|---|---|---|---|
| Credentials in `creds map` | Critical | Critical | Low | attacker via timing user-enum, brute-force, log leak |
| Password/token in Authorization header | Critical | Critical | Low | logger unescape, log file read |
| Session cookies (set by app) | Critical | Critical | Medium | CRLF in Set-Cookie, CORS reflection with credentials |
| Response body content | High | Critical | High | BREACH/CRIME (via compress), cross-origin via CORS misconfig |
| Internal topology (secret routes) | Medium | Low | N/A | `Routes()`/`Walk()` leak, route-existence timing oracle |
| Process memory | High | Critical | Critical | compression bomb, unbounded buf in compress, slowloris goroutine, throttle unlimited state |
| Log stream (stdout/stderr) | Medium | High | Medium | CRLF injection, ANSI escape, creds leak via logger |
| Handler execution slot (throttle) | N/A | High | Critical | XFF spoof to exhaust throttle, token leak via panic |
| `sync.Pool` (RC pool) | High | Critical | High | cross-request contamination (requires careful audit) |

## 9. Enumerated attack surface

### 9.1 Direct external inputs (all untrusted)

| Input | Origin | Used in |
|---|---|---|
| `r.Method` | request line | `methodIdx`, `allowed` |
| `r.URL.Path` | request line | `tree.getValue`, redirect building, logger |
| `r.URL.RawPath` | request line | `Mount`, opt `UseRawPath` |
| `r.URL.Query()` | query string | (caller handlers — not used by router directly) |
| `r.Header["Origin"]` | header | `cors.go` |
| `r.Header["Accept-Encoding"]` | header | `compress.go` |
| `r.Header["X-Forwarded-For"]` | header | `real_ip.go`, indirectly `throttle.go` |
| `r.Header["X-Real-IP"]` | header | `real_ip.go` |
| `r.Header["X-Request-ID"]` | header | `request_id.go` |
| `r.Header["Authorization"]` | header | `basic_auth.go` (decoded via `r.BasicAuth()`) |
| `r.RemoteAddr` | TCP | `real_ip.go` overrides this |
| `r.Body` | body | caller handlers only (not used by router) |

### 9.2 Outputs controlled by module (may carry injection)

| Output | Built in | Attacker bytes |
|---|---|---|
| Response `Location` header | `mux.go` (redirect), `response.Redirect` | entire `r.URL.Path` (no manual sanitization — delegates to stdlib) |
| Response `Allow` header | `mux.go:allowed` | — (fixed method strings) |
| Response `Access-Control-Allow-Origin` | `cors.go` | **reflects origin directly** |
| Response `Access-Control-Allow-Credentials` | `cors.go` | — |
| Response `Access-Control-Expose-Headers` | `cors.go` | config-time strings |
| Response `Access-Control-Allow-Methods` | `cors.go` | config-time |
| Response `Access-Control-Allow-Headers` | `cors.go` | config-time |
| Response `WWW-Authenticate` | `basic_auth.go` | **`realm` string** (config-time, but if caller passes user input → CRLF) |
| Response `X-Request-ID` | `request_id.go` | **header reflected from client** |
| Response `Cache-Control`, `Pragma`, `Expires` | `no_cache.go` | — |
| Response `Vary`, `Content-Encoding` | `compress.go` | — |
| Response body (500) | `recoverer.go`, `Mux.PanicHandler` default | stdlib `http.Error` — safe |
| `os.Stderr` panic dump | `recoverer.go` | `rcv any` + `debug.Stack()` — **includes local variables in stack** |
| Log line (stdout) | `logger.go` | **`r.URL.Path` raw** via `fmt.Fprintf` |

### 9.3 Configuration inputs (trusted at build time)

| Config | Source | Risk if attacker controls |
|---|---|---|
| `creds map` in basic_auth | application | — (not remotely controllable) |
| `realm` in basic_auth | application | if caller exposes via config file / env poorly-validated, CRLF injection |
| `CORSOptions.AllowedOrigins` | application | — |
| `Compress(level)` | application | invalid level → panic at build (pre-serve) |
| `Logger(out)` | application | if `out` is a file without rotation, log flood DoS |
| `BasicAuth(realm, creds)` | application | creds hardcoded is a smell, but out of scope |

### 9.4 Panic points at runtime (each is a D contingent if recoverer does not cover)

- `Mux.Handle` — panics in: empty method, path not absolute, handler nil, unsupported method, duplicate route, catch-all malformed, wildcard conflict
- `Mux.Mount` — nil handler, prefix not starting with `/`
- `Mux.ServeFiles` — nil root, prefix without `/*name`
- `tree.addRoute` — invalid wildcards, invalid regex, conflict
- `middleware.BasicAuth` — nil creds
- `middleware.Compress` — invalid level
- `middleware.Logger` — nil writer
- `middleware.Throttle` — limit ≤ 0 or backlog < 0
- `middleware.Timeout` — duration ≤ 0
- `middleware.WithValue` — nil key

All these panics are **registration-time**; correctly acceptable. But: none is recoverable in a handler via `recoverer` — they run **before** the chain exists. If an application registers routes dynamically (not supported), panics here collapse the process.

## 10. Concurrency model

- **Tree read:** `m.treesPtr.Load()` lock-free on every request.
- **Tree write:** `Handle` acquires `m.mu.Lock`, clones tree array (copy-on-write), mutates root of relevant method, `treesPtr.Store`. Correct.
- **Critical note:** the **mutation of individual nodes** within `root.addRoute` is **not** copy-on-write. If a method is re-registered concurrently with a serve, the serve may read partially-mutated nodes. Current design assumes complete registration before serving starts. **This is latent TOCTOU if application violates contract** — documentation exists but code does not guard invariance.
- **`rcPool`:** `sync.Pool` with `New` creating `*requestCtx`. `Get/Put` race-safe, but pattern `*origCtxPtr = rc` creates window where two goroutines could see same `rc` if they share same `*http.Request` (e.g. middleware that spawns goroutines with same `r`). Vector for pool contamination.
- **Middleware modification at runtime:** `m.Use`, `m.Pre` alter `m.middleware`/`m.pre`. **No sync**. If called concurrently with serving, guaranteed data race. Documentation says "before routes"; recommend enforcement via `atomic.Bool` flag "has_served".

## 11. Notable unsafe / reflective constructs

| Location | Construct | Purpose | Audit needed |
|---|---|---|---|
| `params.go:140` | `reflect.TypeOf(http.Request{})` in `init()` | Find offset of `ctx` field | If stdlib changes layout, `reqCtxOffset` wrong silently in new Go versions — **latent regression** |
| `params.go:155`, `mux.go:464`, `mux.go:521` | `unsafe.Add(unsafe.Pointer(r), reqCtxOffset)` | Write directly `r.ctx` | Bypasses `r.WithContext` (which clones) — request sharing between goroutines becomes unsafe |
| `introspection.go:99` | `reflect.ValueOf(h)` + `runtime.FuncForPC` | Handler name for display | Info leak if `Routes()` exposed externally |

## 12. External dependencies

```
$ grep -E 'require\s+github' go.mod
(empty)
```

**Zero external dependencies.** Invariant confirmed.

Go stdlib usage (hot path): `net/http`, `net/url`, `context`, `sync`, `sync/atomic`, `unsafe`, `reflect`, `strings`, `strconv`, `path`, `regexp`, `crypto/subtle` (basic_auth), `crypto/rand` (request_id), `encoding/hex`, `compress/gzip`, `io`, `fmt`, `os`, `runtime/debug`, `time`, `encoding/json`, `encoding/xml`.

## 13. Prioritized attack surface (input to sprint plan)

Top 10 surfaces by estimated risk (before investigation):

1. **`unsafe.Add` in `mux.go:464/521`** — write to non-exported field of `*http.Request` shareable between goroutines → potential data race in handlers that escape the request to child goroutines. **Only use of `unsafe` in module**, therefore critical to audit.
2. **`logger.go` CRLF injection** — `fmt.Fprintf(out, "%s %s %s ...", ..., r.URL.Path, ...)` with `r.URL.Path` raw. Trivially exploitable if attacker sends path with `\r\n`.
3. **`basic_auth.go` user enumeration** — map lookup `creds[user]` before `subtle.ConstantTimeCompare`; non-constant-time; timing discrepancy between existing and non-existing user is architecturally guaranteed.
4. **`cors.go` Origin reflection with `allowAll`** — if any origin echoed and attacker sends `Origin: evil.com\r\nSet-Cookie: x=1`, the `allowedOrigins[origin]` (string map) does not contain `\r\n` → would fall to "not listed". BUT if `allowAll=true` the check is skipped and reflects directly. Requires empirical verification.
5. **`request_id.go` CRLF + unbounded length** — `r.Header.Get("X-Request-ID")` direct to `w.Header().Set("X-Request-ID", id)`. Accepts any size and any byte.
6. **`real_ip.go` unconditional XFF trust** — no trusted proxy config. Trivial spoof.
7. **`compress.go` unbounded buffer + BREACH** — `g.buf = append(g.buf, b...)` grows unlimited; BREACH oracle when input reflected is in response.
8. **`RedirectTrailingSlash`/`RedirectFixedPath`** — constructs Location from `r.URL.Path`. If path contains `//evil.com`, `path.Clean` removes duplicate `/` becoming `/evil.com`. `r.URL.String()` may serialize as cross-host URL? Requires rigorous verification.
9. **`tree.go` radix: bypass via case-fold + RedirectFixedPath** — `RedirectFixedPath` calls `getValue(cleaned, nil, false)` with ci=false, but if `CaseInsensitive=true` matching is folded; combination may allow `/ADMIN/` → canonical `/admin` → redirect exposes hidden route.
10. **`timeout.go` goroutine leak** — classic; well-known. Document SLA.

And secondary attention to:
- `Mount` with RawPath prefix trim (Path vs RawPath divergence)
- `ServeFiles` + catch-all + traversal via `http.FileServer`
- `throttle.go` semantics global vs per-IP (docs say per-IP; code is global)
- Introspection `Routes()/Walk()` concurrent with dynamic registration (out of contract but testable)

## 14. Open questions for experts to resolve

1. (http-protocol) `http.Redirect(w, r, r.URL.String(), code)` when `r.URL.Path` starts with `//` produces cross-origin Location? If yes, open redirect.
2. (path-routing) Differential: `httprouter`, `chi`, `bunrouter` all accept `/a%2Fb` as two segments? If they differ, MuxMaster may be outlier.
3. (concurrency) If a handler `go func(){ fmt.Println(r.Context()) }()` what does the goroutine see when main handler returns and `*origCtxPtr = origCtx` fires? Race detector should flag.
4. (middleware) `basic_auth` timing: what is empirical difference existing-user vs non-existing-user in 1M samples?
5. (dos) `compress.go` buf: allocation linear confirmed up to which response body size? Is there effective limit (heap available)?
6. (sast) `reflect.TypeOf(http.Request{})` in `init()` — if future Go version renames `ctx` the offset becomes wrong. Is there assert post-init to validate?
7. (timing) `RedirectFixedPath` timing — measure if distinguishable vs NotFound (route-existence oracle).
8. (fuzz) `findWildcard` + `expandOptional` + regex compile within `insertChild` — fuzzer generating patterns `{name:(.*){0,1000}}` causes ReDoS in compilation?

## 15. Consolidated diagram — surface & TBs

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

## 16. Next revisions

This `system-model.md` is re-evaluated when:
- Middleware is added / removed in `middleware/`
- Signature of `ServeHTTP`, `dispatch`, or format of `*requestCtx` changed
- Pattern of `unsafe.Pointer` / `reflect` use in hot path altered
- New transport introduced (HTTP/3, gRPC, WebSocket)
- Any mechanism for state mutation **after** serving starts is introduced
