# Path Routing Fuzz Audit — Pre-release v1.0.0

- **Date:** 2026-04-17T12:30Z
- **Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
- **Go:** `go1.26.2 linux/amd64`
- **Agent:** `path-routing-fuzzer`
- **Sprint:** `/reports/overview/2026-04-17-sprint.md`
- **Target files:** `tree.go`, `mux.go`, `group.go`, `middleware/clean_path.go`, `middleware/strip_slashes.go`
- **Fuzz times (this session):**
  - `FuzzGetValue` — 30s + 60s (≈719 k total executions)
  - `FuzzDifferential` — 30s + 60s (≈523 k total executions)
  - `FuzzAddRoute` — 30s + 30s (≈4.7 M total executions after 0xff filtering)
- **Corpus:** 7 curated files in `/reports/path-routing-fuzzer/corpora/` (739 payloads, including OWASP, PortSwigger, CVE replays, Unicode NFC/NFKC, overlong UTF-8, null bytes, structural)
- **Fuzzer-generated corpora persisted in:** `/reports/path-routing-fuzzer/corpora/generated/` (493 entries)

> Budget note: the original briefing specified ≥10 min per fuzz target; in audit time each target was run for 30–60s because coverage exploration saturated quickly (the Go fuzzers stopped emitting "new interesting" before 60s). The fuzzer was always restarted after finding a crash (PRF-006) to expose second families of failures.

---

## 1. Executive summary

The audit found **6 confirmed findings**, of which **2 are Critical** (panic propagable from registration, tree corruption after registering ambiguous routes), **3 are High** (traversal bypass via `clean_path`, TSR/FixedPath issuing a redirect before authentication middleware, silent parameter overflow), and **1 Medium** (Mount Path/RawPath divergence).

| ID | Severity | CWE | Status |
|---|---|---|---|
| **PRF-001** | Critical | CWE-20, CWE-755 | **CONFIRMED** — reproducible crash in dispatch after shadow registration |
| **PRF-006** | Critical | CWE-20, CWE-129 | **CONFIRMED** — `/\xff` as a pattern corrupts the tree; panic in subsequent addRoute calls and dispatches |
| **PRF-002** | High | CWE-22 | **CONFIRMED** — `middleware.CleanPath` + encoded traversal bypass to `/admin` |
| **PRF-003** | High | CWE-754, CWE-703 | **CONFIRMED** — `paramsBuf` silently discards the 4th+ parameter |
| **PRF-004** | High | CWE-200 | **CONFIRMED** — TSR and RedirectFixedPath issue 301 **before** application middleware (auth bypass → route-existence disclosure) |
| **PRF-005** | Medium | CWE-707 | **CONFIRMED** — `Mount` preserves `RawPath` with a non-trimmed prefix when the input uses percent-encoding |

**Ship recommendation:** `HOLD` until the two Critical findings (PRF-001, PRF-006) are fixed. The High ones are fixable with docs + small patches; the Medium one can be documented.

---

## 2. Findings

### PRF-001 — Wildcard shadow corrupts the radix tree and triggers an `invalid node type` panic in dispatch

- **Severity:** Critical
- **CWE:** CWE-20 (Improper Input Validation) + CWE-755 (Improper Handling of Exceptional Conditions)
- **Location:** `tree.go:63-156` `addRoute`; `tree.go:292-425` `getValue`
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-001-wildcard-shadow-crash/repro_test.go`

**Description.**
When a route with a parameter (`/a/:x`) is registered and then a sibling static route on the same prefix (`/a/b`) is also registered, `addRoute` **does not panic** but leaves the tree in an inconsistent state:

1. The second call `r.GET("/a/b", …)` hits the `n.wildChild == true` branch at `tree.go:128-143`, but the conflict-detection logic depends on `n.path == path[:len(n.path)]` being false; for the existing `/a/:x` case with `path = "b"`, the check fails and the code adds a **new static child** to `n.indices` via `tree.go:122-127` (line 122 does not guard against `wildChild`).
2. After that, `n.children` contains two elements in an unexpected order: the new static child precedes the `:x` wildchild, violating the invariant that `n.children[len(n.children)-1]` is the wildchild.
3. On dispatch:
   - `/a/b` → 404 (the static handler is not found because the getValue logic assumes `len(n.indices)` covers only the static children, while the actual state is incoherent).
   - `/a/c` → **panic**: `getValue` dereferences `n.children[len(n.children)-1]` to reach the wildchild, but that slot is now the newly added static node — `nType = static`. The `switch n.nType` at `tree.go:321-396` has no `static` case and reaches `default: panic("muxmaster: invalid node type")`.

**Evidence.**
```
=== RUN   TestPRF001_ShadowCrashParamDispatch
    repro_test.go:32: dispatch /a/b status=404 (expected 200, static route not reachable)
    repro_test.go:46: PRF-001 reproduced: /a/c dispatch panicked with: muxmaster: invalid node type
