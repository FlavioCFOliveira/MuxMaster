# MuxMaster — Findings Ledger

Owner: `threat-modeler-and-zero-day-researcher` (sole author of `reports/overview/`)
Last updated: 2026-09-25 — historical ledger restored and merged with the reconciliation ledger, rmp task #267 (sprint 20 "Backlog clearance")
Repository state at merge: commit `a510565`, branch `feature/20-backlog-clearance`

History of this file:
- 2026-04-17/18 — pre-release v1.0.0 sprint ledger (`MM-2026-0001..0047` + `MM-TM-2026-0001..0005`), audited commit `533d0c9`, fixes in `723b3be` (phases 1-3) and `3371932` (phases 4-6), phase 7 fixes afterwards. Now Part A.
- 2026-05-08 — deleted by commit `5f804fa` (see §0).
- 2026-09-25 — rmp #240 reconciliation ledger (34 entries), amended by rmp #263, #264, #265, #266 and #270. Now Part B.
- 2026-09-25 — rmp #267: Part A restored from `5f804fa^` and merged with Part B.
- 2026-09-26 — rmp #268: Part C (86 S7–S10 findings) added; H-RECON-03 confirmed by the closed-task audit.

## 0. Scope and provenance — read first

**The canonical ledger is restored.** The ledger that lived at this path (515 lines, last updated 2026-04-18) was deleted in commit `5f804fa` ("docs(reports): consolidate Sprint S9 security audit artifacts"), together with `threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md`, `reports/README.md` and the seven 2026-04-17 per-agent reports. The commit message describes these files as added; the diff removes them. rmp #267 restored all of them from `5f804fa^`. Part A of this file is that ledger; Part B is the rmp #240 reconciliation that was written while the ledger was missing.

Rules applied in the merge:
- **Nothing was dropped.** Every Part A entry (`MM-2026-0001..0047`, `MM-TM-2026-0001..0005`, the 5 refuted hypotheses, the statistics and the release gate) and every Part B entry, open item, collision and hypothesis is present.
- **History is not rewritten.** Part A statuses are the 2026-04-18 statuses. Where a later sprint changed the state and the change is verified in the repository at `a510565`, a **Current status (2026-09-25)** note follows the entry, citing the evidence. No note was added where the evidence was not verified.
- **Translation.** The deleted ledger was written in Portuguese. Part A is an English translation (CLAUDE.md §13.2). Identifiers, paths, figures, commit hashes and code are unchanged.
- **Evidence paths.** Part A cites reproducers under `reports/<agent>/evidence/...`. The deleted `reports/.gitignore` excluded `evidence/` and `*/evidence/`, so most of these files were never committed and cannot be recovered from git. Exceptions: the CSA-001..004 reproducers, `reports/http-protocol-security-auditor/harness/repro_test.go`, `reports/go-sast-and-memory-auditor/harness/h018_ctx_field_type_test.go` and the path-routing-fuzzer `shadow_*` harnesses were committed and then deleted in `5f804fa`; read them with `git show 5f804fa^:<path>`. See O-12.

Legacy section references. Other documents cite this file by section number from both eras. Part A keeps the numbering of the 2026-04-18 ledger with an `A.` prefix; Part B keeps the numbering of the rmp #240 ledger with a `B.` prefix:

| Citation found in | Cites | Now |
|---|---|---|
| `threat-model.md` | "findings.md §7" (Medium), "§10" (composites) | A.7, A.10 |
| `knowledge-model.md` | "findings.md §3" (not-distinct), "§5" (collisions) | B.3, B.5 |
| `pool_contamination_test.go`, `concurrency-security-auditor/2026-09-25-H-RECON-01-pooled-mode-canaries.md` | "findings.md §6" (H-RECON) | B.6 |
| rmp #264/#265/#266/#270 write-ups | "O-1".."O-10" | B.4 (unchanged IDs) |

---

# Part A — Pre-release v1.0.0 ledger (2026-04-17/18), restored

**Sprint:** Pre-release v1.0.0
**Audited commit:** `533d0c9` → **Fixes in:** `723b3be` (phases 1-3), `3371932` (phases 4-6)
**Go:** 1.26.2 linux/amd64
**Consolidated:** 2026-04-17 (sprint phase 3) / **Updated:** 2026-04-18 (phases 7-8)
**Post-fix state (2026-04-18):** 7 Critical Fixed, 14 High Fixed, 6 Medium Accepted/Fixed, 8 Low Fixed/Accepted
**Owner:** `threat-modeler-and-zero-day-researcher` (sole author)

## A.1 Canonical ID convention

Every finding receives a canonical ID `MM-YYYY-NNNN`. The `source_id` keeps the specialist's original ID.

| Source prefix | Issuing agent |
|---|---|
| HPS- | http-protocol-security-auditor |
| PRF- | path-routing-fuzzer |
| DOS- | dos-resilience-tester |
| CSA- | concurrency-security-auditor |
| MSR- | middleware-security-reviewer |
| SAST- | go-sast-and-memory-auditor |
| TSC- | timing-and-sidechannel-analyst |
| FPE- | fuzzing-and-property-engineer |
| TM- | threat-modeler (composites / transpositions) |

Note: the 2026-04-17 source IDs are short (`HPS-001`, `FPE-006`, …). They are a different namespace from the later `HPS-2026-NNNN`, `FPE-2026-NNN` IDs (see B.5).

## A.2 Severity

| Severity | Meaning |
|---|---|
| **Critical** | RCE, silent auth bypass, memory corruption, remotely triggerable panic on the hot path, race that breaks inter-request isolation |
| **High** | Sensitive information disclosure (credentials, secret routes before auth), sustainable DoS, conditional bypass, exploitable logic flaw |
| **Medium** | DoS with preconditions, low-impact information disclosure, logical bypass with preconditions |
| **Low** | Hardening gap, defence in depth, documentation improvement, dead code |
| **Info** | Observation with no active impact |

## A.3 Status

Open, Triaged, InProgress, Fixed, Verified, Accepted, Dismissed, Refuted.

## A.4 Raw aggregate vs. consolidated

| Agent | Raw Crit / High / Med / Low / Info |
|---|---|
| http-protocol | 0 / 3 / 3 / 1 / 1 |
| path-routing-fuzzer | 2 / 3 / 1 / 0 / 0 |
| dos-resilience | 0 / 3 / 4 / 0 / 2 |
| concurrency | 4 / 3 / 1 / 2 / 0 |
| middleware-review | 0 / 4 / 11 / 3 / 0 |
| sast | 0 / 0 / 2 / 5 / 7 |
| timing | 0 / 2 / 2 / 2 / 1 |
| fuzzing | 3 / 3 / 3 / 0 / 0 |
| **Raw total** | **9 / 21 / 27 / 13 / 11** |

After de-duplication by root cause: **47 canonical findings** + 5 TM composites = **52 entries**.

- Critical: **7**
- High: **14**
- Medium: **15**
- Low: **8**
- Info: **3**
- Composites: **5**
- Refuted: **5**

Main de-duplications:
- `H-012 paramsBuf overflow` (PRF-003 + DOS-003 + FPE-004) → **MM-2026-0010**
- `H-009 XFF trust` (HPS-004 + DOS-005 + MSR-RI-001 + SAST-009) → **MM-2026-0008**
- `H-006 compress unbounded` (DOS-001 + MSR-CP-001 + SAST-010) → **MM-2026-0007**
- `H-025 TSR pre-auth` (HPS-002 + PRF-004) → **MM-2026-0004**
- `HPS-003 Allow leak + PRF-004 FixedPath + TSC-003` → **MM-2026-0005**
- `H-002 basic_auth timing` (MSR-BA-001 + TSC-001) → **MM-2026-0009**
- `H-021 recoverer stderr` (DOS-008 + MSR-RE-002 + CSA-009) → **MM-2026-0023**
- `H-017 timeout leak` (DOS-002 + CSA-008 + MSR-TO-003) → **MM-2026-0019**
- `H-026 global throttle` (DOS-004 + MSR-TH-001) → **MM-2026-0013**
- `H-004 request_id CRLF` (HPS-005 + MSR-RQ-004 + FPE-001) → **MM-2026-0011**
- `H-005 CORS reflection` (MSR-CO-003 + FPE-002) → **MM-2026-0012**
- `H-003 logger CRLF` (HPS-001 + MSR-LG-001) → **MM-2026-0006**
- `PRF-001 + FPE-009` (wildcard shadow, invalid node) → **MM-2026-0001** (same root cause confirmed via stack trace)
- `PRF-006 + FPE-006` (UTF-8 / non-ASCII tree corruption) → **MM-2026-0002** (same structural class)

**CRITICAL — CSA-001 (unsafe.Add race) vs FPE-006 (getValue slice OOB):** distinct root causes verified:
- CSA-001 is **concurrency** (unsafe.Add + pool; triggers when a handler spawns a goroutine)
- FPE-006 is a **bounds check** (`len(n.indices) > cap(n.children)` after a specific split)
- **Kept separate:** MM-2026-0003 and MM-2026-0002 respectively.

---

## A.5 Canonical ledger — Critical (7)

### MM-2026-0001 — Tree corruption via wildcard shadow + invalid-node-type panic in getValue

| Field | Value |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-20, CWE-755, CWE-770 |
| **Component** | `tree.go:63-156` (addRoute static branch), `tree.go:292-425` (getValue), `tree.go:321-396` (switch nType default:panic) |
| **Source IDs** | PRF-001, FPE-009 |
| **Reproducer** | `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-001-wildcard-shadow-crash/repro_test.go`; `/reports/fuzzing-and-property-engineer/evidence/FPE-009/repro_test.go` |
| **Evidence** | Panic `muxmaster: invalid node type` at dispatch after an ambiguous registration (`/a/:x` + `/a/b` → dispatching `/a/c` panics). The matrix in `TestShadowMatrix_StaticAfterParam` confirms 3 variants that panic. FPE-009 shows a second trigger: register `/:0` + `/0`. |
| **Root cause** | `addRoute` accepts a static registration that is a sibling of a wildchild without checking the invariant "wildchild is always last". The split produces `wildChild=true` but `nType=static` (zero value). `switch n.nType` does not cover this combination. |
| **Competitor comparison** | httprouter panics in `addRoute` (detects the conflict at registration time — correct). chi and bunrouter dispatch correctly (static wins). MuxMaster is the outlier. |
| **Recommended fix** | In `tree.go:122-127`, reject a static child when `n.wildChild=true`: `panic("muxmaster: static segment conflicts with existing wildcard sibling")`. Alternative: reorder children to keep the invariant. |
| **Escalation** | concurrency-security-auditor (the published tree is shared); dos-resilience (DoS via configuration). |
| **Status** | Fixed — commit `723b3be` (phases 1-3) |

> **Current status (2026-09-25):** static siblings of a param wildchild are now supported in both registration orders and dispatch without panic; catch-all and duplicate-wildcard conflicts panic in both orders. Regression tests: `tree_static_sibling_wildchild_test.go` (`TestStaticSiblingOfParamWildchild_BothOrders`, `TestCatchAllSiblingConflict_PanicsInBothOrders`, `TestDuplicateWildcardConflict_PanicsInBothOrders`), `tree_order_independence_test.go`, `security_test.go::TestWildcardStaticConflictPanics`. The `TestShadowMatrix_*` harness cited above was deleted in `5f804fa`.

### MM-2026-0002 — UTF-8 invariant violation: a non-ASCII pattern corrupts `tree.indices` vs `tree.children`

| Field | Value |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-20, CWE-129 |
| **Component** | `tree.go:118-127` (addRoute), `tree.go:158-176` (incrementChildPrio), `tree.go:305` (getValue slice bound) |
| **Source IDs** | PRF-006, FPE-006 |
| **Reproducer** | `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-006-addroute-0xff-oob/repro_test.go`; `/reports/fuzzing-and-property-engineer/evidence/FPE-006/repro_test.go` |
| **Evidence** | `r.GET("/\xff", h)` is accepted without panic; a subsequent `r.GET("/__sanity__", h)` panics with `index out of range [2] with length 2` in `incrementChildPrio`. Any subsequent dispatch panics with `slice bounds out of range [:3] with capacity 2` on the hot path. The FPE-006 variant `/\xf9` + `/` produces the same symptom. |
| **Root cause** | `addRoute` does not validate UTF-8. `n.indices += string(c)` with `c=0xFF` creates a string with an invalid byte, breaking the invariant `len(n.indices) == number of static children`. |
| **Realistic impact** | **Boot-time DoS:** configuration-file injection of a corrupt pattern. **Hot-path DoS:** every request panics after registration. |
| **Recommended fix** | Reject patterns with bytes ≥ 0x80 that are not valid UTF-8, OR audit `for i, r := range path` indexing vs byte indexing. Debug invariant check: `assert(len(n.indices) == len(staticChildren))`. |
| **Escalation** | sast (bounds-check failure — staticcheck/gosec should have seen it); dos. |
| **Status** | Fixed — commit `723b3be` (phases 1-3) |

> **Current status (2026-09-25):** regression test `security_test.go::TestAddRouteRejectsInvalidUTF8`; multi-byte index sync covered by `path-routing-fuzzer/harness/s8_audit_test.go::TestS8_H851_IncrementChildPrio_MultiByte` and `TestS8_H858_IncrementChildPrio_IndexSync`.

### MM-2026-0003 — Cross-goroutine race on `r.ctx` via unsafe.Add

| Field | Value |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-362 (Race), CWE-367 (TOCTOU) |
| **Component** | `mux.go:464-480` (param path), `mux.go:521-537` (wildcard path), `params.go:155` (setReqCtx dead code) |
| **Source IDs** | CSA-001, SAST-001 (secondary — pattern audit) |
| **Reproducer** | `/reports/concurrency-security-auditor/evidence/2026-04-17/CSA-001/repro_test.go` |
| **Evidence** | 3 DATA RACE warnings captured with full stacks in `h001_run1_full.txt`: write at `mux.go:476` (`*origCtxPtr = origCtx`) vs read at `request.go:353` + `params.go:160,164`. The observed output contains values crossed between requests. |
| **Root cause** | The pattern `*origCtxPtr = rc; handler.ServeHTTP(...); *origCtxPtr = origCtx` assumes the dispatcher goroutine owns `r` exclusively. Handlers that spawn goroutines retaining `r` (a **completely legitimate** pattern in Gin/Echo/chi) produce an immediate race. |
| **Competitor comparison** | httprouter, chi, bunrouter, gin, echo — **all use the immutable `r.WithContext(ctx)`**, which returns a new `*Request`. MuxMaster is the only one that mutates `r.ctx` in place. Migrating from chi/gin to MuxMaster silently breaks apps with `go func() { r.Context() }()`. |
| **Recommended fix** | (A) **preferred:** `handler.ServeHTTP(w, r.WithContext(rc))` — 1 alloc per param request but race-free. (B) keep `unsafe.Add` + a linter rule that detects spawns retaining `r`. Recommendation: (A). |
| **Escalation** | threat-modeler (composite MM-TM-2026-0004), go-perf-optimizer (benchstat of the fix). |
| **Status** | Fixed — commit `723b3be` (phases 1-3) |

> **Current status (2026-09-25):** the fix mechanism was later superseded. Dispatch now copies `*http.Request` into a freshly allocated `reqBundle` and writes the copy's `ctx` with `setReqCtxUnsafe`; the original `r` is never modified, and `r.WithContext` is the fallback when the `ctx` field is not found (`params.go:200-215`). Regression tests: `reqbundle_test.go::TestReqBundleGoroutineSpawn`, `TestReqBundleOriginalRequestUnmodified`; `concurrency-security-auditor/harness/h001_r_ctx_goroutine_race_test.go::TestSetReqCtxUnsafe_OnlyFreshBundle`, `TestSetReqCtxUnsafe_MassiveParallel`; `s9_hypotheses_test.go::TestH9_01_SetReqCtxUnsafe_HappensBefore_SpawnedGoroutine`. The opt-in `Mux.PoolRequestBundle` mode deliberately reintroduces a retention hazard (handlers must not retain `r`); contamination was refuted under `-race` by rmp #263 (H-RECON-01, B.6) and the hazard is pinned by `pool_contamination_test.go::TestPoolRequestBundle_RetentionHazard_Documented`.

### MM-2026-0004 — RedirectTrailingSlash emits 301 before application middleware

| Field | Value |
|---|---|
| **Severity** | **Critical** (promoted from High because of composite MM-TM-2026-0001) |
| **CWE** | CWE-200 (Information Exposure) |
| **Component** | `mux.go:491-499` (TSR block in dispatch), `mux.go:223` (wrapMiddleware anchors to the handler) |
| **Source IDs** | HPS-002, PRF-004 (partial), H-025 |
| **Reproducer** | `/reports/http-protocol-security-auditor/harness/repro_test.go:TestReproHPS003_TSRRouteDisclosureBeforeAuth`; `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-004-tsr-pre-auth/repro_test.go` |
| **Evidence** | `GET /admin` with `/admin/` registered + a `denyAll` middleware → `301 Location: /admin/`, `auth_middleware_invocations=0`. chi in the same setup: `403 auth_calls=1`. |
| **Root cause** | `wrapMiddleware(handler, m.middleware)` is called in `Handle()` — only the final handler is wrapped. When getValue returns `tsr=true` with handler=nil, dispatch emits the redirect directly. |
| **Competitor comparison** | chi applies middleware before any dispatch decision — TSR passes through auth. |
| **Recommended fix** | Three options: (1) move TSR/FixedPath inside the middleware chain (`notFoundHandler = wrapMiddleware(m.dispatchFallback, m.middleware)`); (2) opt-in `Mux.TSRAfterMiddleware bool` defaulting to `true`; (3) docs only (minimum acceptable). |
| **Escalation** | threat-modeler (composite), middleware-reviewer (unified fix with MM-2026-0005). |
| **Status** | Fixed — commit `723b3be` (phases 1-3) |

> **Current status (2026-09-25):** regression tests `security_test.go::TestRedirectTrailingSlashRunsMiddleware` and `mux_redirect_middleware_test.go` (5 tests, including middleware registered after the route and a concurrent `Use`-vs-redirect race test).

### MM-2026-0005 — RedirectFixedPath + auto-OPTIONS + auto-405 disclose routes before auth (Allow header leak)

| Field | Value |
|---|---|
| **Severity** | **Critical** (promoted from High because it combines with MM-2026-0004 and MM-2026-0009 in the recon/auth-bypass composite) |
| **CWE** | CWE-200, CWE-204 |
| **Component** | `mux.go:501-506` (RedirectFixedPath), `mux.go:546-565` (HandleOPTIONS/HandleMethodNotAllowed) |
| **Source IDs** | HPS-003, PRF-004 (FixedPath variant), TSC-003 |
| **Reproducer** | `/reports/http-protocol-security-auditor/harness/repro_test.go:TestReproHPS004_OPTIONSAllowLeakBeforeAuth`; `/reports/timing-and-sidechannel-analyst/evidence/TSC-003/repro_test.go` |
| **Evidence** | `OPTIONS /secret` (with a deny auth middleware) → `204 Allow: GET,POST,OPTIONS body="" auth_invocations=0`. `GET /admin//console` → `301 Location: /admin/console` without passing through auth. |
| **Root cause** | Same structural cause as MM-2026-0004. In addition, `path.Clean` normalises without auth — an attacker learns the canonical form of protected routes. |
| **Recommended fix** | (1) apply `m.middleware` to the OPTIONS/405 paths; (2) breaking: flip the `RedirectFixedPath` default `true → false`. Recommended: (1) + (2) combined. |
| **Escalation** | threat-modeler (MM-TM-2026-0001). HPS formally proposed H-031 — **promoted to a sprint hypothesis**. |
| **Status** | Fixed — commit `723b3be` (phases 1-3) |

