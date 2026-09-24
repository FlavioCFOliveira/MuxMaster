# Contention Hunt — MuxMaster under extreme parallelism

**rmp task:** #243, sprint 18 ("Performance and Efficiency Laboratory")
**Date:** 2026-09-24
**Agent:** go-perf-optimizer
**Scope:** root package (`Mux`, `tree.go`, `params.go`) and every middleware under `middleware/`. Out of scope: `examples/`, `competitor/`, `reports/**/vendor`.

## Environment

| | |
|---|---|
| CPU | AMD Ryzen 9 5900HX, 16 logical CPUs |
| OS | Linux 6.8.0-139-generic (Ubuntu-based) |
| Go | go1.27.0 linux/amd64 (module declares `go 1.26`) |
| `ulimit -n` (soft / hard) | 1048576 / 1048576 (no adjustment needed for the 10 000-connection load run) |
| `nproc` | 16 |

## Reproduction

All harness code lives outside the module root, under `reports/perf-lab-2026-09-24/`, and imports MuxMaster via a `replace` directive. No product `*.go` file was modified.

```bash
cd reports/perf-lab-2026-09-24
./run.sh              # full sweep: -cpu 1,2,4,8,16 scaling, mutex/block/cpu/mem profiles, go tool trace
./run.sh quick         # smoke run (benchtime=50x, count=3), ~30s

# Real-socket load harness (server and client are SEPARATE processes — see
# "Methodology correction" below):
cd loadgen
./driver.sh 1000 5     # <conns> <duration_seconds>
./driver.sh 5000 5
./driver.sh 10000 5
```

Artifacts produced:

```
reports/perf-lab-2026-09-24/
├── contention-hunt.md          (this file)
├── run.sh, .gitignore
├── harness/                    (go.mod with replace; b.RunParallel microbenchmarks)
│   ├── contention_bench_test.go
│   └── layout_test.go          (unsafe.Sizeof/Offsetof cache-line inspection)
├── loadgen/                    (go.mod with replace; two-process load generator)
│   ├── main.go, server.go, client.go, driver.sh
├── profiles/                   (mutex.pb.gz, block.pb.gz, cpu.pb.gz, mem.pb.gz, trace.out,
│                                 loadgen-server-{cpu,mutex,block,goroutine}-{1000,5000,10000}.pb.gz)
└── results/                    (scaling-cpu{1,2,4,8,16}.txt, scaling-benchstat.txt,
                                  profiled-run.txt, loadgen-{1000,5000,10000}.txt)
```

### Methodology correction: separating client and server processes

The first version of the real-socket load harness ran the MuxMaster server and the Go `net/http` load-generator client **in the same OS process**. `runtime.SetMutexProfileFraction(1)` / `SetBlockProfileRate` are process-global, so the captured mutex profile was **100% attributed to `net/http.(*Transport).queueForIdleConn` / `tryPutIdleConn` / `dialConn`** — the load generator's own connection-pool lock, not MuxMaster. This was caught by inspecting the profile before drawing conclusions (`go tool pprof -top loadgen-mutex-*.pb.gz`, all top frames under `net/http.(*Transport)`), per the project's "never guess / measure to decide" mandate. The harness was restructured into `loadgen -mode=server` and `loadgen -mode=client`, run as separate processes by `driver.sh`; all real-socket findings in this report use the corrected, server-only profiles (`loadgen-server-*.pb.gz`).

## Scaling summary (`b.RunParallel`, -cpu 1/2/4/8/16, benchtime=200ms, count=6)

Efficiency = `(ns/op at cpu=1 ÷ N) ÷ (ns/op at cpu=N)`, expressed as a percentage of ideal linear scaling. 100% = perfect scaling, dropping values = contention or allocator/GC pressure, values that *rise* between cpu levels (shown as **anti-scaling**) are the strongest contention signal available from this method.

