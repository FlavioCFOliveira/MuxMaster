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
| **CSA-2026-0060** | 8 | CWE-440 / CWE-863 | `ParamsFromContext()` silently returned empty params when any `Use()`-registered middleware wrapped the request context (`Timeout`, `WithValue`, `RealIP`). An auth handler comparing `:userID` to a JWT subject would see `""` and could grant or deny access incorrectly. | `params.go`, `routeCtxParams` — when the context is not the router's own, a slow-path `ctx.Value(contextKey{})` fallback walks the wrapped context chain | **FIXED** |
| **HPS-2026-0005** | 7 | CWE-601 | When the request line used absolute-form URI (RFC 7230 §5.3.2, e.g. `GET http://evil.com/x HTTP/1.1`), `RedirectTrailingSlash` and `RedirectFixedPath` echoed the attacker-controlled scheme + host into the `Location` header — open redirect. | `mux.go`, `Mux.dispatch` (trailing-slash and fixed-path redirect branches) and `buildRedirectTarget` — `Location` is built from the path and raw query only, so the scheme + host can never originate from request input | **FIXED** |
| **FPE-2026-010** | 6 | CWE-693 / CWE-863 | Calling `Mux.Use(authMW)` followed by `Mux.HandleFast(...)` silently registered a fast route with NO middleware applied. `Use()`'s GoDoc explicitly claimed this combination panics — but the panic guard from CSA-2026-0054 was only wired to `Group.HandleFast`, not root `Mux.HandleFast`. | `mux.go`, `Mux.HandleFast` — root `HandleFast` panics when `Use()`-registered middleware is present, mirroring `Group.HandleFast` | **FIXED** |
| **TM-2026-005** | 4 | CWE-532 | The construction-time `slog.Warn` issued when `OAuth2Introspect` is configured with `AllowInsecureEndpoint: true` logged the full endpoint URL — including any credentials embedded in the query string. | `middleware/oauth2.go`, `OAuth2Introspect` — the construction-time `slog.Warn` and `slog.Info` log `host` + `scheme` only, never the full URL | **FIXED** |

## Defects Found and Fixed Before v1.2.0 (Sprint 18)

Three defects were discovered, fixed, and validated during Sprint 18's waste-hunt profiling and middleware security review. **No released version is affected** — all three defects were introduced and fixed within the development cycle that produced v1.2.0. Each fix includes a regression test that fails against the defective code and passes against the current code.

| ID | Sev | Class | Summary | Fix location | Regression test |
|---|---|---|---|---|---|
| **MID-COMPRESS-1** | Critical | CWE-670 | `Compress` middleware: 1xx informational status (e.g., 103 Early Hints) followed by a final status (e.g., 403) caused the final status to be silently dropped; the client received an implicit **200 OK** with the full response body for any request with `Accept-Encoding: gzip`. Root cause: the "first WriteHeader wins" lock in `gzipResponseWriter` lacked the 1xx exemption that `net/http` itself applies. | `middleware/compress.go:WriteHeader` — added exemption: `if code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols { return }` | `middleware/wrapper_flusher_test.go:TestCompress_1xxInformational_DoesNotBlockFinalStatus` |
| **MID-SETHEADER-1** | High | CWE-668 | `SetHeader` middleware, `response.go` `JSON`/`XML`/`Text`, `mux.go` `lazyMethodNotAllowed`/`lazyOPTIONS`: hoisted header-value `[]string` (not just the constant string) into closure/package variables, shared across requests. Downstream code indexing directly (`w.Header()[k][0] = ...`) mutated **shared backing array**, corrupting headers for all other requests until process restart. **Introduced and fixed within the v1.2.0 development cycle**. | `middleware/set_header.go`, `response.go`, `mux.go` — each changed to allocate slices fresh per request. | `middleware/setheader_wastehunt_test.go`, `header_aliasing_wastehunt_test.go`. |
| **MID-LOGGER-1** | Medium | CWE-778 | `Logger` middleware: when a handler sent both a 1xx informational status and a final status, the logger recorded the 1xx code instead of the final status in the access log. Client-visible response was correct (net/http applies its own 1xx exemption to the real `ResponseWriter`); only the **logged** status was wrong, hiding security-relevant codes (401/403/429) from status-code-based log monitoring. Root cause: same as MID-COMPRESS-1 — missing 1xx exemption in the "first wins" lock. | `middleware/logger.go:statusRecorder.WriteHeader` — added exemption matching net/http's own predicate. | `middleware/wrapper_flusher_test.go:TestLogger_1xxInformational_LogsFinalStatusNotInformational` |

## Pre-Existing Header-Aliasing Defects (Fixed in Sprint 18)

Two middleware components shipped with header-slice aliasing defects that affected released versions. Both defects are identical in root cause to MID-SETHEADER-1 (hoisted `[]string` shared across requests) but differed in scope and visibility:

