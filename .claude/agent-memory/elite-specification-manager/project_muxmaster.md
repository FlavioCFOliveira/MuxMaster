---
name: MuxMaster project identity and constraints
description: Core identity, non-negotiable design principles, Go version, and package layout for MuxMaster
type: project
---

MuxMaster is a high-performance HTTP router (request multiplexer) for Go. Pure stdlib, zero external dependencies, forever.

**Why:** The project's identity is defined by these three constraints: zero deps, 100% net/http compatibility, and performance parity with httprouter/bunrouter.

**How to apply:** Any proposed feature or change must be evaluated against these constraints before being added to the specification. A feature that requires an external import is out of scope by definition.

## Non-negotiable design principles

1. Zero external dependencies. Stdlib only.
2. 100% net/http compatible. Middleware type is `func(http.Handler) http.Handler`. No custom context type. No custom handler signature.
3. Performance first. Target: match or beat httprouter and bunrouter in ns/op and allocs/op.
4. Idiomatic Go. Zero-values are useful. Small interfaces.
5. Zero overhead when optional features are disabled.

## Go version

Minimum: Go 1.26 (declared in go.mod). Uses `for i := range n` (Go 1.22+) and `min`/`max` builtins (Go 1.21+).

## Package layout (target)

- `mux.go` — Mux struct, ServeHTTP, HTTP methods, configuration
- `tree.go` — Radix tree (addRoute, getValue, regex support)
- `params.go` — Param, Params, pool, type helpers
- `group.go` — Group, Route, Mount, With
- `handler.go` — HandlerFuncE, HTTPError, error adapters
- `response.go` — JSON, XML, Text, Redirect, NoContent
- `middleware/` — stdlib middleware sub-package

## Performance targets (AMD Ryzen 9 5900HX reference)

| Case | ns/op | allocs/op |
|---|---|---|
| Static route | ≤ 150 | 0 |
| 1 param | ≤ 200 | 0 |
| 5 params | ≤ 300 | 0 |
| Catch-all | ≤ 150 | 0 |
| Parallel static | ≤ 80 | 0 |
