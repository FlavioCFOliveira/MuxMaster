# Path Parameters

## Scope

This file specifies the `Param` type, the `Params` type and its methods, the `PathParam` and `ParamsFromContext` access functions, the `RoutePattern` function, and the internal parameter-storage and pooling mechanisms that affect parameter lifetime.

This file does not cover how parameters are extracted from the URL path during tree traversal (see [routing.md](routing.md)), or how parameters are stored in the request context (that is an implementation detail referenced here only where it affects observable behavior).

---

## 1. The Param Type

1. `Param` is a struct with two exported string fields: `Key` and `Value`.
2. `Key` holds the parameter name without the leading `:` or `*` prefix.
3. `Value` holds the raw captured string from the URL path. URL percent-encoding in the value is decoded before storage when `UnescapePathValues` is true (see [configuration.md](configuration.md)).

```go
type Param struct {
    Key   string
    Value string
}
```

---

## 2. The Params Type

4. `Params` is a named slice type: `type Params []Param`.
5. Parameters appear in `Params` in the order they appear in the pattern, from left to right.
6. There is no fixed upper limit on the number of parameters a pattern may declare, and registering a pattern with many parameters never causes a panic. Internally, the first 3 parameters captured for a request are held in a fixed-size stack buffer; a route with more than 3 parameters spills the remainder into one additional heap-allocated slice for that request. This affects allocation count only (see [performance.md](performance.md) section 8) and never truncates or drops a captured parameter.

### 2.1 Get

7. `(ps Params) Get(name string) string` returns the value of the first `Param` in `ps` whose `Key` equals `name`.
8. If no parameter with that name exists, `Get` returns the empty string `""`.
9. `Get` does not distinguish between a missing key and a key whose value is the empty string. Use `Lookup` to make that distinction.

### 2.2 Lookup

10. `(ps Params) Lookup(name string) (value string, ok bool)` returns the value and a boolean indicating whether the key was found.
11. If the key is not found, `Lookup` returns `("", false)`.
12. If the key is found with an empty value, `Lookup` returns `("", true)`.

### 2.3 Type-Conversion Helpers

13. Each helper below looks up the named parameter in `ps` and converts its string value to the target type. If the key is not found or the conversion fails, the helper returns the zero value for that type and a non-nil error.
14. The error returned when the key is not found is distinct from the error returned when the key exists but the value cannot be converted.

| Method signature | Converts to | Zero value on error |
|---|---|---|
| `Int(name string) (int, error)` | `int` via `strconv.Atoi` | `0` |
| `Int64(name string) (int64, error)` | `int64` via `strconv.ParseInt` (base 10, 64-bit) | `0` |
| `Uint64(name string) (uint64, error)` | `uint64` via `strconv.ParseUint` (base 10, 64-bit) | `0` |
| `Float64(name string) (float64, error)` | `float64` via `strconv.ParseFloat` (64-bit) | `0` | 
| `Bool(name string) (bool, error)` | `bool` via `strconv.ParseBool` | `false` |

15. Type-conversion helpers do not modify `ps`.

### 2.4 Map

16. `(ps Params) Map() map[string]string` returns a new `map[string]string` containing all key-value pairs from `ps`.
17. If `ps` contains duplicate keys (which only occurs with regex or optional parameters in edge cases), later values overwrite earlier ones in the returned map.
18. `Map` allocates a new map on every call. It must not be called on the hot path when performance matters.

---

## 3. Accessing Parameters in Handlers

### 3.1 PathParam

19. `PathParam(r *http.Request, name string) string` returns the value of the named path parameter from the current request.
20. `PathParam` is a package-level function, not a method. Its signature never requires a custom context type.
21. `PathParam` is equivalent to `ParamsFromContext(r.Context()).Get(name)`.
22. If no parameter named `name` exists for the current request, `PathParam` returns `""`.

### 3.2 ParamsFromContext

23. `ParamsFromContext(ctx context.Context) Params` returns the `Params` stored in `ctx` by the router.
24. If no parameters are present (e.g., the route is static), `ParamsFromContext` returns a nil `Params` slice. Calling `Get` on a nil `Params` slice returns `""` without panicking.
25. The returned `Params` slice is owned by the request context. When `Mux.PoolRequestBundle` is `false` (the default), it is safe to retain across goroutines. When `Mux.PoolRequestBundle` is `true`, it is NOT safe to retain past the handler's return — see [configuration.md](configuration.md) section 4.6 for the full lifetime contract.

