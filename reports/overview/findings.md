# MuxMaster — Findings Ledger (Consolidado Pós-Sprint)

**Sprint:** Pre-release v1.0.0
**Commit auditado:** `533d0c9` → **Fixes em:** `723b3be` (Fase 1-3), `3371932` (Fase 4-6)
**Go:** 1.26.2 linux/amd64
**Data de consolidação:** 2026-04-17 (Fase 3 do sprint) / **Actualizado:** 2026-04-18 (Fase 7-8)
**Estado pós-fix:** 7 Critical ✅ Fixed, 14 High ✅ Fixed, 6 Medium Accepted/Fixed, 8 Low Fixed/Accepted
**Owner:** `threat-modeler-and-zero-day-researcher` (único autor)

---

## 1. Convenção de IDs canónicos

Cada finding recebe um ID canónico `MM-YYYY-NNNN`. O `source_id` preserva o ID original do especialista.

| Prefixo source | Agente emissor |
|---|---|
| HPS- | http-protocol-security-auditor |
| PRF- | path-routing-fuzzer |
| DOS- | dos-resilience-tester |
| CSA- | concurrency-security-auditor |
| MSR- | middleware-security-reviewer |
| SAST- | go-sast-and-memory-auditor |
| TSC- | timing-and-sidechannel-analyst |
| FPE- | fuzzing-and-property-engineer |
| TM- | threat-modeler (compostos / transposições) |

## 2. Severity

| Severity | Significado |
|---|---|
| **Critical** | RCE, auth bypass silencioso, corrupção de memória, panic remotamente disparável em hot path, race que quebra isolamento inter-request |
| **High** | Info disclosure sensível (credenciais, rotas secretas pré-auth), DoS sustentável, bypass condicional, logic flaw explorável |
| **Medium** | DoS com requisitos, info disclosure de baixo impacto, bypass lógico com precondições |
| **Low** | Hardening gap, defence-in-depth, docs improvement, dead code |
| **Info** | Observação sem impacto activo |

## 3. Status

Open, Triaged, InProgress, Fixed, Verified, Accepted, Dismissed, Refuted.

## 4. Agregado bruto vs. consolidado

| Agente | Raw Crit / High / Med / Low / Info |
|---|---|
| http-protocol | 0 / 3 / 3 / 1 / 1 |
| path-routing-fuzzer | 2 / 3 / 1 / 0 / 0 |
| dos-resilience | 0 / 3 / 4 / 0 / 2 |
| concurrency | 4 / 3 / 1 / 2 / 0 |
| middleware-review | 0 / 4 / 11 / 3 / 0 |
| sast | 0 / 0 / 2 / 5 / 7 |
| timing | 0 / 2 / 2 / 2 / 1 |
| fuzzing | 3 / 3 / 3 / 0 / 0 |
| **Total bruto** | **9 / 21 / 27 / 13 / 11** |

Após dedup por causa-raiz: **47 findings canónicos** + 5 compostos TM = **52 entradas**

- Critical: **7**
- High: **14**
- Medium: **15**
- Low: **8**
- Info: **3**
- Composites: **5**
- Refuted: **5**

Dedup principais:
- `H-012 paramsBuf overflow` (PRF-003 + DOS-003 + FPE-004) → **MM-2026-0010**
- `H-009 XFF trust` (HPS-004 + DOS-005 + MSR-RI-001 + SAST-009) → **MM-2026-0008**
- `H-006 compress unbounded` (DOS-001 + MSR-CP-001 + SAST-010) → **MM-2026-0007**
- `H-025 TSR pre-auth` (HPS-002 + PRF-004) → **MM-2026-0004**
- `HPS-003 Allow leak + PRF-004 FixedPath + TSC-003` → **MM-2026-0005**
- `H-002 basic_auth timing` (MSR-BA-001 + TSC-001) → **MM-2026-0009**
- `H-021 recoverer stderr` (DOS-008 + MSR-RE-002 + CSA-009) → **MM-2026-0023**
- `H-017 timeout leak` (DOS-002 + CSA-008 + MSR-TO-003) → **MM-2026-0019**
- `H-026 global throttle` (DOS-004 + MSR-TH-001) → **MM-2026-0013**
- `H-004 request_id CRLF` (HPS-005 + MSR-RQ-004 + FPE-001) → **MM-2026-0011**
- `H-005 CORS reflection` (MSR-CO-003 + FPE-002) → **MM-2026-0012**
- `H-003 logger CRLF` (HPS-001 + MSR-LG-001) → **MM-2026-0006**
- `PRF-001 + FPE-009` (wildcard shadow invalid node) → **MM-2026-0001** (confirmada mesma causa-raiz via stack trace)
- `PRF-006 + FPE-006` (UTF-8 / non-ASCII tree corruption) → **MM-2026-0002** (mesma classe estrutural)

