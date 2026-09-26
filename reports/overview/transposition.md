# MuxMaster — Cross-Ecosystem Vulnerability Transposition

**Date:** 2026-04-17
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Living table — scanned before every release against last 90 days of GHSA/NVD

---

## Purpose

For each attack vector known in another ecosystem, we ask: **does an equivalent structural exist in MuxMaster?** This table is the primary source for generating zero-day hypotheses — attackers are creative, and transpositions work because the same mental model errors persist across implementations.

---

## 1. nginx / Apache (classic HTTP servers)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| Off-by-slash trailing rewrite (nginx alias) | classic | `RedirectTrailingSlash` — redirect to `/foo/` when only `/foo/` exists; but construction via `r.URL.Path + "/"`/`[:len-1]` may be problematic with `//` | **RISK** — path-routing + http-protocol |
| `mod_rewrite` normalization bypass — CVE-2021-41773 (Apache 2.4.49) | Apache Alias normalizes 2x | `clean_path` middleware — **single pass** of `path.Clean` | **RISK** — C2.2 in attack tree; path-routing |
| CVE-2021-42013 (Apache follow-up) | continuation of 41773 | same, aggravated by mod_cgi | **RISK** — path-routing + docs warning |
| nginx merge_slashes = off leak | nginx config | MuxMaster no explicit option; `//admin` treated as literal path with empty segment | **INVESTIGATE** — path-routing differential |
| CVE-2022-41741 (nginx mp4 module) | memory corruption | N/A — MuxMaster does not parse media | **N/A** |
| CVE-2023-44487 Rapid Reset | nginx/haproxy HTTP/2 | Go stdlib `net/http2` — confirm `govulncheck` + patch level | **PASS** presumed; http-protocol to verify |
| nginx X-Forwarded-For chain pollution | classic | `real_ip.go` — no trusted-proxy list, total trust | **CONFIRMED** vulnerable; middleware |
| nginx auth_request race | TOCTOU | registration race in MuxMaster — docs say unsupported | **ACCEPTED** with docs |

## 2. Rails (Ruby) / Sinatra

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| Mass-assignment | Rails | N/A — Go typing eliminates | **N/A** |
| Strong parameters bypass | Rails | N/A | **N/A** |
| CVE-2019-5420 dev mode secret | Rails | N/A (no framework secrets) | **N/A** |
| CVE-2022-23633 body leak after render | Rails | `compress.go` accumulates buffer before flush — leak cross-request if buffer reused? | **INVESTIGATE** — middleware |
| Sinatra regex in routes (ReDoS) | Ruby regex | `regexParam` via `regexp.Compile`; Go RE2 safe vs backtracking, but memory not | **INVESTIGATE** — fuzzing + dos |
| Session fixation (session adoption) | Rails | N/A (handler layer) | **N/A** |

## 3. Express / Koa / Node.js

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| Prototype pollution | Node | N/A — Go typing | **N/A** |
| qs parser DoS (CVE-2017-1000048) | Express/qs | MuxMaster does not parse query | **N/A** |
| Express path-to-regexp ReDoS (CVE-2024-45296, -52798, -45590) | `:param(pattern)` | MuxMaster `{name:pattern}` via regexp.Compile — Go RE2 not backtracking but compile cost scales in crafted patterns | **INVESTIGATE** — fuzzing |
| body-parser DoS | Express | N/A | **N/A** |
| CORS package wildcard bug | `cors` npm | `cors.go` — verify allowAll + reflection | **RISK** — middleware |
| cookie-parser CRLF | npm | `set_header`, `request_id` — verify | **RISK** — middleware + http-protocol |
| CVE-2024-28849 follow-redirect auth header leak | follow-redirects | MuxMaster does not follow cross-host redirect (only serves); N/A | **N/A** |
| Express session fixation | express-session | N/A | **N/A** |

## 4. Spring (Java)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2022-22963 (Spring Cloud) EL injection | SpEL | N/A — MuxMaster no EL | **N/A** |
| CVE-2022-22965 Spring4Shell | class binding | N/A | **N/A** |
| Spring path traversal via ant patterns | `/**` ambiguity | MuxMaster `*filepath` — confirm catch-all vs traversal | **RISK** — path-routing |
| Spring actuator exposed (info leak) | default config | introspection.go — if `Routes()` used for public endpoint | **RISK** — middleware-review |

## 5. Traefik / Caddy

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2022-46153 Traefik path-traversal middleware chain | middleware order | MuxMaster: `clean_path` must run **before** routing decisions; does not run by default | **RISK** — middleware + docs |
| CVE-2024-45410 Traefik XFF pollution | real_ip | `real_ip.go` trust total | **CONFIRMED** — middleware |
| Caddy reverse-proxy header trust | classic | MuxMaster: `real_ip` — same class | **CONFIRMED** — middleware |
| Caddy CORS reflection (historical) | config footgun | `cors.go` allowAll mode | **RISK** — middleware |

