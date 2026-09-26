# MuxMaster Documentation

Welcome to the MuxMaster documentation. Use the table below to navigate to the topic you need.

## Guides

| Guide | Description |
|-------|-------------|
| [Getting Started](getting-started.md) | Build your first application step by step: hello world, path parameters, middleware, groups, JSON responses, and error handling |
| [Routing](routing.md) | Route patterns, the ten supported HTTP methods (including QUERY, RFC 10008), priority and conflict rules, trailing-slash and fixed-path redirects, and custom methods through `Mount` |
| [Middleware](middleware.md) | How middleware works, the `Pre` / `Use` / `UseFast` scopes and which route types each wraps, group and per-route middleware, writing custom middleware, and the built-in middleware reference |
| [Groups](groups.md) | Organizing routes with shared prefixes and middleware, nesting groups, inline `Route` blocks, `With` scoping, and mounting sub-routers |
| [Error Handling](error-handling.md) | `HandlerFuncE`, `HTTPError`, custom error handlers, 404/405 handlers, and panic recovery |
| [Configuration](configuration.md) | Every `*Mux` option and handler field with its default, semantics and example, and how the configuration snapshot and `Rebuild` work |
| [Response Helpers](response-helpers.md) | `JSON`, `XML`, `Text`, `Redirect`, `NoContent` |
| [Performance](performance.md) | How MuxMaster minimises allocations, the 2026-09-26 benchmark results (AMD Ryzen 9 5900HX, Go 1.27.0) against httprouter, bunrouter, chi and gorilla/mux, the changes since v1.1.0, and historical results |
| [**Maximum Performance Guide**](max-performance.md) | **Configure `PoolRequestBundle`, `PoolFastParams` and `HandleFast` for zero-allocation dispatch. On the 2026-09-26 run, a 1-parameter route with `PoolRequestBundle` took 46.7 ns with 0 allocations against httprouter's 50.5 ns with 1 allocation. Includes the handler lifetime contract, audit checklist and runnable recipes.** |
| [Observability](observability.md) | Access logging, request correlation, custom Prometheus metrics, OpenTelemetry tracing, health checks, pprof, and route introspection |
| [Migration Guide](migration.md) | Step-by-step migration from gorilla/mux, chi, httprouter and `net/http.ServeMux` |
| [Cookbook](cookbook.md) | Ready-to-use patterns: REST API structure, JWT auth, validation, pagination, graceful shutdown, CORS for SPAs, testing, and more |

## Quick Links

- [pkg.go.dev API reference](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster)
- [GitHub repository](https://github.com/FlavioCFOliveira/MuxMaster)
- [CHANGELOG](../CHANGELOG.md)
- [CONTRIBUTING](../CONTRIBUTING.md)
