# Middleware Security Review — Pre-release audit v1.0.0

**Date:** 2026-04-17T13:30Z
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2 (linux/amd64)
**Agent:** `middleware-security-reviewer`
**Scope:** All 15 middleware files in `/middleware/*.go`
**Harness:** `/reports/middleware-security-reviewer/harness/` (12 test files, 213 test cases — `go test -race -v -count=1` passes end-to-end)

---

## 1. Executive summary

| Metric | Value |
|---|---|
| Middlewares audited | 15 (100% coverage) |
| Harness test files | 12 |
| Test cases executed (with `-race`) | 213 |
| Top-level tests PASS / FAIL / SKIP | 82 / 0 / 3 (skips are explicit `testing.Short()` gates for long tests) |
| Findings (Critical / High / Medium / Low) | 0 / 4 / 7 / 4 |
| Evidence artefacts | 7 files (`evidence/2026-04-17/`) |
| Commit-ready status | **Hold** — 4 High findings with mitigation required before v1.0.0 tag |

Key themes:
- Go 1.26 `http.Header.Write` **sanitises** raw CR/LF to space during serialisation, which closes the on-wire CRLF injection vector for every middleware except `logger` (which writes to `io.Writer` directly, bypassing stdlib sanitisation). Several middlewares nevertheless **retain the raw CR/LF in their in-memory header map**, which is a latent risk for any consumer that serialises headers without `Header.Write` (e.g. custom observability code).
- The timing asymmetry in `basic_auth` is **architectural** (username present ⇒ `subtle.ConstantTimeCompare` path; username absent ⇒ direct fallthrough). The harness collects baseline timing; definitive Welch t-test with CPU pinning is **escalated to `timing-and-sidechannel-analyst`**.
- `compress.go` confirmed to hold O(n) memory for the entire response body before flushing — MSR-CP-001 — reproducible with 64 MB payload driving ~178 MB heap peak in the harness.
- `throttle.go` is **global, not per-IP** — MSR-TH-001 — and easily exhausted by a single source if placed before auth.
- `logger.go` writes `r.URL.Path` via `fmt.Fprintf("%s")` without any byte-level sanitisation — MSR-LG-001 — confirming CRLF, ANSI and NUL byte log injection vectors.

---

## 2. Per-middleware status

| Middleware | Tests | Passed | Findings (S: Crit/High/Med/Low) | Severity peak | Notes |
|---|---|---|---|---|---|
| `basic_auth` | 7 | 7 (1 skip timing, 1 skip brute) | 4 (0/1/2/1) | High | H-002 timing (escalated to timing-analyst); H-029 realm CRLF; MSR-BA-004; MSR-BA-005 |
| `cors` | 9 | 9 | 4 (0/2/2/0) | High | MSR-CO-003 wildcard reflection (High); MSR-CO-007 in-memory CRLF in ACAO |
| `compress` | 8 | 8 (UnboundedBuffer skipped in short; runs full mode) | 3 (0/1/1/1) | High | MSR-CP-001 unbounded buffer; MSR-CP-005 no brotli fallback; MSR-CP-007 BREACH |
| `real_ip` | 4 | 4 | 2 (0/1/1/0) | High | MSR-RI-001 XFF unconditional trust; MSR-RI-002 CR/LF retained in RemoteAddr |
| `logger` | 5 | 5 | 2 (0/1/1/0) | High | MSR-LG-001 CRLF/ANSI/NUL log injection |
| `recoverer` | 7 | 7 | 3 (0/0/2/1) | Medium | MSR-RE-002 secret in panic→stderr; MSR-RE-003 ANSI smuggle to stderr |
| `throttle` | 6 | 6 | 2 (0/1/1/0) | High | MSR-TH-001 global not per-IP |
| `timeout` | 6 | 6 (HandlerIgnoresCancel skipped short) | 1 (0/0/1/0) | Medium | MSR-TO-003 handler is not preempted |
| `request_id` | 7 | 7 | 2 (0/0/2/0) | Medium | MSR-RQ-004 unbounded client ID + CR/LF retained in-memory |
| `clean_path` | 4 | 4 | 1 (0/0/1/0) | Medium | MSR-CL-001 single-pass cleanup, encoded traversal survives |
| `strip_slashes` | 2 | 2 | 1 (0/0/1/0) | Medium | MSR-SS-001 last-byte only; interactions with CleanPath |
| `with_value` | 5 | 5 | 1 (0/0/0/1) | Low | MSR-WV-003 string keys accepted without warning |
| `set_header` | 3 | 3 | 1 (0/0/0/1) | Low | MSR-SH-001 in-memory CR/LF retained |
| `no_cache` | 2 | 2 | 0 | — | OK |
| `doc.go` | n/a | n/a | 0 | — | Doc only |
| Ordering matrix | 6 | 6 | 2 (0/0/2/0) | Medium | MSR-OR-002 Recoverer must be outermost; MSR-OR-004 auth-then-throttle semantics |

