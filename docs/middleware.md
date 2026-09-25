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
  - [JWTAuth](#jwtauth)
  - [OAuth2Introspect](#oauth2introspect)
  - [APIKey](#apikey)
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
mux.Use(middleware.Recoverer())

mux.GET("/api/users", listUsers) // wrapped by Logger and Recoverer
```

---

## Pre-Routing Middleware

`Pre` registers middleware that runs **before** the router matches the request. Use it to rewrite or normalize the URL before the radix tree sees it.

```go
mux.Pre(middleware.CleanPath())
mux.Pre(middleware.StripSlashes())
```

Pre-routing middleware cannot access path parameters because routing has not happened yet. It is useful for path normalization, request ID injection, and real IP extraction.

**Exception — asterisk-form `OPTIONS * HTTP/1.1`:** Under `net/http`'s default server configuration (`http.Server.DisableGeneralOptionsHandler == false`, the default), an incoming `OPTIONS * HTTP/1.1` request is answered by `net/http` itself before `Mux.ServeHTTP` is called, so pre-routing middleware does not run for it. To route these requests through MuxMaster and its middleware, set `http.Server.DisableGeneralOptionsHandler` to `true`:

```go
server := &http.Server{
    Addr:                         ":8080",
    Handler:                      mux,
    DisableGeneralOptionsHandler: true,  // Allow OPTIONS * to reach MuxMaster
}
server.ListenAndServe()
```

When `DisableGeneralOptionsHandler` is `true`, `OPTIONS *` requests reach `Mux.ServeHTTP` with `r.URL.Path == "*"` and pre-routing middleware does run, consistent with all other requests. No route can match the path `*`, so the router then answers through `NotFound` (404 by default): no automatic `Allow` response and no `GlobalOPTIONS` call (see `specification/routing.md` section 10).

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

Logs each request after it completes. Output format: `timestamp method path status duration`. The logged status is always the final HTTP status code; if a handler sends a 1xx informational response (e.g., 103 Early Hints) followed by a final status (e.g., 403), the final status is recorded, not the informational code.

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

**Supported interfaces:**

Logger implements `http.Flusher` (delegating to the underlying response writer) and `io.ReaderFrom` (for `sendfile`/`splice` fast paths). It also exposes `Unwrap() http.ResponseWriter` for tools that use `http.ResponseController`.

---

### Recoverer

Catches panics in downstream handlers and resumes normal request processing. Without this middleware a panic crashes the entire server. The panic value and stack trace are always logged; the panic value itself is never written to the response body.

```go
mux.Use(middleware.Recoverer())
```

**Response behavior:** Recoverer writes a plain 500 response only if the handler has not already committed its own response — that is, only if the handler panicked before calling `WriteHeader` or `Write`. If the handler already sent a status or wrote body bytes before panicking, Recoverer leaves the response exactly as the handler left it and does not append anything; this is a handler bug independent of Recoverer, not something Recoverer can safely correct after the fact.

**Supported interfaces:**

Recoverer implements `http.Flusher` (delegating to the underlying response writer) and exposes `Unwrap() http.ResponseWriter`, so `http.ResponseController` reaches `Hijack` and other optional interfaces on the underlying writer.

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

**Header isolation:**

Header values set by CORS are independent per request. Code downstream that directly indexes into the `Header()` map (e.g., `w.Header()["Key"][0] = ...`) mutates only that request's copy; other requests are unaffected. This is true for every CORS() instance.

**QUERY method and CORS preflight:**

The QUERY method (RFC 10008) is not a CORS-safelisted method. Cross-origin QUERY requests require a preflight OPTIONS request. To support QUERY from browser clients, include `"QUERY"` in `AllowedMethods`:

```go
mux.Use(middleware.CORS(middleware.CORSOptions{
    AllowedOrigins: []string{"https://app.example.com"},
    AllowedMethods: []string{"GET", "POST", "QUERY", "OPTIONS"},
}))
```

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

### JWTAuth

Validates JSON Web Tokens (JWT) from the `Authorization: Bearer <token>` header. The token signature is always verified before claims are parsed to prevent payload manipulation. On success, the validated claims are injected into the request context and available via `GetJWTClaims`.

```go
import (
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// Example with ECDSA (P-256) signing
privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
pubKey := &privKey.PublicKey

mux.Pre(middleware.JWTAuth(middleware.JWTOptions{
    PublicKey:     pubKey,
    Algorithms:    []string{"ES256"},
    RequireExpiry: true,  // RFC 8725 §4.4: reject tokens without "exp"
    Issuers:       []string{"https://auth.example.com"},
    Audiences:     []string{"api"},
}))

// In a handler, extract the claims:
func myHandler(w http.ResponseWriter, r *http.Request) {
    claims, ok := middleware.GetJWTClaims(r.Context())
    if !ok {
        http.Error(w, "claims not found", http.StatusInternalServerError)
        return
    }
    fmt.Fprintf(w, "User: %s\n", claims.Subject)
}
```

**`JWTOptions` fields:**

| Field              | Type       | Description |
|---|---|---|
| `Secret`           | `[]byte`   | HMAC signing key; required for HS256, HS384, HS512 |
| `PublicKey`        | `crypto.PublicKey` | RSA or ECDSA public key; required for RS*/ES* algorithms |
| `Algorithms`       | `[]string` | Accepted signing algorithms (required, non-empty). Supported: HS256, HS384, HS512, RS256, RS384, RS512, ES256, ES384, ES512 |
| `Issuers`          | `[]string` | If non-empty, restricts accepted "iss" claim values |
| `Audiences`        | `[]string` | If non-empty, requires at least one "aud" entry to match |
| `ClockSkew`        | `time.Duration` | Permitted clock drift for "exp" and "nbf" checks; default: 0 |
| `RequireExpiry`    | `bool`     | When true, rejects tokens without an "exp" claim; default: **false** (unsafe — production MUST set to true) |

**Claims returned by `GetJWTClaims`:**

```go
type JWTClaims struct {
    Subject    string    // "sub" claim
    Issuer     string    // "iss" claim
    Audience   []string  // "aud" claim (may be single or array in JWT)
    ExpiresAt  time.Time // "exp" claim, or zero if absent
    IssuedAt   time.Time // "iat" claim, or zero if absent
    NotBefore  time.Time // "nbf" claim, or zero if absent
    RawPayload []byte    // Raw decoded JSON payload for extracting custom claims
}
```

**Security Considerations:**

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. The `Use()` family does not wrap fast routes and will panic if both are present. See [Pre vs. Use security boundary](../SECURITY.md#thread-safety-contract-mm-2026-0017--csa-2026-0052) in SECURITY.md.

- **Algorithm mixing (timing oracle — TSC-2026-0003):** Mixing algorithm families (e.g., HS256 alongside RS256) in `Algorithms` leaks the verification path via response latency: HMAC verification is ~1 µs, RSA ~300 µs. An attacker submitting tokens with different `alg` values can infer which path the server runs. Configure each endpoint with a single algorithm family (e.g., only `ES256`, not a mix). JWTAuth emits a `slog.Warn` at construction time when this misconfiguration is detected.

- **Require expiry (RFC 8725 §4.4 — TM-2026-001):** The default `RequireExpiry: false` is unsafe in production. A stolen token without an `"exp"` claim remains valid indefinitely. Production deployments **must** set `RequireExpiry: true`. JWTAuth emits a `slog.Warn` at construction time when this default is in effect.

- **Critical extensions rejected:** Tokens with a `"crit"` field in the header (RFC 7515 §4.1.11) are rejected, as MuxMaster does not support custom critical extensions.

- **Negative timestamps rejected (TM-2026-002):** Any negative value in `"exp"`, `"nbf"`, or `"iat"` claims is rejected as malformed per RFC 7519 §2.

---

### OAuth2Introspect

Validates Bearer tokens via RFC 7662 token introspection against a remote authorization server. Active tokens are cached (keyed by SHA-256 hash of the token) to avoid per-request network calls. Concurrent requests for the same token are coalesced via singleflight to prevent cache-stampede attacks against the introspection endpoint.

```go
import (
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

mux.Pre(middleware.OAuth2Introspect(middleware.OAuth2Options{
    Endpoint:     "https://auth.example.com/oauth2/introspect",
    ClientID:     "my-service",
    ClientSecret: "secret",
    CacheTTL:     60 * time.Second,
    MaxCacheSize: 10000,
}))

// In a handler, extract the introspection response:
func myHandler(w http.ResponseWriter, r *http.Request) {
    resp, ok := middleware.GetOAuth2Claims(r.Context())
    if !ok {
        http.Error(w, "introspection response not found", http.StatusInternalServerError)
        return
    }
    fmt.Fprintf(w, "Subject: %s\nScope: %s\n", resp.Subject, resp.Scope)
}
```

**`OAuth2Options` fields:**

| Field                   | Type              | Description |
|---|---|---|
| `Endpoint`              | `string`          | RFC 7662 introspection URL (required); must be HTTPS unless `AllowInsecureEndpoint: true` |
| `ClientID`              | `string`          | Username for HTTP Basic authentication (optional) |
| `ClientSecret`          | `string`          | Password for HTTP Basic authentication (optional) |
| `AllowInsecureEndpoint` | `bool`            | Allow non-HTTPS endpoint; default: **false**. Set to true ONLY for testing on localhost. A slog warning is emitted at construction time when true |
| `CacheTTL`              | `time.Duration`   | How long active tokens are cached; default: 60 seconds. Set to a negative value (e.g., -1) to disable caching entirely — every request hits the endpoint. Effective TTL is `min(CacheTTL, token.exp - now)` |
| `MaxCacheSize`          | `int`             | Maximum number of cached tokens; default: 10000. When full, expired tokens are evicted first; if none are expired, the entry with the soonest expiry is evicted |
| `HTTPClient`            | `*http.Client`    | HTTP client for introspection requests; default: 10-second timeout. Override to use a custom certificate or proxy |
| `ExtractFn`             | `func(*http.Request) string` | Custom token extraction function; default: `Authorization: Bearer <token>` |

**Introspection response (`IntrospectResponse`) fields:**

```go
type IntrospectResponse struct {
    Active    bool      // RFC 7662: whether the token is active
    Subject   string    // "sub" claim
    Scope     string    // Space-separated scopes
    ClientID  string    // "client_id" claim
    Username  string    // "username" claim
    TokenType string    // "token_type" claim
    ExpiresAt time.Time // "exp" claim, or zero if absent
    IssuedAt  time.Time // "iat" claim, or zero if absent
    NotBefore time.Time // "nbf" claim, or zero if absent
    Issuer    string    // "iss" claim
    Audience  []string  // "aud" claim (may be single or array)
}
```

**Security Considerations:**

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. See [Pre vs. Use security boundary](../SECURITY.md#thread-safety-contract-mm-2026-0017--csa-2026-0052) in SECURITY.md.

- **HTTPS endpoint required (RFC 7662 §4 — MSR-2026-0067):** Bearer tokens transmitted over plaintext are exposed to passive observers and man-in-the-middle attackers. The `Endpoint` must use the `https://` scheme. MuxMaster panics at construction time unless `AllowInsecureEndpoint: true` is explicitly set (testing/localhost only). Production deployments must use HTTPS.

- **Endpoint URL validation (TM-2026-004):** The `Endpoint` URL is validated to ensure it has a non-empty host and contains no embedded userinfo (which could exfiltrate credentials). Misconfigured endpoints are detected at construction time.

- **Credential logging mitigation (TM-2026-005):** Construction-time logs emit only the host and scheme of the endpoint, never the full URL, to prevent credentials embedded in query strings from being recorded.

- **Cache poisoning (MSR-2026-0063):** Because tokens are cached, a revoked token remains valid until the TTL expires. High-security endpoints should disable caching by setting `CacheTTL` to a negative value (e.g., `-1`). The singleflight mechanism still coalesces concurrent calls for the same token, preventing IDP load spikes.

- **Singleflight defense (DOS-OAUTH2-001):** Concurrent requests for the same token share a single upstream introspection call. If the leader's request context is cancelled, the call detaches to a 30-second background timeout so followers receive the legitimate result instead of being poisoned with a 401.

---

### APIKey

Authenticates requests by matching an extracted API key against a pre-validated set. All keys are hashed at construction time; per-request overhead is one SHA-256 hash plus a lookup.

```go
import (
    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

keys := map[string]string{
    "sk_live_abc123": "service-a",
    "sk_live_def456": "service-b",
}

mux.Pre(middleware.APIKey(middleware.APIKeyOptions{
    Keys:   keys,
    Header: "X-API-Key",  // default header name
}))

// In a handler, extract the identity:
func myHandler(w http.ResponseWriter, r *http.Request) {
    identity, ok := middleware.GetAPIKeyIdentity(r.Context())
    if !ok {
        http.Error(w, "identity not found", http.StatusInternalServerError)
        return
    }
    fmt.Fprintf(w, "Request from: %s\n", identity)
}
```

**`APIKeyOptions` fields:**

| Field      | Type                        | Description |
|---|---|---|
| `Keys`     | `map[string]string`         | Map of raw API key → identity string; must be non-empty (panics otherwise) |
| `Header`   | `string`                    | Request header to read; default: "X-API-Key" |
| `ExtractFn` | `func(*http.Request) string` | Custom key extraction function; overrides `Header` if set |

**Security Considerations:**

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. See [Pre vs. Use security boundary](../SECURITY.md#thread-safety-contract-mm-2026-0017--csa-2026-0052) in SECURITY.md.

- **WWW-Authenticate header (RFC 7235 §3.1 — MM-2026-0052):** MuxMaster sets the `WWW-Authenticate: ApiKey realm="api"` header on 401 responses to comply with the HTTP specification.

- **Timing oracle mitigation (TSC-2026-0008):** To avoid leaking whether the API key was found via response latency, the hit path (valid key) performs an equivalent header operation (set + delete) as the miss paths, equalising the cost of both branches. This prevents attackers from distinguishing valid keys from invalid ones by measuring response time.

- **Pre-hashing:** All keys are SHA-256 hashed at construction time. Per-request overhead is one SHA-256 hash of the submitted key plus a constant-time map lookup.

---

### Compress

Compresses responses using gzip or deflate, depending on the `Accept-Encoding` header.

```go
mux.Use(middleware.Compress(5)) // compression level 1–9; 5 is a good default
```

Responses smaller than a threshold are not compressed. The `Content-Encoding: gzip` header is set automatically.

The middleware applies a "first-WriteHeader wins" lock to prevent multiple calls from changing the status code once compression has begun. To match `net/http`'s own behaviour, 1xx informational responses (e.g., 103 Early Hints) are exempt from this lock and do not block subsequent final status codes.

**Supported interfaces:**

Compress implements `http.Flusher` (delegating to the underlying gzip writer) and `Unwrap() http.ResponseWriter` for tools that use `http.ResponseController`.

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

Attaches a unique request ID to every request, generating a 16-byte random value encoded as a 32-character lowercase hexadecimal identifier, or validating an inbound one. The ID is stored in the request context and written to the response header.

```go
mux.Use(middleware.RequestID())
```

**Header behavior:**

- **Inbound:** If the incoming request has an `X-Request-ID` header, it is validated (MM-2026-0011): ASCII alphanumeric plus `-`, `_`, `.`; length 1–128 characters. Invalid or empty values are replaced with a freshly generated ID.
- **Outbound:** The request ID is written to the `X-Request-ID` response header.

**Reading the request ID in a handler:**

```go
id := middleware.GetRequestID(r.Context())
```

**Performance:**

- **Allocation budget:** Exactly 2 allocations per request — one fused allocation for the context node + hex-encoded ID buffer + response header backing array, and one for `r.WithContext()`'s copy of `*http.Request`.

---

### RealIP

Extracts the real client IP address from `X-Forwarded-For` or `X-Real-IP` headers set by a reverse proxy, and sets `r.RemoteAddr` to that value.

```go
trustedProxy := netip.MustParsePrefix("10.0.0.0/8")
mux.Use(middleware.RealIP(&trustedProxy))
```

For `X-Forwarded-For` (a comma-separated list of IPs in proxy chain order), RealIP searches from right-to-left for the rightmost untrusted proxy in the chain. It respects a 30-hop limit to defend against unbounded list sizes.

**Security:**

Only use this middleware if the server is behind a trusted reverse proxy. Accepting these headers from arbitrary clients is a security risk — the client can spoof `X-Forwarded-For` to claim any IP address. If the proxy chain is compromised, RealIP will assign the IP address that an attacker inserted into the rightmost position.

---

### CleanPath

Redirects URLs with redundant components to their canonical form:
- `//users` → `/users`
- `/a/../users` → `/users`
- `/a/./users` → `/a/users`

```go
mux.Pre(middleware.CleanPath()) // run before routing to avoid a redirect
```

---

### StripSlashes

Removes trailing slashes from the URL path before routing. Unlike `RedirectTrailingSlash`, this modifies the request in-place without issuing a redirect.

```go
mux.Pre(middleware.StripSlashes())
```

---

### NoCache

Sets headers that instruct browsers and intermediaries not to cache the response.

```go
mux.Use(middleware.NoCache())
```

Headers set: `Cache-Control: no-cache, no-store, no-transform, must-revalidate, private, max-age=0`, `Pragma: no-cache`, `Expires: 0`.

**Header isolation:**

Each request gets its own independent copy of the cache-control headers. Code downstream that directly indexes into the `Header()` map (e.g., `w.Header()["Cache-Control"][0] = ...`) mutates only that request's copy; other requests retain the original no-cache headers.

---

### SetHeader

Sets a fixed response header for every request:

```go
mux.Use(middleware.SetHeader("X-Content-Type-Options", "nosniff"))
mux.Use(middleware.SetHeader("X-Frame-Options", "DENY"))
mux.Use(middleware.SetHeader("Strict-Transport-Security", "max-age=31536000"))
```

**Header isolation:**

Each request gets its own independent copy of the header value. Code downstream that directly indexes into the `Header()` map (e.g., `w.Header()["X-Custom"][0] = ...`) mutates only that request's copy; other requests are unaffected.

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
