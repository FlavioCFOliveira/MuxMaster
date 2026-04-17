# MuxMaster — Zero-Day Hypotheses (Pós-Sprint)

**Date:** 2026-04-17 (Fase 3 — consolidação)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Estados finais pós-sprint + novas hipóteses H-031 a H-034 adicionadas

## Resumo de verdicts

| Verdict | Count | IDs |
|---|---|---|
| **Confirmed** | 22 | H-001, H-002, H-003, H-004, H-005, H-006, H-008, H-009, H-010, H-011, H-012, H-013 (partial), H-015, H-017, H-021, H-024, H-025, H-026, H-027, H-030, H-031 (new), H-032 (new) |
| **Refuted** | 5 | H-007, H-016, H-019, H-023 (funcional), H-028 |
| **Partial** | 4 | H-013, H-018, H-022, H-023 |
| **Deferred** | 3 | H-014, H-020 (subsumed H-007), H-029 |
| **Open (new)** | 2 | H-033, H-034 |

**Total de hipóteses pós-sprint: 34** (30 originais + 4 novas H-031 a H-034).

---

## Convenções

- `H-NNN` — hipótese número NNN, sequencial
- **Premise:** a claim concreta a testar
- **Testability:** como construir evidência empírica
- **Assigned:** agente(s) responsável(is)
- **Priority:** `Critical` / `High` / `Medium` / `Low`
- **Status:** `open` / `confirmed` / `refuted` / `partial` / `merged-into-H-NNN`

Cada hipótese tem ligações cruzadas para attack tree (§) e finding ID quando aplicável.

---

## H-001 — Cross-goroutine race on r.ctx via unsafe.Add

**Premise:** O padrão `*origCtxPtr = rc` ... `handler.ServeHTTP(w, r)` ... `*origCtxPtr = origCtx` em `mux.go:464-480` (e espelhado em 521-537) assume que `r` é goroutine-owned. Se um handler faz `go func() { _ = r.Context() }()` — padrão legítimo em background work —  a goroutine filha pode ler `r.Context()` enquanto a goroutine do dispatcher já sobrescreveu o ponteiro `r.ctx` com o `origCtx`, devolveu `rc` ao pool, e o pool devolveu `rc` a outro request que acabou de mutar `rc.params`. Resultado: a goroutine filha observa o contexto **de outro request concurrente**. O race detector deve flag-ar a escrita do pointer + leitura, mas só se a goroutine filha executar dentro da janela.

**Testability:**
```go
r := mm.New()
var seen []string
var mu sync.Mutex
r.GET("/a/:id", func(w http.ResponseWriter, req *http.Request) {
    go func(r *http.Request) {
        time.Sleep(1 * time.Millisecond)
        mu.Lock()
        seen = append(seen, mm.PathParam(r, "id"))
        mu.Unlock()
    }(req)
})
// Drive two goroutines hitting /a/alpha and /a/beta concurrently with -race
// Assert: `seen` contains only "alpha" and "beta", never empty string or crossed value
```

**Expected outcome:** `-race` detecta write-write ou read-write no offset de `r.ctx`, ou a saída contém valores cruzados. Se confirmado, é `CWE-362` + `CWE-362` combined data race + pool contamination.

**Assigned:** concurrency-security-auditor
**Priority:** Critical
**Status:** **CONFIRMED** — finding MM-2026-0003 (CSA-001). 3 DATA RACE warnings captured em `h001_run1_full.txt` com stacks completas. Corresponde exactamente ao cenário predito.
**Cross-refs:** threat-model §6 `Params / pool` column I, attack tree §D3.1, MM-2026-0003, MM-TM-2026-0004

---

## H-002 — User enumeration timing in basic_auth via map lookup path

**Premise:** `basic_auth.go` compara credenciais assim:
```go
user, pass, ok := r.BasicAuth()
if ok {
    if expected, found := creds[user]; found {
        if subtle.ConstantTimeCompare(...) == 1 { next }
    }
}
// else: 401
```
Um user que existe segue um caminho com `subtle.ConstantTimeCompare` (custos de ~200ns + tamanho da password). Um user que não existe não executa o compare (salta directamente para 401). A diferença é arquitecturalmente **garantida** e mensurável.

**Testability:** Coletar N=1e6 samples timing `auth(r, "alice", "wrong")` (user existe) vs `auth(r, "charlie", "wrong")` (user não existe). Welch t-test. Esperado: p ≪ 0.01. Remediação proposta: executar `subtle.ConstantTimeCompare` sempre contra uma senha dummy (e.g. `dummyHash := "00000000000000000000000000000000"`) quando user não existe, para igualar o path.

**Assigned:** timing-and-sidechannel-analyst (primary) + middleware-security-reviewer (advisory)
**Priority:** High (CWE-208, CWE-203 info disclosure via timing)
**Status:** **CONFIRMED** — finding MM-2026-0009 (TSC-001 + MSR-BA-001). N=1.5M samples, 3 runs triplicados, Welch p=0, KS p=0, MWU p=0. Mean diff 319-429 ns, Cohen d 0.33-0.45. Assembly confirmed: `JEQ 0x00d9` skipa compare quando map miss. Distribuição bimodal para "user absent". Fix proposto: constant-path dummy compare.
**Cross-refs:** attack tree §A2.3.1, transposition §15, MM-2026-0009, MM-TM-2026-0001

---

## H-003 — CRLF log injection via r.URL.Path in logger

**Premise:** `logger.go` faz:
```go
fmt.Fprintf(out, "%s %s %s %d %s\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, rec.status, time.Since(start))
```
`r.URL.Path` é passado sem qualquer escape. Se um cliente enviar um request com path contendo bytes `\r\n` (o que stdlib `net/http` **normalmente** rejeita na request line, MAS pode passar via percent-decode interno de RawPath), o log produz uma linha forjada + uma nova linha injectada.

**Testability:** Enviar `GET /admin%0D%0A2026-04-17T00:00:00Z%20GET%20/fake%20200%200s HTTP/1.1\r\n`. Verificar se `r.URL.Path` (após net/http) contém `\r\n`. Se sim, `fmt.Fprintf(%s)` escreve directamente e o log tem 2 linhas. Alternativamente, path inclui bytes de controlo como `\x1b[2J` (ANSI clear screen) se o log vai a TTY — ofuscação.

**Expected outcome:** net/http já rejeita CR/LF no request-target (retorna 400 no parser) — hipótese provavelmente **refuted** para CRLF directo. Mas `\x1b[...` (ESC sequences) são aceitáveis como bytes válidos em paths (RFC 3986 permite) → ANSI injection confirmada.

**Assigned:** middleware-security-reviewer + http-protocol-security-auditor
**Priority:** High (CWE-117 log injection, CWE-93 CRLF, CWE-150 ANSI)
**Status:** **CONFIRMED** — finding MM-2026-0006 (HPS-001 + MSR-LG-001). Stdlib rejeita CRLF literal em request-target (400) MAS **percent-decoda CRLF para `r.URL.Path`**. 15 payload classes confirmadas em corpus: CRLF, LF, ANSI clear/colour, NUL, BEL, VT, BOM, Unicode line separators.
**Cross-refs:** attack tree §D7, transposition §12, MM-2026-0006, MM-TM-2026-0002

