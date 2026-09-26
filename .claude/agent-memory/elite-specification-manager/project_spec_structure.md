---
name: Specification structure, terminology, and conventions
description: File inventory, key terminology decisions, and structural conventions used in the MuxMaster specification corpus
type: project
---

The specification lives at `specification/` (repo root). It is the sole source of truth. SPEC.md at the project root is a superseded draft and must not be edited. (Note: this file previously hardcoded a machine-specific absolute path — the repo is checked out at different absolute paths on different machines/sessions; always resolve relative to the repo root.)

**Why:** The /specification directory was created 2026-04-16 to replace SPEC.md, which was a working draft in Portuguese. The new specification is in English and structured for both human and AI agent readers.

**How to apply:** All future specification work targets the /specification directory only. Never treat SPEC.md as authoritative.

## File inventory (14 files, unchanged as of 2026-07-14)

| File | Primary subject |
|---|---|
| README.md | Master index, terminology glossary, design principles |
| routing.md | Path syntax, methods, registration, matching, precedence, panics |
| params.md | Param/Params types, helpers, RoutePattern, internal parameter storage and pooling (stack buffer, tiered bundle, PoolFastParams/PoolRequestBundle cross-refs) |
| middleware.md | Middleware type, four `http.Handler` scopes, execution order, FastMiddleware (section 6), Pre/Use/UseFast route-type coverage matrix (section 7) |
| groups.md | Group, Route (inline), Mount, sub-groups |
| error-handling.md | NotFound, MethodNotAllowed, PanicHandler, GlobalOPTIONS, HandlerFuncE, HTTPError, ErrorHandler |
| configuration.md | All Mux fields incl. PoolFastParams/PoolRequestBundle (4.5/4.6), defaults, behavior when toggled, configuration-snapshot + Rebuild() semantics (section 5) |
| introspection.md | Lookup, Routes, Walk, WalkFast, RoutePattern (router perspective) |
| static-files.md | ServeFiles behavior and constraints |
| response-helpers.md | JSON, XML, Text, Redirect, NoContent |
| middleware-stdlib.md | All middleware in muxmaster/middleware sub-package |
| compatibility.md | Go version, net/http ecosystem, what is incompatible |
| performance.md | Targets, methodology, what affects performance, FastHandler dispatch (6), lock-free dispatch (7), tiered request bundle (8) |
| out-of-scope.md | Features that will never be implemented |

See [[task_kg2026001_fastpath_spec]] for the 2026-07-14 change that added the FastHandler/pooling/Rebuild content above.

## Terminology decisions (from README.md glossary)

- "Router" = the `*Mux` value. Also "the mux".
- "Route" = the combination of method + pattern + handler.
- "Pattern" = the path string used at registration (may contain wildcards).
- "Handler" = any `http.Handler` or `http.HandlerFunc`.
- "FastHandler" = `func(http.ResponseWriter, *http.Request, Params)`, registered via `HandleFast`/`...Fast`. Params arrive as a direct argument, never via context.
- "Middleware" = `func(http.Handler) http.Handler`. Never any other type.
- "FastMiddleware" = `func(FastHandler) FastHandler`, registered via `UseFast`. Wraps `HandleFast` routes only — structurally incompatible with stdlib middleware.
- "Segment" = slash-delimited path component.
- "Named parameter" = `:name` syntax. Captures one non-slash segment.
- "Catch-all parameter" = `*name` syntax. Captures rest of path including slashes.
- "Regex parameter" = `{name:expr}` syntax. Validates and captures one segment.
- "Registration time" = moment Handle/HandleFunc is called. Middleware applied here.
- "Request time" = moment ServeHTTP is called.
- "TSR" = Trailing Slash Redirect.
- "Fixed path" = path.Clean result that differs from original but has a handler.
- "Request bundle" = the single fused allocation (context wrapper + `*http.Request` copy) for parameterized `Handle` routes. Tiered by param count: `reqBundle1`/`reqBundle2`/`reqBundle` (384/416/480 B GC size classes).
- "Configuration snapshot" = one-time immutable copy of Mux config fields taken on first `ServeHTTP`/first need; reset via `Rebuild()`.
- "Shallow request copy" (added 2026-09-24, [[task_rmp250_253_perflab_spec]]) = a new `*http.Request` struct-copy with a fresh `*url.URL` (net/http.StripPrefix technique); shares header map and context with the original; original never mutated. Used by Mount, ServeFiles/Group.ServeFiles, StripSlashes, CleanPath — always cross-reference the README glossary entry rather than re-describing the mechanism inline. Distinct from "request bundle."

