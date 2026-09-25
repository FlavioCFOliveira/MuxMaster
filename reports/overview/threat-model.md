# MuxMaster — Threat Model (STRIDE) — Post-Sprint

**Date:** 2026-04-17 (Phase 3 — consolidation)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Matrix complete with confirmed findings. Cells RISK/OPEN → CONFIRMED (with ID MM-2026-NNNN), REFUTED, ACCEPTED, or PASS.

---

## 1. Method

STRIDE applied by component (rows) × threat class (columns). Each cell has a state:

- **PASS** — demonstrated secure (with test/evidence)
- **OPEN** — analysis pending in this sprint
- **RISK** — concrete hypothesis to investigate
- **N/A** — not applicable with justification
- **ACCEPTED** — documented and accepted risk (e.g. public by design)

Each cell points to the responsible agent(s) for validation.

Column legend:
- **S** = Spoofing
- **T** = Tampering
- **R** = Repudiation
- **I** = Information disclosure
- **D** = Denial of service
- **E** = Elevation of privilege

## 2. STRIDE Matrix — router core (CLOSED)

Legend of final states: **CONFIRMED (MM-NNNN)** / **REFUTED** / **ACCEPTED** / **PASS** / **N/A**.

| Component | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| `ServeHTTP` entry (mux.go) | **PASS**: method case-sensitive confirmed by design (HPS-008 + method-dispatch-matrix.csv) | **PASS**: stdlib rejects literal CRLF in request-target (400); decoded CRLF flows to logger — **CONFIRMED MM-2026-0006** | **N/A** | **CONFIRMED MM-2026-0046**: error-oracle 15/15 pairs distinguishable (ACCEPTED) | **PASS**: `allowed()` cost O(methods × k) is Info in MM-2026-0036 | **CONFIRMED MM-2026-0005**: Mount `/*` tree + auto-OPTIONS bypass method ACL |
| Radix tree lookup (tree.go:getValue) | **N/A** | **CONFIRMED MM-2026-0005**: RedirectFixedPath reveals route | **N/A** | **CONFIRMED MM-2026-0026**: route-existence timing (ACCEPTED — radix intrinsic); **CONFIRMED MM-2026-0005**: redirect disclosure | **REFUTED H-019**: RE2 linear; exponential patterns rejected | **CONFIRMED MM-2026-0001**: wildcard shadow → invalid node type panic in dispatch |
| `addRoute` (tree.go) | **N/A** | **CONFIRMED MM-2026-0002**: non-ASCII corrupts indices/children; **CONFIRMED MM-2026-0021**: index OOB in `/{…}*name` | **N/A** | **N/A** | **REFUTED H-019** (RE2); **CONFIRMED MM-2026-0032**: pathological UTF-8 loop | **N/A** |
| `requestCtx` via unsafe.Add (params.go) | **N/A** | **CONFIRMED MM-2026-0003**: race writing/restoring r.ctx cross-goroutine (CSA-001) | **N/A** | **REFUTED functional H-023**: 256k canary 0 leaks (defense-in-depth zero `rc.small` optional) | **PASS** (pool integrity 0 mismatches in GC storm) | **CONFIRMED MM-2026-0003**: handler captures r + reads ctx → cross-req leak |
| `paramsBuf` (tree.go) | **N/A** | **CONFIRMED MM-2026-0010**: silent overflow (PRF-003 + DOS-003 + FPE-004) | **N/A** | **REFUTED H-023** (params iterated via `[:n]`) | **N/A** | **CONFIRMED MM-2026-0010**: auth-middleware assumes 5 params but only 3 captured |
| Introspection (Routes, Walk, Lookup) | **N/A** | **CONFIRMED MM-2026-0016**: Walk concurrent with addRoute (63 RACE warnings/2s) | **N/A** | **ACCEPTED**: caller responsibility to not expose publicly | **N/A** | **N/A** |
| `Mount` (mux.go) | **N/A** | **CONFIRMED MM-2026-0022**: RawPath divergence (PRF-005) | **N/A** | **PASS** (clone OK; H-013 partial) | **N/A** | **PASS** (Mount does not bypass method tree by design) |
| `ServeFiles` (mux.go, group.go) | **N/A** | **CONFIRMED MM-TM-2026-0003**: catch-all + clean_path + `..%2f` | **N/A** | **ACCEPTED**: stdlib FileServer rejects `..` (internal path.Clean); symlink is caller config | **N/A** | **CONFIRMED MM-TM-2026-0003**: traversal via clean_path composition |
| `RedirectTrailingSlash` | **N/A** | **PASS**: CRLF in path rejected by stdlib in request line; decoded CRLF only affects logger (MM-2026-0006) | **N/A** | **CONFIRMED MM-2026-0004**: reveals route before auth | **N/A** | **REFUTED H-007**: `path.Clean("//...")` collapses, Location relative same-origin |
| `RedirectFixedPath` | **N/A** | **CONFIRMED MM-2026-0005**: path.Clean discloses canonical | **N/A** | **CONFIRMED MM-2026-0005**: discloses hidden routes via canonicalization | **N/A** | **REFUTED H-007** (base variant); **CONFIRMED MM-2026-0005** (enumeration variant) |
| `Mux.dispatch` (hot path) | **N/A** | **N/A** | **N/A** | **PASS** | **PASS**: MM-2026-0036 allocation amplification is info | **N/A** |
| Public fields (`NotFound`, `MethodNotAllowed`, `PanicHandler`, `ErrorHandler`, 8 bool flags) | **CONFIRMED MM-2026-0017**: race in reassignment post-start | **N/A** | **N/A** | **ACCEPTED**: caller responsibility (custom handlers) | **N/A** | **N/A** |
| `wrapMiddleware` (mux.go) | **N/A** | **N/A** | **N/A** | **N/A** | **N/A** | **CONFIRMED H-008 (docs)**: chain built at registration — `Use()` after `Handle()` silently ignored (propose normative docs) |
| `Use()`/`Pre()` | **CONFIRMED MM-2026-0014**: race vs Handle/ServeHTTP | — | — | — | — | — |
| Handler panic cleanup | **N/A** | **CONFIRMED MM-2026-0015**: r.ctx leaked + rc leaked (CSA-004/005) | — | — | — | — |
| `reqCtxOffset` init | **N/A** | **CONFIRMED partial MM-2026-0035**: H-018 latent in future Go (test gate added by sast) | — | — | — | — |

