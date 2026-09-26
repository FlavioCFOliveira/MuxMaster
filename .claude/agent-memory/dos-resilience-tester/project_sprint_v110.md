---
name: Sprint v1.1.0 + S8/S9 DoS Audit State
description: 2026-05-07 sprint S8+S9 — harness files, all finding IDs, empirical results
type: project
---

## S8/S9 findings (2026-05-07, commit e30ae94)

Harness files: `/reports/dos-resilience-tester/harness/`
- s8_dos_test.go — H8-04/06/25/47/49/50/56
- dos_v2_test.go — DOS-2026-0051..0056
- s9_dos_test.go — DOS-2026-0057..0063 (new in S9)
- complexity_test.go, compress_oom_test.go, breach_oracle_test.go
- sniff_buffer_test.go, throttle_test.go, timeout_leak_test.go
- recoverer_throttle_test.go, oauth2_stampede_test.go
- paramsbuf_test.go, notfound_amplification_test.go, jwks_storm_test.go

**Why:** S8/S9 are exhaustive pre-release audits covering all DoS taxonomy classes.
**How to apply:** For S10+, start from DOS-2026-0064 and re-run full harness suite. All tests pass with -race -short.

### S9 Finding Summary (2026-05-07)

| ID | Status | Component | Summary |
|---|---|---|---|
| DOS-2026-0057 | CONFIRMED (accepted) | throttle.go | ThrottlePerIPCapped: N concurrent attackers lock out new IPs (#168) |
| DOS-2026-0058 | REFUTED | mux.go | methodNotAllowedCache/optionsCache key space is closed (#169) |
| DOS-2026-0059 | CONFIRMED | real_ip.go | selectXFFRightmost O(N×M): 2.3ms at N=74k entries, M=50 CIDRs (#170) |
| DOS-2026-0060 | INFORMATIONAL | middleware/logger.go | sanitiseForLog O(L): 7.9 ns/byte, bounded (#171) |
| DOS-2026-0061 | PASS (regression guard) | throttle.go | Timeout-path refs decrement correct (#172) |
| DOS-2026-0062 | PASS | middleware/compress.go | Streaming confirmed: 1.2 MB heap for 10 MB response (#174) |
| DOS-2026-0063 | PASS | mux.go | Method dispatch O(1) regardless of name length (#175) |

### S8 Finding Summary (2026-05-07, all from previous session)

| ID | Status | Component | Summary |
|---|---|---|---|
| DOS-2026-0001 | CONFIRMED (sev 5) | compress | BREACH oracle: Cohen's d=10.28, ~2 requests/char |
| DOS-2026-0002 | CONFIRMED (sev 4) | real_ip+throttle | ThrottlePerIP before RealIP degrades to global |
| DOS-2026-0003 | ACCEPTED | timeout | Goroutine survival after timeout: ctx-only cancel |
| DOS-2026-0004 | FIXED | oauth2 | Stampede singleflight — now 1 IDP call for 100 concurrent |
| DOS-2026-0005 | CONFIRMED (sev 4) | oauth2 cache | Cache-full DoS: attacker fills MaxCacheSize with long-lived tokens |
| DOS-2026-0006 | CONFIRMED (sev 5) | compress | BREACH oracle Cohen's d=10.28 (empirical) |
| DOS-2026-0007 | INFORMATIONAL | compress | sniff buffer: N*8192 bytes for N stalled connections |
| DOS-2026-0051 | CONFIRMED (sev 3) | tree.go | addRoute O(N^2): N=2000 takes 450ms startup |
| DOS-2026-0052 | INFORMATIONAL | logger | Sync I/O backpressure on logger writer |
| DOS-2026-0053 | INFORMATIONAL | mux | No MaxBytesReader in core mux |
| DOS-2026-0054 | PASS | mux | Rebuild() under load: no race, correct responses |
| DOS-2026-0055 | REFUTED | tree.go | ReDoS: Go RE2 O(n), no exponential backtracking |
| DOS-2026-0056 | INFORMATIONAL | net/http | Slowloris: 50 goroutines for 50 stalled connections |

### Key empirical results (S9)

- getValue O(k) slope: **0.033 ns/segment** (threshold < 5.0 ns) — PASS
- GC pacing: 15 GC cycles / 100k requests, 416 B/req, 17 ms total pause — PASS
- selectXFFRightmost at N=74k entries: 2.3 ms raw; negligible in httptest (dominated by routing overhead)
- ThrottlePerIP timeout refs: 49/50 requests timed out correctly, table drained — PASS
- Compress streaming: 1.2 MB heap for 10 MB logical output — PASS
- Method dispatch (10k char name): ~320 ns — O(1) confirmed
- BREACH oracle: Cohen's d=10.28 (critical) without mitigation; d=0.024 with 256-byte random padding
