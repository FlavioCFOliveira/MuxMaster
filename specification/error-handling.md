# Error Handling

## Scope

This file specifies how the router handles four categories of error conditions: route not found, method not allowed, panics in handlers, and global OPTIONS responses. It also specifies the `HandlerFuncE` pattern, the `HTTPError` interface, and the `ErrorHandler` field.

This file does not cover redirect behavior (see [configuration.md](configuration.md) and [routing.md](routing.md)), or response helper functions (see [response-helpers.md](response-helpers.md)).

---

## 1. Route Not Found (404)

1. When no route matches the request path and method, and no redirect or 405 response applies, the router calls the handler assigned to `Mux.NotFound`.
2. If `Mux.NotFound` is nil, the router calls `http.NotFound(w, r)`, which writes a 404 status and plain-text body `"404 page not found"`.
3. `Mux.NotFound` is of type `http.Handler`. Any value that implements `http.Handler` is valid.
4. The `NotFound` handler does not receive path parameters. The request context has no `Params` value when `NotFound` is called.
5. Global middleware registered via `Use` wraps the `NotFound` handler. The router builds a middleware-wrapped `NotFound` handler and caches it; the cache is invalidated on every subsequent `Use` call (and by `Rebuild`), so the wrapping always reflects the most recently registered `Use` chain, not the chain in effect when `Mux.NotFound` was assigned or when any particular route was registered. Pre-routing middleware also wraps it, because pre-routing middleware wraps the entire router (section 2.1).

---

## 2. Method Not Allowed (405)

6. When `Mux.HandleMethodNotAllowed` is true, the path is registered for at least one method, but not for the requested method, the router sets the `Allow` response header and calls the handler assigned to `Mux.MethodNotAllowed`.
7. If `Mux.MethodNotAllowed` is nil, the router writes a plain-text 405 response with `http.Error(w, http.StatusText(405), 405)`.
8. The `Allow` header value is a comma-separated list of the HTTP methods registered for the matched path. `OPTIONS` is always included in the list. See [routing.md](routing.md) section 4.7, rule 61, for the exact method order, including where `QUERY` appears in it.
9. `Mux.MethodNotAllowed` is of type `http.Handler`.
10. Global middleware registered via `Use` wraps the `MethodNotAllowed` handler, with the same live-binding behavior as the `NotFound` handler (requirement 5). The router caches one middleware-wrapped handler per distinct `Allow` header value; every cache entry is invalidated on the next `Use` call (and by `Rebuild`), so the wrapping always reflects the most recently registered `Use` chain.

---

## 3. Panic Recovery

11. When `Mux.PanicHandler` is non-nil, the router installs a deferred recover at the start of every `ServeHTTP` call.
12. If a handler panics, the deferred recover calls `Mux.PanicHandler(w, r, rcv)` where `rcv` is the value passed to `panic`.
13. `Mux.PanicHandler` has the signature `func(http.ResponseWriter, *http.Request, any)`. The third argument is the recovered value.
14. When `Mux.PanicHandler` is nil, panics from handlers propagate normally. The recover is not installed, so there is zero overhead per request.
15. `Mux.PanicHandler` is responsible for writing an appropriate response. The router does not write any response after calling `PanicHandler`.
16. If `PanicHandler` itself panics, the panic propagates unrecovered.

---

## 4. GlobalOPTIONS Handler

17. `Mux.GlobalOPTIONS` is of type `http.Handler`. When set, it is called instead of the default 204 No Content response for automatic OPTIONS replies.
18. The router sets the `Allow` header before calling `GlobalOPTIONS`. The handler can read the header value via `w.Header().Get("Allow")`.
19. `GlobalOPTIONS` applies only to the automatic OPTIONS response triggered by `HandleOPTIONS`. It does not apply to paths where an OPTIONS handler has been explicitly registered.
20. If `GlobalOPTIONS` is nil, the automatic OPTIONS response writes status 204 with no body.
21. Global middleware registered via `Use` wraps the automatic OPTIONS response — whether or not `GlobalOPTIONS` is set — with the same live-binding behavior as the `NotFound` and `MethodNotAllowed` handlers (requirements 5 and 10). The router caches one middleware-wrapped handler per distinct `Allow` header value; every cache entry is invalidated on the next `Use` call (and by `Rebuild`), so the wrapping always reflects the most recently registered `Use` chain.

---

## 5. HandlerFuncE — Handler with Error Return

22. `HandlerFuncE` is an alternative handler type defined as `type HandlerFuncE func(http.ResponseWriter, *http.Request) error`.
23. `HandlerFuncE` is additive. It does not replace `http.HandlerFunc`. Both types are supported simultaneously.
24. `(*Mux).HandleE(method string, pattern string, h HandlerFuncE)` registers a `HandlerFuncE` handler. Internally, `HandleE` wraps `h` in an adapter that calls `h` and, if it returns a non-nil error, delegates the error to `Mux.ErrorHandler`.
25. Convenience methods `GETE`, `HEADE`, `POSTE`, `PUTE`, `PATCHE`, `DELETEE`, `OPTIONSE`, and `QUERYE` are the error-returning equivalents of `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, and `QUERY`. Each calls `HandleE` with the corresponding method. `QUERY` is a standard HTTP method (RFC 10008); see [routing.md](routing.md) section 9.
26. `(*Group).HandleE`, `(*Group).GETE`, and all corresponding group variants follow the same rules as their `*Mux` counterparts.
27. All middleware rules that apply to `Handle` apply equally to `HandleE`. The only difference is the handler signature.

---

## 6. HTTPError Interface

28. `HTTPError` is an interface:

```go
type HTTPError interface {
    error
    StatusCode() int
}
```

29. `HTTPError` extends the standard `error` interface with a `StatusCode() int` method.
30. `Error(code int, err error) HTTPError` is a package-level constructor that returns an `HTTPError` with the given status code and wrapping the given error.
31. If `err` is nil, `Error` panics.
32. The `Error()` string method of the returned value returns `err.Error()`.
33. `StatusCode()` returns `code`.

---

## 7. ErrorHandler

34. `Mux.ErrorHandler` has the signature `func(http.ResponseWriter, *http.Request, error)`.
35. When `HandleE` routes a request and the handler returns a non-nil error, the adapter calls `Mux.ErrorHandler(w, r, err)`.
36. If `Mux.ErrorHandler` is nil, the adapter writes a plain-text 500 response: `http.Error(w, "Internal Server Error", 500)`.
37. A typical `ErrorHandler` implementation uses `errors.As` to check whether the error implements `HTTPError` and writes the appropriate status code.

```go
// Example
mux.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    var he muxmaster.HTTPError
    if errors.As(err, &he) {
        http.Error(w, he.Error(), he.StatusCode())
        return
    }
    http.Error(w, "Internal Server Error", 500)
}
```

38. `ErrorHandler` is called only for errors returned by `HandlerFuncE` handlers. Standard `http.HandlerFunc` handlers do not use `ErrorHandler`.
39. `ErrorHandler` is responsible for writing the full response. The adapter does not write anything before or after calling `ErrorHandler`.