Owner keys: hp=http-protocol, pr=path-routing, cc=concurrency, mw=middleware-reviewer, ts=timing-sidechannel, ds=dos-resilience, sa=sast, fz=fuzzing, tm=threat-modeler.

## 3. STRIDE Matrix — middlewares (CLOSED)

### 3.1 `basic_auth`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0009**: user enum via map lookup timing (p=0, N=1.5M) | **LOW MM-2026-0038**: realm CRLF retained in-memory (wire sanitised) | **N/A** | **CONFIRMED MM-2026-0020**: password-length oracle via subtle early-exit | **CONFIRMED MM-2026-0027**: 10k unbounded brute-force without slowdown | **CONFIRMED MM-2026-0009**: timing → username → pw brute-force chain (composite MM-TM-2026-0001) |

### 3.2 `cors`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0012**: Origin reflection with allowAll — spec violation | **CONFIRMED MM-2026-0028**: Origin CRLF retained in-memory (sanitised on wire) | **N/A** | **CONFIRMED MM-2026-0012**: CSRF bypass via reflection | **N/A** | **PASS**: `*` + AllowCredentials=true panics at config-time (guarded) |

### 3.3 `compress`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **ACCEPTED MM-2026-0047**: BREACH is handler-level, compress does not mitigate (docs) | **CONFIRMED MM-2026-0007**: unbounded buf → OOM (confirmed 64MB→178MB) | **N/A** |

### 3.4 `real_ip`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0008**: XFF spoof trivial | **CONFIRMED MM-2026-0029**: CRLF in XFF retained in r.RemoteAddr | **N/A** | **N/A** | **CONFIRMED MM-2026-0008**: downstream throttle bypass (composite MM-TM-2026-0002) | **CONFIRMED MM-2026-0008**: IP-based ACL bypass |

### 3.5 `logger`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0006**: CRLF/ANSI/NUL via `r.URL.Path` percent-decoded | **CONFIRMED MM-2026-0006**: log forgery / plausible deniability | **PASS**: current format does not log Auth/Cookie headers (caller responsibility) | **PASS**: stderr bytes acceptable (docs optional) | **N/A** |

### 3.6 `recoverer`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **ACCEPTED**: panics are recovered (audit trail preserved via stderr dump — docs) | **CONFIRMED MM-2026-0023**: panic value + debug.Stack() raw in stderr (attacker-controlled) | **PASS**: recovery from 16k panics concurrent without pool contamination | **N/A** |