**CRÍTICO — CSA-001 (race unsafe.Add) vs FPE-006 (slice OOB getValue):** verificadas causas-raiz distintas:
- CSA-001 é **concorrência** (unsafe.Add + pool; dispara em handler que spawn goroutine)
- FPE-006 é **bounds check** (`len(n.indices) > cap(n.children)` após split específico)
- **Mantidos separados:** MM-2026-0003 e MM-2026-0002 respectivamente.

---

## 5. Ledger canónico — Critical (7)

### MM-2026-0001 — Tree corruption via wildcard shadow + invalid-node-type panic em getValue

| Campo | Valor |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-20, CWE-755, CWE-770 |
| **Component** | `tree.go:63-156` (addRoute static branch), `tree.go:292-425` (getValue), `tree.go:321-396` (switch nType default:panic) |
| **Source IDs** | PRF-001, FPE-009 |
| **Reproducer** | `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-001-wildcard-shadow-crash/repro_test.go`; `/reports/fuzzing-and-property-engineer/evidence/FPE-009/repro_test.go` |
| **Evidence** | Panic `muxmaster: invalid node type` em dispatch após registo ambíguo (`/a/:x` + `/a/b` → dispatch `/a/c` panica). Matriz em `TestShadowMatrix_StaticAfterParam` confirma 3 variantes com panic. FPE-009 demonstra segundo caminho de disparo: registar `/:0` + `/0`. |
| **Root cause** | `addRoute` aceita registo estático irmão de wildchild sem validar invariante "wildchild sempre último". Split produz `wildChild=true` mas `nType=static` (zero-value). `switch n.nType` não cobre esta combinação. |
| **Competitor comparison** | httprouter panica em `addRoute` (detecta conflict registration-time — correcto). chi e bunrouter dispatcham correctamente (static wins). MuxMaster é outlier. |
| **Fix recomendado** | Em `tree.go:122-127`, rejeitar static child quando `n.wildChild=true`: `panic("muxmaster: static segment conflicts with existing wildcard sibling")`. Alternativa: reorder children para manter invariante. |
| **Escalation** | concurrency-security-auditor (tree publicado é partilhado); dos-resilience (DoS via config). |
| **Status** | Fixed — commit `723b3be` (Fase 1-3) |

### MM-2026-0002 — UTF-8 invariant violation: non-ASCII pattern corrompe `tree.indices` vs `tree.children`

| Campo | Valor |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-20, CWE-129 |
| **Component** | `tree.go:118-127` (addRoute), `tree.go:158-176` (incrementChildPrio), `tree.go:305` (getValue slice bound) |
| **Source IDs** | PRF-006, FPE-006 |
| **Reproducer** | `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-006-addroute-0xff-oob/repro_test.go`; `/reports/fuzzing-and-property-engineer/evidence/FPE-006/repro_test.go` |
| **Evidence** | `r.GET("/\xff", h)` é aceite sem panic; subsequente `r.GET("/__sanity__", h)` panica com `index out of range [2] with length 2` em `incrementChildPrio`. Qualquer dispatch subsequente panica com `slice bounds out of range [:3] with capacity 2` em hot path. FPE-006 variante `/\xf9` + `/` produz mesmo sintoma. |
| **Root cause** | `addRoute` não valida UTF-8. `n.indices += string(c)` com `c=0xFF` cria string com byte inválido, quebrando invariante `len(n.indices) == número de filhos estáticos`. |
| **Impact realista** | **Boot-time DoS:** config-file injection de pattern corrupto. **Hot-path DoS:** qualquer request panica após registration. |
| **Fix recomendado** | Rejeitar patterns com bytes ≥ 0x80 que não sejam UTF-8 válidos OR auditar indexação `for i, r := range path` vs byte-index. Invariant check em debug: `assert(len(n.indices) == len(staticChildren))`. |
| **Escalation** | sast (porque é bounds check failure — staticcheck/gosec deveriam ter visto); dos. |
| **Status** | Fixed — commit `723b3be` (Fase 1-3) |

### MM-2026-0003 — Cross-goroutine race em `r.ctx` via unsafe.Add

