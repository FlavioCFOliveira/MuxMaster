---
name: Specification structure, terminology, and conventions
description: File inventory, key terminology decisions, and structural conventions used in the MuxMaster specification corpus
type: project
---

The specification lives at `/Users/flaviocfo/dev/github.com/FlavioCFOliveira/MuxMaster/specification/`. It is the sole source of truth. SPEC.md at the project root is a superseded draft and must not be edited.

**Why:** The /specification directory was created 2026-04-16 to replace SPEC.md, which was a working draft in Portuguese. The new specification is in English and structured for both human and AI agent readers.

**How to apply:** All future specification work targets the /specification directory only. Never treat SPEC.md as authoritative.

## File inventory (14 files)

| File | Primary subject |
|---|---|
| README.md | Master index, terminology glossary, design principles |
| routing.md | Path syntax, methods, registration, matching, precedence, panics |
| params.md | Param/Params types, helpers, RoutePattern, pool behavior |
| middleware.md | Middleware type, four scopes, execution order |
| groups.md | Group, Route (inline), Mount, sub-groups |
| error-handling.md | NotFound, MethodNotAllowed, PanicHandler, GlobalOPTIONS, HandlerFuncE, HTTPError, ErrorHandler |
| configuration.md | All Mux fields, defaults, behavior when toggled |
| introspection.md | Lookup, Routes, Walk, RoutePattern (router perspective) |
| static-files.md | ServeFiles behavior and constraints |
| response-helpers.md | JSON, XML, Text, Redirect, NoContent |
| middleware-stdlib.md | All middleware in muxmaster/middleware sub-package |
| compatibility.md | Go version, net/http ecosystem, what is incompatible |
| performance.md | Targets, methodology, what affects performance |
| out-of-scope.md | Features that will never be implemented |

## Terminology decisions (from README.md glossary)

- "Router" = the `*Mux` value. Also "the mux".
- "Route" = the combination of method + pattern + handler.
- "Pattern" = the path string used at registration (may contain wildcards).
- "Handler" = any `http.Handler` or `http.HandlerFunc`.
- "Middleware" = `func(http.Handler) http.Handler`. Never any other type.
- "Segment" = slash-delimited path component.
- "Named parameter" = `:name` syntax. Captures one non-slash segment.
- "Catch-all parameter" = `*name` syntax. Captures rest of path including slashes.
- "Regex parameter" = `{name:expr}` syntax. Validates and captures one segment.
- "Registration time" = moment Handle/HandleFunc is called. Middleware applied here.
- "Request time" = moment ServeHTTP is called.
- "TSR" = Trailing Slash Redirect.
- "Fixed path" = path.Clean result that differs from original but has a handler.

## Structural conventions

- Requirements are numbered sequentially within each file, starting at 1 per section.
- Every file has a "Scope" section stating what the file covers and what it does not.
- Active voice, short sentences, no jargon unless defined in README.md glossary.
- No emojis, no decorative characters, no implementation-status flags.
- "Out of scope" statements appear in relevant files and are consolidated in out-of-scope.md.
