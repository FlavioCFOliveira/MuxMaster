# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] - 2026-05-12

Minor release focused on **maximum performance**. Three deep-audit
optimisations (O10, O12, O13), a full FastHandler pool integration, and a
sustained three-sprint perf push (S14/S15/S16) bring MuxMaster to the
fastest stdlib-compatible HTTP router in the Go ecosystem: **45 ns / 0 B /
0 allocs** on 1-parameter routes via the opt-in `Mux.PoolRequestBundle`
(20 % faster than `httprouter` with zero allocations on the `http.Handler`
path). The public API is fully backward-compatible with `v1.0.x`: every
new capability is gated behind an opt-in flag with a strict, documented
lifetime contract. No breaking changes.

### Added

- **`Mux.PoolRequestBundle` opt-in (Opt O13)** — recycles the per-request
  `reqBundle` (tiered: 1/2/3+ parameters) via three matched `sync.Pool`s,
  eliminating the single 384/416/480 B allocation on the stdlib
  `http.Handler` path. When enabled, the entire hot path becomes
  zero-allocation. Strict lifetime contract: handlers MUST NOT retain
  `*http.Request` past return. Default `false`; full documentation in
  `docs/max-performance.md` and `mux.go:239–266`. (`6cc0686`)
- **`Mux.PoolFastParams` opt-in (Opt O9)** — three tier-matched
  `sync.Pool`s recycle the `Params` slice handed to `FastHandler` routes.
  Default `false` preserves the previously documented goroutine-safe
  lifetime. Pools store `*[N]Param` (pointer-to-array, not
  pointer-to-slice) to keep `Put` zero-alloc. (`3cf1a44`)
- **Five Gin-parity examples** under `/examples` — `rest-api`,
  `versioning`, `reverse-proxy`, `server-sent-events`, `server-side-render`
  — plus a curated index at `examples/README.md`. Each example is
  realistic, well-commented, and matches the corresponding Gin idiom
  one-for-one. (`e657868`)
- **Runnable Maximum Performance example** at `examples/max-performance/`
  demonstrating `Mux.PoolRequestBundle = true` end-to-end. (`8558bfb`)
- **`docs/max-performance.md`** — the canonical guide for the
  zero-allocation hot path, with the lifetime contract, the failure
  modes, and the benchmark evidence. (`8558bfb`)
- **`gorilla/mux` competitor benchmark** added to the apples-to-apples
  bench suite under `competitor/bench_test.go`. (`cb80b83`)
- **Static-only fast path in `getValue`** — `getValueStatic` skips param
  bookkeeping for routes known at registration time to contain zero
  wildcards. (`18287ef`)

### Changed

- **`requestCtx1` / `requestCtx2` slimmed (Opt O12)** — the `params Params`
  field is dropped from both layouts; the slice is now derived from
  `small[:N]` at access time. `reqBundle1` drops 416 → 384 B and
  `reqBundle2` drops 448 → 416 B, landing each one in the next-smaller GC
  size class. (`6cc0686`)
- **Direct `dispatchParams1`/`dispatchParams2` calls (Opt O10)** — the
  `doDispatch1`/`doDispatch2` function-pointer indirection is gone. The
  `if hasReqCtxField` branch is inlined into a single dispatcher,
  restoring branch prediction and inlining-budget headroom for the
  compiler. Zero API surface change. (`6cc0686`)
- **Direct unsafe `r.ctx` write in param dispatch** — the param dispatch
  fast path writes the request context via the reflected offset of the
  private `ctx` field of `http.Request`, avoiding the
  `r.WithContext(ctx)` allocation; automatic fallback to `WithContext` if
  the offset is not found via reflection in a future Go release.
  (`474f672`)
- **`url.URL` allocation dropped on the redirect path** in
  `RedirectTrailingSlash` / `RedirectFixedPath`; the redirect handler
  also bypasses `wrapMiddleware`, which was redundant. (`2b09d37`)
- **Pre-built default 405 response** — `Method Not Allowed` is now
  served from a pre-rendered, immutable `[]byte` buffer, eliminating
  the per-request `http.Error` allocations on the 405 path. (`97bc6c1`)
- **`Logger` middleware** — `statusRecorder` recycled via `sync.Pool`,
  `fmt.Fprintf` replaced with `strconv.Append*` to drop the `%d` /
  `%s` format-string overhead. (`031c5a5`)
- **Pre-canonical header keys + direct map assignment** in
  `RequestID`, `RealIP`, and `SetHeader` middlewares to skip the
  `textproto.MIMEHeader.canonicalMIMEHeaderKey` per-call work. (`7a6d39c`)
- **Redundant `children` slice header dropped in `getValue` walk
  loop** — the slice header was being re-read on every iteration even
  when the inner branch was statically determined. (`39a619d`)