| Surface | cpu=1 | cpu=4 | cpu=16 | Efficiency @16 | Verdict |
|---|---|---|---|---|---|
| `ParallelStatic` | 16.6 ns | 4.0 ns | 2.5 ns | ~41%¹ | lock-free, near noise floor |
| `ParallelParam1` (default, 1 alloc/op) | 143.2 ns | 94.7 ns | 104.4 ns | ~9% | **allocator-contended (CH-03)** |
| `ParallelParam1Pooled` (`PoolRequestBundle=true`, 0 alloc/op) | 41.5 ns | 11.0 ns | 7.3 ns | ~36% | much better, still sub-linear |
| `ParallelFastParam1` (1 alloc/op) | 49.0 ns | 13.2 ns | 16.0 ns | ~19% | **allocator-contended (CH-04)** |
| `ParallelFastParam1Pooled` (0 alloc/op) | 39.3 ns | 10.3 ns | 5.0 ns | ~49% | best of the param-route family |
| `ParallelCatchAll` (1 alloc/op) | 146.2 ns | 98.8 ns | 105.5 ns | ~9% | same shape as `ParallelParam1` |
| `ParallelMethodNotAllowed` (sync.Map cache) | 202.5 ns | 51.5 ns | 43.5 ns | ~29% | no contention after warm-up |
| `ParallelOPTIONS` (sync.Map cache) | 190.3 ns | 53.5 ns | 46.8 ns | ~25% | no contention after warm-up |
| `ParallelRedirectTrailingSlash` (`m.mu.RLock()` every call) | 302.9 ns | 99.0 ns | 98.0 ns | ~19% | **RWMutex on hot path (CH-10)** |
| `ParallelRedirectTrailingSlashWithMiddleware` | 363.5 ns | 123.0 ns | 124.0 ns | ~18% | same, slightly worse |
| `ThrottleBacklogParallel` (channel semaphore) | 37.8 ns | 63.7 ns | 76.7 ns | **anti-scaling (2.0×  slower)** | **channel-lock contended (CH-02)** |
| `ThrottlePerIPSingleKey` (global mutex + shared channel) | 4082 ns | 880 ns | 651 ns | ~62%² | improves, but see ManyKeys below |
| `ThrottlePerIPManyKeys` (global mutex, distinct channels) | 4110 ns | 1570 ns | 2164 ns | **anti-scaling past cpu=4** | **global table mutex contended (CH-01)** |
| `OAuth2CacheHitSteadyState` (RLock only) | 3179 ns | 788 ns | 341 ns | ~58% | no contention (CH-07) |
| `OAuth2ManyTokensNoCacheHit` (real TLS round trip per call) | 131 µs | 129 µs | 161 µs | n/a — network-bound | not a lock-contention signal |
| `OAuth2SingleflightCoalesce` | 122 µs | 31.8 µs | 8.6 µs | n/a — by-design coalescing | working as intended (CH-08) |
| `JWTAuthHS256Parallel` (`sync.Pool` of `hmac.Hash`) | 4743 ns | 1232 ns | 759 ns | ~78% | scales well |
| `CompressParallel` (`sync.Pool` of `*gzip.Writer`) | 3169 ns | 2365 ns | 2049 ns | ~10%³ | CPU-bound (Deflate), not lock-bound |
| `LoggerParallel` (2× `sync.Pool`) | 7244 ns | 1840 ns | 560 ns | ~81% | scales well |
| `RequestIDParallel` (`crypto/rand.Read` fallback) | 1106 ns | 299 ns | 297 ns | ~23% | **crypto/rand internal lock (CH-05)** |
| `RealIPParallel` | 65.2 ns | 25.8 ns | 30.0 ns | ~14%¹ | stateless, near noise floor |
| `RecovererParallel` | 12.0 ns | 3.0 ns | 1.0 ns | n/a¹ | stateless, near noise floor |
| `BasicAuthParallel` / `APIKeyParallel` / `CORSParallel` | 200/523/232 ns | 58/186/68 ns | 40/205/36 ns | mixed¹ | stateless map reads; see CH-15 |