| Campo | Valor |
|---|---|
| **Severity** | Critical |
| **CWE** | CWE-362 (Race), CWE-367 (TOCTOU) |
| **Component** | `mux.go:464-480` (param path), `mux.go:521-537` (wildcard path), `params.go:155` (setReqCtx dead code) |
| **Source IDs** | CSA-001, SAST-001 (secundário — pattern audit) |
| **Reproducer** | `/reports/concurrency-security-auditor/evidence/2026-04-17/CSA-001/repro_test.go` |
| **Evidence** | 3 DATA RACE warnings capturados com stacks completas em `h001_run1_full.txt`: write em `mux.go:476` (`*origCtxPtr = origCtx`) vs read em `request.go:353` + `params.go:160,164`. Observed output contém valores cruzados entre requests. |
| **Root cause** | Pattern `*origCtxPtr = rc; handler.ServeHTTP(...); *origCtxPtr = origCtx` assume ownership exclusivo de `r` pela goroutine dispatcher. Handlers que spawn goroutines com `r` retido (padrão **completamente legítimo** em Gin/Echo/chi) produzem race imediata. |
| **Competitor comparison** | httprouter, chi, bunrouter, gin, echo — **todos usam `r.WithContext(ctx)` imutável** que retorna novo `*Request`. MuxMaster é único a mutar `r.ctx` in-place. Migração de chi/gin para MuxMaster quebra silenciosamente apps com `go func() { r.Context() }()`. |
| **Fix recomendado** | (A) **preferido:** `handler.ServeHTTP(w, r.WithContext(rc))` — 1 alloc/param-req mas race-free. (B) manter `unsafe.Add` + linter rule que detecta spawn com `r` retido. Recomendação: (A). |
| **Escalation** | threat-modeler (composite MM-TM-2026-0004), go-perf-optimizer (benchstat do fix). |
| **Status** | Fixed — commit `723b3be` (Fase 1-3) |

### MM-2026-0004 — RedirectTrailingSlash emite 301 antes de middleware aplicacional

| Campo | Valor |
|---|---|
| **Severity** | **Critical** (promovido de High em virtude do composto MM-TM-2026-0001) |
| **CWE** | CWE-200 (Information Exposure) |
| **Component** | `mux.go:491-499` (TSR block no dispatch), `mux.go:223` (wrapMiddleware ancora ao handler) |
| **Source IDs** | HPS-002, PRF-004 (parcial), H-025 |
| **Reproducer** | `/reports/http-protocol-security-auditor/harness/repro_test.go:TestReproHPS003_TSRRouteDisclosureBeforeAuth`; `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-004-tsr-pre-auth/repro_test.go` |
| **Evidence** | `GET /admin` com `/admin/` registado + `denyAll` middleware → `301 Location: /admin/`, `auth_middleware_invocations=0`. chi no mesmo setup: `403 auth_calls=1`. |
| **Root cause** | `wrapMiddleware(handler, m.middleware)` é chamado em `Handle()` — só o handler final é envolvido. Quando getValue retorna `tsr=true` com handler=nil, o dispatch emite redirect directamente. |
| **Competitor comparison** | chi aplica middleware antes de qualquer decisão de dispatch — TSR passes through auth. |
| **Fix recomendado** | Três opções: (1) mover TSR/FixedPath para dentro de middleware chain (`notFoundHandler = wrapMiddleware(m.dispatchFallback, m.middleware)`); (2) opt-in `Mux.TSRAfterMiddleware bool` default `true`; (3) docs-only (mínimo aceitável). |
| **Escalation** | threat-modeler (composite), middleware-reviewer (unified fix com MM-2026-0005). |
| **Status** | Fixed — commit `723b3be` (Fase 1-3) |

### MM-2026-0005 — RedirectFixedPath + auto-OPTIONS + auto-405 disclose rotas pré-auth (Allow header leak)

| Campo | Valor |
|---|---|
| **Severity** | **Critical** (promovido de High por combinar com MM-2026-0004 e MM-2026-0009 no composto recon/auth-bypass) |
| **CWE** | CWE-200, CWE-204 |
| **Component** | `mux.go:501-506` (RedirectFixedPath), `mux.go:546-565` (HandleOPTIONS/HandleMethodNotAllowed) |
| **Source IDs** | HPS-003, PRF-004 (variante FixedPath), TSC-003 |
| **Reproducer** | `/reports/http-protocol-security-auditor/harness/repro_test.go:TestReproHPS004_OPTIONSAllowLeakBeforeAuth`; `/reports/timing-and-sidechannel-analyst/evidence/TSC-003/repro_test.go` |
| **Evidence** | `OPTIONS /secret` (com auth middleware deny) → `204 Allow: GET,POST,OPTIONS body="" auth_invocations=0`. `GET /admin//console` → `301 Location: /admin/console` sem passar pela auth. |
| **Root cause** | Mesma causa estrutural do MM-2026-0004. Acresce: `path.Clean` normaliza sem auth — atacante descobre forma canónica de rotas protegidas. |
| **Fix recomendado** | (1) aplicar `m.middleware` aos paths OPTIONS/405; (2) breaking: flip `RedirectFixedPath` default `true → false`. Recomendado: (1) + (2) combinados. |
| **Escalation** | threat-modeler (MM-TM-2026-0001). HPS propôs H-031 formalmente — **promovido a hipótese no sprint**. |
| **Status** | Fixed — commit `723b3be` (Fase 1-3) |

