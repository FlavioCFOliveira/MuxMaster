---
name: task_sprint20_242_241_backlog_clearance
description: 2026-09-25 sprint 20 "Backlog clearance" — scrubbed the identifier "RegisterMethod" from specification/ entirely, and corrected groups.md Mount catch-all registration string to the real "/*mux_mount"
metadata:
  type: feedback
---

Task #242 (2026-09-25): the user decided that the identifier `RegisterMethod` must not appear anywhere under specification/, even inside a negative statement ("there is no RegisterMethod function"). This is stricter than the [[task_rmp262_method_set_correction]] fix, which only corrected the *claim* (RegisterMethod doesn't exist / isn't a way to add custom methods) but still named the fictional identifier as a negation. Rephrased name-free in two places, meaning preserved exactly, numbering unchanged:
- `specification/routing.md` rule 35: "There is no function, or other mechanism, on `*Mux` or `*Group` to declare or register one."
- `specification/out-of-scope.md` §2.7: "...and provides no function or other mechanism to declare or register a custom or extension method."

**Why:** naming a non-existent identifier, even to deny it, invites confusion (readers may search the codebase for it, or later code could accidentally introduce a symbol with that exact name and appear to "fulfill" a spec sentence that was only ever describing an absence). The user's instruction implies this is a general precept for this spec: **when documenting that a feature/function does not exist, describe the absence in generic terms — do not coin or repeat a specific identifier name for something fictional.**

**How to apply:** in future "X is out of scope" / "there is no Y" sentences, prefer "no function or mechanism to do Z" over inventing/repeating a plausible-sounding but nonexistent API name, unless the user explicitly wants the fictional name documented as a rejected proposal.

Task #241 (2026-09-25): `specification/groups.md` §7 rule 28 said Mount registers `Handle("*", prefix+"/*", ...)` — an anonymous catch-all. The real registration in `mux.go` (~line 812) is `m.Handle("*", prefix+"/*mux_mount", mountH)`; the captured value is read via `PathParam(r, "mux_mount")` (~line 776). Fixed rule 28 to the real string and explained the `mux_mount` capture semantics (remaining path including leading `/`, tied into existing rule 23's shallow-copy mechanism), citing mux.go directly. Verified empirically via `redirect_tsr_safety_test.go` (mux_mount="/" for the bare-prefix-with-slash case) that the leading-slash capture claim already in rule 23 was correct — no other change needed there.

Checked and explicitly rejected as unverified: the task prompt speculated that Mount might validate a prefix that contains the literal string "mux_mount" and reject it. Read `mountAt` in full (mux.go ~756-812) — the only three registration-time panics are: nil handler, prefix not starting with `/`, and prefix containing invalid UTF-8 (the UTF-8 check exists specifically so a `tree.go` panic doesn't leak the internal `*mux_mount` param name in its message — see the FPE-2026-002 comment). There is no name-collision validation. Did not add this UTF-8-validation behavior to groups.md since it wasn't part of the requested scope and isn't currently specified anywhere in specification/ — flagging here in case a future task asks to specify Mount's prefix-validation panics exhaustively.

AC verified: `grep -rn RegisterMethod specification/` → empty. `grep -n 'prefix+"/\*mux_mount"' specification/groups.md` → rule 28 matches. `grep -rn '"/\*"' specification/` → empty (no stale anonymous Mount catch-all left).
