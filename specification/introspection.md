# Introspection

## Scope

This file specifies the programmatic route lookup function `Lookup`, the route listing functions `Routes`, `Walk`, and `WalkFast`, and the `RoutePattern` function.

`RoutePattern` is also described in [params.md](params.md) from the handler perspective. This file specifies it from the router implementation perspective.

This file does not cover how routes are matched during normal request dispatch (see [routing.md](routing.md)).

---

## 1. Lookup

1. `(*Mux).Lookup(method string, path string) (http.Handler, Params, bool)` performs a route lookup without dispatching a request and without any side effects.
2. `Lookup` does not issue redirects, does not call `NotFound` or `MethodNotAllowed`, and does not trigger `PanicHandler`.
3. `Lookup` returns three values:
    - `handler`: the matched `http.Handler` including all applied middleware, or `nil` if no route matches for the given method and path.
    - `params`: the extracted `Params` for the matched route, or a nil slice if no route matches or the route has no parameters.
    - `found`: `true` if a route was found, `false` otherwise.
4. `Lookup` covers both `Handle` and `HandleFast` routes uniformly, but `handler` is `nil` in two distinct cases that must not be confused: when `found` is `false` (no route matches at all), and when `found` is `true` but the matched route was registered via `HandleFast` rather than `Handle`. In the latter case, `params` is still populated with the captured path parameters. There is no dedicated lookup function for `FastHandler` routes; a caller that needs to distinguish "no route" from "a `FastHandler` route matched" must check `found` together with `handler == nil`.
5. The `Params` slice returned by `Lookup` is a new allocation. It is safe to retain. The caller owns it.
6. `Lookup` acquires the same `sync.RWMutex` (read lock) that guards route registration and the other introspection functions (`Routes`, `Walk`, `WalkFast`). This is a separate synchronization mechanism from `ServeHTTP`'s request-time dispatch path, which is lock-free (see [performance.md](performance.md) section 7, Lock-Free Dispatch): `ServeHTTP` never acquires this mutex. `Lookup`'s read lock only contends with a registration call (`Handle`, `HandleFast`, `Mount`, etc.) in progress on the same `*Mux`, or with a concurrent call to `Routes`, `Walk`, or `WalkFast`; it never blocks on, or is blocked by, concurrent request dispatch.
7. `Lookup` is intended for: writing tests that verify routing behavior, building reverse proxies that need to inspect handlers before forwarding, and generating URL patterns for documentation.
8. `Lookup` with an empty `path` or a path that does not begin with `/` returns `(nil, nil, false)`. It does not panic.

---

## 2. RouteInfo

9. `RouteInfo` is a struct:

```go
type RouteInfo struct {
    Method  string
    Pattern string
    Handler string
}
```

10. `Method` is the HTTP method string (e.g., `"GET"`), or the internal token `"*"` for a route registered via `Mount` or directly via `Handle("*", ...)` (see [routing.md](routing.md) section 2.1, rule 31, and [groups.md](groups.md) section 7).
11. `Pattern` is the registered path pattern (e.g., `"/users/:id"`), stored exactly as it was passed to the registration call — including, for a `Mount` entry, the full internal pattern the mount point actually registers (see rule 16 below).
12. `Handler` is the fully qualified function name of the innermost handler, obtained via `runtime.FuncForPC`. If the handler is a closure or an anonymous function, the name reflects what the runtime assigns. This applies identically whether the entry's underlying route is a `Handle` (`http.Handler`) or `HandleFast` (`FastHandler`) route; `Routes` (section 3) reports both, using the same `runtime.FuncForPC` mechanism for each.

---

## 3. Routes

13. `(*Mux).Routes() []RouteInfo` returns a slice containing one `RouteInfo` entry for every registered route, including both `Handle` routes and `HandleFast` routes.
14. The order of entries in the returned slice is not specified. Callers must not rely on any particular order.
15. Mount points (registered via `Mount`) appear as a single entry, with `Method` set to the internal token `"*"` and `Pattern` set to the router's full internal registration pattern for the mount point: `prefix` joined with the literal suffix `/*mux_mount` (the fixed catch-all parameter name; see [groups.md](groups.md) requirement 28) — for example, `Mount("/legacy", h)` produces the entry `Pattern: "/legacy/*mux_mount"`, not `"/legacy/*"`. `Handler` is set to the string representation of the mounted handler, obtained the same way as for any other route (rule 12); routes registered inside the mounted handler itself are not visible, because its internal structure is opaque to `Routes` (see [groups.md](groups.md) requirement 27).
16. Routes registered for multiple methods via `ANY` or `Match` appear as separate entries, one per method.
17. `Routes` is safe for concurrent use.
18. `Routes` allocates a new slice and `RouteInfo` structs on every call. It must not be called on the hot path.

---

## 4. Walk and WalkFast

19. `(*Mux).Walk(fn func(method, pattern string, handler http.Handler) error) error` calls `fn` once for each registered `Handle` (`http.Handler`) route. It does not call `fn` for a route registered via `HandleFast`: `WalkFast` (rule 25 below) is the dedicated function for those. A `*Mux` that has both `Handle` and `HandleFast` routes registered at different paths requires both `Walk` and `WalkFast` to enumerate its full route set.
20. If `fn` returns a non-nil error, `Walk` stops immediately and returns that error.
21. If `fn` returns nil for every route, `Walk` returns nil.
22. The order in which routes are visited is not specified.
23. Modifying the router (registering or removing routes) from within `fn` is not permitted and produces undefined behavior.
24. `Walk` is safe for concurrent use when no route registration is in progress.
25. The `handler` passed to `fn` is the fully wrapped handler (including all middleware). It is the same value that `Lookup` returns for a `Handle` route.
26. `(*Mux).WalkFast(fn func(method, pattern string, handler FastHandler) error) error` is the `HandleFast`-route equivalent of `Walk`: it calls `fn` once for each registered `HandleFast` route, and does not call `fn` for a route registered via `Handle`. In every other respect — error propagation and early stop (rule 20), unspecified visit order (rule 22), the prohibition on modifying the router from within `fn` (rule 23), and safety for concurrent use absent an in-progress registration (rule 24) — `WalkFast` follows the same rules as `Walk`, with `FastHandler` in place of `http.Handler` throughout.

---

## 5. RoutePattern (Implementation Perspective)

27. The router stores the matched pattern string in the request context only when the matched route declares at least one parameter (named, regex, or catch-all). A static route stores no pattern. The key is an unexported context key defined in `params.go`.
28. The pattern is stored alongside the `Params` slice. Both use the same `withParams` call internally.
29. `RoutePattern(r *http.Request) string` reads this value from the context and returns it.
30. The pattern value stored is the original pattern as registered (e.g., `/users/:id`), not a normalized or compiled form.
31. `RoutePattern` returns `""` when no pattern is stored in the context. This occurs for requests handled by `NotFound`, `MethodNotAllowed`, TSR redirects, fixed-path redirects, and OPTIONS auto-responses; for any static route (rule 27); and inside `Pre` middleware, which runs before route lookup (see [params.md](params.md) section 4, rules 28-30 for the full handler-facing statement, including the `Mount` exception).
