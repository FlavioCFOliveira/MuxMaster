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

See section 21 for `ThrottleAllBacklog` (an alternative name for this same function), and for `ThrottlePerIP` / `ThrottlePerIPCapped`, which limit concurrency per client key instead of globally across all clients.

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

---

## 18. APIKey

79. `APIKey(opts APIKeyOptions) func(http.Handler) http.Handler` authenticates requests by comparing an extracted key against a pre-configured set of valid keys.

| Field | Type | Description |
|---|---|---|
| `Keys` | `map[string]string` | Maps a raw API key value to the identity string injected into the request context on a match. Panics at construction if `nil` or empty. |
| `Header` | `string` | Request header read to extract the key when `ExtractFn` is nil. Default: `X-API-Key`. |
| `ExtractFn` | `func(*http.Request) string` | Overrides key extraction. When set, `Header` is not consulted. |

80. `APIKey(opts)` panics with `middleware: APIKey requires at least one key in opts.Keys` when `len(opts.Keys) == 0`.
81. At construction time, every value in `opts.Keys` is SHA-256 hashed into an internal `map[[32]byte]string`; the raw key strings themselves are not retained after construction. Per-request cost is one SHA-256 hash of the extracted key plus one `[32]byte`-keyed map lookup — no iteration over the configured key set and no per-key string comparison.
82. At request time, the key is extracted via `ExtractFn` (if set) or by reading the `Header` request header:
    - If the extracted value is the empty string, `APIKey` responds with 401 and sets `WWW-Authenticate: ApiKey realm="api"`.
    - If the extracted value's SHA-256 hash is not present in the hashed key set, `APIKey` responds with 401 and sets `WWW-Authenticate: ApiKey realm="api", error="invalid_key"`.
    - If the hash is found, the associated identity string is injected into the request context (retrievable via `GetAPIKeyIdentity`) and the next handler is called. Before doing so, `APIKey` also sets and immediately deletes a `WWW-Authenticate` header on this success path, so that the header-map manipulation cost is identical on both the hit and the miss paths (TSC-2026-0008): without this, the miss path's extra `Header().Set` call was measurably slower than the hit path, letting a caller distinguish a valid key from an invalid one by response latency alone. The client-visible response carries no `WWW-Authenticate` header on this path, because the header is deleted before the response is sent.
83. `GetAPIKeyIdentity(ctx context.Context) (string, bool)` returns the identity string injected by `APIKey`, and `false` if no `APIKey` middleware ran for the request (or a matching key was never found).

---

## 19. JWTAuth

84. `JWTAuth(opts JWTOptions) func(http.Handler) http.Handler` validates a JSON Web Token carried in the `Authorization` request header using the `Bearer` scheme (RFC 6750). The scheme name is matched case-insensitively (`bearer`, `Bearer`, `BEARER`), sharing the same extraction helper `OAuth2Introspect` uses (section 20).

| Field | Type | Description |
|---|---|---|
| `Secret` | `[]byte` | HMAC signing key. Required when `Algorithms` includes `HS256`, `HS384`, or `HS512`. |
| `PublicKey` | `crypto.PublicKey` | RSA or ECDSA public key. Required when `Algorithms` includes an `RS*` or `ES*` algorithm. |
| `Algorithms` | `[]string` | Accepted signing algorithms. Must be non-empty. Supported values: `HS256`, `HS384`, `HS512`, `RS256`, `RS384`, `RS512`, `ES256`, `ES384`, `ES512`. |
| `Issuers` | `[]string` | When non-empty, the token's `iss` claim must exactly match one entry. |
| `Audiences` | `[]string` | When non-empty, at least one entry of the token's `aud` claim must match one entry. |
| `ClockSkew` | `time.Duration` | Permitted clock drift applied to `exp` and `nbf` comparisons. Default: `0`. |
| `RequireExpiry` | `bool` | When `true`, a token whose payload has no `exp` claim (or `exp == 0`) is rejected. Default: `false`. |