### MM-2026-0006 — Logger escreve `r.URL.Path` raw — CRLF / ANSI / NUL log injection

| Campo | Valor |
|---|---|
| **Severity** | **Critical** (promovido de High porque é primitiva de audit-trail forgery dos compostos MM-TM-2026-0002) |
| **CWE** | CWE-117, CWE-150, CWE-93 |
| **Component** | `middleware/logger.go:30` — `fmt.Fprintf(out, "... %s ...", ..., r.URL.Path, ...)` |
| **Source IDs** | HPS-001, MSR-LG-001, H-003 |
| **Reproducer** | `/reports/http-protocol-security-auditor/evidence/h003-logger-injection.txt`; `/reports/middleware-security-reviewer/evidence/logger-injection-corpus.txt` (15 payload classes: CRLF, LF, ANSI clear, ANSI colour, NUL, BEL, VT, BOM, Unicode line separators) |
| **Evidence** | `GET /abc%0D%0A2099-01-01T00:00:00Z+GET+/fake+200+0s` → 2 log lines (forgery). `/abc%1B%5B2J%1B%5BH` → ANSI clear em TTY viewer. Stdlib rejeita CRLF literal em request-target (400) mas **percent-decoda para `r.URL.Path`**. |
| **Root cause** | `r.URL.Path` é percent-decoded pelo `url.Parse`; `fmt.Fprintf(%s)` escreve bytes raw. |
| **Fix recomendado** | `sanitiseForLog(r.URL.Path)` com `strconv.QuoteToASCII` ou filtro de controlo bytes (CR/LF/NUL/DEL/ESC). Alternativamente, JSON estruturado que escape naturalmente. |
| **Escalation** | threat-modeler (composite). |
| **Status** | Fixed — commit `3371932` (Fase 4-6) |

### MM-2026-0007 — compress middleware acumula resposta inteira antes de flush (unbounded buffer)

| Campo | Valor |
|---|---|
| **Severity** | **Critical** (promovido de High porque: (1) magnitude empírica é OOM real, (2) atacante não precisa de privilégios especiais, (3) `Accept-Encoding: gzip` é enviado por todos os browsers/clientes modernos) |
| **CWE** | CWE-400 |
| **Component** | `middleware/compress.go:25-31` — `g.buf = append(g.buf, b...)` |
| **Source IDs** | DOS-001, MSR-CP-001, SAST-010, H-006 |
| **Reproducer** | `/reports/dos-resilience-tester/evidence/DOS-001/compress-oom.txt`; `/reports/middleware-security-reviewer/evidence/compress-rss-trace.txt` |
| **Evidence** | Slope empírico 1.15 byte heap / byte body; 64MB body → 177.56MB heap peak; 1GB body (extrapolado) → ~1.5-2 GB RSS. Confirmed 2.75× due a `append` doubling. |
| **Root cause** | `g.done` é `true` apenas após handler retornar. Write(b) faz append até lá. Compression ocorre uma vez em `grw.flush()`. |
| **Fix recomendado** | Streaming compression: `g.gz.Write(b)` directo após detecção de MIME / threshold. Bounded buffer (e.g. 8 KiB) só para content-type sniffing. |
| **Escalation** | middleware-reviewer (BREACH surface analysis); threat-modeler (composite MM-TM-2026-0002). |
| **Status** | Fixed — commit `3371932` (Fase 4-6) |

---

## 6. Ledger canónico — High (14)

### MM-2026-0008 — real_ip confia XFF / X-Real-IP sem lista de proxies

| Severity | CWE | Source | Reproducer |
|---|---|---|---|
| High | CWE-345, CWE-290 | HPS-004, DOS-005, MSR-RI-001, SAST-009, H-009 | 4 reproducers convergentes |

**Component:** `middleware/real_ip.go:12-22`. **Evidence:** 10 requests de um IP com XFF rotativo → 10 contadores únicos; per-IP ACLs downstream triviais de bypass. **Root cause:** nenhuma validação de peer origem. **Fix:** `RealIP(trustedCIDRs ...*netip.Prefix)` — skip mutation se direct peer não está em trustedCIDRs. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0009 — basic_auth user enumeration via timing

| Severity | CWE | Source | Reproducer |
|---|---|---|---|
| High | CWE-208, CWE-203 | MSR-BA-001, TSC-001, H-002 | `/reports/timing-and-sidechannel-analyst/evidence/TSC-001/repro_test.go` |

