# SAST & Memory Safety Audit — Pre-release MuxMaster v1.0.0

**Date:** 2026-04-17 13:13 (UTC+01)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go toolchain:** `go1.26.2 linux/amd64`
**Platform:** Linux 6.8.0-107-generic (x86_64)
**Auditor:** `go-sast-and-memory-auditor`
**Sprint:** `2026-04-17-sprint.md`

Tools (versions in `tools/versions.txt`):

| Tool | Version | Status |
|---|---|---|
| `go vet` | go1.26.2 | OK |
| `staticcheck` | 2026.1 (v0.7.0) | OK |
| `gosec` | dev (latest master — 2026-04) | OK |
| `govulncheck` | v1.2.0 (DB: vuln.go.dev) | OK |
| `errcheck` | v1.10.0 | OK |
| `ineffassign` | v0.2.0 | OK |
| `golangci-lint` | v2.11.4 | OK |
| `osv-scanner` | 1.9.2 | OK |
| `cyclonedx-gomod` | v1.10.0 | OK |
| `semgrep` | 1.159.0 (p/golang + p/security-audit + p/gosec + custom rules) | OK |
| `CodeQL` | — | NOT RUN (unavailable) |
| `go-licenses` | — | NOT RUN (zero deps — manual audit) |

---

## 1. Summary

| Tool | Findings | Critical | High | Medium | Low | Informational |
|---|---|---|---|---|---|---|
| `go vet` (module only) | 0 | 0 | 0 | 0 | 0 | 0 |
| `staticcheck -checks=all` | 3 | 0 | 0 | 0 | 3 | 0 |
| `gosec` | 4 | 0 | 0 | 0 | 4 | 0 |
| `errcheck` (prod code only) | 2 | 0 | 0 | 0 | 2 | 0 |
| `ineffassign` | 0 | — | — | — | — | — |
| `golangci-lint` (security+bugs profile) | 23 | 0 | 0 | 0 | 13 | 10 |
| `govulncheck` | **0** | — | — | — | — | — |
| `osv-scanner` | **0** | — | — | — | — | — |
| `semgrep p/golang` | 0 | — | — | — | — | — |
| `semgrep p/security-audit` | 0 | — | — | — | — | — |
| `semgrep p/gosec` | 0 | — | — | — | — | — |
| `semgrep` custom MuxMaster rules | 35 | 0 | 0 | 1 | 5 | 29 |

**Deduplicated total: 14 distinct findings** (SAST-001 through SAST-014 below).

### Release-gate status

| Gate | Requirement | Status |
|---|---|---|
| `govulncheck` | zero findings | PASS (0 vulns) |
| `osv-scanner` | zero findings | PASS (0 vulns) |
| Zero external deps | `require github` empty | PASS (no `require` directives at all) |
| `go mod verify` | OK | PASS |
| `go test -race` | zero races in module | PASS |
| No `unsafe.Pointer` regressions | documented sites only | PASS (3 sites, all audited — see §5) |
| No `cgo` | `import "C"` empty | PASS |
| No `SetFinalizer` | empty | PASS |
| `staticcheck` critical (SA*) | zero | PASS (only U1000 `unused`, Low) |
| `gosec` High/Critical | zero | PASS (only G103 LOW for documented unsafe) |

**Release recommendation from SAST standpoint: CONDITIONAL APPROVE**.
All hard gates PASS. Two Low findings require quick decision: `setReqCtx` dead code (SAST-003) and `maxParams` unused constant (SAST-004) should either be deleted or documented as public-API reserve. No blocker for v1.0.0.

---

## 2. Zero-dependency invariant

**Status:** PASS.

```
$ cat go.mod
module github.com/FlavioCFOliveira/MuxMaster

go 1.26

$ grep -E 'require\s+github' go.mod
(no matches)

$ grep -E 'require\s+golang\.org' go.mod
(no matches)

$ go mod graph
github.com/FlavioCFOliveira/MuxMaster go@1.26
go@1.26 toolchain@go1.26

$ go mod verify
all modules verified

$ ls go.sum
(not present — no transitive deps to pin)
```

SBOM: `evidence/2026-04-17/sbom.cdx.json` (CycloneDX 1.6; single component — the module itself).
License audit: `evidence/2026-04-17/licenses.txt` (MIT; no third-party licences in scope).

---

## 3. Known-CVE exposure

**Status:** PASS — **zero CVEs** disclosed against the Go toolchain used or the module itself.

```
$ govulncheck ./...
No vulnerabilities found.
```

Full JSON report: `evidence/2026-04-17/govulncheck.json` (342 KB — DB snapshot).

Cross-validated via OSV:

```
$ osv-scanner --lockfile=go.mod -r .
Scanned .../go.mod file and found 1 package
No issues found
```

