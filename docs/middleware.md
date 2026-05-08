# Middleware

Middleware in MuxMaster is any function with the signature:

```go
func(http.Handler) http.Handler
```

This is the same signature used by `net/http`, chi, gorilla/mux, and most other Go web libraries, so any existing middleware is compatible with MuxMaster without modification.

## Table of Contents

- [How Middleware Works](#how-middleware-works)
- [Global Middleware](#global-middleware)
- [Pre-Routing Middleware](#pre-routing-middleware)
- [Group Middleware](#group-middleware)
- [Per-Route Middleware with With](#per-route-middleware-with-with)
- [Writing Custom Middleware](#writing-custom-middleware)
- [Built-in Middleware Reference](#built-in-middleware-reference)
  - [Logger](#logger)
  - [Recoverer](#recoverer)
  - [CORS](#cors)
  - [BasicAuth](#basicauth)
  - [Compress](#compress)
  - [ThrottleBacklog](#throttlebacklog)
  - [Timeout](#timeout)
  - [RequestID](#requestid)
  - [RealIP](#realip)
  - [CleanPath](#cleanpath)
  - [StripSlashes](#stripslashes)
  - [NoCache](#nocache)
  - [SetHeader](#setheader)
  - [WithValue](#withvalue)

---

## How Middleware Works

MuxMaster applies middleware at **route registration time**. When you call `mux.Use(mw)` and then `mux.GET("/path", handler)`, the handler stored in the router is `mw(handler)` — not the original handler plus a middleware list.

This means:
- Zero per-request overhead from iterating a middleware chain
- Middleware applied to a route stays with that route, regardless of later `Use` calls
- `Use` must be called **before** the routes it should affect

Execution order mirrors nesting order: the first middleware listed in `Use` is the outermost wrapper (runs first on request, last on response).

```go
mux.Use(A)
mux.Use(B)
mux.GET("/path", handler)
// Execution: A → B → handler → B → A
```

---

## Global Middleware

`Use` appends middleware to the mux's global chain. It applies to all routes registered **after** the call:

```go
mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout))
mux.Use(middleware.Recoverer)

mux.GET("/api/users", listUsers) // wrapped by Logger and Recoverer
```

---

## Pre-Routing Middleware

`Pre` registers middleware that runs **before** the router matches the request. Use it to rewrite or normalize the URL before the radix tree sees it.

```go
mux.Pre(middleware.CleanPath)
mux.Pre(middleware.StripSlashes)
```

Pre-routing middleware cannot access path parameters because routing has not happened yet. It is useful for path normalization, request ID injection, and real IP extraction.

---

## Group Middleware

Middleware registered on a group applies only to the routes in that group, after any mux-level middleware:

```go
mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout)) // runs for all routes

api := mux.Group("/api/v1")
api.Use(requireAPIKey)  // runs only for routes in /api/v1

api.GET("/users", listUsers) // Logger → requireAPIKey → listUsers
mux.GET("/health", health)     // Logger → health (no requireAPIKey)
```

---

## Per-Route Middleware with `With`

`With` returns a copy of the mux or group with additional middleware scoped to the next route registration:

```go
// On the mux
mux.With(requireAdmin).DELETE("/users/:id", deleteUser)

// On a group
api.With(rateLimit, auditLog).POST("/payments", processPayment)
```

`With` does not mutate the original mux or group; it returns a new, temporary scope.

---

## Writing Custom Middleware

A middleware is a function that receives the next handler and returns a new handler:

```go
func requireAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("Authorization")
        if !isValidToken(token) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}

mux.Use(requireAuth)
```

### Passing configuration to middleware

Wrap the middleware function in a constructor that accepts options:

```go
func RateLimit(requestsPerSecond int) func(http.Handler) http.Handler {
    limiter := rate.NewLimiter(rate.Limit(requestsPerSecond), requestsPerSecond)
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if !limiter.Allow() {
                http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

mux.Use(RateLimit(100))
```

### Sharing data between middleware and handlers via context

Use `context.WithValue` with an unexported key type to avoid collisions:

```go
type ctxKey string

const userIDKey ctxKey = "userID"

func injectUserID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        userID := extractUserIDFromToken(r.Header.Get("Authorization"))
        ctx := context.WithValue(r.Context(), userIDKey, userID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// In a handler:
userID := r.Context().Value(userIDKey).(string)
```

Alternatively, use `middleware.WithValue` for simple cases:

```go
mux.Use(middleware.WithValue("requestEnv", "production"))

// In a handler:
env := r.Context().Value("requestEnv").(string)
```

---

## Built-in Middleware Reference

Import the `middleware` sub-package:

```go
import "github.com/FlavioCFOliveira/MuxMaster/middleware"
```

---

### Logger

Logs each request after it completes. Output format: `timestamp method path status duration`.

```go
mux.Use(middleware.Logger(os.Stdout))
```

Sample output:

```
2026-04-17T10:05:31Z GET /api/v1/users 200 1.243ms
2026-04-17T10:05:32Z POST /api/v1/users 201 4.871ms
```

**Parameters:**
- `out io.Writer` — destination for log lines; panics if nil

---

### Recoverer

Catches panics in downstream handlers, writes a 500 response, and resumes normal request processing. Without this middleware a panic crashes the entire server.

```go
mux.Use(middleware.Recoverer)
```

---

### CORS

Handles Cross-Origin Resource Sharing. Responds to preflight OPTIONS requests and sets the appropriate CORS headers on responses.

```go
mux.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins:   []string{"https://app.example.com"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Authorization", "Content-Type"},
    AllowCredentials: true,
    MaxAge:           86400, // seconds to cache preflight response
}))
```

To allow all origins (not recommended for authenticated APIs):

```go
mux.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins: []string{"*"},
    AllowedMethods: []string{"GET", "POST"},
}))
```

**`CORSOptions` fields:**

| Field              | Type       | Description                                                 |
|--------------------|------------|-------------------------------------------------------------|
| `AllowedOrigins`   | `[]string` | Origins that may access the resource                        |
| `AllowedMethods`   | `[]string` | HTTP methods allowed in the actual request                  |
| `AllowedHeaders`   | `[]string` | Request headers that may be used                            |
| `ExposedHeaders`   | `[]string` | Response headers accessible to the browser                  |
| `AllowCredentials` | `bool`     | Whether the response can include cookies (cannot use `"*"`) |
| `MaxAge`           | `int`      | Seconds to cache the preflight response                     |

---

### BasicAuth

Requires HTTP Basic Authentication credentials for the wrapped route or group.

```go
credentials := map[string]string{
    "admin":  "secret",
    "reader": "readonly",
}
mux.Use(middleware.BasicAuth("My API", credentials))
```

**Parameters:**
- `realm string` — shown to the user in the browser's credential prompt
- `credentials map[string]string` — map of username → password

---

### Compress

Compresses responses using gzip or deflate, depending on the `Accept-Encoding` header.

```go
mux.Use(middleware.Compress(5)) // compression level 1–9; 5 is a good default
```

Responses smaller than a threshold are not compressed. The `Content-Encoding: gzip` header is set automatically.

---

### ThrottleBacklog

Limits the number of concurrently executing handlers. Requests that exceed the limit are queued; requests that exceed the queue are rejected with 503.

```go
mux.Use(middleware.ThrottleBacklog(
    100,                // max concurrent handlers
    50,                 // max queued requests
    30*time.Second,     // max time a request may wait in the queue
))
```

**Parameters:**
- `limit int` — maximum number of handlers running simultaneously; must be > 0
- `backlog int` — maximum number of requests waiting for a slot; must be ≥ 0
- `timeout time.Duration` — how long a queued request waits before receiving a 503

---

### Timeout

Cancels the request context after the specified duration. The handler is expected to honour `ctx.Done()` to exit early.

```go
mux.Use(middleware.Timeout(10 * time.Second))
```

The timeout applies to the handler execution time, not to the total connection lifetime.

---

### RequestID

Attaches a unique request ID to every request. Reads `X-Request-Id` from the incoming headers; generates a random UUID if absent. Writes the ID back in the response as `X-Request-Id`.

```go
mux.Use(middleware.RequestID)
```

To read the request ID in a handler:

```go
id := r.Header.Get("X-Request-Id")
```

---

### RealIP

Extracts the real client IP address from `X-Forwarded-For` or `X-Real-IP` headers set by a reverse proxy, and sets `r.RemoteAddr` to that value.

```go
mux.Use(middleware.RealIP)
```

Only use this middleware if the server is behind a trusted reverse proxy. Accepting these headers from arbitrary clients is a security risk.

---

### CleanPath

Redirects URLs with redundant components to their canonical form:
- `//users` → `/users`
- `/a/../users` → `/users`
- `/a/./users` → `/a/users`

```go
mux.Pre(middleware.CleanPath)  // run before routing to avoid a redirect
```

---

### StripSlashes

Removes trailing slashes from the URL path before routing. Unlike `RedirectTrailingSlash`, this modifies the request in-place without issuing a redirect.

```go
mux.Pre(middleware.StripSlashes)
```

---

### NoCache

Sets headers that instruct browsers and intermediaries not to cache the response.

```go
mux.Use(middleware.NoCache)
```

Headers set: `Cache-Control: no-cache, no-store, no-transform, must-revalidate, private, max-age=0`, `Pragma: no-cache`, `Expires: 0`.

---

### SetHeader

Sets a fixed response header for every request:

```go
mux.Use(middleware.SetHeader("X-Content-Type-Options", "nosniff"))
mux.Use(middleware.SetHeader("X-Frame-Options", "DENY"))
mux.Use(middleware.SetHeader("Strict-Transport-Security", "max-age=31536000"))
```

---

### WithValue

Stores a value in the request context. Useful for injecting configuration or feature flags:

```go
mux.Use(middleware.WithValue("appEnv", "production"))

// In a handler:
env := r.Context().Value("appEnv").(string)
```

---

## See Also

- [Groups](groups.md) — applying middleware to a subset of routes
- [Error Handling](error-handling.md) — error-returning handlers
- [Cookbook](cookbook.md) — middleware composition patterns