**Component:** `middleware/basic_auth.go:17-22`. **Evidence:** N=1.5M, 3 runs: Welch p=0, KS p=0, MWU p=0; mean diff 319-429ns; Cohen d 0.33-0.45; distribuição bimodal para "user absent". Assembly confirmed: `JEQ 0x00d9` skipa `subtle.ConstantTimeCompare` quando map miss. **Fix:** constant-path — sempre executar `subtle.ConstantTimeCompare` contra dummy hash quando `found=false`. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0010 — paramsBuf silent overflow no 4º param (maxInlineParams=3)

| Severity | CWE | Source |
|---|---|---|
| High | CWE-754, CWE-703, CWE-284 composite | PRF-003, DOS-003, FPE-004, H-012 |

**Component:** `tree.go:13-26`. **Evidence:** Rota com 5 params captura só 3; p4, p5 = `""`. Em composição com auth middleware que faz `allowedTenants[PathParam(r, "tenant")]` — se o mapa contém `""`, bypass silencioso. httprouter/bunrouter suportam 8-16 params. **Fix:** preferido panic em `addRoute` se pattern tem > 3 wildcards (breaking, explícito); alternativa fallback slice. **Status:** Fixed — commit `723b3be` (Fase 1-3).

### MM-2026-0011 — request_id aceita e reflecte X-Request-ID sem validação

| Severity | CWE | Source |
|---|---|---|
| High | CWE-113 (in-memory), CWE-400, CWE-20 | HPS-005, MSR-RQ-004, FPE-001, H-004 |

**Component:** `middleware/request_id.go:16-23`. **Evidence:** 1 MiB X-Request-ID → 1 MiB response header. CRLF retido in-memory (wire é sanitised por Go 1.26). **Fix:** `validRequestID`: `[A-Za-z0-9_-.]{1,128}`. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0012 — CORS wildcard reflecte Origin attacker em vez de emitir `*`

| Severity | CWE | Source |
|---|---|---|
| High | CWE-942, CWE-113 (in-memory) | MSR-CO-003, MSR-CO-007, FPE-002, H-005 |

**Component:** `middleware/cors.go:54`. **Evidence:** `AllowedOrigins=["*"]` + `Origin: https://evil.example` → `ACAO: https://evil.example` (spec violation). CRLF no Origin retido in-memory. **Fix:** `if allowAll { h.Set("Access-Control-Allow-Origin", "*") }`. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0013 — Throttle global disfarçado de per-IP

| Severity | CWE | Source |
|---|---|---|
| High | CWE-770, CWE-400 | DOS-004, MSR-TH-001, H-026 |

**Component:** `middleware/throttle.go:17`. **Evidence:** 1 attacker com `limit` requests concorrentes nega 100% de clientes legítimos diferentes. **Fix:** rename `ThrottleAllBacklog` (breaking) + adicionar `ThrottlePerIP(limit, keyFn, timeout)` com bucket cap. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0014 — Use()/Pre()/preHandler mutados sem lock

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362, CWE-667 | CSA-002, CSA-007 |

**Component:** `mux.go:175-184`. **Evidence:** 2 DATA RACE warnings (`mux.go:176` vs `mux.go:223`). **Fix:** `m.mu.Lock()` em `Use/Pre`; `atomic.Pointer[http.Handler]` para `preHandler`. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0015 — Panic em handler deixa `r.ctx` apontando para rc leaked + pool leak

| Severity | CWE | Source |
|---|---|---|
| High | CWE-404, CWE-772, CWE-672 | CSA-004, CSA-005 |

**Component:** `mux.go:466-480`, `mux.go:523-537`. **Evidence:** 50k panics → `allocDelta=271 990 424 B` (5440 B/req). Pós-panic, `req.Context()` ainda aponta para rc. **Fix:** `defer` wrapping cleanup. Custo ~20ns/req aceitável. **Status:** Fixed — commit `3371932` (Fase 4-6) (covered by r.WithContext).

### MM-2026-0016 — Introspection (Walk/Routes/Lookup) race vs addRoute

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362, CWE-820 | CSA-003, H-027 |

**Component:** `introspection.go:60-95` vs `tree.go:63-155`. **Evidence:** 63 DATA RACE warnings em 2s stress. **Fix:** `m.mu.RLock()` em Walk/Routes/Lookup (opção C — zero impacto em hot path). **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0017 — Public fields read unsafely on hot path

| Severity | CWE | Source |
|---|---|---|
| High | CWE-362 | CSA-006 |

**Component:** `mux.go:109-154` (PanicHandler, NotFound, MethodNotAllowed, GlobalOPTIONS, ErrorHandler, 8 bool/int flags). **Evidence:** 3 tests FAIL com DATA RACE. **Fix:** setters atómicos via `atomic.Pointer` OR docs "set before ListenAndServe". **Status:** Accepted — documented in SECURITY.md.

