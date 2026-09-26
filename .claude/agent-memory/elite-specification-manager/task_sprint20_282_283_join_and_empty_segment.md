---
name: task_sprint20_282_283_join_and_empty_segment
description: rmp #282 (group prefix join, done) + #283 (regex-vs-named empty-segment, BLOCKED - coordinator's premise found factually wrong)
type: project
---

2026-09-26, sprint 20, two grouped items.

## #282 — done: specification/groups.md new section 11 (items 41-44) + static-files.md rule 2 edit

Verified in code first (group.go): `(*Group).Handle` does `g.mux.Handle(method, g.prefix+path, ...)`,
`(*Group).Group` does `g.prefix + prefix`, `(*Group).Mount` does `g.prefix+prefix`, `(*Group).ServeFiles` does
`fullPrefix := g.prefix + prefix` — ALL plain string concatenation, zero normalization, confirmed
PRF-S9-004/008 (`mux.Group("/api/").GET("/users", h)` → literal `/api//users`, no panic). Also confirmed
`Handle`/`addRoute` never validates `%` in patterns — a pattern is arbitrary literal text once past the
wildcard-token/leading-slash checks — so per the coordinator's own conditional, no new Group-specific panic for
`%2f`/`%2F` in a prefix; specified as consistent, ordinary literal pattern text instead (groups.md item 44).

New groups.md section 11 "Prefix and Path Joining" (items 41-44): defines the join (drop exactly one `/` when
left ends with it and right begins with it; otherwise plain concat, never inserting a missing `/`; other
internal `//` untouched; percent-encoded content in a prefix is not special-cased). Existing rules 7, 9, 14, 22
(groups.md) and static-files.md rule 2 were edited in place (numbers unchanged) to cross-reference section 11
instead of asserting raw concatenation — they were flatly describing the old, now-superseded behavior
("without any normalization", "`group.prefix + path`", etc.) and would have contradicted section 11 if left
as-is.

groups.md now ends at requirement 44 (up from 40, see [[task_rmp281_mount_redirect_prefix_spec]] for how it got
to 40).

## #283 — BLOCKED: coordinator's stated premise is factually wrong, needs a decision before spec text is written

Coordinator's ask characterized this as "regex parameter allows empty segment (`[a-z]*` matches `//profile`),
unlike a named parameter, which never matches an empty segment" — i.e. framed as regex-only catching up to
already-correct named-param behavior.

Verified empirically (throwaway `_test.go`, deleted, nothing committed) against tree.go's `getValue` AND
`getValueBacktrack` (`case param:` / `case regexParam:`, both code paths, ~line 770 and ~line 1000): **named
parameters have the exact same behavior as regex parameters today.** `/:id/profile` matched against `//profile`
matches with `id=""`, status 200 — identically to `/{id:[a-z]*}/profile` against the same request. Neither
`case param:` branch has an `end == 0` (empty-segment) guard; both just capture `path[:end]` unconditionally
when the segment scan stops immediately at a `/`. Confirmed via a THIRD probe that a regex requiring at least
one char (`[a-z]+`) correctly rejects `//profile` (404) — so the *regex* mechanism can already express
"non-empty" when the pattern's author asks for it; the *named*-parameter mechanism has no equivalent lever at
all today.

Checked existing spec for a contradiction: routing.md rule 11 ("`/users/:id` ... does not match `/users/`") is
about a DIFFERENT case — a TERMINAL empty remainder (nothing at all follows the trailing `/`), which structurally
never reaches the wildchild switch (a different code branch, the "exact length match" path at tree.go ~line
725, never falls through to the param/regexParam switch). It says nothing about a MID-path empty segment
followed by more path (`//profile`), which is the case in question. No existing routing.md/params.md rule
covers this edge case either way — confirmed by grep for "empty" in both files. So there is no spec text I'd be
contradicting; the issue is purely that the coordinator's factual premise about CURRENT CODE BEHAVIOR is wrong,
which changes the scope of the fix: a "consistency" fix framed as "make regex behave like named already does" is
not available, because named doesn't already do that — it would need to be "make BOTH regex and named parameters
reject an empty segment," a broader (and technically breaking, for named params) change than described.

Reported this back to the coordinator with the evidence above and two lettered options before writing #283's
spec text. Coordinator chose (B) broad — both named and regex parameters reject an empty segment uniformly.

## #283 resolved — routing.md new §12 (97-101) + params.md new §6 (37)

Before writing the fallback-outcome rule (101), empirically verified (not guessed) what `//profile` gets when
the empty-segment rejection applies and no other route matches, using a regex requiring `[a-z]+` as a proxy for
the future rejection (same `break walk`/`goto fail` code path the real fix will use) — confirmed via throwaway
test, deleted, nothing committed:
- Default config (`RedirectTrailingSlash=true`, `RedirectFixedPath=false`), no separate `/profile` route: 404,
  no Location header.
- `RedirectFixedPath=true`, still no separate `/profile` route: still 404 (path.Clean("//profile")="/profile"
  has no handler either).
- `RedirectFixedPath=true` AND a separate `/profile` route exists: 301 → `/profile` (ordinary fixed-path
  redirect, nothing special).
- `RedirectFixedPath=false` (default) with the same extra `/profile` route: 404 (no cleaning without the flag).
- No TSR ever fires in any case (no Location header) — confirmed `path.Clean("//profile") == "/profile"` too,
  via direct `path.Clean` call, not assumed.

routing.md new §12 "Empty Segment Rejection for Named and Regex Parameters" (97-101): rule 97 is the core rule
(applies identically to `param` and `regexParam` node types, empty check runs BEFORE regex evaluation); 98
extends it to any mid-path position, not just trailing; 99 is the explicit "behavior change relative to earlier
releases" + rationale paragraph (bold-flagged); 100 cross-references and distinguishes rule 11's pre-existing
TERMINAL-empty-remainder case (different code path — never reaches the wildchild switch at all — from this
mid-path case); 101 is the verified fallback-outcome rule (TSR/fixed-path/404), with the empirical results above
folded in as worked examples. Existing rules 9, 11, 19 got one-sentence forward-pointer edits (numbers
unchanged) to section 12.

params.md new §6 "Captured Values Are Never Empty" (37): states the resulting global invariant — every `Param`
from ordinary route matching (named, regex, OR catch-all — catch-all's floor is the literal `/` before `*`, per
routing.md rule 16) has non-empty `Value`; notes rule 12 (Lookup's empty-value contract) stays valid as a general
API statement but no longer describes a reachable case.

routing.md now ends at 101 (was 96). params.md now ends at 37 (was 36). Both files verified with a grep sanity
check (line-sequential numbering, no gaps/dupes) after editing — first attempt at adding params.md's rule 37
mistakenly inserted it physically mid-file (right after rule 12, textually before rules 13-36), which would have
made the file's numbers non-monotonic top-to-bottom; caught and moved it to a proper new trailing section before
finishing. Lesson: when appending a single new rule number to a file (not a whole new section), still place it
at the true physical end of the file, never immediately after whichever existing rule it happens to
cross-reference.
