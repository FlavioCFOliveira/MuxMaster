# Concurrency Security Audit — Sprint 18 Contention-Hunt Fixes

Date: 2026-09-24
Commit under audit: f53aab1 (branch feature/18-performance-and-efficiency-laboratory), plus the uncommitted working-tree diff
Go: go1.27.0 linux/amd64
GOMAXPROCS used for all stress runs: 16

## Scope

This audit covers exactly the four concurrent-state changes made by Sprint 18
to fix the contention hotspots identified in
`reports/perf-lab-2026-09-24/contention-hunt.md`:

1. `middleware/throttle.go` — `ThrottlePerIPCapped`'s 64-way `maphash`-sharded
   key table (CH-01), and `ThrottleBacklog`'s lock-free `throttleSem`
   (CAS fast path + bounded channel slow path) replacing the pre-filled
   token-channel semaphore (CH-02).
2. `middleware/oauth2.go` — `oauth2Cache` eviction rewritten from an O(n)
   full-map scan to an O(log n) `container/heap` min-heap ordered by expiry
   (CH-09), plus the accompanying fix that extends `get()`'s `RLock` across
   the field reads of `e.resp`/`e.expiry` (required once `set()` started
   mutating an existing entry in place for `heap.Fix`).
3. `middleware/request_id.go` — a `sync.Pool` of 4 KiB pre-generated
   `crypto/rand` buffers (CH-05) plus the follow-up allocation rewrite: a
   fused `requestIDCtx` (context node + hex id storage + response-header
   backing array in one allocation) using `unsafe.String`.
4. `mux.go` — `redirectMWPtr`, a lock-free `atomic.Pointer[[]func(http.Handler)
   http.Handler]` snapshot of `m.middleware`, refreshed by `Use()`, read by
   `serveRedirect` instead of taking `m.mu.RLock()` on every redirect
   (CH-06).

No other file was in scope. `CLAUDE.md`, `knowledge-model.md`, docs,
`README.md`, `CHANGELOG.md` and `specification/` were not touched.

## Method

Read every line of the diff and reasoned through the Go memory model for
each change (atomics, `sync.RWMutex`, `sync.Pool`, channel happens-before,
interior pointers / `unsafe.String`, GC liveness of `unsafe`-aliased
storage). For every property named in the task brief, wrote an adversarial
stress test — internal (white-box, `package middleware`) where the property
requires inspecting private state (raw atomic counters, heap internals,
pool buffers), black-box (`package middleware_test` / `muxmaster_test`)
where the property is observable through the public API — then ran every
new and pre-existing test under `-race` at high concurrency (thousands of
goroutines, GOMAXPROCS=16) and, for the mandated files, `-count=20`.

One genuine defect was found and fixed — in my own test code, not in the
product. Full detail in "Self-caught test defects" below.

## Shared state enumeration

