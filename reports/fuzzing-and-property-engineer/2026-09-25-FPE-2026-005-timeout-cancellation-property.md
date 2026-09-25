# FPE-2026-005 — `TestProp_TimeoutCancelsContext`: context not cancelled within a 1 ms margin of the deadline

Date of this write-up: 2026-09-25 (retroactive; the finding was raised and closed on 2026-05-07)
Author of this write-up: threat-modeler-and-zero-day-researcher (findings reconciliation, rmp task #240)
Original owner: fuzzing-and-property-engineer
Originating rmp task: #176 (COMPLETED 2026-05-07T21:31, no completion summary recorded)
Commit audited originally: `e30ae94` (S9 working tree, per `invariants.md` header)

## Why this document exists

FPE-2026-005 was never written up. Its only surviving artefact is the rapid
failure file
`reports/fuzzing-and-property-engineer/harness/testdata/rapid/TestProp_TimeoutCancelsContext/TestProp_TimeoutCancelsContext-20260507141841-313149.fail`.
The property test that produced it, `TestProp_TimeoutCancelsContext`, no
longer exists anywhere in the repository or in git history (it was never
committed). This document is the human-readable record.

## Classification

| Field | Value |
|---|---|
| Type | Property-test failure on a timing-dependent invariant |
| Severity | Low (rmp severity 3) |
| Component | `middleware/timeout.go` (`Timeout`) |
| Status | Closed in rmp; see "Resolution as found in the repository" — the acceptance criteria were not met as written |

## Observation (from the rapid failure file)

```
[rapid] draw timeoutMs: 50
[rapid] draw sleepMs: 51
context not cancelled after timeout=50ms sleep=51ms
```

The shrunk counter-example: with `Timeout(50ms)`, a handler that sleeps 51 ms
and then inspects its request context did not observe the context as
cancelled. The deadline itself was set (invariant I-15 passed).

## Analysis (from rmp task #176, not re-verified)

`Timeout` is implemented as `context.WithTimeout(r.Context(), d)` followed by
`next.ServeHTTP(w, r.WithContext(ctx))` (`middleware/timeout.go:19-30` at
HEAD `b038632`). The deadline fires from a runtime timer; closing `Done()`
is asynchronous with respect to the handler goroutine. A 1 ms margin between
the deadline and the handler's check is within scheduler latency, so the
property as written is not a guaranteed behaviour of `context.WithTimeout`.
The task listed three options: (a) document it as a design limitation of the
cooperative model, (b) make the test tolerate scheduler delay, (c) evaluate
`http.TimeoutHandler` as an alternative implementation.

This analysis is the task author's; no reproduction was re-run for this
write-up, because the original test source is lost.

## Resolution as found in the repository

- The property was replaced by `TestProp_TimeoutSetsDeadline`
  (`reports/fuzzing-and-property-engineer/harness/properties_test.go:554`),
  which checks only that a deadline is present. The test carries a one-line
  comment: "We test deadline presence, not cancellation (which is
  timing-dependent)."
- `invariants.md` I-15 is titled "Timeout sets deadline" and records
  `Fail-count: 0`. It contains no engineering note about the cancellation
  failure.

The rmp acceptance criteria required either (1) the cancellation property to
pass under `-count=1000` on every GOOS, or (2) the finding to be documented as
an accepted timing limitation in `invariants.md` with an engineering note.
Neither is present. The weakening of the property is recorded only in a code
comment.

## Relation to other findings

The operator-facing consequence (handlers must observe `ctx.Done()`; the
middleware does not preempt) is already documented in `SECURITY.md` under
"Timeout Middleware (MM-2026-0019)" and "Timeout middleware preemption
(DOS-2026-0003)". FPE-2026-005 adds the narrower fact that cancellation is
observable only after a scheduler-dependent delay past the deadline.

## Open item

Add the engineering note to `invariants.md` I-15 (or restore a cancellation
property with a tolerant margin) so that the acceptance criteria of rmp task
#176 are actually met. Not done as part of this reconciliation (out of scope).