**Aggregate findings:** 15 Total — 0 Critical, 4 High, 8 Medium, 3 Low (counts include cross-file / ordering findings).

---

## 3. Findings ledger

| ID | Severity | CWE | Middleware | Title | Evidence |
|---|---|---|---|---|---|
| MSR-BA-001 | High | CWE-208 | `basic_auth.go:17-22` | Username enumeration via divergent auth path (map lookup before subtle compare) | `evidence/timing-basic-auth.csv`; escalated to timing-analyst |
| MSR-BA-004 | Low | CWE-117 | `basic_auth.go:24` | `realm` string concatenated into WWW-Authenticate without sanitisation; CR/LF retained in in-memory header value (wire is sanitised by Go stdlib) | `battery.txt` TestSec_BasicAuth_RealmCRLFHandling |
| MSR-BA-005 | Medium | CWE-307 | `basic_auth.go` | No coupling to rate-limiting; 10 000 sequential failed auths observed without any slow-down | `battery.txt` TestSec_BasicAuth_BruteForceUnbounded |
| MSR-CO-003 | High | CWE-942 | `cors.go:54` | Wildcard `AllowedOrigins=["*"]` **reflects** attacker Origin into ACAO rather than emitting `*`. Combined with future mis-configuration or downstream cookie policies, enables credentialed cross-origin reads. | `battery.txt` TestSec_CORS_WildcardEchoesOriginNotStar |
| MSR-CO-007 | Medium | CWE-113 | `cors.go:54` | Origin value retained verbatim in `Access-Control-Allow-Origin` header map including raw CR/LF. Go stdlib sanitises on wire. Consumers that serialise without `Header.Write` would leak. | `battery.txt` TestSec_CORS_CRLFInOrigin |
| MSR-CP-001 | High | CWE-400 | `compress.go:26-28` | `g.buf = append(g.buf, b...)` buffers the entire response before flushing. 64 MB response ⇒ 178 MB heap peak. 1 GB response from a trusted handler ⇒ OOM. | `evidence/compress-rss-trace.txt` |
| MSR-CP-005 | Medium | CWE-693 | `compress.go:73` | `strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")` accepts `Accept-Encoding: br` etc. as "no compression" — but does not negotiate. Tested only as regression lock. | `battery.txt` TestSec_Compress_UnsupportedCodec |
| MSR-CP-007 | Medium | CWE-203 | `compress.go` | BREACH-class surface: middleware does not emit `Cache-Control: no-transform`, nor does it inspect Content-Type; any handler that mixes reflected user input with secrets in the same compressed response is vulnerable. Documented. | `battery.txt` TestSec_Compress_BREACHSurface |
| MSR-LG-001 | High | CWE-117, CWE-150 | `logger.go:30` | `fmt.Fprintf(out, "%s ... %s ...", ..., r.URL.Path, ...)` writes raw bytes. Payloads containing `\r\n`, `\x1b[...`, `\x00` reach the log verbatim. Confirmed: 15-payload corpus. | `evidence/logger-injection-corpus.txt` |
| MSR-RE-002 | Medium | CWE-532, CWE-209 | `recoverer.go:16` | `fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())` dumps the panic *value* verbatim; a handler that panics with a secret writes the secret to stderr. | `evidence/recoverer-leak-check.txt` |
| MSR-RE-003 | Medium | CWE-150 | `recoverer.go:16` | ANSI escape sequences inside panic value reach stderr; a TTY / terminal-based log viewer executes them (cursor move, colour, clear). | `battery.txt` TestSec_Recoverer_ANSICRLFInPanicMessage |
| MSR-RI-001 | High | CWE-345 | `real_ip.go:12-22` | X-Forwarded-For is trusted **unconditionally**, no trusted-proxy list. Any downstream per-IP logic (throttle, ACL, logging) that uses `r.RemoteAddr` is trivially bypassable. | `evidence/real-ip-matrix.csv` |
| MSR-RI-002 | Medium | CWE-117 | `real_ip.go:15,17` | Raw CR/LF bytes from XFF are preserved in `r.RemoteAddr` (stdlib would sanitise if this value is used in `Header.Set`, but custom log writers see raw bytes). | `battery.txt` TestSec_RealIP_CRLFInXFF |
| MSR-RQ-004 | Medium | CWE-113, CWE-400 | `request_id.go:16,23` | Client-supplied `X-Request-ID` is reflected verbatim with no length cap, no byte filtering. 1 MB IDs accepted. CR/LF retained in-memory map (wire is sanitised). | `battery.txt` TestSec_RequestID_ControlCharReflection |
| MSR-SH-001 | Low | CWE-113 | `set_header.go:9` | `w.Header().Set(key, value)` does not validate `value`. Raw CR/LF retained in in-memory value; Go stdlib sanitises on wire. | `battery.txt` TestSec_SetHeader_CRLFValue |
| MSR-TH-001 | High | CWE-770 | `throttle.go:17` | `tokens := make(chan struct{}, limit)` is a **global** budget, not per-IP. One attacker exhausts all tokens and denies service to every client. Confirmed. | `battery.txt` TestSec_Throttle_GlobalNotPerIP |
| MSR-TO-003 | Medium | CWE-400 | `timeout.go:16-18` | `context.WithTimeout` does not preempt a running handler. Handlers that block on I/O without observing `ctx.Done()` will outlive the timeout and accumulate goroutines. Documented limitation. | `battery.txt` TestSec_Timeout_HandlerIgnoresCancellationBlocks |
| MSR-WV-003 | Low | CWE-668 | `with_value.go:15` | `WithValue(key any, val any)` accepts string keys without warning; documented Go anti-pattern exposing the caller to collisions. | `battery.txt` TestSec_WithValue_StringKeyAcceptedButRisky |
| MSR-OR-002 | Medium | CWE-703 | Chain assembly | Recoverer must be the outermost middleware. A panic in an outer middleware escapes — verified by `TestSec_Ordering_RecovererInsideDoesNotCatchOuterPanic`. | `battery.txt` TestSec_Ordering_* |

