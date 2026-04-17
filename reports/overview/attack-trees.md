# MuxMaster — Attack Trees (Schneier-style) — Pós-Sprint

**Date:** 2026-04-17 (Fase 3 — folhas anotadas com evidência)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Cada folha testada anotada com **[CONFIRMED MM-NNNN]** ou **[REFUTED]**; composições multi-agente identificadas no final.

---

## Legenda

- `AND` — todos os sub-nós necessários
- `OR` — qualquer sub-nó suficiente
- `[agente]` — agente responsável por auditar a folha
- `§N.M` — referência cruzada para outra árvore
- severidade em `{}`: `{C}` crítico `{H}` alto `{M}` médio `{L}` baixo

---

## Árvore A — Bypass de autenticação (anotada)

```
GOAL A: Obter acesso a uma rota protegida por basic_auth (ou auth application-level)
│
├─ AND A1: Descobrir que a rota existe
│   │
│   ├─ OR A1.1: Força bruta de lista de paths conhecidos      {M}  [path-routing-fuzzer]
│   │           → [CONFIRMED genérico — accepted risk]
│   │
│   ├─ OR A1.2: Oracle de existência via timing de lookup     {H}  [timing-and-sidechannel-analyst]
│   │           → [CONFIRMED MM-2026-0026] — 440ns gap, p=0, N=1.5M
│   │           → radix intrinsic; ACCEPTED RISK (chi/httprouter/bunrouter idem)
│   │
│   ├─ OR A1.3: Introspection endpoint exposto                {C}  [middleware-security-reviewer]
│   │           → [ACCEPTED] caller responsibility; MM-2026-0016 garante Walk race-safe pós-fix
│   │
│   ├─ OR A1.4: Leaked via log line                            {H}  [middleware-security-reviewer]
│   │           → [CONFIRMED MM-2026-0006] — CRLF/ANSI via percent-decoded r.URL.Path
│   │
│   ├─ OR A1.5: RedirectTrailingSlash revela rota antes de auth  {H}  [http-protocol + path-routing]
│   │           → [CONFIRMED MM-2026-0004] — HPS + PRF confirmed; chi differential: auth_calls=1
│   │
│   ├─ OR A1.6: RedirectFixedPath canonicaliza case → rota descoberta {H} [path-routing + timing]
│   │           → [CONFIRMED MM-2026-0005] — TSC-003 + HPS-003
│   │
│   └─ OR A1.7: Panic stack em recoverer revela rota       {M} [middleware-security-reviewer]
│               → [CONFIRMED MM-2026-0023] — `debug.Stack()` + `%v rcv` em stderr
│
└─ AND A2: Ultrapassar o middleware de auth
    │
    ├─ OR A2.1: Middleware ordering misconfiguration           {C}  [middleware]
    │   ├─ A2.1.1: auth registado com `r.Use()` APÓS `r.GET("/admin")`
    │   │          → [CONFIRMED docs] H-008 — silent bypass by design; docs normativas required
    │   └─ A2.1.2: grupo `admin := r.Group("/admin"); admin.GET(...)` sem `admin.Use(auth)`
    │              → [CONFIRMED docs] same class as A2.1.1
    │
    ├─ OR A2.2: Path normalisation bypass                      {C}  [path-routing-fuzzer]
    │   ├─ A2.2.1: Case-fold: /ADMIN com CaseInsensitive=true
    │   │          → [REFUTED direto] foldEq ASCII-only correcto; combined com RedirectFixedPath ver A1.6
    │   ├─ A2.2.2: Encoded: /%61dmin
    │   │          → [CONFIRMED divergência] MuxMaster decodes (200), chi/bunrouter use RawPath (404) — docs gap
    │   ├─ A2.2.3: Overlong: /%c0%61dmin
    │   │          → [REFUTED] Go net/url rejeita overlong UTF-8
    │   ├─ A2.2.4: Unicode confusable: /аdmin (а=U+0430)
    │   │          → [REFUTED] H-028 refuted; confusables não cross-fold
    │   ├─ A2.2.5: Double-encoded: /%2561dmin
    │   │          → [REFUTED] stdlib só decodes uma vez
    │   ├─ A2.2.6: Double-slash: //admin (clean em clean_path único)
    │   │          → [CONFIRMED MM-2026-0018] — CleanPath bypass via encoded traversal
    │   ├─ A2.2.7: strip_slashes + trailing: /admin/ vs /admin (semantics)
    │   │          → [CONFIRMED MM-2026-0025] — não-idempotente
    │   └─ A2.2.8: %2e%2e/ traversal de rota não protegida para protegida
    │              → [CONFIRMED MM-TM-2026-0003] — ServeFiles + CleanPath chain
    │
    ├─ OR A2.3: Credential timing leak                          {C}  [timing-and-sidechannel-analyst]
    │   ├─ A2.3.1: User enumeration via map lookup timing
    │   │          → [CONFIRMED MM-2026-0009] — N=1.5M, p=0, Cohen d 0.33-0.45
    │   └─ A2.3.2: Password dudect via timing (subtle já protege — verificar compilação)
    │              → [CONFIRMED variant MM-2026-0020] — password-length oracle via subtle early-exit
    │
    ├─ OR A2.4: HTTP request smuggling                          {C}  [http-protocol-security-auditor]
    │   ├─ A2.4.1: Frontend (nginx) vê /public; backend (muxmaster) vê /admin
    │   │          → [REFUTED wire-level] 20 smuggling variants tested; stdlib defends all
    │   ├─ A2.4.2: CL.TE / TE.CL / TE.TE entre nginx e net/http
    │   │          → [REFUTED] stdlib rejects (MM-2026-0045)
    │   └─ A2.4.3: H2 → H1 downgrade smuggling
    │              → [PARTIAL] partial coverage; govulncheck clean for CVE-2023-44487
    │
    ├─ OR A2.5: CORS-based CSRF bypass origin                   {C}  [middleware-security-reviewer]
    │   ├─ A2.5.1: allowAll=true + credentials=true (config error)
    │   │          → [REFUTED] panic em config-time (correct defence)
    │   ├─ A2.5.2: Origin null accepted
    │   │          → [REFUTED] rejected by whitelist unless explicit opt-in
    │   └─ A2.5.3: Origin reflection em allowAll → attacker.com recebe response
    │              → [CONFIRMED MM-2026-0012] — wildcard reflects Origin verbatim
    │
    ├─ OR A2.6: Mount bypass                                    {H}  [path-routing + middleware]
    │   ├─ A2.6.1: r.Mount("/api", innerRouter); innerRouter has no auth
    │   │           → [ACCEPTED] caller responsibility; MM-2026-0022 (RawPath divergence) partial
    │   └─ A2.6.2: Catch-all na tree de `*` bypassa method tree de GET
    │              → [PARTIAL] MM-2026-0005 demonstrates auto-OPTIONS bypass method
    │
    ├─ OR A2.7: request_id CRLF → log forgery / SIEM bypass    {H}  [http-protocol + middleware]
    │   → forjar X-Request-ID: <evil>\r\nStatus: 200 OK\r\n
    │   → [CONFIRMED MM-2026-0011 partial] wire sanitised; in-memory retained; amplification confirmed
    │
    └─ OR A2.8: Response splitting em Location → token leak    {M}  [http-protocol]
        → [REFUTED on-wire] Go 1.26 stdlib sanitises Location headers; percent-encoded CRLF in path
          fails dispatch routing (404). No observed splitting on wire.

Outcome: acesso não autorizado a rota protegida
```

