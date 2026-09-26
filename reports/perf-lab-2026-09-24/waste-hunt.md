# Waste Hunt — MuxMaster under real example workloads

**rmp task:** #249, sprint 18 ("Performance and Efficiency Laboratory")
**Date:** 2026-09-24
**Agent:** extreme-code-profiler
**Scope:** root package and `middleware/`. Contention findings CH-01..CH-10 (commit `0df676a`) are out of scope.
**Product code changed:** none. All experiments ran on scratch copies of the module; `examples/` was never modified.

## Conclusions

1. **MuxMaster is a minority of the server's cost, and most of its share is middleware.** Across 13 examples driven over real sockets, `net/http` owns 54–91% of server CPU. MuxMaster middleware owns 0.3–16.8% of CPU and up to 47.9% of allocated bytes. The root package owns 0.05–5.1% of CPU and up to 16% of bytes. The router's own dispatch is lean: a static route takes 23 ns with 0 allocations.
2. **Thirteen waste findings (WH-01..WH-13) were proven**, each with profile, benchmark or A/B evidence. The four largest are:
   - **WH-01 `ThrottlePerIP`:** 3.93 µs → 0.11 µs per request.
   - **WH-02 `Logger`:** 5 → 0 allocations and 5 → 4 clock reads per request. `Logger` is the largest MuxMaster CPU owner in all 8 examples that use it.
   - **WH-03 `Compress` sniff buffer:** −55% time and 43 KiB → 53 B per chunked response.
   - **WH-04 per-request deep `Clone`:** `Mount` −76%, `CleanPath` −84%. Validated on the product code with the full test suite passing.
3. **Host caveat.** On this host the kernel clocksource is HPET. Every clock read is a real system call costing about 1.4 µs, where a TSC host pays about 20 ns. Clock-related savings (in WH-01 and WH-02) are therefore reported as **counts of clock reads**, which do not depend on the host. Their ns figures apply to this host only.
4. **Exercising the examples found serious pre-existing defects that are out of scope** (see the out-of-scope section at the end):
   - 5 of the 13 examples panic at start-up.
   - `reverse-proxy` **crashes the whole process** under load with `PoolRequestBundle=true`: the request bundle is used after it has been recycled.
   - `Compress` turns a 404 into a 200.
   - `Logger` and `Compress` hide `http.Flusher`.
5. **Suggested grouping:** 7 coherent efforts (§ "Fix grouping").

## Environment

| | |
|---|---|
| CPU | AMD Ryzen 9 5900HX, 16 logical CPUs, cpufreq governor `powersave` |
| OS | Linux 6.8.0-139-generic x86_64 |
| Clocksource | **`hpet`** (`available_clocksource`: `hpet acpi_pm`; TSC is not offered by the kernel). `clock_gettime` is a real syscall: `time.Now` = 2 syscalls, 2.83 µs; `time.Since` = 1 syscall, 1.42 µs |
| Go | go1.27.0 linux/amd64 (module declares `go 1.26`) |
| Git HEAD | `0df676a` (+ unrelated uncommitted edits to `CLAUDE.md`, `knowledge-model.md`) |
| Host load | load average < 1.3 before each phase; background services (mysqld, mongod, redis, java) each < 2% CPU |

## Reproduction

One command re-runs everything (about an hour on this host). Ports 8080, 6061, 9001 and 9002 must be free.

```bash
reports/perf-lab-2026-09-24/waste-hunt/run.sh            # all phases
reports/perf-lab-2026-09-24/waste-hunt/run.sh <phase>    # examples | attribute | strace | bench | escape | experiments
```

Requirements: Go ≥ 1.26, `python3`, `curl` and `patch`. `strace` and `benchstat` are optional; their steps are skipped if absent. Knobs: `CONNS` (32), `LOAD_S` (12), `PROF_S` (10), `STRACE_S` (5), `BENCH_COUNT` (10) and `EXAMPLES` (a subset). Binaries and example copies live in a `mktemp` directory that is deleted on exit.

```
reports/perf-lab-2026-09-24/waste-hunt/
├── run.sh                    one-command driver (phases above)
├── probe/zz_wastehunt_probe.go.in   injected into a TEMP copy of each example: private pprof listener on $WH_PPROF_ADDR
├── patches/*.diff            start-up / crash patches for 6 examples (applied to the temp copy only)
├── loadgen/                  scenario-driven load generator (separate process, own go.mod)
├── scenarios/*.json          one per example; every request carries its expected status
├── analyze/owner.py          nearest-owner attribution of pprof stacks
├── bench/                    micro-benchmarks: current code vs harness-local alternative + equivalence tests
├── experiments/              root-package A/B patches (E1, E1b, E2, E4) + run-experiments.sh
├── profiles/<example>/       cpu.pb.gz, allocs.pb.gz (10 s windows under load)
└── results/
    ├── examples/<example>/   load.txt, attribution.txt, cpu/alloc tops, muxmaster-focused tops, rss.txt
    ├── strace/<example>/     strace -f -c idle vs loaded, syscalls-per-request.txt
    ├── bench/                bench.txt (count=10), benchstat.txt, equivalence-tests.txt, syscalls-per-op.txt
    ├── experiments/          <ID>-tests.txt (full suite on patched copy), <ID>-{base,patched,benchstat}.txt
    ├── escape/               -gcflags=-m=2 escape lines for root and middleware
    ├── defects/              evidence of the out-of-scope defects listed at the end
    └── environment-<phase>.txt
```

## Method