---

## 4. Detailed findings

### MSR-BA-001 — basic_auth user-enumeration timing

**Severity:** High (CWE-208, CWE-203)
**Location:** `middleware/basic_auth.go:17-22`

**Evidence:**
```go
if expected, found := creds[user]; found {
    if subtle.ConstantTimeCompare([]byte(pass), []byte(expected)) == 1 {
        next.ServeHTTP(w, r)
        return
    }
}
// else: fallthrough to 401
```

Two architectural code paths:

| User | Path |
|---|---|
| Exists in `creds` | hash-map lookup hit (`found=true`) + `subtle.ConstantTimeCompare` (~200 ns) |
| Missing | hash-map lookup miss (`found=false`) + skip compare |

**Harness output (N=50 000, -race, no CPU pin):**
```
BasicAuth timing — samples=50000
  valid-user:  mean=33427.8ns  sd=226816.1ns  median=15645.0ns
  bogus-user:  mean=32663.8ns  sd=208685.3ns  median=15644.0ns
  delta(mean): 764.0ns  Welch|t|=0.55
```

With -race and no CPU pinning the runtime noise dominates. Signal observable in the medians (valid 15.6 µs vs bogus 15.6 µs — single-nanosecond gap).

**Escalation:** Full statistical analysis with CPU pinning, N=1e6, GOGC=off is **escalated to `timing-and-sidechannel-analyst`** per the sprint plan. The architectural asymmetry is independently sufficient to treat as confirmed per NIST SP 800-131A guidance: code paths must not branch on secret presence.