## Árvore B — Denial of Service (anotada)

```
GOAL B: Causar indisponibilidade do serviço com um único atacante (ou pool pequeno)
│
├─ OR B1: Exaurir CPU via complexidade algorítmica             {H}  [dos-resilience-tester]
│   ├─ B1.1: Árvore patológica registada (não applicable — atacante não regista)
│   │        → [N/A]
│   ├─ B1.2: Regex ReDoS via {name:(a+)+$}                     {C}
│   │        → [REFUTED H-019] Go RE2 linear; 10k alternations em 380µs
│   ├─ B1.3: Deep path scan em `allowed()` 10 métodos × tree   {M}
│   │        → [CONFIRMED info MM-2026-0036] — 8 allocs, 236B; same shape como stdlib
│   ├─ B1.4: findWildcard em path com muitos `{` aninhados     {M}
│   │        → [CONFIRMED variant MM-2026-0032] — pathological UTF-8 loop (MM-2026-0002 family)
│   └─ B1.5: `getValue` com path de 1MB contendo 1M de `/`     {H}
│            → [REFUTED] complexity O(k) empirically confirmed
│
├─ OR B2: Exaurir memória (heap)                                {C}  [dos-resilience-tester]
│   ├─ B2.1: compress.go g.buf = append(...) ilimitado         {C}
│   │        → [CONFIRMED MM-2026-0007] — 1.15× slope, 64MB→178MB, 1GB→2GB RSS
│   ├─ B2.2: request_id.go aceita X-Request-ID 1MB → header bloat
│   │        → [CONFIRMED MM-2026-0011] — 1MiB → 1MiB amplification
│   ├─ B2.3: CORS headers acumulados → response size
│   │        → [REFUTED] headers são config-time fixed
│   ├─ B2.4: log flood via logger.go (synchronous write)       {M}
│   │        → [PARTIAL] não medido sob load; escalated from DOS §12
│   └─ B2.5: logger goroutine sem buffer — scroll-lock no stdout consome IO
│            → [ACCEPTED] caller responsibility (log sink configuration)
│
├─ OR B3: Exaurir goroutines                                    {H}  [dos + concurrency]
│   ├─ B3.1: Slowloris (ReadHeaderTimeout default=0 em stdlib) {H}
│   │        → [CONFIRMED MM-2026-0024] — 200 drip → +400 goroutines; mitigated by ReadHeaderTimeout
│   ├─ B3.2: Timeout middleware — handler continua após timeout {H}
│   │        → [CONFIRMED MM-2026-0019] — 1000 req / 10ms / 10s handler = 1000 goroutines durante 10s
│   ├─ B3.3: Throttle queue backlog cheio mas tokens não libertados  {M}
│   │        → [REFUTED H-016] defer cleanup correcto; 0 token leaks em 16k panics
│   └─ B3.4: PanicHandler recursivo — recover() em recoverer falha
│             → [REFUTED] 16k panics concurrent recuperados correctamente
│
├─ OR B4: Exaurir sync.Pool (cache miss storm)                 {M}  [concurrency + dos]
│   ├─ B4.1: High-churn pattern: 1M unique params per second
│   │        → [PASS] pool integrity 0 mismatches em GC storm (256k iter)
│   └─ B4.2: rcPool never drained but fragmentation cross-P
│            → [REFUTED] stable under sustained load
│
├─ OR B5: Bomb via gzip                                         {H}
│   ├─ B5.1: Handler retorna conteúdo compressível gigante
│   │        → [CONFIRMED MM-2026-0007] — mesmo vector que B2.1
│   └─ B5.2: BREACH oracle via compression ratio timing/size   {M}
│            → [ACCEPTED MM-2026-0047] handler-level; docs-only
│
├─ OR B6: Throttle bypass via XFF spoof                         {H}
│   ├─ B6.1: real_ip accepts XFF unconditionally
│   │        → [CONFIRMED MM-2026-0008] — 4 reproducers
│   └─ B6.2: Throttle global means 1 attacker exhausts shared limit
│            → [CONFIRMED MM-2026-0013] — 1 attacker nega 100% dos legits
│
├─ OR B7: Hash-flood via map[string]                            {M}
│   ├─ B7.1: CORS allowedOrigins map — keys from config (N/A)   → [N/A]
│   ├─ B7.2: basic_auth creds map — keys from config (N/A)      → [N/A]
│   └─ B7.3: caller middleware with user-controlled map key     → [N/A caller responsibility]
│
├─ OR B8: GC pressure                                           {M}
│   ├─ B8.1: 1M parallel requests → rcPool churn → GC          → [REFUTED] pool integrity confirmed
│   └─ B8.2: CSA-004 rc leak em panic storms                   → [CONFIRMED MM-2026-0015] — 5.4 KB/req leaked
│
└─ OR B9: TCP-level DoS (out of scope for router)               {N/A}
            → coberto pelo operator (TCP syncookies, LB rate limit)

Outcome: OOM kill, 99p latency explode, or worker exhaustion
```

## Árvore C — Routing bypass / path traversal (anotada)

```
GOAL C: Atingir handler diferente do esperado pelo developer
│
├─ OR C1: Traversal via encoding                                {C}
│   ├─ C1.1: /static/..%2f..%2fetc/passwd em catch-all
│   │         → [PARTIAL] http.FileServer aplica path.Clean interno; mas CleanPath middleware composto bypassa
│   ├─ C1.2: /a%2Fb como um segmento único vs dois segmentos
│   │         → [CONFIRMED divergência] MuxMaster decodes para /; chi/bunrouter preservam
│   ├─ C1.3: Overlong UTF-8 %c0%2f → bypass clean_path
│   │         → [REFUTED] Go net/url rejeita overlong
│   ├─ C1.4: Null byte /admin%00.txt vira /admin no match
│   │         → [PARTIAL] Go stdlib decoda NUL; MuxMaster match treats as path byte
│   ├─ C1.5: Fullwidth / (%ef%bc%8f) → não é /
│   │         → [REFUTED] nenhum router equiv
│   └─ C1.6: Backslash / vs \\ em path → Windows-style
│             → [REFUTED] Go net/url não normaliza
│
├─ OR C2: Normalisation divergence                              {H}
│   ├─ C2.1: Router normaliza em X form, proxy em Y form
│   │         → [PARTIAL] documented as accept-risk; Unicode NFC/NFD sempre diverge
│   ├─ C2.2: clean_path faz uma só passada
│   │         → [CONFIRMED MM-2026-0018] — 136 bypass combinations
│   └─ C2.3: CaseInsensitive match + RedirectFixedPath
│             → [CONFIRMED MM-2026-0005] — canonicalization discloses
│
├─ OR C3: Wildcard / catch-all shadow                           {H}
│   ├─ C3.1: Registar /:a e /:b simultaneamente → panic
│   │         → [PASS] detected at registration
│   ├─ C3.2: Registar /*a e /b/static → panic
│   │         → [PASS] detected at registration
│   ├─ C3.3: Order dependency — /users/:id e /users/admin
│   │         → [PASS] static wins por design (tree.go:305)
│   └─ C3.4: /:param pode capturar "" (empty) segment?
│             → [CONFIRMED divergência] `/users/` → TSR redirect; edge case documented
│   └─ C3.5 (NEW from findings): static-after-param produces invalid node type panic
│             → [CONFIRMED MM-2026-0001] — critical tree corruption
│
├─ OR C4: Mount bypass                                          {H}
│   ├─ C4.1: Mount em prefixo longer-than-registered-route
│   │         → [PASS] addRoute panics on conflict
│   ├─ C4.2: RawPath prefix trim divergente de Path
│   │         → [CONFIRMED MM-2026-0022] — PRF-005
│   └─ C4.3: Inner router recebe r.URL com Path trimado mas RawPath não
│             → [CONFIRMED same as C4.2]
│
├─ OR C5: paramsBuf overflow                                    {M}
│   └─ C5.1: Rota com 5 params registada → só 3 capturados
│             → [CONFIRMED MM-2026-0010] — auth bypass via "" → allowedMap[""]
│
├─ OR C6: regex DoS / cost                                      {M}
│   ├─ C6.1: Padrão {id:.*} em /users/{id:.*}/posts
│   │         → [REFUTED] Go RE2 linear
│   └─ C6.2: Padrão {id:(a|a)*} → força Go RE2 a expandir NFA
│             → [REFUTED H-019] Go regex limit rejeita patterns exponenciais
│
├─ OR C7: ServeFiles path escape                                {C}
│   ├─ C7.1: PathParam com %2e%2e/ (UnescapePathValues=true)
│   │         → [PARTIAL] http.FileServer ServeContent interno rejeita; mas composed com CleanPath bypassa (MM-TM-2026-0003)
│   ├─ C7.2: Symlink em root → follow → file fora de root
│   │         → [ACCEPTED] caller responsibility (config de http.FileServer)
│   └─ C7.3: File:// URL scheme leak se handler retornar file path
│             → [N/A] handler responsibility
│
└─ OR C8 (NEW): UTF-8 invariant violation no radix tree         {C}
    └─ C8.1: Pattern com byte ≥ 0x80 não-UTF-8 corrompe indices/children
              → [CONFIRMED MM-2026-0002] — PRF-006 + FPE-006 — hot path panic

Outcome: handler não-pretendido executa; leak de conteúdo OR auth bypass OR process crash
```

## Árvore D — Information disclosure (information leakage)

```
GOAL D: Extrair informação sensível do servidor
│
├─ OR D1: Credential exfiltration                              {C}  [multiple]
│   ├─ D1.1: Authorization header log leak                    {C}  [logger]
│   │         → logger.go só imprime method+path+status — OK até header log ser pedido
│   │         → se caller customiza format → instant leak
│   ├─ D1.2: Panic stack contém senha                         {H}  [recoverer]
│   │         → handler: panic("auth failed for user="+user+" pw="+pw)
│   │         → stderr leak; SIEM ingere
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

## Árvore E — Elevation of privilege

```
GOAL E: Executar operação que require privilégio superior ao actual
│
├─ OR E1: Auth bypass → admin route                            → ver Árvore A
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
│   │         (confirmar)
│   └─ E4.2: _method query param — idem
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

## Árvore F — Supply chain / build integrity

```
GOAL F: Inject malicious code into MuxMaster binary
│
├─ OR F1: Dependency confusion
│   └─ F1.1: go.mod deve ter zero requires (check in SAST)    {PASS hoje}
│
├─ OR F2: Tooling supply chain (golangci-lint, staticcheck)
│   └─ F2.1: Fora do binário — docs-only risk
│
├─ OR F3: CI/CD
│   ├─ F3.1: GitHub Actions tokens (check .github/workflows)
│   └─ F3.2: Build reproducibility (check bytes-for-bytes with go build -buildvcs)
│
└─ OR F4: Malicious commits
    → out of scope for this audit (code-review responsibility)
```

## Composições multi-agente confirmadas (MM-TM-NNNN)

Consolidação após sprint: 5 cadeias compostas **CONFIRMED** com evidência cruzada de múltiplos agentes.

### Composition CC-1 = MM-TM-2026-0001 — Pre-auth reconnaissance + credential pipeline (Critical)

```
A1.5 (TSR leak) = MM-2026-0004 [CONFIRMED]
  → enumera rotas protegidas SEM disparar auth
  + A1.6 (FixedPath canonicaliza) = MM-2026-0005 [CONFIRMED]
  + A2.2.2 (encoded decode) = differential [CONFIRMED]
  → descobre /admin
  + A2.3.1 (user enum timing) = MM-2026-0009 [CONFIRMED p=0]
  → enumera usernames válidos (N=1e5 probes)
  + A2.3.2 (password-length oracle) = MM-2026-0020 [CONFIRMED]
  → descobre comprimento da password
  + MM-2026-0027 (unbounded brute-force)
  → credentials theft

Folhas envolvidas: A1.5, A1.6, A1.7, A2.2.2, A2.3.1, A2.3.2 + BasicAuth unbounded
Agents: HPS + PRF + TSC + MSR
Severity agregada: CRITICAL
```

### Composition CC-2 = MM-TM-2026-0002 — Multi-channel exfiltration + audit-trail forgery (High)

```
B6.1 (XFF spoof) = MM-2026-0008 [CONFIRMED]
  → forja source IP no logger
  + A1.4 (logger CRLF) = MM-2026-0006 [CONFIRMED]
  → log forgery com source IP plausível
  + A2.7 (request_id reflection) = MM-2026-0011 [CONFIRMED partial]
  + A2.5.3 (CORS reflection) = MM-2026-0012 [CONFIRMED]
  + B5.2 (BREACH) = MM-2026-0047 [ACCEPTED handler-level]
  → 4 canais de reflexão paralelos; attacker codifica payload exfil em cada

Agents: HPS + MSR + DOS + TSC
Severity agregada: HIGH
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
Severity agregada: HIGH
```

### Composition CC-4 = MM-TM-2026-0004 — Concurrency + pool + panic composite (Critical)

```
CSA-001 (unsafe.Add race) = MM-2026-0003 [CONFIRMED 3 RACE warnings]
  + CSA-004/005 (panic cleanup skip) = MM-2026-0015 [CONFIRMED 5.4 KB/req leak]
  + CSA-002 (Use/Pre race) = MM-2026-0014 [CONFIRMED 2 RACE warnings]
  + CSA-006 (public fields race) = MM-2026-0017 [CONFIRMED]

Handler: spawns goroutine → reads r.Context() during dispatcher cleanup
→ pool contamination + leaked rc + cross-req data observation
→ se handler panica, rc leak permanente (GC eventually recupera mas pool benefit lost)

Agents: CSA solo (4 findings de origem única consolidam cross-impacts)
Severity agregada: CRITICAL
```

### Composition CC-5 = MM-TM-2026-0005 — Slowloris + timeout + goroutine exhaustion (High)

```
B3.1 (slowloris) = MM-2026-0024 [CONFIRMED; mitigable via ReadHeaderTimeout]
  + B3.2 (timeout leak) = MM-2026-0019 [CONFIRMED]
  → 1000 drip conns + request que bloqueia em body read + timeout cancel + handler ignora ctx.Done
  → goroutine pool cresce unboundedly

Agents: DOS + CSA
Severity agregada: HIGH (deployment-dependent)
```

---

## Legenda anotações pós-sprint

- **[CONFIRMED MM-NNNN]** — finding canónico confirmado; ver `/reports/overview/findings.md`
- **[REFUTED]** — testado e não confirmado; ver `/reports/overview/hypotheses.md`
- **[PARTIAL]** — parcialmente confirmado com caveats
- **[ACCEPTED]** — risco reconhecido; mitigação via docs ou caller responsibility
- **[PASS]** — defesa confirmada (geralmente inherited do stdlib ou design)
- **[N/A]** — não aplicável a MuxMaster

Total folhas anotadas neste sprint: **~80 folhas** distribuídas por árvores A, B, C, D, E.

---

## Árvore D — Information disclosure (anotada)

(Preserva estrutura original; status de folhas individuais marcadas em §D1-D7 abaixo)

D1.1 Authorization log leak → [ACCEPTED] format actual não loga Authorization (caller reviewing responsibility se customizado)
D1.2 Panic stack com pw → [CONFIRMED MM-2026-0023]
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

## Árvore E — Elevation of privilege (anotada)

E1 Auth bypass → Árvore A (ver MM-TM-2026-0001)
E2.1 CORS wildcard + credentials → [REFUTED panics em config-time]
E2.2 CORS reflection em allowAll → [CONFIRMED MM-2026-0012]
E2.3 Subdomain wildcard → [N/A não implementado]
E3 Request smuggling → [REFUTED] stdlib defends (MM-2026-0045)
E4 Method override → [PASS] MuxMaster não honra X-HTTP-Method-Override (verified)
E5 IP allowlist bypass via XFF → [CONFIRMED MM-2026-0008]
E6 Registration race → [CONFIRMED MM-2026-0016]
E7 PanicHandler hijack → [CONFIRMED MM-2026-0017 base class]
E8 Middleware ordering → [CONFIRMED docs H-008]
E9 Mount pass-through → [CONFIRMED partial MM-2026-0022]

---

## Árvore F — Supply chain (PASS)

F1.1 Dependency confusion → [PASS] zero deps (confirmed SAST §2)
F2 Tooling supply chain → out of scope for this audit
F3 CI/CD security → out of scope
F4 Malicious commits → out of scope (code review)
