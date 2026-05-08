# MuxMaster Invariants (fuzz/property-checked)
Version: 2026-05-07-S9 | Commit: e30ae94 | Go: 1.26

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

## I-REGEX-01 — Regex params: valid regex (without '}') registers without panic (H8-52)
Any syntactically valid Go regex that does not contain a `}` byte registers as a `{name:expr}` param without panic. Regex with `}` in the expression body fails due to the tree parser's delimiter search (known limitation FPE-2026-REGEX-01).
- Test: `FuzzRegexParamRegistration`
- Last verified: 2026-05-07 (60s, 1.8M execs, 0 crashes post-fix)
- Finding: FPE-2026-REGEX-01 (sev=3) — `}` in regex body causes confusing panic
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