85. `JWTAuth(opts)` panics with `middleware: JWTAuth requires at least one algorithm in opts.Algorithms` when `opts.Algorithms` is empty.
86. For each algorithm listed in `opts.Algorithms`, construction validates the required key material and panics if it is missing or of the wrong type:
    - `HS256`, `HS384`, `HS512`: panics with `middleware: JWTAuth HS256 requires opts.Secret` (respectively `HS384`, `HS512`) when `opts.Secret` is `nil`.
    - `RS256`, `RS384`, `RS512`: panics with `middleware: JWTAuth <alg> requires opts.PublicKey to be *rsa.PublicKey` when `opts.PublicKey` does not have that concrete type.
    - `ES256`: panics with `middleware: JWTAuth ES256 requires opts.PublicKey to be *ecdsa.PublicKey` when the type does not match, or with `middleware: JWTAuth ES256 requires a P-256 public key (RFC 7518 §3.4)` when it is an `*ecdsa.PublicKey` on the wrong curve. `ES384` and `ES512` apply the identical pattern against P-384 and P-521 respectively.
    - Any other string in `opts.Algorithms` panics with `middleware: JWTAuth unsupported algorithm: <alg>`.
87. Construction emits a one-time `slog.Warn` in two cases, neither of which prevents construction from succeeding: when `opts.Algorithms` mixes more than one algorithm family (`HS*`, `RS*`, `ES*`) in the same `JWTAuth` instance (TSC-2026-0003 — verification latency differs measurably between families, e.g. HMAC versus RSA-2048, letting a caller infer which algorithm path a given token took); and whenever `opts.RequireExpiry` is left at its default `false` (TM-2026-001, RFC 8725 §4.4 — a token with no `exp` claim never expires).
88. If the `Authorization` header does not carry a well-formed `Bearer <token>` value, `JWTAuth` responds with 401 and sets `WWW-Authenticate: Bearer realm="api"`, without attempting to parse a token.
89. A token is structurally three `.`-separated base64url segments (header, payload, signature). Signature verification always runs before the payload is parsed or any claim is inspected, so a token cannot influence claim-parsing behavior before its signature has been checked.
90. The header segment is base64url-decoded and JSON-parsed into an `alg` string and an optional `crit` array (RFC 7515 section 4.1.11). The token is rejected (as an invalid token, requirement 93) if: the segment does not decode as base64url; the decoded bytes are not valid JSON; `alg` is not one of the algorithms in `opts.Algorithms`; or `crit` is present and non-empty (this implementation understands no critical extensions, so any `crit` entry mandates rejection). The single most recently accepted (header-segment-bytes, validated `alg`) pair is cached in a per-`JWTAuth`-instance, lock-free single-entry memo (WH-06): a byte-identical header segment on a later request skips the decode and validation steps and reuses the cached `alg`, but this memo never participates in signature verification (which still runs, unconditionally, on every request) and can only ever return a result that a full decode would also have produced, because an entry is stored only after passing the same checks.
91. Signature verification runs over the exact byte range `header.payload` (the token substring up to and including the second `.`), never decoded. For `HS256`/`HS384`/`HS512`, verification uses a pooled `hash.Hash` (recycled across requests to avoid re-deriving the HMAC key schedule) and constant-time comparison (`crypto/hmac.Equal`). For `RS256`/`RS384`/`RS512`, verification uses `crypto/rsa.VerifyPKCS1v15` against the corresponding SHA-2 digest. For `ES256`/`ES384`/`ES512`, the signature is expected in the fixed-width IEEE P1363 `r‖s` format (not ASN.1 DER); a signature of the wrong byte length is rejected without calling `crypto/ecdsa.Verify`.
92. Once the signature verifies, the payload segment is base64url-decoded and JSON-parsed into `sub`, `iss`, `aud` (accepted as either a single JSON string or a JSON array of strings), `exp`, `nbf`, and `iat` (each a `NumericDate` per RFC 7519 section 2, i.e. an integer count of seconds since the Unix epoch). The token is rejected when any of the following holds:
    - `exp`, `nbf`, or `iat` is negative (TM-2026-002 — RFC 7519 section 2 defines `NumericDate` as non-negative).
    - `opts.RequireExpiry` is `true` and `exp` is absent (`0`).
    - `exp` is present and `time.Now()` is after `time.Unix(exp, 0)` plus `opts.ClockSkew`.
    - `nbf` is present and `time.Now()` plus `opts.ClockSkew` is before `time.Unix(nbf, 0)`.
    - `opts.Issuers` is non-empty and `iss` is not one of its entries.
    - `opts.Audiences` is non-empty and no entry of `aud` is one of its entries.
