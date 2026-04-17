# MuxMaster

[![CI](https://github.com/FlavioCFOliveira/MuxMaster/actions/workflows/ci.yml/badge.svg)](https://github.com/FlavioCFOliveira/MuxMaster/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/FlavioCFOliveira/MuxMaster.svg)](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster)
[![Go Report Card](https://goreportcard.com/badge/github.com/FlavioCFOliveira/MuxMaster)](https://goreportcard.com/report/github.com/FlavioCFOliveira/MuxMaster)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A high-performance HTTP router for Go. Zero external dependencies, 100% compatible with `net/http`.

Routes are matched with a radix (compressed prefix) tree — O(k) lookup where k is the path length. The hot path allocates zero bytes for static routes and routes with up to three path parameters.

## Benchmarks

Measured on Apple M4, Go 1.26, compared to the most popular Go routers:

| Route type      | MuxMaster       | httprouter      | bunrouter       |
|-----------------|-----------------|-----------------|-----------------|
| Static          | **13.5 ns, 0 allocs** | 15.9 ns, 0 allocs | 14.0 ns, 0 allocs |
| 1 parameter     | 27 ns, 0 allocs | 32.8 ns, 1 alloc | 22.4 ns, 0 allocs |
| 2 parameters    | **38.7 ns, 0 allocs** | 40.0 ns, 1 alloc | 41.7 ns, 0 allocs |
| 3 parameters    | 46.7 ns, 0 allocs | 44.5 ns, 1 alloc | 29.8 ns, 0 allocs |
| Catch-all       | **23.2 ns, 0 allocs** | 28.0 ns, 1 alloc | 11.9 ns, 0 allocs |
| Parallel static | **1.55 ns, 0 allocs** | 1.98 ns, 0 allocs | 1.77 ns, 0 allocs |

Run `go test -bench=. -benchmem ./...` to reproduce.

## Installation

```
go get github.com/FlavioCFOliveira/MuxMaster
```

Requires Go 1.22+.

## Quick start

```go
package main

import (
    "fmt"
    "net/http"

    "github.com/FlavioCFOliveira/MuxMaster"
)

func main() {
    r := muxmaster.New()

    r.GET("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "Hello, World!")
    })

    r.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
        id := muxmaster.PathParam(r, "id")
        fmt.Fprintf(w, "user %s\n", id)
    })

    http.ListenAndServe(":8080", r)
}
```

## Route syntax

| Pattern              | Matches                        | Notes                        |
|----------------------|--------------------------------|------------------------------|
| `/users`             | `/users`                       | Static segment               |
| `/users/:id`         | `/users/42`                    | Named parameter              |
| `/users/{id:[0-9]+}` | `/users/42` (not `/users/abc`) | Regex-constrained parameter  |
| `/files/*filepath`   | `/files/a/b/c.txt`             | Catch-all (greedy)           |

## Reading path parameters

```go
// Inside a handler:
id := muxmaster.PathParam(r, "id")

// Or via context (useful in middleware):
ps := muxmaster.ParamsFromContext(r.Context())
id := ps.Get("id")

// Typed helpers:
n, err := ps.Int("id")
f, err := ps.Float64("weight")
```

## Middleware

Middleware wraps all routes registered **after** the `Use` call. The first middleware is outermost.

```go
r := muxmaster.New()
r.Use(middleware.Logger(os.Stdout))
r.Use(middleware.Recoverer)

r.GET("/users", listUsers)
```

Pre-dispatch middleware (runs before routing, e.g. to rewrite paths):

```go
r.Pre(middleware.CleanPath)
```

## Groups

Groups share a path prefix and an optional middleware stack.

```go
api := r.Group("/api/v1")
api.Use(requireAPIKey)

api.GET("/users", listUsers)
api.POST("/users", createUser)

// Sub-groups
admin := api.Group("/admin")
admin.Use(requireAdmin)
admin.DELETE("/users/:id", deleteUser)
```

Inline sub-routes:

```go
r.Route("/api/v1", func(api *muxmaster.Group) {
    api.GET("/users", listUsers)
    api.POST("/users", createUser)
})
```

## Mounting sub-routers

```go
v2 := muxmaster.New()
v2.GET("/items", listItems)

r.Mount("/v2", v2)
```

## Serving static files

```go
r.ServeFiles("/static/*filepath", http.Dir("./public"))
```

## Error-returning handlers

```go
r.GETE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    user, err := db.FindUser(muxmaster.PathParam(r, "id"))
    if err != nil {
        return muxmaster.HTTPError{Code: 404, Message: "not found"}
    }
    return muxmaster.JSON(w, user)
})
```

## Router options

All options default to sensible production values.

```go
r := muxmaster.New()
r.RedirectTrailingSlash  = true  // /foo/ → /foo when /foo is registered
r.RedirectFixedPath      = true  // /FOO  → /foo when /foo is registered
r.HandleMethodNotAllowed = true  // 405 with Allow header
r.HandleOPTIONS          = true  // auto-reply to OPTIONS
r.CaseInsensitive        = false // disable for strict matching
r.UseRawPath             = false // use r.URL.RawPath for matching
r.UnescapePathValues     = false // percent-decode param values

// Custom handlers
r.NotFound        = myNotFoundHandler
r.MethodNotAllowed = myMethodNotAllowedHandler
r.PanicHandler    = func(w http.ResponseWriter, r *http.Request, rcv any) {
    http.Error(w, "internal error", 500)
}
```

## Included middleware

The `middleware` sub-package provides ready-to-use handlers:

| Middleware      | Description                              |
|-----------------|------------------------------------------|
| `Logger`        | Request/response logging                 |
| `Recoverer`     | Panic recovery with 500 response         |
| `CORS`          | Cross-Origin Resource Sharing            |
| `BasicAuth`     | HTTP Basic Authentication                |
| `Compress`      | Gzip/deflate response compression        |
| `Throttle`      | Concurrency limiting                     |
| `Timeout`       | Per-request deadline                     |
| `RequestID`     | Attach unique request ID                 |
| `RealIP`        | Extract real client IP from headers      |
| `CleanPath`     | Redirect double slashes and dot segments |
| `StripSlashes`  | Remove trailing slashes before routing   |
| `NoCache`       | Set Cache-Control: no-cache              |
| `SetHeader`     | Set arbitrary response headers           |
| `WithValue`     | Store a value in request context         |

```go
import "github.com/FlavioCFOliveira/MuxMaster/middleware"

r.Use(middleware.Logger(os.Stdout))
r.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins: []string{"https://example.com"},
    AllowedMethods: []string{"GET", "POST", "PUT", "DELETE"},
}))
```

## Route introspection

```go
// Check if a path is registered:
handler, params, _ := r.Lookup("GET", "/users/42")

// Iterate all registered routes:
r.Walk(func(method, pattern string, handler http.Handler) error {
    fmt.Printf("%s %s\n", method, pattern)
    return nil
})

// List all routes:
routes := r.Routes()
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