1. **Examples as real servers.** Each example is copied to a temporary directory. A private `net/http/pprof` listener is added as an extra file, `go.mod` is pointed at this working tree, and the example is built and started unchanged apart from the patches listed below. `loadgen` is a separate OS process. It drives the example's scenario with 32 keep-alive connections: a 2 s warm-up, then 12 s under load. During that window a 10 s CPU profile and a 10 s `allocs` delta profile are captured. Every response status is checked against the scenario's expectation, and **all 13 runs ended `STATUS-OK`**.
2. **Nearest-owner attribution** (`analyze/owner.py`). Each sampled stack is charged to the first frame, walking from the leaf, that belongs to MuxMaster root, MuxMaster middleware, the example (`main.*`) or net/http server machinery. Library code, including net/http *helper* APIs such as `Header.Set`, `WithContext`, `Error` and `Redirect`, is charged to its caller. This is why `time.Now` called from `Logger` counts as `Logger` cost.
3. **Syscall accounting.** Each example runs under `strace -f -c` once idle and once under 5 s of load; the per-request figure is (loaded − idle) / requests. `ptrace_scope=1` forced launching the server under strace. ptrace slows the runtime, which inflates `clock_gettime` and `futex` counts, so only `read`/`write`/`sendfile` counts are used from this phase. Exact clock-read counts come from micro-benchmarks under strace: an N=2001 run minus an N=1 run, divided by 2000.
4. **Line attribution.** `go tool pprof -list` and `-peek` on the example profiles, `-gcflags=-m=2` escape analysis, and `-gcflags=-S` for one assembly question.
5. **Quantification.** `bench/` compares the **real** code with a harness-local rewrite of only the wasteful part: `count=10`, `benchstat`, with sub-benchmarks interleaved within each count. Every alternative has an **equivalence test** (`results/bench/equivalence-tests.txt`, all PASS), for example 200 000 random strings for sanitisation, 500 000 random durations, 50 000 random `X-Forwarded-For` values under two trust configurations, and byte-identical gzip bodies.
6. **Product A/B** (`experiments/`). Candidate root-package fixes are applied to a scratch copy of the module. The project's **full** suite runs on it (`go vet ./...`, `go test ./...`, all PASS), then base and patched benchmarks run alternately 10 times and are compared with `benchstat`.
7. **Noise floor.** Most benchmarks have a spread of ±0–2%; the maximum is ±6% (`Compress` chunked). In experiment E4, the unpatched replica benchmarks show no significant change between base and patched builds (p > 0.1), which bounds the cross-build noise.

## Examples exercised

Every example was exercised; none was excluded. Six ran with a harness patch because they cannot run as shipped. The patches only move registrations or disable pooling, and are applied to the temporary copy (`patches/*.diff`).

| Example | As shipped | Harness patch | Requests (12 s) | req/s | MuxMaster CPU share (middleware / root) | MuxMaster bytes share (middleware / root) |
|---|---|---|---|---|---|---|
| authn | panics at start-up | `HandleFast` moved above `Use` | 654 239 | 54 516 | 11.1% / 2.4% | 30.4% / 6.2% |
| cache | panics at start-up | same | 647 363 | 53 943 | 9.0% / 2.3% | 22.6% / 7.9% |
| graceful-shutdown | runs (`/slow` excluded from load: it is a 3 s timer, no CPU) | — | 686 475 | 57 203 | 16.8% / 0.3% | 47.9% / 0% |
| jwt | panics at start-up | same | 590 796 | 49 230 | 14.6% / 2.9% | 34.2% / 4.3% |
| max-performance | runs (its pprof Mount returns 404) | — | 629 299 | 52 438 | 6.5% / 0.8% | 22.1% / 5.3% |
| oauth2 | runs | — | 684 864 | 57 070 | 12.0% / 5.1% | 20.5% / 14.4% |
| rest-api | panics at start-up (3 distinct causes) | `HandleFast` moved; regex route `/{id:[0-9]+}/details` → `/:id/details`; static siblings moved above `/:id` | 541 891 | 45 155 | 15.8% / 4.3% | 39.5% / 10.7% |
| reverse-proxy | **process crash under load** | `PoolRequestBundle=false` | 257 354 | 21 443 | 1.5% / 2.1% | 3.3% / 16.1% |
| server-sent-events | runs (64 subscribers + publish load) | — | 292 116 | 24 341 | 0.3% / 0.05% | 16.9% / 0% |
| server-side-render | runs | — | 584 091 | 48 671 | 8.6% / 0.7% | 14.1% / 3.4% |
| static-site | panics at start-up | `HandleFast` moved above `Use` | 522 206 | 43 514 | 9.9% / 2.0% | 28.0% / 5.2% |
| upload-file | runs (64 KiB multipart) | — | 219 752 | 18 310 | 0.3% / 0.1% | 0.3% / 0% |
| versioning | runs | — | 681 336 | 56 775 | 1.4% / 0.7% | 22.3% / 0.3% |

Source: `results/examples/<example>/{load.txt,attribution.txt}`.

## Findings — ranked by gain

The gain column states the measured per-request gain, then the share of the real example workload that the wasteful code owns (from the profiles). Host-dependent ns figures are marked *(HPET)*.

