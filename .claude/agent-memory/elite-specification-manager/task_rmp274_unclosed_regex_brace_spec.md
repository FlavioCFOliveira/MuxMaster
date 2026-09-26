---
name: rmp task 274 — unclosed regex parameter brace panic (FPE-O14-002)
description: 2026-09-25 spec-only addition documenting the new registration-time panic for a pattern segment with an unclosed '{' — regression fix for a silent tree-corruption defect
type: project
---

Specified the panic MuxMaster now raises when a pattern segment contains an opening `{` with no matching closing `}` before the next `/` or end of pattern, for rmp task #274 (sprint 20). Spec-only — verified against the (uncommitted at spec-time) code fix in `tree.go` (`findWildcard`'s `{` case, and `insertChild`'s `!valid` branch) and the regression test `tree_unclosed_regex_param_test.go::TestUnclosedRegexParamPanicsWithoutCorruptingTree`.

**Why:** FPE-O14-002 was a real silent-corruption defect (found by the fuzzing-and-property-engineer's `FuzzWalkRoutes`): registering a malformed pattern like `/{` used to silently overwrite whatever route already occupied that tree node — no panic, no conflict signal — because `findWildcard` returned `start=-1` for an unclosed brace, which `addRoute`'s walk loop treated as "no wildcard marker at all," falling through to `insertChild`'s unconditional node-overwrite path meant only for freshly allocated nodes.

**How to apply:** [[project_spec_structure]] now points here for routing.md's current item count (96). Read routing.md section 11 before answering any question about malformed `{`/regex-parameter patterns.

## Files changed

- **routing.md §1.5 Regex Parameters** — non-renumbering trailing sentence after rule 23, cross-referencing new section 11.
- **routing.md §5 Registration-Time Panics** — non-renumbering trailing sentence after rule 71, cross-referencing section 11.
- **routing.md**: appended new **section 11 "Unclosed Regex Parameter Brace"**, items 94-96 (append-only convention, no renumbering):
  - 94: the trigger condition and exact panic message `muxmaster: regex param '{' in path '<pattern>' is missing its closing '}'`; three examples taken directly from the coordinator's report and the test file, including the subtle case `/x/{id:[0-9]+/y` where the closing-brace search does not cross a `/` segment boundary (verified this against `findWildcard`'s `closeIdx` scan, which is bounded by the segment).
  - 95: disambiguates this from rule 70 (invalid regex *inside* a properly closed `{name:expr}` — a different, later-stage check).
  - 96: the tree-unchanged guarantee. **Verified against the test, not assumed**: `TestUnclosedRegexParamPanicsWithoutCorruptingTree` asserts, after the panic, that `Lookup` still finds the pre-existing base route and `Walk` shows exactly one entry for it and zero for the malformed pattern, across three cases (root bare `/{`, root named `/{a`, and a static-sibling case `/admin` + `/{oops`).

## Note on scope of the underlying code change

The code fix (`tree.go`) was **uncommitted** at the time this spec task ran (confirmed via `git diff tree.go` and the new root-level `tree_unclosed_regex_param_test.go`, also uncommitted). This spec-authoring task only reads and describes that diff/test; it does not commit anything, per instruction. Whoever closes rmp #274 in code still needs to commit `tree.go` + the test file.