| Name | Kind | Access (R/W) | Sync | Lifetime |
|---|---|---|---|---|
| `throttleSem.inUse` | `atomic.Int64` | R/W every acquire/release | atomic CAS | per-middleware-instance |
| `throttleSem.queue` / `.wake` | bounded `chan struct{}` | R/W on slow path only | channel semantics | per-middleware-instance |
| `throttleTable.shards[i].m` | `map[string]*throttleEntry` | R/W per request (own shard only) | per-shard `sync.Mutex` | per-middleware-instance |
| `throttleTable.size` | `atomic.Int64` | R/W on every first-acquire/last-release | atomic reserve-then-insert | per-middleware-instance |
| `throttleEntry.tokens` | buffered `chan struct{}` | R/W per request | channel semantics + owning shard's mutex for entry lifecycle | per-key |
| `throttleEntry.refs` | `int` | R/W per acquire/decRefs | owning shard's mutex | per-key |
| `oauth2Cache.entries` / `.heap` | `map[[32]byte]*oauth2Entry` / `oauth2ExpiryHeap` | R (get) / W (set, evict) | `sync.RWMutex`, reads now span the field access | per-middleware-instance |
| `oauth2Entry.resp` / `.expiry` / `.idx` | fields | R under RLock / W under Lock (including in-place mutation on re-cache) | `oauth2Cache.mu` | per-cache-entry |
| `oauth2Inflight.calls` | `map[[32]byte]*oauth2InflightCall` | R/W per request | `oauth2Inflight.mu` | per-middleware-instance (transient per in-flight token) |
| `requestIDBufPool` | `sync.Pool` of `*requestIDBuf` | Get/Put per generated id | `sync.Pool` internal synchronisation | package-level, unbounded lifetime |
| `requestIDBuf.b` / `.off` | `[4096]byte` / `int` | R/W exclusively between Get and Put | exclusive pool ownership (no separate lock needed) | per pooled buffer |
| `requestIDCtx` (`c`) | struct (`buf`, `id`, `hdr`) | W once per request before publication, R after | none needed — per-request, non-pooled, single-owner until context escapes | single request |
| `Mux.redirectMWPtr` | `atomic.Pointer[[]func(http.Handler)http.Handler]` | W by `Use()` under `m.mu.Lock()`; R lock-free by `serveRedirect` | atomic pointer + one-writer-at-a-time via `m.mu` | mux lifetime |
| `Mux.middleware` | `[]func(http.Handler)http.Handler` | W by `Use()`; R by `Handle`, `lazyNotFound`, `lazyMethodNotAllowed`, `lazyOPTIONS` (all still under `m.mu`) | `sync.RWMutex` (`m.mu`) | mux lifetime |

## Per-area analysis, verdict and evidence

### 1. `middleware/throttle.go` — verdict: **sound**

**`throttleSem` (`ThrottleBacklog`).** The fast path (`tryAcquire`, atomic
CAS loop) and the slow path (`acquireWait`, backlog-bounded queue + bounded
wake channel) never disagree: `inUse` is the single source of truth for
"how many permits outstanding", updated only via `Add`/`CompareAndSwap`, and
`release()` decrements before signalling, so a waiter that wakes always
observes the decrement (both via the atomic total order and via the
channel-receive happens-before edge). The "no lost wake-up" design rests on
`cap(wake) == cap(queue) == backlog`: since at most `backlog` waiters can
ever be concurrently blocked (bounded by the `queue` reservation), the wake
channel's buffer can never be "under capacity" for the actual number of
waiters that need a signal — a dropped send (buffer momentarily full) only
happens when enough signals are already buffered to cover every currently
possible waiter. Verified this holds under adversarial conditions: a
single-permit baton handed serially through 2000 waiters that all queue up
simultaneously (`TestThrottleSem_NoLostWakeup_SerialisedHandoff`), and
4000 waiters at `limit=5` with a total-capacity budget that comfortably
exceeds demand (`TestThrottleSem_NoLostWakeup_HighConcurrencyChurn`) — zero
timeouts in either case, and `inUse` never negative or above `limit`
(`TestThrottleSem_InUseNeverNegativeNeverExceedsLimit`, 3000 goroutines x 40
iterations, peak sampled continuously).