| ID | Sev | Class | Summary | Affected versions | Fix location |
|---|---|---|---|---|---|
| **MID-CORS-HEADER-1** | High | CWE-668 | `CORS()`: seven per-instance closure variables (`Access-Control-Allow-Origin`, `-Credentials`, `-Vary`, `-Methods`, `-Headers`, `-Expose-Headers`, `-Max-Age`) were hoisted and shared across all requests through that middleware instance. Any downstream code mutating these slices directly corrupted headers for all other requests (and other CORS() instances). | All versions shipping `middleware.CORS` | `middleware/cors.go` — value slices now allocated fresh per request. |
| **MID-NOCACHE-HEADER-1** | High | CWE-668 | `NoCache()`: five package-level `[]string` values (`Cache-Control`, `Pragma`, `Expires`, `Surrogate-Control`, `X-Accel-Expires`) shared across every `NoCache()` instance and every request in the process — **widest blast radius found**. Any downstream code mutating these slices corrupted headers for all other requests. | All versions shipping `middleware.NoCache` | `middleware/no_cache.go` — value slices now allocated fresh per request. |

Regression tests: `middleware/cors_wastehunt_test.go`, `middleware/nocache_wastehunt_test.go` — sequential and concurrent mutation tests preventing reintroduction.

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

## Hardening Behaviours Added in Sprints 19–20 (v1.2.0)

