# Observability

MuxMaster ships with two first-class observability primitives — a
structured `Logger` middleware (built on `log/slog`) and a `RequestID`
middleware that attaches an `X-Request-Id` header to every response.
Everything else (metrics, tracing, profiling) is intentionally
**operator-supplied**: the router exposes the hooks, and you bring the
backend (Prometheus, OpenTelemetry, Datadog, etc.). This page documents
the recommended integration patterns.

## Why no built-in metrics or tracing?

The router runs in many wildly different deployments:
high-throughput edge proxies, internal microservices, CLI-served
admin UIs. A built-in Prometheus exporter would force a dependency on
`github.com/prometheus/client_golang` (violating MuxMaster's zero-deps
invariant); a built-in OpenTelemetry SDK would have the same problem.
By keeping the surface to `http.Handler` and per-request `slog`
events, you can plug any observability stack with a thin middleware
of your own — with no abandoned defaults to migrate away from later.

## Structured logging (slog)

`middleware.Logger` writes one structured event per request. It uses
`log/slog` from the stdlib so the output format is controlled by your
default `*slog.Logger` (text vs JSON, level filter, custom handlers).

```go
import "log/slog"

logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)

mux := muxmaster.New()
mux.Pre(middleware.RequestID())              // attach X-Request-Id first
mux.Use(middleware.Logger(os.Stdout))         // logs method, path, status, duration
mux.Use(middleware.RecovererWithLogger(logger))
```

Each Logger event includes:

| Field      | Type     | Notes                                                    |
|------------|----------|----------------------------------------------------------|
| `time`     | RFC 3339 | Set by slog                                              |
| `level`    | string   | INFO for normal requests, WARN/ERROR for ≥500 responses  |
| `method`   | string   | Sanitised (CRLF-stripped — HPS-2026-0001)                |
| `path`     | string   | `r.URL.Path` (sanitised; param values are NOT included)  |
| `status`   | int      | Captured response status                                 |
| `duration` | string   | `time.Duration.String()` of the handler execution        |

If you need to add custom fields (tenant ID, user agent, response size)
write your own middleware that wraps `http.ResponseWriter` and emits a
`slog` event of its own — `Logger` is intentionally minimal so it
composes cleanly.

## Request correlation

`middleware.RequestID` generates a 16-byte cryptographically random ID
(crypto/rand) per request, attaches it as `X-Request-Id` on the
response, and stores it in the request context.

```go
mux.Pre(middleware.RequestID())

mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    rid, _ := middleware.GetRequestID(r.Context())
    slog.InfoContext(r.Context(), "user lookup",
        "request_id", rid,
        "user_id", muxmaster.PathParam(r, "id"),
    )
})
```

If a client supplies its own `X-Request-Id`, the middleware respects it
(after sanitisation). Pass `RequestID()` via `Pre(...)` so the ID is
attached BEFORE any other middleware logs the request.

## Custom metrics middleware (Prometheus pattern)

The recommended pattern is a single user-side middleware that captures
the per-route latency and increments counters. MuxMaster's
`Mux.Routes()` and `Mux.Walk()` introspection let you pre-register
counters at startup so the cardinality is bounded.

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
    reqCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "http_requests_total"},
        []string{"method", "route", "status"},
    )
    reqDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "route"},
    )
)

func init() {
    prometheus.MustRegister(reqCount, reqDuration)
}

// Metrics records one observation per request, keyed on the matched
// route pattern (NOT the raw URL — that would explode cardinality).
func Metrics(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        rec := &statusRecorder{ResponseWriter: w, status: 200}
        next.ServeHTTP(rec, r)
        route := muxmaster.RoutePattern(r) // bounded label cardinality
        reqCount.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
        reqDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
    })
}

type statusRecorder struct {
    http.ResponseWriter
    status int
}

func (r *statusRecorder) WriteHeader(code int) {
    r.status = code
    r.ResponseWriter.WriteHeader(code)
}
```

Register the metrics middleware via `Use` (it must observe the matched
route pattern, which is only available inside the dispatch frame). Then
expose `/metrics`:

```go
mux.Use(Metrics)
mux.GET("/metrics", promhttp.Handler().ServeHTTP)
```

`muxmaster.RoutePattern(r)` returns the matched pattern (e.g.
`/users/:id`) — using the raw `r.URL.Path` would create one Prometheus
series per unique URL and pin the metric server's heap.

## Distributed tracing (OpenTelemetry pattern)

The same middleware-injection model applies to OpenTelemetry. Wrap
`Pre` so the span boundary covers the entire dispatch (including
`HandleFast` routes):

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/trace"
)

func Tracing(tracer trace.Tracer, prop propagation.TextMapPropagator) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
            ctx, span := tracer.Start(ctx, muxmaster.RoutePattern(r))
            defer span.End()
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

mux.Pre(Tracing(otel.Tracer("api"), otel.GetTextMapPropagator()))
```

