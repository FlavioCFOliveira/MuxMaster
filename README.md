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

**MuxMaster** is a high-performance HTTP router for Go. It is 100% compatible with the standard `net/http` package, requires zero external dependencies, and is built on a radix tree (compressed prefix trie) that delivers O(k) route lookup — where k is the length of the URL path, not the number of registered routes.

The hot path allocates **zero bytes** for static routes and makes a **single tiered allocation** (416–480 B) for parameterized routes — fusing the request context and parameters in one GC-class-aligned object — while preserving a familiar, idiomatic Go API.

## Why MuxMaster?

- **Zero allocations for static routes** — parameterized routes use a single tiered allocation that fuses request context and parameters, minimising allocator pressure
- **100% `net/http` compatible** — drop in anywhere `http.Handler` is accepted; works with all existing middleware
- **Zero external dependencies** — pure standard library; no dependency bloat
- **Radix tree routing** — O(k) lookup, independent of the total number of registered routes
- **Path parameters** — named (`:id`), regex-constrained (`{id:[0-9]+}`), and catch-all (`*filepath`)
- **Typed parameter helpers** — parse path parameters directly to `int`, `int64`, `float64`, `bool`
- **Middleware scopes** — apply middleware globally, to a group, or to a single route
- **Groups and sub-groups** — organize routes with shared path prefixes and middleware stacks
- **Error-returning handlers** — `HandlerFuncE` enables centralized error handling without boilerplate
- **FastHandler routes** — ultra-low-latency alternative that bypasses context allocation; params passed as a direct argument
- **14 built-in middleware** — logger, CORS, Basic Auth, compression, throttle, timeout, and more
- **Route introspection** — `Lookup`, `Routes`, `Walk`, and `WalkFast` for programmatic route inspection

## Contents

