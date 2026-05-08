# Getting Started with MuxMaster

This guide walks you through building a small but realistic REST API with MuxMaster from scratch. By the end you will have a working server with route groups, middleware, path parameters, JSON responses, and centralized error handling.

## Prerequisites

- Go 1.26 or later ([download](https://go.dev/dl/))
- Familiarity with `net/http` and Go modules

## Install

Create a new module and add MuxMaster as a dependency:

```
mkdir myapi && cd myapi
go mod init myapi
go get github.com/FlavioCFOliveira/MuxMaster
```

## Step 1 — Hello, World

Create `main.go`:

```go
package main

import (
    "fmt"
    "log"
    "net/http"

    "github.com/FlavioCFOliveira/MuxMaster"
)

func main() {
    mux := muxmaster.New()

    mux.GET("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "Hello, World!")
    })

    log.Println("listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Run it and test it:

```
go run .
curl http://localhost:8080/
# Hello, World!
```

## Step 2 — Path Parameters

Path parameters are named segments in the URL pattern prefixed with `:`. Use `muxmaster.PathParam` to read a single value, or `muxmaster.ParamsFromContext` to read all of them.

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id := muxmaster.PathParam(r, "id")
    fmt.Fprintf(w, "user: %s\n", id)
})
```

```
curl http://localhost:8080/users/42
# user: 42
```

To parse the value as an integer:

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    ps := muxmaster.ParamsFromContext(r.Context())
    id, err := ps.Int("id")
    if err != nil {
        http.Error(w, "invalid id", http.StatusBadRequest)
        return
    }
    fmt.Fprintf(w, "user id: %d\n", id)
})
```

## Step 3 — Add Middleware

Middleware wraps all routes registered **after** the `Use` call. The most common setup adds logging and panic recovery at the top of `main`:

```go
import (
    "os"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout))
mux.Use(middleware.Recoverer)
```

After restarting, every request prints a log line:

```
2026-04-17T10:05:31Z GET /users/42 200 87.5µs
```

## Step 4 — Route Groups

Use groups to share a path prefix and middleware across a set of related routes:

```go
api := mux.Group("/api/v1")
api.Use(requireAPIKey) // only applies to routes in this group

api.GET("/users", listUsers)
api.POST("/users", createUser)
api.GET("/users/:id", getUser)
api.PUT("/users/:id", updateUser)
api.DELETE("/users/:id", deleteUser)
```

You can nest groups to add another layer of middleware:

```go
admin := api.Group("/admin")
admin.Use(requireAdmin)
admin.GET("/stats", getStats)
```

## Step 5 — JSON Responses

`muxmaster.JSON` marshals any value to JSON, sets `Content-Type: application/json`, and writes the status code in one call:

```go
type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id, _ := muxmaster.ParamsFromContext(r.Context()).Int("id")
    user := User{ID: id, Name: "Alice"}
    muxmaster.JSON(w, http.StatusOK, user)
})
```

## Step 6 — Error-Returning Handlers

Repeat `if err != nil { http.Error(...); return }` quickly becomes noisy. `HandlerFuncE` lets handlers return an error instead:

```go
mux.GETE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    id, err := muxmaster.ParamsFromContext(r.Context()).Int("id")
    if err != nil {
        return muxmaster.Error(http.StatusBadRequest, err)
    }
    user, err := db.FindUser(id)
    if err != nil {
        return muxmaster.Error(http.StatusNotFound, errors.New("user not found"))
    }
    return muxmaster.JSON(w, http.StatusOK, user)
})
```

Set a custom error handler to produce consistent JSON error responses:

```go
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    code := http.StatusInternalServerError
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        code = he.StatusCode()
    }
    muxmaster.JSON(w, code, map[string]string{"error": err.Error()})
}
```

## Step 7 — Complete Example

Here is the full application combining everything from the steps above:

```go
package main

import (
    "errors"
    "log"
    "net/http"
    "os"

    "github.com/FlavioCFOliveira/MuxMaster"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

var users = map[int]User{
    1: {ID: 1, Name: "Alice"},
    2: {ID: 2, Name: "Bob"},
}

func main() {
    mux := muxmaster.New()
    mux.Use(middleware.Logger(os.Stdout))
    mux.Use(middleware.Recoverer)

    mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
        code := http.StatusInternalServerError
        var he muxmaster.HTTPError
        if errors.As(err, &he) {
            code = he.StatusCode()
        }
        muxmaster.JSON(w, code, map[string]string{"error": err.Error()})
    }

    api := mux.Group("/api/v1")

    api.GET("/users", func(w http.ResponseWriter, r *http.Request) {
        list := make([]User, 0, len(users))
        for _, u := range users {
            list = append(list, u)
        }
        muxmaster.JSON(w, http.StatusOK, list)
    })

    api.GETE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
        id, err := muxmaster.ParamsFromContext(r.Context()).Int("id")
        if err != nil {
            return muxmaster.Error(http.StatusBadRequest, err)
        }
        u, ok := users[id]
        if !ok {
            return muxmaster.Error(http.StatusNotFound, errors.New("user not found"))
        }
        return muxmaster.JSON(w, http.StatusOK, u)
    })

    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

```
curl http://localhost:8080/api/v1/users
curl http://localhost:8080/api/v1/users/1
curl http://localhost:8080/api/v1/users/99
# {"error":"user not found"}
```

## Next Steps

- [Routing](routing.md) — complete pattern syntax and method reference
- [Middleware](middleware.md) — built-in middleware and writing your own
- [Groups](groups.md) — advanced grouping and sub-router mounting
- [Error Handling](error-handling.md) — centralized error patterns
- [Cookbook](cookbook.md) — common production patterns