### 3.7 `throttle`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0013**: global not per-IP | **N/A** | **N/A** | **REFUTED TSC-007**: near-limit timing is noise; not-exploitable | **REFUTED H-016**: defer cleanup correct (0 token leaks in 16k panics) | **CONFIRMED MM-2026-0013**: 1 attacker exhausts global budget |

### 3.8 `timeout`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0019**: goroutine leak 1000→1000 blocked | **N/A** |

### 3.9 `request_id`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0011**: client X-Request-ID override without validation | **CONFIRMED MM-2026-0011**: CRLF retained in-memory (sanitised on wire) | **CONFIRMED MM-2026-0011**: forge request_id to confuse correlation | **N/A** | **CONFIRMED MM-2026-0011**: 1MiB X-Request-ID amplifies 1024× | **N/A** |

### 3.10 `clean_path`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0018**: single `path.Clean` pass — encoded traversal bypass | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0018**: 136 bypass combinations in matrix (MM-TM-2026-0003) |

### 3.11 `strip_slashes`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0025**: non-idempotent (only 1 trailing slash) | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0025 (variant)**: interaction with CleanPath + RedirectTrailingSlash |

### 3.12 `set_header`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **LOW MM-2026-0037**: CRLF retained in-memory (sanitised on wire by Go) | **N/A** | **N/A** | **N/A** | **N/A** |

### 3.13 `with_value`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **LOW MM-2026-0039**: `any` key accepted — string collision risk (caller responsibility) | **N/A** | **ACCEPTED**: if val is secret, caller must not pass to logging middleware | **N/A** | **N/A** |

### 3.14 `no_cache`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **PASS** | **N/A** | **N/A** |

## 4. STRIDE aggregate — coverage

| Component | S | T | R | I | D | E | Coverage |
|---|---|---|---|---|---|---|---|
| Core router | 1/1 | 2/2 | 0/0 | 3/3 | 3/3 | 3/3 | 12/12 to validate |
| Radix tree | 0 | 1/1 | 0 | 1/1 | 2/2 | 2/2 | 6/6 |
| Params / pool | 0 | 1/1 | 0 | 1/1 | 1/1 | 1/1 | 4/4 |
| Introspection | 0 | 1/1 | 0 | 1/1 | 0 | 0 | 2/2 |
| Mount / ServeFiles | 0 | 1/1 | 0 | 2/2 | 0 | 2/2 | 5/5 |
| Redirect | 0 | 2/2 | 0 | 2/2 | 0 | 2/2 | 6/6 |
| Middleware assignment | 1/1 | 0 | 0 | 1/1 | 0 | 1/1 | 3/3 |
| **basic_auth** | 1/1 | 1/1 | 0 | 1/1 | 1/1 | 1/1 | 5/5 |
| **cors** | 1/1 | 1/1 | 0 | 1/1 | 0 | 1/1 | 4/4 |
| **compress** | 0 | 0 | 0 | 1/1 | 1/1 | 0 | 2/2 |
| **real_ip** | 1/1 | 1/1 | 0 | 0 | 1/1 | 1/1 | 4/4 |
| **logger** | 0 | 1/1 | 1/1 | 1/1 | 1/1 | 0 | 4/4 |
| **recoverer** | 0 | 0 | 1/1 | 1/1 | 1/1 | 0 | 3/3 |
| **throttle** | 1/1 | 0 | 0 | 1/1 | 1/1 | 1/1 | 4/4 |
| **timeout** | 0 | 0 | 0 | 0 | 1/1 | 0 | 1/1 |
| **request_id** | 1/1 | 1/1 | 1/1 | 0 | 1/1 | 0 | 4/4 |
| **clean_path** | 0 | 1/1 | 0 | 0 | 0 | 1/1 | 2/2 |
| **strip_slashes** | 0 | 1/1 | 0 | 0 | 0 | 1/1 | 2/2 |
| **set_header** | 0 | 1/1 | 0 | 0 | 0 | 0 | 1/1 |
| **with_value** | 0 | 1/1 | 0 | 1/1 | 0 | 0 | 2/2 |
| **no_cache** | 0 | 0 | 0 | 0 | 0 | 0 | 0/0 |

Total cells with RISK/OPEN to close in this sprint: **76**.

## 5. Known mitigations (present in code)