## 6. Kong / Tyk / Envoy (API gateways)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| Kong CVE-2023-22459 DoS | cache storm | N/A (MuxMaster no cache) | **N/A** |
| Envoy HTTP/2 smuggling | H2 framing | Go stdlib responsibility | **govulncheck** |
| Envoy header case-folding divergence | proxy | MuxMaster dispatches method case-sensitive — proxy may fold → mismatch? | **INVESTIGATE** — http-protocol |
| Kong JWT algorithm confusion | auth lib | N/A (no JWT middleware) | **N/A** |
| Envoy PROXY protocol spoof | proto | N/A (stdlib) | **N/A** |

## 7. Werkzeug / Flask (Python)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2020-28724 Werkzeug URL ambiguity | parsing | Go `net/url` stricter; still verify with `%00`, `\0x7f`, high ASCII | **INVESTIGATE** — path-routing + fuzzing |
| Werkzeug debug PIN bypass | dev endpoint | N/A | **N/A** |
| Werkzeug session cookie desync | framework | N/A | **N/A** |
| Flask send_file traversal (CVE-2024-34069) | file serve | `ServeFiles` — delegated to http.FileServer; confirm boundary | **INVESTIGATE** — path-routing + middleware |

## 8. fasthttp / Fiber (alternative Go stack — for our own ecosystem)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| fasthttp linear route scan — N/A | design | MuxMaster uses radix | **N/A** |
| fiber Ctx.Params case-sensitivity | fiber | MuxMaster PathParam case-sensitive keys | **PASS** by design |
| fasthttp smuggling (older versions) | framing | N/A (Go stdlib) | **N/A** |

## 9. Gorilla mux / httprouter / chi / bunrouter (direct competitors)

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| gorilla/mux regex ReDoS (not public CVE, known) | regex routes | MuxMaster has regex params, Go RE2 safe vs backtracking, but memory not | **PASS** (RE2) / **INVESTIGATE** cost |
| httprouter trailing method check — not applicable in muxmaster design | — | MuxMaster matches methods via idx array | **N/A** |
| chi path ambiguity with catchall + param | chi | reproduce: `/a/:b` + `/a/*c` conflict | **INVESTIGATE** — path-routing differential |
| bunrouter RawPath divergence | bun | MuxMaster `UseRawPath` opt; `Mount` uses RawPath — verify | **RISK** — path-routing |
| chi Middleware.With race (historical) | chi | MuxMaster `r.With` / `r.Use` not thread-safe post-serve | **ACCEPTED** docs |
| httprouter NotFound/MethodNotAllowed assignment race | post-serve | MuxMaster same — `NotFound` is plain field | **RISK** — concurrency |

## 10. Netty / Java HTTP servers

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2024-27316 CONTINUATION flood (Netty, Apache httpd) | HTTP/2 | Go `net/http2` — `govulncheck` | **PASS** assumed; verify |
| CVE-2021-43797 Netty header smuggling | H1 parsing | Go `net/http` normalised; verify + differential | **INVESTIGATE** — http-protocol |
| Netty ByteBuf leak | alloc | N/A (Go GC) | **N/A** |
| HPACK bomb (CVE-2023-44487 family) | H2 | stdlib | **verify** |

## 11. IIS (Windows) / .NET

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| IIS short-filename bypass | Windows | N/A | **N/A** |
| IIS URL double-decoding | feature | `UnescapePathValues` + `clean_path` double-clean concern | **RISK** — path-routing |
| .NET serialization RCE | ViewState | N/A | **N/A** |
| ASP.NET Core CORS reflection | framework | cors.go | **RISK** — middleware |

## 12. Classic web vulns (framework-agnostic)

| Vector | Where it hits | MuxMaster | Status |
|---|---|---|---|
| HTTP response splitting (CRLF) | any header set with user input | `logger`, `request_id`, `cors`, `set_header`, redirect Location | **RISK** — http-protocol (multiple vectors) |
| HTTP Parameter Pollution | duplicate params | MuxMaster does not parse query; handler responsibility | **N/A** for router; document |
| Host header injection | Host routing | MuxMaster routes by path only; Host unused | **PASS** |
| Open redirect | Location construction | `RedirectTrailingSlash`, `RedirectFixedPath`, `response.Redirect` | **RISK** — http-protocol |
| XSS via reflected bytes | content-type | MuxMaster router does not write HTML; JSON/XML helpers use stdlib encoders | **PASS** for router; caller responsibility |
| SSRF | outbound HTTP | MuxMaster is inbound only | **N/A** |
| XXE | XML parsing | `response.XML` uses `encoding/xml` Marshal only (not Unmarshal); safe | **PASS** |
| SQL injection | DB | N/A | **N/A** |
| Command injection | exec | N/A | **N/A** |
| Timing SCA | cache/side-channel | `basic_auth` user-enum | **CONFIRMED** — timing |

