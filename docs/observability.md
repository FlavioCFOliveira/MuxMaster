# Observability

MuxMaster ships with three observability primitives in its `middleware`
package — an access-log `Logger`, a `RequestID` middleware that attaches an
`X-Request-ID` header to every response, and `RecovererWithLogger`, which
logs panics through `log/slog`. Everything else (metrics, tracing,
profiling) is intentionally **operator-supplied**: the router exposes the
hooks, and you bring the backend (Prometheus, OpenTelemetry, Datadog,
etc.). This page documents the recommended integration patterns.

## Why no built-in metrics or tracing?

The router runs in many wildly different deployments:
high-throughput edge proxies, internal microservices, CLI-served
admin UIs. A built-in Prometheus exporter would force a dependency on
`github.com/prometheus/client_golang` (violating MuxMaster's zero-deps
invariant); a built-in OpenTelemetry SDK would have the same problem.
By keeping the surface to `http.Handler` middleware, you can plug any
observability stack with a thin middleware of your own — with no
abandoned defaults to migrate away from later.

## Access logging

`middleware.Logger(out io.Writer)` writes one plain-text line per request
to `out` after the handler returns. It does not use `log/slog`. The format
is:

```
<time RFC 3339> <method> <path> <status> <duration>
2026-04-17T10:05:31Z GET /users/42 200 1.243ms
```

The method and path are sanitised before they are written, so control
characters in the request cannot forge log lines. The status is the final
status code (a 1xx informational response is not logged as the status).
`Logger` panics if `out` is `nil`.

```go
import (
    "log/slog"
    "os"

    "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

mux := muxmaster.New()
mux.Pre(middleware.RequestID())                 // attach X-Request-ID first
mux.Use(middleware.Logger(os.Stdout))           // access log line per request
mux.Use(middleware.RecovererWithLogger(logger)) // panics logged through slog
```

For structured (JSON) access logs, or extra fields such as a tenant ID
or response size, write your own middleware that wraps
`http.ResponseWriter` and emits a `slog` event — see
[Request logging with structured output](cookbook.md#request-logging-with-structured-output).

## Request correlation

`middleware.RequestID` generates a 16-byte random ID from `crypto/rand`
per request, encoded as 32 lowercase hexadecimal characters, sets it as
the `X-Request-ID` response header, and stores it in the request context.

```go
mux.Pre(middleware.RequestID())

mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
    slog.InfoContext(r.Context(), "user lookup",
        "request_id", middleware.GetRequestID(r.Context()),
        "user_id", muxmaster.PathParam(r, "id"),
    )
})
```

If a client supplies its own `X-Request-ID`, the middleware propagates it
only if it is 1–128 characters of ASCII letters, digits, `-`, `_` or `.`;
any other value is replaced with a freshly generated ID. Register
`RequestID()` with `Pre(...)` so the ID exists before any other middleware
logs the request.

## Custom metrics middleware (Prometheus pattern)

Label metrics by the matched route pattern, never by the raw URL, which
would create one series per unique path. `muxmaster.RoutePattern(r)` is
not enough for this: it returns `""` for static routes and for the 404,
405 and redirect responses (it is set only for routes with parameters).
The reliable approach is to attach the pattern when you register the
route:

```go
import (
    "net/http"
    "strconv"
    "time"

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

// instrument wraps h with metrics labelled by the route pattern, which is
// known here at registration time for every route, static or not.
func instrument(method, pattern string, h http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
        h.ServeHTTP(rec, r)
        reqCount.WithLabelValues(method, pattern, strconv.Itoa(rec.status)).Inc()
        reqDuration.WithLabelValues(method, pattern).Observe(time.Since(start).Seconds())
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

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
```

Register routes through the wrapper and expose `/metrics`:

```go
mux.Handle(http.MethodGet, "/orders/:id", instrument(http.MethodGet, "/orders/:id", http.HandlerFunc(getOrder)))
mux.Handle(http.MethodGet, "/metrics", promhttp.Handler())
```

A small helper of your own that calls `mux.Handle(method, pattern,
instrument(method, pattern, h))` removes the repetition. `Mux.Routes()`
lists every registered pattern if you want to pre-create the label
values at start-up.

## Distributed tracing (OpenTelemetry pattern)

The same middleware-injection model applies to OpenTelemetry. Register the
tracing middleware with `Pre` so the span covers the entire dispatch,
including `HandleFast` routes, 404, 405 and redirects:

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
            ctx, span := tracer.Start(ctx, "HTTP "+r.Method)
            defer span.End()
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

mux.Pre(Tracing(otel.Tracer("api"), otel.GetTextMapPropagator()))
```

`Pre` middleware runs before routing, so `RoutePattern(r)` is always
`""` there; the span starts with a method-only name. To name it after the
route, rename it from a wrapper attached at registration, as with metrics:

```go
func traced(pattern string, h http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        trace.SpanFromContext(r.Context()).SetName(r.Method + " " + pattern)
        h(w, r)
    }
}

mux.GET("/items/:id", traced("/items/:id", getItem))
```

Never use the raw URL as the span name, for the same cardinality reason
as metrics. `RequestID` and tracing are independent; if you need both,
log the request ID and the trace ID together.

## Health checks

Health endpoints can be `HandleFast` routes, which `Use` middleware
never wraps. Register them **before** any `Use` call: registering a
`HandleFast` route on a `Mux` that already has `Use` middleware panics.

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

These routes skip the `Use(...)` chain, so they stay reachable even if
an authentication or throttling middleware registered with `Use` is
failing. `Pre` middleware still runs for them.

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

Do not use `Mount` for pprof: it strips the prefix, and `pprof.Index`
needs the full `/debug/pprof/` path.

`Mux.Routes()` lists every registered route, both `Handle` and
`HandleFast` (`Walk` visits only `Handle` routes and `WalkFast` only
`HandleFast` routes). It is useful for an internal admin endpoint:

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
mux.Pre(middleware.RequestID())                    // X-Request-ID
mux.Pre(middleware.RecovererWithLogger(logger))    // panic safety net
mux.Pre(middleware.RealIP(&trustedProxyCIDR))      // before throttle

// Use — runs INSIDE dispatch on stdlib (Handle) routes.
mux.Use(middleware.Timeout(5 * time.Second))
mux.Use(middleware.ThrottlePerIP(100, time.Second, nil))
mux.Use(middleware.Logger(os.Stdout))
```

Register `HandleFast` routes (such as the health checks above) before the
`Use` calls, and attach metrics per route with `instrument`.

See [`examples/graceful-shutdown`](../examples/graceful-shutdown/) for a
self-contained program demonstrating signal-driven shutdown, the
recommended `http.Server` timeouts, and a cooperative handler that
yields to context cancellation.

## Related reading

- [Performance](performance.md) — measured throughput and allocation
  profile under realistic load.
- [Middleware](middleware.md) — full reference for built-in
  middleware, including `Logger`, `RequestID`, `RecovererWithLogger`,
  `Timeout`, and the `Pre` / `Use` / `UseFast` scopes.
- [`SECURITY.md`](../SECURITY.md) — operator-required defaults
  (`http.Server` timeouts, `RealIP` trusted CIDRs, `JWTAuth`
  `RequireExpiry`, OAuth2 HTTPS endpoint).
