# Standard Library Middleware

## Scope

This file specifies every middleware provided in the `muxmaster/middleware` sub-package.

All middleware in this sub-package follows the standard signature `func(http.Handler) http.Handler`. All middleware uses only the Go standard library. No external dependencies are permitted.

This file does not specify how middleware is registered (see [middleware.md](middleware.md)) or the pre-routing middleware scope.

---

## 1. Package

1. All middleware in this file is part of the `muxmaster/middleware` package.
2. The import path is `github.com/FlavioCFOliveira/MuxMaster/middleware`.
3. Every function in this package accepts configuration arguments and returns a `func(http.Handler) http.Handler`.

---

## 2. Logger

4. `Logger(out io.Writer) func(http.Handler) http.Handler` logs each HTTP request after it completes.
5. Each log line includes: timestamp, HTTP method, request path, response status code, and elapsed duration.
6. The format is a single line of plain text. JSON format is out of scope for this middleware (use a custom logger if structured logging is needed).
7. `Logger` uses the response status code written by the wrapped handler. To capture the status code, `Logger` wraps `http.ResponseWriter` with a recorder that intercepts `WriteHeader`.
8. If `out` is nil, `Logger` panics.
9. Concurrent requests write to `out` without additional synchronization. If `out` requires exclusive access, the caller must provide a thread-safe writer.

---

## 3. Recoverer

10. `Recoverer() func(http.Handler) http.Handler` recovers from panics in the wrapped handler.
11. On recovery, `Recoverer` writes a 500 response (if headers have not already been sent) and logs the panic value and stack trace to `os.Stderr`.
12. `Recoverer` is an alternative to `Mux.PanicHandler`. Using `Recoverer` as middleware provides idiomatic chaining behavior.
13. If `Recoverer` is used together with `Mux.PanicHandler`, the `Mux.PanicHandler` takes priority because it is installed at the `ServeHTTP` level before any middleware executes.

---

## 4. RequestID

14. `RequestID() func(http.Handler) http.Handler` generates a unique identifier for each request and:
    - Stores the identifier in the request context under an unexported key accessible via `middleware.GetRequestID(ctx context.Context) string`.
    - Sets the `X-Request-ID` response header to the same value.
15. If the incoming request already has an `X-Request-ID` header, that value is used instead of generating a new one.
16. The generated identifier is a random 16-byte value encoded as a lowercase hexadecimal string (32 characters). It uses `crypto/rand`.
17. `middleware.GetRequestID(ctx context.Context) string` returns the request ID stored in `ctx`, or `""` if none is present.

---

## 5. RealIP

18. `RealIP() func(http.Handler) http.Handler` overwrites `r.RemoteAddr` with the client IP extracted from trusted proxy headers.
19. The headers are checked in this order: `X-Forwarded-For` (first IP in the comma-separated list), then `X-Real-IP`.
20. If neither header is present, `r.RemoteAddr` is not modified.
21. `RealIP` does not validate that the request comes from a trusted proxy. Use this middleware only when the server is behind a known reverse proxy.

---

## 6. Timeout

22. `Timeout(d time.Duration) func(http.Handler) http.Handler` applies a `context.WithTimeout` to the request context.
23. If the handler does not complete within `d`, the context is cancelled.
24. `Timeout` does not automatically write a 503 or 504 response. The handler is responsible for checking `ctx.Err()` and writing an appropriate response when the context is cancelled.
25. If `d` is zero or negative, `Timeout` panics.

---

## 7. NoCache

26. `NoCache() func(http.Handler) http.Handler` sets response headers to instruct clients and proxies not to cache the response.
27. The headers set are:
    - `Cache-Control: no-store, no-cache, must-revalidate`
    - `Pragma: no-cache`
    - `Expires: 0`

---

## 8. Compress

28. `Compress(level int) func(http.Handler) http.Handler` compresses the response body using gzip when the request includes `Accept-Encoding: gzip`.
29. `level` is passed to `compress/gzip.NewWriterLevel`. Valid values are `gzip.DefaultCompression` (-1), `gzip.BestSpeed` (1) through `gzip.BestCompression` (9), and `gzip.NoCompression` (0). An invalid level causes a panic.
30. If the client does not accept gzip, the response is sent uncompressed.
31. `Compress` sets `Content-Encoding: gzip` and removes `Content-Length` from compressed responses (because the compressed length is not known until the body is fully written).
32. `Compress` adds `Vary: Accept-Encoding` to the response.
33. Small responses (less than 1 KB) are not compressed. The threshold is not configurable in this version.

