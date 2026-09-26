# Migration Guide

This guide shows how to migrate an existing Go application to MuxMaster from three common routers: **gorilla/mux**, **chi**, and **httprouter**.

MuxMaster implements `http.Handler` and uses the same `http.HandlerFunc` signature as the standard library. Most migrations consist of replacing route registration calls — no handler code changes are required.

## Table of Contents

- [From gorilla/mux](#from-gorillamux)
- [From chi](#from-chi)
- [From httprouter](#from-httprouter)
- [From net/http ServeMux](#from-nethttpservemux)
- [Common Adjustments](#common-adjustments)

---

## From gorilla/mux

gorilla/mux was archived in 2022. MuxMaster offers equivalent routing primitives with a different parameter syntax (`:id` instead of `{id}`); on the 2026-09-26 benchmark run it was 8–14× faster than gorilla/mux on parameterised routes ([Performance](performance.md#vs-gorillamux)).

### Route registration

```go
// gorilla/mux
r := mux.NewRouter()
r.HandleFunc("/users", listUsers).Methods("GET")
r.HandleFunc("/users", createUser).Methods("POST")
r.HandleFunc("/users/{id}", getUser).Methods("GET")
r.HandleFunc("/users/{id}", updateUser).Methods("PUT")
r.HandleFunc("/users/{id}", deleteUser).Methods("DELETE")
```

```go
// MuxMaster
mux := muxmaster.New()
mux.GET("/users", listUsers)
mux.POST("/users", createUser)
mux.GET("/users/:id", getUser)
mux.PUT("/users/:id", updateUser)
mux.DELETE("/users/:id", deleteUser)
```

### Path parameters

```go
// gorilla/mux
id := mux.Vars(r)["id"]
```

```go
// MuxMaster
id := muxmaster.PathParam(r, "id")
// or
id := muxmaster.ParamsFromContext(r.Context()).Get("id")
```

### Regex-constrained parameters

gorilla/mux regex syntax and MuxMaster syntax differ slightly:

```go
// gorilla/mux — regex inside curly braces after colon
r.HandleFunc("/users/{id:[0-9]+}", getUser)

// MuxMaster — same syntax, compatible
mux.GET("/users/{id:[0-9]+}", getUser)
```

### Subrouters

```go
// gorilla/mux
api := r.PathPrefix("/api/v1").Subrouter()
api.Use(requireAPIKey)
api.HandleFunc("/users", listUsers).Methods("GET")
```

```go
// MuxMaster
api := mux.Group("/api/v1")
api.Use(requireAPIKey)
api.GET("/users", listUsers)
```

### Middleware

```go
// gorilla/mux
r.Use(loggingMiddleware)
```

```go
// MuxMaster — identical
mux.Use(loggingMiddleware)
```

### Starting the server

```go
// gorilla/mux
http.ListenAndServe(":8080", r)

// MuxMaster — identical
http.ListenAndServe(":8080", mux)
```

---

## From chi

chi and MuxMaster share a very similar API. Most migrations require minimal changes.

### Route registration

```go
// chi
r := chi.NewRouter()
r.Get("/users", listUsers)
r.Post("/users", createUser)
r.Get("/users/{id}", getUser)
r.Put("/users/{id}", updateUser)
r.Delete("/users/{id}", deleteUser)
```

```go
// MuxMaster — uppercase method names
mux := muxmaster.New()
mux.GET("/users", listUsers)
mux.POST("/users", createUser)
mux.GET("/users/:id", getUser)      // chi uses {id}, MuxMaster uses :id
mux.PUT("/users/:id", updateUser)
mux.DELETE("/users/:id", deleteUser)
```

chi uses `{param}` syntax; MuxMaster uses `:param` syntax. Regex constraints use the same `{param:regexp}` syntax in both.

### Path parameters

```go
// chi
id := chi.URLParam(r, "id")
```

```go
// MuxMaster
id := muxmaster.PathParam(r, "id")
```

### Route groups

```go
// chi
r.Route("/api/v1", func(r chi.Router) {
    r.Use(requireAPIKey)
    r.Get("/users", listUsers)
    r.Post("/users", createUser)
})
```

```go
// MuxMaster — nearly identical
mux.Route("/api/v1", func(g *muxmaster.Group) {
    g.Use(requireAPIKey)
    g.GET("/users", listUsers)
    g.POST("/users", createUser)
})
```

### Inline scoped middleware

```go
// chi
r.With(requireAdmin).Delete("/users/{id}", deleteUser)
```

```go
// MuxMaster — identical
mux.With(requireAdmin).DELETE("/users/:id", deleteUser)
```

### Mounting sub-routers

```go
// chi
r.Mount("/admin", adminRouter())
```

```go
// MuxMaster
mux.Mount("/admin", adminRouter())
```

One difference matters for handlers: MuxMaster's `Mount` strips the prefix from `r.URL.Path` before calling the mounted handler (a request for `/admin/users` arrives as `/users`), whereas chi leaves `r.URL.Path` unchanged and routes on its own context. Handlers of a mounted router that read `r.URL.Path` see the shorter path.

### chi Middleware

chi's `middleware` package uses the same `func(http.Handler) http.Handler` signature. All chi middleware is compatible with MuxMaster:

```go
import chimiddleware "github.com/go-chi/chi/v5/middleware"

mux.Use(chimiddleware.Logger)
mux.Use(chimiddleware.Recoverer)
```

You can migrate gradually: keep using chi middleware while replacing the router.

---

## From httprouter

httprouter has a different handler signature: `func(http.ResponseWriter, *http.Request, httprouter.Params)`. Migrating to MuxMaster requires updating handler signatures to use the standard `http.HandlerFunc`.

### Handler signature

```go
// httprouter
func getUser(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
    id := ps.ByName("id")
    // ...
}

router := httprouter.New()
router.GET("/users/:id", getUser)
```

```go
// MuxMaster — standard net/http signature
func getUser(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    // ...
}

mux := muxmaster.New()
mux.GET("/users/:id", getUser)
```

For a large codebase, you can write a thin adapter to avoid rewriting all handlers at once:

```go
// Adapter: wraps a httprouter-style handler as a MuxMaster handler
func adapt(h func(http.ResponseWriter, *http.Request, httprouter.Params)) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ps := muxmaster.ParamsFromContext(r.Context())
        // Convert muxmaster.Params to httprouter.Params
        hrps := make(httprouter.Params, len(ps))
        for i, p := range ps {
            hrps[i] = httprouter.Param{Key: p.Key, Value: p.Value}
        }
        h(w, r, hrps)
    }
}

mux.GET("/users/:id", adapt(getUser))
```

### Route registration

```go
// httprouter
router := httprouter.New()
router.GET("/users", listUsers)
router.POST("/users", createUser)
router.GET("/users/:id", getUser)
```

```go
// MuxMaster — identical route patterns, different handler type
mux := muxmaster.New()
mux.GET("/users", listUsers)
mux.POST("/users", createUser)
mux.GET("/users/:id", getUser)
```

### Custom error handlers

```go
// httprouter
router.NotFound         = myNotFoundHandler
router.MethodNotAllowed = myMethodNotAllowedHandler
router.PanicHandler     = myPanicHandler
```

```go
// MuxMaster — identical
mux.NotFound         = myNotFoundHandler
mux.MethodNotAllowed = myMethodNotAllowedHandler
mux.PanicHandler     = myPanicHandler
```

---

## From net/http ServeMux

Since Go 1.22, `net/http.ServeMux` supports method-qualified patterns and `{name}` wildcards read with `r.PathValue`, but it has no middleware, route groups or radix-tree lookup. Handler signatures stay the same when you migrate to MuxMaster.

```go
// net/http
mux := http.NewServeMux()
mux.HandleFunc("GET /users", listUsers)
mux.HandleFunc("GET /users/{id}", userDetail)
```

```go
// MuxMaster — explicit path parameters
mux := muxmaster.New()
mux.GET("/users", listUsers)
mux.GET("/users/:id", userDetail)
```

Replace `r.PathValue` (or manual extraction from `r.URL.Path`) with `muxmaster.PathParam`:

```go
// net/http
func userDetail(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    // ...
}

// MuxMaster
func userDetail(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    // ...
}
```

---

## Common Adjustments

### GET and HEAD Method Handling

Registering a GET handler in MuxMaster does **not** make it answer HEAD requests: HEAD on a GET-only route returns 405 Method Not Allowed with `Allow: GET, OPTIONS` (`specification/routing.md` section 15). Only `net/http.ServeMux` falls back from HEAD to GET; httprouter and chi behave like MuxMaster (verified in their source: httprouter v1.3.0 has no fallback, and chi v5 provides `middleware.GetHead` to add one).

| Router | HEAD on a GET-only route |
|--------|----------|
| `net/http.ServeMux` | served by the GET handler |
| chi | 405, unless `middleware.GetHead` is used |
| httprouter | 405 (register a HEAD handler) |
| **MuxMaster** | **405** (register a HEAD handler) |

When migrating from `net/http.ServeMux`, or from chi with `middleware.GetHead`, register HEAD explicitly:

```go
// Before (net/http.ServeMux)
http.HandleFunc("GET /users/{id}", getUser) // also answers HEAD

// After (MuxMaster)
mux.GET("/users/:id", getUser)
mux.HEAD("/users/:id", getUser)  // explicit HEAD handler required

// Or use Match to register both at once:
mux.Match([]string{"GET", "HEAD"}, "/users/:id", getUser)
```

See [Routing Reference](routing.md#get-and-head-methods) for full details.

### Parameter syntax

| Router       | Named param | Catch-all     | Regex param           |
|--------------|-------------|---------------|-----------------------|
| gorilla/mux  | `{id}`      | —             | `{id:[0-9]+}`         |
| chi          | `{id}`      | `*`           | `{id:[0-9]+}`         |
| httprouter   | `:id`       | `*id`         | —                     |
| MuxMaster    | `:id`       | `*id`         | `{id:[0-9]+}`         |

### Middleware compatibility

Any middleware with the signature `func(http.Handler) http.Handler` is compatible with MuxMaster. This covers:

- All chi middleware (`github.com/go-chi/chi/v5/middleware`)
- All gorilla handlers with that signature
- Most popular community middleware packages

### Trailing slash behaviour

MuxMaster redirects trailing slashes by default (`RedirectTrailingSlash = true`): a request for `/users/` is redirected to `/users` when only `/users` is registered, and vice versa. A path that has its own route is always served directly, so registering both `/users` and `/users/` needs no change. To return 404 instead of redirecting:

```go
mux.RedirectTrailingSlash = false
```

### HTTP methods

MuxMaster accepts only the ten methods GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE and QUERY; registering any other method (for example `PURGE`) panics. To serve extension methods, attach a handler with `Mount` and switch on `r.Method` — see [Routing](routing.md#handling-custom-methods).

---

## See Also

- [Routing](routing.md) — complete pattern syntax reference
- [Middleware](middleware.md) — built-in and custom middleware
- [Groups](groups.md) — organizing routes with groups and sub-routers
- [Configuration](configuration.md) — all router options