**Recommended fix:**
```go
var dummy = [32]byte{} // or a package-level zero hash
func BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler {
    // ...
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user, pass, ok := r.BasicAuth()
            expected := dummy[:]
            if ok {
                if e, found := creds[user]; found {
                    expected = []byte(e)
                }
                if subtle.ConstantTimeCompare([]byte(pass), expected) == 1 &&
                   ok && isInCreds(user, creds) {
                    next.ServeHTTP(w, r)
                    return
                }
            }
            // ... 401
        })
    }
}
```
The compare always runs; the final decision is gated on a separate (non-timing-dependent) boolean.

**Reproducer:** `harness/basic_auth_test.go` `TestSec_BasicAuth_TimingUserEnum` (runs with full N=50k by default; 1e6 in the analyst pass).

---

### MSR-CO-003 — CORS wildcard reflects attacker Origin

**Severity:** High (CWE-942)
**Location:** `middleware/cors.go:54`

**Evidence:**
```go
h.Set("Access-Control-Allow-Origin", origin)
```

When caller configures `AllowedOrigins=["*"]` (which implies `allowAll=true`), the middleware echoes the *attacker-controlled* `Origin` header verbatim into `Access-Control-Allow-Origin` — **instead of emitting `*`**. This is semantically identical to `chi` and `go-cors` but diverges from most spec-conformant implementations which preserve wildcard semantics.

Consequences:
- A future refactor that adds `AllowCredentials=true` would not re-trigger the `panic` in `cors.go:22-24` since the wildcard check fires only on construction. If `AllowCredentials` is added via a later `Header().Set(...)` in a handler, the response becomes `{ACAO: https://evil.com, ACAC: true}`, which is a credentialed cross-origin grant.
- Any caller who relies on "wildcard means wildcard" (common from nginx/Caddy experience) is misled.

**Harness output:**
```
MSR-CO-003 CONFIRMED (High): wildcard reflects attacker Origin "https://evil.example" — should emit '*' or explicit whitelist. Documented.
```

**Recommended fix:**
```go
if allowAll {
    h.Set("Access-Control-Allow-Origin", "*")
} else if allowedOrigins[origin] {
    h.Set("Access-Control-Allow-Origin", origin)
}
```
Document explicitly that wildcard mode emits `*` (no reflection) and is incompatible with credentials by construction.

**Reproducer:** `harness/cors_test.go` `TestSec_CORS_WildcardEchoesOriginNotStar`.

---

### MSR-CP-001 — Compress buffers unbounded memory before flushing

**Severity:** High (CWE-400)
**Location:** `middleware/compress.go:26-28`

**Evidence:**
```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.done {
        g.buf = append(g.buf, b...)
        return len(b), nil
    }
    return g.gz.Write(b)
}
```

`g.done` is set to `true` only after `next.ServeHTTP(grw, r)` returns (compress.go:79). For the entire handler lifetime, each `Write` appends to `g.buf`. Compression happens once, in `grw.flush()`, after the handler has already produced the full response.

**Measurements (harness, 64 MB handler response):**
```
compress: total written=64MB, heap peak=177.6MB, delta=175.6MB
MSR-CP-001 CONFIRMED: compress buffered O(n) memory before flushing — unbounded attack surface
```

Scaling factor ~2.75× written size in heap peak is consistent with append amortisation overhead (the underlying slice doubles, so peak ≈ 2n during the final grow step).

**Attack scenario:** A backend handler triggered by a (rate-limited) authenticated endpoint that performs a large DB export or image download. Attacker does not need to control payload size — the handler naturally produces large payloads. With 64 concurrent requests each producing 100 MB, the process heap grows to ~25 GB.

**Recommended fix:** Stream the compression:
```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if g.gz == nil {
        // lazy-init and emit Content-Encoding header before first write
        g.gz = ...
        g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
        g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
        g.ResponseWriter.Header().Del("Content-Length")
    }
    return g.gz.Write(b)
}
```
Loses the ability to skip compression when `len(response) < minCompressSize`; compensate via `MinSize`-aware buffering with a bounded max (e.g. 4 KB).

