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

## Resolved Findings (v1.0.0)

The router has been audited across nine specialist sprints (S1..S9) plus
two pre-release mini-sprints (S10-PreCSA, S10-PreMSR) covering 95+
findings and 51 explicit threat-model hypotheses (TM-2026-001..051).
The full evidence is preserved under
[`/reports/`](reports/) — every harness is reproducible with
`go test -race`.

The four findings that materially gated the v1.0.0 release are listed
here for operator awareness. All are fixed in code at the v1.0.0 tag;
this section exists so a security-conscious adopter can verify by ID
that the issue is closed.

| ID | Sev | Class | Summary | Fix location | Status |
|---|---|---|---|---|---|
| **CSA-2026-0060** | 8 | CWE-440 / CWE-863 | `ParamsFromContext()` silently returned empty params when any `Use()`-registered middleware wrapped the request context (`Timeout`, `WithValue`, `RealIP`). An auth handler comparing `:userID` to a JWT subject would see `""` and could grant or deny access incorrectly. | `params.go:357-378` — slow-path `ctx.Value` fallback gated by reflection-discovered `hasReqCtxField` | **FIXED** |
| **HPS-2026-0005** | 7 | CWE-601 | When the request line used absolute-form URI (RFC 7230 §5.3.2, e.g. `GET http://evil.com/x HTTP/1.1`), `RedirectTrailingSlash` and `RedirectFixedPath` echoed the attacker-controlled scheme + host into the `Location` header — open redirect. | `mux.go:945, 960` — `Location` is now built from a path-only `url.URL` so the scheme + host can never originate from request input | **FIXED** |
| **FPE-2026-010** | 6 | CWE-693 / CWE-863 | Calling `Mux.Use(authMW)` followed by `Mux.HandleFast(...)` silently registered a fast route with NO middleware applied. `Use()`'s GoDoc explicitly claimed this combination panics — but the panic guard from CSA-2026-0054 was only wired to `Group.HandleFast`, not root `Mux.HandleFast`. | `mux.go:435-445` — root `HandleFast` panics when `Use()`-registered middleware is present, mirroring `Group.HandleFast` | **FIXED** |
| **TM-2026-005** | 4 | CWE-532 | The construction-time `slog.Warn` issued when `OAuth2Introspect` is configured with `AllowInsecureEndpoint: true` logged the full endpoint URL — including any credentials embedded in the query string. | `middleware/oauth2.go:229-233` — log `host` + `scheme` only, never the full URL | **FIXED** |

### Audit-trail breakdown

- **S1..S6:** initial security battery covering SAST, supply-chain, HTTP protocol, path-routing, DoS, concurrency, middleware, timing.
- **S7:** focused fuzzing + property-based test infrastructure.
- **S8:** delta-driven re-validation of S1..S7 closures with new harnesses.
- **S9:** pre-release Onda 2 consolidation (95+ findings, 51 hypotheses,
  three composite-attack hypotheses TM-COMPOSITE-2026-001..003).
- **S10-PreCSA / S10-PreMSR:** S9 follow-up to close the
  budget-exhaustion gaps. **9 of 9** UNTESTED concurrency hypotheses
  (TM-007/008/019/025/027/028/036/037/045) **REFUTED** under
  `-race -count=3`. **6 of 6** middleware hypotheses
  (TM-001/002/004/005/022/044) closed (4 REFUTED, 2 documented as
  defaults requiring operator opt-in).

The maturity verdict (production-readiness for high-load, stress, and
high-concurrency environments) is recorded in
[`/reports/overview/2026-05-08-final-maturity-verdict.md`](reports/overview/2026-05-08-final-maturity-verdict.md).

### Operator-facing defaults requiring opt-in

Three middleware components ship with backwards-compatible defaults
that are unsafe in production. Each emits a `slog.Warn` at construction
time when the unsafe default is in effect; search startup logs for these
warnings.

