# HTTP Protocol Security Audit — Pre-release v1.0.0

**Date:** 2026-04-17T13:15:00+01:00
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** `go1.26.2 linux/amd64`
**Auditor:** `http-protocol-security-auditor`
**Harness root:** `/reports/http-protocol-security-auditor/harness/`
**Evidence root:** `/reports/http-protocol-security-auditor/evidence/2026-04-17/`

---

## 1. Scope

### In-scope files (touched by this audit)
- `mux.go` — `ServeHTTP`, `dispatch`, `methodIdx`, `RedirectTrailingSlash`, `RedirectFixedPath`, `HandleOPTIONS`, `HandleMethodNotAllowed`, `allowed`, `cleanedPath`.
- `response.go` — JSON/XML/Text/Redirect helpers.
- `middleware/request_id.go` — X-Request-ID header echo.
- `middleware/cors.go` — Origin reflection.
- `middleware/logger.go` — CLF-style log of `r.URL.Path`.
- `middleware/real_ip.go` — XFF/X-Real-IP ingestion into `r.RemoteAddr`.
- `middleware/set_header.go` — caller-supplied response header.
- `middleware/basic_auth.go` — realm string in `WWW-Authenticate`.

### Threat classes exercised
- HTTP/1.1 request smuggling (CL.TE / TE.CL / TE.TE / obs-fold / whitespace / case-fold / duplicate CL / duplicate TE).
- CRLF / NUL / ANSI / bidi injection in request-target, request headers, response headers, logs.
- Open redirect via `RedirectTrailingSlash`, `RedirectFixedPath`, `path.Clean`.
- Response splitting via `Location` header, `X-Request-ID` reflection, CORS `Access-Control-Allow-Origin` reflection, `SetHeader`.
- Pre-auth information disclosure via TSR / fixed-path / auto-OPTIONS / MethodNotAllowed.
- Method dispatch case sensitivity and whitespace tolerance.
- HTTP/2 smoke (H2 upgrade, Rapid Reset proxy test, header validation).

### Explicitly out-of-scope (noted in coverage gaps)
- HTTP/3 / QUIC (stdlib does not support).
- Full raw HPACK / h2 framer attacks (would need `golang.org/x/net/http2` — module is zero-dep, so we do a weaker stdlib-only smoke test instead).
- Timing-based oracles (owned by `timing-and-sidechannel-analyst`).
- Path-parsing fuzz against competitors (owned by `path-routing-fuzzer`).
- TLS, DNS, resolver — inherited from stdlib.

---

## 2. Methodology

### Baseline environment
```
$ git rev-parse HEAD
533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c
$ go version
go version go1.26.2 linux/amd64
```

### Harness inventory
All harnesses live in `/reports/http-protocol-security-auditor/harness/` and run under `go test` with the race detector enabled:
```
$ go test -race -count=1 ./reports/http-protocol-security-auditor/harness/
ok  github.com/FlavioCFOliveira/MuxMaster/reports/http-protocol-security-auditor/harness  10.1s
```

| Harness file | Targets |
|---|---|
| `rawio_test.go` | helper: raw-TCP exchange + transcript recorder |
| `smuggle_test.go` | 20 HTTP/1.1 smuggling variants |
| `redirect_test.go` | TSR / FixedPath redirect matrix + pre-auth repro |
| `redirect_raw_test.go` | wire-level Location byte analysis |
| `method_dispatch_test.go` | 22-method case/whitespace matrix |
| `cors_test.go` | Origin echo via ServeHTTP and raw TCP |
| `request_id_test.go` | X-Request-ID echo under a dozen byte payloads |
| `logger_test.go` | log injection via percent-decoded path, set_header CRLF |
| `real_ip_test.go` | XFF trust via in-memory ServeHTTP |
| `real_ip_raw_test.go` | XFF trust via raw TCP |
| `options_test.go` | pre-auth OPTIONS / 405 disclosure |
| `options_asterisk_test.go` | `OPTIONS *` and `TRACE` behaviour |
| `h2_test.go` | HTTP/2 smoke + Rapid-Reset surrogate + header validation |
| `url_path_surface_test.go` | byte classes admitted by stdlib into `r.URL.Path` |
| `header_serialization_test.go` | wire-level sanitisation of response headers |
| `repro_test.go` | minimal 1-file reproducers for every finding ≥ Medium |