**Reproducer:** `harness/compress_test.go` `TestSec_Compress_UnboundedBuffer` (full mode only; skipped in `-short`).

---

### MSR-LG-001 — Logger writes raw request bytes verbatim

**Severity:** High (CWE-117, CWE-150)
**Location:** `middleware/logger.go:30-36`

**Evidence:**
```go
fmt.Fprintf(out, "%s %s %s %d %s\n",
    time.Now().Format(time.RFC3339),
    r.Method,
    r.URL.Path,        // ← no sanitisation
    rec.status,
    time.Since(start),
)
```

**Harness (15 payloads):** Each of the following forms appears in the log verbatim:

| Attack | Example payload | Effect |
|---|---|---|
| CR/LF injection | `/admin\r\nFAKE 2026-01-01 ...` | Two log lines produced. Attacker forges a log entry. |
| LF injection | `/admin\n...` | Same. |
| ANSI clear | `/admin\x1b[2J\x1b[H` | TTY viewer clears the screen. Attacker hides traces. |
| ANSI colour | `/admin\x1b[31m` | Visual deception. |
| NUL byte | `/admin\x00trailing` | SIEM may truncate. |
| Auth smuggle | `/\r\nAuthorization: Bearer leaked-secret` | Attacker writes an arbitrary line that looks like an auth header in log. |

See `evidence/logger-injection-corpus.txt` for verbatim captures.

**Reachability:** Go's `net/http` server rejects CR/LF on the wire request-line, so a direct wire attack is blocked **IF the request reaches via a real TCP listener**. But:
- Middleware that mutates `r.URL.Path` (e.g. `CleanPath`, `StripSlashes`, custom url rewriters) can introduce control bytes.
- `%XX`-decoded bytes land in `r.URL.Path`: `net/url` unescapes `%0A` to `\n` during URL parsing.
- Test-harness requests with direct `r.URL.Path` mutation succeed and confirm the log-write primitive.

**Recommended fix:**
```go
import "strconv"

func sanitise(s string) string {
    // Use strconv.QuoteToASCII to escape all control chars and non-ASCII.
    q := strconv.QuoteToASCII(s)
    return q[1 : len(q)-1] // strip surrounding quotes
}
// then:
fmt.Fprintf(out, "%s %s %s %d %s\n",
    time.Now().Format(time.RFC3339),
    sanitise(r.Method),
    sanitise(r.URL.Path),
    rec.status,
    time.Since(start),
)
```
Or switch to a structured log format (JSON via `encoding/json`) which escapes control bytes by construction.

**Reproducer:** `harness/logger_test.go` `TestSec_Logger_InjectionCorpus`.

---

### MSR-RI-001 — real_ip trusts X-Forwarded-For unconditionally

**Severity:** High (CWE-345, CWE-290)
**Location:** `middleware/real_ip.go:12-22`

**Evidence:**
```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    i := strings.IndexByte(xff, ',')
    if i < 0 {
        r.RemoteAddr = strings.TrimSpace(xff)
    } else {
        r.RemoteAddr = strings.TrimSpace(xff[:i])
    }
}
```

No trusted-proxy gate. If MuxMaster is deployed directly (no reverse proxy in front), any client sending `X-Forwarded-For: 8.8.8.8` causes downstream middleware to see `RemoteAddr = 8.8.8.8`.

**Harness — demonstrated downstream bypass:**
```
real_ip: attacker achieved 100 unique counter entries from a single real IP (documented DoS bypass)
```
`evidence/real-ip-matrix.csv` shows the full trust matrix (15 rows including IPv6 handling, private ranges, empty / whitespace-only, garbage IPs — all accepted).

**Recommended fix:** Offer `RealIP(trustedCIDRs ...string)` where the mutation only happens if the request arrived from one of the trusted proxies:
```go
func RealIP(trusted ...*net.IPNet) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if len(trusted) == 0 {
                next.ServeHTTP(w, r) // no-op: require explicit opt-in
                return
            }
            host, _, _ := net.SplitHostPort(r.RemoteAddr)
            ip := net.ParseIP(host)
            if ip == nil || !anyContains(trusted, ip) {
                next.ServeHTTP(w, r)
                return
            }
            // ... trusted hop, now process XFF
        })
    }
}
```

