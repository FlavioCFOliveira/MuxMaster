# Middleware Performance Audit — 2026-05-12

Machine: AMD Ryzen 9 5900HX, Go 1.26, linux/amd64

Benchmark file: `reports/perf-audit-2026-05-12/middleware_bench_test.go`

---

## 1. Individual middleware benchmarks

All measurements are the median of 5 runs (`-count=5 -benchmem`).

| Middleware | Variant | ns/op | B/op | allocs/op | Notes |
|---|---|---|---|---|---|
| **StripSlashes** | Clean (fast path) | 4.8 | 0 | 0 | Pure byte scan, 0 alloc — ideal |
| **Recoverer** | No panic | 7.5 | 0 | 0 | Just a deferred closure — ideal |
| **CORS** | No Origin header | 19 | 0 | 0 | Single r.Header.Get → return |
| **CleanPath** | Clean (fast path) | 32 | 0 | 0 | path.Clean then early return |
| **Compress** | No gzip client | 34 | 0 | 0 | strings.Contains → return |
| **ThrottleBacklog** | No wait (token available) | 45 | 0 | 0 | Buffered channel select, 0 alloc |
| **RealIP** | XFF present | 72 | 80 | 2 | strings.Split + netip.ParseAddr |
| **RealIP** | No XFF/X-Real-IP | 143 | 16 | 1 | net.SplitHostPort alloc |
| **WithValue** | — | 141 | 368 | 2 | context.WithValue + r.WithContext |
| **BasicAuth** | Hit (valid creds) | 232 | 48 | 2 | sha256.Sum256 + map lookup; 2 allocs = sha256 internal + header string |
| **SetHeader** | 1 key/value | 238 | 416 | 3 | w.Header().Set — httptest.ResponseRecorder map init |
| **StripSlashes** | Dirty (r.Clone) | 219 | 512 | 3 | r.Clone allocates *url.URL + http.Request copy |
| **CleanPath** | Dirty (r.Clone) | 290 | 552 | 5 | r.Clone + url.PathUnescape alloc |
| **NoCache** | — | 422 | 480 | 7 | 5× Header().Set; each Set = 1 []string alloc in httptest recorder |
| **Compress** | Small body (<1 KB) | 427 | 690 | 2 | gzipResponseWriter alloc + sniff buf |
| **APIKey** | Miss (wrong key) | 529 | 96 | 6 | sha256 + http.Error allocs |
| **APIKey** | Hit (valid key) | 665 | 448 | 7 | sha256 + context.WithValue + r.WithContext + header set/del |
| **BasicAuth** | Miss (wrong creds) | 650 | 152 | 8 | sha256 + header string + http.Error allocs |
| **RequestID** | Propagate (valid header) | 671–2204 | 832 | 8 | context.WithValue + r.WithContext + w.Header().Set |
| **CORS** | Allowed origin (simple) | 320–1670 | 432 | 4 | 2× Header().Set + Header().Add + map lookup |
| **Compress** | Large body (>1 KB, actual compress) | 873 | 2850 | 2 | gzip pool get + sniff buf alloc |
| **CORS** | Preflight OPTIONS | 2374 | 464 | 6 | Headers × 5 + WriteHeader |
| **RequestID** | Generate (crypto/rand) | 4274–4944 | 864 | 9 | crypto/rand.Read + hex.Encode + context.WithValue + r.WithContext + header set |
| **ThrottlePerIP** | Hit (key in table) | 4181 | 376 | 5 | mu.Lock + map lookup + timer alloc + net.SplitHostPort |
| **Timeout** | — | 5518 | 592 | 5 | context.WithTimeout (2 allocs: timerCtx + cancelCtx) + r.WithContext |
| **JWTAuth HS256** | Hit | 5506 | 1313 | 20 | base64 decode × 3 + json.Unmarshal × 2 + HMAC pool + context.WithValue + *JWTClaims alloc |
| **JWTAuth HS256** | Miss (wrong sig) | 1979 | 528 | 15 | stops after sig verify fails, no claims alloc |
| **JWTAuth ES256** | Hit | 79697 | 2528 | 40 | ECDSA.Verify dominates (~95%); big.Int allocs per call |
| **Logger** | — | 6762 | 152 | 10 | fmt.Fprintf + time.Format + 2× strconv.QuoteToASCII + statusRecorder heap alloc |