These behaviours shipped in v1.2.0 (see the `[1.2.0]` entry of
[CHANGELOG.md](CHANGELOG.md#120---2026-09-26)); operators relying on the previous behaviour
should review them.

- **Redirect `Location` encoding (rmp #260, rmp #279).** The router's own
  trailing-slash and fixed-path redirects percent-encode every ASCII
  control byte (0x00–0x1F, 0x7F) per RFC 9110 §5.5 and every backslash
  (`\` → `%5C`). The backslash encoding neutralises the WHATWG
  "special authority slashes" shape, in which a browser resolves a
  `Location` such as `/\evil.com/` to another origin. It is a deliberate
  divergence from `net/http.Redirect`, which encodes neither; the
  `muxmaster.Redirect` helper delegates to `net/http.Redirect` and
  therefore does not apply it. `%5C` decodes back to `\` on the follow-up
  request, so a registered backslash route is still reached on the same
  origin. Defence in depth: a backslash can reach a redirect target only
  through a route the operator registered.
- **`CORS` sends `Vary: Origin` on every response (TM-2026-033,
  rmp #291).** Previously it was added only for a matched, non-wildcard
  origin, so a shared cache could serve a response fetched without CORS
  relevance to a CORS-relevant origin. It is now added (with `Header.Add`,
  keeping other `Vary` values) to every response CORS produces or
  forwards, including requests without `Origin` and CORS's own 400/403
  responses.
- **`BasicAuth` constant-time user lookup (TSC-2026-0002, rmp #290).**
  Every request scans all registered users with `crypto/subtle`; see
  TSC-2026-0002 under "Accepted Timing Oracles" for the residual figures
  and the per-user cost.
- **`OAuth2Introspect` credential redaction (CWE-532, rmp #280).** The
  construction-time panics for a malformed `Endpoint`, an `Endpoint`
  without a host and an `Endpoint` with userinfo now redact credentials
  embedded in the URL, as the construction-time log lines already did
  (TM-2026-005). The singleflight leader keeps request-scoped context
  values while ignoring the leader's cancellation (MSR-2026-0071).
- **`Recoverer` no longer corrupts a started response (O-14, rmp #276).**
  It writes its 500 response only if the handler has not yet sent a
  status or body bytes.

## Thread-Safety Contract (MM-2026-0017 / CSA-2026-0052)

All public `Mux` fields (`PanicHandler`, `NotFound`, `MethodNotAllowed`,
`GlobalOPTIONS`, `ErrorHandler`, `RedirectTrailingSlash`, `RedirectFixedPath`,
`CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode`,
`HandleMethodNotAllowed`, `HandleOPTIONS`, `PoolFastParams`,
`PoolRequestBundle`) **must be set before the first call to `ServeHTTP`**. On the first request these values are atomically
captured into a frozen `muxConfig` snapshot and every subsequent dispatch
reads from that snapshot — direct field mutation after serving begins is
ignored by the dispatch path and races with the snapshot's first read.

To reconfigure handlers after serving starts, mutate the field and then call
`Mux.Rebuild()`. `Rebuild()` atomically resets the snapshot and the lazy
NotFound/405/OPTIONS/redirect handler caches so the next request re-reads
every field. `Rebuild()` is safe to call concurrently with `ServeHTTP`.

`Use()` and `Pre()` are safe to call concurrently with `Handle()` during
route registration (before serving), but must not be called concurrently with
active requests.

## HTTP/2 Cleartext (h2c) Upgrade Handling (TM-2026-050, HPS-2026-EXT)

MuxMaster does not implement h2c-specific code. A request carrying
`Upgrade: h2c` and `Connection: Upgrade` headers is routed and dispatched
identically to any other request. By default, `net/http` does not perform
the h2c upgrade (RFC 7540 §3.4); the server responds with 200 OK and the
upgrade is silently ignored (confirmed by test `TestHPSExt16_H2CUpgradeRejected`,
2026-09-26). To enable HTTP/2 over unencrypted TCP, operators must
explicitly configure the server (Go 1.27+: `http.Server.Protocols` with
`UnencryptedHTTP2`; earlier versions: use `golang.org/x/net/http2/h2c`);
MuxMaster's dispatcher operates identically in both cases. Routing and
middleware behaviour is unaffected by the Upgrade headers; treat h2c like
any other client request.

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

The timing difference between dispatch to a **registered** route (200) and
dispatch to an **unregistered** path (404) — same depth, no auth middleware
involved — is measured with the identical harness against the vendored
httprouter, chi, and bunrouter modules: the same registered-vs-unregistered
pair yields ~1014 ns (httprouter), ~897 ns (chi), and ~777 ns (bunrouter),
against MuxMaster's ~932 ns — all four land within the same 750–1050 ns band
(Cohen's d 0.69–1.24, medium-to-large) and MuxMaster is not the largest
(measured AMD Ryzen 9 5900HX, Go 1.27.0, N=200,000 samples × 3 independent
runs, Welch t-test + Kolmogorov-Smirnov + Mann-Whitney U, 2026-09-26,
`reports/timing-and-sidechannel-analyst/2026-09-26-route-existence-oracle-stats.md`
and `competitor/route_existence_timing_test.go`). The finer-grained
depth-correlation signal is not uniform across routers: chi and bunrouter
show roughly 6–10× more cumulative depth-vs-latency signal than MuxMaster or
httprouter. This is intrinsic to radix tree lookup; all major routers exhibit
similar timing. If this is a concern, use a WAF or add uniform response delays
via middleware.

### Path normalisation accepted behaviour (PRF-2026-0001..0005)

The following routing behaviours are intentional and documented as operator
responsibilities — they are not router defects:

- **`%61dmin` matches `/admin`** when `UseRawPath=false` (the default).
  `net/http` decodes `%61` to `a` during URL parsing per RFC 3986 §6.2.2.2,
  so the router sees `/admin`. To enforce byte-exact path matching set
  `mux.UseRawPath = true` — patterns then match against `r.URL.RawPath`,
  which preserves the percent-encoded form (PRF-2026-0002).

#### UseRawPath traversal (PRF-2026-0002 / CDX-S8-002)

When BOTH `UseRawPath = true` AND `UnescapePathValues = true`, `%2f`
inside a single path segment is matched as one segment by the radix tree
(because `/` is preserved as the path separator only via a literal
slash) and is then DECODED in the captured param value. A request such
as `/files/..%2fetc%2fpasswd` against the route `/files/:filepath`
binds `:filepath` to the literal string `..\x2fetc\x2fpasswd` — i.e. the
captured value contains a real slash.

Handlers that pass `ParamsFromContext(...).Get("filepath")` to
`os.Open`, `http.FileServer`, or any URL/file API WITHOUT calling
`path.Clean` (and rejecting values that contain `..`) are vulnerable to
directory traversal. The `CleanPath` middleware does NOT normalise
post-decode values; it only canonicalises the request path before
dispatch.

**Mitigation.** Choose one of:

1. Leave `UnescapePathValues = false` (default) and let the handler
   call `url.PathUnescape` only after `path.Clean` and a `..` check.
2. Use `http.FileServer` (as in `examples/static-site/`), which applies
   `path.Clean` internally and prevents escaped traversal sequences from
   escaping the configured root.

When `UseRawPath` and `UnescapePathValues` are both set, MuxMaster
emits a one-time `slog.Warn` at the first `Handle`/`HandleFast` call
to make the misconfiguration visible in startup logs. Additionally,
`ServeFiles` PANICS at registration when both flags are set, since
http.FileServer would treat decoded slashes as path separators inside
the static-file root (CDX-S8-002).

**UseRawPath + CleanPath interaction:** When `UseRawPath = true`, the router
matches against `r.URL.RawPath` (the percent-encoded form, which `net/http`
sets only when it differs from the decoded `r.URL.Path`). If `CleanPath` is
registered (as a Pre-gate), it cleans the decoded path via `path.Clean`, then
compares the result against a cleaned RawPath: if they diverge (e.g. for
`/a/%2e%2e/b`, `Path` is `/a/../b`, which cleans to `/b`, while `path.Clean`
does not treat the encoded `%2e%2e` in `RawPath` as `..`), `CleanPath` zeroes
`RawPath` to prevent encoded traversal bypass (test `TestSec_CleanPath_RawPathWithTraversalZeroed`,
MSR-2026-0061, 2026-09-26). This means CleanPath already protects against
encoded traversal even with `UseRawPath = true`. Handlers still must not
pass RawPath values directly to file I/O; always decode and validate
(PRF-2026-S9-001 / MSR-2026-0061).

- **Catch-all `*filepath` parameters carry raw bytes**, including any
  `..` traversal sequences. The router does NOT sanitise catch-all values
  — that is the boundary between router and storage backend. Handlers
  serving files MUST call `path.Clean` and must reject paths that escape
  their root (e.g. via `filepath.IsLocal` or by checking
  `filepath.Rel(root, joined)`). `Mux.ServeFiles` already delegates to
  `http.FileServer` which performs path cleaning (PRF-2026-0005).

- **Path parameters may contain any byte, including CR/LF.** When
  `UseRawPath=false` (the default), `net/http` decodes percent-encoded
  sequences before routing. A request like `/users/%0D%0ASet-Cookie:%20hacked`
  becomes `/users/\r\nSet-Cookie: hacked` in `r.URL.Path`, and captured
  parameters carry the literal CR/LF bytes. Handlers must NOT echo
  parameters directly into headers or logs without escaping (use `url.PathEscape`
  for header injection mitigation, `html.EscapeString` for logs). The router
  itself does not strip or reject CR/LF; that is the handler's responsibility
  (PRF-2026-S9-002, rmp #156).

- **Order-independent static and parameter routes.** Routes can be registered
  in any order: `r.GET("/users/:id", h)` followed by `r.GET("/users/active", h)`
  succeeds without panic (rmp #256, fixed). Static routes are never shadowed
  by param routes at the same depth (PRF-2026-0003).

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
  semantically meaningful. **Ordering:** When composing CleanPath with
  path-inspecting Pre-gates (e.g. authorization checks that reject `/admin/*`),
  register CleanPath FIRST — a gate registered before CleanPath sees the
  raw path and can be bypassed by `/admin/../public`, `//admin`, or
  `%2e%2e`-encoded variants (rmp #284, TM-2026-040). **Query fragment
  in parameters:** Path parameters may contain `?` and `#` if percent-encoded
  in the request URL (`%3F` and `%23`). When handlers construct URLs with
  captured parameters, use `url.QueryEscape` (for query-string values) or
  `url.PathEscape` (for path segments) to prevent accidental query/fragment
  boundary shifts (PRF-2026-S9-006, rmp #161) (PRF-2026-0001).

### Mount inner handler path sanitisation (PRF-2026-S9-009)

`Mux.Mount` and `Group.Mount` forward requests to an inner handler with the
mount prefix stripped. The router does NOT clean the request path before
passing it to the inner handler. When the inner handler is another `*Mux`,
its routing receives the uncleaned path; if a request like `/mount/../secret`
reaches the mount (e.g. via `CleanPath` *after* the Mount prefix is consumed),
the inner handler receives `/../secret` and may dispatch it differently than
the outer router did. This is expected behaviour: inner handlers are responsible
for their own path validation and cleanup (e.g. calling `CleanPath` or validating
input). The inner `Mux`'s own `CleanPath` Pre-gate must run for its own security
(confirmed by test `TestMount_TraversalEscapesPrefix`, 2026-09-26). See
`specification/groups.md` §8–10 and `groups.md` rule 23–24 for Mount's
path-forwarding contract.

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

### OAuth2 singleflight leader cancellation (MSR-2026-0071)

When `OAuth2Introspect` has concurrent requests for the same token, the
first request (the "leader") performs the introspection call while others
(the "followers") wait for its result via an in-process singleflight group
keyed by `sha256(token)` (MuxMaster has no external dependencies).
If the leader's context (the first request's `r.Context()`) is cancelled
(e.g. by a `Timeout` middleware or client disconnect), the leader's context
cancellation must not poison the cached result for followers. As of commit
0eafe9e (rmp #280), the leader performs introspection with a detached
`context.WithTimeout(context.WithoutCancel(r.Context()), 30s)`: the
leader's request-scoped context values are kept, but its cancellation and
deadline are not, so a cancelled leader does not fail its followers. The 30s timeout is a fixed upper bound on
introspection latency; it is **not** configurable and is separate from
`CacheTTL` (which controls cache expiry, not introspection timeout).

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

### Startup-time route registration cost (DOS-2026-0051)

Registering routes with `Handle`, `GET`, `POST`, and related methods incurs
a one-time cost at application startup. The router's radix tree uses
copy-on-write semantics to guarantee rollback safety (see MM-2026-0033), and
this cost scales linearly with the route count on typical hardware.

**Measured on AMD Ryzen 9 5900HX, Go 1.27, 2026-09-25:**

| Route count | Wall time | Per-route cost |
|---|---|---|
| 1 000 | ~0.4–0.8 ms | ~0.4–0.8 µs |
| 10 000 | ~6.6–9.9 ms | ~0.7–1.0 µs |
| 100 000 | ~104 ms | ~1.0 µs |

Complexity is linear (slope ~1.15–1.25 at small N, flat ~1 µs/route asymptotically),
not quadratic. The minor super-linear behaviour at N < 10 000 is a warm-up and GC
artefact; beyond 10 000 routes, per-route cost is flat.

**Threat assessment:** Registration is performed only at application startup
from the router's own code; it is not reachable from HTTP requests
(see `specification/out-of-scope.md` §3.1 — dynamic route registration after serving
begins is unsupported). A one-second startup delay would require roughly 1 000 000 routes.

**Residual risk:** Applications that construct their route table from untrusted
configuration (e.g. a remote endpoint, a database, or user-supplied YAML) should
bound the maximum route count. An attacker controlling the configuration could
inflate the route count to increase startup time, trading startup latency for a
slowdown that does not reach production serving.

See `reports/dos-resilience-tester/2026-09-25-DOS-2026-0051-registration-cost.md`
for the full measurement harness, CPU profile, and complexity analysis.

### Accepted Timing Oracles (TSC-2026-0001..0007)

The timing-and-side-channel analyst sprint catalogued seven sub-microsecond
to low-microsecond timing differences. The following are accepted as
architectural or stdlib-derived; mitigation requires either Go runtime
changes (out of MuxMaster's scope) or invasive padding that would degrade
valid-request latency. Operators concerned about LAN-adjacent statistical
attacks should rate-limit aggressively and monitor for prefix-scan probes.

- **TSC-2026-0001 (BasicAuth valid vs invalid password, 890 ns; accepted
  bound: ≤2000 ns).** The 890 ns figure was measured when the credential
  lookup still used a `map[string][32]byte` (`runtime.mapaccess2_faststr`,
  not constant time). Since commit 6ac8772 (rmp #290, see TSC-2026-0002
  below) the lookup is a constant-time scan over all users, so no map
  remains; the visible delta is dominated by the post-auth code path
  (`next.ServeHTTP` vs `http.Error` + `WWW-Authenticate`), which differs
  by design. The
  bound is asserted by `TestTiming_BasicAuth_ValidVsInvalid`
  (`tsc20260001BoundNs` in `basic_auth_timing_test.go`), derived 2026-09-25
  (rmp #270 / O-9) as 2× the worst of the documented figure and 6
  independent `-count=1` runs (worst observed: 1258.16 ns), rounded up.

- **TSC-2026-0002 (BasicAuth user-exists vs not-exists, 15–121 ns residual;
  accepted bound: ≤700 ns).** The credential lookup originally used a
  `map[string][32]byte` which leaked existence; as of commit 6ac8772 (rmp #290),
  BasicAuth now scans all registered users in constant time via
  `subtle.ConstantTimeCompare`, eliminating the map-lookup oracle at code level.
  The residual timing difference (measured across 3 independent runs,
  2026-09-26, worst: 121 ns) comes from other code paths (handler dispatch,
  middleware chain) and is well within the ≤700 ns bound. Cost per request is
  ~26 ns per registered user (1 user: 202→322 ns; linear scaling). The bound
  is asserted by `TestTiming_BasicAuth_UserExistsVsNotExists`
  (`tsc20260002BoundNs`), derived 2026-09-25 (rmp #270 / O-9) from the original
  6-run sample; the implementation fix (commit 6ac8772) was validated separately
  with a re-measurement on 2026-09-26 showing the oracle is closed at the
  code level.

- **TSC-2026-0004 (APIKey hit vs miss, 1141 ns; accepted bound: ≤2500 ns).**
  The `map[[32]byte]string` lookup leaks key existence. Unlike BasicAuth
  (which was fixed by commit 6ac8772 to use constant-time scanning),
  APIKey keeps its map-based lookup because a constant-time alternative
  would require iterating every registered key with `subtle.ConstantTimeCompare`
  (O(n) per request) — only worthwhile for very small key sets. The bound is
  asserted by `TestTiming_APIKey_HitVsMiss` (`tsc20260004BoundNs` in
  `api_key_timing_test.go`), derived 2026-09-25 (rmp #270 / O-9) as 2× the
  worst of the documented figure and 6 independent `-count=1` runs (worst
  observed: 501.04 ns), rounded up. This replaces an earlier, ungrounded
  100 ns threshold in the same test that had no relationship to this
  documented figure.

- **TSC-2026-0013 (BasicAuth password-length, same-length vs
  different-length wrong password, ≤360 ns; accepted bound: ≤700 ns) —
  MM-2026-0020 regression test.** Historical pre-fix evidence (raw
  passwords compared directly, before `middleware/basic_auth.go` hashed
  both sides): N=1.5M, p=0, mean difference 284-316 ns, maximum latency
  when `len(pass)==len(expected)` — `subtle.ConstantTimeCompare` returns 0
  immediately, non-constant-time, whenever the two slice lengths differ.
  The fix SHA-256-hashes both the supplied and the stored password before
  the compare, so the compare always runs on two 32-byte digests
  regardless of the caller-supplied password's length; the length-mismatch
  branch can no longer diverge by input length. The bound is asserted by
  `TestTiming_BasicAuth_PasswordLengthOracle` (`tsc20260013BoundNs` in
  `basic_auth_timing_test.go`), derived 2026-09-25 (rmp #274 / O-14) as 2×
  the worst of 6 independent `-count=1` runs on a shared/virtualised
  sandbox (30.86, 329.56, 83.47, 217.33, 41.97, 299.47 ns — worst observed:
  329.56 ns), rounded up, then confirmed passing on 3 further independent
  runs (worst: 359.64 ns). This test — the sole regression coverage for
  MM-2026-0020 — was removed by commit `5f804fa` without a like-for-like
  replacement; see `reports/overview/findings.md` O-14.

- **TSC-2026-0014 (Throttle near-limit vs below-limit, ≤99 ns; accepted
  bound: ≤200 ns).** `middleware.ThrottleBacklog`'s fast-path acquisition
  (`throttleSem.tryAcquire` — a single atomic Load + CompareAndSwap, per
  `middleware/throttle.go`'s CH-02 doc comment) does not depend on how
  close the current in-use count is to the configured limit; fill levels
  0/16 through 15/16 (parked-goroutine occupied slots, no request ever
  saturates the throttle) are statistically indistinguishable within this
  bound. `TestTiming_Throttle_BoundaryOracle`
  (`reports/timing-and-sidechannel-analyst/harness/throttle_timing_test.go`)
  — restored 2026-09-25 (rmp #274 / O-14) — originally measured each fill
  level in a separate sequential 100k-sample block; sequential blocks let
  uncontrolled host-load drift between blocks masquerade as a fill-level
  effect (standalone the drift-free difference was ~45 ns, but inside the
  full `-tags timing` suite the same sequential comparison read
  1100-2100 ns, MEDIUM per `classifyOracle` — a measurement-methodology
  artefact, not a code regression). Fixed 2026-09-25 (rmp #274 / part 5c)
  by giving every fill level its own independent `ThrottleBacklog`
  instance, holding all 5 open simultaneously, and sampling them in
  round-robin interleaved order (one sample per arm per round) — the same
  alternating pattern `TestTiming_BasicAuth_ValidVsInvalid` uses for its
  two arms, generalised to 5. The bound (`tsc20260014BoundNs`) is derived
  as 2× the worst of 6 independent `-count=1` runs of the interleaved
  harness on a shared/virtualised sandbox (98.35, 17.48, 30.70, 53.63,
  87.34, 84.62 ns — worst observed: 98.35 ns), rounded up, then confirmed
  passing on 3 further independent standalone runs (worst: 66.02 ns) and
  once inside the full `TestTiming_` suite (worst: 10.60 ns — confirming
  the interleaved design also removes the in-suite drift). Same derivation
  method as TSC-2026-0001/0002/0004/0013 (rmp #270 / O-9, rmp #274 / O-14).

- **TSC-2026-0005 (Route existence, ~932 ns) — MM-2026-0026 magnitude
  update.** Registered vs unregistered paths take measurably different
  time inside the radix tree. Already documented as the
  "Route-Existence Timing Oracle" earlier in this file; both sections cite
  the same current figure as of 2026-09-26 (rmp #290 / #11).

- **TSC-2026-0011 (JWTAuth HS256 vs ES256 path latency, ~80 ns at
  HEAD; no bound asserted).** The finding (rmp #153) first measured a
  320 ns distinguishable difference between the HS256 and ES256 paths; the
  re-measurement during the closed-task audit found HS256 ~7.0 µs vs ES256
  ~7.1 µs, a mean difference of 0.08 µs (80 ns).
  Unlike the HS/RS oracle (TSC-2026-0003, ~25 µs), this oracle is negligible.
  Measured 2026-09-26, N=200k samples, `reports/timing-and-sidechannel-analyst/harness/jwt_alg_confusion_timing_test.go`,
  reported in `reports/overview/2026-09-26-closed-task-audit.md` row #153.

- **TSC-2026-0012 (JWTAuth alg=none vs alg=HS256, 226 ns; no bound
  asserted).** `JWTAuth` never accepts the unsigned `alg=none` format:
  `"none"` is not a supported algorithm, and listing it in `Algorithms`
  panics at construction. A token whose header declares `alg=none`, sent to
  an HS256-configured `JWTAuth`, is rejected with 401 at the algorithm
  allow-list check; that rejection path is measurably different from HMAC
  verification of an HS256 token (mean-diff 226 ns, alg=none SLOWER, not
  faster as the original hypothesis suggested). The oracle is
  sub-microsecond and reveals only that the token was rejected. Measured 2026-09-26
  (N=200k, `reports/timing-and-sidechannel-analyst/harness/jwt_alg_confusion_timing_test.go`,
  `reports/overview/2026-09-26-closed-task-audit.md` row #154).

- **TSC-2026-0006 (ECDSA zero-sig vs random-sig, 1234 ns).** Stdlib
  `ecdsa.Verify` returns at slightly different times depending on
  signature shape. The signature is rejected either way; the oracle
  only reveals that the signature is zero, which is publicly observable
  in the request anyway.

- **TSC-2026-0007 (OAuth2 cache hit active vs inactive, 143 µs).** The
  401 vs 200 response paths differ in execution length by design. The
  timing difference reveals nothing beyond what the HTTP status code
  already exposes.

### Composition of timing oracles (CDX-2026-005)

Three independent timing oracles documented above can combine to enable
user-correlation attacks in multi-tenant deployments or to facilitate
reconnaissance of IdP infrastructure:

1. **JWT algorithm-path timing (TSC-2026-0003):** Configuring both HMAC
   (HS256) and RSA (RS256) algorithms reveals which algorithm family the
   server accepted the token with — a ~25.2 µs observable latency gap
   (HS256 vs RS256 path difference; measured 2026-09-26,
   `reports/timing-and-sidechannel-analyst/harness/jwt_alg_confusion_timing_test.go`).

2. **OAuth2 introspection cache timing (TSC-2026-0007):** The local token
   cache hits or misses depending on whether a token was previously seen
   by **any** client. Cache hit (~2 ms) vs miss (~145 µs) produces a **143 µs**
   timing difference that leaks whether a token was recently active on the service.

3. **Route existence timing (TSC-2026-0005):** Dispatch to a registered
   route and to an unregistered path at the same depth differ by about
   932 ns, a difference intrinsic to radix-tree lookup (see
   "Route-Existence Timing Oracle (MM-2026-0026)").

**Composite vector:** An attacker can submit multiple tokens and measure
response latencies to infer: (a) which algorithm family is configured,
(b) which tokens have been recently used by other clients, and
(c) which API paths are registered. Combined, this enables correlation
of tokens across different clients if they are retried by multiple users,
or reconnaissance of the internal endpoint topology.

**Recommended deployment posture:**

- **Option 1 (strict):** Configure a single algorithm family per endpoint
  (HS256 or RS256, not both). This closes the TSC-2026-0003 oracle entirely.

- **Option 2 (layered):** If mixed algorithms are required, apply uniform
  response-time padding at the edge (reverse proxy, WAF, or a `Pre`-registered
  middleware that adds fixed or random delays). This compresses the observable
  latency deltas below the statistical threshold reachable in a few hundred
  requests.

- **Option 3 (rate-limit):** Use `middleware.ThrottlePerIP` to rate-limit
  token validation attempts to tens of requests per minute per client.
  This makes collecting sufficient samples for statistical analysis infeasible
  in practice.

- **Option 4 (combination):** Deploy a reverse proxy or WAF with DDoS
  scrubbing and request rate limiting, plus strict algorithm configuration
  at the IdP level (do not mix families).

The OAuth2 cache-hit/miss oracle (TSC-2026-0007) is inherent to token
caching and is already mitigated in the code: set `OAuth2Options.CacheTTL = -1`
to disable caching if this exposure is unacceptable. The route-existence
oracle (TSC-2026-0005) is intrinsic to any radix-tree router; all major
HTTP routers (httprouter, chi, bunrouter) exhibit similar timing. See
"Route-Existence Timing Oracle (MM-2026-0026)" earlier in this file and
`reports/overview/2026-05-07-posture.md` (CDX-5) for the full analysis.

### JWT Subject Claim Validation (TM-2026-003)

`JWTAuth` does NOT validate the `sub` (subject) claim — it performs only
signature verification and (optionally) expiry checking. When a JWT is
verified as authentic, the `sub` claim value is **not** authoritative for
identifying the token's bearer; an attacker with access to a single valid
signing key can forge arbitrary `sub` values in new tokens. Authorization
logic must bind the token to the (issuer, subject) pair: do not authorise
a request based on `sub` alone, and do not assume that two tokens with the
same `sub` but different `iss` (issuer) claims belong to the same principal.
Extract both `iss` and `sub`, verify they match your expected issuer and
subject registry, and enforce additional checks (e.g. IP, user-agent, request
time) if needed (TM-2026-003, rmp #286).

### JWT Expiry Claim Handling (TM-2026-002)

`JWTAuth` treats `exp: null` (the JSON null value) and `exp: 0` as if the
`exp` claim is absent — the token is never expired by the JWT expiry check.
When `RequireExpiry: true` (the recommended production setting), a token
without an `exp` claim or with `exp` null/0 fails validation. When
`RequireExpiry: false` (the default, kept for backward compatibility), such
tokens pass expiry validation (though they may fail on other checks like issuer or
signature). Tokens created by non-standard JWT libraries or hand-crafted
edge cases must carry a numeric, positive Unix timestamp in the `exp` claim
for expiry validation to work correctly (TM-2026-002, rmp #287).

### JWT Mixed-Family Algorithms (TSC-2026-0003)

`JWTAuth` configured with HS\* and RS\*/ES\* algorithms in the same
`Algorithms` list leaks the algorithm code-path via response latency
(~25.2 µs HS256 vs RS256 latency difference, per
`reports/overview/2026-05-07-posture.md` CDX-5). An attacker submitting
tokens labelled with different `alg` values can determine which path the
server runs from the response time alone, narrowing the attack surface for
algorithm-confusion attacks (RFC 8725 §3.1). Configure each endpoint with
a single algorithm family. Mixed-family configuration emits a `slog.Warn`
at construction time.

### Recoverer Middleware Panic Logging (TM-2026-025)

`middleware.Recoverer` and `middleware.RecovererWithLogger` catch panics
and log, at Error level (via the slog default logger or the supplied
logger), the raw panic value and the full stack trace. A panic that
includes sensitive data (e.g. `panic("user_id=%d, api_key=%s", userID, key)`)
will be logged verbatim. Log sinks (files, centralized logging services,
SIEM systems) must be protected with appropriate access controls and
sanitisation rules. Do not use Recoverer as the sole defense against
information leakage in panic messages — validate and sanitise panic
recovery output at the log ingestion layer (TM-2026-025, rmp #286).

### SIEM Re-interpretation of Log Fields (TM-2026-032)

MuxMaster's `Logger` middleware writes one plain-text line per request
(`<time> <method> <path> <status> <duration>`) containing user-controlled
data: the request method and path. When either contains a control byte, a
non-ASCII byte, `"` or `\`, it is written with Go string-literal escaping
(`strconv.QuoteToASCII`, without the surrounding quotes), so a CR/LF in the
path appears as the literal two-character sequences `\r` and `\n`, never as
raw bytes. A SIEM or log parser that later unescapes these sequences may
re-interpret them as control characters. This is a log-consumer concern, not
a router defect. Operators must configure their log aggregation layer to
treat these escapes as text.
Test `TestSec_TM_2026_032_LoggerSanitises` confirms no raw CR/LF/TAB reaches
logs (2026-09-26) (TM-2026-032, rmp #122).

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

### Registration Panics Rollback Guarantee (MM-2026-0033)

If `Handle` (or any route registration method) panics — for example due to a
route conflict — the radix tree is guaranteed to remain unchanged. The two-phase copy-on-write implementation (path copying instead of full tree cloning) ensures that any mutation panicking mid-insertion rolls back atomically. **Do not catch and ignore registration panics.** Let them crash `main()` during development — the tree's consistency is protected, but the error is a sign of a bug (route conflict, invalid pattern, etc.). The rollback guarantee is enforced by test `TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched` in the test suite.

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

Up to four layers may catch a panic. From outermost to innermost:

| Layer | Frame | Catches |
|---|---|---|
| `net/http` per-connection recover | around the whole handler | anything that escapes every layer below; logs "http: panic serving ..." and closes the connection |
| `Mux.PanicHandler` (when set) | deferred in `Mux.ServeHTTP` | panics from `Pre` middleware and from dispatch (`Use` / `UseFast` middleware, handlers of both route types), unless an inner layer catches them first |
| `RecovererWithLogger` registered with `Pre` | around every `Pre` middleware registered after it and the dispatch | panics from those `Pre` middleware, from dispatch and from both route types |
| `RecovererWithLogger` registered with `Use` | around the `Handle` routes registered after it | panics from the `Use` middleware registered after it and from those handlers |

A panic is caught by the **innermost** recovering frame that encloses it.
Consequently, with a `Pre`-registered `RecovererWithLogger`, a panic in a
handler is handled by the Recoverer (plain 500) and `PanicHandler` never
sees it; `PanicHandler` then only receives panics raised by `Pre`
middleware registered before the Recoverer.

**Boundary rules.**

1. `Mux.PanicHandler` MUST NOT panic. Its frame is outside every
   MuxMaster-owned recovery, including a `Pre`-registered Recoverer, so a
   secondary panic propagates to `net/http`, which closes the connection
   mid-response. There is no goroutine leak and no process crash, but the
   client sees a reset stream — confusing for HTTP/2 multiplexing and
   reverse-proxy retries.
2. For uniform recovery across stdlib and FastHandler routes, either set
   `Mux.PanicHandler` (it covers both route types and `Pre` middleware)
   or register `RecovererWithLogger` as the first `r.Pre(...)` middleware.
   A `Use`-registered Recoverer cannot cover `HandleFast` routes (see
   CSA-2026-0054).
3. Choose one primary mechanism. If you configure both, decide which one
   owns the 500 response: the innermost wins.

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

**Exception — Asterisk-form `OPTIONS * HTTP/1.1`:** Auth gates registered
in `Pre()` do not see asterisk-form OPTIONS requests by default, because
`net/http` intercepts and answers them before `Mux.ServeHTTP` is called
(under the default server configuration `DisableGeneralOptionsHandler ==
false`). This is NOT exploitable: `net/http`'s response is fixed (200 OK,
`Content-Length: 0`, no `Allow` header) and route-independent. To route
these requests through MuxMaster and apply `Pre()` middleware to them, set
`http.Server.DisableGeneralOptionsHandler = true`.

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
