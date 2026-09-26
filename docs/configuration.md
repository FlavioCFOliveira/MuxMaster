# Configuration Reference

All configuration is done by setting fields on the `*Mux` returned by `muxmaster.New()`. `New` sets production-safe defaults; a zero-value `Mux{}` does not have them (for example, its `RedirectTrailingSlash` is `false`).

## Table of Contents

- [When Configuration Takes Effect](#when-configuration-takes-effect)
- [Router Behaviour](#router-behaviour)
- [Path Matching](#path-matching)
- [Opt-in Pools](#opt-in-pools)
- [Custom Handlers](#custom-handlers)
- [Complete Example](#complete-example)

---

## When Configuration Takes Effect

The first call to `ServeHTTP` copies every option and handler field into an internal snapshot, and requests read only that snapshot. Assigning a field after the first request has no effect until you call `Rebuild()`, which discards the snapshot and the cached 404, 405, OPTIONS and redirect handlers; the next request takes a new snapshot. `Rebuild` is safe to call while the server is serving.

```go
mux.HandleMethodNotAllowed = false
mux.Rebuild() // the next request uses the new value
```

Set all fields before the server starts whenever possible.

| Field | Default after `New()` |
|---|---|
| `RedirectTrailingSlash` | `true` |
| `RedirectFixedPath` | `false` |
| `HandleMethodNotAllowed` | `true` |
| `HandleOPTIONS` | `true` |
| `RedirectCode` | `0` (301 for GET and HEAD, 307 otherwise) |
| `CaseInsensitive` | `false` |
| `UseRawPath` | `false` |
| `UnescapePathValues` | `false` |
| `PoolFastParams` | `false` |
| `PoolRequestBundle` | `false` |
| `NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `ErrorHandler`, `PanicHandler` | `nil` (built-in behaviour) |

---

## Router Behaviour

### RedirectTrailingSlash

```go
mux.RedirectTrailingSlash = true // default
```

Automatically redirects requests with a trailing slash mismatch:

- `GET /users/` → 301 redirect to `/users` (when `/users` is registered but not `/users/`)
- `GET /users` → 301 redirect to `/users/` (when `/users/` is registered but not `/users`)

Other methods receive 307 by default. The redirect code is controlled by `RedirectCode`. No redirect is issued for `/` or for CONNECT requests.

Set to `false` to return 404 in both cases instead of redirecting.

---

### RedirectFixedPath

```go
mux.RedirectFixedPath = false // default
```

When `true`, a request whose path has no route is checked again with its `path.Clean` form, and redirected there if that path has a route:

- Removes extra slashes: `GET //users` → 301 to `/users`
- Resolves dots: `GET /a/../users` → 301 to `/users`

It does not change letter case. With the default `false`, such requests receive 404 (or 405).

**Security:** the default is `false` because path canonicalisation can bypass middleware that inspects the raw path. To serve the cleaned path without a redirect, use `mux.Pre(middleware.CleanPath())` instead.

---

### HandleMethodNotAllowed

```go
mux.HandleMethodNotAllowed = true // default
```

When `true`, and the URL matches a registered route but not for the requested method, MuxMaster responds with `405 Method Not Allowed` and sets the `Allow` header to the list of allowed methods.

When `false`, such requests receive a `404 Not Found` instead.

The response handler can be customized via `mux.MethodNotAllowed`.

---

### HandleOPTIONS

```go
mux.HandleOPTIONS = true // default
```

Automatically responds to an `OPTIONS` request for a path that has routes but no explicit OPTIONS handler: status `204 No Content`, an empty body, and an `Allow` header listing the registered methods in the order GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, followed by OPTIONS. An explicitly registered OPTIONS route takes precedence.

To customize the OPTIONS response globally:

```go
mux.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
    w.WriteHeader(http.StatusNoContent)
})
```

Set to `false` to send unmatched OPTIONS requests to the 405 or 404 handling instead. The `CORS` middleware answers preflight requests itself (204) before the router's OPTIONS handling runs, so it works with either setting.

**Note — Asterisk-form `OPTIONS * HTTP/1.1`:** By default, `net/http` intercepts and answers asterisk-form OPTIONS requests directly (see [Middleware — Pre routing](middleware.md#pre-routing-middleware) for details). These requests never reach `GlobalOPTIONS`. To route them through MuxMaster, set `http.Server.DisableGeneralOptionsHandler = true`.

---

### RedirectCode

```go
mux.RedirectCode = 0 // default
```

The HTTP status code used for redirects triggered by `RedirectTrailingSlash` and `RedirectFixedPath`. With `0`, GET and HEAD receive `301 Moved Permanently` and every other method, including QUERY, receives `307 Temporary Redirect`, which preserves the method and body. A non-zero value is used for every method. Common values:

| Code | Constant                      | Semantics                          |
|------|-------------------------------|------------------------------------|
| 301  | `http.StatusMovedPermanently` | Permanent redirect (cached)        |
| 302  | `http.StatusFound`            | Temporary redirect (not cached)    |
| 307  | `http.StatusTemporaryRedirect`| Temporary, preserves method        |
| 308  | `http.StatusPermanentRedirect`| Permanent, preserves method        |

If you set 301 or 302, clients may change a POST (or other non-GET) request to GET when following the redirect; prefer 307 or 308 when the method must be preserved.

---

## Path Matching

### CaseInsensitive

```go
mux.CaseInsensitive = false // default
```

When `true`, static segments of a pattern match regardless of letter case. A request for `/Users/42` matches a route registered as `/users/:id`, and captured parameter values keep the case of the request (`/USERS/AbC` gives `id = "AbC"`).

No redirect is issued; the handler sees the original URL. `Lookup` ignores this option.

---

### UseRawPath

```go
mux.UseRawPath = false // default
```

When `true`, MuxMaster uses `r.URL.RawPath` for route matching instead of `r.URL.Path`. This matters when path values contain percent-encoded slashes (`%2F`):

- `r.URL.Path`: `/files/a%2Fb` is decoded to `/files/a/b` and would match `/files/*filepath` with `filepath = "/a/b"` (two separate segments)
- `r.URL.RawPath`: `/files/a%2Fb` is kept as-is and matches `/files/*filepath` with `filepath = "/a%2Fb"` (treated as a single segment)

Enable this only if your application legitimately uses encoded slashes in URL paths.

---

### UnescapePathValues

```go
mux.UnescapePathValues = false // default
```

Takes effect only when `UseRawPath` is also `true`. With `UseRawPath = false` (the default), `net/http` has already decoded the path, so parameter values are already decoded and a second decode is never applied.

With `UseRawPath = true`, parameters are captured from the raw path, still percent-encoded; setting `UnescapePathValues = true` decodes them. For the route `/files/:name` and the request `/files/a%2Fb`:

| `UseRawPath` | `UnescapePathValues` | Result |
|---|---|---|
| `false` | either | 404: the decoded path `/files/a/b` has an extra segment |
| `true` | `false` | `name = "a%2Fb"` |
| `true` | `true` | `name = "a/b"` |

**Security:** with both options `true`, a captured value can contain a real `/` (and `..`). Clean and confine it before using it as a file or URL path. MuxMaster logs a warning when a route is registered with this combination, and `Mux.ServeFiles` panics rather than register under it. See [SECURITY.md](../SECURITY.md).

---

## Opt-in Pools

```go
mux.PoolRequestBundle = false // default
mux.PoolFastParams    = false // default
```

`PoolRequestBundle` recycles the per-request bundle of `Handle` routes with parameters; `PoolFastParams` recycles the `Params` slice of `HandleFast` routes with 1–3 parameters. Both remove the per-request allocation but require that handlers never retain the request (or `ps`) after returning. See the [Maximum Performance Guide](max-performance.md) before enabling either.

---

## Custom Handlers

### NotFound

```go
mux.NotFound = myNotFoundHandler
```

Called when no route matches the request path. Defaults to `http.NotFound` (plain-text 404).

```go
mux.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusNotFound, map[string]string{
        "error": "not found",
        "path":  r.URL.Path,
    })
})
```

---

### MethodNotAllowed

```go
mux.MethodNotAllowed = myMethodNotAllowedHandler
```

Called when the path matches a route but not for the requested HTTP method. The `Allow` header is set to the list of allowed methods before this handler is called. When it is `nil`, the router writes `405 Method Not Allowed` as plain text with `X-Content-Type-Options: nosniff`.

```go
mux.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusMethodNotAllowed, map[string]string{
        "error":   "method not allowed",
        "allowed": w.Header().Get("Allow"),
    })
})
```

Only active when `HandleMethodNotAllowed` is `true`.

---

### GlobalOPTIONS

```go
mux.GlobalOPTIONS = myOptionsHandler
```

Called instead of the default `204 No Content` for every auto-handled OPTIONS request. The `Allow` header is already set when this handler runs.

Only active when `HandleOPTIONS` is `true`.

**Middleware wrapping:**

The automatic OPTIONS response is wrapped by any global middleware registered via `Use()`. The wrapper is applied dynamically: if you call `Use()` after assigning `GlobalOPTIONS`, the OPTIONS handler will be re-wrapped with the new middleware chain. This ensures that authentication, rate-limiting, logging, and other policies apply to OPTIONS responses.

---

### PanicHandler

```go
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }
```

If set, recovers panics raised anywhere in `ServeHTTP` — `Pre` middleware, `Use` middleware and handlers of both `Handle` and `HandleFast` routes — and calls this function with the value passed to `panic()` as `rcv`. A `RecovererWithLogger` middleware catches the panics raised inside it first, so `PanicHandler` does not see those. `PanicHandler` must not panic itself: a second panic is not recovered by MuxMaster and reaches `net/http`, which closes the connection.

```go
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
    log.Printf("panic: %v", rcv)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

---

### ErrorHandler

```go
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) { ... }
```

Called for every `HandlerFuncE` that returns a non-nil error. When it is `nil`, the router writes a plain-text `500 Internal Server Error`, even if the error is an `HTTPError`. See [Error Handling](error-handling.md) for details.

---

## Complete Example

```go
mux := muxmaster.New()

// Routing behaviour
mux.RedirectTrailingSlash  = true
mux.RedirectFixedPath      = false // keep false unless you need path.Clean redirects
mux.HandleMethodNotAllowed = true
mux.HandleOPTIONS          = true
mux.RedirectCode           = 0 // 301 for GET/HEAD, 307 otherwise

// Path matching
mux.CaseInsensitive       = false
mux.UseRawPath            = false
mux.UnescapePathValues    = false

// Custom error responses (JSON)
mux.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
})

mux.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusMethodNotAllowed, map[string]string{
        "error":   "method not allowed",
        "allowed": w.Header().Get("Allow"),
    })
})

mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
    log.Printf("panic: %v\n%s", rcv, debug.Stack())
    muxmaster.JSON(w, http.StatusInternalServerError, map[string]string{
        "error": "internal server error",
    })
}

mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    code := http.StatusInternalServerError
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        code = he.StatusCode()
    } else {
        log.Printf("unhandled error: %v", err)
    }
    muxmaster.JSON(w, code, map[string]string{"error": err.Error()})
}
```

---

## See Also

- [Routing](routing.md) — trailing slash and path normalization in more detail
- [Error Handling](error-handling.md) — custom error handler patterns
- [Middleware](middleware.md) — CleanPath and StripSlashes as alternatives to redirect-based normalization
