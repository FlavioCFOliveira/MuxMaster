# Memory Index — dos-resilience-tester

- [Sprint v1.1.0 DoS Audit State](project_sprint_v110.md) — 2026-05-07 sprint findings, new harness, finding IDs MM-2026-0050..0053
- [Production Load Test 2026-05-08](project_production_loadtest_2026_05_08.md) — 67k RPS × 30s, O(k) slopes, GC steady-state, GO verdict, no new sev≥4
- [Closed-task audit #285 gaps closed](project_closed_task_audit_285.md) — 2026-09-26: MSR-2026-0068 peak-size + 1M-IP profile, DOS-2026-0060 real assertion + race-build idiom, TM-2026-020 WriteTimeout-through-Compress slow-reader proof
- [Deterministic concurrency test design](feedback_deterministic_concurrency_tests.md) — phase-based sync (poll a post-event signal) beats sleep-after-WaitGroup barriers for hard concurrent-invariant assertions
