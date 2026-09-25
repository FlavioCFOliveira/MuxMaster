# MuxMaster — Findings Ledger

Owner: `threat-modeler-and-zero-day-researcher` (sole author of `reports/overview/`)
Last updated: 2026-09-25 — findings reconciliation, rmp task #240 (sprint 20 "Backlog clearance")
Repository state at reconciliation: commit `b038632`, branch `feature/20-backlog-clearance`
Amended 2026-09-25 by `fuzzing-and-property-engineer` (rmp task #265, at commit `b1986ba`): closed O-2, O-5, O-6 and updated the FPE-2026-005/FPE-2026-006 rows above — see each entry for evidence. No other section touched.

## 0. Scope and provenance — read first

The canonical ledger that lived at this path (515 lines, last updated 2026-04-18, entries `MM-2026-0001..0047` + 5 `MM-TM` composites) was **deleted** in commit `5f804fa` ("docs(reports): consolidate Sprint S9 security audit artifacts"), together with `threat-model.md`, `hypotheses.md`, `attack-trees.md`, `system-model.md`, `transposition.md` and the 2026-04-17 per-agent reports. The commit message describes these files as added, but the diff removes them. `2026-05-07-posture-S9.md` §9 still points readers to this file. The deleted ledger can be read with `git show 5f804fa^:reports/overview/findings.md`.

**This file currently holds only the 34 findings reconciled by rmp task #240.** These are the Finding nodes of the knowledge graph whose identifier appears in an rmp task title but was not linked to any report. Rebuilding the full historical ledger is outside this task's scope and is waiting for a maintainer decision.

## 1. Conventions

- **Severity** = rmp `severity` (0–10): 9–10 Critical, 7–8 High, 4–6 Medium, 1–3 Low, 0 Info.
- **Status**: `Fixed`, `Verified-safe` (no defect: a hypothesis was refuted, or the result is a regression-guard PASS), `Accepted` (a documented trade-off), `Open`, or `Not-distinct` (bucket (c); each has a justification in §3). The flag `AC-unmet` means the rmp task was closed although its acceptance criteria are not satisfied in the repository (§4).
- **Bucket**: (a) documented under another name, or under the same ID in a file an `.md`-only search missed; (b) never documented, now written up; (c) not a distinct finding.
- Paths in "Documented in" are relative to `reports/`, except `SECURITY.md` and `docs/` (repository root). `harness/…` and `evidence/…` refer to the directory of the agent named in the same row.

## 2. Reconciled entries (34)

| ID | Agent | Sev | Title | Bucket | Documented in (alias) | Status | Fix commit |
|---|---|---|---|---|---|---|---|
| CDX-2026-001 | threat-modeler (composite) | 9 | HandleFast composite: Use auth skip + Recoverer skip + raw-path log injection | a | `overview/2026-05-07-posture.md` (CDX-1) | Fixed via CSA-2026-0054 / CSA-2026-0053 / MSR-2026-0057 | not traced |
| CDX-2026-002 | threat-modeler (composite) | 9 | Tree corruption composite: UTF-8 OOB panic + partial tree + 2^N expansion | a | `overview/2026-05-07-posture.md` (CDX-2) | Fixed via PRF-2026-0009 / MM-2026-0033 / MM-2026-0050 (cap 8 in `786cf9f`) | partially traced |
| CDX-2026-003 | threat-modeler (composite) | 8 | RealIP + Throttle ordering composite, no safe default | a | `overview/2026-05-07-posture.md` (CDX-3); `SECURITY.md` | Accepted (construction-time `slog.Warn` + docs) | not traced |
| CDX-2026-004 | threat-modeler (composite) | 8 | Path-decode composite: %2520 + raw catch-all + %61dmin | a | `overview/2026-05-07-posture.md` (CDX-4); `SECURITY.md` | Fixed (`url.PathUnescape`) + documented | not traced |
| CDX-2026-005 | threat-modeler (composite) | 6 | Timing-oracle composition: JWT alg + OAuth2 cache + route existence | a | `overview/2026-05-07-posture.md` (CDX-5); `SECURITY.md` (oracles listed individually) | Accepted — **AC-unmet** | — |
| CSA-2026-0056 | concurrency | 0 | Pool/GC canary: no cross-request param contamination | b | `concurrency-security-auditor/2026-09-25-CSA-2026-0056-pool-gc-canary.md` | Verified-safe (default mode only; see H-RECON-01) | — |
| CSA-2026-0057 | concurrency | 0 | Introspection vs concurrent ServeHTTP: race-free | a | `overview/2026-05-07-posture.md` STRIDE (CSA-57); `harness/s8_hypotheses_test.go`; `harness/h027_introspection_race_test.go` | Verified-safe | — |
| DOS-2026-0051 | dos | 3 | `addRoute` O(N^2) registration cost | a | `dos-resilience-tester/harness/dos_v2_test.go` (same ID) | Accepted — **AC-unmet** (no SECURITY.md note) | — |
| DOS-2026-0061 | dos | 2 | ThrottlePerIP timeout refs decrement under surge | c | `dos-resilience-tester/harness/s9_dos_test.go` | Not-distinct (re-validates MSR-2026-0068) | — |
| DOS-2026-0063 | dos | 1 | Large method-name dispatch is O(1) | a | `dos-resilience-tester/harness/s9_dos_test.go` (same ID) | Verified-safe | — |
| FPE-2026-0001 | fuzzing | 3 | Regex `{name:expr}` parser stops at the first `}` | a | `fuzzing-and-property-engineer/invariants.md` I-REGEX-01 + `evidence/2026-05-07/CRASH-REGEX-01` (FPE-2026-REGEX-01); `overview/2026-05-07-posture-S8.md` (#83) | Fixed (`tree.go:1255`) | `825c623` |
| FPE-2026-001 | fuzzing | 2 | Unnamed wildcard panics without `muxmaster:` prefix | a | `fuzzing-and-property-engineer/evidence/2026-05-07/CRASH-FPE-001/repro_test.go` (same ID) | Fixed | `dc775ca` |
| FPE-2026-002 | fuzzing | 5 | Mount invalid-UTF-8 prefix panics and leaks `*mux_mount` | a | `overview/2026-05-07-posture.md` STRIDE (FPE-2); `evidence/2026-05-07/CRASH-FPE-002` | Fixed | `7512e98` |
| FPE-2026-004 | fuzzing | 4 | Walk/WalkFast on a post-panic tree: no UB | a | `fuzzing-and-property-engineer/invariants.md` (I-WALK-01..04); `harness/fuzz_walk_corrupted_test.go` | Verified-safe | — |
| FPE-2026-005 | fuzzing | 3 | Timeout context not cancelled within 1 ms of the deadline | b | `fuzzing-and-property-engineer/2026-09-25-FPE-2026-005-timeout-cancellation-property.md` | **Resolved (rmp #265):** reconstructed `TestProp_TimeoutCancelsContext` (I-15b) blocks on `ctx.Done()` and compares the observed fire time against `ctx.Deadline()` with a measured, justified 300ms tolerance (never before the deadline, unconditionally). Root cause was a test-tolerance defect in the lost original property (1ms margin between `sleepMs` and `timeoutMs`), not a `middleware/timeout.go` defect — no library code changed. `go test -race -count=20`: 20/20 pass, 2000 total iterations, 0 failures. | — |
| FPE-2026-006 | fuzzing | 4 | Fuzz coverage gap on 8 public surfaces | a | `fuzzing-and-property-engineer/harness/fuzz_s9_new_surfaces_test.go` (same ID); `invariants.md` S9 + Sprint 20 sections | **Fixed (rmp #265):** `FuzzServeFiles` + `TestProp_ServeFilesNoEscape` added (I-SERVEFILES-01/02), closing the last of the 8 named surfaces. 36s run, 658K execs, 0 crashes, 121 corpus entries persisted to `corpora/FuzzServeFiles/`. | not traced |
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

## 3. Not-distinct entries — justification

**DOS-2026-0061 → MSR-2026-0068.** rmp #172 calls this "regression guard for MSR-2026-0068 fix". `TestThrottlePerIPTimeoutRefsCleanup` (`s9_dos_test.go:642-728`) checks that the timeout branch decrements `refs` so entries drain. It passed and found no new defect. The fixed defect is MSR-2026-0068, cited in SECURITY.md ("ThrottlePerIPCapped saturation").

**TSC-2026-0010 (no ID to duplicate).** rmp #152 is a "PRNG re-audit at HEAD" with verdict "PASS — no action required". It repeats §7 of the 2026-04-17 timing audit (crypto/rand, 100 000 IDs, 0 collisions), which never had a finding ID. That report was deleted in `5f804fa`; read it with `git show 5f804fa^:reports/timing-and-sidechannel-analyst/2026-04-17-1320-prerelease-timing-audit.md`. The Sprint 18 request_id buffer pool changed the code after the audit, so the harness was re-run at `b038632`: all `TestPRNG_RequestID_*` pass (chi-square 11.39 < 37.70).

**TSC-REVAL-2026-001..007 → TSC-2026-0001..0007.** Each is an S9 re-measurement, at `e30ae94`, of an oracle already accepted in SECURITY.md ("Accepted Timing Oracles (TSC-2026-0001..0007)"). None produced a new defect under its own label:
- 001, 002, 003, 005 and 006 each closed "within accepted envelope / no code change".
- 004 found a widened delta caused by `WWW-Authenticate` being set only on the miss path. That cause was promoted to TSC-2026-0008 (posture-S9; fixed in `825c623`).
- 007 was not measured; it is a deferral record.

posture-S9 refers to the batch only in aggregate ("5 + 7 revals").

## 4. Open items found during reconciliation (not acted on)

| # | Item | Evidence |
|---|---|---|
| O-1 | ~~TSC-2026-0009 harness: `TestTiming_ErrorOracle_404vs405` gets 401 for both arms (BasicAuth registered via `Use`) and passes silently~~ — **Resolved (rmp #264):** `buildErrorOracleMux` now registers BasicAuth on a `Group`, not the root `Mux`; every `TestTiming_*` verifies each arm's status before and during measurement. See `timing-and-sidechannel-analyst/2026-09-25-TSC-2026-0009-error-oracle.md` | Re-run at `b038632`; TSC-2026-0009 write-up (updated 2026-09-25) |
| O-2 | ~~FPE-2026-005: cancellation property replaced by a deadline-presence property; no engineering note in `invariants.md` I-15~~ — **Resolved (rmp #265):** `invariants.md` I-15 now carries the engineering note, and a new I-15b documents the reconstructed `TestProp_TimeoutCancelsContext` property (measured 300ms tolerance, never-before-deadline asserted unconditionally). `go test -race -count=20`: 20/20 pass. No `middleware/timeout.go` code change — the original failure was a test-tolerance defect, not a library defect. | FPE-2026-005 write-up §"Resolution (2026-09-25, rmp #265)"; `invariants.md` I-15/I-15b; `harness/properties_test.go` |
| O-3 | DOS-2026-0051: no startup-time registration-cost note in SECURITY.md (rmp numbers N=2000 → 1.4 s, N=5000 → ~5 s; not re-measured) | rmp #166 |
| O-4 | CDX-2026-005: SECURITY.md lists the three oracles individually, not their composition or the deployment posture | SECURITY.md "Accepted Timing Oracles" |
| O-5 | ~~`invariants.md` I-REGEX-01 still calls the `}` defect a "known limitation" although `tree.go:1255` fixes it~~ — **Resolved (rmp #265):** I-REGEX-01 now reads "Fixed", cites commit `825c623`. The claim is backed by code, not just prose: `FuzzRegexParamRegistration`'s stale "known limitation: document, do not fail" swallow was removed (it now asserts success for `}`-containing valid regexes, same as any other valid regex), and a new deterministic regression guard `TestRegexBraceFixed` registers+dispatches 4 distinct `}`-containing regexes. `FuzzRegexParamRegistration`: 20s, 409K execs, 0 crashes. | `invariants.md` I-REGEX-01; `harness/fuzz_regex_param_test.go` (`FuzzRegexParamRegistration`, `TestRegexBraceFixed`) |
| O-6 | ~~FPE-2026-006: ServeFiles has no fuzz or property target~~ — **Resolved (rmp #265):** `FuzzServeFiles` (36s, 658K execs, 0 crashes) + `TestProp_ServeFilesNoEscape` (rapid, 100 runs, 0 failures) added; new invariants I-SERVEFILES-01/02. Confirms `http.FileServer`'s `path.Clean` protection holds under fuzzing for the default (`UseRawPath=false`) config; symlink-follow (an inherited, documented `net/http.Dir` characteristic, not a MuxMaster gap) is pinned separately by `TestServeFiles_SymlinkFollowsUpstreamBehavior` so it is never conflated with a regression. | `fuzz_s9_new_surfaces_test.go`; `harness/fuzz_servefiles_test.go`; `invariants.md` Sprint 20 section |
| O-7 | MM-2026-0051/0052/0053 acceptance criteria cite `v2_new_findings_test.go::TestSec_*_V2_001`, which does not exist | rmp #4, #5, #6 |
| O-8 | Overview canonical documents deleted in `5f804fa` | §0 |
| O-9 | ~~`TestTiming_APIKey_HitVsMiss`/`TestTiming_BasicAuth_ValidVsInvalid`/`TestTiming_BasicAuth_UserExistsVsNotExists` fail on any statistically significant difference, conflicting with SECURITY.md's "Accepted Timing Oracles" doctrine for TSC-2026-0001/0002/0004~~ — **Resolved (rmp #270):** each now asserts against an explicit accepted bound (2000 ns / 700 ns / 2500 ns) derived from measured evidence and documented in SECURITY.md next to each TSC entry. All 3 pass, 3× triplicated + once in the full 19-test suite. No library code changed. | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; `evidence/2026-09-25/o9_bound_runs_1-3.log`, `full_suite_o270.log` |
| O-10 | ~~SECURITY.md's "Route-Existence Timing Oracle (MM-2026-0026)" prose ("~440 ns", mislabelled "404 vs 405") does not match the TSC-2026-0005 entry ("923 ns", registered-vs-unregistered) from the same harness~~ — **Resolved (rmp #270):** both sections now cite the same current figure (~960 ns, 4 independent runs) for the same, correctly-described pair (registered/200 vs unregistered/404). | TSC-2026-0009 write-up §"O-9 and O-10 — resolved"; SECURITY.md "Route-Existence Timing Oracle (MM-2026-0026)" and TSC-2026-0005 |

## 5. Identifier-namespace collisions

| ID | Collision | Recommendation |
|---|---|---|
| FPE-2026-001 | rmp #38 (wildcard panic) and rmp #173 (FuzzCORS corsEmpty harness fix after MSR-2026-0070) share the ID | Keep #38; detach #173 (harness maintenance, `fuzz_cors_test.go:32-37`) |
| FPE-2026-0001 / FPE-2026-001 | Different findings (regex `}` vs unnamed wildcard) | Never merge on a normalised ID |
| FPE-2026-002 / FPE-2026-0002 | Mount invalid UTF-8 vs `sanitiseForLog` non-idempotency (sprint-S9, `middleware/logger.go:112`) | Distinct nodes |
| HPS-2026-0005..0010 | In HPS harnesses, 0006 also labels a CONTINUATION-flood check (`h2_attack_test.go:342-362`) and a HandleFast guard (`http_protocol_audit_test.go:391`); 0007..0010 are each used twice; 0005 also labels Rapid Reset/HPACK checks | No REPORTED_IN edges from a plain string match in HPS harnesses |

## 6. Hypotheses raised by the reconciliation

**H-RECON-01 — contamination under the opt-in pooled modes** (owner: concurrency-security-auditor, High). **Resolved — refuted (rmp #263):** `pool_contamination_test.go` runs concurrent canaries (64 goroutines × 3000 requests, 1/2/3/overflow/catch-all tiers) and panic-path checks with `PoolRequestBundle` and `PoolFastParams` enabled, separately and together, under `-race`: no cross-request contamination and no dirty object returned to a pool. Two deterministic tests pin the documented retention hazard. See `concurrency-security-auditor/2026-09-25-H-RECON-01-pooled-mode-canaries.md`. Original hypothesis: CSA-2026-0056 covers only the GC-managed default. `PoolRequestBundle` (O13) and `PoolFastParams` (O9) recycle storage across requests by design, and no test runs a contamination canary with either flag set; they appear only in benchmarks. Test: run the `TestPool_*` canaries with each flag on, under `-race`, plus a handler that retains `r` past return.

**H-RECON-02 — silent-pass timing harnesses** (owner: timing-and-sidechannel-analyst, Medium). **Resolved (rmp #264):** all 19 `TestTiming_*` functions across the 6 timing-harness files now call `VerifyArmStatus` (new helper in `harness/timing.go`) once per arm before the sample loop, and re-check the status on every sample inside the loop, `t.Fatalf`-ing on any mismatch. Demonstrated live: deliberately setting the wrong expected status for the "405" arm made `TestTiming_ErrorOracle_404vs405` fail immediately at the preflight step. See `timing-and-sidechannel-analyst/2026-09-25-TSC-2026-0009-error-oracle.md` for the fix, the re-measured 404-vs-405/404-vs-401 figures (triplicated), and a new open item (O-9) about 3 unrelated, pre-existing `t.Errorf`-on-any-significance failures in the BasicAuth/APIKey constant-time tests that this task did not touch. Original hypothesis: TSC-2026-0009 shows a harness that logs, rather than fails on, a wrong status code. Test: every `TestTiming_*` asserts that the status codes of both arms differ as intended before computing statistics. Every accepted oracle in SECURITY.md depends on these harnesses.

**H-RECON-03 — closed-with-unmet-acceptance** (owner: threat-modeler, Medium). 4 of the 34 closed tasks (#151, #166, #176, #60) fail their acceptance criteria in the repository. None of the 35 originating tasks records a completion summary, and many were closed milliseconds after being started. Test: check every closed security task's acceptance criteria against the repository.
