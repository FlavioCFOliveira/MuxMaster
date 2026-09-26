---
name: task_rmp281_mount_redirect_prefix_spec
description: rmp #281 - Mount Location-rewrite spec (done) + Mount prefix validation (blocked on user decision, empirical findings recorded)
type: project
---

2026-09-26, rmp task #281 (CDX/audit #268 defect): `outer.Mount("/v2", inner)` where inner issues its own
TSR/RedirectFixedPath redirect loses the mount prefix in `Location` (redirects the client outside the mount).

## Done
groups.md new section 8 "Location Rewriting for Automatic Redirects Through Mount", items 31-35 (file was at
max 30 before this task). Specifies: outer Mux rewrites Location to prepend the full mount-prefix chain
(recursive for nested mounts), only for the inner handler's own automatic redirects (TSR/fixed-path), never
for application-issued redirects or non-`*Mux` mounted handlers (e.g. http.FileServer). Applies via
`(*Group).Mount` too, even when group middleware wraps the handler.

## Blocked — needs user decision before touching mux.go's `mountAt` prefix validation

Empirically reproduced (throwaway `_test.go`, deleted after, never committed) the exact reported panic:
`mux.Mount("/v2{/:id}", inner)` → `panic: muxmaster: '' in path '/v2/:id/*mux_mount' conflicts with existing
wildcard '/*mux_mount'`. Root cause: `{/:id}` (routing.md §1.6 optional parameter) expands into TWO
registrations under Mount's internal `"*"` method tree (`/v2/*mux_mount` and `/v2/:id/*mux_mount`); when the
optional segment is the trailing segment of the prefix, both expansions collide as two different wildcard
kinds (catch-all vs named param) at the exact same tree node — `strings.SplitN(path, "/", 2)[0]` on a path
that still starts with `/` yields the confusing empty-string `''` in the message.

Empirical matrix of what mountAt currently accepts (see [[project_spec_structure]] for file conventions):
- static prefix: OK. named-param prefix (`/v2/:id`, mid or trailing): OK, param capture IS preserved and
  reachable via `PathParam`/`ParamsFromContext` in the mounted handler (verified — not silently dropped).
