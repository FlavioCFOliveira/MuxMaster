# Configuration

## Scope

This file specifies all exported fields of the `Mux` struct, their types, their default values, and the exact behavior change when each field is set to a non-default value.

This file does not cover the behavior of `NotFound`, `MethodNotAllowed`, `PanicHandler`, `GlobalOPTIONS`, and `ErrorHandler` in detail (see [error-handling.md](error-handling.md)). Redirect status codes are also governed by the matching rules in [routing.md](routing.md).

---

## 1. Constructor

1. `New() *Mux` returns a `*Mux` with all boolean feature flags set to `true` and all handler fields set to nil.
2. The zero value of `Mux` (i.e., `Mux{}`) has all boolean fields set to `false`. This is a valid but minimally featured router. Use `New()` for production use.

---

## 2. Feature Flags

### 2.1 RedirectTrailingSlash

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default (via `New`) | `true` |
| Zero value | `false` |

3. When `true`, the router issues a redirect when a route exists at the alternate trailing-slash path. For GET and HEAD, the redirect code is 301. For all other methods, the redirect code is the value of `RedirectCode`.
4. When `false`, no trailing-slash redirect is issued. A request to a path without a matching route proceeds to 404 (or 405) even if the alternate path has a handler.

### 2.2 RedirectFixedPath

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default (via `New`) | `true` |
| Zero value | `false` |

5. When `true`, the router computes `path.Clean(r.URL.Path)` and issues a redirect if the cleaned path has a registered handler and differs from the original path.
6. For GET and HEAD, the redirect code is 301. For all other methods, the redirect code is the value of `RedirectCode`.
7. When `false`, no fixed-path redirect is issued.

### 2.3 HandleMethodNotAllowed

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default (via `New`) | `true` |
| Zero value | `false` |

8. When `true`, the router responds with 405 and an `Allow` header when the path is registered for at least one method but not the requested method.
9. When `false`, a request to a registered path with an unregistered method results in a 404 response.

### 2.4 HandleOPTIONS

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default (via `New`) | `true` |
| Zero value | `false` |

10. When `true`, the router automatically handles OPTIONS requests by responding with the `Allow` header and a 204 status (or the `GlobalOPTIONS` handler response) for paths that have at least one registered handler.
11. This automatic response does not apply to paths where an explicit OPTIONS handler has been registered. Explicit handlers always take precedence.
12. When `false`, OPTIONS requests are treated like any other method. If no OPTIONS handler is registered, the request results in a 404 or 405 response.

---

## 3. Handler Fields

### 3.1 NotFound

| Attribute | Value |
|---|---|
| Type | `http.Handler` |
| Default | `nil` (uses `http.NotFound`) |

13. See [error-handling.md](error-handling.md) section 1 for full behavior.

### 3.2 MethodNotAllowed

| Attribute | Value |
|---|---|
| Type | `http.Handler` |
| Default | `nil` (uses plain-text 405) |

14. See [error-handling.md](error-handling.md) section 2 for full behavior.

### 3.3 PanicHandler

| Attribute | Value |
|---|---|
| Type | `func(http.ResponseWriter, *http.Request, any)` |
| Default | `nil` (no recovery) |

15. See [error-handling.md](error-handling.md) section 3 for full behavior.

### 3.4 GlobalOPTIONS

| Attribute | Value |
|---|---|
| Type | `http.Handler` |
| Default | `nil` (uses 204 No Content) |

16. See [error-handling.md](error-handling.md) section 4 for full behavior.

### 3.5 ErrorHandler

| Attribute | Value |
|---|---|
| Type | `func(http.ResponseWriter, *http.Request, error)` |
| Default | `nil` (uses plain-text 500) |

17. See [error-handling.md](error-handling.md) section 7 for full behavior.

---

## 4. Advanced Configuration Fields

### 4.1 RedirectCode

| Attribute | Value |
|---|---|
| Type | `int` |
| Default | `0` (resolved to 301 for GET/HEAD, 307 for others) |

18. When `RedirectCode` is `0`, the router uses 301 for GET and HEAD redirects and 307 for all other methods. This is the default behavior.
19. When `RedirectCode` is set to a non-zero HTTP redirect status code (e.g., 308), that code is used for all redirects regardless of method. The caller is responsible for choosing a semantically appropriate code.
20. Setting `RedirectCode` to a value outside the 3xx range causes a panic at the time the first redirect is issued.

### 4.2 CaseInsensitive

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |

21. When `false` (the default), route matching is case-sensitive. `/Users` and `/users` are distinct paths.
22. When `true`, route matching is case-insensitive. `/Users`, `/users`, and `/USERS` all match a route registered as `/users`. The matched parameter values retain the original case from the request path, not the pattern.
23. Case-insensitive matching has a performance cost relative to case-sensitive matching. It must not be enabled unless required.
24. Case-insensitive matching applies to static segments and parameter separators only. Parameter values are always captured as-is from the request.

### 4.3 UseRawPath

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |

25. When `false` (the default), the router uses `r.URL.Path` for route matching and parameter extraction.
26. When `true`, the router uses `r.URL.RawPath` if it is non-empty, and falls back to `r.URL.Path` when `RawPath` is empty. This is useful for routes where path parameters may contain percent-encoded slashes (`%2F`).

### 4.4 UnescapePathValues

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `true` |

27. When `true` (the default), captured path parameter values are URL-decoded using `url.QueryUnescape` before being stored in `Params`.
28. When `false`, parameter values are stored as raw strings. Percent-encoding is not decoded. This is useful when the application needs to handle encoding itself.
29. If URL decoding of a parameter value fails (malformed percent-encoding), the raw value is stored and no error is returned.
