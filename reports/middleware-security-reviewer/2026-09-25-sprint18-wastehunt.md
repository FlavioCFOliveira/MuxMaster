# Middleware Security Review — Sprint 18 Waste-Hunt (WH-01/02/03/05/06/07/11/12)

Date: 2026-09-25   Baseline commit: d980583 (+ uncommitted working-tree changes under review)   Go: go1.27.0 linux/amd64

Scope: `git diff d980583 -- middleware/throttle.go middleware/logger.go middleware/compress.go middleware/jwt_auth.go middleware/real_ip.go middleware/api_key.go middleware/set_header.go middleware/clean_path.go middleware/strip_slashes.go`, plus the new `middleware/*_wastehunt*_test.go` and `middleware/wrapper_flusher_test.go`.

## Verdicts

| Middleware | Change | Verdict | Findings |
|---|---|---|---|
| throttle.go | WH-01: non-blocking fast path + entryPool recycling | **PASS** | none |
| logger.go | WH-02/07: single clock read, alloc-free formatting, ReadFrom/Flush/Unwrap | **PASS (fixed)** | MID-LOGGER-1 (Medium) — fixed |
| compress.go | WH-03: pooled gzipResponseWriter, 8 KiB sniff array, Flush/Unwrap | **PASS (fixed)** | MID-COMPRESS-1 (Critical) — fixed |
| jwt_auth.go | WH-06: single-entry header memo, `bytesFromString` unsafe view | **PASS (test fixed)** | MID-JWT-TEST-1 (test-only, no product defect) — fixed |
| real_ip.go | WH-11: right-to-left XFF scan, no `strings.Split` | **PASS** | none |
| api_key.go | WH-12: fused context node | **PASS** | none |
| set_header.go | WH-05: prebuilt canonical key + hoisted value slice | **FAIL (fixed)** | MID-SETHEADER-1 (High) — fixed |
| clean_path.go / strip_slashes.go | WH-04: shallow request copy instead of `r.Clone` | **PASS** | none (matches stdlib `http.StripPrefix` idiom exactly) |

Three genuine defects were found in the reviewed diff, plus one flaky/incorrect security regression test (no product defect). All four were small, unambiguous, and inside the changed code, so I fixed them directly as authorized by the task. Full regression tests were added for all four and pass under `-race`.

## Findings

| ID | Severity | CWE | Location | Summary |
|---|---|---|---|---|
| MID-COMPRESS-1 | Critical | CWE-670 (incorrect control flow) / access-control impact | middleware/compress.go: `gzipResponseWriter.WriteHeader` | A 1xx informational status (e.g. 103 Early Hints) followed by the real final status (e.g. 401/403) caused the final status to be silently dropped; the client received an implicit **200 OK** with the full response body, for any request advertising `Accept-Encoding: gzip`. |
| MID-SETHEADER-1 | High | CWE-668 (exposure of resource to wrong sphere) / cross-request state leak | middleware/set_header.go: `SetHeader` | The header value's `[]string` was hoisted into the middleware closure and shared across every request. Any downstream code that mutates the slice by index (`w.Header()[k][0] = ...`) permanently corrupts the header for all subsequent requests through that middleware instance until process restart. |
| MID-LOGGER-1 | Medium | CWE-778-adjacent (insufficient logging / incorrect log data) | middleware/logger.go: `statusRecorder.WriteHeader` | Same missing 1xx exemption as MID-COMPRESS-1, but here the client-visible response was already correct (every call is forwarded to the real `ResponseWriter`, and net/http itself applies the 1xx exemption). Only the **logged** status was wrong: a response preceded by a 1xx call was logged with the 1xx code instead of its real final status, which can hide security-relevant events (401/403/429/…) from status-code-based log monitoring. |
| MID-JWT-TEST-1 | N/A (test defect, no product vulnerability) | — | middleware/jwt_wastehunt_test.go: `flipBase64Char` | The security regression test `TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification` used a "flip the last base64 character" tamper technique. For a base64url-encoded 32-byte HMAC-SHA256 signature (a 2-byte-remainder group), the last character carries only 4 significant bits; Go's `base64.RawURLEncoding.DecodeString` does not validate that the other 2 bits are zero (documented stdlib leniency), so several distinct last characters decode to byte-identical output. Roughly 1-in-8 to 1-in-16 runs, the "tampered" token was not actually tampered, and the test failed with a false alarm ("got 200, want 401"). Reproduced deterministically: 24/200 failures under `-race -count=200` before the fix, 0/500 after. **The middleware itself was correct in every one of these runs** — JWTAuth accepted a token whose signature bytes genuinely matched, which is the only correct behaviour. |