93. Every rejection described in requirements 88-92 — a missing or malformed `Authorization` header excepted (requirement 88, which never carries the `error` parameter) — produces the same 401 response: `WWW-Authenticate: Bearer realm="api", error="invalid_token"`. `JWTAuth` does not distinguish an expired token from an invalid signature, an unmet audience, or any other rejection reason in the HTTP response; the distinct internal error values (invalid, expired, not-yet-valid) are not exposed to the client.
94. On success, a `*JWTClaims` value is injected into the request context, retrievable via `GetJWTClaims(ctx context.Context) (*JWTClaims, bool)`. `JWTClaims` has the exported fields `Subject`, `Issuer`, `Audience []string`, `ExpiresAt time.Time`, `IssuedAt time.Time`, `NotBefore time.Time` (each `time.Time` is the zero value when the corresponding claim was absent from the token), and `RawPayload []byte` — the decoded JSON payload bytes, made available so application code can unmarshal claims beyond the standard set this middleware itself parses.

---

## 20. OAuth2Introspect

95. `OAuth2Introspect(opts OAuth2Options) func(http.Handler) http.Handler` validates a Bearer token by calling a remote RFC 7662 token introspection endpoint. Token extraction defaults to the same `Authorization: Bearer <token>` helper `JWTAuth` uses (section 19, requirement 84) and can be overridden.

| Field | Type | Description |
|---|---|---|
| `Endpoint` | `string` | RFC 7662 introspection URL. Required. Must use the `https://` scheme unless `AllowInsecureEndpoint` is `true`. |
| `AllowInsecureEndpoint` | `bool` | Disables the HTTPS-only enforcement on `Endpoint`. Default: `false`. |
| `ClientID`, `ClientSecret` | `string` | When `ClientID` is non-empty, the introspection request authenticates via HTTP Basic using these values. |
| `CacheTTL` | `time.Duration` | How long an active token's introspection result is cached. `0` selects the default of 60 seconds; a negative value disables caching entirely. |
| `MaxCacheSize` | `int` | Caps the number of distinct cached tokens. A value `<= 0` selects the default of 10000. |
| `HTTPClient` | `*http.Client` | Used for introspection HTTP requests. Default (when `nil`): a new client, built once per `OAuth2Introspect` call, with a 10-second `Timeout`. If `http.DefaultTransport` is an `*http.Transport`, the client's `Transport` is a new `*http.Transport` built at construction by copying the exported settings of `http.DefaultTransport` (proxy, dialers, TLS configuration, timeouts, buffer sizes, protocol selection). The copy reads those fields only and calls no `http.DefaultTransport` method, so it neither modifies nor initialises `http.DefaultTransport` (in particular, it does not trigger its HTTP/2 set-up). `TLSNextProto` is not copied; when `http.DefaultTransport.TLSNextProto` is non-nil and has no `"h2"` entry (the documented way to disable HTTP/2), the new transport receives an empty non-nil map, so HTTP/2 stays disabled. The new transport always has keep-alives enabled (`DisableKeepAlives` is `false`) and sets `MaxIdleConns`, `MaxIdleConnsPerHost`, and `MaxConnsPerHost` to 100: calls beyond that limit wait for a free connection, and the wait counts toward the 10-second `Timeout`. The bound of 100 connections is exact for HTTP/1.1; over HTTP/2 each connection multiplexes many calls, and the HTTP/2 layer may open additional connections. The copy is a snapshot: later changes to `http.DefaultTransport` do not affect the middleware. If `http.DefaultTransport` is not an `*http.Transport`, the client's `Transport` is left `nil`, so `http.DefaultTransport` is used unchanged. Because construction reads `http.DefaultTransport`'s fields, an application that relies on this default must construct the middleware during startup, before any concurrent use of `http.DefaultTransport`. |
| `ExtractFn` | `func(*http.Request) string` | Overrides token extraction. Default: the same Bearer-scheme extractor `JWTAuth` uses. |

96. Construction validates `opts.Endpoint` and panics on any of the following, in this order — every panic message that could otherwise echo a credential embedded in `opts.Endpoint` (for example `https://user:secret@host/introspect`) instead renders a redacted form with the userinfo component removed entirely, never password-masked (CWE-532):
    1. `opts.Endpoint == ""`: `middleware: OAuth2Introspect requires a non-empty opts.Endpoint`.
    2. `opts.Endpoint` fails `url.Parse`: `middleware: OAuth2Introspect: malformed Endpoint URL (<redacted>): <reason>`, where `<redacted>` strips any `user:pass@` or `user@` substring immediately following the scheme separator via a regular expression (there is no parsed `*url.URL` to redact structurally at this stage) and `<reason>` is the underlying parse error, never the raw URL `url.Parse` itself would otherwise have echoed.
    3. The parsed URL's host component is empty: `middleware: OAuth2Introspect: Endpoint URL has no host: <redacted>`, where `<redacted>` is the parsed URL re-serialized with its userinfo component cleared.
    4. The parsed URL contains a userinfo component (`user:pass@` or `user@`): `middleware: OAuth2Introspect: Endpoint URL must not contain userinfo (RFC 3986 §3.2.1) — credentials in URL are an exfiltration vector: <redacted>`.
    5. The parsed URL's scheme is not `https` and `opts.AllowInsecureEndpoint` is not `true`: `middleware: OAuth2Introspect: Endpoint must use https:// — bearer tokens over plaintext leak to passive observers (RFC 7662 §4). Set OAuth2Options.AllowInsecureEndpoint=true ONLY for testing.`
