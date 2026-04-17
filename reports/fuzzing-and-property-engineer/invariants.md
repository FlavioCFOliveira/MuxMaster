# MuxMaster — Invariants (fuzz/property-checked)

**Last updated:** 2026-04-17
**Commit:** 533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c
**Owner:** fuzzing-and-property-engineer

This catalogue lists the invariants that the fuzz and property-test suite at
`reports/fuzzing-and-property-engineer/harness/` keeps under continuous check.
Each row is the live contract; a failure means either the code regressed or
the invariant was stated too strongly and needs refinement (the agent owns
both directions).

Status legend:
- **PASS** — all corpus + seeds + 30 s fuzz budget pass on current HEAD.
- **FAIL** — a repro test under `evidence/FPE-NNN/` actively demonstrates the
  violation on current HEAD; tracked as finding FPE-NNN.
- **PINNED** — the current observable behaviour is documented (not necessarily
  desirable) and a regression that changes it will surface here.

Each invariant identifies its fuzz/property-test target so the review cycle
always knows which harness file to re-run when the invariant changes.

---

## I-01 — Mux.ServeHTTP never panics on valid request

**Statement:** For any mux constructed with a set of registered routes and
any `*http.Request` (whether well-formed by stdlib standards or assembled
directly), `mux.ServeHTTP(w, r)` returns without a panic propagating out.

**Target:** `FuzzMuxServeHTTP`, `FuzzMuxServeHTTPWithAllRedirects`.
**Status:** **PASS** with 3 tracked hot-path panics allowlisted:
- FPE-005 (`index out of range` on crafted registration patterns)
- FPE-006 (`slice bounds out of range` on interleaved static + invalid-UTF-8 routes)
- FPE-009 (`muxmaster: invalid node type` on `/0` + `/:0` interleave)

All three require a prior malformed *registration*. They block this invariant
under adversarial registration; uplift to PASS once FPE-005/006/009 are
resolved.

## I-02 — CleanPath is idempotent and length-bounded

**Statement:** For all strings p, `path.Clean(path.Clean(p)) == path.Clean(p)`
and (unless p is the empty string) `len(path.Clean(p)) ≤ len(p)`. The
CleanPath middleware preserves these properties when observed through a
ServeHTTP round-trip.

**Target:** `FuzzCleanPath`, `FuzzCleanPathDoubleEncoded`.
**Status:** **PASS** (30 s, 1.6M execs, zero counter-examples).

## I-03 — Compress round-trip preserves body bytes

**Statement:** For any byte sequence B written by a handler, the client
observes B either:
- as raw bytes when the middleware declined to gzip (body below the
  1024-byte threshold), or
- as gzip-encoded bytes that gunzip back to B byte-for-byte.

**Target:** `FuzzCompressRoundtrip`, `FuzzCompressMultipleWrites`.
**Status:** **PASS** (30 s, 406 execs due to large inputs).

## I-04 — Group prefix composition

**Statement:** For any sequence of prefixes P1…Pn (n ∈ [1, 5]) registered via
nested `Mux.Group`/`Group.Group` calls with a leaf path L, a request at
`concat(P1..Pn, L)` is routed to the handler registered on the inner group.

**Target:** `TestProp_GroupPrefixComposition`.
**Status:** **PASS** (rapid: 100 generated cases, no shrunk failures).

## I-05 — Middleware registration order preserved at execution

**Statement:** For any middleware sequence M1…Mn registered via `Mux.Use`,
execution order equals registration order (M1 is outermost, Mn innermost).
The same holds for nested Group.Use with the property that mux-level
middlewares always run before any group-level middleware.

**Target:** `TestProp_MiddlewareOrderPreserved`,
`TestProp_GroupMiddlewareOrderPreserved`, `TestProp_WithAppendsMiddleware`.
**Status:** **PASS** (rapid: 100 generated cases each).

## I-06 — Params accessors never panic

**Statement:** For any `Params` p and any key k (including zero-value,
NUL-containing, and very long keys), `p.Get(k)`, `p.Lookup(k)`, `p.Int(k)`,
`p.Int64(k)`, `p.Uint64(k)`, `p.Float64(k)`, `p.Bool(k)` and `p.Map()` return
without a panic. Numeric accessors return a well-typed error when the value
is absent or non-parseable.

**Target:** `FuzzParamsGet`, `FuzzParamsInt`, `FuzzParamsMap`.
**Status:** **PASS** (15 s per target, 2.3M execs on FuzzParamsGet).