## 13. HTTP/2 / HTTP/3 specific

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2023-44487 Rapid Reset | h2 | Go stdlib `golang.org/x/net/http2` fixed in Go 1.21.3+ | **PASS** (1.26); verify |
| CVE-2024-27316 CONTINUATION flood | h2 | stdlib | **verify** |
| HPACK bombing | h2 dynamic table | stdlib | **verify** |
| H2 → H1 downgrade smuggling | proxy | Go stdlib fronts; middleware-agnostic; but ensure MuxMaster path not assumes H1 | **INVESTIGATE** — http-protocol |
| HTTP/3 (QUIC) vulns | out of scope | N/A | **N/A** |

## 14. Go-specific supply-chain / stdlib

| Vector / CVE | Origin | Equivalent MuxMaster | Status |
|---|---|---|---|
| CVE-2024-24785 html/template | template | N/A (not used by router) | **N/A** |
| CVE-2024-24789 archive/zip | zip | N/A | **N/A** |
| CVE-2023-29403 crypto path | crypto | only `subtle`, `rand` used — verify | **PASS** assumed |
| CVE-2024-34155 parser eval regexp | syntax | Go regexp used for regexParam — verify | **verify** |
| Zero-width space in import paths (classic) | go get | N/A | **N/A** |
| go.sum tampering | supply | CI `go mod verify` | **PASS** |

## 15. Crypto / subtle / timing

| Vector | MuxMaster | Status |
|---|---|---|
| bytes.Equal on secret | grep all | **PASS** — basic_auth uses `subtle.ConstantTimeCompare`; BUT user lookup before is non-const-time | **RISK** timing |
| HMAC early exit | N/A (no HMAC middleware) | **N/A** |
| Padding oracle | N/A | **N/A** |
| math/rand for tokens | request_id uses crypto/rand | **PASS** |

## 16. Go concurrency patterns transposed

| Bug pattern | MuxMaster locus | Status |
|---|---|---|
| Closure captures loop variable (pre-1.22) | all for ranges | **PASS** (Go 1.26) |
| sync.Map misuse | not used | **N/A** |
| Unprotected map read/write | `treesPtr.Load` atomic vs `Handle` mutates nodes | **RISK** — if concurrent registration; **accepted** with docs |
| Channel leak via select default | throttle.go | **RISK** — channel closed vs panic flow |
| Context value with string key | with_value.go accepts `any` — caller decides | **WARNING** docs |
| Goroutine spawned in handler without wg | not done by router; caller responsibility | **N/A** |

## 17. Zero-day raw transposition ideas (seeds for hypotheses.md)

Combinations across-ecosystem that may work in MuxMaster because they combine known classes in novel ways:

1. **nginx CVE-2021-41773 + Traefik CVE-2022-46153**: double-decode en route + middleware chain order — if `clean_path` runs before routing but after hypothetical `UnescapePathValues`, double-decode window exists.
2. **Rails BREACH + Express CORS bug + logger CRLF**: attacker reflects input in 3 places — one in response body (BREACH), one in ACAO, one in log — and measures 3 side channels simultaneously to accelerate extraction.
3. **Werkzeug URL ambiguity + chi `:param` span**: `:id` param routing decision differs between MuxMaster and competitors for `/users/1%2F2` — differential reveals.
4. **Envoy header case-fold + MuxMaster case-sensitive method**: proxy lowercases `GET` → MuxMaster's methodIdx map misses → falls through to `*` tree → Mount handler bypasses method ACL.
5. **HTTP/2 → H1 downgrade + MuxMaster `*` tree**: H2 client sends custom method; proxy downgrades to H1 with uppercase-pretty; downstream MuxMaster routes via `*` tree silently.
6. **fiber fasthttp URL dup + MuxMaster `RedirectFixedPath`**: behavior difference when deployed side-by-side or behind same LB.
7. **Spring EL injection → muxmaster.Error body**: if custom ErrorHandler calls `err.Error()` which formats `%v` of attacker-influenced struct, and struct formatted via `fmt.Sprintf(...Stringer)` that evaluates expression, arbitrary behavior. Requires caller cooperation — edge case.
8. **real_ip CRLF + logger format with IP**: if logger includes RemoteAddr (currently doesn't — but common ask), CRLF-laden XFF creates fake log lines attributed to trusted IP.
9. **Panic in recoverer's stderr write + IDS consuming stderr**: flood stderr to evict real log events from SIEM.
10. **Compression of concat(secret, attacker_reflected) + HTTP/2 flow control timing**: finer-grained BREACH via HTTP/2 window timing.

## 18. Scan cadence

This document is updated:
- **Before every release:** scan GHSA advisories Go + HTTP/router CVEs last 90 days
- **After any high-impact CVE published in adjacent router** (chi, httprouter, gin, echo, fiber, bunrouter)
- **After publication of research PortSwigger / Black Hat / DEF CON** in routing bypass or HTTP smuggling

## 19. Next scheduled scans

- 2026-04-17 (today) — initial scan — complete above
- 2026-07-17 — 90-day rescan pre-v1.1
- Punctually after GHSA-go-* advisory new