### Evidence layout
```
evidence/2026-04-17/
  smuggling/*.txt                 — 20 raw-TCP transcripts
  redirect-matrix.csv             — 20 case × 8 column sweep
  redirect-raw-bytes.txt          — wire-level Location capture
  method-dispatch-matrix.csv      — 22-row case matrix
  url-path-surface.txt            — what stdlib admits into r.URL.Path
  header-serialisation.txt        — what Go writes on the wire
  h003-logger-injection.txt       — percent-decoded log injection
  h003-logger-direct.txt          — raw r.URL.Path log injection
  h004-request-id-*.txt           — X-Request-ID reflection
  h005-cors-reflection.txt        — ACAO reflection (in-memory)
  cors-literal_*.txt              — ACAO reflection on the wire
  h007-h020-redirect-fixedpath.txt — open redirect probes
  h009-real-ip-xff.txt            — XFF in-memory
  real-ip-raw-tcp.txt             — XFF on the wire
  h025-tsr-pre-auth.txt           — TSR disclosure
  options-*.txt                   — OPTIONS / 405 pre-auth
  set-header-crlf.txt             — caller-supplied CRLF
  h2-*.txt                        — HTTP/2 smoke
```

### Reproducibility
Every finding has a test function starting with `TestRepro` in `harness/repro_test.go`. To reproduce:
```bash
go test -v -run TestReproHPS ./reports/http-protocol-security-auditor/harness/
```

---

## 3. Findings summary

| ID | Severity | CWE | Component | Summary | Evidence |
|---|---|---|---|---|---|
| HPS-001 | **High** | CWE-117, CWE-150, CWE-93 | `middleware/logger.go:30` | Logger writes percent-decoded `r.URL.Path` raw into the log stream, permitting CRLF log forgery, ANSI escape injection, NUL injection and right-to-left-override spoofing. | `h003-logger-injection.txt`, `h003-logger-direct.txt` |
| HPS-002 | **High** | CWE-200 | `mux.go:491-498` | `RedirectTrailingSlash` emits a 301 before any user middleware runs, disclosing the existence of routes (including admin / authenticated endpoints) to unauthenticated clients. | `h025-tsr-pre-auth.txt` |
| HPS-003 | **High** | CWE-200 | `mux.go:546-565` | Auto-OPTIONS and auto-405 responses run before user middleware, exposing the `Allow:` list (full set of methods) for any known path without authentication. | `options-pre-auth.txt`, `method-not-allowed-pre-auth.txt`, `allow-header-leak.txt` |
| HPS-004 | **Medium** | CWE-345, CWE-290 | `middleware/real_ip.go:12-22` | `RealIP` trusts `X-Forwarded-For` and `X-Real-IP` unconditionally, with no trusted-proxy list, allowing trivial `r.RemoteAddr` spoofing that cascades into any downstream code using it (per-IP throttle, audit log, IP allow-lists). | `h009-real-ip-xff.txt`, `real-ip-raw-tcp.txt`, `h009-throttle-bypass-scenario.txt` |
| HPS-005 | **Medium** | CWE-400 | `middleware/request_id.go:16-23` | Client-supplied `X-Request-ID` is reflected unbounded into the response; we observed 1 MiB echo. Amplifier for egress bandwidth and a client-controlled signal into logs. | `h004-request-id-reflection.txt` |
| HPS-006 | **Medium** | CWE-22 | `clean_path.go` + stdlib | Percent-encoded `%2f` within a path segment is decoded into `r.URL.Path` by stdlib, exposing `path.Clean`-based normalisation (in `clean_path` and `RedirectFixedPath`) to traversal attempts that bypass the raw-path view. | `url-path-surface.txt` |
| HPS-007 | **Low** | CWE-113 (def.-in-depth) | `middleware/set_header.go:9` | `SetHeader` accepts raw CR/LF in caller values; Go's wire serialiser replaces them with spaces (no splitting on wire), but middleware that reads `w.Header().Get` downstream sees the raw bytes. | `set-header-crlf.txt`, `header-serialisation.txt` |
| HPS-008 | **Info** | — | `mux.go` dispatch flow | MuxMaster is 100 % dependent on stdlib `net/http` for framing; no bespoke CR/LF filter exists. All smuggling variants are defended by stdlib. | `smuggling/*.txt` |

No Critical findings. Findings are additive: none are independently RCE / auth-bypass at the router level, but HPS-001 through HPS-004 form a composite reconnaissance/audit-trail-forgery chain that warrants fixes before tagging v1.0.0.