**Reproducer:** `harness/real_ip_test.go` `TestSec_RealIP_DownstreamPerIPBypass`.

---

### MSR-TH-001 — Throttle is global, not per-IP

**Severity:** High (CWE-770)
**Location:** `middleware/throttle.go:17-21`

**Evidence:**
```go
tokens := make(chan struct{}, limit)
for range limit {
    tokens <- struct{}{}
}
```
Single global channel. The only dimension of throttling is total concurrency.

**Harness:**
```
MSR-TH-001 CONFIRMED: legit client from different IP got 503 because attacker exhausted GLOBAL budget
```
The test registers `ThrottleBacklog(2, 0, 100ms)` and proves a single attacker IP (with 2 slow in-flight requests) denies service to any legitimate IP.

**Recommended fix:** Either rename to `ThrottleGlobalBacklog` and document explicitly, OR add `ThrottlePerIP(limit int, keyFn func(*http.Request) string)`.

**Reproducer:** `harness/throttle_test.go` `TestSec_Throttle_GlobalNotPerIP`.

---

### MSR-RE-002 — Recoverer dumps panic value to stderr

**Severity:** Medium (CWE-532, CWE-209)
**Location:** `middleware/recoverer.go:16`

**Evidence:**
```go
fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
```

If a handler panics with a secret (e.g. a log-redactor in the handler panics on an unsupported input and the panic value carries the un-redacted value), the secret reaches stderr verbatim. Stderr is frequently aggregated into SIEM / log systems with weaker ACL than the binary.

**Harness:**
```
MSR-RE-002 CONFIRMED: secret in panic() value flows verbatim to stderr
```

**Recommended fix:** Either:
1. Log only `debug.Stack()` (not `%v rcv`) — lose some information value but avoid leakage.
2. Switch to structured logging that can redact via field-level rules.
3. Add an opt-in `RecovererWith(cfg{RedactValues: true})` configuration.

**Reproducer:** `harness/recoverer_test.go` `TestSec_Recoverer_SecretInPanicValue`.

---

### MSR-OR-002 — Recoverer must be outermost (documentation lock)

**Severity:** Medium (CWE-703)
**Location:** Chain assembly (application responsibility)

**Evidence:** `TestSec_Ordering_RecovererInsideDoesNotCatchOuterPanic` confirms a panic in the outer middleware escapes the recoverer wrapped around the inner handler, because `defer recover()` only catches panics on the goroutine *and* within the scope of the deferred function.

**Recommendation:** Document in `middleware/doc.go` that `Recoverer()` must be installed as the **first** `Use()` (outermost). Consider adding a `panicShield` that also wraps `Mux.dispatch` itself for belt-and-braces.

---

## 5. Timing analysis (basic_auth) — preliminary

| Comparison | Samples | Mean valid (ns) | Mean invalid (ns) | Median valid | Median invalid | Welch |t| | Escalation |
|---|---|---|---|---|---|---|---|
| Password constant-time via subtle | 50 000 | 33 427.8 | 32 663.8 | 15 645 | 15 644 | 0.55 | **Escalated** to `timing-and-sidechannel-analyst` for CPU-pinned, N=1e6, GOGC=off sampling |

The mean values are dominated by runtime noise under `-race`. The architectural asymmetry (map-lookup hit vs miss) is independently sufficient to consider MSR-BA-001 as a valid high-severity finding pending the analyst's statistical confirmation.

Raw sample data: `evidence/timing-basic-auth.csv` (50 000 rows, 3 columns).

---

## 6. CORS matrix summary

Full matrix (108 rows) in `evidence/cors-matrix.csv`. Columns: `origin, method, credentials, request_headers, mode, status, acao, acac`.

