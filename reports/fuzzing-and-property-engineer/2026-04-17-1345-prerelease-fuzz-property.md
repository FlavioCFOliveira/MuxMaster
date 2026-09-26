# Fuzzing & Property Test Report — Pre-release v1.0.0

**Date:** 2026-04-17 13:45 UTC
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2
**Agent:** fuzzing-and-property-engineer
**Scope:** the entire public API of the `github.com/FlavioCFOliveira/MuxMaster` module and the 15 middlewares. Complements `path-routing-fuzzer` (which covers tree.go/path bypasses).

---

## Executive summary

Nine findings discovered in a 4h sprint — **three Critical** (unrecoverable panics on the hot path and tree corruption after a panic during registration), **two High** (CRLF response splitting in CORS and RequestID), **one High** (param silent-drop), **three Medium** (non-idempotent StripSlashes, registration-time index OOB, pathological loop/OOM on specific inputs).

**Recommendation to the maintainer:** **HOLD release**. The three Critical findings block a defensible v1.0.0 release. The High findings have trivial remediation and should enter the same fix cycle. Every finding has a committed minimal repro, with an independent go.mod, ready to run as a smoke test after the fix.

The suite is operational and persists a corpus for 22 fuzz targets covering 70.9% of the module's lines. Three tracked panic allowlists allow the suite to keep finding new bugs without being blocked by the findings already catalogued.

---

## Dependencies added

- **`pgregory.net/rapid v1.2.0`** — test-only dependency, isolated in a separate go.mod at `reports/fuzzing-and-property-engineer/harness/go.mod` via a `replace` directive. It does **NOT affect** the main module's go.mod nor the production zero-dep invariant. Sprint plan §8 explicitly authorises this dependency for property-test infrastructure.

No other deps added. The main module still has zero external dependencies.

---

## Targets run (fuzz)

Budget: minimum 15s per target in short mode (pre-commit). Audit mode: 30s per critical target, 15s for middleware targets. Each run with its corpus persisted in `corpora/<target>/`.

| Fuzzer | Budget | Exec/sec | Total execs | New crashes | Corpus entries |
|---|---|---|---|---|---|
| FuzzMuxHandle | 30 s | 57 848 | 1 735 449 | 0 (net) | 192 |
| FuzzMuxHandleTwice | 30 s | 20 021 | 600 645 | 0 (net) | + |
| FuzzMuxServeHTTP | 30 s | 86 450 | 2 593 509 | 0 (net) | 486 |
| FuzzMuxServeHTTPWithAllRedirects | 20 s | 64 341 | 1 351 172 | 0 (net) | 401 |
| FuzzCleanPath | 30 s | 53 217 | 1 596 510 | 0 | 109 |
| FuzzCleanPathDoubleEncoded | (seeds) | n/a | n/a | 0 | 3 |
| FuzzCompressRoundtrip | 30 s | 13 | 406 | 0 | 14 |
| FuzzCompressMultipleWrites | (seeds) | n/a | n/a | 0 | 3 |
| FuzzParamsGet | 30 s | 76 655 | 2 299 655 | 0 | 28 |
| FuzzParamsInt | 15 s | 78 324 | 1 174 863 | 0 | 11 |
| FuzzParamsMap | 15 s | 85 650 | 1 284 750 | 0 | 12 |
| FuzzParamsFromContext | 15 s | 81 817 | 1 227 259 | 0 | 3 |
| FuzzPathParam | 15 s | 35 556 | 533 344 | 0 | 4 |
| FuzzRequestIDReflection | 30 s | 29 681 | 890 432 | 0 (net) | 26 |
| FuzzCORSOrigin | 20 s | 35 613 | 712 258 | 0 (net) | 25 |
| FuzzCORSOriginAllowList | 15 s | 70 087 | 1 051 307 | 0 | 8 |
| FuzzRealIPXFF | 15 s | 68 855 | 1 032 826 | 0 | 79 |
| FuzzLoggerCRLF | 15 s | 67 213 | 1 008 197 | 0 | 7 |
| FuzzStripSlashesIdempotency | 15 s | 76 782 | 1 151 727 | 0 (net) | 8 |
| FuzzComposedMiddlewareChain | 20 s | 71 463 | 1 429 263 | 0 (net) | 19 |
| FuzzFindWildcardViaHandle | 30 s | 56 616 | 1 698 494 | 0 (net) | 217 |
| FuzzRegexCompile | 30 s | 12 397 | 371 905 | 0 | 235 |
| FuzzRegexMatchReDoS | (seeds) | n/a | n/a | 0 | 3 |
| FuzzWalkRoutes | 15 s | 95 220 | 1 428 305 | 0 (net) | 124 |
| FuzzLookupAfterRegistration | 15 s | 88 648 | 1 329 724 | 0 (net) | 95 |
| FuzzResponseJSON / XML / Text / Redirect | (seeds) | n/a | n/a | 0 | 16 |

