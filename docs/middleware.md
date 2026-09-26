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
  - [RecovererWithLogger](#recovererwithlogger)
  - [CORS](#cors)
  - [BasicAuth](#basicauth)
  - [JWTAuth](#jwtauth)
  - [OAuth2Introspect](#oauth2introspect)
  - [APIKey](#apikey)
  - [Compress](#compress)
  - [ThrottleBacklog and ThrottleAllBacklog](#throttlebacklog-and-throttleallbacklog)
  - [ThrottlePerIP and ThrottlePerIPCapped](#throttleperip-and-throttleperipcapped)
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

The router's own responses — the `NotFound` and `MethodNotAllowed` handlers, the automatic OPTIONS response and the trailing-slash and fixed-path redirects — are wrapped with the complete `Use` chain at the time they run, including middleware added after they were first used.

### Which middleware wraps which route type

| Registered with | Wraps `Handle` routes | Wraps `HandleFast` routes |
|---|---|---|
| `mux.Pre(...)` | Yes | Yes |
| `mux.Use(...)` / `group.Use(...)` (`func(http.Handler) http.Handler`) | Yes | No — registering a `HandleFast` route after `Use` panics |
| `mux.UseFast(...)` / `group.UseFast(...)` (`FastMiddleware`) | No | Yes |

An authentication gate that must also cover `HandleFast` routes must be registered with `Pre`. See [SECURITY.md](../SECURITY.md#pre-vs-use-security-boundary-csa-2026-0059--h8-01).

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
mux.Use(middleware.RecovererWithLogger(slog.Default()))

mux.GET("/api/users", listUsers) // wrapped by Logger and RecovererWithLogger
```

---

## Pre-Routing Middleware

`Pre` registers middleware that runs **before** the router matches the request. Use it to rewrite or normalize the URL before the radix tree sees it.

```go
mux.Pre(middleware.CleanPath())
mux.Pre(middleware.StripSlashes())
```

`Pre` middleware wraps the whole dispatch, so it runs for every request — `Handle` routes, `HandleFast` routes, 404, 405, automatic OPTIONS and redirects. It cannot access path parameters or `RoutePattern`, because routing has not happened yet. It is the right place for path normalisation, request IDs, real IP extraction, panic recovery and authentication gates that must cover fast routes. Each `Pre` call appends to the chain; the first registered is outermost.

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

Middleware registered on a group applies only to the routes in that group; mux-level `Use` middleware wraps it, so it runs first:

```go
mux := muxmaster.New()
mux.Use(middleware.Logger(os.Stdout)) // runs for all routes

api := mux.Group("/api/v1")
api.Use(requireAPIKey)  // runs only for routes in /api/v1

api.GET("/users", listUsers) // Logger → requireAPIKey → listUsers
mux.GET("/health", health)     // Logger → health (no requireAPIKey)
```

A group that has `Use` middleware panics when you register a `HandleFast` route on it; use `group.UseFast` for fast routes.

---

## Per-Route Middleware with `With`

`With` returns a new `*Group` that adds middleware to the routes you register through it. `mux.With(...)` returns a group with an empty prefix; `group.With(...)` keeps the group's prefix and middleware:

```go
// On the mux
mux.With(requireAdmin).DELETE("/users/:id", deleteUser)

// On a group
api.With(rateLimit, auditLog).POST("/payments", processPayment)
```

`With` does not modify the original mux or group.

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
type ctxKey struct{}

var userIDKey ctxKey

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

Alternatively, use `middleware.WithValue` for simple cases, again with an unexported key type:

```go
type envKey struct{}

mux.Use(middleware.WithValue(envKey{}, "production"))

// In a handler:
env := r.Context().Value(envKey{}).(string)
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

### RecovererWithLogger

Recovers panics in downstream handlers, logs the panic value and stack trace at Error level through the given `*slog.Logger`, and answers with a plain `500 Internal Server Error`. The panic value is never written to the response body. Without recovery, `net/http` recovers the panic per connection, logs it and closes the connection.

```go
mux.Use(middleware.RecovererWithLogger(slog.Default()))
```

Register it with `Pre` instead of `Use` to cover `HandleFast` routes and the other `Pre` middleware registered after it. `middleware.Recoverer()` is deprecated; it is equivalent to `RecovererWithLogger(slog.Default())`.

**Response behaviour:** it writes a plain 500 response only if the handler has not already committed its own response — that is, only if the handler panicked before calling `WriteHeader` or `Write`. If the handler already sent a status or wrote body bytes before panicking, Recoverer leaves the response exactly as the handler left it and does not append anything; this is a handler bug independent of Recoverer, not something Recoverer can safely correct after the fact.

**Supported interfaces:**

`RecovererWithLogger` implements `http.Flusher` (delegating to the underlying response writer) and exposes `Unwrap() http.ResponseWriter`, so `http.ResponseController` reaches `Hijack` and other optional interfaces on the underlying writer.

---

### CORS

Handles Cross-Origin Resource Sharing. It answers every `OPTIONS` request that carries an `Origin` header as a preflight (`204 No Content`, without calling the next handler) and sets the CORS headers on other responses.

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

**Behaviour:**

- `Vary: Origin` is added to **every** response that passes through CORS, including requests without an `Origin` header and CORS's own error responses, so shared caches never reuse a response across origins. It is added, not set, so other `Vary` values are kept.
- A request without an `Origin` header is passed to the next handler unchanged apart from `Vary`.
- An `Origin` containing CR, LF or NUL receives `400 Bad Request`; an origin not in `AllowedOrigins` receives `403 Forbidden`.
- With `AllowedOrigins: []string{"*"}` the response carries the literal `Access-Control-Allow-Origin: *`; the request origin is never reflected.
- Construction panics if `AllowedOrigins` is empty, or if `AllowCredentials` is `true` while `AllowedOrigins` contains `"*"`.
- A `SetHeader` middleware that runs after CORS and sets an `Access-Control-*` header overwrites CORS's value; see [SetHeader](#setheader).

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
- `credentials map[string]string` — map of username → password; panics if `nil`

A request without valid credentials receives `401 Unauthorized` with `WWW-Authenticate: Basic realm="<realm>"`.

**Constant-time lookup:** usernames and passwords are SHA-256 hashed at construction. Every request compares the supplied credentials against **all** registered users with `crypto/subtle`, with no early exit, so response time does not reveal whether a username exists (TSC-2026-0002). The cost therefore grows with the number of users. On the 2026-09-26 benchmark run (AMD Ryzen 9 5900HX), a successful request took about 320 ns, 550 ns and 2.8 µs with 1, 10 and 100 users, and a rejected one about 730 ns, 960 ns and 3.2 µs ([Performance](performance.md#middleware-middlewarebench_testgo)). For large user bases, use a credential store designed for it.

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

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. The `Use()` family does not wrap fast routes and will panic if both are present. See [Pre vs. Use security boundary](../SECURITY.md#pre-vs-use-security-boundary-csa-2026-0059--h8-01) in SECURITY.md.

- **Supported algorithms only:** `Algorithms` accepts HS256, HS384, HS512, RS256, RS384, RS512, ES256, ES384 and ES512. Any other value, including `none`, panics at construction, as does a listed algorithm whose key material (`Secret` or a `PublicKey` of the right type and curve) is missing. Tokens whose `alg` is not listed are rejected with 401.

- **Algorithm mixing (timing oracle — TSC-2026-0003):** Mixing algorithm families (e.g., HS256 alongside RS256) in `Algorithms` leaks the verification path via response latency: the measured HS256 and RS256 paths differ by about 25 µs. An attacker submitting tokens with different `alg` values can infer which path the server runs. Configure each endpoint with a single algorithm family (e.g., only `ES256`, not a mix). JWTAuth emits a `slog.Warn` at construction time when this misconfiguration is detected.

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

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. See [Pre vs. Use security boundary](../SECURITY.md#pre-vs-use-security-boundary-csa-2026-0059--h8-01) in SECURITY.md.

- **HTTPS endpoint required (RFC 7662 §4 — MSR-2026-0067):** Bearer tokens transmitted over plaintext are exposed to passive observers and man-in-the-middle attackers. The `Endpoint` must use the `https://` scheme. MuxMaster panics at construction time unless `AllowInsecureEndpoint: true` is explicitly set (testing/localhost only). Production deployments must use HTTPS.

- **Endpoint URL validation (TM-2026-004):** The `Endpoint` URL is validated to ensure it has a non-empty host and contains no embedded userinfo (which could exfiltrate credentials). Misconfigured endpoints are detected at construction time.

- **Credential redaction (TM-2026-005, CWE-532):** Construction-time log lines and panic messages never render credentials embedded in the `Endpoint` URL.

- **Cache poisoning (MSR-2026-0063):** Because tokens are cached, a revoked token remains valid until the TTL expires. High-security endpoints should disable caching by setting `CacheTTL` to a negative value (e.g., `-1`). The singleflight mechanism still coalesces concurrent calls for the same token, preventing IDP load spikes.

- **Singleflight defense (DOS-OAUTH2-001, MSR-2026-0071):** Concurrent requests for the same token share a single upstream introspection call. The call runs on a context detached from the leader's cancellation (`context.WithoutCancel`) with a 30-second timeout, so a cancelled leader does not fail its followers with a 401; request-scoped context values are kept.

---

### APIKey

Authenticates requests by matching an extracted API key against a configured set. All keys are SHA-256 hashed at construction time; per-request overhead is one SHA-256 hash plus one map lookup keyed by that hash. A missing or unknown key receives `401 Unauthorized`.

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

- **Pre-routing placement (Auth gates):** If this middleware must cover routes registered with `HandleFast`, register it via `mux.Pre(...)`, not `mux.Use(...)`. See [Pre vs. Use security boundary](../SECURITY.md#pre-vs-use-security-boundary-csa-2026-0059--h8-01) in SECURITY.md.

- **WWW-Authenticate header (RFC 7235 §3.1 — MM-2026-0052):** MuxMaster sets the `WWW-Authenticate: ApiKey realm="api"` header on 401 responses to comply with the HTTP specification.

- **Timing oracle mitigation (TSC-2026-0008):** To avoid leaking whether the API key was found via response latency, the hit path (valid key) performs an equivalent header operation (set + delete) as the miss paths, equalising the cost of both branches. This prevents attackers from distinguishing valid keys from invalid ones by measuring response time.

- **Pre-hashing:** All keys are SHA-256 hashed at construction time, and the submitted key is hashed before the map lookup, so the lookup never compares the raw key bytes.

---

### Compress

Compresses responses with gzip when the request's `Accept-Encoding` header contains `gzip`. No other encoding is supported.

```go
mux.Use(middleware.Compress(5)) // a compress/gzip level; an invalid level panics
```

Responses smaller than 1024 bytes are sent uncompressed, as are responses whose `Content-Type` is already compressed or that already carry a `Content-Encoding`. `Vary: Accept-Encoding` is always added; `Content-Encoding: gzip` is set when the body is compressed. While deciding, the middleware buffers up to 8 KiB per response, so configure `http.Server` timeouts. Do not compress responses that echo user input next to a secret (BREACH); see [SECURITY.md](../SECURITY.md).

The middleware applies a "first-WriteHeader wins" lock to prevent multiple calls from changing the status code once compression has begun. To match `net/http`'s own behaviour, 1xx informational responses (e.g., 103 Early Hints) are exempt from this lock and do not block subsequent final status codes.

**Supported interfaces:**

Compress implements `http.Flusher` (delegating to the underlying gzip writer) and `Unwrap() http.ResponseWriter` for tools that use `http.ResponseController`.

---

### ThrottleBacklog and ThrottleAllBacklog

Limits the number of concurrently executing handlers across all clients combined. Requests over the limit wait in a backlog; a request that finds the backlog full, or waits longer than the timeout, receives `503 Service Unavailable`. `ThrottleAllBacklog` is an equivalent name for `ThrottleBacklog`.

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

### ThrottlePerIP and ThrottlePerIPCapped

Limits the number of concurrently executing handlers **per client key**. A request that cannot get a slot for its key within the timeout receives `503 Service Unavailable`.

```go
trustedProxy := netip.MustParsePrefix("10.0.0.0/8")
mux.Use(middleware.RealIP(&trustedProxy))                 // first: set RemoteAddr
mux.Use(middleware.ThrottlePerIP(10, time.Second, nil))   // 10 concurrent requests per IP
```

**Parameters:**
- `limit int` — maximum concurrent requests per key; must be > 0
- `timeout time.Duration` — how long a request waits for a slot
- `keyFn func(*http.Request) string` — extracts the key; `nil` uses the host part of `r.RemoteAddr`

With `keyFn == nil`, register `RealIP` (with explicit trusted proxy CIDRs) before `ThrottlePerIP`; otherwise every request behind a load balancer shares the balancer's address and the limit becomes global. `ThrottlePerIP` logs a reminder at construction when `keyFn` is `nil`.

The table of tracked keys is capped at `DefaultThrottlePerIPMaxTableSize` (100 000). When it is full, requests for keys not already in the table receive 503 immediately, which bounds memory under IP churn. `ThrottlePerIPCapped(limit, timeout, maxTableSize, keyFn)` sets a different cap; `maxTableSize <= 0` removes it, which is not recommended in production.

---

### Timeout

Sets a deadline on the request context. It does not interrupt the handler or write a response when the deadline passes: the handler must watch `ctx.Done()` (and use context-aware calls such as `QueryContext`) and write its own error response. Panics if the duration is not positive.

```go
mux.Use(middleware.Timeout(10 * time.Second))
```

The deadline covers the handlers wrapped by `Timeout`, not the connection. Use `http.Server` timeouts for the connection.

---

### RequestID

Attaches a unique request ID to every request, generating a 16-byte random value encoded as a 32-character lowercase hexadecimal identifier, or validating an inbound one. The ID is stored in the request context and written to the response header.

```go
mux.Use(middleware.RequestID())
```

**Header behaviour:**

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

Extracts the real client IP address from the `X-Forwarded-For` header (or, when it is absent, `X-Real-IP`) set by a reverse proxy, and sets `r.RemoteAddr` to that value. It does so only when the direct peer's address lies in one of the trusted CIDR prefixes passed as `*netip.Prefix`; values that are not valid IP addresses are ignored.

```go
trustedProxy := netip.MustParsePrefix("10.0.0.0/8")
mux.Use(middleware.RealIP(&trustedProxy))
```

For `X-Forwarded-For` (a comma-separated list of IPs in proxy chain order), RealIP searches from right-to-left for the rightmost untrusted proxy in the chain. It respects a 30-hop limit to defend against unbounded list sizes.

**Security:**

Only use this middleware if the server is behind a trusted reverse proxy, and always pass the proxy CIDRs. Called with no CIDRs, `RealIP()` trusts every peer, so any client can spoof its address; it logs a warning at construction. Every proxy in the chain must be covered by a trusted CIDR, or the walk stops at the uncovered proxy. If the proxy chain is compromised, RealIP assigns whatever address the attacker inserted.

---

### CleanPath

Normalises the request path with `path.Clean` before routing. When the path changes, the next handler receives a shallow copy of the request with the cleaned path; the original request is not modified:
- `//users` → `/users`
- `/a/../users` → `/users`
- `/a/./users` → `/a/users`

```go
mux.Pre(middleware.CleanPath()) // run before routing to avoid a redirect
```

**Ordering with authorization gates:** When used with path-inspecting Pre-gates
(e.g., gates that check `if strings.HasPrefix(r.URL.Path, "/admin")`), register
CleanPath FIRST. A gate registered before CleanPath sees the raw, unnormalised path
and can be bypassed by traversal sequences like `/admin/../public` or `//admin`.
CleanPath must run first to normalise the path before the gate inspects it:

```go
func adminGate(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/admin") && !isAdmin(r) {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}

mux.Pre(middleware.CleanPath()) // first: normalise the path
mux.Pre(adminGate)              // then: check authorisation on the clean path
```

If this order is reversed, `//admin` reaches the gate unnormalised, does not
match the `/admin` prefix and is let through, and CleanPath then hands the router
the cleaned `/admin` path.

When `r.URL.RawPath` is set it is cleaned too, and it is cleared if it no longer
matches the cleaned `Path`, so an encoded traversal cannot survive in `RawPath`.

---

### StripSlashes

Removes all trailing slashes from the URL path before routing (`/a///` becomes `/a`), and strips the matching separators from `r.URL.RawPath` when it is set. Unlike `RedirectTrailingSlash`, it issues no redirect: the next handler receives a shallow copy of the request with the stripped path, and the original request is not modified.

```go
mux.Pre(middleware.StripSlashes())
```

---

### NoCache

Sets headers that instruct browsers, CDNs and reverse proxies not to cache the response. It sets them before calling the next handler, which can still override them.

```go
mux.Use(middleware.NoCache())
```

Headers set: `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Expires: 0`, `Surrogate-Control: no-store`, `X-Accel-Expires: 0`.

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

The header is set before the next handler runs, so later middleware and the handler can override it. Construction panics if the key or value contains CR or LF.

**Ordering with CORS:** a `SetHeader` registered after `CORS` in the same chain runs later and overwrites CORS's values; `Use(CORS(...), SetHeader("Access-Control-Allow-Origin", "*"))` defeats the origin allow-list. Register `SetHeader` before `CORS`, or do not use it for `Access-Control-*` or `Vary`.

**Header isolation:**

Each request gets its own independent copy of the header value. Code downstream that directly indexes into the `Header()` map (e.g., `w.Header()["X-Custom"][0] = ...`) mutates only that request's copy; other requests are unaffected.

---

### WithValue

Stores a value in the request context. Useful for injecting configuration or feature flags. Use an unexported key type: a string key can collide with another package's, and `WithValue` logs a warning at construction when given one. A `nil` key panics.

```go
type appEnvKey struct{}

mux.Use(middleware.WithValue(appEnvKey{}, "production"))

// In a handler:
env := r.Context().Value(appEnvKey{}).(string)
```

---

## See Also

- [Groups](groups.md) — applying middleware to a subset of routes
- [Error Handling](error-handling.md) — error-returning handlers
- [Cookbook](cookbook.md) — middleware composition patterns