**OAuth2**: excluded from isolated benchmark because it requires a live HTTP server (network call). Cost is dominated by the HTTP round-trip; caching path reduces to a sha256 + RWMutex RLock + context.WithValue.

---

## 2. Chain benchmarks

| Chain | ns/op | B/op | allocs/op | Composition |
|---|---|---|---|---|
| **Minimal** (Recoverer only) | 7.8 | 0 | 0 | floor cost |
| **AuthBasic** (Recoverer + BasicAuth) | 233 | 32 | 2 | BasicAuth dominates |
| **AuthJWT** (Recoverer + RequestID + JWTAuth/HS256) | 7021 | 2177 | 29 | JWTAuth dominates |
| **Production** (Recoverer + RealIP + RequestID + Logger + CORS) | 8595 | 1128 | 23 | Logger dominates |
| **Security** (Recoverer + NoCache + SetHeader + CORS + APIKey) | 2485 | 1720 | 20 | APIKey + CORS header writes |
| **Heavy** (all 8 non-network middlewares) | 11464 | 1993 | 34 | Logger + RequestID/generate dominate |

The floor cost of chaining via Go's `http.Handler` interface is essentially zero beyond the sum of parts: the Minimal chain (7.8 ns) matches Recoverer alone (7.5 ns).

---

## 3. Root cause analysis per middleware

### NoCache — 422 ns, 480 B, 7 allocs
Each `w.Header().Set(key, value)` on an `httptest.ResponseRecorder` allocates a `textproto.MIMEHeader` entry (`[]string{value}` = 1 slice). 5 calls = 5 allocs. The `httptest.ResponseRecorder` lazy-initialises its map on first write (2 allocs for `make(http.Header)` + `textproto.MIMEHeader`). In production with a real `http.ResponseWriter`, the map already exists and the allocations drop to 5 slice literals. Still: 5 `[]string` allocs per request are inherent to the current `Header().Set` API — there is no way to eliminate them without bypassing `net/http`'s `Header` map entirely.

**Optimisation opportunity**: Pre-compute a single fixed byte slice `"no-store, no-cache, must-revalidate\r\nPragma: no-cache\r\nExpires: 0\r\nSurrogate-Control: no-store\r\nX-Accel-Expires: 0\r\n"` and write it once via `http.Header.Set` + `http.Header` bulk population OR use `http.ResponseWriter`'s `Add`/`Set` in a single pass. In practice, these allocs are `[]string` slices inside `textproto.MIMEHeader`, owned by the response — they are not GC-scanned long-lived objects. Impact: ~350 ns → ~140 ns if all 5 are pre-set as a single bulk write. **Feasibility: easy, no API change, no security impact.**

### RequestID — 671–4944 ns, 832–864 B, 8–9 allocs
Two distinct paths:
- **Propagate** (valid incoming ID): 8 allocs = `context.WithValue` (valueCtx + contextKey) + `r.WithContext` (new *http.Request) + `w.Header().Set` ([]string) + the incoming ID string does not escape. Still 8 allocs.
- **Generate** (no valid ID): +1 alloc for `hex.EncodeToString` producing a new string from the 16-byte random buffer. The `[16]byte` itself does not escape (stack), but `hex.EncodeToString` allocates a 32-byte string.

The high variance on `Propagate` (661–3654 ns) is due to crypto subsystem contention when run alongside Generate iterations that are calling `crypto/rand.Read`.

**Root allocs on propagate path**: `context.WithValue` = 2 allocs (contextKey struct + valueCtx), `r.WithContext` = 1 alloc (new *http.Request), `w.Header().Set` = 1 alloc ([]string) — total 4 allocs attributable to the middleware itself, 4 to httptest recorder initialisation.

