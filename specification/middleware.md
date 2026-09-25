# Middleware

## Scope

This file specifies the middleware type, the four middleware scopes (pre-routing, global, group, and per-route), the execution order, and the rules governing when middleware is applied.

This file does not cover the stdlib middleware sub-package (see [middleware-stdlib.md](middleware-stdlib.md)), group-specific middleware registration (see [groups.md](groups.md)), or per-route middleware on `HandlerFuncE` handlers (see [error-handling.md](error-handling.md)).

---

## 1. Middleware Type

1. The middleware type is `func(http.Handler) http.Handler`. This is the standard Go middleware type and is compatible with all existing Go HTTP middleware libraries (alice, negroni, and others) without adaptation.
2. There is no custom `MiddlewareFunc` alias exported by the package. The type is used directly as `func(http.Handler) http.Handler`.
3. A middleware function receives the next handler in the chain and returns a new handler that wraps it.

---

## 2. Middleware Scopes

There are four middleware scopes. Each scope defines which requests a middleware applies to.

### 2.1 Pre-Routing Middleware

4. Pre-routing middleware is registered via `r.Pre(middleware ...func(http.Handler) http.Handler)`.
5. Pre-routing middleware executes before route lookup. It can inspect or modify the request, including `r.URL.Path`, before the router selects a route.
6. Pre-routing middleware wraps the entire router. It sees every request, including requests that result in 404 or 405.
7. Because pre-routing middleware runs before route lookup, it does not have access to path parameters or the matched route pattern.
8. Pre-routing middleware is applied in registration order. The first `Pre` call registers the outermost middleware.

### 2.2 Global Middleware

9. Global middleware is registered via `r.Use(middleware ...func(http.Handler) http.Handler)`.
10. Global middleware wraps every handler registered after the `Use` call. It does not wrap handlers registered before the `Use` call.
11. Global middleware wraps every internally generated response the router produces on its own, not only explicitly registered route handlers. This includes:
    - the 404 handler (`NotFound`) (see [error-handling.md](error-handling.md) requirement 5);
    - the 405 handler (`MethodNotAllowed`) (see [error-handling.md](error-handling.md) requirement 10);
    - the automatic OPTIONS response, whether or not `GlobalOPTIONS` is set (see [error-handling.md](error-handling.md) requirement 21);
    - the TSR and fixed-path redirect response (see [routing.md](routing.md) requirements 52 and 56).

    For all four, the middleware binding is dynamic rather than fixed at registration time: the router builds and caches a middleware-wrapped handler for each of them, invalidates that cache on every subsequent `Use` call (and on `Rebuild`), and rebuilds it — with whatever `Use` chain is registered at that moment — the next time it is needed. The wrapping therefore always reflects the most recently registered `Use` chain as of when the response is produced, never the chain in effect at some earlier point such as when `NotFound` was assigned or when a particular route was registered. This is the opposite binding rule from ordinary route handlers, whose middleware is fixed at the moment `Handle` is called and never changes afterward (requirement 12 below).
12. Global middleware is applied at registration time (see [routing.md](routing.md) requirement 44). There is no per-request overhead for global middleware.
13. Multiple `Use` calls append to the middleware chain. Earlier calls are outermost.

### 2.3 Group Middleware

14. Group middleware is registered via `g.Use(middleware ...func(http.Handler) http.Handler)`.
15. Group middleware wraps every handler registered on that group after the `Use` call.
16. Group middleware is inner relative to global middleware. For a route registered on a group, global middleware wraps group middleware, which wraps the handler.
17. Group middleware from a parent group is inherited by sub-groups created after the middleware is registered. Sub-groups receive a copy of the parent's middleware slice at the time `Group` is called.
18. Group middleware is applied at registration time.

### 2.4 Per-Route Middleware

19. Per-route middleware is registered via `r.With(middleware ...func(http.Handler) http.Handler)`. `With` returns a scoped router that applies the given middleware only to routes registered on that scoped router.
20. `With` does not mutate the original `*Mux`. It returns a new value (a scope object). Routes registered on the original `*Mux` are not affected.
21. Per-route middleware is inner relative to group middleware.
22. Per-route middleware is applied at registration time.

```go
// Example
mux.With(authMiddleware, rateLimiter).GET("/admin", adminHandler)
```

---

## 3. Execution Order

23. For a request matched to a route that has all four scopes active, middleware executes in this order:

```
Pre-routing middleware (outermost)
    → [route lookup]
    → Global middleware
        → Group middleware
            → Per-route middleware
                → Handler (innermost)
```

24. Within each scope, middleware registered first is outermost (executes first on the way in, last on the way out).
25. Pre-routing middleware is the only scope that executes before route lookup. All other scopes execute after the route has been identified.

