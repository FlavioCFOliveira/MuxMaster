# Configuration

## Scope

This file specifies all exported fields of the `Mux` struct, their types, their default values, and the exact behavior change when each field is set to a non-default value. It also specifies `(*Mux).Rebuild()`, the method that resets the configuration snapshot described in section 5.

This file does not cover the behavior of `NotFound`, `MethodNotAllowed`, `PanicHandler`, `GlobalOPTIONS`, and `ErrorHandler` in detail (see [error-handling.md](error-handling.md)). Redirect status codes are also governed by the matching rules in [routing.md](routing.md). It does not cover `FastHandler` dispatch mechanics or the tiered request bundle (see [performance.md](performance.md)) or `FastMiddleware` composition (see [middleware.md](middleware.md)).

---

## 1. Constructor

1. `New() *Mux` returns a `*Mux` with all handler fields set to nil. Its boolean feature flags are **not** uniformly `true`: `RedirectTrailingSlash`, `HandleMethodNotAllowed`, and `HandleOPTIONS` default to `true`; `RedirectFixedPath` defaults to `false` — a deliberate security default, because path canonicalization can bypass a Pre-gate or other middleware that inspects the raw, uncleaned path (see section 2.2 and [SECURITY.md](../SECURITY.md) for the rationale). Every advanced configuration field in section 4 (`CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `PoolFastParams`, `PoolRequestBundle`) also defaults to `false` via `New()`, exactly as it does for the zero value (rule 2); `New()` only elevates the three flags named above from their zero-value `false`.
2. The zero value of `Mux` (i.e., `Mux{}`) has all boolean fields set to `false`. This is a valid but minimally featured router — it additionally lacks the three flags `New()` sets to `true` (rule 1), so `RedirectTrailingSlash`, `HandleMethodNotAllowed`, and `HandleOPTIONS` are also `false` on a zero-value `Mux`, unlike on one returned by `New()`. Use `New()` for production use.

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
| Default (via `New`) | `false` |
| Zero value | `false` |

5. When `true`, the router computes `path.Clean(r.URL.Path)` and issues a redirect if the cleaned path has a registered handler and differs from the original path.
6. For GET and HEAD, the redirect code is 301. For all other methods, the redirect code is the value of `RedirectCode`.
7. When `false` (the default via both `New()` and the zero value — unlike `RedirectTrailingSlash`, `HandleMethodNotAllowed`, and `HandleOPTIONS`, `New()` does not enable this flag), no fixed-path redirect is issued. This is a deliberate security default, not an oversight: path canonicalization performed before a Pre-gate or other path-inspecting middleware has run can let a request bypass that middleware's check on the original, uncleaned path (see [SECURITY.md](../SECURITY.md)). An operator who enables `RedirectFixedPath` is responsible for confirming that no registered `Pre` middleware relies on inspecting the pre-canonicalization path.

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

18. When `RedirectCode` is `0`, the router uses 301 for GET and HEAD redirects and 307 for all other methods. This is the default behavior. This includes `QUERY`: although `QUERY` is safe and idempotent (RFC 10008 section 2), it is not GET or HEAD, so it receives 307 by default. See [routing.md](routing.md) section 4.4, rule 53, for why this does not weaken QUERY's semantics.
19. When `RedirectCode` is set to a non-zero value, that exact integer value is used as the response status code for every redirect the router issues, regardless of the request's method, and regardless of whether it is a semantically valid HTTP redirect status code.
20. **The router performs no validation of `RedirectCode`.** Setting it to a value outside the 3xx range — including a value that is not a valid HTTP status code at all — does not panic, at registration time, at the time the first redirect is issued, or ever. The unvalidated value is written directly as the response status code (via `w.WriteHeader`) on every redirect, and the surrounding response (headers, body) is otherwise constructed exactly as it would be for a valid 3xx code. The caller is entirely responsible for choosing a semantically appropriate code; misconfiguring `RedirectCode` produces a malformed or nonsensical response rather than a panic that would surface the mistake at development time.

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
| Default | `false` |

27. When `true`, captured path parameter values are URL-decoded using `url.QueryUnescape` before being stored in `Params`.
28. When `false` (the default, both via `New()` and the zero value), parameter values are stored as raw strings. Percent-encoding is not decoded. This is useful when the application needs to handle encoding itself, and it is also the safer default: decoding is opt-in specifically because combining it with `UseRawPath` requires the additional handler-side precautions in rule 30.
29. If URL decoding of a parameter value fails (malformed percent-encoding), the raw value is stored and no error is returned.
30. **Enabling `UseRawPath` and `UnescapePathValues` together (`PRF-2026-0002`) has a security-relevant consequence beyond either flag alone:** a captured parameter value can then contain a decoded literal `/` (from a `%2f` or `%2F` sequence in the raw path) that a plain, unescaped parameter value could never contain, because `UseRawPath` makes the router match against `r.URL.RawPath` (section 4.3) while `UnescapePathValues` still decodes the captured segment before storing it. The first time a `*Mux` with both flags enabled serves a request, it emits a one-time `slog.Warn`: `muxmaster: UseRawPath+UnescapePathValues enabled — captured path params may contain literal '/' from %2f decode. Handlers using params as filesystem/URL components MUST call path.Clean and reject values containing '..'. See SECURITY.md "UseRawPath traversal" (PRF-2026-0002).` A handler that uses such a parameter value to build a filesystem path or a URL must call `path.Clean` on it and reject any value containing `..`, exactly as the warning states; the router itself performs no such sanitization, because doing so unconditionally would defeat the purpose of enabling `UseRawPath` for operators who intentionally rely on encoded slashes.

### 4.5 PoolFastParams

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |

31. When `false` (the default), every `FastHandler` route (see performance.md section 6) with at least one path parameter receives a freshly allocated `Params` slice. The slice remains valid indefinitely after the handler returns; it is safe for the handler, or any goroutine it spawns, to retain and read it.
32. When `true`, the `Params` slice for `FastHandler` routes with 1, 2, or 3 parameters is drawn from a tiered `sync.Pool` and is cleared and returned to the pool immediately after the handler's call returns. Handlers MUST NOT retain the `Params` slice, or any element of it, past their return. A handler that needs the values afterward must copy them into a new slice before returning:

    ```go
    func myHandler(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
        cp := make(muxmaster.Params, len(ps))
        copy(cp, ps)
        go func() { use(cp) }() // safe — cp owns its data
    }
    ```

33. `FastHandler` routes with more than 3 parameters always allocate a fresh slice, regardless of `PoolFastParams`. Pooling covers only the 1-, 2-, and 3-parameter tiers.
34. `PoolFastParams` has no effect on `Handle` (stdlib `http.Handler`) routes. It governs only the `Params` argument passed to `FastHandler`. See section 4.6 for the equivalent pooling control on `Handle` routes.

### 4.6 PoolRequestBundle

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |

35. When `false` (the default), every `Handle` route with at least one path parameter allocates a fresh request bundle per request (see performance.md section 8, Tiered Request Bundle). Its lifetime is managed by the garbage collector; the handler, and any goroutine it spawns, may retain the `*http.Request` passed to them after the handler returns.
36. When `true`, the request bundle is drawn from a tiered `sync.Pool` and is zeroed and returned to the pool the instant the handler's `ServeHTTP` call returns. Handlers MUST NOT retain the `*http.Request` they were given past their return. A goroutine that captures `r` and outlives the handler may observe a recycled request that already belongs to a later, unrelated request — a use-after-free against the pooled bundle storage, not a supported pattern.
37. This lifetime contract is stricter than the general `net/http` contract, under which a handler may retain `r` indefinitely because `net/http` never recycles a `*http.Request` while any code might still reference it. `PoolRequestBundle` deliberately trades that guarantee for reduced allocation and lower latency. An operator must audit every handler reachable from a `Mux` with `PoolRequestBundle` enabled and confirm that none of them retain `r` past return before enabling it in production.
38. Before a bundle returns to the pool, its entire memory — the request context wrapper, the captured parameter values, and the embedded `*http.Request` copy — is zeroed. This prevents a value written directly into a bundle field by one handler from being visible to a later, unrelated request that reuses the same pooled bundle. It does not change how `net/http` itself shares underlying data (for example, `Header` map contents) between the original request and its copy; that sharing exists independently of pooling.
39. `PoolRequestBundle` has no effect on `FastHandler` routes, on static `Handle` routes (which never allocate a bundle), or on the `Params` slice passed to `FastHandler` (see section 4.5, `PoolFastParams`).

---

## 5. Configuration Snapshot and Rebuild

40. The router does not re-read its configuration fields on every request. On the first `ServeHTTP` call (or the first internal call that needs it), it captures a one-time, immutable snapshot of every feature flag in section 2, every advanced configuration field in section 4, and every handler field in section 3.
41. After the snapshot is captured, further mutation of these fields on the live `Mux` — for example, setting `mux.HandleOPTIONS = false` after the server has already handled a request — has no effect on request handling. Every subsequent `ServeHTTP` call continues to use the values captured in the snapshot.
42. `(*Mux).Rebuild()` discards the snapshot, along with the internally cached, middleware-wrapped `NotFound`, `MethodNotAllowed`, and OPTIONS auto-response handlers built from it. The next call that needs the snapshot rebuilds it from the current values of the `Mux` fields.
43. `Rebuild` is safe to call concurrently with `ServeHTTP`. Every request observes either the pre-`Rebuild` snapshot in full or the post-`Rebuild` snapshot in full; it never observes a partially rebuilt snapshot.
44. `Rebuild` is intended for tests and for scenarios where an operator deliberately reconfigures a running `Mux`. It affects only the configuration snapshot described in this section. It does not add, remove, or modify any registered route; route registration continues to follow the rules in routing.md and remains unsupported after the server has begun serving requests (see out-of-scope.md section 3.1).
45. Calling `Handle`, `HandleFunc`, `HandleFast`, `HandleE`, or any convenience registration method does not implicitly invoke `Rebuild`. The configuration snapshot must be reset explicitly.
