# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Radix tree router with O(k) lookup (k = path length)
- Named path parameters (`:id`), regex-constrained parameters (`{id:[0-9]+}`), and catch-all parameters (`*filepath`)
- `Mux.Use` — global middleware (applied at registration time, zero per-request overhead)
- `Mux.Pre` — pre-dispatch middleware (runs before routing)
- `Mux.Group` / `Mux.Route` — path prefix groups with independent middleware stacks
- `Mux.With` — inline middleware scoping without a prefix
- `Mux.Mount` — sub-router mounting with automatic prefix stripping
- `Mux.ServeFiles` — static file serving
- `Mux.Match` — register a handler for multiple methods at once
- `Mux.ANY` — register a handler for all standard HTTP methods
- `Mux.HandleE` / shorthand `GETE`, `POSTE`, etc. — error-returning handler variant
- `Mux.Lookup` — programmatic route lookup for testing and introspection
- `Mux.Walk` / `Mux.Routes` — iterate all registered routes
- `PathParam` / `ParamsFromContext` / `RoutePattern` — typed path parameter access
- `Params.Int`, `Params.Int64`, `Params.Uint64`, `Params.Float64`, `Params.Bool` — typed parameter parsing
- `RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`, `HandleOPTIONS` — production-safe defaults
- `CaseInsensitive`, `UseRawPath`, `UnescapePathValues`, `RedirectCode` — opt-in options
- Custom `NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`, `PanicHandler`, `ErrorHandler`
- `middleware` sub-package: Logger, Recoverer, CORS, BasicAuth, Compress, Throttle, Timeout, RequestID, RealIP, CleanPath, StripSlashes, NoCache, SetHeader, WithValue, APIKey, JWTAuth, OAuth2Introspect
- Response helpers: `JSON`, `XML`, `Text`, `Redirect`, `NoContent`
- 100% compatible with `net/http` — implements `http.Handler`
- Zero external dependencies
- `FastHandler` / `FastMiddleware` — fast-path handler and middleware types that bypass the standard `http.Handler` chain; intended for trusted internal routes where stdlib middleware overhead is unacceptable
- `Mux.HandleFast` / `Mux.UseFast` — register `FastHandler` routes and `FastMiddleware` chains
- Convenience methods `GETFast`, `POSTFast`, `PUTFast`, `PATCHFast`, `DELETEFast`, `HEADFast`, `OPTIONSFast`, `CONNECTFast`, `TRACEFast` (and `Group` equivalents)
- `Rebuild()` — resets the frozen configuration snapshot; intended for tests that change Mux flags after first use
- Authentication middleware: `APIKey` (SHA-256 hashed key lookup), `JWTAuth` (HS*/RS*/ES* token validation), `OAuth2Introspect` (RFC 7662 introspection with caching)

### Fixed
- **JWT compliance (RFC 7515 §4.1.11)** — tokens with a `"crit"` header field are now rejected; support for critical extensions is not implemented
- **ECDSA key validation (RFC 7518 §3.4)** — JWT middleware now validates curve selection at construction time (ES256→P-256, ES384→P-384, ES512→P-521); panics on misconfiguration
- **Bearer scheme case-insensitivity (RFC 7235)** — `JWTAuth` and `OAuth2Introspect` now match the Authorization header scheme case-insensitively ("bearer", "Bearer", "BEARER")

### Performance
- Zero allocations for static routes; single tiered allocation (416–480 B) for parameterized routes, fusing the request context and `*http.Request` copy into one GC-class-aligned object
- **Tiered reqBundle allocations** — `reqBundle1` (416 B, 1 param), `reqBundle2` (448 B, 2 params), `reqBundle` (480 B, 3+ params); reduces B/op by 13–35 % vs. a single fixed-size bundle
- **Configuration snapshot** — Mux flags are frozen into a `muxConfig` snapshot on the first `ServeHTTP` call; subsequent requests use a single atomic pointer load instead of 6–8 struct field reads
- **FastHandler footprint** — `FastHandler` struct reduced to 32 B (from 128 B) via exact `Params` slice allocation bounded by `maxParams = 3`

[Unreleased]: https://github.com/FlavioCFOliveira/MuxMaster/commits/main