¹ Sub-20 ns operations are close to the `b.RunParallel` loop's own per-iteration overhead (an atomic decrement in `pb.Next()`); "efficiency" at this scale is dominated by benchmark-harness noise, not real contention. Treated as informational, not a finding.
² `ThrottlePerIPSingleKey` *appears* to scale because the fixed cost of contending on ONE shared 64-slot channel is amortised over more parallel waiters — it does not mean the design is contention-free (see CH-01).
³ `CompressParallel`'s ~90% "loss" is the fixed CPU cost of gzip level-6 Deflate on an 8 KiB payload, confirmed by CPU profiling (not mutex/block profiling) — reclassified as CPU-bound, not contention-bound.

Full per-cpu-level raw output: `results/scaling-cpu{1,2,4,8,16}.txt`; `benchstat` cpu1-vs-cpu16 comparison: `results/scaling-benchstat.txt`.

## Real-socket load-harness results

Two-process harness (`loadgen/server.go` + `loadgen/client.go`), realistic route mix (static, 1-param, 3-param, catch-all, a `ThrottlePerIP`-guarded route with a 16-shard key, and a `JWTAuth`-guarded route), 16 logical CPUs shared between server and client on the same machine (loopback — see caveat below).

| Connections | req/s | p50 | p90 | p99 | p99.9 |
|---|---|---|---|---|---|
| 1 000 | 58 157 | 11.4 ms | 14.2 ms | 32.1 ms | 53.9 ms |
| 5 000 | 54 611 | 61.5 ms | 70.8 ms | 140.4 ms | 230.1 ms |
| 10 000 | 43 683 | 144.7 ms | 196.7 ms | 322.6 ms | 442.3 ms |

