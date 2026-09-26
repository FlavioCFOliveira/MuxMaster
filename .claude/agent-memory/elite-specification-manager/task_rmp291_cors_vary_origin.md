---
name: task_rmp291_cors_vary_origin
description: rmp #291 (sprint 20) - CORS Vary:Origin fix for TM-2026-033, middleware-stdlib.md new section 16 (71-75)
type: project
---

2026-09-26. Defect TM-2026-033: `middleware.CORS` (middleware/cors.go) returns early for a request with no
`Origin` header, before setting any header — no `Vary: Origin`. Also confirmed while reading the code (not
mentioned in the original report, but directly relevant): the wildcard (`AllowedOrigins: ["*"]`) branch NEVER
sets `Vary: Origin` either, even for an Origin-bearing request — only the specific/non-wildcard allow-list
match branch does, via `if existing, ok := h["Vary"]; ok { h["Vary"] = append(existing, "Origin") } else {...}`.
The 400 (invalid/CRLF Origin) and 403 (disallowed origin) responses CORS generates itself also return before
that block, so they get no Vary either. Coordinator's decision (mine, not asked): Vary: Origin on EVERY response
that passes through CORS, including these two self-generated error responses — read literally, not escalated as
a separate question, since "every response" is explicit and unconditional in the coordinator's own wording.

Confirmed no existing dedup helper anywhere in middleware/ for Vary (compress.go's `hdr.Add("Vary",
"Accept-Encoding")` is the same style, no case-insensitive-token check exists there either) — "no duplicate
token" in the ask is a NEW requirement, not something the code already does; specified it as a case-insensitive
substring/token check against every existing Vary value before appending (RFC 9110 §5.1: field names are
case-insensitive tokens — verified via WebFetch against httpwg.org/specs/rfc9110.html, not guessed). Also
verified via WebFetch that Fetch Standard section 8.4 is literally titled "CORS protocol and HTTP caches" (cited
by that exact section number/title in the spec text).

specification/middleware-stdlib.md new section 16 "CORS Vary: Origin (TM-2026-033)" (items 71-75, file's max
was 70, now 75): 71 = the core behavior-change rule (bold-flagged), enumerating every response path (no-Origin
pass-through, wildcard, allow-list match, 400 malformed-Origin, 403 disallowed-origin, simple, preflight) plus
the Fetch Standard §8.4 rationale; 72 = ACAO/Access-Control-* headers unaffected, still absent for a no-Origin
request; 73 = merge rule unchanged (Header.Add-style, separate Vary lines, NOT comma-joined into one line —
matches Compress's existing `Vary: Accept-Encoding` coexisting as ITS OWN line); 74 = the new case-insensitive
dedup-token check (RFC 9110 §5.1); 75 = order-independence (works the same whichever of CORS/Compress runs
first in the Use() chain).

Noted but NOT fixed (out of scope, flagged only): middleware-stdlib.md rule 50 ("An empty AllowedOrigins list
causes CORS to block all cross-origin requests, returns 403") is pre-existing spec/code drift — the actual code
panics at CONSTRUCTION time on empty AllowedOrigins (`middleware: CORS requires a non-empty AllowedOrigins...`),
it does not defer to a runtime 403. Also: section 10 (CORS, rules 46-50) still does not document the 400
(malformed Origin) or 403 (disallowed origin) response paths at all — rule 71 originally cross-referenced
"see section 10" for these but section 10 doesn't actually cover them, so that dangling cross-reference was
removed and the two response shapes are described inline in rule 71 instead, without expanding section 10.

middleware-stdlib.md now ends at requirement 75.
