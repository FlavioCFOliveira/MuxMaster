# Security Policy

## Supported Versions

Only the latest release receives security fixes. Older versions are not maintained.

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report security issues to **flaviocfo@gmail.com** with:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a minimal proof-of-concept
- Any suggested mitigations you are aware of

You will receive an acknowledgement within 72 hours. We aim to release a fix
within 14 days for critical issues and 30 days for others. We will credit you
in the release notes unless you prefer to remain anonymous.

## Thread-Safety Contract (MM-2026-0017 / CSA-2026-0052)

All public `Mux` fields (`PanicHandler`, `NotFound`, `MethodNotAllowed`,
`GlobalOPTIONS`, `ErrorHandler`, `RedirectTrailingSlash`, `RedirectFixedPath`,
`CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode`,
`HandleMethodNotAllowed`, `HandleOPTIONS`) **must be set before the first
call to `ServeHTTP`**. On the first request these values are atomically
captured into a frozen `muxConfig` snapshot and every subsequent dispatch
reads from that snapshot — direct field mutation after serving begins is
ignored by the dispatch path and races with the snapshot's first read.

To reconfigure handlers after serving starts, mutate the field and then call
`Mux.Rebuild()`. `Rebuild()` atomically resets the snapshot and the lazy
NotFound/405/OPTIONS handler caches so the next request re-reads every
field. `Rebuild()` is safe to call concurrently with `ServeHTTP`.

`Use()` and `Pre()` are safe to call concurrently with `Handle()` during
route registration (before serving), but must not be called concurrently with
active requests.

## Timeout Middleware (MM-2026-0019)

`middleware.Timeout` cancels the request context after the configured duration.
**Handlers must actively check `ctx.Done()`** (or use context-aware I/O) to be
preempted. Handlers that ignore the context will continue running until they
return, regardless of the timeout — goroutine exhaustion is possible if handlers
block indefinitely.

Example of a cooperative handler:

```go
func myHandler(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    select {
    case result := <-doWork(ctx):
        w.Write(result)
    case <-ctx.Done():
        http.Error(w, "request timeout", http.StatusGatewayTimeout)
    }
}
```

## Slowloris / Server Timeouts (MM-2026-0024)

MuxMaster is an `http.Handler` and does not configure the underlying
`http.Server`. To mitigate Slowloris and similar attacks, always set timeouts
on your server:

```go
srv := &http.Server{
    Handler:           r,
    ReadHeaderTimeout: 30 * time.Second,
    ReadTimeout:       60 * time.Second,
    WriteTimeout:      60 * time.Second,
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20, // 1 MiB
}
```

## Accepted Known Limitations

### Route-Existence Timing Oracle (MM-2026-0026)

The timing difference between a 404 response (path not in tree) and a 405
response (path exists, wrong method) is ~440 ns. This is intrinsic to radix
tree lookup and is present in httprouter, chi, and bunrouter as well. If this
is a concern, use a WAF or add uniform response delays via middleware.

### Path normalisation accepted behaviour (PRF-2026-0001..0005)

The following routing behaviours are intentional and documented as operator
responsibilities — they are not router defects:

- **`%61dmin` matches `/admin`** when `UseRawPath=false` (the default).
  `net/http` decodes `%61` to `a` during URL parsing per RFC 3986 §6.2.2.2,
  so the router sees `/admin`. To enforce byte-exact path matching set
  `r.UseRawPath = true` — patterns then match against `r.URL.RawPath`,
  which preserves the percent-encoded form (PRF-2026-0002).

- **Catch-all `*filepath` parameters carry raw bytes**, including any
  `..` traversal sequences. The router does NOT sanitise catch-all values
  — that is the boundary between router and storage backend. Handlers
  serving files MUST call `path.Clean` and must reject paths that escape
  their root (e.g. via `filepath.IsLocal` or by checking
  `filepath.Rel(root, joined)`). `Mux.ServeFiles` already delegates to
  `http.FileServer` which performs path cleaning (PRF-2026-0005).

- **Static routes must be registered before sibling wildcards.** Calling
  `r.GET("/users/:id", h)` and then `r.GET("/users/active", h)` panics
  because `:id` already claims the wildcard slot at that depth. Register
  the more-specific static route first; this matches httprouter's
  behaviour (PRF-2026-0003).

- **`RedirectFixedPath=true` discloses route existence via the redirect
  status.** The default is `false` precisely because path cleaning before
  dispatch can convert non-existent paths into observable hits. Leave the
  default unless you understand the disclosure trade-off (PRF-2026-0004).

- **`middleware.CleanPath` (registered via `Pre`) normalises dot segments
  before dispatch.** It does NOT bypass authentication — middleware
  registered via `Use` still wraps the dispatched handler — but it can
  cause requests to land on a different handler than the raw path
  suggests. CleanPath is intended for clients that emit `/foo/../bar`
  and similar; do not use it on endpoints where the literal path is
  semantically meaningful (PRF-2026-0001).

### RealIP misconfiguration (MSR-2026-0055)

