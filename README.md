<p align="center">
  <img src="assets/logo-muxmaster.png" alt="MuxMaster" width="250">
</p>

# MuxMaster

[![CI](https://github.com/FlavioCFOliveira/MuxMaster/actions/workflows/ci.yml/badge.svg)](https://github.com/FlavioCFOliveira/MuxMaster/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/FlavioCFOliveira/MuxMaster.svg)](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster)
[![Go Report Card](https://goreportcard.com/badge/github.com/FlavioCFOliveira/MuxMaster)](https://goreportcard.com/report/github.com/FlavioCFOliveira/MuxMaster)
[![Go Version](https://img.shields.io/github/go-mod/go-version/FlavioCFOliveira/MuxMaster)](https://github.com/FlavioCFOliveira/MuxMaster/blob/main/go.mod)
[![Latest Release](https://img.shields.io/github/v/release/FlavioCFOliveira/MuxMaster)](https://github.com/FlavioCFOliveira/MuxMaster/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**MuxMaster** is a high-performance HTTP router for Go. It is 100% compatible with the standard `net/http` package, has zero external dependencies, and is built on a radix tree (compressed prefix trie) whose lookup cost is O(k) in the length of the URL path, not in the number of registered routes.

Static routes allocate **zero bytes**. By default, a route with path parameters makes **one tiered allocation** (384, 416 or 480 B for 1, 2 or 3+ parameters) that fuses the request copy and its parameter context into a single GC-managed object. With the opt-in `Mux.PoolRequestBundle = true`, that object is recycled through `sync.Pool` and parameterised routes also dispatch with **zero allocations**, under a stricter handler lifetime contract — see the [Maximum Performance Guide](docs/max-performance.md).

## Why MuxMaster?

- **Zero allocations on static routes**; one fused allocation on parameterised routes by default
- **Zero allocations on parameterised routes too (opt-in)** — with `Mux.PoolRequestBundle = true`, a 1-parameter route measured 46.7 ns / 0 B / 0 allocs, against 50.5 ns / 64 B / 1 alloc for `httprouter` on the same host ([Benchmarks](#benchmarks))
- **100% `net/http` compatible** — `*Mux` is an `http.Handler`; handlers and middleware use the standard signatures
- **Zero external dependencies** — standard library only
- **Radix tree routing** — O(k) lookup, independent of the total number of registered routes
- **Path parameters** — named (`:id`), regex-constrained (`{id:[0-9]+}`), optional (`{/:id}`), and catch-all (`*filepath`)
- **Typed parameter helpers** — parse path parameters to `int`, `int64`, `uint64`, `float64`, `bool`
- **Ten HTTP methods** — GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, and QUERY ([RFC 10008](https://www.rfc-editor.org/rfc/rfc10008))
- **Three middleware families** — `Pre` (before routing, covers every route), `Use` (standard `func(http.Handler) http.Handler`), and `UseFast` (for `FastHandler` routes)
- **Groups, sub-groups, and `Mount`** — shared prefixes and middleware stacks; attach any `http.Handler` under a prefix
- **Error-returning handlers** — `HandlerFuncE` with a central `ErrorHandler`
- **FastHandler routes** — parameters passed as a direct argument instead of through the request context
- **21 middleware constructors** in the `middleware` package — logging, panic recovery, CORS, Basic Auth, API keys, JWT, OAuth 2.0 introspection, compression, throttling, timeouts, request IDs, and more
- **Route introspection** — `Lookup`, `Routes`, `Walk`, and `WalkFast`

## What's new in v1.2.0

These changes shipped in v1.2.0 (2026-09-26); the full list, with rationale and tests, is in [CHANGELOG.md](CHANGELOG.md#120---2026-09-26), and the upgrade notes are in [release-notes/v1.2.0-20260926.md](release-notes/v1.2.0-20260926.md#upgrade-notes).

- **HTTP QUERY method (RFC 10008)** — `MethodQuery`, `Mux.QUERY`, `Mux.QUERYE`, `Mux.QUERYFast`, `Group.QUERY`, `Group.QUERYE`; `ANY` now also registers QUERY, and the `Allow` header lists it.
- **Mount** — an inner `*Mux`'s own redirects now keep the mount prefix in `Location`; `RawPath` is propagated through parameterised prefixes; a prefix ending in an optional parameter panics at registration.
- **Routing fixes** — a named or regex parameter no longer captures an empty segment (`//posts`); group prefixes no longer produce `//` at the join; an unclosed `{` panics instead of overwriting a route; parameter routes no longer panic on a request whose context is `nil`.
- **Security hardening** — redirect targets percent-encode `\` as `%5C` (WHATWG backslash-authority shape); `BasicAuth` checks credentials with a constant-time scan over all users; `CORS` always adds `Vary: Origin`; `Recoverer` no longer appends its 500 body to a response the handler had already started.
- **Performance** — registration is O(depth) instead of O(tree size), and several middleware lost allocations. Measured figures, including the costs some security fixes added, are in [Measured changes since v1.1.0](#measured-changes-since-v110) and [docs/performance.md](docs/performance.md).

## Contents

- [Installation](#installation)
- [Quick Start](#quick-start)
- [Route Syntax](#route-syntax)
- [Path Parameters](#path-parameters)
- [Middleware](#middleware)
- [Fast Routes](#fast-routes)
- [Maximum Performance Mode (Zero Allocations)](#maximum-performance-mode-zero-allocations)
- [Groups](#groups)
- [Mounting Sub-Routers](#mounting-sub-routers)
- [Static Files](#static-files)
- [Error Handling](#error-handling)
- [Response Helpers](#response-helpers)
- [Router Options](#router-options)
- [Included Middleware](#included-middleware)
- [Route Introspection](#route-introspection)
- [Benchmarks](#benchmarks)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [License](#license)

---

## Installation

```bash
go get github.com/FlavioCFOliveira/MuxMaster
```

**Requires Go 1.26 or later** (`go.mod` declares `go 1.26`).

---

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    "log/slog"
    "net/http"
    "os"

    "github.com/FlavioCFOliveira/MuxMaster"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
    mux := muxmaster.New()

    // Global middleware — wraps every route registered after these calls.
    mux.Use(middleware.Logger(os.Stdout))
    mux.Use(middleware.RecovererWithLogger(slog.Default()))

    mux.GET("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "Hello, World!")
    })

    // Named path parameter
    mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "user %s\n", muxmaster.PathParam(r, "id"))
    })

    // Versioned API group with its own middleware
    api := mux.Group("/api/v1")
    api.Use(requireToken)
    api.GET("/items", func(w http.ResponseWriter, r *http.Request) {
        _ = muxmaster.JSON(w, http.StatusOK, []string{"alpha", "beta"})
    })

    log.Fatal(http.ListenAndServe(":8080", mux))
}

// requireToken rejects requests without an Authorization header.
func requireToken(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") == "" {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

## Route Syntax

| Pattern                  | Example match              | Description                                         |
|--------------------------|----------------------------|-----------------------------------------------------|
| `/users`                 | `/users`                   | **Static segment** — exact match                    |
| `/users/:id`             | `/users/42`                | **Named parameter** — matches one non-empty segment |
| `/users/{id:[0-9]+}`     | `/users/42`                | **Regex parameter** — the segment must match        |
| `/users{/:id}`           | `/users` and `/users/42`   | **Optional parameter** — the segment may be absent  |
| `/files/*filepath`       | `/files/img/logo.png`      | **Catch-all** — matches the rest of the path        |

Rules:
- Named and regex parameters match exactly one path segment (no `/`) and never match an empty segment: `/users/:id/posts` does not match `//posts`.
- Catch-all parameters (`*name`) match the remainder of the path, including slashes, and must be the last element of the pattern.
- A pattern may contain at most 8 optional parameters, and two optional parameters may not be adjacent.
- When a static segment and a parameter compete at the same position, the static route wins; if the static branch fails further down the path, the router falls back to the parameter branch.
- Catch-all values are raw path suffixes. A handler that uses one as a file path must clean it (`http.FileServer`, `http.ServeContent`, or `filepath.Clean`).

The full rules are in [docs/routing.md](docs/routing.md).

### Registering routes

```go
mux.GET("/users", listUsers)
mux.POST("/users", createUser)
mux.PUT("/users/:id", updateUser)
mux.PATCH("/users/:id", patchUser)
mux.DELETE("/users/:id", deleteUser)
mux.HEAD("/users/:id", headUser)
mux.OPTIONS("/users", optionsUsers)
mux.QUERY("/books/search", searchBooks) // RFC 10008

// All ten methods at once: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY
mux.ANY("/health", healthCheck)

// A specific subset of methods (takes an http.Handler)
mux.Match([]string{http.MethodGet, http.MethodHead}, "/ping", http.HandlerFunc(ping))

// Low-level registration accepting any http.Handler
mux.Handle(http.MethodGet, "/users", http.HandlerFunc(listUsers))
```

MuxMaster recognises exactly these ten methods. Registering any other method string (for example `PURGE`) panics with `muxmaster: unsupported HTTP method '<method>'`. Registering GET does **not** register HEAD; register HEAD explicitly or use `Match`. The router does not validate the body or `Content-Type` of a QUERY request — that is the handler's responsibility (RFC 10008 §2).

---

## Path Parameters

### Reading a single parameter

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    fmt.Fprintf(w, "user: %s\n", id)
})
```

### Reading all parameters

```go
mux.GET("/posts/:year/:month/:slug", func(w http.ResponseWriter, r *http.Request) {
    ps := muxmaster.ParamsFromContext(r.Context())
    fmt.Fprintf(w, "%s/%s/%s\n", ps.Get("year"), ps.Get("month"), ps.Get("slug"))
})
```

### Typed helpers

`Params` provides helpers that parse a value into a Go type. They return an error if the parameter is absent or cannot be parsed:

```go
mux.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
    id, err := muxmaster.ParamsFromContext(r.Context()).Int("id")
    if err != nil {
        http.Error(w, "invalid id", http.StatusBadRequest)
        return
    }
    fmt.Fprintf(w, "item %d\n", id)
})
```

| Method               | Return type         | Notes                         |
|----------------------|---------------------|-------------------------------|
| `ps.Get("name")`     | `string`            | Returns `""` if not present   |
| `ps.Lookup("name")`  | `string, bool`      | Also reports presence         |
| `ps.Int("name")`     | `int, error`        |                               |
| `ps.Int64("name")`   | `int64, error`      |                               |
| `ps.Uint64("name")`  | `uint64, error`     |                               |
| `ps.Float64("name")` | `float64, error`    |                               |
| `ps.Bool("name")`    | `bool, error`       |                               |
| `ps.Map()`           | `map[string]string` | All parameters as a map       |

### Catch-all parameters

```go
mux.GET("/files/*filepath", func(w http.ResponseWriter, r *http.Request) {
    // For /files/img/logo.png, filepath == "/img/logo.png"
    fmt.Fprintln(w, muxmaster.PathParam(r, "filepath"))
})
```

### Regex-constrained parameters

```go
// Matches /users/42 and /users/100, not /users/abc
mux.GET("/users/{id:[0-9]+}", func(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintln(w, muxmaster.PathParam(r, "id"))
})
```

### Reading parameters in middleware

Parameters are stored in the request context, so middleware registered with `Use` can read them:

```go
func auditMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Printf("params: %v", muxmaster.ParamsFromContext(r.Context()))
        next.ServeHTTP(w, r)
    })
}
```

`Pre` middleware runs before routing, so it never sees path parameters.

### The Param type

Each path parameter is a `Param` struct with `Key` and `Value` string fields; `Params` is a `[]Param`:

```go
for _, p := range muxmaster.ParamsFromContext(r.Context()) {
    fmt.Printf("%s=%s\n", p.Key, p.Value)
}
```

### Route pattern

`RoutePattern` returns the registered pattern that matched a parameterised route (for example `/users/:id`). It returns `""` when no pattern is stored in the request context: for static routes (which skip the context allocation entirely), and for `NotFound`, `MethodNotAllowed`, redirect, and automatic OPTIONS responses.

```go
func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        next.ServeHTTP(w, r)
        log.Printf("%s %s matched %q", r.Method, r.URL.Path, muxmaster.RoutePattern(r))
    })
}
```

---

## Middleware

Middleware has the signature `func(http.Handler) http.Handler`. `Use` wraps routes **at registration time**, so call it **before** registering the routes it should wrap; routes registered earlier are not wrapped.

### Global middleware

```go
mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout))                    // outermost
mux.Use(middleware.RecovererWithLogger(slog.Default()))  // inner

mux.GET("/users", listUsers) // wrapped by both
```

`Use` middleware also wraps the `NotFound`, `MethodNotAllowed`, automatic OPTIONS, and redirect responses.

### Pre-routing middleware

`Pre` middleware runs in `ServeHTTP` **before** route matching, on every request. Use it for path rewriting and for policy that must cover every route type:

```go
mux.Pre(middleware.CleanPath())
mux.Pre(middleware.StripSlashes())
```

### Which middleware wraps which route type

| Middleware family | Wraps `Handle` routes | Wraps `HandleFast` routes |
|---|---|---|
| `mux.Pre(...)` | Yes | Yes |
| `mux.Use(...)` (`func(http.Handler) http.Handler`) | Yes | No — registering a fast route after `Use` panics |
| `mux.UseFast(...)` (`FastMiddleware`) | No | Yes |

An authentication gate that must protect fast routes belongs in `Pre`. See [SECURITY.md](SECURITY.md) ("Pre vs Use security boundary").

### Per-route middleware with `With`

`With` returns a `*Group` that carries the extra middleware. Routes registered through that returned value are wrapped by it; the router or group `With` was called on is unchanged:

```go
mux.With(requireAdmin).DELETE("/users/:id", deleteUser)
mux.With(rateLimit, audit).POST("/payments", processPayment)
```

### Writing custom middleware

```go
func requireAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !isValid(r.Header.Get("Authorization")) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}

mux.Use(requireAuth)
```

---

## Fast Routes

`FastHandler` receives path parameters as a third argument instead of through the request context, so a parameterised fast route allocates only the parameter slice (32, 64 or 96 B for 1, 2 or 3 parameters) instead of the 384–480 B request bundle:

```go
type FastHandler func(http.ResponseWriter, *http.Request, Params)
```

By default the `Params` slice is freshly allocated and may be retained after the handler returns. With `Mux.PoolFastParams = true` it is recycled when the handler returns; in that mode, copy it before handing it to a goroutine:

```go
func myFast(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    ps2 := make(muxmaster.Params, len(ps))
    copy(ps2, ps)
    go process(ps2)
}
```

### Registering fast routes

```go
mux := muxmaster.New()

mux.GETFast("/api/v1/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    fmt.Fprintf(w, "user: %s\n", ps.Get("id"))
})
mux.POSTFast("/api/v1/items", createItemFast)
mux.DELETEFast("/api/v1/items/:id", deleteItemFast)

// Any method
mux.HandleFast(http.MethodGet, "/files/*filepath", serveFilesFast)
```

`*Mux` methods: `GETFast`, `HEADFast`, `POSTFast`, `PUTFast`, `PATCHFast`, `DELETEFast`, `OPTIONSFast`, `CONNECTFast`, `TRACEFast`, `QUERYFast`, and `HandleFast`.

### Fast middleware

Fast routes do not run `Use` middleware. Use `FastMiddleware` with `UseFast`, called before the fast routes it should wrap:

```go
type FastMiddleware func(FastHandler) FastHandler

func loggingFast(next muxmaster.FastHandler) muxmaster.FastHandler {
    return func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
        log.Printf("%s %s", r.Method, r.URL.Path)
        next(w, r, ps)
    }
}

mux.UseFast(loggingFast)
mux.GETFast("/api/status", statusFast)
```

### Fast routes in groups

`Group` has `HandleFast` and `UseFast`, but no per-method fast shorthands. `Group.HandleFast` panics if the group has `Use` middleware:

```go
api := mux.Group("/api/v1")
api.UseFast(loggingFast)

api.HandleFast(http.MethodGet, "/users/:id", getUserFast)
api.HandleFast(http.MethodPost, "/users", createUserFast)
```

### Costs and trade-offs

Measured on 2026-09-26 (AMD Ryzen 9 5900HX, Go 1.27.0, `competitor/` suite; see [Benchmarks](#benchmarks)):

| Route | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static, `http.Handler` | 29.6 | 0 | 0 |
| Static, `FastHandler` | 30.6 | 0 | 0 |
| 1 parameter, `http.Handler` (default) | 116.9 | 384 | 1 |
| 1 parameter, `FastHandler` (default) | 47.8 | 32 | 1 |
| 1 parameter, `http.Handler` + `PoolRequestBundle` | 46.7 | 0 | 0 |

`FastHandler` + `PoolFastParams` removes the remaining 32 B allocation; it was not part of the 2026-09-26 run. The 2026-09-24 scaling measurement is in the [Maximum Performance Guide](docs/max-performance.md#high-concurrency-scaling).

Trade-offs:
- `Use` middleware does not apply — use `FastMiddleware`, or `Pre` for policy that must cover all routes.
- Parameters are not in the request context, so `PathParam`, `ParamsFromContext` and `RoutePattern` do not see them.
- `Lookup` reports a fast route as found but returns a `nil` `http.Handler`; `Walk` skips fast routes — use `WalkFast`.

---

## Maximum Performance Mode (Zero Allocations)

Two opt-in switches recycle the per-request objects through `sync.Pool`:

```go
mux := muxmaster.New()
mux.PoolRequestBundle = true // Handle routes with parameters: 0 allocs (Opt O13)
mux.PoolFastParams    = true // HandleFast routes with 1-3 parameters: 0 allocs (Opt O9)
```

Both default to `false`. They require a stricter handler lifetime contract: **handlers must not retain `*http.Request` (or, on fast routes, the `Params` slice) after they return**. A goroutine that keeps `r` observes a zeroed or reissued bundle belonging to another request — a use-after-free against the pool storage. Handlers that proxy through `net/http.Transport` (for example `httputil.ReverseProxy`) are not pool-safe.

Static routes never allocate a bundle, so the switches do not change them. The measured effect on parameterised routes is in [Benchmarks](#benchmarks); the audit checklist, recipes and a runnable example are in the **[Maximum Performance Guide](docs/max-performance.md)** and [`examples/max-performance/`](examples/max-performance/).

---

## Groups

Groups share a path prefix and a middleware stack. Every route registered on a group is prefixed with the group's path and wrapped by the group's middleware, inside any `Use` middleware of the `*Mux`.

### Basic group

```go
api := mux.Group("/api/v1")
api.Use(requireAPIKey)

api.GET("/users", listUsers)   // GET /api/v1/users
api.POST("/users", createUser) // POST /api/v1/users
```

### Nested groups

A sub-group starts with a copy of its parent's middleware:

```go
admin := api.Group("/admin")
admin.Use(requireAdmin)
admin.DELETE("/users/:id", deleteUser) // DELETE /api/v1/admin/users/:id
```

When a prefix ends in `/` and the path begins with `/`, one slash is dropped: `mux.Group("/api/")` plus `"/users"` registers `/api/users`.

### Inline groups with `Route`

```go
mux.Route("/api/v1", func(api *muxmaster.Group) {
    api.Use(requireAPIKey)
    api.GET("/users", listUsers)

    api.Route("/admin", func(admin *muxmaster.Group) {
        admin.Use(requireAdmin)
        admin.DELETE("/users/:id", deleteUser)
    })
})
```

### Scoped middleware with `With`

```go
api.With(requireAdmin).DELETE("/users/:id", deleteUser)
api.With(throttle).POST("/exports", exportData)
```

### Multiple methods on a group

```go
api.Match([]string{http.MethodGet, http.MethodHead}, "/status", http.HandlerFunc(status))
```

Groups also provide `ANY`, the `...E` error-returning variants, `Mount`, and `ServeFiles`.

---

## Mounting Sub-Routers

`Mount` attaches any `http.Handler` — including another `*muxmaster.Mux` — under a prefix, for every method. The prefix is stripped before the request is forwarded:

```go
v2 := muxmaster.New()
v2.GET("/items", listItemsV2)
v2.POST("/items", createItemV2)

mux.Mount("/v2", v2) // GET /v2/items → listItemsV2, which sees r.URL.Path == "/items"
```

Behaviour to know:
- The mounted handler receives a **shallow copy** of the request: a new `*http.Request` with a new `*url.URL`, sharing the original's header map, trailer, form and context. Read headers freely, but do not mutate them in place.
- When the mounted handler is a `*muxmaster.Mux`, its own trailing-slash and fixed-path redirects keep the mount prefix in `Location`.
- Internally, `Mount("/v2", h)` registers the pattern `/v2/*mux_mount` under the method token `"*"`, which is how it appears in `Routes()` and `Walk`.
- A request to the bare prefix (`/v2`) is redirected to `/v2/` when `RedirectTrailingSlash` is enabled.
- A prefix ending in an optional parameter (`{/:name}`) panics at registration.
- `Group.Mount` wraps the mounted handler with the group's `Use` middleware.

---

## Static Files

`ServeFiles` serves files from an `http.FileSystem` for GET and HEAD. The pattern must end with `/*name`:

```go
// ./public/css/main.css is served at /static/css/main.css
mux.ServeFiles("/static/*filepath", http.Dir("./public"))
```

```go
//go:embed public
var publicFS embed.FS

mux.ServeFiles("/assets/*filepath", http.FS(publicFS))
```

`ServeFiles` delegates to `http.FileServer`, which cleans the path before opening files. `Mux.ServeFiles` panics at registration when `UseRawPath` and `UnescapePathValues` are both `true`, because the decoded catch-all value could then contain `/` separators. `Group.ServeFiles` provides the same routing with the group prefix and middleware.

---

## Error Handling

### Error-returning handlers

`HandlerFuncE` adds an `error` return value to the handler signature:

```go
mux.GETE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    id, err := muxmaster.ParamsFromContext(r.Context()).Int("id")
    if err != nil {
        return muxmaster.Error(http.StatusBadRequest, err)
    }
    user, err := db.FindUser(id)
    if err != nil {
        return muxmaster.Error(http.StatusNotFound, err)
    }
    return muxmaster.JSON(w, http.StatusOK, user)
})
```

Error-returning variants: `GETE`, `HEADE`, `POSTE`, `PUTE`, `PATCHE`, `DELETEE`, `OPTIONSE`, `QUERYE`, on both `*Mux` and `*Group`. Use `HandleE` for `CONNECT` and `TRACE`.

### The `HTTPError` interface

`HTTPError` is an `error` with a `StatusCode() int` method. `muxmaster.Error(code, err)` constructs one and panics if `err` is `nil`.

```go
err := muxmaster.Error(http.StatusNotFound, errors.New("user not found"))
```

### Custom error handler

Without an `ErrorHandler`, a returned error produces `500 Internal Server Error`. Set `ErrorHandler` to handle errors centrally:

```go
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        _ = muxmaster.JSON(w, he.StatusCode(), map[string]string{"error": err.Error()})
        return
    }
    log.Printf("unexpected error: %v", err)
    _ = muxmaster.JSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
```

### Custom 404 and 405 handlers

```go
mux.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    _ = muxmaster.JSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
})

mux.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    _ = muxmaster.JSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
})
```

The router sets the `Allow` header before calling `MethodNotAllowed`. Methods are listed in the order GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, OPTIONS.

### Panic recovery

`PanicHandler` receives the recovered value. It must not panic itself: a second panic is not recovered by MuxMaster and terminates the connection.

```go
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
    log.Printf("panic: %v\n%s", rcv, debug.Stack())
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

### Custom OPTIONS handler

When `HandleOPTIONS` is `true` (the default), an OPTIONS request to a path with no explicit OPTIONS route receives `204 No Content` with an `Allow` header. `GlobalOPTIONS` replaces that response; the `Allow` header is set before it runs:

```go
mux.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Origin", "https://app.example.com")
    w.WriteHeader(http.StatusNoContent)
})
```

---

## Response Helpers

`JSON`, `XML` and `Text` set `Content-Type` (`application/json`, `application/xml`, `text/plain`, each with `charset=utf-8`), write the status code, and return an error. A status code of `0` means `200`.

```go
_ = muxmaster.JSON(w, http.StatusOK, map[string]any{"id": 42, "name": "Alice"})

_ = muxmaster.XML(w, http.StatusOK, struct {
    XMLName xml.Name `xml:"user"`
    Name    string   `xml:"name"`
}{Name: "Alice"})

_ = muxmaster.Text(w, http.StatusOK, "pong")

muxmaster.Redirect(w, r, http.StatusMovedPermanently, "/new-path") // wraps http.Redirect
muxmaster.NoContent(w)                                             // 204
```

In an error-returning handler:

```go
mux.POSTE("/users", func(w http.ResponseWriter, r *http.Request) error {
    var payload CreateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
        return muxmaster.Error(http.StatusBadRequest, err)
    }
    user, err := db.CreateUser(payload)
    if err != nil {
        return err
    }
    return muxmaster.JSON(w, http.StatusCreated, user)
})
```

---

## Router Options

Options are fields on `*Mux`. Set them after `New()` and before the first request. The values below are the defaults `New()` sets:

```go
mux := muxmaster.New()

// Redirect /foo/ → /foo (or /foo → /foo/) when only the alternate path has a handler.
mux.RedirectTrailingSlash = true

// Redirect to path.Clean(path) (for example /a/../b → /b) when the cleaned path has a handler.
// Off by default: canonicalising the path can let a request bypass middleware that
// inspects the original path. See SECURITY.md before enabling it.
mux.RedirectFixedPath = false

// Answer 405 Method Not Allowed, with an Allow header, when the path exists for other methods.
// When false, such requests receive 404.
mux.HandleMethodNotAllowed = true

// Answer OPTIONS automatically with 204 and an Allow header.
mux.HandleOPTIONS = true

// Match static segments case-insensitively (no redirect). Parameter values keep their case.
mux.CaseInsensitive = false

// Match against r.URL.RawPath when it is non-empty (for values containing %2F).
mux.UseRawPath = false

// Percent-decode parameter values. Takes effect only together with UseRawPath:
// net/http already decodes r.URL.Path, and a second decode would corrupt values.
// Enabling both logs a warning — decoded values may contain "/" (see SECURITY.md).
mux.UnescapePathValues = false

// Redirect status code. 0 means 301 for GET and HEAD, 307 for every other method.
mux.RedirectCode = 0

// Opt-in pools with a strict handler lifetime contract (see Maximum Performance Mode).
mux.PoolRequestBundle = false
mux.PoolFastParams = false
```

A zero-value `Mux{}` has every flag `false`; use `New()`.

### Resetting configuration

The option fields and the `NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `ErrorHandler` and `PanicHandler` handlers are frozen into a snapshot on the first `ServeHTTP` call. To apply a change made afterwards, call `Rebuild()`:

```go
mux.RedirectTrailingSlash = false
mux.Rebuild()
```

`Rebuild` is safe to call concurrently with `ServeHTTP`: each request sees either the old or the new snapshot, never a mix. It does not add or remove routes; registering routes after the server has started serving is not supported.

---

## Included Middleware

The `middleware` package provides 21 constructors in 17 source files:

```go
import "github.com/FlavioCFOliveira/MuxMaster/middleware"
```

### Overview

| Constructor | Description |
|---|---|
| `Logger(out io.Writer)` | One line per request: RFC 3339 timestamp, method, path, status, duration |
| `RecovererWithLogger(l *slog.Logger)` | Recovers panics, logs the value and stack, writes 500 only if the response has not started |
| `Recoverer()` | **Deprecated** — equivalent to `RecovererWithLogger(slog.Default())` |
| `CORS(CORSOptions)` | Cross-Origin Resource Sharing; always adds `Vary: Origin`; panics on an empty `AllowedOrigins` |
| `BasicAuth(realm, creds)` | HTTP Basic Authentication; constant-time scan over all users (cost grows with user count) |
| `APIKey(APIKeyOptions)` | API key authentication; keys are SHA-256 hashed at construction |
| `JWTAuth(JWTOptions)` | JWT Bearer validation (HS256/384/512, RS256/384/512, ES256/384/512) |
| `OAuth2Introspect(OAuth2Options)` | RFC 7662 token introspection with an expiry-bounded cache |
| `Compress(level int)` | gzip response compression |
| `ThrottleAllBacklog(limit, backlog, timeout)` | Global concurrency limit with a backlog queue; 503 on timeout |
| `ThrottleBacklog(limit, backlog, timeout)` | Same behaviour as `ThrottleAllBacklog` (original name) |
| `ThrottlePerIP(limit, timeout, keyFn)` | Per-client concurrency limit (key defaults to `r.RemoteAddr`); tracks up to `DefaultThrottlePerIPMaxTableSize` (100 000) clients |
| `ThrottlePerIPCapped(limit, timeout, maxTableSize, keyFn)` | `ThrottlePerIP` with an explicit client-table cap |
| `Timeout(d)` | Request deadline via `context.WithTimeout` |
| `RequestID()` | Propagates a valid inbound `X-Request-ID` or generates one; read it with `GetRequestID` |
| `RealIP(trusted ...*netip.Prefix)` | Rewrites `r.RemoteAddr` from `X-Forwarded-For` / `X-Real-IP` sent by trusted proxies |
| `CleanPath()` | Normalises the path with `path.Clean` (double slashes, dot segments) |
| `StripSlashes()` | Removes trailing slashes before routing |
| `NoCache()` | Sets `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma`, `Expires`, `Surrogate-Control`, `X-Accel-Expires` |
| `SetHeader(key, value)` | Sets a response header |
| `WithValue(key, val)` | Stores a value in the request context |

### Usage examples

```go
mux.Use(middleware.Logger(os.Stdout))
mux.Use(middleware.RecovererWithLogger(slog.Default()))

mux.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins:   []string{"https://app.example.com"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Authorization", "Content-Type"},
    AllowCredentials: true,
    MaxAge:           86400,
}))

mux.Use(middleware.BasicAuth("realm", map[string]string{"admin": "secret"}))
mux.Use(middleware.Compress(5))

// At most 100 concurrent requests, up to 50 queued, 503 after 30 s in the queue.
mux.Use(middleware.ThrottleAllBacklog(100, 50, 30*time.Second))

mux.Use(middleware.Timeout(10 * time.Second))
mux.Use(middleware.RequestID())

// Trust X-Forwarded-For / X-Real-IP only from a known reverse proxy (TM-2026-044).
proxyCIDR := netip.MustParsePrefix("10.0.0.0/8")
mux.Use(middleware.RealIP(&proxyCIDR))

mux.Use(middleware.SetHeader("X-Content-Type-Options", "nosniff"))
mux.Use(middleware.WithValue(envKey{}, "production"))
```

### Authentication middleware

#### API key

```go
mux.Use(middleware.APIKey(middleware.APIKeyOptions{
    Keys: map[string]string{
        "sk_test_abc123": "user-123", // raw key → identity
        "sk_test_def456": "user-456",
    },
    Header: "X-API-Key", // the default
}))

mux.GET("/api/data", func(w http.ResponseWriter, r *http.Request) {
    identity, _ := middleware.GetAPIKeyIdentity(r.Context())
    fmt.Fprintf(w, "authenticated as %s\n", identity)
})
```

#### JWT Bearer token

```go
// pubKey is an *ecdsa.PublicKey or *rsa.PublicKey you load yourself
// (for example with crypto/x509.ParsePKIXPublicKey).
mux.Use(middleware.JWTAuth(middleware.JWTOptions{
    PublicKey:     pubKey,
    Algorithms:    []string{"ES256"},
    Issuers:       []string{"https://auth.example.com"},
    Audiences:     []string{"https://api.example.com"},
    ClockSkew:     5 * time.Second,
    RequireExpiry: true, // RFC 8725 §4.4 — reject tokens without "exp"
}))

mux.GET("/api/profile", func(w http.ResponseWriter, r *http.Request) {
    claims, _ := middleware.GetJWTClaims(r.Context())
    fmt.Fprintf(w, "user: %s\n", claims.Subject)
})
```

Configure one algorithm family per endpoint: mixing HMAC and RSA/ECDSA algorithms leaks the verification path through response latency, and `JWTAuth` logs a warning when you do.

#### OAuth 2.0 token introspection

```go
mux.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
    Endpoint:     "https://idp.example.com/oauth/introspect", // must be HTTPS
    ClientID:     "my_service",
    ClientSecret: os.Getenv("OAUTH2_SECRET"),
    CacheTTL:     60 * time.Second, // 0 = default 60 s; negative disables the cache
    MaxCacheSize: 10000,
}))

mux.GET("/api/resource", func(w http.ResponseWriter, r *http.Request) {
    resp, _ := middleware.GetOAuth2Claims(r.Context())
    fmt.Fprintf(w, "scope: %s\n", resp.Scope)
})
```

### Security defaults

Three middleware keep backwards-compatible defaults that are **unsafe in production**. Each logs a `slog.Warn` at construction time when the unsafe setting is in effect. Full discussion is in [SECURITY.md](SECURITY.md).

| Middleware         | Unsafe setting                                                  | Safe production setting                                  | Finding ID    |
|--------------------|-----------------------------------------------------------------|----------------------------------------------------------|---------------|
| `JWTAuth`          | `RequireExpiry: false` (the default) accepts tokens without `exp` | `RequireExpiry: true` (RFC 8725 §4.4)                    | TM-2026-001   |
| `RealIP()`         | No trusted CIDRs: every peer may spoof `X-Forwarded-For` / `X-Real-IP` | `RealIP(&proxyCIDR)` with the trusted proxy ranges | TM-2026-044   |
| `OAuth2Introspect` | `AllowInsecureEndpoint: true` sends bearer tokens over plaintext | Leave `AllowInsecureEndpoint: false` (the default); HTTPS only | MSR-2026-0067 |

The hardened pattern, as used by `examples/jwt`, `examples/oauth2` and `examples/authn`:

```go
trusted := netip.MustParsePrefix("10.0.0.0/8")

// Pre runs before dispatch — it covers Handle and HandleFast routes.
mux.Pre(middleware.RealIP(&trusted))

// Use wraps Handle routes registered after this call.
mux.Use(
    middleware.ThrottlePerIP(100, 5*time.Second, nil),
    middleware.JWTAuth(middleware.JWTOptions{
        Secret:        secret,
        Algorithms:    []string{"HS256"},
        RequireExpiry: true,
    }),
)
```

---

## Route Introspection

`Lookup`, `Routes`, `Walk` and `WalkFast` take the registration read lock; they never block request dispatch, which is lock-free.

### Check whether a route is registered

```go
handler, params, found := mux.Lookup(http.MethodGet, "/users/42")
if found {
    fmt.Printf("found, %d params\n", len(params))
}
```

`Lookup` covers both route types. For a `HandleFast` route it returns `found == true`, the parameters, and a `nil` handler. It performs no redirects and calls no `NotFound` or `MethodNotAllowed` handler.

### List all registered routes

```go
for _, route := range mux.Routes() {
    fmt.Printf("%-8s %s → %s\n", route.Method, route.Pattern, route.Handler)
}
```

`Routes` includes fast routes and mount points (method `"*"`, pattern `<prefix>/*mux_mount`).

### Iterate routes with a callback

`Walk` visits every `http.Handler` route, including mount points, and skips fast routes; `WalkFast` visits only fast routes. Returning a non-nil error stops the walk.

```go
err := mux.Walk(func(method, pattern string, handler http.Handler) error {
    fmt.Printf("%s %s\n", method, pattern)
    return nil
})

err = mux.WalkFast(func(method, pattern string, handler muxmaster.FastHandler) error {
    fmt.Printf("%s %s (FastHandler)\n", method, pattern)
    return nil
})
```

---

## Benchmarks

All current figures come from one host and one session: **AMD Ryzen 9 5900HX (8 cores / 16 threads), Linux 6.8, Go 1.27.0, 2026-09-26, `-count=3`**, CPU governor `powersave`. With three samples, `benchstat` cannot test significance, so treat small differences as noise. Raw data, commands and caveats: [`reports/perf-lab-2026-09-26-docs/`](reports/perf-lab-2026-09-26-docs/README.md).

### Routing hot path versus other routers

`competitor/` suite; ns/op, allocs/op in parentheses. "Pooled" is `PoolRequestBundle = true`; "Fast" is `HandleFast` without `PoolFastParams`.

| Route type        | MuxMaster default | MuxMaster Pooled | MuxMaster Fast | httprouter | bunrouter¹ | chi v5    | gorilla/mux |
|-------------------|-------------------|------------------|----------------|------------|------------|-----------|-------------|
| Static            | **29.6 (0)**      | 30.7 (0)         | 30.6 (0)       | 34.7 (0)   | 168.1 (3)  | 217.9 (2) | 578.9 (7)   |
| 1 parameter       | 116.9 (1)         | **46.7 (0)**     | 47.8 (1)       | 50.5 (1)   | 160.3 (3)  | 360.7 (4) | 954.7 (8)   |
| 2 parameters      | 131.9 (1)         | 60.9 (0)         | 67.8 (1)       | **60.2 (1)** | 181.6 (3) | 405.0 (4) | 1 497 (8)  |
| 3 parameters      | 144.4 (1)         | **67.6 (0)**     | 83.0 (1)       | 74.4 (1)   | 179.9 (3)  | 415.5 (4) | 1 729 (8)   |
| Catch-all         | 116.4 (1)         | 46.5 (0)         | 48.6 (1)       | **42.8 (1)** | 152.5 (3) | 334.2 (4) | 1 611 (8)  |
| Not found         | **254.7 (3)**     | —                | —              | 398.2 (3)  | 275.5 (4)  | 346.5 (5) | 1 034 (4)   |
| Parallel static   | **4.51 (0)**      | —²               | 4.55 (0)       | 4.91 (0)   | 126.1 (3)  | 133.8 (2) | 345.6 (7)   |
| Parallel 1 param  | 102.3 (1)         | **7.19 (0)**     | 17.2 (1)       | 21.8 (1)   | 126.1 (3)  | 232.3 (4) | 456.8 (8)   |

¹ bunrouter measured through its `http.Handler` adapter, which stores parameters with `context.WithValue`. Its native `bunrouter.HandlerFunc` API allocates nothing but is not `net/http`-compatible and was not measured here.
² Not measured. Static routes never allocate a request bundle, so pooling does not change them.

Reading the table:
- MuxMaster is the fastest of the measured routers on static, not-found and parallel-static routes.
- In the default mode, parameterised routes are about 2–2.7× slower than httprouter serially and 4.7× slower on the parallel parameter benchmark, because each request copies `*http.Request` into a 384–480 B GC-managed bundle; in exchange, handlers may keep `r` indefinitely.
- With `PoolRequestBundle`, parameterised routes allocate nothing. Pooled MuxMaster is faster than httprouter on 1 and 3 parameters and on parallel parameter routes, level on 2 parameters, and slower on catch-all.
- httprouter's allocation is only its `Params` slice (32–96 B), and its handlers take a third argument instead of the plain `http.Handler` signature.

### Compared with v1.1.0

The 18 routing benchmarks shared with v1.1.0 have **identical B/op and allocs/op** at HEAD. None of the ns/op deltas is statistically significant at n=3. In v1.1.0's `ParamRoute2` and `ParamRoute3`, two of the three samples read 1.5–1.7 µs, roughly ten times every other sample in either version; this is a host perturbation during that window, not a property of v1.1.0, and it makes the geometric-mean line of that comparison meaningless. Details: [report §1](reports/perf-lab-2026-09-26-docs/README.md#1-root-package-v110-vs-head-hot-path-cases).

### Measured changes since v1.1.0

**Re-verified on 2026-09-26** (sprint 18 waste-hunt claims, re-run against HEAD; [report §4](reports/perf-lab-2026-09-26-docs/README.md#4-sprint-18-waste-hunt-campaign-claim-verification)):

| Change | Before (2026-09-24) | HEAD (2026-09-26) |
|---|---|---|
| Route registration, 5 000 routes (O(depth) copy-on-write) | 2.86 s | 4.70 ms, 939.6 ns/route |
| Route registration, 1 000 routes | 90.6 ms | 693 µs |
| `Mount`, static prefix (shallow request copy) | 1 024 ns, 9 allocs | 237.5 ns, 2 allocs |
| `CleanPath`, dirty path (shallow request copy) | 810 ns, 7 allocs | 145.4 ns, 2 allocs |
| Trailing-slash redirect, no middleware | 884 ns | 641.3 ns, 10 allocs |
| Trailing-slash redirect, 5-middleware chain | 1 050 ns, 19 allocs | 760.7 ns, 11 allocs |
| `ThrottlePerIP`, one client, one core | 4 051 ns, 5 allocs | 115.0 ns, 0 allocs |
| `Logger` | 7.5 µs, 5 allocs | 5.67 µs, 0 allocs |
| `Compress`, 600 B body | 435 ns, 4 allocs | 316.6 ns, 2 allocs |
| `Compress`, chunked 12 KiB | 14.6 µs, 16 allocs | 7.77 µs, 3 allocs |
| `JWTAuth`, HS256 | 5.16 µs, 10 allocs | 4.35 µs, 7 allocs |
| `RealIP`, 3-hop `X-Forwarded-For` | 257 ns, 2 allocs | 178.7 ns, 1 alloc |
| `APIKey`, hit | 574 ns, 7 allocs | 453.3 ns, 6 allocs |
| 405 Method Not Allowed | 153 ns, 2 allocs | 109.6 ns, 1 alloc |
| Automatic OPTIONS | 124 ns, 3 allocs | 61.9 ns, 1 alloc |
| `Text` response helper | 98 ns, 2 allocs | 60.4 ns, 1 alloc |

**Measured on 2026-09-24 with `-cpu` scaling, not re-measured on 2026-09-26** (sprint 18 contention hunt; [`reports/perf-lab-2026-09-24/contention-hunt.md`](reports/perf-lab-2026-09-24/contention-hunt.md)):

- `ThrottlePerIP`, many clients at 16 CPUs (64-way sharded table): 2 114 ns → 451 ns (4.68×); no longer slows down above 4 CPUs.
- `ThrottleBacklog` lock-free fast path: 39.12 → 22.76 ns at 1 CPU (−42%), 76.15 → 66.23 ns at 16 CPUs (−13%).
- `RequestID`: 7 → 2 allocations per request; about 4.7× faster at 1 CPU, 2.8× at 4 CPUs, 1.95× at 16 CPUs.
- `OAuth2Introspect` eviction at a full cache (min-heap instead of a full scan): 257.4 µs → 2.15 µs at 16 CPUs.
- Redirects read the `Use` middleware snapshot lock-free; no measurable ns/op change.

**Costs added by security fixes** (measured with `-count=10` when each fix landed):

- `BasicAuth` scans every registered user in constant time (TSC-2026-0002), so cost grows with the user count: a successful check takes about 320 ns with 1 user, 550 ns with 10 and 2.8 µs with 100 (2026-09-26 run).
- `CORS` always adds `Vary: Origin` (TM-2026-033): a request without an `Origin` header went from 25.7 ns / 0 allocs to 71.0 ns / 1 alloc (112 B).
- `Recoverer` tracks whether the response has started (O-14): the no-panic path went from 8.4 ns to 19.4 ns, still 0 allocs.

### Historical: Apple M4 (2026-05-12, v1.1.0-era code, Go 1.26.2)

| Route type        | MuxMaster default | MuxMaster Pooled | httprouter  | chi v5     |
|-------------------|-------------------|------------------|-------------|------------|
| Static            | 14 ns, 0 allocs   | 14 ns, 0 allocs  | 14.7 ns, 0  | 114 ns, 2  |
| 1 parameter       | 57 ns, 1 alloc    | 28 ns, 0 allocs  | 33.0 ns, 1  | 196 ns, 4  |
| Catch-all         | 58 ns, 1 alloc    | 29 ns, 0 allocs  | 27.3 ns, 1  | 175 ns, 4  |
| Parallel 1 param  | 112 ns, 1 alloc   | 10.2 ns, 0 allocs | 18.9 ns, 1 | 188 ns, 4  |

Source: [`reports/apple-m4-benchmarks-2026-05-12.md`](reports/apple-m4-benchmarks-2026-05-12.md). Not re-measured on current code.

### Running the benchmarks

```bash
make bench   # root and middleware packages (excludes reports/ and competitor/)

# Competitor suite (separate module with vendored dependencies)
cd competitor && go test -mod=mod -run='^$' -bench=. -benchmem -count=3 .
```

`go test -bench=. ./...` from the repository root also runs the audit harnesses under `reports/`, which belong to the same module. Compare runs with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat), using at least `-count=6` if you need confidence intervals.

---

## Documentation

Extended documentation is in the [`docs/`](docs/) directory:

| Guide | Description |
|-------|-------------|
| [Getting Started](docs/getting-started.md) | Step-by-step guide for building your first application |
| [Routing](docs/routing.md) | Complete routing reference — syntax, methods, patterns |
| [Middleware](docs/middleware.md) | Writing and composing middleware; the built-in middleware reference |
| [Groups](docs/groups.md) | Organising routes with groups and sub-routers |
| [Error Handling](docs/error-handling.md) | Centralised error handling patterns |
| [Configuration](docs/configuration.md) | All router options with defaults and examples |
| [Response Helpers](docs/response-helpers.md) | JSON, XML, Text, Redirect, NoContent |
| [Performance](docs/performance.md) | How MuxMaster minimises allocations, and the measured results |
| [Maximum Performance Guide](docs/max-performance.md) | `PoolRequestBundle`, `PoolFastParams` and `HandleFast`: the zero-allocation setup and its lifetime contract |
| [Observability](docs/observability.md) | Logging, request correlation, metrics, tracing, health checks, pprof |
| [Migration Guide](docs/migration.md) | Migrating from gorilla/mux, chi, and httprouter |
| [Cookbook](docs/cookbook.md) | Common patterns and production recipes |

The full API reference is on [pkg.go.dev](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster). Security guidance is in [SECURITY.md](SECURITY.md).

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

In brief:
1. Fork the repository and create a feature branch.
2. Run `go test -race ./...`, `go vet ./...` and `golangci-lint run` before pushing.
3. Open a pull request against `main`.

---

## License

[MIT](LICENSE) — © 2026 Flavio CF Oliveira