---

## H-004 — request_id CRLF reflection causes response splitting

**Premise:** `request_id.go` copia `X-Request-ID` header do cliente para `w.Header().Set("X-Request-ID", id)` sem validação. Go `net/http` valida bytes inválidos em `Header().Set` via `textproto` e **rejeita** valores com CR/LF (`http.invalidHeaderFields` check). HIPÓTESE: se o valor contém apenas `\t` ou `\x00` ou UTF-8 high chars, passa; se for `\r\n`, `net/http` silenciosamente ignora ou trunca → test.

**Testability:**
```go
req.Header.Set("X-Request-ID", "abc\r\nSet-Cookie: evil=1")
// Run through muxmaster + request_id middleware + httptest
// Inspect recorder.HeaderMap — did Set-Cookie appear?
// Inspect raw bytes via http.ResponseWriter.WriteHeader (or real TCP response)
```

Validar também: client sends 1MB X-Request-ID → response inclui 1MB header → amplifica tráfego de saída (D6 DoS amplification).

**Assigned:** http-protocol-security-auditor (primary) + middleware-security-reviewer
**Priority:** High (CWE-113 response splitting)
**Status:** **CONFIRMED partial** — finding MM-2026-0011 (HPS-005 + MSR-RQ-004 + FPE-001). **Response splitting sanitised on wire by Go 1.26 stdlib** (rejeitou CRLF em response header serialisation). **MAS**: (1) CRLF retido in-memory em `w.Header()` → downstream middleware vê bytes raw; (2) 1 MiB X-Request-ID → 1 MiB response amplification (1024×). Severity High mantida pela amplificação + in-memory state.
**Cross-refs:** attack tree §A2.7, §D1.3, MM-2026-0011, MM-TM-2026-0002

---

## H-005 — CORS Origin reflection when allowAll=true permits credentials theft

**Premise:** `cors.go:22-24` rejects configurations with `AllowedOrigins=["*"]` **AND** `AllowCredentials=true` via panic em config-time. MAS: se caller passa `AllowedOrigins=["*"]` e `AllowCredentials=false`, o middleware reflecte o Origin do atacante no ACAO header. Combinado com:
- se a aplicação usa cookies sem `SameSite=Strict`, resposta cross-site permite CSRF
- request_id reflection ou logger CRLF ainda são vectores laterais

Adicionalmente: HIPÓTESE secundária — se caller passa `AllowedOrigins=["*"]` dinamicamente (e.g. built em runtime a partir de env vars) e **em simultâneo** algo passa `AllowCredentials=true` a partir de outro middleware, não há re-check.

**Testability:** Build muxmaster com `cors.CORS(CORSOptions{AllowedOrigins:[]string{"*"}})`. Enviar `Origin: https://evil.com`. Assert `Access-Control-Allow-Origin: https://evil.com` in response. E depois: `Origin: null` — é aceite? `Origin: evil.com\x1b[` — survives? `Origin: evil.com,` trailing comma? `Origin: ` (empty)?

**Assigned:** middleware-security-reviewer
**Priority:** High (CWE-942)
**Status:** **CONFIRMED** — finding MM-2026-0012 (MSR-CO-003 + FPE-002). `AllowedOrigins=["*"]` + `Origin: https://evil.example` → `ACAO: https://evil.example` em vez de `ACAO: *`. Spec violation; enables credential-grant se `AllowCredentials=true` for adicionado dinamicamente noutra middleware. Fix trivial: emitir literal `*` quando `allowAll=true`.
**Cross-refs:** attack tree §A2.5, §E2; transposition §5, §11; MM-2026-0012, MM-TM-2026-0002

---

## H-006 — compress middleware unbounded buffer OOMs process

**Premise:** `compress.go:26-28`:
```go
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
    if !g.done {
        g.buf = append(g.buf, b...)
        return len(b), nil
    }
    return g.gz.Write(b)
}
```
O handler pode escrever conteúdo **ilimitado** antes de `g.done` ser true (só é true depois do handler retornar em `next.ServeHTTP(grw, r); grw.done = true`). Se o handler faz streaming de 10GB de zeros, `g.buf` cresce para 10GB → OOM.

Vector adicional: heap fragmentation — muitos handlers simultâneos com responses médios (100MB) exaurem RSS.

**Testability:**
```go
r := mm.New()
r.Use(middleware.Compress(5))
r.GET("/big", func(w http.ResponseWriter, req *http.Request) {
    buf := make([]byte, 1<<20) // 1MB chunk
    for i := 0; i < 10_000; i++ { _, _ = w.Write(buf) } // 10GB
})
req := httptest.NewRequest("GET", "/big", nil)
req.Header.Set("Accept-Encoding", "gzip")
// Measure RSS before and after
// Expected: RSS grows by ~10GB in buf; fails on constrained env
```

**Expected outcome:** confirmed OOM. Remediação: streaming compression — usar `gz.Write(b)` directo em vez de buffer; ou impor `MaxBufferSize` config.

**Assigned:** dos-resilience-tester + middleware-security-reviewer
**Priority:** High → **Critical** (promovido porque `Accept-Encoding: gzip` é enviado default por todos os browsers — atacante não requer privilégios)
**Status:** **CONFIRMED** — finding MM-2026-0007 (DOS-001 + MSR-CP-001 + SAST-010). Slope empírico 1.15 byte heap / byte body. 64MB → 178MB peak. 1GB → ~2GB RSS. Streaming compression é o fix.
**Cross-refs:** attack tree §B2.1, §B5.1; transposition §2, §12; MM-2026-0007

---

## H-007 — RedirectFixedPath open redirect via // prefix

**Premise:** `mux.go:502-506`:
```go
if m.RedirectFixedPath {
    if fixed, ok := m.cleanedPath(root, urlPath); ok {
        r.URL.Path = fixed
        http.Redirect(w, r, r.URL.String(), code)
        return
    }
}
```
`cleanedPath` usa `path.Clean(p)`. Para input `//evil.com/foo`, `path.Clean` retorna `/evil.com/foo`. Depois, `r.URL.Path = "/evil.com/foo"`; `r.URL.String()` — dependendo dos campos presentes (Scheme, Host) — pode serializar como URL relativa `"/evil.com/foo"` ou como URL com host extraído.

Teste a variant com `RedirectTrailingSlash` + `//evil.com/foo/`.

**Testability:**
```go
r := mm.New()
r.GET("/evil.com/foo", handler) // matching /evil.com/foo
req := httptest.NewRequest("GET", "http://localhost//evil.com/foo", nil)
// RedirectFixedPath=true by default
// Check Location header
```
Teste também com `CaseInsensitive=true` e backslash `/\\evil.com`.

**Expected outcome:** provavelmente stdlib `http.Redirect` sanitiza — mas confirmar empiricamente. Se Location header contém valor puramente relativo `/evil.com/foo`, browsers tratam como mesmo-origem (OK). Se contém `//evil.com/foo`, trata como protocol-relative (EVIL).