- **Inline 1-parameter dispatch** — `dispatchWithParams` is bypassed for
  the most common REST-API case (single path parameter), saving the call
  overhead. (`09eae8c`)

### Performance

Internal benchmarks (`bench_test.go`, AMD Ryzen 9 5900HX, Go 1.26):

| Case                       | v1.0.1     | v1.1.0 default | v1.1.0 Pooled |
|----------------------------|------------|----------------|---------------|
| Static route               | 27 ns / 0 B | 25.1 ns / 0 B | 25.1 ns / 0 B |
| 1-parameter route          | 110 ns / 416 B / 1 alloc | 105 ns / 384 B / 1 alloc | **49.6 ns / 0 B / 0 allocs** |
| 2-parameter route          | 124 ns / 448 B / 1 alloc | 119 ns / 416 B / 1 alloc | **55.9 ns / 0 B / 0 allocs** |
| 3-parameter route          | 138 ns / 480 B / 1 alloc | 135 ns / 480 B / 1 alloc | **58.6 ns / 0 B / 0 allocs** |
| Catch-all                  | 112 ns / 384 B / 1 alloc | 108 ns / 384 B / 1 alloc | **43.9 ns / 0 B / 0 allocs** |
| Parallel 1-parameter route | 105 ns / 384 B / 1 alloc | 100 ns / 384 B / 1 alloc | **6.3 ns / 0 B / 0 allocs** |
| Fast 1-parameter route     | 51 ns / 32 B / 1 alloc | 50.3 ns / 32 B / 1 alloc | n/a |

Competitive benchmarks (`competitor/bench_test.go`, 1-parameter route,
same harness, same machine):

| Router                        | ns/op | B/op | allocs/op |
|-------------------------------|-------|------|-----------|
| **MuxMaster Pooled (Opt O13)**| **45** | **0** | **0** |
| MuxMaster default             | 108   | 384  | 1         |
| MuxMaster Fast                | 50    | 32   | 1         |
| httprouter                    | 56    | 64   | 1         |
| Fiber v3 (fasthttp stack)     | 212   | 0    | 0         |
| bunrouter (vendored fork)     | 183   | 192  | 3         |
| chi v5                        | 354   | 304  | 4         |
| gorilla/mux                   | 3 444 278 | n/a | 156 015 |

- **Sprint S14/S15/S16 consolidated benchmarks** captured in
  `bench_test.go` and reproduced under `reports/perf-audit-2026-05-12/`
  for adopters who want to verify the gains independently. (`1164f60`,
  `7aaf977`, `bfbd66b`)
- **Fiber v3 results refreshed** to the latest stable release of the
  competitor suite. (`9f8c5aa`)

### Documentation

- **`docs/max-performance.md`** — new exhaustive guide covering the
  zero-allocation hot path, the `PoolRequestBundle` and `PoolFastParams`
  contracts, the failure modes when the contract is broken, and the
  benchmark methodology. (`8558bfb`)
- **`examples/README.md`** — curated index of all twelve runnable
  examples with a one-line summary for each. (`e657868`)
- **Five Gin-parity example READMEs** explaining the *why*, the *what*,
  and the *runnable command* for each example. (`e657868`)
- **Competitor showdown report** under `reports/competitor/` documenting
  MuxMaster's wins category-by-category. (`97cb4f5`)
- **Router variable standardised to `mux`** across every doc snippet
  (was inconsistent `r`/`router`/`m`); fixes a shadowing bug in the
  README quick-start. (`4bf3cd6`)
- **Throttle IP-churn cap test de-flaked under QEMU emulation** so the
  CI matrix is fully green on every supported runner. (`7827183`)

### Notes

- **Pre-existing harness flakiness, unrelated to release.** Running
  `go test -race ./reports/concurrency-security-auditor/harness/`
  in parallel mode can produce two timing-sensitive failures
  (`TestTimeoutMW_SlowHandler_ContextCancelled` and
  `TestTimeoutMW_GoroutineLeak`) because `runtime.NumGoroutine()` is
  polluted by sibling `t.Parallel()` tests in the same package. The
  failures are in the audit harness (not in production code), they are
  not introduced by this release (last touched in commits `5f804fa` and
  `e744b23`, both already on `main` at `v1.0.1`), and they reproduce
  cleanly when the same suite is run with `-p 1 -parallel 1`. A
  follow-up patch release will reorganise the harness to isolate the
  `runtime.NumGoroutine()`-based assertions.

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

[Unreleased]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0-rc1...v1.0.0
[1.0.0-rc1]: https://github.com/FlavioCFOliveira/MuxMaster/releases/tag/v1.0.0-rc1