---

## 4. Detailed findings

### HPS-001 — Logger percent-decoded CRLF / ANSI injection (High)

**Location:** `middleware/logger.go:30`

```go
fmt.Fprintf(out, "%s %s %s %d %s\n",
    time.Now().Format(time.RFC3339),
    r.Method,
    r.URL.Path,               // ← unsanitised, percent-decoded
    rec.status,
    time.Since(start),
)
```

**Root cause:** Stdlib rejects literal CR/LF/NUL/ANSI in the request-target (400 Bad Request), but **percent-encoded** control bytes are accepted by `url.Parse`/`http.ReadRequest` and **decoded** into `r.URL.Path`. A request of `GET /%0D%0AFOO HTTP/1.1` produces `r.URL.Path = "/\r\nFOO"`. The logger then calls `fmt.Fprintf("%s %s %s %d %s\n", ..., path, ...)` with that raw value.

**Demonstration (from `evidence/2026-04-17/h003-logger-injection.txt`):**
```
[percent_encoded_crlf] attacker-forged second log line
  target="/abc%0D%0A2099-01-01T00:00:00Z+GET+/fake+200+0s" raw_err=<nil>
  log_bytes="2026-04-17T13:12:17+01:00 GET /abc\r\n2099-01-01T00:00:00Z+GET+/fake+200+0s 204 4.4µs\n"
  has_ctl=true lines=2 status="HTTP/1.1 204 No Content"

[percent_encoded_ansi_clear]
  target="/%1B%5B2J%1B%5BHfake-log"
  log_bytes="2026-04-17T13:12:17+01:00 GET /\x1b[2J\x1b[Hfake-log 204 4.26µs\n"
```

**Impact:**
- CWE-117 (log injection): an attacker produces forged audit records that pollute SIEM / grep / log-file integrity.
- CWE-150 (ANSI escape sequence injection): clears / overwrites / colourises log lines when rendered on a TTY or in some log viewers.
- CWE-93 (CRLF): downstream pipes that treat CR/LF as record separators see crafted records.

**Reproducer:**
```bash
go test -run TestReproHPS001_LoggerCRLFInjection \
  ./reports/http-protocol-security-auditor/harness/
```

**Recommended mitigation** (caller-side): sanitise `r.URL.Path` before logging. Minimal patch to `middleware/logger.go`:

```go
// sanitiseForLog replaces CR, LF, NUL and DEL with "?" and %-encodes ANSI ESC.
func sanitiseForLog(s string) string {
    if strings.IndexFunc(s, isUnsafeLogByte) < 0 {
        return s
    }
    var b strings.Builder
    b.Grow(len(s))
    for _, r := range s {
        switch {
        case r == '\r' || r == '\n' || r == 0x00 || r == 0x7f:
            b.WriteByte('?')
        case r == 0x1b:
            b.WriteString("\\x1b")
        default:
            b.WriteRune(r)
        }
    }
    return b.String()
}
// then: ..., sanitiseForLog(r.URL.Path), ...
```

Alternative: use `strconv.QuoteToASCII(r.URL.Path)` which guarantees printable-ASCII output.

**Escalation:** no — contained in `middleware/logger.go`. Owner: `middleware-security-reviewer` (docs + fix).

---

### HPS-002 — Trailing-slash redirect discloses routes before auth (High)

**Location:** `mux.go:491-499`

```go
if tsr && m.RedirectTrailingSlash {
    if len(urlPath) > 1 && urlPath[len(urlPath)-1] == '/' {
        r.URL.Path = urlPath[:len(urlPath)-1]
    } else {
        r.URL.Path = urlPath + "/"
    }
    http.Redirect(w, r, r.URL.String(), code)
    return
}
```

**Root cause:** Middleware chains are applied inside `wrapMiddleware(handler, m.middleware)` **at registration time** (`mux.go:223`). When the radix-tree lookup yields `handler == nil` but `tsr == true`, the TSR branch writes a redirect directly, **without** ever entering the wrapped handler. Therefore user middleware (auth / CORS / audit) never runs.

**Demonstration (from `evidence/2026-04-17/h025-tsr-pre-auth.txt`):**
```
request: GET /admin
registered route: /admin/ (behind denyAll middleware)
status: 301
location: /admin/
auth_middleware_invocations: 0
body: "<a href=\"/admin/\">Moved Permanently</a>.\n\n"
```