- regex-param prefix (non-optional): OK.
- catch-all in prefix: already panics with a CLEAR, correct message (routing.md §1.4/rule 67, "catch-all only
  at the end") — not confusing, out of scope.
- unnamed wildcard (`/v2/:`, `/v2/*`), unclosed `{`: already panic with clear, correct existing messages —
  out of scope.
- optional segment (`{/:name}` or `{/:name:expr}`) as the trailing segment of the prefix: PANICS with the
  confusing message above. Optional segment NOT trailing (e.g. `/v2{/:id}/more`): does NOT panic today.

Separate defect found while investigating (NOT part of this task, flagged to user, not fixed): for ANY
Mount prefix containing a wildcard token (named param, regex param, or optional param — trailing or not),
`r.URL.RawPath` is unconditionally zeroed in the mounted handler, because `mountAt`'s `strings.TrimPrefix(r.URL.RawPath, prefix)`
compares against the literal, unexpanded prefix string (e.g. containing literal `{/:id}` or matching only
the no-param branch), which never equals the actual request RawPath. Verified empirically for both
`/v2/:id` (always empty RawPath) and `/v2{/:id}/more` (both expansion branches, always empty RawPath).

## Resolved — coordinator chose option (a), narrow

Coordinator decision: reject only a Mount prefix whose trailing segment is an optional parameter; named/regex-
param prefixes and non-trailing optional segments keep working (no breaking change vs. current behavior).

groups.md gained two more new sections after the Location-rewrite one (8), continuing the same append-only
numbering (file was at max 35 after section 8):

- **Section 9 "Mount Prefix Validation"** (items 36-37): the exact panic — `muxmaster: Mount prefix '<prefix>'
  ends with an optional parameter; Mount does not support an optional parameter as the last element of its
  prefix` — fired when the combined (group+local) prefix's last element, after trailing-`/` trim (req. 25), is
  `{/:name}`/`{/:name:expr}`. Item 37 explicitly scopes the restriction to ONLY the trailing case, preserving
  static/named-param/regex-param/non-trailing-optional prefixes as still valid (matches empirical findings
  above).
- **Section 10 "RawPath Propagation Through a Parameterized Mount Prefix"** (items 38-40): folds in the
  RawPath-zeroing finding. Defines "matched prefix" = `r.URL.Path` minus the `mux_mount`-captured suffix, and a
  segment-by-segment "decode-consistent" check (percent-decode each of the first *k* raw segments of
  `r.URL.RawPath` independently and compare to the matched prefix's segments) — preserve RawPath's tail
  (original encoding intact) when consistent, zero it otherwise (same fallback shape as the pre-existing
  HPS-2026-0001 encoded-`/` case, generalized). Item 39 confirms the static-prefix case is bit-for-bit
  unchanged by this refinement; item 40 states the actual fix (compare against captured segment values, not
  literal `:name`/`{name:expr}` pattern text).
- Requirement 24 (pre-existing, text left in place) got one added cross-reference sentence pointing to section
  10, since 24 never described the truncation/zeroing behavior at all — that was pure code-comment-only
  knowledge (HPS-2026-0001) never previously spec'd; section 10 is now the authoritative source for it.
- Requirement 33 (my own item from the first half of this task) had one clause fixed: "does not change RawPath
  propagation, which continues to follow requirement 24 unmodified" → "...is governed separately by requirement
  24 and section 10 below" (24 alone was no longer an accurate forward-reference once section 10 existed).

groups.md now ends at requirement 40. Nothing else in the file touched. Not committed (per task instructions —
that's the coordinator's/gitflow's job).

## Reconciliation (same day): §10 corrected against the actual (uncommitted) implementation

Coordinator caught that my original §10 text (rule 39: "for a static prefix this is always decode-consistent
… unchanged") was WRONG. Read the real uncommitted `mux.go` (`mountAt`, `rawPathRemainder`,
`endsWithOptionalParam`) plus `mount_redirect_test.go` and `security_test.go`
(`TestMountRawPathNormalisedOnMismatch`, MM-2026-0022) to confirm: the code gates on
`hasParam := strings.ContainsAny(prefix, ":{")` computed once at registration — segment-wise
decode-consistent stripping (`rawPathRemainder`) runs ONLY when the prefix has a parameter token; a purely
static prefix keeps the pre-existing literal `TrimPrefix`-against-the-registered-text algorithm byte-for-byte,
which zeros RawPath on ANY literal mismatch — including when the client percent-encodes a byte of the static
prefix itself (proof case: prefix `/api`, raw target `/%61pi/v1/resource` → decoded path matches `/api` but
literal RawPath bytes don't start with `/api` → zeroed, even though decode-consistent stripping would have
succeeded here).

Rewrote groups.md §10 (renamed from "...Through a Parameterized Mount Prefix" to "...Through Mount", since it
now genuinely covers both cases) keeping ALL THREE numbers 38/39/40 as distinct items (did not drop or merge
any number away — first attempt at the fix collapsed to only two items and had to be corrected back to three):
- **38**: explicitly scoped to "only when the prefix contains a parameter token"; algorithm unchanged from
  before (matched-prefix + segment-wise decode-consistent check).
- **39** (rewritten): the static-prefix case now correctly states it uses the literal `TrimPrefix` algorithm,
  NOT rule 38's decoding, and zeros on any literal mismatch.
- **40** (rewritten): states explicitly, with the `/api` vs `/%61pi/...` worked example, that this is a real
  behavioral divergence between the two rules (not a bug) — a client-encoded static-prefix byte gets RawPath
  zeroed even though it's decode-consistent, because rule 39 deliberately never decodes before comparing.

Lesson for future §10-adjacent work: when a rule's claim depends on "what the code actually gates on", read the
real diff/implementation before asserting it in the spec, even under time pressure to answer a coordinator
follow-up fast — the first draft got this exactly backwards by assuming the new algorithm subsumed the static
case instead of checking the `hasParam` branch split.