| Middleware                 | Unsafe default                                  | Safe production setting                                | Finding ID    |
|----------------------------|-------------------------------------------------|--------------------------------------------------------|---------------|
| `JWTAuth`                  | `RequireExpiry: false` (no `exp` ⇒ replayable) | `RequireExpiry: true` (RFC 8725 §4.4)                  | TM-2026-001   |
| `RealIP()`                 | called with no CIDRs                            | `RealIP(&proxyCIDR)` with explicit trusted CIDR list   | TM-2026-044   |
| `OAuth2Introspect`         | `AllowInsecureEndpoint: true`                   | leave `false` — HTTPS endpoint only                    | MSR-2026-0067 |

The README "Security defaults" section reproduces this matrix and the
hardened-stack snippet.

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
    Handler:           mux,
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

#### UseRawPath traversal (PRF-2026-0002 / CDX-S8-002)

When BOTH `UseRawPath = true` AND `UnescapePathValues = true`, `%2f`
inside a single path segment is matched as one segment by the radix tree
(because `/` is preserved as the path separator only via a literal
slash) and is then DECODED in the captured param value. A request such
as `/files/..%2fetc%2fpasswd` against the route `/files/:filepath`
binds `:filepath` to the literal string `..\x2fetc\x2fpasswd` — i.e. the
captured value contains a real slash.

Handlers that pass `ParamsFromContext(...).ByName("filepath")` to
`os.Open`, `http.FileServer`, or any URL/file API WITHOUT calling
`path.Clean` (and rejecting values that contain `..`) are vulnerable to
directory traversal. The `clean_path` middleware does NOT normalise
post-decode values; it only canonicalises the request path before
dispatch.

**Mitigation.** Choose one of:

1. Leave `UnescapePathValues = false` (default) and let the handler
   call `url.PathUnescape` only after `path.Clean` and a `..` check.
2. Use the safe pattern in `examples/static-site/` which calls
   `path.Clean` on the param BEFORE filesystem dispatch and rejects
   paths whose cleaned form starts with `..`.

When `UseRawPath` and `UnescapePathValues` are both set, MuxMaster
emits a one-time `slog.Warn` at the first `Handle`/`HandleFast` call
to make the misconfiguration visible in startup logs. Additionally,
`ServeFiles` PANICS at registration when both flags are set, since
http.FileServer would treat decoded slashes as path separators inside
the static-file root (CDX-S8-002).

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

### OAuth2 introspection cache poisoning (MSR-2026-0063)

The `OAuth2Introspect` middleware caches the introspection response
(keyed by sha256 of the bearer token) for `CacheTTL` seconds. If the IDP
revokes a token mid-cache-window, MuxMaster continues to honour the
cached `active=true` response until the cache entry expires — a blast
radius of up to `CacheTTL`. For high-security endpoints, set
`OAuth2Options.CacheTTL = -1` to disable caching entirely; every request
will hit the introspection endpoint, but the singleflight group
(DOS-OAUTH2-001 fix) coalesces concurrent calls for the same token, so
the IDP load growth is bounded by distinct-token concurrency rather
than total request rate.

### Timeout middleware preemption (DOS-2026-0003)

`middleware.Timeout` cancels the request context after the configured
duration but does NOT preempt the handler goroutine. Go has no
preemption primitive for blocked syscalls — a handler that ignores
`ctx.Done()` will continue running to completion, regardless of the
timeout. Under load this accumulates goroutines and exhausts memory or
upstream connections.

Handlers MUST observe `ctx.Done()` on every blocking call (DB, network,
file I/O). Use the `*Context` variants of stdlib APIs (`sql.DB.QueryContext`,
HTTP request bodies via `r.Context()`, etc.).

### Compress sniff-buffer per-connection memory (DOS-2026-0007)

`middleware.Compress` buffers up to 8 KiB per stalled connection while
sniffing whether the response body is large enough to be compressed.
Total memory under attack is `N × 8 KiB` for N concurrent stalled
connections. The middleware does not enforce a per-connection timeout;
operators MUST set `http.Server.ReadHeaderTimeout`,
`http.Server.WriteTimeout` and a connection cap on the listener to
bound this exposure.

