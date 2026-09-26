---
name: rmp #250/#253 spec-first amendments for Sprint 18 waste-hunt fixes
description: 2026-09-24 changes to performance.md, groups.md, static-files.md, middleware-stdlib.md, README.md for WH-08 (path-copying registration) and WH-04 (shallow request copy); WH-10/#248 required no spec change
type: project
---

Source: `reports/perf-lab-2026-09-24/waste-hunt.md` (WH-01..WH-13), rmp tasks #250 and #253, sprint 18 "Performance and Efficiency Laboratory". Specification-first step only — no Go code touched (see [[project_muxmaster]] for the SPECIFICATION FIRST policy this enforces).

## What changed

1. **performance.md §36-37 (WH-08 / #253).** Registration copy-on-write changed from "deep-clone the whole method tree" to path copying: only nodes on the insertion path are copied, untouched subtrees are shared between old and new published trees (safe because published nodes are never mutated), atomic publish + full panic rollback unchanged, `maxParams` now updated incrementally per node on the copied path instead of a whole-tree walk after insertion.
2. **groups.md §23 (Mount), static-files.md new item 15 (ServeFiles/Group.ServeFiles), middleware-stdlib.md §52 (StripSlashes) and §56 (CleanPath) (WH-04 / #250).** All five sites now documented as using a "shallow request copy" — new `*http.Request` sharing the original's header map and context, with a fresh `*url.URL` — instead of `r.Clone(ctx)` (Mount/ServeFiles, already accurate in code but not stated in spec) or "in place" mutation (StripSlashes/CleanPath — the OLD spec text was already wrong: the code cloned via `r.Clone`, the spec said "in place"; this is now corrected to the NEW, more efficient shallow-copy mechanism at once).
3. **README.md Terminology table.** Added a new term, **"Shallow request copy"**, next to "Request bundle" (thematically related, both about request representations), so the four affected files reference one definition instead of repeating it. Explicitly distinguished from "Request bundle" to avoid confusion (different mechanism, different purpose).
4. **WH-10 + #248 (serveRedirect chain caching + direct byte-identical response writing): NO spec files changed.** Verified by grepping the whole `specification/` corpus for `http.Redirect`, `wrapMiddleware`, "Location header" — the spec documents only *when* a redirect happens and *what status code* (routing.md §4.4/4.5, configuration.md §2.1/2.2/4.1), never the low-level construction (middleware-chain-per-redirect, Location/Content-Type/body bytes). Per the task's own instruction ("if the spec describes redirect construction, update it; otherwise add nothing"), nothing was added.

## Drift found and deliberately NOT fixed (flagged to user, out of scope for #250/#253)

- **middleware.md §11** ("Global middleware does not wrap ... the TSR/fixed-path redirects") appears to contradict the actual `serveRedirect` implementation in `mux.go`, which reads `m.redirectMWPtr` — a live snapshot of `Use()`-registered global middleware — and wraps the redirect handler with it when non-empty. This is a real behavior/spec mismatch, but is not one of WH-08/WH-04/WH-10 and the task said "do not touch any other spec drift." Needs its own decision: does the spec need updating to say global middleware DOES wrap redirects, or does the code need to stop doing so?
- **SECURITY.md vs performance.md §37**: the waste-hunt report (line ~431) says SECURITY.md's MM-2026-0033 entry states the tree "may be inconsistent after a registration panic," while performance.md §37 specifies full rollback. SECURITY.md is outside `/specification` (not my authority) — flagged for the user/adr-guardian, not resolved here.

## Terminology convention established

"Shallow request copy" is now the standard term (README.md glossary) for the `*http.Request`-struct-copy + fresh-`*url.URL` technique (the `net/http.StripPrefix` pattern). Use this exact term, not "clone" or "in place," for any future spec describing this mechanism, and cross-reference the README glossary entry rather than re-explaining the mechanism inline.
