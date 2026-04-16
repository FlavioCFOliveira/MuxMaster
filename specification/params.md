# Path Parameters

## Scope

This file specifies the `Param` type, the `Params` type and its methods, the `PathParam` and `ParamsFromContext` access functions, the `RoutePattern` function, and the internal `sync.Pool` used to recycle parameter slices.

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
6. At most `maxParams` (16) parameters are stored per request. A pattern with more than 16 wildcard tokens causes a panic at registration time.

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
25. The returned `Params` slice is a copy owned by the context. It is safe to retain across goroutines.

---

## 4. RoutePattern

26. `RoutePattern(r *http.Request) string` returns the matched route pattern for the current request.
27. Example: a request to `/users/42` matched by the pattern `/users/:id` causes `RoutePattern` to return `"/users/:id"`.
28. `RoutePattern` returns `""` when called outside a handler that was dispatched by the router (for example, in pre-routing middleware or in a manually constructed request).
29. The primary use cases for `RoutePattern` are structured logging, metrics (to avoid high-cardinality labels from raw paths), distributed tracing (span names), and OpenAPI generation.
30. `RoutePattern` is a package-level function.

---

## 5. Pool Behavior (Internal)

This section describes internal behavior. It is specified here because it affects the observable contract around parameter lifetime.

31. The router uses a `sync.Pool` to recycle `Params` slices and avoid per-request heap allocation.
32. The pool pre-allocates slices with a capacity of `maxParams` (16).
33. When the router finds a matching handler:
    - If the route has no parameters, the pool slice is returned to the pool immediately after the handler lookup. No `Params` value is placed in the context.
    - If the route has parameters, the router copies the parameters into a new slice (`make(Params, n)` + `copy`), places the copy in the request context, and returns the pool slice to the pool. The copy is what `ParamsFromContext` and `PathParam` access.
34. The copy ensures that the `Params` slice in the request context is not shared with or overwritten by concurrent requests using the pool.
35. The pool slice must never be retained beyond the route lookup phase. Handler code always receives the safe copy via the context.