Operator notes:

- Span name uses `RoutePattern` (not raw URL) for the same cardinality
  reason as metrics.
- Place `Tracing` in `Pre`, BEFORE `RequestID`, so the trace ID is the
  source of truth and `RequestID` becomes a fallback only. If you need
  both (legacy callers without W3C trace headers), have your handler
  emit the OTel trace ID into the `X-Request-Id` response header so
  log-trace correlation works in either direction.

## Health checks

Add a couple of fast routes that bypass middleware:

```go
// /healthz returns 200 unconditionally — used by k8s liveness probes.
mux.GETFast("/healthz", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
    w.WriteHeader(http.StatusOK)
})

// /readyz returns 503 until startup is complete (e.g. DB pool warm).
mux.GETFast("/readyz", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
    if !ready.Load() {
        w.WriteHeader(http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
})
```

Health endpoints registered as `HandleFast` skip the `Use(...)` chain
entirely — useful so they remain reachable even if `JWTAuth` or
`ThrottlePerIP` is misbehaving.

## pprof / runtime introspection

The stdlib `net/http/pprof` package registers handlers on
`http.DefaultServeMux`; route them onto MuxMaster manually:

```go
import (
    "net/http/pprof"
)

// Mount pprof on a separate, internal-only Mux so it is NEVER exposed
// publicly. Bind to 127.0.0.1 or a private VPC interface.
debug := muxmaster.New()
debug.GET("/debug/pprof/",        pprof.Index)
debug.GET("/debug/pprof/cmdline", pprof.Cmdline)
debug.GET("/debug/pprof/profile", pprof.Profile)
debug.GET("/debug/pprof/symbol",  pprof.Symbol)
debug.GET("/debug/pprof/trace",   pprof.Trace)
debug.GET("/debug/pprof/:profile", pprof.Index) // heap, goroutine, …

go http.ListenAndServe("127.0.0.1:6060", debug)
```

`Mux.Routes()` and `Mux.Walk()` are also useful for an internal admin
endpoint that lists every registered route:

```go
debug.GET("/debug/routes", func(w http.ResponseWriter, _ *http.Request) {
    for _, route := range mainRouter.Routes() {
        fmt.Fprintf(w, "%-8s %s\n", route.Method, route.Pattern)
    }
})
```

## Putting it together

A production-ready stack typically looks like this:

```go
mux := muxmaster.New()

// Pre — runs OUTSIDE dispatch; covers Handle and HandleFast routes.
mux.Pre(Tracing(tracer, propagator))               // span boundary
mux.Pre(middleware.RequestID())                    // X-Request-Id
mux.Pre(middleware.RecovererWithLogger(logger))    // panic safety net
mux.Pre(middleware.RealIP(&trustedProxyCIDR))      // before throttle

// Use — runs INSIDE dispatch on stdlib (Handle) routes.
mux.Use(middleware.Timeout(5 * time.Second))
mux.Use(middleware.ThrottlePerIP(100, time.Second, nil))
mux.Use(middleware.Logger(os.Stdout))
mux.Use(Metrics)                                   // your custom Prometheus mw
```

See [`examples/graceful-shutdown`](../examples/graceful-shutdown/) for a
self-contained program demonstrating signal-driven shutdown, the
recommended `http.Server` timeouts, and a cooperative handler that
yields to context cancellation.

## Related reading

- [Performance](performance.md) — measured throughput and allocation
  profile under realistic load.
- [Middleware](middleware.md) — full reference for built-in
  middleware, including `Logger`, `RequestID`, `Recoverer`,
  `Timeout`, and the four scopes (`Pre`, `Use`, group, per-route).
- [`SECURITY.md`](../SECURITY.md) — operator-required defaults
  (`http.Server` timeouts, `RealIP` trusted CIDRs, `JWTAuth`
  `RequireExpiry`, OAuth2 HTTPS endpoint).
