# Compatibility

## Scope

This file specifies Go version requirements, net/http ecosystem compatibility, and explicit statements of what is not compatible with MuxMaster and why.

---

## 1. Go Version

1. MuxMaster requires Go 1.26 or later. This is the minimum version declared in `go.mod`.
2. The following language features used in the implementation require Go 1.22 or later:
    - `for i := range n` syntax (range over integer).
3. The following language features used in the implementation require Go 1.21 or later:
    - `min` and `max` built-in functions.
4. MuxMaster does not support Go 1.25 or earlier. Code that attempts to build with an older toolchain will produce a compilation error.
5. MuxMaster will track Go's compatibility guarantee. Code that compiles under Go 1.26 must continue to compile under future Go releases without modification, unless a Go release introduces a breaking change to the standard library (which is rare and covered by the Go compatibility promise).

---

## 2. net/http Ecosystem Compatibility

### 2.1 http.Handler Interface

6. `*Mux` implements `http.Handler`. It can be passed to `http.ListenAndServe`, `http.ListenAndServeTLS`, and any function that accepts `http.Handler`.
7. `*Mux` can be used as a sub-handler within any other `http.Handler` implementation.

### 2.2 Middleware Compatibility

8. MuxMaster middleware uses the type `func(http.Handler) http.Handler`. This is the de facto standard Go middleware type.
9. Middleware written for alice, negroni, or any other library that uses `func(http.Handler) http.Handler` works with MuxMaster without modification.

### 2.3 http.FileServer

10. `http.FileServer` works with MuxMaster via `ServeFiles` (see [static-files.md](static-files.md)) or via `Mount` (see [groups.md](groups.md)).

### 2.4 httptest Package

11. `*Mux` works with `httptest.NewRecorder` and `httptest.NewServer` without modification.

### 2.5 context Package

12. MuxMaster stores path parameters in the request context using an unexported key. The context is accessible via `r.Context()` as with any standard Go HTTP handler.
13. Middleware that wraps the context (e.g., using `r.WithContext`) works correctly. MuxMaster parameters remain accessible as long as the parent context is not replaced entirely (i.e., as long as the new context chains to the original).

---

## 3. Incompatible Handler Types

The following handler types are not compatible with MuxMaster and require adaptation.

| Handler type | Package | Reason |
|---|---|---|
| `httprouter.Handle` | `github.com/julienschmidt/httprouter` | Third argument `httprouter.Params` not present in `http.HandlerFunc` |
| `gin.HandlerFunc` | `github.com/gin-gonic/gin` | Uses `*gin.Context` instead of `(http.ResponseWriter, *http.Request)` |
| `echo.HandlerFunc` | `github.com/labstack/echo/v5` | Uses `echo.Context` |
| `fiber.Handler` | `github.com/gofiber/fiber/v3` | Uses `*fiber.Ctx` and is built on `fasthttp`, not `net/http` |

MuxMaster does not provide adapters for these types. Converting an existing codebase from one of these frameworks requires rewriting handlers.

---

## 4. fasthttp Incompatibility

14. MuxMaster does not support `fasthttp`. It is built exclusively on `net/http`. The two HTTP server libraries use incompatible interfaces. This is by design (see [out-of-scope.md](out-of-scope.md)).

---

## 5. Dynamic Route Registration

15. MuxMaster does not support registering routes after `ServeHTTP` has begun serving requests. The radix tree nodes are written once at registration time and then read concurrently at request time without write locks on the nodes themselves.
16. Calling `Handle` (or any registration method) concurrently with `ServeHTTP` in a way that creates a new method tree is safe (a `sync.RWMutex` protects the top-level method-to-tree map). However, calling `Handle` after the server has started serving is considered a misuse. Behavior under concurrent registration and serving on the same method tree is undefined.
