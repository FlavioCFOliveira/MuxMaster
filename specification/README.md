# MuxMaster Functional Specification

**Version:** 1.0
**Status:** Active
**Date:** 2026-04-16

---

## Purpose

This directory is the single source of truth for all behavior of the MuxMaster project. All implementation decisions must conform to the requirements stated here. Any discrepancy between the code and this specification is a defect in the code unless a specification revision has been explicitly initiated.

No feature may be implemented without a corresponding specification entry.

---

## Project Overview

MuxMaster is a high-performance HTTP router (request multiplexer) for Go. It is implemented in pure Go using only the standard library. Zero external dependencies are permitted at any time.

The central algorithm is a radix tree (also called a Patricia trie). Route lookup is O(k) where k is the length of the request path.

MuxMaster implements the `http.Handler` interface. It is a drop-in replacement for `net/http.ServeMux` and is compatible with every piece of Go HTTP middleware and tooling that accepts `http.Handler`.

---

## Terminology

The following terms are used consistently throughout all specification files. When a term defined here appears in a specification file, it carries precisely this meaning.

| Term | Definition |
|---|---|
| **Router** | The `*Mux` value that holds the route tree and configuration. Also referred to as "the mux". |
| **Route** | A registered combination of an HTTP method, a path pattern, and a handler. |
| **Pattern** | The path string used when registering a route (e.g., `/users/:id`). Patterns may contain static segments, named parameters, and a catch-all parameter. |
| **Handler** | A value that implements `http.Handler` or a function with the signature `func(http.ResponseWriter, *http.Request)`. |
| **Middleware** | A function with the signature `func(http.Handler) http.Handler`. It wraps a handler to add behavior before or after it executes. |
| **Segment** | A slash-delimited component of a URL path. In `/users/123/posts`, the segments are `users`, `123`, and `posts`. |
| **Named parameter** | A path segment prefixed with `:` in a pattern (e.g., `:id`). It captures one non-slash segment. |
| **Catch-all parameter** | A path segment prefixed with `*` in a pattern (e.g., `*filepath`). It captures the rest of the path including slashes. It must appear at the end of the pattern. |
| **Regex parameter** | A path segment using the `{name:expr}` syntax. It captures one non-slash segment only when the value matches the regular expression `expr`. |
| **Static route** | A route whose pattern contains no named parameters, regex parameters, or catch-all parameters. |
| **Group** | A `*Group` value that prefixes all its routes with a common path and applies a shared middleware stack. |
| **Scope** | The set of routes to which a middleware applies. The four scopes are: pre-routing, global, group, and per-route. |
| **Registration time** | The moment `Handle`, `HandleFunc`, or a convenience method is called. Middleware is applied at registration time, not at request time. |
| **Request time** | The moment `ServeHTTP` is called for an incoming HTTP request. |
| **TSR** | Trailing Slash Redirect. A redirect issued when a route exists at the alternate trailing-slash path. |
| **Fixed path** | A path produced by `path.Clean` that differs from the original but has a registered handler. Used by the `RedirectFixedPath` feature. |
| **Allow header** | The `Allow` HTTP response header listing the HTTP methods registered for a given path. Used in 405 and OPTIONS responses. |
| **Pool** | The `sync.Pool` used to recycle `Params` slices and avoid per-request allocation during route lookup. |

---

## Design Principles

These principles are non-negotiable. They constrain all implementation decisions. If a proposed feature violates any principle, the feature must be redesigned or rejected.

1. **Zero external dependencies.** Only the Go standard library is permitted. Any feature that requires a third-party package is explicitly out of scope or must be made pluggable via an interface.
2. **100% net/http compatibility.** The middleware type is `func(http.Handler) http.Handler`. The router implements `http.Handler`. No custom context type, no custom handler signature. Any existing Go HTTP middleware works without adaptation.
3. **Performance first.** Every design decision weighs impact on ns/op and allocs/op. The target is to match or beat `httprouter` and `bunrouter`. Features that add per-request allocation or overhead when disabled are not acceptable.
4. **Idiomatic Go.** Follows Go naming conventions. Zero-values are useful where possible. Interfaces are small. No magic.
5. **Zero overhead when optional features are disabled.** Features controlled by boolean flags or nil handlers must have zero cost at request time when they are not configured.

---

## Specification Files

| File | Contents |
|---|---|
| [routing.md](routing.md) | Path syntax, HTTP methods, route registration, matching, precedence, and panics |
| [params.md](params.md) | `Param` type, `Params` type, type-conversion helpers, `RoutePattern`, and pool behavior |
| [middleware.md](middleware.md) | Middleware type, scopes (pre-routing, global, group, per-route), and execution order |
| [groups.md](groups.md) | `Group`, `Route` (inline group), `Mount`, and sub-groups |
| [error-handling.md](error-handling.md) | `NotFound`, `MethodNotAllowed`, `PanicHandler`, `GlobalOPTIONS`, `HandlerFuncE`, `HTTPError`, and `ErrorHandler` |
| [configuration.md](configuration.md) | All `Mux` fields, their default values, and their behavior when toggled |
| [introspection.md](introspection.md) | `Lookup`, `Routes`, `Walk`, and `RoutePattern` |
| [static-files.md](static-files.md) | `ServeFiles` behavior and constraints |
| [response-helpers.md](response-helpers.md) | `JSON`, `XML`, `Text`, `Redirect`, and `NoContent` |
| [middleware-stdlib.md](middleware-stdlib.md) | Each middleware in the `muxmaster/middleware` sub-package |
| [compatibility.md](compatibility.md) | Go version requirements and net/http ecosystem compatibility |
| [performance.md](performance.md) | Performance targets, measurement methodology, and what affects performance |
| [out-of-scope.md](out-of-scope.md) | Features that will never be implemented and the reason for each |

---

## Implementation Status Notation

Specification requirements are written as obligations, not as status flags. Each requirement states what the system must do regardless of whether it is currently implemented. The current codebase may not yet satisfy all requirements. That is expected and acceptable during active development.

The test suite and any automated conformance tooling determine whether the implementation satisfies a given requirement.

---

## Package Structure

The target package structure for the full implementation is:

```
muxmaster/
├── mux.go          # Mux struct, ServeHTTP, HTTP method helpers, configuration
├── tree.go         # Radix tree: addRoute, getValue, regex support
├── params.go       # Param, Params, pool, type-conversion helpers
├── group.go        # Group, Route (inline), Mount, With
├── handler.go      # HandlerFuncE, HTTPError, error adapters
├── response.go     # JSON, XML, Text, Redirect, NoContent
├── middleware/
│   ├── logger.go
│   ├── recoverer.go
│   ├── request_id.go
│   ├── real_ip.go
│   ├── timeout.go
│   ├── compress.go
│   ├── basic_auth.go
│   ├── cors.go
│   ├── throttle.go
│   ├── no_cache.go
│   ├── strip_slashes.go
│   ├── clean_path.go
│   ├── set_header.go
│   └── with_value.go
├── mux_test.go
├── bench_test.go
├── go.mod
└── README.md
```
