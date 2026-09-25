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
7. `Logger` wraps `http.ResponseWriter` with a recorder that intercepts `WriteHeader` to capture the status code for the log line. Every `WriteHeader` call is still forwarded to the underlying `http.ResponseWriter` unchanged, so the client always receives whatever status the handler actually sends. Only the status *recorded for the log line* follows a first-call-wins rule: once a non-informational status has been recorded, later `WriteHeader` calls do not change the logged status. A `Write` call with no prior `WriteHeader` implicitly records status 200, exactly as a bare `http.ResponseWriter` does.
8. 1xx informational status codes (for example, 100 Continue or 103 Early Hints), other than 101 Switching Protocols, never update the recorded status. A handler that sends an informational response ahead of its real final status is logged with that final status, not the informational one.
9. The recorder implements `http.Flusher`, delegating to the underlying `http.ResponseWriter` when it supports flushing, and `Unwrap() http.ResponseWriter`, for `http.ResponseController` and other `Unwrap`-aware callers. Streaming responses and optional interfaces such as `http.Hijacker` remain reachable through `Logger`.
10. The recorder implements `io.ReaderFrom`, delegating to the underlying `http.ResponseWriter`'s own `io.ReaderFrom` when it has one. This preserves the `sendfile`/`splice` fast path that `http.ServeContent` and `http.ServeFile` reach through `io.CopyN`; without it, a file response wrapped by `Logger` would fall back to a buffered copy loop.
11. If `out` is nil, `Logger` panics.
12. Concurrent requests write to `out` without additional synchronization. If `out` requires exclusive access, the caller must provide a thread-safe writer.

---

## 3. Recoverer

13. `Recoverer() func(http.Handler) http.Handler` recovers from panics in the wrapped handler.
14. On recovery, `Recoverer` writes a 500 response (if headers have not already been sent) and logs the panic value and stack trace to `os.Stderr`.
15. `Recoverer` is an alternative to `Mux.PanicHandler`. Using `Recoverer` as middleware provides idiomatic chaining behavior.
16. If `Recoverer` is used together with `Mux.PanicHandler`, the `Mux.PanicHandler` takes priority because it is installed at the `ServeHTTP` level before any middleware executes.

---

## 4. RequestID

17. `RequestID() func(http.Handler) http.Handler` generates a unique identifier for each request and:
    - Stores the identifier in the request context under an unexported key accessible via `middleware.GetRequestID(ctx context.Context) string`.
    - Sets the `X-Request-ID` response header to the same value.
18. If the incoming request already has an `X-Request-ID` header, that value is used instead of generating a new one.
19. The generated identifier is a random 16-byte value encoded as a lowercase hexadecimal string (32 characters). It uses `crypto/rand`.
20. `middleware.GetRequestID(ctx context.Context) string` returns the request ID stored in `ctx`, or `""` if none is present.

---

## 5. RealIP

21. `RealIP(trustedCIDRs ...*netip.Prefix) func(http.Handler) http.Handler` overwrites `r.RemoteAddr` with a client IP derived from the `X-Forwarded-For` or `X-Real-IP` header. The header is trusted only when the request's immediate TCP peer address (parsed from `r.RemoteAddr`) falls inside one of the given `trustedCIDRs`. If the peer address cannot be parsed, or `trustedCIDRs` is non-empty and the peer is not inside any of them, `r.RemoteAddr` is left unmodified and neither header is consulted. When `RealIP` is called with no `trustedCIDRs` at all, every peer is trusted implicitly, and `RealIP` emits a one-time `slog.Warn` at construction time, because this makes the header trivially spoofable by any client.
22. When `X-Forwarded-For` is present and the peer is trusted, `RealIP` selects the client address with a rightmost-untrusted-hop algorithm: the header is parsed as a comma-separated list and scanned from the rightmost entry toward the left, skipping every entry whose address falls inside a `trustedCIDRs` prefix. The first entry that does not fall inside any trusted prefix becomes `r.RemoteAddr`. If every entry in the header lies inside a trusted prefix, the leftmost valid entry is used instead. When `RealIP` was called with no `trustedCIDRs`, this degrades to the leftmost valid entry (legacy single-proxy behavior). If no entry in the header parses as a valid address, `r.RemoteAddr` is left unmodified.
23. At most the rightmost 30 comma-separated entries of `X-Forwarded-For` are scanned; any entries further to the left are ignored. This bounds the CPU cost of parsing an arbitrarily long, adversarial header.
24. When `X-Forwarded-For` is absent or empty, and `X-Real-IP` is present, and the peer is trusted, `RealIP` uses the `X-Real-IP` value as `r.RemoteAddr` after validating it as a well-formed IP address; an invalid value leaves `r.RemoteAddr` unmodified. Every address `RealIP` accepts, from either header, is parsed with `netip.ParseAddr`, and any IPv6 zone identifier is stripped from the result.

