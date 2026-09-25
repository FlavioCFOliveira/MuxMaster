# Routing Reference

MuxMaster dispatches HTTP requests using a radix tree (compressed prefix trie). Each HTTP method has its own tree. Route lookup is O(k) in the length of the URL path, independent of the total number of registered routes.

## Table of Contents

- [Registering Routes](#registering-routes)
- [Path Pattern Syntax](#path-pattern-syntax)
- [Pattern Priority and Conflicts](#pattern-priority-and-conflicts)
- [HTTP Method Helpers](#http-method-helpers)
- [ANY and Match](#any-and-match)
- [Low-Level Registration](#low-level-registration)
- [Route Ordering and Middleware Timing](#route-ordering-and-middleware-timing)
- [Trailing Slash Behaviour](#trailing-slash-behaviour)
- [Path Normalization](#path-normalization)

---

## Registering Routes

The most common way to register a route is with one of the HTTP method helpers:

```go
mux := muxmaster.New()
mux.GET("/users", listUsers)
mux.POST("/users", createUser)
mux.PUT("/users/:id", updateUser)
mux.PATCH("/users/:id", patchUser)
mux.DELETE("/users/:id", deleteUser)
mux.HEAD("/users/:id", headUser)
mux.OPTIONS("/users", optionsUsers)
mux.CONNECT("/tunnel", tunnel)
mux.TRACE("/trace", trace)
mux.QUERY("/books/search", searchBooks)
```

All helpers accept a `http.HandlerFunc`. To pass an `http.Handler` directly, use `Handle`.

---

## Path Pattern Syntax

Patterns are strings that begin with `/`. Four types of segment are supported:

### Static segments

A plain string matches exactly:

```
/                  matches  /
/users             matches  /users
/api/v1/health     matches  /api/v1/health
```

### Named parameters (`:name`)

A segment starting with `:` captures one path segment (everything up to the next `/`):

```
/users/:id         matches  /users/42         → id = "42"
                   matches  /users/alice      → id = "alice"
                   no match /users/           (empty segment)
                   no match /users/42/posts   (extra segment)
```

Multiple parameters in the same pattern:

```
/posts/:year/:month/:slug
```

### Regex-constrained parameters (`{name:pattern}`)

A segment of the form `{name:regexp}` captures the segment only if it matches the regular expression:

```
/users/{id:[0-9]+}   matches  /users/42    → id = "42"
                     no match /users/abc
                     no match /users/3.14
```

The regexp is anchored automatically — you do not need `^` or `$`. The full Go regexp syntax is supported.

### Catch-all parameters (`*name`)

A segment starting with `*` captures the rest of the path, including slashes. It must appear at the end of the pattern:

```
/files/*filepath   matches  /files/img/logo.png   → filepath = "/img/logo.png"
                   matches  /files/a/b/c.txt       → filepath = "/a/b/c.txt"
                   matches  /files/                → filepath = "/"
```

The captured value always starts with `/`.

---

## Pattern Priority and Conflicts

When multiple patterns could match the same URL, MuxMaster resolves the conflict with the following priority (highest first):

1. **Static segments** — exact text always wins over parameters at the same position
2. **Named parameters** — `:name` wins over `*catch-all` at the same position
3. **Catch-all** — matches anything that nothing else matched

Example:

```go
mux.GET("/users/me",   getMe)       // 1. static → /users/me
mux.GET("/users/:id",  getUser)     // 2. param  → /users/42
mux.GET("/users/*all", catchAll)    // 3. catch  → /users/a/b/c
```

Registering two patterns that are ambiguous (e.g. two different named parameters at the same position) panics at startup to surface the conflict early.

### Lookup Fallback: Static Branch to Param Sibling

When a request URL matches a static path segment exactly but that static route has no handler registered, MuxMaster falls back to check any sibling parameter routes at the same position.

Example:

```go
mux.GET("/users/list",  listUsers)   // static route
mux.GET("/users/:id",   getUser)     // param route

mux.ServeHTTP(rw, request("/users/list"))   // → listUsers (exact static match)
mux.ServeHTTP(rw, request("/users/alice"))  // → getUser (no static /alice, fallback to :id)
mux.ServeHTTP(rw, request("/users/listx"))  // → getUser (no static /listx, fallback to :id)
```

This allows static and param routes to coexist at the same tree depth in either registration order. Both `/users/list` and `/users/:id` work correctly whether you register them as `GET("/users/list", ...)` then `GET("/users/:id", ...)` or vice versa.

---

## HTTP Method Helpers

Each standard HTTP method has a direct helper on `*Mux` and on `*Group`:

| Method    | Mux helper    | Group helper     |
|-----------|---------------|------------------|
| GET       | `mux.GET`     | `g.GET`          |
| HEAD      | `mux.HEAD`    | `g.HEAD`         |
| POST      | `mux.POST`    | `g.POST`         |
| PUT       | `mux.PUT`     | `g.PUT`          |
| PATCH     | `mux.PATCH`   | `g.PATCH`        |
| DELETE    | `mux.DELETE`  | `g.DELETE`       |
| OPTIONS   | `mux.OPTIONS` | `g.OPTIONS`      |
| CONNECT   | `mux.CONNECT` | `g.CONNECT`      |
| TRACE     | `mux.TRACE`   | `g.TRACE`        |
| QUERY¹    | `mux.QUERY`   | `g.QUERY`        |

¹ QUERY is standardised by RFC 10008 (June 2026). It is a safe, idempotent method like GET, but carries request content in the body like POST. The router performs no Content-Type validation; the handler is responsible.

Each helper also has an error-returning variant (`GETE`, `POSTE`, `PUTE`, `QUERYE`, etc.) — see [Error Handling](error-handling.md).

---

## ANY and Match

### ANY

`ANY` registers the same handler for all standard HTTP methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY):

```go
mux.ANY("/health", func(w http.ResponseWriter, r *http.Request) {
    muxmaster.Text(w, http.StatusOK, "ok")
})
```

### Match

`Match` registers the handler for a specific subset of methods:

```go
mux.Match([]string{"GET", "HEAD"}, "/ping", pingHandler)
mux.Match([]string{"POST", "PUT"}, "/upload", uploadHandler)
```

---

## Low-Level Registration

`Handle`, `HandleFunc`, and `HandleE` accept an explicit method string and are used for all ten standard methods and the `QUERY` method (RFC 10008). When called on `*Group`, these methods delegate to the parent `*Mux`'s same methods with identical behaviour.

### Supported Methods

The following eleven tokens are recognized by the router:

```
GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY
```

These can be registered via the HTTP method helpers (e.g., `mux.GET`, `mux.QUERY`) or via `Handle` with an explicit method string:

```go
mux.Handle("GET", "/users", listUsers)
mux.Handle("POST", "/users", createUsers)
mux.Handle("QUERY", "/search", searchHandler)
```

All three pairs of methods are equivalent:
- `mux.GET(path, h)` ↔ `mux.Handle("GET", path, h)`
- `mux.QUERYE(path, h)` ↔ `mux.HandleE("QUERY", path, h)`
- `mux.POSTFast(path, h)` ↔ `mux.HandleFast("POST", path, h)`

### Custom or Extension Methods

MuxMaster does not support registering handlers for custom or extension HTTP methods such as `PURGE` (used by caching proxies) or `PROPFIND` (WebDAV). Attempting to register one panics:

```go
mux.Handle("PURGE", "/cache/*key", handler)  // panics: "unsupported HTTP method 'PURGE'"
```

This is by design. The router uses a fixed array of method indices (not a map) to provide O(1) method dispatch on the request-time hot path. Supporting an open-ended set of methods would reintroduce a hash map or equivalent dynamic structure, compromising the zero-allocation performance design. See [out-of-scope.md](../specification/out-of-scope.md) section 2.7 for the architectural rationale.

### Handling Custom Methods

To serve requests with custom methods, use `Mount` to attach a handler that switches on the request method. Mount registers on the internal `"*"` tree, which is consulted only after the request method's own tree (see [specification/routing.md](../specification/routing.md) §4.1 rule 47). Consequently:

- A request matching an explicit route in its method's tree takes precedence over a Mount prefix.
- A request with an unrecognized method (e.g., PURGE) bypasses its method's tree entirely and falls through to the `"*"` tree, where Mount matches.
- The mounted handler receives `r.URL.Path` with the Mount prefix stripped (e.g., a request to `/cache/data` matched by `Mount("/cache", h)` sees `/data`).

```go
mux.GET("/cache/pinned", func(w http.ResponseWriter, r *http.Request) {
    // GET /cache/pinned → this handler (explicit GET route takes precedence)
    fmt.Fprintf(w, "Cached data: %s\n", r.URL.Path)
})

mux.Mount("/cache", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // r.URL.Path has the "/cache" prefix stripped:
    // GET /cache/other → /other (no explicit route, Mount handles)
    // PURGE /cache/data → /data (unrecognized method, Mount handles)
    switch r.Method {
    case "PURGE":
        fmt.Fprintf(w, "Purging %s\n", r.URL.Path)
    case "GET":
        fmt.Fprintf(w, "Getting %s\n", r.URL.Path)
    default:
        w.Header().Set("Allow", "GET, PURGE")
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    }
}))
```

This is the supported way to serve custom-method requests with MuxMaster. Alternatively, `Handle("*", pattern, handler)` is the low-level mechanism that Mount is built on (per [specification/routing.md](../specification/routing.md) §2.1 rule 31), but Mount is the recommended, documented API.

---

## Route Ordering and Middleware Timing

MuxMaster wraps middleware at **registration time**, not at request time. This means middleware is applied to the handler function at the moment `GET`, `POST`, `Handle`, etc. is called.

The practical consequence is that `Use` must be called **before** the routes it should wrap:

```go
mux := muxmaster.New()

mux.GET("/public", publicHandler)  // NOT wrapped by auth

mux.Use(requireAuth)
mux.GET("/private", privateHandler) // wrapped by auth
```

This design eliminates per-request middleware iteration. Combined with the radix tree and the tiered request bundle described in [Performance](performance.md), it allows static routes to dispatch with zero allocations and parameterised routes with a single fused allocation.

---

## Trailing Slash Behaviour

`RedirectTrailingSlash` (default `true`) automatically handles the common discrepancy between `/users` and `/users/`:

- If a request arrives for `/users/` and only `/users` is registered, MuxMaster redirects to `/users`.
- If a request arrives for `/users` and only `/users/` is registered, MuxMaster redirects to `/users/`.

This also applies to catch-all routes and mounted handlers. For example, with `Mount("/api", handler)` (internally registered as `Handle("*", "/api/*mux_mount", ...)`, with a wrapper that strips the prefix before calling `handler`):

- A request to `/api` (bare prefix, no trailing slash) triggers a redirect to `/api/` when `RedirectTrailingSlash` is `true`.
- A request to `/api/` and `/api/anything` both match the mounted handler directly.

The redirect uses the code set in `RedirectCode` (default 301).

**Redirect target encoding:**

When building the redirect target, MuxMaster percent-encodes any ASCII control bytes (0x00–0x1F and 0x7F) that appear in the computed path. This conforms to RFC 9110 section 5.5, which prohibits raw control bytes in HTTP field values. All other bytes, including the query string, are left unchanged. This ensures that redirects containing decoded control characters (e.g., a newline in a path segment after percent-decoding) cannot inject headers or response content.

To disable this and return 404 instead:

```go
mux.RedirectTrailingSlash = false
```

---

## Path Normalization

`RedirectFixedPath` (default `true`) normalizes the URL before matching:

- Removes duplicate slashes: `//users` → `/users`
- Resolves dot segments: `/a/../users` → `/users`
- If a match is found after normalization, issues a redirect to the clean URL

To use pre-routing path cleaning instead of a redirect (useful when you want the clean path without a round-trip), add the middleware:

```go
mux.Pre(middleware.CleanPath())
```

`CleanPath` modifies the request in-place before the router sees it, so no redirect is issued.

---

## Related Topics

- [Path Parameters](getting-started.md#step-2----path-parameters) — reading and parsing parameter values
- [Middleware](middleware.md) — applying middleware globally or per route
- [Groups](groups.md) — organizing routes into groups
- [Configuration](configuration.md) — all router options and their defaults
