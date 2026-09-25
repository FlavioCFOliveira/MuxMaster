# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **HTTP QUERY method (RFC 10008)** — first-class support for the QUERY method standardised by RFC 10008 (June 2026). QUERY is a safe, idempotent method like GET, but carries request content in the body like POST. Supports `Mux.QUERY`, `Mux.QUERYE`, `Mux.QUERYFast`, `Group.QUERY`, and `Group.QUERYE`. Included in the `ANY` method set. The router performs no Content-Type or body validation; responsibility is the handler's, per RFC 10008 §2. Default redirect code for `RedirectTrailingSlash` and `RedirectFixedPath` on QUERY routes is 307 (preserves method and body).

- **`MethodQuery` constant** — defined in muxmaster because Go 1.27's `net/http` does not yet define `http.MethodQuery` (tracked by golang/go#80058). The constant value is guaranteed to be `"QUERY"` and will remain equal to any future `http.MethodQuery` added by the Go project. Previously, attempting to register a route with `Handle("QUERY", ...)` panicked with "unsupported HTTP method 'QUERY'"; this panic is now eliminated.

- **Allow header includes QUERY** — the `Allow` header in 405 Method Not Allowed and automatic OPTIONS responses now includes QUERY when applicable. Order: GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, OPTIONS.

### Changed

- **`Mux.ANY` and `Group.ANY` now register QUERY** — routes registered via `ANY` now also match QUERY requests (RFC 10008), in addition to GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, and TRACE. This is an observable behaviour change: previously, QUERY requests to a path registered only via `ANY` would receive 405 Method Not Allowed (if `HandleMethodNotAllowed=true`) or 404 Not Found (if false). Now they are matched and handled.

- **Performance: `ThrottlePerIP` and `ThrottlePerIPCapped`** — sharded the internal rate-limit table from a single global `sync.Mutex` to 64-way per-shard mutexes (selected by `hash/maphash`), with an atomic global entry counter keeping the `maxTableSize` cap exact. Eliminates anti-scaling at high core counts. Measured at 16 logical CPUs: **4.68× faster** (2114 ns → 451 ns/op), scales correctly above 4 cores instead of anti-scaling. Closes CH-01 / rmp #244.

- **Performance: `ThrottleBacklog`** — replaced the per-request channel lock with a lock-free CAS-based fast path. Channel is now used only for the backlog-wait slow path. At 16 cores: **−13% ns/op** (76.15 ns → 66.23 ns); at 1 core: **−42% ns/op** (39.12 ns → 22.76 ns). Exact global limit maintained; multi-core cost reflects the cache-coherence floor of atomic counter contention. Closes CH-02 / rmp #244.

- **Performance: `RequestID`** — batched `crypto/rand` reads via a `sync.Pool` of 4 KiB buffers, rewriting the allocation strategy to fuse the context node, hex-digit buffer, and response-header backing array into a single allocation. Allocations reduced from 7 to 2 per request (−71.4%). Measured performance improvement at different core counts: **~4.7× at cpu=1**, **~2.8× at cpu=4**, **~1.95× at cpu=16**. Closes CH-05 / rmp #245.

- **Performance: `OAuth2Introspect` cache eviction** — changed eviction from O(n) full-table scan to O(log n) min-heap-based soonest-expiry selection when the cache is at capacity. Benchmark at cache saturation: **76× faster at cpu=1** (247.7 µs → 3.27 µs), **168× at cpu=4** (257.2 µs → 1.53 µs), **120× at cpu=16** (257.4 µs → 2.15 µs). Closes CH-09 / rmp #246.

- **Performance: `mux.go` redirect path** — snapshot the middleware chain into `redirectMWPtr` (an atomic pointer refreshed by `Use()`) and read it lock-free in `serveRedirect`, eliminating the unconditional `m.mu.RLock()` call on every redirect request. No measurable ns/op change on synthetic benchmarks (RWMutex was already cheap for reader-only access), but removes a reader-count atomic operation from the hot path. Closes CH-06 / rmp #247.

- **Documentation: `RequestID` middleware reference** — rewritten to clarify context-based retrieval via `middleware.GetRequestID()`, explain inbound header validation (MM-2026-0011: ASCII alphanumeric plus `-`, `_`, `.`; max 128 characters), and document the 2-allocation budget. Updated `docs/middleware.md` with correct function call form and validation rules. Added high-concurrency scaling subsection to `docs/max-performance.md` with measured data at 1/4/16 cores, explaining the allocation-driven GC and runtime lock pressure mechanism.

### Fixed

