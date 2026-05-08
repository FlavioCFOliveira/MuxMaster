# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.1] - 2026-05-08

Patch release. No functional, behavioural, or API changes — the public surface,
performance characteristics, and security guarantees of `v1.0.0` are preserved
in full. This release exists exclusively to clear cosmetic findings reported by
the [Go Report Card](https://goreportcard.com/report/github.com/FlavioCFOliveira/MuxMaster)
analysis on `v1.0.0` so adopters resolving the module via the Go module proxy
see a 100 % score on the published tag.

### Style

- **`gofmt -s` simplification across 53 files** — re-aligned `var()` block
  declarations, normalised numbered comment lists to godoc list style
  (`//   1.` → `//  1.`), and adjusted whitespace in struct/literal
  alignment. Affected files: `mux_test.go`, `middleware/oauth2.go`,
  `middleware/middleware_test.go`, and 50 files under `reports/*/harness/`
  used by the security audit harness suite. Diff: 490 insertions / 471
  deletions; zero token-level semantic differences (verified with
  `go vet`, `golangci-lint run` and the full test suite).

### Quality

- **Go Report Card now scores 100 % (A+)** — the 19 `gofmt -s` warnings
  reported on `v1.0.0` are cleared. `go_vet`, `gocyclo`, `ineffassign`,
  `license`, and `misspell` checks remain at 100 %.

## [1.0.0] - 2026-05-08

First general-availability release. The public API is now stable; subsequent
1.x releases are bound by the Semantic Versioning compatibility guarantees
documented in `COMPATIBILITY.md`. There are no breaking changes between
`v1.0.0-rc1` and `v1.0.0`.

### Security

- **OAuth2Introspect: redact endpoint URL in slog warning (TM-2026-005, sev 4)** —
  the construction-time `slog.Warn` issued when `AllowInsecureEndpoint=true` no
  longer logs the full endpoint URL. Only the resolved `host` and `scheme` are
  emitted, preventing query-string credentials (e.g. `?client_secret=...`) from
  leaking to slog sinks. `middleware/oauth2.go:229`.
- **SECURITY.md: add "Resolved Findings (v1.0.0)" section** enumerating
  CSA-2026-0060 (sev 8), HPS-2026-0005 (sev 7), FPE-2026-010 (sev 6), and
  TM-2026-005 (sev 4) with their fix locations so adopters can verify by ID
  that each issue is closed.
- **SECURITY.md: document operator-facing defaults requiring opt-in** —
  `JWTAuth.RequireExpiry`, `RealIP()` trusted CIDRs, and OAuth2 HTTPS-only
  endpoint are now listed in a single matrix.
- **Sprint S10 pre-release closure** — the `concurrency-security-auditor`,
  `middleware-security-reviewer`, `path-routing-fuzzer`, and
  `go-sast-and-memory-auditor` agents reran the full hypothesis battery
  against HEAD: 9/9 TM-CSA hypotheses REFUTED, 6/6 TM-MSR domains covered,
  fuzz harness (`prerelease_v100_test.go`) clean, SAST clean. Evidence
  archived under `/reports/<agent>/2026-05-08-*/`.

### Documentation

- **Add `docs/observability.md`** — structured logging with `slog`,
  `RequestID` correlation, custom Prometheus metrics middleware
  pattern, OpenTelemetry tracing pattern, health checks, and pprof
  integration. The router stays zero-dep; operators bring their own
  metrics/tracing SDK.
- **Add `examples/graceful-shutdown/`** — production-ready pattern
  demonstrating signal-driven `srv.Shutdown(ctx)` with bounded drain
  deadline, the recommended `http.Server` timeout set
  (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`),
  and a cooperative handler that yields to context cancellation.
- **README: add "Security defaults" section** consolidating the three
  unsafe-by-default middleware options (`JWTAuth.RequireExpiry`,
  `RealIP()` no CIDRs, `OAuth2Introspect.AllowInsecureEndpoint`) with
  the recommended hardened-stack snippet.
- **README: fix unsafe snippets** — the `Trust X-Forwarded-For` example
  now passes a trusted CIDR; the JWT example now sets
  `RequireExpiry: true`.
- **JWTAuth.RequireExpiry GoDoc** strengthened — explicit "DO NOT use
  in production" caveat on the `false` default plus pointer to
  RFC 8725 §4.4 (TM-2026-001).
- **`docs/README.md`: index Observability page** so adopters can find
  the operability guide from the documentation hub.

### Changed

- **`.gitignore`: ignore example binaries** (`examples/oauth2/oauth2`,
  `examples/server-side-render/server-side-render`,
  `examples/graceful-shutdown/graceful-shutdown`) to prevent accidental
  commits of build artefacts.

## [1.0.0-rc1] - 2026-05-08

First release candidate. Public API is considered stable; breaking
changes between rc1 and 1.0.0 will be enumerated in this changelog and
discussed in a GitHub issue before landing.

### Security

- **Sprint S9 audit closed** — 95 findings across 9 specialist domains
  (CSA-2026-0060 sev 8 silent params loss; HPS-2026-0005 sev 7 open
  redirect via absolute-form URI; FPE-2026-010 sev 6 silent
  middleware-skip on root `Mux.HandleFast`; plus JWT/OAuth2/APIKey
  hardening).

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

[Unreleased]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0-rc1...v1.0.0
[1.0.0-rc1]: https://github.com/FlavioCFOliveira/MuxMaster/releases/tag/v1.0.0-rc1