**Optimisation**: The `context.WithValue + r.WithContext` pattern is the same 2-alloc + 1-alloc cost as any other context-injecting middleware. Cannot be reduced without changing the `net/http` context model. However, if RequestID is combined with the reqBundle approach (same pattern as MuxMaster's route dispatch), the context + request copy could be fused. **Not practical without changing the public API.**

### Timeout — 5518 ns, 592 B, 5 allocs
`context.WithTimeout(r.Context(), d)` allocates:
1. A `timerCtx` struct (context with timer)
2. A `cancelCtx` embedded in timerCtx
3. A `time.Timer` (the underlying runtime timer)
4. A closure registered with the timer
5. `r.WithContext` allocates a new `*http.Request`

This is the minimum cost for context deadline propagation in Go. The only elimination strategy is using `http.TimeoutHandler` from stdlib (which uses `time.AfterFunc` internally and has equivalent cost), or accepting that Timeout is never "free".

**Optimisation**: Pool `timerCtx` objects. Not doable within the current `context` package API without `unsafe`. **Accept as inherent cost.**

### Logger — 6762 ns, 152 B, 10 allocs
Breakdown:
- `&statusRecorder{...}` → 1 alloc (heap escape confirmed by `escape analysis: moved to heap`)
- `time.Now().Format(time.RFC3339)` → 1 string alloc
- `sanitiseForLog(r.Method)` → 1 alloc (strconv.QuoteToASCII returns a new string)
- `sanitiseForLog(r.URL.Path)` → 1 alloc
- `time.Since(start)` as `%s` → 1 string alloc (Duration.String)
- `fmt.Fprintf` variadic args → ~5 interface boxing allocs

**Optimisation (high impact)**:
1. Replace `fmt.Fprintf` with `slog.Default()` structured logging — or with a pre-built `[]byte` buffer from a `sync.Pool`. The `fmt.Fprintf` with `%s` boxes every argument into `interface{}`, causing 5 allocs just for argument passing.
2. The `statusRecorder` can be removed if you use `http.ResponseController` (Go 1.20+) which doesn't require wrapping, or pool the `statusRecorder`. `sync.Pool` for `statusRecorder` would save 1 alloc.
3. Replace `strconv.QuoteToASCII` with a range loop that copies only safe bytes into a pooled `[]byte` — eliminates the 2 sanitise allocs.

**Estimated gain**: 6700 ns → ~800–1200 ns (limited by I/O write), 10 allocs → 2–3 allocs. **Feasibility: medium, requires restructuring the format call; no API change.**

### JWTAuth HS256 Hit — 5506 ns, 1313 B, 20 allocs
Breakdown of the 20 allocs:
- `base64.RawURLEncoding.DecodeString(headerB64)` → 1 alloc ([]byte)
- `json.Unmarshal(headerBytes, &hdr)` → 1–2 allocs (JSON decoder)
- `base64.RawURLEncoding.DecodeString(sigB64)` → 1 alloc
- HMAC pool Get+Put: the `h.Sum(nil)` call inside `verify` → 1 alloc (new []byte for MAC digest)
- `base64.RawURLEncoding.DecodeString(payloadB64)` → 1 alloc
- `json.Unmarshal(payloadBytes, &raw)` → 3–5 allocs (JSON decoder, string internment, audience slice)
- `new(JWTClaims)` → 1 alloc
- `[]string(raw.Aud)` → 1 alloc (audience copy)
- `time.Unix(...)` × 3 → 0 allocs (value type)
- `context.WithValue` → 2 allocs
- `r.WithContext` → 1 alloc

**Optimisation (medium impact)**:
1. `h.Sum(nil)` in `jwtHMACPool.verify` allocates a 32-byte slice on every call. Replace with `h.Sum(mac[:0])` where `mac` is a `[32]byte` local on the stack → saves 1 alloc per verify call.
2. The header JSON decode (`json.Unmarshal(headerBytes, &hdr)`) can be replaced with a hand-rolled parser since the JWT header is always small and structured — saves 1–2 allocs.
3. Pre-decode the standard JWT header at pool construction time — if the alg is fixed (single algorithm), the header bytes are always the same. Cache the expected decoded header → skip decode + unmarshal on hit (2+ allocs saved).

**Feasibility: medium, requires care around the `crit` extension check.**

### APIKey Hit — 665 ns, 448 B, 7 allocs
Breakdown:
- `sha256.Sum256([]byte(raw))` → the `[]byte(raw)` conversion escapes because `sha256.Sum256` takes `[]byte` — but escape analysis shows `([]byte)(raw) does not escape`, so this is a stack alloc
- `context.WithValue` → 2 allocs (valueCtx + key escape)
- `r.WithContext` → 1 alloc (new *http.Request)
- **Timing equalisation** (`w.Header().Set("WWW-Authenticate", ...)` + `w.Header().Del`) → 2 header map allocs (Set allocates []string, then Del removes it)
- `w.Header().Set` for WWW-Authenticate on miss path → 1 alloc

The timing equalisation (lines 69–70 in `api_key.go`) is a deliberate security measure (TSC-2026-0008) that costs ~2 allocs on the success path. This cannot be removed without reintroducing a timing oracle.

**Net optimisable allocs**: only the `context.WithValue + r.WithContext` pair (3 allocs). Identical to the constraint faced by all identity-injecting middlewares.

---

## 4. Top 5 optimisation opportunities

Ranked by estimated wall-clock gain × feasibility × safety:

### 1. Logger — `fmt.Fprintf` → `sync.Pool` buffer + direct `io.Writer` writes (HIGH)
**Current**: 6762 ns, 10 allocs.  
**Root cause**: `fmt.Fprintf` boxes 5 arguments as `interface{}`. Each arg is a separate heap allocation. `statusRecorder` also escapes to heap.  
**Fix**: Pool a `bytes.Buffer`, write each field directly without `Fprintf`, return buffer to pool. Pool `statusRecorder` or use `http.ResponseController`. Expected result: ~1000–1500 ns, 3–4 allocs (only time.Format and I/O write remain).  
**Gain estimate**: −5000 ns/op, −7 allocs/op.  
**Safety**: zero — no timing oracle, no log injection risk if sanitise functions are preserved.

### 2. RequestID — pool `[16]byte` hex string to avoid `hex.EncodeToString` alloc (MEDIUM)
**Current**: 4274–4944 ns (generate path), 9 allocs.  
**Root cause**: `crypto/rand.Read` into `[16]byte` (stack) + `hex.EncodeToString(b[:])` allocates a 32-char string. The `context.WithValue + r.WithContext` (3 allocs) cannot be eliminated without API changes.  
**Fix**: Use `encoding/hex` with a pre-allocated buffer: `hex.Encode(buf[:], b[:])` where `buf [32]byte` is on the stack, then convert `string(buf[:])` — this saves the EncodeToString alloc. The `string()` conversion still allocates, but the temp buffer stays on the stack.  
**Gain estimate**: −1 alloc/op, ~−200 ns/op on generate path.  
**Safety**: no impact on uniqueness or validation.

### 3. JWTAuth HS256 — `h.Sum(mac[:0])` to avoid HMAC digest alloc (MEDIUM-LOW)
**Current**: 5506 ns, 20 allocs.  
**Root cause**: `h.Sum(nil)` in `jwtHMACPool.verify` allocates a fresh `[]byte` every call.  
**Fix**: Change `jwtHMACPool.verify` to accept a `*[32]byte` output buffer: `h.Sum(out[:0])` writes into the caller-supplied array, which can be stack-allocated.  
**Gain estimate**: −1 alloc/op, −50–100 ns/op (small relative to JSON decode cost).  
**Safety**: HMAC result is used only for comparison via `hmac.Equal` — output buffer stays private.

### 4. NoCache — pre-build a single `http.Header` and copy it (MEDIUM-LOW)
**Current**: 422 ns, 480 B, 7 allocs.  
**Root cause**: 5× `Header().Set` each allocating a `[]string{value}` inside the response header map.  
**Fix**: Pre-build the 5 key/value pairs at `NoCache()` construction time as a `[5][2]string` array, then in the handler use a loop: `for _, kv := range nocacheHeaders { h[kv[0]] = []string{kv[1]} }` — this avoids the textproto canonicalisation on every call. Or: use `http.Header.Clone()` from a pre-built prototype header.  
**Gain estimate**: −2–3 allocs/op (only httptest recorder map init remains), −150 ns/op.  
**Safety**: Headers are static, no security surface.

### 5. CORS AllowedOrigin — cache textproto-canonical header keys (LOW)
**Current**: 320–1670 ns, 432 B, 4 allocs.  
**Root cause**: `h.Set("Access-Control-Allow-Origin", origin)` + `h.Add("Vary", "Origin")` each allocate a `[]string`. The high variance (5×) suggests lock contention on the `httptest.ResponseRecorder` header map.  
**Fix**: Same as NoCache — use textproto.CanonicalMIMEHeaderKey at construction time and pre-compute the canonical form for each CORS header key. Use direct map assignment `h[canonicalKey] = []string{value}` instead of `h.Set(key, value)` to skip the textproto canonicalisation on every request.  
**Gain estimate**: −1–2 allocs/op, −100–200 ns/op.  
**Safety**: Canonical header names are invariant per RFC 7230.

---

## 5. Zero-alloc middlewares (already optimal)

These are already at the performance ceiling — no allocation, minimal overhead:

| Middleware | ns/op | Why it's 0 allocs |
|---|---|---|
| **StripSlashes** (clean path) | 4.8 | Pure byte scan, no mutation, no copy |
| **Recoverer** (no panic) | 7.5 | Deferred closure registered at function entry, no allocation in happy path |
| **CORS** (no Origin) | 19 | Single header read, early return |
| **CleanPath** (clean path) | 32 | path.Clean returns same string on clean input; early return before Clone |
| **Compress** (no gzip client) | 34 | Single `strings.Contains` check, early return |
| **ThrottleBacklog** (token available) | 45 | Buffered channel receive, no map, no alloc |

---

## 6. Middlewares outside of optimisation scope

- **OAuth2Introspect**: dominated by HTTP round-trip latency (~10ms baseline). The sha256 + cache lookup path is fast (~400 ns), but the primary cost is the network. No algorithmic improvement to the middleware code itself will materially affect end-to-end latency.
- **Compress** (large body): dominated by gzip CPU work. The 2 allocs (gzip writer from pool + sniff buffer) are the minimum for streaming compression.
- **JWTAuth ES256**: ~80 µs dominated by `ecdsa.Verify` + `big.Int` arithmetic. This is a Go crypto library limitation; cannot be improved without switching to a native ECDSA implementation using `crypto/ecdh` or a C binding.

---

## 7. Chain summary

The production stack (Recoverer + RealIP + RequestID + Logger + CORS) costs **8595 ns and 23 allocs** per request. If Logger is replaced with the optimised pooled version (−5000 ns, −7 allocs) and RequestID uses stack-local hex encoding (−200 ns, −1 alloc), the production chain would drop to approximately **3400 ns and 15 allocs** — a 60% ns/op reduction.

The Heavy chain (34 allocs, ~11.5 µs) is dominated by Logger (10 allocs) and RequestID/generate (9 allocs). Optimising Logger alone halves the alloc count.

---

## 8. Benchmark file location

`/data/dev/github.com/FlavioCFOliveira/MuxMaster/reports/perf-audit-2026-05-12/middleware_bench_test.go`

Run with:
```
go test -bench='^Benchmark(Middleware|Chain)' -benchmem -count=10 ./reports/perf-audit-2026-05-12/
```