| Mitigation | Location | What it covers |
|---|---|---|
| `subtle.ConstantTimeCompare` | `basic_auth.go:18` | password comparison; **does not** cover user-enum via map lookup |
| `crypto/rand` | `request_id.go:18` | id generation |
| `http.ErrAbortHandler` propagation via `defer recover()` | `recoverer.go` | panic containment (but stderr dump) |
| `CORSOptions.AllowCredentials` + `*` → panic | `cors.go:22` | prevents forbidden combination **at config-time** |
| `atomic.Pointer[methodTrees]` | `mux.go:107` | lock-free read path |
| `sync.Mutex` in `Handle` | `mux.go:159` | protects tree COW |
| `path.Clean` in `clean_path` and `RedirectFixedPath` | — | normalizes `.` and `..` **textual** |
| Wildcard validation (`findWildcard`) | `tree.go:485` | rejects multiple wildcards in segment |
| `r.Clone(r.Context())` in `Mount`/`ServeFiles`/`clean_path`/`strip_slashes` | — | avoids mutating shared `*http.Request` — BUT does not cover `unsafe.Add` in `mux.go` |

## 6. Known gaps (explicitly not mitigated)

1. **XFF trust without proxy config** — `real_ip.go` trusts unconditionally
2. **Single-pass path normalization** — `clean_path.go` calls `path.Clean` only once
3. **Global throttle** — `throttle.go` does not do per-IP
4. **Unbounded response buffering** — `compress.go` accumulates everything before compressing
5. **No rate limit on auth** — `basic_auth.go` does not integrate with throttle
6. **Client X-Request-ID accepted without validation** — `request_id.go` reflects CRLF
7. **Logger without escape** — `logger.go` prints path raw
8. **Handler continues after timeout** — `timeout.go` only cancels context
9. **Registration races documented, not enforced** — concurrent `Use`/`Handle` with serve is UB
10. **`unsafe.Add` in request** — functional if request is goroutine-owned; but **nothing in code guarantees this**

## 7. Post-sprint prioritization (update with MM-IDs)

**Critical — v1.0.0 blockers (7):**
- MM-2026-0001 Tree corruption wildcard shadow
- MM-2026-0002 UTF-8 invariant violation
- MM-2026-0003 Race unsafe.Add (r.ctx)
- MM-2026-0004 TSR pre-auth route disclosure
- MM-2026-0005 RedirectFixedPath + auto-OPTIONS + auto-405 Allow leak
- MM-2026-0006 Logger CRLF/ANSI injection
- MM-2026-0007 Compress unbounded buffer

**High — v1.0.0 blockers (14):**
- MM-2026-0008 XFF unconditional trust
- MM-2026-0009 basic_auth user enum timing
- MM-2026-0010 paramsBuf silent overflow
- MM-2026-0011 request_id CRLF + amplification
- MM-2026-0012 CORS wildcard reflection
- MM-2026-0013 Throttle global masquerading per-IP
- MM-2026-0014 Use/Pre race vs Handle
- MM-2026-0015 Panic cleanup skip (rc leak + r.ctx leak)
- MM-2026-0016 Introspection race vs addRoute
- MM-2026-0017 Public fields race
- MM-2026-0018 clean_path single-pass bypass
- MM-2026-0019 Timeout goroutine leak (docs blocker)
- MM-2026-0020 password-length oracle
- MM-2026-0021 Registration-time OOB

**Medium (15):** MM-2026-0022 to MM-2026-0036 — see findings.md §7.

**Low (8):** MM-2026-0037 to MM-2026-0044 — docs-only / code hygiene.

**Info (3):** MM-2026-0045 to MM-2026-0047.

**Composite (5):** MM-TM-2026-0001 to MM-TM-2026-0005 — see findings.md §10.

**Refuted (5):** H-007, H-016, H-019, H-028, H-023.

## 8. Final STRIDE coverage

| Component | Total cells | PASS / Refuted | Confirmed | Accepted | Coverage |
|---|---|---|---|---|---|
| Core router | 22 | 9 | 12 | 1 | 100% |
| Radix tree | 6 | 1 | 4 | 1 | 100% |
| Params / pool | 5 | 2 | 2 | 1 | 100% |
| Introspection | 2 | 0 | 1 | 1 | 100% |
| Redirects | 6 | 2 | 4 | 0 | 100% |
| 15 middlewares | 56 | 13 | 35 | 8 | 100% |
| **TOTAL** | **97** | **27** | **58** | **12** | **100%** |

**No OPEN/RISK cells remain.**

## 9. Review

Mandatory re-evaluation:
- After each CRITICAL/HIGH fix (re-run reproducer → Verified)
- Before v1.0.0 tag (release gate)
- After any architectural change (new middleware, new transport, introspection endpoint)
- Scan for new CVEs in GHSA Go every 90 days
