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
| [`basic`](#basic) — see `rest-api` | — | — | — |
| [`max-performance`](max-performance/) | Every opt-in stacked: `PoolRequestBundle` + `PoolFastParams` + `Pre` + `HandleFast` + `/bench` endpoint that measures live speed-up | ✅ | ⭐⭐⭐ |
| [`rest-api`](rest-api/) | Full bookstore REST API: groups, sub-groups, every HTTP method, regex params, validation, `HandlerFuncE`, middleware composition, error handling | ✅ | ⭐⭐ |
| [`versioning`](versioning/) | Path-based (`/v1/`, `/v2/`) + header-based (`Accept: ...;v=N`) API versioning with nested groups + admin gate | ✅ | ⭐⭐⭐ |
| [`server-sent-events`](server-sent-events/) | SSE streaming endpoint — pool-safe because handler stays alive for the whole stream | ✅ | ⭐⭐ |
| [`upload-file`](upload-file/) | Multipart file upload showing the **body-drain-before-spawn** pattern that makes goroutines pool-safe | ✅ | ⭐⭐⭐ |
| [`reverse-proxy`](reverse-proxy/) | `httputil.ReverseProxy` mounted on MuxMaster with round-robin + per-route gating; safe under Pool because the proxy returns before `ServeHTTP` exits | ✅ | ⭐⭐ |
| [`websocket`](websocket/) | gorilla/websocket chat hub; **intentionally avoids Pool** to teach when retention crosses the safe boundary | ❌ (deliberate) | ⭐ |
| [`graceful-shutdown`](graceful-shutdown/) | `http.Server.Shutdown` integration with SIGINT/SIGTERM | ✅ | ⭐ |
| [`authn`](authn/) | Multiple auth strategies: `BasicAuth`, API key, JWT chain | ✅ | ⭐ |
| [`jwt`](jwt/) | JWT issuance + verification middleware | ✅ | ⭐ |
| [`oauth2`](oauth2/) | OAuth2 provider integration | ✅ | ⭐ |
| [`cache`](cache/) | `Cache-Control` headers + ETag pattern | ✅ | ⭐ |
| [`server-side-render`](server-side-render/) | `html/template` rendering with per-page parsed templates | ✅ | ⭐ |
| [`static-site`](static-site/) | Static-file serving via `ServeFiles` with compression + CORS | ✅ | ⭐ |

**Pool-safe column:** ✅ means the example is compatible with `Mux.PoolRequestBundle = true` (and many of these examples enable it). ❌ marks examples where Pool would introduce a use-after-free risk (currently only `websocket`, because of `Hijack()`'s ownership transfer).

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

[`reverse-proxy/`](reverse-proxy/) — `httputil.ReverseProxy` returns to the caller before `ServeHTTP` exits, so it is naturally pool-safe. The Rewrite hook mutates the request URL inline (which is part of the recycled bundle — that mutation is local to this dispatch and discarded on Put).

### "I upgrade to a long-lived protocol (WebSocket, gRPC over HTTP/2)"

[`websocket/`](websocket/) — DO NOT enable Pool. After Hijack(), the underlying TCP connection lives independently of `*http.Request`, and any reference held by the upgrade library or your own code becomes a use-after-free against the recycled bundle. The example uses the default `Handle` path (~108 ns / 1 alloc) — irrelevant cost in front of the microsecond-scale WebSocket handshake.

### "I have a versioned API with shared middleware"

[`versioning/`](versioning/) — Group + Sub-group composition has ZERO per-request cost: middleware is wrapped at registration time, not per dispatch. A 3-level-deep `/api/v2/admin/users/:id` route dispatches at the same cost as a flat `/users/:id`.

---

## See also

- [docs/max-performance.md](../docs/max-performance.md) — the full guide to the lifetime contract, the audit checklist, and recipes
- [docs/performance.md](../docs/performance.md) — measurement methodology and benchmark numbers
- [`reports/perf-audit-2026-05-12/2026-05-12-competitor-showdown.md`](../reports/perf-audit-2026-05-12/2026-05-12-competitor-showdown.md) — full comparison against `httprouter`, `bunrouter`, `chi`, `gorilla/mux`, `fiber`