- **Documentation corrected: custom HTTP methods are not supported** (rmp #262, sprint 19): `README.md`, `docs/routing.md`, and specification have been corrected to reflect the verified behavior — MuxMaster recognizes a fixed, closed set of eleven method tokens (the ten standard HTTP methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY, plus the internal `"*"` token used by `Mount`), and registering any other method string (e.g., `PURGE`, `PROPFIND`) via `Handle`, `HandleFunc`, `HandleE`, `HandleFast`, or `Match` panics with `"muxmaster: unsupported HTTP method '<method>'"`. No `RegisterMethod` function exists. The router provides no mechanism to extend the method set at runtime. Custom-method requests can be served by attaching a `Mount` at a prefix and dispatching on `r.Method` within the mounted handler. Previously, documentation incorrectly claimed support for custom methods. Regression tests added in `method_set_test.go` pin all related specification rules and panic messages.

- **Documentation corrected: Mount's internal catch-all parameter now named accurately** (rmp #241, sprint 20): `docs/routing.md` line 276 now correctly describes `Mount("/api", handler)` as internally registered via `Handle("*", "/api/*mux_mount", ...)` instead of the anonymous `"/*"` description; specification `groups.md` rule 28 has been updated accordingly.

### Performance (Sprint 18 — Waste-Hunt Campaign)

Measured on AMD Ryzen 9 5900HX under load with real example workloads (`reports/perf-lab-2026-09-24/waste-hunt/`).

#### Root package and routing

- **Registration cost reduced from O(tree size) to O(depth per route)** (rmp #253, WH-08): path copy-on-write replaces deep cloning. `RegisterRoutes`: N=100 **−93.96%** (884 µs → 53 µs), N=1000 **−99.29%** (90.6 ms → 0.65 ms), N=5000 **−99.84%** (2.86 s → 4.7 ms). Registration now scales linearly with path depth, not tree size. **Specification amendment:** `specification/performance.md` §36–37 updated to reflect O(depth) copying and rollback guarantee.

- **`Mount`, `ServeFiles`, `Group.ServeFiles`, `CleanPath`, `StripSlashes` shallow request copy** (rmp #250, WH-04): replaced `r.Clone()` with struct-value copy + new `*url.URL` (matches `http.StripPrefix` strategy). Measured: `Mount` **−76.25%** (1024 ns → 243 ns), 9 → 3 allocs; `CleanPath` (dirty path) **−83.74%** (810 ns → 132 ns), 7 → 2 allocs. Shares header map and context with original request; header mutations are visible to outer middleware (by design, matches stdlib idiom).

- **Redirect path caching + fused bundle** (rmp #248, #250, WH-10): cached middleware-wrapped redirect handler, carrying per-request target via fused `redirectBundle` allocation (models `reqBundle` pattern). Measured: `RedirectTSL` **−27.67%** (884 ns → 639 ns), `RedirectTSLWithMiddleware` **−24.52%** (1050 ns → 792 ns), 19 → 12 allocs (−36.84%). Parallel case: concurrent `Use()` calls now safe via generation-tagged cache invalidation (fixes ABA race).

#### Middleware

- **`ThrottlePerIP` fast-path tokenisation** (rmp #251, WH-01): non-blocking `select` before timer creation; entry recycling via `sync.Pool`. Measured: **−97.09%** (4051 ns → 118 ns), 5 → 0 allocs. Syscalls reduced: 2.2 → 0.07 `clock_gettime` per request (HPET host; counts are host-independent).

- **`Logger` alloc-free formatting + single clock read** (rmp #251, WH-02, WH-07): `AppendQuoteToASCII` into pooled buffer; one end-of-request `time.Now()` for both timestamp and duration; `io.ReaderFrom` delegation to preserve `sendfile` fast path. Measured: **−24.03%** (7.5 µs → 5.7 µs), 5 → 0 allocs per request; `sendfile` syscalls preserved (3 per 1 MiB file with Logger, unchanged vs without).

- **`Compress` pooled writer with fixed sniff array** (rmp #251, WH-03): `gzipResponseWriter` recycled via `sync.Pool`, carrying an embedded `[8192]byte` array. Measured: chunked 12 KiB **−44.88%** (14.6 µs → 8.1 µs), 16 → 3 allocs; small 600 B **−17.97%** (435 ns → 356 ns), 4 → 2 allocs.

- **`JWTAuth` header memo + zero-copy HMAC input** (rmp #251, WH-06): single-entry cache of last-accepted JOSE header (stored after alg allow-list and RFC 7515 crit checks); `unsafe` string view (`unsafe.Slice(unsafe.StringData(...))`) for HMAC input, eliminating copy. Measured: **−14.03%** (5.16 µs → 4.44 µs), 10 → 7 allocs.

- **`RealIP` right-to-left `X-Forwarded-For` scan** (rmp #251, WH-11): replaced `strings.Split`-based rightmost-untrusted hop walk with right-to-left byte scan. Measured: 3-hop **−21.66%** (257 ns → 201 ns), 2 → 1 allocs; 1-hop **−14.79%** (195 ns → 166 ns).

- **`APIKey` fused context node** (rmp #251, WH-12): single allocation combining context wrapper and identity string (matches `RequestID` pattern). Measured: **−14.96%** (574 ns → 488 ns), 7 → 6 allocs. TSC-2026-0008 timing-equality constraint preserved unchanged.

- **405 / OPTIONS `Allow` header table + prebuilt slices** (rmp #250, WH-09): method bitmask index into pre-computed `Allow` strings; direct map assignment for header. Measured (post-aliasing-fix): `MethodNotAllowed` **−9.74%** (153 ns → 138 ns), 2 → 3 allocs; `OPTIONSAuto` **−51.01%** (124 ns → 61 ns), 3 → 1 allocs. Allocations include per-request header-slice isolation (see "Fixed" below).

- **Header-slice aliasing defect fix** (rmp #250, security review): WH-05 and WH-09 hoisted constant header **values** (`[]string`) instead of just strings, sharing slices across requests. Five affected code paths: `response.go` `JSON`/`XML`/`Text`, `mux.go` `lazyMethodNotAllowed`/`lazyOPTIONS`, `middleware/set_header.go`, `middleware/cors.go`, `middleware/no_cache.go`. Fixed by allocating header slices fresh per request. Post-fix alloc costs: `Text` **−41.39%** (98 ns → 58 ns, 2 → 1 allocs net), `JSONHelper` **−2.73%** (500 ns → 487 ns, 3 allocs unchanged but B/op restored to baseline).

#### Routing behaviour

- **Lookup fallback: static branch to param sibling** (rmp #259, DIV-001): when a static path segment exists alongside a param segment (e.g., `/users/list` and `/users/:id`), requests to a non-existent static segment (e.g., `/users/listx`) now match the param route instead of returning 404. Both static and param registration orders work correctly.

- **Mount bare prefix and TSR** (specification.md §28): a request to a bare mount prefix (e.g., `/v2` without trailing slash) now triggers `RedirectTrailingSlash` (when enabled, the default) to `/v2/` before the mounted handler receives the request. Requests to `/v2/` and `/v2/anything` match the mount directly. Catch-all routes (used internally by `Mount`) now participate in trailing-slash redirect logic.


### Fixed

- **Compress middleware first-WriteHeader-wins now exempts 1xx informational responses** (rmp #254): corrected a pre-existing defect where a 1xx code (e.g., 103 Early Hints) followed by a final status (e.g., 403) caused the final status to be silently dropped; the client received an implicit **200 OK** with the full body. Added 1xx exemption matching `net/http`'s own behaviour.

- **Logger middleware 1xx status handling** (rmp #254): corrected a pre-existing defect where 1xx informational codes in `WriteHeader` calls were recorded in the access log instead of the final status. Client-visible response was always correct; only the logged status was wrong, hiding security-relevant codes from log monitoring.

- **Compress and Logger now expose optional HTTP interfaces** (rmp #254): both middlewares now implement `http.Flusher` (delegating to underlying writer) and `Unwrap() http.ResponseWriter` (for `http.ResponseController` and other interface-aware tools). `Logger` additionally implements `io.ReaderFrom` to preserve the `sendfile`/`splice` fast path for file serving.

- **Static routes can now be registered after sibling param routes** (rmp #256): both registration orders — `mux.GET("/books/:id", ...)` then `mux.GET("/books/featured", ...)`, or vice versa — now succeed without panic. Both routes work correctly in either order. Catch-all vs static routes still conflict in both directions (as expected).

- **Trailing-slash redirects now work for catch-all routes and Mount bare prefixes** (rmp #255): corrected a pre-existing oversight where the dispatch path discarded the trailing-slash-redirect bit for catch-all routes. A request to `/api` (bare `Mount("/api", handler)` without trailing slash) now triggers a TSR redirect to `/api/` when `RedirectTrailingSlash` is enabled.

- **Redirect Location control bytes percent-encoded per RFC 9110** (rmp #260): control bytes (0x00–0x1F, 0x7F) in redirect target paths are now percent-encoded, conforming to RFC 9110 §5.5. Byte-identical differential testing against `net/http.Redirect` for 520+ cases.

- **Examples fixed for correct pool and routing behaviour** (rmp #255): five examples (`authn`, `cache`, `jwt`, `static-site`, `rest-api`) that panicked at startup now register routes correctly; `max-performance` pprof Mount now reachable; `static-site` asset and doc paths now serve correctly; `reverse-proxy` no longer crashes under load with `PoolRequestBundle=true` — documentation updated to explain the unsafe pattern.

- **Registration rollback guarantee strengthened** (rmp #253): tree copy-on-write with rollback now enforced by test `TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched` — if a registration panics mid-mutation, the live tree is guaranteed untouched. MM-2026-0033 statement updated from "may be inconsistent" to "full rollback guaranteed."

- **Documentation: specification corrected to match actual behaviour** (rmp #254): `specification/error-handling.md` clarified that `Use()`-registered middleware wraps `NotFound`, `MethodNotAllowed`, and auto-OPTIONS handlers (this was always the case in the code; the spec now matches reality). Cache invalidation on every `Use()` call (and `Rebuild()`). Applies to root `Mux` and all `Group` instances uniformly.

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
