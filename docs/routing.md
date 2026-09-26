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
- [Route Introspection](#route-introspection)

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

All helpers accept an `http.HandlerFunc`. To pass an `http.Handler` directly, use `Handle`.

---

## Path Pattern Syntax

Patterns are strings that begin with `/`. Five kinds of segment are supported: static text, named parameters, regex-constrained parameters, catch-all parameters and optional parameters.

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

A named or regex parameter never captures an empty segment, including one produced by a doubled slash: `/:id/posts` does not match `//posts`.

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

The expression is compiled at registration as `^(?:expr)$`, so it is anchored automatically — you do not need `^` or `$`. It uses Go's [`regexp`](https://pkg.go.dev/regexp/syntax) (RE2) syntax; an invalid expression panics at registration. Even an expression that accepts the empty string, such as `[a-z]*`, never matches an empty segment.

### Catch-all parameters (`*name`)

A segment starting with `*` captures the rest of the path, including slashes. It must appear at the end of the pattern:

```
/files/*filepath   matches  /files/img/logo.png   → filepath = "/img/logo.png"
                   matches  /files/a/b/c.txt       → filepath = "/a/b/c.txt"
                   matches  /files/                → filepath = "/"
```

The captured value always starts with `/`. The pattern must have a `/` immediately before `*`.

The value is the unsanitised remainder of the request path and may contain `..` segments. Handlers that map it to files must use `http.FileServer` or `ServeFiles`, which clean the path, or clean and confine the value themselves.

### Optional parameters (`{/:name}` and `{/:name:pattern}`)

An optional parameter declares that a segment may be present or absent. Optional named parameters use `{/:name}` syntax; optional regex parameters use `{/:name:pattern}`:

```go
mux.GET("/users{/:id}", handler)       // matches /users AND /users/42
mux.GET("/items{/:id:[0-9]+}", handler) // matches /items AND /items/123 (regex)
```

When a route contains optional parameters, the router automatically expands it into multiple registrations. A pattern with `N` optional parameters expands into `2^N` registrations. For example, `/users{/:id}` expands into two handler registrations: one for `/users` and one for `/users/:id`.

**Limits:**

- A pattern may contain **at most 8 optional parameters**. Exceeding this limit panics at registration time to prevent exponential complexity in route expansion (a pattern with 8 optional parameters expands into 256 routes).
- **Two optional parameters may not appear consecutively** (with no literal segment between them). For example, `/users{/:id}{/:post}` is invalid. Separate them with a literal segment: `/users{/:id}/posts{/:post}`.

These constraints prevent both exponential expansion and conflicts in the radix tree structure. See [specification/routing.md](../specification/routing.md) section 13 (requirements 103–105) for the full rationale.

---

## Pattern Priority and Conflicts

A static segment and a named (or regex) parameter may share the same position. When both could match, the static segment wins:

```go
mux.GET("/users/me",  getMe)    // GET /users/me → getMe
mux.GET("/users/:id", getUser)  // GET /users/42 → getUser
```

The tree holds one wildcard per position, so the following registrations panic, whichever of the two is registered second:

- two different parameters at the same position, such as `/users/:id` and `/users/:name/posts`, or `/u/{id:[0-9]+}` and `/u/:name`;
- a parameter and a catch-all at the same position, such as `/users/:id` and `/users/*all`;
- a catch-all and a static sibling at the same position, such as `/*filepath` and `/api/users`.

A catch-all therefore never competes with another route at its own position: it matches everything below its prefix that no deeper route claims. Registering the same method and pattern twice also panics.

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

Error-returning variants (`GETE`, `HEADE`, `POSTE`, `PUTE`, `PATCHE`, `DELETEE`, `OPTIONSE`, `QUERYE`) exist on both `*Mux` and `*Group`; there is no `CONNECTE` or `TRACEE` — use `HandleE` for those methods. See [Error Handling](error-handling.md).

`*Mux` also has a `FastHandler` variant for every method (`GETFast`, …, `QUERYFast`). `*Group` has none; register a fast route on a group with `g.HandleFast(method, path, h)`.

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

`Handle`, `HandleFunc`, `HandleE` and `HandleFast` accept an explicit method string. When called on `*Group`, they join the group prefix, apply the group middleware and delegate to the parent `*Mux`.

### Supported Methods

The router accepts exactly ten method tokens:

```
GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY
```

Internally it also uses the token `"*"` for routes registered by `Mount`. `muxmaster.MethodQuery` holds the string `"QUERY"`, because `net/http` does not define an `http.MethodQuery` constant.

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

### GET and HEAD Methods

Unlike `net/http.ServeMux`, **registering a handler for GET does not automatically handle HEAD requests**. A HEAD request to a GET-only route returns `405 Method Not Allowed` with `Allow: GET, OPTIONS` (or `404 Not Found` when `HandleMethodNotAllowed` is `false`). To handle HEAD requests, register them explicitly:

```go
mux.GET("/users/:id", getUser)
mux.HEAD("/users/:id", headUser)  // explicit HEAD handler required
```

Alternatively, use `Match` to register a single handler for both methods:

```go
mux.Match([]string{"GET", "HEAD"}, "/users/:id", func(w http.ResponseWriter, r *http.Request) {
    // Handle both GET and HEAD here
})
```

The router's GET and HEAD methods are independent; there is no implicit relationship between them per RFC 9110 §9.3.2 (which describes the *semantics* of HEAD, not the routing semantics). From the router's perspective, HEAD is a distinct HTTP method.

### Regex Parameter Name Length

Regex-constrained parameter names in `{name:expr}` are limited to 254 characters. Names longer than 254 bytes panic at route registration:

```go
mux.GET("/users/{" + strings.Repeat("x", 255) + ":[0-9]+}", handler)  // panics
mux.GET("/users/{" + strings.Repeat("x", 254) + ":[0-9]+}", handler)  // OK
```

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

The redirect happens only when the requested path itself has no route; if both `/users` and `/users/` are registered, each is served directly. It never applies to the root path `/` or to CONNECT requests. The query string is preserved.

This also applies to catch-all routes and mounted handlers. For example, with `Mount("/api", handler)` (internally registered as `Handle("*", "/api/*mux_mount", ...)`, with a wrapper that strips the prefix before calling `handler`):

- A request to `/api` (bare prefix, no trailing slash) triggers a redirect to `/api/` when `RedirectTrailingSlash` is `true`.
- A request to `/api/` and `/api/anything` both match the mounted handler directly.

When the mounted handler is itself a `*muxmaster.Mux`, its own automatic redirects keep the mount prefix: an inner redirect from `/x` to `/x/` reaches the client as `Location: /api/x/`.

**Redirect status code:** with `RedirectCode` left at `0` (the default), the redirect is `301 Moved Permanently` for GET and HEAD and `307 Temporary Redirect` for every other method, including QUERY, so the method and body are preserved. A non-zero `RedirectCode` is used for every method.

**Redirect target encoding:** the `Location` value is always a path on the same origin, never a scheme or host taken from the request. Before it is written, MuxMaster:

- percent-encodes every backslash (`\` → `%5C`), because browsers treat `/\` at the start of a URL like `//` and would resolve the redirect to another origin (WHATWG URL Standard);
- percent-encodes every ASCII control byte (0x00–0x1F) and DEL (0x7F), which RFC 9110 section 5.5 forbids in field values, so a decoded control character in the path cannot inject headers;
- percent-encodes non-ASCII bytes, as `net/http.Redirect` does.

The query string is appended unchanged. Apart from the backslash and control-byte encoding, the response is byte-identical to `net/http.Redirect`.

To disable trailing-slash redirects and return 404 instead:

```go
mux.RedirectTrailingSlash = false
```

---

## Path Normalization

`RedirectFixedPath` is **`false` by default**. When you enable it, a request whose path has no route (and no trailing-slash redirect) is checked again with its `path.Clean` form; if that cleaned path has a route, MuxMaster redirects to it:

- duplicate slashes: `//users` → `/users`
- dot segments: `/a/../users` → `/users`

It does not change letter case. The redirect uses the same status codes and `Location` encoding as trailing-slash redirects.

```go
mux.RedirectFixedPath = true
```

It is off by default for security: path canonicalisation can bypass middleware that inspects the raw path.

To route the cleaned path directly, without a redirect, add the `CleanPath` middleware before routing:

```go
mux.Pre(middleware.CleanPath())
```

`CleanPath` hands the router a shallow copy of the request with the cleaned path; the original request is not modified. Register it before any `Pre` middleware that inspects the path.

---

## Route Introspection

`*Mux` can report what is registered without serving a request:

| Method | Behaviour |
|---|---|
| `Lookup(method, path) (http.Handler, Params, bool)` | Matches `path` exactly as registered: no redirects, and `CaseInsensitive` is ignored. For a `HandleFast` route it returns a `nil` handler, the captured `Params` and `true`. |
| `Routes() []RouteInfo` | Lists every route, both `Handle` and `HandleFast`, with its method, pattern and handler function name. A mount point is listed with method `"*"` and pattern `<prefix>/*mux_mount`. An optional-parameter pattern is listed as its expanded routes. |
| `Walk(fn)` | Calls `fn` for every `Handle` route and skips `HandleFast` routes; returning an error stops the walk. |
| `WalkFast(fn)` | Calls `fn` for every `HandleFast` route and skips `Handle` routes. |

These methods take the registration read lock, which request dispatch never takes; they are intended for start-up checks, tests and admin endpoints, not the request path.

```go
if _, _, ok := mux.Lookup(http.MethodGet, "/users/42"); !ok {
    log.Fatal("route /users/:id is missing")
}
for _, r := range mux.Routes() {
    fmt.Printf("%-7s %s\n", r.Method, r.Pattern)
}
```

---

## Related Topics

- [Path Parameters](getting-started.md#step-2----path-parameters) — reading and parsing parameter values
- [Middleware](middleware.md) — applying middleware globally or per route
- [Groups](groups.md) — organizing routes into groups
- [Configuration](configuration.md) — all router options and their defaults
