# Groups and Sub-Routers

Groups allow you to organize routes that share a common URL prefix and/or a common set of middleware. Sub-routers extend this further by allowing completely independent `*Mux` instances to be mounted at a path.

## Table of Contents

- [Creating a Group](#creating-a-group)
- [Group Middleware](#group-middleware)
- [Nested Groups](#nested-groups)
- [Inline Groups with Route](#inline-groups-with-route)
- [Scoped Middleware with With](#scoped-middleware-with-with)
- [Mounting Sub-Routers](#mounting-sub-routers)
- [Mounting on a Group](#mounting-on-a-group)
- [Serving Static Files from a Group](#serving-static-files-from-a-group)
- [Design Patterns](#design-patterns)

---

## Creating a Group

`Group` returns a `*Group` that shares the parent `*Mux` and prepends a path prefix to every route:

```go
mux := muxmaster.New()

api := mux.Group("/api/v1")
api.GET("/users", listUsers)      // → GET /api/v1/users
api.POST("/users", createUser)    // → POST /api/v1/users
api.GET("/users/:id", getUser)    // → GET /api/v1/users/:id
```

The group shares the same underlying tree as the parent router. Routes are registered directly into the parent mux with the full prefix appended.

---

## Group Middleware

Middleware applied to a group wraps only the routes of that group, after any mux-level middleware:

```go
mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout)) // applied to every route

api := mux.Group("/api/v1")
api.Use(requireAPIKey)              // applied only to /api/v1/* routes

api.GET("/users", listUsers)
// request path: Logger → requireAPIKey → listUsers

mux.GET("/health", health)
// request path: Logger → health (no requireAPIKey)
```

**Important:** call `Use` before registering routes on the group, for the same reason it must be called before routes on the mux.

---

## Nested Groups

Groups can be nested to any depth. Each level adds its prefix and optionally its own middleware:

```go
api := mux.Group("/api/v1")
api.Use(requireAPIKey)

// Sub-group for admin endpoints
admin := api.Group("/admin")
admin.Use(requireAdmin)

admin.GET("/stats", getStats)        // GET /api/v1/admin/stats
admin.DELETE("/users/:id", deleteUser) // DELETE /api/v1/admin/users/:id
```

Each group maintains an independent copy of its middleware list, so adding middleware to a sub-group does not affect the parent group.

---

## Inline Groups with `Route`

`Route` creates a sub-group and calls a closure with it. This is equivalent to calling `Group` manually, but keeps related routes visually grouped in the source code:

```go
mux.Route("/api/v1", func(api *muxmaster.Group) {
    api.Use(requireAPIKey)

    api.GET("/users", listUsers)
    api.POST("/users", createUser)

    api.Route("/admin", func(admin *muxmaster.Group) {
        admin.Use(requireAdmin)
        admin.DELETE("/users/:id", deleteUser)
        admin.GET("/stats", getStats)
    })
})
```

Groups can also call `Route` on themselves:

```go
api := mux.Group("/api/v1")
api.Route("/reports", func(g *muxmaster.Group) {
    g.GET("/daily", dailyReport)
    g.GET("/weekly", weeklyReport)
})
```

---

## Scoped Middleware with `With`

`With` returns a new group with additional middleware appended, without modifying the original group. It is useful for applying middleware to a single route:

```go
api := mux.Group("/api/v1")

// deleteUser is wrapped by both api's middleware and requireAdmin
api.With(requireAdmin).DELETE("/users/:id", deleteUser)

// processPayment is wrapped by both api's middleware and rateLimit + auditLog
api.With(rateLimit, auditLog).POST("/payments", processPayment)

// listUsers uses only api's middleware
api.GET("/users", listUsers)
```

---

## Mounting Sub-Routers

`Mount` attaches a separate `http.Handler` at a path prefix. The prefix is stripped from the request URL before it is forwarded to the mounted handler. This allows independently built routers to be composed:

```go
// Version 2 router — built and tested independently
v2 := muxmaster.New()
v2.GET("/users", listUsersV2)
v2.POST("/users", createUserV2)

// Attach to the main router
mux.Mount("/v2", v2)
// GET /v2/users → v2 sees GET /users
```

The mounted handler receives `r.URL.Path` with the prefix stripped, so a sub-router mounted at `/v2` sees `/users`, not `/v2/users`.

### Organizing a large application

```go
func main() {
    mux := muxmaster.New()
    mux.Use(middleware.Logger(os.Stdout))
    mux.Use(middleware.Recoverer)

    mux.Mount("/api/v1", newV1Router())
    mux.Mount("/api/v2", newV2Router())
    mux.Mount("/admin",  newAdminRouter())

    log.Fatal(http.ListenAndServe(":8080", mux))
}

func newV1Router() http.Handler {
    mux := muxmaster.New()
    mux.Use(requireAPIKey)
    mux.GET("/users", listUsers)
    mux.POST("/users", createUser)
    return mux
}
```

---

## Mounting on a Group

`Mount` is also available on `*Group`, which combines the group's prefix with the mount prefix:

```go
api := mux.Group("/api")
v1 := muxmaster.New()
v1.GET("/users", listUsers)

api.Mount("/v1", v1)
// GET /api/v1/users → handled by listUsers
```

---

## Serving Static Files from a Group

`ServeFiles` on a group prepends the group prefix:

```go
assets := mux.Group("/static")
assets.ServeFiles("/*filepath", http.Dir("./public"))
// GET /static/css/main.css → ./public/css/main.css
```

---

## Design Patterns

### API versioning

```go
v1 := mux.Group("/api/v1")
v2 := mux.Group("/api/v2")

v1.GET("/users", listUsersV1)
v2.GET("/users", listUsersV2)
```

### Feature-based organization

Group by feature domain rather than by HTTP method:

```go
mux.Route("/api/v1", func(api *muxmaster.Group) {
    api.Use(requireAPIKey)

    // Users domain
    api.Route("/users", func(g *muxmaster.Group) {
        g.GET("", listUsers)
        g.POST("", createUser)
        g.GET("/:id", getUser)
        g.PUT("/:id", updateUser)
        g.DELETE("/:id", deleteUser)
    })

    // Orders domain
    api.Route("/orders", func(g *muxmaster.Group) {
        g.GET("", listOrders)
        g.POST("", createOrder)
        g.GET("/:id", getOrder)
    })
})
```

### Micro-service composition

Mount independent services behind a reverse proxy router:

```go
mux := muxmaster.New()
mux.Use(middleware.RealIP)
mux.Use(middleware.RequestID)

mux.Mount("/auth",    authService)
mux.Mount("/catalog", catalogService)
mux.Mount("/orders",  orderService)
mux.Mount("/payment", paymentService)
```

---

## See Also

- [Middleware](middleware.md) — middleware scopes and composition
- [Routing](routing.md) — pattern syntax and conflict resolution
- [Cookbook](cookbook.md) — application structure recipes