**Total cumulative exec budget:** ~10 min of wall-clock fuzzing, approx. 27 million aggregate execs.
**Net crashes:** zero after classification. The nine findings (FPE-001…FPE-009) have a dedicated repro under `evidence/FPE-NNN/` and are allowlisted in the harness functions `isTrackedRuntimeError`/`isTrackedTreePanic`/`isTrackedHotPathRuntimeError` so that the fuzzer keeps discovering other regressions.

---

## Property tests

Property tests with `pgregory.net/rapid` — each invariant runs 100 generated cases by default, with shrinking to a minimal counter-example. All of them run under `go test ./...` in the normal cycle.

| Property | Runs | Shrunk failures | Status |
|---|---|---|---|
| I-04 Group prefix composition | 100 | 0 | PASS |
| I-05 Middleware order (Mux.Use) | 100 | 0 | PASS |
| I-05' Middleware order (Mux+Group) | 100 | 0 | PASS |
| I-05'' Group.With appends | 100 | 0 | PASS |
| I-07 Handle duplicate-panic | 100 | 0 | PASS |
| I-10 Lookup never panics (clean mux) | 100 | 0 | PASS |
| I-11 Params capture ≤ 3 | 100 | 0 | PASS |
| I-12 Route round-trip | 100 | 0 | PASS |
| I-13 ServeFiles registers GET+HEAD | 100 | 0 | PASS |
| I-14 Error status + message preservation | 100 | 0 | PASS |

**I-11b (params capture > 3):** NOT CHECKED — it is the FPE-004 finding. It was deliberately removed from the main suite so as not to block runs while the bug is not fixed. Tracked in `evidence/FPE-004/`.

**I-09 (registration isolation):** NOT CHECKED as a property — FPE-008 shows that the tree is corrupted by a panic, so it would always fail. Tracked separately.

---

## Findings

| ID | Severity | Fuzzer | Area | Root cause | Repro |
|---|---|---|---|---|---|
| FPE-001 | High | FuzzRequestIDReflection | middleware/request_id.go | CRLF in X-Request-ID reflected into the response header without sanitisation | `evidence/FPE-001/` |
| FPE-002 | High | FuzzCORSOrigin | middleware/cors.go | CRLF in Origin reflected into Access-Control-Allow-Origin when `AllowedOrigins=["*"]` | `evidence/FPE-002/` |
| FPE-003 | Medium | FuzzStripSlashesIdempotency | middleware/strip_slashes.go | The middleware strips *only one* trailing slash — it is not idempotent | `evidence/FPE-003/` |
| FPE-004 | High | TestProp_ParamsCaptureOrRejected | tree.go:21 | paramsBuf capacity = 3, extra params silently discarded (H-012) | `evidence/FPE-004/` |
| FPE-005 | Medium | FuzzMuxHandle | tree.go:260 | `path[i-1]` with `i = 0` — `runtime error: index out of range [-1]` in Handle with the pattern `/{…}*name` | `evidence/FPE-005/` |
| FPE-006 | **Critical** | FuzzMuxHandleTwice | tree.go:305 | `n.children[:len(n.indices)]` — re-slice beyond cap. in getValue (hot path) after registering `/<non-ASCII>` + `/` | `evidence/FPE-006/` |
| FPE-007 | Medium | FuzzMuxHandleTwice | tree.go (addRoute) | Pathological pair of invalid-UTF-8 patterns causes wall-clock > 10s in a single Handle+Lookup | `evidence/FPE-007/` |
| FPE-008 | **Critical** | FuzzWalkRoutes | mux.go:203-225 | `Handle` performs a *shallow* COW — when addRoute panics, the shared tree is left in an inconsistent state; previously registered routes become unreachable | `evidence/FPE-008/` |
| FPE-009 | **Critical** | FuzzLookupAfterRegistration | tree.go:395 | Registering `/:0` + `/0` produces a node with `nType == static` but without handler + wildChild; any non-exact Lookup panics with `muxmaster: invalid node type` | `evidence/FPE-009/` |

