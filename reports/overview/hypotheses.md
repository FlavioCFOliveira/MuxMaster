# MuxMaster — Zero-Day Hypotheses (Post-Sprint)

**Date:** 2026-04-17 (Phase 3 — consolidation)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Final states post-sprint + new hypotheses H-031 to H-034 added

## Summary of verdicts

| Verdict | Count | IDs |
|---|---|---|
| **Confirmed** | 22 | H-001, H-002, H-003, H-004, H-005, H-006, H-008, H-009, H-010, H-011, H-012, H-013 (partial), H-015, H-017, H-021, H-024, H-025, H-026, H-027, H-030, H-031 (new), H-032 (new) |
| **Refuted** | 5 | H-007, H-016, H-019, H-023 (functional), H-028 |
| **Partial** | 4 | H-013, H-018, H-022, H-023 |
| **Deferred** | 3 | H-014, H-020 (subsumed H-007), H-029 |
| **Open (new)** | 2 | H-033, H-034 |

**Total hypotheses post-sprint: 34** (30 original + 4 new H-031 to H-034).

---

## Conventions

- `H-NNN` — hypothesis number NNN, sequential
- **Premise:** concrete claim to test
- **Testability:** how to build empirical evidence
- **Assigned:** responsible agent(s)
- **Priority:** `Critical` / `High` / `Medium` / `Low`
- **Status:** `open` / `confirmed` / `refuted` / `partial` / `merged-into-H-NNN`

Each hypothesis has cross-links to attack tree (§) and finding ID when applicable.

---

## H-001 — Cross-goroutine race on r.ctx via unsafe.Add

**Premise:** The pattern `*origCtxPtr = rc` ... `handler.ServeHTTP(w, r)` ... `*origCtxPtr = origCtx` in `mux.go:464-480` (and mirrored in 521-537) assumes that `r` is goroutine-owned. If a handler does `go func() { _ = r.Context() }()` — a legitimate pattern in background work — the child goroutine may read `r.Context()` while the dispatcher goroutine has already overwritten the pointer `r.ctx` with `origCtx`, returned `rc` to the pool, and the pool has returned `rc` to another request that just mutated `rc.params`. Result: the child goroutine observes the context **of another concurrent request**. The race detector should flag-ar the pointer write + read, but only if the child goroutine executes within the window.

**Testability:**
```go
r := mm.New()
var seen []string
var mu sync.Mutex
r.GET("/a/:id", func(w http.ResponseWriter, req *http.Request) {
    go func(r *http.Request) {
        time.Sleep(1 * time.Millisecond)
        mu.Lock()
        seen = append(seen, mm.PathParam(r, "id"))
        mu.Unlock()
    }(req)
})
// Drive two goroutines hitting /a/alpha and /a/beta concurrently with -race
// Assert: `seen` contains only "alpha" and "beta", never empty string or crossed value
```

**Expected outcome:** `-race` detects write-write or read-write on `r.ctx` offset, or output contains crossed values. If confirmed, it is `CWE-362` + `CWE-362` combined data race + pool contamination.

**Assigned:** concurrency-security-auditor
**Priority:** Critical
**Status:** **CONFIRMED** — finding MM-2026-0003 (CSA-001). 3 DATA RACE warnings captured in `h001_run1_full.txt` with complete stacks. Matches exactly the predicted scenario.
**Cross-refs:** threat-model §6 `Params / pool` column I, attack tree §D3.1, MM-2026-0003, MM-TM-2026-0004

---

## H-002 — User enumeration timing in basic_auth via map lookup path

**Premise:** `basic_auth.go` compares credentials like this:
```go
user, pass, ok := r.BasicAuth()
if ok {
    if expected, found := creds[user]; found {
        if subtle.ConstantTimeCompare(...) == 1 { next }
    }
}
// else: 401
```
A user that exists follows a path with `subtle.ConstantTimeCompare` (costs ~200ns + password size). A user that does not exist does not execute the compare (jumps directly to 401). The difference is architecturally **guaranteed** and measurable.

**Testability:** Collect N=1e6 samples timing `auth(r, "alice", "wrong")` (user exists) vs `auth(r, "charlie", "wrong")` (user does not exist). Welch t-test. Expected: p ≪ 0.01. Proposed remediation: always execute `subtle.ConstantTimeCompare` against a dummy password (e.g. `dummyHash := "00000000000000000000000000000000"`) when user does not exist, to equalize the path.

**Assigned:** timing-and-sidechannel-analyst (primary) + middleware-security-reviewer (advisory)
**Priority:** High (CWE-208, CWE-203 info disclosure via timing)
**Status:** **CONFIRMED** — finding MM-2026-0009 (TSC-001 + MSR-BA-001). N=1.5M samples, 3 runs tripled, Welch p=0, KS p=0, MWU p=0. Mean diff 319-429 ns, Cohen d 0.33-0.45. Assembly confirmed: `JEQ 0x00d9` skips compare on map miss. Bimodal distribution for "user absent". Proposed fix: constant-path dummy compare.
**Cross-refs:** attack tree §A2.3.1, transposition §15, MM-2026-0009, MM-TM-2026-0001

---

## H-003 — CRLF log injection via r.URL.Path in logger

**Premise:** `logger.go` does:
```go
fmt.Fprintf(out, "%s %s %s %d %s\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, rec.status, time.Since(start))
```
`r.URL.Path` is passed without any escape. If a client sends a request with path containing bytes `\r\n` (which stdlib `net/http` **normally** rejects in the request line, BUT can pass via internal percent-decode of RawPath), the log produces a forged line + a new injected line.

**Testability:** Send `GET /admin%0D%0A2026-04-17T00:00:00Z%20GET%20/fake%20200%200s HTTP/1.1\r\n`. Check if `r.URL.Path` (after net/http) contains `\r\n`. If yes, `fmt.Fprintf(%s)` writes directly and the log has 2 lines. Alternatively, path includes control bytes like `\x1b[2J` (ANSI clear screen) if the log goes to TTY — obfuscation.

**Expected outcome:** net/http already rejects CR/LF in request-target (returns 400 in parser) — hypothesis probably **refuted** for direct CRLF. But `\x1b[...` (ESC sequences) are acceptable as valid bytes in paths (RFC 3986 allows) → ANSI injection confirmed.

**Assigned:** middleware-security-reviewer + http-protocol-security-auditor
**Priority:** High (CWE-117 log injection, CWE-93 CRLF, CWE-150 ANSI)
**Status:** **CONFIRMED** — finding MM-2026-0006 (HPS-001 + MSR-LG-001). Stdlib rejects literal CRLF in request-target (400) BUT **percent-decodes CRLF to `r.URL.Path`**. 15 payload classes confirmed in corpus: CRLF, LF, ANSI clear/colour, NUL, BEL, VT, BOM, Unicode line separators.
**Cross-refs:** attack tree §D7, transposition §12, MM-2026-0006, MM-TM-2026-0002

---

## H-004 — request_id CRLF reflection causes response splitting

**Premise:** `request_id.go` copies `X-Request-ID` header from client to `w.Header().Set("X-Request-ID", id)` without validation. Go `net/http` validates invalid bytes in `Header().Set` via `textproto` and **rejects** values with CR/LF (`http.invalidHeaderFields` check). HYPOTHESIS: if the value contains only `\t` or `\x00` or UTF-8 high chars, it passes; if it is `\r\n`, `net/http` silently ignores or truncates → test.