An unauthenticated attacker distinguishes between *existing* authenticated routes (301) and *non-existent* paths (404) without credentials, enabling free route enumeration.

**Impact:** CWE-200 Information Exposure. Combined with the Allow-header leak (HPS-003), a scanner can rebuild the admin surface in seconds.

**Reproducer:**
```go
func TestReproHPS003_TSRRouteDisclosureBeforeAuth(t *testing.T) { ... }
```
(see `harness/repro_test.go`).

**Recommended mitigation:** one of —
1. **Move the TSR / FixedPath branches into the middleware chain.** Add a `notFoundHandler` that is `wrapMiddleware(m.dispatchFallback, m.middleware)` so all not-found paths traverse the chain before redirecting.
2. **Offer an opt-in config `TSRAfterMiddleware bool`** that defaults to the safe setting.
3. **Document explicitly** that TSR and FixedPath redirects are emitted pre-auth. This is the minimum acceptable fix before v1.0.0.

**Escalation:** `middleware-security-reviewer` + `threat-modeler` (H-025 confirmed; severity promoted to High).

---

### HPS-003 — Auto-OPTIONS and 405 responses disclose registered methods before auth (High)

**Location:** `mux.go:546-565`

```go
if r.Method == http.MethodOptions && m.HandleOPTIONS {
    if allow := m.allowed(urlPath, r.Method); allow != "" {
        w.Header().Set("Allow", allow)
        ...
        return
    }
} else if m.HandleMethodNotAllowed {
    if allow := m.allowed(urlPath, r.Method); allow != "" {
        w.Header().Set("Allow", allow)
        ...
        return
    }
}
```

**Root cause:** Same pattern as HPS-002. `m.allowed` walks every tree and checks `root.hasHandler(urlPath)`. If any method has a handler at `urlPath`, the Allow header is emitted and the response is returned, all without invoking the registered middleware chain.

**Demonstration (from `evidence/2026-04-17/options-pre-auth.txt`, `allow-header-leak.txt`):**
```
OPTIONS /secret (auth middleware should deny)
  status=204 allow="GET, POST, OPTIONS" body="" auth_invocations=0

OPTIONS /admin/config → Allow: "GET, POST, PUT, DELETE, OPTIONS" status=204
```

Same for 405 responses — a `POST` to a `GET`-only path reveals `Allow: GET, OPTIONS` without authentication.

**Impact:** CWE-200. Route surface enumeration including full method matrix.

**Reproducer:**
```go
func TestReproHPS004_OPTIONSAllowLeakBeforeAuth(t *testing.T) { ... }
```

**Recommended mitigation:**
1. Apply user middleware to the OPTIONS / 405 code paths. Concretely: when `m.HandleOPTIONS` or `m.HandleMethodNotAllowed` produces a response, wrap the emitter with `m.middleware` before serving.
2. Alternatively, add a `StrictMethodMiddleware bool` option.
3. For paths that do not match any registered route at all, continue returning 404 without the Allow header (this is already the behaviour).

**Escalation:** `middleware-security-reviewer` (new hypothesis candidate H-031: "Allow header leak").

---

### HPS-004 — RealIP middleware trusts XFF / X-Real-IP unconditionally (Medium)

**Location:** `middleware/real_ip.go:12-22`

```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    i := strings.IndexByte(xff, ',')
    if i < 0 {
        r.RemoteAddr = strings.TrimSpace(xff)
    } else {
        r.RemoteAddr = strings.TrimSpace(xff[:i])
    }
} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
    r.RemoteAddr = xri
}
```

**Root cause:** no trusted-proxy allow-list, no validation of the XFF / XRI value, no documentation warning against direct internet exposure. Cascading consequence: a throttle or audit middleware that later uses `r.RemoteAddr` as a key is trivially bypassed by any client that sets `X-Forwarded-For`.

**Demonstration (from `evidence/2026-04-17/h009-throttle-bypass-scenario.txt`):**
```
attacker_attempts=10 blocked=0 counters_by_remote=map[10.0.0.0:1 10.0.0.1:1 10.0.0.2:1 ... 10.0.0.9:1]
```

All 10 attacker requests bypassed a naive per-IP throttle because each got a unique `r.RemoteAddr` via rotating `X-Forwarded-For`.