### Reflect-based ctx field offset (MM-2026-0035)

The tiered `reqBundle` optimisation (1 alloc per request with parameters)
relies on `unsafe.Add` over the offset of the private `ctx` field of
`*http.Request`, derived once via reflection at `init()`. If a future Go
version removes or renames the field, the offset cannot be resolved and
MuxMaster automatically falls back to `r.WithContext` (2 allocs per
request) without crashing. The fallback is exercised in
`reports/concurrency-security-auditor/harness/h018_reqctx_offset_test.go`
as part of the CSA harness. CI should cross-build against `gotip`
periodically to surface drift before a stable Go release.

### RealIP misconfiguration (MSR-2026-0055)

`middleware.RealIP()` called with no trusted-proxy CIDR list trusts every
peer — any client can spoof `X-Forwarded-For` / `X-Real-IP` and the router
will accept it as the real client IP. This is only safe behind a single
trusted proxy that strips inbound XFF; in any other deployment it is a
trivial spoofing primitive that defeats `ThrottlePerIP` and IP-based
access controls. Always pass the proxy CIDR list explicitly:

```go
proxyCIDR := netip.MustParsePrefix("10.0.0.0/8")
mux.Use(middleware.RealIP(&proxyCIDR))
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
mux.Use(middleware.RealIP(&proxyCIDR))           // first
mux.Use(middleware.ThrottlePerIP(50, ts, nil))   // then
```

A `slog.Warn` is emitted at construction time when `ThrottlePerIP` is
called with a nil keyFn.

### Accepted Timing Oracles (TSC-2026-0001..0007)