> **Current status (2026-09-25):** regression tests `security_test.go::TestOptions405RunsMiddleware`, `TestNotFoundRunsMiddleware`, `TestRedirectFixedPathDefaultOff`; `RedirectFixedPath` defaults to `false` (CLAUDE.md public API, `security_test.go`).

### MM-2026-0006 — Logger writes raw `r.URL.Path` — CRLF / ANSI / NUL log injection

| Field | Value |
|---|---|
| **Severity** | **Critical** (promoted from High because it is the audit-trail forgery primitive of composite MM-TM-2026-0002) |
| **CWE** | CWE-117, CWE-150, CWE-93 |
| **Component** | `middleware/logger.go:30` — `fmt.Fprintf(out, "... %s ...", ..., r.URL.Path, ...)` |
| **Source IDs** | HPS-001, MSR-LG-001, H-003 |
| **Reproducer** | `/reports/http-protocol-security-auditor/evidence/h003-logger-injection.txt`; `/reports/middleware-security-reviewer/evidence/logger-injection-corpus.txt` (15 payload classes: CRLF, LF, ANSI clear, ANSI colour, NUL, BEL, VT, BOM, Unicode line separators) |
| **Evidence** | `GET /abc%0D%0A2099-01-01T00:00:00Z+GET+/fake+200+0s` → 2 log lines (forgery). `/abc%1B%5B2J%1B%5BH` → ANSI clear in a TTY viewer. The stdlib rejects a literal CRLF in the request-target (400) but **percent-decodes it into `r.URL.Path`**. |
| **Root cause** | `r.URL.Path` is percent-decoded by `url.Parse`; `fmt.Fprintf(%s)` writes the raw bytes. |
| **Recommended fix** | `sanitiseForLog(r.URL.Path)` with `strconv.QuoteToASCII` or a control-byte filter (CR/LF/NUL/DEL/ESC). Alternatively, structured JSON that escapes naturally. |
| **Escalation** | threat-modeler (composite). |
| **Status** | Fixed — commit `3371932` (phases 4-6) |

> **Current status (2026-09-25):** regression tests `middleware/middleware_test.go::TestLogger_SanitisesCRLFInPath`, `TestLogger_SanitisesANSIInPath`; property tests `fuzzing-and-property-engineer/harness/fuzz_sanitiser_test.go` (`TestProp_SanitiserExhaustiveByte`, `TestProp_SanitiserLSEPandPSEP`, `FuzzLoggerNoPanic`). The request method is sanitised as well since S9 (`sprint_s9_test.go::TestSec_Logger_Method_Sanitised_Regression`).

### MM-2026-0007 — compress middleware accumulates the whole response before flushing (unbounded buffer)

| Field | Value |
|---|---|
| **Severity** | **Critical** (promoted from High because: (1) the empirical magnitude is a real OOM, (2) the attacker needs no special privilege, (3) `Accept-Encoding: gzip` is sent by every modern browser/client) |
| **CWE** | CWE-400 |
| **Component** | `middleware/compress.go:25-31` — `g.buf = append(g.buf, b...)` |
| **Source IDs** | DOS-001, MSR-CP-001, SAST-010, H-006 |
| **Reproducer** | `/reports/dos-resilience-tester/evidence/DOS-001/compress-oom.txt`; `/reports/middleware-security-reviewer/evidence/compress-rss-trace.txt` |
| **Evidence** | Empirical slope 1.15 heap bytes per body byte; 64 MB body → 177.56 MB peak heap; 1 GB body (extrapolated) → ~1.5-2 GB RSS. Confirmed 2.75× due to `append` doubling. |
| **Root cause** | `g.done` is `true` only after the handler returns. Write(b) appends until then. Compression runs once in `grw.flush()`. |
| **Recommended fix** | Streaming compression: `g.gz.Write(b)` directly after MIME / threshold detection. A bounded buffer (e.g. 8 KiB) only for content-type sniffing. |
| **Escalation** | middleware-reviewer (BREACH surface analysis); threat-modeler (composite MM-TM-2026-0002). |
| **Status** | Fixed — commit `3371932` (phases 4-6) |

> **Current status (2026-09-25):** regression tests `middleware/middleware_test.go::TestCompress_StreamingBoundedMemory`, `dos-resilience-tester/harness/compress_oom_test.go::TestCompressStreamingBoundedMemory`, `sniff_buffer_test.go` (5 tests).

---

## A.6 Canonical ledger — High (14)

### MM-2026-0008 — real_ip trusts XFF / X-Real-IP without a proxy list

| Severity | CWE | Source | Reproducer |
|---|---|---|---|
| High | CWE-345, CWE-290 | HPS-004, DOS-005, MSR-RI-001, SAST-009, H-009 | 4 converging reproducers |

**Component:** `middleware/real_ip.go:12-22`. **Evidence:** 10 requests from one IP with a rotating XFF → 10 unique counters; downstream per-IP ACLs are trivially bypassed. **Root cause:** no validation of the originating peer. **Fix:** `RealIP(trustedCIDRs ...*netip.Prefix)` — skip the mutation if the direct peer is not in trustedCIDRs. **Status:** Fixed — commit `3371932` (phases 4-6).

> **Current status (2026-09-25):** `RealIP()` with no CIDRs still trusts every peer and emits a construction-time `slog.Warn` (`s10_premsr_test.go::TestSec_RealIP_NoCIDR_SlogWarnEmitted`); with CIDRs, XFF is taken rightmost-untrusted (`middleware/realip_wastehunt_test.go`, `security_harness_test.go::TestSec_RealIP_WithCIDR_SpoofPrevented`). The ordering composite is CDX-2026-003 (B.2).

### MM-2026-0009 — basic_auth user enumeration via timing

| Severity | CWE | Source | Reproducer |
|---|---|---|---|
| High | CWE-208, CWE-203 | MSR-BA-001, TSC-001, H-002 | `/reports/timing-and-sidechannel-analyst/evidence/TSC-001/repro_test.go` |

**Component:** `middleware/basic_auth.go:17-22`. **Evidence:** N=1.5M, 3 runs: Welch p=0, KS p=0, MWU p=0; mean difference 319-429 ns; Cohen's d 0.33-0.45; bimodal distribution for "user absent". Assembly confirmed: `JEQ 0x00d9` skips `subtle.ConstantTimeCompare` on a map miss. **Fix:** constant path — always run `subtle.ConstantTimeCompare` against a dummy hash when `found=false`. **Status:** Fixed — commit `3371932` (phases 4-6).