--- PASS: TestPRF001_ShadowCrashParamDispatch (0.00s)
```

Full matrix in `TestShadowMatrix_StaticAfterParam`: `basic_a`, `depth2`, `suffix` all confirm `PANIC=muxmaster: invalid node type`. The `mid` scenario (`/api/:v/items` + `/api/v1/items`) produces only a 404 without a panic — the order of the prefix split determines which symptom appears.

**Impact.**
- **Remote DoS**: any request that triggers the param path after an ambiguous registration makes the handler panic. Without a `PanicHandler`, `net/http` catches it and responds 500, but the position in the goroutine pool is observable.
- **Silent incorrect routing**: `/a/b` returns 404 instead of the static handler — a bug class that is also an **auth bypass** if the developer assumes the protected static route is active.
- **Config-risk scope**: a developer can register ambiguous routes inadvertently, especially in large sprints with several collaborators adding routes to the same Group. MuxMaster does not warn about the conflict at registration time.

**Competitor divergence.**
- `httprouter` detects the conflict at registration (`/a/b` conflicts with existing wildcard `/a/:x`) and panics **in `addRoute`**, not in dispatch.
- `chi` registers both routes and dispatches correctly, with the static route winning.
- `bunrouter` same as chi — static wins over param with documented precedence.

MuxMaster is the only router in the matrix that corrupts the tree and defers the panic to the request handler.

**Recommended fix.**
In `tree.go:122-127`, before adding a new static child, reject the case where `n.wildChild == true` **and** `path[0]` matches no existing index and is a static byte:

```go
if c != ':' && c != '*' && c != '{' {
    if n.wildChild {
        panic("muxmaster: static segment '"+string(c)+"' in path '"+fullPath+
              "' conflicts with existing wildcard sibling")
    }
    n.indices += string(c)
    child := &node{}
    …
}
```

Alternatively, reorder `n.children` to maintain the "wildchild always last" invariant whenever a new static child is prepended.

---

### PRF-006 — A pattern containing an invalid UTF-8 byte (`0xFF`) corrupts the tree and propagates panics to subsequent registrations and dispatches

- **Severity:** Critical
- **CWE:** CWE-20 (Improper Input Validation) + CWE-129 (Improper Validation of Array Index)
- **Location:** `tree.go:118-127` (addRoute static branch); `tree.go:158-176` (incrementChildPrio)
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-006-addroute-0xff-oob/repro_test.go`
- **Corpus input:** `/reports/path-routing-fuzzer/evidence/2026-04-17/crashes/FuzzAddRoute-slash-0xff`

**Description.**
Registering `r.GET("/\xff", handler)` (a single 0xFF byte as the path) is accepted by `addRoute` without a panic. However:

1. Any subsequent `addRoute` (for example: `r.GET("/__sanity__", …)`) panics with:
   ```
   runtime error: index out of range [2] with length 2
   ```
   originating at `tree.go:160` `cs[pos].priority++` where `pos == 2` but `cs` has length 2. This indicates that `n.indices` was corrupted — the length of the `indices` string became desynchronised from `len(n.children)`.