**Testability:**
```go
req.Header.Set("X-Request-ID", "abc\r\nSet-Cookie: evil=1")
// Run through muxmaster + request_id middleware + httptest
// Inspect recorder.HeaderMap — did Set-Cookie appear?
// Inspect raw bytes via http.ResponseWriter.WriteHeader (or real TCP response)
```

Also validate: client sends 1MB X-Request-ID → response includes 1MB header → amplifies outbound traffic (D6 DoS amplification).

**Assigned:** http-protocol-security-auditor (primary) + middleware-security-reviewer
**Priority:** High (CWE-113 response splitting)
**Status:** **CONFIRMED partial** — finding MM-2026-0011 (HPS-005 + MSR-RQ-004 + FPE-001). **Response splitting sanitised on wire by Go 1.26 stdlib** (rejected CRLF in response header serialization). **BUT**: (1) CRLF retained in-memory in `w.Header()` → downstream middleware sees raw bytes; (2) 1 MiB X-Request-ID → 1 MiB response amplification (1024×). Severity High maintained by amplification + in-memory state.
**Cross-refs:** attack tree §A2.7, §D1.3, MM-2026-0011, MM-TM-2026-0002

---

## H-005 — CORS Origin reflection when allowAll=true permits credentials theft

**Premise:** `cors.go:22-24` rejects configurations with `AllowedOrigins=["*"]` **AND** `AllowCredentials=true` via panic at config-time. BUT: if caller passes `AllowedOrigins=["*"]` and `AllowCredentials=false`, the middleware reflects the attacker's Origin in the ACAO header. Combined with:
- if the application uses cookies without `SameSite=Strict`, cross-site response permits CSRF
- request_id reflection or logger CRLF are still lateral vectors

Additionally: secondary HYPOTHESIS — if caller passes `AllowedOrigins=["*"]` dynamically (e.g. built at runtime from env vars) and **simultaneously** something passes `AllowCredentials=true` from another middleware, there is no re-check.

**Testability:** Build muxmaster with `cors.CORS(CORSOptions{AllowedOrigins:[]string{"*"}})`. Send `Origin: https://evil.com`. Assert `Access-Control-Allow-Origin: https://evil.com` in response. And then: `Origin: null` — is it accepted? `Origin: evil.com\x1b[` — survives? `Origin: evil.com,` trailing comma? `Origin: ` (empty)?

**Assigned:** middleware-security-reviewer
**Priority:** High (CWE-942)
**Status:** **CONFIRMED** — finding MM-2026-0012 (MSR-CO-003 + FPE-002). `AllowedOrigins=["*"]` + `Origin: https://evil.example` → `ACAO: https://evil.example` instead of `ACAO: *`. Spec violation; enables credential-grant if `AllowCredentials=true` is added dynamically to another middleware. Fix trivial: emit literal `*` when `allowAll=true`.
**Cross-refs:** attack tree §A2.5, §E2; transposition §5, §11; MM-2026-0012, MM-TM-2026-0002

---

## H-006 — compress middleware unbounded buffer OOMs process

**Premise:** `compress.go:26-28`:
```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.done {
        g.buf = append(g.buf, b...)
        return len(b), nil
    }
    return g.gz.Write(b)
}
```
The handler can write **unlimited** content before `g.done` is true (only true after handler returns in `next.ServeHTTP(grw, r); grw.done = true`). If the handler does streaming of 10GB of zeros, `g.buf` grows to 10GB → OOM.

Additional vector: heap fragmentation — many simultaneous handlers with medium responses (100MB) exhaust RSS.

**Testability:**
```go
r := mm.New()
r.Use(middleware.Compress(5))
r.GET("/big", func(w http.ResponseWriter, req *http.Request) {
    buf := make([]byte, 1<<20) // 1MB chunk
    for i := 0; i < 10_000; i++ { _, _ = w.Write(buf) } // 10GB
})
req := httptest.NewRequest("GET", "/big", nil)
req.Header.Set("Accept-Encoding", "gzip")
// Measure RSS before and after
// Expected: RSS grows by ~10GB in buf; fails on constrained env
```

**Expected outcome:** confirmed OOM. Remediation: streaming compression — use `gz.Write(b)` directly instead of buffering; or impose `MaxBufferSize` config.

**Assigned:** dos-resilience-tester + middleware-security-reviewer
**Priority:** High → **Critical** (promoted because `Accept-Encoding: gzip` is sent by default by all browsers — attacker requires no privileges)
**Status:** **CONFIRMED** — finding MM-2026-0007 (DOS-001 + MSR-CP-001 + SAST-010). Empirical slope 1.15 byte heap / byte body. 64MB → 178MB peak. 1GB → ~2GB RSS. Streaming compression is the fix.
**Cross-refs:** attack tree §B2.1, §B5.1; transposition §2, §12; MM-2026-0007

---

## H-007 — RedirectFixedPath open redirect via // prefix

**Premise:** `mux.go:502-506`:
```go
if m.RedirectFixedPath {
    if fixed, ok := m.cleanedPath(root, urlPath); ok {
        r.URL.Path = fixed
        http.Redirect(w, r, r.URL.String(), code)
        return
    }
}
```
`cleanedPath` uses `path.Clean(p)`. For input `//evil.com/foo`, `path.Clean` returns `/evil.com/foo`. Then, `r.URL.Path = "/evil.com/foo"`; `r.URL.String()` — depending on the fields present (Scheme, Host) — may serialize as relative URL `"/evil.com/foo"` or as URL with extracted host.

Test the variant with `RedirectTrailingSlash` + `//evil.com/foo/`.

**Testability:**
```go
r := mm.New()
r.GET("/evil.com/foo", handler) // matching /evil.com/foo
req := httptest.NewRequest("GET", "http://localhost//evil.com/foo", nil)
// RedirectFixedPath=true by default
// Check Location header
```
Also test with `CaseInsensitive=true` and backslash `/\\evil.com`.

**Expected outcome:** probably stdlib `http.Redirect` sanitizes — but confirm empirically. If Location header contains a purely relative value `/evil.com/foo`, browsers treat as same-origin (OK). If it contains `//evil.com/foo`, treats as protocol-relative (EVIL).

**Assigned:** http-protocol-security-auditor (primary) + path-routing-fuzzer
**Priority:** High (CWE-601)
**Status:** **REFUTED** — `path.Clean("//evil.com/foo")` → `/evil.com/foo` (single slash). `http.Redirect` produces `Location: /evil.com/foo` — relative Location **same-origin**. Tested in HPS (`evidence/redirect-raw-bytes.txt`). **Important note:** the TSC-003 variant (canonicalization discloses hidden routes) is **different** and is CONFIRMED as MM-2026-0005.
**Cross-refs:** attack tree §A2.8, §D2.3

---

## H-008 — Middleware ordering silent bypass via Use after Handle

**Premise:** `mux.go:223`:
```go
root.addRoute(pattern, wrapMiddleware(handler, m.middleware))
```
`wrapMiddleware` is called at `Handle` time, capturing the **snapshot** of `m.middleware` at that instant. If caller does:
```go
r := mm.New()
r.GET("/admin", adminHandler)       // wrapMiddleware sees m.middleware = []
r.Use(auth)                          // ADDED AFTER
r.GET("/profile", profileHandler)   // wrapMiddleware sees m.middleware = [auth]
```
→ `/admin` **does not** have auth. Silent.

