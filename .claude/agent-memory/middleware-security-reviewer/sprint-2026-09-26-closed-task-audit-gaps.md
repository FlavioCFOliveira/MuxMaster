---
name: sprint-2026-09-26-closed-task-audit-gaps
description: rmp #284/#285 gap-closure — TM-040 Pre ordering hazard confirmed, 5 harness/unit test defects fixed (MM-2026-0051/0052, MSR-2026-0059/0060/0061), CDX-2026-003 "detect RealIP" AC ruled unimplementable
metadata:
  type: project
---

Follow-up to [[sprint-2026-05-08-S10-PreMSR]]. Source: `reports/overview/2026-09-26-closed-task-audit.md`
(rmp #268, H-RECON-03) found 51/183 closed security tasks with unmet acceptance criteria —
mostly promised tests that were only `t.Logf` (never failed on regression) or never written.
rmp #284 + #285 (grouped) closed the middleware-scoped subset. Branch
`feature/20-backlog-clearance`. No commit made (per task instructions) — work is uncommitted
at handoff.

## Files changed

- `reports/middleware-security-reviewer/harness/tm040_pre_ordering_test.go` (NEW) — TM-2026-040 / #284
- `reports/middleware-security-reviewer/harness/cdx2026003_test.go` (NEW) — CDX-2026-003 / #58
- `reports/middleware-security-reviewer/harness/security_harness_test.go` (EDITED) — #4, #44, #46
- `middleware/middleware_test.go` (EDITED) — #5, #45

All pass under `go test -race -count=3` (harness module needs `GOFLAGS=-mod=mod` — its go.mod
has a `replace` to `../../../` but no fresh go.sum entry for local edits). `go vet` and
`golangci-lint run` clean on both the root module and the harness module.

## Verdicts

| Item | Finding | Verdict | Key detail |
|---|---|---|---|
| #284/TM-040 | Pre(gate,CleanPath()) ordering | CONFIRMED bypass + fix verified | See below — also found a NEW nuance the task's own hypothesis got half-wrong |
| #4 | MM-2026-0051 | test added, behaviour already correct | Vary:[Origin,Accept-Encoding] both present when CORS+Compress composed, either order |
| #5 | MM-2026-0052 | test added, behaviour already correct | WWW-Authenticate present exactly once on BOTH missing-key and invalid-key 401s |
| #44 | MSR-2026-0059 | test FIXED (was Logf-only) | now asserts slog.Warn fires (2x) + CWE-1021 text, and that the collision actually happens |
| #45 | MSR-2026-0060 | test added, behaviour already correct | Vary:Accept-Encoding present even on <1024B (uncompressed) response |
| #46 | MSR-2026-0061 | test FIXED (was Logf-only) | now asserts RawPath=="" for %2e%2e traversal (was already correct at HEAD) |
| #58 | CDX-2026-003 | AC's "detect RealIP" ruled NOT specified/unimplementable; safe-composition test added instead | see below |

**Pattern across #4/#5/#45/#58's "keys correctly" half:** in every case the PRODUCTION CODE
was already correct at HEAD (per the S9/S10 fix waves) — the audit's gap was purely "no test
would fail if this regressed." None of these required a middleware.go code change.

## TM-040 detail (important nuance)

`Pre(gate, CleanPath())` (wrong order): gate runs on the RAW path first, so `/pub/../admin`,
`/pub/%2e%2e/admin`, `//admin` all miss gate's naive `strings.HasPrefix(path, "/admin")`
check (raw path doesn't literally start with `/admin`); CleanPath then normalises and dispatch
routes to the real `/admin` handler. **Confirmed 200 admin-secret for all three payloads.**

`Pre(CleanPath(), gate)` (correct order): CleanPath normalises first, gate sees `/admin` and
rejects. **Confirmed 403 for all three.**

The task's own hypothesis text said Use()-ordering is "irrelevant" because CleanPath via Use
runs after routing. **I verified this is half-true and had to correct my own test**: routing
(tree lookup on the raw path) is indeed decided before ANY Use-registered middleware runs, so
the real `/admin` handler's body is NEVER reached via Use() in either order — genuinely no
handler bypass. BUT MuxMaster's `Use()` middleware chain also wraps the shared `lazyNotFound`
handler (mux.go:1619), so gate/CleanPath still execute on the unmatched-route path — just
downstream of the routing decision instead of upstream. Result: `gate→CleanPath` order gives
404 (gate saw the raw non-matching path, let it fall through); `CleanPath→gate` order gives
403 (gate saw the cleaned "/admin" path and rejected it) for the exact same requests. Status
differs by order even though no bypass ever occurs. Recorded as
`TestTM040_UseOrdering_NoHandlerBypass` (renamed from the task's suggested
`_Irrelevant` once the 404-vs-403 asymmetry was measured).

**No SECURITY.md/docs.md edit was made** — the audit noted no doc states CleanPath must
precede path-inspecting Pre middleware; that's a documentation task, out of scope for this
subagent invocation (tests only). Should be flagged to whoever owns SECURITY.md next.

## CDX-2026-003 / #58 — "detect RealIP" ruled out

Read `findings.md` (CDX-2026-003 row) and `SECURITY.md` "RealIP + ThrottlePerIP ordering
(DOS-2026-0002)": the project ALREADY treats this as **Accepted (construction-time slog.Warn +
docs)** — not a runtime-detection requirement. Confirmed `throttle.go`'s `ThrottlePerIPCapped`
warns unconditionally whenever `keyFn==nil`, regardless of whether RealIP ran; there is no
reliable way to distinguish "RealIP ran and left RemoteAddr as the raw peer" from "RealIP
never registered" from inside ThrottlePerIP, and no cross-middleware registration-order
introspection API exists (Pre/Use chains are plain func values, no metadata). **Recommendation
to task owner: record rmp #58's "detect RealIP" clause as superseded by the existing
construction-time warning; do not implement.** Instead added
`TestSec_CDX_2026_003_RealIP_Before_ThrottlePerIP_KeysCorrectly` (distinct XFF clients behind
one trusted proxy get distinct buckets) and
`TestSec_CDX_2026_003_UntrustedPeer_XFFSpoof_DoesNotChangeKey` (an untrusted peer rotating its
spoofed XFF value never escapes its own real-peer-address bucket — proven by both key-identity
assertion and an actual 503 when a second spoofed identity from the same untrusted peer
contends for a limit=1 slot already held).

## Follow-up round (same session): coordinator-directed sweep

The coordinator asked me to fix the two items above (they were in #285's scope after all —
"convert log-only checks to assertions") plus grep the whole harness for the same anti-pattern.

- `TestSec_Compress_SmallResponseVaryAbsent` — REMOVED (redundant with
  `middleware/middleware_test.go::TestSec_Compress_SmallResponseVaryPresent`), replaced with a
  pointer comment at its former location in `security_harness_test.go`.
- `TestSec_CleanPath_PercentEncodedDotTraversal_RawPathBypass` — RENAMED to
  `..._RawPathZeroed` and converted to `t.Errorf` assertions (both the decoded-Path-cleaned and
  RawPath-zeroed properties). Kept (not removed) since it also covers the decoded-Path side in
  the same request, distinct from its sibling `TestSec_CleanPath_RawPathWithTraversalZeroed`.

**Grep sweep result:** found 5 MORE instances of the exact same pattern, all in
`sprint_s8_test.go` — an S8 (pre-fix) battery whose findings were later fixed, with a proper
asserting regression test already added to `sprint_s9_test.go`, but the ORIGINAL S8 Logf-only
test was never removed, so two versions of the same finding co-existed: one silently always
"passing", one actually asserting.

| Removed (sprint_s8_test.go) | Superseded by (sprint_s9_test.go) |
|---|---|
| `TestSec_RealIP_MultiHop_LeftmostXFF_Spoofable` | `TestSec_RealIP_AttackerInjectionRejected_Regression` |
| `TestSec_JWT_NoExpClaim_AcceptedForever` | `TestSec_JWT_RequireExpiryFalse_NoExpAccepted` (+ `..._RequireExpiry_NoExpRejected` for the opt-in path) |
| `TestSec_OAuth2_PlaintextHTTP_EndpointAccepted` | `TestSec_OAuth2_HTTPSEnforcement_Regression` (+ `..._AllowInsecureEndpoint_NopanicInTest`) |
| `TestSec_Logger_Method_NotSanitised` | `TestSec_Logger_Method_Sanitised_Regression` |
| `TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn` | `TestSec_ThrottlePerIPCapped_CapEnforced_Regression` + `TestSec_ThrottlePerIP_DefaultCap_Present` |

Each removal left a pointer comment at its former location (same treatment as the Compress
case) rather than duplicating an assertion that already exists in `sprint_s9_test.go`.

**Deliberately NOT touched** (read, judged NOT the same pattern, left alone):
- `TestSec_CORS_EmptyAllowedOrigins_SilentPermissive` — already asserts correctly (`t.Error` on
  the bad branch, `t.Logf` only on the good branch — the CORRECT pattern).
- `TestSec_Order_ThrottleBeforeAuthEnablesDenialOfService`,
  `TestSec_Composition_SetHeaderAfterCORS_OverwritesCORSHeaders`,
  `TestSec_RealIP_NoArgs_TrustsAllPeers_DocumentedInsecure`, and the two MSR-2026-0056
  CDN-header-absence Logfs — all explicitly self-declared in their own comments as documented,
  accepted ordering/config traps ("this test documents the risk — it doesn't assert a specific
  code outcome"), not accidental omissions. Each has a sibling test asserting the SAFE
  composition instead (e.g. `TestSec_Composition_SetHeaderBeforeCORS_CORSWins` has the real
  `t.Errorf`). Leaving these as documentation-only is consistent with how the project already
  treats other accepted risks (RealIP no-CIDR, JWT RequireExpiry default, throttle/auth
  ordering) elsewhere in SECURITY.md/findings.md.
- `TestSec_Composition_RequestID_LogNotEntangled` — borderline (neither branch asserts, no
  explicit "accepted risk" disclaimer, no existing superseding test to point at) but judged NOT
  "clearly" the same pattern since there's no definitive existing regression test elsewhere to
  supersede it with, and asserting one branch as "correct" would require a judgment call this
  agent shouldn't make unprompted. Flagged here in case a future task wants it addressed.

**Full result:** all -race/-count=3/vet/golangci-lint re-runs clean; harness test count went
from ~171 to 165 (7 removed: the 2 originally reported + 5 from the sweep; 0 net new beyond the
rename, since #284/#44/#45/#46/#58/#4/#5 additions from the first round already counted).
