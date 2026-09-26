# Maximum Performance Guide

This guide shows how to configure MuxMaster for the absolute lowest latency and zero per-request allocations on production workloads. The recipes here trade a strict handler-lifetime contract for the fastest possible dispatch.

If you are starting out, read the [Getting Started](getting-started.md) guide first — the default configuration is already fast and avoids every pitfall described here.

## Table of Contents

- [TL;DR — the fastest setup](#tldr--the-fastest-setup)
- [How fast can it go?](#how-fast-can-it-go)
- [Decision tree — which API should I use?](#decision-tree--which-api-should-i-use)
- [Opt-in #1: `PoolRequestBundle`](#opt-in-1-poolrequestbundle)
- [Opt-in #2: `PoolFastParams`](#opt-in-2-poolfastparams)
- [`HandleFast` vs `Handle` — when to use each](#handlefast-vs-handle--when-to-use-each)
- [Lifetime contract — what you must not do](#lifetime-contract--what-you-must-not-do)
- [Auditing your handlers](#auditing-your-handlers)
- [Real-world recipes](#real-world-recipes)
- [Measuring your own configuration](#measuring-your-own-configuration)

---

## TL;DR — the fastest setup

For a service whose handlers do **not** retain `*http.Request` past return (the common case):

```go
mux := muxmaster.New()
mux.PoolRequestBundle = true   // recycle the request bundle (Opt O13)
mux.PoolFastParams    = true   // recycle Params slices for HandleFast (Opt O9)

// Use Pre for cross-cutting middleware — runs once per request, no per-route wrap
mux.Pre(realIP, requestID, recoverer)

// Register routes normally
mux.GET("/health", healthHandler)               // static: 0 allocs with or without the pool
mux.GET("/users/:id", getUser)                  // 0 allocs (default: 384 B, 1 alloc)
mux.GET("/orgs/:org/repos/:repo", getRepo)      // 0 allocs (default: 416 B, 1 alloc)
mux.GET("/static/*filepath", serveStatic)       // 0 allocs (default: 384 B, 1 alloc)

http.ListenAndServe(":8080", mux)
```

Measured on 2026-09-26 (AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`, root `bench_test.go`; [report](../reports/perf-lab-2026-09-26-docs/README.md)):

| Route | Default | `PoolRequestBundle = true` | Speed-up |
|---|---:|---:|---:|
| Static | 28.63 ns / 0 B / 0 allocs | unchanged — static routes have no bundle | — |
| 1 param | 118.5 ns / 384 B / 1 alloc | **48.51 ns / 0 B / 0 allocs** | **2.4×** |
| 2 params | 130.1 ns / 416 B / 1 alloc | **59.76 ns / 0 B / 0 allocs** | **2.2×** |
| 3 params | 149.6 ns / 480 B / 1 alloc | **61.76 ns / 0 B / 0 allocs** | **2.4×** |
| Catch-all | 132.4 ns / 384 B / 1 alloc | **44.95 ns / 0 B / 0 allocs** | **2.9×** |
| Parallel param | 104.5 ns / 384 B / 1 alloc | **6.901 ns / 0 B / 0 allocs** | **15×** |

In the competitor suite on the same host, the pooled `Handle` path was faster than `httprouter` on 1 parameter (46.7 vs 50.5 ns), 3 parameters and the parallel benchmark, level on 2 parameters, and slower on catch-all (46.5 vs 42.8 ns) — with zero allocations and the standard `http.Handler` signature. See [Performance](performance.md#comparison-notes).

---

## How fast can it go?

1-parameter route, `competitor/` suite, 2026-09-26 (AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`):

| Configuration | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `gorilla/mux` | 954.7 | 1 153 | 8 |
| `chi v5` | 360.7 | 704 | 4 |
| `bunrouter` (`http.Handler` adapter) | 160.3 | 416 | 3 |
| **MuxMaster default `Handle`** | 116.9 | 384 | 1 |
| `httprouter` (3-argument handler) | 50.5 | 64 | 1 |
| **MuxMaster `HandleFast`** | 47.8 | 32 | 1 |
| **MuxMaster `Handle` + `PoolRequestBundle`** | **46.7** | **0** | **0** |
| **MuxMaster `HandleFast` + `PoolFastParams`** | not in this run¹ | 0 | 0 |

¹ The suite has no pooled fast-route benchmark. The most recent measurement is the 2026-09-24 scaling benchmark below (`b.RunParallel`, 39.3 ns at 1 CPU, 0 allocs); it is not directly comparable with the serial figures above.

---

## Decision tree — which API should I use?

```
Does the handler need to retain *http.Request or Params past return?
(e.g. send r into a goroutine that outlives ServeHTTP)
│
├── YES ─ Use default Handle (no opt-ins).
│         Lifetime is GC-managed. Costs ~117 ns / 384 B / 1 alloc.
│
└── NO  ─ Do you need the stdlib http.Handler signature?
          │
          ├── YES ─ Use Handle + Mux.PoolRequestBundle = true.
          │         ~47 ns / 0 B / 0 allocs. Full stdlib middleware compatibility.
          │
          └── NO  ─ Use HandleFast + Mux.PoolFastParams = true.
                    0 B / 0 allocs. FastMiddleware only (not stdlib).
                    Params arrive as a 3rd argument (no context lookup).
```

You can mix the two on the same Mux: `Handle` routes use the pool, `HandleFast` routes use the params pool. They are independent opt-ins.

---

## Opt-in #1: `PoolRequestBundle`

When `Mux.PoolRequestBundle = true`, MuxMaster recycles the per-request `reqBundle` (the fused `requestCtx` + `http.Request` copy) via `sync.Pool`. This eliminates the 384 / 416 / 480 B allocation on every parameterised `Handle` route.

```go
mux := muxmaster.New()
mux.PoolRequestBundle = true

mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    // Use r and id ONLY during this function. Do NOT store r in a global,
    // a channel, or a goroutine that will outlive this call.
    w.Write([]byte("user " + id))
})
```

**What the pool actually recycles**

The pool holds three tiers — `reqBundle1` (368 B, 384 B size class), `reqBundle2` (400 B, 416 B), `reqBundle` (456 B, 480 B) — matching the parameter count of the matched route. On Get the bundle is filled with a copy of the current request. After the handler returns, the bundle is **fully zeroed** and put back, so the next request cannot observe stale state. Routes with more than three parameters still allocate their overflow parameter slice. Static routes are unaffected: they never use a bundle.

**What happens if the unsafe shortcut is unavailable**

Future Go versions may rename or remove the unexported `ctx` field of `http.Request`. MuxMaster detects this at init via reflection (`hasReqCtxField`) and falls back to the non-pooled `r.WithContext(...)` path automatically — `PoolRequestBundle = true` is silently ignored in that case, preserving correctness over speed. The field is present on Go 1.27.1 (the module minimum; asserted by `TestReqCtxFieldDetected`) and was present on Go 1.27.0 (used for the 2026-09-26 measurements), where the pooled path measured zero allocations.

---

## Opt-in #2: `PoolFastParams`

When `Mux.PoolFastParams = true`, MuxMaster recycles the `Params` slice handed to `FastHandler` routes via three pools (1 / 2 / 3 params).

```go
mux := muxmaster.New()
mux.PoolFastParams = true

mux.GETFast("/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    id := ps.Get("id")
    // ps is recycled the instant this function returns.
    // Do NOT keep ps or ps[i] alive in any goroutine that outlives this call.
    w.Write([]byte("user " + id))
})
```

The pool is independent of `PoolRequestBundle` — you can enable either, both, or neither.

### High-concurrency scaling

Pooling becomes more beneficial as the number of CPUs grows, because allocation drives GC and allocator contention. Measured on 2026-09-24 with `b.RunParallel`, `-cpu 1,4,16`, `-count=6` (AMD Ryzen 9 5900HX, Go 1.27.0; [contention-hunt report](../reports/perf-lab-2026-09-24/contention-hunt.md)); not re-measured on 2026-09-26:

| Route type | CPU=1 | CPU=4 | CPU=16 | Pooled benefit |
|---|---:|---:|---:|---:|
| **1-param `Handle` (default)** | 143.2 ns | 94.7 ns | 104.4 ns | — |
| **1-param `Handle` + `PoolRequestBundle`** | 41.5 ns | 11.0 ns | **7.3 ns** | **14.3× at cpu=16** |
| **1-param `FastHandler` (default)** | 49.0 ns | 13.2 ns | 16.0 ns | — |
| **1-param `FastHandler` + `PoolFastParams`** | 39.3 ns | 10.3 ns | **5.0 ns** | **3.2× at cpu=16** |

**Recommendation:**

Pooling was faster at every tested CPU count, and the gap widened with more CPUs: the allocating variants stopped improving beyond 4 CPUs, while the pooled ones kept scaling. Consider enabling `PoolRequestBundle` and/or `PoolFastParams` if your deployment runs on several cores and your handlers meet the lifetime contract.

**Audit the lifetime contract first:** Verify that no handler retains `*http.Request` or `Params` past return. Violating this contract results in use-after-free against recycled pool storage. See [Lifetime contract — what you must not do](#lifetime-contract--what-you-must-not-do) below.

**Default configuration:** `PoolRequestBundle` and `PoolFastParams` default to `false` for maximum safety. Handlers may retain the request object freely in default mode, incurring a single allocation per request instead.

---

## `HandleFast` vs `Handle` — when to use each

Both APIs run on the same radix tree and the same atomic dispatch. The difference is how parameters are delivered to the handler:

| Aspect | `Handle` (stdlib) | `HandleFast` |
|---|---|---|
| Handler signature | `func(w, r)` (standard) | `func(w, r, ps muxmaster.Params)` |
| Read params | `muxmaster.PathParam(r, "id")` | `ps.Get("id")` (direct) |
| Stdlib middleware (`Use`) | ✅ Applied | ❌ Panics at registration |
| FastMiddleware (`UseFast`) | ❌ Not applied | ✅ Applied |
| `Pre` middleware | ✅ Applied | ✅ Applied |
| Default cost (1 param, 2026-09-26) | 116.9 ns / 384 B / 1 alloc | 47.8 ns / 32 B / 1 alloc |
| With pool opt-in | 46.7 ns / 0 B / 0 allocs (2026-09-26) | 0 B / 0 allocs (39.3 ns at 1 CPU, `RunParallel`, 2026-09-24) |
| Best for | Handlers that interact with `r` or use stdlib middleware ecosystems | Hot internal routes; latency-sensitive paths |

**Mixing on the same Mux**

```go
mux := muxmaster.New()
mux.PoolRequestBundle = true
mux.PoolFastParams = true

// Cross-cutting policies via Pre — runs on BOTH route types
mux.Pre(realIP, requestID, recoverer)

// Latency-critical: HandleFast
mux.GETFast("/v1/quote/:symbol", quoteHandler)
mux.GETFast("/v1/tick/:symbol", tickHandler)

// Standard: Handle with stdlib middleware
api := mux.Group("/v1")
api.Use(authMiddleware)        // stdlib middleware — only applies to api.GET / api.POST below
api.GET("/users/:id", getUser)
api.POST("/users", createUser)
```

> **Note.** Calling `mux.Use(stdlibMiddleware)` and then `mux.HandleFast(...)` panics at registration time on purpose: stdlib middleware does not run on the fast path, and silently mixing them would let `HandleFast` routes bypass authentication, logging, or any other policy you intended to apply. Use `Pre` for cross-cutting policy, `Use` for stdlib-style middleware on `Handle` routes, and `UseFast` for `FastMiddleware` on `HandleFast` routes.

---

## Lifetime contract — what you must not do

When `PoolRequestBundle` or `PoolFastParams` is enabled, the recycled object is returned to the pool **the instant your handler returns**. A goroutine still holding a reference will observe one of two states:

1. **Zeroed** — if the bundle has not been reissued yet. `r.URL` is `nil`, `ps[0]` is `Param{}`.
2. **Another request's state** — if the bundle has been reissued to a concurrent request. You see a path, body, and params that belong to an unrelated client.

Both are use-after-free against the pool storage. **Always copy what you need before spawning a goroutine.**

### ❌ Wrong — captures `r` in a goroutine

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    go func() {
        // BUG: `r` is recycled the moment the outer handler returns.
        log.Printf("processed request from %s for id=%s", r.RemoteAddr, id)
    }()
    w.WriteHeader(http.StatusAccepted)
})
```

### ❌ Wrong — captures `ps` in a `FastHandler` goroutine

```go
mux.GETFast("/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    go func() {
        // BUG: ps is returned to the pool when this handler returns.
        record(ps.Get("id"))
    }()
})
```

### ✅ Right — copy primitives before spawning

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id        := muxmaster.PathParam(r, "id")  // string — safe to capture
    remote    := r.RemoteAddr                  // string — safe to capture
    userAgent := r.UserAgent()                 // string — safe to capture
    go func() {
        log.Printf("processed request from %s (%s) for id=%s", remote, userAgent, id)
    }()
    w.WriteHeader(http.StatusAccepted)
})
```

### ✅ Right — clone params before retaining

```go
mux.GETFast("/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    psCopy := make(muxmaster.Params, len(ps))
    copy(psCopy, ps)
    go func() {
        record(psCopy.Get("id"))
    }()
})
```

### ✅ Right — drain the body before spawning

```go
mux.POST("/uploads/:id", func(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)  // string + bytes — safe to capture
    id := muxmaster.PathParam(r, "id")
    go processUpload(id, body)
    w.WriteHeader(http.StatusAccepted)
})
```

### Special case: libraries that spawn background goroutines

Some standard library handlers and third-party middleware spawn goroutines that outlive `ServeHTTP`. The most common case is `net/http.Transport` (used by `httputil.ReverseProxy` and HTTP clients): under concurrent load, `Transport.startDialConnForLocked` can start a background dial goroutine that continues calling `ctx.Value()` on the request's context **after** your handler returns.

**❌ Do NOT enable `PoolRequestBundle` if:**
- Your handler calls `httputil.ReverseProxy.ServeHTTP`
- Your handler calls an HTTP client that uses `net/http.Transport` and reuses the request object
- Any middleware in the chain spawns long-lived goroutines that read the request or its context

If you need pooling with a reverse-proxy gateway, keep `PoolRequestBundle = false` on the gateway handler and enable it only on handlers that remain pool-safe (e.g., the backend services being proxied to).

---

## Auditing your handlers

If you are turning on `PoolRequestBundle` for an existing codebase, the audit reduces to one question per handler:

> *Does this handler keep `r` (or values derived from `r` that are not strings/copies) alive past its return?*

**Safe captures** (these are values, not references into the bundle):
- `r.Method`, `r.URL.Path`, `r.URL.Query()` results, `r.RemoteAddr`, `r.UserAgent()`, `r.Host` — all strings or freshly-allocated maps
- The return value of `muxmaster.PathParam(r, "...")` — a string
- The return value of `io.ReadAll(r.Body)` — a byte slice copy

**Unsafe captures** (these point into the recycled bundle, or are read through it):
- `r` itself (the `*http.Request` pointer) — any later read through it (`r.URL`, `r.Header`, `r.Context()`, …) sees a zeroed or reissued bundle
- `r.Body` if you store the `io.ReadCloser` instead of draining it — `net/http` closes the body when the request ends
- `ps` (the `Params` slice from `FastHandler`)
- Any element `ps[i]` of `Params` if you keep the `Param` struct beyond return

A quick grep helps catch the common offenders:

```bash
grep -nR 'go func.*r\b' .
grep -nR 'go func.*\bps\b' .
grep -nR 'go.*\.ServeHTTP' .   # third-party libraries that spawn from handlers
```

If your grep returns clean, your handlers are pool-safe. If it finds matches, audit each one and apply the copy patterns above before enabling `PoolRequestBundle`.

---

## Real-world recipes

### Recipe 1 — High-throughput JSON REST API

Goal: a JSON REST API with zero per-request allocations in the routing layer.

```go
package main

import (
    "encoding/json"
    "log/slog"
    "net/http"
    "net/netip"
    "os"
    "strconv"

    muxmaster "github.com/FlavioCFOliveira/MuxMaster"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
    logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
    mux := muxmaster.New()
    mux.PoolRequestBundle = true   // 0-alloc Handle path
    mux.PoolFastParams    = true   // 0-alloc HandleFast path

    // Pre runs once per request, before routing — applies to both Handle and HandleFast
    proxyNet := netip.MustParsePrefix("10.0.0.0/8") // trust your proxy network
    mux.Pre(
        middleware.RealIP(&proxyNet),
        middleware.RequestID(),
        middleware.RecovererWithLogger(logger),
    )

    // Stdlib middleware for the API group — applies only to Handle routes below
    api := mux.Group("/v1")
    api.Use(middleware.Logger(os.Stdout))

    api.GET("/users/:id", getUser)
    api.POST("/users", createUser)
    api.GET("/users/:id/orders/:orderID", getUserOrder)

    // Hot path: prefer HandleFast for trusted internal routes
    mux.GETFast("/v1/health", healthFast)
    mux.GETFast("/v1/metrics/:metric", metricsFast)

    _ = http.ListenAndServe(":8080", mux)
}

func getUser(w http.ResponseWriter, r *http.Request) {
    id, _ := strconv.Atoi(muxmaster.PathParam(r, "id"))
    _ = json.NewEncoder(w).Encode(map[string]any{"id": id})
}

func createUser(w http.ResponseWriter, r *http.Request) {
    var u struct{ Name string }
    _ = json.NewDecoder(r.Body).Decode(&u)
    w.WriteHeader(http.StatusCreated)
}

func getUserOrder(w http.ResponseWriter, r *http.Request) {
    id      := muxmaster.PathParam(r, "id")
    orderID := muxmaster.PathParam(r, "orderID")
    _ = json.NewEncoder(w).Encode(map[string]any{"user": id, "order": orderID})
}

func healthFast(w http.ResponseWriter, r *http.Request, _ muxmaster.Params) {
    w.WriteHeader(http.StatusOK)
}

func metricsFast(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    name := ps.Get("metric")
    _, _ = w.Write([]byte(name + " 42\n"))
}
```

### Recipe 2 — Spawning background work from a handler

```go
// Background-work pattern: copy primitives, drain body, then go.
mux.POST("/v1/events", func(w http.ResponseWriter, r *http.Request) {
    // Snapshot everything we need before the bundle goes back to the pool.
    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "bad body", http.StatusBadRequest)
        return
    }
    requestID := muxmaster.PathParam(r, "id")        // string (may be "")
    correlation := r.Header.Get("X-Correlation-Id")  // string

    // Now we are safe to spawn.
    go processEventAsync(body, requestID, correlation)

    w.WriteHeader(http.StatusAccepted)
})
```

### Recipe 3 — Streaming response (no opt-in pool needed)

When a handler streams a large body, the response itself dominates the cost — the allocation savings of `PoolRequestBundle` are immaterial. But it is still safe to use:

```go
mux.PoolRequestBundle = true

mux.GET("/v1/export/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    w.Header().Set("Content-Type", "text/csv")
    w.WriteHeader(http.StatusOK)

    fw := bufio.NewWriter(w)
    for row := range loadRows(r.Context(), id) {
        fw.Write(row)
    }
    fw.Flush()
    // The bundle is returned to the pool only after this function returns,
    // i.e. after the stream completes, so using r and r.Context() here is safe.
})
```

### Recipe 4 — Switching pools off in tests

The pool path tightens the lifetime contract. When writing integration tests that intentionally race goroutines past handler return (testing what the user's own code might do), it can be useful to run with the pool off and reproduce the slower-but-safer default:

```go
func TestHandler_AllowsRetainingRequest(t *testing.T) {
    mux := muxmaster.New()
    mux.PoolRequestBundle = false  // explicit, even though it is the default

    mux.GET("/users/:id", retainingHandler)

    // ...
}
```

For most production code the answer is the inverse: turn the pool **on** in tests too, so CI catches a retention violation before it ships.

---

## Measuring your own configuration

```bash
# Establish a baseline with your current configuration
go test -bench='YourBench' -benchmem -count=10 -benchtime=2s . > before.txt

# Flip Mux.PoolRequestBundle on (or wire up HandleFast for a route)
go test -bench='YourBench' -benchmem -count=10 -benchtime=2s . > after.txt

# Compare statistically
go install golang.org/x/perf/cmd/benchstat@latest
benchstat before.txt after.txt
```

For a CPU and memory profile:

```bash
go test -bench='YourBench' -benchmem \
    -cpuprofile=cpu.prof -memprofile=mem.prof \
    -benchtime=5s -run=^$ .

go tool pprof -top -cum cpu.prof
go tool pprof -alloc_space -top mem.prof
```

In production, wire up `net/http/pprof` and capture under real load:

```go
import _ "net/http/pprof"

// Handle (not Mount): DefaultServeMux registers the pprof handlers under
// /debug/pprof/, and Mount would strip that prefix before forwarding.
mux.Handle(http.MethodGet, "/debug/pprof/*path", http.DefaultServeMux)

// curl -s http://your-host/debug/pprof/profile?seconds=30 > cpu.prof
// go tool pprof -top -cum cpu.prof
```

---

## See Also

- [Performance](performance.md) — design rationale, current and historical measurements
- [`reports/perf-audit-2026-05-12/2026-05-12-deep-audit.md`](../reports/perf-audit-2026-05-12/2026-05-12-deep-audit.md) — the audit that produced the pool opt-ins
- [Configuration](configuration.md) — every `*Mux` field and its default
- [SECURITY.md](../SECURITY.md) — the concurrency analysis behind the lifetime contracts