**Assigned:** http-protocol-security-auditor (primary) + path-routing-fuzzer
**Priority:** High (CWE-601)
**Status:** **REFUTED** — `path.Clean("//evil.com/foo")` → `/evil.com/foo` (single slash). `http.Redirect` produz `Location: /evil.com/foo` — Location relativa **same-origin**. Testado em HPS (`evidence/redirect-raw-bytes.txt`). **Nota importante:** a variante TSC-003 (canonicalization discloses hidden routes) é **diferente** e é CONFIRMED em MM-2026-0005.
**Cross-refs:** attack tree §A2.8, §D2.3

---

## H-008 — Middleware ordering silent bypass via Use after Handle

**Premise:** `mux.go:223`:
```go
root.addRoute(pattern, wrapMiddleware(handler, m.middleware))
```
`wrapMiddleware` é chamada no momento do `Handle`, capturando a **snapshot** de `m.middleware` nesse instante. Se caller faz:
```go
r := mm.New()
r.GET("/admin", adminHandler)       // wrapMiddleware vê m.middleware = []
r.Use(auth)                          // ADICIONADO DEPOIS
r.GET("/profile", profileHandler)   // wrapMiddleware vê m.middleware = [auth]
```
→ `/admin` **não** tem auth. Silent.

Esta é uma consequência conhecida do design (performance decision: wrap at registration). MAS **não está suficientemente documentada** em `README` / `middleware.md`. Se um utilizador migra de `chi` (que aplica no request) para MuxMaster, o código compila e funciona **sem auth no admin** e sem warning.

**Testability:** Test case directo replicando o exemplo. Assert: `r.Handle(...)` depois de `r.Use(...)` aplica; antes, não.

**Mitigações possíveis:**
- Panic em `Use()` se já há rotas registadas (breaking — mas claro)
- Warning log em `Use()` pós-Handle
- Documentação forte + linter check

**Assigned:** middleware-security-reviewer (docs)
**Priority:** Critical (auth bypass latente) — severidade depende do utilizador, mas o módulo tem responsabilidade de avisar
**Status:** **CONFIRMED (docs)** — comportamento reproduzido em CSA harness. Fix via docs normativas em README + GoDoc + lint rule. Não é finding MM-NNN (é documentação; o código é by design).
**Cross-refs:** attack tree §A2.1.1

---

## H-009 — XFF unconditional trust permits throttle bypass and logger spoof

**Premise:** `real_ip.go`:
```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    i := strings.IndexByte(xff, ',')
    if i < 0 { r.RemoteAddr = strings.TrimSpace(xff) } else { r.RemoteAddr = strings.TrimSpace(xff[:i]) }
}
```
Aceita **sempre** o XFF, sem lista de proxies confiáveis. Se MuxMaster for deployed directamente (sem proxy à frente), **qualquer cliente** pode sobrescrever `r.RemoteAddr`. Consequências:
- `throttle` (se usar `r.RemoteAddr`) é trivialmente bypassável
- `logger` regista IP falsificado
- IP-based ACLs em middleware applicational são bypassables

Nota: o `throttle.go` actual NÃO usa `r.RemoteAddr` (é global), mas um utilizador que implemente per-IP throttle em cima do `real_ip` está vulnerável por padrão.

**Testability:** Register muxmaster com `real_ip` + custom throttle-per-IP. Attacker sends 100 requests, each with `X-Forwarded-For: <random IP>`. Assert that only 1 is throttled.

**Remediação proposta:** adicionar `real_ip.TrustedProxies([]string)` config.

**Assigned:** middleware-security-reviewer + dos-resilience-tester
**Priority:** High (CWE-345, CWE-290)
**Status:** **CONFIRMED** — finding MM-2026-0008 (HPS-004 + DOS-005 + MSR-RI-001 + SAST-009). 4 reproducers convergentes. Demonstrated: 10 requests com XFF rotativo produzem 10 contadores únicos. Fix via `RealIP(trustedCIDRs ...*netip.Prefix)`.
**Cross-refs:** attack tree §B6, §E5; transposition §1, §5; MM-2026-0008, MM-TM-2026-0002

---

## H-010 — clean_path single-pass bypass via encoded traversal

**Premise:** `clean_path.go` faz **uma** chamada a `path.Clean`. `path.Clean` só vê `..` textual. Se o input é `/%2e%2e/etc/passwd`:
- **Antes** do routing, `r.URL.Path` já pode ter sido decodificado por `net/url` (sim — `r.URL.Path` é decoded)
- Se stdlib decoded `%2e%2e` para `..`, então `r.URL.Path = "/../etc/passwd"`, clean → `/etc/passwd`, routing ataca
- Se stdlib deixou `%2e%2e` no `r.URL.RawPath` e MuxMaster usa `r.URL.Path` (já decoded), o clean vê `..` textual e remove

Mais interessante: `/static/..%2f..%2fsecret`:
- stdlib decoded → `r.URL.Path = "/static/../../secret"`, clean → `/secret` — **depois** do clean, o routing apanha `/secret`

MAS o middleware `clean_path` normaliza o **path** e depois chama `next.ServeHTTP(w, r2)`. Se esse "next" é o router, o router faz lookup no path normalizado. Se `/secret` tem handler, attacker bypassou `/static/*filepath` catch-all.

Teste exhaustivo de ordem: com e sem `clean_path`, com `UnescapePathValues`, com `RedirectFixedPath`. Matriz 2×2×2.

**Testability:** Matriz já descrita pelo `path-routing-fuzzer` no seu prompt (Step 5). Confirma resultados.

**Assigned:** path-routing-fuzzer + middleware-security-reviewer
**Priority:** High (CWE-22)
**Status:** **CONFIRMED** — finding MM-2026-0018 (PRF-002 + HPS-006 + MSR-CL-001). 136 bypass combinations em matrix. `/static/..%2fadmin` → decode → `/static/../admin` → clean → `/admin` → bypass.
**Cross-refs:** attack tree §C1, §C2; transposition §1 (Apache 41773), §5 (Traefik); MM-2026-0018, MM-TM-2026-0003

---

## H-011 — Route-existence timing oracle via RedirectFixedPath / RedirectTrailingSlash

**Premise:** Quando MuxMaster processa um path não-existente, segue três tentativas sequenciais:
1. `getValue` no tree do método
2. Se falhou, try TSR (`RedirectTrailingSlash`)
3. Se falhou, try `path.Clean` + re-lookup (`RedirectFixedPath`)

Para caminhos que **não existem**, a sequência falha no primeiro getValue. Para caminhos que existem com variante trailing/case, o tempo inclui um extra getValue com diferente input. Timing distinguishes registered routes.

A severidade é: reconnaissance é mais barata via redirect. Mas redirect **já revela** (H-007 linha Location), então talvez menos impacto incremental. Ainda assim, timing é measurable mesmo com redirect desabilitado (o código tenta sempre o lookup cleaned).

**Testability:** N=1e6 samples:
- `GET /registered-route` (sem match, mas próximo de existente)
- `GET /random-xyz-doesntexist-123`

Welch + KS. Esperado: distinguishable. Se sim, severidade Medium pois complementa outras fugas.