This is a known consequence of the design (performance decision: wrap at registration). BUT **it is not sufficiently documented** in `README` / `middleware.md`. If a user migrates from `chi` (which applies on request) to MuxMaster, the code compiles and works **without auth on admin** and without warning.

**Testability:** Direct test case replicating the example. Assert: `r.Handle(...)` after `r.Use(...)` applies; before, it does not.

**Possible mitigations:**
- Panic in `Use()` if there are already routes registered (breaking — but clear)
- Warning log in `Use()` post-Handle
- Strong documentation + linter check

**Assigned:** middleware-security-reviewer (docs)
**Priority:** Critical (latent auth bypass) — severity depends on user, but the module has responsibility to warn
**Status:** **CONFIRMED (docs)** — behavior reproduced in CSA harness. Fix via normative docs in README + GoDoc + lint rule. Not a MM-NNN finding (it is documentation; the code is by design).
**Cross-refs:** attack tree §A2.1.1

---

## H-009 — XFF unconditional trust permits throttle bypass and logger spoof

**Premise:** `real_ip.go`:
```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    i := strings.IndexByte(xff, ',')
    if i < 0 { r.RemoteAddr = strings.TrimSpace(xff) } else { r.RemoteAddr = strings.TrimSpace(xff[:i]) }
}
```
Always accepts XFF, without trusted proxy list. If MuxMaster is deployed directly (without proxy in front), **any client** can overwrite `r.RemoteAddr`. Consequences:
- `throttle` (if using `r.RemoteAddr`) is trivially bypassable
- `logger` records falsified IP
- IP-based ACLs in application middleware are bypassable

Note: the current `throttle.go` does NOT use `r.RemoteAddr` (it is global), but a user implementing per-IP throttle on top of `real_ip` is vulnerable by default.

**Testability:** Register muxmaster with `real_ip` + custom throttle-per-IP. Attacker sends 100 requests, each with `X-Forwarded-For: <random IP>`. Assert that only 1 is throttled.

**Proposed remediation:** add `real_ip.TrustedProxies([]string)` config.

**Assigned:** middleware-security-reviewer + dos-resilience-tester
**Priority:** High (CWE-345, CWE-290)
**Status:** **CONFIRMED** — finding MM-2026-0008 (HPS-004 + DOS-005 + MSR-RI-001 + SAST-009). 4 convergent reproducers. Demonstrated: 10 requests with rotating XFF produce 10 unique counters. Fix via `RealIP(trustedCIDRs ...*netip.Prefix)`.
**Cross-refs:** attack tree §B6, §E5; transposition §1, §5; MM-2026-0008, MM-TM-2026-0002

---

## H-010 — clean_path single-pass bypass via encoded traversal

**Premise:** `clean_path.go` makes **one** call to `path.Clean`. `path.Clean` only sees textual `..`. If the input is `/%2e%2e/etc/passwd`:
- **Before** routing, `r.URL.Path` may already be decoded by `net/url` (yes — `r.URL.Path` is decoded)
- If stdlib decoded `%2e%2e` to `..`, then `r.URL.Path = "/../etc/passwd"`, clean → `/etc/passwd`, routing attacks
- If stdlib left `%2e%2e` in `r.URL.RawPath` and MuxMaster uses `r.URL.Path` (already decoded), the clean sees textual `..` and removes

More interesting: `/static/..%2f..%2fsecret`:
- stdlib decoded → `r.URL.Path = "/static/../../secret"`, clean → `/secret` — **after** clean, routing catches `/secret`

BUT the middleware `clean_path` normalizes the **path** and then calls `next.ServeHTTP(w, r2)`. If that "next" is the router, the router does lookup on the normalized path. If `/secret` has handler, attacker bypassed `/static/*filepath` catch-all.

Exhaustive test of order: with and without `clean_path`, with `UnescapePathValues`, with `RedirectFixedPath`. Matrix 2×2×2.

**Testability:** Matrix already described by `path-routing-fuzzer` in its prompt (Step 5). Confirms results.

**Assigned:** path-routing-fuzzer + middleware-security-reviewer
**Priority:** High (CWE-22)
**Status:** **CONFIRMED** — finding MM-2026-0018 (PRF-002 + HPS-006 + MSR-CL-001). 136 bypass combinations in matrix. `/static/..%2fadmin` → decode → `/static/../admin` → clean → `/admin` → bypass.
**Cross-refs:** attack tree §C1, §C2; transposition §1 (Apache 41773), §5 (Traefik); MM-2026-0018, MM-TM-2026-0003

---

## H-011 — Route-existence timing oracle via RedirectFixedPath / RedirectTrailingSlash

**Premise:** When MuxMaster processes a non-existent path, it follows three sequential attempts:
1. `getValue` on the tree of the method
2. If failed, try TSR (`RedirectTrailingSlash`)
3. If failed, try `path.Clean` + re-lookup (`RedirectFixedPath`)

For paths that **do not exist**, the sequence fails on the first getValue. For paths that exist with trailing/case variant, the time includes an extra getValue with different input. Timing distinguishes registered routes.

The severity is: reconnaissance is cheaper via redirect. But redirect **already reveals** (H-007 line Location), so perhaps less incremental impact. Still, timing is measurable even with redirect disabled (the code always tries the cleaned lookup).

**Testability:** N=1e6 samples:
- `GET /registered-route` (no match, but close to existing)
- `GET /random-xyz-doesntexist-123`

Welch + KS. Expected: distinguishable. If yes, severity Medium since it complements other leaks.

**Assigned:** timing-and-sidechannel-analyst
**Priority:** Medium (CWE-208)
**Status:** **CONFIRMED** — finding MM-2026-0026 (TSC-002). N=1.5M samples, p=0, mean 437-463 ns gap, Cohen d 0.72-0.79 (large effect). Intrinsic to any radix router — httprouter, chi, bunrouter exhibit the same magnitude. **Accepted risk** (document in SECURITY.md).
**Cross-refs:** attack tree §A1.2, §D2.2; MM-2026-0026

---

## H-012 — paramsBuf silent overflow at 4th param causes handler logic error

**Premise:** `paramsBuf.add` in `tree.go:21`:
```go
func (pb *paramsBuf) add(key, value string) {
    if pb.count < maxInlineParams {  // 3
        pb.buf[pb.count] = Param{Key: key, Value: value}
        pb.count++
    }
}
```
For a route with 4+ params, the 4th param onwards is **silently discarded**. If a developer registers `/a/:b/:c/:d/:e/:f` and a handler reads `PathParam(r, "f")`, gets `""`. If that value is used in auth logic (`if allowedUsers[pathParam("f")]`), `""` may map to an accepted value (e.g. empty string in map) → logic bypass.

The comment says "maxInlineParams covers ≥99% of real-world APIs" — `1%` of cases silently break.

**Testability:**
```go
r := mm.New()
r.GET("/a/:p1/:p2/:p3/:p4", h)
// Register with 4 params; issue request /a/w/x/y/z
// Assert: PathParam("p4") returns "" (confirmed overflow)
```

**Proposed mitigation:** panic in `addRoute` if pattern contains >3 params (breaking), or increase `maxInlineParams` to 8 with fallback to `append` outside the buf. Architectural decision.