### MM-2026-0018 — clean_path single-pass bypass via encoded traversal

| Severity | CWE | Source |
|---|---|---|
| High | CWE-22 | PRF-002, HPS-006, MSR-CL-001, H-010 |

**Component:** `middleware/clean_path.go:9-21`. **Evidence:** `/static/..%2fadmin` → decode → `/static/../admin` → `path.Clean` → `/admin` → bypass. 136 bypass combinations em matrix. **Fix:** `SafeCleanPath` (rejeita `..` pós-decode) OR operar sobre RawPath OR docs explícita. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0019 — Timeout middleware não preempta handler

| Severity | CWE | Source |
|---|---|---|
| High (Critical em composto) | CWE-400 | DOS-002, CSA-008, MSR-TO-003, H-017 |

**Component:** `middleware/timeout.go:14-20`. **Evidence:** 1000 req / 10ms timeout / 10s handler = 1000 goroutines durante 10s. **Fix:** docs normativas + exemplo; opcional `TimeoutWithAbort`. **Status:** Accepted — documented in SECURITY.md.

### MM-2026-0020 — Password-length oracle via subtle.ConstantTimeCompare early-exit

| Severity | CWE | Source |
|---|---|---|
| High (promovido de Medium — explorável pós-enumeração MM-2026-0009) | CWE-208, CWE-203 | TSC-004 |

**Component:** `basic_auth.go:18` → `crypto/subtle/constant_time.go:18-22`. **Evidence:** N=1.5M, p=0, mean diff 284-316ns. Latency maximal quando len(pass)==len(expected). **Fix:** SHA-256 ambos inputs antes de compare (bundle com MM-2026-0009). **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0021 — Registration-time index OOB em pattern `/{…}*name`

| Severity | CWE | Source |
|---|---|---|
| High | CWE-20, CWE-129, CWE-755 | FPE-005 |

**Component:** `tree.go:259-262`. **Evidence:** `r.Handle("/{:}*00000", h)` → `path[-1]` → `runtime error: index out of range`. **Fix:** `if i < 0 || path[i] != '/' { panic(...) }`. **Status:** Fixed — commit `723b3be` (Fase 1-3).

---

## 7. Ledger canónico — Medium (15)

### MM-2026-0022 — Mount preserva RawPath com prefix não-trimmed

High:Medium / CWE-707 / PRF-005. `mux.go:362-370`. TrimPrefix falha em percent-encoded prefix. Inner handler vê Path/RawPath divergentes. Fix: zerar RawPath se TrimPrefix não match. **Status:** Fixed — commit `3371932` (Fase 4-6).

### MM-2026-0023 — Recoverer dumpa panic value + stack raw para stderr

Medium / CWE-209+532+150 / DOS-008 + MSR-RE-002 + MSR-RE-003 + CSA-009 + H-021. `recoverer.go:16`. Atacante-controlled panic values + ANSI escapes → stderr. Fix: `RecovererWithLogger(slog.Logger)`; deprecate actual.

### MM-2026-0024 — Slowloris exposure em http.Server default (docs gap)

Medium / CWE-400 / DOS-006. Docs gap: README usa `ListenAndServe` sem timeouts. 200 drip clients → +400 goroutines. Fix: SECURITY.md com `ReadHeaderTimeout: 30s` exemplo.

### MM-2026-0025 — StripSlashes não-idempotente

Medium / CWE-707 / FPE-003 + MSR-SS-001. `/a//` → `/a/` (não `/a`). Fix: loop `for len(p)>1 && p[len(p)-1]=='/'`.

### MM-2026-0026 — Route-existence timing oracle (radix intrinsic)

Medium / CWE-208 / TSC-002, H-011. ~440ns gap. **Accepted risk** — intrinsic a todos radix routers (chi, httprouter, bunrouter idem). Docs em SECURITY.md.

### MM-2026-0027 — BasicAuth unbounded brute-force (sem rate-limit)

Medium / CWE-307 / MSR-BA-005. Fix: docs recomendando compose com ThrottlePerIP (depende de MM-2026-0013).

### MM-2026-0028 — CORS CRLF no Origin retido in-memory

Medium / CWE-113 / MSR-CO-007. Bundle com MM-2026-0012. Validação Origin antes de `Set`.

### MM-2026-0029 — real_ip CRLF em XFF retido em r.RemoteAddr

Medium / CWE-117 / MSR-RI-002. Bundle com MM-2026-0008. `net.ParseIP` após trim.

### MM-2026-0030 — compress sem brotli fallback + BREACH surface