| Rank | ID | Location | Measured cost | Proposed fix | Measured / estimated gain | Effort · risk |
|---|---|---|---|---|---|---|
| 1 | WH-01 | `middleware/throttle.go:325-328`, `:235-238`, `:246-253` | 3.93 µs, 376 B, 5 allocs, 2 clock syscalls per request | non-blocking token receive before any timer; recycle entries at refs==0 | 3.93 µs → 0.107 µs (−97%) *(HPET)*; 5 → 0 allocs; 2.2 → 0.07 clock syscalls. Code owns 5.8% CPU / 11.9% objects (oauth2), 2.5% / 5.3% (rest-api) | moderate · concurrency |
| 2 | WH-02 | `middleware/logger.go:49-52, 68, 86, 88, 90, 94` | 5 allocs / 104 B; 5 clock syscalls per request | one end-of-request clock read; append-sanitise into the pooled buffer; allocation-free duration | 7.35 → 5.67 µs (−23%) *(HPET)*; 5 → 0 allocs; 5.17 → 4.18 clock syscalls. `Logger` = 5.2–8.0% of server CPU in 8 examples; `sanitiseForLog` = 1.9–6.9% of objects | low · low |
| 3 | WH-03 | `middleware/compress.go:39, 43, 192` | 12 KiB chunked: 17.7 µs, 43.2 KiB, 16 allocs | pooled writer with a fixed 8 KiB sniff array | −55% time, 43.2 KiB → 53 B, 16 → 3 allocs; small body −19%, 752 → 32 B. Code owns 7.8% bytes (static-site) | moderate · lifetime |
| 4 | WH-04 | `mux.go:681-684`, `mux.go:742-745`, `group.go:205-208`, `middleware/clean_path.go:24`, `middleware/strip_slashes.go:21` | `Mount`: 1010 ns, 1928 B, 9 allocs; `CleanPath` (path changed): 820 ns, 1400 B, 7 allocs | `http.StripPrefix` copy strategy (shallow copy + new URL) | **E4, product A/B, full suite PASS:** `Mount` −76.3%, −56% B, 9 → 3 allocs; `CleanPath` −83.8%, −67% B, 7 → 2; real `ServeFiles` −5 allocs, −0.9 KiB/request | low · semantic decision |
| 5 | WH-05 | `middleware/set_header.go:37`; `response.go:19, 35, 46, 48`; `mux.go:836` | `SetHeader`: 65 ns + 1 alloc each; `Text`: 109 ns, 2 allocs | canonical key + `[]string` built once; `io.WriteString` | `SetHeader`×4 −55%, 4 → 0 allocs; `Text` −58%, 2 → 0; 1 alloc less per JSON/XML response. `SetHeader` = 5.7% of objects (static-site) | low · aliasing, spec wording |
| 6 | WH-06 | `middleware/jwt_auth.go:311, 316, 336, 93` | header decode 395 ns, 2 allocs; `[]byte` copy 36 ns / 128 B; `Sum(nil)` +1 alloc | memo of the last accepted header segment; no copy for HMAC input; `Sum` into a stack array | header step 395 → 4 ns; ≈ −440 ns / −4 allocs per request (≈ −9% of `JWTAuth`, estimated sum of parts); header parsing = 1.8% of server CPU (jwt) | moderate · security review |
| 7 | WH-07 | `middleware/logger.go:11-19` (`statusRecorder`) | no `sendfile`; +31 `read` +32 `write`, +32 KiB per 1 MiB response | recorder delegates `io.ReaderFrom` | 16 KiB −3.1%, 128 KiB −10.1%, 1 MiB −8.1%; 25–41 KiB → 9.3 KiB/op; **965 B: +5.8% (loss)** | low · size trade-off |
| 8 | WH-08 | `mux.go:412-414, 514-516`; `tree.go:118-142, 192` | registration O(N²): 5000 routes = 2.15 s, 3.41 GiB, 48.4 M allocs | path-copying copy-on-write + incremental `maxParams` | E1 (incremental `maxParams`, suite PASS): −5%. E1b bound (no clone): −95% time, −99.95% bytes | high · **spec change** |
| 9 | WH-09 | `mux.go:1251-1279`, `mux.go:836` | 405: 204 ns, 3 allocs; auto-OPTIONS: 219 ns, 4 allocs | `Allow` value table by method bitmask; prebuilt header slice | **E2, product A/B, suite PASS:** 405 −39.5%, OPTIONS −51.3%, all allocs → 0 | low · low |
| 10 | WH-10 | `mux.go:1226-1228` (`serveRedirect`) | +8 allocs per redirect (7-middleware chain rebuilt per request) | cache the wrapped redirect chain (design needed) | ≤ 8 allocs per redirect; `serveRedirect` owns 3.0% of objects (cache) | moderate · semantic |
| 11 | WH-11 | `middleware/real_ip.go:103` | `strings.Split` per request | right-to-left scan without allocation | 3 hops −21.5%, 2 → 1 allocs; 1 hop: 2 → 1 allocs, time gain not reproducible | low · security review |
| 12 | WH-12 | `middleware/api_key.go:71` | identity string boxed into `any` | fused context node (RequestID pattern) | −3.5%, 7 → 6 allocs, −32 B | low · timing review |
| 13 | WH-13 | `mux.go:969`, comment `mux.go:979-980` | comment claims the static path skips zeroing `ps`; the assembly zeroes it | correct the comment (or restructure) | none measurable (8 stores); documentation accuracy | trivial |

### WH-01 — `ThrottlePerIP`: a timer and a re-created table entry on every request

**Where.**
- `middleware/throttle.go:325-326` calls `time.NewTimer(timeout)` + `defer timer.Stop()` on every request, even when a token is immediately available.
- The subsequent `select` (`:327-328`) on the timer channel reads the clock again (`runtime.(*timer).maybeRunChan`).
- `decRefs` deletes the entry when `refs` drops to 0 (`:246-253`). A client whose requests do not overlap therefore makes `acquire` re-create it every time (`:235-238`): `make(chan struct{}, limit)` plus `limit` channel sends.

**Evidence.**
- Benchmark, limit 50 as in the rest-api example:
  - Current: 3.925 µs ± 0%, 376 B, 5 allocs.
  - Alternative: 106.7 ns ± 1%, 0 B, 0 allocs.
  - The many-clients variant gives 3.971 µs → 115.1 ns.
- Single-change attribution:
  - Timer fast path only: 738.8 ns (removes 3.19 µs *(HPET)*).
  - Entry reuse only: 3.269 µs (removes 0.66 µs and 128 B; host-independent).