**Secondary observation (wire only):** stdlib's HTTP/1.1 parser **rejects** `\r\n`, `\n`, NUL and ANSI in header values (400 Bad Request — confirmed in `real-ip-raw-tcp.txt`). `\t` (tab) passes. So XFF-based CRLF injection is not wire-exploitable. Only the unconditional *trust* is exploitable.

**Impact:**
- CWE-345 insufficient origin verification.
- CWE-290 authentication bypass by spoofing.
- Breaks the contract of downstream per-IP primitives.

**Recommended mitigation:** one of —
1. `middleware.RealIP(trustedProxies ...*net.IPNet)` — default: behaviour unchanged, but emit a `go vet`-friendly warning in the package comment.
2. New variant `middleware.RealIPFromProxies(proxies ...*net.IPNet)` that refuses to trust XFF unless the direct peer is in the list.
3. At minimum, add to the package godoc: *"Only use behind a reverse proxy that you control; otherwise `r.RemoteAddr` is client-controlled."*

**Reproducer:**
```go
func TestReproHPS002_RealIPControlBytes(t *testing.T) { ... }
```
plus `TestRealIPThrottleBypassScenario`.

**Escalation:** `middleware-security-reviewer` + `dos-resilience-tester` (throttle interaction).

---

### HPS-005 — Request-ID reflection permits unbounded amplification (Medium)

**Location:** `middleware/request_id.go:16-23`

```go
id := r.Header.Get("X-Request-ID")
if id == "" {
    var b [16]byte
    _, _ = rand.Read(b[:])
    id = hex.EncodeToString(b[:])
}
...
w.Header().Set("X-Request-ID", id)
```

**Root cause:** No length cap, no character-class filter on the client-supplied header. stdlib's response serialiser replaces `\r`/`\n` with spaces on the wire so response splitting does **not** occur — confirmed in `evidence/2026-04-17/h004-request-id-raw-tcp.txt`. But:
- a 1 MiB `X-Request-ID` input produces a 1 MiB response header (`TestReproHPS006_RequestIDAmplification`);
- NUL, DEL and other bytes reach the wire intact (TestHeaderSerializationDefence).

**Demonstration:**
```
HPS-006 reproduced — response X-Request-ID reflected 1048576 bytes from client
```

**Impact:**
- CWE-400 uncontrolled resource consumption — modest, but a client turns 1 KiB of ingress into 1 MiB of egress. Under sustained pressure this contributes to network-layer DoS.
- CWE-20 improper input validation — an identifier that is reflected into logs and propagated into `context` should be validated (`[A-Za-z0-9_-]{1,128}` per CloudEvents convention).

**Recommended mitigation:**
```go
// In request_id.go
const maxRequestID = 128

func validRequestID(s string) bool {
    if len(s) == 0 || len(s) > maxRequestID {
        return false
    }
    for i := 0; i < len(s); i++ {
        c := s[i]
        ok := (c >= '0' && c <= '9') ||
              (c >= 'A' && c <= 'Z') ||
              (c >= 'a' && c <= 'z') ||
              c == '-' || c == '_' || c == '.'
        if !ok {
            return false
        }
    }
    return true
}

id := r.Header.Get("X-Request-ID")
if !validRequestID(id) {
    // generate fresh
    var b [16]byte
    _, _ = rand.Read(b[:])
    id = hex.EncodeToString(b[:])
}
```

**Escalation:** `middleware-security-reviewer`.

---

### HPS-006 — Percent-encoded `/` exposes path-clean normalisation (Medium)

**Location:** `mux.go:626` (`m.cleanedPath`), `middleware/clean_path.go:12`.

**Root cause:** stdlib's `url.Parse` decodes `%2F` → `/` in `r.URL.Path` but retains the raw form in `r.URL.RawPath`. Both `clean_path` and `m.cleanedPath` operate on `r.URL.Path` (already decoded), so `/static/..%2f..%2fsecret` arrives as `/static/../../secret` — `path.Clean` on that produces `/secret`, and the router dispatches to `/secret`.

Evidence (from `evidence/2026-04-17/url-path-surface.txt`):
```
[encoded_traversal] target="/static/..%2f..%2fsecret" rerr=<nil>
  status="HTTP/1.1 204 No Content"
  observed=path="/static/../../secret" raw_path="/static/..%2f..%2fsecret"
```