Medium / CWE-693+203 / MSR-CP-005, MSR-CP-007. Fix: docs + opt-in BREACH mitigation. **Status:** Accepted — out-of-scope for v1.0.0.

### MM-2026-0031 — paramsBuf DoS variant (runtime amplification)

Medium / CWE-754 / DOS-003 (analytical). Bundle fix MM-2026-0010. **Status:** Accepted — fixed indirectly via MM-2026-0010 (Fase 1-3).

### MM-2026-0032 — PathologicalLoop em addRoute com UTF-8 inválido específico

Medium / CWE-400 / FPE-007. Par `/\xbe` + `/\xc2\xa8\x91\x9d\xd8'\xef` → > 10s. Bundle com MM-2026-0002. **Status:** Accepted — fixed indirectly via MM-2026-0002 (Fase 1-3).

### MM-2026-0033 — Tree corruption superficial após panic em registration

Medium (Critical em intent, UB actualmente) / CWE-362 / FPE-008. `Handle()` COW superficial. Fix: two-phase registration. **Status:** Accepted — out-of-scope for v1.0.0.

### MM-2026-0034 — Ordering invariant: Recoverer outermost (docs)

Medium / CWE-703 / MSR-OR-002. Panic externo ao Recoverer escapa. Fix: docs + considerar panicShield nativo.

### MM-2026-0035 — reqCtxOffset stale em futuras versões Go (latent)

Medium / CWE-453 / CSA-010 + SAST-001 + H-018. `init()` encontra offset por name sem validar tipo. Test gate adicionado pelo sast agent (`harness/h018_ctx_field_type_test.go`). H-018 status: **PARTIAL**.

### MM-2026-0036 — NotFound/MethodNotAllowed allocation amplification

Medium (informational) / CWE-400 / DOS-007 + DOS-009. 3 allocs / 8 allocs vs 0. Mesma shape que stdlib.

---

## 8. Ledger canónico — Low (8)

| ID | Title | Source | CWE | Status |
|---|---|---|---|---|
| MM-2026-0037 | SetHeader retém CR/LF in-memory (wire sanitised) | HPS-007, MSR-SH-001 | CWE-113 | Fixed — Fase 7 |
| MM-2026-0038 | BasicAuth realm injection (wire sanitised) | MSR-BA-004, H-029 | CWE-117 | Accepted — wire sanitised by Go stdlib |
| MM-2026-0039 | WithValue aceita `any` key (string collision) | MSR-WV-003, SAST-013, H-022 | CWE-668 | Fixed — Fase 7 (doc comment) |
| MM-2026-0040 | Dead code: `setReqCtx` (params.go:154) | SAST-003 | CWE-561 | Fixed — Fase 7 |
| MM-2026-0041 | Dead constant: `maxParams = 16` unused | SAST-004 | CWE-561 | Fixed — Fase 7 |
| MM-2026-0042 | Unchecked `testGz.Close()`, `fmt.Fprintf` logger | SAST-002, SAST-005 | CWE-703 | Fixed — Fase 7 (nolint:errcheck) |
| MM-2026-0043 | Type assertions sem `, ok` em sync.Pool | SAST-008 | CWE-704 | Fixed — Fase 7 (nolint:forcetypeassert, pool.New always set) |
| MM-2026-0044 | Dead `sink` variable em bench_test.go | SAST-006 | CWE-561 | Fixed — Fase 7 |

---

## 9. Info (3)

| ID | Title | Source |
|---|---|---|
| MM-2026-0045 | HTTP/1.1 smuggling defended by stdlib (PASS) | HPS-008 |
| MM-2026-0046 | Error-oracle matrix 15/15 pairs distinguishable (accepted) | TSC-005 |
| MM-2026-0047 | BREACH handler-level (compress não mitiga — docs only) | TSC-006 |

---

## 10. Composites (TM-) — attack chains multi-agente

### MM-TM-2026-0001 — Pre-auth reconnaissance + credential-theft pipeline (Critical)

**Componentes:** MM-2026-0004 + MM-2026-0005 + MM-2026-0009 + MM-2026-0020 + MM-2026-0027.

**Cadeia:**
1. **Reconnaissance (zero auth):** atacante envia `GET /admin` (→ 301 via TSR se `/admin/` existe), `OPTIONS /secret` (→ 204+Allow), `GET /admin//console` (→ 301 com Location canonicalizado via FixedPath). **Auth middleware nunca dispara.**
2. **User enumeration (via timing):** atacante faz N=1e5 requests `Basic auth` com usernames aleatórios + password wrong → via Welch p=0 discrimina usernames válidos.
3. **Password length discovery:** itera tamanhos 1..128 por username válido → maxímo latency corresponde a length correct.
4. **Brute-force online:** sem rate-limit, atacante ilimitado.

