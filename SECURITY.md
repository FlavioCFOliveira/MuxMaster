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

## Thread-Safety Contract (MM-2026-0017)

All public `Mux` fields (`PanicHandler`, `NotFound`, `MethodNotAllowed`,
`RedirectTrailingSlash`, `RedirectFixedPath`, etc.) **must be set before the
first call to `ServeHTTP`**. Mutating these fields after the server starts
serving is a data race and produces undefined behaviour. This mirrors the
contract of `net/http.Server`.

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

### BasicAuth Brute-Force (MM-2026-0027)

`middleware.BasicAuth` does not rate-limit authentication attempts. Compose it
with `middleware.ThrottlePerIP` to mitigate online brute-force:

```go
r.Use(
    middleware.ThrottlePerIP(10, time.Second, nil),
    middleware.BasicAuth("realm", creds),
)
```

### BREACH Compression Oracle (MM-2026-0030)

`middleware.Compress` does not mitigate the BREACH attack. If your application
reflects user-controlled input alongside secret values in the same response
body **and** compression is enabled, an attacker may be able to recover the
secret via a compression oracle. Mitigations: disable compression for sensitive
endpoints, or ensure secrets and reflected input are never in the same response.

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