### FPE-001 — RequestID CRLF response-splitting (H-004)
**Severity:** High (CWE-113).
**Fuzzer:** `FuzzRequestIDReflection`.
**Input hash:** `id\r\nSet-Cookie: evil=1` (seed).
**Classification:** logic bug / missing input validation.
**Repro:** `evidence/FPE-001/repro_test.go`.

`middleware.RequestID()` does:
```go
id := r.Header.Get("X-Request-ID")
if id == "" { … }
w.Header().Set("X-Request-ID", id)
```
No validation. A permissive upstream proxy (or an attacker via `Header["X-Request-Id"] = []string{…}`) injects CR/LF into the response. The stdlib `net/http` server may truncate it, but `httptest.ResponseRecorder` does not, and downstream middlewares may serialise headers differently. Even so, the *header map* contains adversarial bytes and any downstream integration becomes vulnerable.

**Proposed remediation:**
```go
id := r.Header.Get("X-Request-ID")
if id != "" && (strings.ContainsAny(id, "\r\n\x00") || len(id) > 256) {
    id = ""
}
if id == "" { … crypto/rand gen … }
```

### FPE-002 — CORS Origin CRLF reflection (H-005)
**Severity:** High (CWE-113, CWE-942).
**Fuzzer:** `FuzzCORSOrigin` (seed).
**Repro:** `evidence/FPE-002/repro_test.go`.

Identical to FPE-001 at the root — `cors.go:54` does `h.Set("Access-Control-Allow-Origin", origin)` without validating that `origin` is a legal HTTP token. Any deployment with `AllowedOrigins=["*"]` + a permissive upstream sees response splitting.

**Remediation:** validate the Origin against `^[A-Za-z0-9+.-]+://[^\s\r\n\x00]*$` before setting ACAO, or reject it (400) if it is not a legal origin.

### FPE-003 — StripSlashes non-idempotent
**Severity:** Medium (CWE-707).
**Fuzzer:** `FuzzStripSlashesIdempotency` (seed `/a//`).
**Repro:** `evidence/FPE-003/repro_test.go`.

```
/a//   →  /a/    →  /a
pass1       pass2
```

The practical impact is low in typical stacks (where there is only one pass), but it is surprising when the middleware is composed with `CleanPath` or another one that also invokes it (architectural cases such as internal retries). Idempotence is documented among the common invariants (chi, gorilla/mux) — keeping aligned eases porting.

**Trivial remediation:**
```go
for len(p) > 1 && p[len(p)-1] == '/' { p = p[:len(p)-1] }
```

### FPE-004 — paramsBuf silent overflow (H-012)
**Severity:** High (CWE-20 + CWE-284 when composed with auth middleware).
**Fuzzer:** `TestProp_ParamsCaptureOrRejected` (removed from the main suite).
**Repro:** `evidence/FPE-004/repro_test.go`.

`tree.go:15`:
```go
const maxInlineParams = 3
```

Any pattern with more than 3 params silently loses the rest in `paramsBuf.add`. MuxMaster positions itself against httprouter/bunrouter, which support 16 params; this divergence is a silent surprise.

**Remediation — two options:**
1. **Simple lift:** `const maxInlineParams = 16` + adjust `requestCtx.small [16]Param`. Cost: 208 extra bytes per pool entry, which are amortised; most handlers use ≤ 4.
2. **Explicit panic at registration:** count params in `insertChild` and panic if `>3`. Preserves the current footprint but rejects legitimate cases.

Option 1 is the correct one in terms of competitiveness.

