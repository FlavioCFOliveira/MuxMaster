# MuxMaster Documentation

Welcome to the MuxMaster documentation. Use the table below to navigate to the topic you need.

## Guides

| Guide | Description |
|-------|-------------|
| [Getting Started](getting-started.md) | Build your first application step by step: hello world, path parameters, middleware, groups, JSON responses, and error handling |
| [Routing](routing.md) | Complete reference for route patterns, HTTP method helpers, priority rules, trailing slash behaviour, and path normalization |
| [Middleware](middleware.md) | How middleware works, all four scopes (global, pre-routing, group, per-route), writing custom middleware, and the full built-in middleware reference |
| [Groups](groups.md) | Organizing routes with shared prefixes and middleware, nesting groups, inline `Route` blocks, `With` scoping, and mounting sub-routers |
| [Error Handling](error-handling.md) | `HandlerFuncE`, `HTTPError`, custom error handlers, 404/405 handlers, and panic recovery |
| [Configuration](configuration.md) | Every `*Mux` field with its default value, semantics, and example |
| [Response Helpers](response-helpers.md) | `JSON`, `XML`, `Text`, `Redirect`, `NoContent` |
| [Performance](performance.md) | How MuxMaster achieves zero allocations, benchmark results, and comparison notes against httprouter, bunrouter, and chi |
| [Migration Guide](migration.md) | Step-by-step migration from gorilla/mux, chi, httprouter, and `net/http.ServeMux` |
| [Cookbook](cookbook.md) | Ready-to-use patterns: REST API structure, JWT auth, validation, pagination, graceful shutdown, CORS for SPAs, testing, and more |

## Quick Links

- [pkg.go.dev API reference](https://pkg.go.dev/github.com/FlavioCFOliveira/MuxMaster)
- [GitHub repository](https://github.com/FlavioCFOliveira/MuxMaster)
- [CHANGELOG](../CHANGELOG.md)
- [CONTRIBUTING](../CONTRIBUTING.md)