---

## 4. RoutePattern

26. `RoutePattern(r *http.Request) string` returns the matched route pattern for the current request.
27. Example: a request to `/users/42` matched by the pattern `/users/:id` causes `RoutePattern` to return `"/users/:id"`.
28. `RoutePattern` returns `""` when called outside a handler that was dispatched by the router (for example, in `Pre` middleware, which runs before route lookup, or in a manually constructed request).
29. `RoutePattern` also returns `""` for a static route — one whose pattern declares no named, regex, or catch-all parameter — because only a route with at least one parameter stores its pattern in the request context (see [introspection.md](introspection.md) section 5, rule 27). For the same reason, it returns `""` inside `NotFound`, `MethodNotAllowed`, the automatic `OPTIONS` responder, and a trailing-slash or fixed-path redirect handler: none of these carries a stored pattern.
30. A static route belonging to a `*Mux` attached with `Mount` is the one exception to rule 29: `RoutePattern` returns the mount's own pattern, `prefix + "/*mux_mount"`, because the context stores the nearest parameterized match, and `Mount` registers the mounted sub-router behind a catch-all (see [groups.md](groups.md)).
31. The primary use cases for `RoutePattern` are structured logging, metrics (to avoid high-cardinality labels from raw paths), distributed tracing (span names), and OpenAPI generation.
32. `RoutePattern` is a package-level function.

---

## 5. Parameter Storage and Pooling (Internal)

This section describes internal behavior. It is specified here because it affects the observable contract around parameter lifetime.

31. During route lookup, captured parameters are accumulated in a fixed-size, stack-allocated buffer (holding up to 3 parameters inline) that does not itself escape to the heap. A route with more than 3 parameters spills the extra parameters into one heap-allocated slice, created fresh for that request.
32. For `Handle` routes, once lookup completes, the router places the captured parameters where `ParamsFromContext` and `PathParam` can find them:
    - Static pattern (no parameters): no `Params` value is placed in the context; `ParamsFromContext` returns `nil`.
    - 1, 2, or 3 parameters: the parameter values are stored inline in the same single allocation as the request-scoped context wrapper (see [performance.md](performance.md) section 8, Tiered Request Bundle). No separate `Params` slice is allocated for these cases.
    - More than 3 parameters: the first 3 are stored inline as above; the remainder are held in a second, separately allocated `Params` slice.
33. For `FastHandler` routes, the captured parameters are copied into a `Params` slice passed directly as the handler's third argument, never through the request context (see [performance.md](performance.md) section 6).
34. Whether the underlying storage is drawn from a `sync.Pool` and recycled after the handler returns is controlled independently for each route type: `Mux.PoolRequestBundle` for `Handle` routes and `Mux.PoolFastParams` for `FastHandler` routes (see [configuration.md](configuration.md) sections 4.6 and 4.5). Both default to `false`.
35. When the relevant pooling flag is `false` (the default for both), the `Params` slice — and, for `Handle` routes, the request context and `*http.Request` that carry it — remain valid indefinitely after the handler returns and are safe to retain across goroutines.
36. When the relevant pooling flag is `true`, the underlying storage is returned to a `sync.Pool` the instant the handler returns. Handlers MUST NOT retain the `Params` slice — nor, for pooled `Handle` routes, the `*http.Request` — past their return. See [configuration.md](configuration.md) sections 4.5 and 4.6 for the full lifetime contract.

---

## 6. Captured Values Are Never Empty

37. As of [routing.md](routing.md) section 12, a named parameter and a regex parameter never capture an empty string: `Value` is always at least one byte long for a `Param` produced by either kind. A catch-all parameter's captured value is also never empty, because a catch-all pattern requires a literal `/` immediately preceding the `*` token (routing.md rule 16), so its minimum possible capture is the single character `/`. Consequently, every `Param` produced by ordinary route matching has a non-empty `Value`. Rule 12 remains a correct, general statement of the `Lookup` API's contract for an empty-valued key; as of this rule, it no longer describes a case that ordinary route matching itself can produce.