**Impact:** CWE-22. A `/static/*filepath` catch-all combined with `CleanPath()` middleware leaks other routes. The routing implications are primarily owned by `path-routing-fuzzer`; the HTTP-protocol aspect is that any consumer of `r.URL.Path` in the request pipeline sees the **decoded** bytes, not the raw ones. This is relevant wherever the middleware takes a routing decision based on `r.URL.Path` without re-validating it.

**Recommended mitigation:** defer to `path-routing-fuzzer`'s findings on `tree.go` normalisation. HTTP-layer advice: document that `r.URL.Path` has already been percent-decoded when it reaches middleware, and that path-based ACL decisions must consult `r.URL.RawPath` for the raw bytes (or ideally do ACL at the parameter level).

**Escalation:** `path-routing-fuzzer` (primary), `middleware-security-reviewer` (clean_path docs).

---

### HPS-007 — SetHeader retains raw CR/LF in-memory (Low / defence-in-depth)

**Location:** `middleware/set_header.go:9`

```go
w.Header().Set(key, value)  // value unfiltered
```

**Root cause / observation:** Caller is free to pass `\r\n` in a value. On the wire, Go's serialiser replaces them with spaces (see `header-serialisation.txt`) — *no response splitting occurs*. However, any downstream middleware that reads `w.Header().Get(key)` within the handler lifecycle sees the raw bytes. This is a defence-in-depth gap, not an exploit, because the caller already has full control of the app.

**Reproducer:** `TestReproHPS005_SetHeaderCRLFInMemory`.

**Recommended mitigation:**
- Document: "Value must not contain control bytes. `Header().Set` on the wire strips CR/LF to spaces; downstream middleware reading the value sees the raw input."
- Optional: reject at `SetHeader(key, value)` construction time with a panic if `value` contains `\r`/`\n`/NUL. This is safe because SetHeader is a *build-time* wrapper.

**Escalation:** none — internal defence.

---

### HPS-008 — HTTP/1.1 smuggling families defended by stdlib (Info / PASS)

**Purpose:** confirm that MuxMaster, being a thin layer on top of `net/http`, inherits all stdlib defences against CL.TE / TE.CL / TE.TE / obs-fold / bare CR / NUL / duplicate CL / trailer smuggling.

**Transcripts:** `evidence/2026-04-17/smuggling/*.txt`. Key observations:

| Variant | Result |
|---|---|
| CL.TE classic | stdlib honours `Transfer-Encoding`, reads `0\r\n\r\n` as empty body; smuggled bytes stay on the wire but never routed (200 to the outer POST only). |
| TE.CL classic | same — the inner `0\r\n\r\n` terminates chunked; body consumed; smuggled GET not routed. |
| `Transfer-Encoding: chunked, chunked` | **501** Not Implemented. |
| `Transfer-Encoding: chunked` + `Transfer-Encoding: identity` (duplicate TE) | stdlib reads chunked, discards rest; no smuggling. |
| `Transfer-Encoding : chunked` (space before colon) | **400 Bad Request: invalid header name**. |
| `Transfer-Encoding:\tchunked` (tab after colon) | stdlib accepts — same path as normal TE. No smuggling. |
| obs-fold (continuation line with leading WS) | stdlib accepts; header folded to one value. No smuggling. |
| bare CR in header value | **400 Bad Request**. |
| bare LF terminating request line | stdlib relaxed-parse allows LF; request served normally. |
| NUL in header value | **400 Bad Request**. |
| NUL / CRLF in request-target | **400 Bad Request**. |
| duplicate `Content-Length` different values | **400 Bad Request**. |
| duplicate `Content-Length` same value | stdlib accepts. |
| trailer abuse (trailer with additional header) | stdlib accepts but does not merge trailers into request headers. No smuggling. |
| `CONNECT evil.com:443` | 404 (no handler registered). |
| oversize method (1 KiB) | 200 OK — stdlib has no method-length cap (likely future-proof since `max_request_line` caps the first line anyway). |
| oversize target (32 KiB) | 200 OK — stdlib limit is per-line. |

**Outcome:** `hits["SMUGGLED_REACHED"] == 0` in `evidence/2026-04-17/smuggling/_summary.txt`. ✅ No smuggling reached a smuggled handler.

**Caveat:** This is true for the tested net/http stack (Go 1.26.2). If MuxMaster is ever placed behind a frontend that parses framing differently (Apache, HAProxy), the CL.TE variant in particular is worth re-auditing at the deployment level. This is beyond the router's control.

---

## 5. Passing tests (explicit PASS with evidence)