- CPU profile of the benchmark: 70.8% in `runtime.nanotime` (`time.when` and `maybeRunChan`).
- Syscalls per request: 2.21 → 0.07 `clock_gettime` (`results/bench/syscalls-per-op.txt`).
- Escape analysis: `throttle.go:235` `&throttleEntry{...}` escapes to the heap.
- In the examples, `ThrottlePerIPCapped` + `throttleTable` own:
  - oauth2: 5.77% CPU, 9.36% bytes, 11.93% objects.
  - rest-api: 2.46% / 3.58% / 5.27%.
  - authn: 1.15% / 3.38% / 4.52%.

**Fix.**
- (a) `select { case <-ch: default: <create timer, wait> }`, so the timer exists only on the wait path. The timeout semantics are unchanged: the timer still starts when waiting starts.
- (b) An entry whose `refs` reaches 0 is removed from the shard map under the shard lock and put in a per-instance `sync.Pool`. At that point every token has been returned, because the deferred release sends the token before `decRefs`, so the channel is full and reusable.

The harness replica is in `bench/throttle_test.go`. `TestEquivThrottle` checks admission, the 503 on saturation after the timeout, and 1 000 sequential re-admissions.

**Risk and constraints.**
- The concurrency correctness of reuse must be proven with `-race` stress and the existing `throttle_*_test.go` suites (candidate reviewer: concurrency-security-auditor).
- The table bound (`maxTableSize`, TM-2026-013 / DOS-2026-0057) is preserved: `size` still counts table entries only.
- `ThrottlePerIP` has no specification section. `middleware-stdlib.md` §11 covers `ThrottleBacklog` only.

### WH-02 — `Logger`: redundant clock read and five allocations per request

**Where.** `middleware/logger.go` reads the clock three times per request: `time.Now()` at `:68`, `time.Now()` at `:86`, `time.Since` at `:94`.
- On this host that is 5 `clock_gettime` syscalls (strace: **5.17 per request**).
- `sanitiseForLog` (`:49-52`) calls `strconv.QuoteToASCII`: one heap buffer and one heap string per field, twice per request (method and path, `:88`, `:90`). The bytes are then copied into the pooled buffer.
- `Duration.String()` (`:94`) allocates 16 B.

**Evidence.**
- `Logger`'s closure is the largest MuxMaster CPU owner in every example that uses it: authn 7.63%, cache 7.33%, graceful-shutdown 7.97%, jwt 6.70%, max-performance 5.16%, rest-api 6.10%, server-side-render 7.23%, static-site 5.21% of total server CPU.
- Line profile (cache): `:68` 1.43 s, `:86` 1.55 s, `:94` 0.87 s of 64.14 s total — 6.0% of server CPU in the clock lines alone.
- `sanitiseForLog` owns 1.87–6.94% of **all** allocated objects in those examples.
- Escape analysis: `logger.go:50` builds the quoted buffer and the string on the heap.
- Benchmark:
  - Current: 7.348 µs, 104 B, 5 allocs.
  - Alternative: 5.672 µs, 0 B, 0 allocs (−22.8%).
  - Single clock read only: 5.959 µs.
  - Allocation fixes only: 7.073 µs (−275 ns host-independent, −104 B, −5 allocs).
  - Parts: `QuoteToASCII` path 158 ns / 2 allocs vs 14 ns / 0; `Duration.String` 25 ns / 1 alloc vs 15 ns / 0.