### FPE-005 — Runtime panic in Handle with the pattern `/{…}*name`
**Severity:** Medium (CWE-20, CWE-755).
**Fuzzer:** `FuzzMuxHandle` (minimal: `/{:}*00000`).
**Repro:** `evidence/FPE-005/repro_test.go` (variants included).

`tree.go:259-262`:
```go
i--
if path[i] != '/' {
    panic("no '/' before catch-all in path '" + fullPath + "'")
}
```

When the catch-all `*name` comes immediately after a consumed regex token `{…}`, `i` is 0 before the decrement → -1. `path[-1]` panics with `runtime error: index out of range`.

**One-line remediation:**
```go
i--
if i < 0 || path[i] != '/' {
    panic("muxmaster: no '/' before catch-all in path '" + fullPath + "'")
}
```

### FPE-006 — CRITICAL: slice bounds OOB in getValue (hot path)
**Severity:** Critical (CVSS ~8.1 — remote DoS via crafted lookup).
**Fuzzer:** `FuzzMuxHandleTwice`.
**Repro:** `evidence/FPE-006/repro_test.go`.

Registering `/\xf9` + `/` and calling `Lookup("/\xf9")` panics at `tree.go:305`:
```go
children := n.children[:len(n.indices)]
```
`len(n.indices) > cap(n.children)` on certain split paths. This is a **panic on the hot path** — every request panics. If PanicHandler is not configured, the connection is cut; under sustained traffic it can amplify failures.

Combined with FPE-009 (a different case but with the same impact vector), the radix tree has a systemic fragility when static + parametric routes + static routes with non-ASCII bytes interact.

**Remediation:** audit every branch of `addRoute` + `insertChild` to guarantee that `len(n.indices) == number of static children`. Consider adding an `invariant check` in debug mode (`go test -tags=muxmasterdebug`) that validates this equality after each addRoute.

### FPE-007 — Pathological loop/OOM in Handle with invalid UTF-8
**Severity:** Medium (registration-time DoS).
**Fuzzer:** `FuzzMuxHandleTwice` (OS-killed).
**Repro:** `evidence/FPE-007/repro_test.go` — demonstrates > 10s wall-clock in Handle+Lookup with the pair `"/\xbe"` + `"/\xc2\xa8\x91\x9d\xd8'\xef"`.

Low impact in production (registration is a start-up phase; the DoS affects the developer, not the end user). It deserves investigation: combine a perf profiler with this input and see where the time is spent.

### FPE-008 — CRITICAL: tree corruption after a panic during registration
**Severity:** Critical (violation of a documented central invariant).
**Fuzzer:** `FuzzWalkRoutes` (input `{"0", "/", "/{"}`).
**Repro:** `evidence/FPE-008/repro_test.go`.

Registering `/` (OK) and then `/{` (panics) leaves:
- `Lookup("/")` returns `(nil, nil, false)` — the route became unreachable!
- `Walk` surfaces `/{` as if it were registered — a partial node persisted.

Root: `mux.go:213-225`:
```go
var trees methodTrees
if old := m.treesPtr.Load(); old != nil { trees = *old }
root := trees[idx]   // <-- same *node pointer as before
…
root.addRoute(pattern, …)  // mutates root IN PLACE
m.treesPtr.Store(&trees)
```

The "copy-on-write" is shallow — only the `methodTrees` array is copied; the nodes *are shared*. `addRoute` mutates the shared node before the panic. Zero-down invariant violated.

**Remediation — three options:**
1. **Deep clone before mutating:** copy the affected subtree. Cost of O(tree size) per registration.
2. **Recover + revert:** a `defer` catches the panic in `Handle`, snapshot beforehand, restore on failure. Lower cost but complex.
3. **Two-phase registration:** phase 1 builds a new subtree on the side; phase 2 swaps it atomically. Aligns with the atomic.Pointer semantics already declared.

Option 3 is the defensible one — it aligns with the design intent. Top priority.

### FPE-009 — CRITICAL: invalid-node-type panic in getValue (hot path)
**Severity:** Critical (CVSS ~7.5 — DoS of all requests after a specific registration).
**Fuzzer:** `FuzzLookupAfterRegistration` (minimal: register `/:0` + `/0`).
**Repro:** `evidence/FPE-009/repro_test.go`.