## Current highest requirement number per file (updated 2026-09-26, see [[task_rmp297_documentation_truth_pass]])

Grep `^[0-9]\+\.` before appending to any of these — do not trust an older number from memory. **Before inserting a new rule mid-file, always renumber by POSITION (walk `^(\d+)\. ` lines in file order, reassign 1..N), never by "bump every value ≥ N" — the latter can silently collide with an existing duplicate created by the insertion itself** (see [[task_rmp297_documentation_truth_pass]] for the exact incident in performance.md).

- routing.md: **109** (fully renumbered 2026-09-26 — §8 gained rule 81, the backslash-percent-encoding fact (rmp #279); everything from old §9 onward shifted +1, i.e. §9 QUERY is now 83-90, §10 Asterisk-Form is 91-94, §11 Unclosed Brace is 95-97, §12 Empty Segment is 98-102, §13 Optional Param Limits is 103-105, §14 Regex Name Length is 106, §15 HEAD≠GET is 107-109 — see [[task_rmp297_documentation_truth_pass]]. Old numbers 82-108 from before 2026-09-26 are STALE.)
- params.md: 37 (added §6 Captured Values Are Never Empty, item 37 — see [[task_sprint20_282_283_join_and_empty_segment]])
- middleware.md: 43 (rule 43 text corrected in place, no new max — see [[task_rmp288_spec_code_drift_batch]])
- compatibility.md: **20** (fully renumbered 2026-09-26 — new rule 14, the nil-context-fallback fact (rmp #292), inserted into §2.5; §4-6 shifted +1 — see [[task_rmp297_documentation_truth_pass]]. Old numbers 14-19 from before 2026-09-26 are STALE.)
- middleware-stdlib.md: **111** (§18 APIKey [79-83], §19 JWTAuth [84-94], §20 OAuth2Introspect [95-104], §21 ThrottlePerIP/ThrottlePerIPCapped [105-111] added 2026-09-26 — see [[task_rmp297_documentation_truth_pass]]. Rules 1-78 unchanged from the 2026-09-25 renumbering.)
- groups.md: 44 (§8 Location Rewriting for Automatic Redirects Through Mount [31-35], §9 Mount Prefix Validation [36-37], §10 RawPath Propagation Through Mount [38-40] — see [[task_rmp281_mount_redirect_prefix_spec]]; §11 Prefix and Path Joining [41-44] — see [[task_sprint20_282_283_join_and_empty_segment]])
- performance.md: **46** (fully renumbered 2026-09-26 — new rule 4 in §1 sourcing the Measured/External split from `reports/perf-lab-2026-09-26-docs/README.md`; everything after shifted +1 — see [[task_rmp297_documentation_truth_pass]]. Old numbers 4-45 from before 2026-09-26 are STALE.)
- error-handling.md: 39
- configuration.md: **45** (fully renumbered 2026-09-26 — new rule 30 in §4.4 documenting the PRF-2026-0002 `UseRawPath+UnescapePathValues` warning; §4.5 onward shifted +1 — see [[task_rmp297_documentation_truth_pass]]. Old numbers 30-44 from before 2026-09-26 are STALE. Also: rule 1 and the RedirectFixedPath/UnescapePathValues tables were corrected — both actually default to `false` via `New()`, not `true`.)
- introspection.md: 31 (was 29 — added rule 4 in §1 [Lookup vs FastHandler-nil-handler + lock clarification] and rule 26 in new "§4 Walk and WalkFast" — see [[task_rmp297_documentation_truth_pass]])
- out-of-scope.md: no numbered rules (prose sections only); §2.6 and §5 updated 2026-09-26

## Structural conventions

- Requirement numbers are sequential across the WHOLE file (not restarted per `##`/`###` section). E.g. performance.md runs 1→45 straight through sections 1–8. Always grep the highest existing number before appending new requirements.
- Every file has a "Scope" section stating what the file covers and what it does not. Update the Scope section's cross-reference list when a file gains a new subject area.
- Active voice, short sentences, no jargon unless defined in README.md glossary.
- No emojis, no decorative characters, no implementation-status flags.
- "Out of scope" statements appear in relevant files and are consolidated in out-of-scope.md.
- README.md's Terminology table is not strictly ordered (alphabetical or otherwise) — new terms are inserted near thematically related existing terms (e.g. FastHandler next to Handler, Request bundle next to Pool).