**Assigned:** fuzzing-and-property-engineer + path-routing-fuzzer
**Priority:** Medium → **High** (promoted; combined with auth-middleware creates auth bypass)
**Status:** **CONFIRMED** — finding MM-2026-0010 (PRF-003 + DOS-003 + FPE-004). Route `/a/:p1/:p2/:p3/:p4/:p5` → p4, p5 = `""`. httprouter/bunrouter support 8-16 params — MuxMaster is outlier.
**Cross-refs:** attack tree §C5; MM-2026-0010, MM-TM-2026-0003

---

## H-013 — Mount RawPath vs Path divergence enables method ACL bypass

**Premise:** `mux.go:357-373`:
```go
r2 := r.Clone(r.Context())
r2.URL = new(url.URL)
*r2.URL = *r.URL
r2.URL.Path = p  // PathParam "mux_mount" (from muxmaster)
if r.URL.RawPath != "" {
    r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
}
```
If the mounted handler inspects `r.URL.RawPath`, sees a prefix trim different from what the outer router processed. If `r.URL.Path = "/api/foo"` but `r.URL.RawPath = "/%61pi/foo"`, `TrimPrefix("/%61pi/foo", "/api")` fails (no trim), and the inner router receives RawPath `/%61pi/foo` — if that inner does its own parsing, divergence possible.

Combined with: Mount is registered in the tree `*` (method wildcard), which is checked AFTER standard methods. If attacker sends non-standard method, falls through to `*` tree and executes the Mount handler with arbitrary method — potentially bypassing method-specific ACL.

**Testability:** `r.Mount("/api", innerRouter)`. Send `GET /api/foo` vs `PROPFIND /api/foo`. Assert: inner router receives PROPFIND (yes — by design). Additional test: inner router check `r.Method` to enforce ACL. Is it consistent?

**Assigned:** path-routing-fuzzer + http-protocol-security-auditor
**Priority:** Medium
**Status:** **CONFIRMED partial** — finding MM-2026-0022 (PRF-005). TrimPrefix fails on percent-encoded; inner handler sees `Path="/foo"` but `RawPath="/%61pi/foo"` (prefix intact). Real risk only if inner handler uses RawPath for its own routing. Fix: zero RawPath if TrimPrefix does not match.
**Cross-refs:** attack tree §C4, §A2.6; transposition §6; MM-2026-0022

---

## H-014 — Registration-time panic in addRoute allows DoS at init

**Premise:** `tree.addRoute` panics in various conditions (catch-all conflict, regex invalid, etc.). If the application loads routes from a config file or from an external variable (env var expansion in pattern), attacker that influences that input causes panic in `main()` → process never starts → DoS of entire application.

Not a bug in MuxMaster per se — it is a design choice. But the **scope of risk** depends on how callers construct patterns. Must be explicitly documented with warning.

**Testability:** Trivial — register `/[` and see panic from regexp.Compile. Document.

**Assigned:** middleware-security-reviewer (docs) + sast
**Priority:** Low (caller responsibility, but document)
**Status:** **DEFERRED** — documentation-only; not blocking for v1.0.0. Move to SECURITY.md / README post-release.

---

## H-015 — Composite: logger CRLF + request_id reflection + CORS reflection → multi-channel exfiltration

**Premise (composite):** Combines H-003, H-004 and H-005. An attacker sending `X-Request-ID: <exfil data>\r\nZ:`, `Origin: <exfil data>`, and path `/path%20<exfil data>`, with `Authorization: Bearer <secret>`, and compress enabled, has 4 paths for secret exfiltration:
1. Log line includes the path (exfil data observable if attacker reads log storage)
2. Response X-Request-ID reflected (attacker reads own response) — **if CRLF survives**
3. Response ACAO reflected with Origin — same
4. Response body BREACH via compression — if the app uses auth context in response

Each channel alone may be Medium. Combined, they permit parallel information exfiltration — attacker may correlate or verify on multiple channels.

**Testability:** e2e test: attacker client + muxmaster + logger to file + response measurement. Count available channels per request.

**Assigned:** threat-modeler-and-zero-day-researcher (consolidates after H-003, H-004, H-005)
**Priority:** High (composite)
**Status:** **CONFIRMED** — promoted to formal composite **MM-TM-2026-0002** (Multi-channel exfiltration + audit-trail forgery). Now includes 4 channels: logger CRLF (MM-2026-0006) + X-Request-ID reflection (MM-2026-0011) + CORS reflection (MM-2026-0012) + BREACH oracle (MM-2026-0047, handler-level) + XFF spoof (MM-2026-0008) to forge IP source.
**Cross-refs:** MM-TM-2026-0002, composite derived

---

## H-016 — Throttle token leak on panic in handler

**Premise:** `throttle.go`:
```go
case t := <-tokens:
    defer func() { tokens <- t }()
    next.ServeHTTP(w, r)
    return
```
If `next.ServeHTTP` panics and there is NO `recoverer` **inside** (before the throttle wrap), the defer runs and the token returns. OK. BUT: if `next` panics with `http.ErrAbortHandler` sentinel that stdlib net/http captures silently (does not call user recoverer), the behavior still is OK because defers run. CONFIRMED safe.

Edge case: if panic happens AFTER the token is returned (i.e. in `defer tokens <- t` itself panic)? Closing the channel if closed causes panic. The channel is never closed in the code — safe.

HYPOTHESIS alternative: combination with `context.WithTimeout` — timeout middleware cancel, but handler keeps running (goroutine leak H-017). Token returned normally (via defer). OK.

**Status:** probably **refuted** via code review, but auditor should confirm with panic injection test.

**Assigned:** concurrency-security-auditor + dos-resilience-tester
**Priority:** Low
**Status:** **REFUTED** — DOS agent confirmed defer cleanup correct (16k concurrent panics, 0 token leaks). `recoverer_throttle_test.go` PASS. Feedback for CSA: the rc leak corresponding (MM-2026-0015) is different bug — do not confuse.

---

## H-017 — Timeout middleware goroutine leak under sustained slow handlers

**Premise:** `timeout.go`:
```go
ctx, cancel := context.WithTimeout(r.Context(), d)
defer cancel()
next.ServeHTTP(w, r.WithContext(ctx))
```
Only sets a deadline on the context. Handler is not preempted. A blocking handler (`time.Sleep(time.Hour)`) keeps running even after `cancel()`. The dispatcher goroutine is blocked inside that handler — so the **entire server** does not "leak", but the individual goroutine gets stuck. Under 1000 req/s with 1h handlers, 3.6M goroutines accumulate until crash.

**Testability:**
```go
r := mm.New()
r.Use(middleware.Timeout(10*time.Millisecond))
r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
    time.Sleep(10 * time.Second)
})
before := runtime.NumGoroutine()
// fire 1000 requests in parallel goroutines
time.Sleep(11*time.Second)
after := runtime.NumGoroutine()
// Assert: after - before ≈ 1000 (all stuck)
```

**Mitigation:** impossible without handler cooperation. Document and recommend `r.Context().Done()` check in handler.

**Assigned:** dos-resilience-tester + concurrency-security-auditor + docs
**Priority:** Medium (documented limitation, design choice)
**Status:** **CONFIRMED** — finding MM-2026-0019 (DOS-002 + CSA-008 + MSR-TO-003). 1000 req/10ms timeout/10s handler → 1000 goroutines alive during 10s. Fix: normative docs. **Docs blocker for v1.0.0.**
**Cross-refs:** MM-2026-0019, MM-TM-2026-0005