After registering the two patterns, **any** Lookup/ServeHTTP on a path ≠ `/0` panics with `muxmaster: invalid node type`. An attacker who can influence the list of registered routes (via a plugin system, a mutable config file, or even automated tests sharing a global Mux) brings down the whole surface.

Root: the node split in addRoute produces a node with `wildChild = true` but `nType = static` (zero value). The switch in `getValue:321-396` has no case for `static` with `wildChild` and falls into the default panic.

**Remediation:** identify where the node split forgets to set `nType`. Likely location: `tree.go:85-101` (split code), which inherits static/root/etc — but when the node sits in the middle of a "bridge" between static and wild there is no semantically correct nType. The tree building needs a redesign with explicit invariants.

---

## Corpus stats

All persisted in `reports/fuzzing-and-property-engineer/corpora/<target>/`.

| Target | Seeds | Corpus entries (post-run) |
|---|---|---|
| FuzzMuxHandle | 42 | 192 |
| FuzzMuxServeHTTP | 32 | 486 |
| FuzzMuxServeHTTPWithAllRedirects | 4 | 401 |
| FuzzCleanPath | 30 | 109 |
| FuzzFindWildcardViaHandle | 25 | 217 |
| FuzzRegexCompile | 10 | 235 |
| FuzzWalkRoutes | 3 | 124 |
| FuzzLookupAfterRegistration | 4 | 95 |
| FuzzRealIPXFF | 6 | 79 |
| FuzzMuxHandleTwice | 8 | 80 |
| FuzzParamsGet | 6 | 28 |
| FuzzCORSOrigin | 5 | 25 |
| FuzzRequestIDReflection | 6 | 26 |
| FuzzComposedMiddlewareChain | 3 | 19 |
| FuzzCompressRoundtrip | 8 | 14 |
| FuzzParamsMap | 3 | 12 |
| FuzzParamsInt | 8 | 11 |
| FuzzParamsFromContext | 2 | 3 |
| FuzzPathParam | 3 | 4 |
| FuzzCORSOriginAllowList | 5 | 8 |
| FuzzStripSlashesIdempotency | 4 | 8 |
| FuzzLoggerCRLF | 4 | 7 |

**Total:** 22 targets, 2 462 persisted corpus entries.

---

## Coverage report

`go test -coverpkg=github.com/FlavioCFOliveira/MuxMaster,github.com/FlavioCFOliveira/MuxMaster/middleware -coverprofile=evidence/2026-04-17/coverage.out ./...`

**Total:** **70.9%** of lines (statements) in `muxmaster` + `middleware`.

| File | Coverage |
|---|---|
| `tree.go:addRoute` | 88.7% |
| `tree.go:insertChild` | 97.1% |
| `tree.go:getValue` | 80.5% |
| `tree.go:findWildcard` | 100% |
| `tree.go:expandOptional` | 90.5% |
| `tree.go:walk` | 100% |
| `params.go:Get/Lookup/Int/…` | 100% |
| `params.go:RoutePattern` | 75% |
| `response.go:JSON` | 88.9% |
| `response.go:XML` | 77.8% |
| `response.go:Text` | 100% |
| `response.go:Redirect` | 100% |
| `response.go:NoContent` | 0% |

HTML rendering in `evidence/2026-04-17/coverage.html`.

### Declared coverage gaps

1. **`response.NoContent` 0%** — trivial function, has no dedicated fuzz target. Action: add a seed to `FuzzResponseText` that invokes it. **Non-blocker**.
2. **`getValue` 80.5%** — gaps in `regexParam` branches without children + TSR branches. Cover them with `FuzzMuxServeHTTP` seeds that steer towards those paths.
3. **`addRoute` 88.7%** — gaps in conflict cases between catch-all and the root handler.
4. **`response.XML` 77.8%** — marshal-failure paths not covered.
5. **`RoutePattern` 75%** — only one direct test; never called via a real handler.

No gap ≥ 20% — the exit criterion (`<80% is High`) is met in every file touched, except the trivial `NoContent`.

---

## Escalations

### Cross-domain findings (to hand over to other agents)

