# Memory Index — middleware-security-reviewer

- [Sprint 2026-05-07 JWT/OAuth2/APIKey/Full-16-middleware + gap-iteration findings](sprint-2026-05-07-findings.md) — 17 middleware audit + gap iter: Mount bypass (High/ID 61), OAuth2 MITM blast-radius (Medium/ID 62), JWT crit PASS (Info/ID 63)
- [Sprint S10-PreMSR 2026-05-08 — TM-001/002/004/005/022/044 verdicts](sprint-2026-05-08-S10-PreMSR.md) — 2 confirmed vulns (TM-005 slog leak, TM-001 eternal token), 1 partial (TM-002 null→0), 2 refuted (TM-004 fix OK, TM-022 logger safe), 1 documented (TM-044 RealIP warn-but-accepts)
- [Sprint 2026-09-26 closed-task-audit gap closure](sprint-2026-09-26-closed-task-audit-gaps.md) — rmp #284/#285: TM-040 Pre ordering bypass CONFIRMED (+ Use()/NotFound nuance), 5 stale-Logf test fixes, CDX-2026-003 "detect RealIP" ruled unimplementable
- [Sprint 2026-09-26 TSC-2026-0002 constant-time lookup](sprint-2026-09-26-tsc-2026-0002-constant-time-lookup.md) — rmp #290/#29: BasicAuth map→scanned-slice fix, ~26ns/user cost, timing bound confirmed, SECURITY.md left for user
