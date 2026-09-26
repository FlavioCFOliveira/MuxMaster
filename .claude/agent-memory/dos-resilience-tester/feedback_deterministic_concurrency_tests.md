---
name: feedback_deterministic_concurrency_tests
description: Prefer deterministic phase-based synchronisation over timing-heuristic barriers (sleep-after-WaitGroup) when a test needs to assert a hard concurrent invariant
metadata:
  type: feedback
---

When a DoS/concurrency test needs to assert a hard bound on a concurrently-mutated value (e.g.
"peak table size never exceeds the cap"), do not rely on a `WaitGroup` signalled at the START of a
goroutine plus a fixed `time.Sleep` guess to approximate "everyone has reached the interesting
point." That pattern (seen in the pre-fix
`middleware/middleware_test.go::TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn`, see
[[project_closed_task_audit_285]]) only proves goroutines have *started*, not that they've reached
the specific internal call (e.g. `acquire()`) whose outcome you want to snapshot — and once you
start releasing/draining resources concurrently with stragglers still arriving, any "peak" number
you read back is unsound (could be an undercount OR an overcount depending on interleaving).

**Why:** this is exactly the gap a closed-task audit flagged (row #74, rmp #285) — the existing
test's synchronisation could not support the hard assertion the audit wanted, so the assertion had
just been left out (or downgraded to `t.Logf`), silently weakening the regression guard.

**How to apply:** when a test needs "prove peak/exact concurrent state X", restructure into
deterministic phases instead of tuning sleep durations:
1. Phase 1 — drive the system to the state you want to measure, using a signal that fires
   *after* the specific call you care about (e.g. an atomic counter incremented inside the handler
   itself, right after the operation under test succeeds) rather than *before* it. Poll that signal
   in a bounded loop (deadline, not a fixed sleep) until it reaches the expected count.
2. Phase 2 — with the system provably in that state and nothing yet allowed to drain/release, take
   your measurement or run your probe requests synchronously — no concurrency needed here, which is
   what makes it deterministic.
3. Only THEN release/drain, and assert on the settled outcome.

This costs a little more code than a sleep-and-hope barrier but produces an assertion that is
sound under `-race`, under CI scheduler pressure, and under `-count=N` repeats — verified empirically
(0 flakes across `-count=3` normal + `-count=5` `-race` runs after the redesign).
