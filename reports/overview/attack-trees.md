# MuxMaster — Attack Trees (Schneier-style) — Post-Sprint

**Date:** 2026-04-17 (Phase 3 — leaves annotated with evidence)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Each leaf tested and annotated with **[CONFIRMED MM-NNNN]** or **[REFUTED]**; multi-agent compositions identified at the end.

---

## Legend

- `AND` — all sub-nodes required
- `OR` — any sub-node sufficient
- `[agent]` — agent responsible for auditing the leaf
- `§N.M` — cross-reference to another tree
- severity in `{}`: `{C}` critical `{H}` high `{M}` medium `{L}` low

---

## Tree A — Authentication bypass (annotated)

```
GOAL A: Obtain access to a route protected by basic_auth (or application-level auth)
│
├─ AND A1: Discover that the route exists
│   │
│   ├─ OR A1.1: Brute-force list of known paths      {M}  [path-routing-fuzzer]
│   │           → [CONFIRMED generic — accepted risk]
│   │
│   ├─ OR A1.2: Existence oracle via lookup timing     {H}  [timing-and-sidechannel-analyst]
│   │           → [CONFIRMED MM-2026-0026] — 440ns gap, p=0, N=1.5M
│   │           → radix intrinsic; ACCEPTED RISK (chi/httprouter/bunrouter same)
│   │
│   ├─ OR A1.3: Introspection endpoint exposed                {C}  [middleware-security-reviewer]
│   │           → [ACCEPTED] caller responsibility; MM-2026-0016 guarantees Walk race-safe post-fix
│   │
│   ├─ OR A1.4: Leaked via log line                            {H}  [middleware-security-reviewer]
│   │           → [CONFIRMED MM-2026-0006] — CRLF/ANSI via percent-decoded r.URL.Path
│   │
│   ├─ OR A1.5: RedirectTrailingSlash reveals route before auth  {H}  [http-protocol + path-routing]
│   │           → [CONFIRMED MM-2026-0004] — HPS + PRF confirmed; chi differential: auth_calls=1
│   │
│   ├─ OR A1.6: RedirectFixedPath canonicalizes case → route discovered {H} [path-routing + timing]
│   │           → [CONFIRMED MM-2026-0005] — TSC-003 + HPS-003
│   │
│   └─ OR A1.7: Panic stack in recoverer reveals route       {M} [middleware-security-reviewer]
│               → [CONFIRMED MM-2026-0023] — `debug.Stack()` + `%v rcv` to stderr
│
└─ AND A2: Bypass the auth middleware
    │
    ├─ OR A2.1: Middleware ordering misconfiguration           {C}  [middleware]
    │   ├─ A2.1.1: auth registered with `r.Use()` AFTER `r.GET("/admin")`
    │   │          → [CONFIRMED docs] H-008 — silent bypass by design; normative docs required
    │   └─ A2.1.2: group `admin := r.Group("/admin"); admin.GET(...)` without `admin.Use(auth)`
    │              → [CONFIRMED docs] same class as A2.1.1
    │
    ├─ OR A2.2: Path normalisation bypass                      {C}  [path-routing-fuzzer]
    │   ├─ A2.2.1: Case-fold: /ADMIN with CaseInsensitive=true
    │   │          → [REFUTED direct] foldEq ASCII-only correct; combined with RedirectFixedPath see A1.6
    │   ├─ A2.2.2: Encoded: /%61dmin
    │   │          → [CONFIRMED divergence] MuxMaster decodes (200), chi/bunrouter use RawPath (404) — docs gap
    │   ├─ A2.2.3: Overlong: /%c0%61dmin
    │   │          → [REFUTED] Go net/url rejects overlong UTF-8
    │   ├─ A2.2.4: Unicode confusable: /аdmin (а=U+0430)
    │   │          → [REFUTED] H-028 refuted; confusables do not cross-fold
    │   ├─ A2.2.5: Double-encoded: /%2561dmin
    │   │          → [REFUTED] stdlib only decodes once
    │   ├─ A2.2.6: Double-slash: //admin (clean in single pass in clean_path)
    │   │          → [CONFIRMED MM-2026-0018] — CleanPath bypass via encoded traversal
    │   ├─ A2.2.7: strip_slashes + trailing: /admin/ vs /admin (semantics)
    │   │          → [CONFIRMED MM-2026-0025] — not idempotent
    │   └─ A2.2.8: %2e%2e/ traversal from unprotected to protected route
    │              → [CONFIRMED MM-TM-2026-0003] — ServeFiles + CleanPath chain
    │
    ├─ OR A2.3: Credential timing leak                          {C}  [timing-and-sidechannel-analyst]
    │   ├─ A2.3.1: User enumeration via map lookup timing
    │   │          → [CONFIRMED MM-2026-0009] — N=1.5M, p=0, Cohen d 0.33-0.45
    │   └─ A2.3.2: Password dudect via timing (subtle protects — verify compilation)
    │              → [CONFIRMED variant MM-2026-0020] — password-length oracle via subtle early-exit
    │
    ├─ OR A2.4: HTTP request smuggling                          {C}  [http-protocol-security-auditor]
    │   ├─ A2.4.1: Frontend (nginx) sees /public; backend (muxmaster) sees /admin
    │   │          → [REFUTED wire-level] 20 smuggling variants tested; stdlib defends all
    │   ├─ A2.4.2: CL.TE / TE.CL / TE.TE between nginx and net/http
    │   │          → [REFUTED] stdlib rejects (MM-2026-0045)
    │   └─ A2.4.3: H2 → H1 downgrade smuggling
    │              → [PARTIAL] partial coverage; govulncheck clean for CVE-2023-44487
    │
    ├─ OR A2.5: CORS-based CSRF bypass origin                   {C}  [middleware-security-reviewer]
    │   ├─ A2.5.1: allowAll=true + credentials=true (config error)
    │   │          → [REFUTED] panic at config-time (correct defense)
    │   ├─ A2.5.2: Origin null accepted
    │   │          → [REFUTED] rejected by whitelist unless explicit opt-in
    │   └─ A2.5.3: Origin reflection in allowAll → attacker.com receives response
    │              → [CONFIRMED MM-2026-0012] — wildcard reflects Origin verbatim
    │
    ├─ OR A2.6: Mount bypass                                    {H}  [path-routing + middleware]
    │   ├─ A2.6.1: r.Mount("/api", innerRouter); innerRouter has no auth
    │   │           → [ACCEPTED] caller responsibility; MM-2026-0022 (RawPath divergence) partial
    │   └─ A2.6.2: Catch-all in `*` tree bypasses method tree of GET
    │              → [PARTIAL] MM-2026-0005 demonstrates auto-OPTIONS bypass method
    │
    ├─ OR A2.7: request_id CRLF → log forgery / SIEM bypass    {H}  [http-protocol + middleware]
    │   → forge X-Request-ID: <evil>\r\nStatus: 200 OK\r\n
    │   → [CONFIRMED MM-2026-0011 partial] wire sanitised; in-memory retained; amplification confirmed
    │
    └─ OR A2.8: Response splitting in Location → token leak    {M}  [http-protocol]
        → [REFUTED on-wire] Go 1.26 stdlib sanitises Location headers; percent-encoded CRLF in path
          fails dispatch routing (404). No observed splitting on wire.

Outcome: unauthorised access to protected route
```

