---
name: task_kg2026001_fastpath_spec
description: rmp task 237 (KG-2026-001) — specified 7 previously-unspecified implemented features and corrected spec/code drift discovered along the way
type: project
---

On 2026-07-14, rmp task 237 (KG-2026-001) added specification entries for 7 features that were implemented but had no `specification/` entry: FastHandler dispatch, FastMiddleware, PoolFastParams, PoolRequestBundle, lock-free dispatch (`treesPtr atomic.Pointer[methodTrees]`), the tiered request bundle (`reqBundle1`/`reqBundle2`/`reqBundle`), and `Mux.Rebuild()`.

**Why:** The knowledge graph query `MATCH (f:Feature) WHERE NOT (f)-[:SPECIFIED_IN]->() RETURN f.name` found these 7 gaps against the project's own "no feature may be implemented without a corresponding specification entry" policy stated in specification/README.md.

**How to apply:** If asked to audit for more spec/code gaps, re-run that KG query rather than assuming the corpus is exhaustive — the KG can drift from the actual specification files just as the spec can drift from code (see below).

## Files changed and what was added

- `specification/performance.md`: new sections 6 (FastHandler Dispatch), 7 (Lock-Free Dispatch), 8 (Tiered Request Bundle). Also corrected section 3.1 (items 10-11), which described a stale single-`sync.Pool`-of-`Params`-with-`make(Params,n)`-copy model that no longer matches the code.
- `specification/middleware.md`: new sections 6 (FastMiddleware) and 7 (Route-Type Coverage Matrix: Pre vs Use vs UseFast). Documents an important asymmetry verified in source: `HandleFast` panics if `Use` was already called with middleware present, but calling `Use` *after* `HandleFast` does NOT panic and silently does not wrap the fast routes — order-dependent, one-directional guard (mux.go `HandleFast`, group.go `Group.HandleFast`).
- `specification/configuration.md`: new fields 4.5 `PoolFastParams`, 4.6 `PoolRequestBundle`, and new section 5 (Configuration Snapshot and Rebuild) describing the freeze-on-first-`ServeHTTP` model that `mux.go`'s `muxConfig`/`cfg atomic.Pointer[muxConfig]` implements.
- `specification/params.md`: corrected section 2 item 6 (falsely claimed a hard cap of 16 params with a registration-time panic — actual code: `maxParams = 3` is a stack-buffer size, not a cap; overflow spills to a heap slice, no panic, no limit) and rewrote section 5 "Pool Behavior" (renamed "Parameter Storage and Pooling") to describe the real tiered-bundle + FastParams-pool architecture. Also qualified item 25 (Params-safe-to-retain-across-goroutines guarantee) with the `PoolRequestBundle` exception.
- `specification/out-of-scope.md`: corrected section 3.1 (Hot Reload of Routes) — it claimed "the current design ... serves [routes] read-only" and dismissed "a lock-free tree structure" as unbuilt "significant complexity". That is exactly what `treesPtr atomic.Pointer[methodTrees]` + copy-on-write `Handle`/`HandleFast` now is. Rewrote to keep the *policy* identical (hot reload remains unsupported/untested/undocumented) while fixing the technical claim, and cross-referenced performance.md section 7.
- `specification/README.md`: added glossary terms FastHandler, FastMiddleware, Request bundle, Configuration snapshot; broadened the Pool glossary entry (it's no longer one `sync.Pool`, there are five: `fastParams1/2/3Pool`, `reqBundle1/2/N Pool`).

## Key lesson: the spec corpus had drifted from code, independent of the 7-feature gap

`params.md` and `out-of-scope.md` described an architecture that predates several optimization passes (Opt O9–O13 per CLAUDE.md's changelog-style comments in the source). This was NOT part of the 7 named features but was directly entangled with them (I could not accurately spec pooling/bundling without first fixing the stale baseline they contradicted). Treated as an "obvious, low-risk correction" (CLAUDE.md working-agreement §1) since it was a factual correction verified against source, not a scope/design decision — the *policy* conclusions in out-of-scope.md were preserved unchanged; only the inaccurate technical justification was fixed.

**Lesson for future spec work on this project:** before trusting any existing `specification/*.md` file as ground truth for a *new* task, verify its claims against the current source (`mux.go`, `params.go`, `tree.go`, `group.go`, `handler.go`) — do not assume internal spec-to-spec consistency implies spec-to-code accuracy. This codebase changes fast (see CLAUDE.md's Opt O1–O13 performance changelog) and the spec does not always keep pace.

## Findings noted but NOT fixed (out of this task's scope — flagged to user in final report)

- `specification/groups.md` item 28 says Mount registers `Handle("*", prefix+"/*", ...)`; actual code (`mux.go` `mountAt`) uses `prefix+"/*mux_mount"` — the internal catch-all param name differs from what's documented.
- `specification/routing.md` item 34 documents a `RegisterMethod(method string)` API; no such function exists in the current source (grepped, not found). Likely aspirational/unimplemented, same category as the old 2026-04-16 "NOT yet implemented" list.