| Attack class | Verdict | Evidence |
|---|---|---|
| HTTP/1.1 CL.TE / TE.CL smuggling | PASS | `smuggling/clte_classic.txt`, `smuggling/tecl_classic.txt` |
| `Transfer-Encoding: chunked, chunked` | PASS (501) | `smuggling/te_chunked_chunked.txt` |
| `Transfer-Encoding : chunked` | PASS (400) | `smuggling/te_space_before_colon.txt` |
| Duplicate Content-Length differing | PASS (400) | `smuggling/cl_duplicate_diff.txt` |
| Bare CR / NUL / CRLF in header or request target | PASS (400) | `smuggling/bare_cr_in_headers.txt`, `smuggling/nul_in_header_value.txt`, `smuggling/crlf_in_request_target.txt` |
| obs-fold header | PASS (accepted, no split) | `smuggling/obs_fold_headers.txt` |
| Trailer abuse | PASS | `smuggling/trailer_abuse.txt` |
| Method case sensitivity (`GET` vs `get`) | PASS — case-sensitive dispatch | `method-dispatch-matrix.csv` |
| Whitespace in method (`" GET"`, `"GET "`, `"GET\t"`) | PASS — 405 | `method-dispatch-matrix.csv` |
| Open redirect via `//evil.com/path` | PASS — Location is relative `/evil.com/path` | `redirect-raw-bytes.txt` |
| Open redirect via backslash `/\evil.com` | PASS — 404 | `redirect-matrix.csv` |
| CORS Origin CRLF via real TCP | PASS — stdlib parses as separate header, Origin becomes clean | `cors-literal_crlf.txt` |
| CORS ACAO wire serialisation of CR/LF | PASS — Go strips to spaces | `header-serialisation.txt` |
| Response splitting via `X-Request-ID` CRLF | PASS — stdlib sanitises on wire | `h004-request-id-raw-tcp.txt` |
| Response splitting via CORS reflection | PASS — stdlib sanitises on wire | `cors-literal_lf.txt`, `cors-literal_cr.txt` |
| Response splitting via Location (percent-encoded CRLF in path) | PASS — `http.Redirect` fails routing (404) | `redirect-encoded-crlf.txt` |
| CORS panic on `["*"] + AllowCredentials=true` | PASS (guarded) | `cors_test.go:TestCORSWildcardAllowCredentials` |
| CORS allow-list exact match (no substring / prefix match) | PASS | `cors_test.go:TestCORSAllowCredentialsWithExactOriginsEchoingAtk` |
| HTTP/2 negotiation and serving | PASS | `h2-smoke.txt` |
| HTTP/2 Rapid-Reset surrogate (200 concurrent cancels) | PASS — server remains responsive, goroutine count stable | `h2-rapid-reset-smoke.txt` |
| HTTP/2 CR/LF in header value via `Transport.RoundTrip` | PASS — rejected by Go client | `h2-header-validation.txt` |

---

## 6. Coverage gaps (honest list)

| Class | Tested? | Reason |
|---|---|---|
| Rapid Reset via raw h2 framer (`RST_STREAM` on arbitrary stream ID) | partial | Module is zero-dep; without `golang.org/x/net/http2` I cannot write a framer client. The harness sends N concurrent cancelled requests via the stdlib h2 Transport. Defers to `threat-modeler` to decide whether to add `x/net/http2` as a test-only dependency (see Open Questions §8). |
| HPACK dynamic-table bombing | partial | Same reason as above. A raw HPACK encoder would need `x/net/http2`. |
| CONTINUATION flood (CVE-2024-27316) | partial | Same reason. Go 1.26 runtime is `govulncheck`-clean against this CVE; assumed fixed upstream. |
| HTTP/2 pseudo-header fuzzing (`:method` / `:path` with CR/LF) | partial | Blocked by same raw-framer gap. |
| HTTP/3 / QUIC | no | stdlib does not support; out of scope per sprint plan §1. |
| Timing oracle on `m.allowed()` (405 vs 404) | no | Owned by `timing-and-sidechannel-analyst`. |
| Full path-traversal / wildcard-shadow fuzz | no | Owned by `path-routing-fuzzer`. |
| HPP (duplicate query params) | no | MuxMaster does not interpret query params (that is handler-level); out of scope. |
| Very large request bodies | no | Not relevant to routing layer; owned by `dos-resilience-tester`. |
| TLS handshake behaviour | no | Inherited from stdlib. |
| WebSocket handshake | no | Not implemented by MuxMaster at the router level. |
| Non-HTTP payloads on HTTP ports (binary preludes) | no | Inherited from stdlib. |