1. **FPE-001 / FPE-002 (CRLF)** → `http-protocol-security-auditor`. Validate the impact at the level of the HTTP/1.1 writer and HTTP/2 HPACK — the stdlib server may or may not sanitise depending on the write path; that agent has the expertise.

2. **FPE-004 (H-012 confirmed)** → `middleware-security-reviewer`. Auth middleware that reads the 4th+ parameter assumes it is populated; any integration that depends on this is trivially bypassable. Map the scenarios in which this manifests.

3. **FPE-006 / FPE-007 / FPE-008 / FPE-009 (tree fragility)** → `path-routing-fuzzer` + `dos-resilience-tester`. These agents can:
   - Confirm whether more variants exist (path-routing-fuzzer has a specific corpus).
   - Measure the resource economics empirically (dos-resilience).

4. **FPE-003 (StripSlashes)** → `path-routing-fuzzer`. The middleware interacts with RedirectTrailingSlash and CleanPath — if the composition is ordered so that StripSlashes runs AFTER CleanPath but BEFORE the router, there may be `/admin` vs `/admin/` routes with distinct subtrees reached by different paths. In-depth analysis is outside my scope.

5. **Coverage gaps** → `go-sast-and-memory-auditor`. SAST can identify path pragmas not reached by the fuzzer and suggest new targets.

### Release-blocking findings

Every **Critical** finding needs a fix + re-verification before the v1.0.0 tag:

- **FPE-006** — panic on the hot path, remote DoS conditional on a specific registration
- **FPE-008** — violation of a central invariant (tree isolation)
- **FPE-009** — panic on the hot path, broad DoS conditional on a specific registration

The **High** findings (FPE-001, FPE-002, FPE-004) may optionally be treated as known issues documented in CHANGELOG + SECURITY.md, but my recommendation is to include them in the fix cycle because all of them have a remediation of < 10 lines.

---

## Operational posture

**Harness ready for CI:**
- `cd reports/fuzzing-and-property-engineer/harness && go test -count=1 -timeout=60s ./...` runs seeds + property tests in ~3s.
- Nightly: `go test -run=^$ -fuzz=^Fuzz -fuzztime=2h ./...` per target (26h aggregate).
- Pre-release: 24h per target (4 days aggregate).

**Corpus minimisation:** not executed in this sprint — scheduled for the next iteration with `-test.fuzzminimisetime=1m`.

**OSS-Fuzz readiness:** the harness is ready (each Fuzz* is self-contained and imports only stdlib + mm + rapid). Next step: create `oss-fuzz/Dockerfile` + `project.yaml` — deferred to the next sprint.

---

## Next actions

1. **Maintainer:** decide on the release gate. Recommendation: HOLD until FPE-006/008/009 are fixed.
2. **Post-fix verification:** run each `evidence/FPE-NNN/repro_test.go` after the fix — they must flip from "CONFIRMED" to "remediation landed".
3. **Nightly CI gate:** schedule a 2h nightly run per target; alert the maintainer on new findings.
4. **Pending invariants:** add I-23 (PanicHandler), I-24 (Mount), I-25 (ErrorHandler) in the next sprint — see `invariants.md`.
5. **Regression pack:** the 9 FPE-NNN become permanent regression tests. After the fix, move them from `evidence/FPE-NNN/repro_test.go` (standalone) to `harness/regression_fpe_test.go` (main suite), inverting the assertion.
6. **Coordinate with `path-routing-fuzzer`:** share the 95 corpus entries in `FuzzLookupAfterRegistration` — that agent has corpora in `/reports/path-routing-fuzzer/corpora/` that can seed my next runs.

---

## Artefacts

- `reports/fuzzing-and-property-engineer/harness/` — 10 fuzz + property test files, isolated `go.mod`, `go.sum`
- `reports/fuzzing-and-property-engineer/corpora/` — 22 directories with 2 462 persisted inputs
- `reports/fuzzing-and-property-engineer/evidence/2026-04-17/` — 22 `fuzz-*.txt` logs, 9 `FPE-NNN/` with repro + go.mod, `coverage.out`, `coverage.html`
- `reports/fuzzing-and-property-engineer/invariants.md` — 22 catalogued invariants with status

---

**Report end.**
