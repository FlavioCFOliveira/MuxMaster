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
| introspection.md | Lookup, Routes, Walk, RoutePattern (router perspective) |
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

## Structural conventions

- Requirement numbers are sequential across the WHOLE file (not restarted per `##`/`###` section). E.g. performance.md runs 1→45 straight through sections 1–8. Always grep the highest existing number before appending new requirements.
- Every file has a "Scope" section stating what the file covers and what it does not. Update the Scope section's cross-reference list when a file gains a new subject area.
- Active voice, short sentences, no jargon unless defined in README.md glossary.
- No emojis, no decorative characters, no implementation-status flags.
- "Out of scope" statements appear in relevant files and are consolidated in out-of-scope.md.
- README.md's Terminology table is not strictly ordered (alphabetical or otherwise) — new terms are inserted near thematically related existing terms (e.g. FastHandler next to Handler, Request bundle next to Pool).