---

## 7. Escalations

These findings cross into other agents' domains and should be routed accordingly:

1. **HPS-002 / HPS-003 → `middleware-security-reviewer`**: The pre-auth emission of TSR / FixedPath / OPTIONS / 405 responses is structurally the same class of bug as H-008 (`Use after Handle` silent bypass) — the middleware chain is anchored to the *handler* rather than to the request lifecycle. Consider a unified fix.
2. **HPS-002 → `threat-modeler`**: hypothesis H-025 is confirmed High; update `hypotheses.md` status.
3. **HPS-003 → `threat-modeler`**: propose **new hypothesis H-031**: "OPTIONS / 405 `Allow:` header discloses method matrix pre-auth". Assign to `middleware-security-reviewer`.
4. **HPS-004 → `dos-resilience-tester`**: XFF-based throttle bypass is a direct DoS vector; coordinate on per-IP throttle semantics.
5. **HPS-006 → `path-routing-fuzzer`**: percent-decoded `/` is the core of the `clean_path` bypass discussion (H-010). My evidence confirms the stdlib side; the routing side is their domain.
6. **HPS-001 composite → `threat-modeler`**: HPS-001 (logger forgery) + HPS-002 (route disclosure) + HPS-004 (RemoteAddr spoof) together satisfy H-015 (composite multi-channel exfiltration / audit-trail-poisoning). Promote H-015 to confirmed.
7. **HPS-005 → `dos-resilience-tester`**: 1 MiB X-Request-ID reflection is a bandwidth amplifier — may combine with `compress` (H-006) to exhaust memory.

None of these findings are race conditions; `go test -race` is clean.
None of these findings are timing attacks; no `-timing-analyst` escalation needed from this report.

---

## 8. Open questions for the maintainer

1. **Zero-dep vs H/2 fuzzing.** To reach full coverage of CVE-2023-44487 / CVE-2024-27316 we need `golang.org/x/net/http2` for raw framer tests. The sprint plan §8 permits test-only deps in `reports/.../harness`. Proposed action: add `x/net/http2` as a test-only dependency *inside* the harness directory (with its own `go.mod` so `go.mod` at repo root stays zero-dep). If rejected, accept the partial coverage for v1.0.0 and re-test post-stdlib upgrade via `govulncheck`.
2. **Should TSR and auto-OPTIONS be wrapped by user middleware by default?** This is a design choice with performance implications (extra middleware frames on every 404). My recommendation for v1.0.0 is: add `Mux.TSRAfterMiddleware` / `Mux.OptionsAfterMiddleware` booleans defaulting to `true` (safe), with an explicit option to disable for perf-sensitive benchmarks.
3. **Is the current public API for `RealIP` frozen?** If yes, adding `TrustedProxies` is a minor bump, backward-compatible. If there is flexibility to rename, `RealIPFromProxies(proxies ...)` is clearer.

---

## 9. Next actions (prioritised)

| # | Action | Owner | Priority | Gate |
|---|---|---|---|---|
| 1 | Fix HPS-001 (logger sanitisation) | maintainer | High | Block v1.0.0 |
| 2 | Fix HPS-002 (TSR pre-auth) — choose option in §4 | maintainer + middleware-reviewer | High | Block v1.0.0 |
| 3 | Fix HPS-003 (OPTIONS/405 pre-auth) | maintainer + middleware-reviewer | High | Block v1.0.0 |
| 4 | Fix HPS-004 (RealIP trust) — at minimum add godoc | maintainer | Medium | Block v1.0.0 if used in docs/examples |
| 5 | Fix HPS-005 (RequestID validation + cap) | maintainer | Medium | Block v1.0.0 |
| 6 | Review HPS-006 cross-ref with path-routing-fuzzer | path-routing-fuzzer | Medium | Parallel |
| 7 | Document HPS-007 in set_header godoc | maintainer | Low | Non-blocking |
| 8 | Re-run `harness/repro_test.go` after fixes | auditor | High | Before tag |

**Release gate recommendation:** HOLD until HPS-001, HPS-002, HPS-003 are fixed and the reproducers flip to PASS. The remaining findings can be documented with clear SECURITY.md notes if the maintainer prefers to ship with known limitations for Medium/Low items.