**Assigned:** timing-and-sidechannel-analyst
**Priority:** Medium (CWE-208)
**Status:** **CONFIRMED** — finding MM-2026-0026 (TSC-002). N=1.5M samples, p=0, mean 437-463 ns gap, Cohen d 0.72-0.79 (large effect). Intrinsic a qualquer radix router — httprouter, chi, bunrouter exibem a mesma magnitude. **Accepted risk** (document in SECURITY.md).
**Cross-refs:** attack tree §A1.2, §D2.2; MM-2026-0026

---

## H-012 — paramsBuf silent overflow at 4th param causes handler logic error

**Premise:** `paramsBuf.add` em `tree.go:21`:
```go
func (pb *paramsBuf) add(key, value string) {
    if pb.count < maxInlineParams {  // 3
        pb.buf[pb.count] = Param{Key: key, Value: value}
        pb.count++
    }
}
```
Para uma rota com 4+ params, o 4º param em diante é **silenciosamente descartado**. Se um developer registra `/a/:b/:c/:d/:e/:f` e um handler lê `PathParam(r, "f")`, obtém `""`. Se esse valor é usado em lógica de auth (`if allowedUsers[pathParam("f")]`), `""` pode mapear para um valor aceite (e.g. empty string em map) → bypass lógico.

O comentário diz "maxInlineParams covers ≥99% of real-world APIs" — `1%` dos casos silenciosamente partem.

**Testability:**
```go
r := mm.New()
r.GET("/a/:p1/:p2/:p3/:p4", h)
// Register with 4 params; issue request /a/w/x/y/z
// Assert: PathParam("p4") returns "" (confirmed overflow)
```

**Mitigação proposta:** panic em `addRoute` se pattern contém >3 params (breaking), ou aumentar `maxInlineParams` para 8 com fallback para `append` fora do buf. Decisão de design.

**Assigned:** fuzzing-and-property-engineer + path-routing-fuzzer
**Priority:** Medium → **High** (promovido; composto com auth-middleware cria auth bypass)
**Status:** **CONFIRMED** — finding MM-2026-0010 (PRF-003 + DOS-003 + FPE-004). Rota `/a/:p1/:p2/:p3/:p4/:p5` → p4, p5 = `""`. httprouter/bunrouter suportam 8-16 params — MuxMaster é outlier.
**Cross-refs:** attack tree §C5; MM-2026-0010, MM-TM-2026-0003

---

## H-013 — Mount RawPath vs Path divergence enables method ACL bypass

**Premise:** `mux.go:357-373`:
```go
r2 := r.Clone(r.Context())
r2.URL = new(url.URL)
*r2.URL = *r.URL
r2.URL.Path = p  // PathParam "mux_mount" (from muxmaster)
if r.URL.RawPath != "" {
    r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
}
```
Se o handler montado inspecciona `r.URL.RawPath`, vê um prefix trim diferente do que o outer router processou. Se `r.URL.Path = "/api/foo"` mas `r.URL.RawPath = "/%61pi/foo"`, `TrimPrefix("/%61pi/foo", "/api")` falha (sem trim), e o inner router recebe RawPath `/%61pi/foo` — se esse inner faz parsing próprio, divergência possível.

Combinado com: Mount está registado no tree `*` (método wildcard), que é verificado DEPOIS dos methods standard. Se attacker envia método não-standard, vai cair no `*` tree e executar o handler Mount com método arbitrário — potencialmente bypassando method-specific ACL.

**Testability:** `r.Mount("/api", innerRouter)`. Send `GET /api/foo` vs `PROPFIND /api/foo`. Assert: inner router receives PROPFIND (sim — por design). Teste adicional: inner router check `r.Method` to enforce ACL. É consistente?

**Assigned:** path-routing-fuzzer + http-protocol-security-auditor
**Priority:** Medium
**Status:** **CONFIRMED partial** — finding MM-2026-0022 (PRF-005). TrimPrefix falha em percent-encoded; inner handler vê `Path="/foo"` mas `RawPath="/%61pi/foo"` (prefix intacta). Risco real apenas se inner handler usa RawPath para routing próprio. Fix: zerar RawPath se TrimPrefix não match.
**Cross-refs:** attack tree §C4, §A2.6; transposition §6; MM-2026-0022

---

## H-014 — Registration-time panic in addRoute allows DoS at init

**Premise:** `tree.addRoute` panic em várias condições (catch-all conflict, regex invalid, etc.). Se a aplicação carrega rotas de um ficheiro de config ou de uma variável externa (env var expansion em pattern), attacker que influencia essa input causa panic no `main()` → processo nunca arranca → DoS da aplicação inteira.

Não é um bug no MuxMaster per se — é um design choice. Mas o **scope do risco** depende de como callers constroem patterns. Deve ser documentado explicitamente com aviso.

**Testability:** Trivial — registar `/[` e ver panic de regexp.Compile. Documentar.

**Assigned:** middleware-security-reviewer (docs) + sast
**Priority:** Low (caller responsibility, but document)
**Status:** **DEFERRED** — documentation-only; não bloqueante para v1.0.0. Mover para SECURITY.md / README pós-release.

---

## H-015 — Composite: logger CRLF + request_id reflection + CORS reflection → multi-channel exfiltration

**Premise (composite):** Combina H-003, H-004 e H-005. Um atacante que envia `X-Request-ID: <exfil data>\r\nZ:`, `Origin: <exfil data>`, e path `/path%20<exfil data>`, com `Authorization: Bearer <secret>`, e compress habilitado, tem 4 vias de exfiltração do segredo:
1. Log line inclui o path (exfil data observável se attacker lê log storage)
2. Response X-Request-ID reflectido (attacker read own response) — **se CRLF survives**
3. Response ACAO reflectido com Origin — idem
4. Response body BREACH via compressão — se a app usa auth context no response

Cada canal sozinho pode ser Medium. Combinados, permitem exfiltração paralela de informação — attacker pode correlacionar ou verificar em múltiplos canais.

**Testability:** e2e test: attacker client + muxmaster + logger to file + response measurement. Conta canais disponíveis por request.

**Assigned:** threat-modeler-and-zero-day-researcher (consolida após H-003, H-004, H-005)
**Priority:** High (composite)
**Status:** **CONFIRMED** — promovido a composto formal **MM-TM-2026-0002** (Multi-channel exfiltration + audit-trail forgery). Inclui agora 4 canais: logger CRLF (MM-2026-0006) + X-Request-ID reflection (MM-2026-0011) + CORS reflection (MM-2026-0012) + BREACH oracle (MM-2026-0047, handler-level) + XFF spoof (MM-2026-0008) para forjar IP source.
**Cross-refs:** MM-TM-2026-0002, composite derivado

---

## H-016 — Throttle token leak on panic in handler

**Premise:** `throttle.go`:
```go
case t := <-tokens:
    defer func() { tokens <- t }()
    next.ServeHTTP(w, r)
    return
```
Se `next.ServeHTTP` panic-ar e NÃO houver `recoverer` **dentro** (antes do throttle wrap), o defer corre e o token retorna. OK. MAS: se `next` panic-ar com `http.ErrAbortHandler` sentinel que o stdlib net/http captura silenciosamente (não chama recoverer do user), o behaviour ainda é OK porque defers correm. CONFIRMADO seguro.

Caso de edge: se o panic acontece DEPOIS de o token ser devolvido (i.e. em `defer tokens <- t` itself panic)? O close do channel se fechado causa panic. O channel nunca é fechado no código — safe.