## Tree B — Denial of Service (annotated)

```
GOAL B: Cause service unavailability with single attacker (or small pool)
│
├─ OR B1: Exhaust CPU via algorithmic complexity             {H}  [dos-resilience-tester]
│   ├─ B1.1: Pathological tree registered (not applicable — attacker does not register)
│   │        → [N/A]
│   ├─ B1.2: Regex ReDoS via {name:(a+)+$}                     {C}
│   │        → [REFUTED H-019] Go RE2 linear; 10k alternations in 380µs
│   ├─ B1.3: Deep path scan in `allowed()` 10 methods × tree   {M}
│   │        → [CONFIRMED info MM-2026-0036] — 8 allocs, 236B; same shape as stdlib
│   ├─ B1.4: findWildcard with many nested `{`                 {M}
│   │        → [CONFIRMED variant MM-2026-0032] — pathological UTF-8 loop (MM-2026-0002 family)
│   └─ B1.5: `getValue` with 1MB path containing 1M of `/`     {H}
│            → [REFUTED] complexity O(k) empirically confirmed
│
├─ OR B2: Exhaust memory (heap)                                {C}  [dos-resilience-tester]
│   ├─ B2.1: compress.go g.buf = append(...) unlimited         {C}
│   │        → [CONFIRMED MM-2026-0007] — 1.15× slope, 64MB→178MB, 1GB→2GB RSS
│   ├─ B2.2: request_id.go accepts X-Request-ID 1MB → header bloat
│   │        → [CONFIRMED MM-2026-0011] — 1MiB → 1MiB amplification
│   ├─ B2.3: CORS headers accumulated → response size
│   │        → [REFUTED] headers are config-time fixed
│   ├─ B2.4: log flood via logger.go (synchronous write)       {M}
│   │        → [PARTIAL] not measured under load; escalated from DOS §12
│   └─ B2.5: logger goroutine without buffer — scroll-lock on stdout consumes IO
│            → [ACCEPTED] caller responsibility (log sink configuration)
│
├─ OR B3: Exhaust goroutines                                    {H}  [dos + concurrency]
│   ├─ B3.1: Slowloris (ReadHeaderTimeout default=0 in stdlib) {H}
│   │        → [CONFIRMED MM-2026-0024] — 200 drip → +400 goroutines; mitigated by ReadHeaderTimeout
│   ├─ B3.2: Timeout middleware — handler keeps running after timeout {H}
│   │        → [CONFIRMED MM-2026-0019] — 1000 req / 10ms / 10s handler = 1000 goroutines during 10s
│   ├─ B3.3: Throttle queue backlog full but tokens not released  {M}
│   │        → [REFUTED H-016] defer cleanup correct; 0 token leaks in 16k panics
│   └─ B3.4: PanicHandler recursive — recover() in recoverer fails
│             → [REFUTED] 16k concurrent panics recovered correctly
│
├─ OR B4: Exhaust sync.Pool (cache miss storm)                 {M}  [concurrency + dos]
│   ├─ B4.1: High-churn pattern: 1M unique params per second
│   │        → [PASS] pool integrity 0 mismatches in GC storm (256k iter)
│   └─ B4.2: rcPool never drained but fragmentation cross-P
│            → [REFUTED] stable under sustained load
│
├─ OR B5: Bomb via gzip                                         {H}
│   ├─ B5.1: Handler returns compressible giant content
│   │        → [CONFIRMED MM-2026-0007] — same vector as B2.1
│   └─ B5.2: BREACH oracle via compression ratio timing/size   {M}
│            → [ACCEPTED MM-2026-0047] handler-level; docs-only
│
├─ OR B6: Throttle bypass via XFF spoof                         {H}
│   ├─ B6.1: real_ip accepts XFF unconditionally
│   │        → [CONFIRMED MM-2026-0008] — 4 reproducers
│   └─ B6.2: Throttle global means 1 attacker exhausts shared limit
│            → [CONFIRMED MM-2026-0013] — 1 attacker denies 100% of legits
│
├─ OR B7: Hash-flood via map[string]                            {M}
│   ├─ B7.1: CORS allowedOrigins map — keys from config (N/A)   → [N/A]
│   ├─ B7.2: basic_auth creds map — keys from config (N/A)      → [N/A]
│   └─ B7.3: caller middleware with user-controlled map key     → [N/A caller responsibility]
│
├─ OR B8: GC pressure                                           {M}
│   ├─ B8.1: 1M parallel requests → rcPool churn → GC          → [REFUTED] pool integrity confirmed
│   └─ B8.2: CSA-004 rc leak in panic storms                   → [CONFIRMED MM-2026-0015] — 5.4 KB/req leaked
│
└─ OR B9: TCP-level DoS (out of scope for router)               {N/A}
            → covered by operator (TCP syncookies, LB rate limit)

Outcome: OOM kill, 99p latency explode, or worker exhaustion
```

## Tree C — Routing bypass / path traversal (annotated)

```
GOAL C: Reach handler different from intended by developer
│
├─ OR C1: Traversal via encoding                                {C}
│   ├─ C1.1: /static/..%2f..%2fetc/passwd in catch-all
│   │         → [PARTIAL] http.FileServer applies path.Clean internal; but CleanPath middleware composed bypasses
│   ├─ C1.2: /a%2Fb as single segment vs two segments
│   │         → [CONFIRMED divergence] MuxMaster decodes to /; chi/bunrouter preserve
│   ├─ C1.3: Overlong UTF-8 %c0%2f → bypass clean_path
│   │         → [REFUTED] Go net/url rejects overlong
│   ├─ C1.4: Null byte /admin%00.txt becomes /admin on match
│   │         → [PARTIAL] Go stdlib decodes NUL; MuxMaster match treats as path byte
│   ├─ C1.5: Fullwidth / (%ef%bc%8f) → not /
│   │         → [REFUTED] no router equivalent
│   └─ C1.6: Backslash / vs \\ in path → Windows-style
│             → [REFUTED] Go net/url does not normalize
│
├─ OR C2: Normalisation divergence                              {H}
│   ├─ C2.1: Router normalizes in X form, proxy in Y form
│   │         → [PARTIAL] documented as accept-risk; Unicode NFC/NFD always diverge
│   ├─ C2.2: clean_path single pass
│   │         → [CONFIRMED MM-2026-0018] — 136 bypass combinations
│   └─ C2.3: CaseInsensitive match + RedirectFixedPath
│             → [CONFIRMED MM-2026-0005] — canonicalization discloses
│
├─ OR C3: Wildcard / catch-all shadow                           {H}
│   ├─ C3.1: Register /:a and /:b simultaneously → panic
│   │         → [PASS] detected at registration
│   ├─ C3.2: Register /*a and /b/static → panic
│   │         → [PASS] detected at registration
│   ├─ C3.3: Order dependency — /users/:id and /users/admin
│   │         → [PASS] static wins by design (tree.go:305)
│   ├─ C3.4: /:param can capture "" (empty) segment?
│             → [CONFIRMED divergence] `/users/` → TSR redirect; edge case documented
│   └─ C3.5 (NEW from findings): static-after-param produces invalid node type panic
│             → [CONFIRMED MM-2026-0001] — critical tree corruption
│
├─ OR C4: Mount bypass                                          {H}
│   ├─ C4.1: Mount at prefix longer-than-registered-route
│   │         → [PASS] addRoute panics on conflict
│   ├─ C4.2: RawPath prefix trim divergent from Path
│   │         → [CONFIRMED MM-2026-0022] — PRF-005
│   └─ C4.3: Inner router receives r.URL with Path trimmed but RawPath not
│             → [CONFIRMED same as C4.2]
│
├─ OR C5: paramsBuf overflow                                    {M}
│   └─ C5.1: Route with 5 params registered → only 3 captured
│             → [CONFIRMED MM-2026-0010] — auth bypass via "" → allowedMap[""]
│
├─ OR C6: regex DoS / cost                                      {M}
│   ├─ C6.1: Pattern {id:.*} in /users/{id:.*}/posts
│   │         → [REFUTED] Go RE2 linear
│   └─ C6.2: Pattern {id:(a|a)*} → forces Go RE2 to expand NFA
│             → [REFUTED H-019] Go regex limit rejects exponential patterns
│
├─ OR C7: ServeFiles path escape                                {C}
│   ├─ C7.1: PathParam with %2e%2e/ (UnescapePathValues=true)
│   │         → [PARTIAL] http.FileServer ServeContent internal rejects; but composed with CleanPath bypasses (MM-TM-2026-0003)
│   ├─ C7.2: Symlink in root → follow → file outside root
│   │         → [ACCEPTED] caller responsibility (http.FileServer config)
│   └─ C7.3: File:// URL scheme leak if handler returns file path
│             → [N/A] handler responsibility
│
└─ OR C8 (NEW): UTF-8 invariant violation in radix tree         {C}
    └─ C8.1: Pattern with byte ≥ 0x80 not-UTF-8 corrupts indices/children
              → [CONFIRMED MM-2026-0002] — PRF-006 + FPE-006 — hot path panic

Outcome: unintended handler executes; content leak OR auth bypass OR process crash
```

## Tree D — Information disclosure (information leakage)

```
GOAL D: Extract sensitive information from server
│
├─ OR D1: Credential exfiltration                              {C}  [multiple]
│   ├─ D1.1: Authorization header log leak                    {C}  [logger]
│   │         → logger.go only prints method+path+status — OK until header log is requested
│   │         → if caller customizes format → instant leak
│   ├─ D1.2: Panic stack contains password                     {H}  [recoverer]
│   │         → handler: panic("auth failed for user="+user+" pw="+pw)
│   │         → stderr leak; SIEM ingests
│   ├─ D1.3: Response splitting → Set-Cookie read             {H}  [http-protocol]
│   └─ D1.4: CORS reflection → page reads session cookie     {H}  [cors]
│
├─ OR D2: Route topology disclosure                            {M}
│   ├─ D2.1: Introspection exposed (Routes endpoint)          {C}  [middleware-review]
│   ├─ D2.2: Timing oracle reveals hidden routes              {H}  [timing]
│   ├─ D2.3: Redirect reveals canonical form                  {M}  [http-protocol + path-routing]
│   ├─ D2.4: 405 Method Not Allowed + Allow header reveals
│   │          which methods exist at hidden path            {M}  [http-protocol]
│   │         → GET /hidden-admin → 405 Allow: POST
│   │         → attacker learns /hidden-admin exists
│   └─ D2.5: Panic stack trace shows file paths and handler names  {M}
│
├─ OR D3: Process memory via unsafe                            {M}  [sast + concurrency]
│   ├─ D3.1: reqCtxOffset stale → writing to wrong offset
│   │         → future Go version moves ctx field
│   │         → silent corruption / reads old stack data
│   └─ D3.2: rcPool reuse cross-request → reads prior params
│             (mitigated by rc.params = nil on release?)
│
├─ OR D4: Backend via BREACH / CRIME                           {H}  [compress + timing]
│   └─ D4.1: Secret in response + attacker-controlled reflected input
│             + compress enabled → measure compressed size
│             → extract secret byte-by-byte
│
├─ OR D5: Error messages reveal internals                      {M}  [middleware]
│   ├─ D5.1: http.Error default text
│   ├─ D5.2: Custom handlers in muxmaster.Error(500, err).Error() → leak err
│   ├─ D5.3: JSON marshalling error reveals struct names
│   └─ D5.4: regexp compile error reveals pattern
│
├─ OR D6: Cache-based channels                                  {L}
│   └─ D6.1: ETag / Last-Modified not set → timing difference
│              between cached and fresh (out of scope — handler resp)
│
└─ OR D7: Log injection enables log-line forgery               {M}  [logger + http-protocol]
    └─ D7.1: Attacker injects fake log lines — plausible deniability
             for real attack traces

Outcome: secret, route, or internal metadata revealed
```

## Tree E — Elevation of privilege

```
GOAL E: Execute operation that requires privilege superior to current
│
├─ OR E1: Auth bypass → admin route                            → see Tree A
│
├─ OR E2: CSRF via CORS misconfiguration → authenticated action from other origin
│   ├─ E2.1: allowAll + credentials (confirmed panic — safe)
│   ├─ E2.2: Origin reflection in allowAll mode                {C}  [cors]
│   └─ E2.3: Subdomain wildcard misimplementation              {H}  [cors]
│
├─ OR E3: Request smuggling → hijack next request of another user {C}  [http-protocol]
│
├─ OR E4: Method override abuse                                 {M}  [http-protocol]
│   ├─ E4.1: X-HTTP-Method-Override — MuxMaster does not honour by default
│   │         (confirm)
│   └─ E4.2: _method query param — same
│
├─ OR E5: Session hijack via XFF spoof → IP-allowlist bypass   {H}  [middleware]
│   └─ E5.1: Admin area protected by IP allowlist → attacker sets
│             X-Forwarded-For to trusted IP
│
├─ OR E6: Race in registration during serving                  {M}  [concurrency]
│   └─ E6.1: Delete /admin, add /admin→public handler, while
│             legitimate request is mid-lookup → sees partial tree
│
├─ OR E7: PanicHandler hijack                                   {L}  [middleware]
│   └─ E7.1: Mux.PanicHandler unset → default recover
│             → panics swallowed; subsequent requests see bad state
│
├─ OR E8: Middleware ordering yields unauth path to privileged handler {C}  [middleware + path-routing]
│   → see A2.1
│
└─ OR E9: Mount pass-through of internal-only handler           {H}  [path-routing + middleware]
    → see A2.6.1

Outcome: action performed with privilege beyond current identity
```

## Tree F — Supply chain / build integrity

```
GOAL F: Inject malicious code into MuxMaster binary
│
├─ OR F1: Dependency confusion
│   └─ F1.1: go.mod must have zero requires (check in SAST)    {PASS today}
│
├─ OR F2: Tooling supply chain (golangci-lint, staticcheck)
│   └─ F2.1: Outside the binary — docs-only risk
│
├─ OR F3: CI/CD
│   ├─ F3.1: GitHub Actions tokens (check .github/workflows)
│   └─ F3.2: Build reproducibility (check bytes-for-bytes with go build -buildvcs)
│
└─ OR F4: Malicious commits
    → out of scope for this audit (code-review responsibility)
```

## Multi-agent compositions confirmed (MM-TM-NNNN)

Consolidation post-sprint: 5 confirmed **CONFIRMED** chains with cross-evidence from multiple agents.

### Composition CC-1 = MM-TM-2026-0001 — Pre-auth reconnaissance + credential pipeline (Critical)

```
A1.5 (TSR leak) = MM-2026-0004 [CONFIRMED]
  → enumerates protected routes WITHOUT triggering auth
  + A1.6 (FixedPath canonicalizes) = MM-2026-0005 [CONFIRMED]
  + A2.2.2 (encoded decode) = differential [CONFIRMED]
  → discovers /admin
  + A2.3.1 (user enum timing) = MM-2026-0009 [CONFIRMED p=0]
  → enumerates valid usernames (N=1e5 probes)
  + A2.3.2 (password-length oracle) = MM-2026-0020 [CONFIRMED]
  → discovers password length
  + MM-2026-0027 (unbounded brute-force)
  → credentials theft

Leaves involved: A1.5, A1.6, A1.7, A2.2.2, A2.3.1, A2.3.2 + BasicAuth unbounded
Agents: HPS + PRF + TSC + MSR
Aggregated severity: CRITICAL
```

### Composition CC-2 = MM-TM-2026-0002 — Multi-channel exfiltration + audit-trail forgery (High)

```
B6.1 (XFF spoof) = MM-2026-0008 [CONFIRMED]
  → forges source IP in logger
  + A1.4 (logger CRLF) = MM-2026-0006 [CONFIRMED]
  → log forgery with plausible source IP
  + A2.7 (request_id reflection) = MM-2026-0011 [CONFIRMED partial]
  + A2.5.3 (CORS reflection) = MM-2026-0012 [CONFIRMED]
  + B5.2 (BREACH) = MM-2026-0047 [ACCEPTED handler-level]
  → 4 parallel reflection channels; attacker encodes exfil payload in each

Agents: HPS + MSR + DOS + TSC
Aggregated severity: HIGH
```

### Composition CC-3 = MM-TM-2026-0003 — ServeFiles + clean_path encoded traversal (High)

```
C7.1 (ServeFiles + %2e%2e) = MM-2026-0018 [CONFIRMED MM-2026-0018]
  → encoded traversal decoded by stdlib
  + A2.2.6 (clean_path single-pass) = MM-2026-0018 [CONFIRMED]
  + catch-all shadow mechanics
  → /static/..%2fadmin → /admin dispatch
  + C5.1 (paramsBuf overflow) = MM-2026-0010 [CONFIRMED]
  → composed with auth middleware: "" param bypasses allowedMap check

Agents: PRF + MSR + HPS
Aggregated severity: HIGH
```

### Composition CC-4 = MM-TM-2026-0004 — Concurrency + pool + panic composite (Critical)

```
CSA-001 (unsafe.Add race) = MM-2026-0003 [CONFIRMED 3 RACE warnings]
  + CSA-004/005 (panic cleanup skip) = MM-2026-0015 [CONFIRMED 5.4 KB/req leak]
  + CSA-002 (Use/Pre race) = MM-2026-0014 [CONFIRMED 2 RACE warnings]
  + CSA-006 (public fields race) = MM-2026-0017 [CONFIRMED]

Handler: spawns goroutine → reads r.Context() during dispatcher cleanup
→ pool contamination + leaked rc + cross-req data observation
→ if handler panics, rc leak permanent (GC eventually recovers but pool benefit lost)

Agents: CSA solo (4 findings of single origin consolidate cross-impacts)
Aggregated severity: CRITICAL
```

### Composition CC-5 = MM-TM-2026-0005 — Slowloris + timeout + goroutine exhaustion (High)

```
B3.1 (slowloris) = MM-2026-0024 [CONFIRMED; mitigable via ReadHeaderTimeout]
  + B3.2 (timeout leak) = MM-2026-0019 [CONFIRMED]
  → 1000 drip conns + request that blocks on body read + timeout cancel + handler ignores ctx.Done
  → goroutine pool grows unboundedly

Agents: DOS + CSA
Aggregated severity: HIGH (deployment-dependent)
```

---

## Post-sprint annotation legend

- **[CONFIRMED MM-NNNN]** — canonical finding confirmed; see `/reports/overview/findings.md`
- **[REFUTED]** — tested and not confirmed; see `/reports/overview/hypotheses.md`
- **[PARTIAL]** — partially confirmed with caveats
- **[ACCEPTED]** — recognized risk; mitigation via docs or caller responsibility
- **[PASS]** — defense confirmed (usually inherited from stdlib or design)
- **[N/A]** — not applicable to MuxMaster

Total annotated leaves this sprint: **~80 leaves** distributed across trees A, B, C, D, E.

---

## Tree D — Information disclosure (annotated)

(Preserves original structure; status of individual leaves marked in §D1-D7 below)

D1.1 Authorization log leak → [ACCEPTED] actual format does not log Authorization (caller reviewing responsibility if customized)
D1.2 Panic stack with pw → [CONFIRMED MM-2026-0023]
D1.3 Response splitting → Set-Cookie → [REFUTED on-wire]
D1.4 CORS reflection → [CONFIRMED MM-2026-0012]
D2.1 Introspection exposed → [ACCEPTED caller]
D2.2 Timing oracle → [CONFIRMED MM-2026-0026 accepted]
D2.3 Redirect reveals canonical → [CONFIRMED MM-2026-0005]
D2.4 405 + Allow header → [CONFIRMED MM-2026-0005]
D2.5 Panic stack file paths → [CONFIRMED MM-2026-0023]
D3.1 reqCtxOffset stale → [PARTIAL MM-2026-0035 — test gate added]
D3.2 rcPool reuse cross-req → [REFUTED H-023]
D4.1 BREACH → [ACCEPTED handler-level MM-2026-0047]
D5.x Error messages → [ACCEPTED caller]
D6.x Cache channels → [N/A handler]
D7.1 Log injection enables forgery → [CONFIRMED MM-2026-0006]

---

## Tree E — Elevation of privilege (annotated)

E1 Auth bypass → Tree A (see MM-TM-2026-0001)
E2.1 CORS wildcard + credentials → [REFUTED panics at config-time]
E2.2 CORS reflection in allowAll → [CONFIRMED MM-2026-0012]
E2.3 Subdomain wildcard → [N/A not implemented]
E3 Request smuggling → [REFUTED] stdlib defends (MM-2026-0045)
E4 Method override → [PASS] MuxMaster does not honour X-HTTP-Method-Override (verified)
E5 IP allowlist bypass via XFF → [CONFIRMED MM-2026-0008]
E6 Registration race → [CONFIRMED MM-2026-0016]
E7 PanicHandler hijack → [CONFIRMED MM-2026-0017 base class]
E8 Middleware ordering → [CONFIRMED docs H-008]
E9 Mount pass-through → [CONFIRMED partial MM-2026-0022]

---

## Tree F — Supply chain (PASS)

F1.1 Dependency confusion → [PASS] zero deps (confirmed SAST §2)
F2 Tooling supply chain → out of scope for this audit
F3 CI/CD security → out of scope
F4 Malicious commits → out of scope (code review)
