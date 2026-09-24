# Middleware Security Review — Sprint 18 Contention Fixes

**Date:** 2026-09-24
**Scope:** `middleware/throttle.go`, `middleware/request_id.go`, `middleware/oauth2.go` — the changes made by rmp tasks #244, #245 and #246 to remove the contention found in `reports/perf-lab-2026-09-24/contention-hunt.md`.
**Related:** `reports/concurrency-security-auditor/2026-09-24-sprint18-contention-fixes.md` (race and invariant testing — not repeated here).

## Summary verdict

| Middleware | Verdict | Defects found | Fix applied |
|---|---|---|---|
| `middleware/throttle.go` | PASS | None | None |
| `middleware/oauth2.go` | PASS | None | None |
| `middleware/request_id.go` | PASS, one defect fixed | MSR-2026-0072 (unreachable fallback code) | Unreachable `math/rand/v2` fallback removed |

Validation: `go build ./...`, `go vet ./...`, `golangci-lint run ./...` (0 issues), `staticcheck ./middleware/...`, `gofmt -l middleware/` (no output), `go test -race ./middleware/...` — all pass.

## 1. `middleware/throttle.go` — PASS

Reviewed the 64-way `hash/maphash`-sharded table (`throttleTable`) and the atomic fast path with channel slow path (`throttleSem`) against MSR-2026-0068, DOS-2026-0002, TM-2026-013 and DOS-2026-0057.

- **Shard targeting.** `newThrottleTable` draws a fresh `maphash.MakeSeed()` per instance. Verified that independently constructed tables get different seeds, that the seed changes `shardFor`'s output, and that 200 000 adversarial sequential-IP-shaped keys distribute within 2× of the mean across all 64 shards with no empty shard. An attacker cannot precompute a shard-colliding key set offline or reuse one across deployments.
- **`maxTableSize` cap under churn.** Proven exact by `TestThrottlePerIPCapped_ExactCapUnderConcurrentChurn` and `TestThrottleTable_ExactCapAcrossAllShards_NoOvershoot` (reserve-then-insert atomic counter).
- **503 semantics at saturation.** With the table full, a new key is rejected and every already-tracked key is still served, with no duplicate slot consumed.
- **No new DoS vector.** `sem.queue` and `sem.wake` both have `cap == backlog`. Neither middleware spawns goroutines; `acquireWait` runs synchronously in the caller's goroutine.
- **No per-key limit bypass under churn.** 500 attackers × 20 rounds with 2 ms timeouts and a concurrent watchdog: `inUse` never exceeded `limit`.

Regression tests: `middleware/throttle_security_test.go` (5 tests).

## 2. `middleware/oauth2.go` — PASS

Reviewed the `container/heap` min-heap eviction rewrite.

- **`MaxCacheSize` bound.** Proven by `TestOAuth2Cache_SizeNeverExceedsMaxSize` and its concurrent-churn variant (`heap.Len() == len(entries)` throughout).
- **Expired-first eviction and DOS-2026-0005 fallback.** The heap root is always the minimum-expiry entry, so one pop satisfies both guarantees of the former two-phase scan.
- **No stale-token serving.** `get()` checks `time.Now().After(e.expiry)` independently of eviction order. A short-lived entry that is not the next eviction candidate was never served past its expiry across 200 unrelated inserts. The "never cache `Active=false`" guarantee lives at the `OAuth2Introspect` call site (`if !resp.Active { return }` before `cache.set`), not in the cache primitive.
- **`sha256` keying.** Unchanged.

**Behavioural note (not a defect):** the former implementation could evict every expired entry in one cache-full `set()`; the new one evicts one entry (the heap root) per call. The cap stays exact; expired entries may occupy a slot until the next `set()` reaps them.

Regression tests: `middleware/oauth2_security_test.go` (2 tests).

## 3. `middleware/request_id.go` — PASS, one defect fixed

### MSR-2026-0072 — unreachable `crypto/rand.Read` error branch (Informational)

The change under review added a `math/rand/v2` fallback, with a warning log, for the case where `crypto/rand.Read` returns an error.

**Finding:** since Go 1.24 (this module declares Go 1.26), `crypto/rand.Read` never returns an error. On an entropy-source failure it crashes the program irrecoverably before returning, as its documentation states: *"Read calls io.ReadFull on Reader and crashes the program irrecoverably if an error is returned."* Verified empirically with an injected failing `rand.Reader`:

```
fatal error: crypto/rand: failed to read random data (see https://go.dev/issue/66821): injected failure
```

The fallback branch was therefore unreachable, and its comment misdescribed the runtime behaviour.

**Fix:** removed the unreachable branch, the `math/rand/v2` fallback, the warning log, the unused imports (`encoding/binary`, `log/slog`, `math/rand/v2`) and the unused `requestIDReadFallbackWarnOnce` variable. The pooled `crypto/rand` refill, the fused `requestIDCtx` allocation, the canonical header access and `validRequestID` are unchanged. IDs are generated only from `crypto/rand`, as `specification/middleware-stdlib.md` §4.16 requires.

**Regression test:** `TestSec_NextRandomID_CryptoRandDefaultFailureModeCrashesProcess_NotFallback` in `middleware/request_id_security_test.go` re-executes the test binary as a subprocess, injects a failing `Reader`, and asserts the child terminates with `crypto/rand`'s fatal message. It fails if a future Go release loosens this contract, which would require this code to be reviewed again.

### Other properties — PASS

- **Multiple inbound `X-Request-Id` values:** direct access `r.Header[requestIDHeaderKey][0]` matches `Header.Get` (first value wins).
- **CRLF and control characters:** `validRequestID` (alphanumerics plus `-_.`) rejects CRLF, bare LF and NUL before any header write.
- **Oversized inbound value (10 KiB):** rejected by the 128-character bound. Empty value: replaced.
- **`unsafe.String` memory safety:** `c.buf` is a fixed-size field of the same allocation as `*requestIDCtx`, written once before `c` is published by `r.WithContext(c)` and never mutated afterwards.
- **MM-2026-0011 validation:** unchanged and enforced.

Regression tests: `middleware/request_id_security_blackbox_test.go` (4 tests), `middleware/request_id_security_test.go` (1 test).

## Open decisions for the maintainer

None.
