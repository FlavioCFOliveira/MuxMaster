# Error Handling

MuxMaster provides a structured approach to error handling that eliminates boilerplate in individual handlers while giving you full control over how errors are serialized and logged.

## Table of Contents

- [The Problem with Standard Handlers](#the-problem-with-standard-handlers)
- [HandlerFuncE](#handlefunce)
- [HTTPError](#httperror)
- [The Default Error Handler](#the-default-error-handler)
- [Custom Error Handler](#custom-error-handler)
- [Error-Returning Method Variants](#error-returning-method-variants)
- [Custom 404 and 405 Handlers](#custom-404-and-405-handlers)
- [Panic Recovery](#panic-recovery)
- [Patterns and Best Practices](#patterns-and-best-practices)

---

## The Problem with Standard Handlers

A `http.HandlerFunc` has no return value, so error handling is manual:

```go
mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    id, err := strconv.Atoi(muxmaster.PathParam(r, "id"))
    if err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    user, err := db.FindUser(id)
    if err != nil {
        http.Error(w, "not found", http.StatusNotFound)
        return
    }
    if err := json.NewEncoder(w).Encode(user); err != nil {
        log.Printf("encode error: %v", err)
    }
})
```

Every handler repeats the same pattern: check error, write response, return. The error format (plain text in this case) must be kept consistent manually across all handlers.

---

## HandlerFuncE

`HandlerFuncE` extends the standard handler signature with an error return:

```go
type HandlerFuncE func(http.ResponseWriter, *http.Request) error
```

Handlers return `nil` on success, or an error to be handled centrally:

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

The same pattern applies to every HTTP method: `GETE`, `POSTE`, `PUTE`, `PATCHE`, `DELETEE`, `HEADE`, `OPTIONSE`.

---

## HTTPError

`muxmaster.Error(code, err)` wraps an error with an HTTP status code:

```go
// Create an HTTPError
err := muxmaster.Error(http.StatusNotFound, errors.New("user not found"))

// Check status code
var he muxmaster.HTTPError
if errors.As(err, &he) {
    fmt.Println(he.StatusCode()) // 404
}
```

`HTTPError` is an interface:

```go
type HTTPError interface {
    error
    StatusCode() int
}
```

Any error that implements this interface is recognized by MuxMaster's error-handling pipeline, including custom implementations:

```go
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string   { return e.Field + ": " + e.Message }
func (e *ValidationError) StatusCode() int { return http.StatusUnprocessableEntity }
```

---

## The Default Error Handler

When no `ErrorHandler` is set, MuxMaster's default behaviour is:

- If the error implements `HTTPError`, respond with that status code and the error message as plain text.
- Otherwise, respond with 500 Internal Server Error.

---

## Custom Error Handler

Set `mux.ErrorHandler` to take over all error responses globally:

```go
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    code := http.StatusInternalServerError
    msg  := "internal server error"

    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        code = he.StatusCode()
        msg  = err.Error()
    } else {
        // Log unexpected errors; do not leak internal details to the client
        log.Printf("unhandled error [%s %s]: %v", r.Method, r.URL.Path, err)
    }

    muxmaster.JSON(w, code, map[string]string{"error": msg})
}
```

The `ErrorHandler` is called for every `HandlerFuncE` that returns a non-nil error, across the entire mux and all its groups.

### Structured error responses

For APIs that need machine-readable errors:

```go
type APIError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Detail  string `json:"detail,omitempty"`
}

mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    apiErr := APIError{
        Code:    http.StatusInternalServerError,
        Message: "internal server error",
    }

    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        apiErr.Code    = he.StatusCode()
        apiErr.Message = err.Error()
    }

    var ve *ValidationError
    if errors.As(err, &ve) {
        apiErr.Detail = "field: " + ve.Field
    }

    muxmaster.JSON(w, apiErr.Code, apiErr)
}
```

---

## Error-Returning Method Variants

Every standard HTTP method has an error-returning variant:

| Standard     | Error-returning |
|--------------|-----------------|
| `mux.GET`    | `mux.GETE`      |
| `mux.POST`   | `mux.POSTE`     |
| `mux.PUT`    | `mux.PUTE`      |
| `mux.PATCH`  | `mux.PATCHE`    |
| `mux.DELETE` | `mux.DELETEE`   |
| `mux.HEAD`   | `mux.HEADE`     |
| `mux.OPTIONS`| `mux.OPTIONSE`  |

The same variants exist on `*Group`:

```go
api := mux.Group("/api/v1")
api.POSTE("/users", createUser)
api.GETE("/users/:id", getUser)
api.DELETEE("/users/:id", deleteUser)
```

---

## Custom 404 and 405 Handlers

### Not Found (404)

Called when no route matches the request path:

```go
mux.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    muxmaster.JSON(w, http.StatusNotFound, map[string]string{
        "error": "the requested resource does not exist",
        "path":  r.URL.Path,
    })
})
```

### Method Not Allowed (405)

Called when the path is registered for at least one method, but not the requested method. MuxMaster sets the `Allow` header automatically:

```go
mux.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    allowed := w.Header().Get("Allow")
    muxmaster.JSON(w, http.StatusMethodNotAllowed, map[string]string{
        "error":   "method not allowed",
        "allowed": allowed,
    })
})
```

To enable 405 responses, `HandleMethodNotAllowed` must be `true` (the default).

---

## Panic Recovery

MuxMaster does not automatically recover from panics. Use `middleware.Recoverer` to catch panics before they crash the server:

```go
mux.Use(middleware.Recoverer)
```

For custom panic handling, set `mux.PanicHandler`:

```go
mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
    log.Printf("panic recovered [%s %s]: %v\n%s",
        r.Method, r.URL.Path, rcv, debug.Stack())
    muxmaster.JSON(w, http.StatusInternalServerError, map[string]string{
        "error": "an unexpected error occurred",
    })
}
```

`PanicHandler` receives:
- `w http.ResponseWriter` — the response writer
- `r *http.Request` — the request that caused the panic
- `rcv any` — the value passed to `panic()`

---

## Patterns and Best Practices

### Wrap sentinel errors early

Define your domain errors using `muxmaster.Error` at the service boundary, so handlers never need to know status codes:

```go
// In your repository or service layer
var ErrUserNotFound = muxmaster.Error(http.StatusNotFound, errors.New("user not found"))
var ErrUserExists   = muxmaster.Error(http.StatusConflict,  errors.New("user already exists"))

// In the handler — no status code knowledge needed
mux.POSTE("/users", func(w http.ResponseWriter, r *http.Request) error {
    user, err := userService.Create(payload)
    if err != nil {
        return err // ErrUserExists passes through to ErrorHandler with 409
    }
    return muxmaster.JSON(w, http.StatusCreated, user)
})
```

### Distinguish client errors from server errors in the error handler

```go
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        if he.StatusCode() >= 500 {
            log.Printf("server error: %v", err) // log server errors
        }
        muxmaster.JSON(w, he.StatusCode(), map[string]string{"error": err.Error()})
        return
    }
    log.Printf("unexpected error: %v", err) // always log unexpected errors
    muxmaster.JSON(w, http.StatusInternalServerError, map[string]string{
        "error": "internal server error",
    })
}
```

### Use `errors.As` to unwrap chains

`muxmaster.Error` wraps the original error, so you can unwrap the chain:

```go
base := errors.New("record not found")
he   := muxmaster.Error(http.StatusNotFound, base)

errors.Is(he, base) // true — unwraps through the HTTPError wrapper
```

---

## See Also

- [Response Helpers](response-helpers.md) — JSON, XML, Text helpers
- [Middleware](middleware.md) — Recoverer middleware
- [Cookbook](cookbook.md) — error handling patterns for production APIs
