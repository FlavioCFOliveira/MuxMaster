---
name: rmp #262 fixed-method-set specification correction
description: 2026-09-25 fix — routing.md and out-of-scope.md corrected to match code's fixed 11-token method set (no RegisterMethod, no custom methods ever existed in code)
type: project
---

Source: rmp task #262 (sprint 19), explicit user decision: correct the SPECIFICATION to match the code, do NOT specify new features. The pre-existing routing.md described a "RegisterMethod" function and open-ended custom-method registration (`PURGE`, `PROPFIND`) that never existed in the code — this was pure spec fiction, not a stale-after-a-change drift. See [[project_spec_structure]] for file inventory and [[task_rmp258_sprint18_reconciliation]] for the renumbering-safety convention this task followed.

## Verified code behavior (mux.go, group.go)

- `methodIdx(m string) int` recognizes exactly 11 exact-match strings: the 10 standard methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, QUERY) plus the internal token `"*"` (idxWild=10, methodCount=11). Everything else returns -1. Case-sensitive exact string match only — no normalization.
- `Handle`/`HandleFast` (mux.go ~451-469, ~547-577) check order: (1) `method == ""` → `panic("muxmaster: HTTP method must not be empty")`; (2) pattern doesn't start with `/`; (3) handler nil; (4) `mu.Lock()`; (5) `methodIdx(method) < 0` → `panic("muxmaster: unsupported HTTP method '<method>'")`. The empty-method panic is a DIFFERENT message from the unsupported-method panic, and is checked first.
- `Group.Handle`/`HandleFunc`/`HandleE`/`Match` all delegate to `g.mux.Handle` (same checks). `Group.HandleFast` delegates to `g.mux.HandleFast`. No independent validation logic in group.go.
- `ANY`/`Group.ANY` iterate `anyMethods` (10 standard methods only, never `"*"`).
- No `RegisterMethod` function exists anywhere in the module (only in vendored chi code under `reports/path-routing-fuzzer/harness/vendor/`, irrelevant).
- `"*"` is not just internal-only: `m.Handle("*", pattern, handler)` called directly by a user IS accepted (methodIdx("*")=idxWild≥0, no panic) and registers on the same wildcard tree `Mount` uses internally (`mountAt` calls `m.Handle("*", prefix+"/*mux_mount", mountH)`), matching every request method at that pattern. This is documented as the low-level mechanism, not a recommended public API — `ANY` is the supported way to hit every standard method.
- Dispatch (mux.go `dispatch`, ~1097-1354): `methodIdx(r.Method) < 0` → `root` stays nil → falls through to `allowed()` check → 405 with Allow header if `HandleMethodNotAllowed` and the path has at least one other registered method, else `lazyNotFound` (404). This is the exact same code path used for a recognized-but-not-registered-here method — `routing.md` section 4.7 (rules 60-61) already covered this generically and needed no edit.

## What changed

1. **routing.md**: rule 31 rewritten (was: "any HTTP method string can be registered" — false); section 2.3 renamed "Custom Methods" → "Unsupported Methods", rules 34-36 rewritten (panic message, no RegisterMethod + pointer to out-of-scope.md §2.7, ANY has nothing to include/exclude since no custom method can register); section 2.4 rules 37-38 rewritten with exact panic messages and the 405/404 dispatch cross-reference. Rule 82 (§9 QUERY semantics) rewritten to drop the RegisterMethod/§2.3-custom-method-rules reference. **Item numbering preserved exactly (still 1-88, no gaps)** — deliberately did NOT insert new numbered items or a new subsection, because error-handling.md rule 8 and configuration.md rule 18 cross-reference routing.md's rule 61 and rule 53 by number; any insertion before those would have silently broken those two external references. Confirmed via grep this is the only place in the whole spec corpus that mentioned "custom method"/"RegisterMethod"/"any HTTP method string".
2. **out-of-scope.md**: new §2.7 "Custom and Extension HTTP Methods", inserted correctly as `### 2.7` right after `### 2.6` (before the `## 3.` boundary) — first attempt landed it in the wrong place (end of file, wrong heading level) and had to be corrected. Rationale grounded in mux.go's own comment ("replace the map[string]*node lookup with an O(1) array access") — not invented.
3. **groups.md**: already accurate (rule 28 already documents `Handle("*", prefix+"/*", ...)`) — no changes needed, only cross-referenced from routing.md rule 31.
4. **README.md**: no changelog section exists in this spec (just an index + terminology + design principles); nothing there referenced the wrong claims, so untouched.

## Follow-up amendments (same session, same task #262)

- **Rule 38** amended: an unrecognized/unmatched method does NOT always fall straight to section 4.7 (405/404) — the internal `"*"` tree (rule 31) is checked first (dispatch() checks `trees[idxWild]` unconditionally after the primary-tree attempts, whether or not `root` was nil). A matching `"*"` route (Mount, or a direct `Handle("*", ...)`) serves the request regardless of method. `allowed()` skips `idxWild`, so `"*"` routes never appear in the `Allow` header (rule 61 already only lists the 10 standard methods, so rule 61 itself needed no edit — only rule 38 needed to state the exception and the Allow-header omission).
- **Rule 47** (§4.1 Lookup Sequence) amended to add the missing `"*"`-tree fallback as new sub-steps: kept rule number 47 fixed, renumbered only its internal sub-step list from 1-8 to 1-10 (inserted new steps 6-7 for the `"*"` tree lookup + its own TSR handling — confirmed via mux.go dispatch() ~1263-1339 that the `"*"` tree gets TSR treatment but NOT a RedirectFixedPath equivalent, since `cleanedPath` is only ever called with the primary `root`, never `starRoot`). Old steps 6/7/8 (OPTIONS/405/404) shifted to 8/9/10. Grepped the whole `/specification` corpus for "rule 47", "step 6/7/8", "Lookup Sequence" cross-references before renumbering — none existed outside routing.md's own rule 47 text, so the sub-step renumbering was safe and required no other file changes.

## Renumbering-safety lesson (reinforces [[task_rmp258_sprint18_reconciliation]])

Before editing any numbered spec file, grep the ENTIRE `specification/` corpus for cross-file references to that file's specific rule/item numbers (not just section numbers) before deciding whether renumbering is safe. Here it wasn't (rules 53, 61 are pinned externally), so same-count in-place rewrites were used instead of inserting new subsections mid-file.
