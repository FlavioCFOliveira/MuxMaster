# Configuration Reference

All configuration is done by setting fields on `*Mux` after calling `muxmaster.New()`. Every field has a sensible default suitable for production use.

## Table of Contents

- [Router Behaviour](#router-behaviour)
- [Path Matching](#path-matching)
- [Custom Handlers](#custom-handlers)
- [Complete Example](#complete-example)

---

## Router Behaviour

### RedirectTrailingSlash

```go
mux.RedirectTrailingSlash = true // default
```

Automatically redirects requests with a trailing slash mismatch:

- `GET /users/` → 301 redirect to `/users` (when `/users` is registered but not `/users/`)
- `GET /users` → 301 redirect to `/users/` (when `/users/` is registered but not `/users`)

The redirect code is controlled by `RedirectCode`.

Set to `false` to return 404 in both cases instead of redirecting.

---

### RedirectFixedPath

```go
mux.RedirectFixedPath = true // default
```

Cleans the request path and issues a redirect if a match is found after cleaning:

- Removes extra slashes: `GET //users` → 301 to `/users`
- Resolves dots: `GET /a/../users` → 301 to `/users`

This prevents duplicate-content issues where `/users` and `//users` serve identical content under different URLs.

Set to `false` to return 404 for malformed paths instead of redirecting.

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

Automatically responds to `OPTIONS` requests with the list of allowed HTTP methods in the `Allow` header. The default response body is empty with status 200.

To customize the OPTIONS response globally:

```go
mux.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
    w.WriteHeader(http.StatusNoContent)
})
```

Set to `false` if you handle OPTIONS manually (e.g. through the CORS middleware).

---

### RedirectCode

```go
mux.RedirectCode = http.StatusMovedPermanently // default: 301
```

The HTTP status code used for redirects triggered by `RedirectTrailingSlash` and `RedirectFixedPath`. Common values:

| Code | Constant                      | Semantics                          |
|------|-------------------------------|------------------------------------|
| 301  | `http.StatusMovedPermanently` | Permanent redirect (cached)        |
| 302  | `http.StatusFound`            | Temporary redirect (not cached)    |
| 307  | `http.StatusTemporaryRedirect`| Temporary, preserves method        |
| 308  | `http.StatusPermanentRedirect`| Permanent, preserves method        |

Use 307 or 308 if you need POST requests to be redirected without the browser changing the method to GET.

---

## Path Matching

### CaseInsensitive

```go
mux.CaseInsensitive = false // default
```

When `true`, route matching ignores case in path segments. A request for `/Users/42` matches a route registered as `/users/:id`.

Note: this does not issue a redirect; the original URL is preserved in the response.

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

When `true`, path parameter values are percent-decoded before being returned by `PathParam` and `ParamsFromContext`.

For example, with the route `/search/:query` and the URL `/search/hello%20world`:

- `false`: `PathParam(r, "query")` returns `"hello%20world"`
- `true`:  `PathParam(r, "query")` returns `"hello world"`

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

Called when the path matches a route but not for the requested HTTP method. The `Allow` header is set to the list of allowed methods before this handler is called.

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

Called for every auto-handled OPTIONS request. The `Allow` header is already set when this handler runs.

Only active when `HandleOPTIONS` is `true`.

---

### PanicHandler

```go
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) { ... }
```

If set, catches panics in downstream handlers and calls this function instead of letting the panic propagate. Receives the value passed to `panic()` as `rcv`.

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

Called for every `HandlerFuncE` that returns a non-nil error. See [Error Handling](error-handling.md) for details.

---

## Complete Example

```go
mux := muxmaster.New()

// Routing behaviour
mux.RedirectTrailingSlash  = true
mux.RedirectFixedPath      = true
mux.HandleMethodNotAllowed = true
mux.HandleOPTIONS          = true
mux.RedirectCode           = http.StatusMovedPermanently

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