### MID-COMPRESS-1 — 1xx informational response silently downgrades the final status to 200 OK

**Severity:** Critical
**CWE:** CWE-670 (Always-Incorrect Control Flow Implementation); practical impact is access-control bypass (a 401/403/429/etc. response becomes 200 OK with body delivered) for any client sending `Accept-Encoding: gzip`.
**Location:** `middleware/compress.go`, `gzipResponseWriter.WriteHeader` (introduced by WH-03/#254's "first WriteHeader wins" fix for MM-2026-0254).

**Root cause:** net/http's own `(*response).WriteHeader` (`net/http/server.go`) explicitly exempts 1xx informational codes from its "first call wins" lock:
```go
if code >= 100 && code <= 199 && code != StatusSwitchingProtocols {
    // send immediately, do NOT set wroteHeader
    return
}
w.wroteHeader = true
```
`gzipResponseWriter.WriteHeader` reimplemented "first wins" without this exemption:
```go
func (g *gzipResponseWriter) WriteHeader(code int) {
    if g.wroteHeader { return }
    g.wroteHeader = true
    g.status = code
}
```
Because `commit()` only ever forwards `g.status` to the real `ResponseWriter` **once**, a handler calling `WriteHeader(103)` then `WriteHeader(403)` locks `g.status` at 103 on the first call. `commit()` later calls `g.ResponseWriter.WriteHeader(103)` — which net/http treats as informational and does NOT mark its own `wroteHeader` — so the subsequent body write triggers net/http's own implicit `WriteHeader(200)`. The intended 403 is never sent.

The pre-diff implementation (`g.status = code` unconditionally, no lock) happened to avoid this by accident, since "last write wins" converges on the real final status. The optimisation introduced the regression.

**Reproducer (verified against a real `httptest.Server`, not `httptest.ResponseRecorder` — the recorder does not itself implement net/http's 1xx exemption and cannot distinguish the fix from the bug):**
```go
h := middleware.Compress(gzip.BestSpeed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(103)
    w.WriteHeader(403)
    w.Write(body) // >= 1024 bytes
}))
srv := httptest.NewServer(h)
req.Header.Set("Accept-Encoding", "gzip")
resp, _ := srv.Client().Do(req)
// BEFORE FIX: resp.StatusCode == 200, full body delivered.
// AFTER FIX:  resp.StatusCode == 403.
```
Confirmed empirically before the fix: `real server status: 200, content-encoding=gzip`.

**Fix applied** (small, unambiguous, inside the changed code — `middleware/compress.go`): exempt 1xx codes from the lock, mirroring net/http's own predicate exactly:
```go
func (g *gzipResponseWriter) WriteHeader(code int) {
	if code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols {
		return
	}
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.status = code
}
```
**Regression test:** `TestCompress_1xxInformational_DoesNotBlockFinalStatus` in `middleware/wrapper_flusher_test.go` (real server, verifies both status code and decoded body).

### MID-SETHEADER-1 — shared header-value slice allows cross-request contamination

**Severity:** High
**CWE:** CWE-668 (Exposure of Resource to Wrong Sphere), shared-mutable-state class.
**Location:** `middleware/set_header.go`, `SetHeader` (WH-05/#254).

**Root cause:** the optimisation hoisted both the canonicalised key AND the value slice out of the per-request path:
```go
canonKey := http.CanonicalHeaderKey(key)
val := []string{value}   // <-- built ONCE, shared forever
...
w.Header()[canonKey] = val
```
`http.Header.Set/Add/Del` never mutate an existing slice in place (they always install a fresh one), so ordinary header manipulation cannot observe this. But `http.Header` is just `map[string][]string`, and any code that indexes directly into the slice — `w.Header()[key][0] = "x"` — mutates the **shared backing array** in place. Since `val` is the exact same slice object installed into every request's header map for the lifetime of the middleware instance, one request's in-place write permanently corrupts the value seen by every other request (past and future) until process restart. This defeats normal `http.Request`/`http.ResponseWriter` per-request isolation for whatever header `SetHeader` was configured to manage — commonly a security header (CSP, HSTS, X-Frame-Options, a fixed CORS allow-origin).

This pattern is not far-fetched in a MuxMaster-based codebase specifically: this same diff uses exactly this "index into `Header()` directly" style as a deliberate optimisation technique elsewhere, so downstream application code influenced by the same style is a realistic trigger.

**Reproducer:** confirmed empirically —
```go
mw := SetHeader("X-Test", "original-value")
// request 1: handler does w.Header()["X-Test"][0] = "evil"
// request 2: unrelated handler, touches nothing
// BEFORE FIX: request 2 sees "evil".
// AFTER FIX:  request 2 sees "original-value".
```

**Fix applied** (small, unambiguous, inside the changed code — `middleware/set_header.go`): allocate the value slice fresh per request; keep the canonicalised key (safe to hoist — it never changes):
```go
canonKey := http.CanonicalHeaderKey(key)
return func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()[canonKey] = []string{value}
		next.ServeHTTP(w, r)
	})
}
```
**Regression tests:** `middleware/setheader_wastehunt_test.go` — `TestSetHeader_InPlaceSliceMutation_DoesNotLeakAcrossRequests` (sequential leak repro) and `TestSetHeader_ConcurrentRequests_EachGetsIndependentSlice` (200-goroutine concurrent stress, each expecting only its own mutation).

### MID-LOGGER-1 — logged status pinned to a preceding 1xx code

**Severity:** Medium
**CWE:** related to CWE-778 (insufficient logging) via incorrect log content.
**Location:** `middleware/logger.go`, `statusRecorder.WriteHeader` (WH-02/#254).

Same missing 1xx exemption as MID-COMPRESS-1, but `statusRecorder.WriteHeader` **always forwards every call** to the real `ResponseWriter`:
```go
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader { r.wroteHeader = true; r.status = code }
	r.ResponseWriter.WriteHeader(code)   // always forwarded
}
```
So the client-visible response was already correct (net/http applies its own 1xx exemption on the real object). The bug is confined to the **recorded** status used for the log line: `r.status` locked onto the first (1xx) call, so the log line for a request that sent `103` then `403` recorded `103` — misrepresenting a security-relevant final status in the access log. Confirmed via a real server: client status 403 (correct) but log line `... 103 ...` (wrong) before the fix.

**Fix applied** (`middleware/logger.go`): same exemption predicate as net/http, applied only to the local recording decision (the unconditional forward to the real `ResponseWriter` is unchanged and correct):
```go
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader && !(code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols) {
		r.wroteHeader = true
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}
```
**Regression test:** `TestLogger_1xxInformational_LogsFinalStatusNotInformational` in `middleware/wrapper_flusher_test.go`.

### MID-JWT-TEST-1 — flaky signature-tamper helper in the WH-06 regression test (test-only)

**Severity:** N/A — no product vulnerability. Flaky/incorrect test, fixed for CI reliability.

This directly addresses the task's request to investigate "a transient JWT test failure ... observed once during an unrelated busy full-suite run." It reproduces **reliably**, not just transiently: `go test ./middleware/... -run 'JWT|Jwt' -race -count=200` failed `TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification` 24/200 times (see `evidence/2026-09-25/jwt-flaky-repro-count200.txt`), always with `got 200, want 401`.

**Root cause (verified empirically, not by inspection alone):** the test's tamper technique,
```go
tampered := valid[:len(valid)-1] + string(flipBase64Char(valid[len(valid)-1]))
```
flips only the last character of the whole token (the last character of the base64url-encoded HMAC-SHA256 signature). A 32-byte MAC leaves a 2-byte remainder in base64 grouping (32 = 3×10 + 2), so its last character carries only 4 significant bits; the bottom 2 bits are unused padding. Go's `base64.RawURLEncoding.DecodeString` does not require those padding bits to be zero (confirmed with a standalone program: for an all-zero 32-byte payload, encoded last-characters `A`, `B`, `C`, `D` all decode to byte-identical output). `flipBase64Char` always targets `'A'` (or `'B'` if the original was already `'A'`) — both in the same 4-character bucket — so whenever the token's real last character already fell in that bucket (~1-in-16 to ~1-in-8 depending on the exact original/target pairing), the "tampered" token decoded to the exact same signature bytes as the original. `hmac.Equal` then correctly reported equality, and JWTAuth correctly accepted a token that was, at the byte level, never actually tampered — the test's failure was a false alarm about the test itself, not about the memo, the middleware, or WH-06's caching logic. **No bypass of signature verification, the algorithm allow-list, or the header memo was ever observed or is possible from this issue.**

**Fix applied** (`middleware/jwt_wastehunt_test.go`): replaced the char-flip with `corruptSignature`, which decodes the signature, XORs a full byte (`corrupted[0] ^= 0xFF`), and re-encodes — guaranteeing a genuine byte-level change for every possible signature value, independent of base64 grouping boundaries.

**Verification:** `go test ./middleware/... -run 'TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification' -race -count=500` → 500/500 pass (was ~88% pass rate before). Full `-run 'JWT|Jwt' -race -count=200` → clean.

## Per-diff detail (no finding)

- **throttle.go (WH-01):** Verified the stated invariant end-to-end from the code (`acquire`/`decRefs`, `ThrottlePerIPCapped`'s fast/slow path split): every code path that takes a token from `e.tokens` returns it via `ch <- struct{}{}` strictly before its matching `decRefs` call (the timeout path never took a token and correctly skips the return); since `refs` only reaches zero after every acquirer for that key has already run its own decRefs, the channel is provably full at the moment an entry is handed to `entryPool.Put`. The existing table lock discipline (per-shard mutex covering both `acquire` and `decRefs`) is unchanged, so there is no new TOCTOU between key removal and pool reuse. Existing tests (`TestThrottleTable_EntryPool_ReusedEntryIsFullyUsable`, `..._CapacityMismatch_FallsBackToFreshEntry`, `TestThrottlePerIPCapped_FastPath_RaceStress`) already validate the token-count invariant and cap-mismatch defence; I closed the one coverage gap I found — no test asserted the *concurrency limit itself* was respected (only that no request was ever rejected) — by adding `TestThrottlePerIPCapped_EntryPoolRecycling_NeverExceedsLimit`, which drives heavy key churn (maximising entry-pool reuse) and asserts the observed in-flight count never exceeds `limit` for any key. Passes under `-race`.
- **real_ip.go (WH-11):** The right-to-left comma scan is a byte-for-byte equivalent refactor of the previous `strings.Split`-based rightmost-untrusted-hop walk, including the `maxXFFHops` truncation boundary (verified algebraically: cutting after the k-th comma from the right is exactly the same substring as keeping the last `maxXFFHops` `strings.Split` parts) and the "no trust list" legacy-leftmost fallback. The existing `TestRealIP_SelectXFFRightmost_TableCases` and `_FuzzEquivalence` tests (50 000-header corpus, independent oracle) already provide strong differential coverage across spaces, empty entries, IPv6, zone IDs, bracketed/ported entries, garbage, and >30-hop headers, with and without a trust list. No gap found; no new test added.
- **api_key.go (WH-12):** The fused `apiKeyCtx` node follows the exact pattern already used by `requestIDCtx` (RequestID). `apiKeyCtxKey{}` remains an unexported, unforgeable type, so no cross-package or cross-instance collision is possible; `Value` intercepts only that key and falls through to the parent for everything else, correctly preserving values set both before and after APIKey in the chain. `apikey_wastehunt_test.go` already covers identity retrieval, fall-through in both directions, absence on a bare context, and cross-instance non-leakage. No gap found.
- **clean_path.go / strip_slashes.go (WH-04):** The shallow-copy pattern (`r2 := new(http.Request); *r2 = *r; r2.URL = new(url.URL); *r2.URL = *r.URL; r2.URL.Path = p`) is **identical** to Go's own standard library `net/http.StripPrefix` and to the internal semicolon-in-query fixup handler (`net/http/server.go`), confirmed by direct source inspection. Sharing the header map and context between `r` and `r2` is safe and intended here: both are used purely to rewrite the path immediately before continuing the SAME single-request, single-goroutine pipeline (the discarded `r` is never referenced again after this call), which is exactly the situation this stdlib idiom exists for. No finding.

## Coverage gaps

- The base64-decoder leniency behind MID-JWT-TEST-1 (Go's `RawURLEncoding.DecodeString` not validating unused trailing bits) is a general property of the Go standard library and the wider JOSE/JWT ecosystem, pre-dating and unrelated to WH-06. It was not evaluated as a product-level "signature malleability" concern (e.g. whether a system that deduplicates/blacklists JWTs by raw string rather than decoded bytes could be confused by multiple valid encodings of one MAC) because that is orthogonal to this diff and not something JWTAuth itself does (it only ever compares decoded bytes via `hmac.Equal`/`ecdsa.Verify`/RSA verification, never raw strings). Flagging for awareness only; recommend a dedicated pass by `timing-and-sidechannel-analyst` or a future audit if JWT string-based caching/deduplication is ever introduced elsewhere in the project.
- 1xx informational responses (Early Hints) are not otherwise supported end-to-end by `Compress`/`Logger` — after the fix, a 1xx call is simply ignored by both wrappers (never forwarded immediately to the real `ResponseWriter`), matching the pre-existing (pre-WH) behaviour rather than adding real Early-Hints pass-through. Implementing true immediate 1xx forwarding through `gzipResponseWriter`'s deferred commit/sniff design is a larger architectural change, out of scope for this security-regression fix (which only needed to stop the final status from being lost).
- `set_header.go`, `clean_path.go`, `strip_slashes.go` had no pre-existing dedicated `_wastehunt_test.go`; a new `middleware/setheader_wastehunt_test.go` was added. `clean_path.go`/`strip_slashes.go` needed no new test (matches stdlib idiom, no defect found).

## Evidence

- `reports/middleware-security-reviewer/evidence/2026-09-25/battery.txt` — full `go test ./middleware/... -race -count=1 -v` run after all fixes: 205 passed, 0 failed.
- `reports/middleware-security-reviewer/evidence/2026-09-25/jwt-flaky-repro-count200.txt` — full log of `go test ./middleware/... -run 'JWT|Jwt' -race -count=200 -v` **before** the MID-JWT-TEST-1 fix, showing the 24/200 flaky failures of `TestJWTAuth_HeaderMemo_DoesNotBypassSignatureVerification`, all with message `got 200, want 401`.

## Files changed by this review

Fixes (inside the already-changed diff, small and unambiguous per task authorization):
- `middleware/compress.go` — `gzipResponseWriter.WriteHeader` 1xx exemption (MID-COMPRESS-1)
- `middleware/logger.go` — `statusRecorder.WriteHeader` 1xx exemption (MID-LOGGER-1)
- `middleware/set_header.go` — per-request value slice instead of a shared singleton (MID-SETHEADER-1)
- `middleware/jwt_wastehunt_test.go` — replaced `flipBase64Char` with `corruptSignature` (MID-JWT-TEST-1)

New regression tests:
- `middleware/wrapper_flusher_test.go` — `TestCompress_1xxInformational_DoesNotBlockFinalStatus`, `TestLogger_1xxInformational_LogsFinalStatusNotInformational`
- `middleware/setheader_wastehunt_test.go` (new file) — `TestSetHeader_InPlaceSliceMutation_DoesNotLeakAcrossRequests`, `TestSetHeader_ConcurrentRequests_EachGetsIndependentSlice`
- `middleware/throttle_wastehunt_test.go` — added `TestThrottlePerIPCapped_EntryPoolRecycling_NeverExceedsLimit` (closes a coverage gap: prior tests checked "no rejections", not "limit never exceeded")

No git commits were made (per task instructions). No changes were made to CLAUDE.md, knowledge-model.md, or specification/.
