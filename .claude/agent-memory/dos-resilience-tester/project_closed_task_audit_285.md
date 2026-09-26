---
name: Closed-task audit #285 — gaps closed
description: 2026-09-26 — closed audit gaps rows #74 (MSR-2026-0068), #171/#107 (DOS-2026-0060), #110 (TM-2026-020)
metadata:
  type: project
---

rmp task #285 (DoS part) closed three gaps that `reports/overview/2026-09-26-closed-task-audit.md`
flagged as "met" or "unmet" on paper but not actually enforced by a real assertion. All three
share the same underlying pattern worth remembering: **a test that only `t.Logf`s a threshold, or
whose synchronisation only approximates an invariant, is not evidence — it is documentation that
looks like evidence.** Audits in this repo now specifically hunt for that gap, not just for
missing tests.

**Why:** the audit found three instances of exactly this pattern in DoS-resilience harnesses that
had previously been marked PASS/CONFIRMED/bounded.
**How to apply:** when reviewing or writing a DoS/complexity assertion, check it actually calls
`t.Fatalf`/`t.Errorf` on the bad branch, not just `t.Logf`. Check that any concurrent
synchronisation used to justify a "peak X" claim is actually deterministic (a WaitGroup signalled
*after* the event you care about, not a timing-based sleep heuristic) — see [[feedback_deterministic_concurrency_tests]].

### Fix 1 — MSR-2026-0068 peak table size + 1M-IP memory profile (audit row #74)

- `middleware/middleware_test.go::TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn` was redesigned
  from a racy single-phase flood (WaitGroup signalled right after a start barrier + a 20ms sleep
  guess) into a deterministic two-phase test: phase 1 saturates the table with exactly
  `maxTable` distinct-key requests and polls (bounded, no fixed sleep) an atomic counter until all
  of them have provably passed `acquire()`; phase 2 then probes with `release` still closed
  (nothing can have drained yet) and asserts every probe is rejected — this IS the race-free
  "peak table size <= MaxTableSize" proof the audit wanted.
- New test `reports/dos-resilience-tester/harness/msr_0068_1m_ips_test.go::TestThrottlePerIPCapped1MDistinctIPsMemoryProfile`
  drives 1,000,000 sequential distinct IPs through `ThrottlePerIPCapped` (not plain `ThrottlePerIP`
  — the previous evidence, `s8_dos_test.go::TestThrottlePerIP1MDistinctIPs`, used only 100k IPs on
  the un-capped wrapper), records heap before/peak/after via periodic `runtime.ReadMemStats`
  sampling every 20k iterations, and asserts bounds proportional to `MaxTableSize` (1000), not to
  N (1,000,000) — with an explicit order-of-magnitude separation check between the two so the
  assertion can't accidentally pass by being too loose. Runs in ~2s (non-race), ~12s (-race).
  Evidence: `reports/dos-resilience-tester/evidence/2026-09-26/MSR-2026-0068-throttlepercapped-1m-ips-memprofile.txt`
  (regenerated on every run — read it fresh, don't assume the file is a historical snapshot).

### Fix 2 — DOS-2026-0060 real assertion, race-aware threshold (audit rows #171/#107)

- `reports/dos-resilience-tester/harness/s9_dos_test.go::TestLoggerSanitiseForLogAdversarialUTF8`
  used to `t.Logf` a "NOTE"/"PASS" regardless of whether the measured ns/byte slope or the 65KB
  latency exceeded their stated thresholds — it could never fail. Fixed to `t.Fatalf` on both
  checks, PLUS added a build-invariant super-linearity ratio check (first-interval slope vs
  last-interval slope, bound 5x) that doesn't depend on knowing the race/non-race scaling factor
  at all — ratios are preserved under race instrumentation since it multiplies both intervals by
  roughly the same constant.
- Race-build detection added as a new harness-package idiom:
  `reports/dos-resilience-tester/harness/racedetect_{on,off}.go` (`//go:build race` /
  `//go:build !race`, each defining `const raceBuild bool`). This is the standard Go idiom for
  detecting `-race` at compile time from within a test (mirrors `runtime/race.go`/`race0.go`).
  Reuse this pair for any FUTURE DoS test that needs a race-aware threshold instead of inventing a
  new detection mechanism each time.
  Measured: 4.5-4.6 ns/byte without `-race`, ~63-65 ns/byte with `-race` (threshold set to 10.0 /
  150.0 respectively, ~2.4x margin over the measured race value).

### Fix 3 — TM-2026-020 WriteTimeout through Compress with a real slow reader (audit row #110)

- New test `reports/dos-resilience-tester/harness/tm_020_write_timeout_test.go::TestCompressSlowReaderWriteTimeoutFires`.
  Previous evidence (`DOS-2026-0062`/`TestCompressSlowReadMemoryProfile`) only measured heap with a
  FAST synchronous reader — never exercised `WriteTimeout`, never used a slow reader, never checked
  connection lifetime or goroutine cleanup.
- Technique worth reusing: `httptest.NewUnstartedServer` + `srv.Config.WriteTimeout = ...` +
  `srv.Start()`; raw `net.Dial` (not `http.Client`, which insists on reading the response); a
  background goroutine trickle-reads 1 byte every 50ms (genuine slow-reader shape, not a
  zero-read stall) from a connection with `SetReadBuffer(1024)` set deliberately tiny so TCP
  backpressure builds up fast and deterministically regardless of the host's default socket
  buffer auto-tuning; the handler writes FRESH random (incompressible) 64KB chunks each iteration
  — reusing the same buffer would let gzip's LZ77 window compress it to near-nothing across
  writes, and wire bytes would never accumulate enough to trigger backpressure.
  Result: deadline fires at ~300.3-301.8ms against a configured `WriteTimeout=300ms`, every run,
  both with and without `-race` — remarkably tight and deterministic. Goroutine count returns to
  baseline (slack=3) after `conn.Close()` + waiting on the handler's own completion signal.