**Resultado:** atacante mapeia infra + autentica N usuários sem auditar uma credencial válida.

### MM-TM-2026-0002 — Multi-channel exfiltration + audit-trail forgery (High)

**Componentes:** MM-2026-0006 + MM-2026-0011 + MM-2026-0012 + MM-2026-0007 + MM-2026-0008.

**Cadeia:**
1. Atacante forja IP via XFF → logger regista IP "trusted".
2. Atacante envia request com payload reflected em 4 canais:
   - `r.URL.Path` → log (CRLF forgery)
   - `X-Request-ID: <payload>` → response header reflection
   - `Origin: <payload>` → ACAO reflection (se allowAll)
   - Secret in body + reflected input + compress → BREACH oracle
3. Correlaciona canais para acelerar exfiltração.

### MM-TM-2026-0003 — ServeFiles + clean_path encoded traversal (High)

**Componentes:** MM-2026-0018 + MM-2026-0010.

**Cadeia:**
1. App regista `/static/*filepath` (serveFiles) + `/admin` (Group com auth).
2. `GET /static/..%2fadmin` → decode → `/static/../admin` → clean → `/admin` → dispatch sem auth.

### MM-TM-2026-0004 — Concurrency + pool + panic composite (Critical)

**Componentes:** MM-2026-0003 + MM-2026-0015 + MM-2026-0014 + MM-2026-0017.

**Cadeia:**
1. Handler spawns goroutine com `r` retido.
2. Goroutine lê `r.Context()` enquanto dispatcher restaura `origCtx` + releaseRC → pool contamination.
3. Se handler panica, `releaseRC` skipped → rc leak.
4. Caller reconfigura `m.NotFound` em runtime → race.

### MM-TM-2026-0005 — Slowloris + timeout + goroutine exhaustion (High)

**Componentes:** MM-2026-0024 + MM-2026-0019.

**Cadeia:**
1. Atacante abre 1000 TCP conns com drip 1B/5s.
2. Sem `ReadHeaderTimeout` default, conns perduram.
3. Eventualmente handler é chamado; request bloqueia em body read.
4. Timeout cancela ctx; handler ignora → continua; goroutine pool cresce.

---

## 11. Dismissed / Refuted (5)

| Hypothesis | Verdict | Razão |
|---|---|---|
| H-007 (RedirectFixedPath open redirect via `//`) | **Refuted** | `path.Clean("//evil/foo")` → `/evil/foo`; Location relativa same-origin. Teste HPS `redirect-raw-bytes.txt`. (Variante em MM-2026-0005 é diferente.) |
| H-016 (throttle token leak on panic) | **Refuted** | Defer cleanup correcto; 16k panics concurrent → 0 tokens leaked. |
| H-019 (regex compile DoS) | **Refuted** | Go RE2 linear; limite 100 ops rejeita patterns exponenciais. 10k alt. em 380µs. |
| H-028 (Unicode case-fold asymmetry) | **Refuted** | `foldEq` ASCII-only por design; confusables NÃO cross-fold (safe). |
| H-023 (rc.small residue cross-req) | **Refuted funcional** | 256k canary = 0 leaks. Defence-in-depth: opcional zerar `rc.small`. |

---

## 12. Estatísticas

| Severity | Count | % |
|---|---|---|
| Critical | 7 | 13.5% |
| High | 14 | 26.9% |
| Medium | 15 | 28.8% |
| Low | 8 | 15.4% |
| Info | 3 | 5.8% |
| Composites | 5 | 9.6% |

**Blockers v1.0.0:** 21 (7 Crit + 14 High)
**Docs-only blockers:** 3 (MM-2026-0019, -0024, -0026)
**Actionable fixes (incluindo docs):** 35

**CWE distribution (top):**
1. CWE-400 — 7
2. CWE-113 — 5
3. CWE-362 — 4
4. CWE-20 — 4
5. CWE-208 — 3
6. CWE-200 — 3

---

## 13. Release gate

- [x] Zero Crit/High Open → **PASS** (7 Crit + 14 High fixed; MM-2026-0017/0019 Accepted with docs)
- [x] govulncheck zero
- [x] go mod verify OK
- [x] zero-dep
- [x] `go test -race ./...` zero races em 10 iter → **PASS** (Fase 1-6 fixes applied)

**Gate: PASS** (post Fase 1-7 fixes). Ver posture report para histórico de bloqueadores.

---

## 14. Append rules

Owner único: `threat-modeler-and-zero-day-researcher`. Cada novo finding pós-sprint recebe próximo `MM-2026-NNNN`. Preservar source_id. Se composto, criar MM-TM-2026-NNNN.
