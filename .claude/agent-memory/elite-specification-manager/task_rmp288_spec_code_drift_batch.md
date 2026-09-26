---
name: task_rmp288_spec_code_drift_batch
description: rmp #288 (sprint 20) - 5-item spec/code drift correction batch, all verified against code at HEAD
type: project
---

2026-09-26. Five independent spec-vs-code drift corrections, each verified against the actual code before writing
(none guessed):

1. **middleware.md rule 43** (HandleFast-after-Use ordering) — rewritten in place (number unchanged). Old text
   wrongly implied a `HandleFast` route registered on a `Mux`/`Group` that already has `Use`-registered stdlib
   middleware succeeds silently, unwrapped. Verified in mux.go `HandleFast` (~566) and group.go `HandleFast`
   (~68): both PANIC (`len(m.middleware) > 0` / `len(g.middleware) > 0` check), with exact messages now quoted
   in the rule (`muxmaster: HandleFast route registered on a Mux/Group with stdlib middleware (Use) — ...`).
   Corrected the auditing-burden closing sentence too: only routes registered BEFORE the relevant `Use` call
   need checking; later ones can't silently slip through, since they panic.

2. **routing.md new §13 "Optional Parameter Expansion Limits"** (items 102-104) — max 8 optional parameters per
   pattern (2^8=256 cap, `countOptionalSegments`/`maxOptionalSegments` in tree.go) and the separate "no two
   consecutive optional segments" rule (`expandOptional`'s `{/:`-adjacency check, PRF-2026-0001, tree.go ~1376).
   Both panic messages quoted verbatim from tree.go. Confirmed the two checks are independent (count-only vs.
   adjacency-only) and neither existed anywhere in routing.md before (rules 24-26, section 1.6, only described
   the happy-path 2-route expansion). Edited rule 25 in place to forward-reference the new section.

3. **routing.md new §14 "Regex Parameter Name Length Limit"** (item 105) — 254-byte max regex-param name
   (tree.go `regexpNameEnd uint8`, PRF-2026-0003), exact panic quoted. Confirmed named-parameter and catch-all
   names have NO such limit (not `uint8`-backed). Edited rule 18 in place to forward-reference.

4. **routing.md new §15 "HEAD Does Not Follow GET"** (items 106-108) — confirmed via code read (mux.go: GET/HEAD
   registered under fully independent `idxGET`/`idxHEAD` slots, ServeFiles explicitly registers BOTH because
   nothing links them) AND empirically (throwaway test, deleted, nothing committed: HEAD against a GET-only
   route → 405, `Allow: GET, OPTIONS`). Verified the Go-stdlib contrast claim via WebFetch against
   go.dev/blog/routing-enhancements rather than trusting the coordinator's assertion outright — confirmed exact
   quote: "As a special case, GET also matches HEAD; all the other methods match exactly." Cited by name/URL.
   Edited rule 30 in place to forward-reference.

5. **middleware-stdlib.md**: rule 50 (CORS, section 10) rewritten in place — was "empty AllowedOrigins → runtime
   403"; actually `CORS()` panics at CONSTRUCTION time on `len(opts.AllowedOrigins)==0`, quoted verbatim. New §17
   "CORS Request-Time Error Responses" (items 76-78) documents the 400 (CRLF/NUL in `Origin`, before any header
   is set) and 403 (origin present, non-wildcard, not in `AllowedOrigins`) responses that DO exist at request
   time — both via `net/http.Error`, bodies `"Bad Request"`/`"Forbidden"` literally (not `http.StatusText`, but
   same output) — cross-referenced against BasicAuth (§9 rule 43) and ThrottleBacklog (§11 rules 53-54), which
   use the identical `net/http.Error` mechanism, for stylistic consistency. Item 78 notes the `else` branch in
   cors.go's 403 check (empty-AllowedOrigins fallback to `next.ServeHTTP`) is dead code given the construction-time
   panic — flagged as such, not hidden.
   Also fixed rule 71 (§16, from the previous CORS Vary:Origin task, [[task_rmp291_cors_vary_origin]]): its
   400/403 parenthetical used to describe the two responses inline with a dangling non-reference; now it just
   points to §17, which fully documents them. This closes the gap [[task_rmp291_cors_vary_origin]]'s memory
   entry flagged as "noted but not fixed."

File maxes after this task: middleware.md 43 (unchanged, in-place edit only), routing.md 108 (was 101),
middleware-stdlib.md 78 (was 75). All re-checked for monotonic, gap-free numbering after editing.