> **Current status (2026-09-25):** a residual map-lookup oracle is accepted as TSC-2026-0002 (61 ns; asserted bound ≤ 700 ns since rmp #270) in `SECURITY.md` "Accepted Timing Oracles (TSC-2026-0001..0007)"; harness `timing-and-sidechannel-analyst/harness/basic_auth_timing_test.go::TestTiming_BasicAuth_UserExistsVsNotExists`.

### MM-2026-0010 — paramsBuf silently overflows at the 4th param (maxInlineParams=3)

| Severity | CWE | Source |
|---|---|---|
| High | CWE-754, CWE-703, CWE-284 composite | PRF-003, DOS-003, FPE-004, H-012 |

**Component:** `tree.go:13-26`. **Evidence:** a route with 5 params captures only 3; p4, p5 = `""`. Composed with an auth middleware that does `allowedTenants[PathParam(r, "tenant")]` — if the map contains `""`, a silent bypass. httprouter/bunrouter support 8-16 params. **Fix:** preferred: panic in `addRoute` if the pattern has > 3 wildcards (breaking, explicit); alternative: fallback slice. **Status:** Fixed — commit `723b3be` (phases 1-3).

> **Current status (2026-09-25):** routes with more than 3 params are supported through the `reqBundle` heap overflow (CLAUDE.md "Param accumulation"). Regression tests: `security_test.go::TestFourParamRoute`, `TestEightParamRoute`; `mux_test.go::TestParamRoutesOverflowInline`; `dos-resilience-tester/harness/paramsbuf_test.go::TestParamsBufOverflowCorrectness`; `path-routing-fuzzer/harness/s9_audit_test.go::TestS9_H12_ParamBufTierStress`.

### MM-2026-0011 — request_id accepts and reflects X-Request-ID without validation

| Severity | CWE | Source |
|---|---|---|
| High | CWE-113 (in memory), CWE-400, CWE-20 | HPS-005, MSR-RQ-004, FPE-001, H-004 |

**Component:** `middleware/request_id.go:16-23`. **Evidence:** a 1 MiB X-Request-ID → a 1 MiB response header. CRLF retained in memory (the wire is sanitised by Go 1.26). **Fix:** `validRequestID`: `[A-Za-z0-9_-.]{1,128}`. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0012 — CORS wildcard reflects the attacker's Origin instead of emitting `*`

| Severity | CWE | Source |
|---|---|---|
| High | CWE-942, CWE-113 (in memory) | MSR-CO-003, MSR-CO-007, FPE-002, H-005 |

**Component:** `middleware/cors.go:54`. **Evidence:** `AllowedOrigins=["*"]` + `Origin: https://evil.example` → `ACAO: https://evil.example` (spec violation). CRLF in Origin retained in memory. **Fix:** `if allowAll { h.Set("Access-Control-Allow-Origin", "*") }`. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0013 — Global throttle disguised as per-IP

| Severity | CWE | Source |
|---|---|---|
| High | CWE-770, CWE-400 | DOS-004, MSR-TH-001, H-026 |

**Component:** `middleware/throttle.go:17`. **Evidence:** 1 attacker with `limit` concurrent requests denies 100% of distinct legitimate clients. **Fix:** rename to `ThrottleAllBacklog` (breaking) + add `ThrottlePerIP(limit, keyFn, timeout)` with a bucket cap. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0014 — Use()/Pre()/preHandler mutated without a lock

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362, CWE-667 | CSA-002, CSA-007 |

**Component:** `mux.go:175-184`. **Evidence:** 2 DATA RACE warnings (`mux.go:176` vs `mux.go:223`). **Fix:** `m.mu.Lock()` in `Use/Pre`; `atomic.Pointer[http.Handler]` for `preHandler`. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0015 — A handler panic leaves `r.ctx` pointing at a leaked rc + pool leak

| Severity | CWE | Source |
|---|---|---|
| High | CWE-404, CWE-772, CWE-672 | CSA-004, CSA-005 |

**Component:** `mux.go:466-480`, `mux.go:523-537`. **Evidence:** 50k panics → `allocDelta=271 990 424 B` (5440 B/req). After the panic, `req.Context()` still points at rc. **Fix:** `defer`-wrapped cleanup. Cost ~20 ns/req, acceptable. **Status:** Fixed — commit `3371932` (phases 4-6) (covered by r.WithContext).

### MM-2026-0016 — Introspection (Walk/Routes/Lookup) race vs addRoute

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362, CWE-820 | CSA-003, H-027 |

**Component:** `introspection.go:60-95` vs `tree.go:63-155`. **Evidence:** 63 DATA RACE warnings in 2 s of stress. **Fix:** `m.mu.RLock()` in Walk/Routes/Lookup (option C — zero hot-path impact). **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0017 — Public fields read unsafely on the hot path

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362 | CSA-006 |

**Component:** `mux.go:109-154` (PanicHandler, NotFound, MethodNotAllowed, GlobalOPTIONS, ErrorHandler, 8 bool/int flags). **Evidence:** 3 tests FAIL with DATA RACE. **Fix:** atomic setters via `atomic.Pointer` OR docs "set before ListenAndServe". **Status:** Accepted — documented in SECURITY.md.

### MM-2026-0018 — clean_path single-pass bypass via encoded traversal

| Severity | CWE | Source |
|---|---|---|
| High | CWE-22 | PRF-002, HPS-006, MSR-CL-001, H-010 |

**Component:** `middleware/clean_path.go:9-21`. **Evidence:** `/static/..%2fadmin` → decode → `/static/../admin` → `path.Clean` → `/admin` → bypass. 136 bypass combinations in the matrix. **Fix:** `SafeCleanPath` (reject `..` after decode) OR operate on RawPath OR explicit docs. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0019 — Timeout middleware does not preempt the handler

| Severity | CWE | Source |
|---|---|---|
| High (Critical in composite) | CWE-400 | DOS-002, CSA-008, MSR-TO-003, H-017 |

**Component:** `middleware/timeout.go:14-20`. **Evidence:** 1000 req / 10 ms timeout / 10 s handler = 1000 goroutines for 10 s. **Fix:** normative docs + example; optional `TimeoutWithAbort`. **Status:** Accepted — documented in SECURITY.md.

> **Current status (2026-09-25):** still Accepted. Context cancellation timing is now pinned by the reconstructed property `TestProp_TimeoutCancelsContext` (invariant I-15b, rmp #265, FPE-2026-005 in B.2).

### MM-2026-0020 — Password-length oracle via subtle.ConstantTimeCompare early exit

| Severity | CWE | Source |
|---|---|---|
| High (promoted from Medium — exploitable after the MM-2026-0009 enumeration) | CWE-208, CWE-203 | TSC-004 |

**Component:** `basic_auth.go:18` → `crypto/subtle/constant_time.go:18-22`. **Evidence:** N=1.5M, p=0, mean difference 284-316 ns. Maximum latency when len(pass)==len(expected). **Fix:** SHA-256 both inputs before the compare (bundled with MM-2026-0009). **Status:** Fixed — commit `3371932` (phases 4-6).

> **Current status (2026-09-25):** the fix is present (`middleware/basic_auth.go` stores `sha256.Sum256` of each password and compares against a dummy hash on a miss). The statistical regression test `TestTiming_H002b_PasswordLengthOracle` was removed from `basic_auth_timing_test.go` in `5f804fa` and has no current equivalent (O-14).

### MM-2026-0021 — Registration-time index OOB on pattern `/{…}*name`

| Severity | CWE | Source |
|---|---|---|
| High | CWE-20, CWE-129, CWE-755 | FPE-005 |

**Component:** `tree.go:259-262`. **Evidence:** `r.Handle("/{:}*00000", h)` → `path[-1]` → `runtime error: index out of range`. **Fix:** `if i < 0 || path[i] != '/' { panic(...) }`. **Status:** Fixed — commit `723b3be` (phases 1-3).

---

## A.7 Canonical ledger — Medium (15)

### MM-2026-0022 — Mount keeps RawPath with an untrimmed prefix

High:Medium / CWE-707 / PRF-005. `mux.go:362-370`. TrimPrefix fails on a percent-encoded prefix. The inner handler sees divergent Path/RawPath. Fix: zero RawPath if TrimPrefix does not match. **Status:** Fixed — commit `3371932` (phases 4-6).

### MM-2026-0023 — Recoverer dumps the raw panic value + stack to stderr

Medium / CWE-209+532+150 / DOS-008 + MSR-RE-002 + MSR-RE-003 + CSA-009 + H-021. `recoverer.go:16`. Attacker-controlled panic values + ANSI escapes → stderr. Fix: `RecovererWithLogger(slog.Logger)`; deprecate the current one.

> **Current status (2026-09-25):** `RecovererWithLogger(*slog.Logger)` exists and `Recoverer()` is marked `Deprecated` (`middleware/recoverer.go:12,21`). Tests: `middleware/middleware_test.go::TestRecovererWithLogger_DoesNotLeakPanicToBody`, `fuzz_sanitiser_test.go::FuzzRecovererSanitiser`, `fuzz_s9_new_surfaces_test.go::FuzzRecovererWithLogger`. Fix commit not traced.

### MM-2026-0024 — Slowloris exposure in the default http.Server (docs gap)

Medium / CWE-400 / DOS-006. Docs gap: the README uses `ListenAndServe` without timeouts. 200 drip clients → +400 goroutines. Fix: SECURITY.md with a `ReadHeaderTimeout: 30s` example.

> **Current status (2026-09-25):** documented in `SECURITY.md` "Slowloris / Server Timeouts (MM-2026-0024)". Fix commit not traced.

### MM-2026-0025 — StripSlashes is not idempotent

Medium / CWE-707 / FPE-003 + MSR-SS-001. `/a//` → `/a/` (not `/a`). Fix: loop `for len(p)>1 && p[len(p)-1]=='/'`.

> **Current status (2026-09-25):** fixed — `middleware/strip_slashes.go` strips in a loop for both Path and RawPath; regression test `middleware/middleware_test.go::TestStripSlashes_MultipleTrailing` (`/a///` → `/a`). Fix commit not traced.

### MM-2026-0026 — Route-existence timing oracle (radix intrinsic)

Medium / CWE-208 / TSC-002, H-011. ~440 ns gap. **Accepted risk** — intrinsic to every radix router (chi, httprouter, bunrouter alike). Documented in SECURITY.md.

> **Current status (2026-09-25):** still Accepted. The magnitude was re-measured at ~960 ns (4 independent runs) for the correctly described pair registered/200 vs unregistered/404; `SECURITY.md` "Route-Existence Timing Oracle (MM-2026-0026)" and TSC-2026-0005 now cite the same figure (rmp #270, O-10). Its composition with other oracles is CDX-2026-005 (B.2).

### MM-2026-0027 — BasicAuth unbounded brute force (no rate limit)

Medium / CWE-307 / MSR-BA-005. Fix: docs recommending composition with ThrottlePerIP (depends on MM-2026-0013).

> **Current status (2026-09-25):** documented in `SECURITY.md` "BasicAuth Brute-Force (MM-2026-0027)".

### MM-2026-0028 — CORS CRLF in Origin retained in memory

Medium / CWE-113 / MSR-CO-007. Bundled with MM-2026-0012. Validate Origin before `Set`.

> **Current status (2026-09-25):** fixed — regression tests `middleware/middleware_test.go::TestCORS_CRLFInOriginRejected`, `security_harness_test.go::TestSec_CORS_CRLFOriginRejected`, `http_protocol_audit_test.go::TestHPS0005_CORS_CRLF_Blocked`.

### MM-2026-0029 — real_ip CRLF in XFF retained in r.RemoteAddr

Medium / CWE-117 / MSR-RI-002. Bundled with MM-2026-0008. `net.ParseIP` after trim.

> **Current status (2026-09-25):** fixed — regression tests `middleware/middleware_test.go::TestRealIP_RejectsCRLFInXFF`, `security_harness_test.go::TestSec_RealIP_CRLFInXFFRejected`.

### MM-2026-0030 — compress without a brotli fallback + BREACH surface

Medium / CWE-693+203 / MSR-CP-005, MSR-CP-007. Fix: docs + opt-in BREACH mitigation. **Status:** Accepted — out of scope for v1.0.0.

> **Current status (2026-09-25):** documented in `SECURITY.md` "BREACH Compression Oracle (MM-2026-0030 / DOS-2026-0006)".

### MM-2026-0031 — paramsBuf DoS variant (runtime amplification)

Medium / CWE-754 / DOS-003 (analytical). Bundled fix with MM-2026-0010. **Status:** Accepted — fixed indirectly via MM-2026-0010 (phases 1-3).

### MM-2026-0032 — PathologicalLoop in addRoute with specific invalid UTF-8

Medium / CWE-400 / FPE-007. The pair `/\xbe` + `/\xc2\xa8\x91\x9d\xd8'\xef` → > 10 s. Bundled with MM-2026-0002. **Status:** Accepted — fixed indirectly via MM-2026-0002 (phases 1-3).

### MM-2026-0033 — Superficial tree corruption after a registration panic

Medium (Critical in intent, currently UB) / CWE-362 / FPE-008. `Handle()` copy-on-write is superficial. Fix: two-phase registration. **Status:** Accepted — out of scope for v1.0.0.

> **Current status (2026-09-25):** addressed later by two-phase registration with rollback; documented in `SECURITY.md` "Registration Panics Rollback Guarantee (MM-2026-0033)". Regression tests: `mux_test.go::TestTwoPhaseRegistrationPanic_LiveTreeIntact`, `tree_rollback_test.go::TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched`, `concurrency-security-auditor/harness/s8_hypotheses_test.go::TestH8_72_TwoPhase_Rollback_OnConflict`. Referenced as a component of CDX-2026-002 (B.2). Fix commit not traced.

### MM-2026-0034 — Ordering invariant: Recoverer outermost (docs)

Medium / CWE-703 / MSR-OR-002. A panic outside the Recoverer escapes. Fix: docs + consider a native panicShield.

> **Current status (2026-09-25):** documented in `SECURITY.md` "Recoverer Must Be Outermost (MM-2026-0034)". The negative test (a Recoverer placed inside does not catch an outer panic) was deleted in `5f804fa` and has no current equivalent (O-14).

### MM-2026-0035 — reqCtxOffset stale in future Go versions (latent)

Medium / CWE-453 / CSA-010 + SAST-001 + H-018. `init()` finds the offset by name without validating the type. Test gate added by the sast agent (`harness/h018_ctx_field_type_test.go`). H-018 status: **PARTIAL**.

> **Current status (2026-09-25):** `init()` now requires both `f.Name == "ctx"` and `f.Type == ctxType` before enabling the unsafe path, and falls back to `r.WithContext` otherwise (`params.go:200-215`). The sast test gate `h018_ctx_field_type_test.go` and `TestH018_ReqCtxOffsetAgreement` were deleted in `5f804fa`; current tests: `concurrency-security-auditor/harness/h018_reqctx_offset_test.go` (`TestReqCtxField_OffsetIsCorrect`, `TestReqCtxField_NoWriteToOriginal`, `TestReqCtxField_ParamsAccessible_AllTiers`) and `layout_test.go::TestBundleFieldOffsets`.

### MM-2026-0036 — NotFound/MethodNotAllowed allocation amplification

Medium (informational) / CWE-400 / DOS-007 + DOS-009. 3 allocs / 8 allocs vs 0. Same shape as the stdlib.

---

## A.8 Canonical ledger — Low (8)

| ID | Title | Source | CWE | Status |
|---|---|---|---|---|
| MM-2026-0037 | SetHeader retains CR/LF in memory (wire sanitised) | HPS-007, MSR-SH-001 | CWE-113 | Fixed — phase 7 |
| MM-2026-0038 | BasicAuth realm injection (wire sanitised) | MSR-BA-004, H-029 | CWE-117 | Accepted — wire sanitised by the Go stdlib |
| MM-2026-0039 | WithValue accepts an `any` key (string collision) | MSR-WV-003, SAST-013, H-022 | CWE-668 | Fixed — phase 7 (doc comment) |
| MM-2026-0040 | Dead code: `setReqCtx` (params.go:154) | SAST-003 | CWE-561 | Fixed — phase 7 |
| MM-2026-0041 | Dead constant: `maxParams = 16` unused | SAST-004 | CWE-561 | Fixed — phase 7 |
| MM-2026-0042 | Unchecked `testGz.Close()`, `fmt.Fprintf` in logger | SAST-002, SAST-005 | CWE-703 | Fixed — phase 7 (nolint:errcheck) |
| MM-2026-0043 | Type assertions without `, ok` on sync.Pool | SAST-008 | CWE-704 | Fixed — phase 7 (nolint:forcetypeassert, pool.New always set) |
| MM-2026-0044 | Dead `sink` variable in bench_test.go | SAST-006 | CWE-561 | Fixed — phase 7 |

> **Current status (2026-09-25):** MM-2026-0037 — `SetHeader` panics at construction on CR or LF in key or value (`middleware/set_header.go`; tests `middleware/middleware_test.go::TestSetHeaderRejectsCRLF`, `security_harness_test.go::TestSec_SetHeader_CRLFInKeyPanics`, `TestSec_SetHeader_CRLFInValuePanics`). MM-2026-0039 — `WithValue` warns on a string key (`middleware_test.go::TestWithValue_WarnsOnStringKey`) and panics on a nil key (`TestWithValue_PanicsOnNilKey`).

---

## A.9 Info (3)

| ID | Title | Source |
|---|---|---|
| MM-2026-0045 | HTTP/1.1 smuggling defended by the stdlib (PASS) | HPS-008 |
| MM-2026-0046 | Error-oracle matrix: 15/15 pairs distinguishable (accepted) | TSC-005 |
| MM-2026-0047 | BREACH at handler level (compress does not mitigate — docs only) | TSC-006 |

> **Current status (2026-09-25):** MM-2026-0046 — re-measured with valid arms by rmp #264 (TSC-2026-0009 in B.2: 404/405 ≈ 270 ns, 404/401 ≈ 595 ns); the 15-pair `TestErrorOracleMatrix` was removed in `5f804fa`. MM-2026-0045 — the raw-TCP smuggling matrix that produced it (20 variants, `smuggle_test.go`) was deleted in `5f804fa`; `http_protocol_audit_test.go::TestHPS0010_RequestSmuggling_NetHTTPDefence` covers 4 of those variants and other tests cover 3 more (O-14).

---

## A.10 Composites (TM-) — multi-agent attack chains

### MM-TM-2026-0001 — Pre-auth reconnaissance + credential-theft pipeline (Critical)

**Components:** MM-2026-0004 + MM-2026-0005 + MM-2026-0009 + MM-2026-0020 + MM-2026-0027.

**Chain:**
1. **Reconnaissance (zero auth):** the attacker sends `GET /admin` (→ 301 via TSR if `/admin/` exists), `OPTIONS /secret` (→ 204+Allow), `GET /admin//console` (→ 301 with a Location canonicalised by FixedPath). **The auth middleware never fires.**
2. **User enumeration (via timing):** the attacker sends N=1e5 `Basic auth` requests with random usernames + a wrong password → Welch p=0 discriminates valid usernames.
3. **Password-length discovery:** iterates lengths 1..128 per valid username → maximum latency corresponds to the correct length.
4. **Online brute force:** no rate limit; the attacker is unbounded.

**Result:** the attacker maps the infrastructure and authenticates N users without a valid credential being audited.

### MM-TM-2026-0002 — Multi-channel exfiltration + audit-trail forgery (High)

**Components:** MM-2026-0006 + MM-2026-0011 + MM-2026-0012 + MM-2026-0007 + MM-2026-0008.

**Chain:**
1. The attacker forges the IP via XFF → the logger records a "trusted" IP.
2. The attacker sends a request whose payload is reflected in 4 channels:
   - `r.URL.Path` → log (CRLF forgery)
   - `X-Request-ID: <payload>` → response-header reflection
   - `Origin: <payload>` → ACAO reflection (if allowAll)
   - Secret in body + reflected input + compress → BREACH oracle
3. Correlates the channels to speed up exfiltration.

### MM-TM-2026-0003 — ServeFiles + clean_path encoded traversal (High)

**Components:** MM-2026-0018 + MM-2026-0010.

**Chain:**
1. The app registers `/static/*filepath` (serveFiles) + `/admin` (Group with auth).
2. `GET /static/..%2fadmin` → decode → `/static/../admin` → clean → `/admin` → dispatch without auth.

### MM-TM-2026-0004 — Concurrency + pool + panic composite (Critical)

**Components:** MM-2026-0003 + MM-2026-0015 + MM-2026-0014 + MM-2026-0017.

**Chain:**
1. The handler spawns a goroutine retaining `r`.
2. The goroutine reads `r.Context()` while the dispatcher restores `origCtx` + releaseRC → pool contamination.
3. If the handler panics, `releaseRC` is skipped → rc leak.
4. The caller reconfigures `m.NotFound` at runtime → race.

### MM-TM-2026-0005 — Slowloris + timeout + goroutine exhaustion (High)

**Components:** MM-2026-0024 + MM-2026-0019.

**Chain:**
1. The attacker opens 1000 TCP connections with a 1 B / 5 s drip.
2. Without a default `ReadHeaderTimeout`, the connections persist.
3. Eventually the handler is called; the request blocks on a body read.
4. Timeout cancels ctx; the handler ignores it → continues; the goroutine pool grows.

---

## A.11 Dismissed / Refuted (5)

| Hypothesis | Verdict | Reason |
|---|---|---|
| H-007 (RedirectFixedPath open redirect via `//`) | **Refuted** | `path.Clean("//evil/foo")` → `/evil/foo`; relative, same-origin Location. HPS test `redirect-raw-bytes.txt`. (The variant in MM-2026-0005 is different.) |
| H-016 (throttle token leak on panic) | **Refuted** | Deferred cleanup is correct; 16k concurrent panics → 0 tokens leaked. |
| H-019 (regex compile DoS) | **Refuted** | Go RE2 is linear; a 100-op limit rejects exponential patterns. 10k alternations in 380 µs. |
| H-028 (Unicode case-fold asymmetry) | **Refuted** | `foldEq` is ASCII-only by design; confusables do NOT cross-fold (safe). |
| H-023 (rc.small residue across requests) | **Refuted (functional)** | 256k canary = 0 leaks. Defence in depth: optionally zero `rc.small`. |

---

## A.12 Statistics (as of 2026-04-18)

| Severity | Count | % |
|---|---|---|
| Critical | 7 | 13.5% |
| High | 14 | 26.9% |
| Medium | 15 | 28.8% |
| Low | 8 | 15.4% |
| Info | 3 | 5.8% |
| Composites | 5 | 9.6% |

**v1.0.0 blockers:** 21 (7 Crit + 14 High)
**Docs-only blockers:** 3 (MM-2026-0019, -0024, -0026)
**Actionable fixes (including docs):** 35

**CWE distribution (top):**
1. CWE-400 — 7
2. CWE-113 — 5
3. CWE-362 — 4
4. CWE-20 — 4
5. CWE-208 — 3
6. CWE-200 — 3

---

## A.13 Release gate (as of 2026-04-18)

- [x] Zero Crit/High Open → **PASS** (7 Crit + 14 High fixed; MM-2026-0017/0019 Accepted with docs)
- [x] govulncheck zero
- [x] go mod verify OK
- [x] zero-dep
- [x] `go test -race ./...` zero races in 10 iterations → **PASS** (phase 1-6 fixes applied)

**Gate: PASS** (after phase 1-7 fixes). See the posture report (`2026-04-17-posture.md`) for the history of blockers.

---

## A.14 Append rules (2026-04-18)

Sole owner: `threat-modeler-and-zero-day-researcher`. Each new post-sprint finding receives the next `MM-2026-NNNN`. Preserve the source_id. If it is a composite, create `MM-TM-2026-NNNN`.

> **Note (2026-09-25):** from sprint S7 onwards specialists issued `<PREFIX>-2026-NNNN` IDs directly and no new `MM-2026-NNNN` entries were written here; `MM-2026-0048..0053` exist in later reports but only `MM-2026-0051..0053` have a row (B.2). See O-11 and the current rules in "Append rules" at the end of this file.

---

# Part B — Findings reconciliation (rmp #240, 2026-09-25), amended by rmp #263–#266 and #270

Reconciliation state: commit `b038632`. Amended by `fuzzing-and-property-engineer` (rmp #265, at commit `b1986ba`): closed O-2, O-5, O-6 and updated the FPE-2026-005/FPE-2026-006 rows.

Part B holds the 34 findings reconciled by rmp #240: the knowledge-graph Finding nodes whose identifier appears in an rmp task title but was not linked to any report.

## B.1 Conventions

- **Severity** = rmp `severity` (0–10): 9–10 Critical, 7–8 High, 4–6 Medium, 1–3 Low, 0 Info.
- **Status**: `Fixed`, `Verified-safe` (no defect: a hypothesis was refuted, or the result is a regression-guard PASS), `Accepted` (a documented trade-off), `Open`, or `Not-distinct` (bucket (c); each has a justification in B.3). The flag `AC-unmet` means the rmp task was closed although its acceptance criteria are not satisfied in the repository (B.4).
- **Bucket**: (a) documented under another name, or under the same ID in a file an `.md`-only search missed; (b) never documented, now written up; (c) not a distinct finding.
- Paths in "Documented in" are relative to `reports/`, except `SECURITY.md` and `docs/` (repository root). `harness/…` and `evidence/…` refer to the directory of the agent named in the same row.

## B.2 Reconciled entries (34)

| ID | Agent | Sev | Title | Bucket | Documented in (alias) | Status | Fix commit |
|---|---|---|---|---|---|---|---|
| CDX-2026-001 | threat-modeler (composite) | 9 | HandleFast composite: Use auth skip + Recoverer skip + raw-path log injection | a | `overview/2026-05-07-posture.md` (CDX-1) | Fixed via CSA-2026-0054 / CSA-2026-0053 / MSR-2026-0057 | not traced |
| CDX-2026-002 | threat-modeler (composite) | 9 | Tree corruption composite: UTF-8 OOB panic + partial tree + 2^N expansion | a | `overview/2026-05-07-posture.md` (CDX-2) | Fixed via PRF-2026-0009 / MM-2026-0033 / MM-2026-0050 (cap 8 in `786cf9f`) | partially traced |
| CDX-2026-003 | threat-modeler (composite) | 8 | RealIP + Throttle ordering composite, no safe default | a | `overview/2026-05-07-posture.md` (CDX-3); `SECURITY.md` | Accepted (construction-time `slog.Warn` + docs) | not traced |
| CDX-2026-004 | threat-modeler (composite) | 8 | Path-decode composite: %2520 + raw catch-all + %61dmin | a | `overview/2026-05-07-posture.md` (CDX-4); `SECURITY.md` | Fixed (`url.PathUnescape`) + documented | not traced |
| CDX-2026-005 | threat-modeler (composite) | 6 | Timing-oracle composition: JWT alg + OAuth2 cache + route existence | a | `overview/2026-05-07-posture.md` (CDX-5); `SECURITY.md` "Composition of timing oracles (CDX-2026-005)" + individual oracles | Accepted (rmp #266) | — |
| CSA-2026-0056 | concurrency | 0 | Pool/GC canary: no cross-request param contamination | b | `concurrency-security-auditor/2026-09-25-CSA-2026-0056-pool-gc-canary.md` | Verified-safe (default mode; pooled modes verified by rmp #263, see H-RECON-01) | — |
| CSA-2026-0057 | concurrency | 0 | Introspection vs concurrent ServeHTTP: race-free | a | `overview/2026-05-07-posture.md` STRIDE (CSA-57); `harness/s8_hypotheses_test.go`; `harness/h027_introspection_race_test.go` | Verified-safe | — |
| DOS-2026-0051 | dos | 3 | `addRoute` O(N^2) registration cost | a | `dos-resilience-tester/2026-09-25-DOS-2026-0051-registration-cost.md`; `SECURITY.md` "Startup-time route registration cost (DOS-2026-0051)" | Documented (rmp #266, re-measured linear, not O(N²)) | — |
| DOS-2026-0061 | dos | 2 | ThrottlePerIP timeout refs decrement under surge | c | `dos-resilience-tester/harness/s9_dos_test.go` | Not-distinct (re-validates MSR-2026-0068) | — |
| DOS-2026-0063 | dos | 1 | Large method-name dispatch is O(1) | a | `dos-resilience-tester/harness/s9_dos_test.go` (same ID) | Verified-safe | — |
| FPE-2026-0001 | fuzzing | 3 | Regex `{name:expr}` parser stops at the first `}` | a | `fuzzing-and-property-engineer/invariants.md` I-REGEX-01 + `evidence/2026-05-07/CRASH-REGEX-01` (FPE-2026-REGEX-01); `overview/2026-05-07-posture-S8.md` (#83) | Fixed (`tree.go:1255`) | `825c623` |
| FPE-2026-001 | fuzzing | 2 | Unnamed wildcard panics without `muxmaster:` prefix | a | `fuzzing-and-property-engineer/evidence/2026-05-07/CRASH-FPE-001/repro_test.go` (same ID) | Fixed | `dc775ca` |
| FPE-2026-002 | fuzzing | 5 | Mount invalid-UTF-8 prefix panics and leaks `*mux_mount` | a | `overview/2026-05-07-posture.md` STRIDE (FPE-2); `evidence/2026-05-07/CRASH-FPE-002` | Fixed | `7512e98` |
| FPE-2026-004 | fuzzing | 4 | Walk/WalkFast on a post-panic tree: no UB | a | `fuzzing-and-property-engineer/invariants.md` (I-WALK-01..04); `harness/fuzz_walk_corrupted_test.go` | Verified-safe | — |
| FPE-2026-005 | fuzzing | 3 | Timeout context not cancelled within 1 ms of the deadline | b | `fuzzing-and-property-engineer/2026-09-25-FPE-2026-005-timeout-cancellation-property.md` | **Resolved (rmp #265):** reconstructed `TestProp_TimeoutCancelsContext` (I-15b) blocks on `ctx.Done()` and compares the observed fire time against `ctx.Deadline()` with a measured, justified 300 ms tolerance (never before the deadline, unconditionally). Root cause was a test-tolerance defect in the lost original property (1 ms margin between `sleepMs` and `timeoutMs`), not a `middleware/timeout.go` defect — no library code changed. `go test -race -count=20`: 20/20 pass, 2000 total iterations, 0 failures. | — |
| FPE-2026-006 | fuzzing | 4 | Fuzz coverage gap on 8 public surfaces | a | `fuzzing-and-property-engineer/harness/fuzz_s9_new_surfaces_test.go` (same ID); `invariants.md` S9 + Sprint 20 sections | **Fixed (rmp #265):** `FuzzServeFiles` + `TestProp_ServeFilesNoEscape` added (I-SERVEFILES-01/02), closing the last of the 8 named surfaces. 36 s run, 658K execs, 0 crashes, 121 corpus entries persisted to `corpora/FuzzServeFiles/`. | not traced |
| FPE-2026-008 | fuzzing | 3 | FuzzMiddlewareChain missing | a | same harness (same ID); `invariants.md` I-MW-CHAIN-01 | Fixed (narrower catalogue than specified) | not traced |
| FPE-2026-009 | fuzzing | 3 | FuzzMuxDispatch missing | a | same harness (same ID); `invariants.md` I-DISPATCH-01 | Fixed | not traced |
| HPS-2026-0006 | http-protocol | 2 | Compress Vary cache-poisoning blast radius: safe with CDN caveat | a | `http-protocol-security-auditor/harness/compress_vary_test.go` + `evidence/compress-vary-2026-05-07.txt` (HPS-2026-0007..0010); `overview/2026-05-07-posture.md` coverage-gap row | Verified-safe | — |
| MM-2026-0051 | middleware (legacy MM) | 5 | CORS reflects Origin without `Vary: Origin` | a | `middleware-security-reviewer/harness/security_harness_test.go` + `evidence/2026-05-07/battery.txt` (same ID) | Fixed | `7512e98` |
| MM-2026-0052 | middleware (legacy MM) | 2 | api_key 401 without `WWW-Authenticate` | a | `overview/2026-05-07-posture.md` (MM-52); MSR harness + battery; `docs/middleware.md` | Fixed | `7512e98` |
| MM-2026-0053 | middleware (legacy MM) | 2 | compress gzips already-compressed MIME | a | `overview/2026-05-07-posture.md` (MM-53); `middleware-security-reviewer/evidence/2026-09-25/battery.txt` | Fixed | `7512e98` |
| MSR-2026-0056 | middleware | 2 | NoCache missing Surrogate-Control / X-Accel-Expires | a | `middleware-security-reviewer/harness/security_harness_test.go` (same ID) | Fixed | `7512e98` |
| MSR-2026-0064 | middleware | 1 | JWT `crit` rejection conforms to RFC 8725 §3.6 | a | `middleware-security-reviewer/harness/jwt_crit_header_test.go` (Gap C); `overview/2026-05-07-posture.md` coverage-gap row | Verified-safe | — |
| MSR-2026-0066 | middleware | 5 | JWTAuth accepts no-`exp` tokens; add RequireExpiry | a | `overview/2026-05-07-posture-S8.md`, `overview/2026-05-07-sprint-S9.md` (#72); MSR `harness/sprint_s8_test.go`, `harness/sprint_s9_test.go` | Fixed (opt-in; default = TM-2026-001) | `825c623` |
| TSC-2026-0009 | timing | 2 | Error-oracle re-audit + harness validity defect | b | `timing-and-sidechannel-analyst/2026-09-25-TSC-2026-0009-error-oracle.md` | Oracle Accepted; harness defect **Fixed** (rmp #264) | not traced |
| TSC-2026-0010 | timing | 1 | PRNG audit PASS (crypto/rand) | c | `timing-and-sidechannel-analyst/harness/request_id_prng_test.go` | Not-distinct | — |
| TSC-REVAL-2026-001 | timing | 2 | Re-validate BasicAuth password oracle | c | aggregate in `overview/2026-05-07-posture-S9.md` | Not-distinct (TSC-2026-0001) | — |
| TSC-REVAL-2026-002 | timing | 2 | Re-validate BasicAuth user-exists oracle | c | same | Not-distinct (TSC-2026-0002) | — |
| TSC-REVAL-2026-003 | timing | 4 | Re-validate JWT HS256 vs RS256 oracle | c | same | Not-distinct (TSC-2026-0003) | — |
| TSC-REVAL-2026-004 | timing | 3 | Re-validate APIKey hit/miss oracle | c | same | Not-distinct (TSC-2026-0004; regression promoted to TSC-2026-0008) | — |
| TSC-REVAL-2026-005 | timing | 2 | Re-validate route-existence oracle | c | same | Not-distinct (TSC-2026-0005) | — |
| TSC-REVAL-2026-006 | timing | 2 | Re-validate ECDSA zero-sig vs max-sig | c | same | Not-distinct (TSC-2026-0006) | — |
| TSC-REVAL-2026-007 | timing | 1 | OAuth2 cache active vs inactive: not re-measured | c | same | Not-distinct (deferral for TSC-2026-0007) | — |

Totals: (a) 22, (b) 3, (c) 9.

## B.3 Not-distinct entries — justification

**DOS-2026-0061 → MSR-2026-0068.** rmp #172 calls this "regression guard for MSR-2026-0068 fix". `TestThrottlePerIPTimeoutRefsCleanup` (`s9_dos_test.go:642-728`) checks that the timeout branch decrements `refs` so entries drain. It passed and found no new defect. The fixed defect is MSR-2026-0068, cited in SECURITY.md ("ThrottlePerIPCapped saturation").

**TSC-2026-0010 (no ID to duplicate).** rmp #152 is a "PRNG re-audit at HEAD" with verdict "PASS — no action required". It repeats §7 of the 2026-04-17 timing audit (crypto/rand, 100 000 IDs, 0 collisions), which never had a finding ID. That report is restored at `timing-and-sidechannel-analyst/2026-04-17-1320-prerelease-timing-audit.md` (rmp #267). The Sprint 18 request_id buffer pool changed the code after the audit, so the harness was re-run at `b038632`: all `TestPRNG_RequestID_*` pass (chi-square 11.39 < 37.70).

**TSC-REVAL-2026-001..007 → TSC-2026-0001..0007.** Each is an S9 re-measurement, at `e30ae94`, of an oracle already accepted in SECURITY.md ("Accepted Timing Oracles (TSC-2026-0001..0007)"). None produced a new defect under its own label:
- 001, 002, 003, 005 and 006 each closed "within accepted envelope / no code change".
- 004 found a widened delta caused by `WWW-Authenticate` being set only on the miss path. That cause was promoted to TSC-2026-0008 (posture-S9; fixed in `825c623`).
- 007 was not measured; it is a deferral record.

posture-S9 refers to the batch only in aggregate ("5 + 7 revals").

## B.4 Open items

O-1..O-10 were found by the rmp #240 reconciliation; O-11..O-14 by the rmp #267 restore and merge. None was acted on by the task that found it, except O-8 and O-12, which rmp #267 closed.

| # | Item | Evidence |
|---|---|---|
| O-1 | ~~TSC-2026-0009 harness: `TestTiming_ErrorOracle_404vs405` gets 401 for both arms (BasicAuth registered via `Use`) and passes silently~~ — **Resolved (rmp #264):** `buildErrorOracleMux` now registers BasicAuth on a `Group`, not the root `Mux`; every `TestTiming_*` verifies each arm's status before and during measurement. See `timing-and-sidechannel-analyst/2026-09-25-TSC-2026-0009-error-oracle.md` | Re-run at `b038632`; TSC-2026-0009 write-up (updated 2026-09-25) |
| O-2 | ~~FPE-2026-005: cancellation property replaced by a deadline-presence property; no engineering note in `invariants.md` I-15~~ — **Resolved (rmp #265):** `invariants.md` I-15 now carries the engineering note, and a new I-15b documents the reconstructed `TestProp_TimeoutCancelsContext` property (measured 300 ms tolerance, never-before-deadline asserted unconditionally). `go test -race -count=20`: 20/20 pass. No `middleware/timeout.go` code change — the original failure was a test-tolerance defect, not a library defect. | FPE-2026-005 write-up §"Resolution (2026-09-25, rmp #265)"; `invariants.md` I-15/I-15b; `harness/properties_test.go` |
| O-3 | ~~DOS-2026-0051: no startup-time registration-cost note in SECURITY.md (rmp numbers N=2000 → 1.4 s, N=5000 → ~5 s; not re-measured)~~ — **Resolved (rmp #266):** `dos-resilience-tester` re-measured registration cost and confirmed O(N) linear scaling, not O(N²). SECURITY.md "Startup-time route registration cost (DOS-2026-0051)" section added with measured figures (1 000 → ~0.4–0.8 ms, 10 000 → ~6.6–9.9 ms, 100 000 → ~104 ms), threat assessment, and link to the detailed report. | `dos-resilience-tester/2026-09-25-DOS-2026-0051-registration-cost.md` |
| O-4 | ~~CDX-2026-005: SECURITY.md lists the three oracles individually, not their composition or the deployment posture~~ — **Resolved (rmp #266):** new "Composition of timing oracles (CDX-2026-005)" section added to SECURITY.md describing how JWT algorithm-timing (TSC-2026-0003, ~25 µs), OAuth2 cache-hit/miss (TSC-2026-0007, 143 µs), and route-existence (TSC-2026-0005, ~960 ns) oracles combine; four deployment postures recommended (strict algorithm config, response-time padding, rate-limiting, reverse proxy/WAF); all mitigations verified to exist in the codebase or noted as external controls. | SECURITY.md "Composition of timing oracles (CDX-2026-005)" |
| O-5 | ~~`invariants.md` I-REGEX-01 still calls the `}` defect a "known limitation" although `tree.go:1255` fixes it~~ — **Resolved (rmp #265):** I-REGEX-01 now reads "Fixed", cites commit `825c623`. The claim is backed by code, not just prose: `FuzzRegexParamRegistration`'s stale "known limitation: document, do not fail" swallow was removed (it now asserts success for `}`-containing valid regexes, same as any other valid regex), and a new deterministic regression guard `TestRegexBraceFixed` registers and dispatches 4 distinct `}`-containing regexes. `FuzzRegexParamRegistration`: 20 s, 409K execs, 0 crashes. | `invariants.md` I-REGEX-01; `harness/fuzz_regex_param_test.go` (`FuzzRegexParamRegistration`, `TestRegexBraceFixed`) |
| O-6 | ~~FPE-2026-006: ServeFiles has no fuzz or property target~~ — **Resolved (rmp #265):** `FuzzServeFiles` (36 s, 658K execs, 0 crashes) + `TestProp_ServeFilesNoEscape` (rapid, 100 runs, 0 failures) added; new invariants I-SERVEFILES-01/02. Confirms that `http.FileServer`'s `path.Clean` protection holds under fuzzing for the default (`UseRawPath=false`) configuration; symlink-follow (an inherited, documented `net/http.Dir` characteristic, not a MuxMaster gap) is pinned separately by `TestServeFiles_SymlinkFollowsUpstreamBehavior` so it is never conflated with a regression. | `fuzz_s9_new_surfaces_test.go`; `harness/fuzz_servefiles_test.go`; `invariants.md` Sprint 20 section |
| O-7 | ~~MM-2026-0051/0052/0053 acceptance criteria cite `v2_new_findings_test.go::TestSec_*_V2_001`, which does not exist.~~ Re-checked 2026-09-25 (rmp #267): the file exists in no commit of any branch. **Resolved (rmp #268):** the closed-task audit (`2026-09-26-closed-task-audit.md`, "Cited artifacts that do not exist") lists every cited artifact that never existed and its current equivalent; for MM-2026-0051/0053 equivalents exist (`TestSec_CORS_VaryOriginMissing`, `TestCompress_AlreadyCompressedMIME`), for MM-2026-0052 only a partial one — the missing assertions are planned in rmp #285. | rmp #4, #5, #6 |
| O-8 | ~~Overview canonical documents deleted in `5f804fa`~~ — **Resolved (rmp #267):** `findings.md` (this file, Part A), `threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md`, `2026-04-17-posture.md`, `2026-04-17-sprint.md`, `reports/README.md`, the seven 2026-04-17 per-agent reports and `go-sast-and-memory-auditor/semgrep-rules/muxmaster.yml` restored from `5f804fa^`. Not restored by maintainer decision: `reports/.gitignore` (it ignored `evidence/` and `*.pprof`, contradicting the current practice of versioning evidence), ~2 070 corpus files and 60 harness `.go` files (see O-14). | §0 |
| O-9 | ~~`TestTiming_APIKey_HitVsMiss`/`TestTiming_BasicAuth_ValidVsInvalid`/`TestTiming_BasicAuth_UserExistsVsNotExists` fail on any statistically significant difference, conflicting with SECURITY.md's "Accepted Timing Oracles" doctrine for TSC-2026-0001/0002/0004~~ — **Resolved (rmp #270):** each now asserts against an explicit accepted bound (2000 ns / 700 ns / 2500 ns) derived from measured evidence and documented in SECURITY.md next to each TSC entry. All 3 pass, 3× triplicated + once in the full 19-test suite. No library code changed. | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; `evidence/2026-09-25/o9_bound_runs_1-3.log`, `full_suite_o270.log` |
| O-10 | ~~SECURITY.md's "Route-Existence Timing Oracle (MM-2026-0026)" prose ("~440 ns", mislabelled "404 vs 405") does not match the TSC-2026-0005 entry ("923 ns", registered-vs-unregistered) from the same harness~~ — **Resolved (rmp #270):** both sections now cite the same current figure (~960 ns, 4 independent runs) for the same, correctly described pair (registered/200 vs unregistered/404). | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; SECURITY.md "Route-Existence Timing Oracle (MM-2026-0026)" and TSC-2026-0005 |
| O-11 | Findings from sprints S7–S10 (2026-05-07/08) have no ledger row. 86 finding identifiers cited in `2026-05-07-posture*.md`, `2026-05-07-sprint*.md` and `2026-05-08-*.md` appear nowhere in this file: 50 `TM-2026-002..051`, 7 `MSR-2026-*`, 7 `DOS-2026-*`, 6 `CSA-2026-*`, 6 `PRF-2026-*`, 4 `HPS-2026-0001..0004`, 2 `MM-2026-0048/0049`, 2 `FPE-2026-007/010`, 2 `TSC-2026-0011/0012`. `2026-05-07-posture-S8.md` lists "`findings.md` (S8 section appended)" as an output, but the file deleted in `5f804fa` has no S8 section (last update 2026-04-18), so that claim is false. **Resolved (rmp #268):** all 86 identifiers have a row in Part C. | `git show 5f804fa^:reports/overview/findings.md`; posture-S8 outputs list |
| O-12 | ~~Most reproducer and evidence paths cited in Part A were never committed: the deleted `reports/.gitignore` excluded `evidence/` and `*/evidence/`.~~ — **Accepted (rmp #267):** the files cannot be recovered from git. Only the CSA-001..004 reproducers, HPS `harness/repro_test.go`, SAST `harness/h018_ctx_field_type_test.go` and the path-routing-fuzzer `shadow_*` harnesses were in git (all deleted in `5f804fa`, readable with `git show 5f804fa^:<path>`). The Part A citations stay as historical record; each fixed Part A entry with a "Current status" note names the current regression tests that replace the lost reproducers. | `git show 5f804fa^:reports/.gitignore` |
| O-13 | ~~The restored 2026-04-17 overview documents (`threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md`, `2026-04-17-*.md`) are written wholly or partly in Portuguese, contrary to CLAUDE.md §13.2. Part A of this file was translated during the merge; the others were restored verbatim.~~ — **Resolved (rmp #275):** all 6 overview documents have been translated to English per CLAUDE.md §13.2. `threat-model.md` fully verified (128 identifiers preserved); remaining 5 files translated with English content. All identifying IDs (MM-NNNN, H-NNN), file paths, commit hashes, and table structures preserved across all files. Residual Portuguese terms in posture/sprint table headers flagged for final cleanup. | rmp #275 translation sprint; threat-model identifier verification |
| O-14 | ~~Coverage lost in `5f804fa` and not restored.~~ **Resolved (rmp #272, #273, #274).** (1) 60 harness `.go` files were deleted; the per-file assessment is in the rmp #267 task log. Properties with **no current equivalent**: compress removes a stale `Content-Length` after compressing (no test; code at `middleware/compress.go:178`); CORS wildcard at a non-first index + `AllowCredentials` panics; CORS request without `Origin` passes through with no ACAO; Logger writer error does not panic or alter the response; Logger concurrent writes stay line-atomic; NoCache lets the handler override `Cache-Control`; Recoverer survives a panic value whose `String()` panics; a Recoverer placed inside does not catch an outer panic (MM-2026-0034 negative); `ThrottleBacklog` configuration validation panics (limit ≤ 0, backlog < 0); nested `Timeout` picks the shortest deadline; RealIP XFF-over-X-Real-IP precedence; `SetHeader("", v)` behaviour; `WithValue(k, nil)`; 13 of the 20 raw-TCP `smuggle_test.go` variants (TE chunked+chunked, TE tab before value, TE mixed case, obs-fold, bare LF line terminator, bare CR in headers, NUL in header value, NUL and CRLF in request target, duplicate `Content-Length` same and different, oversize method, oversize target); `OPTIONS *` handling; parent-context cancellation reaching a param-route handler's `ctx.Done()`; throttle load-level timing oracle; fuzz-driven differential against httprouter/chi/bunrouter; wire-level XFF CRLF admission. (2) `5f804fa` also removed about 60 `Test*` functions from harness files it rewrote (e.g. `TestTiming_H002b_PasswordLengthOracle` — the MM-2026-0020 regression test —, `TestErrorOracleMatrix`, `TestTiming_RedirectFixedPath_Oracle`, 11 `TestTiered_*`, `TestProp_HandleIdempotencyAndConflict`, `TestProp_ServeFilesRegistersTwoRoutes`, `TestMiddlewareInteractionMatrix`); (2) has not been assessed. **Middleware part resolved (rmp #273):** the 13 middleware properties above plus 7 more found while assessing the 81 functions removed from the MSR harness now have tests in `middleware/gap_o14_test.go` (23 `TestSec_*`); ~67 removed functions have equivalents under new names (mostly `middleware-security-reviewer/harness/security_harness_test.go`); no defect in the restored properties. One discrepancy found and fixed (rmp #276): Recoverer appended its error text after a partial response; it now writes the 500 only if the response has not started (`specification/middleware-stdlib.md` item 14). **HTTP-protocol part resolved (rmp #272):** the 13 raw-TCP smuggling variants have per-subtest-isolated, RFC 9112-cited tests (`http-protocol-security-auditor/harness/hps_o14_smuggling_test.go`); `OPTIONS *`, handler-set header CRLF on the wire, wire-level `X-Forwarded-For` control bytes with RealIP (7 byte classes), a raw-TCP byte-class map to `r.URL.Path` (10 classes) and `Use` wrapping of 404/405/OPTIONS/TSR are in `harness/hps_o14_wire_test.go`; HTTP/2 header-value CRLF rejection is in `harness/h2harness/h2_crlf_test.go`. Of 36 removed HTTP-scope functions, 10 restored, 20 have equivalents. No MuxMaster defect; one documentation gap: `OPTIONS *` is answered by `net/http`'s own handler before `Mux.ServeHTTP` and `Pre` run — planned as its own task. **Routing, concurrency, fuzz and timing parts resolved (rmp #274):** context propagation through every reqBundle tier and pooled mode (`ctx_propagation_test.go`), tiered-dispatch and throttle-peak harness tests; `FuzzDifferentialSecurity` against httprouter/chi/bunrouter (1.33M execs, no bypass), wire-level percent-encoded traversal, ServeFiles boundary; `FuzzCleanPathDoubleEncoded`, `FuzzParamsMap`, `FuzzWalkRoutes`, `FuzzMuxHandleTwice`, `FuzzCompressMultipleWrites`, `FuzzStripSlashesIdempotency` and five properties; the MM-2026-0020 password-length oracle (TSC-2026-0013), the 15-pair error-oracle matrix, the RedirectFixedPath oracle and an interleaved throttle boundary oracle (TSC-2026-0014); literal `RedirectFixedPath` Location values against open redirect. Two real defects found and fixed: **FPE-O14-002** (an unclosed regex `{` silently overwrote an existing route) and **FPE-O14-003** (`StripSlashes` desynchronised `RawPath` on an encoded trailing slash). **O-14 resolved.** | rmp #267 and #272 task logs; `git show 5f804fa^:<path>`; `git diff 5f804fa^ 5f804fa -- '*.go'` |

## B.5 Identifier-namespace collisions

| ID | Collision | Recommendation |
|---|---|---|
| FPE-2026-001 | rmp #38 (wildcard panic) and rmp #173 (FuzzCORS corsEmpty harness fix after MSR-2026-0070) share the ID | Keep #38; detach #173 (harness maintenance, `fuzz_cors_test.go:32-37`) |
| FPE-2026-0001 / FPE-2026-001 | Different findings (regex `}` vs unnamed wildcard) | Never merge on a normalised ID |
| FPE-2026-002 / FPE-2026-0002 | Mount invalid UTF-8 vs `sanitiseForLog` non-idempotency (sprint-S9, `middleware/logger.go:112`) | Distinct nodes |
| HPS-2026-0005..0010 | In HPS harnesses, 0006 also labels a CONTINUATION-flood check (`h2_attack_test.go:342-362`) and a HandleFast guard (`http_protocol_audit_test.go:391`); 0007..0010 are each used twice; 0005 also labels Rapid Reset/HPACK checks | No REPORTED_IN edges from a plain string match in HPS harnesses |
| 2026-04-17 short source IDs (`HPS-001..008`, `PRF-001..006`, `DOS-001..009`, `CSA-001..010`, `SAST-001..013`, `TSC-001..006`, `FPE-001..009`) vs `<PREFIX>-2026-NNN[N]` | Different namespaces: e.g. `FPE-001` (request_id CRLF, part of MM-2026-0011) ≠ `FPE-2026-001` (unnamed wildcard); `PRF-001` (MM-2026-0001) ≠ `PRF-2026-0001` | Match only on the full literal ID (added rmp #267) |
| `MM-TM-2026-NNNN` vs `TM-2026-NNN` | Part A composites vs later threat-model items (e.g. `TM-2026-001` in MSR-2026-0066) | Distinct namespaces (added rmp #267) |

## B.6 Hypotheses raised by the reconciliation

These hypotheses are also recorded in `hypotheses.md` (section "Hypotheses from the 2026-09-25 findings reconciliation").

**H-RECON-01 — contamination under the opt-in pooled modes** (owner: concurrency-security-auditor, High). **Resolved — refuted (rmp #263):** `pool_contamination_test.go` runs concurrent canaries (64 goroutines × 3000 requests, 1/2/3/overflow/catch-all tiers) and panic-path checks with `PoolRequestBundle` and `PoolFastParams` enabled, separately and together, under `-race`: no cross-request contamination and no dirty object returned to a pool. Two deterministic tests pin the documented retention hazard. See `concurrency-security-auditor/2026-09-25-H-RECON-01-pooled-mode-canaries.md`. Original hypothesis: CSA-2026-0056 covers only the GC-managed default. `PoolRequestBundle` (O13) and `PoolFastParams` (O9) recycle storage across requests by design, and no test runs a contamination canary with either flag set; they appear only in benchmarks. Test: run the `TestPool_*` canaries with each flag on, under `-race`, plus a handler that retains `r` past return.

**H-RECON-02 — silent-pass timing harnesses** (owner: timing-and-sidechannel-analyst, Medium). **Resolved (rmp #264):** all 19 `TestTiming_*` functions across the 6 timing-harness files now call `VerifyArmStatus` (new helper in `harness/timing.go`) once per arm before the sample loop, and re-check the status on every sample inside the loop, `t.Fatalf`-ing on any mismatch. Demonstrated live: deliberately setting the wrong expected status for the "405" arm made `TestTiming_ErrorOracle_404vs405` fail immediately at the preflight step. See `timing-and-sidechannel-analyst/2026-09-25-TSC-2026-0009-error-oracle.md` for the fix, the re-measured 404-vs-405/404-vs-401 figures (triplicated), and O-9. Original hypothesis: TSC-2026-0009 shows a harness that logs, rather than fails on, a wrong status code. Test: every `TestTiming_*` asserts that the status codes of both arms differ as intended before computing statistics. Every accepted oracle in SECURITY.md depends on these harnesses.

**H-RECON-03 — closed-with-unmet-acceptance** (owner: threat-modeler, Medium). **Confirmed (rmp #268):** of 183 closed rmp tasks carrying a finding identifier, 79 are met, 53 met by a later mechanism, 50 unmet and 1 unverifiable (`2026-09-26-closed-task-audit.md`). Most gaps are promised tests, documents or evidence rather than missing fixes; every unmet task maps to an open rmp task (#280–#290). The process lesson stands: a task is not closed until each acceptance criterion is verified in the repository. The rmp #240 reconciliation found 4 of the 34 closed tasks failing their acceptance criteria in the repository: #151, #166, #176, #60. None of the 35 originating tasks records a completion summary, and many were closed milliseconds after being started. Test: check every closed security task's acceptance criteria against the repository.

State of the 4 known cases, verified 2026-09-25 at `a510565` (rmp #267):
- **#151** (TSC-2026-0009) — **met** after rmp #264: `error_oracle_test.go::buildErrorOracleMux` registers BasicAuth on a `Group`; the 404/405 distinction is measured with valid arms; accepted oracles are documented in `SECURITY.md`.
- **#166** (DOS-2026-0051) — **met** after rmp #266 through the AC's documentation branch: `SECURITY.md` "Startup-time route registration cost (DOS-2026-0051)".
- **#176** (FPE-2026-005) — **met** after rmp #265 through the AC's second branch (engineering note in `invariants.md` I-15, tolerance-bounded property I-15b). Caveat: the first branch ("passes consistently under `-count=1000` on all GOOS") was never exercised; the evidence is `-count=20` on linux/amd64.
- **#60** (CDX-2026-005) — **met** after rmp #266: `SECURITY.md` "Composition of timing oracles (CDX-2026-005)" enumerates the three oracles and four deployment postures.

The hypothesis stays open because its test — checking every closed security task, not only these 4 — has not been run. The 4 rmp tasks still have `completion_summary = null`.

# Part C — Findings from sprints S7–S10 (2026-05-07/08), ledgered by rmp #268

These rows close O-11: the finding identifiers cited in `2026-05-07-posture*.md`, `2026-05-07-sprint*.md` and `2026-05-08-*.md` that had no ledger row. Status is the state verified at HEAD on 2026-09-26, including the corrections found by the closed-task audit (`2026-09-26-closed-task-audit.md`). Five identifiers were reused for different findings (HPS-2026-0001, CSA-2026-0058, PRF-2026-0001, PRF-2026-0002, PRF-2026-0005); each row describes both meanings.

| ID | Family | Severity | Title | Documented in | Status | Evidence | Fix commit | rmp |
|---|---|---|---|---|---|---|---|---|
| CSA-2026-0050 | concurrency-security-auditor (CSA) | rmp #19 sev 9 (S7 posture: sev=9 release blocker "Rebuild/cfgOnce race") | Rebuild() reset of cfgOnce races frozenConfigSlow -> SIGSEGV | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-07-sprint-S8.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | cfgOnce removed; Rebuild() is a single m.cfg.Store(nil) (mux.go:1054). reports/concurrency-security-auditor/harness/rebuild_race_test.go TestRebuild_CfgOnce_Race + TestRebuild_NilCfgPtr_Race PASS -race (run 2026-09-26, rmp #268 audit); full go test -race ./... PASS (root module). Note: 2026-05-07-sprint-S8.md:24 attributes this fix to e6c34a9; the commit that names CSA-2026-0050 is af0d497. | af0d497 | #19 |
| CSA-2026-0051 | concurrency-security-auditor (CSA) | rmp #20 sev 9 | Use() races m.middleware read in the lazy NotFound/405/OPTIONS builders | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-07-sprint-S8.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Middleware slice snapshotted under m.mu.RLock before wrapMiddleware. reports/concurrency-security-auditor/harness/tiered_dispatch_race_test.go TestUse_LazyNotFound_Race / _LazyMethodNotAllowed_Race / _LazyOPTIONS_Race PASS -race (run 2026-09-26, rmp #268 audit). | 6bab46d | #20 |
| CSA-2026-0052 | concurrency-security-auditor (CSA) | rmp #21 sev 8 | MethodNotAllowed (and other public handler fields) race lazy builder reads | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-08-docs-audit.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Public handler fields frozen into muxConfig; Rebuild resets caches. reports/concurrency-security-auditor/harness/public_fields_race_test.go:116 TestPublicFields_MethodNotAllowed_Race PASS -race (run 2026-09-26, rmp #268 audit); SECURITY.md:97 "Thread-Safety Contract (MM-2026-0017 / CSA-2026-0052)". | e6c34a9 (follow-up test fix-up 802f656) | #21 |
| CSA-2026-0055 | concurrency-security-auditor (CSA) | rmp #24 sev not set (0); S7 posture hypothesis #2 REFUTED | setReqCtxUnsafe ABI drift on Go 1.26.x (hypothesis; validated safe) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | S7 posture hypothesis #2 REFUTED (validated safe). TestSetReqCtxUnsafe_MassiveParallel (h001_r_ctx_goroutine_race_test.go:83), TestReqCtxField_OffsetIsCorrect / _ParamsAccessible_AllTiers (h018_reqctx_offset_test.go:42,112) PASS -race (run 2026-09-26, rmp #268 audit). No gotip cross-build job exists (tracked in rmp #289). | none (no defect) | #24, #289 |
| CSA-2026-0058 | concurrency-security-auditor (CSA) | rmp #85 sev 4 (priority 2) | PanicHandler that panics gets no second recover; net/http closes the connection (H8-30, doc-only) | reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | Behaviour unchanged by design; documented: Mux.PanicHandler GoDoc MUST-NOT-panic warning (mux.go, added in 825c623) and SECURITY.md:653 "Layered panic recovery (CSA-2026-0058 / CSA-2026-0059)". reports/concurrency-security-auditor/harness/s8_hypotheses_test.go TestH8_30_PanicHandler_Panics_IsContained PASS -race (run 2026-09-26, rmp #268 audit). ID COLLISION: rmp #27 also carries CSA-2026-0058 for a different result (jwtHMACPool/oauth2Cache race-free; met: mwharness/middleware_race_test.go:48,100,151,207 PASS -race). The source document (2026-05-07-sprint-S9.md:31) cites the PanicHandler meaning (#85). | 825c623 (GoDoc warning; traced by git log -S "CSA-2026-0058", not named in the message) | #85, #27 |
| CSA-2026-0060 | concurrency-security-auditor (CSA) | sev 8 (S9 posture; rmp #182 sev 8); CWE-440 / CWE-863 | routeCtxParams type switch drops params when Use() middleware wraps the context | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-08-docs-audit.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Slow-path ctx.Value(contextKey{}) fallback (params.go:534, :557). In-tree mux_test.go TestRegression_CSA_2026_0060 and _DeepWrap PASS -race; harness s9_hypotheses_test.go TestH9_10_ContextPropagation_ReqBundle_Done PASS; go test -race -count=10 . ./middleware/ PASS (run 2026-09-26, rmp #268 audit). SECURITY.md "Resolved Findings (v1.0.0)" row (SECURITY.md:37). | 825c623 | #182 |
| DOS-2026-0002 | dos-resilience-tester (DOS) | rmp #36 sev 6; part of CDX-3 (S7 posture composite sev 8) | ThrottlePerIP registered before RealIP degrades to a global rate limit | reports/middleware-security-reviewer/2026-09-24-sprint18-contention-fixes.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | Operator-ordering trap retained; mitigated by GoDoc (middleware/throttle.go), construction-time slog.Warn when keyFn is nil, and SECURITY.md:313 "RealIP + ThrottlePerIP ordering (DOS-2026-0002)". Correct-order assertion: reports/dos-resilience-tester/harness/dos_v2_test.go TestThrottlePerIPNoBlockWithCorrectOrdering and MSR sprint_s8_test.go TestSec_Composition_RealIP_Before_ThrottlePerIP_CorrectKey PASS (run 2026-09-26, rmp #268 audit). Composite CDX-2026-003 (rmp #58) IP-spoof -> 401 test still missing (rmp #285). | b390495 (docs + warning) | #36, #58, #285 |
| DOS-2026-0005 | dos-resilience-tester (DOS) | rmp #35 sev 7 | OAuth2 cache-full DoS: attacker fills MaxCacheSize with long-lived tokens | reports/middleware-security-reviewer/2026-09-24-sprint18-contention-fixes.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | cache.set evicts the soonest-expiring entry when full (later rewritten as a min-heap, sprint 18 rmp #246). reports/dos-resilience-tester/harness/oauth2_stampede_test.go TestOAuth2MaxCacheSizeExhaustion PASS (run 2026-09-26, rmp #268 audit). | e30ae94 | #35 |
| DOS-2026-0057 | dos-resilience-tester (DOS) | sev 5 (S9 posture; rmp #168 sev 5) | ThrottlePerIPCapped saturation: attacker holds all table slots, blocking new IPs | reports/dos-resilience-tester/2026-05-08-production-loadtest.md, reports/middleware-security-reviewer/2026-09-24-sprint18-contention-fixes.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/perf-lab-2026-09-24/waste-hunt.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | SECURITY.md:736 "ThrottlePerIPCapped saturation (TM-2026-013, DOS-2026-0057) - ACCEPTED"; GoDoc contract in middleware/throttle.go. reports/dos-resilience-tester/harness/s9_dos_test.go:70 TestThrottlePerIPCappedSaturationHoldout PASS -race (run 2026-09-26, rmp #268 audit) (harness-only; CI excludes /reports/, rmp #289). | 825c623 (documentation of the accepted contract) | #168, #103 |
| DOS-2026-0058 | dos-resilience-tester (DOS) | rmp #169 sev 1 | methodNotAllowedCache / optionsCache key space is closed (not attacker-controlled) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | Caches keyed by Allow values built from registered methods only. reports/dos-resilience-tester/harness/s9_dos_test.go:210 TestMethodNotAllowedCacheKeySpace and :255 TestOptionsCacheBoundedGrowth PASS -race (run 2026-09-26, rmp #268 audit). Refutes TM-2026-014. | none (no defect) | #169, #104 |
| DOS-2026-0059 | dos-resilience-tester (DOS) | rmp #170 sev 4 | selectXFFRightmost O(N x M) on adversarial XFF with an all-trusted chain | reports/dos-resilience-tester/2026-05-08-production-loadtest.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Hop cap const maxXFFHops = 30 (middleware/real_ip.go:100). In-tree TestSec_TM_2026_021_RealIP_XFF_HopCap PASS -race; s9_dos_test.go TestSelectXFFRightmostAdversarialLength / _WorstCaseAllTrusted PASS -race (run 2026-09-26, rmp #268 audit). | 825c623 | #170, #111 |
| DOS-2026-0060 | dos-resilience-tester (DOS) | rmp #171 sev 2; S9 posture: "bounded - informational" | Logger sanitiseForLog O(L) on adversarial UTF-8 path (bounded, informational) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | reports/dos-resilience-tester/harness/s9_dos_test.go:546 TestLoggerSanitiseForLogAdversarialUTF8 PASS: 4.579 ns/byte without -race (run 2026-09-26, rmp #268 audit). The 10 ns/byte threshold is only logged, not asserted (tracked in rmp #285). | none (no defect) | #171, #107, #285 |
| DOS-2026-0062 | dos-resilience-tester (DOS) | rmp #174 sev 1 | Compress slow-read: middleware does not buffer the full response in memory | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | reports/dos-resilience-tester/harness/s9_dos_test.go:772 TestCompressSlowReadMemoryProfile PASS -race: heap delta 887 KB for a 10 MB response (run 2026-09-26, rmp #268 audit). Covers memory only; the slow-reader/WriteTimeout angle of TM-2026-020 is untested (rmp #285). | none (no defect) | #174, #110 |
| FPE-2026-007 | fuzzing-and-property-engineer (FPE) | sev 5 (S9 posture; rmp #178 sev 5) | CDX-S8-003 matrix property test missing for Use -> HandleFast panic | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #290 | Partially addressed: reports/fuzzing-and-property-engineer/harness/fuzz_s9_cdx_matrix_test.go TestProp_CDXMatrix_UsePanicsOnHandleFast / _PreWrapsBothRouteTypes / _UseFastOnlyForFastRoutes PASS -race (run 2026-09-26, rmp #268 audit), but the property test exercises only Group.HandleFast; the root Mux case is asserted only by in-tree mux_test.go TestRegression_FPE_2026_010. invariants.md has no I-CDX-01 (only I-CDX-01b, -02, -03) and still lists FPE-2026-010 as OPEN. rmp #178 audit verdict: unmet. | not traced (harness file first appears in 5f804fa, a report-consolidation commit) | #178, #290 |
| FPE-2026-010 | fuzzing-and-property-engineer (FPE) | sev 6 (S9 posture; rmp #181 sev 6); CWE-693 / CWE-863 | Mux.Use + Mux.HandleFast silently skips middleware (missing root panic guard) | reports/fuzzing-and-property-engineer/invariants.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-08-docs-audit.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/perf-lab-2026-09-24/waste-hunt-results.md, reports/perf-lab-2026-09-24/waste-hunt.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Panic guard in Mux.HandleFast (mux.go:562-572). In-tree mux_test.go TestRegression_FPE_2026_010 PASS -race (run 2026-09-26, rmp #268 audit); SECURITY.md "Resolved Findings (v1.0.0)" row (SECURITY.md:39). Residue: invariants.md still says OPEN; TestCDX_MuxUsePlusHandleFastNowPanics never added (rmp #290). | 825c623 | #181, #290 |
| HPS-2026-0001 | http-protocol-security-auditor (HPS) | rmp #15 sev 5 (Mount meaning); rmp #77 sev 4 (Logger meaning, CWE-117) | ID COLLISION: (a) Mount.mountAt RawPath trim yields a non-rooted URL (S7 posture); (b) Logger logs r.Method unsanitised, log injection via LF (S8/S9 posture) | reports/overview/2026-05-07-posture-S8.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | (a) mux.go Mount zeroes RawPath on a non-rooted trim (mux.go:797, :804); http_protocol_audit_test.go TestHPS0003_Mount_RawPath_Asymmetry PASS with no asymmetry line (run 2026-09-26, rmp #268 audit). (b) middleware/logger.go writes appendSanitisedForLog(buf, r.Method) (logger.go:310); s8_hypotheses_test.go TestS8_Logger_Method_NotSanitised and MSR sprint_s8_test.go TestSec_Logger_Method_Sanitised_Regression PASS (run 2026-09-26, rmp #268 audit) (harness-only). 2026-05-07-posture.md uses meaning (a); 2026-05-07-posture-S8.md and -S9.md use meaning (b). Both meanings Fixed. | (a) 32d3c77; (b) 825c623 (introduced sanitiseForLog(r.Method); traced by git log -S, not named in the message) | #15, #77 |
| HPS-2026-0002 | http-protocol-security-auditor (HPS) | rmp #16 sev not set (0); duplicate of CSA-2026-0054 (S7 posture: sev=9 structural trap) | HandleFast bypasses Group.Use() stdlib middleware (auth-bypass trap) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Duplicate of CSA-2026-0054 (rmp #23) per 2026-05-07-posture.md "Overlaps" table. At HEAD: registration-time panics mux.go:562-572 and group.go:67-71; SECURITY.md "Pre vs Use security boundary (CSA-2026-0059 / H8-01)"; tests mux_test.go (HandleFast-with-Use panic tests) and harness handlefast_panic_test.go PASS (run 2026-09-26, rmp #268 audit). | 65cde88 (Group.HandleFast guard, canonical CSA-2026-0054); 825c623 (root Mux guard) | #16, #23 |
| HPS-2026-0003 | http-protocol-security-auditor (HPS) | rmp #17 sev 4 | CORS silent-permissive when AllowedOrigins is empty | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | CORS panics at construction on nil/empty AllowedOrigins (middleware/cors.go:49). Harness TestSec_CORS_EmptyAllowedOrigins_SilentPermissive asserts the panic and PASSes (run 2026-09-26, rmp #268 audit). | 7512e98 | #17, #43 |
| HPS-2026-0004 | http-protocol-security-auditor (HPS) | rmp #18 sev 5 | StripSlashes does not update RawPath (divergence when UseRawPath=true) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Initial fix trimmed trailing slashes from RawPath (32d3c77); encoded-slash (%2F) case completed in c6b0d8e: middleware/strip_slashes.go strips RawPath by the same number of separator tokens; in-tree TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync PASS (run 2026-09-26, rmp #268 audit). TestHPS0014_StripSlashes_RawPath_Divergence PASS but its INFO text is stale. | 32d3c77 (partial); c6b0d8e (complete) | #18, #101 |
| MM-2026-0048 | legacy MM (orchestrator ledger series) | rmp #1 sev not set (0); rmp #1 FR: "Critical race"; canonical CSA-2026-0050 sev 9 | Rebuild() races frozenConfigSlow -> SIGSEGV | reports/concurrency-security-auditor/2026-09-25-rmp271-race-suite-timeout-fix.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Duplicate of CSA-2026-0050 (rmp #19), which is canonical per 2026-05-07-posture.md "Overlaps" table; see CSA-2026-0050 evidence (rebuild_race_test.go TestRebuild_CfgOnce_Race PASS -race (run 2026-09-26, rmp #268 audit)). Not present in Part A of findings.md (which ends at MM-2026-0047). | af0d497 (canonical fix) | #1, #19 |
| MM-2026-0049 | legacy MM (orchestrator ledger series) | rmp #2 sev not set (0); canonical CSA-2026-0051 sev 9 | Use() races the lazy handler cache (lazyNotFound / 405 / OPTIONS) | reports/concurrency-security-auditor/2026-09-25-rmp271-race-suite-timeout-fix.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Umbrella duplicate of CSA-2026-0051 (rmp #20) and CSA-2026-0052 (rmp #21) per 2026-05-07-posture.md "Overlaps" table; tiered_dispatch_race_test.go TestUse_Lazy*_Race PASS -race (run 2026-09-26, rmp #268 audit). | 6bab46d (canonical fix); e6c34a9 (public-field facet) | #2, #20, #21 |
| MSR-2026-0055 | middleware-security-reviewer (MSR) | rmp #40 sev 7; part of CDX-3 (S7 posture composite sev 8) | RealIP() with no trusted CIDRs trusts every peer (trivial XFF spoof) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | Trust-all default retained; mitigated by construction-time slog.Warn (middleware/real_ip.go:46), GoDoc, SECURITY.md:296 "RealIP misconfiguration (MSR-2026-0055)" and README "Security defaults". Harness TestSec_RealIP_NoTrustedProxies_IgnoresXFF only logs. rmp #40 audit verdict unmet: docs/max-performance.md:345 calls RealIP() without CIDRs (tracked in rmp #288). Same class as TM-2026-044. | b390495 (docs + warning) | #40, #134, #288 |
| MSR-2026-0058 | middleware-security-reviewer (MSR) | rmp #43 sev not set (0); canonical HPS-2026-0003 sev 4 | CORS empty AllowedOrigins passes requests through silently | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Duplicate of HPS-2026-0003 (rmp #17) per 2026-05-07-posture.md "Overlaps" table; CORS panics on empty AllowedOrigins (middleware/cors.go:49); TestSec_CORS_EmptyAllowedOrigins_SilentPermissive PASS (run 2026-09-26, rmp #268 audit). | 7512e98 (canonical fix) | #43, #17 |
| MSR-2026-0060 | middleware-security-reviewer (MSR) | rmp #45 sev 3 | Compress omits Vary: Accept-Encoding for responses below minCompressSize | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | middleware/compress.go:151-155 always adds Vary: Accept-Encoding in commit(); probe at HEAD: 100-byte response carries Vary with no Content-Encoding (run 2026-09-26, rmp #268 audit). Regression gap: TestSec_Compress_SmallResponseVaryPresent never added; TestSec_Compress_SmallResponseVaryAbsent only logs (rmp #285). Related: HPS-2026-0006 (rmp #68) blast-radius assessment. | 7512e98 | #45, #68, #285 |
| MSR-2026-0061 | middleware-security-reviewer (MSR) | rmp #46 sev 5 | CleanPath does not zero RawPath when %2e%2e traversal remains valid after Clean | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | middleware/clean_path.go:44-54 zeroes RawPath for encoded traversal; probe at HEAD: RawPath /a/%2e%2e/etc/passwd -> "" (run 2026-09-26, rmp #268 audit). Regression gap: TestSec_CleanPath_RawPathWithTraversalZeroed and _PercentEncodedDotTraversal_RawPathBypass only t.Logf; in-tree test uses a literal "..", not %2e%2e (rmp #285). | 32d3c77 | #46, #285 |
| MSR-2026-0063 | middleware-security-reviewer (MSR) | rmp #62 sev 5 | OAuth2 introspection cache poisoning blast radius up to CacheTTL (default 60 s) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | Documented in OAuth2Options.CacheTTL GoDoc (middleware/oauth2.go:51-59) and SECURITY.md:248 "OAuth2 introspection cache poisoning (MSR-2026-0063)"; CacheTTL<0 disables the cache. TestSec_OAuth2_MITMFalseIdPWindowDocumentation PASS (run 2026-09-26, rmp #268 audit). No test asserts CacheTTL=-1 re-introspects per request; middleware_test.go:2275 comment "CacheTTL: 0 // Disable caching" is wrong (rmp #288). | e30ae94 (documentation) | #62, #288 |
| MSR-2026-0069 | middleware-security-reviewer (MSR) | rmp #75 sev 3 | Logger does not sanitise r.Method | reports/overview/2026-05-07-posture-S8.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Exact duplicate of HPS-2026-0001 meaning (b) (rmp #77) per 2026-05-07-posture-S8.md:31. Fixed: logger.go:310 appendSanitisedForLog(buf, r.Method); sprint_s8_test.go TestSec_Logger_Method_NotSanitised / _Sanitised_Regression PASS (run 2026-09-26, rmp #268 audit). | 825c623 (canonical fix, traced by git log -S) | #75, #77 |
| MSR-2026-0071 | middleware-security-reviewer (MSR) | sev 3 (S9 posture; rmp #159 sev 3) | OAuth2 singleflight leader cancellation poisons followers with 401 | reports/concurrency-security-auditor/2026-09-24-sprint18-contention-fixes.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Leader detaches with context.WithTimeout(context.Background(), 30s) (middleware/oauth2.go:416). In-tree middleware_test.go TestSec_MSR_2026_0071_OAuth2SingleflightLeaderCancel and harness sprint_s9_test.go TestSec_OAuth2_SingleflightLeaderCancel_PoisonsFollowers PASS -race (run 2026-09-26, rmp #268 audit). Open residue: not documented in SECURITY.md (rmp #287); detaching from Background drops request context values - context.WithoutCancel proposed (rmp #280). | 825c623 | #159, #280, #287 |
| PRF-2026-0001 | path-routing-fuzzer (PRF) | rmp #47 sev 5 (CleanPath meaning); rmp #78 sev 3 (consecutive-optional meaning) | CleanPath via Pre() re-routes traversal to an unintended handler (operator boundary) | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-08-docs-audit.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | SECURITY.md:178 "Path normalisation accepted behaviour (PRF-2026-0001..0005)" CleanPath bullet; reports/path-routing-fuzzer/harness/hypotheses_test.go TestHA_CleanPathThenStripSlashes PASS (run 2026-09-26, rmp #268 audit). CleanPath GoDoc lacks a re-routing warning; UseRawPath amplification (PRF-2026-S9-001, rmp #155) not in SECURITY.md (rmp #287). ID COLLISION: rmp #78 reuses PRF-2026-0001 for "consecutive optional segments {/:a}{/:b} always panic" - panic message added (tree.go:1350, commit 825c623), mux_test.go TestS8_PRF_ConsecutiveOptionals PASS, but the specification (routing.md rules 24-25) does not state the limitation (rmp #288). The source documents (2026-05-07-posture.md:94, 2026-05-08-docs-audit.md:51 range "PRF-2026-0001-0005") use the CleanPath meaning. | deca72f (documentation); rmp #78 meaning: 825c623 | #47, #78, #155, #287, #288 |
| PRF-2026-0002 | path-routing-fuzzer (PRF) | rmp #48 sev 4 (%61dmin meaning); rmp #79 sev 4 (UseRawPath+UnescapePathValues meaning) | UseRawPath=false lets %61dmin match /admin (RFC 3986 normalisation) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | SECURITY.md:178 accepted-behaviour bullet and SECURITY.md:189 "UseRawPath traversal (PRF-2026-0002 / CDX-S8-002)"; TestEncoding_SinglePercent PASS (run 2026-09-26, rmp #268 audit). ID COLLISION: rmp #79 reuses PRF-2026-0002 for "UseRawPath+UnescapePathValues yields %2f-decoded / inside :param" - runtime slog.Warn (mux.go, 825c623), ServeFiles panics on the combination; TestS8_H820_RawPathMatrix PASS; SECURITY.md mitigation 2 wrongly claims examples/static-site sanitises params (rmp #287). The source document (2026-05-07-posture.md:66, CDX-4) uses the %61dmin meaning. | deca72f (documentation); rmp #79 meaning: 825c623 (warning) | #48, #79, #59, #287 |
| PRF-2026-0005 | path-routing-fuzzer (PRF) | rmp #51 sev 4 (catch-all meaning); rmp #82 sev 1 (harness meaning) | Catch-all *filepath value carries raw traversal sequences (operator boundary) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | SECURITY.md:178 catch-all bullet; ServeFiles GoDoc SECURITY note; TestInvariant_CatchallNotEscapingPrefix PASS (run 2026-09-26, rmp #268 audit). rmp #51 audit verdict unmet: doc.go catch-all syntax GoDoc says nothing about raw ".." (rmp #288). ID COLLISION: rmp #82 reuses PRF-2026-0005 for "FuzzCleanPath/FuzzStripSlashes harness panics on space/#/% via httptest.NewRequest" - harness fixed with fuzzServeBypassParse (s8_audit_test.go), 60 s fuzz runs clean (run 2026-09-26, rmp #268 audit). The source document (2026-05-07-posture.md:66, CDX-4) uses the catch-all meaning. | deca72f (documentation); rmp #82 meaning: not traced (harness file first appears in 5f804fa) | #51, #82, #59, #288 |
| PRF-2026-0006 | path-routing-fuzzer (PRF) | rmp #52 sev 7 | UnescapePathValues=true double-decodes %2520 to a space (validation bypass) | reports/overview/2026-05-07-posture.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | Decode gated on UseRawPath=true and switched to url.PathUnescape. reports/path-routing-fuzzer/harness/hypotheses_test.go:876 TestUnescapePathValues_NoDoubleDecode PASS against HEAD (-mod=mod); probe /x/hello%2520world -> "hello%20world" (run 2026-09-26, rmp #268 audit). Gaps: no CHANGELOG entry; regression test lives only in the PRF harness (stale vendor copy, excluded from CI; rmp #289). | 3a9be0b | #52, #59, #289 |
| PRF-2026-0007 | path-routing-fuzzer (PRF) | rmp #53 sev not set (0); canonical MM-2026-0050 | expandOptional exponential O(2^N) registration for N optional segments | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-07-sprint-S8.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Duplicate of MM-2026-0050 (rmp #3) per 2026-05-07-posture.md "Overlaps" table. Cap maxOptionalSegments = 8 (tree.go:151) counted in O(len) before expansion; consecutive optionals rejected (tree.go:1350); evidence repro reports/path-routing-fuzzer/evidence/2026-05-07/PRF-007-expandOptional-exponential/repro_test.go rejects N=12 in ~15 us (run 2026-09-26, rmp #268 audit). | 786cf9f | #53, #3, #57 |
| PRF-2026-0008 | path-routing-fuzzer (PRF) | rmp #54 sev not set (0); canonical CSA-2026-0050 sev 9 | Rebuild() non-atomic write to cfgOnce races concurrent ServeHTTP | reports/overview/2026-05-07-posture.md, reports/overview/2026-05-07-sprint-S8.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | Duplicate of CSA-2026-0050 (rmp #19) per 2026-05-07-posture.md "Overlaps" table; TestRebuild_CfgOnce_Race PASS -race (run 2026-09-26, rmp #268 audit). Inconsistency: 2026-05-07-sprint-S8.md:25 uses "PRF-2026-0008/9 family" for two-phase registration + RawPath canonicalisation (commit 32d3c77), which is not the rmp #54 meaning. | af0d497 (canonical fix) | #54, #19 |
| TM-2026-002 | threat-modeler hypothesis (S9 catalogue); assigned MSR/FPE/TSC | rmp #92 sev 6 | JWT exp claim type confusion (null/negative/NaN/float overflow) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | S9 posture: UNTESTED. Fix rejects negative exp/nbf/iat and malformed JSON types (guard jwt_auth.go ~:448). reports/middleware-security-reviewer/harness/2026-05-08-S10-PreMSR/s10_premsr_test.go TestSec_JWT_ExpNull/_ExpStringValue/_ExpNegative/_ExpZero/_ExpLargeFloat/_ExpBoolean/_ExpNaNFloat/_ExpInRangeFloat PASS (run 2026-09-26, rmp #268 audit). Residue: exp=null and exp=0 are treated as "no exp" (accepted only when RequireExpiry=false); this equivalence is not documented in GoDoc or SECURITY.md and is not tracked by any open rmp task. | 825c623 | #92 |
| TM-2026-003 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #93 sev 4 | JWT sub-claim spoof within a trusted issuer (no sub validation) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted (rmp #286) | rmp #286 (2026-09-26): JWTAuth authenticates the token (signature, alg allowlist, exp/nbf, Issuers and Audiences allowlists: jwt_auth.go:60-63,156-163) but performs no validation or authorisation of 'sub'; raw.Sub is copied verbatim into JWTClaims.Subject (jwt_auth.go:310,480). Test shows: two issuers sharing one HMAC key both assert sub=admin and both are accepted, distinguishable only by Issuer; a syntactically odd sub ('../admin\n') is passed through unchanged; re-encoding the payload with a different sub while keeping the signature is rejected with 401 and the handler is not reached. Spoofing sub therefore requires the signing key (refuted); what remains is by design: sub is signer-asserted, not unique across issuers, and authorisation is the handler's job. Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_003_JWT_SubjectIsSignerAssertedNotValidated. Correction: S9 posture §5 recorded 'REFUTED — by-design; documented'. The by-design part holds, but it was never tested and it is NOT documented (no statement in SECURITY.md, JWTAuth/JWTClaims GoDoc or docs/middleware.md). Reclassify as Accepted (by design) with the doc gap tracked. Previous: S9 posture: REFUTED "by-design; documented". #268 audit: no test exists and the "documented" basis is false - neither SECURITY.md, JWTAuth GoDoc nor docs/middleware.md says JWTAuth does not validate sub and handlers must authorise on Claims.Subject (jwt_auth.go exposes Subject). Reclassification in rmp #286; documentation in rmp #287. | none | #93, #286, #287 |
| TM-2026-004 | threat-modeler hypothesis (S9 catalogue); assigned HPS/MSR | rmp #94 sev 6 | OAuth2 url.Parse vs HTTP-client URL handling divergence | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | S9 posture: UNTESTED; S10 final verdict: REFUTED (mitigated, CL-OAUTH2-1). OAuth2Introspect rejects userinfo and empty host at construction (middleware/oauth2.go:264-272). S10-PreMSR TestSec_OAuth2_URLParse_UserinfoRejected / _EmptyHostRejected / _PlaintextRejected / _CRLFInPath / _JavascriptScheme PASS (run 2026-09-26, rmp #268 audit). | 825c623 | #94 |
| TM-2026-005 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #95 sev 4 | OAuth2 AllowInsecureEndpoint slog.Warn leaks the endpoint with credentials | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | S10 final verdict: CONFIRMED -> fixed; SECURITY.md:40 "Resolved Findings" row TM-2026-005 (sev 4, CWE-532). slog calls log only host+scheme (middleware/oauth2.go:278-290). S10-PreMSR TestSec_OAuth2_SlogWarn_NoCredentialLeak / _InsecureEndpoint_FullURL_NotLogged PASS (run 2026-09-26, rmp #268 audit); no in-tree regression test. Residual same-class leak: the userinfo-rejection panic embeds the full endpoint incl. password (oauth2.go:272) - tracked in rmp #280. | 825c623; e9fd648 | #95, #280 |
| TM-2026-006 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #96 sev 3 | OAuth2 introspection cache key collision (sha256 truncation?) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9 posture: REFUTED by code reading. Verified at HEAD: cache and singleflight maps keyed by the full [32]byte sha256 digest (middleware/oauth2.go) - no truncation (run 2026-09-26, rmp #268 audit). | none (no defect) | #96 |
| TM-2026-007 | threat-modeler hypothesis (S9 catalogue); assigned CSA/MSR | rmp #97 sev 5 | Group-mounted stdlib handler bypasses Pre-registered JWT | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: PARTIAL (CL-AUTH-1); S10 final verdict: REFUTED. In-tree mux_test.go TestRegression_TM_2026_007 PASS; S10-PreCSA TestMount_ExternalHandler_WithGroupMiddleware, TestTM007_GroupHandleFast_* PASS (run 2026-09-26, rmp #268 audit). | none (regression test added in 825c623) | #97 |
| TM-2026-008 | threat-modeler hypothesis (S9 catalogue); assigned CSA | rmp #98 sev 4 | UseFast on Group bypasses Pre-registered JWT when Pre is registered after the Group | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: PARTIAL; S10 final verdict: REFUTED. In-tree mux_test.go TestRegression_TM_2026_008 PASS; S10-PreCSA TestTM008_UseFast_Group_WithPreOnRoot PASS (run 2026-09-26, rmp #268 audit). | none (regression test added in 825c623) | #98 |
| TM-2026-009 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #99 sev 5 | UseRawPath + UnescapePathValues + custom Handle (not ServeFiles) traversal | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | S9: CONFIRMED-AS-DOC (operator boundary; canonical PRF-2026-0002 / CDX-S8-002). SECURITY.md:189 "UseRawPath traversal"; runtime slog.Warn in mux.go; in-tree mux_test.go TestRegression_TM_2026_009_UseRawPath_HandlerBoundary PASS (run 2026-09-26, rmp #268 audit). SECURITY.md mitigation 2 points to examples/static-site, which does not implement the sanitisation (rmp #287). | 825c623 (warning + regression test) | #99, #79, #287 |
| TM-2026-010 | threat-modeler hypothesis (S9 catalogue); assigned PRF/MSR | rmp #100 sev 6 | clean_path + UseRawPath=true ordering: clean uses Path, dispatch uses RawPath | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): CleanPath zeroes RawPath whenever path.Clean changes it or when its decoded form differs from the cleaned Path (clean_path.go:41-57, MSR-2026-0061), so after CleanPath RawPath is empty or percent-decodes to the cleaned Path. New E2E test: Mux.UseRawPath=true, Pre(CleanPath, path-based /admin gate), routes /admin, /admin/*rest, /public/*f, /:seg; 30 targets including /admin/%2e%2e/x, /admin/sub/%2e%2e/%2e%2e/x, /public/%2e%2e%2fadmin, double-encoded and mixed-case forms: 0 bypasses and the RawPath/Path invariant holds for every request. Negative control: a cleaner that cleans Path but leaves RawPath (the pre-MSR-0061 shape) IS caught by the same harness (bypass via /admin/%2e%2e/x -> /admin/*rest), proving the test is discriminating. Tests: security_tm_reclass_test.go::TestSec_TM_2026_010_CleanPath_UseRawPath_PreGate_NoBypass; middleware/middleware_test.go::TestSec_TM_2026_010_CleanPath_UseRawPath_NoBypass (existing; weak — asserts no panic only); reports/path-routing-fuzzer/harness/s9_audit_test.go::TestS9_H05_CleanPath_MSR2026_0061_EncodedDotDot (existing). Correction: S9 posture §5 recorded CONFIRMED (PRF-2026-S9-001) and §7 item 10 recommended documenting clean_path+UseRawPath as mutually exclusive and panicking. That applied to the pre-MSR-2026-0061 CleanPath; at HEAD the combination is safe and neither the guard nor the doc is needed. Reclassify: CONFIRMED (historical, fixed by MSR-2026-0061) -> Refuted at HEAD; drop the mutual-exclusion recommendation. Previous: Contradictory classifications: S9 posture CONFIRMED (PRF-2026-S9-001) with a recommendation to make the pair mutually exclusive; #268 audit: no auth bypass at HEAD - in-tree middleware_test.go TestSec_TM_2026_010_CleanPath_UseRawPath_NoBypass (/a%2fdmin -> 404; /admin/%2e%2e/admin -> RawPath zeroed) PASS (run 2026-09-26, rmp #268 audit). The residual re-routing (/static/%2e%2e/admin -> admin handler, TestS9_H05_CleanPath_MSR2026_0061_EncodedDotDot) is the accepted PRF-2026-0001 class; no mutual-exclusion guard exists and the UseRawPath amplification is not in SECURITY.md. Reclassification: rmp #286; documentation: rmp #287 (via #155). | none (no guard; RawPath zeroing from MSR-2026-0061 fix 32d3c77) | #100, #155, #286, #287 |
| TM-2026-011 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #101 sev 3 | strip_slashes + Mount prefix + escaped trailing slash | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | S9: CONFIRMED (PRF-2026-S9-003). StripSlashes strips RawPath by the same number of / or %2F tokens; in-tree TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync PASS; PRF TestS9_H07_StripSlashes_RawPath_HPS2026_0004 PASS (-mod=mod) (run 2026-09-26, rmp #268 audit). | c6b0d8e | #101 |
| TM-2026-012 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #102 sev 3 | Catch-all + Group prefix joining a percent-encoded segment | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #282 | S9: PARTIAL ("covered by PRF-S9-008"). #268 audit: hypothesis never tested (TestTM012_GroupCatchAllTraversal tests dot-segments, not an encoded separator in a Group prefix); group.go has no prefix validation. Probe at HEAD: Group("/v1%2fapi").GET("/x") registers; with UseRawPath=true /v1%2fapi/x -> 200 and /v1/api/x -> 404; default mode both 404. | none | #102, #282 |
| TM-2026-013 | threat-modeler hypothesis (S9 catalogue); assigned DOS | rmp #103 sev 6 | ThrottlePerIPCapped saturation: attacker holds all slots indefinitely | reports/middleware-security-reviewer/2026-09-24-sprint18-contention-fixes.md, reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/perf-lab-2026-09-24/waste-hunt.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | S9: CONFIRMED as DOS-2026-0057; ACCEPTED in SECURITY.md:736. s9_dos_test.go:70 TestThrottlePerIPCappedSaturationHoldout PASS (run 2026-09-26, rmp #268 audit). | 825c623 (documentation) | #103, #168 |
| TM-2026-014 | threat-modeler hypothesis (S9 catalogue); assigned DOS | rmp #104 sev 4 | lazyNotFound / lazyMethodNotAllowed / optionsCache unbounded growth | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED (DOS-2026-0058, closed key space). s9_dos_test.go TestMethodNotAllowedCacheKeySpace / TestOptionsCacheBoundedGrowth PASS; PRF TestS8_AllowHeader_SyncMapBloat PASS (run 2026-09-26, rmp #268 audit). | none (no defect) | #104, #169 |
| TM-2026-015 | threat-modeler hypothesis (S9 catalogue); assigned DOS | rmp #105 sev 4 | OAuth2Cache TTL eviction race vs introspect storm under singleflight | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): Every cache miss goes through the per-token singleflight group (oauth2.go:453-483); the leader re-checks the cache (oauth2.go:457-461). New test: warm the cache (1 IdP call), let the 50 ms TTL expire, hold the refresh in flight, fire 256 concurrent requests for the same token, release: exactly 2 IdP calls total (warm-up + one coalesced refresh), all 256 requests 200. Passed 3/3 runs under -race. Residual (accepted, bounded): the singleflight key is removed (oauth2.go:161-164) before the caller stores the result in the cache (oauth2.go:495-501); a request that misses the cache inside that microsecond window starts one extra refresh — at most one additional call per window per token, not an N-fold storm. Tests: reports/dos-resilience-tester/harness/tm015_ttl_storm_test.go::TestDOS_TM_2026_015_TTLExpiryStorm_SingleUpstreamCall; reports/dos-resilience-tester/harness/oauth2_stampede_test.go::TestDOS_OAuth2CacheStampede (existing, related). Correction: S9 posture §5 UNTESTED; final-maturity-verdict §4.2 'TM-015/019 deferred v1.1.x'. Now tested: Refuted (singleflight, DOS-OAUTH2-001 fix). Record the delete-before-set window as an accepted, bounded residual. Previous: Never tested: S9 posture UNTESTED; S10 final verdict "TM-015/019 deferred v1.1.x". Related coverage only (oauth2_stampede_test.go TestDOS_OAuth2CacheStampede, TestOAuth2StaleReadAfterExpiry). No test triggers TTL eviction during in-flight introspection. | none | #105, #286 |
| TM-2026-016 | threat-modeler hypothesis (S9 catalogue); assigned DOS | rmp #106 sev 4 | paramsBuf overflow / nested Mount+Group exceeding the 8-optional cap | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED. Cap enforced on the fully joined pattern: PRF TestS8_H826_OptionalSegmentsCap_GroupChain, TestS8_H827_MountGroupOptionalCap and dos TestCap8OptionalUpperBound PASS (run 2026-09-26, rmp #268 audit). | none (no defect) | #106 |
| TM-2026-017 | threat-modeler hypothesis (S9 catalogue); assigned DOS/FPE | rmp #107 sev 3 | strconv.QuoteToASCII CPU on adversarial UTF-8 | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: UNTESTED ("DOS-2026-0060 says bounded - informational"). s9_dos_test.go:546 TestLoggerSanitiseForLogAdversarialUTF8 PASS, linear ~4.5 ns/byte up to 65 535-byte paths (run 2026-09-26, rmp #268 audit); no super-linear cost. Valid-vs-invalid ratio not computed explicitly. | none (no defect) | #107, #171 |
| TM-2026-018 | threat-modeler hypothesis (S9 catalogue); assigned PRF/DOS | rmp #108 sev 3 | expandOptional cap=8 vs Mount-prefix concatenation | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED. Probe at HEAD: the cap applies after prefix join (Mount with a 9-optional prefix panics; Group(5)+Mount(4) panics); TestS8_H827_MountGroupOptionalCap PASS (run 2026-09-26, rmp #268 audit). Side observation: any optional segment in a Mount prefix panics with a confusing "'' in path" wildcard-conflict message (rmp #281). | none (cap from 786cf9f) | #108, #281 |
| TM-2026-019 | threat-modeler hypothesis (S9 catalogue); assigned DOS/CSA | rmp #109 sev 3 | OAuth2Cache evictExpiredLocked under contention | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): The O(n) evictExpiredLocked scan no longer exists: sprint-18 rmp #246 (CH-09) replaced it with a container/heap min-heap; set() evicts one heap root in O(log n) under the write lock (oauth2.go:196-245). Measured saturation set() latency 3.27 us @cpu1 / 2.15 us @cpu16 vs 247-257 us before (reports/perf-lab-2026-09-24/contention-hunt.md CH-09). Re-run 2026-09-26 with -race: CSA S10 TestTM019_EvictExpiredLocked_Contention and TestTM019_NoRaceInEviction PASS; in-tree TestOAuth2Cache_* (size cap, expired-first, soonest-expiry fallback, heap invariants under concurrent churn, no race at saturation) PASS. Tests: reports/concurrency-security-auditor/harness/2026-05-08-S10-PreCSA/s10_tm019_oauth2_eviction_test.go::TestTM019_EvictExpiredLocked_Contention; reports/concurrency-security-auditor/harness/2026-05-08-S10-PreCSA/s10_tm019_oauth2_eviction_test.go::TestTM019_NoRaceInEviction; middleware/oauth2_cache_internal_test.go::TestOAuth2Cache_HeapInvariants_UnderConcurrentChurn; middleware/oauth2_cache_internal_test.go::TestOAuth2Cache_ConcurrentGetSetAtSaturation_NoRace. Correction: final-maturity-verdict §4.2 listed TM-019 both as REFUTED (CL-AUTH-1) and deferred (CL-OAUTH2-1). Single status: Refuted — superseded by the CH-09 min-heap rewrite (rmp #246). Remove the 'deferred v1.1.x' entry. Previous: Contradictory classifications: S9 UNTESTED; S10 final verdict lists TM-019 both as REFUTED (CL-AUTH-1) and deferred (CL-OAUTH2-1). #268 audit: S10-PreCSA s10_tm019_oauth2_eviction_test.go TestTM019_EvictExpiredLocked_Contention / _NoRaceInEviction PASS (run 2026-09-26, rmp #268 audit); superseded by the sprint-18 min-heap eviction (rmp #246; set() 3.27 us @cpu1 vs 247-257 us before). Reclassification recorded in rmp #286. | not traced (min-heap rewrite is rmp #246, sprint 18) | #109, #246, #286 |
| TM-2026-020 | threat-modeler hypothesis (S9 catalogue); assigned DOS | rmp #110 sev 3 | compress + slow-read attacker (response slowloris) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #285 | S9: REFUTED on the basis of DOS-2026-0062 (heap only). #268 audit: the hypothesis (slow reader; server WriteTimeout must still fire through Compress) was never tested - no test reads at 1 B/s against an http.Server with WriteTimeout. Memory aspect verified (TestCompressSlowReadMemoryProfile PASS). | none | #110, #174, #285 |
| TM-2026-021 | threat-modeler hypothesis (S9 catalogue); assigned DOS/FPE | rmp #111 sev 4 | selectXFFRightmost on adversarial XFF with 1000 entries | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Fixed | S9: CONFIRMED as DOS-2026-0059. maxXFFHops = 30 (middleware/real_ip.go:100). In-tree TestSec_TM_2026_021_RealIP_XFF_HopCap PASS; s9_dos_test.go TestSelectXFFRightmostAdversarialLength / _WorstCaseAllTrusted PASS (run 2026-09-26, rmp #268 audit). | 825c623 | #111, #170 |
| TM-2026-022 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #112 sev 3 | Logger leaks Authorization / Cookie / X-Api-Key headers | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: UNTESTED. S10-PreMSR TestSec_Logger_AuthorizationHeader_NotLogged / _LogFormat_OnlyMethodPathStatusDuration / _CRLFInAuthHeader_NotInLog PASS (run 2026-09-26, rmp #268 audit): the log line is timestamp, method, path, status, duration only. No in-tree test. | none (no defect) | #112 |
| TM-2026-023 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #113 sev 3 | OAuth2 slog.Warn leaks Endpoint userinfo at construction | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-distinct | S9: "dup TM-005". Resolved by the TM-2026-005 fix and tests; same residual panic-message leak (oauth2.go:272) tracked in rmp #280. | 825c623; e9fd648 (via TM-2026-005) | #113, #95, #280 |
| TM-2026-024 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #114 sev 2; S9 posture label: "sev 2 - informational" | RealIP misconfiguration slog.Warn leaks the trusted-CIDR list | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): The only log call in RealIP is the no-CIDR construction warning (real_ip.go:45-49); it is a constant message with no attributes. Test captures slog.Default as JSON: RealIP(10.20.30.0/24, 2001:db8:abcd::/48) emits no RealIP record; RealIP() emits exactly one WARN record whose keys are only time/level/msg and whose text contains no address or prefix. Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_024_RealIP_WarningCarriesNoCIDRs. Correction: S9 posture §5 UNTESTED. Now tested: Refuted. Previous: S9: UNTESTED (sev 2 informational); never classified later. #268 audit code reading suggests refutation (the only RealIP log call, middleware/real_ip.go:46, carries no CIDR list), but no test or recorded classification exists. | none | #114, #286 |
| TM-2026-025 | threat-modeler hypothesis (S9 catalogue); assigned MSR/CSA | rmp #115 sev 2; S9 posture label: "sev 2" | Recoverer logs handler name / function path | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted (rmp #286) | rmp #286 (2026-09-26): Confirmed by design, not a defect: RecovererWithLogger logs the raw panic value and the full debug.Stack() (handler function names and absolute source paths) at Error level (recoverer.go:104-109), as its GoDoc states (recoverer.go:78-79). The client receives only the generic 'Internal Server Error\n' body (recoverer.go:110-112). Test asserts the stack record contains the handler name tmReclassPanickingHandler and the source file name, the panic value is logged verbatim, and the response body is the generic 500 text only. Rationale for acceptance: the information stays in the server-side log sink; stack traces are required for diagnosis. Residual: log sinks receive code-structure detail and whatever the panic value contains (e.g. a secret passed to panic()). Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_025_Recoverer_LogsStackServerSideOnly. Correction: final-maturity-verdict §4.2 recorded REFUTED; the S10 CSA tests it cited (TestTM025_PanicRecoverer_NoPoolContamination / _PanicHandler_NoPoolContamination) test pool contamination, not this hypothesis. Behaviour confirms the premise (handler names and paths ARE logged) -> reclassify as Confirmed-by-design / Accepted. Previous: S10 final verdict recorded REFUTED (CL-AUTH-1); #268 audit: wrong - RecovererWithLogger logs the full debug.Stack() (function names, source paths) at Error level (middleware/recoverer.go:106), documented intended server-side behaviour, so correct classification is confirmed-by-design. S10 tests TestTM025_* test pool contamination, not this hypothesis. Reclassification recorded in rmp #286. | none (by design) | #115, #286 |
| TM-2026-026 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #116 sev 1; S9 posture label: "sev 1" | SetHeader CRLF panic message exposes the header value | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted (rmp #286) | rmp #286 (2026-09-26): SetHeader panics only in its constructor (set_header.go:29-34), from operator-supplied configuration, before any request is served; the request path is a single map assignment that cannot panic (set_header.go:55-58). The panic message DOES contain the value, escaped with strconv.QuoteToASCII (no raw CR/LF). Test asserts: CRLF value -> panic whose message contains QuoteToASCII(value) and no raw CR/LF; a valid SetHeader never panics per request even with CR/LF/NUL/ESC in the request path and a conflicting request header. No attacker-controlled path reaches the panic. Residual: an operator who passes a secret containing CR/LF sees it (escaped) in the startup panic output. Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_026_SetHeader_PanicIsConstructionTimeAndEscaped; middleware/middleware_test.go::TestSetHeaderRejectsCRLF (existing). Correction: S9 posture §5 UNTESTED. Premise partially true (value is echoed) but only at construction from trusted config, escaped: Accepted, sev 1 informational. Previous: S9: UNTESTED (sev 1); never classified. Code at HEAD: SetHeader panics only in its constructor (middleware/set_header.go:29-34); in-tree TestSetHeaderRejectsCRLF PASS (run 2026-09-26, rmp #268 audit). No classification recorded. | none | #116, #286 |
| TM-2026-027 | threat-modeler hypothesis (S9 catalogue); assigned CSA/HPS | rmp #117 sev 4; S9 posture label: "sev 4 - needs verification" | PanicHandler default behaviour reaches the client (recovered value in body) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10 final verdict: REFUTED. In-tree mux_test.go TestRegression_TM_2026_027_PanicHandlerNoLeak PASS; S10-PreCSA TestTM027_PanicHandlerPanicsItself / _WritesAfterWritten / _DoubleWrite_Idempotency PASS (run 2026-09-26, rmp #268 audit). With PanicHandler unset, the panic propagates with nothing written. | none (regression test added in 825c623) | #117 |
| TM-2026-028 | threat-modeler hypothesis (S9 catalogue); assigned CSA | rmp #118 sev 2 | introspection Routes/Walk leak topology when exposed via a debug endpoint | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED by-design (operator-exposed only); S10 CL-AUTH-1 REFUTED. S10-PreCSA s10_tm028_walk_test.go (3 tests, concurrency) PASS (run 2026-09-26, rmp #268 audit). No exposure warning in introspection.go GoDoc; docs/observability.md places /debug/routes on an internal-only Mux. | none (by design) | #118 |
| TM-2026-029 | threat-modeler hypothesis (S9 catalogue); assigned TSC | rmp #119 sev 5 | BREACH on compress + dynamic tokens (chosen plaintext) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-applicable (rmp #286) | rmp #286 (2026-09-26): Premise false: there is no 'compress variable-padding fix'. Compress contains no padding logic (compress.go has none; random padding is operator guidance in the Compress GoDoc, compress.go:242-263, and SECURITY.md 'BREACH Compression Oracle', SECURITY.md:551+). Test asserts the compressed body decompresses byte-identically to the handler output and its length is identical across 33 runs — Compress adds nothing that could mask length. The underlying BREACH oracle is an already-accepted, documented risk (MM-2026-0030 / DOS-2026-0006; reports/dos-resilience-tester/harness/breach_oracle_test.go incl. TestBREACHOracleWithRandomPadding showing operator-side padding drops Cohen's d below 0.03). Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_029_Compress_HasNoPadding; reports/dos-resilience-tester/harness/breach_oracle_test.go::TestBREACHOracleWithRandomPadding (existing). Correction: S9 posture §5 UNTESTED ('TSC coverage deferred'). No KS test is needed: the hypothesis tests a mitigation MuxMaster does not implement. Reclassify Not-applicable; BREACH itself stays Accepted under MM-2026-0030. Previous: Never tested: S9 UNTESTED ("TSC coverage deferred"); no KS test with paired secret/no-secret samples exists. #268 audit: premise false - Compress has no variable-padding logic (padding is operator guidance in its GoDoc). Related only: dos TestBREACHOracleWithRandomPadding PASS. | none | #119, #286 |
| TM-2026-030 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #120 sev 1; S9 posture label: "sev 1 cosmetic" | set_header overwrites Server header (fingerprint inconsistency) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): net/http emits no Server header and MuxMaster adds none, so SetHeader('Server', v) adds rather than overwrites. Registered mux-wide via Use or Pre it is present, with the same single value, on every router-generated response class: matched route 200, 404, 405 (incl. HEAD on GET-only), 301 trailing-slash redirect, 204 automatic OPTIONS (test covers both modes and asserts each class was exercised). A handler can still overwrite it (SetHeader runs before next, set_header.go:56-57) and scoped registration (Group.Use, or Use after some routes) limits it to that scope — both follow the documented registration-time model, not a router inconsistency. Tests: security_tm_reclass_test.go::TestSec_TM_2026_030_SetHeaderServer_UniformAcrossResponseClasses. Correction: S9 posture §5 UNTESTED (sev 1 cosmetic). Now tested: Refuted for mux-wide registration; per-scope variation is operator configuration. Previous: S9: UNTESTED (sev 1 cosmetic); never validated or classified later. | none | #120, #286 |
| TM-2026-031 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #121 sev 4; S9 posture label: "sev 4" | CORS reflects Origin without Vary: Origin (CDN cache poisoning) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: UNTESTED (sev 4). In-tree middleware TestSec_TM_2026_031_CORS_VaryOrigin PASS (reflected ACAO carries Vary: Origin); HPS TestHPSExt17_CORS_VaryOriginPresent PASS (run 2026-09-26, rmp #268 audit). Vary: Origin emission introduced for MM-2026-0051 (7512e98). | none (Vary: Origin from 7512e98; test added in 825c623) | #121 |
| TM-2026-032 | threat-modeler hypothesis (S9 catalogue); assigned MSR/HPS | rmp #122 sev 4 | sanitiseForLog edge cases enable log injection in a SIEM | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #287 | S9: PARTIAL. In-tree TestSec_TM_2026_032_LoggerSanitises PASS (no raw CR/LF/TAB) (run 2026-09-26, rmp #268 audit). SIEM-specific vector (escaped sequences re-interpreted after a JSON-parse layer) untested and the SECURITY.md note was not written (audited under rmp #122; documentation in rmp #287). | none | #122, #287 |
| TM-2026-033 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #123 sev 3 | cors + CDN + Vary cache-key smuggling | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Confirmed — defect, fix planned in rmp #291 (rmp #286) | rmp #286 (2026-09-26): DEFECT (not fixed, per instructions). What MuxMaster controls: for an Origin in an explicit allowlist, CORS sets ACAO=<origin> and Vary: Origin (cors.go:134-149); Compress adds Vary: Accept-Encoding (compress.go:155); on the wire Go emits them as two separate Vary field lines (valid per RFC 9110 §5.3). But CORS returns early for a request without Origin (cors.go:93-96) and emits neither Vary: Origin nor ACAO, and in wildcard mode emits ACAO: * only for CORS requests (cors.go:130-133). The Fetch Standard ('CORS protocol and HTTP caches') requires exactly the opposite: when ACAO is sent only in response to CORS requests, Vary: Origin must be sent on non-CORS responses too; when ACAO is * it must be sent on non-CORS responses too. Consequence: a cache (browser HTTP cache or shared CDN) that stores a cacheable non-CORS response (no Vary) serves it, without ACAO, to a later CORS request from an allowed origin (RFC 9111 §4.1: a stored response without Vary matches any request), and the browser blocks it — cache-poisoned denial of service of cross-origin consumers, triggerable by any non-CORS request (navigation, curl, cache warmer). Severity: Low (availability only, no confidentiality/integrity; requires a cacheable response, i.e. explicit Cache-Control or heuristic freshness). CWE-436 (interpretation conflict) is the closest class. Divergent CDN Vary parsing (e.g. reading only the first Vary line) is a deployment residual outside MuxMaster's control. Tests: reports/http-protocol-security-auditor/harness/tm_reclass_s20_test.go::TestHPS_TM_2026_033_Reproducer_NonCORSResponseLacksVaryOrigin (pins the defect; fails with an explicit message once fixed and must then be inverted); reports/http-protocol-security-auditor/harness/tm_reclass_s20_test.go::TestHPS_TM_2026_033_CORSCompress_VaryOnTheWire; middleware/middleware_test.go::TestSec_TM_2026_031_CORS_VaryOrigin (existing; allowed-origin case only). Correction: S9 posture §5 UNTESTED ('no CDN harness'). Analysis of emitted headers shows a MuxMaster-side defect independent of any CDN: CORS does not send Vary: Origin (allowlist mode) or ACAO: * (wildcard mode) on non-CORS responses, contrary to the Fetch Standard. New finding needs a ledger ID and a fix task (not created here). Deployment residual: CDNs that ignore Vary or read only the first Vary field line. Previous: S9: UNTESTED ("no CDN harness"); no test simulates divergent Vary parsing. | none | #123, #286 |
| TM-2026-034 | threat-modeler hypothesis (S9 catalogue); assigned HPS/PRF | rmp #124 sev 3; S9 posture label: "sev 3" | Redirect chain via RedirectTrailingSlash + Mount nesting | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #281 | S9: UNTESTED. #268 audit probe at HEAD: outer.Mount("/v2", inner): GET /v2/x -> 301 Location "/x/", GET /v2/y/ -> 301 Location "/y" - the inner Mux redirect drops the mount prefix (wrong-target redirect, not an open redirect). Partial coverage: in-tree redirect_tsr_safety_test.go (plain-handler Mount). | none | #124, #281 |
| TM-2026-035 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #125 sev 2; S9 posture label: "sev 2" | HTTP/2 trailers carrying unsanitised values reach Logger | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted (rmp #286) | rmp #286 (2026-09-26): Logger formats only time, method, URL.Path, status and duration (logger.go:297-307) and never reads r.Trailer. Test 1: request with r.Trailer carrying ESC, a marker and '\nFAKE 200' -> exactly one log line with none of them. Test 2: real HTTP/1.1 chunked request with a trailer field delivered to the handler (precondition asserted) -> one log line without the trailer value. HTTP/2 delivers trailers through the same r.Trailer field, which Logger does not access. Tests: middleware/tm_reclass_test.go::TestSec_TM_2026_035_Logger_IgnoresTrailers. Correction: S9 posture §5 UNTESTED. Now tested: Refuted. Previous: S9: UNTESTED (sev 2); never classified under its own ID. #268 audit code reading suggests refutation (Logger formats only method, URL.Path, status, duration; no Trailer access), but no classification is recorded. | none | #125, #286 |
| TM-2026-036 | threat-modeler hypothesis (S9 catalogue); assigned CSA | rmp #126 sev 4 | Group middleware ordering allows skip via direct child registration | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: PARTIAL; S10: REFUTED. In-tree mux_test.go TestRegression_TM_2026_036 PASS; S10-PreCSA TestTM036_Group_Middleware_Ordering / _Race PASS (run 2026-09-26, rmp #268 audit). | none (regression test added in 825c623) | #126 |
| TM-2026-037 | threat-modeler hypothesis (S9 catalogue); assigned CSA | rmp #127 sev 3 | UseFast bypasses Use auth gate on /admin/* via accidental fast-route registration | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: PARTIAL ("FPE-010 covers root case"); S10: REFUTED. S10-PreCSA TestTM037_RootMuxUse_HandleFast_Panics and related matrix tests PASS; in-tree TestRegression_FPE_2026_010 PASS (run 2026-09-26, rmp #268 audit). Depends on the FPE-2026-010 guard. | none (guard from 825c623) | #127, #181 |
| TM-2026-038 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #128 sev 3; S9 posture label: "sev 3" | Transposition: nginx merge_slashes / off-by-slash | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10: REFUTED (CL-PATH-1). reports/path-routing-fuzzer/harness/prerelease_v100_test.go TestTM038_NginxMergeSlashes PASS (-mod=mod) (run 2026-09-26, rmp #268 audit); //admin, ///admin, /admin// -> 404 with no redirect (fail-closed). Harness-only. | none (no defect) | #128 |
| TM-2026-039 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #129 sev 5; S9 posture label: "sev 5 - important" | Transposition: Caddy CVE-2022-0653 path normalisation order | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10: REFUTED (CL-PATH-1). In-tree mux_test.go TestRegression_TM_2026_039_CaddyTransposition PASS -race; prerelease_v100_test.go TestTM039_CaddyCVE20220653 (10 payloads, 3 modes) PASS (run 2026-09-26, rmp #268 audit). | none (regression test added in 825c623) | #129 |
| TM-2026-040 | threat-modeler hypothesis (S9 catalogue); assigned PRF/MSR | rmp #130 sev 4 | Transposition: Traefik CVE-2022-46153 middleware-chain path traversal | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #284 | S9: PARTIAL ("covered by CL-PATH-1"); never re-classified. #268 audit probe at HEAD: a path-inspecting gate registered as Pre(gate, CleanPath()) is bypassed - /pub/../admin, /pub/%2e%2e/admin and //admin reach the /admin handler (200); Pre(CleanPath(), gate) returns 403. No document states the required order. | none | #130, #284 |
| TM-2026-041 | threat-modeler hypothesis (S9 catalogue); assigned - | rmp #131 sev 2 | Transposition: Express path-to-regexp ReDoS | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED ("N/A - radix not regex"); stated basis is wrong (regex params compile a Go regexp, tree.go), but the conclusion holds because Go regexp is RE2 (linear). TestS9_H16_RegexParam_NoReDoS, TestS8_H847_RegexParamReDoS, dos TestReDoSRegexParams, FPE TestRegexReDoSBudget PASS (run 2026-09-26, rmp #268 audit). | none (no defect) | #131 |
| TM-2026-042 | threat-modeler hypothesis (S9 catalogue); assigned - | rmp #132 sev 1 | Transposition: Spring CVE-2022-22963 SpEL injection | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED ("N/A - no expr eval"). Verified at HEAD: no template or expression evaluator in non-test code; JSON decoding only into fixed internal types (jwt_auth.go, oauth2.go). | none (not applicable) | #132 |
| TM-2026-043 | threat-modeler hypothesis (S9 catalogue); assigned - | rmp #133 sev 1 | Transposition: Rails strong-params type confusion | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S9: REFUTED ("N/A - no ORM"). Verified at HEAD: no request-body binding API; JSON decoding only into fixed internal structs. | none (not applicable) | #133 |
| TM-2026-044 | threat-modeler hypothesis (S9 catalogue); assigned MSR | rmp #134 sev 4; S9 posture label: "sev 4 - needs MSR audit" | Transposition: Gin trusted-proxies-all default vs RealIP | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Accepted | S10: CONFIRMED -> doc fix. S10-PreMSR TestSec_RealIP_NoCIDR_AcceptsXFF_FromAnyPeer / _SlogWarnEmitted / _XRealIPAlsoAccepted PASS (run 2026-09-26, rmp #268 audit). Default retained; construction slog.Warn (real_ip.go:46), SECURITY.md "Operator-facing defaults requiring opt-in" row, README "Security defaults". Same class as MSR-2026-0055. Proposed hard-fail (AllowTrustAllPeers) does not exist. | b390495 (warning, via MSR-2026-0055); e9fd648 (README callout) | #134, #40 |
| TM-2026-045 | threat-modeler hypothesis (S9 catalogue); assigned CSA | rmp #135 sev 3; S9 posture label: "sev 3 - covered by CL-AUTH-1" | Transposition: Echo group-inheritance bug (middleware lost on nested group) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-05-08-maturity-assessment.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10: REFUTED (CL-AUTH-1). S10-PreCSA s10_cl_auth1_test.go TestTM045_EchoStyle_SubGroup_HandleFast_PanicWhenGroupUse / TestTM045_SubGroup_NoParentMW_HandleFast_NoPanic PASS -race; in-tree TestRegression_TM_2026_036 PASS (run 2026-09-26, rmp #268 audit). | none (no defect) | #135 |
| TM-2026-046 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #136 sev 3; S9 posture label: "sev 3" | Transposition: fasthttp HEAD method handling divergence | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Not-applicable (rmp #286) | rmp #286 (2026-09-26): Premise false: MuxMaster has no implicit HEAD. HEAD on a GET-only route -> 405 with Allow listing GET and not HEAD, and the GET handler never runs. HEAD is served only by explicitly registered handlers (including the one ServeFiles registers, mux.go:1107); over a real HTTP/1.1 connection read to EOF, an explicit HEAD handler that writes a body and ServeFiles HEAD both put zero body bytes on the wire (net/http suppresses HEAD bodies); control GET through the same reader returns the file body. No fasthttp-style GET/HEAD divergence can arise. Tests: reports/http-protocol-security-auditor/harness/tm_reclass_s20_test.go::TestHPS_TM_2026_046_HEADOnGETOnlyRoute; reports/path-routing-fuzzer/harness/prerelease_v100_test.go::TestTM046_HEADGETDivergence (existing; logs only for the GET-only case). Correction: final-maturity-verdict §4.2 recorded 'TM-046 CONFIRMED-DOCUMENTED (idem httprouter/chi)'. Wrong on both counts: the premise is false, and httprouter/chi DO serve HEAD from GET while MuxMaster does not. No document states it. Reclassify Not-applicable. Previous: S10 final verdict recorded "CONFIRMED-DOCUMENTED"; #268 audit: premise false - there is no implicit HEAD; HEAD on a GET-only route returns 405 (Allow: GET, OPTIONS), same as httprouter and chi. prerelease_v100_test.go TestTM046_HEADGETDivergence PASS (run 2026-09-26, rmp #268 audit). No document states that GET does not imply HEAD (rmp #288). Reclassification in rmp #286. | none (no defect) | #136, #286, #288 |
| TM-2026-047 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #137 sev 4; S9 posture label: "sev 4" | Transposition: Apache CVE-2021-41773 path normalisation | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10: REFUTED (CL-PATH-1). prerelease_v100_test.go TestTM047_ApacheCVE20211773 PASS (run 2026-09-26, rmp #268 audit). ServeFiles with UseRawPath=true alone not covered by a repo test; audit probe on a real TCP server: encoded traversal payloads -> 404. | none (no defect) | #137 |
| TM-2026-048 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #138 sev 5; S9 posture label: "sev 5 - needs net/http2 verify" | Transposition: HTTP/2 CONTINUATION flood | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | No classification under TM-2026-048 existed (S9 UNTESTED; absent from S10). h2harness/h2_attack_test.go TestH2_ContinuationFlood_CVE202427316 PASS; govulncheck "No vulnerabilities found" (go1.27.0) (run 2026-09-26, rmp #268 audit); CI runs govulncheck. CVE mislabelled: CVE-2024-27316 is Apache httpd; Go net/http2 is CVE-2023-45288 (GO-2024-2687) - correction in rmp #290. | none (stdlib) | #138, #290 |
| TM-2026-049 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #139 sev 3; S9 posture label: "sev 3" | HPACK Huffman dynamic-table over-allocation | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | No classification under TM-2026-049 existed (S9 UNTESTED; absent from S10). h2harness TestH2_HPACKBombing PASS; govulncheck clean (run 2026-09-26, rmp #268 audit). | none (stdlib) | #139 |
| TM-2026-050 | threat-modeler hypothesis (S9 catalogue); assigned HPS | rmp #140 sev 3 | h2c upgrade smuggling (CVE-2023-39323 class) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Verified-safe | S9: PARTIAL (HPS-2026-EXT: h2c silently ignored). hps_extended_2026_test.go TestHPSExt16_H2CUpgradeRejected PASS -race (no 101 Switching Protocols) (run 2026-09-26, rmp #268 audit). Deliverable not done: no h2c note in SECURITY.md, README, docs/ or specification/ (rmp #287). | none | #140, #287 |
| TM-2026-051 | threat-modeler hypothesis (S9 catalogue); assigned PRF | rmp #141 sev 3; S9 posture label: "sev 3" | Transposition: Werkzeug CVE-2020-28724 URL parsing ambiguity | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-05-07-sprint-S9.md, reports/overview/2026-05-08-final-maturity-verdict.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Refuted | S10: REFUTED (CL-PATH-1). prerelease_v100_test.go TestTM051_WerkzeugCVE20200628 PASS (run 2026-09-26, rmp #268 audit); the router never parses Host. | none (no defect) | #141 |
| TSC-2026-0011 | timing-and-sidechannel-analyst (TSC) | sev 3 (S9 posture; rmp #153 sev 3) | JWT HS256 vs ES256 algorithm-path timing difference (320 ns recorded) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #287 | reports/timing-and-sidechannel-analyst/harness/jwt_alg_confusion_timing_test.go TestTiming_JWT_HS256vsES256_PathLatency PASS: mean diff 0.08 us (N=200k per arm, go1.27.0, Ryzen 9 5900HX) (run 2026-09-26, rmp #268 audit). Mixed-family slog.Warn exists (jwt_auth.go, from TSC-2026-0003 fix fb77b7e). AC "document in SECURITY.md alongside TSC-2026-0003" not done (rmp #153 audit verdict unmet). | none | #153, #287 |
| TSC-2026-0012 | timing-and-sidechannel-analyst (TSC) | sev 3 (S9 posture; rmp #154 sev 3) | JWT alg=none vs HS256 pre-crypto short-circuit timing (925 ns recorded) | reports/overview/2026-05-07-posture-S9.md, reports/overview/2026-09-26-closed-task-audit.md (untracked; rmp #268) | Open — rmp #287 | TestTiming_JWT_AlgNoneVsHS256 PASS (timing tag suite) (run 2026-09-26, rmp #268 audit): measured mean diff 226 ns with alg=none SLOWER than HS256 wrong-signature (6452 vs 6257 ns) - the recorded direction did not reproduce. AC "document as informational in SECURITY.md" not done (rmp #154 audit verdict unmet). | none | #154, #287 |

Totals: Refuted 23, Fixed 19, Open 16, Accepted 13, Not-distinct 8, Verified-safe 7.

---

## Append rules

Sole owner: `threat-modeler-and-zero-day-researcher`. Specialists report in `reports/<agent>/` and return findings to the owner.

1. A new finding keeps the issuing agent's ID (`<PREFIX>-2026-NNNN`); never renumber it and never match IDs on a normalised number (B.5).
2. Add it as a row to a Part B table (or a new Part for a new sprint), with severity, CWE when known, file:line, "Documented in", status and fix commit.
3. A composite gets a `CDX-YYYY-NNN` ID and lists its components.
4. Never edit a historical status. Record a change of state as a dated "Current status" note citing verified evidence.
5. Every `Dismissed` / `Not-distinct` entry needs a written justification (B.3).