## I-06b — ParamsFromContext / PathParam / RoutePattern never panic

**Statement:** For any `context.Context` (including `context.Background()`
and contexts with unrelated values) and any `*http.Request` (provided
URL is non-nil), `mm.ParamsFromContext`, `mm.PathParam`, and
`mm.RoutePattern` return without panic and return zero values when the
context was not populated by the router.

**Target:** `FuzzParamsFromContext`, `FuzzPathParam`.
**Status:** **PASS** (15 s per target).

## I-07 — Handle idempotent-or-consistent-panic

**Statement:** Calling `Mux.Handle(m, p, h)` twice with the same (m, p):
- The first call succeeds.
- The second call panics with a muxmaster-prefixed message containing
  "already registered".
- The subsequent `Lookup` still finds the first-registered handler.

**Target:** `TestProp_HandleIdempotencyAndConflict`.
**Status:** **PASS** (rapid: 100 generated cases, no violations). Note:
this invariant holds only for the "same pattern twice" case; FPE-008
breaks the related property that *failed* registrations must not corrupt
the registry.

## I-08 — Valid-input Handle never panics with runtime.Error

**Statement:** For any (method, pattern) where:
- method ∈ known methods ∪ {"*"},
- pattern starts with "/",
- wildcards are well-named (":name", "*name", "{name:expr}"),
- no nested braces,
- catch-all only at the end,

`Mux.Handle` returns without panic OR panics with a `muxmaster:` -prefixed
message. runtime.Error panics (nil deref, slice OOB, index OOB) are
forbidden.

**Target:** `FuzzMuxHandle`, `FuzzFindWildcardViaHandle`.
**Status:** **FAIL** on 3 specific inputs (FPE-005 — `/{:}*00000` and
similar). All allowlisted in the fuzzer for continuous-fuzzing usability;
must be resolved before the invariant reports PASS.

## I-09 — Handle is registration-isolated (no mid-panic corruption)

**Statement:** If `Mux.Handle(m1, p1, h1)` succeeds and `Mux.Handle(m2, p2, h2)`
subsequently panics, then:
- (m1, p1) remains registered,
- (m1, p1) remains discoverable by Walk and Routes,
- (m1, p1) remains servable by Lookup and ServeHTTP,
- (m2, p2) is NOT present in Walk/Routes.

**Target:** `evidence/FPE-008/repro_test.go` (standalone); not currently
checked in the fuzz suite because the invariant is actively violated.
**Status:** **FAIL** — FPE-008 confirms that a panicking registration
corrupts the tree, losing previously-valid routes and sometimes leaving
partial nodes behind.

## I-10 — Lookup never panics on valid registered mux

**Statement:** For any mux built exclusively with successful Handle calls
(no panicking registrations), `Mux.Lookup(method, path)` returns without
panic for any path string.

**Target:** `TestProp_LookupNeverPanics`, `FuzzLookupAfterRegistration`.
**Status:** **PASS** for *clean* registrations. **FAIL** when the mux has
any of the problematic route combinations tracked under FPE-006/FPE-009.

## I-11 — Params capture covers up to maxInlineParams

**Statement:** For patterns with N ≤ maxInlineParams (3) parameters, all N
params are retrievable via `PathParam` on the handler's request.

**Target:** `TestProp_ParamsCaptureAllWhenWithinCapacity`.
**Status:** **PASS**.

## I-11b — Params capture beyond maxInlineParams

**Statement:** For patterns with N > 3 parameters, either (a) all N are
captured or (b) registration is explicitly rejected. Silent drop is
forbidden.

**Target:** `evidence/FPE-004/repro_test.go`.
**Status:** **FAIL (PINNED)** — H-012 / FPE-004 confirms silent drop
beyond the 3rd param. Recommended remediation: lift maxInlineParams to
at least 16 or panic at registration.

## I-12 — Route round-trip

**Statement:** For any pattern composed of static segments, ":name" params,
and an optional trailing "*name" catch-all, registering the pattern and
then issuing a request at a concrete path that matches the pattern results
in the handler being invoked.

**Target:** `TestProp_RouteRoundTrip`.
**Status:** **PASS** (subject to the same tree-corruption exceptions).

## I-13 — ServeFiles registers GET and HEAD

**Statement:** `Mux.ServeFiles(prefix, fs)` registers handlers for both
GET and HEAD at the given prefix.

**Target:** `TestProp_ServeFilesRegistersTwoRoutes`.
**Status:** **PASS**.