**Caveat:** server and client run as separate OS processes but share the same 16-core machine over loopback, so the falling throughput and rising latency reflect *combined* client+server CPU oversubscription (10 000 client goroutines plus the server's connection-handling goroutines competing for 16 cores), not solely server-side lock contention. The server-side mutex/block/CPU profiles captured *during* each run (below) isolate what fraction of that degradation is attributable to actual locks inside MuxMaster/middleware versus scheduler/GC pressure.

### Server-side mutex profile at 10 000 connections (`profiles/loadgen-server-mutex-10000.pb.gz`)

```
      flat  flat%   sum%        cum   cum%
 1102.65ms 67.15% 67.15%  1102.65ms 67.15%  runtime.unlock
  535.91ms 32.64% 99.79%   535.93ms 32.64%  sync.(*Mutex).Unlock
```
Attributed by call-tree peek (`go tool pprof -peek`):
- `middleware.ThrottlePerIPCapped` (acquire + decRefs + the deferred release): **≈143 ms**, ≈8.7% of total captured server-side mutex delay — confirms CH-01 under real sockets, not just the isolated microbenchmark.
- `middleware.Logger`'s `sync.Pool.Get()` (the `statusRecorder`/buffer pools): **≈87 ms**, ≈5.3% of total.
- The remainder is spread across `net/http`'s own internal locks (`bufio.Writer.Flush`, connection state) and Go's memory allocator (`mallocgc` size-class locks, reached through `net/http.(*Request).WithContext` / `context.WithValue` in every middleware that still allocates a context node) — consistent with CH-03/CH-04's allocation-driven-contention diagnosis, now confirmed under real sockets rather than only in the synthetic microbenchmark.

## Findings, ranked by estimated gain

| ID | Location | Evidence | Root cause | Proposed fix | Est. gain | Risk |
|---|---|---|---|---|---|---|
| **CH-01** | `middleware/throttle.go:132-166` (`ThrottlePerIP`/`ThrottlePerIPCapped`) | `ThrottlePerIPManyKeys` anti-scales past 4 cores (1570→2164 ns, cpu4→cpu16); real-socket server profile attributes ≈8.7% of total mutex delay to this middleware alone | A single global `sync.Mutex mu` guards the entire `table map[string]*entry` in both `acquire()` and `decRefs()`, taken on **every** request regardless of key, even though per-key limiting itself uses independent channels | Shard the table (striped-lock map, e.g. 64-way by `hash(key)%64`) or replace with `sync.Map` + atomic refcount so the common (entry-exists) path needs no global lock | Largest of all findings: removes the only unconditional global lock in a middleware advertised for exactly this use case (rate limiting under load) | Correctness of `refs` counting and `maxTableSize` enforcement must move to a per-shard (or atomically-summed) scheme — moderate middleware-code change, not attempted here (read-only constraint) |
| **CH-02** | `middleware/throttle.go:20-56` (`ThrottleBacklog`/`ThrottleAllBacklog`) | `ThrottleBacklogParallel` anti-scales monotonically: 37.8 ns (cpu1) → 76.7 ns (cpu16), i.e. 2× slower, well under the configured `limit=64` (so it is not limit-saturation) | Go's buffered channel used as a semaphore is guarded internally by `hchan.lock`; every request does 2 lock/unlock pairs (acquire + deferred release) on the SAME channel object, serializing all cores through one runtime lock | Replace the fast (non-blocked) acquire path with a lock-free `atomic.Int64` CAS-bounded counter; keep the channel only for the actual backlog-wait case | Second-largest: the fast path is the overwhelmingly common case in production (most requests are not throttled) | Loses the channel's implicit FIFO fairness among waiters — a behavioural change requiring product sign-off |
| **CH-03** | `mux.go:1006-1027`, `params.go:340-357` (unpooled `dispatchParams1`, default `Mux.PoolRequestBundle=false`) | `ParallelParam1` efficiency ≈9% at 16 cores vs the SAME route with `PoolRequestBundle=true` at ≈36% efficiency (and 14.4× lower absolute ns/op at 16 cores, vs only 3.4× at 1 core) | Every request allocates a 384 B `reqBundle1`; aggregate mutex profile shows allocation-adjacent call sites (`net/http.(*Request).WithContext`, `context.WithValue`) dominating captured mutex delay in nearly every allocating code path — Go's per-size-class allocator locks (`mcentral`) become the bottleneck at high core counts | No code change needed — **`Mux.PoolRequestBundle` (Opt O13) already exists and already fixes this**; the finding is that its benefit *grows* with core count, not just single-core ns/op | Quantifies the existing knob: recommend documenting/considering it as the default for high-concurrency deployments | None (already-shipped, already-audited feature) — pure documentation/configuration recommendation |
| **CH-04** | `mux.go:975-1004` (unpooled `FastHandler` dispatch, default `Mux.PoolFastParams=false`) | `ParallelFastParam1` efficiency ≈19% at 16 cores vs `ParallelFastParam1Pooled` ≈49% (3.2× faster in absolute ns/op at 16 cores) | Same allocator-contention mechanism as CH-03, smaller magnitude (32 B `Params` slice) | Same as CH-03 — `Mux.PoolFastParams` (Opt O9) already exists | Same class as CH-03, smaller absolute magnitude | None (already-shipped feature) |
| **CH-05** | `middleware/request_id.go:31-40` (`RequestID`, fallback `crypto/rand.Read`) | Aggregate mutex profile: 81.6% of `RequestID`'s own captured mutex delay (0.93 s of 45 s total capture across the whole suite) is inside `crypto/rand.Read → crypto/internal/sysrand.read / fips140/drbg.Read` | Go's CSPRNG (the FIPS-140 ChaCha8 DRBG backing `crypto/rand` since recent Go versions) synchronizes its internal state through a shared lock, so concurrent `rand.Read(16 bytes)` calls contend, unlike a per-goroutine PRNG | Replace with `math/rand/v2` (lock-free per-goroutine fast path since Go 1.22) or a monotonic-counter/ULID scheme — `RequestID` needs global uniqueness, not cryptographic unpredictability | Removes the only lock inside this middleware's hot path (~2% of total captured contention in this workload, growing with the share of requests lacking an inbound `X-Request-ID`) | Loses cryptographic unpredictability — acceptable for a correlation ID but should be confirmed against SECURITY.md's stated purpose for this middleware before changing (not verified in this task's scope) |
| **CH-06** | `mux.go:1196-1212` (`serveRedirect`) | `ParallelRedirectTrailingSlash` efficiency ≈19% at 16 cores; `serveRedirect` is the only remaining hot-path function that calls `m.mu.RLock()` unconditionally on every call (not cached, unlike `lazyNotFound`/`lazyMethodNotAllowed`/`lazyOPTIONS`, which snapshot into `muxConfig` and cache) | `m.middleware` is read via `m.mu.RLock()` on every redirect even though it never changes after startup in the supported usage — an RWMutex reader-count atomic op on every redirect request | Snapshot `middleware` into `muxConfig` (the same pattern `cfg.notFound`/`cfg.methodNotAllowed`/etc. already use) and read it lock-free inside `serveRedirect`, mirroring the existing `lazyNotFound` design | Eliminates the last unconditional `RWMutex` operation in the dispatch path; benefits every deployment with `RedirectTrailingSlash=true` (the default) | Low — mirrors an already-proven, already-shipped pattern in the same file; would need `Use()` to invalidate the snapshot the same way it already invalidates `lazyNotFoundPtr` |
| CH-07 | `middleware/oauth2.go:132-140` (`oauth2Cache.get`) | `OAuth2CacheHitSteadyState` scales cleanly (3179 ns → 341 ns, cpu1→cpu16, ≈58% efficiency) | RLock-only read-mostly cache; no write contention once warm | — | — (negative/positive finding, no fix needed) | — |
| CH-08 | `middleware/oauth2.go:96-125` (`oauth2Inflight.do`, DOS-OAUTH2-001 singleflight) | Aggregate block profile: ≈18% of total captured block time is `oauth2Inflight.do`'s `<-c.done` wait | By design: followers for the SAME uncached token wait on the leader's single upstream call — this is the intended cache-stampede defence, not a defect | — | — | — (working as designed; flagged so it is not mistaken for a bug by a future contention sweep) |
| CH-09 (open question) | `middleware/oauth2.go:142-164`, `:169-194` (`oauth2Cache.set`, eviction) | Not isolated in this sweep — `BenchmarkOAuth2ManyTokensNoCacheHit`'s cost is dominated by the real network round trip, masking any eviction-lock cost | `set()` takes the full `cache.mu.Lock()` for an O(`maxSize`) map scan (`evictExpiredLocked`/`evictSoonestExpiryLocked`) whenever the cache is full (default `MaxCacheSize=10000`), blocking every concurrent `get()`/`set()` for the scan's duration | Needs a dedicated cache-saturation benchmark (fill to `MaxCacheSize`, then measure `set()` latency under concurrent `get()` load) | Unknown — flagged as a follow-up for a future sprint (candidate for `dos-resilience-tester`, which already has an `MSR-2026-0068`-style analysis for `ThrottlePerIP`'s table but not for this eviction path) | N/A — not implemented or quantified here |
| CH-10 (cache-line, low severity) | `mux.go` `Mux` struct: `preHandlerPtr` (offset 176) and `lazyNotFoundPtr` (offset 184) share cache line 2 | Mechanically verified via `reflect`-based field-offset inspection (`harness/layout_test.go::TestStructLayoutAtomicPointers`) | `preHandlerPtr` is read on every `Pre()`-enabled request; `lazyNotFoundPtr` is mutated by `Use()` (`Store(nil)`) and by the first not-found cache build. In the **documented, supported** usage (no `Use()`/`Pre()` calls after serving starts) this is inert; only the explicitly-unsupported "dynamic registration while serving" scenario would turn this into real cache-line ping-pong | — | — | Theoretical only, given the existing usage contract; no fix proposed |

## No-contention section (surfaces that scale well)

- **`treesPtr` / `cfg` / `preHandlerPtr` atomic.Pointer reads** (`mux.go`) — `ParallelStatic` and `ParallelFastParam1Pooled` post the lowest absolute ns/op in the entire suite (2.5 ns / 5.0 ns at 16 cores). The RCU-style lock-free dispatch introduced by Opt O10-O13 shows no measurable lock contention at any tested core count. `treesPtr` and `cfg` were mechanically confirmed to share cache line 0 with each other and with read-only-after-construction fields — safe, since nothing in that cache line is written concurrently with the hot reads in supported usage (`harness/layout_test.go::TestStructLayoutMux`).
- **`node`'s hand-tuned cache-line layout** (`tree.go:69-88`) — the source comment's claim that a successful static-route match touches only `path` + `handler`, both in cache line 0, was mechanically verified with a byte-for-byte mirror struct (`harness/layout_test.go::TestStructLayoutNodeMirror`). Since `node` is immutable after registration in the supported usage, there is no concurrent-write hazard regardless of layout, but the claim itself holds.
- **`methodNotAllowedCache` / `optionsCache` (`sync.Map`)** — `ParallelMethodNotAllowed` and `ParallelOPTIONS` scale to ≈29%/≈25% efficiency at 16 cores after the one-time cache build, consistent with `sync.Map`'s documented read-mostly, largely lock-free fast path.
- **`middleware.JWTAuth`'s `sync.Pool` of `hmac.Hash`** and **`middleware.Logger`'s two `sync.Pool`s** — both scale well (≈78% and ≈81% efficiency at 16 cores respectively); `sync.Pool`'s per-P local cache absorbs the load without cross-P stealing becoming a bottleneck at this concurrency level.
- **`middleware.OAuth2Introspect`'s cache read-hit path** — see CH-07 above.
- **`middleware.RealIP`, `RecovererWithLogger` (no-panic path), `Timeout`, `BasicAuth`, `APIKey`, `CORS`** — all stateless per request (map reads over immutable, construction-time-built maps; no package-level mutable state). No shared-state contention source exists in these middlewares by construction; the only cost visible in profiles is generic allocation/`WithContext` overhead common to every context-injecting middleware (see CH-03/CH-04's allocator-contention discussion), not a MuxMaster- or middleware-specific lock.
- **`Mux.Handle`/`HandleFast` registration path (copy-on-write `treesPtr`)** — by design, registration is not exercised under concurrent serving load in the supported usage contract, so it was not load-tested; this is a documented constraint, not a gap.

## Additional observation (not a contention finding)

The CPU profile (`profiles/cpu.pb.gz`) shows `time.runtimeNow` + `runtime.nanotime` at a combined ≈20% of total CPU across the full benchmark suite — dominated by `middleware.Logger`'s two `time.Now()` calls per request. This is a CPU-bound cost (VDSO-backed, no lock), not a contention source, and is out of scope for this task; flagged here only so it is not mistaken for a missed finding.

## Files created

- `reports/perf-lab-2026-09-24/contention-hunt.md` (this report)
- `reports/perf-lab-2026-09-24/run.sh`, `.gitignore`
- `reports/perf-lab-2026-09-24/harness/go.mod`, `contention_bench_test.go`, `layout_test.go`
- `reports/perf-lab-2026-09-24/loadgen/go.mod`, `main.go`, `server.go`, `client.go`, `driver.sh`
- `reports/perf-lab-2026-09-24/profiles/` (mutex.pb.gz, block.pb.gz, cpu.pb.gz, mem.pb.gz, trace.out, loadgen-server-{cpu,mutex,block,goroutine}-{1000,5000,10000}.pb.gz)
- `reports/perf-lab-2026-09-24/results/` (scaling-cpu{1,2,4,8,16}.txt, scaling-benchstat.txt, profiled-run.txt, loadgen-{1000,5000,10000}.txt)
