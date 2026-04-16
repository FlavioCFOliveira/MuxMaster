# Introspection

## Scope

This file specifies the programmatic route lookup function `Lookup`, the route listing functions `Routes` and `Walk`, and the `RoutePattern` function.

`RoutePattern` is also described in [params.md](params.md) from the handler perspective. This file specifies it from the router implementation perspective.

This file does not cover how routes are matched during normal request dispatch (see [routing.md](routing.md)).

---

## 1. Lookup

1. `(*Mux).Lookup(method string, path string) (http.Handler, Params, bool)` performs a route lookup without dispatching a request and without any side effects.
2. `Lookup` does not issue redirects, does not call `NotFound` or `MethodNotAllowed`, and does not trigger `PanicHandler`.
3. `Lookup` returns three values:
    - `handler`: the matched `http.Handler` including all applied middleware, or `nil` if no route matches.
    - `params`: the extracted `Params` for the matched route, or a nil slice if no route matches or the route has no parameters.
    - `found`: `true` if a handler was found, `false` otherwise.
4. The `Params` slice returned by `Lookup` is a new allocation. It is safe to retain. The caller owns it.
5. `Lookup` is safe for concurrent use. It uses the same read lock as `ServeHTTP`.
6. `Lookup` is intended for: writing tests that verify routing behavior, building reverse proxies that need to inspect handlers before forwarding, and generating URL patterns for documentation.
7. `Lookup` with an empty `path` or a path that does not begin with `/` returns `(nil, nil, false)`. It does not panic.

---

## 2. RouteInfo

8. `RouteInfo` is a struct:

```go
type RouteInfo struct {
    Method  string
    Pattern string
    Handler string
}
```

9. `Method` is the HTTP method string (e.g., `"GET"`).
10. `Pattern` is the registered path pattern (e.g., `"/users/:id"`).
11. `Handler` is the fully qualified function name of the innermost handler, obtained via `runtime.FuncForPC`. If the handler is a closure or an anonymous function, the name reflects what the runtime assigns.

---

## 3. Routes

12. `(*Mux).Routes() []RouteInfo` returns a slice containing one `RouteInfo` entry for every registered route.
13. The order of entries in the returned slice is not specified. Callers must not rely on any particular order.
14. Mount points (registered via `Mount`) appear as a single entry with the pattern `prefix/*` and `Handler` set to the string representation of the mounted handler.
15. Routes registered for multiple methods via `ANY` or `Match` appear as separate entries, one per method.
16. `Routes` is safe for concurrent use.
17. `Routes` allocates a new slice and `RouteInfo` structs on every call. It must not be called on the hot path.

---

## 4. Walk

18. `(*Mux).Walk(fn func(method, pattern string, handler http.Handler) error) error` calls `fn` once for each registered route.
19. If `fn` returns a non-nil error, `Walk` stops immediately and returns that error.
20. If `fn` returns nil for every route, `Walk` returns nil.
21. The order in which routes are visited is not specified.
22. Modifying the router (registering or removing routes) from within `fn` is not permitted and produces undefined behavior.
23. `Walk` is safe for concurrent use when no route registration is in progress.
24. The `handler` passed to `fn` is the fully wrapped handler (including all middleware). It is the same value that `Lookup` returns.

---

## 5. RoutePattern (Implementation Perspective)

25. The router stores the matched pattern string in the request context when a route is matched. The key is an unexported context key defined in `params.go`.
26. The pattern is stored alongside the `Params` slice. Both use the same `withParams` call internally.
27. `RoutePattern(r *http.Request) string` reads this value from the context and returns it.
28. The pattern value stored is the original pattern as registered (e.g., `/users/:id`), not a normalized or compiled form.
29. `RoutePattern` returns `""` when no pattern is stored in the context. This occurs for requests handled by `NotFound`, `MethodNotAllowed`, TSR redirects, fixed-path redirects, and OPTIONS auto-responses.