The timing-and-side-channel analyst sprint catalogued seven sub-microsecond
to low-microsecond timing differences. The following are accepted as
architectural or stdlib-derived; mitigation requires either Go runtime
changes (out of MuxMaster's scope) or invasive padding that would degrade
valid-request latency. Operators concerned about LAN-adjacent statistical
attacks should rate-limit aggressively and monitor for prefix-scan probes.

- **TSC-2026-0001 (BasicAuth valid vs invalid password, 890 ns).** The
  `map[string][32]byte` credential lookup uses `runtime.mapaccess2_faststr`
  which is not constant time. The post-auth code path (`next.ServeHTTP` vs
  `http.Error+WWW-Authenticate`) also dominates the visible delta.
  Constant-time comparison of the hashed credentials is already used; the
  remaining oracle is the map shape.

- **TSC-2026-0002 (BasicAuth user-exists vs not-exists, 61 ns).** Same
  root cause as TSC-0001 — the hashedCreds map lookup leaks existence.
  Effect size is sub-microsecond and impractical over WAN.

- **TSC-2026-0004 (APIKey hit vs miss, 1141 ns).** The
  `map[[32]byte]string` lookup leaks key existence with a similar
  magnitude. A constant-time alternative requires iterating every
  registered key with `subtle.ConstantTimeCompare`, which is O(n) per
  request — only worthwhile for very small key sets.

- **TSC-2026-0005 (Route existence, 923 ns) — MM-2026-0026 magnitude
  update.** Registered vs unregistered paths take measurably different
  time inside the radix tree. Already documented as the
  "Route-Existence Timing Oracle" earlier in this file.

- **TSC-2026-0006 (ECDSA zero-sig vs random-sig, 1234 ns).** Stdlib
  `ecdsa.Verify` returns at slightly different times depending on
  signature shape. The signature is rejected either way; the oracle
  only reveals that the signature is zero, which is publicly observable
  in the request anyway.

- **TSC-2026-0007 (OAuth2 cache hit active vs inactive, 143 µs).** The
  401 vs 200 response paths differ in execution length by design. The
  timing difference reveals nothing beyond what the HTTP status code
  already exposes.

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
mux.Use(
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

### Composite token-handling stack (CDX-S8-001)

A bearer-token endpoint that combines OAuth2 introspection (or JWT) with
per-IP throttling and reverse-proxy IP forwarding has FOUR distinct
attack surfaces. Defaulting any one to "off" is exploitable end-to-end:
a passive observer captures a bearer token over plaintext HTTP, replays
it indefinitely (no `exp`), and forges `X-Forwarded-For` to bypass per-IP
throttle. The hardened stack flips ALL of the following invariants ON:

| # | Invariant                                              | Mechanism                                            | Default |
|---|--------------------------------------------------------|------------------------------------------------------|---------|
| 1 | Introspection endpoint MUST be HTTPS                   | `OAuth2Options.Endpoint` panics on non-HTTPS         | enforced |
| 2 | JWT tokens MUST carry `exp`                            | `JWTOptions.RequireExpiry = true`                    | OFF (opt-in) |
| 3 | XFF must be parsed RIGHTMOST past trusted CIDRs        | `RealIP(trustedCIDRs...)` walks rightmost-leftward   | enforced when `len(trustedCIDRs)>0` |
| 4 | Per-IP table size must be CAPPED                       | `ThrottlePerIPCapped` (default `100_000`)            | enforced via `ThrottlePerIP` wrapper |

A configuration that inadvertently leaves any of these OFF (e.g. opens
`AllowInsecureEndpoint` "for testing", omits `RequireExpiry`, calls
`RealIP()` with no CIDRs, or builds a custom throttle without a cap)
is exploitable. The recommended pattern is:

```go
// Hardened token-handling stack — see CDX-S8-001 / SECURITY.md
//   "Composite token-handling stack".
trusted, _ := netip.ParsePrefix("10.0.0.0/8")
mux.Pre(
    middleware.RealIP(&trusted),                  // (3) rightmost XFF
)
mux.Use(
    middleware.ThrottlePerIP(100, 5*time.Second, nil), // (4) capped per-IP
    middleware.JWTAuth(middleware.JWTOptions{
        Secret:        secret,
        Algorithms:    []string{"HS256"},
        RequireExpiry: true,                       // (2) reject no-exp
    }),
    // OR use OAuth2Introspect with HTTPS endpoint:
    // middleware.OAuth2Introspect(middleware.OAuth2Options{
    //   Endpoint: "https://idp.example/introspect",  // (1) HTTPS only
    //   ...
    // }),
)
```

`examples/jwt/` and `examples/oauth2/` demonstrate the full pattern
with comments cross-referencing CDX-S8-001.

### Layered panic recovery (CSA-2026-0058 / CSA-2026-0059)

MuxMaster has THREE layers that may catch panics. Understanding the order
is essential when configuring PanicHandler and middleware.Recoverer.

| Layer                         | What it catches                              | What it does on a second panic                |
|-------------------------------|----------------------------------------------|-----------------------------------------------|
| `Mux.PanicHandler`            | Panics in handler / `Use` / `UseFast` chain. | NOT recovered by MuxMaster.                   |
| `middleware.Recoverer` (Pre)  | Panics anywhere downstream including the    | Catches if PanicHandler panicked.             |
|                               | first PanicHandler invocation.               |                                               |
| `net/http` per-conn recover   | Anything that escapes both above.            | Logs "http: panic serving ..." and closes TCP.|

**Boundary rules.**

1. `Mux.PanicHandler` MUST NOT panic. If it does, the secondary panic
   propagates to the next layer (Recoverer in `Pre()`, or net/http) and
   the connection is terminated mid-response. There is no goroutine leak
   and no process crash, but the client sees a reset stream — confusing
   for HTTP/2 multiplexing and reverse-proxy retries.
2. To make recovery FULLY symmetric for both stdlib and FastHandler
   routes, register `RecovererWithLogger` via `r.Pre(...)`. Pre wraps
   ALL dispatch including HandleFast, so even a panic in fast routes
   (which `Use`-registered Recoverer cannot catch — see CSA-2026-0054)
   is contained.
3. `Mux.PanicHandler` runs INSIDE the dispatch frame and covers both
   stdlib and FastHandler routes; it is the single, route-type-agnostic
   recovery point if you do not want to register a Pre middleware.

### Pre vs Use security boundary (CSA-2026-0059 / H8-01)

`Mux.Pre()` and `Mux.Use()` register middleware in different positions
of the dispatch pipeline. Mistaking one for the other has direct
authentication/authorisation consequences when `HandleFast` routes are
also in play.

| Middleware family        | Wraps `Handle` (stdlib)? | Wraps `HandleFast`?       | Mode                                |
|--------------------------|--------------------------|---------------------------|-------------------------------------|
| `r.Pre(...)`             | YES                      | YES                       | Outside dispatch — `http.Handler`.  |
| `r.Use(...)`             | YES                      | NO — panics at register.  | Inside dispatch — `http.Handler`.   |
| `r.UseFast(...)`         | NO                       | YES                       | Inside dispatch — `FastMiddleware`. |

**Implications.**

- An auth gate (e.g. `JWTAuth`) registered via `r.Use(...)` does NOT cover
  `HandleFast` routes — MuxMaster panics at `HandleFast` registration to
  expose this immediately (CSA-2026-0054). Either (a) register the auth
  via `r.Pre(...)` so it covers BOTH route types, or (b) duplicate the
  auth as a `FastMiddleware` and register it via `r.UseFast(...)`.
- Because `Pre` runs OUTSIDE the dispatch (in `Mux.ServeHTTP` before the
  radix-tree lookup), it sees the path BEFORE any route-specific handler
  decision — useful for `CleanPath`, `RealIP`, `RecovererWithLogger`,
  request ID assignment, or any policy that must be uniform across the
  router.
- `Use` runs INSIDE the dispatch (after the route is matched) and is
  baked into the wrapped handler at registration time. Its only path
  to a fast route is via `UseFast`.

Operators auditing an auth/CORS/rate-limit policy by reading `Use(...)`
calls SHOULD also inspect `HandleFast(...)` registrations and
`UseFast(...)` calls; otherwise a fast-route bypass is invisible.

## HTTP/1.1 Smuggling (MM-2026-0045)

Request smuggling (CL.TE / TE.CL / TE.TE) is defended by Go's `net/http`
package at the framing layer. No action is required at the MuxMaster level.
Verified clean against standard smuggling test suites.

## Error Oracle (MM-2026-0046)

Differential error responses (404 vs 405 vs 301) are intentional HTTP
semantics and are present in all HTTP routers. If normalising error responses
is required for your threat model, use a WAF or a custom `NotFound` /
`MethodNotAllowed` handler.

## ThrottlePerIPCapped saturation (TM-2026-013, DOS-2026-0057) — ACCEPTED

When the per-IP throttle table reaches `maxTableSize` and every slot is
in active use (`refs > 0`), new client IPs receive HTTP 503 immediately.
An attacker controlling at least `maxTableSize` distinct IPs (default
100 000) and keeping their requests open can sustain this lockout for
as long as the connections remain.

**Reproduction:** `reports/dos-resilience-tester/harness/s9_dos_test.go:TestThrottlePerIPCappedSaturationHoldout`.

**Required operator mitigations:**

- Deploy upstream DDoS scrubbing (Cloudflare, AWS Shield, GCP Cloud Armor).
- Configure `http.Server{ReadHeaderTimeout, IdleTimeout, ReadTimeout}` so
  slow-handler connections cannot hold throttle slots indefinitely.
- Reduce `maxTableSize` for high-sensitivity endpoints — the cap
  intentionally trades fairness for memory safety.

This is accepted behaviour: the cap exists precisely to prevent
unbounded memory growth under IP-churn attacks. See
`/reports/dos-resilience-tester/harness/s9_dos_test.go` for the
evidence and `MSR-2026-0068` for the cooperative refs-decrement fix
that ensures the table drains correctly when timed-out requests release.
