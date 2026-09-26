# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.3.0] - 2026-09-26

Minor release. It raises the minimum Go version to 1.27.1, fixes
ephemeral-port exhaustion in the default `OAuth2Introspect` client, applies
the `ServeFiles` raw-path guard to `Group.ServeFiles`, and repairs the CI,
commitlint and release workflows. The exported API is identical to v1.2.0
(`apidiff`: no changes in the root or `middleware` package). Read the upgrade
notes in
[`release-notes/v1.3.0-20260926.md`](https://github.com/FlavioCFOliveira/MuxMaster/blob/v1.3.0/release-notes/v1.3.0-20260926.md#upgrade-notes)
before upgrading.

### Changed

- **Minimum Go version raised from 1.26 to 1.27.1** (rmp #306): every module now declares `go 1.27.1`. Under [COMPATIBILITY.md](COMPATIBILITY.md#go-version-policy), raising the minimum Go version is a MINOR change, hence v1.3.0. Programs built with Go 1.26 or Go 1.27.0 must upgrade their toolchain. With the default `GOTOOLCHAIN=auto` (Go 1.21 or later), the `go` command switches to Go 1.27.1 or newer automatically, downloading it when no matching toolchain is in `PATH`; with `GOTOOLCHAIN=local`, the build fails with an error stating that the module requires `go >= 1.27.1`.

### Fixed

- **`OAuth2Introspect`: the default introspection client no longer exhausts ephemeral ports under load** (rmp #300): when `HTTPClient` is nil, the default introspection client now uses its own transport. It copies `http.DefaultTransport`'s settings once, at construction, without modifying or initialising `http.DefaultTransport`. It keeps connections alive and allows at most 100 connections to the introspection host (exact for HTTP/1.1; HTTP/2 may open more). Calls beyond that limit wait for a free connection within the 10 s timeout. This fixes ephemeral-port exhaustion under concurrent load, which rejected valid tokens with 401 (notably on Windows). Later changes to `http.DefaultTransport` no longer affect the middleware, so construct the middleware at startup. Set `HTTPClient` to use other settings. Regression tests: `TestOAuth2Introspect_DefaultClient_BoundsConnectionsUnderConcurrency` (`middleware/oauth2_concurrency_test.go`), `middleware/oauth2_transport_test.go`, and `middleware/oauth2_transport_internal_test.go`, whose reflection test fails when a Go release adds an `http.Transport` field that the copy does not classify.

- **gosec findings resolved without behaviour change** (rmp #301): the three findings that failed CI were false positives. `tree.go` saturates `maxParams` with an explicit `math.MaxUint8` check (registration time only; `TestMaxParamsSaturatesAtUint8Max`); the redirect body in `mux.go` escapes its target with `html.EscapeString`, which applies the same mapping as `net/http`'s private replacer, so the body stays byte-identical to `net/http.Redirect` (`TestWriteRedirect_ByteIdenticalToNetHTTPRedirect`); the intentional `int64`-to-`uint64` conversion in `Logger`'s duration formatting carries a scoped justification.

- **Test suite: skips replaced by assertions** (rmp #303): no test outside `reports/` and `competitor/` calls `t.Skip`. `FuzzTSRRedirectSafety` now checks empty, oversized, and `http.NewRequest`-rejected paths; the differential, order-independence, and `reqBundle` tests assert instead of skipping. This also fixes a latent integer-negation overflow in the order-independence test.

### Security

- **`Group.ServeFiles` now applies the raw-path guard** (CDX-S8-002 / PRF-2026-0002, rmp #302): `Mux.ServeFiles` refuses to register, with a panic, when the `Mux` has both `UseRawPath` and `UnescapePathValues` enabled, because the captured path could then contain decoded `/` characters that `http.FileServer` treats as separators. `Group.ServeFiles` skipped that check, although the specification states that both variants are equivalent. Both now call one shared check. Regression test: `TestGroupServeFiles_RawPathUnescapeGuard` (`group_servefiles_test.go`), covering option order, sub-groups, and the configurations that must not panic.

### CI and Build

- **`ci.yml`** (rmp #304): runs on pushes to `main`, `develop`, `release/**`, and `hotfix/**`, as well as on pull requests against `main`. The `CHANGELOG.md updated` gate no longer fails with `SIGPIPE` under `pipefail`, and now also runs on push. `apidiff` runs on every push as an advisory check and still blocks pull requests. gosec v2.29.0 is installed with the `go.mod` toolchain, because the gosec action images cannot load a Go 1.27.1 module. The test matrix covers Go 1.27.1 and `stable`; the linux/arm64 job uses the `golang:1.27.1` image.
- **`commitlint.yml`**: also validates the commits of pushed ranges, and exempts merge commits by parent count.
- **`release.yml`**: extracts the release notes inside the release job and passes them by file, because GitHub masked the job output and left the v1.2.0 release body empty; tests the requested tag on manual runs; drops the unused `id-token` permission.
- **Actions updated**: `actions/checkout` v7.0.1, `actions/setup-go` v7.0.0, `golangci/golangci-lint-action` v9.3.0.
- **golangci-lint v2.14.0** in CI and in the pre-push hook, as required by the new `go` directive.
- **`dependabot.yml`**: drops entries without third-party dependencies, covers the three security-harness modules that have them, and uses commit prefixes that pass commitlint.
- **Auxiliary modules** (not part of the published module, which still has zero external dependencies): `golang.org/x/net` v0.59.0 and `golang.org/x/text` v0.42.0 in the HTTP/2 harness; `github.com/go-chi/chi/v5` v5.3.2 and `github.com/stretchr/testify` v1.12.1 in the routing-fuzzer harness and `competitor/`, which was re-vendored so it builds and benchmarks the current code. Three examples (`max-performance`, `reverse-proxy`, `upload-file`) were reformatted with `gofmt`.

### Documentation

- **Specification** (rmp #305): `routing.md` §16 (rules 110–114) specifies the remaining registration panics — an unclosed `{/:`, an invalid UTF-8 pattern, a `{name}` token without `:`, a `*` not preceded by `/` — and that a panicking registration leaves the route tree unchanged. `groups.md` rule 6 no longer claims that `Group` panics on a prefix without a leading `/`, and rule 45 specifies the panic for an invalid UTF-8 `Mount` prefix. `static-files.md` item 11 states that both `ServeFiles` variants panic with `UseRawPath` and `UnescapePathValues` set. `compatibility.md` specifies the Go 1.27.1 minimum and the toolchain-switching behaviour. `middleware-stdlib.md` specifies the `OAuth2Introspect` default HTTP client.
- **GoDoc, `api.md`, and `docs/middleware.md`**: `OAuth2Options.HTTPClient` and `OAuth2Introspect` document the default transport and the requirement to construct the middleware at startup; `Group.ServeFiles` documents the CDX-S8-002 panic.
- **Guides**: README, CONTRIBUTING, COMPATIBILITY, the guides under `docs/`, and the bug-report issue template state the Go 1.27.1 minimum; `.github/branch-protection.md` drops the rules that are incompatible with direct gitflow merges and states what branch protection can enforce.

## [1.2.0] - 2026-09-26

Minor release. It adds first-class support for the HTTP QUERY method
(RFC 10008), fixes routing, `Mount`, `Group` and middleware defects, hardens
redirects, `BasicAuth`, `CORS`, `OAuth2Introspect` and `Recoverer`, and
removes waste from registration and several middleware. The exported API is a
strict superset of v1.1.0 (`apidiff`: six additions, no incompatible changes).
Several fixes change observable behaviour; read the upgrade notes in
[`release-notes/v1.2.0-20260926.md`](https://github.com/FlavioCFOliveira/MuxMaster/blob/v1.2.0/release-notes/v1.2.0-20260926.md#upgrade-notes)
before upgrading.

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

- **Routing: parameter routes no longer panic for a request without a context** (rmp #292): a `*http.Request` built as a struct literal (common in tests and in code that calls `ServeHTTP` directly) has a nil internal context; Opt O5a's fast context read skipped `r.Context()`'s fallback to `context.Background()`, so `Value`, `Done`, `Deadline` or `Err` on the handler's context panicked on parameter, catch-all and Mount routes (exposed by the mount-prefix lookup added in rmp #281). The fast read now falls back to `context.Background()`. Allocations are unchanged; ns/op deltas are within the measured code-layout noise floor.

- **`CORS` now sends `Vary: Origin` on every response it produces or forwards** (rmp #291, TM-2026-033, sprint 20, `specification/middleware-stdlib.md` §16, rules 71-75) — **behaviour change:** previously, `Vary: Origin` was added only when the request carried an `Origin` header that matched a specific, non-wildcard entry in `AllowedOrigins`. Every other case — no `Origin` header at all, a wildcard `AllowedOrigins`, and CORS's own error responses (400 for a CR/LF/NUL-bearing `Origin`, 403 for an origin absent from a non-wildcard `AllowedOrigins`) — reached a downstream or intermediary HTTP cache with no `Vary: Origin`, so a cacheable response for a request without CORS relevance could later be served, unmodified, to a request from a different, CORS-relevant origin; the browser then blocks the reused response because it lacks the `Access-Control-Allow-Origin` value that origin's CORS check requires, even though the server would have supplied one for a request it actually saw (Fetch Standard §8.4, "CORS protocol and HTTP caches"). `CORS` now calls its Vary-adding logic unconditionally, as the first action in its handler, before the `Origin` header is even read — so it survives every exit path. `Access-Control-Allow-Origin` and every other `Access-Control-*` header are unchanged: a request without an `Origin` header still receives none of them. The addition uses `Header.Add` semantics — a separate `Vary:` value/field line, never merged into an existing comma-joined value — and is skipped when `Origin` is already present as a token (case-insensitively, RFC 9110 §5.1) in any existing `Vary` value, so composing `CORS` with `Compress` (which unconditionally adds `Vary: Accept-Encoding`) in either `Use()`-chain order still produces exactly one `Origin` token and one `Accept-Encoding` token, regardless of order. Measured impact (`benchstat -count=10`, AMD Ryzen 9 5900HX, HEAD vs. this change, `reports/perf-audit-2026-05-12/middleware_bench_test.go`): the previously zero-allocation "no `Origin` header" fast path now costs 1 allocation (112 B) and +45 ns absolute (25.7 ns → 71.0 ns) — the unavoidable cost of storing the new header value on a path that used to do no work at all; the allowed-origin and preflight hit paths are unaffected (3 allocs/op unchanged — 2 of those are a benchmark-harness artifact of resetting `httptest.ResponseRecorder.HeaderMap`, not `CORS`'s own allocation, which stays fused into the same request-scoped `[7]string` backing array as before) and measure 2-4% faster, within normal run-to-run noise. Regression tests: `middleware/cors_vary_origin_test.go` (mode × Origin-presence × preflight/simple/rejected/invalid matrix, existing-Vary case-insensitivity and combined-token-list handling, coexistence with `Compress` in both orders), `middleware/gap_o14_test.go::TestSec_CORS_NoOriginHeader_NoACAO` (extended with the `Vary` assertion), and `reports/http-protocol-security-auditor/harness/tm_reclass_s20_test.go::TestHPS_TM_2026_033_Regression_NonCORSResponseHasVaryOrigin` (converted from the pre-fix reproducer).

- **A named or regex parameter no longer captures an empty path segment** (rmp #283, sprint 20, `specification/routing.md` §12, requirements 97-101; `specification/params.md` rule 37) — **behaviour change:** previously, a doubled `/` in the request path could be matched as a zero-length parameter value: `/{id:[a-z]*}/profile` matched `//profile` (capturing `id = ""`), and `/:id/posts` matched `//posts` the same way, whenever the pattern's parameter was reached with an empty candidate segment — whether mid-path (e.g. `/:id/posts` against `//posts`) or at the pattern's last position (e.g. `/:id` against `//`). Neither matches now: `getValue` and `getValueBacktrack` (in `tree.go`) reject a zero-length segment for both the `param` and `regexParam` node types before capturing it — for a regex parameter, the check runs **before** the expression is evaluated at all, so an expression that would itself accept the empty string (e.g. `[a-z]*`) never gets the chance to. The rejection is a single `end == 0` branch tested against the segment boundary the existing scan loop already computes — no new scan, no new data dependency, placed at the earliest point correctness allows (before `params.add`, before the regexp is evaluated). Measured impact (`benchstat -count=10`/`-count=18` interleaved, AMD Ryzen 9 5900HX, HEAD vs. this change): allocations and bytes/op are bit-identical on every benchmark (0 change); ns/op moves within ±0–5% across the parameter/catch-all benchmarks, with a geomean of +0.7–1.5% depending on run — some routes measure faster, some slower, by 1–3 ns absolute on operations in the 45–150 ns range. This is consistent with ordinary binary-layout jitter between builds rather than a per-call cost of the branch itself; the check is already at the theoretically cheapest possible placement, since the emptiness of the segment cannot be known any earlier than the point where its boundary is first computed. This is distinct from the pre-existing terminal case (rule 11: `/users/:id` already did not match `/users/`, because no path remains at all after the parameter's boundary `/`) — rule 97 instead covers a *genuine* empty segment, reached with a non-empty remaining path whose next byte is itself `/`. Applies uniformly to `Handle`/`HandleFunc` routes, `HandleFast` routes, the case-insensitive matching path, and the bounded-backtracking retry path (`getValueBacktrack`) used when a static sibling route forces a fork. When rule 97 rejects a candidate and no other route matches, the request falls through to the ordinary 404/redirect sequence exactly as any other unmatched path: no trailing-slash redirect fires merely because of the rejection (it still requires its own independent condition — a registered handler at the path with its trailing `/` added or removed), and a fixed-path redirect (`RedirectFixedPath`) fires only when a handler is separately registered at `path.Clean` of the request path. Differential testing against `chi`, `httprouter`, and `bunrouter` (`reports/path-routing-fuzzer/harness`) shows the new behaviour aligns with `chi` (both now 404 on `/users//`); `httprouter` issues its own fixed-path redirect for the same input (unaffected by this change, as MuxMaster's default `RedirectFixedPath` remains `false`); `bunrouter`'s native API normalises the doubled slash before matching and is unaffected either way. Regression tests: `empty_segment_test.go` (named/regex parameter, mid-path/last-position, stdlib/`HandleFast`, case-insensitive, and the full set of rule-101 outcomes including the `RedirectFixedPath` + separately-registered `/profile` 301 case).

- **`Group` prefix, sub-group prefix, `Group.Mount`, and `Group.ServeFiles` no longer concatenate a duplicate `/` at the join boundary** (rmp #282, sprint 20, `specification/groups.md` §11, requirements 41-44; `specification/static-files.md` rule 2) — previously, `group.prefix + path` (and the equivalent for sub-group creation, `Group.Mount`, and `Group.ServeFiles`) was a plain, unconditional string concatenation: a group prefix ending in `/` combined with a route-local path beginning with `/` (e.g. `mux.Group("/api/")` then `api.GET("/users", ...)`) produced the pattern `/api//users` instead of the intended `/api/users` — a literal, generally unreachable double-slash route, not a normalization of the request path. All five join sites in `group.go` (`Group.Handle`, `Group.HandleFast`, `Group.Group`, `Group.Mount`, `Group.ServeFiles`) now go through a single unexported `joinPrefix` helper: exactly one of the two slashes is dropped when the left operand ends with `/` and the right operand begins with `/`; every other case (including when the right operand has no leading `/`, regardless of the left operand) is unchanged, plain concatenation — a route-local path is never given a `/` it did not already have. A repeated `/` that already exists strictly inside either operand, away from the join boundary, is left untouched, and a `%`-encoded sequence (including an encoded slash such as `%2f`) is treated as ordinary literal text, never decoded or special-cased. The join runs at registration time, before `Handle`'s own path-validation panic, so a pattern that still does not begin with `/` after joining continues to panic exactly as before. Regression tests: `group_join_internal_test.go` (a white-box truth table for `joinPrefix` covering all four requirements) and `group_join_test.go` (end-to-end registration/dispatch coverage for trailing-slash prefixes, nested sub-groups, `Group.Mount`, `Group.ServeFiles`, a literal inner `//` preserved, and the pre-existing "path without a leading `/` still panics" invariant).

- **`Mount`: an inner `*Mux`'s own automatic redirects now carry the mount prefix; a Mount prefix ending in an optional parameter now panics at registration; `RawPath` is now propagated correctly through a parameterized Mount prefix** (rmp #281, sprint 20, `specification/groups.md` §§8-10, requirements 31-40):
  - **Location rewriting through Mount.** When the handler passed to `Mount` is itself a `*muxmaster.Mux`, and that inner `Mux` issues one of its own automatic redirects (a trailing-slash redirect or a fixed-path redirect) while serving a request forwarded to it with the mount prefix stripped, the outer `Mux` now prepends the mount prefix to the `Location` header before it reaches the client — previously the inner `Mux` computed its redirect target from the already-stripped path, producing a `Location` that omitted the mount prefix and sent the client outside the mount entirely. The rewriting is recursive across a chain of nested mounts, and applies identically whether `Mount` was called directly or via `(*Group).Mount` (including when the group has its own middleware). It does **not** apply to a redirect application code issues itself (e.g. `http.Redirect`), nor to a mounted handler that is not a `*muxmaster.Mux` (e.g. `http.FileServer`, a third-party router) — both keep working exactly as before, unmodified. Implemented by propagating the accumulated mount-prefix chain to the inner `Mux` via a lightweight context value (`mountPrefixCtx`) that only MuxMaster's own redirect machinery reads; the composed target still goes through the existing control-byte/backslash `Location` encoding and preserves the query string unchanged.
  - **Mount prefix validation.** A `Mount` prefix whose last element (after any trailing `/` is removed) is an optional parameter — `{/:name}` or `{/:name:expr}` — now panics at registration with `"muxmaster: Mount prefix '<prefix>' ends with an optional parameter; Mount does not support an optional parameter as the last element of its prefix"` instead of the previous unrelated internal wildcard-conflict panic that named neither the Mount prefix nor the real cause. An optional parameter that is *not* the prefix's last element (e.g. `/v2{/:id}/admin`) is unaffected and continues to work.
  - **`RawPath` propagation for a parameterized Mount prefix.** The forwarded request's `RawPath` is now derived by comparing it, segment-by-segment and percent-decoded, against the prefix actually matched for that specific request — rather than a literal `TrimPrefix` against the pattern text, which could never match a prefix containing a named parameter, a regex parameter, or a non-trailing optional parameter, and previously left `RawPath` always zeroed for those shapes. `RawPath` is now preserved whenever the captured parameter's raw form decodes cleanly to the same text the router matched on, and zeroed only when it does not (including when a percent-encoded `/` inside the captured segment shifts the raw segment boundaries out of alignment with the decoded ones). A purely static Mount prefix's behavior is unchanged, byte-for-byte, from before this fix.
  - **Performance:** the request copy Mount already builds for every forwarded request is now fused with the new mount-prefix context value into a single allocation (`mountBundle`, mirroring the existing `reqBundle`/`redirectBundle` pattern), reducing Mount's own per-request allocation count from 2 to 1 (measured: 3→2 allocs/op for a static-prefix route, 4→3 for a one-parameter route, AMD Ryzen 9 5900HX, `benchstat -count=10`) despite the added bookkeeping. Non-mounted routes (static, parameter, catch-all) show zero change in `B/op`/`allocs/op`. Regression/feature tests in `mount_redirect_test.go`; benchmarks `BenchmarkMount_Static`, `BenchmarkMount_Param`, `BenchmarkMount_TSRRedirect` added to `bench_test.go`.

- **Middleware: `StripSlashes` keeps `RawPath` in sync with an encoded trailing slash** (FPE-O14-003, rmp #274): for a request target such as `/a%2f` (`Path` `/a/`, `RawPath` `/a%2f`), `StripSlashes` stripped `Path` to `/a` but left `RawPath` as `/a%2f`, so the two no longer described the same path. `RawPath` is now stripped by the same number of separators, literal or `%2F`/`%2f`, as `specification/middleware-stdlib.md` rule 59 requires. Found by `FuzzStripSlashesIdempotency`; regression test `TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync`.

- **Routing: a regex parameter with an unclosed `{` no longer overwrites an existing route** (FPE-O14-002, rmp #274): registering a pattern such as `/{`, `/a/{id` or `/x/{id:[0-9]+/y` silently replaced the handler of a previously registered route instead of failing. It now panics at registration with `muxmaster: regex param '{' in path '<pattern>' is missing its closing '}'` and leaves the route tree unchanged (`specification/routing.md` section 11). Found by `FuzzWalkRoutes`; regression test `tree_unclosed_regex_param_test.go`.

- **Documentation: asterisk-form `OPTIONS *` request behaviour clarified** (rmp #278, sprint 20): Added documentation of how `http.Server.DisableGeneralOptionsHandler` affects asterisk-form OPTIONS request handling. Clarified that by default, `net/http` intercepts these requests before `Mux.ServeHTTP`, so pre-routing middleware and `GlobalOPTIONS` do not run. Documented how to configure the server to route these requests through MuxMaster. Updated files: `docs/middleware.md` (Pre section), `docs/configuration.md` (HandleOPTIONS section), and `SECURITY.md` (Pre vs Use security boundary section).

- **Documentation: middleware constructor calls corrected** (rmp #277, sprint 20): Fixed 24 instances across documentation and GoDoc comments where middleware constructors were called incorrectly. Affected middleware: `Recoverer()`, `CleanPath()`, `StripSlashes()`, `NoCache()`, `RequestID()`, and `RealIP()`. All instances now include the correct function-call parentheses. Updated files: `README.md`, `docs/getting-started.md`, `docs/cookbook.md`, `docs/routing.md`, `docs/error-handling.md`, `docs/middleware.md`, `docs/groups.md`, `docs/max-performance.md`, and `middleware/doc.go`. Examples now build successfully with all corrections applied.

- **Documentation: SECURITY.md corrections and completeness** (rmp #287, #284): Corrected and completed SECURITY.md with verified facts from post-release audits and threat-model closures. Fixed: PRF-2026-0003 (registration-order restriction removed), CDX-2026-005 (JWT algorithm latencies ~7.8/~33 µs not ~1/~300 µs), route-existence oracle figures (measured with competitors: 750–1050 ns band), TSC-2026-0002 (BasicAuth user-exists oracle closed at code level via constant-time scan), TSC-2026-0012 (alg=none measurement direction). Added: TSC-2026-0011/0012 timing oracle entries, h2c support note, UseRawPath+CleanPath interaction, CR/LF in path parameters, MSR-2026-0071 (OAuth2 singleflight cancellation isolation), ?/# escaping guidance, Mount traversal notes, SIEM re-interpretation note, JWT subject validation (TM-2026-003), JWT exp:null/0 handling (TM-2026-002), Recoverer logging protection (TM-2026-025), CleanPath Pre-gate ordering (TM-2026-040). Updated `docs/middleware.md` CleanPath description (rewrites in-place, not redirects) and added Pre-gate ordering guidance. Updated GoDoc: `CleanPath` (ordering requirement), `PathParam` (CR/LF handling), `Timeout` (ctx.Done() example). Closes #287, #284.

- **Documentation corrected: custom HTTP methods are not supported** (rmp #262, sprint 19): `README.md`, `docs/routing.md`, and specification have been corrected to reflect the verified behavior — MuxMaster recognizes a fixed, closed set of eleven method tokens (the ten standard HTTP methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY, plus the internal `"*"` token used by `Mount`), and registering any other method string (e.g., `PURGE`, `PROPFIND`) via `Handle`, `HandleFunc`, `HandleE`, `HandleFast`, or `Match` panics with `"muxmaster: unsupported HTTP method '<method>'"`. No `RegisterMethod` function exists. The router provides no mechanism to extend the method set at runtime. Custom-method requests can be served by attaching a `Mount` at a prefix and dispatching on `r.Method` within the mounted handler. Previously, documentation incorrectly claimed support for custom methods. Regression tests added in `method_set_test.go` pin all related specification rules and panic messages.

- **Documentation corrected: Mount's internal catch-all parameter now named accurately** (rmp #241, sprint 20): `docs/routing.md` line 276 now correctly describes `Mount("/api", handler)` as internally registered via `Handle("*", "/api/*mux_mount", ...)` instead of the anonymous `"/*"` description; specification `groups.md` rule 28 has been updated accordingly.

- **`middleware.Recoverer` / `RecovererWithLogger` no longer corrupt a response the handler had already started writing** (O-14, rmp #276, sprint 20): specification item 14 states Recoverer writes its 500 response "if headers have not already been sent". The previous implementation called `http.Error(w, ..., 500)` unconditionally on every recovered panic; on a live connection this did not rewrite an already-sent status line (net/http silently discards a superfluous `WriteHeader`), but it still appended `"Internal Server Error\n"` to whatever body bytes the handler had already streamed (e.g. a handler that wrote `WriteHeader(200)` and `"partial"` before panicking left the client with `200` and a body of `"partialInternal Server Error\n"`). `RecovererWithLogger` now wraps the `ResponseWriter` it hands to the next handler with an internal writer that tracks whether the response has already been committed — a `WriteHeader` call with a final (non-1xx) status, or the first byte written — and writes the 500 response only when the wrapped handler has not started one, matching the spec literally instead of only for the status line. The panic value and stack trace are still always logged (MM-2026-0057 log-injection sanitisation and MM-2026-0023 body-leak prevention are both unchanged). The wrapper implements `http.Flusher` and exposes `Unwrap() http.ResponseWriter` so `http.ResponseController` (Flush, Hijack) keeps working through Recoverer exactly as it already does through `Logger` and `Compress`. It is pooled via `sync.Pool` (`recovererWriterPool`) to keep the no-panic hot path free of allocations — measured on the panic-free path (AMD Ryzen 9 5900HX, `-count=10`): **+11 ns/op** (8.4 ns → 19.4 ns, 0 allocs/op both before and after) for wrapping every request, the unavoidable cost of tracking response state so the 500-body fix is correct; confirmed via a direct comparison against an unpooled per-request allocation (24.4 ns/op, 1 alloc, 24 B/op) that the pool is faster and allocation-free. Regression tests in `middleware/gap_o14_test.go`, `middleware/recoverer_internal_test.go`, and the `"recoverer"` case added to `middleware/wrapper_flusher_test.go`'s `TestResponseController_Flush_ThroughWrappers`.

- **Documentation: GoDoc, routing guide, and middleware documentation corrected** (rmp #288, sprint 21): Corrected multiple accuracy issues to match actual behaviour and specification:
  - Added sanitisation boundary documentation to `doc.go` and `mux.Handle()` GoDoc: catch-all parameters (`*name`) are raw path suffixes, not percent-decoded; handlers serving files must use `http.FileServer`/`http.ServeContent` or call `filepath.Clean`.
  - Fixed `HandleFast` GoDoc wording: clarified that Use-registered middleware panics at `HandleFast` registration (CSA-2026-0054, FPE-2026-010), added cross-reference to SECURITY.md "Pre vs Use security boundary" section with correct anchor.
  - Added example function `ExampleMux_HandleFast_auth` demonstrating Pre-gated `HandleFast` routes (correct auth-gate placement for fast routes).
  - Fixed three broken anchor links in `docs/middleware.md` (JWTAuth, OAuth2Introspect, APIKey sections): corrected from `#thread-safety-contract-mm-2026-0017--csa-2026-0052` to `#pre-vs-use-security-boundary-csa-2026-0059--h8-01`.
  - Added GET/HEAD method documentation to `docs/routing.md` and `docs/migration.md`: clarified that registering GET does NOT implicitly handle HEAD (unlike `net/http.ServeMux`, chi, httprouter); HEAD must be registered explicitly or via `Match()`. Compared routing behaviour vs. other routers in migration guide.
  - Added optional parameter documentation to `docs/routing.md`: documented `{/:name}` syntax, the maximum of 8 optional parameters per pattern, and the constraint that two optional parameters may not be consecutive.
  - Documented regex parameter name limit: regex-constrained parameter names (`{name:expr}`) are limited to 254 characters; names longer than 254 bytes panic at registration.
  - Fixed `middleware_test.go` comment for `OAuth2Introspect` example: corrected `CacheTTL: 0` from "disable caching" to `CacheTTL: -1` (0 = default 60s, -1 = disabled); also added clarifying note in the comment.
  - Fixed `docs/max-performance.md` example: added realistic CIDR parameter to `middleware.RealIP()` call (security default #40, closed) and added `"net/netip"` import.

- **Documentation: specification aligned with the code, performance re-measured, and documentation truth pass** (sprint 21):
  - **Specification** (rmp #297, `cf30358`): corrected statements that contradicted the code — `New()` leaves `RedirectFixedPath` and `UnescapePathValues` `false`; `Lookup` also matches `HandleFast` routes (returning a `nil` handler) and takes the registration read lock, which request dispatch never takes; `Routes()` lists a mount point with method `"*"` and pattern `<prefix>/*mux_mount`; `Walk` skips fast routes and `WalkFast` is specified. Added the rules the specification omitted: `\` in redirect targets is percent-encoded as `%5C` (`routing.md`); `APIKey`, `JWTAuth`, `OAuth2Introspect`, `ThrottlePerIP` and `ThrottlePerIPCapped` (`middleware-stdlib.md`); a request with a `nil` context is dispatched with `context.Background()` as parent (`compatibility.md`). Stale competitor figures in `performance.md` were replaced with the 2026-09-26 measurement and its significance caveat.
  - **Measurement** (rmp #296, `1c1ba00`, [`reports/perf-lab-2026-09-26-docs/`](reports/perf-lab-2026-09-26-docs/README.md)): HEAD against v1.1.0 on one host (AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`). Hot-path B/op and allocs/op are identical on all 18 shared benchmarks; no ns/op delta is statistically significant at n=3. All 18 verifiable sprint 18 waste-hunt claims reproduced, several better than first recorded.
  - **Documentation** (rmp #298): every user-facing document was checked against the code at HEAD and the specification.
    - `README.md` rewritten: it covers the full feature set (QUERY, `Mount`, `ServeFiles`, introspection, the `Pre`/`Use`/`UseFast` matrix, all 21 middleware constructors) and adds a "What's new since v1.1.0" section.
    - `README.md` and the `docs/` guides corrected where they contradicted the code: `RedirectFixedPath` defaults to `false` and redirects to `path.Clean(path)` (documented as `true` and as a case-insensitive redirect); the automatic `OPTIONS` response is `204 No Content` (documented as 200); without an `ErrorHandler`, every returned error produces `500 Internal Server Error`, whatever status an `HTTPError` carries; `Compress` supports gzip only (documented as gzip or deflate); `Logger` writes one plain-text line per request (documented as structured `slog` output); `Rebuild` is safe while serving. Examples that panicked at registration were fixed: the routing priority example (a parameter and a catch-all at the same position) and the cookbook SPA recipe (a root catch-all next to other routes; it now serves the SPA from `NotFound`). The `docs/max-performance.md` pprof example no longer uses `Mount`, which strips the prefix and made every pprof page return 404, and a `RealIP` example no longer passes a `netip.Prefix` value where a pointer is required.
    - `docs/performance.md` and `docs/max-performance.md` rebuilt from [`reports/perf-lab-2026-09-26-docs/`](reports/perf-lab-2026-09-26-docs/README.md): every figure states its date, host and source; Apple M4 and Raspberry Pi 5 figures are marked historical (v1.1.0-era code); figures not re-measured on 2026-09-26 are labelled as such; the measured costs of the sprint 20 security fixes (`BasicAuth`, `CORS`, `Recoverer`) are stated.
    - `CONTRIBUTING.md` describes the gitflow branching model the repository uses (`develop` as the integration branch, pull requests against `develop`) and states that CI runs only on pull requests against `main`; `CONTRIBUTING.md` and `.github/branch-protection.md` state that branch protection on `main` is required by policy but not applied on GitHub, with the exact steps to apply it; `SECURITY.md` source references point to the current locations. Contributor notes in `CLAUDE.md` were updated to match.
  - **GoDoc** (rmp #298): `Mux.Lookup` referred to a non-existent `LookupFast` (it now documents the `nil` handler returned for `HandleFast` routes, `WalkFast`, and the registration read lock); `ThrottleAllBacklog` carried a self-referential `Deprecated:` marker, now removed, and both throttle names are documented as equivalent; `Mux.PoolRequestBundle` mixed struct sizes and a size class ("368/400/480-byte") and now gives the 384 / 416 / 480 B size classes with the 2026-09-26 measurement. Other doc comments that contradicted the code (defaults, panics, `RoutePattern` on static routes, stale figures, a non-existent `Params.ByName`, the deprecated `Recoverer()` in the package example, a duplicate package comment) were corrected.
  - **`api.md`** (rmp #298): regenerated with `make api`. It was already out of date at the start of the sprint (`make api` at `a5f1cce` changes it), and now also reflects the GoDoc corrections above.

- **Compress middleware first-WriteHeader-wins now exempts 1xx informational responses** (rmp #254): corrected a defect introduced after v1.1.0 (no released version is affected; SECURITY.md MID-COMPRESS-1) where a 1xx code (e.g., 103 Early Hints) followed by a final status (e.g., 403) caused the final status to be silently dropped; the client received an implicit **200 OK** with the full body. Added 1xx exemption matching `net/http`'s own behaviour.

- **Logger middleware 1xx status handling** (rmp #254): corrected a defect introduced after v1.1.0 (no released version is affected; SECURITY.md MID-LOGGER-1) where 1xx informational codes in `WriteHeader` calls were recorded in the access log instead of the final status. Client-visible response was always correct; only the logged status was wrong, hiding security-relevant codes from log monitoring.

- **Compress and Logger now expose optional HTTP interfaces** (rmp #254): both middlewares now implement `http.Flusher` (delegating to underlying writer) and `Unwrap() http.ResponseWriter` (for `http.ResponseController` and other interface-aware tools). `Logger` additionally implements `io.ReaderFrom` to preserve the `sendfile`/`splice` fast path for file serving.

- **Static routes can now be registered after sibling param routes** (rmp #256): both registration orders — `mux.GET("/books/:id", ...)` then `mux.GET("/books/featured", ...)`, or vice versa — now succeed without panic. Both routes work correctly in either order. Catch-all vs static routes still conflict in both directions (as expected).

- **Trailing-slash redirects now work for catch-all routes and Mount bare prefixes** (rmp #255): corrected a pre-existing oversight where the dispatch path discarded the trailing-slash-redirect bit for catch-all routes. A request to `/api` (bare `Mount("/api", handler)` without trailing slash) now triggers a TSR redirect to `/api/` when `RedirectTrailingSlash` is enabled.

- **Redirect Location control bytes percent-encoded per RFC 9110** (rmp #260): control bytes (0x00–0x1F, 0x7F) in redirect target paths are now percent-encoded, conforming to RFC 9110 §5.5. Byte-identical differential testing against `net/http.Redirect` for 520+ cases.

- **Examples fixed for correct pool and routing behaviour** (rmp #255): five examples (`authn`, `cache`, `jwt`, `static-site`, `rest-api`) that panicked at startup now register routes correctly; `max-performance` pprof Mount now reachable; `static-site` asset and doc paths now serve correctly; `reverse-proxy` no longer crashes under load with `PoolRequestBundle=true` — documentation updated to explain the unsafe pattern.

- **Registration rollback guarantee strengthened** (rmp #253): tree copy-on-write with rollback now enforced by test `TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched` — if a registration panics mid-mutation, the live tree is guaranteed untouched. MM-2026-0033 statement updated from "may be inconsistent" to "full rollback guaranteed."

- **Documentation: specification corrected to match actual behaviour** (rmp #254): `specification/error-handling.md` clarified that `Use()`-registered middleware wraps `NotFound`, `MethodNotAllowed`, and auto-OPTIONS handlers (this was always the case in the code; the spec now matches reality). Cache invalidation on every `Use()` call (and `Rebuild()`). Applies to root `Mux` and all `Group` instances uniformly.

### Security

- **`BasicAuth` looks up users in constant time** (TSC-2026-0002, rmp #290, `6ac8772`): the credential lookup went through a `map[string][32]byte`, whose hit/miss timing revealed whether a username exists; SECURITY.md previously only bounded that oracle. `BasicAuth` now keeps a slice of `{sha256(user), sha256(password)}` entries and scans **all** of them on every request with `subtle.ConstantTimeCompare` and `subtle.ConstantTimeCopy` — no early exit and no data-dependent branch — falling back to the dummy hash when no user matches. Behaviour is otherwise unchanged and allocations are identical. **Cost:** the scan is O(number of users) on every request, matched or not — about 26 ns per registered user when the fix landed (1 user: 202 → 322 ns on a hit). The 2026-09-26 run ([`reports/perf-lab-2026-09-26-docs/`](reports/perf-lab-2026-09-26-docs/README.md) §2; AMD Ryzen 9 5900HX, Go 1.27.0, `-count=3`) measured a hit at ≈ 320 / 550 / 2 836 ns and a miss at ≈ 733 / 959 / 3 232 ns for 1 / 10 / 100 users. The user-exists vs not-exists timing difference measured 15–121 ns over three runs, within the accepted bound. Regression tests in `middleware/middleware_test.go`; `BenchmarkBasicAuth` in `middleware/bench_test.go`.

- **Hardening: `writeRedirect` neutralises the WHATWG backslash-authority Location shape** (rmp #279, sprint 20, implementing the rmp #274 part 5a HOLD recommendation from the `http-protocol-security-auditor`): per the WHATWG URL Standard's "special authority slashes state", a browser's URL parser treats any two-byte combination of `/` and `\` at the start of a relative reference — not just RFC 3986's `//` — as authority-establishing, resolving a `Location` such as `/\evil.com/foo` to a *different* origin even though `net/url` (and RFC 3986) see no host component at all. `writeRedirect` (`mux.go`) now percent-encodes every `\` byte in the redirect target to `%5C` (`percentEncodeBackslash`) before any other processing; `net/http`'s own request-target decoding turns `%5C` back into a literal `\` in `r.URL.Path` on the follow-up request, so an operator's registered backslash-shaped route is still reached, on the same origin. This is a **deliberate divergence from `net/http.Redirect`**, which does not defend against this shape (its own `path.Clean` is a no-op on `\`, confirmed empirically) — documented in `writeRedirect`'s doc comment. `percentEncodeBackslash` is a no-op (no allocation) for the overwhelming majority of targets, which never contain a backslash, so output stays byte-identical to `net/http.Redirect` for every such target (`TestWriteRedirect_ByteIdenticalToNetHTTPRedirect` is unmodified and still passes). This is defense-in-depth, **not** a fix for an exploitable vulnerability: a `\`-containing target can only ever reach `writeRedirect` if the operator registers that exact literal route themselves (`path.Clean`, used by every redirect-target construction path, never introduces a backslash that was not already present verbatim in a registered pattern or the decoded request path), so an anonymous attacker cannot choose the host in this shape. Every backslash in the target is encoded, not only a leading `/\` pair — simpler than special-casing position 1, and additional defense-in-depth against components (some reverse proxies, WAFs, legacy browsers) that fold *any* backslash in a path to a forward slash. The `\/` and `\\` shapes (`target[0] == '\\'`) need no explicit handling, since every `writeRedirect` caller's target is derived from a `Handle()`-registered pattern and `Handle()` panics unless a pattern starts with `/` — that shape is unreachable in this package. Regression tests: `TestWriteRedirect_BackslashAuthorityShape_Neutralised` and `TestWriteRedirect_BackslashAuthorityShape_NeutralisesEndToEnd` (`redirect_bytediff_test.go`, the latter a real `httptest.Server` + `http.Client` round trip confirming the client lands on the same origin and reaches the registered handler).

- **Hardening: `OAuth2Introspect` no longer renders credentials in construction-time panics, and its singleflight leader no longer discards request-scoped context values** (CWE-532, MSR-2026-0071 follow-up, rmp #280, sprint 20): the TM-2026-005 fix (sprint 18) redacted userinfo only from the `slog.Warn`/`slog.Info` construction-time log lines; the three construction-time `panic` paths in `OAuth2Introspect` were never audited and kept rendering `opts.Endpoint` verbatim, including embedded Basic-auth-style credentials (e.g. `https://user:secret@host/introspect`) — reachable via the "malformed Endpoint URL", "Endpoint URL has no host", and "Endpoint URL must not contain userinfo" panics (the last one is the exact defect reported: the URL a caller is told to fix for containing credentials was itself echoed back with those same credentials). All three now redact via `redactedEndpointURL`/`redactedEndpointRaw`, which strip the userinfo component **entirely** (not just password-mask it), matching the existing slog policy so every diagnostic surface applies the same rule; the host and scheme are still shown so operators can identify the misconfigured endpoint. Separately, the singleflight leader's detachment of the outbound introspection call (MSR-2026-0071, sprint 18) used `context.WithTimeout(context.Background(), 30*time.Second)` to shield the shared call from the leader's own request cancellation — correct for cancellation-immunity, but `context.Background()` also discarded every request-scoped context *value* (trace/correlation IDs, etc.), so the outbound IdP request could never observe them. Changed to `context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)`: `context.WithoutCancel` preserves the `Value()` chain from `r.Context()` while guaranteeing its `Done()` never fires from the original request's cancellation, and the independent 30-second timeout is layered on top exactly as before, so the original MSR-2026-0071 guarantee (one caller's cancellation must never fail another singleflight follower) is unchanged. Regression tests: `TestSec_OAuth2Introspect_UserinfoEndpointPanic_RedactsCredentials`, `TestSec_OAuth2Introspect_UserinfoOnlyNoPasswordEndpointPanic_RedactsUsername`, `TestSec_OAuth2Introspect_NoHostWithUserinfoPanic_RedactsCredentials`, `TestSec_OAuth2Introspect_MalformedURLWithUserinfoPanic_RedactsCredentials`, and `TestSec_OAuth2Introspect_PlainEndpointPanic_Unaffected` (`middleware/oauth2_redaction_test.go`, all confirmed to fail against the pre-fix code); `TestOAuth2Introspect_LeaderDetach_ValuesPropagate_CancellationDoesNot` (`middleware/oauth2_concurrency_test.go`), which drives a genuine leader/follower singleflight race through a custom `http.RoundTripper` and asserts both that the leader's context value reaches the outbound request and that the leader canceling its own request never aborts the shared call.

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

- **405 / OPTIONS `Allow` header table + prebuilt slices** (rmp #250, WH-09): method bitmask index into pre-computed `Allow` strings; direct map assignment for header. Measured (post-aliasing-fix): `MethodNotAllowed` **−9.74%** (153 ns → 138 ns), 2 → 3 allocs; `OPTIONSAuto` **−51.01%** (124 ns → 61 ns), 3 → 1 allocs. Allocations include per-request header-slice isolation (see "Header-slice aliasing defect fix" below).

- **Header-slice aliasing defect fix** (rmp #250, security review): WH-05 and WH-09 hoisted constant header **values** (`[]string`) instead of just strings, sharing slices across requests. Five affected code paths: `response.go` `JSON`/`XML`/`Text`, `mux.go` `lazyMethodNotAllowed`/`lazyOPTIONS`, `middleware/set_header.go`, `middleware/cors.go`, `middleware/no_cache.go`. Fixed by allocating header slices fresh per request. Post-fix alloc costs: `Text` **−41.39%** (98 ns → 58 ns, 2 → 1 allocs net), `JSONHelper` **−2.73%** (500 ns → 487 ns, 3 allocs unchanged but B/op restored to baseline).

#### Routing behaviour

- **Lookup fallback: static branch to param sibling** (rmp #259, DIV-001): when a static path segment exists alongside a param segment (e.g., `/users/list` and `/users/:id`), requests to a non-existent static segment (e.g., `/users/listx`) now match the param route instead of returning 404. Both static and param registration orders work correctly.

- **Mount bare prefix and TSR** (specification.md §28): a request to a bare mount prefix (e.g., `/v2` without trailing slash) now triggers `RedirectTrailingSlash` (when enabled, the default) to `/v2/` before the mounted handler receives the request. Requests to `/v2/` and `/v2/anything` match the mount directly. Catch-all routes (used internally by `Mount`) now participate in trailing-slash redirect logic.

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

[Unreleased]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/FlavioCFOliveira/MuxMaster/compare/v1.0.0-rc1...v1.0.0
[1.0.0-rc1]: https://github.com/FlavioCFOliveira/MuxMaster/releases/tag/v1.0.0-rc1