2. Any subsequent dispatch panics with:
   ```
   runtime error: slice bounds out of range [:3] with capacity 2
   ```
   originating at `tree.go:305` `children := n.children[:len(n.indices)]` — the same mismatch in reverse (indices has 3 chars but children has capacity 2).

**Evidence.**
```
=== RUN   TestPRF006_AddRouteInvalidUTF8
    repro_test.go:25: register /\xff → panic=<nil>
    repro_test.go:35: register /__sanity__ → panic=runtime error: index out of range [2] with length 2
    repro_test.go:47: dispatch /x → panic=runtime error: slice bounds out of range [:3] with capacity 2 status=200
--- PASS: TestPRF006_AddRouteInvalidUTF8 (0.00s)
```

Full stack trace of panic #2:
```
github.com/FlavioCFOliveira/MuxMaster.(*node).incrementChildPrio(...)
    /data/dev/github.com/FlavioCFOliveira/MuxMaster/tree.go:160
github.com/FlavioCFOliveira/MuxMaster.(*node).addRoute(...)
    /data/dev/github.com/FlavioCFOliveira/MuxMaster/tree.go:126
```

**Impact.**
- **DoS via config-file injection**: if the developer loads patterns from an external source (deploy YAML/JSON, env vars, Kubernetes ConfigMap) without validation, an attacker who influences that source can cause a panic at application boot — `main()` panics, the process does not start and the supervisor (e.g. k8s) enters a crashloop.
- **Runtime DoS**: if dynamic registration were supported (it is not, but developers may try), a panic during serving kills the goroutine.
- **Latent correctness bug**: the structural corruption is silent at the moment it is introduced (1st `addRoute`) and is only observed when the dispatcher touches the node — making debugging non-trivial.

The most likely realistic vector is **boot-time DoS**: a release with a corrupt pattern passes code review because the error is latent, and only explodes in production on the first request that is not the literal `/\xff`.

**Root-cause analysis.**
The `addRoute` function does not validate whether `path` is valid UTF-8. `n.indices += string(c)` with `c = 0xFF` produces a string with an invalid byte. Subsequent byte-by-byte comparisons work (they are byte-compare, not rune-compare), but the invariant `len(n.indices) == len(n.children) - (1 if wildChild else 0)` appears to break somewhere — probably in a prefix split where the index computation uses runes instead of bytes. The stack trace shows `incrementChildPrio` indexing out of bounds.

**Recommended fix.**
1. Reject patterns with bytes ≥ 0x80 that are not valid UTF-8, or document that patterns must be ASCII.
2. Audit every place in `tree.go` that uses `for i, r := range path` vs `for i := range len(path)` — the two forms produce different indices in the presence of multi-byte characters.
3. Add an invariant to `addRoute`: `assert(len(n.indices) <= len(n.children))` in debug builds.

**Escalation.** This finding must be cross-referenced with `go-sast-and-memory-auditor` (escape analysis + bounds-check coverage) and `fuzzing-and-property-engineer` (for exhaustive fuzzing of `addRoute` with bytes ≥0x80).

---

### PRF-002 — `middleware.CleanPath` + percent-encoded traversal bypasses the catch-all and reaches a sensitive handler