---

## H-018 — reflect-based reqCtxOffset staleness across Go versions

**Premise:** `params.go:139-148`:
```go
func init() {
    t := reflect.TypeOf(http.Request{})
    for i := 0; i < t.NumField(); i++ {
        f := t.Field(i)
        if f.Name == "ctx" {
            reqCtxOffset = f.Offset
            break
        }
    }
}
```
If a future version of Go:
- Rename `ctx` to another name (e.g. `context`) → `reqCtxOffset = 0` silently
- Remove the field → same
- Move to struct embedding → `NumField` does not see
- Makes `http.Request` opaque (private) → init panics on access

All lead to `unsafe.Add(r, 0)` write at wrong offset → silent corruption OR panic that stdlib catches. **Specific danger:** automatic Go upgrade in CI → tests pass (routing works, static routes work), but param routes give wrong behavior.

**Mitigations:**
- Assert in `init()` that the field found is correct type: `if f.Type != reflect.TypeOf((*context.Context)(nil)).Elem() { panic }` — `panic` in init makes program fail immediately with clear error
- Add test that asserts `reqCtxOffset != 0` and that `unsafe.Add(r, reqCtxOffset)` reads a value compatible with `context.Context` after `r.WithContext(x)` equality.

**Testability:**
```go
// After r.WithContext(ctxX), read via unsafe.Add and assert equals ctxX
```

**Assigned:** go-sast-and-memory-auditor + concurrency-security-auditor
**Priority:** Medium (latent on toolchain upgrade)
**Status:** **PARTIAL** — finding MM-2026-0035 (CSA-010 + SAST-001). H-018 PASSES on Go 1.26.2 (`TestH018_ReqCtxOffsetAgreement`). Preventive test gate added by SAST agent in `harness/h018_ctx_field_type_test.go`. Additional preventive fix recommended: assert `f.Type == reflect.TypeOf((*context.Context)(nil)).Elem()` in init with panic if fails.
**Cross-refs:** MM-2026-0035

---

## H-019 — Regex compile cost in addRoute enables pre-serve DoS

**Premise:** `tree.go:223-225`:
```go
re, err := regexp.Compile("^(?:" + expr + ")$")
```
Go `regexp` uses RE2, which is linear-time for match but **compilation** can be O(2^n) in time/space for patterns like `(a|a)*` (in reality Go rejects via limit `SyntaxError`, but the limit is high — up to ~100 operations).

If the application registers routes dynamically (hypothetical) with pattern based on input, attacker provokes CPU/memory spike during registration.

**Testability:** Measure `regexp.Compile` time for progressively complex synthetic patterns. Document upper bound observed.

**Assigned:** fuzzing-and-property-engineer + dos-resilience-tester
**Priority:** Low (requires dynamic registration — not supported)
**Status:** **REFUTED** — DOS agent confirmed RE2 linear; Go regex limit rejects exponential patterns. 10k alternations compile in 380µs. Not-exploitable.

---

## H-020 — path.Clean + // serialization open redirect (overlap with H-007)

**Premise:** Refinement of H-007 focused on a specific variant. `path.Clean("//evil.com/path")` in Go returns `/evil.com/path` (not `//evil.com/path`) — verified. Therefore the clean output is safe. BUT: if attacker sends `/../evil.com/path`, `path.Clean` returns `/evil.com/path`. If `r.URL` still has `Scheme=http Host=legit.com`, then `r.URL.String()` produces `http://legit.com/evil.com/path` — safe (internal path).

HYPOTHESIS THAT REMAINS: `r.URL.String()` may omit Host in certain configs (e.g. if `httptest.NewRequest` did not set Host). Then serializes only `/evil.com/path`. Browser receiving `Location: /evil.com/path` interprets as same-origin — safe. Browser receiving `Location: //evil.com/path` interprets cross-host — unsafe. What is the actual output?

**Testability:** `http.Redirect` wraps `Location` with `r.URL.ResolveReference` logic; should sanitize. But confirm exhaustively:
- Cleaned path starts with `//` — happens ever?
- Cleaned path with backslash `\\evil` — Go net/url rejects in parse?

**Assigned:** http-protocol-security-auditor
**Priority:** Medium (refinement of H-007)
**Status:** **REFUTED (variant `//` open redirect)** + **CONFIRMED (variant canonicalization discloses routes)**. The sub-aspect open-redirect is refuted (see H-007). The sub-aspect "FixedPath reveals canonical form of hidden routes" is confirmed as MM-2026-0005.
**Cross-refs:** MM-2026-0005

---

## H-021 — recoverer panic stack disclosure to os.Stderr

**Premise:** `recoverer.go`:
```go
fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
```
`debug.Stack()` includes function names, file paths, and **line numbers** — sufficient for partial reverse-engineering. If the operator ingests stderr into SIEM or log aggregator with weaker ACL than the binary, attacker that causes panic (e.g. sends payload that triggers `json.Unmarshal` panic in a handler) sees internal layout.

Additionally: `%v` of `rcv` may include arbitrary bytes — if attacker provokes `panic(evilString)`, the evil string (with ANSI escapes, CRLF) enters directly into stderr.

**Proposed mitigation:** structure output as JSON escaped; redact paths via build flag.

**Testability:** Handler panic with `panic("\r\n\x1b[2J" + secret)`. Inspect stderr capture.

**Assigned:** middleware-security-reviewer
**Priority:** Medium (CWE-209 info disclosure, CWE-117 log injection)
**Status:** **CONFIRMED** — finding MM-2026-0023 (DOS-008 + MSR-RE-002 + MSR-RE-003 + CSA-009). Panic value with `Authorization: Bearer sk_live_SECRETTOKENVALUEEEEEE` reproduced in 1505 bytes of stderr. ANSI escapes preserved. Fix: `RecovererWithLogger(slog.Logger)` with optional redaction; deprecate current.
**Cross-refs:** MM-2026-0023

---

## H-022 — Pre-middleware path mutation bypasses group auth

**Premise:** `Mux.Pre()` executes middleware **before** dispatch. If `Pre(CleanPath)` or `Pre(StripSlashes)` is used, these middlewares clone `r` and alter path, then call `m.dispatch`. OK — dispatch sees clean path.

HYPOTHESIS: if there is a `Pre()` that does "magic": transforms `/admin` into `/admin/cleaned` via custom algorithm, can isolate paths that look different for auth from path that ends up in the handler. However this requires explicit Pre middleware with custom logic — caller-side risk.

**More interesting:** `Pre()` is applied **to `m.dispatch`**, therefore `m.preHandler = wrapMiddleware(http.HandlerFunc(m.dispatch), m.pre)`. If caller calls `Pre` after `Handle`, the snapshot works like `Use` (H-008) — or does Pre only affect future? NO — Pre applies to m.dispatch which is invariant; the preHandler is constructed with current `m.pre`. Each `Pre(...)` **reconstructs** `preHandler`. Therefore Pre called after Handle affects all routes (different from Use). This design is **inconsistent** with Use — risk of confusion for callers.

**Testability:** Register Handle, then Pre. Verify that Pre runs on requests for Handle. Yes — consistent with the construct of `preHandler`.