97. If the scheme is not `https` and `opts.AllowInsecureEndpoint` is `true`, construction succeeds but emits a one-time `slog.Warn` logging only the resolved `host` and `scheme` fields (never the full URL, never any userinfo). Construction also always emits a `slog.Info` at this same reduced (`host`, `scheme`) granularity when it succeeds, regardless of scheme (TM-2026-005): neither log line, nor any panic message in requirement 96, ever includes embedded credentials.
98. Tokens are looked up and cached by `sha256(token)`; the raw token value itself is never used as a cache key or otherwise retained beyond the lifetime of the request that carried it and the single in-flight upstream call it may trigger.
99. Caching is enabled whenever the effective `CacheTTL` (`opts.CacheTTL`, or 60 seconds when `opts.CacheTTL == 0`) is positive, and disabled (every request reaches the introspection endpoint) only when `opts.CacheTTL` is explicitly negative. When enabled:
    - Only a response with `Active == true` is ever cached; a response with `Active == false` is never stored, so a rejected token is re-introspected on every subsequent request.
    - A cached entry's expiry is `min(now + effective CacheTTL, token's own ExpiresAt)` when the introspection response carries a non-zero `ExpiresAt` earlier than `now + effective CacheTTL`; otherwise it is `now + effective CacheTTL`.
    - When the cache is at its `MaxCacheSize` (or default 10000) capacity and a new token must be inserted, exactly one existing entry — the one with the globally earliest expiry across the whole cache — is evicted first, via a min-heap ordered by expiry giving amortised O(log n) insert and evict (CH-09), replacing an earlier O(n) full-cache scan performed on every cache-full write. This eviction choice guarantees that if any cached entry has already expired, an expired entry is evicted before a live one.
    - A cache hit for a token whose cached `Active` is `false` is treated identically to requirement 102's `invalid_token` case below; a cache hit with `Active == true` injects the cached `*IntrospectResponse` and calls the next handler without contacting the introspection endpoint.
100. Concurrent requests for the identical token (same `sha256(token)`) are coalesced by an in-process, per-`OAuth2Introspect`-instance singleflight group into a single outbound introspection HTTP call: one request becomes the leader and performs the call; every other concurrent request for the same token becomes a follower and waits for either the leader's result or its own request context to be done, whichever comes first. A follower's own cancellation never affects the leader or any other follower. Once it holds leadership, the leader re-checks the cache (in case a different, concurrent leader for the same token already populated it) before making the outbound HTTP call. The leader's outbound HTTP request runs on a context built from `context.WithoutCancel(r.Context())` plus an independently applied 30-second timeout: this means the leader's own request being cancelled (for example, the leader's client disconnecting) never aborts the shared call — every follower still receives a legitimate result — while context values already attached to `r.Context()` (for example, a trace or correlation ID) still propagate to the outbound introspection request (MSR-2026-0071). The independent 30-second timeout, and `opts.HTTPClient`'s own configured timeout, both still bound how long the outbound call can run.
101. The outbound introspection request is an HTTP POST to `opts.Endpoint` with `Content-Type: application/x-www-form-urlencoded` and a body of `token=<token>&token_type_hint=access_token`; when `opts.ClientID` is non-empty, HTTP Basic authentication is set from `opts.ClientID` and `opts.ClientSecret`. The response body is read through a reader limited to 64 KB to bound memory consumption from an oversized response. A non-`200` response status, a transport-level error, or a response body that does not decode as the expected JSON shape are all treated as an error, which produces the same 401 response as requirement 102's `invalid_token` case.
102. Request-time responses:
    - No token could be extracted: 401, `WWW-Authenticate: Bearer realm="api"`.
    - A token was extracted but the introspection result — whether from cache or freshly fetched — has `Active == false`, or fetching or decoding it failed (requirement 101): 401, `WWW-Authenticate: Bearer realm="api", error="invalid_token"`.
    - A token was extracted and the (possibly cached) introspection result has `Active == true`: the `*IntrospectResponse` is injected into the request context, retrievable via `GetOAuth2Claims`, and the next handler is called.
103. `IntrospectResponse` has the exported fields `Active bool`, `Subject string`, `Scope string`, `ClientID string`, `Username string`, `TokenType string`, `ExpiresAt time.Time`, `IssuedAt time.Time`, `NotBefore time.Time`, `Issuer string`, and `Audience []string`, populated from the introspection endpoint's JSON response fields `active`, `sub`, `scope`, `client_id`, `username`, `token_type`, `exp`, `iat`, `nbf`, `iss`, and `aud` (accepted as either a JSON string or a JSON array of strings) respectively. A `time.Time` field is the zero value when the corresponding JSON field was absent or zero.
104. `GetOAuth2Claims(ctx context.Context) (*IntrospectResponse, bool)` returns the value injected by requirement 102, and `false` if no `OAuth2Introspect` middleware produced one for the request.

---

## 21. ThrottlePerIP and ThrottlePerIPCapped

105. `ThrottlePerIP(limit int, timeout time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler` and `ThrottlePerIPCapped(limit int, timeout time.Duration, maxTableSize int, keyFn func(*http.Request) string) func(http.Handler) http.Handler` limit concurrent handler execution per client-supplied key, in contrast to `ThrottleBacklog` (section 11), which limits concurrency globally across all clients combined. `ThrottlePerIP(limit, timeout, keyFn)` is defined as `ThrottlePerIPCapped(limit, timeout, DefaultThrottlePerIPMaxTableSize, keyFn)`, where the exported constant `DefaultThrottlePerIPMaxTableSize` is `100000`.
106. `keyFn` extracts the rate-limit key from the request. If `keyFn` is `nil`, the key is the host part of `r.RemoteAddr` (via `net.SplitHostPort`), and construction emits a one-time `slog.Warn` recommending that `RealIP` (section 5), configured with explicit trusted proxy CIDRs, be registered before `ThrottlePerIP` — otherwise, behind a reverse proxy or load balancer that does not rewrite `RemoteAddr`, every request can appear to originate from the same address, and the per-key limit degrades to a single global limit shared by every client.
107. Both functions panic with `middleware: ThrottlePerIP limit must be > 0` when `limit <= 0`. Neither function validates `timeout`.
108. `maxTableSize` (fixed at `DefaultThrottlePerIPMaxTableSize` for `ThrottlePerIP`, explicit for `ThrottlePerIPCapped`) bounds the number of distinct keys tracked concurrently. A value `<= 0` disables the bound entirely (unbounded growth; not recommended in production). When the table already holds `maxTableSize` distinct keys and an incoming request's key is not among them, that request is rejected immediately with 503 Service Unavailable, without waiting; requests for keys already present in the table continue to be served normally. This exists to bound memory growth under an attack that cycles through a very large or unbounded number of distinct client keys.
109. Internally, the per-key table is partitioned into 64 independent shards, selected by a hash (`hash/maphash`, seeded once per middleware instance with a random seed) of the key; unrelated keys are very likely to land in different shards and therefore contend on independent mutexes rather than a single global one, and a single atomic counter tracks the live entry count across all shards to keep the `maxTableSize` bound exact under concurrent registration and removal of keys. This sharding is an internal scalability mechanism (CH-01) and has no effect on any behavior described in requirements 105-108 and 110-111.
110. For a request whose key is accepted (requirement 108 does not reject it), a concurrency token for that key is requested:
    - If a token is immediately available (fewer than `limit` requests for that key already in flight), the handler runs immediately.
    - Otherwise, the request waits up to `timeout` for a token to become available; if `timeout` elapses first, the middleware responds with 503 Service Unavailable. There is no separate queue-capacity parameter (unlike `ThrottleBacklog`'s `backlog`): any number of requests for the same key beyond `limit` may wait concurrently, each independently governed by `timeout`.
    A token held for a key is released when the handler for that request returns, making it available to the next waiter (if any) for the same key.
111. A key's tracking entry is removed from the table once no request is holding, or waiting for, a token under that key, freeing its slot in the `maxTableSize` bound (requirement 108) for a different key.
