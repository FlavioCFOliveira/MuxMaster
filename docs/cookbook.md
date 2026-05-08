# Cookbook

This page collects ready-to-use patterns for common production scenarios.

## Table of Contents

- [REST API with versioning](#rest-api-with-versioning)
- [JSON API with centralized error handling](#json-api-with-centralized-error-handling)
- [Authentication middleware](#authentication-middleware)
- [JWT authentication](#jwt-authentication)
- [Request validation](#request-validation)
- [Pagination](#pagination)
- [Graceful shutdown](#graceful-shutdown)
- [Health and readiness endpoints](#health-and-readiness-endpoints)
- [Rate limiting per IP](#rate-limiting-per-ip)
- [CORS for a React SPA](#cors-for-a-react-spa)
- [Serving embedded frontend assets](#serving-embedded-frontend-assets)
- [Request logging with structured output](#request-logging-with-structured-output)
- [Testing handlers with MuxMaster](#testing-handlers-with-muxmaster)
- [Feature-based file structure](#feature-based-file-structure)

---

## REST API with versioning

```go
func main() {
    mux := muxmaster.New()
    mux.Use(middleware.Logger(os.Stdout))
    mux.Use(middleware.Recoverer)
    mux.Use(middleware.RequestID)

    mux.Mount("/api/v1", v1Router())
    mux.Mount("/api/v2", v2Router())

    mux.GET("/health", func(w http.ResponseWriter, r *http.Request) {
        muxmaster.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
    })

    log.Fatal(http.ListenAndServe(":8080", mux))
}

func v1Router() http.Handler {
    mux := muxmaster.New()
    mux.Use(requireAPIKey)

    mux.GET("/users", listUsersV1)
    mux.POST("/users", createUserV1)
    mux.GET("/users/:id", getUserV1)

    return mux
}

func v2Router() http.Handler {
    mux := muxmaster.New()
    mux.Use(requireAPIKey)

    mux.GET("/users", listUsersV2) // new response shape
    mux.POST("/users", createUserV2)
    mux.GET("/users/:id", getUserV2)
    mux.GET("/users/:id/posts", getUserPostsV2) // new in v2

    return mux
}
```

---

## JSON API with centralized error handling

```go
type APIError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func setupRouter() *muxmaster.Mux {
    mux := muxmaster.New()

    mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
        code := http.StatusInternalServerError
        msg  := "internal server error"

        var he muxmaster.HTTPError
        if errors.As(err, &he) {
            code = he.StatusCode()
            msg  = err.Error()
        } else {
            slog.Error("unhandled error",
                "method", r.Method,
                "path",   r.URL.Path,
                "error",  err,
            )
        }

        muxmaster.JSON(w, code, APIError{Code: code, Message: msg})
    }

    mux.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        muxmaster.JSON(w, http.StatusNotFound, APIError{
            Code: http.StatusNotFound, Message: "not found",
        })
    })

    mux.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        muxmaster.JSON(w, http.StatusMethodNotAllowed, APIError{
            Code: http.StatusMethodNotAllowed, Message: "method not allowed",
        })
    })

    return mux
}
```

---

## Authentication middleware

```go
type ctxKey string

const claimsKey ctxKey = "claims"

type Claims struct {
    UserID int
    Role   string
}

func requireAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        claims, err := verifyToken(token)
        if err != nil {
            muxmaster.JSON(w, http.StatusUnauthorized, map[string]string{
                "error": "invalid or missing token",
            })
            return
        }
        ctx := context.WithValue(r.Context(), claimsKey, claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func requireRole(role string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            claims := r.Context().Value(claimsKey).(*Claims)
            if claims.Role != role {
                muxmaster.JSON(w, http.StatusForbidden, map[string]string{
                    "error": "insufficient permissions",
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

// Usage
api := mux.Group("/api/v1")
api.Use(requireAuth)

api.GET("/profile", getProfile)                              // any authenticated user
api.With(requireRole("admin")).DELETE("/users/:id", deleteUser) // admin only
```

---

## JWT authentication

```go
import "github.com/golang-jwt/jwt/v5"

var jwtSecret = []byte(os.Getenv("JWT_SECRET"))

type JWTClaims struct {
    UserID int    `json:"uid"`
    Role   string `json:"role"`
    jwt.RegisteredClaims
}

func jwtMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        token, err := jwt.ParseWithClaims(raw, &JWTClaims{},
            func(t *jwt.Token) (any, error) {
                if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
                    return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
                }
                return jwtSecret, nil
            },
        )
        if err != nil || !token.Valid {
            muxmaster.JSON(w, http.StatusUnauthorized, map[string]string{
                "error": "invalid token",
            })
            return
        }
        claims := token.Claims.(*JWTClaims)
        ctx := context.WithValue(r.Context(), claimsKey, claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

---

## Request validation

A simple approach using a `validate` helper that returns an `HTTPError`:

```go
type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func (req *CreateUserRequest) Validate() error {
    if req.Name == "" {
        return muxmaster.Error(http.StatusUnprocessableEntity,
            errors.New("name is required"))
    }
    if !strings.Contains(req.Email, "@") {
        return muxmaster.Error(http.StatusUnprocessableEntity,
            errors.New("email is invalid"))
    }
    return nil
}

mux.POSTE("/users", func(w http.ResponseWriter, r *http.Request) error {
    var req CreateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        return muxmaster.Error(http.StatusBadRequest, err)
    }
    if err := req.Validate(); err != nil {
        return err
    }
    user, err := db.CreateUser(req.Name, req.Email)
    if err != nil {
        return err
    }
    return muxmaster.JSON(w, http.StatusCreated, user)
})
```

---

## Pagination

```go
type PageRequest struct {
    Page  int
    Limit int
}

func parsePage(r *http.Request) PageRequest {
    page, _ := strconv.Atoi(r.URL.Query().Get("page"))
    limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
    if page < 1 {
        page = 1
    }
    if limit < 1 || limit > 100 {
        limit = 20
    }
    return PageRequest{Page: page, Limit: limit}
}

mux.GET("/users", func(w http.ResponseWriter, r *http.Request) {
    pg := parsePage(r)
    users, total, err := db.ListUsers(pg.Page, pg.Limit)
    if err != nil {
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    muxmaster.JSON(w, http.StatusOK, map[string]any{
        "data":  users,
        "total": total,
        "page":  pg.Page,
        "limit": pg.Limit,
    })
})
```

---

## Graceful shutdown

```go
func main() {
    mux := muxmaster.New()
    // ... register routes ...

    server := &http.Server{
        Addr:         ":8080",
        Handler:      mux,
        ReadTimeout:  5 * time.Second,
        WriteTimeout: 10 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    // Start server in a goroutine
    go func() {
        log.Println("listening on :8080")
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen: %v", err)
        }
    }()

    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit
    log.Println("shutting down...")

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if err := server.Shutdown(ctx); err != nil {
        log.Fatalf("shutdown: %v", err)
    }
    log.Println("server stopped")
}
```

---

## Health and readiness endpoints

```go
mux.GET("/health", func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
})

mux.GET("/ready", func(w http.ResponseWriter, r *http.Request) {
    if err := db.Ping(); err != nil {
        muxmaster.JSON(w, http.StatusServiceUnavailable, map[string]string{
            "status": "unavailable",
            "reason": "database unreachable",
        })
        return
    }
    muxmaster.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
})
```

---

## Rate limiting per IP

```go
import "golang.org/x/time/rate"

func ipRateLimiter(rps float64, burst int) func(http.Handler) http.Handler {
    mu       := sync.Mutex{}
    limiters := map[string]*rate.Limiter{}

    getLimiter := func(ip string) *rate.Limiter {
        mu.Lock()
        defer mu.Unlock()
        if l, ok := limiters[ip]; ok {
            return l
        }
        l := rate.NewLimiter(rate.Limit(rps), burst)
        limiters[ip] = l
        return l
    }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := r.RemoteAddr // use middleware.RealIP if behind a proxy
            if !getLimiter(ip).Allow() {
                muxmaster.JSON(w, http.StatusTooManyRequests, map[string]string{
                    "error": "too many requests",
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

mux.Use(ipRateLimiter(10, 30)) // 10 req/s, burst of 30
```

---

## CORS for a React SPA

```go
mux.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins: []string{
        "http://localhost:3000",         // local development
        "https://myapp.example.com",     // production
    },
    AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
    ExposedHeaders:   []string{"X-Request-Id"},
    AllowCredentials: true,
    MaxAge:           86400,
}))
```

Put CORS middleware before authentication middleware so preflight OPTIONS requests (which do not carry credentials) are handled without authentication.

---

## Serving embedded frontend assets

```go
import "embed"

//go:embed frontend/dist
var frontendFS embed.FS

func main() {
    mux := muxmaster.New()

    // API routes
    api := mux.Group("/api")
    api.GET("/users", listUsers)

    // Frontend — serve index.html for all unmatched routes (SPA fallback)
    mux.GET("/*filepath", func(w http.ResponseWriter, r *http.Request) {
        path := muxmaster.PathParam(r, "filepath")
        f, err := frontendFS.Open("frontend/dist" + path)
        if err != nil {
            // File not found — serve index.html for client-side routing
            http.ServeFileFS(w, r, frontendFS, "frontend/dist/index.html")
            return
        }
        f.Close()
        http.ServeFileFS(w, r, frontendFS, "frontend/dist"+path)
    })

    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

---

## Request logging with structured output

```go
func structuredLogger(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        rec   := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(rec, r)
        slog.Info("request",
            "method",    r.Method,
            "path",      r.URL.Path,
            "status",    rec.status,
            "duration",  time.Since(start),
            "ip",        r.RemoteAddr,
            "requestID", r.Header.Get("X-Request-Id"),
        )
    })
}

type statusRecorder struct {
    http.ResponseWriter
    status int
}
func (r *statusRecorder) WriteHeader(code int) {
    r.status = code
    r.ResponseWriter.WriteHeader(code)
}

mux.Use(structuredLogger)
```

---

## Testing handlers with MuxMaster

Test handlers by creating a router, registering the routes under test, and using `httptest`:

```go
import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestGetUser(t *testing.T) {
    mux := muxmaster.New()
    mux.GET("/users/:id", getUser)

    req  := httptest.NewRequest("GET", "/users/42", nil)
    rec  := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
}
```

Testing with a custom error handler:

```go
func TestCreateUserValidation(t *testing.T) {
    mux := muxmaster.New()
    mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
        var he muxmaster.HTTPError
        if errors.As(err, &he) {
            w.WriteHeader(he.StatusCode())
            return
        }
        w.WriteHeader(http.StatusInternalServerError)
    }
    mux.POSTE("/users", createUser)

    body := strings.NewReader(`{}`) // missing required fields
    req  := httptest.NewRequest("POST", "/users", body)
    req.Header.Set("Content-Type", "application/json")
    rec  := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)

    if rec.Code != http.StatusUnprocessableEntity {
        t.Fatalf("expected 422, got %d", rec.Code)
    }
}
```

---

## Feature-based file structure

For larger applications, organize code by domain rather than by layer:

```
myapi/
├── main.go
├── internal/
│   ├── user/
│   │   ├── handler.go    // HTTP handlers
│   │   ├── service.go    // business logic
│   │   ├── repository.go // database access
│   │   └── routes.go     // route registration
│   ├── order/
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── routes.go
│   └── middleware/
│       └── auth.go
└── go.mod
```

Each `routes.go` registers routes on a group passed from `main.go`:

```go
// internal/user/routes.go
func RegisterRoutes(g *muxmaster.Group, svc *Service) {
    h := &Handler{svc: svc}
    g.GET("/users",      h.List)
    g.POST("/users",     h.Create)
    g.GET("/users/:id",  h.Get)
    g.PUT("/users/:id",  h.Update)
    g.DELETE("/users/:id", h.Delete)
}

// main.go
api := mux.Group("/api/v1")
api.Use(middleware.RequireAuth)

user.RegisterRoutes(api, userService)
order.RegisterRoutes(api, orderService)
```

---

## See Also

- [Getting Started](getting-started.md) — step-by-step introduction
- [Middleware](middleware.md) — all built-in middleware
- [Error Handling](error-handling.md) — centralized error patterns
- [Groups](groups.md) — organizing routes
