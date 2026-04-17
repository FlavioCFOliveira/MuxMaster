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
r := muxmaster.New()
r.GET("/users", listUsers)
r.POST("/users", createUser)
r.PUT("/users/:id", updateUser)
r.PATCH("/users/:id", patchUser)
r.DELETE("/users/:id", deleteUser)
r.HEAD("/users/:id", headUser)
r.OPTIONS("/users", optionsUsers)
r.CONNECT("/tunnel", tunnel)
r.TRACE("/trace", trace)
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
r.GET("/users/me",   getMe)       // 1. static → /users/me
r.GET("/users/:id",  getUser)     // 2. param  → /users/42
r.GET("/users/*all", catchAll)    // 3. catch  → /users/a/b/c
```

Registering two patterns that are ambiguous (e.g. two different named parameters at the same position) panics at startup to surface the conflict early.

---

## HTTP Method Helpers

Each standard HTTP method has a direct helper on `*Mux` and on `*Group`:

| Method    | Mux helper   | Group helper     |
|-----------|--------------|------------------|
| GET       | `r.GET`      | `g.GET`          |
| HEAD      | `r.HEAD`     | `g.HEAD`         |
| POST      | `r.POST`     | `g.POST`         |
| PUT       | `r.PUT`      | `g.PUT`          |
| PATCH     | `r.PATCH`    | `g.PATCH`        |
| DELETE    | `r.DELETE`   | `g.DELETE`       |
| OPTIONS   | `r.OPTIONS`  | `g.OPTIONS`      |
| CONNECT   | `r.CONNECT`  | `g.CONNECT`      |
| TRACE     | `r.TRACE`    | `g.TRACE`        |

Each helper also has an error-returning variant (`GETE`, `POSTE`, `PUTE`, etc.) — see [Error Handling](error-handling.md).

---

## ANY and Match

### ANY

`ANY` registers the same handler for all standard HTTP methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE):

```go
r.ANY("/health", func(w http.ResponseWriter, r *http.Request) {
    muxmaster.Text(w, http.StatusOK, "ok")
})
```

### Match

`Match` registers the handler for a specific subset of methods:

```go
r.Match([]string{"GET", "HEAD"}, "/ping", pingHandler)
r.Match([]string{"POST", "PUT"}, "/upload", uploadHandler)
```

---

## Low-Level Registration

`Handle` and `HandleFunc` accept an explicit method string, which allows custom HTTP methods beyond the nine standard ones:

```go
r.Handle("PURGE", "/cache/*key", purgeCache)
r.HandleFunc("REPORT", "/dav/*path", davReport)
```

`HandleE` is the error-returning equivalent:

```go
r.HandleE("PURGE", "/cache/:key", func(w http.ResponseWriter, r *http.Request) error {
    key := muxmaster.PathParam(r, "key")
    return cache.Invalidate(key)
})
```

---

## Route Ordering and Middleware Timing

MuxMaster wraps middleware at **registration time**, not at request time. This means middleware is applied to the handler function at the moment `GET`, `POST`, `Handle`, etc. is called.

The practical consequence is that `Use` must be called **before** the routes it should wrap:

```go
r := muxmaster.New()

r.GET("/public", publicHandler)  // NOT wrapped by auth

r.Use(requireAuth)
r.GET("/private", privateHandler) // wrapped by auth
```

This design eliminates per-request middleware iteration and contributes to zero allocations on the hot path.

---

## Trailing Slash Behaviour

`RedirectTrailingSlash` (default `true`) automatically handles the common discrepancy between `/users` and `/users/`:

- If a request arrives for `/users/` and only `/users` is registered, MuxMaster redirects to `/users`.
- If a request arrives for `/users` and only `/users/` is registered, MuxMaster redirects to `/users/`.

The redirect uses the code set in `RedirectCode` (default 301).

To disable this and return 404 instead:

```go
r.RedirectTrailingSlash = false
```

---

## Path Normalization

`RedirectFixedPath` (default `true`) normalizes the URL before matching:

- Removes duplicate slashes: `//users` → `/users`
- Resolves dot segments: `/a/../users` → `/users`
- If a match is found after normalization, issues a redirect to the clean URL

To use pre-routing path cleaning instead of a redirect (useful when you want the clean path without a round-trip), add the middleware:

```go
r.Pre(middleware.CleanPath)
```

`CleanPath` modifies the request in-place before the router sees it, so no redirect is issued.

---

## Related Topics

- [Path Parameters](getting-started.md#step-2----path-parameters) — reading and parsing parameter values
- [Middleware](middleware.md) — applying middleware globally or per route
- [Groups](groups.md) — organizing routes into groups
- [Configuration](configuration.md) — all router options and their defaults
