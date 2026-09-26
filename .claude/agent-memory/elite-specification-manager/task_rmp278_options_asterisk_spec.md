---
name: rmp task 278 part 2 — asterisk-form OPTIONS specification
description: 2026-09-25 spec-only addition documenting RFC 9110 §9.3.7 "OPTIONS *" behavior in both net/http server configurations
type: project
---

Specified the asterisk-form request target (`OPTIONS * HTTP/1.1`, RFC 9110 section 9.3.7) for rmp task #278 part 2. Spec-only — no code changes. The behavior was already verified empirically by tests before this task started: `options_asterisk_test.go` (repo root) and `reports/http-protocol-security-auditor/harness/hps_o14_wire_test.go::TestHPS_O14_OptionsAsterisk`.

**Why:** Asterisk-form requests interact with `Pre` middleware and `GlobalOPTIONS` in a way the spec had never described, and the two possible `net/http` server configurations (`http.Server.DisableGeneralOptionsHandler` true/false) produce entirely different behavior — one where MuxMaster is never invoked at all, one where it is invoked and always falls through to `NotFound`.

**How to apply:** [[project_spec_structure]] now points here for routing.md's current item count (93). Read routing.md section 10 before answering any question about `OPTIONS *`.

## Files changed

- **middleware.md rule 6** (number preserved, text amended in place): added the exception that `net/http`'s default server config intercepts `OPTIONS *` before `Mux.ServeHTTP` runs, so `Pre` does not see it in that config; with `DisableGeneralOptionsHandler=true`, `Pre` does see it, consistent with the general rule. Cross-references new routing.md section 10.
- **routing.md**: appended new **section 10 "Asterisk-Form Request Target (`OPTIONS *`)"**, items 90-93, following the append-only convention (see [[project_spec_structure]]):
  - 90: what the asterisk-form target is (RFC 9110 §9.3.7), `r.URL.Path == "*"`.
  - 91: default server config — `net/http` answers before `Mux.ServeHTTP` is ever called; nothing in MuxMaster runs.
  - 92: `DisableGeneralOptionsHandler=true` — request reaches `ServeHTTP`; walks through why every step of the lookup sequence (rule 47), TSR (§4.4), fixed-path (§4.5), the internal `"*"` **method**-wildcard tree used by `Mount` (rule 31 — explicitly disambiguated from the asterisk-form **path**, since both use the character `*` but are unrelated mechanisms), automatic OPTIONS (§4.6) and 405 (§4.7) all fail to apply; falls through to `NotFound`. Holds for every combination of `HandleOPTIONS`, registered routes, and `GlobalOPTIONS`.
  - 93: `Pre` does run in the `DisableGeneralOptionsHandler=true` case.
  - Also appended non-renumbering cross-reference sentences to existing rule 47 (end of the lookup-sequence step list) and rule 59 (end of §4.6) pointing to section 10.
- **configuration.md**: deliberately NOT edited. Its §2.4 (HandleOPTIONS) and §3.4 (GlobalOPTIONS) sections only cross-reference error-handling.md/routing.md for full behavior and do not themselves describe path-matching mechanics; their existing wording ("for paths that have at least one registered handler") already holds unchanged for the asterisk-form case without amendment. Flagged this reasoning to the user rather than editing preemptively.
- **error-handling.md**: NOT edited — out of the task's explicit scope (only middleware.md, routing.md, and conditionally configuration.md were named). Its GlobalOPTIONS section (§4, rule 19: "applies only to the automatic OPTIONS response triggered by HandleOPTIONS") already implies the asterisk-form non-invocation without contradiction; worth revisiting if the user later wants an explicit cross-reference there too.

## Key disambiguation worth remembering

The internal `"*"` **method**-wildcard tree (routing.md rule 31, used by `Mount`) and the asterisk-form **request-target path** `"*"` (RFC 9110 §9.3.7) are two unrelated mechanisms that happen to share the character `*`. Do not conflate them in future spec work — rule 92's third bullet exists specifically to head off that confusion.