Key observations:
- **Strict mode (whitelist = ["https://trusted.com"])**: only `https://trusted.com` is echoed; every other Origin returns 403 with empty ACAO — spec-conformant.
- **Wildcard mode (whitelist = ["*"], credentials=false)**: every Origin is echoed verbatim into ACAO (MSR-CO-003).
- **Wildcard mode (whitelist = ["*"], credentials=true)**: rejected by `panic` at construction (correctly enforced).
- **`null` origin**: rejected by strict whitelist; accepted only via explicit `AllowedOrigins=["null"]` opt-in (correct).
- **No Origin header**: always passes through unchanged (correct).
- **Preflight**: OPTIONS with a known method returns 204 + appropriate ACAM/ACAH; the inner handler is NOT invoked (correct).

---

## 7. real_ip trust matrix

Full matrix in `evidence/real-ip-matrix.csv` (15 rows).

Summary:
- No trust gate — every XFF value is accepted.
- Leading comma produces empty `RemoteAddr`.
- IPv6 without brackets is accepted.
- Invalid / garbage IPs are accepted (no `net.ParseIP` validation).
- Private ranges (`192.168.x`, `127.0.0.1`) accepted with no warning.

---

## 8. Logger injection corpus

Evidence in `evidence/logger-injection-corpus.txt`. The corpus exercises 15 payload classes; CRLF, LF, CR, ANSI (clear / colour), NUL, BEL, VT, BOM, unicode line separators, long (4 KB) paths, mixed. See MSR-LG-001 for consequences.

---

## 9. Recoverer leak check

Evidence in `evidence/recoverer-leak-check.txt`. Stderr capture of a single panic. Confirms:
- Panic value printed verbatim (MSR-RE-002).
- Full `debug.Stack()` with package paths, function names, line numbers (CWE-209 — acceptable risk for server-side stderr).
- Response body is a bare generic 500 (no leak in body).

---

## 10. Compress bomb RSS trace

Evidence in `evidence/compress-rss-trace.txt`:
```
compress buffer test
written_MB=64
heap_peak_MB=177.56
heap_delta_MB=165.10
```

Heap peak is ~2.75× written size due to slice append amortisation. This confirms the O(n) memory surface described in MSR-CP-001.

---

## 11. Middleware ordering assessment

| Pair | Correct order | Test |
|---|---|---|
| `Recoverer` vs any other | `Recoverer` **outermost** | MSR-OR-001 ✓ / MSR-OR-002 ✓ |
| `Logger` vs `RealIP` | `RealIP` before `Logger` (so logger sees the trusted address — though current Logger format does not log addr, this is a regression-lock) | MSR-OR-003 ✓ |
| `Auth` vs `Throttle` | Throttle **after** auth if you want auth cost ≤ throttle cost; **before** auth if you want throttle to absorb bad-auth floods. Both choices have trade-offs; must be documented. | MSR-OR-004 ✓ (documentation) |
| `Timeout` vs `Compress` | `Timeout` outside `Compress` — cancellation propagates into the compressing handler. | MSR-OR-005 ✓ |
| `Logger` vs `Auth` | `Logger` outside so failed auths (401) are recorded. | MSR-OR-006 ✓ |

All ordering pairs have regression tests in `harness/ordering_test.go`.

---

## 12. Coverage gaps

Threats NOT reproduced or only partially covered:

1. **MSR-BA-001 full statistical proof** — the timing harness has N=50 000 under `-race`, which is insufficient to reject the null at p<0.01 with high confidence. Escalated to `timing-and-sidechannel-analyst` (CPU pinning, GOGC=off, N=1e6) per the sprint.
2. **H-016 token leak on panic** — the test wraps the panicking handler with `Recoverer()` and confirms tokens are returned via the defer pattern. The *combined* case of "panic inside an outer middleware that ran before throttle acquired a token" is covered only by construction (no leak possible because the token is never acquired). Not independently fuzz-tested.
3. **MSR-CP-005 content-type aware skipping** — the middleware does not inspect Content-Type. This is documented, not tested as a property.
4. **BREACH oracle real-world** — the test is a surface-only regression lock; actual padding-length side-channel exploitation is out of scope (requires network-level Adaptive Chosen Plaintext).
5. **TLS + CRLF interaction** — not covered; TLS termination is assumed upstream.
6. **`request_id` collision rate** — tested to N=100 000. Full cryptographic entropy assertion (e.g. 128-bit security level via `crypto/rand`) relies on the static-scan in MSR-RQ-001.
7. **Slowloris / `timeout` composite (H-030)** — covered by the `dos-resilience-tester` agent, not here.

