# Memory Index — http-protocol-security-auditor

- [Sprint 2026-05-07 findings](sprint_2026_05_07.md) — H-032/033/034/037/044 all REFUTED; HandleFast parity confirmed; JWT/OAuth2 safe
- [Sprint 2026-05-07 v1.2.0 pre-release audit](sprint_2026_05_07_v2.md) — 4 findings (tasks #15-18); 13 classes clean; govulncheck clean; CL.TE is pipelining not smuggling
- [Sprint 2026-09-25 O-14 HTTP gap closure](sprint_2026_09_25_o14_http.md) — OPTIONS * bypasses MuxMaster entirely (stdlib globalOptionsHandler, not a defect); 13 smuggling variants + 5 wire tests + H2 CRLF raw-framer test added