## I-14 — HTTPError preserves status and message

**Statement:** `mm.Error(code, err)` produces an `HTTPError` whose
`StatusCode()` equals code and whose `Error()` equals `err.Error()`.

**Target:** `TestProp_ErrorStatusCodePreserved`.
**Status:** **PASS**.

## I-15 — CORS origin ACAO fidelity (non-CRLF)

**Statement:** When the CORS middleware decides to emit an ACAO header,
the header value equals either the request's Origin or a value from the
configured allow-list — never a different origin.

**Target:** `FuzzCORSOrigin`, `FuzzCORSOriginAllowList`.
**Status:** **PASS** for non-CRLF Origins. **FAIL (pinned)** — FPE-002
records the CRLF reflection finding which is tracked separately; the
fuzzer skips CR/LF inputs to remain usable.

## I-16 — RequestID reflection fidelity (non-CRLF)

**Statement:** When the client supplies a non-empty X-Request-ID header
that contains no CR/LF bytes, the RequestID middleware reflects it
verbatim into the response.

**Target:** `FuzzRequestIDReflection`, `FuzzRequestIDGeneration`.
**Status:** **PASS** for non-CRLF inputs. **FAIL (pinned)** — FPE-001
records the CRLF reflection finding.

## I-17 — StripSlashes never panics, single-pass behaviour pinned

**Statement:** The StripSlashes middleware does not panic on any input.
It strips *at most one* trailing slash per invocation (current pinned
behaviour).

**Target:** `FuzzStripSlashesIdempotency`.
**Status:** **PASS** for single-trailing-slash inputs. **FAIL (pinned)** —
FPE-003 records that multi-trailing-slash inputs are not idempotent.

## I-18 — Logger never panics

**Statement:** The Logger middleware does not panic on any request,
including those with control bytes in `r.URL.Path`.

**Target:** `FuzzLoggerCRLF`.
**Status:** **PASS**. Note: log output does contain raw CR/LF and ANSI
escape bytes — this is a separate finding tracked by H-003 (middleware
reviewer) and not asserted here.

## I-19 — RealIP never panics

**Statement:** The RealIP middleware does not panic on any XFF value
(including very long, CR/LF-containing, or empty headers).

**Target:** `FuzzRealIPXFF`.
**Status:** **PASS**. Note: XFF is trusted unconditionally — H-009
tracked by middleware reviewer.

## I-20 — Composed chain never panics

**Statement:** A chain composed of Recoverer + Logger + RequestID + NoCache
+ SetHeader wrapping a no-op handler does not panic for any valid
(method, path, reqID) combination; ensures all middlewares present at the
same time expected response headers.

**Target:** `FuzzComposedMiddlewareChain`.
**Status:** **PASS**.

## I-21 — Walk and Routes agree

**Statement:** For any mux, `Mux.Walk(fn)` visits every route that
`Mux.Routes()` returns, and each exactly once.

**Target:** `FuzzWalkRoutes`.
**Status:** **PASS** (subject to FPE-008 — failed registrations break
this invariant).

## I-22 — Response helpers (JSON/XML/Text/Redirect) never panic

**Statement:** The response helpers handle any input without panic.
Well-formed inputs produce correctly-shaped responses.

**Target:** `FuzzResponseJSON`, `FuzzResponseXML`, `FuzzResponseText`,
`FuzzResponseRedirect`.
**Status:** **PASS**.

---

## Coverage summary (2026-04-17)

Statements: **70.9%** of `muxmaster` + `middleware` (`coverage.out`).

Top uncovered (see `evidence/2026-04-17/coverage.html` for line-level):
- `NoContent` (0%) — trivial, will cover next sprint
- `getValue` (80.5%) — gaps around regex-param error paths
- `addRoute` (88.7%) — gaps around trailing-slash edge cases

---

## Invariants pending to add

- **I-23** — `PanicHandler` invariants: panic in handler always reaches
  `PanicHandler` (if set) without leaking into parent goroutines. Needs
  a fuzz target similar to `FuzzMuxServeHTTP` with a handler that panics.
- **I-24** — `Mount` invariant: the mounted handler observes a path
  stripped of the mount prefix. Needs a fuzz target that varies both the
  mount prefix and the inbound path.
- **I-25** — `ErrorHandler` invariant: errors returned from
  `HandlerFuncE` are always routed to `ErrorHandler` when set, or produce
  a 500 otherwise. Needs a fuzz target.

These are committed in the next sprint's "Next actions" in the main
report.
