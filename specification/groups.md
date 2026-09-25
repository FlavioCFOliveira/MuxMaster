# Groups

## Scope

This file specifies the `Group` type, group creation, the `Route` inline group function, the `Mount` method, and sub-group nesting.

This file does not cover the middleware application rules for groups (see [middleware.md](middleware.md)), path parameter syntax (see [routing.md](routing.md)), or how registered routes behave at request time (see [routing.md](routing.md)).

---

## 1. The Group Type

1. `Group` is a struct that holds a reference to the parent `*Mux`, a path prefix, and a middleware slice.
2. `Group` is obtained by calling `(*Mux).Group(prefix string) *Group` or `(*Group).Group(prefix string) *Group`.
3. `Group` does not create a new radix tree. All routes registered on a group are stored in the parent `*Mux` tree with the full resolved path.
4. `Group` has zero overhead at request time. There is no indirection through a group at request time.

---

## 2. Creating a Group

5. `(*Mux).Group(prefix string) *Group` returns a new `*Group` with the given prefix and an empty middleware slice.
6. The prefix must begin with `/`. A prefix that does not begin with `/` causes a panic.
7. The prefix may end with `/` or not. Both are valid. The final route path is produced by concatenating the group prefix and the route-local path without any normalization. It is the caller's responsibility to ensure the resulting path is valid.

---

## 3. Registering Routes on a Group

8. A `*Group` exposes the same route registration methods as `*Mux`: `Handle`, `HandleFunc`, `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `CONNECT`, `TRACE`, `QUERY`, `ANY`, and `Match`. `QUERY` is a standard HTTP method (RFC 10008); see [routing.md](routing.md) section 9 for its full semantics. `*Group` also exposes `HandleFast` (see [performance.md](performance.md) section 6) and `HandleE` plus its `...E` convenience methods, including `QUERYE` (see [error-handling.md](error-handling.md) section 5). Unlike `*Mux`, `*Group` has no dedicated `...Fast` convenience methods (such as `GETFast` or `QUERYFast`): a fast route on a group must be registered via `(*Group).HandleFast(method, pattern, h)` directly.
9. For each registration, the final pattern passed to the underlying `*Mux` is `group.prefix + path`.
10. The handler passed to the group is first wrapped with the group's middleware, then passed to `(*Mux).Handle`, where the mux's global middleware is applied. The resulting wrapped handler is stored in the tree.

---

## 4. Group Middleware

11. `(*Group).Use(middleware ...func(http.Handler) http.Handler)` appends middleware to the group's chain.
12. Group middleware must be called before the routes on the group that it is intended to wrap. Calling `Use` after registering a route on the group does not affect that route.
13. See [middleware.md](middleware.md) for the full execution order.

---

## 5. Sub-Groups

14. `(*Group).Group(prefix string) *Group` returns a new sub-group whose full prefix is `parentGroup.prefix + prefix`.
15. The sub-group receives a copy of the parent group's middleware slice at the time `Group` is called. Middleware added to the parent group after the sub-group is created does not affect the sub-group.
16. Sub-groups may be nested to any depth.

```go
// Example
api := mux.Group("/api/v1")
api.Use(apiKeyCheck)

admin := api.Group("/admin")   // prefix: /api/v1/admin
admin.Use(adminOnly)           // admin middleware: [apiKeyCheck, adminOnly] (copies + extends)
admin.DELETE("/users/:id", deleteUser)
// Full path: /api/v1/admin/users/:id
// Middleware chain (outermost first): globalMW → apiKeyCheck → adminOnly → deleteUser
```

---

## 6. Route — Inline Group

17. `(*Mux).Route(prefix string, fn func(*Group))` creates a group with the given prefix and calls `fn` with that group as the argument.
18. `(*Group).Route(prefix string, fn func(*Group))` creates a sub-group and calls `fn` with it.
19. `Route` is purely syntactic sugar over `Group`. The behavior is identical to creating a group, calling `fn` on it, and discarding the returned pointer.
20. `Route` does not return a value.

```go
// Example
mux.Route("/api/v1", func(api *muxmaster.Group) {
    api.Use(apiKeyCheck)
    api.GET("/users", listUsers)
    api.POST("/users", createUser)

    api.Route("/admin", func(admin *muxmaster.Group) {
        admin.Use(adminOnly)
        admin.DELETE("/users/:id", deleteUser)
    })
})
```

---

## 7. Mount — External Handler

21. `(*Mux).Mount(prefix string, h http.Handler)` registers `h` to handle all requests whose path begins with `prefix`.
22. `(*Group).Mount(prefix string, h http.Handler)` is equivalent to `(*Mux).Mount` with the group prefix prepended to `prefix`.
23. Before delegating to `h`, the router builds a shallow request copy (see the Terminology section in [README.md](README.md)) — a new `*http.Request` sharing the original's header map and context, with a new `*url.URL` copied from the original — and sets the copy's `URL.Path` to the remaining path after stripping `prefix`. If the remaining path is empty, the copy's `URL.Path` is set to `/`. `h` receives the copy; the original request passed to `ServeHTTP` is never mutated.
24. The original (unstripped) path is available via `r.URL.RawPath` or via the `RoutePattern` function if the mounted handler is a `*Mux`.
25. Trailing slashes on `prefix` are normalized: a trailing `/` is removed from `prefix` before matching.
26. `h` may be any `http.Handler`, including another `*Mux`, `http.ServeMux`, or a third-party router.
27. Routes registered via `Mount` do not appear in `Routes()` or `Walk()` output because their internal structure is opaque. Only the mount point itself is recorded.
28. The mount point registers a catch-all route internally: `Handle("*", prefix+"/*mux_mount", ...)`, using the fixed internal catch-all parameter name `mux_mount`. This means a mount at `/v2` handles `/v2/` and `/v2/anything` directly. A request to the bare prefix `/v2` (no trailing slash) does not match this catch-all directly: when `RedirectTrailingSlash` is `true` (the default), the router issues a trailing-slash redirect from `/v2` to `/v2/` (see [routing.md](routing.md) section 4.4 and requirement 52), which then reaches the mounted handler. When `RedirectTrailingSlash` is `false`, a request to the bare prefix results in 404 unless a separate route is registered for it. The value captured by `mux_mount` is the remaining path after the prefix, including its leading `/` — for example, a request to `/v2/a/b` captures `mux_mount = "/a/b"`, and a request to `/v2/` captures `mux_mount = "/"` — and that value is exactly the path used to build the mounted handler's `URL.Path`, as described in requirement 23. The name `mux_mount` is an internal implementation detail, not a user-configurable setting.
29. Calling `Mount` with a nil handler causes a panic.
30. Calling `Mount` with a prefix that does not begin with `/` causes a panic.

```go
// Example
mux.Mount("/legacy", legacyRouter)
// A request to /legacy/users is dispatched to legacyRouter with r.URL.Path = /users
```
