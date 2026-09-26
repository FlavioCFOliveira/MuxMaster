---
name: rmp task 261 — QUERY verb specification (sprint 19)
description: 2026-09-25 spec-only addition of the RFC 10008 QUERY HTTP method — files touched, decisions, and the code-side gap this creates
type: project
---

Added first-class specification support for the HTTP `QUERY` method (RFC 10008, June 2026, https://www.rfc-editor.org/rfc/rfc10008.html) for rmp task #261 / sprint 19 "Add QUERY verb". Spec-only change — no code, no other docs. Code does not implement any of this yet; this is a SPECIFICATION FIRST addition per CLAUDE.md §workflow.

**Why:** RFC 10008 standardizes a safe, idempotent, cacheable HTTP method that carries request content (a GET-with-body use case). Go 1.27's `net/http` has no `MethodQuery` constant (golang/go#80058), so MuxMaster defines its own `muxmaster.MethodQuery = "QUERY"`.

**How to apply:** Before implementing QUERY support in code, read routing.md section 9 (the full QUERY semantics section) plus the touch points listed below. The go-developer agent doing the implementation should treat routing.md §9 items 82-89 as the acceptance criteria.

## Files changed and what was added

- **routing.md** — the primary target. Table in item 30 (§2.1) gained a QUERY row. Item 32 (§2.2, `ANY`) now includes QUERY in its method list. Items 53/57 (§4.4/§4.5 redirect codes) gained QUERY-specific notes: QUERY still gets 307 by default despite being safe/idempotent — the historical POST→GET 301/302 client rewrite exception is moot because MuxMaster never uses 301/302 for QUERY by default. Item 58 (§4.6 OPTIONS) and item 61 (§4.7 Allow header) now cross-reference/state the Allow-header method order explicitly for the first time: `GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY`, then `OPTIONS` last — this order was already true in code (`mux.go` `allowTable`/`methodNames`) but had never been written into the spec before this task. New **section 9 "QUERY Method Semantics"** appended at the end (items 82-89, following the file's established append-only convention — see [[project_spec_structure]]): standard-not-custom method status, the `MethodQuery` constant, safe/idempotent/cacheable framing, the informative note that MuxMaster does NOT validate Content-Type or body of QUERY requests (that's RFC 10008 §2/§2.1's 400/415/422 handling — the registered handler's job), `Accept-Query` is application-set, and QUERY is not CORS-safelisted (browser preflight).
- **groups.md** item 8 — added QUERY to the `*Group` registration-method list, plus a new clarifying sentence: `*Group` has `HandleE`/`...E` convenience methods (now including `QUERYE`) but has **no** `...Fast` convenience methods at all (confirmed by reading group.go — only `*Mux` has `GETFast` etc.); a fast route on a group must go through `(*Group).HandleFast(method, ...)` directly.
- **error-handling.md** item 25 — added `QUERYE` to the `...E` convenience family list. Item 8 — added a cross-reference to routing.md's new Allow-header order rule.
- **performance.md** item 26 — added `QUERYFast` to the `*Mux`-only Fast convenience method list, with an explicit note that `*Group` has no Fast convenience methods (see groups.md finding above).
- **configuration.md** item 18 (§4.1 RedirectCode) — added a QUERY cross-reference note.
- **compatibility.md** — new **section 6 "HTTP Method Constants Not Yet in the Standard Library"** (items 17-19, appended, zero renumbering): documents the `muxmaster.MethodQuery` constant, the golang/go#80058 gap, and the forward-compatibility guarantee if Go ever adds `http.MethodQuery`.

## Key decisions (given by the user, not made unilaterally)

1. `QUERY` convenience methods: `*Mux` gets `QUERY`, `QUERYE`, `QUERYFast`; `*Group` gets `QUERY`, `QUERYE` only (no `QUERYFast` — matches existing Group/Mux asymmetry).
2. `ANY` registers QUERY.
3. Allow header order: `...TRACE, QUERY`, then `OPTIONS` last — this is the first time the spec states Allow-header ordering explicitly for ANY method, not just QUERY; it was reverse-engineered from `mux.go`'s `allowTable` construction (verified: loop skips `idxOPTIONS`, appends OPTIONS after the loop) and is accurate as of 2026-09-25.
4. QUERY still gets the default 307 redirect (not 301/302) despite being safe/idempotent.
5. Router does not validate QUERY's Content-Type/body — informative-only note, no new behavioral requirement on the router itself.

## Pre-existing issue found, NOT fixed (per explicit instruction)

`RegisterMethod`/custom-method support (routing.md §2.3, items 34-36) is specified but **not implemented in code** — confirmed via [[project_implementation_status]] (no `RegisterMethod` function found via grep as of 2026-07-14) and re-confirmed by grep during this task. QUERY was deliberately kept OUT of that custom-method machinery (it's a standard method with its own convenience methods, not something requiring `RegisterMethod`), so this task does not touch or worsen that gap — it is simply left as-is and re-flagged here for whoever picks up the RegisterMethod implementation work.

## Numbering state after this task (supersedes the 2026-09-25 line in [[project_spec_structure]] for these two files)

- routing.md: **89** (was 81; added §9 QUERY Method Semantics, items 82-89)
- compatibility.md: **19** (was 16; added §6 HTTP Method Constants, items 17-19)
- groups.md, error-handling.md, performance.md, configuration.md: item counts unchanged (30, 39, 45, 44 respectively) — only existing items were amended in place, no new items added, so no renumbering was needed there.
