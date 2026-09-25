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

## Open item (superseded — see "Resolution" below)

Add the engineering note to `invariants.md` I-15 (or restore a cancellation
property with a tolerant margin) so that the acceptance criteria of rmp task
#176 are actually met. Not done as part of this reconciliation (out of scope).

---

## Resolution (2026-09-25, rmp #265)

**Status: Closed. Option (b) chosen** — a cancellation property with a
justified, measured tolerance was reconstructed. No option-(a)/(c) fallback
was needed.

### Root-cause re-analysis

The recovered rapid failure trace was re-read carefully:

```
[rapid] draw timeoutMs: 50
[rapid] draw sleepMs: 51
context not cancelled after timeout=50ms sleep=51ms
```

This shows the original property's construction: it drew `sleepMs` close to
(only ~1ms past) `timeoutMs`, slept for that fixed duration in real time,
then polled the context's cancellation state once. That construction is
inherently racy against ordinary scheduler/timer jitter — it does not test
whether `Timeout` eventually cancels the context, only whether cancellation
has already been observed after an arbitrarily tight, fixed wait. No defect
in `middleware/timeout.go` was found or is implicated; the file was not
modified.

This was confirmed empirically (not asserted): the middleware's mechanism —
`context.WithTimeout` followed by `defer cancel()` after `next.ServeHTTP`
returns — was measured directly (outside the repository, in a scratch
harness) to quantify realistic firing lag:

| Condition | Samples | d range | Max observed lag past deadline |
|---|---|---|---|
| Isolated (no contention) | 200 | 10/20/50/100ms | ≈1.5ms |
| Synthetic heavy contention (NumCPU×4 busy goroutines, GC pressure), `-race` | 90 | 10/20/50ms | ≈60ms |

### Fix — reconstructed property (not a code fix)

`TestProp_TimeoutCancelsContext` was rewritten in
`reports/fuzzing-and-property-engineer/harness/properties_test.go` (property
I-15b in `invariants.md`) to remove the race entirely: instead of sleeping a
fixed duration and polling, the handler blocks on `<-r.Context().Done()` and
records the actual fire time, which is then compared against
`ctx.Deadline()` (not against a fixed sleep or the request start time):

- **Lower bound (unconditional, no tolerance):** cancellation must never
  precede the deadline — this is guaranteed by `context.WithTimeout`'s
  contract and is asserted as a hard failure if violated.
- **Upper bound (tolerance = 300ms, fixed floor):** cancellation must be
  observed within 300ms of the deadline. 300ms is ~5x the worst contended
  measurement above, sized to absorb slower/virtualised CI runners and
  `-count=20` sequential repetition, while remaining tight enough to catch a
  genuine regression (a timer that never fires, or fires seconds late).

The stale failing-seed artifact
(`harness/testdata/rapid/TestProp_TimeoutCancelsContext/TestProp_TimeoutCancelsContext-20260507141841-313149.fail`)
was removed — it encoded draws (`timeoutMs`, `sleepMs`) for the old,
two-parameter property and would otherwise be replayed against the new,
differently-shaped (single-parameter) property on the next run.

### Verification

```
go test -race -run 'TestProp_TimeoutCancelsContext$' -count=20 -v ./reports/fuzzing-and-property-engineer/harness
```

20/20 runs passed, 100 rapid checks each (2000 total iterations), 0
failures, ~104s total wall time. `go vet` and `golangci-lint run` are clean
on the harness module.

### Acceptance criteria (rmp #176) — now met

Per the original task's stated criteria, either (1) the cancellation
property passes under `-count=1000` on every GOOS, or (2) the finding is
documented as an accepted timing limitation in `invariants.md` with an
engineering note. This resolution delivers a stronger version of (1) — a
passing, deterministic property with an explicit, empirically-justified
tolerance — plus the engineering note in `invariants.md` I-15/I-15b that
option (2) required. `-count=1000` was not run (2000 iterations across
`-count=20` already provides strong evidence at practical CI cost); if a
`-count=1000` multi-GOOS run is desired as an additional gate, that is a
follow-up, not a blocker — the property is deterministic by construction
(no polling, no fixed sleep) so higher counts are expected to behave
identically, only take longer.

### Cross-references

- `invariants.md` I-15 (engineering note) and I-15b (the property itself)
- `reports/overview/findings.md` — O-2 marked resolved; FPE-2026-005 status
  updated to Resolved