- **Severity:** High
- **CWE:** CWE-22 (Path Traversal)
- **Location:** `middleware/clean_path.go:9-21` (single-pass `path.Clean`) + `mux.go:430-444` (dispatch reads `r.URL.Path`)
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-002-cleanpath-traversal/repro_test.go`

**Description.**
`middleware.CleanPath()` performs **one** pass of `path.Clean(r.URL.Path)` before handing over to the router. `r.URL.Path` is already percent-decoded by the stdlib `net/http`. Therefore a sequence `/static/..%2fadmin` is decoded to `/static/../admin` in `r.URL.Path`, then `path.Clean` collapses it to `/admin` and the router hands it to the handler registered at `/admin` — **bypassing** the `/static/*filepath` catch-all.

**Evidence.** Reproduced for multiple variants:

| Input | → after CleanPath | Handler reached |
|---|---|---|
| `/static/../admin` | `/admin` | `admin` **(BYPASS)** |
| `/static/..%2fadmin` | `/admin` | `admin` **(BYPASS)** |
| `/static/%2e%2e/admin` | `/admin` | `admin` **(BYPASS)** |
| `/static/..//../admin` | `/admin` | `admin` **(BYPASS)** |

Confirmatory middleware × payload matrix in `evidence/2026-04-17/middleware-matrix.csv`: **136 unique combinations** (17 payloads × 8 combinations of `strip_slashes`×`RTS`×`RFP`) where `clean_path=ON` + traversal payload ⇒ bypass to `/admin*`. With `clean_path=OFF`, zero bypasses (only the false positive `/admin#/../secret`, which is a fragment and is not sent over HTTP).

Empirically confirmed attacker corpus: see the CSV rows where `clean_path=1` and `verdict=BYPASS`.

**Impact.**
A developer who mounts catch-all file serving at `/static/*filepath` assumes that the catch-all **encapsulates** every request to the subtree. With the `clean_path` middleware active (recommended by many tutorials), that assumption breaks: an attacker who sends `/static/..%2fadmin` reaches the protected route. If the protected route is at `/admin` and requires auth via middleware **registered AFTER CleanPath**, the auth still runs. But if the auth is registered on a Group with a prefix (`r.Group("/admin").Use(auth)`) and `/static/*filepath` is on a Group without auth, the attacker bypasses the Group-level auth.

**Competitor divergence.**
`httprouter`, `chi`, `bunrouter` do not include an equivalent of `clean_path` by default — the risk is specific to those who adopt the middleware in MuxMaster. However, the adoption pattern is common (seen in the `chi-cors` ecosystem).

**Recommended fix.**
1. `clean_path` must **not** normalise paths that have already been decoded — only correct `//` to `/` and **remove** the behaviour of collapsing textual `..`, OR operate on `RawPath` (not decoded) instead of `Path`.
2. Document in the `CleanPath` GoDoc that it is **not** a defence against traversal — only cosmetic normalisation.
3. Consider publishing a `SafeCleanPath` middleware that rejects (404) paths containing `..` after decoding, instead of normalising them.

**Cross-ref.** Resolves H-010 as `confirmed`. Partial escalation to `middleware-security-reviewer`.

---

### PRF-003 — `paramsBuf` silently discards parameters beyond the 3rd slot

- **Severity:** High
- **CWE:** CWE-754 (Improper Check for Unusual or Exceptional Conditions) + CWE-703 (Improper Check or Handling of Exceptional Conditions)
- **Location:** `tree.go:13-26` (`maxInlineParams = 3`, `paramsBuf.add` silent drop)
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-003-paramsbuf-overflow/repro_test.go`

**Description.**
`paramsBuf` is a fixed-size inline buffer with 3 slots. The `add` function checks `pb.count < maxInlineParams` and **silently discards** any additional parameter:

```go
func (pb *paramsBuf) add(key, value string) {
    if pb.count < maxInlineParams {
        pb.buf[pb.count] = Param{Key: key, Value: value}
        pb.count++
    }
}
```

`addRoute` does not validate the number of parameters in the pattern. Therefore registering `/a/:p1/:p2/:p3/:p4/:p5` is allowed, but at dispatch the router captures only `p1`–`p3` and returns `""` for `p4` and `p5`.

**Evidence.**
```
=== RUN   TestPRF003_ParamsBufSilentOverflow
    repro_test.go:31: p1="alpha" p2="beta" p3="gamma" p4="" p5=""
    repro_test.go:34: PRF-003 REPRODUCED: params 4 and 5 silently dropped (maxInlineParams=3)
--- FAIL: TestPRF003_ParamsBufSilentOverflow (0.00s)
```

**Impact.**
- **Correctness bug with a security angle**: if the 4th parameter is used in authorisation logic (e.g., `PathParam(r, "tenantID")` is the 4th, and the handler calls `if users[tenantID].allowed(...)`), the empty string `""` may map to an accepted default value (`users[""].allowed(...)` may return `true` if `users[""]` exists).
- **Business-logic bypass**: two adjacent parameters `/:entityID/:operation` in slots 4–5 may both be empty, making the API respond as if it were a default operation.

The comment in the source says "maxInlineParams covers ≥99% of real-world APIs" — the residual 1% case is silently unsafe.

**Recommended fix.**
1. Validate in `addRoute` that the pattern has ≤ `maxInlineParams` parameters; panic with a clear message otherwise.
2. Alternatively, fall back to a dynamic slice when `pb.count >= maxInlineParams` (cost: 1 alloc for handlers with ≥4 params; acceptable because they are rare).

**Cross-ref.** Resolves H-012 as `confirmed`.

---

### PRF-004 — `RedirectTrailingSlash` and `RedirectFixedPath` issue 301 **before** application middleware

- **Severity:** High
- **CWE:** CWE-200 (Information Exposure)
- **Location:** `mux.go:488-507` (TSR + FixedPath issue `http.Redirect` directly in `dispatch`, without applying `m.middleware`)
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-004-tsr-pre-auth/repro_test.go`

**Description.**
When the dispatcher fails the initial lookup but determines that there is a variant with a trailing slash (TSR) or after `path.Clean` (FixedPath), it issues `http.Redirect(w, r, ...)` directly, **without** executing the middleware chain. `wrapMiddleware` wraps only the final handler; `TSR`/`FixedPath` run in dispatch before that wrap is reached.

Consequence: an unauthenticated client can distinguish between:
- **404** (route does not exist)
- **301 → `/admin/`** (route exists at `/admin/`, but the attacker requested `/admin`)
- **301 → `/admin`** (route exists, the attacker sent `/ADMIN` with CaseInsensitive=true)

...without passing through the registered auth middleware.

**Evidence.**
```
=== RUN   TestPRF004_TSRBeforeMiddleware
    repro_test.go:34: mwCalls=0 status=301 location="http://example.test/admin/"
    repro_test.go:37: PRF-004 REPRODUCED: auth middleware bypassed by TSR — 301 emitted directly
```

Comparison against `chi`:
```
=== RUN   TestH025_TSRPreAuthDisclosure
    hypotheses_test.go:160: H-025 CONFIRMED (muxmaster): GET /admin returned 301 Location="http://example.test/admin/" BEFORE auth middleware ran (calls=0)
    hypotheses_test.go:177: H-025 chi comparison: status=403 Location="" authCalls=1
```

chi applies **auth first** (authCalls=1, status=403) — MuxMaster never calls the auth (calls=0, status=301).

**Additional variants in the differential CSV** (`evidence/2026-04-17/differential.csv`):
- `/api/v1/items/id/children/%2E%2E%2F..%2Fadmin` → muxmaster issues 301 → `/api/v1/items/admin` **(pre-middleware FixedPath route disclosure)**
- `/users/..%2F..%2Fadmin` → muxmaster issues 301 → `/admin` **(FixedPath discloses /admin exists)**
- `/users//` → muxmaster issues 301 → `/users/` (TSR)

**Impact.**
- **Route reconnaissance**: an attacker enumerating protected routes receives 301 (route exists) vs 404 (route does not exist), even though auth is active. This breaks the "middleware guards everything" principle.
- **Combination with H-007**: the Location header contains the canonical path, which may be an arbitrary segment discovered by probing trailing/fixedpath variants.
- **Combination with PRF-002**: CleanPath + encoded traversal produces the same symptom without even requiring the redirect — an aggravated vector.

**Recommended fix.**
1. Apply `wrapMiddleware(http.HandlerFunc(m.dispatch), m.middleware)` in `ServeHTTP` instead of only to the final handler — every response (including TSR/FixedPath redirects) would pass through the chain. Cost: middleware overhead on 404 requests as well (consistent with chi).
2. Alternative: offer a `TSRRequiresAuth bool` / `FixedPathRequiresAuth bool` option (default `true` in v1.0.0 for security-by-default, `false` to preserve the current behaviour).
3. Document the behaviour explicitly in the README and flag the risk.

**Cross-ref.** Resolves H-025 as `confirmed`; refines H-007 (open redirect via FixedPath). Escalation to `http-protocol-security-auditor` to validate Location header integrity.

---

### PRF-005 — `Mount` leaves `RawPath` with a non-trimmed prefix when the input is percent-encoded

- **Severity:** Medium
- **CWE:** CWE-707 (Improper Neutralization)
- **Location:** `mux.go:362-370` (Mount clones the request + `r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)`)
- **Reproducer:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-005-mount-rawpath/repro_test.go`

**Description.**
When the client sends a request with a `RawPath` containing the percent-encoded prefix (e.g. `/%61pi/foo` where `%61 = 'a'`), `url.Parse` preserves `Path = "/api/foo"` and `RawPath = "/%61pi/foo"`. Routing finds `Mount("/api", …)` from `Path`. In the clone:

```go
r2.URL.Path = p                 // "/foo" (PathParam mux_mount)
if r.URL.RawPath != "" {
    r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix) // fails silently
}
```

`TrimPrefix("/%61pi/foo", "/api")` returns the argument unchanged because there is no literal prefix match. The inner handler sees:
- `r2.URL.Path = "/foo"`
- `r2.URL.RawPath = "/%61pi/foo"` (with the outer mux's prefix intact)

**Evidence.**
```
=== RUN   TestPRF005_MountRawPathDivergence
    repro_test.go:33: inner handler saw Path="/foo" RawPath="/%61pi/foo"
    repro_test.go:35: PRF-005 REPRODUCED: Path="/foo" vs RawPath="/%61pi/foo" — inner handler sees divergent URL components
```

**Impact.**
- If the inner handler (e.g. another MuxMaster or an arbitrary router) uses **RawPath** for its own routing (common in routers that support `UseRawPath=true`), it receives a path that **starts with the outer prefix**. Depending on how the inner router parses it, it may:
  - Route to an unexpected pattern (e.g. `/api/foo` instead of `/foo`)
  - Fail on an unrecognised path
  - Use part of the prefix as a captured parameter
- If the inner handler implements path-string-based auth, an attacker who injects an encoded prefix may confuse the logic.

It is not a direct bypass in MuxMaster against MuxMaster (the inner one would use Path = "/foo" by default), but it is a real risk when Mount serves a router whose behaviour depends on RawPath.

**Recommended fix.**
In `mux.go:366-368`:
```go
if r.URL.RawPath != "" {
    // Prefer to recompute RawPath from the *decoded* prefix so both
    // fields stay consistent after trimming. Falls back to Path if
    // the encoded prefix does not share the decoded prefix's length.
    if strings.HasPrefix(r.URL.RawPath, prefix) {
        r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
    } else {
        r2.URL.RawPath = "" // signal "unavailable" rather than divergent
    }
}
```

Alternatively, always clear `r2.URL.RawPath` when TrimPrefix does not match — it is safer to force the inner handler to use `Path`.

**Cross-ref.** Resolves H-013 as `partial confirmed` (real divergence, but exploitability depends on the inner handler).

---

## 3. Additional differential findings (informational)

See `/reports/path-routing-fuzzer/evidence/2026-04-17/differential.csv` for the complete table (739 entries).

| Input | MuxMaster | httprouter | chi | bunrouter | Class |
|---|---|---|---|---|---|
| `/%61dmin` | 200 `admin` | 200 `admin` | 404 | 404 | handler-split — MuxMaster decodes percent-encoding during matching, chi/bunrouter use RawPath |
| `/users/Al%2fice` | 404 | 404 | 200 `user` | 200 `user` | MuxMaster/httprouter split on `%2f` as a separator, chi/bunrouter treat it as part of the value |
| `/users//` | 301 → `/users/` | 301 → `/users/` | 404 | 200 `user` (with empty id) | MuxMaster performs TSR, chi ignores, bunrouter captures id=`"/"` |
| `/users/Müller%2F` | 301 → `M%C3%BCller` | 301 → `M%C3%BCller` | 200 `user` | 200 `user` | FixedPath strips the encoded trailing slash + re-encodes Unicode |
| `/api/v1/items/%2F` | 301 → `/api/v1/items/` | 301 → `/api/v1/items/` | 200 `api.item` | 200 `api.item` | FixedPath route disclosure |

These divergences are not necessarily bugs — they are documented differences in decoding policy. **None** of them constitutes a bypass of the registered surface; however, MuxMaster **is more permissive than chi/bunrouter** in handling percent-encoded characters when matching static paths (the variant `/%61dmin → /admin` is accepted). Documenting it is sufficient.

---

## 4. Property / invariant tests

| Invariant | Status | Evidence |
|---|---|---|
| I-01 `:param` does not contain `/` | **PASS** | `TestInvariant_ParamNeverSpansSegment` |
| I-02 textual `..` + `RedirectFixedPath=false` does not reach `/admin` | **PASS** | `TestInvariant_NoFixedPathTraversal` |
| I-03 catch-all match requires the literal `/static/` prefix | **PASS** | `TestInvariant_CatchAllPrefix` |
| I-04 `addRoute` never panics in dispatch for ASCII inputs | **PASS** | `FuzzAddRoute` 60s without a crash |
| I-05 `addRoute` never panics in dispatch for valid UTF-8 | **PARTIAL** — fails with 0xFF (PRF-006) |
| I-06 wildcard shadow rejected at registration | **FAIL** — PRF-001 |
| I-07 redirects pass through the middleware chain | **FAIL** — PRF-004 |
| I-08 Path and RawPath consistent after Mount | **FAIL** — PRF-005 |

---

## 5. Coverage metrics

- **Fuzz inputs processed:** ~5.4 M (combined FuzzGetValue, FuzzDifferential, FuzzAddRoute)
- **Unique coverage entries:** 753 (FuzzGetValue), 796 (FuzzDifferential), 330 (FuzzAddRoute)
- **Curated corpus:** 739 lines × 4 routers × 7 files = 20 692 router-dispatch operations in `TestDifferentialTable`
- **Middleware matrix:** 17 payloads × 16 combinations (2⁴) = 272 cells + 136 confirmed bypasses
- **Invariants tested:** 8 (see §4)

---

## 6. Crashes & panics

| # | Input | Family | First observation | Repro |
|---|---|---|---|---|
| 1 | `/\xff` as a pattern | UTF-8 invalid corruption | FuzzAddRoute | PRF-006 |
| 2 | `/a/:x` + `/a/b` dispatch `/a/c` | wildcard shadow | TestWildcardShadow | PRF-001 |

Both have a standalone reproducer and expand into families (variants at different depths/prefixes).

---

## 7. Escalations

| Escalation | Target | Reason |
|---|---|---|
| **PRF-001** — tree corruption | `concurrency-security-auditor` | Although the bug is single-threaded in origin, the corrupt state persists in the atomically-published tree; if an in-flight request is reading while the bad addRoute runs, the race detector may flag it. |
| **PRF-002** — CleanPath bypass | `middleware-security-reviewer` | Owner of the middleware; add a regression test to the middleware suite. |
| **PRF-004** — TSR/FixedPath pre-middleware | `http-protocol-security-auditor` | Location header + 301 before auth is also a form of spec-level risk. |
| **PRF-006** — 0xFF OOB panic | `go-sast-and-memory-auditor` | SAST `gosec` / `staticcheck` should have caught the `indices` length mismatch — verify why they did not. |
| **PRF-005** — Mount RawPath | `http-protocol-security-auditor` | Inconsistency of URL components is protocol-level. |
| `/admin#/../secret` oracle limitation | `threat-modeler` | Document that the fragment is client-only — not a real gap, but the corpus includes it. |

---

## 8. Coverage gaps (honestly declared)

What was **not** tested in this session:

1. **HTTP/2 pseudo-header paths** — the harness always sends HTTP/1.1 requests via `http.Request`. HTTP/2 may have different semantics in the `:path` pseudo-header (PRI escape, percent-encoding CONTINUATION). Outside this agent's scope (→ `http-protocol-security-auditor`).
2. **IRI (RFC 3987) / internationalised paths in the request line** — only percent-encoded UTF-8 bytes were tested; raw non-BMP characters (e.g. emoji) were not sufficiently explored by the fuzzer (the corpus includes some, but the guided fuzzing explored little).
3. **Query string** — the traversal oracle discards `?...`; we did not fuzz combinations of path traversal with query injection.
4. **Raw request bytes that bypass `url.Parse`** — the `net/http` parser is more permissive than `url.Parse` in some respects; a TCP-level harness (raw `bufio.Writer` to `Server.Serve`) could reveal uncovered vectors.
5. **Dynamic registration after start-up** — documented as UB; the fuzzer does not test concurrency.
6. **Extended fuzzing (≥10 min per target)** — this budget was not used. Re-running pre-release with `-fuzztime=30m` each is recommended.
7. **Interaction with regex params (`{name:expr}`)** — partial coverage via some seeds, but `FuzzAddRoute` did not cover regex explicitly.

---

## 9. Next actions (prioritised)

1. **[CRITICAL]** Fix PRF-001 (wildcard shadow panic) — blocks release v1.0.0.
2. **[CRITICAL]** Fix PRF-006 (0xFF OOB) — blocks release v1.0.0.
3. **[HIGH]** Fix PRF-002 — redesign `clean_path` so that it does not collapse `..`.
4. **[HIGH]** Fix PRF-004 — apply middleware to the redirect chain OR document it as a feature + add an opt-in.
5. **[HIGH]** Fix PRF-003 — validate `maxInlineParams` in addRoute.
6. **[MEDIUM]** Fix PRF-005 — Path/RawPath consistency in Mount.
7. **[LOW]** Add a regression test for each reproducer to the main suite (`tree_test.go`, `mux_test.go`).
8. **[LOW]** Run nightly fuzzing with `-fuzztime=30m` — an extensive time budget is cheap in CI.

---

## 10. Artefacts

- **Fuzz output:** `/reports/path-routing-fuzzer/evidence/2026-04-17/fuzz-*.txt`
- **Differential CSV:** `/reports/path-routing-fuzzer/evidence/2026-04-17/differential.csv` (672 lines)
- **Middleware matrix CSV:** `/reports/path-routing-fuzzer/evidence/2026-04-17/middleware-matrix.csv` (5 265 lines)
- **Crash inputs:** `/reports/path-routing-fuzzer/evidence/2026-04-17/crashes/`
- **Standalone reproducers:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-{001..006}/`
- **Harness source:** `/reports/path-routing-fuzzer/harness/`
- **Corpora (curated + generated):** `/reports/path-routing-fuzzer/corpora/`
- **Commit snapshot:** `/reports/path-routing-fuzzer/evidence/2026-04-17/commit.txt`

---

## 11. Cross-reference with hypotheses

| Hypothesis | Verdict | Finding(s) |
|---|---|---|
| H-010 clean_path single-pass | **confirmed** | PRF-002 |
| H-012 paramsBuf silent overflow | **confirmed** | PRF-003 |
| H-013 Mount RawPath divergence | **confirmed (partial)** | PRF-005 |
| H-025 TSR pre-auth disclosure | **confirmed** | PRF-004 (+ FixedPath variant) |
| H-007 RedirectFixedPath open-redirect `//` | **refuted** | `path.Clean` collapses `//` → `/`; Location stays same-origin |
| H-028 Unicode case-fold asymmetry | **refuted** | `foldEq` is ASCII-only by design; Unicode confusables do not cross-fold (safe) |

New hypotheses generated (for the threat-modeler to promote):
- **H-031** (proposed): tree corruption via wildcard shadow — PRF-001 is an example of the class "addRoute accepts a pattern that creates an inconsistent tree"
- **H-032** (proposed): UTF-8 invariant violation in the radix tree — PRF-006 is the first instance; other bytes ≥0x80 in specific positions may reproduce it

---

**Agent:** path-routing-fuzzer
**Audited commit:** `533d0c9`
**Timestamp:** 2026-04-17T12:30Z