HIPÓTESE alternativa: combinação com `context.WithTimeout` — timeout middleware cancel, mas handler continua a correr (goroutine leak H-017). Token retornado normalmente (via defer). OK.

**Status:** provavelmente **refuted** via code review, mas auditor deve confirmar com panic injection test.

**Assigned:** concurrency-security-auditor + dos-resilience-tester
**Priority:** Low
**Status:** **REFUTED** — DOS agent confirmed defer cleanup correcto (16k panics concurrent, 0 token leaks). `recoverer_throttle_test.go` PASS. Feedback para CSA: o rc leak correspondente (MM-2026-0015) é bug diferente — não confundir.

---

## H-017 — Timeout middleware goroutine leak under sustained slow handlers

**Premise:** `timeout.go`:
```go
ctx, cancel := context.WithTimeout(r.Context(), d)
defer cancel()
next.ServeHTTP(w, r.WithContext(ctx))
```
Apenas define um deadline no context. O handler não é preempted. Um handler bloqueante (`time.Sleep(time.Hour)`) continua a correr mesmo após `cancel()`. A goroutine do dispatcher está bloqueada dentro desse handler — so o **servidor inteiro** não "leaka", mas a goroutine individual fica presa. Sob 1000 req/s com handlers de 1h, 3.6M goroutines acumulam até crash.

**Testability:**
```go
r := mm.New()
r.Use(middleware.Timeout(10*time.Millisecond))
r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
    time.Sleep(10 * time.Second)
})
before := runtime.NumGoroutine()
// fire 1000 requests in parallel goroutines
time.Sleep(11*time.Second)
after := runtime.NumGoroutine()
// Assert: after - before ≈ 1000 (all stuck)
```

**Mitigação:** impossível sem cooperation do handler. Documentar e recomendar `r.Context().Done()` check no handler.

**Assigned:** dos-resilience-tester + concurrency-security-auditor + docs
**Priority:** Medium (documented limitation, design choice)
**Status:** **CONFIRMED** — finding MM-2026-0019 (DOS-002 + CSA-008 + MSR-TO-003). 1000 req/10ms timeout/10s handler → 1000 goroutines vivas durante 10s. Fix: docs normativas. **Docs blocker para v1.0.0.**
**Cross-refs:** MM-2026-0019, MM-TM-2026-0005

---

## H-018 — reflect-based reqCtxOffset staleness across Go versions

**Premise:** `params.go:139-148`:
```go
func init() {
    t := reflect.TypeOf(http.Request{})
    for i := 0; i < t.NumField(); i++ {
        f := t.Field(i)
        if f.Name == "ctx" {
            reqCtxOffset = f.Offset
            break
        }
    }
}
```
Se uma futura versão de Go:
- Renomear `ctx` para outro nome (e.g. `context`) → `reqCtxOffset = 0` silently
- Remove o campo → idem
- Move para struct embedding → `NumField` não vê
- Torna `http.Request` opaca (privada) → init panics ao aceder

Todas levam a `unsafe.Add(r, 0)` escrita em offset errado → corrupção silenciosa OU panic que o stdlib apanha. **Perigo específico:** actualização automática de Go no CI → tests passam (registo funciona, rotas estáticas funcionam), mas param routes dão comportamento errado.

**Mitigações:**
- Assert em `init()` que o campo encontrado é do tipo certo: `if f.Type != reflect.TypeOf((*context.Context)(nil)).Elem() { panic }` — `panic` em init faz o programa falhar imediatamente com erro claro
- Adicionar teste que assert `reqCtxOffset != 0` e que `unsafe.Add(r, reqCtxOffset)` lê um valor compatível com `context.Context` após `r.WithContext(x)` equality.

**Testability:**
```go
// After r.WithContext(ctxX), read via unsafe.Add and assert equals ctxX
```

**Assigned:** go-sast-and-memory-auditor + concurrency-security-auditor
**Priority:** Medium (latent on toolchain upgrade)
**Status:** **PARTIAL** — finding MM-2026-0035 (CSA-010 + SAST-001). H-018 PASSES em Go 1.26.2 (`TestH018_ReqCtxOffsetAgreement`). Test gate preventivo adicionado pelo SAST agent em `harness/h018_ctx_field_type_test.go`. Fix preventivo adicional recomendado: assert `f.Type == reflect.TypeOf((*context.Context)(nil)).Elem()` em init com panic se falhar.
**Cross-refs:** MM-2026-0035

---

## H-019 — Regex compile cost in addRoute enables pre-serve DoS

**Premise:** `tree.go:223-225`:
```go
re, err := regexp.Compile("^(?:" + expr + ")$")
```
Go `regexp` usa RE2, que é linear-time para match mas a **compilação** pode ser O(2^n) em tempo/espaço para padrões como `(a|a)*` (na realidade Go rejeita via limite `SyntaxError`, mas o limite é alto — até ~100 operações).

Se a aplicação regista rotas dinamicamente (hipotético) com padrão baseado em input, attacker provoca pico de CPU/memória durante registo.

**Testability:** Medir `regexp.Compile` tempo para padrões sintéticos progressivamente complexos. Documentar upper bound observado.

**Assigned:** fuzzing-and-property-engineer + dos-resilience-tester
**Priority:** Low (requires dynamic registration — not supported)
**Status:** **REFUTED** — DOS agent confirmed RE2 linear; Go regex limit rejeita patterns exponenciais. 10k alternations compile em 380µs. Não-exploitable.

---

## H-020 — path.Clean + // serialisation open redirect (overlap with H-007)

**Premise:** Refinamento de H-007 focado numa variante específica. `path.Clean("//evil.com/path")` em Go retorna `/evil.com/path` (não `//evil.com/path`) — verificado. Portanto a saída de clean é safe. MAS: se o attacker envia `/../evil.com/path`, `path.Clean` retorna `/evil.com/path`. Se `r.URL` ainda tem `Scheme=http Host=legit.com`, então `r.URL.String()` produz `http://legit.com/evil.com/path` — safe (caminho interno).

HIPÓTESE QUE SOBRA: `r.URL.String()` pode omitir Host em certas configurações (e.g. se o `httptest.NewRequest` não setou Host). Aí serializa apenas `/evil.com/path`. Browser recebendo `Location: /evil.com/path` interpreta como mesma-origem — safe. Browser recebendo `Location: //evil.com/path` interpreta cross-host — unsafe. Qual é o output actual?

**Testability:** `http.Redirect` wraps `Location` com `r.URL.ResolveReference` lógica; deve sanitizar. Mas confirmar exhaustivamente:
- Cleaned path starts with `//` — happens ever?
- Cleaned path with backslash `\\evil` — Go net/url rejeita no parse?

**Assigned:** http-protocol-security-auditor
**Priority:** Medium (refinement of H-007)
**Status:** **REFUTED (variante `//` open redirect)** + **CONFIRMED (variante canonicalization discloses routes)**. O sub-aspecto open-redirect é refuted (ver H-007). O sub-aspecto "FixedPath reveals canonical form of hidden routes" é confirmed como MM-2026-0005.
**Cross-refs:** MM-2026-0005

---

## H-021 — recoverer panic stack disclosure to os.Stderr