Notes:
- Go 1.26.2 is a patched release; `govulncheck` confirmed no stdlib CVEs apply to the symbols reached by MuxMaster's imports.
- No third-party packages means no transitive CVE surface.

---

## 4. Escape analysis

Full output: `evidence/2026-04-17/escape.txt` (4 426 lines; `go build -gcflags='-m=2'`).

### 4.1 Hot-path functions — PASS

No escape in the path of a hit-path lookup. The only heap operation on the hot path is `rcPool.Get()` (intentional: the pool's `New` allocates once per shared slot).

| Function | Hot-path allocations | Observation |
|---|---|---|
| `(*Mux).ServeHTTP` | 0 escapes (fast path `m.dispatch` directly) | `m.dispatchWithRecover` only enters when `PanicHandler != nil` (opt-in) |
| `(*Mux).dispatch` | 0 escapes for static routes; 1 pool Get for param routes (requestCtx) | Consistent with baseline `bench_test.go` of 0 allocs/op |
| `(*node).getValue` | 0 escapes (paramsBuf is stack-allocated as fixed array) | Key optimisation; confirmed `// does not escape` for `&ps` in mux.go:449 |
| `acquireRC` / `releaseRC` | `new(requestCtx)` escapes to heap in `init.func1` (pool `New`) — expected | Amortised ~once per pool slot per GC |
| `Params.Get` / `Lookup` | 0 escapes | Inlined range over the inline array backing `Params` |
| `(*Mux).allowed` | `strings.Builder` state escapes locally | Only on 404/405 (cold); acceptable |

### 4.2 Registration-time (cold path) — expected escapes only

`(*node).addRoute` + `insertChild` + `expandOptional` produce heap allocations for error strings (panics) and for `append`-grown children slices. These are legitimate: registration is a one-time init-phase cost.

No unexpected escapes detected; the module is **consistent with the published baseline** (13.5 ns / 0 allocs static; 27 ns / 0 allocs 1-param).

### 4.3 Regression guardrails

For the release, the `bench_test.go` suite serves as the escape-analysis gate:

- Any PR that introduces `allocs/op > 0` on `BenchmarkStatic*` or `BenchmarkParam*` is a High finding.
- The escape evidence in this report is the baseline against which future audits compare.

---

## 5. Memory-safety grep results

Ran via `Grep` (ripgrep) over `*.go` and `middleware/*.go` (excluding `competitor/`, `reports/*/harness/`).

| Pattern | Hits (module) | Classification |
|---|---|---|
| `unsafe.` (call site) | 3 | Documented; see §5.1 |
| `import "unsafe"` | 1 (mux.go) + 1 (params.go) | Required for the 3 sites above |
| `import "C"` | 0 | PASS |
| `reflect.` | 2 | Audited; see §5.2 |
| `sync/atomic` | 1 (mux.go — `atomic.Pointer[methodTrees]`) | Audited; lock-free read of trees |
| `SetFinalizer`, `runtime.SetFinalizer` | 0 | PASS |
| `go func(` | 0 | PASS — module spawns no goroutines |
| `sync.Pool` | 2 (params.go `rcPool`, compress.go `pool`) | Audited; both reset-before-Put (see §5.3) |
| `bytes.Equal`, `== password/token/secret` | 0 | PASS (only `crypto/subtle.ConstantTimeCompare` used in `basic_auth.go`) |

### 5.1 unsafe.Add audit — three sites, all identical pattern

All three sites perform `*(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))` where `reqCtxOffset` is computed **once** via `reflect` at `init()` time from `http.Request.ctx`.

```
params.go:155  func setReqCtx(r *http.Request, ctx context.Context) {
                   *(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset)) = ctx
               }

mux.go:464     origCtxPtr := (*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))
               origCtx := *origCtxPtr
               rc := acquireRC()
               ...
               *origCtxPtr = rc
               handler.ServeHTTP(w, r)
               *origCtxPtr = origCtx
               ...
               releaseRC(rc)

mux.go:521     (identical to mux.go:464, in the wildcard-method/Mount path)
```

**Safety argument (reviewed line-by-line):**

1. **Bounded offset.** `reqCtxOffset` is resolved at process `init()` from `reflect.TypeOf(http.Request{}).Field(i).Offset`. On Go 1.26.2 this resolves to the byte offset of `http.Request.ctx` — a valid field within the allocation.
2. **checkptr safety.** Per `cmd/compile/internal/ssagen/ssa.go` rules, `unsafe.Add(unsafe.Pointer(X), N)` is checkptr-safe iff `N` is within the allocation of `X`. Since `r *http.Request` is always a valid allocation and `reqCtxOffset < sizeof(http.Request)`, this is within bounds.
3. **Type compatibility.** The field is of type `context.Context` (interface) — the same type we cast to. `context.Context` is a two-word interface (`(*itab, *data)`), and `*(*context.Context)(...) = rc` writes an aligned 16-byte value. Go's memory model requires atomicity of 16-byte writes only on aligned word operations; this write is not atomic w.r.t. concurrent reads.
4. **Goroutine-ownership assumption.** The pattern is **only safe** if `r` is owned exclusively by the goroutine that called `ServeHTTP`. This is true for the dispatcher-local phase. **It breaks** if a handler spawns `go func() { r.Context() }()` — see hypothesis H-001/H-024 owned by `concurrency-security-auditor`.

**Risk categorisation:** INFO within the SAST remit (pattern is correctly implemented and constrained); the runtime-race dimension is the concurrency auditor's scope. The regression test added in `harness/h018_ctx_field_type_test.go` gates the reflect-offset assumption (see §11 — H-018 resolution).

**Dead code finding:** `setReqCtx` at params.go:154 is **unused in the compiled binary** (U1000 from staticcheck, corroborated by golangci-lint `unused`). It should be either removed or promoted (documented + used by an exported API). See SAST-003.

### 5.2 reflect audit

Two call sites, both in init / cold paths:

| File:line | Purpose | Classification |
|---|---|---|
| `params.go:140` | `reflect.TypeOf(http.Request{})` — one-shot at init to compute `reqCtxOffset` | Audited; gated by test in `harness/h018_ctx_field_type_test.go` |
| `introspection.go:99` | `reflect.ValueOf(h)` + `reflect.Func` check in `handlerName` (cold introspection) | Safe; called from `Walk`/`Routes` only; does not touch unsafe |

**No `reflect.Unsafe*`, no `reflect.NewAt`, no dangerous reflective mutation.**

### 5.3 sync.Pool audit

Two pools in the module; both perform state reset before `Put`.

| Pool | `New` | Reset | Put |
|---|---|---|---|
| `rcPool` (`params.go:128`) | `new(requestCtx)` | `rc.Context=nil; rc.params=nil; rc.pattern=""` (mux.go:477-479 and :534-536) | `releaseRC(rc)` |
| `gzipResponseWriter.pool` (`compress.go:64`) | `gzip.NewWriterLevel(io.Discard, level)` | `gz.Reset(w)` on acquire; `gz.Close()` before Put | `pool.Put(gz)` |

**Note on `rc.small`:** the inline `[3]Param` array is not zero'd between requests. In the current code path this is **safe** because `rc.params = Params(rc.small[:copy(...)])` always restricts the visible slice to the latest write, and `copy` overwrites slots 0..n-1. Slots n..2 retain data from a prior request but are not reachable through the `Params` slice returned by `ParamsFromContext` / `PathParam`. **If** external code uses reflection to extract `rc.small[i]` for `i >= rc.params.Len()`, it would see stale data — see hypothesis H-023 (Low, concurrency-security-auditor scope).

**gzip pool side-effect:** the `pool` is created **per-middleware-instance** in the closure of `Compress(level)`. If the caller creates two `Compress` middlewares with different levels, each has its own pool — correct isolation. The pool is **not** cleared at shutdown (relies on GC), which is acceptable for stateless writers.

---

## 6. Error-handling audit (errcheck + govet + manual)

Filtered to production code (non-`_test.go`, excluding `example_test.go`):

| File:line | Call | Intentional? | Classification |
|---|---|---|---|
| `middleware/compress.go:39` | `_, _ = w.Write(g.buf)` | Yes (explicit `_, _ =`) | PASS (idiomatic) |
| `middleware/compress.go:48` | `pool.Get().(*gzip.Writer)` — type assertion without `, ok` | Yes — pool `New` returns the exact type | Low (SAST-008 — defensive hardening recommended) |
| `middleware/compress.go:50-51` | `_, _ = gz.Write(...); _ = gz.Close()` | Yes (explicit) | PASS |
| `middleware/compress.go:62` | `testGz.Close()` — no `_ =` | **Partial** — discarded implicitly | Low (SAST-006 — style; level validation is discard-only) |
| `middleware/compress.go:66` | `gz, _ := gzip.NewWriterLevel(...)` (pool `New`) | Yes (error checked by `testGz` earlier) | PASS |
| `middleware/logger.go:30` | `fmt.Fprintf(out, ...)` — no `_ =`, no error handling | **Partial** — intentional but unmarked | Low (SAST-005 — logger write failure is swallowed; consider `if err := fmt.Fprintf(...); err != nil` with fallback) |
| `middleware/request_id.go:31` | `id, _ := ctx.Value(requestIDKey{}).(string)` | Yes (nil-returns "") | PASS |
| `params.go:132` | `rcPool.Get().(*requestCtx)` — no `, ok` | Yes — pool `New` returns the exact type | Low (SAST-008 — same as compress, recommend `, ok`+fallback) |
| `params.go:160, 169, 179` | `rc, _ := r.Context().(*requestCtx)` | Yes (nil-returns "") | PASS |
| `response.go:48` | `_, _ = w.Write([]byte(s))` | Yes (explicit) | PASS |

`ineffassign`: **0 findings**.

---

## 7. golangci-lint breakdown

Raw output: `evidence/2026-04-17/golangci.txt` and SARIF at `golangci.sarif`. Custom config at `/tmp/golangci-sast.yml` (security+bugs profile: errcheck, govet -enable-all, ineffassign, staticcheck, unused, misspell, revive, bodyclose, contextcheck, noctx, gosec, nilerr, wastedassign).

Breakdown:

| Linter | Count | Severity | Notes |
|---|---|---|---|
| `errcheck` | 2 | Low | compress.go:62 testGz.Close; logger.go:30 Fprintf — both covered in §6 |
| `govet fieldalignment` | 6 | Low (Info) | Suggests reordering struct fields to save pointer bytes. Intentional in this codebase: `node` layout is hand-tuned for cache-line alignment (tree.go:47 comment), not field-alignment size. **Dismissed with justification.** See SAST-009 |
| `noctx` | 3 | Low | example_test.go uses `httptest.NewRequest` (test-only); linter prefers `NewRequestWithContext`. **Dismissed** (tests are out of scope) |
| `revive unused-parameter` | 10 | Informational | Stylistic — unused `w`/`r`/`req` in closures; not security-relevant |
| `revive context-as-argument` | 1 | Low | `setReqCtx(r *http.Request, ctx context.Context)` — context is 2nd param. **Moot** since function is unused dead code (SAST-003) |
| `unused` | 2 | Low | `bench_test.go:51 var sink`, `params.go:122 const maxParams` — see SAST-004 |

**No Critical, no High.**

---

## 8. Semgrep custom rules (MuxMaster-specific)

Ruleset: `reports/go-sast-and-memory-auditor/semgrep-rules/muxmaster.yml` (7 rules).
Output: `evidence/2026-04-17/semgrep-custom.sarif`.

| Rule | Hits | True positives | False positives |
|---|---|---|---|
| `muxmaster-context-string-key` | 0 | 0 | 0 |
| `muxmaster-non-constant-time-compare` | 2 | 0 | 2 (matched the `panic` line at basic_auth.go:11 and the `subtle.ConstantTimeCompare` call itself at line 18; regex too loose) |
| `muxmaster-panic-on-hotpath` | 28 | 0 | 28 (all matches are registration-time panics, not request-time hot path — rule needs tighter scope) |
| `muxmaster-sync-pool-without-reset` | 0 | 0 | 0 |
| `muxmaster-xff-unconditional-trust` | 3 | 3 (real_ip.go:15, :17, :20) | 0 — **confirms H-009 from hypotheses.md** |
| `muxmaster-unbounded-buffer-append` | 1 | 1 (compress.go:27) | 0 — **confirms H-006 from hypotheses.md** |
| `muxmaster-unsafe-ptr-without-checkptr-note` | 1 | 0 | 1 (params.go:155 actually has the comment on line 152-153; regex matches doc-block with newlines failed) |

**SAST-owned forwards to other agents:**
- `muxmaster-xff-unconditional-trust` (3 hits) → `middleware-security-reviewer` (H-009 tracked)
- `muxmaster-unbounded-buffer-append` (1 hit) → `dos-resilience-tester` + `middleware-security-reviewer` (H-006 tracked)

No new findings from public Semgrep rulesets (`p/golang`, `p/security-audit`, `p/gosec`): all returned **zero**.

---

## 9. Findings

Fourteen distinct findings, consolidated across tools. None Critical or High.

### SAST-001 — Unsafe.Add pattern depends on unexported `http.Request.ctx` field name/type

**Tools:** gosec G103 (Low), manual review
**Severity:** Informational (documented, gated)
**CWE:** CWE-242 (Use of Inherently Dangerous Function)
**Locations:** `mux.go:464`, `mux.go:521`, `params.go:155`
**Finding text (gosec):**
```
[mux.go:464] - G103 (CWE-242): Use of unsafe calls should be audited
  (Confidence: HIGH, Severity: LOW)
    origCtxPtr := (*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset))
```
**Analysis:** True positive on tool grounds (`unsafe` usage), but the pattern is correctly constrained and documented. Safety argument in §5.1. The dependence on the unexported `ctx` field name is gated by the test in `harness/h018_ctx_field_type_test.go` (added this sprint — see §11), which fails fast if a future Go release renames or retypes the field.
**Recommended action:** Accept. Keep the test gate. Cross-reference: H-018 (resolved — see §11).

### SAST-002 — gosec G104 unchecked error on `testGz.Close()` (compress.go:62)

**Tools:** gosec G104, errcheck, golangci-lint
**Severity:** Low
**CWE:** CWE-703 (Improper Check or Handling of Exceptional Conditions)
**Location:** `middleware/compress.go:62`
**Finding text:**
```
> 62: 	testGz.Close()
```
**Analysis:** True positive. The test writer is used only to validate the gzip level and then discarded. `Close` error cannot meaningfully be handled (writer targets `io.Discard`), but the ignored return should be made explicit.
**Recommended fix:**
```go
- testGz.Close()
+ _ = testGz.Close() // discard: validation-only writer wrapping io.Discard
```

### SAST-003 — Dead code: `setReqCtx` is exported-internal but never called

**Tools:** staticcheck U1000, golangci-lint unused, revive context-as-argument
**Severity:** Low
**CWE:** CWE-561 (Dead Code)
**Location:** `params.go:154`
**Finding text:**
```
params.go:154:6: func setReqCtx is unused (U1000)
params.go:154:33: context-as-argument: context.Context should be the first parameter
```
**Analysis:** True positive. `setReqCtx` is lowercase (package-private) but unused. Two possible interpretations:
1. It's a reserve helper for a future public API — document and keep, or
2. It's a legacy helper replaced by the inline `unsafe.Add` at mux.go:464/521 — delete.
Deletion is preferred because it reduces the count of `unsafe.` sites from 3 to 2 (one per `dispatch` branch), tightening the audit surface.
**Recommended fix:**
```go
- // setReqCtx writes ctx into r's unexported ctx field.
- // Safe when r is exclusively owned by the current goroutine (pool get → put pattern).
- // Uses unsafe.Add which is checkptr-safe: the base pointer is a valid allocation and
- // reqCtxOffset is within that allocation's bounds.
- func setReqCtx(r *http.Request, ctx context.Context) {
- 	*(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset)) = ctx
- }
```

### SAST-004 — Dead constant: `maxParams = 16` is declared but unused

**Tools:** staticcheck U1000, golangci-lint unused
**Severity:** Low
**CWE:** CWE-561 (Dead Code)
**Location:** `params.go:122`
**Finding text:**
```
params.go:122:7: const maxParams is unused (U1000)
```
**Analysis:** True positive. `maxParams = 16` is not referenced anywhere in the module (`maxInlineParams = 3` in `tree.go:13` is the actual limit). Likely a left-over from an earlier iteration where params were capped at 16.
**Recommended fix:** Delete. If intended as a documented absolute upper bound for future use, convert to a comment block near `maxInlineParams` instead.

### SAST-005 — Unchecked `fmt.Fprintf` in `Logger` middleware

**Tools:** errcheck, golangci-lint errcheck
**Severity:** Low
**CWE:** CWE-703 (Improper Check or Handling of Exceptional Conditions)
**Location:** `middleware/logger.go:30`
**Finding text:**
```
middleware/logger.go:30:15: Error return value of `fmt.Fprintf` is not checked (errcheck)
```
**Analysis:** True positive. A write failure to the logger sink (e.g., closed pipe) is swallowed silently. This is intentional for a logger (retrying is not meaningful) but the lack of `_ =` is stylistically inconsistent with response.go (which uses `_, _ =`).
**Recommended fix:**
```go
- fmt.Fprintf(out, "%s %s %s %d %s\n", ...)
+ _, _ = fmt.Fprintf(out, "%s %s %s %d %s\n", ...)
```

### SAST-006 — Dead variable `sink` in bench_test.go

**Tools:** staticcheck U1000, golangci-lint unused
**Severity:** Low (test-only)
**CWE:** CWE-561
**Location:** `bench_test.go:51`
**Finding text:**
```
bench_test.go:51:5: var sink is unused (U1000)
```
**Analysis:** True positive. Comment says `// prevent dead-code elimination`, but the variable is not read or written in any benchmark. Replaced-but-forgotten pattern.
**Recommended fix:** Delete, or add actual use in a benchmark.

### SAST-007 — golangci-lint `fieldalignment` suggestions (6 structs)

**Tools:** golangci-lint govet fieldalignment
**Severity:** Informational
**CWE:** none (performance-related advisory)
**Locations:** handler.go:14, mux.go:104, mux_test.go:40, params.go:102, tree.go:15, tree.go:47
**Finding text (example):**
```
tree.go:47:11: fieldalignment: struct with 104 pointer bytes could be 80 (govet)
type node struct {
```
**Analysis:** **DISMISSED WITH JUSTIFICATION.** The `node` struct layout (tree.go:47) is **hand-tuned for cache-line alignment**, not pointer-byte minimisation. The comment at tree.go:44-46 documents the choice:
```
// Field layout is hand-tuned to put the hot-read fields in cache line 0 (0-63).
// A successful static route match reads only `path` + `handler` — both in CL0.
```
Reordering to minimise pointer bytes would regress hot-path performance. The other 5 structs follow the same philosophy (see CLAUDE.md for the perf-driven layout rules).
**Recommended action:** Add `//nolint:fieldalignment` comments with short justification, OR add a `fieldalignment` exception in `.golangci.yml` for these struct definitions.

### SAST-008 — Type assertions without `, ok` on sync.Pool returns

**Tools:** manual review (patterns revealed by errcheck on assertion targets)
**Severity:** Low
**CWE:** CWE-704 (Incorrect Type Conversion or Cast)
**Locations:** `middleware/compress.go:48`, `params.go:132`
**Code:**
```go
// compress.go:48
gz := pool.Get().(*gzip.Writer)

// params.go:132
func acquireRC() *requestCtx { return rcPool.Get().(*requestCtx) }
```
**Analysis:** True positive on strict grounds. If another caller accidentally puts a wrong type into `pool`/`rcPool`, these assertions panic (not gracefully fail). In the current code the pool is closure-local or package-private, so cross-contamination is unlikely — but the hardening cost is zero.
**Recommended fix:**
```go
- gz := pool.Get().(*gzip.Writer)
+ gz, ok := pool.Get().(*gzip.Writer)
+ if !ok {
+     gz, _ = gzip.NewWriterLevel(io.Discard, level)
+ }
  gz.Reset(w)
```
*For `acquireRC`: similar pattern — fallback to `new(requestCtx)`.*

### SAST-009 — XFF unconditional trust confirmed by custom rule

**Tools:** semgrep custom `muxmaster-xff-unconditional-trust` (3 hits)
**Severity:** Medium (escalated to `middleware-security-reviewer` as H-009)
**CWE:** CWE-345 (Insufficient Verification of Data Authenticity), CWE-290 (Authentication Bypass by Spoofing)
**Locations:** `middleware/real_ip.go:15, 17, 20`
**Finding text (rule message):**
```
Overwriting r.RemoteAddr from X-Forwarded-For without trusted-proxies allowlist permits spoofing.
```
**Analysis:** True positive. The `RealIP` middleware unconditionally trusts `X-Forwarded-For` and `X-Real-IP` headers, overwriting `r.RemoteAddr`. If MuxMaster is deployed without a trusted reverse proxy in front, any client can spoof the IP. Downstream middleware (custom rate limits, IP ACLs, logger) consumes the spoofed value.
**Escalation:** forward to `middleware-security-reviewer` (owner of H-009). SAST confirms the architectural finding; fix is API-level.
**Recommended fix path (at middleware level):** introduce `real_ip.TrustedProxies([]string)` option and skip overwrite if the direct peer (original `r.RemoteAddr`) is not in the trusted set.

### SAST-010 — Unbounded append in `gzipResponseWriter.Write`

**Tools:** semgrep custom `muxmaster-unbounded-buffer-append` (1 hit)
**Severity:** Medium (escalated to `dos-resilience-tester` as H-006)
**CWE:** CWE-400 (Uncontrolled Resource Consumption)
**Location:** `middleware/compress.go:27`
**Code:**
```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.done {
        g.buf = append(g.buf, b...)   // <-- unbounded
        return len(b), nil
    }
    return g.gz.Write(b)
}
```
**Analysis:** True positive. A handler that writes N GB before returning causes `g.buf` to hold N GB. RSS grows linearly with handler output. The structure was designed around small responses (`minCompressSize = 1024` threshold); large streaming handlers break the assumption silently.
**Escalation:** `dos-resilience-tester` owns the empirical confirmation (H-006). `middleware-security-reviewer` owns the fix design.
**Recommended fix path:** one of:
1. Stream: write directly to `gz.Write(b)` after the first 1 KB, abandoning buffering; OR
2. Add `MaxBufferBytes` config option with a default of e.g. 8 MB; writes beyond it error out or flush immediately uncompressed.

### SAST-011 — Revive `unused-parameter` in tests and examples

**Tools:** golangci-lint revive (10 hits)
**Severity:** Informational
**CWE:** none
**Locations:** bench_test.go:12, example_test.go:34/65/82, middleware/middleware_test.go:15/66, mux_test.go:13/291/295
**Analysis:** Stylistic. Unused handler-signature parameters (common in tests where `w` or `r` is unused). Not a security concern.
**Recommended action:** Rename unused params to `_` OR accept as test-code convention. `.golangci.yml` can exclude `_test.go` from revive's `unused-parameter` if desired.

### SAST-012 — noctx in example_test.go

**Tools:** golangci-lint noctx (3 hits)
**Severity:** Informational
**CWE:** none
**Locations:** example_test.go:21, 43, 70
**Analysis:** Test-only; examples use `httptest.NewRequest` instead of `NewRequestWithContext`. Already excluded scope.
**Recommended action:** Accept (tests are out of production scope).

### SAST-013 — `with_value` middleware accepts `any` key (string collision surface)

**Tools:** manual review (rule `muxmaster-context-string-key` did not trigger because there's no literal string call in the module, but the **API** accepts any key)
**Severity:** Informational (caller responsibility)
**CWE:** CWE-668 (Exposure of Resource to Wrong Sphere) — weak, via user-side misuse
**Location:** `middleware/with_value.go:9`
**Code:**
```go
func WithValue(key, val any) func(http.Handler) http.Handler { ... }
```
**Analysis:** The middleware itself is fine — it delegates to `context.WithValue` which is stdlib-approved. But a caller passing `middleware.WithValue("user", data)` creates a cross-package collision risk. Contrast with `request_id.go`, which correctly uses a typed `requestIDKey{}` key.
**Recommended action:** Document in GoDoc:
```go
// WithValue injects a value into the request context.
//
// Use a custom unexported type (e.g. `type myKey struct{}`) as the key to prevent
// cross-package collisions; passing a bare string is strongly discouraged.
func WithValue(key, val any) func(http.Handler) http.Handler {
```

### SAST-014 — Empty `require` block in go.mod is implicit

**Tools:** manual review
**Severity:** Informational (positive observation)
**CWE:** none
**Location:** `go.mod`
**Analysis:** `go.mod` contains no `require` directives at all. This is the strongest statement of the zero-dependency invariant — but tools like Snyk/Dependabot may alert if they interpret "no require" as "incomplete manifest". Consider adding a comment block documenting the intentional absence:
```go
module github.com/FlavioCFOliveira/MuxMaster

go 1.26

// Intentional: zero third-party dependencies. See CLAUDE.md.
// Any addition must be justified as security-critical and reviewed.
```
**Recommended action:** Non-blocking; cosmetic.

---

## 10. Dismissed findings

Every dismissal has written justification.

| Finding | Reason |
|---|---|
| gosec G103 on mux.go:464, mux.go:521, params.go:155 | Documented unsafe sites; safety argument in §5.1; gated by H-018 test |
| govet `fieldalignment` on node, mux, params, tree, paramsBuf, httpError | Hand-tuned cache-line alignment overrides pointer-byte minimisation (see SAST-007) |
| revive `unused-parameter` in tests | Test convention; not security-relevant |
| noctx in example_test.go | Test/documentation scope |
| Panics in registration path (tree.go:141–395, mux.go:196–208, mux.go:350–387, etc.) | All panics are configuration errors during `Handle()` / `Mount()` / `ServeFiles()` registration — registration is init-time, cannot occur per-request |
| Semgrep `muxmaster-non-constant-time-compare` on basic_auth.go:10,18 | False positive: matched the `panic` call and the actual `subtle.ConstantTimeCompare` call; the module **does** use constant-time compare |
| Semgrep `muxmaster-panic-on-hotpath` (28 hits) | All matches are registration-time panics, not request-time. Rule needs tighter scope (exclude `Handle`, `addRoute`, etc.). |
| Semgrep `muxmaster-unsafe-ptr-without-checkptr-note` on params.go:155 | False positive: comment at lines 152-153 **does** document checkptr safety; regex did not match across lines |
| errcheck on `mux_test.go` / `example_test.go` | Test code scope; excluded |

---

## 11. Hypothesis resolution — H-018

**Hypothesis:** `reflect-based reqCtxOffset staleness across Go versions` — if a future Go release renames/retypes/removes `http.Request.ctx`, `reqCtxOffset` silently resolves to `0` (or wrong offset), causing `unsafe.Add` to write into wrong memory.

**Resolution:** Added `harness/h018_ctx_field_type_test.go` — a regression test that:

1. Asserts `http.Request` has a field named `ctx`.
2. Asserts the field's type is `context.Context` (interface).
3. Asserts the offset is non-zero.

**Result on Go 1.26.2:** PASS. File committed to `reports/go-sast-and-memory-auditor/harness/`. Integrate into CI via `go test ./reports/go-sast-and-memory-auditor/harness/...` — any future Go upgrade that changes the struct layout will break this test before the `unsafe.Add` pattern silently corrupts data.

**Status in hypotheses.md:** recommend updating to `partial` (test gate added; does not remove the inherent fragility of unexported-field access, but detects it at the earliest opportunity).

---

## 12. Escalations

Cross-domain findings handed off to other sprint agents:

| Finding | Agent | Hypothesis |
|---|---|---|
| SAST-009 (XFF unconditional trust) | `middleware-security-reviewer`, `dos-resilience-tester` | H-009 |
| SAST-010 (compress unbounded buffer) | `dos-resilience-tester`, `middleware-security-reviewer` | H-006 |
| SAST-001 (unsafe.Add + goroutine ownership) | `concurrency-security-auditor` | H-001 / H-024 |
| SAST-011 (residue in `rc.small`) | `concurrency-security-auditor` | H-023 |
| `muxmaster-non-constant-time-compare` false-positive pattern on basic_auth.go | `timing-and-sidechannel-analyst` | H-002 (separate architectural timing concern) |

No findings require `http-protocol-security-auditor` or `path-routing-fuzzer` escalation from SAST perspective.

---

## 13. Coverage gaps

Honest declaration of what this audit did **not** cover:

| Tool / Analysis | Status | Reason |
|---|---|---|
| CodeQL (`go-security-extended`, `go-security-and-quality`) | NOT RUN | Binary unavailable in environment; would add ~20 min runtime |
| `go-licenses` | NOT RUN | Module has zero third-party deps — manual audit in `licenses.txt` is equivalent |
| Dynamic taint analysis | NOT RUN | Out of SAST scope (owned by fuzzing-and-property-engineer) |
| Binary-level analysis (cgo strings, RELRO, etc.) | NOT RUN | Out of scope; module is pure Go, no cgo |
| Fuzz testing | NOT RUN | Owned by `fuzzing-and-property-engineer` + `path-routing-fuzzer` |
| Stack overflow / recursion bombs | NOT RUN | Owned by `dos-resilience-tester` |
| Supply-chain provenance (SLSA attestation) | NOT RUN | Module is ours; no upstream to attest |
| Secrets scanning (e.g. `detect-secrets`, `gitleaks`) | NOT RUN | Codebase manually reviewed; no obvious secrets; recommend adding as CI step |

---

## 14. Evidence index

All artefacts in `reports/go-sast-and-memory-auditor/evidence/2026-04-17/`:

| File | Purpose |
|---|---|
| `govet.txt` | `go vet -all` (module + module+harness) |
| `govet-module-only.txt` | Module-only variant |
| `staticcheck.txt`, `staticcheck.json` | staticcheck all-checks output |
| `gosec.txt`, `gosec.sarif` | gosec Low/Low |
| `errcheck.txt` | errcheck (blank + asserts) |
| `ineffassign.txt` | empty (no findings) |
| `golangci.txt`, `golangci.sarif` | golangci-lint security-bugs profile |
| `govulncheck.txt`, `govulncheck.json` | Go vuln DB — **zero vulns** |
| `osv-scanner.txt` | OSV cross-check — **zero vulns** |
| `semgrep.sarif` | `p/golang` ruleset — 0 findings |
| `semgrep-security.sarif` | `p/security-audit` ruleset — 0 findings |
| `semgrep-gosec.sarif` | `p/gosec` ruleset — 0 findings |
| `semgrep-custom.sarif` | MuxMaster custom rules |
| `escape.txt` | `go build -gcflags='-m=2'` — escape analysis |
| `mod-verify.txt` | `go mod verify` — OK |
| `mod-graph.txt` | `go mod graph` — 1 edge (toolchain only) |
| `sbom.cdx.json` | CycloneDX 1.6 SBOM (JSON) |
| `licenses.txt` | License summary |
| `zero-dep-check.txt` | Zero-dep invariant evidence |
| `go-test-race.txt` | `go test -race` — zero races |
| `combined.sarif` | Merged SARIF (gosec + golangci + 4× semgrep) |

Harness:
- `harness/h018_ctx_field_type_test.go` — H-018 gate test (passes on Go 1.26.2)

Custom rules:
- `semgrep-rules/muxmaster.yml` — 7 MuxMaster-specific rules

Tools:
- `tools/versions.txt` — tool version pins

---

## 15. Next actions

### Before v1.0.0 tag (recommended, not blocking)

1. SAST-003 — decide: delete `setReqCtx` or promote to public API with test. Recommend **delete**.
2. SAST-004 — delete `maxParams` or repurpose as documented `MaxPerRoute` bound.
3. SAST-006 — delete or use `sink` in `bench_test.go`.
4. SAST-002 — add `_ = testGz.Close()` in compress.go:62.
5. SAST-005 — add `_, _ = fmt.Fprintf(...)` in logger.go:30.

### CI integration (strongly recommended before release)

Add this job to `.github/workflows/` (the existing CI setup):

```yaml
- name: SAST — release gate
  run: |
    go install honnef.co/go/tools/cmd/staticcheck@latest
    go install github.com/securego/gosec/v2/cmd/gosec@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest
    go vet ./...
    staticcheck -checks=all .
    staticcheck -checks=all ./middleware/...
    gosec -severity=medium .
    gosec -severity=medium ./middleware/...
    govulncheck ./...
    go test -race -count=1 ./...
    # H-018 gate
    go test ./reports/go-sast-and-memory-auditor/harness/...
```

### Post-release (nightly)

- Re-run `govulncheck` daily to catch newly-published stdlib CVEs.
- Re-run `osv-scanner` weekly.
- On every Go toolchain upgrade: re-run `h018_ctx_field_type_test.go` before merge.

---

**End of report.**

Signed off by `go-sast-and-memory-auditor`, 2026-04-17 13:13 UTC+01.