**Assigned:** middleware-security-reviewer (docs + invariant)
**Priority:** Low (inconsistency; document)
**Status:** **PARTIAL** — confirmed de jure by structural analysis. Did not produce specific MM-NNN finding because it is design inconsistency, not bug. Subsumed in MM-2026-0014 (race in Pre/Use).

---

## H-023 — Silent cross-request param retention in rc.small array

**Premise:** `params.go:106-110`:
```go
type requestCtx struct {
    context.Context
    params Params
    pattern string
    small [3]Param  // shared backing for params
}
```
In `mux.go:473`: `rc.params = Params(rc.small[:copy(rc.small[:], pslice)])`. Copy fills `rc.small[0..n]`. If count=2, `rc.small[2]` still has the value from the **previous request** (because it is `[3]Param` fixed array, not zeroed on release).

Release (line 478-480): `rc.params = nil; rc.pattern = ""`. BUT `rc.small[0..2]` is not zeroed. If on the next iteration the pool returns the same `rc` and only 1 param is written, `rc.small[1..2]` contains data from the previous request. This only "leaks" if some code accesses `rc.small` directly — but `rc.params` is the correct slicing view `rc.small[:n]`, so handlers **do not** see old data **via `PathParam`**.

HYPOTHESIS: via `Value(key)` method in `requestCtx`, someone may obtain the entire `rc` (`return c` in `Value` returns `rc`) → via reflection, access `rc.small[2]` directly. Unlikely but testable.

**Status:** suspected safe, but confirm.

**Assigned:** concurrency-security-auditor (canary test)
**Priority:** Low — depends on exploitability path
**Status:** **REFUTED functional** — CSA canary 256 000 iter = 0 leaks via public API. `rc.params[:count]` restricts view correctly. Defence-in-depth: zero `rc.small` on release optional (Low hardening).

---

## H-024 — Handler-spawned goroutine with r retained causes unsafe.Add data race (refinement of H-001)

See H-001. Kept separate to include specific scenario of async logging that is common pattern: handler does `go logAsync(r.Context(), ...)` — goroutine lives 10ms, during which dispatcher overwrites `r.ctx` 100 times → race guaranteed in 1 request.

**Status:** **CONFIRMED** (same as H-001); high-confidence variant — finding MM-2026-0003.
**Priority:** Critical
**Assigned:** concurrency-security-auditor

---

## H-025 — RedirectTrailingSlash reveals routes before auth middleware

**Premise:** In `dispatch` in `mux.go:490-507`:
- If path has no handler but has TSR, ServeHTTP emits `http.Redirect` with 301/307 **without** having executed any application middleware (because middleware is wrapped to handler, and here there is no handler).

Result: unauthenticated client can distinguish between non-existent route (404) and protected route that exists only with trailing slash variation (301). The route existence is revealed **before** any auth runs.

**Testability:**
```go
r := mm.New()
r.Use(authThatDenies)
r.GET("/admin/", handler)  // trailing slash variant
// Request: GET /admin
// Expected: 301 Location: /admin/ (!! auth NOT applied, information disclosed)
```

**Severity:** High because it bypasses the "middleware guards everything" threat model.

**Mitigation:** document that TSR happens pre-middleware; or offer option `TSRRequiresAuth` that applies middleware to redirect response. Alternative: always 404 instead of 301 for path alternates (breaks UX — config).

**Assigned:** middleware-security-reviewer + path-routing-fuzzer + http-protocol
**Priority:** High → **Critical** (promoted via composition MM-TM-2026-0001)
**Status:** **CONFIRMED** — finding MM-2026-0004 (HPS-002 + PRF-004). Reproduced in HPS + PRF + differential vs chi. chi in same setup: `403 auth_calls=1`. MuxMaster: `301 Location: /admin/` auth_calls=0.
**Cross-refs:** attack tree §A1.5, §D2.2; MM-2026-0004, MM-TM-2026-0001

---

## H-026 — Global throttle not per-IP — trivial DoS on shared limit

**Premise:** `throttle.go` uses global channels without partitioning by IP. 1 attacker with 100 req concurrent exhausts the budget; all other clients receive 503.

The spec/docs should clarify that it is "throttle overall" and not "throttle per IP". The UI of the middleware (`ThrottleBacklog(limit, backlog, timeout)`) does not signal the partitioning.

**Testability:** 1 attacker socket, 1 legit client socket. Attacker opens `limit` long-running requests. Legit client → 503.

**Mitigation:** rename to `ThrottleAllBacklog` or add `ThrottlePerIP(limit, keyFunc)`.

**Assigned:** middleware-security-reviewer + dos-resilience-tester
**Priority:** High (easy DoS + user surprise)
**Status:** **CONFIRMED** — finding MM-2026-0013 (DOS-004 + MSR-TH-001). 1 attacker with 5 requests in limit=5 denies 100% of 10 different legitimate clients.
**Cross-refs:** attack tree §B6.2; MM-2026-0013, MM-TM-2026-0002

---

## H-027 — introspection Walk/Routes concurrent with Handle produces torn read

**Premise:** `introspection.go` uses `treesPtr.Load()` — atomic, OK. BUT once it has a pointer to `methodTrees`, it iterates the nodes. If another goroutine calls `Handle`, does COW of the array **but** mutates the **root** referenced inside the array (in `root.addRoute`). Therefore introspection walker sees mutations in real time — line 223 "root.addRoute(pattern, ...)" alters structure (children, indices, handler fields).

**Mitigation:** copy-on-write the nodes also, not just the array. But this breaks performance. Alternative: docs "Lookup/Walk are not safe with concurrent registration".

**Testability:** stress test — Walk in goroutine A, Handle in B. With -race. Expected: race detector reports.

**Assigned:** concurrency-security-auditor
**Priority:** Medium (violates docs contract — "no dynamic registration" — so in-practice rare)
**Status:** **CONFIRMED** — finding MM-2026-0016 (CSA-003). 63 DATA RACE warnings in 2 seconds of stress. Fix: `m.mu.RLock()` in `Walk/Routes/Lookup` (option C).
**Cross-refs:** MM-2026-0016

---

## H-028 — Unicode case-folding asymmetry in CaseInsensitive mode

**Premise:** `foldEq` in `tree.go:445`:
```go
if a >= 'A' && a <= 'Z' { a += 32 }
```
ASCII-only. If pattern is `/Café` and request is `/café` (both NFC), is not case-folded — `é` != `É`. If `/CAFE` with `CaseInsensitive=true` vs request `/cafe`, works (ASCII). But `/АDMIN` (Cyrillic А) vs `/admin` — fails (different char).

OK — NFC/NFD comparison always fails without normalization, but this is **by design** and correct (confusables should not cross-fold). HYPOTHESIS alternative: attacker sends `/Admin` (ASCII) vs route `/admin` — with CaseInsensitive=false, RedirectFixedPath uses `path.Clean` (which does not fold) → no match → 404. Then there is no fold problem point.

Status: probably **refuted**. Auditor should confirm via fuzz with Unicode input.

**Assigned:** path-routing-fuzzer
**Priority:** Low
**Status:** **REFUTED** — `foldEq` is ASCII-only by design. Confusable Cyrillic/Latin (e.g. `а=U+0430` vs `a=U+0061`) **do not** cross-fold. Behavior correct — confusables should not be equivalent by routing. PRF corpus included 20+ Unicode confusables, zero bypasses.