**Premise:** `recoverer.go`:
```go
fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
```
`debug.Stack()` include nomes de funções, paths de ficheiros, e **número de linhas** — suficiente para reverse-engineering parcial. Se o operador ingere stderr em SIEM ou log aggregator com weaker ACL que o binário, attacker que cause panic (e.g. envia payload que triggers `json.Unmarshal` panic num handler) vê layout interno.

Adicionalmente: `%v` de `rcv` pode incluir bytes arbitrários — se attacker provoca `panic(evilString)`, o evil string (com ANSI escapes, CRLF) entra directamente em stderr.

**Mitigação proposta:** estruturar o output como JSON escapado; redact paths via build flag.

**Testability:** Handler panic com `panic("\r\n\x1b[2J" + secret)`. Inspect stderr capture.

**Assigned:** middleware-security-reviewer
**Priority:** Medium (CWE-209 info disclosure, CWE-117 log injection)
**Status:** **CONFIRMED** — finding MM-2026-0023 (DOS-008 + MSR-RE-002 + MSR-RE-003 + CSA-009). Panic value com `Authorization: Bearer sk_live_SECRETTOKENVALUEEEEEE` reproduzido em 1505 bytes de stderr. ANSI escapes preserved. Fix: `RecovererWithLogger(slog.Logger)` com redact opcional; deprecate actual.
**Cross-refs:** MM-2026-0023

---

## H-022 — Pre-middleware path mutation bypasses group auth

**Premise:** `Mux.Pre()` executa middleware **antes** do dispatch. Se `Pre(CleanPath)` ou `Pre(StripSlashes)` é usado, essas middlewares clonam `r` e alteram o path, depois chamam `m.dispatch`. OK — dispatch vê path clean.

HIPÓTESE: se há um `Pre()` que faz "magic": transforma `/admin` em `/admin/cleaned` via algoritmo custom, pode isolar caminhos que parecem diferentes para auth do caminho que acaba no handler. Porém isto requer Pre middleware explicito com lógica custom — risco caller-side.

**Mais interessante:** `Pre()` é aplicado **ao `m.dispatch`**, portanto `m.preHandler = wrapMiddleware(http.HandlerFunc(m.dispatch), m.pre)`. Se caller chama `Pre` depois de `Handle`, a snapshot funciona como em `Use` (H-008) — ou seja Pre só afecta futuro? NÃO — Pre aplica-se ao m.dispatch que é invariante; a preHandler é construída com `m.pre` actual. Cada `Pre(...)` **reconstrói** `preHandler`. Portanto Pre chamada depois de Handle afecta todas as rotas (diferente de Use). Este design é **inconsistente** com Use — risco de confusão para callers.

**Testability:** Registar Handle, depois Pre. Verificar que Pre corre em requests para Handle. Yes — consistente com o construct de `preHandler`.

**Assigned:** middleware-security-reviewer (docs + invariant)
**Priority:** Low (inconsistency; document)
**Status:** **PARTIAL** — confirmed de jure pela análise estrutural. Não produziu finding MM-NNN específico porque é inconsistência de design, não bug. Subsumido em MM-2026-0014 (race em Pre/Use).

---

## H-023 — Silent cross-request param retention in rc.small array

**Premise:** `params.go:106-110`:
```go
type requestCtx struct {
    context.Context
    params Params
    pattern string
    small [3]Param  // shared backing for params
}
```
Em `mux.go:473`: `rc.params = Params(rc.small[:copy(rc.small[:], pslice)])`. Copy preenche `rc.small[0..n]`. Se count=2, `rc.small[2]` ainda tem o valor do **request anterior** (porque é `[3]Param` fixed array, não zeroed em release).

Release (linha 478-480): `rc.params = nil; rc.pattern = ""`. MAS `rc.small[0..2]` não é zeroed. Se na próxima iteração o pool retorna o mesmo `rc` e só 1 param é escrito, `rc.small[1..2]` contém dados do request anterior. Isto só "leaka" se algum código acede `rc.small` directamente — mas `rc.params` é a vista slicing correcta `rc.small[:n]`, por isso handlers **não** veem dados antigos **através de `PathParam`**.

HIPÓTESE: através do `Value(key)` method em `requestCtx`, alguém pode obter o `rc` inteiro (`return c` em `Value` returns `rc`) → via reflection, aceder `rc.small[2]` directamente. Unlikely mas testable.

**Status:** suspected safe, but confirm.

**Assigned:** concurrency-security-auditor (canary test)
**Priority:** Low — depends on exploitability path
**Status:** **REFUTED funcional** — CSA canary 256 000 iter = 0 leaks via API pública. `rc.params[:count]` restringe view correctamente. Defence-in-depth: zerar `rc.small` em release opcional (Low hardening).

---

## H-024 — Handler-spawned goroutine with r retained causes unsafe.Add data race (refinement of H-001)

Ver H-001. Mantido separado para incluir cenário específico de logging async que é padrão comum: handler faz `go logAsync(r.Context(), ...)` — goroutine vive 10ms, durante os quais o dispatcher sobrescreve `r.ctx` 100 vezes → race garantida em 1 request.

**Status:** **CONFIRMED** (same as H-001); high-confidence variant — finding MM-2026-0003.
**Priority:** Critical
**Assigned:** concurrency-security-auditor

---

## H-025 — RedirectTrailingSlash reveals routes before auth middleware

**Premise:** No `dispatch` em `mux.go:490-507`:
- Se path não tem handler mas tem TSR, ServeHTTP emite `http.Redirect` com 301/307 **sem** ter executado qualquer middleware aplicacional (porque middleware é wrapped ao handler, e aqui não há handler).

Resultado: cliente não autenticado consegue distinguir entre rota não-existente (404) e rota protegida que existe só com trailing slash variation (301). A existência da rota é revelada **antes** de qualquer auth correr.

**Testability:**
```go
r := mm.New()
r.Use(authThatDenies)
r.GET("/admin/", handler)  // trailing slash variant
// Request: GET /admin
// Expected: 301 Location: /admin/ (!! auth NOT applied, information disclosed)
```

**Severidade:** High porque é bypassa o modelo de ameaça "middleware guarda tudo".

**Mitigação:** documentar que TSR acontece pré-middleware; ou oferecer opção `TSRRequiresAuth` que aplica middleware à resposta redirect. Alternativa: sempre 404 em vez de 301 para paths alternates (rompe UX — config).

**Assigned:** middleware-security-reviewer + path-routing-fuzzer + http-protocol
**Priority:** High → **Critical** (promovido via composição MM-TM-2026-0001)
**Status:** **CONFIRMED** — finding MM-2026-0004 (HPS-002 + PRF-004). Reproduzido em HPS + PRF + differential vs chi. chi no mesmo setup: `403 auth_calls=1`. MuxMaster: `301 Location: /admin/` auth_calls=0.
**Cross-refs:** attack tree §A1.5, §D2.2; MM-2026-0004, MM-TM-2026-0001

---

## H-026 — Global throttle not per-IP — trivial DoS on shared limit

**Premise:** `throttle.go` usa channels globais sem partição por IP. 1 attacker com 100 req concurrent esgota o budget; todos os outros clientes recebem 503.