**`throttleTable` (`ThrottlePerIPCapped`'s sharded key table).** The
cross-shard TOCTOU concern (CH-01) is closed correctly: `size` is a single
global atomic, so the "reserve capacity, roll back if it would overshoot"
check is race-free regardless of which of the 64 shards a concurrent
caller's key lands on. Confirmed empirically that `size` is a
**reservation** counter that can legitimately read momentarily above
`maxTableSize` mid-race (multiple goroutines calling `Add(1)` before some
roll back) without that ever translating into an actual map insertion
beyond the cap — see "Self-caught test defects" below for how this was
distinguished from a real bug. The real, externally-visible invariant —
the number of keys ever actually accepted — is exact: flooding 4000
goroutines with distinct keys against `maxTableSize=200` (spread across all
64 shards) accepted **exactly** 200
(`TestThrottleTable_ExactCapAcrossAllShards_NoOvershoot`). `size` never
observed negative under 2000 goroutines x 60 iterations of mixed
shared/distinct-key churn, and settles to exactly the real, summed map
population once quiescent
(`TestThrottleTable_SizeNeverNegative_CrossShardChurn`). Refs reclamation,
panic-release, and timeout-release were already covered by the pre-existing
`middleware/throttle_shard_test.go` (written alongside the product change)
and re-verified here at `-count=20`.

### 2. `middleware/oauth2.go` — verdict: **sound**

The fix already present in the diff — extending `get()`'s `RLock` across
the read of `e.resp`/`e.expiry` — correctly closes the race that the
in-place mutation on re-cache (`set()`'s `heap.Fix` path) introduced: since
`get()` (RLock) and `set()`/`evictOneLocked` (Lock) are mutually exclusive
via `sync.RWMutex`, a `get()` call now always observes a fully-consistent,
non-torn entry. The returned `*IntrospectResponse` pointer is never mutated
in place after construction (only the *entry's* `resp` field is
reassigned to a *new* response object), so callers holding a previously
returned pointer are unaffected by later cache activity for the same key.

Heap/map consistency was verified beyond the pre-existing `Len()` equality
check: `assertHeapInvariants` (new, in `oauth2_cache_internal_test.go`)
checks, for every entry, that `idx` is exactly its real position in the
slice (not just in range) and that the array satisfies the min-heap
property — run after 500 goroutines x 200 iterations of mixed
new-key-insert (forces `evictOneLocked`/`heap.Pop`), same-key re-cache
(forces `heap.Fix`, the path this sprint added), and concurrent `get()`
(`TestOAuth2Cache_HeapInvariants_UnderConcurrentChurn`). Singleflight
interaction was verified end-to-end through the real HTTP dispatch (not
just the internal cache API): many concurrent requests for the identical
bearer token, which coalesce into one singleflight leader whose result is
then independently `cache.set()` by every follower — exactly the
MSR-2026-0071 scenario the diff's own comment calls out — produced zero
failures across 20 waves of 300 goroutines
(`TestOAuth2Introspect_ConcurrentSameToken_NoRace`), and 800 goroutines
churning through 5000 distinct tokens against a 32-entry cache (continuous
heap eviction under real HTTP load) also produced zero failures
(`TestOAuth2Introspect_ConcurrentManyTokens_HeapEvictionUnderLoad`). No
stale heap index was observed in any run.

### 3. `middleware/request_id.go` — verdict: **sound**

Pool contamination canary: `nextRandomID()` returns a **value copy**
(`[16]byte`) — the pooled buffer's storage is never aliased out to a
caller, only copied from, and `sync.Pool`'s own Get/Put synchronisation
guarantees exclusive ownership of a given `*requestIDBuf` between a Get and
its matching Put. Drew 2,129,024 IDs across 256 goroutines
(`TestNextRandomID_NoOverlapNoDuplicate_HighConcurrency`) and 256,000 IDs
across 128 goroutines through the full HTTP middleware
(`TestRequestID_ConcurrentRequests_ContextNeverCrossesRequests`,
pre-existing `TestRequestID_NoDuplicates_ConcurrentGeneration` adds another
200,000) — zero duplicates in any run, which at 128-bit entropy is
conclusive evidence against both a byte-range reuse bug and a
double-checked-out buffer, since either would show up as an exact
duplicate at this sample size. `newExhaustedRequestIDBuf`'s `off ==
len(b)` sentinel was confirmed to reliably force an immediate refill
(`TestRequestIDBufPool_RefillNeverServesUninitialisedBytes`), closing the
"first buffer silently serves zero bytes as if random" hazard the comment
calls out.

Memory-model safety of `unsafe.String(&c.buf[0], len(c.buf))`: `c` is a
**per-request, non-pooled** allocation (unlike `requestIDBufPool`), so
there is no cross-request aliasing surface for `c.buf` itself. `c.buf` is
written exactly once, synchronously, strictly before `c` is published via
`r.WithContext(c)` (the only point `c` becomes reachable from code that
could run concurrently), which satisfies the Go memory model's
happens-before requirement for goroutine creation; nothing in this diff
mutates `c.buf` afterward. The GC keeps the whole `c` allocation alive via
interior-pointer semantics for as long as the `unsafe.String`-derived
string is reachable — correct, documented Go behaviour.

Header/context aliasing: `c.hdr[:]` is stored directly as the response
header's backing slice, so a downstream handler that indexes
`w.Header()["X-Request-Id"][0] = ...` directly (bypassing `Header.Set`,
which always allocates a fresh slice and would not touch `c.hdr`) writes
into a field of the SAME allocation as `c.id`/`c.buf`. Verified this can
never corrupt the context's id, in both the generated-id path (backed by
`c.buf` via `unsafe.String`) and the propagated-id path (aliasing the
inbound header's own string) — `c.hdr[0] = c.id` copies a string *header*
(pointer+length) into the slot; overwriting that slot only changes what
the slot points to, it can never mutate the immutable string bytes `c.id`
still points to
(`TestRequestID_DownstreamHeaderMutation_DoesNotCorruptContextID`,
`TestRequestID_DownstreamHeaderMutation_PropagatedPath`).

### 4. `mux.go` — verdict: **sound**

`redirectMWPtr` is only ever written inside `Use()`'s critical section
(`m.mu.Lock()` held throughout), strictly after `m.middleware` itself is
updated, in the same goroutine, in program order — so every published
snapshot exactly matches `m.middleware`'s state at the end of that
particular `Use()` call, and concurrent `Use()` calls are fully serialised
by `m.mu`, giving a monotonically-growing sequence of snapshots with no
reordering possible. `serveRedirect`'s lock-free `Load()` can only ever
observe either the immediately-preceding *complete* snapshot or a more
recent one — the same eventual-consistency guarantee already accepted for
`lazyNotFoundPtr`/`methodNotAllowedCache`/`optionsCache`, and consistent
with the other three `m.middleware` readers, which still correctly take
`m.mu.RLock()` (grep-verified — no other unlocked reader of `m.middleware`
was introduced or pre-existing). `Use()` after routes still applies
correctly to `serveRedirect` (pre-existing
`TestRedirect_Use_MiddlewareAfterRoute_StillWrapsRedirect`). Stress-tested
50→20 concurrent `Use()`-calling goroutines racing 200 goroutines firing
301 redirects simultaneously under `-race`
(`TestRedirect_ConcurrentUseVsConcurrentRedirects_NoRace`): zero races,
zero unexpected status codes, and the final settled redirect is wrapped by
exactly the full, final middleware chain (no middleware layer permanently
lost to the race).

## Self-caught test defects (not product defects)

Two issues surfaced entirely within my own test scaffolding, never in
`throttle.go`, `oauth2.go`, `request_id.go`, or `mux.go`:

1. **False-positive "cap overshoot" assertion.** My first version of the
   `throttleTable` stress tests asserted `table.size` never exceeds
   `maxTableSize` at any sampled instant during active churn. This is not
   the invariant the reserve-then-insert design actually provides:
   `size` is a *reservation* counter that legitimately reads momentarily
   above the cap while several concurrent `Add(1)` calls race ahead of
   their conditional rollback — the design's own safety property is that
   this transient overshoot of the *counter* never translates into an
   overshoot of the *real* map population (insertion only happens after
   the check passes). Verified this with a throwaway probe
   (`accepted == maxTableSize` exactly, every run, `size == accepted`
   settled) before rewriting the tests to assert the correct,
   externally-meaningful invariant: total *accepted* keys, not the raw
   counter's mid-race value. See `TestThrottleTable_SizeNeverNegative_
   CrossShardChurn` and `TestThrottleTable_ExactCapAcrossAllShards_
   NoOvershoot` for the corrected versions and their doc comments.
2. **Infinite loop in a draft test.** An early draft of
   `TestNextRandomID_SequentialBufferConsumption_NoSkipNoRewind` tried to
   "drain" `requestIDBufPool` via `for { v := pool.Get(); if v == nil {
   break } }` — but `requestIDBufPool` has a non-nil `New`, so `Get()`
   never returns `nil` and the loop never terminated. Caught by the
   background test run hanging; killed and replaced with
   `TestNextRandomID_SequentialDraws_SpanningManyRefills_AllUnique`, which
   proves the same property (no skip/rewind in the offset/refill
   bookkeeping — a skip or rewind would manifest as a duplicate) without
   depending on exclusive access to a shared package-level pool, and
   without any `t.Skip` (forbidden by this project's testing rules).

Neither issue reached a committed state; both are documented here per the
audit brief's instruction to report exactly what was found and corrected.

## Race detector results

| Test scope | Command | Iterations | Races | Status |
|---|---|---|---|---|
| New + existing throttle/oauth2/request_id/redirect tests | `go test -race -count=20 ./middleware/ -run 'Throttle\|RequestID\|OAuth2'` | 20 full passes | 0 | PASS (97.9s) |
| Root package (mux.go + redirect stress test) | `go test -race .` | 1 | 0 | PASS (3.0s) |
| Whole middleware package | `go test -race -count=1 ./middleware/...` | 1 | 0 | PASS |
| Whole repository (extra, not mandated) | `go test -race -count=1 ./...` | 2 | 0 in scope | 1 run PASS; 1 run FAIL in an unrelated, out-of-scope package — see note below |
| `go vet ./...` | — | — | — | 0 warnings |
| `golangci-lint run ./...` | — | — | — | 0 issues (after fixing 2 staticcheck QF1012 findings in my own new test file, `oauth2_concurrency_test.go`) |

**Note on the whole-repository run.** Running `go test -race -count=1 ./...`
(my own extra diligence, over and above the task's mandated validation
commands) twice produced two DIFFERENT, non-reproducing failures, both
outside this audit's scope:

- Run 1: `FAIL reports/http-protocol-security-auditor/harness` —
  `TestS8_H8_30_PanicHandlerDoublePanic`, committed 2026-05-08. Re-running
  this package alone immediately afterward: **PASS**.
- Run 2: `FAIL reports/concurrency-security-auditor/harness` —
  `TestTimeoutMW_SlowHandler_ContextCancelled` (in `goroutine_leak_test.go`,
  also committed 2026-05-08). The `http-protocol-security-auditor` package
  that failed in Run 1 **passed** in Run 2.

Both failing tests belong to evidence harnesses from prior sprints
(2026-05-08), in files this diff does not touch, and both assert real
wall-clock timing behaviour (`http.Server`'s connection-level panic
recovery timing; a timeout middleware's context-cancellation deadline)
that is inherently sensitive to scheduling latency when dozens of `-race`-
instrumented packages — several spinning up real `httptest.NewServer`
instances — run concurrently under one `go test ./...` invocation. Neither
failure reproduced on a second, isolated run of its own package, and
neither touches `throttle.go`, `oauth2.go`, `request_id.go`, `mux.go`, or
any file this audit added or edited. This is a **pre-existing, out-of-scope
flakiness characteristic of the whole-repository suite under heavy
concurrent `-race` load**, not a defect in anything audited here; per this
project's scope-discipline rule it is reported to the user rather than
investigated or fixed as part of this task. The two commands the task
actually mandates — `go test -race -count=20 ./middleware/ -run
'Throttle|RequestID|OAuth2'` and `go test -race ./` (root package only) —
both passed cleanly and repeatably every time they were run and are the
sole basis for this audit's PASS verdicts.

## Pool contamination canary results

| Test | Iterations | Leaks/dupes | Status |
|---|---|---|---|
| `TestNextRandomID_NoOverlapNoDuplicate_HighConcurrency` | 2,129,024 draws / 256 goroutines | 0 | PASS |
| `TestRequestID_NoDuplicates_ConcurrentGeneration` (pre-existing) | 200,000 / 64 goroutines | 0 | PASS |
| `TestRequestID_ConcurrentRequests_ContextNeverCrossesRequests` (new) | 256,000 / 128 goroutines | 0 mismatches, 0 dupes | PASS |
| `TestNextRandomID_SequentialDraws_SpanningManyRefills_AllUnique` (new) | 25,607 / 1 goroutine | 0 | PASS |

## Findings

No exploitable defect was found in the shipped product code
(`throttle.go`, `oauth2.go`, `request_id.go`, `mux.go`). All four areas are
assessed **sound** for the properties enumerated in the audit brief, backed
by the adversarial evidence above.

| ID | Severity | CWE | File:line | Summary | Evidence |
|---|---|---|---|---|---|
| — | — | — | — | No findings against shipped product code this sprint | — |

## Documented safe patterns

- **Reserve-then-insert with a global atomic counter, decomposed across
  per-shard locks.** `throttleTable.acquire`/`decRefs` correctly closes a
  cross-shard TOCTOU by making the *capacity* decision a single global
  atomic operation, independent of which shard's mutex protects the
  key-specific map operation. The counter is allowed to be a conservative
  (never-under) estimate transiently — this is safe and expected, not a
  bug, as long as no code path treats the counter's raw value as an exact
  live-population count outside of the create/decrement pairing itself.
- **Bounded dual-channel semaphore (queue + wake) sized identically to the
  backlog bound.** Guarantees no permanent lost wake-up as long as
  `cap(wake) >= max concurrently blocked waiters`, which
  `newThrottleSem` enforces by construction (`cap(queue) == cap(wake) ==
  backlog`).
- **`RWMutex`-protected cache where `get()`'s critical section spans every
  field read of a possibly-in-place-mutated entry.** The correct fix for
  "an entry stopped being immutable-after-construction" is to extend the
  reader's lock over the reads, not to special-case the writer.
- **Per-request, non-pooled context node using `unsafe.String` over an
  embedded byte array.** Safe specifically because the containing object
  is freshly allocated per request (no cross-request reuse), and the
  unsafe aliasing is written-once-before-publication with no reachable
  mutation path afterward — this pattern would NOT be safe if the
  containing struct were ever recycled via `sync.Pool` without an explicit
  clear of the aliased field first.
- **Publishing an `atomic.Pointer` snapshot from inside an existing
  mutex-protected writer critical section**, mirroring the
  `lazyNotFoundPtr`/`methodNotAllowedCache` pattern already established in
  this codebase, gives lock-free readers a monotonically-consistent view
  without any new synchronisation primitive on the reader side.

## Coverage gaps

- **Fuzz/property-based testing** of `throttleTable`/`oauth2Cache`/
  `requestIDCtx` internals was not performed — this audit used targeted,
  hand-constructed adversarial interleavings and high-volume randomised
  duplicate/invariant checks, not `pgregory.net/rapid` or native Go fuzzing.
  That is `fuzzing-and-property-engineer`'s mandate, not this audit's.
- **Real multi-second wall-clock TTL expiry races** in `oauth2Cache` (an
  entry expiring via `time.Now().After(e.expiry)` exactly while a
  concurrent `set()` is mid-`heap.Fix` for the same key) were exercised
  incidentally by the churn tests (sub-second/second-scale expiries) but
  not isolated as a dedicated boundary test.
- **`ThrottlePerIP`'s unbounded-table legacy mode** (`maxTableSize <= 0`)
  was not separately stress-tested for concurrency correctness in this
  sprint — only `ThrottlePerIPCapped`'s bounded path was in scope, and the
  unbounded path shares the same `acquire`/`decRefs` code with the size
  bookkeeping simply skipped.
- **Goroutine-leak profiling** (`goleak`-style before/after counts) was not
  performed for `throttleSem.acquireWait`'s `timer := time.NewTimer(...)`
  path — every path does `defer timer.Stop()`, which is standard-library
  correct, but no explicit leak-detector run was executed against it this
  sprint.
- **Cross-package interaction** between `redirectMWPtr` and `Group`'s own
  middleware chain (if any) was not audited — only the root `Mux.Use()` /
  `serveRedirect` pair was in scope per the task brief.

## Next actions

None required — no defect was found in the audited product code. If the
project wants deeper confidence before the v1.2.0 gate, the coverage gaps
above (particularly fuzzing of `oauth2Cache`/`throttleTable` and a
dedicated TTL-boundary race test) would be reasonable follow-up work for
`fuzzing-and-property-engineer`, but nothing here blocks the sprint.