---

## H-029 — WWW-Authenticate realm injection

**Premise:** `basic_auth.go:24`:
```go
w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
```
If `realm` contains `"\r\n` (quote + CRLF), produces `Basic realm=""\r\nX-Injected: y"`. However Go `Header().Set` validates bytes — rejects CR/LF. If it contains quote without CR/LF, breaks the parser of the client but does not inject header. If caller passes `realm` from non-validated external source, risk of `Set()` silent drop.

**Testability:** `BasicAuth("test\r\nX: y", creds)` — return silent drop or keeps string? Test.

**Assigned:** middleware-security-reviewer + http-protocol
**Priority:** Low
**Status:** **DEFERRED (Low MM-2026-0038)** — wire is sanitized by Go stdlib; realm CRLF retained in-memory only. Docs-only.

---

## H-030 — Composite: Slowloris + timeout + goroutine leak

**Premise:** Combines timeout goroutine leak (H-017) with slowloris. Attacker:
1. Opens 1000 TCP connections
2. Drips 1 byte header per 5 seconds (slowloris)
3. Before reaching handler, Go's `Server.ReadTimeout` fires (if configured) OR `net/http` keeps accepting
4. Eventually handler starts, and attacker sends request that **blocks** on r.Body read
5. Timeout middleware cancels context
6. Handler **doesn't check ctx.Done**, blocks forever
7. Goroutine pool grows unboundedly

MuxMaster cannot fix entire (requires handler cooperation), but can document the risk explicitly.

**Assigned:** dos-resilience-tester + docs
**Priority:** Medium (composite, requires deployment context)
**Status:** **CONFIRMED** — promoted to composite **MM-TM-2026-0005**. Both sub-findings (MM-2026-0019 timeout + MM-2026-0024 slowloris docs gap) confirmed separately. Composite vector requires both mitigations: `ReadHeaderTimeout` of http.Server + handler cooperation with `ctx.Done()`.
**Cross-refs:** MM-TM-2026-0005, MM-2026-0019, MM-2026-0024

---

---

## H-031 — Auto-OPTIONS / 405 Allow header leak pré-auth (NEW — proposed by HPS-003)

**Premise:** The automatic responses of `HandleOPTIONS` and `HandleMethodNotAllowed` in `mux.go:546-565` call `m.allowed(urlPath, r.Method)` which iterates all trees and returns the Allow header with the list of available methods — **before** the auth middleware runs. An attacker discovers:
1. If a path exists (via status 204/405 vs 404).
2. Which methods are registered on that path.
3. Combined with HPS-002/PRF-004 (TSR leak), enumerates the HTTP surface of the server without auditing.

**Testability:** Register `/admin` with auth middleware that denies everything. Send `OPTIONS /admin` — expected in secure design: `401 Unauthorized`. Actual: `204 Allow: GET, POST, OPTIONS body="" auth_invocations=0`.

**Status:** **CONFIRMED** — finding MM-2026-0005 (HPS-003 + PRF-004 FixedPath variant + TSC-003). This hypothesis consolidates the HTTP, path-routing, and timing domain sub-findings into a single canonical finding.

**Assigned:** http-protocol-security-auditor (proponent), middleware-security-reviewer (fix), threat-modeler (consolidation)
**Priority:** **Critical** (blocks v1.0.0)
**Cross-refs:** MM-2026-0005, MM-TM-2026-0001

---

## H-032 — UTF-8 invariant violations in radix tree (NEW — proposed by PRF-006)

**Premise:** The radix tree in `tree.go` operates on `string` (sequence of UTF-8 bytes) but **does not validate UTF-8** in `addRoute`. Patterns with bytes ≥ 0x80 that are not valid UTF-8 (e.g. `0xFF` alone, `0xC0` without continuation byte) corrupt internal invariants:
- `n.indices += string(c)` produces string with invalid byte; subsequent byte-by-byte comparisons work but `for i, r := range path` (which iterates runes, not bytes) produces different indices.
- In certain split paths, `len(n.indices) != len(staticChildren)` → slice OOB in hot path.
- FPE-006 demonstrated `/\xf9` + `/` → slice out of range in dispatch.
- PRF-006 demonstrated `/\xff` → index out of range in `incrementChildPrio`.

**Testability:** Fuzzer target `FuzzAddRoute` with broad coverage of bytes ≥ 0x80 (invalid UTF-8, UTF-8 overlong, BMP, SMP code points). Invariant post-addRoute: `assert(len(n.indices) == num static children)`.

**Status:** **CONFIRMED** — finding MM-2026-0002 (PRF-006 + FPE-006). Two instances documented; the class is larger — entire `addRoute` function and `insertChild` need auditing for bytes vs runes iteration.

**Proposed mitigation:**
1. Reject patterns with bytes ≥ 0x80 that are not valid UTF-8 (via `utf8.Valid`), or explicitly document "patterns must be ASCII".
2. Audit `tree.go` for sites using `for i, r := range path` vs `for i := range len(path)` — they are different contracts.
3. Debug-build assert: after each `addRoute`, validate `len(n.indices) == len(staticChildren)`.

**Assigned:** path-routing-fuzzer (proponent), fuzzing-and-property-engineer, sast (bounds check), threat-modeler
**Priority:** **Critical** (blocks v1.0.0 — boot-time DoS via config-file)
**Cross-refs:** MM-2026-0002

---

## H-033 — atomic.Pointer for Mux.preHandler + public fields setters (NEW — proposed by CSA)

**Premise:** From CSA-006 and CSA-007, the current pattern of unsynchronized read of `m.preHandler`, `m.NotFound`, `m.PanicHandler` and 13 other fields is theoretically race-safe only on amd64 TSO. On ARM64 weak memory model, reads may see stale writes indefinitely. The hypothesis is: "changing all reassignable fields to `atomic.Pointer[T]` has acceptable overhead in hot path".

**Testability:** Benchstat baseline (read plain) vs proposal (`atomic.Pointer.Load`) on BenchmarkStaticRoute + BenchmarkParamRoute. Acceptable <5% regression.

**Status:** **OPEN** — new, pending benchmark. Owner: CSA + go-perf-optimizer.

**Priority:** Medium (hardening post-v1.0.0)

---

## H-034 — unsafe.Add vs r.WithContext performance trade-off (NEW — proposed by CSA)

**Premise:** If we remove `unsafe.Add` and use `r.WithContext(rc)` to definitively eliminate CSA-001 race, what is the regression in ns/op and allocs/op? If <30%, preferable to documentation-normative-only.

**Testability:**
```
bench baseline (current): BenchmarkParamRoute1: 27 ns/op, 0 allocs/op
bench proposal (r.WithContext): BenchmarkParamRoute1: ~40-45 ns/op, 1 alloc/op (estimate)
benchstat before/after: document delta
```

**Status:** **OPEN** — depends on architectural decision. Owner: go-perf-optimizer + CSA.

**Priority:** **Critical (architectural decision)** — determines fix of MM-2026-0003.

---

## Process

**Owner:** threat-modeler-and-zero-day-researcher is the only one who writes here.

**Cycle:**
1. Experts report findings in `/reports/<agent>/`.
2. Threat-modeler copies findings to `findings.md` with canonical ID.
3. If finding refines a hypothesis, update status here to `confirmed` / `partial` / `refuted`.
4. Findings that compose new hypothesis create new `H-NNN` here.
5. Release gate: all Critical/High hypotheses have status `confirmed` (ship with fix) or `refuted` (evidence).