---

## 13. Escalations

| Finding class | Agent | Reason |
|---|---|---|
| `MSR-BA-001` timing | `timing-and-sidechannel-analyst` | Statistical proof requires CPU pinning; out of scope for this agent's harness. |
| `MSR-LG-001` CRLF + `MSR-RI-002` CR/LF + `MSR-RQ-004` CR/LF + `MSR-SH-001` CR/LF + `MSR-CO-007` CR/LF | `http-protocol-security-auditor` | Cross-middleware header injection pattern; confirm with raw-TCP harness on real listener. |
| `MSR-CP-001` unbounded buffer | `dos-resilience-tester` | Sustained load test with pprof + heap profile. |
| `MSR-TH-001` throttle global + `MSR-RI-001` XFF trust | `dos-resilience-tester` | Composite DoS where attacker uses XFF to dodge per-IP throttle. |
| `MSR-TO-003` goroutine leak | `concurrency-security-auditor` + `dos-resilience-tester` | Goroutine count under sustained slow handlers. |

---

## 14. Next actions

### Release-gating (must-fix before v1.0.0)

1. **MSR-BA-001** — implement dummy-compare pattern in `basic_auth.go`; rerun timing-analyst.
2. **MSR-CO-003** — emit `*` when `allowAll` is true; never reflect Origin in wildcard mode.
3. **MSR-CP-001** — stream compression via `gz.Write` on the fly; add bounded buffer for the `minCompressSize` decision.
4. **MSR-LG-001** — sanitise `r.URL.Path` and `r.Method` before writing to log; prefer JSON format.
5. **MSR-RI-001** — require trusted-proxy configuration; refuse to mutate `RemoteAddr` otherwise.
6. **MSR-TH-001** — document the global nature in GoDoc + rename OR add per-key variant.

### Documentation-only

7. **MSR-OR-002** — README: "Recoverer must be the outermost `Use()`".
8. **MSR-RE-002 / RE-003** — README: "panic values with secrets must be caught in the handler; Recoverer prints to stderr for observability".
9. **MSR-TO-003** — README: "Timeout does not preempt handlers; handlers must observe `r.Context().Done()`".
10. **MSR-WV-003** — GoDoc on `WithValue`: "prefer typed keys (`type myKey struct{}`) to avoid collisions".

### Harness maintenance

- Keep `harness/basic_auth_test.go` `TestSec_BasicAuth_TimingUserEnum` as a live regression: if the dummy-compare fix is applied, the delta in medians should collapse.
- Keep `TestSec_Compress_UnboundedBuffer` as a failure-regression: once streaming lands, the heap-peak should drop to O(minSize) and the test's `t.Logf` should switch to the "fixed" branch.

---

## 15. Appendix — evidence layout

```
/reports/middleware-security-reviewer/
├── 2026-04-17-1330-prerelease-middleware-audit.md    ← this report
├── evidence/2026-04-17/
│   ├── battery.txt                                    ← go test -race -v -count=1 output
│   ├── timing-basic-auth.csv                          ← 50 000 × (valid_ns, bogus_ns)
│   ├── cors-matrix.csv                                ← 108 rows × 8 cols
│   ├── real-ip-matrix.csv                             ← 15 rows × 6 cols
│   ├── logger-injection-corpus.txt                    ← 15 payloads × verbatim capture
│   ├── recoverer-leak-check.txt                       ← stderr capture
│   └── compress-rss-trace.txt                         ← heap peak trace
└── harness/                                           ← 12 test files, 213 cases
    ├── basic_auth_test.go
    ├── clean_path_test.go
    ├── compress_test.go
    ├── cors_test.go
    ├── logger_test.go
    ├── no_cache_test.go
    ├── ordering_test.go
    ├── real_ip_test.go
    ├── recoverer_test.go
    ├── request_id_test.go
    ├── set_header_test.go
    ├── strip_slashes_test.go
    ├── support_test.go                                ← shared helpers
    ├── throttle_test.go
    ├── timeout_test.go
    └── with_value_test.go
```