`middleware.RealIP()` called with no trusted-proxy CIDR list trusts every
peer — any client can spoof `X-Forwarded-For` / `X-Real-IP` and the router
will accept it as the real client IP. This is only safe behind a single
trusted proxy that strips inbound XFF; in any other deployment it is a
trivial spoofing primitive that defeats `ThrottlePerIP` and IP-based
access controls. Always pass the proxy CIDR list explicitly:

```go
proxyCIDR := netip.MustParsePrefix("10.0.0.0/8")
r.Use(middleware.RealIP(&proxyCIDR))
```

A `slog.Warn` is emitted at construction time when `RealIP()` is called
without CIDRs.

### RealIP + ThrottlePerIP ordering (DOS-2026-0002)

`middleware.ThrottlePerIP` with a nil `keyFn` keys on `r.RemoteAddr`. If
`RealIP` is not registered (or is registered AFTER `ThrottlePerIP`), every
request behind a reverse proxy carries the LB's own address as
`RemoteAddr` and the per-IP limit collapses to a global rate limit. Always
register `RealIP` first so `r.RemoteAddr` reflects the true client IP
before throttling decisions are made:

```go
r.Use(middleware.RealIP(&proxyCIDR))           // first
r.Use(middleware.ThrottlePerIP(50, ts, nil))   // then
```

A `slog.Warn` is emitted at construction time when `ThrottlePerIP` is
called with a nil keyFn.

### JWT Mixed-Family Algorithms (TSC-2026-0003)

`JWTAuth` configured with HS\* and RS\*/ES\* algorithms in the same
`Algorithms` list leaks the algorithm code-path via response latency
(HMAC verifies in ~1 µs, RSA-2048 in ~300 µs). An attacker submitting
tokens labelled with different `alg` values can determine which path the
server runs from the response time alone, narrowing the attack surface for
algorithm-confusion attacks (RFC 8725 §3.1). Configure each endpoint with
a single algorithm family. Mixed-family configuration emits a `slog.Warn`
at construction time.

### BasicAuth Brute-Force (MM-2026-0027)

`middleware.BasicAuth` does not rate-limit authentication attempts. Compose it
with `middleware.ThrottlePerIP` to mitigate online brute-force:

```go
r.Use(
    middleware.ThrottlePerIP(10, time.Second, nil),
    middleware.BasicAuth("realm", creds),
)
```

### BREACH Compression Oracle (MM-2026-0030 / DOS-2026-0006)

`middleware.Compress` does not mitigate the BREACH attack. Confirmed in
`reports/dos-resilience-tester/harness/breach_oracle_test.go` with a Cohen's
d effect size of ~10.3 — an attacker controlling one URL/query parameter
that is echoed alongside a secret in a gzip-compressed response can recover
each character of the secret with ~2 requests on average.

**Mitigations**, in decreasing order of safety:

1. **Do not compress endpoints that echo user-controlled input near secrets.**
   The simplest fix: register `middleware.Compress` only on routes that do
   not echo attacker-controlled data into the body, or build a separate
   middleware chain for sensitive endpoints.

2. **Move secrets out of the response body.** Put OAuth2 scopes, CSRF
   tokens, session IDs and JWTs in headers, cookies, or dedicated endpoints
   that are never reachable via attacker-controlled input.

3. **Variable-length random padding.** If 1 and 2 are not feasible, append
   a random-length (>= 256 bytes, length randomised per request) random
   payload to the response body. Validated by
   `TestBREACHOracleWithRandomPadding`: with this scheme the oracle's
   Cohen's d drops below 0.03 (negligible). Fixed-length padding is **not**
   sufficient — the random content compresses to similar sizes per request.

Example mitigation pattern (variable-length random padding):

```go
func tokenInfo(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query().Get("q")
    padN := 256 + rand.Intn(256) // length itself is randomised
    pad := make([]byte, padN)
    _, _ = rand.Read(pad)
    body := fmt.Sprintf(`{"scope":"%s","query":"%s","pad":"%x"}`,
        oauthScope, q, pad)
    _, _ = io.WriteString(w, body)
}
```

MuxMaster cannot apply these mitigations on the operator's behalf because
they require domain knowledge of which response fields are secret vs
user-controlled.

### Registration Panics Leave Tree Inconsistent (MM-2026-0033)

If `Handle` (or any route registration method) panics — for example due to a
route conflict — the radix tree may be in an inconsistent state. **Do not catch
and ignore registration panics.** Let them crash `main()` so the inconsistency
is discovered during development, not in production.

### Recoverer Must Be Outermost (MM-2026-0034)

`middleware.Recoverer` (or `RecovererWithLogger`) only catches panics in
middleware and handlers registered *inside* it. Register it as the outermost
middleware — or use `r.Pre(middleware.RecovererWithLogger(logger))` — to
ensure it wraps the full dispatch chain.

## HTTP/1.1 Smuggling (MM-2026-0045)

Request smuggling (CL.TE / TE.CL / TE.TE) is defended by Go's `net/http`
package at the framing layer. No action is required at the MuxMaster level.
Verified clean against standard smuggling test suites.

## Error Oracle (MM-2026-0046)

Differential error responses (404 vs 405 vs 301) are intentional HTTP
semantics and are present in all HTTP routers. If normalising error responses
is required for your threat model, use a WAF or a custom `NotFound` /
`MethodNotAllowed` handler.