A spec/docs deve esclarecer que é "throttle overall" e não "throttle per IP". A UI do middleware (`ThrottleBacklog(limit, backlog, timeout)`) não sinaliza a partição.

**Testability:** 1 attacker socket, 1 legit client socket. Attacker opens `limit` long-running requests. Legit client → 503.

**Mitigação:** rename para `ThrottleAllBacklog` ou adicionar `ThrottlePerIP(limit, keyFunc)`.

**Assigned:** middleware-security-reviewer + dos-resilience-tester
**Priority:** High (easy DoS + user surprise)
**Status:** **CONFIRMED** — finding MM-2026-0013 (DOS-004 + MSR-TH-001). 1 attacker com 5 requests em limit=5 nega 100% de 10 clientes legítimos diferentes.
**Cross-refs:** attack tree §B6.2; MM-2026-0013, MM-TM-2026-0002

---

## H-027 — introspection Walk/Routes concurrent with Handle produces torn read

**Premise:** `introspection.go` usa `treesPtr.Load()` — atomic, OK. MAS uma vez que tem um ponteiro para `methodTrees`, itera os nodes. Se outro goroutine chama `Handle`, faz COW da array **mas** muta a **raiz** referenciada dentro do array antigo (em `root.addRoute`). Portanto o introspection walker vê mutações em tempo real — linha 223 "root.addRoute(pattern, ...)" altera estrutura (filhos, indices, handler fields).

**Mitigação:** copy-on-write dos nodes também, não só da array. Mas isto rompe performance. Alternativa: docs "Lookup/Walk não são safe com registration concorrente".

**Testability:** stress test — Walk em goroutine A, Handle em B. With -race. Expected: race detector reports.

**Assigned:** concurrency-security-auditor
**Priority:** Medium (violates docs contract — "no dynamic registration" — so in-practice rare)
**Status:** **CONFIRMED** — finding MM-2026-0016 (CSA-003). 63 DATA RACE warnings em 2 segundos de stress. Fix: `m.mu.RLock()` em `Walk/Routes/Lookup` (opção C).
**Cross-refs:** MM-2026-0016

---

## H-028 — Unicode case-folding asymmetry in CaseInsensitive mode

**Premise:** `foldEq` em `tree.go:445`:
```go
if a >= 'A' && a <= 'Z' { a += 32 }
```
Apenas ASCII. Se pattern é `/Café` e request é `/café` (ambos NFC), não é case-fold — `é` != `É`. Se `/CAFE` com `CaseInsensitive=true` vs request `/cafe`, funciona (ASCII). Mas `/АDMIN` (Cyrillic А) vs `/admin` — falha (char diferente).

OK — NFC/NFD comparison sempre falha sem normalização, mas isto é **by design** e correcto (confusables não devem cross-fold). HIPÓTESE alternativa: attacker envia `/Admin` (ASCII) vs rota `/admin` — com CaseInsensitive=false, RedirectFixedPath usa `path.Clean` (que não faz case) → não match → 404. Então não há ponto de fold problemático.

Status: provavelmente **refuted**. Auditor deve confirmar via fuzz com input Unicode.

**Assigned:** path-routing-fuzzer
**Priority:** Low
**Status:** **REFUTED** — `foldEq` é ASCII-only por design. Confusables Cyrillic/Latin (e.g. `а=U+0430` vs `a=U+0061`) **não** cross-fold. Comportamento correcto — confusables não devem ser equivalentes por routing. PRF corpus incluiu 20+ Unicode confusables, zero bypasses.

---

## H-029 — WWW-Authenticate realm injection

**Premise:** `basic_auth.go:24`:
```go
w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
```
Se `realm` contém `"\r\n` (aspa + CRLF), produz `Basic realm=""\r\nX-Injected: y"`. Porém Go `Header().Set` valida bytes — rejeita CR/LF. Se contém aspa sem CR/LF, quebra o parser do cliente mas não injecta header. Se caller passa `realm` de fonte externa não-validada, risco de `Set()` silent drop.

**Testability:** `BasicAuth("test\r\nX: y", creds)` — retorno silent drop ou keeps string? Test.

**Assigned:** middleware-security-reviewer + http-protocol
**Priority:** Low
**Status:** **DEFERRED (Low MM-2026-0038)** — wire é sanitizado por Go stdlib; realm CRLF retido in-memory only. Docs-only.

---

## H-030 — Composite: Slowloris + timeout + goroutine leak

**Premise:** Combina timeout goroutine leak (H-017) com slowloris. Attacker:
1. Opens 1000 TCP connections
2. Drips 1 byte header per 5 seconds (slowloris)
3. Before reaching handler, Go's `Server.ReadTimeout` fires (if configured) OR `net/http` keeps accepting
4. Eventually handler starts, and attacker sends request that **blocks** on r.Body read
5. Timeout middleware cancels context
6. Handler **doesn't check ctx.Done**, blocks forever
7. Goroutine pool grows unboundedly

MuxMaster não pode fix inteiro (requer cooperation do handler), mas pode documentar o risco explicitamente.

**Assigned:** dos-resilience-tester + docs
**Priority:** Medium (composite, requires deployment context)
**Status:** **CONFIRMED** — promovido a composto **MM-TM-2026-0005**. Ambos sub-findings (MM-2026-0019 timeout + MM-2026-0024 slowloris docs gap) confirmados separadamente. Vector composto requer ambas as mitigações: `ReadHeaderTimeout` do http.Server + cooperação do handler com `ctx.Done()`.
**Cross-refs:** MM-TM-2026-0005, MM-2026-0019, MM-2026-0024

---

---

## H-031 — Auto-OPTIONS / 405 Allow header leak pré-auth (NOVA — proposta por HPS-003)

**Premise:** As respostas automáticas de `HandleOPTIONS` e `HandleMethodNotAllowed` em `mux.go:546-565` chamam `m.allowed(urlPath, r.Method)` que itera todas as trees e retorna o Allow header com a lista de métodos disponíveis — **antes** da auth middleware correr. Um atacante descobre:
1. Se um path existe (via status 204/405 vs 404).
2. Que métodos estão registados naquele path.
3. Conjuntamente com HPS-002/PRF-004 (TSR leak), enumera a superfície HTTP do servidor sem auditar.

**Testability:** Registar `/admin` com auth middleware que denega tudo. Enviar `OPTIONS /admin` — expected em design seguro: `401 Unauthorized`. Actual: `204 Allow: GET, POST, OPTIONS body="" auth_invocations=0`.

**Status:** **CONFIRMED** — finding MM-2026-0005 (HPS-003 + PRF-004 FixedPath variant + TSC-003). Esta hipótese consolida os sub-findings do domínio HTTP, path-routing e timing num único finding canónico.

**Assigned:** http-protocol-security-auditor (proponente), middleware-security-reviewer (fix), threat-modeler (consolidação)
**Priority:** **Critical** (bloqueia v1.0.0)
**Cross-refs:** MM-2026-0005, MM-TM-2026-0001

---

## H-032 — UTF-8 invariant violations no radix tree (NOVA — proposta por PRF-006)