**Change log:** each time a hypothesis changes state, add line in local changelog of that hypothesis.

## Final hypothesis matrix (summary table)

| ID | Priority | Status | Finding(s) |
|---|---|---|---|
| H-001 | Critical | **CONFIRMED** | MM-2026-0003 |
| H-002 | High | **CONFIRMED** | MM-2026-0009 |
| H-003 | High | **CONFIRMED** | MM-2026-0006 |
| H-004 | High | **CONFIRMED partial** | MM-2026-0011 |
| H-005 | High | **CONFIRMED** | MM-2026-0012 |
| H-006 | High → Critical | **CONFIRMED** | MM-2026-0007 |
| H-007 | High | **REFUTED** | — |
| H-008 | Critical | **CONFIRMED (docs)** | — (design/docs) |
| H-009 | High | **CONFIRMED** | MM-2026-0008 |
| H-010 | High | **CONFIRMED** | MM-2026-0018 |
| H-011 | Medium | **CONFIRMED** (accepted risk) | MM-2026-0026 |
| H-012 | Medium → High | **CONFIRMED** | MM-2026-0010 |
| H-013 | Medium | **CONFIRMED partial** | MM-2026-0022 |
| H-014 | Low | **DEFERRED** | docs-only |
| H-015 | High | **CONFIRMED** | MM-TM-2026-0002 |
| H-016 | Low | **REFUTED** | — |
| H-017 | Medium | **CONFIRMED** | MM-2026-0019 |
| H-018 | Medium | **PARTIAL** | MM-2026-0035 (test gate added) |
| H-019 | Low | **REFUTED** | — |
| H-020 | Medium | **REFUTED (base)** / **CONFIRMED (variant)** | MM-2026-0005 |
| H-021 | Medium | **CONFIRMED** | MM-2026-0023 |
| H-022 | Low | **PARTIAL** | subsumed MM-2026-0014 |
| H-023 | Low | **REFUTED functional** | optional hardening |
| H-024 | Critical | **CONFIRMED** | MM-2026-0003 (variant) |
| H-025 | High → Critical | **CONFIRMED** | MM-2026-0004 |
| H-026 | High | **CONFIRMED** | MM-2026-0013 |
| H-027 | Medium | **CONFIRMED** | MM-2026-0016 |
| H-028 | Low | **REFUTED** | — |
| H-029 | Low | **DEFERRED (Low)** | MM-2026-0038 |
| H-030 | Medium | **CONFIRMED** | MM-TM-2026-0005 |
| **H-031** (new) | **Critical** | **CONFIRMED** | MM-2026-0005 |
| **H-032** (new) | **Critical** | **CONFIRMED** | MM-2026-0002 |
| **H-033** (new) | Medium | **OPEN** (post-v1.0.0) | — |
| **H-034** (new) | Critical (decision) | **OPEN** | determines MM-2026-0003 fix |

**Totals:** 34 hypotheses. 22 confirmed (+ 4 composites consolidated). 5 refuted. 4 partial. 2 deferred. 2 new open.

---

## Hypotheses from the 2026-09-25 findings reconciliation

Raised by rmp #240 (findings reconciliation); states as of rmp #267 (2026-09-25, commit `a510565`). Canonical record: `findings.md` B.6. Written in English (CLAUDE.md §13.2).

## H-RECON-01 — Cross-request contamination under the opt-in pooled modes

**Premise:** CSA-2026-0056 proves the absence of cross-request param contamination only for the GC-managed default. `Mux.PoolRequestBundle` (Opt O13) and `Mux.PoolFastParams` (Opt O9) recycle `reqBundle` / `Params` storage across requests through `sync.Pool`, and no test ran a contamination canary with either flag set — they appeared only in benchmarks. A dirty object returned to a pool (normal or panic path) would leak one request's params into another.

**Testability:** run concurrent canaries (every param tier: 1, 2, 3, overflow, catch-all) with each flag on, separately and together, under `-race`; add panic-path checks that the pool stays clean; add a handler that retains `r` past return to pin the documented retention hazard.

**Assigned:** concurrency-security-auditor
**Priority:** High
**Status:** **REFUTED** (rmp #263) — `pool_contamination_test.go` (64 goroutines × 3000 requests per tier, both flags separately and combined, `-race`): no cross-request contamination, no dirty object returned to a pool. The retention hazard is pinned by `TestPoolRequestBundle_RetentionHazard_Documented` and `TestPoolFastParams_RetentionHazard_Documented`. Report: `concurrency-security-auditor/2026-09-25-H-RECON-01-pooled-mode-canaries.md`.

**Finding(s):** CSA-2026-0056 (scope extended).

---

## H-RECON-02 — Timing harnesses that pass silently on wrong status codes

**Premise:** TSC-2026-0009 showed `TestTiming_ErrorOracle_404vs405` measuring 401 vs 401 (BasicAuth registered with `Use` wrapped the fallback handlers) and passing. Any `TestTiming_*` whose arms do not return the intended status codes measures the wrong thing, and every accepted timing oracle in `SECURITY.md` depends on these harnesses.

**Testability:** every `TestTiming_*` asserts each arm's status code before computing statistics and on every sample; a deliberately wrong expected status must make the test fail.

**Assigned:** timing-and-sidechannel-analyst
**Priority:** Medium
**Status:** **CONFIRMED and FIXED** (rmp #264) — `VerifyArmStatus` (`timing-and-sidechannel-analyst/harness/timing.go`) runs as a preflight per arm and inside the sample loop of all 19 `TestTiming_*`; demonstrated to fail on a wrong expected status. Follow-up O-9/O-10 resolved by rmp #270.

**Finding(s):** TSC-2026-0009.

---

## H-RECON-03 — Security tasks closed with unmet acceptance criteria

**Premise:** security tasks in rmp are closed without their acceptance criteria being satisfied in the repository, so the ledger and SECURITY.md can claim a mitigation that does not exist. Evidence at reconciliation: 4 of the 34 reconciled closed tasks (#151, #166, #176, #60) failed their criteria; none of the 35 originating tasks records a completion summary; many were closed milliseconds after being started.

**Testability:** for every closed rmp task that carries a finding ID, check each acceptance criterion against the repository at HEAD (file, test, SECURITY.md section, measured figure) and record met / unmet with evidence.

**Assigned:** threat-modeler-and-zero-day-researcher
**Priority:** Medium
**Status:** **OPEN** (planned: rmp #268) — the 4 known cases are now met (verified at `a510565`): #151 by rmp #264, #166 and #60 by rmp #266, #176 by rmp #265 through its documentation branch (the `-count=1000` all-GOOS branch was never exercised). The general test over all closed security tasks has not been run; the 4 tasks still have `completion_summary = null`.

**Finding(s):** — (process hypothesis).

### Summary rows (append to the matrix above)

| ID | Priority | Status | Finding(s) |
|---|---|---|---|
| H-RECON-01 | High | **REFUTED** (rmp #263) | CSA-2026-0056 |
| H-RECON-02 | Medium | **CONFIRMED — fixed** (rmp #264) | TSC-2026-0009 |
| H-RECON-03 | Medium | **OPEN** | — |
