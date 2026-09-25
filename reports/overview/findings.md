# MuxMaster — Findings Ledger

Owner: `threat-modeler-and-zero-day-researcher` (sole author of `reports/overview/`)
Last updated: 2026-09-25 — historical ledger restored and merged with the reconciliation ledger, rmp task #267 (sprint 20 "Backlog clearance")
Repository state at merge: commit `a510565`, branch `feature/20-backlog-clearance`

History of this file:
- 2026-04-17/18 — pre-release v1.0.0 sprint ledger (`MM-2026-0001..0047` + `MM-TM-2026-0001..0005`), audited commit `533d0c9`, fixes in `723b3be` (phases 1-3) and `3371932` (phases 4-6), phase 7 fixes afterwards. Now Part A.
- 2026-05-08 — deleted by commit `5f804fa` (see §0).
- 2026-09-25 — rmp #240 reconciliation ledger (34 entries), amended by rmp #263, #264, #265, #266 and #270. Now Part B.
- 2026-09-25 — rmp #267: Part A restored from `5f804fa^` and merged with Part B.

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
| O-7 | MM-2026-0051/0052/0053 acceptance criteria cite `v2_new_findings_test.go::TestSec_*_V2_001`, which does not exist. Re-checked 2026-09-25 (rmp #267): the file exists in no commit of any branch. **Open** — part of the closed-task audit (H-RECON-03, rmp #268). | rmp #4, #5, #6 |
| O-8 | ~~Overview canonical documents deleted in `5f804fa`~~ — **Resolved (rmp #267):** `findings.md` (this file, Part A), `threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md`, `2026-04-17-posture.md`, `2026-04-17-sprint.md`, `reports/README.md`, the seven 2026-04-17 per-agent reports and `go-sast-and-memory-auditor/semgrep-rules/muxmaster.yml` restored from `5f804fa^`. Not restored by maintainer decision: `reports/.gitignore` (it ignored `evidence/` and `*.pprof`, contradicting the current practice of versioning evidence), ~2 070 corpus files and 60 harness `.go` files (see O-14). | §0 |
| O-9 | ~~`TestTiming_APIKey_HitVsMiss`/`TestTiming_BasicAuth_ValidVsInvalid`/`TestTiming_BasicAuth_UserExistsVsNotExists` fail on any statistically significant difference, conflicting with SECURITY.md's "Accepted Timing Oracles" doctrine for TSC-2026-0001/0002/0004~~ — **Resolved (rmp #270):** each now asserts against an explicit accepted bound (2000 ns / 700 ns / 2500 ns) derived from measured evidence and documented in SECURITY.md next to each TSC entry. All 3 pass, 3× triplicated + once in the full 19-test suite. No library code changed. | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; `evidence/2026-09-25/o9_bound_runs_1-3.log`, `full_suite_o270.log` |
| O-10 | ~~SECURITY.md's "Route-Existence Timing Oracle (MM-2026-0026)" prose ("~440 ns", mislabelled "404 vs 405") does not match the TSC-2026-0005 entry ("923 ns", registered-vs-unregistered) from the same harness~~ — **Resolved (rmp #270):** both sections now cite the same current figure (~960 ns, 4 independent runs) for the same, correctly described pair (registered/200 vs unregistered/404). | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; SECURITY.md "Route-Existence Timing Oracle (MM-2026-0026)" and TSC-2026-0005 |
| O-11 | Findings from sprints S7–S10 (2026-05-07/08) have no ledger row. 86 finding identifiers cited in `2026-05-07-posture*.md`, `2026-05-07-sprint*.md` and `2026-05-08-*.md` appear nowhere in this file: 50 `TM-2026-002..051`, 7 `MSR-2026-*`, 7 `DOS-2026-*`, 6 `CSA-2026-*`, 6 `PRF-2026-*`, 4 `HPS-2026-0001..0004`, 2 `MM-2026-0048/0049`, 2 `FPE-2026-007/010`, 2 `TSC-2026-0011/0012`. `2026-05-07-posture-S8.md` lists "`findings.md` (S8 section appended)" as an output, but the file deleted in `5f804fa` has no S8 section (last update 2026-04-18), so that claim is false. **Open** — planned with the closed-task audit (rmp #268). | `git show 5f804fa^:reports/overview/findings.md`; posture-S8 outputs list |
| O-12 | ~~Most reproducer and evidence paths cited in Part A were never committed: the deleted `reports/.gitignore` excluded `evidence/` and `*/evidence/`.~~ — **Accepted (rmp #267):** the files cannot be recovered from git. Only the CSA-001..004 reproducers, HPS `harness/repro_test.go`, SAST `harness/h018_ctx_field_type_test.go` and the path-routing-fuzzer `shadow_*` harnesses were in git (all deleted in `5f804fa`, readable with `git show 5f804fa^:<path>`). The Part A citations stay as historical record; each fixed Part A entry with a "Current status" note names the current regression tests that replace the lost reproducers. | `git show 5f804fa^:reports/.gitignore` |
| O-13 | The restored 2026-04-17 overview documents (`threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md`, `2026-04-17-*.md`) are written wholly or partly in Portuguese, contrary to CLAUDE.md §13.2. Part A of this file was translated during the merge; the others were restored verbatim. **Open** — planned: rmp #275. | restored files |
| O-14 | Coverage lost in `5f804fa` and not restored. (1) 60 harness `.go` files were deleted; the per-file assessment is in the rmp #267 task log. Properties with **no current equivalent**: compress removes a stale `Content-Length` after compressing (no test; code at `middleware/compress.go:178`); CORS wildcard at a non-first index + `AllowCredentials` panics; CORS request without `Origin` passes through with no ACAO; Logger writer error does not panic or alter the response; Logger concurrent writes stay line-atomic; NoCache lets the handler override `Cache-Control`; Recoverer survives a panic value whose `String()` panics; a Recoverer placed inside does not catch an outer panic (MM-2026-0034 negative); `ThrottleBacklog` configuration validation panics (limit ≤ 0, backlog < 0); nested `Timeout` picks the shortest deadline; RealIP XFF-over-X-Real-IP precedence; `SetHeader("", v)` behaviour; `WithValue(k, nil)`; 13 of the 20 raw-TCP `smuggle_test.go` variants (TE chunked+chunked, TE tab before value, TE mixed case, obs-fold, bare LF line terminator, bare CR in headers, NUL in header value, NUL and CRLF in request target, duplicate `Content-Length` same and different, oversize method, oversize target); `OPTIONS *` handling; parent-context cancellation reaching a param-route handler's `ctx.Done()`; throttle load-level timing oracle; fuzz-driven differential against httprouter/chi/bunrouter; wire-level XFF CRLF admission. (2) `5f804fa` also removed about 60 `Test*` functions from harness files it rewrote (e.g. `TestTiming_H002b_PasswordLengthOracle` — the MM-2026-0020 regression test —, `TestErrorOracleMatrix`, `TestTiming_RedirectFixedPath_Oracle`, 11 `TestTiered_*`, `TestProp_HandleIdempotencyAndConflict`, `TestProp_ServeFilesRegistersTwoRoutes`, `TestMiddlewareInteractionMatrix`); (2) has not been assessed. **Middleware part resolved (rmp #273):** the 13 middleware properties above plus 7 more found while assessing the 81 functions removed from the MSR harness now have tests in `middleware/gap_o14_test.go` (23 `TestSec_*`); ~67 removed functions have equivalents under new names (mostly `middleware-security-reviewer/harness/security_harness_test.go`); no defect in the restored properties. One discrepancy found: Recoverer appends its error text after a partial response instead of writing nothing (`specification/middleware-stdlib.md` item 14) — planned as its own task. **Rest open** — planned: rmp #272 (HTTP protocol), #274 (routing, concurrency, timing). | rmp #267 task log; `git show 5f804fa^:<path>`; `git diff 5f804fa^ 5f804fa -- '*.go'` |

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

**H-RECON-03 — closed-with-unmet-acceptance** (owner: threat-modeler, Medium). **Open** (planned: rmp #268). The rmp #240 reconciliation found 4 of the 34 closed tasks failing their acceptance criteria in the repository: #151, #166, #176, #60. None of the 35 originating tasks records a completion summary, and many were closed milliseconds after being started. Test: check every closed security task's acceptance criteria against the repository.

State of the 4 known cases, verified 2026-09-25 at `a510565` (rmp #267):
- **#151** (TSC-2026-0009) — **met** after rmp #264: `error_oracle_test.go::buildErrorOracleMux` registers BasicAuth on a `Group`; the 404/405 distinction is measured with valid arms; accepted oracles are documented in `SECURITY.md`.
- **#166** (DOS-2026-0051) — **met** after rmp #266 through the AC's documentation branch: `SECURITY.md` "Startup-time route registration cost (DOS-2026-0051)".
- **#176** (FPE-2026-005) — **met** after rmp #265 through the AC's second branch (engineering note in `invariants.md` I-15, tolerance-bounded property I-15b). Caveat: the first branch ("passes consistently under `-count=1000` on all GOOS") was never exercised; the evidence is `-count=20` on linux/amd64.
- **#60** (CDX-2026-005) — **met** after rmp #266: `SECURITY.md` "Composition of timing oracles (CDX-2026-005)" enumerates the three oracles and four deployment postures.

The hypothesis stays open because its test — checking every closed security task, not only these 4 — has not been run. The 4 rmp tasks still have `completion_summary = null`.

---

## Append rules

Sole owner: `threat-modeler-and-zero-day-researcher`. Specialists report in `reports/<agent>/` and return findings to the owner.

1. A new finding keeps the issuing agent's ID (`<PREFIX>-2026-NNNN`); never renumber it and never match IDs on a normalised number (B.5).
2. Add it as a row to a Part B table (or a new Part for a new sprint), with severity, CWE when known, file:line, "Documented in", status and fix commit.
3. A composite gets a `CDX-YYYY-NNN` ID and lists its components.
4. Never edit a historical status. Record a change of state as a dated "Current status" note citing verified evidence.
5. Every `Dismissed` / `Not-distinct` entry needs a written justification (B.3).
