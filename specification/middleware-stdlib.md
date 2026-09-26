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
50. **Corrects a prior inaccuracy in this rule.** An empty `AllowedOrigins` list does not cause a runtime 403 response. `CORS(opts)` panics at construction time when `opts.AllowedOrigins` is empty (`len(opts.AllowedOrigins) == 0`), before the returned middleware ever handles a request: `middleware: CORS requires a non-empty AllowedOrigins (use []string{"*"} for wildcard, or an explicit allowlist; nil silently allows traffic with no Access-Control-Allow-Origin)`. This is a misconfiguration caught at boot, not a per-request outcome — an application cannot reach request-serving time with an empty `AllowedOrigins`. See section 17 for the 400 and 403 responses `CORS` actually produces at request time, and the conditions that produce each.

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

---

## 16. CORS Vary: Origin (TM-2026-033)

71. **This is a behavior change, fixing TM-2026-033.** `CORS` (section 10) now adds `Vary: Origin` to every response it produces or forwards — unconditionally, regardless of whether the request carries an `Origin` header, regardless of whether `AllowedOrigins` is the wildcard `["*"]` or an explicit allow-list, regardless of whether the origin is allowed or rejected, and regardless of whether the eventual response is a simple response, a preflight (`OPTIONS`) response, or an error response `CORS` generates itself — a 400 or a 403; see section 17 for the exact condition and body of each. Previously, `Vary: Origin` was added only when the request carried an `Origin` header that matched a specific, non-wildcard entry in `AllowedOrigins`. Every other case — no `Origin` header at all, a wildcard `AllowedOrigins`, and any of `CORS`'s own error responses — reached a downstream or intermediary HTTP cache with no `Vary: Origin`, so a cacheable response for a request without CORS relevance could be reused for a same-URL request from a different, CORS-relevant origin. A browser then blocks the reused response because it does not carry the `Access-Control-Allow-Origin` value that origin's CORS check requires, even though the server would have supplied one for a request it actually saw. This is exactly the failure mode the Fetch Standard describes in its background-reading section "CORS protocol and HTTP caches" (Fetch Standard, section 8.4): a cache that is not aware a response varies by `Origin` can serve it across origins in a way the CORS protocol does not intend.
72. Rule 71 does not change `Access-Control-Allow-Origin` or any other `Access-Control-*` header: a request without an `Origin` header still receives none of them, exactly as before. Only `Vary: Origin` is now added unconditionally; every other header `CORS` sets remains conditioned exactly as section 10 already describes.
73. If `Vary` already has one or more values when `CORS` runs — for example, `Vary: Accept-Encoding`, already set by `Compress` (rule 35) — `CORS` does not overwrite them and does not merge them into a single comma-separated value. It adds `Origin` as an additional value, the same way `http.Header.Add` would: the response is sent with one `Vary` response header line per value (`Vary: Accept-Encoding` followed by a separate `Vary: Origin` line), not a single `Vary: Accept-Encoding, Origin` line. This is the same merging rule already in effect, before this section was added, for the one case that already added `Vary: Origin` (a matched, non-wildcard origin); rule 71 only widens when that rule fires, not how it merges.
74. Before adding `Origin`, `CORS` checks whether `Origin` is already present, case-insensitively, as one of the comma-separated tokens within any existing `Vary` value. If it is, `CORS` does not add a second `Origin` token or a second `Vary: Origin` line. `Vary`'s values are HTTP field names, and field names are case-insensitive tokens (RFC 9110 section 5.1); this check follows that rule.
75. Rules 73 and 74 hold regardless of which middleware — `CORS` or `Compress` — runs first in the `Use()` chain: whichever `Vary` value(s) are already present when `CORS` runs are preserved unchanged, and `CORS` only ever adds its own `Origin` value alongside them, once, per rule 74.

---

## 17. CORS Request-Time Error Responses

76. `CORS` (section 10) responds with 400 Bad Request, without calling the next handler, when the request carries a non-empty `Origin` header containing a carriage return (`\r`), line feed (`\n`), or NUL byte. The response body is the literal text `Bad Request`, written via `net/http.Error`, exactly as for any other middleware in this file that uses `net/http.Error` (section 9, requirement 43; section 11, requirements 53-54). This check runs before any `Access-Control-*` or `Vary` header is set for the request, and before the `AllowedOrigins` check in requirement 77; it exists to prevent a `Origin` header value from being reflected into a response header (`Access-Control-Allow-Origin`, for a non-wildcard match) with an embedded CRLF or NUL byte, which could otherwise inject additional response headers or truncate the header value.

77. `CORS` responds with 403 Forbidden, without calling the next handler, when the request carries a non-empty, validly-formed `Origin` header that is not the wildcard case (`AllowedOrigins` does not contain `"*"`) and does not exactly match any entry in `AllowedOrigins`. The response body is the literal text `Forbidden`, written via `net/http.Error`. Per rule 71, this response still carries `Vary: Origin` (merged per rules 73-74 with any `Vary` value already present).

78. Neither response in requirements 76 or 77 is reachable when `AllowedOrigins` is empty, because `CORS` panics at construction time in that case (rule 50) before any request can be served.