---

## 4. Application-Time vs. Request-Time

26. Middleware is applied at registration time, not at request time. This is the central performance characteristic of MuxMaster middleware.
27. At registration time, `wrapMiddleware` is called. It iterates the middleware slice in reverse and wraps the handler. The resulting closure is stored in the radix tree leaf.
28. At request time, the stored closure is called directly. There is no middleware chain traversal, no slice iteration, and no allocation associated with middleware dispatch.
29. This design means `Use` must be called before the routes it is intended to wrap. Calling `Use` after registering a route does not affect that route.

---

## 5. Nil and Empty Middleware

30. Passing a nil function as a middleware argument to `Use`, `Pre`, or `With` causes a panic.
31. Passing zero arguments to `Use`, `Pre`, or `With` is a no-op.

---

## 6. FastMiddleware

32. `FastMiddleware` is a middleware type reserved for `FastHandler` routes: `type FastMiddleware func(FastHandler) FastHandler`. It follows the same wrap-the-next-handler composition model as the stdlib middleware type (section 1), but operates on `FastHandler` (`func(http.ResponseWriter, *http.Request, Params)`) instead of `http.Handler`. There is no automatic conversion between the two middleware types; they are structurally incompatible.
33. `FastMiddleware` is registered via `(*Mux).UseFast(mw ...FastMiddleware)` or `(*Group).UseFast(mw ...FastMiddleware)`. Each call appends to the fast-route middleware chain in effect on that `Mux` or `Group`.
34. `FastMiddleware` registered on a `*Mux` wraps every `HandleFast` route registered on that `*Mux` after the `UseFast` call. `FastMiddleware` registered on a `*Group` wraps every `HandleFast` route registered on that group after the call.
35. `FastMiddleware` is applied at registration time, exactly like stdlib middleware (section 4): the chain is composed in reverse order at the moment `HandleFast` is called, and the resulting wrapped `FastHandler` is stored in the tree. There is no per-request chain traversal and no allocation associated with `FastMiddleware` dispatch.
36. Within the `FastMiddleware` chain, the middleware registered first is outermost — the same ordering semantics as stdlib middleware (section 3, requirement 24).
37. `FastMiddleware` has no effect on routes registered via `Handle`. Stdlib middleware (registered via `Use`) has no effect on routes registered via `HandleFast`. The two chains are entirely independent; see section 7 for the full coverage matrix.
38. Registering a route via `HandleFast` on a `*Mux` or `*Group` that already has one or more stdlib middleware registered via `Use` (or `Group.Use`) at that point causes a panic. This guard exists to prevent the mistake of assuming that `Use`-registered middleware (for example, an authentication gate) protects fast routes. See section 7, requirement 43, for the exact evaluation-order caveat.

---

## 7. Route-Type Coverage Matrix (Pre vs. Use vs. UseFast)

39. `Handle` routes (`http.Handler`) and `HandleFast` routes (`FastHandler`) are wrapped by different, non-overlapping sets of middleware:

    | Middleware family | Registered via | Wraps `Handle` routes? | Wraps `HandleFast` routes? |
    |---|---|---|---|
    | Pre-routing | `Pre` | Yes | Yes |
    | Global (stdlib) | `Use` | Yes | No |
    | Group (stdlib) | `Group.Use` | Yes | No |
    | Per-route (stdlib) | `With` | Yes | No |
    | Fast (global) | `UseFast` | No | Yes |
    | Fast (group) | `Group.UseFast` | No | Yes |

40. `Pre` is the only middleware family that uniformly covers both `Handle` and `HandleFast` routes, because it runs outside route dispatch, before the router has determined which route type will handle the request (section 2.1).
41. A policy that must apply uniformly to every request regardless of route type — for example, an authentication gate, request-ID assignment, or path cleaning — MUST be registered via `Pre`. Registering it only via `Use` leaves every `HandleFast` route unprotected by that policy.
42. A policy that applies only to `FastHandler` routes must be implemented as a `FastMiddleware` and registered via `UseFast` (or `Group.UseFast`). Stdlib middleware cannot be adapted to wrap a `FastHandler`, because the two middleware function signatures are incompatible.
43. The panic described in section 6, requirement 38, is evaluated only at the moment `HandleFast` is called, and only checks whether stdlib middleware was already registered via `Use` at that point. It does not guard the reverse ordering: calling `Use` (or `Group.Use`) after one or more `HandleFast` routes have already been registered succeeds without a panic, and the newly added stdlib middleware does not wrap those routes, nor any `HandleFast` route registered afterward on that `Mux` or `Group`. An operator auditing middleware coverage must inspect both the relative order of `Use`/`UseFast` calls and the relative order of `Handle`/`HandleFast` registrations, not merely whether each was called.
