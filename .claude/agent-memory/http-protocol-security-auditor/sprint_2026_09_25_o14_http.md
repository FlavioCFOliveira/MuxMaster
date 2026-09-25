---
name: sprint-2026-09-25-o14-http
description: rmp #272 O-14 HTTP-protocol gap closure — key finding (OPTIONS * bypasses MuxMaster entirely) and test locations
metadata:
  type: project
---

Commit: 586578644f1cbc6e8c18d95d29bd5b8d0248cb5d (2026-09-25, branch feature/20-backlog-clearance). Task: rmp #272, closing the HTTP-protocol part of O-14 (reports/overview/findings.md).

**Key finding — "OPTIONS *" never reaches MuxMaster at all (Go stdlib behaviour, not a MuxMaster defect):**
`net/http.serverHandler.ServeHTTP` (net/http/server.go) intercepts the exact request line `OPTIONS * HTTP/1.1` BEFORE calling the configured `Handler`, substituting Go's own `globalOptionsHandler{}` (sets `Content-Length: 0`, implicit 200, no Allow header) — unless the operator sets `http.Server.DisableGeneralOptionsHandler = true`. This means:
- `mux.Pre` — documented in specification/middleware.md rule 6 as seeing "every request, including requests that result in 404 or 405" — does NOT see this one request form, because `Mux.ServeHTTP` is never invoked.
- `mux.Use`, `mux.GlobalOPTIONS`, `HandleOPTIONS` are equally bypassed.
- Not exploitable as route/method enumeration (the stdlib handler always answers the same fixed empty 200 regardless of registered routes) but IS a real gap in the "Pre sees every request" documentation claim.
- Verified empirically with a raw `http2.Framer`-free raw-TCP test (`TestHPS_O14_OptionsAsterisk`, `harness/hps_o14_wire_test.go`) asserting `preCalled.Load() == false` for `OPTIONS *` and `true` for a follow-up `GET`.

**Why:** Worth knowing before ever proposing MuxMaster-side changes to fix "Pre bypass on OPTIONS *" — there is nothing MuxMaster can do; the interception happens one layer up, in the `*http.Server` itself, before the `Handler` interface is even invoked. The only lever is `http.Server.DisableGeneralOptionsHandler`, which the *operator* sets on their own `*http.Server`, not something MuxMaster's API can influence.

**How to apply:** If a future task asks to "make Pre run for every request including OPTIONS *", point to this stdlib mechanism first — recommend a SECURITY.md/spec documentation note (not a code fix) unless the operator's own `*http.Server` config is in scope.

**New test files (all pass `-race -count=10`):**
- `reports/http-protocol-security-auditor/harness/hps_o14_smuggling_test.go` — `TestHPS_O14_SmugglingVariants`, 13 raw-TCP smuggling variants (RFC 9112-cited per case), isolated server per subtest (same pattern as the rmp #269 flake fix in `TestHPS0010`).
- `reports/http-protocol-security-auditor/harness/hps_o14_wire_test.go` — 5 tests: OPTIONS *, handler-header-CRLF-cannot-split-response (real wire), XFF wire-level CRLF/NUL/control-byte admission + RealIP behaviour (7 byte classes), raw-TCP byte-class map to r.URL.Path (10 classes, includes the %2F "not a bypass" nuance below), Use-middleware-wraps-{404,405,OPTIONS,TSR} regression (spec middleware.md rule 11).
- `reports/http-protocol-security-auditor/harness/h2harness/h2_crlf_test.go` — `TestH2_HeaderValueCRLF_Rejected`, raw `http2.Framer` + `hpack.Encoder` client (bypasses every client-side header validator) confirming the server RST_STREAMs with PROTOCOL_ERROR and the connection survives for a subsequent clean stream.

**Non-obvious gotcha for future harness authors:** `/admin%2Fsecret` reaching a route registered at `/admin/secret` is NOT a path-traversal bypass — it's RFC 3986 §2.1 correctness (%2F is just an alternate encoding of the byte 0x2F). MuxMaster's default `UseRawPath=false` routes on the already-decoded `r.URL.Path`, so this is expected. Don't flag it as a finding; only flag dot-segment collapsing (MuxMaster does NOT path.Clean by default) or double-decoding as real traversal risks.

Escalated as explicitly out of this agent's scope (not restored here, flagged for the owning agent): `TestCORSOriginReflectionAllowAll`(+RawTCP)/`TestCORSAllowCredentialsWithExactOriginsEchoingAtk` (CORS reflection-semantics confirmation — middleware-security-reviewer) and `TestRealIPThrottleBypassScenario` (throttle IP-spoof bypass — dos-resilience-tester/middleware-security-reviewer).
