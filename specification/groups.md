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
24. The original (unstripped) path is available via `r.URL.RawPath` or via the `RoutePattern` function if the mounted handler is a `*Mux`. See section 10 for the precise rule governing when `r.URL.RawPath` is preserved (stripped of the matched prefix) versus set to the empty string on the request copy.
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

---

## 8. Location Rewriting for Automatic Redirects Through Mount

31. When the handler passed to `Mount` is itself a `*muxmaster.Mux` (an "inner `Mux`"), and that inner `Mux`, while serving the request forwarded to it with the prefix stripped (requirement 23), issues one of its own automatic redirects — a trailing-slash redirect ([routing.md](routing.md) section 4.4) or a fixed-path redirect ([routing.md](routing.md) section 4.5) — the outer `Mux` rewrites the `Location` header value before the response reaches the client, so that the redirect target is expressed in the client's original URL space: the path the inner `Mux` computed, with the mount prefix prepended. The status code, the query string, and every other header and body byte of the response are unchanged; only the `Location` header's path component is rewritten. Without this rewriting, the inner `Mux` computes its redirect target from the already-stripped path, producing a `Location` that omits the mount prefix and sends the client outside the mount — the defect this section corrects.
32. This rewriting is recursive across a chain of nested mounts. For example, with `a.Mount("/v2", b)` and, on `b`, `b.Mount("/admin", c)`, a redirect issued by `c` while handling a request forwarded through both mounts is rewritten at each mount boundary as the response passes back through it, so the final `Location` the client receives carries every prefix in the chain, in order (`/v2/admin/...` in this example). This holds regardless of which `Mux` in the chain is the one that actually computed the redirect: an intermediate mount that did not itself produce the redirect still forwards and extends the rewriting performed by an inner mount.
33. The rewritten `Location` is constructed as if the mounted route tree, at every level of the chain, had been registered directly on the outermost `Mux` at the fully composed path (the concatenation of every mount prefix in the chain with the inner pattern): the existing redirect-target encoding rules ([routing.md](routing.md) section 8, requirements 80-81 — control-byte percent-encoding and backslash percent-encoding) apply to the composed target as a whole, and the query string is preserved unchanged exactly as requirement 80 already specifies. This requirement governs only the `Location` header of an automatic redirect response; it does not change `RawPath` propagation, which is governed separately by requirement 24 and section 10 below.
34. This rewriting applies only to a redirect that MuxMaster's own automatic-redirect mechanism produced ([routing.md](routing.md) sections 4.4 and 4.5). It does not apply to:
    - A redirect issued by application code registered as a handler anywhere in the mounted tree — for example, a handler that calls `http.Redirect` directly, or sets the `Location` header itself. Such a redirect reaches the client exactly as the application produced it, with no rewriting of any kind, exactly as it would for a route registered directly (not through `Mount`).
    - A redirect (or any other response) produced by a mounted handler that is not a `*muxmaster.Mux` — for example, `http.FileServer`'s directory-redirect behavior, or a third-party router mounted via `Mount`. `Mount` accepts any `http.Handler` (requirement 26); this rewriting is available only when the immediate mounted handler is recognizably a `*muxmaster.Mux`, because only then can the outer `Mux` distinguish its own automatic redirects from an arbitrary response the handler chooses to write.
35. This rewriting applies identically whether `Mount` was called directly on a `*Mux` or via `(*Group).Mount`. When called via a group that has its own middleware, the mounted handler is additionally wrapped with that middleware before being registered (requirement 22); this wrapping does not prevent the rewriting described in this section from applying when the handler wrapped is a `*muxmaster.Mux`.

---

## 9. Mount Prefix Validation

36. Calling `Mount` with a prefix whose last element, after any trailing `/` has been removed (requirement 25), is an optional parameter — `{/:name}` or `{/:name:expr}` ([routing.md](routing.md) section 1.6) — causes a panic at registration time:

    ```
    muxmaster: Mount prefix '<prefix>' ends with an optional parameter; Mount does not support an optional parameter as the last element of its prefix
    ```

    where `<prefix>` is the exact combined prefix passed to `Mount` — for `(*Group).Mount`, the group prefix and the local prefix already concatenated (requirement 22), before the trailing-`/` normalization of requirement 25 is applied. To register both the with-segment and without-segment forms, either move a static path element after the optional segment (see requirement 37), or make two separate `Mount` calls, one for each fully expanded prefix.

    Without this check, such a prefix reaches the general route-registration machinery already described in requirement 28: the optional parameter's two expanded forms ([routing.md](routing.md) section 1.6, requirement 25) each attach a wildcard directly adjacent to the internal `mux_mount` catch-all at the same tree position, and registration panics with an unrelated, internal wildcard-conflict message that names neither the Mount prefix nor the real cause.