---

## 6. Timeout

25. `Timeout(d time.Duration) func(http.Handler) http.Handler` applies a `context.WithTimeout` to the request context.
26. If the handler does not complete within `d`, the context is cancelled.
27. `Timeout` does not automatically write a 503 or 504 response. The handler is responsible for checking `ctx.Err()` and writing an appropriate response when the context is cancelled.
28. If `d` is zero or negative, `Timeout` panics.

---

## 7. NoCache

29. `NoCache() func(http.Handler) http.Handler` sets response headers to instruct clients and proxies not to cache the response.
30. The headers set are:
    - `Cache-Control: no-store, no-cache, must-revalidate`
    - `Pragma: no-cache`
    - `Expires: 0`

---

## 8. Compress

31. `Compress(level int) func(http.Handler) http.Handler` compresses the response body using gzip when the request includes `Accept-Encoding: gzip`.
32. `level` is passed to `compress/gzip.NewWriterLevel`. Valid values are `gzip.DefaultCompression` (-1), `gzip.BestSpeed` (1) through `gzip.BestCompression` (9), and `gzip.NoCompression` (0). An invalid level causes a panic.
33. If the client does not accept gzip, the response is sent uncompressed.
34. `Compress` sets `Content-Encoding: gzip` and removes `Content-Length` from compressed responses (because the compressed length is not known until the body is fully written).
35. `Compress` adds `Vary: Accept-Encoding` to the response.
36. Small responses (less than 1 KB) are not compressed. The threshold is not configurable in this version.
37. The response status ultimately sent to the client follows a first-call-wins rule: once `WriteHeader` has fixed a non-informational status, later calls — for example, a handler that writes an error page after already committing its real status — are no-ops, matching the behavior of a bare `http.ResponseWriter`. 1xx informational codes, other than 101 Switching Protocols, are exempt from this rule and never latch the status.
38. `Compress` implements `http.Flusher`. Calling `Flush` before enough bytes have been written to decide whether to compress forces that decision immediately, using whichever headers the decision would set at that point; the underlying `gzip.Writer` (when compression was chosen) is flushed, followed by the wrapped `http.ResponseWriter`. This allows streaming responses, such as Server-Sent Events, to reach the client without waiting for the handler to return. A stream whose first flush happens before the compress/skip decision would otherwise have been made is served uncompressed for its entire remaining lifetime.
39. `Compress` implements `Unwrap() http.ResponseWriter`, returning the wrapped writer so `http.ResponseController` and other `Unwrap`-aware callers can reach optional interfaces this wrapper does not itself implement, such as `http.Hijacker` and `http.Pusher`.
40. `Compress` recycles its internal `*gzip.Writer` values and response-writer wrapper across requests through a `sync.Pool`. This is an internal optimization; it does not change any observable response behavior.

---

## 9. BasicAuth

41. `BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler` enforces HTTP Basic Authentication.
42. `creds` maps usernames to passwords. The comparison is constant-time to prevent timing attacks.
43. If the provided credentials are not found in `creds`, the middleware responds with 401 and a `WWW-Authenticate: Basic realm="realm"` header.
44. `realm` is used verbatim in the `WWW-Authenticate` header. It must not contain a double-quote character (the behavior is unspecified if it does).
45. Calling `BasicAuth` with a nil `creds` map causes a panic.

---

## 10. CORS

46. `CORS(opts CORSOptions) func(http.Handler) http.Handler` handles Cross-Origin Resource Sharing headers.
47. `CORSOptions` is a struct with the following fields:

| Field | Type | Description |
|---|---|---|
| `AllowedOrigins` | `[]string` | Allowed origins. Use `["*"]` to allow all origins. |
| `AllowedMethods` | `[]string` | Allowed HTTP methods. |
| `AllowedHeaders` | `[]string` | Allowed request headers. |
| `ExposedHeaders` | `[]string` | Response headers exposed to the browser. |
| `AllowCredentials` | `bool` | Whether to include `Access-Control-Allow-Credentials: true`. |
| `MaxAge` | `int` | Value for `Access-Control-Max-Age` in seconds. 0 means omit the header. |

48. `CORS` handles preflight OPTIONS requests by responding with the appropriate `Access-Control-*` headers and status 204, then returning without calling the next handler.
49. `AllowCredentials` must not be `true` when `AllowedOrigins` contains `"*"`. This combination is rejected by browsers and causes `CORS` to panic.
50. An empty `AllowedOrigins` list causes `CORS` to block all cross-origin requests (returns 403 for cross-origin requests).

---

## 11. ThrottleBacklog

51. `ThrottleBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler` limits concurrent handler execution.
52. At most `limit` requests are handled concurrently.
53. Up to `backlog` additional requests wait in a queue. If the queue is full, the middleware responds immediately with 503 Service Unavailable.
54. A waiting request that is not picked up within `timeout` receives a 503 response. `timeout` is measured from when the request enters the queue.
55. If `limit` is less than or equal to 0, `ThrottleBacklog` panics.
56. If `backlog` is less than 0, `ThrottleBacklog` panics.

---

## 12. StripSlashes

57. `StripSlashes() func(http.Handler) http.Handler` removes trailing slashes from `r.URL.Path` before passing the request to the next handler.
58. The root path `/` is not modified.
59. `StripSlashes` never mutates the original request. When the path has trailing slashes to strip, `StripSlashes` builds a shallow request copy (see the Terminology section in [README.md](README.md)) — a new `*http.Request` sharing the original's header map and context, with a new `*url.URL` copied from the original — assigns the stripped path to the copy's `URL.Path` (and, when `r.URL.RawPath` is set, the equivalently stripped value to the copy's `URL.RawPath`), and passes the copy to the next handler. If the path has no trailing slash, the original request is passed through unchanged. `StripSlashes` does not issue a redirect.
60. `StripSlashes` is intended for use with `Pre()` so that it runs before route lookup. Using it as regular middleware (after route lookup) has no effect on routing.

---

## 13. CleanPath

61. `CleanPath() func(http.Handler) http.Handler` normalizes `r.URL.Path` by applying `path.Clean` before passing the request to the next handler.
62. `path.Clean` removes double slashes (`//`), resolves `.` and `..` components, and ensures the path begins with `/`.
63. `CleanPath` never mutates the original request. When the cleaned path differs from the original, `CleanPath` builds a shallow request copy (see the Terminology section in [README.md](README.md)) — a new `*http.Request` sharing the original's header map and context, with a new `*url.URL` copied from the original — assigns the cleaned path to the copy's `URL.Path`, and passes the copy to the next handler. If the path is already clean, the original request is passed through unchanged. `CleanPath` does not issue a redirect.
64. `CleanPath` is intended for use with `Pre()` so that it runs before route lookup.

---

## 14. SetHeader

65. `SetHeader(key, value string) func(http.Handler) http.Handler` sets a fixed response header on every response produced by the wrapped handler.
66. `SetHeader` panics at construction time if `key` or `value` contains a carriage return (`\r`) or line feed (`\n`) byte.
67. `key` is canonicalized once, at construction time, via the same rule `http.CanonicalHeaderKey` applies. On each request, the header is installed by writing that canonical key directly into the response's header map, with a header value slice allocated fresh for that request. This produces the same observable header as calling `w.Header().Set(key, value)` on every request, without repeating the canonicalization work on each one. The next handler may overwrite or delete this header.

---

## 15. WithValue

68. `WithValue(key, val any) func(http.Handler) http.Handler` injects a value into the request context.
69. The value is set via `context.WithValue(r.Context(), key, val)` and the request is updated with the new context before calling the next handler.
70. `key` must be a comparable type. A nil key causes a panic (consistent with `context.WithValue`).
