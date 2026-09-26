# MuxMaster Examples

Each directory below is a self-contained Go module that demonstrates a specific feature, integration pattern, or performance recipe.

Every example follows the same structure:

```
examples/<name>/
├── go.mod    # local module with `replace ../..` to use the in-tree MuxMaster
└── main.go   # runnable program with `go run .`
```

To try one: `cd examples/<name> && go run .`

## Index

| Example | What it shows | Pool-safe? | Performance focus |
|---|---|:---:|---|
| [`max-performance`](max-performance/) | Every opt-in stacked: `PoolRequestBundle` + `PoolFastParams` + `Pre` + `HandleFast` + `/bench` endpoint that measures live speed-up | ✅ | ⭐⭐⭐ |
| [`rest-api`](rest-api/) | Full bookstore REST API: groups, sub-groups, all HTTP methods (including QUERY per RFC 10008), regex params, validation, `HandlerFuncE`, middleware composition, error handling | ✅ | ⭐⭐ |
| [`versioning`](versioning/) | Path-based (`/v1/`, `/v2/`) + header-based (`Accept: ...;v=N`) API versioning with nested groups + admin gate | ✅ | ⭐⭐⭐ |
| [`server-sent-events`](server-sent-events/) | SSE streaming endpoint — pool-safe because handler stays alive for the whole stream | ✅ | ⭐⭐ |
| [`upload-file`](upload-file/) | Multipart file upload showing the **body-drain-before-spawn** pattern that makes goroutines pool-safe | ✅ | ⭐⭐⭐ |
| [`reverse-proxy`](reverse-proxy/) | `httputil.ReverseProxy` mounted on MuxMaster with round-robin + per-route gating | ❌ | ⭐⭐ |
| [`graceful-shutdown`](graceful-shutdown/) | `http.Server.Shutdown` integration with SIGINT/SIGTERM | ✅ | ⭐ |
| [`authn`](authn/) | Two auth strategies with the built-in middleware: `BasicAuth` (with `ThrottlePerIP`) on `/admin`, `APIKey` on `/api` | ✅ | ⭐ |
| [`jwt`](jwt/) | Hand-rolled HS256 token issuance plus verification with the built-in `JWTAuth` | ✅ | ⭐ |
| [`oauth2`](oauth2/) | `OAuth2Introspect` (RFC 7662) with the hardened stack: HTTPS endpoint, `RealIP` with trusted CIDRs, bounded `ThrottlePerIP` | ✅ | ⭐ |
| [`cache`](cache/) | `Cache-Control` headers + ETag pattern | ✅ | ⭐ |
| [`server-side-render`](server-side-render/) | `html/template` rendering with per-page parsed templates | ✅ | ⭐ |
| [`static-site`](static-site/) | Static-file serving via `ServeFiles` with compression + CORS | ✅ | ⭐ |

**Pool-safe column:** ✅ means the example is compatible with `Mux.PoolRequestBundle = true`; `max-performance`, `server-sent-events`, `upload-file` and `versioning` enable it, and the others keep the default. ❌ means the example must NOT enable it. Pool is incompatible with any pattern that lets code read the request, its context, or (for `PoolFastParams`) the `Params` slice after `ServeHTTP` returns — most notably `Hijack()`-based upgrades (WebSocket, HTTP/2 server push), but also `net/http.Transport`-based reverse proxying: under concurrent load, `Transport` can start a background dial goroutine that reads the request context after the proxying handler has already returned (see [`reverse-proxy`](reverse-proxy/) below). See [`docs/max-performance.md`](../docs/max-performance.md) "Lifetime contract" for the full audit checklist.

**Performance focus column:**
- ⭐⭐⭐ — explicitly demonstrates pool opt-ins, lifetime contract, or measurement methodology
- ⭐⭐ — uses opt-ins but the focus is the feature, not the performance
- ⭐ — same default configuration as a typical Go application

---

## Performance recipes by example

### "I want the absolute fastest router setup"

Read [`max-performance/`](max-performance/) first. It enables every opt-in, mixes `Handle` and `HandleFast`, and exposes a `/bench` endpoint that runs an in-process benchmark on the user's hardware and reports the speed-up vs the default configuration.

### "I have a streaming endpoint"

[`server-sent-events/`](server-sent-events/) — Pool is safe here because the handler does not return until the stream ends. The recycled bundle is only returned to the pool after the SSE session closes, so there is no risk of mid-stream reuse.

### "I spawn goroutines from my handlers"

[`upload-file/`](upload-file/) — the `/async` endpoint shows the **only** safe pattern: drain every value the goroutine needs (body bytes, params, headers) into local variables BEFORE the `go func()` call. Never capture `r` itself in the goroutine.

### "I run a reverse proxy"

[`reverse-proxy/`](reverse-proxy/) — **do NOT enable `PoolRequestBundle` on a reverse-proxy handler.** `httputil.ReverseProxy`'s `RoundTrip` does return before `ServeHTTP` exits, but the `net/http.Transport` underneath it does not: under concurrent load, `Transport.startDialConnForLocked` can start a background dial goroutine that keeps calling `ctx.Value()` on the request's context after `RoundTrip` — and therefore `ServeHTTP` — has returned. With pooling on, that context belongs to a recycled, zeroed `reqBundle` by the time the goroutine reads it, producing a nil-pointer dereference and crashing the process under load. This example keeps pooling off on its gateway; see the crash evidence in `reports/perf-lab-2026-09-24/waste-hunt/results/defects/reverse-proxy-pool-crash.txt`. The fake backend server in the same example (`go run . backend <port>`) does not proxy and remains pool-safe.

### "I upgrade to a long-lived protocol (WebSocket, gRPC over HTTP/2)"

**Do NOT enable `PoolRequestBundle`.** After `Hijack()`, the underlying TCP connection lives independently of `*http.Request`, and any reference held by the upgrade library or your own code becomes a use-after-free against the recycled bundle. Use the default `Handle` path (one allocation per request; 118.5 ns for a 1-parameter route on the 2026-09-26 run, see [docs/performance.md](../docs/performance.md)) — a negligible cost next to the upgrade handshake.

### "I have a versioned API with shared middleware"

[`versioning/`](versioning/) — Group and sub-group composition adds no per-request cost: prefixes are joined and middleware is wrapped at registration time, not per dispatch. Lookup cost depends on the length of the request path, not on how deeply the groups that registered it were nested.

---

## See also

- [docs/max-performance.md](../docs/max-performance.md) — the full guide to the lifetime contract, the audit checklist, and recipes
- [docs/performance.md](../docs/performance.md) — measurement methodology and benchmark numbers
- [`reports/perf-audit-2026-05-12/2026-05-12-competitor-showdown.md`](../reports/perf-audit-2026-05-12/2026-05-12-competitor-showdown.md) — historical (2026-05-12, v1.1.0-era code) comparison against `httprouter`, `bunrouter`, `chi`, `gorilla/mux`, `fiber`; current figures are in [docs/performance.md](../docs/performance.md)
