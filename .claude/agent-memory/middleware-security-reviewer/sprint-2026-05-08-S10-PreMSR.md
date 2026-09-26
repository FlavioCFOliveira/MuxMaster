---
name: Sprint S10-PreMSR findings 2026-05-08
description: S10-PreMSR mini-sprint verdicts for TM-2026-001/002/004/005/022/044 — 2 confirmed vulns, 2 partial, 2 refuted
type: project
---

Six UNTESTED hypotheses closed in S10-PreMSR (2026-05-08). Harness at:
`reports/middleware-security-reviewer/harness/2026-05-08-S10-PreMSR/s10_premsr_test.go`

## Verdicts

| Hypothesis | Verdict | Severity | Action |
|---|---|---|---|
| TM-2026-001 JWT RequireExpiry default | CONFIRMED-VULN | 6 | Document RequireExpiry=true as recommended default in README/quickstart |
| TM-2026-002 JWT exp type confusion | PARTIAL | 3 | exp=null silently→0 (risk only when RequireExpiry=false); all other malformed types rejected |
| TM-2026-004 OAuth2 url.Parse | REFUTED (fix in place) | — | userinfo/empty-host/bad-scheme all panic; CRLF rejected by url.Parse itself |
| TM-2026-005 OAuth2 slog credential leak | CONFIRMED-VULN | 4 | slog.Warn logs full URL including query params (client_secret=X visible). Fix: log only parsedEndpoint.Host in Warn path |
| TM-2026-022 Logger leaks Authorization | REFUTED | — | Logger never logs request headers. Authorization/Cookie/ApiKey are not present in log output |
| TM-2026-044 RealIP default trusts all | CONFIRMED (documented behaviour) | 4 | RealIP() with no CIDRs rewrites RemoteAddr from XFF/X-Real-IP from ANY peer. slog.Warn is emitted. Recommend README callout |

## CL-AUTH-1 status
No change from S9. This sprint focused on CL-AUTH-1 adjacent (JWT/OAuth2 cluster). CL-AUTH-1 proper (CSA-0060/FPE-010) still OPEN.

## CL-OAUTH2-1 status
TM-004 REFUTED (fix already in place in oauth2.go). TM-005 CONFIRMED-VULN (new finding: slog.Warn leaks full URL query string). Remaining items: TM-015 (cache TTL race), TM-019 (evictExpiredLocked contention) still UNTESTED.

**Why:** budget-exhaustion gap closure before v1.2.0 release gate.
**How to apply:** when revisiting JWT/OAuth2/Logger/RealIP, start from these verdicts.