---

## 9. BasicAuth

34. `BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler` enforces HTTP Basic Authentication.
35. `creds` maps usernames to passwords. The comparison is constant-time to prevent timing attacks.
36. If the provided credentials are not found in `creds`, the middleware responds with 401 and a `WWW-Authenticate: Basic realm="realm"` header.
37. `realm` is used verbatim in the `WWW-Authenticate` header. It must not contain a double-quote character (the behavior is unspecified if it does).
38. Calling `BasicAuth` with a nil `creds` map causes a panic.

---

## 10. CORS

39. `CORS(opts CORSOptions) func(http.Handler) http.Handler` handles Cross-Origin Resource Sharing headers.
40. `CORSOptions` is a struct with the following fields:

| Field | Type | Description |
|---|---|---|
| `AllowedOrigins` | `[]string` | Allowed origins. Use `["*"]` to allow all origins. |
| `AllowedMethods` | `[]string` | Allowed HTTP methods. |
| `AllowedHeaders` | `[]string` | Allowed request headers. |
| `ExposedHeaders` | `[]string` | Response headers exposed to the browser. |
| `AllowCredentials` | `bool` | Whether to include `Access-Control-Allow-Credentials: true`. |
| `MaxAge` | `int` | Value for `Access-Control-Max-Age` in seconds. 0 means omit the header. |

41. `CORS` handles preflight OPTIONS requests by responding with the appropriate `Access-Control-*` headers and status 204, then returning without calling the next handler.
42. `AllowCredentials` must not be `true` when `AllowedOrigins` contains `"*"`. This combination is rejected by browsers and causes `CORS` to panic.
43. An empty `AllowedOrigins` list causes `CORS` to block all cross-origin requests (returns 403 for cross-origin requests).

---

## 11. ThrottleBacklog

44. `ThrottleBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler` limits concurrent handler execution.
45. At most `limit` requests are handled concurrently.
46. Up to `backlog` additional requests wait in a queue. If the queue is full, the middleware responds immediately with 503 Service Unavailable.
47. A waiting request that is not picked up within `timeout` receives a 503 response. `timeout` is measured from when the request enters the queue.
48. If `limit` is less than or equal to 0, `ThrottleBacklog` panics.
49. If `backlog` is less than 0, `ThrottleBacklog` panics.

---

## 12. StripSlashes

50. `StripSlashes() func(http.Handler) http.Handler` removes trailing slashes from `r.URL.Path` before passing the request to the next handler.
51. The root path `/` is not modified.
52. `StripSlashes` modifies `r.URL.Path` in place. It does not issue a redirect.
53. `StripSlashes` is intended for use with `Pre()` so that it runs before route lookup. Using it as regular middleware (after route lookup) has no effect on routing.

---

## 13. CleanPath

54. `CleanPath() func(http.Handler) http.Handler` normalizes `r.URL.Path` by applying `path.Clean` before passing the request to the next handler.
55. `path.Clean` removes double slashes (`//`), resolves `.` and `..` components, and ensures the path begins with `/`.
56. `CleanPath` modifies `r.URL.Path` in place. It does not issue a redirect.
57. `CleanPath` is intended for use with `Pre()` so that it runs before route lookup.

---

## 14. SetHeader

58. `SetHeader(key, value string) func(http.Handler) http.Handler` sets a fixed response header on every response produced by the wrapped handler.
59. The header is set via `w.Header().Set(key, value)` before calling the next handler. The next handler may overwrite or delete this header.

---

## 15. WithValue

60. `WithValue(key, val any) func(http.Handler) http.Handler` injects a value into the request context.
61. The value is set via `context.WithValue(r.Context(), key, val)` and the request is updated with the new context before calling the next handler.
62. `key` must be a comparable type. A nil key causes a panic (consistent with `context.WithValue`).
