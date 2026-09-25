# MuxMaster Invariants (fuzz/property-checked)
Version: 2026-09-25-Sprint20-O14 | Commit: 3017d71 | Go: 1.26

## I-01 — ServeHTTP never panics
For any `*http.Request` with a valid URL (no raw control bytes), `mux.ServeHTTP(w, r)` returns without panic regardless of method, path, or middleware configuration.
- Test: `FuzzServeHTTP`, `FuzzServeHTTPAllMethods`, `TestProp_ServeHTTPNeverPanics`
- Last verified: 2026-05-07
- Coverage: dispatch, TSR, 405/OPTIONS/404 lazy-handler paths
- Fail-count: 0

## I-01b — Handle/HandleFast document expected panics
`mux.Handle(method, pattern, h)` and `mux.HandleFast(method, pattern, h)` panic on documented invariant violations (empty method, non-absolute path, unsupported method, nil handler, invalid wildcard syntax). All other inputs must not panic.
- Test: `FuzzHandle`, `FuzzHandleFast`
- Last verified: 2026-05-07
- Fail-count: 0 (after harness allow-list was extended)

## I-02 — CleanPath idempotency
`CleanPath(CleanPath(p)) == CleanPath(p)` for all `p`. The length must not grow by more than 1 byte (for a prepended `/`).
- Test: `TestProp_CleanPathIdempotency`
- Last verified: 2026-05-07
- Fail-count: 0

## I-03 — Compress roundtrip
For any response body `B`, if the `Content-Encoding: gzip` header is set by the Compress middleware, `gunzip(body) == B`.
- Test: `FuzzOAuth2IntrospectionResponse`, `TestProp_CompressRoundtrip`
- Last verified: 2026-05-07
- Fail-count: 0

## I-04 — Group prefix composition
For any sequence of prefixes P1..Pn and leaf L registered via nested `Group.Group(...)`, an HTTP request at `concat(P1..Pn, L)` is routed to the handler with status 200.
- Test: `FuzzGroup`, `TestProp_GroupPrefixComposition`
- Last verified: 2026-05-07
- Fail-count: 0

## I-05 — Middleware order preserved
For any middleware sequence M1..Mn registered via `Use()`, execution order equals registration order (index 0 is outermost).
- Test: `TestProp_MiddlewareOrderPreserved`
- Last verified: 2026-05-07
- Fail-count: 0

## I-06 — Params.Get never panics
For any `Params` slice (including nil, empty, and byte-adversarial) and any string key, `p.Get(key)`, `p.Lookup(key)`, `p.Int(key)`, `p.Int64(key)`, `p.Uint64(key)`, `p.Float64(key)`, `p.Bool(key)` all return without panic.
- Test: `FuzzByName`
- Last verified: 2026-05-07
- Fail-count: 0

## I-06b — ParamsFromContext never panics
For any `context.Context` (including background, with unrelated values, or with no params), `ParamsFromContext(ctx)` and `PathParam(r, name)` return without panic.
- Test: `FuzzParamsFromContext`
- Last verified: 2026-05-07
- Fail-count: 0

## I-07 — ParamsFromContext roundtrip
For any param (key, value) injected via a real route, `ParamsFromContext(r.Context()).Get(key) == value`. `Lookup` returns `ok=true` for present keys.
- Test: `FuzzParamsRoundtrip`, `TestProp_ParamsFromContextRoundtrip`
- Last verified: 2026-05-07
- Fail-count: 0

## I-08 — Params overflow (>3 params)
Routes with more than 3 path parameters dispatch without panic and return all param values correctly.
- Test: `FuzzParamsOverflow`
- Last verified: 2026-05-07
- Fail-count: 0

## I-09 — PanicHandler absorbs all panics
When `mux.PanicHandler` is set, no panic escapes `ServeHTTP`. The handler must write a 500 response.
- Test: `TestProp_PanicHandlerAbsorbsPanics`
- Last verified: 2026-05-07
- Fail-count: 0

## I-10 — HandleFast bypasses stdlib middleware (by design)
Routes registered via `HandleFast` do NOT execute middleware registered via `Use()`. This is documented and by design. The property test verifies: stdlib auth middleware runs N times on a stdlib route and 0 times on a fast route.
- Test: `TestProp_HandleFastVsHandleIsolation`
- Last verified: 2026-05-07
- Security note: operators must not mix `Use(authMW)` with `HandleFast` if auth is required

## I-11 — RequestID always set
The `RequestID()` middleware always sets `X-Request-ID` in the response, bounded to ≤128 characters. Incoming IDs failing validation are replaced with a fresh random ID.
- Test: `TestProp_RequestIDAlwaysSet`
- Last verified: 2026-05-07
- Fail-count: 0

## I-12 — BasicAuth correctness
`BasicAuth` correctly admits only requests with valid (user, password) pairs and rejects all others with 401.
- Test: `TestProp_BasicAuthCorrectness`
- Last verified: 2026-05-07
- Fail-count: 0

## I-13 — Lookup consistency
For any route registered via `Handle`, `mux.Lookup(method, pattern)` returns `(non-nil handler, found=true)` immediately after registration.
- Test: `TestProp_LookupConsistency`
- Last verified: 2026-05-07
- Fail-count: 0

