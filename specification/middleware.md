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
11. Global middleware does not wrap the 404 handler (`NotFound`), the 405 handler (`MethodNotAllowed`), the OPTIONS auto-response, or the TSR/fixed-path redirects. It only wraps explicitly registered route handlers.
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