**Fix.**
- `end := time.Now()` serves both the timestamp (`end.AppendFormat`) and the duration (`end.Sub(start)`, monotonic like `Since`).
- `strconv.AppendQuoteToASCII` appends into the pooled buffer, then the quotes are dropped. A fast path appends bytes directly when every byte is printable ASCII other than `"` and `\` (for which `QuoteToASCII` is the identity).
- The duration is appended without allocating.

The output format is unchanged. Equivalence tests: 200 000 random strings (control bytes, invalid UTF-8, quotes, multi-byte) and 500 000 random durations produce identical bytes.

**Estimated gain.** Removing one of five clock reads plus all `Logger` allocations. From the line profile this is ≈ 1.5% of server CPU in the cache example on this host, plus 2–7% of allocated objects in every Logger example.

**Risk and constraints.** Low. The logged duration now ends before the logger formats its own line, so it no longer includes the logger's own overhead. Specification `middleware-stdlib.md` §2 is satisfied: it requires the fields, not the reading order. The floor with the stdlib API is 2 `time.Now` calls: there is no public monotonic-only clock read.

### WH-03 — `Compress`: sniff buffer grown by `append` from nil, fresh writer per request

**Where.**
- `middleware/compress.go:192` allocates a `*gzipResponseWriter` per gzip-accepting request (escapes).
- `:39` and `:43` grow `g.buf` with `append` from nil. A handler that writes in small chunks, as `html/template` and `fmt.Fprintf` do, reallocates and copies the buffer at every growth step up to 8 KiB.

**Evidence.**
- Benchmark, 12 KiB HTML written in 64-byte pieces: 17.69 µs ± 2%, 43.21 KiB, 16 allocs → 7.901 µs, 53 B, 3 allocs (−55%).
- 600 B JSON (passthrough below 1 KiB): 404.2 ns, 752 B, 4 allocs → 328.2 ns, 32 B, 2 allocs (−19%).
- Memory profile of the current path:
  - 74% of bytes come from `gzipResponseWriter.Write`'s `append`.
  - 15% come from `flate.NewWriter`: the gzip writer pool is emptied by the GC cycles that this garbage provokes, so compressors are rebuilt. This is an amplification effect.
- In the examples, `Compress` + `gzipResponseWriter` own 7.77% of bytes / 4.91% of objects (static-site) and 3.65% / 5.45% (rest-api).

**Fix.** Recycle a writer that embeds a fixed `[8192]byte` sniff array through a `sync.Pool`, and reset it (all fields cleared) after `close`. Equivalence: identical compressed bytes and headers for both workloads, and 50 alternating sequential reuses with no state leak.

**Risk and constraints.**
- Pooling relies on the `http.ResponseWriter` contract that `w` is not used after `ServeHTTP` returns. A handler that illegally writes from a leaked goroutine would then corrupt another response rather than a dead one (candidate reviewers: middleware-security-reviewer and concurrency-security-auditor).
- DOS-2026-0007 is unchanged: at most 8 KiB per in-flight request.
- Specification §8 is unaffected.

### WH-04 — Deep `r.Clone` where only the URL changes (Mount, ServeFiles, Group.ServeFiles, CleanPath, StripSlashes)

**Where.**
- `mux.go:681-684` (Mount), `mux.go:742-745` (ServeFiles) and `group.go:205-208` (Group.ServeFiles) run `r2 := r.Clone(ctx)`, then `r2.URL = new(url.URL); *r2.URL = *r.URL`.
  - `Clone` already deep-copied the URL; the second copy discards it. This is redundant: 1 alloc and 144 B per request (escape: `mux.go:682`, `:743`, `group.go:206`).
  - `Clone` also deep-copies the header map and every value slice, plus `Trailer`, `Form` and `TransferEncoding`, although only the URL is modified.
- `middleware/clean_path.go:24` and `middleware/strip_slashes.go:21` do the same whenever the path changes. In static-site this is every `/docs/vN/` request, whose trailing slash is cleaned.

**Evidence.**
- Experiment E4 (product patched; `go vet` + full `go test ./...` PASS):
  - `Mount`: 1010 ns, 1928 B, 9 allocs → 239.8 ns, 848 B, 3 allocs (−76.3%).
  - `CleanPath` with a changed path: 820 ns, 1400 B, 7 allocs → 132.7 ns, 464 B, 2 allocs (−83.8%).
  - Real `ServeFiles` over TCP: 5 fewer allocations and 0.9 KiB less per request (−10% of client+server bytes); time differences are within network noise.
- Harness: removing only the redundant URL copy gives −48 ns, −1 alloc, −144 B.
- Examples: the Mount/ServeFiles closures own 1.9–2.4% of bytes (max-performance, server-side-render, static-site); `CleanPath` owns 3.0% of bytes in static-site.

**Fix.** The `net/http.StripPrefix` strategy, i.e. the E4 patch (`experiments/E4-mount-servefiles-shallow-copy.diff`): `r2 := new(http.Request); *r2 = *r; r2.URL = new(url.URL); *r2.URL = *r.URL`.

**Risk and constraints.**
- The mounted handler would share `r.Header`, `r.Form` and `r.Trailer` with the caller's request. Header mutations made inside a mount would become visible to outer middleware after it returns. This is the same contract as `http.StripPrefix`.
- Specification: `groups.md` §23 and `static-files.md` §7 only require the path rewrite. `middleware-stdlib.md` §52 and §56 even say `StripSlashes`/`CleanPath` modify `r.URL.Path` in place.
- **Decision needed from the user**, with a security review of cross-mount header visibility.

### WH-05 — Constant header values rebuilt on every request

**Where.**
- `middleware/set_header.go:37`: `w.Header().Set(key, value)` canonicalises a key fixed at construction and allocates a new `[]string` (escape: `set_header.go:37`).
- `response.go:19`, `:35`, `:46`: `JSON`/`XML`/`Text` do the same for a constant `Content-Type`.
- `response.go:48`: `Text` copies its argument with `[]byte(s)` (escapes).
- `mux.go:836`: the auto-OPTIONS handler rebuilds the `Allow` slice.

**Evidence.**
- Four stacked `SetHeader` (static-site's global chain): 260.3 ns, 64 B, 4 allocs → 118.2 ns, 0 B, 0 allocs (−55%).
- `Header.Set` with a constant: 39.7 ns / 1 alloc vs 9.8 ns / 0.
- `Text`: 109.4 ns, 96 B, 2 allocs → 46.3 ns, 0, 0 (−58%).
- `JSON` helper: 3 allocs, of which the `Content-Type` slice is one.
- Examples: `SetHeader` is the largest middleware allocator in static-site (5.73% of all objects). The helper's `Header.Set` accounted for ≈ 1 object per JSON response (oauth2: 1.26 M objects in 10 s).

**Fix.** Canonicalise and build the value slice once — at construction for `SetHeader`, at package level for the helpers — and assign directly. `Text` writes with `io.WriteString`. `NoCache`, `CORS` (Opt L3) and the 405 handler (Opt M1) already use this pattern. Equivalence tests pass (`TestEquivSetHeader`, `TestEquivText`).

**Risk and constraints.**
- Shared backing array: a downstream handler that writes `h[k][0] = …` in place would change the value for later responses. `Set`, `Add` and `Del` are safe, because they replace the slice or append to a slice of capacity 1.
- `middleware-stdlib.md` §59 literally prescribes `w.Header().Set(key, value)`, so a wording amendment for observable equivalence is needed.
- `response-helpers.md` §6, §12 and §18 require only the header value.

### WH-06 — `JWTAuth`: identical JOSE header re-decoded on every request; avoidable copies

**Where.**
- `middleware/jwt_auth.go:311` and `:316` base64-decode and `json.Unmarshal` the header segment. That segment is byte-identical for every token minted by one issuer.
- `:336`: `[]byte(signingInput)` copies the signing input (escapes).
- `:93`: `h.Sum(nil)` allocates the MAC.

**Evidence.**
- jwt example line profile: `:311` 250 ms and `:316` 950 ms of 67.9 s (1.8% of server CPU). `:336` accounted for 39.5 MB of copies.
- `parseAndValidateJWT` + HMAC own 6.29% of CPU, 18.0% of bytes and 19.1% of objects there.
- Benchmark:
  - Header step: 395.3 ns, 80 B, 2 allocs → memo hit 4.0 ns, 0, 0.
  - Signing-input copy: 35.8 ns / 128 B / 1 alloc.
  - `Sum(nil)` vs `Sum` into a stack array: +12 ns, +32 B, +1 alloc.
  - Whole middleware: 4.862 µs, 946 B, 10 allocs. The ≈ −440 ns / −4 allocs total is the sum of parts; it was not measured end to end.

**Fix.**
- A one-entry memo of the last accepted header segment. It is compared by exact string equality and stored only after the alg allow-list and `crit` checks pass; any other header takes the full path.
- HMAC over the string without a copy, using a read-only `unsafe.Slice(unsafe.StringData(...))`; `unsafe` is already used in `request_id.go`.
- `Sum` into a `[64]byte` stack buffer.

**Risk and constraints.** Security-sensitive: this is the algorithm-confusion defence (RFC 8725 §3.1). A memo hit versus a miss reveals only whether the header equals the previously accepted one; headers are not secret. Candidate reviewers: timing-and-sidechannel-analyst and middleware-security-reviewer.

### WH-07 — `Logger` hides `io.ReaderFrom`: no `sendfile` behind it

**Where.** `middleware/logger.go:11-19`. `statusRecorder` embeds `http.ResponseWriter`, so it exposes only `Header`/`Write`/`WriteHeader`. `http.ServeContent`'s `io.CopyN` then cannot reach net/http's `ReadFrom` (`sendfile(2)`). It falls back to a per-request heap buffer of up to 32 KiB and a `read`+`write` pair per 32 KiB.

**Evidence (1 MiB file).**
- Syscalls per response:
  - Without Logger: 1 `sendfile`, about 2 `write`.
  - With Logger: 0 `sendfile`, +31 `read`, +32 `write`.
  - Logger with delegated `ReadFrom`: identical to no Logger (`results/bench/syscalls-per-op.txt`).
- Time per request (client and server in-process over loopback, `Logger` current → delegating):

| File size | Current | Delegating | Change | Bytes per op |
|---|---|---|---|---|
| 965 B | 133.8 µs | 141.5 µs | **+5.8% (loss)** | — |
| 16 KiB | 152.3 µs | 147.6 µs | −3.1% | 25.0 KiB → 9.3 KiB |
| 128 KiB | 208.0 µs | 187.0 µs | −10.1% | 41.5 KiB → 9.3 KiB |
| 1 MiB | 659.4 µs | 605.8 µs | −8.1% | 41.3 KiB → 9.3 KiB |

- At 965 B the `sendfile` path adds one syscall and a sniff step.
- The static-site strace shows `sendfile` = 0 for all served files, although its assets are all < 1 KiB.

**Fix.** Give the recorder a `ReadFrom` that delegates to the underlying `io.ReaderFrom`; status capture is unchanged. Equivalence test `TestEquivFileServeLoggerRF` checks identical bodies and log fields.

**Risk.** A net loss for very small bodies. The crossover lies between 1 KiB and 16 KiB in this setup.

### WH-08 — Registration is O(N²): whole-tree deep clone and whole-tree `maxParams` walk per route

**Where.**
- Every `Handle`/`HandleFast` call runs `cloneTree` on the method tree: `mux.go:412-414`, `mux.go:514-516`, `tree.go:118-142`.
- `addRouteInternal` defers `calcPathMaxParams` over the whole tree (`tree.go:192`).

**Evidence.**
- Benchmark: 100 routes 874 µs; 1 000 routes 88.4 ms (88 µs/route); 5 000 routes 2.147 s (429 µs/route), 3.41 GiB, 48.35 M allocs.
- CPU profile at N=1 000: `cloneTree` = 93% of `Handle` and 99.6% of bytes, plus the GC work its garbage triggers. `calcPathMaxParams` = 2.1% of samples.
- E1 (incremental `maxParams`, suite PASS): −4.8% to −5.3%.
- **E1b (bound; no clone at all; not a proposal):** −90.7% (N=100) to −95.5% (N=5000) time; −97.3% to −99.95% bytes.

**Fix.** Path copying: copy only the nodes on the insertion path and share untouched subtrees. This keeps atomic publication and panic rollback while cutting registration to O(depth) copies per route. Add incremental `maxParams` (E1).

**Risk and constraints.**
- **`specification/performance.md` §36 mandates that the tree be "deep-cloned"**, so an amendment is required first.
- The MM-2026-0033 rollback guarantee must be kept. **The current test suite does not detect its removal** (E1b passed every test), so a regression test must come first.
- This is a start-up cost only, but it is 2.1 s and 3.4 GiB of garbage at 5 000 routes.

### WH-09 — 405 / auto-OPTIONS: `Allow` value rebuilt per request

**Where.**
- `mux.go:1251-1279`: `allowed()` builds the string with a `strings.Builder` on every request. The memory profile shows all 3 allocations of a 405 inside `Builder.WriteString`.
- `mux.go:836` adds a `Header().Set` per OPTIONS request.

**Evidence.**
- E2 (product patched, full suite PASS):
  - 405: 203.9 ns, 56 B, 3 allocs → 123.4 ns, 0, 0 (−39.5%).
  - Auto-OPTIONS: 218.7 ns, 72 B, 4 allocs → 106.6 ns, 0, 0 (−51.3%).
- Examples: 0.36% of objects in rest-api, where 2 of its 23 scenario requests are a 405 or an OPTIONS.

**Fix.** A table of `Allow` values indexed by a method bitmask, built at package init (256 valid entries), plus a prebuilt `Allow` slice per cached OPTIONS handler (`experiments/E2-allow-bitmask.diff`). The output is identical, in the same method order.

**Risk.** Low.

### WH-10 — `serveRedirect` re-instantiates the whole `Use()` chain per redirect

**Where.** `mux.go:1226-1228` calls `wrapMiddleware(...)` for every redirect (escape: `mux.go:1226` "func literal escapes").

**Evidence.**
- With the rest-api 7-middleware chain, the memory profile attributes 8 of the redirect's 26 allocations to re-instantiation: one closure per middleware constructor plus the redirect closure.
- Cache example, where 1 of its 6 scenario requests is a trailing-slash redirect: `serveRedirect` owns 0.33% of CPU, 1.32% of bytes and 3.04% of objects.

**Fix (design needed).** Cache one wrapped redirect handler, like `lazyNotFound`, and pass the per-request target to it. Options:
- (a) Recompute the target inside the cached handler from `r`. This changes semantics if a `Use()` middleware rewrites the path.
- (b) Carry the target in a small per-request value: 1–2 allocations instead of 8.

**Risk.** Semantic, on a low-traffic path.

### WH-11 — `RealIP`: `strings.Split` of the whole `X-Forwarded-For`

**Where.** `middleware/real_ip.go:103`.

**Evidence.**
- 3 hops: 255.7 ns, 64 B, 2 allocs → 200.7 ns, 16 B, 1 alloc (−21.5%). An earlier identical run in this session gave 261.2 → 210.8 ns.
- 1 hop: 32 → 16 B and 2 → 1 allocs. The time change is **not established**: 191.9 → 167.7 ns in the retained run, 175.2 → 175.9 ns in the earlier one.
- `RealIP` + `selectXFFRightmost` own 0.77–1.14% of CPU and 2.9–4.6% of objects in rest-api, static-site and oauth2.

**Fix.** Scan from the right with `strings.LastIndexByte`, keeping the 30-hop bound. It is identical to the current code on 50 000 random headers × 2 trust configurations (`TestEquivRealIP`).

**Risk.** A security-sensitive parser (MSR-2026-0065). The differential test must ship with the fix.

### WH-12 — `APIKey`: identity string boxed into `any`

**Where.** `middleware/api_key.go:71`: `context.WithValue(..., apiKeyCtxKey{}, id)`. Escape analysis reports "id escapes to heap".

**Evidence.** 495.7 ns, 448 B, 7 allocs → 478.4 ns, 416 B, 6 allocs (−3.5%) with a fused context node, the same technique `RequestID` already uses.

**Risk.** The hit path gets about 17 ns faster, which changes the TSC-2026-0008 hit/miss balance (`api_key.go:61-68`); needs a timing-and-sidechannel-analyst sign-off.

### WH-13 — Inaccurate hot-path comment in `dispatch`

`mux.go:979-980` states that the static-tree path "skips zeroing the 128B `ps` stack slot". The generated assembly zeroes it unconditionally at `mux.go:969` with eight `MOVUPS X15` stores, before the `getValueStatic` call at `:981`. The cost is not separable from noise (`StaticDispatch` 23.10 ns ± 0%). The finding is about documentation accuracy (working agreement rule 2), not speed.

## Rejected hypotheses and non-findings

| Hypothesis | Verdict | Evidence |
|---|---|---|
| `sha256.Sum256([]byte(x))` copies in APIKey (`api_key.go:55`), OAuth2 (`oauth2.go:386`), BasicAuth (`basic_auth.go:36`) | rejected | escape analysis lists no escape at these lines: non-escaping conversion, no heap copy |
| `sync.Map` lookups in `lazyMethodNotAllowed`/`lazyOPTIONS` box their string key | rejected | memory profile: 0 allocations from `sync.Map`; every 405 allocation is in `strings.Builder` |
| APIKey `Set` + `Del` of `WWW-Authenticate` on the hit path is vacuous | not waste | it is mandated timing equalisation, TSC-2026-0008 (`api_key.go:61-70`) |
| `Timeout` allocations (`Timeout.func1.1`: up to 7.97% CPU / 16.5% bytes in graceful-shutdown) | not waste | spec §22 mandates `context.WithTimeout`; the request copy is inherent to stdlib middleware |
| `RequestID` allocations | not waste | already minimal: fused node + `r.WithContext` (rmp #245) |
| `dispatchParams1` bundle (384 B) | known design | CH-03; `PoolRequestBundle` exists |
| `Logger` does one `write(2)` per request | by design | strace: +0.73–1.0 `write` per request in Logger examples vs exactly 1.000 in versioning. The writer is caller-supplied (spec §9); buffering is the operator's choice |
| BasicAuth SHA-256 per request | not waste | required for the constant-time comparison (spec §35) |
| `fmt` on hot paths | none found | grep: only `introspection.go` (handler names) and the OAuth2 error path |
| `defer` overhead | not pursued | every request-path `defer` guards a release or recovery; no `defer` frame appears in any profile's top functions |
| `Compress` eager `pool.Get()` at construction | negligible | one-off; the flate compressor is created lazily on first write |
| `JSON`/`XML` marshalling cost | not waste | payload work; only the `Content-Type` slice is waste (WH-05) |
| `CORS` `[]string{origin}` per request | not waste | the value differs per request |
| Static-route dispatch | no waste | 23 ns, 0 allocs; see WH-13 for the comment only |
| OAuth2 re-introspects inactive tokens every time (they are never cached) | not classified as waste | a negative-caching policy is a security/product decision |
| `JWTAuth` `time.Now` (1.19 s in the jwt example on this HPET host) | not waste | required for the `exp`/`nbf` checks |

## Example-code waste (not MuxMaster, but shipped documentation)

- **`fastTimer`** (rest-api `main.go:435-441`, max-performance `:270-278`) calls `time.Now` + `time.Since` and boxes the `slog.Debug` arguments on every fast request.
  - In rest-api the package-level default logger drops Debug. That costs 0.16 s of CPU (0.23%) and 65 537 flat objects in 10 s, for output that is never written.
- **`etagFor`** (cache) runs `fmt.Sprintf` + 2 × `fmt.Sprint` per `GET /articles/:id`: 3.67% of all allocated objects in the cache example.
- **Linear scans with `fmt.Sprint`**: `findBookByID`/`findAuthorByID` (rest-api) and `Store.get`/`toggleDone` (cache) format every ID for each lookup instead of `strconv.Atoi` + a map lookup. That is 0.23% of CPU with 3 books, and it grows as O(n).
- Every Logger example passes an unbuffered `os.Stdout`: one `write(2)` per request (see the rejected table).

## Fix grouping — fewest coherent efforts

| Effort | Findings | Files | Why grouped | Suggested order |
|---|---|---|---|---|
| A. Logger | WH-02, WH-07 | `middleware/logger.go` | same function and recorder type | 1 |
| B. ThrottlePerIP | WH-01 | `middleware/throttle.go` | one mechanism; needs its own race/DoS validation | 2 |
| C. Request copy strategy | WH-04 | `mux.go`, `group.go`, `middleware/clean_path.go`, `middleware/strip_slashes.go` | one mechanism; the E4 patch is ready; one semantic decision covers all five sites | 3 |
| D. Compute once, not per request | WH-05, WH-09, WH-10, WH-13 | `middleware/set_header.go`, `response.go`, `mux.go` | same principle (constant or derivable values built at registration or construction); the `mux.go` edits overlap | 4 |
| E. Compress | WH-03 | `middleware/compress.go` | pooling and lifetime review specific to this writer | 5 |
| F. Security-sensitive middleware trims | WH-06, WH-11, WH-12 | `middleware/jwt_auth.go`, `middleware/real_ip.go`, `middleware/api_key.go` | all need middleware-security-reviewer and timing-and-sidechannel-analyst; batch one review | 6 |
| G. Registration copy-on-write | WH-08 | `tree.go`, `mux.go`, `specification/performance.md` §36 | spec amendment and a rollback regression test first | 7 |

## Out-of-scope defects and observations

I did not act on any of these. Evidence is under `results/defects/` or `results/experiments/`.

1. **Five examples panic at start-up:** authn, cache, jwt, rest-api and static-site call `HandleFast` after `Use` (FPE-2026-010 guard).
2. **rest-api has two more start-up panics:**
   - the regex route `/books/{id:[0-9]+}/details` conflicts with `/books/:id`;
   - registering static `/books/featured` after `/books/:id` panics, while the reverse order succeeds.
3. **reverse-proxy crashes the process under load with `PoolRequestBundle=true`**, a remote denial of service (`results/defects/reverse-proxy-pool-crash.txt`). `net/http.Transport`'s dial goroutine calls `Value` on the request context after the handler returned, i.e. on a recycled, zeroed bundle, causing a nil dereference in `requestCtx1.Value` (`params.go:133`). `examples/README.md` lists this example as pool-safe.
4. **`Compress` lets a later `WriteHeader` overwrite the first**: the static-site custom 404 page is served as 200 when the client accepts gzip (`results/defects/compress-writeheader-overwrite.txt`).
5. **`Logger` and `Compress` hide `http.Flusher`/`io.ReaderFrom` and offer no `Unwrap()`**, so `http.ResponseController.Flush` fails with "feature not supported" behind them (`results/defects/wrappers-hide-flusher.txt`).
6. **max-performance:** the documented `curl …/debug/pprof/profile` returns 404, because `Mount` strips the prefix before `DefaultServeMux` sees the path.
7. **static-site:** `ServeFiles("/assets/*filepath", http.Dir("./static"))` serves `/assets/style.css` as 404; the file is reachable only at `/assets/assets/style.css`.
8. **Test gap:** the full suite passes with `Handle`'s copy-on-write clone removed (E1b), so MM-2026-0033 is not guarded on that path.
9. **Specification drift:**
   - `middleware-stdlib.md` §5 (RealIP: first XFF entry, no trust check) differs from the rightmost-untrusted implementation;
   - §52 and §56 say "in place" while the code clones;
   - `out-of-scope.md:98` lists JWT/OAuth2 middleware, which now ship;
   - `SECURITY.md` MM-2026-0033 says the tree may be inconsistent after a registration panic, while `performance.md` §37 specifies rollback.
10. The versioning example's comment says its `Pre` middleware rewrites "the bundle copy". `Pre` runs before dispatch, so it mutates the original request.

## Limits of the method

- **Clocksource.** HPET makes every clock read a roughly 1.4 µs syscall on this host. All clock-related ns and percentage gains (WH-01, WH-02, parts of `Timeout` and net/http) are inflated relative to TSC hosts, where they would be much smaller. The clock-read **counts** and the allocation savings are host-independent. No TSC host was available.
- **Single host, loopback, 32 connections, client and server co-resident.** These runs attribute per-request work; they are not throughput benchmarks. The req/s figures above are context, not results.
- **Sampling.** pprof samples CPU at 100 Hz and allocations every 512 KiB, so shares below about 0.1% are within sampling noise.
- **strace.** Its overhead inflates `clock_gettime`/`futex` counts in the example-level runs, so only I/O syscall counts were used from them.
- **Middleware replicas.** Middleware alternatives are harness replicas, not product patches (only WH-04, WH-08 and WH-09 were A/B-tested in product code). The equivalence tests cover the behaviours listed, not every edge a product change would need; each fix still needs the project's full suite and the reviews named.
- **Estimates.** The example-level gains for WH-02 and WH-06 are estimates derived from line profiles plus benchmark deltas; they were not re-measured with patched examples.
- **Scenario representativeness.** Scenarios weight every route equally, a coverage choice rather than a production traffic mix. Real mixes would shift the shares.