## I-14 — Mount prefix stripping
After `Mount(prefix, inner)`, the inner handler receives `r.URL.Path` without the prefix. The prefix must not reappear in `innerPath`.
- Test: `FuzzMount`, `TestProp_MountRawPathStripping`
- Last verified: 2026-05-07
- Fail-count: 0

## I-15 — Timeout sets deadline
`Timeout(d)` sets a context deadline of `now+d` on the request context. The handler receives a context with `Deadline()` returning `ok=true`.
- Test: `TestProp_TimeoutSetsDeadline`
- Last verified: 2026-05-07
- Fail-count: 0
- Engineering note (added 2026-09-25, rmp #265, closes O-2/FPE-2026-005): this
  property intentionally checks deadline *presence* only, not cancellation
  timing — cancellation is asynchronous with respect to the deadline (fired
  by a runtime timer on a separate goroutine) and is covered separately by
  **I-15b** below, which carries an explicit, measured tolerance. The original
  `TestProp_TimeoutCancelsContext` (rmp #176) was lost before being committed;
  its only trace was a failing rapid seed
  (`harness/testdata/rapid/TestProp_TimeoutCancelsContext/...`, now removed —
  superseded by the reconstructed property below). See
  `2026-09-25-FPE-2026-005-timeout-cancellation-property.md` for the full
  history and root-cause analysis.

## I-15b — Timeout cancels context within a measured, justified tolerance of the deadline (FPE-2026-005)
`Timeout(d)` closes the request context's `Done()` channel no earlier than
`now+d`, and no later than `now+d+300ms`. The 300ms tolerance is a fixed
floor derived from empirical measurement (this repository's dev machine,
go1.26.2 linux/amd64, `go test -race`): isolated firing lag ≈1.5ms max over
200 samples; under synthetic heavy scheduler/GC contention, lag ≈60ms max
over 90 samples. 300ms is ~5x the worst contended measurement, sized to
absorb slower/virtualised CI runners and `-count=20` repetition without
hiding a genuine regression (e.g. a timer that never fires or fires seconds
late). No library code change was required — re-investigation of the
original failing seed (`timeoutMs=50, sleepMs=51`) found a test-tolerance
defect (polling after a fixed sleep only 1ms past the timeout, which races
against the same scheduler jitter measured here), not a defect in
`middleware/timeout.go`.
- Test: `TestProp_TimeoutCancelsContext` (`harness/properties_test.go`)
- Last verified: 2026-09-25 — `go test -race -count=20`: 20/20 runs pass, 2000 total rapid iterations, 0 failures, ~104s total
- Fail-count: 0
- FPE-2026-005: Open (AC-unmet) → **Resolved** — acceptance criteria of rmp #176 met via option (b) (tolerant cancellation property) rather than (a)/(c)

## I-16 — Recoverer prevents panics from escaping
`Recoverer()` absorbs any panic from the wrapped handler and writes a 500 response. No panic escapes `ServeHTTP`.
- Test: `TestProp_RecovererNoPanic`
- Last verified: 2026-05-07
- Fail-count: 0

## I-JWT-01 — JWTAuth never panics
`JWTAuth` middleware processes any Authorization header value without panic. Invalid, malformed, or alg-confusion tokens produce 401, never 500 or panic.
- Test: `FuzzJWTAuth`, `FuzzJWTAlgConfusion`, `FuzzJWTAudClaim`
- Last verified: 2026-05-07
- Fail-count: 0

## I-JWT-02 — JWT alg pinning
When `Algorithms: ["HS256"]` is configured, tokens claiming `alg=RS256` are rejected. Cross-family alg-confusion tokens (H-6 hypothesis) are rejected by the `allowedAlgs` map check before signature verification.
- Test: `FuzzJWTAlgConfusion`
- Last verified: 2026-05-07
- Fail-count: 0

## I-CORS-01 — CORS never panics
CORS middleware processes any Origin header value without panic.
- Test: `FuzzCORS`, `FuzzCORSPreflight`, `FuzzCORSCredentials`
- Last verified: 2026-05-07
- Fail-count: 0

## I-CORS-02 — allowAll emits literal "*"
When `AllowedOrigins: ["*"]`, `Access-Control-Allow-Origin` is set to `"*"` — never to the echoed request origin.
- Test: `FuzzCORS`
- Last verified: 2026-05-07
- Fail-count: 0

## I-CORS-03 — CRLF/NUL origin rejected
Origins containing CR (0x0D), LF (0x0A), or NUL (0x00) are rejected with 400, never reflected in headers.
- Test: `FuzzCORS`
- Last verified: 2026-05-07
- Fail-count: 0

## I-OAUTH2-01 — OAuth2Introspect never panics
OAuth2Introspect processes any Authorization header without panic.
- Test: `FuzzOAuth2Authorization`, `FuzzOAuth2IntrospectionResponse`, `FuzzOAuth2CacheEviction`
- Last verified: 2026-05-07
- Fail-count: 0

## I-OAUTH2-03 — Malformed introspection JSON does not panic
Arbitrarily malformed JSON responses from the introspection endpoint are handled gracefully; only 200 or 401 is emitted.
- Test: `FuzzOAuth2IntrospectionResponse`
- Last verified: 2026-05-07
- Fail-count: 0

---
## S8 Invariants (2026-05-07)

## I-SAN-01 — sanitiseForLog strips all C0/C1 control bytes
`sanitiseForLog(s)` output contains no byte with value < 0x20 or == 0x7F. Every byte from 0x00..0x1F (including NUL, CR, LF, ESC, VT, FF, DEL) is escaped.
- Test: `TestProp_SanitiserAllControlChars`, `TestProp_SanitiserExhaustiveByte`, `FuzzLoggerNoPanic`, `FuzzRecovererSanitiser`
- Last verified: 2026-05-07 (60s each target, 0 crashes)
- Fail-count: 0

## I-SAN-02 — sanitiseForLog excludes U+2028/U+2029 literals
U+2028 (LSEP) and U+2029 (PSEP) do not appear as raw UTF-8 bytes in the sanitised output; they appear as ` `/` ` Go escape sequences.
- Test: `TestProp_SanitiserLSEPandPSEP`
- Last verified: 2026-05-07
- Fail-count: 0

## I-SAN-03 — sanitiseForLog safety is one-pass (NOT idempotent)
FINDING FPE-2026-SAN-01 (sev=3): `sanitiseForLog` is not strictly idempotent. `QuoteToASCII` re-escapes its own output on a second pass (e.g., `"` → `\"` → `\\\"`). The safety property (no control bytes) holds on every pass, but the string grows. This is cosmetic for a single-layer logger — the logger applies sanitisation exactly once per log write.
- Test: `TestProp_SanitiserAllControlChars` (weak idempotency variant)
- Last verified: 2026-05-07
- Classification: cosmetic / low risk for logger use-case; documented non-idempotency is acceptable

## I-JWT-NONE-01 — alg=none always rejected (H8-42, H8-64)
All case variants of `alg=none` (none/None/NONE/nOnE) and decorated variants (leading/trailing whitespace, NUL byte, CRLF) are rejected with 401. The `allowedAlgs` map check is exact-string (case-sensitive), so any non-listed alg value is rejected before signature verification.
- Test: `TestJWT_AlgNoneVariantsRejected`, `FuzzJWTAlgNone`
- Last verified: 2026-05-07 (60s, 1.6M execs, 0 crashes)
- Fail-count: 0
- H8-42: REFUTED — no bypass of alg=none variants found
- H8-64: REFUTED — alg=none/None/NONE all rejected; exact-string map is correct defence

## I-JWT-KID-01 — kid field does not cause panic or path traversal (H8-63)
Arbitrary values in the JWT `kid` header field (path traversal, SQL injection, shell injection, NUL bytes, CRLF) never cause panic or HTTP 500. The `kid` field is parsed into `rawJWTHeader` via `json.Unmarshal` but is not used for key lookup in MuxMaster's single-key JWTAuth implementation.
- Test: `FuzzJWTKidInjection`
- Last verified: 2026-05-07 (60s, 2.0M execs, 0 crashes)
- Fail-count: 0
- H8-63: REFUTED for current implementation — kid is parsed but not used as key/path

## I-JWT-PARSE-01 — JWT parser never panics on arbitrary input (H8-43)
The `parseAndValidateJWT` function and `JWTAuth` middleware never panic or return 500 for any Authorization header value, including: empty strings, malformed base64, alg=none tokens, tokens with 4+ parts, multi-MB tokens.
- Test: `FuzzJWTParserNoPanic`, `FuzzJWTHeaderArbitrary`
- Last verified: 2026-05-07 (60s each, 2.7M + 2.5M execs, 0 crashes)
- Fail-count: 0
- H8-43: REFUTED — parser handles all adversarial inputs without panic

## I-JWT-CTX-01 — GetJWTClaims never panics on arbitrary context (H8-08)
`GetJWTClaims(ctx)` returns `(nil, false)` without panic for any `context.Context`, including background, contexts with unrelated values, and contexts where the stored value has a different concrete type.
- Test: `TestProp_GetJWTClaimsArbitraryContext`, `TestProp_JWTClaimsContextSafety`
- Last verified: 2026-05-07 (100 rapid runs each, 0 failures)
- Fail-count: 0
- H8-08: REFUTED — type assertion is guarded; no panic from malformed claim shapes

## I-RAW-01 — All 4 UseRawPath/UnescapePathValues states never panic (H8-27)
For any (UseRawPath, UnescapePathValues) combination and any URL path, ServeHTTP never panics.
- Test: `FuzzRawPathMatrixNoPanic`
- Last verified: 2026-05-07 (60s, 3.1M execs, 0 crashes)
- Fail-count: 0

## I-RAW-02 — UseRawPath=false makes UnescapePathValues a no-op (H8-27)
When `UseRawPath=false`, the routing result and param values are identical regardless of `UnescapePathValues`.
- Test: `TestProp_RawPathFalseUnescapeNoEffect`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-RAW-03 — UseRawPath=true + UnescapePathValues=true yields decoded params (H8-27)
When both flags are true, path param values equal `url.PathUnescape(rawSegment)`.
- Test: `TestProp_RawPathTrueParamValues`, `TestRawPathDifferential4States`
- Last verified: 2026-05-07
- Fail-count: 0

## I-REGEX-01 — Regex params: any syntactically valid Go regex registers without panic, including expressions containing '}' (H8-52)
Any syntactically valid Go regex registers as a `{name:expr}` param without panic — this now includes expressions containing a `}` byte (e.g. `a{2,3}`, `[}]`, `(})`, `\}`). **Fixed** in `tree.go` (~line 1255, commit `825c623`): the parser used to stop at the FIRST `}` byte in the segment; it now scans the whole path segment and picks the LAST `}` before the next `/` (or end of pattern) as the token's closing brace, so a `}` anywhere inside `expr` no longer truncates or breaks registration.
- Test: `FuzzRegexParamRegistration` (assertion strengthened 2026-09-25 to require success for `}`-containing valid regexes — previously silently skipped as a "known limitation"); `TestRegexBraceFixed` (new 2026-09-25, deterministic regression guard — registers 4 distinct `}`-containing regexes and confirms both successful registration and correct request-time matching)
- Last verified: 2026-09-25 (`FuzzRegexParamRegistration`: 20s, 409K execs, 0 crashes; `TestRegexBraceFixed`: 8/8 subtests pass)
- Finding: FPE-2026-0001 / FPE-2026-REGEX-01 (sev=3) — **Fixed**, commit `825c623`. Closes O-5.
- H8-52: PARTIAL — no catastrophic backtracking at *registration* time; ReDoS at *request* time is bounded by Go's regexp engine (linear for common patterns, see TestRegexReDoSBudget)

## I-REGEX-02 — Regex param ServeHTTP never panics (H8-52)
`ServeHTTP` with registered regex routes never panics for any segment value.
- Test: `FuzzRegexParamServeHTTP`
- Last verified: 2026-05-07 (60s, 4.0M execs, 0 crashes)
- Fail-count: 0

## I-CAP8-01 — cap-8 optional segments enforced, not bypassable via Group nesting (H8-21)
A pattern with >8 optional segments panics with the documented cap message. Group nesting does not re-count optional segments from zero — the cap is per-pattern (applied after prefix concatenation).
- Test: `TestCap8DirectEnforcement`, `TestProp_Cap8NotBypassedViaGroups`, `TestCap8AtMaximum`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0
- H8-21: REFUTED — cap is applied on the final joined pattern; no bypass observed

## I-PARAMS-MANY-01 — N-param routes (N up to 100) never panic (H8-25, H8-56)
Routes with any number of path params (1..100) dispatch without panic and return all param values correctly. The heap-overflow path (N>3) is exercised and stable.
- Test: `TestProp_ParamsManyNoPanic`, `TestParamsExtremeCount`
- Last verified: 2026-05-07 (100 rapid runs + 1 deterministic 100-param run, 0 failures)
- Fail-count: 0
- H8-25/H8-56: REFUTED — 100-param route dispatches correctly; no OOM or panic

## I-CASE-FOLD-01 — RedirectFixedPath=true never panics on multi-byte UTF-8 (H8-23)
With `RedirectFixedPath=true`, requests with multi-byte UTF-8 paths (including 2-byte, 3-byte, 4-byte sequences, emoji, CJK, overlong encoding) never cause OOB or panic in the case-fold path.
- Test: `FuzzCaseFoldUTF8NoPanic`, `TestCaseFoldMultiByteIndex`
- Last verified: 2026-05-07 (60s, 3.8M execs, 0 crashes)
- Fail-count: 0
- H8-23: REFUTED — no OOB or panic found under raw-byte indexing with multi-byte UTF-8

## I-REBUILD-01 — Rebuild() preserves route set consistency (H8-70/H8-71)
After `Rebuild()`, all pre-registered routes remain routable. `Rebuild()` during concurrent request handling does not cause panics or races.
- Test: `TestProp_RebuildConsistency`, `FuzzRebuildConcurrent`
- Last verified: 2026-05-07 (100 rapid runs + 60s fuzz, 0 failures)
- Fail-count: 0

## I-WALK-01..04 — Walk/WalkFast/Routes on any Mux state (including post-panic)
Walk and WalkFast terminate, deliver only non-nil handlers, and never panic — even on trees left in a post-panic state by conflicting route registrations.
- Test: `TestWalkOnCorruptedTree`, `FuzzWalkCorrupted`, `TestWalkCorruptedTreeRace`
- Last verified: 2026-05-07
- Fail-count: 0

---
## S9 Invariants (2026-05-07)

## I-APIKEY-01 — APIKey never panics; only 200 or 401
`APIKey` middleware processes any `X-API-Key` header value without panic. Only 200 (valid key) or 401 (missing/invalid key) are returned.
- Test: `FuzzAPIKey`, `TestProp_APIKeyCorrectness`
- Last verified: 2026-05-07 (30s, 1.7M execs, 0 crashes)
- Fail-count: 0

## I-APIKEY-02 — GetAPIKeyIdentity roundtrip for valid key
For any key in the configured key map, `GetAPIKeyIdentity(r.Context())` returns the associated identity string with `ok=true`.
- Test: `TestProp_APIKeyCorrectness`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-NOCACHE-01 — NoCache always sets Cache-Control/Pragma/Expires
`NoCache()` middleware always sets `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, and `Expires: 0` headers on every response, regardless of method or path.
- Test: `TestProp_NoCacheHeadersAlwaysSet`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-MATCH-01 — Mux.Match never panics unexpectedly
`Mux.Match(methods, pattern, h)` never panics except for documented pre-conditions (empty method, non-absolute path, route conflict).
- Test: `FuzzMatch`, `TestProp_MatchRoutesAllMethods`
- Last verified: 2026-05-07 (30s, 889K execs, 0 crashes)
- Fail-count: 0

## I-ANY-01 — ANY registers across all 9 standard HTTP methods
`ANY(pattern, h)` makes the route reachable with GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, and TRACE. No method returns a 0 status code.
- Test: `TestProp_ANYRoutesAllMethods`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-DISPATCH-01 — ServeHTTP on diverse mux never panics for any (method,path)
For a pre-populated mux covering all radix-tree node types, `ServeHTTP` never panics for any (method, path) combination. Response status is always in 100..599.
- Test: `FuzzMuxDispatch`, `TestDispatchKnownRoutes`
- Last verified: 2026-05-07 (30s, 848K execs, 0 crashes)
- Fail-count: 0

## I-MW-CHAIN-01 — N-middleware chain (0..50) never panics
Composing N middlewares from the catalogue (RequestID, NoCache, Recoverer) via Use() and serving a GET /ping never panics and never returns 500 for this innocuous request.
- Test: `FuzzMiddlewareChain`, `TestProp_MiddlewareChainOrdering`
- Last verified: 2026-05-07 (30s, 1.27M execs, 0 crashes)
- Fail-count: 0

## I-CDX-01b — Group.Use + Group.HandleFast panics (CSA-2026-0054)
Registering a `HandleFast` route on a `Group` that has stdlib middleware (via `Use()`) panics at registration time with a message explaining the incompatibility. This prevents silent auth bypass on group-scoped fast routes.
- Test: `TestCDX_GroupUsePlusHandleFastPanics`, `TestProp_CDXMatrix_UsePanicsOnHandleFast`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-CDX-02 — Pre(mw) wraps both Handle and HandleFast routes
Middleware registered via `Pre()` runs for both stdlib routes (Handle) and fast routes (HandleFast). Pre is the correct registration point for cross-cutting policies.
- Test: `TestProp_CDXMatrix_PreWrapsBothRouteTypes`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-CDX-03 — UseFast(fastMW) runs only on HandleFast routes
Middleware registered via `UseFast()` runs only on `HandleFast` routes, not on stdlib Handle routes.
- Test: `TestProp_CDXMatrix_UseFastOnlyForFastRoutes`
- Last verified: 2026-05-07 (100 rapid runs, 0 failures)
- Fail-count: 0

## FINDING FPE-2026-010 — Mux.Use + Mux.HandleFast does NOT panic (OPEN)
OPEN FINDING (task #181, sev=6): `Mux.Use(stdlibMW) + Mux.HandleFast` does NOT panic at registration time, contrary to the Use() docstring. The panic guard exists only for `Group.HandleFast` (CSA-2026-0054). Auth middleware registered via `Mux.Use()` silently does not cover fast routes on the root mux.
- Test: `TestCDX_MuxUsePlusHandleFastDoesNotPanic` — logs the open gap, becomes an assertion when fixed
- Evidence: `CRASH-FPE-010/repro_test.go`
- Fix: add `if len(m.middleware) > 0 { panic(...) }` guard in `Mux.HandleFast` (mux.go)

---
## Sprint 20 Invariants (2026-09-25, rmp #265 — closes O-6/FPE-2026-006)

## I-SERVEFILES-01 — ServeFiles never discloses a file outside its served root
For any requested path under a `ServeFiles(prefix, root)` route, no response body ever contains the content of a file that lives outside `root`, including for raw `..` traversal, encoded (`%2e%2e`, `%2f`), double-encoded (`%252e%252e`), backslash-separator, absolute-looking, NUL-byte, and pathologically deep traversal payloads. The protection is `http.FileServer`'s internal `path.Clean` step on the rewritten request path (see `mux.go` CDX-S8-002 comment; `specification/static-files.md` item 6) — ServeFiles adds no protection of its own beyond that, under the default (`UseRawPath=false`) configuration it supports.
- Test: `FuzzServeFiles`, `TestProp_ServeFilesNoEscape`
- Last verified: 2026-09-25 (`FuzzServeFiles`: 36s, 658K execs, 0 crashes, 121 corpus entries persisted to `corpora/FuzzServeFiles/`; `TestProp_ServeFilesNoEscape`: 100 rapid runs, 0 failures)
- Fail-count: 0
- Scope note: symlinks placed *inside* the served root that point outside it are NOT covered by this invariant — `http.Dir`'s own godoc documents that it follows such symlinks, and MuxMaster neither adds nor removes that behaviour (spec item 6). See `TestServeFiles_SymlinkFollowsUpstreamBehavior`, which pins and documents that upstream (net/http) characteristic separately, so it is never conflated with a MuxMaster regression.

## I-SERVEFILES-02 — ServeFiles never panics for any requested path
`ServeFiles`-registered routes never panic in `ServeHTTP`, for any requested path (including malformed percent-encoding, NUL bytes, and pathological traversal depth).
- Test: `FuzzServeFiles`, `TestProp_ServeFilesNoEscape`
- Last verified: 2026-09-25 (see I-SERVEFILES-01 run figures — same harness, same runs)
- Fail-count: 0
- Finding: FPE-2026-006 (sev=4) — **Fixed** (last of the 8 named surfaces; ServeFiles was the only remaining gap). Closes O-6.

---
## Sprint 20 Invariants (2026-09-25, rmp #274 part 3/4 — O-14, findings.md B.4)

Commit `5f804fa` rewrote this harness directory wholesale. Most removed
`Fuzz*`/`TestProp_*` targets were superseded by a same-invariant renamed
target in the files that replaced them (see this task's final report for
the full removed → current-equivalent mapping). The invariants below are
the ones that had genuinely dropped to zero coverage and are restored here,
in `harness/fuzz_o14_restored_test.go`, plus one net-new invariant (I-05b)
that was never previously catalogued.

## I-02b — CleanPath does not add transformation beyond `path.Clean`
For any `raw` string with `RawPath` empty, the path observed by the handler
after `CleanPath()` equals `path.Clean(raw)` exactly — no percent-decoding,
no additional normalisation. This is the middleware-level property behind
the router-level pins `TestS8_Middleware_CleanPath_DoubleEncodedSlash` and
`TestS9_H05_CleanPath_MSR2026_0061_EncodedDotDot` (path-routing-fuzzer's
harness), which assert the same non-decoding behaviour through full
dispatch with fixed cases.
- Test: `FuzzCleanPathDoubleEncoded`
- Last verified: 2026-09-25 (35s, 1.99M execs, 0 crashes after fix; 112 corpus entries persisted to `corpora/FuzzCleanPathDoubleEncoded/`)
- Fail-count: 1 (test-defect, not a product bug — see note below), 0 after correction
- Note: an earlier version of this assertion (substring survival of `%2e`/`%2f`) was itself wrong — `path.Clean("/%2f/..")` legitimately collapses the opaque `"%2f"` segment via ordinary `..`-cancellation, which is not decoding. Corrected to compare directly against the `path.Clean` oracle. Regression seed: `/%2f/..` (`testdata/fuzz/FuzzCleanPathDoubleEncoded/dd53de32e83dcac2`).

## I-06c — Params.Map() length/content invariant
`len(ps.Map())` equals the number of **distinct** keys in `ps`; for each
distinct key, `Map()[key]` holds the value of that key's **last** occurrence
in `ps` (`Map()` iterates forward, so later entries overwrite earlier
ones). `Map()` never returns nil, and mutating the returned map never
affects `ps`.
- Test: `FuzzParamsMap`, `TestProp_ParamsMapLengthInvariant`
- Last verified: 2026-09-25 (`FuzzParamsMap`: 36s, 2.32M execs, 0 crashes, 24 corpus entries persisted to `corpora/FuzzParamsMap/`; `TestProp_ParamsMapLengthInvariant`: 100 rapid runs, 0 failures)
- Fail-count: 0

## I-01d — Handle() idempotency and conflict safety
Registering the same `(method, pattern)` twice panics with a message
containing "already registered", and the tree remains fully usable
afterwards — the original registration stays reachable via `Lookup`.
- Test: `TestProp_HandleIdempotencyAndConflict`
- Last verified: 2026-09-25 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-SERVEFILES-03 — ServeFiles registers both GET and HEAD
`ServeFiles(prefix, root)` registers the prefix for **both** GET and HEAD
(`mux.go` calls `m.Handle` twice); no other method resolves at that prefix.
Distinct from I-SERVEFILES-01/02 (escape/panic safety, which only exercise
GET) — this pins the two-route registration shape itself.
- Test: `TestProp_ServeFilesRegistersTwoRoutes`
- Last verified: 2026-09-25 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-05b — Group middleware order preserved across tiers (NEW)
Top-level `mux.Use()` middleware runs strictly before `Group.Use()`
middleware for any request reaching a route registered through that group;
within each tier, execution order equals registration order. Distinct from
I-05 (`TestProp_MiddlewareOrderPreserved`, top-level only) and
`TestProp_WithGroupMiddleware` (presence/absence via `With()`, not
cross-tier ordering) — no prior invariant covered the *composition* of the
two tiers.
- Test: `TestProp_GroupMiddlewareOrderPreserved`
- Last verified: 2026-09-25 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-WALK-01 — Walk()/Routes() consistency and early termination
`Walk()` surfaces every registered route exactly once, agrees with
`Routes()` on the full registered set, and honours callback-error early
termination (stops after the first non-nil error and propagates it).
- Test: `FuzzWalkRoutes`
- Last verified: 2026-09-25 (35s, 2.16M execs, 0 crashes after fix; 195 corpus entries persisted to `corpora/FuzzWalkRoutes/`)
- Fail-count: 1 — **real defect found and fixed**, see FINDING FPE-O14-002 below
- Regression: `testdata/fuzz/FuzzWalkRoutes/71133f44a5fa6f74` (patterns `["/", "0", "/{"]`), plus root-package `TestUnclosedRegexParamPanicsWithoutCorruptingTree`

## I-03b — Compress buffers multiple w.Write() calls correctly
A handler that calls `w.Write()` more than once has its concatenated
output round-trip through gzip exactly, the same as a single-write body of
equal total length.
- Test: `FuzzCompressMultipleWrites`
- Last verified: 2026-09-25 (37s, 118K execs — lower exec/sec than other targets because seeds/mutations include buffers up to 8MB gzipped at level 5, not a hang; 6 corpus entries persisted to `corpora/FuzzCompressMultipleWrites/`)
- Fail-count: 0

## I-ERROR-01 — mm.Error(code, err) round-trips its full contract
`Error(code, err).StatusCode() == code`, `.Error() == err.Error()`, and
`errors.Is(Error(code, err), err)` for any code and any error. Separately,
`Error(code, nil)` panics (deterministic check, not itself fuzzed).
- Test: `TestProp_ErrorStatusCodePreserved`, `TestErrorNilPanics`
- Last verified: 2026-09-25 (100 rapid runs, 0 failures)
- Fail-count: 0

## I-HANDLE-02 — Distinct non-conflicting static patterns coexist
Two distinct static patterns (bounded ASCII charset: `a-z`, `0-9`, `/`)
registered on the same Mux either both succeed (both then reachable via
`Lookup`) or the second panics with a documented conflict message — never a
silent loss of the first pattern's route.
- Test: `FuzzMuxHandleTwice`
- Last verified: 2026-09-25 (35s, 1.80M execs, 0 crashes, 29 corpus entries persisted to `corpora/FuzzMuxHandleTwice/`)
- Fail-count: 0
- Adapted from the removed original: restricted to a bounded ASCII charset instead of the original's goroutine + 2s time-box, which existed only to work around a separate, already-tracked pathological-growth issue on adversarial UTF-8 input (out of scope for the coexistence property itself).

## FINDING FPE-O14-002 — Unclosed regex-param `{` silently overwrites an existing route (FIXED)
**Severity:** Medium (registration-time footgun, not a live runtime attack
surface — MuxMaster routes are read-only after startup, so this requires a
trusted operator/developer registering a malformed pattern; still a
correctness defect that violates the project's "malformed patterns panic
loudly" design principle used everywhere else in `tree.go`).
**Found by:** `FuzzWalkRoutes`, corpus entry `71133f44a5fa6f74`, patterns
`["/", "0", "/{"]`.

**Root cause:** `findWildcard` (`tree.go`, `'{'` case) returned
`(token="", start=-1, valid=false)` when a path segment contained an
unclosed `{` (no matching `}` before the next `/` or end of path).
`addRoute`'s `walk()` loop and `insertChild`'s own loop both treat
`start < 0` as "this segment has no wildcard marker at all" and take the
"plain static" exit — but in `addRoute`, when the current node `n` had no
existing `wildChild` and no matching static index for the byte `'{'`, it
called `n.insertChild(path, fullPath, handler, fast)` directly on the
**existing** node `n` (not a fresh one). `insertChild`'s `i < 0` exit then
fell through to its final, unconditional
`n.path = path; n.handler = handler; n.fast = fast; n.pattern = fullPath`
— silently discarding whatever route `n` already held, with **no panic**
and **no conflict signal**.

Concretely: on a Mux with only `"/"` registered, `mux.GET("/{", h)` did not
panic, and `Lookup("/")` afterwards returned `found=false` — the `"/"`
route had been silently replaced by a literal static route `"/{"`.
Confirmed minimal via `["/{", "/{a"]` (both destroy an existing `"/"`)
vs. `["/}", "/(", "/{a:x}"]` (all leave `"/"` intact) — isolating the cause
to specifically an unclosed `'{'`, not any of `':'`/`'*'`/other punctuation
(those two always return `start >= 0` from `findWildcard`, so they never
hit this fallthrough).

**Fix (tree.go):** `findWildcard`'s `'{'` case now returns the wildcard's
start position (not `-1`) when no closing `'}'` is found, keeping
`valid=false`. This routes the malformed pattern into `insertChild`'s
existing `if !valid { panic(...) }` branch — the same branch already used
for "more than one wildcard marker in a path segment" — instead of the
silent-overwrite fallthrough. The panic message is disambiguated for the
unclosed-brace case: `"muxmaster: regex param '{' in path '<pattern>' is
missing its closing '}'"`.

**Regression tests:**
- `harness/fuzz_o14_restored_test.go::FuzzWalkRoutes` (found it; corpus entry persisted)
- `tree_unclosed_regex_param_test.go::TestUnclosedRegexParamPanicsWithoutCorruptingTree` (root package; 3 cases: bare root, named-but-unclosed at root, unclosed brace as a static sibling)

**Verification:** root package `go test -race -count=3 .` — clean before
and after the fix (the bug was a silent-overwrite logic error, not a race);
`go vet ./...` — clean. `FuzzWalkRoutes` re-run 35s / 2.16M execs clean
after the fix, including replay of the failing corpus entry.

**Cross-reference:** this is unrelated to FPE-008 (`findings.md` line 439,
`Handle()` copy-on-write / registration-panic tree corruption, **already
fixed** by two-phase registration with rollback, MM-2026-0033). FPE-008's
fix does not cover this case because no panic occurred here — the original
`insertChild` code path completed "successfully" from the tree's
perspective; it just overwrote the wrong node's state. Recommend
`path-routing-fuzzer` sweep for any other `findWildcard`/`insertChild`
callers that could reach `n.insertChild` on a non-fresh node with a
similarly malformed wildcard token, since this class of bug (a caller
mistakenly treating an *existing* node as a fresh one) is not obviously
excluded elsewhere in `tree.go` by inspection alone.

## I-17 — StripSlashes idempotency and Path/RawPath sync (MM-2026-0025 restored)
For any `r.URL.Path`/`r.URL.RawPath` pair a real `*http.Request` could carry
(RawPath empty, or a valid net/url encoding of Path — `u.EscapedPath() ==
u.RawPath`):
- `StripSlashes()` never panics;
- the root path `"/"` is never stripped further, and the result is never
  empty;
- applying `StripSlashes` once vs. twice yields an identical
  `(r.URL.Path, r.URL.RawPath)` pair (MM-2026-0025, `findings.md`,
  unconditional idempotency — the original FPE-003 "strips only one
  trailing slash" carve-out is gone now that the fix is in place);
- the output `(Path, RawPath)` pair stays valid per the same net/url
  contract.
- Test: `FuzzStripSlashesIdempotency` (this file's restoration);
  deterministic pins: `middleware/middleware_test.go::TestStripSlashes_MultipleTrailing`,
  `middleware/middleware_test.go::TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync`
- Last verified: 2026-09-25 (45s, 2,415,577 execs, ~53k execs/sec, 0 crashes,
  75 corpus entries persisted to `corpora/FuzzStripSlashesIdempotency/`;
  `go test -race -count=3` clean, 89 subtests × 3 = 267 passes; `go vet`
  clean)
- Fail-count: 0 (after the fix below)

## FINDING FPE-O14-003 — StripSlashes desynchronises Path/RawPath on an encoded trailing slash (FIXED)
**Severity:** Medium (path-confusion class — CWE-706/707 family, same family
as MM-2026-0025). No CVE/finding ID has been assigned in `findings.md`
because this task's scope explicitly excludes editing that file; the
threat-modeler or a future task should assign one and cross-link it to
MM-2026-0025.

**Found by:** `FuzzStripSlashesIdempotency` restoration work (rmp #274 part
5b), while designing the "RawPath stays a valid net/url encoding of Path"
half of this file's I-17 invariant — confirmed deterministically before any
fuzz run (`url.ParseRequestURI("/a%2f")` → `Path="/a/"`, `RawPath="/a%2f"`,
a pair any client sending the raw request-target `/a%2f` produces), then
also rediscovered by the fuzzer itself within the seed corpus.

**Root cause:** `middleware/strip_slashes.go`'s `RawPath` branch stripped
trailing bytes equal to the literal ASCII `'/'` from `r.URL.RawPath`,
independently of the loop stripping trailing `'/'` characters from
`r.URL.Path`. A decoded trailing slash can be spelled in the raw
request-target either literally or percent-encoded (`%2F`/`%2f`, RFC 3986
§2.1); net/url preserves that literal spelling in `RawPath` whenever it
differs from the default re-escaping of `Path`. For input `Path="/a/"`,
`RawPath="/a%2f"`, the `Path` loop stripped the trailing `/` to produce
`"/a"`, but the `RawPath` loop found no trailing `/` byte (the last byte of
`"/a%2f"` is `'f'`) and left `RawPath` untouched at `"/a%2f"` — which still
decodes to `"/a/"`. The two disagree post-strip: a `UseRawPath=true`
consumer downstream, a reverse proxy re-deriving a target from `RawPath`, or
a log line, would see a different path than the one `Path`-based routing
just acted on.

**Fix (`middleware/strip_slashes.go`):** the `Path`-stripping loop now also
counts `n`, the number of trailing `/` characters it removed. A new
`stripTrailingPathSeparators(rp string, n int) string` helper then walks
`RawPath` from the end exactly `n` times, consuming one path-separator
*token* per iteration — either a literal `'/'` byte or a `"%2F"`/`"%2f"`
3-byte triplet — instead of a fixed byte-suffix loop. For any Path/RawPath
pair net/http's URL parser can produce, this keeps the two in sync by
construction (proof sketch: `u.EscapedPath() == u.RawPath` implies
`unescape(RawPath) == Path`, so RawPath's trailing separator tokens, when
decoded, correspond 1:1 in count and position with Path's trailing `/`
characters).

**Regression test:**
`middleware/middleware_test.go::TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync`
(5 cases: `%2f`, `%2F`, nested segment, double-encoded, mixed
literal+encoded) — each asserts both the exact expected `(Path, RawPath)`
and `u.EscapedPath() == RawPath` on the output.

**Verification:** `go test -race ./middleware/... ./...` clean;
`go vet ./...` clean (both root module and harness module);
`FuzzStripSlashesIdempotency` 45s / 2.4M execs clean after the fix,
including replay of the encoded-slash regression seeds now baked into the
fuzzer's `f.Add()` corpus.

**Cross-reference:** related to but distinct from MM-2026-0025 (FPE-003,
"strips only one trailing slash") — that bug was about *how many* trailing
slashes get stripped; this one is about *Path and RawPath disagreeing on
how many were actually removed*. Recommend `path-routing-fuzzer` check
whether `middleware/clean_path.go` has an analogous RawPath-stripping
routine with the same class of bug (a quick read of `clean_path.go` during
this task did not show a byte-suffix RawPath loop of this shape, but it
was not in scope to fuzz here).