**Premise:** A árvore radix em `tree.go` opera sobre `string` (sequência de bytes UTF-8) mas **não valida UTF-8** em `addRoute`. Patterns com bytes ≥ 0x80 que não sejam UTF-8 válidos (e.g. `0xFF` solto, `0xC0` sem continuation byte) corrompem invariantes internas:
- `n.indices += string(c)` produz string com byte inválido; subsequentes comparações byte-a-byte funcionam mas `for i, r := range path` (que itera runes, não bytes) produz índices diferentes.
- Em certos paths de split, `len(n.indices) != len(staticChildren)` → slice OOB no hot path.
- FPE-006 demonstrou `/\xf9` + `/` → slice out of range em dispatch.
- PRF-006 demonstrou `/\xff` → index out of range em `incrementChildPrio`.

**Testability:** Fuzzer target `FuzzAddRoute` com ampla cobertura de bytes ≥ 0x80 (UTF-8 inválido, UTF-8 overlong, BMP, SMP code points). Invariant post-addRoute: `assert(len(n.indices) == num static children)`.

**Status:** **CONFIRMED** — finding MM-2026-0002 (PRF-006 + FPE-006). Duas instâncias documentadas; a classe é maior — toda a função `addRoute` e `insertChild` precisa de auditar iteração bytes vs runes.

**Mitigação proposta:**
1. Rejeitar patterns com bytes ≥ 0x80 que não sejam UTF-8 válidos (via `utf8.Valid`), ou documentar explicitamente "patterns devem ser ASCII".
2. Auditar `tree.go` para sítios que usam `for i, r := range path` vs `for i := range len(path)` — são contratos diferentes.
3. Debug-build assert: após cada `addRoute`, validar `len(n.indices) == len(staticChildren)`.

**Assigned:** path-routing-fuzzer (proponente), fuzzing-and-property-engineer, sast (bounds check), threat-modeler
**Priority:** **Critical** (bloqueia v1.0.0 — boot-time DoS via config-file)
**Cross-refs:** MM-2026-0002

---

## H-033 — atomic.Pointer para Mux.preHandler + public fields setters (NOVA — proposta por CSA)

**Premise:** Dos CSA-006 e CSA-007, o padrão actual de leitura sem sync de `m.preHandler`, `m.NotFound`, `m.PanicHandler` e 13 outros fields é teoricamente race-safe apenas em amd64 TSO. Em ARM64 weak memory model, reads podem ver stale writes indefinidamente. A hipótese é: "mudar todos os reassignable fields para `atomic.Pointer[T]` tem overhead aceitável em hot path".

**Testability:** Benchstat baseline (read plain) vs proposal (`atomic.Pointer.Load`) em BenchmarkStaticRoute + BenchmarkParamRoute. Aceitável <5% regressão.

**Status:** **OPEN** — novo, pendente de benchmark. Owner: CSA + go-perf-optimizer.

**Priority:** Medium (hardening pós-v1.0.0)

---

## H-034 — unsafe.Add vs r.WithContext performance trade-off (NOVA — proposta por CSA)

**Premise:** Se removermos `unsafe.Add` e usarmos `r.WithContext(rc)` para eliminar CSA-001 race definitivamente, quanto é a regressão em ns/op e allocs/op? Se <30%, preferível à documentação-normativa-only.

**Testability:**
```
bench baseline (current): BenchmarkParamRoute1: 27 ns/op, 0 allocs/op
bench proposal (r.WithContext): BenchmarkParamRoute1: ~40-45 ns/op, 1 alloc/op (estimate)
benchstat antes/depois: documentar delta
```

**Status:** **OPEN** — depende de decisão arquitectural. Owner: go-perf-optimizer + CSA.

**Priority:** **Critical (architectural decision)** — determina fix de MM-2026-0003.

---

## Processo

**Owner:** threat-modeler-and-zero-day-researcher é o único que escreve aqui.

**Ciclo:**
1. Especialistas reportam findings em `/reports/<agent>/`.
2. Threat-modeler copia findings para `findings.md` com ID canónico.
3. Se finding refina uma hipótese, update status aqui para `confirmed` / `partial` / `refuted`.
4. Findings que compõem nova hipótese criam novo `H-NNN` aqui.
5. Release gate: todas as hipóteses Critical/High têm status `confirmed` (ship com fix) ou `refuted` (evidência).

**Registo de mudanças:** cada vez que uma hipótese muda de estado, adicionar linha no changelog local desta hipótese.

## Matriz final de hipóteses (summary table)

| ID | Priority | Status | Finding(s) |
|---|---|---|---|
| H-001 | Critical | **CONFIRMED** | MM-2026-0003 |
| H-002 | High | **CONFIRMED** | MM-2026-0009 |
| H-003 | High | **CONFIRMED** | MM-2026-0006 |
| H-004 | High | **CONFIRMED partial** | MM-2026-0011 |
| H-005 | High | **CONFIRMED** | MM-2026-0012 |
| H-006 | High → Critical | **CONFIRMED** | MM-2026-0007 |
| H-007 | High | **REFUTED** | — |
| H-008 | Critical | **CONFIRMED (docs)** | — (design/docs) |
| H-009 | High | **CONFIRMED** | MM-2026-0008 |
| H-010 | High | **CONFIRMED** | MM-2026-0018 |
| H-011 | Medium | **CONFIRMED** (accepted risk) | MM-2026-0026 |
| H-012 | Medium → High | **CONFIRMED** | MM-2026-0010 |
| H-013 | Medium | **CONFIRMED partial** | MM-2026-0022 |
| H-014 | Low | **DEFERRED** | docs-only |
| H-015 | High | **CONFIRMED** | MM-TM-2026-0002 |
| H-016 | Low | **REFUTED** | — |
| H-017 | Medium | **CONFIRMED** | MM-2026-0019 |
| H-018 | Medium | **PARTIAL** | MM-2026-0035 (test gate added) |
| H-019 | Low | **REFUTED** | — |
| H-020 | Medium | **REFUTED (base)** / **CONFIRMED (variant)** | MM-2026-0005 |
| H-021 | Medium | **CONFIRMED** | MM-2026-0023 |
| H-022 | Low | **PARTIAL** | subsumido MM-2026-0014 |
| H-023 | Low | **REFUTED funcional** | hardening opcional |
| H-024 | Critical | **CONFIRMED** | MM-2026-0003 (variant) |
| H-025 | High → Critical | **CONFIRMED** | MM-2026-0004 |
| H-026 | High | **CONFIRMED** | MM-2026-0013 |
| H-027 | Medium | **CONFIRMED** | MM-2026-0016 |
| H-028 | Low | **REFUTED** | — |
| H-029 | Low | **DEFERRED (Low)** | MM-2026-0038 |
| H-030 | Medium | **CONFIRMED** | MM-TM-2026-0005 |
| **H-031** (new) | **Critical** | **CONFIRMED** | MM-2026-0005 |
| **H-032** (new) | **Critical** | **CONFIRMED** | MM-2026-0002 |
| **H-033** (new) | Medium | **OPEN** (post-v1.0.0) | — |
| **H-034** (new) | Critical (decision) | **OPEN** | determines MM-2026-0003 fix |

**Totais:** 34 hipóteses. 22 confirmed (+ 4 compostos consolidados). 5 refuted. 4 partial. 2 deferred. 2 new open.