37. This restriction applies only to an optional parameter that is the prefix's own last element. Every other prefix shape already valid under this specification continues to work exactly as before, and is not affected by requirement 36:
    - A static prefix (requirement 25).
    - A prefix ending in, or containing, a plain named parameter or a regex parameter.
    - An optional parameter that is not the prefix's last element — for example, `/v2{/:id}/admin` — because the static element following the optional segment (`/admin`) absorbs both of the segment's expansions before the internal `mux_mount` catch-all is reached, so the conflict described in requirement 36 does not occur.

---

## 10. RawPath Propagation Through Mount

38. This rule applies only when the Mount prefix contains a parameter token — a named parameter, a regex parameter, or a non-trailing optional parameter (requirement 37). Rule 39 governs a prefix that is entirely static; the two are mutually exclusive and together cover every valid Mount prefix. When the mounted handler receives its shallow request copy (requirement 23) and `r.URL.RawPath` is non-empty, the copy's `URL.RawPath` is derived as follows:
    1. The router determines the *matched prefix*: `r.URL.Path` with its trailing `mux_mount`-captured suffix (requirement 28) removed. Let *k* be the number of segments (see the Terminology section in [README.md](README.md)) in the matched prefix.
    2. If `r.URL.RawPath` has at least *k* segments, and each of its first *k* segments, percent-decoded independently, is byte-for-byte identical to the corresponding segment of the matched prefix, then the match is *decode-consistent*: the copy's `URL.RawPath` is set to the remaining segments of `r.URL.RawPath` — everything after the *k*th segment, including its leading `/` — with their original percent-encoding preserved unchanged.
    3. Otherwise, the copy's `URL.RawPath` is set to the empty string. This includes the case where a percent-encoded `/` inside one of the first *k* raw segments shifts the raw segment boundaries out of alignment with the decoded ones, and any other case where decoding the first *k* raw segments does not reproduce the matched prefix exactly.

    Rule 38 compares against the segments actually captured for the request, not against the literal prefix pattern's `:name` / `{name:expr}` / `{/:name}` text. Consequently, `RawPath` is preserved (rule 38.2) whenever the captured segment's raw form decodes cleanly, and is zeroed (rule 38.3) only when it does not — rather than being unconditionally zeroed merely because the literal pattern text can never equal an actual request path.

39. For a Mount prefix that is entirely static (no parameter token), `RawPath` continues to follow the algorithm already in effect before this section was added, unchanged: the copy's `URL.RawPath` is set to `r.URL.RawPath` with the literal, registered prefix text removed from its front (a plain byte-for-byte `TrimPrefix`, not the segment-wise decoding of rule 38), and set to the empty string whenever that literal removal does not apply cleanly — the prefix's exact bytes are not a leading substring of `r.URL.RawPath`, or removing them would leave a remainder not itself starting with `/` (an encoded `/` immediately after the removed prefix, which would otherwise disguise a stale encoded prefix fragment as the start of the forwarded path). This algorithm never percent-decodes the candidate prefix bytes before comparing them, unlike rule 38.

40. Rule 39's literal comparison has an observable consequence for a static prefix that rule 38's decode-consistent comparison does not share: a request whose `RawPath` percent-encodes one or more bytes of the static prefix itself has its `RawPath` zeroed, even though the request's decoded path matches the registered prefix exactly and would satisfy rule 38's decode-consistency check if that check were applied to it. For example, with a Mount prefix `/api` and a request whose raw target is `/%61pi/v1/resource` (`%61` decodes to `a`, so `r.URL.Path` is `/api/v1/resource` and the prefix matches), the request copy's `r.URL.RawPath` is `""`, not `/v1/resource`. This is deliberate, not an oversight: it preserves the exact behavior already relied upon before this section existed (a pinned regression, `TestMountRawPathNormalisedOnMismatch`), and rule 39 is never merged with rule 38's segment-wise algorithm — the latter exists only to correctly handle a captured parameter value, never to relax the static case.