- [Installation](#installation)
- [Quick Start](#quick-start)
- [Route Syntax](#route-syntax)
- [Path Parameters](#path-parameters)
- [Middleware](#middleware)
- [Fast Routes](#fast-routes)
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

```
go get github.com/FlavioCFOliveira/MuxMaster
```

**Requires Go 1.26 or later.**

---

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    "net/http"
    "os"

    "github.com/FlavioCFOliveira/MuxMaster"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
    r := muxmaster.New()

    // Global middleware — applied to every route registered below.
    r.Use(middleware.Logger(os.Stdout))
    r.Use(middleware.Recoverer)

    r.GET("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "Hello, World!")
    })

    // Named path parameter
    r.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
        id := muxmaster.PathParam(r, "id")
        fmt.Fprintf(w, "user %s\n", id)
    })

    // Versioned API group with its own middleware
    api := r.Group("/api/v1")
    api.Use(requireAPIKey)

    api.GET("/items", listItems)
    api.POST("/items", createItem)
    api.DELETE("/items/:id", deleteItem)

    log.Fatal(http.ListenAndServe(":8080", r))
}
```

---

## Route Syntax

MuxMaster supports four types of path segments:

| Pattern                  | Example match           | Description                                  |
|--------------------------|-------------------------|----------------------------------------------|
| `/users`                 | `/users`                | **Static segment** — exact match             |
| `/users/:id`             | `/users/42`             | **Named parameter** — matches one segment    |
| `/users/{id:[0-9]+}`     | `/users/42`             | **Regex parameter** — validates the value    |
| `/files/*filepath`       | `/files/img/logo.png`   | **Catch-all** — matches the rest of the path |

Rules:
- Path parameters (`:name`) match exactly one path segment (no `/`).
- Regex parameters (`{name:pattern}`) match one segment and must satisfy the regular expression.
- Catch-all parameters (`*name`) match the remainder of the path, including slashes.
- Parameters are extracted in the order they appear in the pattern.
- Conflicts between static and parameterized segments at the same position resolve in favour of the static route.

### Registering routes

```go
r.GET("/users", listUsers)
r.POST("/users", createUser)
r.PUT("/users/:id", updateUser)
r.PATCH("/users/:id", patchUser)
r.DELETE("/users/:id", deleteUser)
r.HEAD("/users/:id", headUser)
r.OPTIONS("/users", optionsUsers)

// Register a handler for all standard HTTP methods at once
r.ANY("/health", healthCheck)

// Register a handler for a specific subset of methods
r.Match([]string{"GET", "HEAD"}, "/ping", pingHandler)

// Low-level registration accepting any http.Handler
r.Handle("GET", "/users", http.HandlerFunc(listUsers))
```

---

## Path Parameters

### Reading a single parameter

```go
r.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    fmt.Fprintf(w, "user: %s\n", id)
})
```

### Reading all parameters

```go
r.GET("/posts/:year/:month/:slug", func(w http.ResponseWriter, r *http.Request) {
    ps := muxmaster.ParamsFromContext(r.Context())
    year  := ps.Get("year")
    month := ps.Get("month")
    slug  := ps.Get("slug")
    fmt.Fprintf(w, "%s/%s/%s\n", year, month, slug)
})
```

### Typed helpers

`Params` provides helpers that parse string values into Go types, returning an error if the parameter is absent or the value cannot be parsed:

```go
r.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
    ps := muxmaster.ParamsFromContext(r.Context())

    id, err := ps.Int("id")
    if err != nil {
        http.Error(w, "invalid id", http.StatusBadRequest)
        return
    }
    _ = id // int
})
```

| Method             | Return type | Notes                          |
|--------------------|-------------|--------------------------------|
| `ps.Get("name")`   | `string`    | Returns `""` if not present    |
| `ps.Lookup("name")`| `string, bool` | Returns presence flag       |
| `ps.Int("name")`   | `int, error`|                                |
| `ps.Int64("name")` | `int64, error` |                             |
| `ps.Uint64("name")`| `uint64, error` |                            |
| `ps.Float64("name")`| `float64, error` |                          |
| `ps.Bool("name")`  | `bool, error` |                              |
| `ps.Map()`         | `map[string]string` | All params as a map   |

### Catch-all parameters

```go
r.GET("/files/*filepath", func(w http.ResponseWriter, r *http.Request) {
    filepath := muxmaster.PathParam(r, "filepath")
    // For /files/img/logo.png, filepath == "/img/logo.png"
    fmt.Fprintln(w, filepath)
})
```

### Regex-constrained parameters

```go
// Only matches /users/42, /users/100 — not /users/abc
r.GET("/users/{id:[0-9]+}", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    fmt.Fprintln(w, id)
})
```

### Reading parameters in middleware

Parameters are stored in the request context and are accessible anywhere you have access to the request:

```go
func auditMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ps := muxmaster.ParamsFromContext(r.Context())
        log.Printf("params: %v", ps)
        next.ServeHTTP(w, r)
    })
}
```

### The Param type

Each path parameter is a `Param` struct with `Key` and `Value` string fields:

```go
r.GET("/posts/:year/:month/:slug", func(w http.ResponseWriter, r *http.Request) {
    ps := muxmaster.ParamsFromContext(r.Context())
    for _, p := range ps {
        fmt.Printf("%s=%s\n", p.Key, p.Value)
    }
})
```

### Route pattern

`RoutePattern` returns the registered route pattern that matched the request (e.g. `/users/:id`), or `""` if the request has not been matched yet:

```go
func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        next.ServeHTTP(w, r)
        pattern := muxmaster.RoutePattern(r)
        log.Printf("%s %s matched pattern %s", r.Method, r.URL.Path, pattern)
    })
}
```

---

## Middleware

Middleware is a function with the signature `func(http.Handler) http.Handler`. MuxMaster applies middleware at registration time, so the call to `Use` must appear **before** the routes it should wrap.

### Global middleware

```go
r := muxmaster.New()
r.Use(middleware.Logger(os.Stdout))   // outermost
r.Use(middleware.Recoverer)           // innermost before the handler

r.GET("/users", listUsers)             // wrapped by both Logger and Recoverer
```

### Pre-routing middleware

Pre-routing middleware runs **before** route matching. This is useful for path rewriting, cleaning, or stripping prefixes before the router sees the URL.

```go
r.Pre(middleware.CleanPath)
r.Pre(middleware.StripSlashes)
```

### Per-route middleware with `With`

`With` creates a copy of the current router or group with additional middleware scoped to a single route call:

```go
r.With(requireAdmin).DELETE("/users/:id", deleteUser)
r.With(rateLimit, audit).POST("/payments", processPayment)
```

### Writing custom middleware

Any function of the form `func(http.Handler) http.Handler` is valid middleware:

```go
func requireAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("Authorization")
        if !isValid(token) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}

r.Use(requireAuth)
```

---

## Fast Routes

`FastHandler` is a high-performance handler type that receives path parameters as a direct argument, bypassing the `context.WithValue` allocation used by standard `http.Handler` routes. Use it on latency-sensitive endpoints where every nanosecond matters.

```go
type FastHandler func(http.ResponseWriter, *http.Request, Params)
```

Parameters are valid only for the duration of the handler call. If you spawn a goroutine that outlives the handler, copy the slice first:

```go
func myFast(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    ps2 := make(muxmaster.Params, len(ps))
    copy(ps2, ps)
    go func() { process(ps2) }()
}
```

### Registering fast routes

Convenience methods exist for all standard HTTP verbs:

```go
r := muxmaster.New()

r.GETFast("/api/v1/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
    id := ps.Get("id")
    fmt.Fprintf(w, "user: %s\n", id)
})

r.POSTFast("/api/v1/items", createItemFast)
r.DELETEFast("/api/v1/items/:id", deleteItemFast)

// Or use HandleFast for any method
r.HandleFast("GET", "/files/*filepath", serveFilesFast)
```

Available methods: `GETFast`, `HEADFast`, `POSTFast`, `PUTFast`, `PATCHFast`, `DELETEFast`, `OPTIONSFast`, `CONNECTFast`, `TRACEFast`.

### Fast middleware

`FastHandler` routes do not support stdlib middleware (`func(http.Handler) http.Handler`). Use `FastMiddleware` instead:

```go
type FastMiddleware func(FastHandler) FastHandler

func loggingFast(next muxmaster.FastHandler) muxmaster.FastHandler {
    return func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
        log.Printf("%s %s", r.Method, r.URL.Path)
        next(w, r, ps)
    }
}

r.UseFast(loggingFast)
r.GETFast("/api/status", statusFast)
```

`UseFast` must be called before the fast routes it should wrap, just like `Use` for standard routes.

### Fast routes in groups

Groups support fast routes via `HandleFast` and fast middleware via `UseFast`:

```go
api := r.Group("/api/v1")
api.UseFast(loggingFast)

api.HandleFast("GET", "/users/:id", getUserFast)
api.HandleFast("POST", "/users", createUserFast)
```

### Performance and trade-offs

| Type | ns/op | B/op | allocs/op |
|---|---|---|---|
| Static `http.Handler` | 25 ns | 0 B | 0 |
| Static `FastHandler` | ~25 ns | 0 B | 0 |
| 1-param `http.Handler` | 112 ns | 416 B | 1 |
| 1-param `FastHandler` | ~50 ns | 32 B | 0–1 |

Trade-offs:
- **Incompatible with stdlib middleware** — use `FastMiddleware` only
- **Params are not in the request context** — they are passed as a direct argument
- **`Lookup()` returns `nil` for FastHandler routes** — use `WalkFast` to enumerate them
- No convenience short-hand methods on `Group` (use `group.HandleFast("GET", ...)`)

---

## Groups

Groups share a path prefix and an optional middleware stack. All routes registered on a group are prefixed with the group's path and wrapped with the group's middleware (applied after any mux-level middleware).

### Basic group

```go
api := r.Group("/api/v1")
api.Use(requireAPIKey)

api.GET("/users", listUsers)    // matches GET /api/v1/users
api.POST("/users", createUser)  // matches POST /api/v1/users
```

### Nested groups

```go
api := r.Group("/api/v1")
api.Use(requireAPIKey)

admin := api.Group("/admin")
admin.Use(requireAdmin)
admin.DELETE("/users/:id", deleteUser)  // matches DELETE /api/v1/admin/users/:id
```

### Inline groups with `Route`

`Route` creates a group and calls a function with it — useful for keeping related routes together:

```go
r.Route("/api/v1", func(api *muxmaster.Group) {
    api.Use(requireAPIKey)

    api.GET("/users", listUsers)
    api.POST("/users", createUser)

    api.Route("/admin", func(admin *muxmaster.Group) {
        admin.Use(requireAdmin)
        admin.DELETE("/users/:id", deleteUser)
    })
})
```

### Scoped middleware with `With`

`With` on a group returns a copy of the group with additional middleware for the next registration only:

```go
api.With(requireAdmin).DELETE("/users/:id", deleteUser)
api.With(throttle).POST("/exports", exportData)
```

### Register multiple methods on a group

`Match` registers the same handler for a set of HTTP methods:

```go
api := r.Group("/api/v1")
api.Match([]string{"GET", "HEAD"}, "/status", statusHandler)
```

---

## Mounting Sub-Routers

Mount attaches a separate `http.Handler` (including another `*muxmaster.Mux`) at a path prefix. The prefix is stripped before the request is forwarded to the mounted handler.

```go
// Build a versioned sub-router independently
v2 := muxmaster.New()
v2.GET("/items", listItemsV2)
v2.POST("/items", createItemV2)

// Attach it to the main router
r.Mount("/v2", v2)
// Now GET /v2/items → handled by listItemsV2
```

Mounted handlers receive a request with the prefix stripped from `r.URL.Path`, so the sub-router sees `/items`, not `/v2/items`.

---

## Static Files

`ServeFiles` serves files from a `http.FileSystem`. The route pattern must end with `/*name`.

```go
// Serve files from the ./public directory
r.ServeFiles("/static/*filepath", http.Dir("./public"))
// GET /static/css/main.css → ./public/css/main.css

// Serve embedded files (Go 1.16+)
import "embed"
//go:embed public
var publicFS embed.FS
r.ServeFiles("/assets/*filepath", http.FS(publicFS))
```

ServeFiles protects against directory traversal attacks by delegating to `http.FileServer`.

---

## Error Handling

### Error-returning handlers

`HandlerFuncE` extends the standard handler signature with an error return value. This eliminates repetitive `if err != nil { http.Error(...) }` blocks:

```go
r.GETE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
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

The common HTTP methods have error-returning variants: `GETE`, `POSTE`, `PUTE`, `PATCHE`, `DELETEE`, `HEADE`, `OPTIONSE`. Use `HandleE` directly for `CONNECT` and `TRACE`.

### The `HTTPError` interface

`muxmaster.Error(code, err)` wraps an error with an HTTP status code. The router's default error handler checks for `HTTPError` and uses its status code; non-`HTTPError` errors produce a 500.

```go
// Construct an HTTPError
err := muxmaster.Error(http.StatusNotFound, errors.New("user not found"))
```

### Custom error handler

Set `ErrorHandler` to centralize error handling across all `HandlerFuncE` routes and groups:

```go
r.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        muxmaster.JSON(w, he.StatusCode(), map[string]string{"error": err.Error()})
        return
    }
    log.Printf("unexpected error: %v", err)
    muxmaster.JSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
```

### Custom 404 and 405 handlers

```go
r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusNotFound, map[string]string{
        "error": "the requested resource was not found",
    })
})

r.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusMethodNotAllowed, map[string]string{
        "error": "method not allowed",
    })
})
```

### Panic recovery

`PanicHandler` intercepts panics in handlers and prevents them from crashing the server:

```go
r.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
    log.Printf("panic: %v\n%s", rcv, debug.Stack())
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

### Custom OPTIONS handler

`GlobalOPTIONS` replaces the default auto-generated response for OPTIONS requests:

```go
r.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
    w.WriteHeader(http.StatusNoContent)
})
```

By default, when `HandleOPTIONS` is true, MuxMaster responds with `204 No Content` and an `Allow` header listing all registered methods for the matched path.

---

## Response Helpers

MuxMaster provides a small set of response-writing helpers. All functions write the `Content-Type` header and status code automatically.

```go
// JSON response
muxmaster.JSON(w, http.StatusOK, map[string]any{"id": 42, "name": "Alice"})

// XML response
muxmaster.XML(w, http.StatusOK, struct {
    XMLName xml.Name `xml:"user"`
    Name    string   `xml:"name"`
}{Name: "Alice"})

// Plain text response
muxmaster.Text(w, http.StatusOK, "pong")

// Redirect
muxmaster.Redirect(w, r, http.StatusMovedPermanently, "/new-path")

// 204 No Content
muxmaster.NoContent(w)
```

Using `JSON` in an error-returning handler:

```go
r.POSTE("/users", func(w http.ResponseWriter, r *http.Request) error {
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

All options are fields on `*Mux` and can be set after `New()` and before registering routes or starting the server.

```go
r := muxmaster.New()

// Automatically redirect /foo/ → /foo when /foo is registered (and vice versa).
// Default: true
r.RedirectTrailingSlash = true

// Automatically redirect /FOO → /foo when /foo is registered (case-insensitive redirect).
// Default: true
r.RedirectFixedPath = true

// Return 405 Method Not Allowed (with Allow header) instead of 404 when the path
// is registered but not for the requested method.
// Default: true
r.HandleMethodNotAllowed = true

// Automatically respond to OPTIONS requests with the allowed methods.
// Default: true
r.HandleOPTIONS = true

// Match routes case-insensitively (no redirect, just serves the route directly).
// Default: false
r.CaseInsensitive = false

// Use r.URL.RawPath for route matching instead of r.URL.Path.
// Useful when path values contain encoded slashes (%2F).
// Default: false
r.UseRawPath = false

// Percent-decode path parameter values before returning them.
// Default: false
r.UnescapePathValues = false

// HTTP redirect code used by RedirectTrailingSlash and RedirectFixedPath.
// Default: 0 (auto: 301 for GET/HEAD, 307 for all other methods)
r.RedirectCode = http.StatusMovedPermanently // override to force a specific code
```

### Resetting configuration

Configuration flags are frozen on the first `ServeHTTP` call. To change a flag after the server has started serving, call `Rebuild()`:

```go
r.RedirectTrailingSlash = false
r.Rebuild() // resets the frozen config snapshot
```

**Warning:** do not call `Rebuild()` while the server is actively serving requests.

---

## Included Middleware

The `middleware` sub-package provides 17 production-ready middleware components. Import it separately:

```go
import "github.com/FlavioCFOliveira/MuxMaster/middleware"
```

### Overview

| Middleware         | Description                                    |
|--------------------|------------------------------------------------|
| `Logger`           | Request/response logging (method, path, status, duration) |
| `Recoverer`        | Panic recovery — returns 500 and logs the stack trace |
| `CORS`             | Cross-Origin Resource Sharing with full options |
| `BasicAuth`        | HTTP Basic Authentication                      |
| `Compress`         | Gzip/deflate response compression              |
| `ThrottleBacklog`  | Concurrency limiting with a backlog queue      |
| `Timeout`          | Per-request deadline using `context.WithTimeout` |
| `RequestID`        | Generates and attaches a unique request ID     |
| `RealIP`           | Extracts the real client IP from proxy headers |
| `CleanPath`        | Normalizes double slashes and dot segments     |
| `StripSlashes`     | Removes trailing slashes before routing        |
| `NoCache`          | Sets `Cache-Control: no-cache, no-store`       |
| `SetHeader`        | Sets arbitrary response headers                |
| `WithValue`        | Stores a value in the request context          |
| `APIKey`           | API key authentication with SHA-256 hashing    |
| `JWTAuth`          | JWT Bearer token validation (HS*/RS*/ES*)      |
| `OAuth2Introspect` | RFC 7662 token introspection with caching      |

### Usage examples

```go
// Structured request logging
r.Use(middleware.Logger(os.Stdout))

// Panic recovery
r.Use(middleware.Recoverer)

// CORS for a single-page application
r.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins:   []string{"https://app.example.com"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Authorization", "Content-Type"},
    AllowCredentials: true,
    MaxAge:           86400,
}))

// HTTP Basic Authentication
r.Use(middleware.BasicAuth("realm", map[string]string{
    "admin": "secret",
}))

// Gzip compression
r.Use(middleware.Compress(5))

// Limit concurrency to 100 simultaneous requests, queue up to 50, timeout after 30s
r.Use(middleware.ThrottleBacklog(100, 50, 30*time.Second))

// 10-second request deadline
r.Use(middleware.Timeout(10 * time.Second))

// Attach a unique X-Request-Id header to every request
r.Use(middleware.RequestID)

// Trust X-Forwarded-For / X-Real-IP from a reverse proxy
r.Use(middleware.RealIP)

// Set a custom response header on every request
r.Use(middleware.SetHeader("X-Content-Type-Options", "nosniff"))

// Store a value in the request context
r.Use(middleware.WithValue("env", "production"))
```

### Authentication Middleware

Three authentication middleware components cover the most common scenarios.

#### API Key Authentication

```go
// Authenticate requests by API key, with identity lookup:
r.Use(middleware.APIKey(middleware.APIKeyOptions{
    Keys: map[string]string{
        "sk_test_abc123": "user-123",   // raw key → identity
        "sk_test_def456": "user-456",
    },
    Header: "X-API-Key",  // default; can be customised
}))

r.GET("/api/data", func(w http.ResponseWriter, r *http.Request) {
    identity, _ := middleware.GetAPIKeyIdentity(r.Context())
    fmt.Fprintf(w, "authenticated as %s\n", identity)
})
```

#### JWT Bearer Token Authentication

```go
// Validate JWT tokens from the Authorization header:
pubKey, _ := jwt.ReadFile("public.pem")  // *ecdsa.PublicKey or *rsa.PublicKey
r.Use(middleware.JWTAuth(middleware.JWTOptions{
    PublicKey:  pubKey,
    Algorithms: []string{"ES256"},
    Issuers:    []string{"https://auth.example.com"},
    Audiences:  []string{"https://api.example.com"},
    ClockSkew:  5 * time.Second,  // tolerance for exp/nbf
}))

r.GET("/api/profile", func(w http.ResponseWriter, r *http.Request) {
    claims, _ := middleware.GetJWTClaims(r.Context())
    fmt.Fprintf(w, "user: %s\n", claims.Subject)
})
```

#### OAuth2 Token Introspection

```go
// Validate tokens via RFC 7662 introspection (with caching):
r.Use(middleware.OAuth2Introspect(middleware.OAuth2Options{
    Endpoint:     "https://idp.example.com/oauth/introspect",
    ClientID:     "my_service",
    ClientSecret: os.Getenv("OAUTH2_SECRET"),
    CacheTTL:     60 * time.Second,  // cache active tokens
    MaxCacheSize: 10000,
}))

r.GET("/api/resource", func(w http.ResponseWriter, r *http.Request) {
    introspect, _ := middleware.GetOAuth2Claims(r.Context())
    fmt.Fprintf(w, "scope: %s\n", introspect.Scope)
})
```

---

## Route Introspection

### Check whether a route is registered

```go
handler, params, found := r.Lookup("GET", "/users/42")
if found {
    fmt.Printf("found: %v params\n", len(params))
}
```

### List all registered routes

```go
routes := r.Routes()
for _, route := range routes {
    fmt.Printf("%-8s %s  →  %s\n", route.Method, route.Pattern, route.Handler)
}
```

### Iterate routes with a callback

`Walk` visits every `http.Handler` route. FastHandler routes are skipped — use `WalkFast` to visit them:

```go
err := r.Walk(func(method, pattern string, handler http.Handler) error {
    fmt.Printf("%s %s\n", method, pattern)
    return nil
})
```

### Iterate fast routes with a callback

```go
err := r.WalkFast(func(method, pattern string, handler muxmaster.FastHandler) error {
    fmt.Printf("%s %s (FastHandler)\n", method, pattern)
    return nil
})
```

---

## Benchmarks

Benchmarks run on AMD Ryzen 9 5900HX, Go 1.26.2. All measurements use the same route set (`/api/v1/...`).

| Route type          | MuxMaster               | httprouter              | chi v5                  |
|---------------------|-------------------------|-------------------------|-------------------------|
| Static              | **25 ns, 0 allocs**     | 33.8 ns, 0 allocs       | 213.5 ns, 2 allocs      |
| 1 parameter         | 112 ns, 1 alloc         | **56.4 ns, 1 alloc**    | 354.1 ns, 4 allocs      |
| 2 parameters        | 130 ns, 1 alloc         | **66.5 ns, 1 alloc**    | 402.2 ns, 4 allocs      |
| 3 parameters        | 141 ns, 1 alloc         | **78.4 ns, 1 alloc**    | 410.2 ns, 4 allocs      |
| Catch-all           | 109 ns, 1 alloc         | **51.3 ns, 1 alloc**    | 330.2 ns, 4 allocs      |
| Parallel static     | **3.7 ns, 0 allocs**    | 4.92 ns, 0 allocs       | 128.2 ns, 2 allocs      |
| Parallel 1 param    | 108 ns, 1 alloc         | **22.2 ns, 1 alloc**    | 223.9 ns, 4 allocs      |

Reproduce with:

```
go test -bench=. -benchmem ./...
```

**Notes:**
- MuxMaster allocates **zero bytes** for static routes and **one tiered allocation** (416–480 B) for parameterized routes. That single allocation fuses the copied `*http.Request` and its context — meaning the router, net/http, and your handler all share one GC object.
- httprouter's 1 alloc for parameterized routes is only a 64 B `Params` slice; it passes parameters via a third argument outside the `http.Handler` interface, requiring a different handler signature.
- MuxMaster is **100% `net/http` compatible** — it accepts `http.Handler` directly, works with all existing middleware ecosystems, and requires no handler signature changes.

---

## Documentation

Extended documentation is in the [`docs/`](docs/) directory:

| Guide | Description |
|-------|-------------|
| [Getting Started](docs/getting-started.md) | Step-by-step guide for building your first application |
| [Routing](docs/routing.md) | Complete routing reference — syntax, methods, patterns |
| [Middleware](docs/middleware.md) | Writing and composing middleware |
| [Groups](docs/groups.md) | Organizing routes with groups and sub-routers |
| [Error Handling](docs/error-handling.md) | Centralized error handling patterns |
| [Configuration](docs/configuration.md) | All router options with defaults and examples |
| [Response Helpers](docs/response-helpers.md) | JSON, XML, Text, Redirect, NoContent |
| [Performance](docs/performance.md) | How MuxMaster achieves zero allocations |
| [Migration Guide](docs/migration.md) | Migrating from gorilla/mux, chi, and httprouter |
| [Cookbook](docs/cookbook.md) | Common patterns and production recipes |

Full API reference is available on [pkg.go.dev](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster).

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

In brief:
1. Fork the repository and create a feature branch.
2. Run `go test -race ./...` and `golangci-lint run` before pushing.
3. Open a pull request against `main`.

---

## License

[MIT](LICENSE) — © 2026 Flavio CF Oliveira
