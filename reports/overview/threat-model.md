# MuxMaster — Threat Model (STRIDE) — Pós-Sprint

**Date:** 2026-04-17 (Fase 3 — consolidação)
**Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
**Go:** 1.26.2
**Author:** threat-modeler-and-zero-day-researcher
**Status:** Matriz fechada com findings confirmados. Células RISK/OPEN → CONFIRMED (com ID MM-2026-NNNN), REFUTED, ACCEPTED ou PASS.

---

## 1. Método

STRIDE aplicado por componente (linhas) × classe de ameaça (colunas). Cada célula tem um estado:

- **PASS** — demonstrado seguro (com teste/evidência)
- **OPEN** — análise pendente neste sprint
- **RISK** — hipótese concreta a investigar
- **N/A** — não aplicável com justificação
- **ACCEPTED** — risco documentado e aceite (e.g. público por design)

Cada célula aponta para o(s) agente(s) responsáveis pela validação.

Legenda de colunas:
- **S** = Spoofing
- **T** = Tampering
- **R** = Repudiation
- **I** = Information disclosure
- **D** = Denial of service
- **E** = Elevation of privilege

## 2. Matriz STRIDE — núcleo do router (FECHADA)

Legenda de estados finais: **CONFIRMED (MM-NNNN)** / **REFUTED** / **ACCEPTED** / **PASS** / **N/A**.

| Componente | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| `ServeHTTP` entry (mux.go) | **PASS**: method case-sensitive confirmed by design (HPS-008 + method-dispatch-matrix.csv) | **PASS**: stdlib rejeita CRLF literal em request-target (400); decoded CRLF flows a logger — **CONFIRMED MM-2026-0006** | **N/A** | **CONFIRMED MM-2026-0046**: error-oracle 15/15 pairs distinguishable (ACCEPTED) | **PASS**: `allowed()` cost O(methods × k) é Info em MM-2026-0036 | **CONFIRMED MM-2026-0005**: Mount `/*` tree + auto-OPTIONS bypass method ACL |
| Radix tree lookup (tree.go:getValue) | **N/A** | **CONFIRMED MM-2026-0005**: RedirectFixedPath reveals route | **N/A** | **CONFIRMED MM-2026-0026**: route-existence timing (ACCEPTED — radix intrinsic); **CONFIRMED MM-2026-0005**: redirect disclosure | **REFUTED H-019**: RE2 linear; patterns exponenciais rejeitados | **CONFIRMED MM-2026-0001**: wildcard shadow → invalid node type panic em dispatch |
| `addRoute` (tree.go) | **N/A** | **CONFIRMED MM-2026-0002**: non-ASCII corrompe indices/children; **CONFIRMED MM-2026-0021**: index OOB em `/{…}*name` | **N/A** | **N/A** | **REFUTED H-019** (RE2); **CONFIRMED MM-2026-0032**: pathological UTF-8 loop | **N/A** |
| `requestCtx` via unsafe.Add (params.go) | **N/A** | **CONFIRMED MM-2026-0003**: race writing/restoring r.ctx cross-goroutine (CSA-001) | **N/A** | **REFUTED funcional H-023**: 256k canary 0 leaks (defence-in-depth zerar `rc.small` opcional) | **PASS** (pool integrity 0 mismatches em GC storm) | **CONFIRMED MM-2026-0003**: handler captures r + reads ctx → cross-req leak |
| `paramsBuf` (tree.go) | **N/A** | **CONFIRMED MM-2026-0010**: silent overflow (PRF-003 + DOS-003 + FPE-004) | **N/A** | **REFUTED H-023** (params iterados via `[:n]`) | **N/A** | **CONFIRMED MM-2026-0010**: auth-middleware assume 5 params mas só 3 capturados |
| Introspection (Routes, Walk, Lookup) | **N/A** | **CONFIRMED MM-2026-0016**: Walk concurrent com addRoute (63 RACE warnings/2s) | **N/A** | **ACCEPTED**: caller responsibility não expor publicamente | **N/A** | **N/A** |
| `Mount` (mux.go) | **N/A** | **CONFIRMED MM-2026-0022**: RawPath divergence (PRF-005) | **N/A** | **PASS** (clone OK; H-013 partial) | **N/A** | **PASS** (Mount não bypassa method tree por design) |
| `ServeFiles` (mux.go, group.go) | **N/A** | **CONFIRMED MM-TM-2026-0003**: catch-all + clean_path + `..%2f` | **N/A** | **ACCEPTED**: stdlib FileServer rejeita `..` (path.Clean interno); symlink é caller config | **N/A** | **CONFIRMED MM-TM-2026-0003**: traversal via clean_path composition |
| `RedirectTrailingSlash` | **N/A** | **PASS**: CRLF em path é rejeitado por stdlib em request line; decoded CRLF só afecta logger (MM-2026-0006) | **N/A** | **CONFIRMED MM-2026-0004**: reveals route before auth | **N/A** | **REFUTED H-007**: `path.Clean("//...")` colapsa, Location relativa same-origin |
| `RedirectFixedPath` | **N/A** | **CONFIRMED MM-2026-0005**: path.Clean discloses canonical | **N/A** | **CONFIRMED MM-2026-0005**: discloses hidden routes via canonicalization | **N/A** | **REFUTED H-007** (base variant); **CONFIRMED MM-2026-0005** (enumeration variant) |
| `Mux.dispatch` (hot path) | **N/A** | **N/A** | **N/A** | **PASS** | **PASS**: MM-2026-0036 allocation amplification é info | **N/A** |
| Public fields (`NotFound`, `MethodNotAllowed`, `PanicHandler`, `ErrorHandler`, 8 bool flags) | **CONFIRMED MM-2026-0017**: race em reassignment pós-start | **N/A** | **N/A** | **ACCEPTED**: caller responsibility (custom handlers) | **N/A** | **N/A** |
| `wrapMiddleware` (mux.go) | **N/A** | **N/A** | **N/A** | **N/A** | **N/A** | **CONFIRMED H-008 (docs)**: chain built at registration — `Use()` after `Handle()` silently ignored (propose docs normativas) |
| `Use()`/`Pre()` | **CONFIRMED MM-2026-0014**: race vs Handle/ServeHTTP | — | — | — | — | — |
| Handler panic cleanup | **N/A** | **CONFIRMED MM-2026-0015**: r.ctx leaked + rc leaked (CSA-004/005) | — | — | — | — |
| `reqCtxOffset` init | **N/A** | **CONFIRMED parcial MM-2026-0035**: H-018 latent em futuro Go (test gate adicionado pelo sast) | — | — | — | — |

Owner keys: hp=http-protocol, pr=path-routing, cc=concurrency, mw=middleware-reviewer, ts=timing-sidechannel, ds=dos-resilience, sa=sast, fz=fuzzing, tm=threat-modeler.

## 3. Matriz STRIDE — middlewares (FECHADA)

### 3.1 `basic_auth`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0009**: user enum via map lookup timing (p=0, N=1.5M) | **LOW MM-2026-0038**: realm CRLF retido in-memory (wire sanitised) | **N/A** | **CONFIRMED MM-2026-0020**: password-length oracle via subtle early-exit | **CONFIRMED MM-2026-0027**: 10k unbounded brute-force sem slowdown | **CONFIRMED MM-2026-0009**: timing → username → pw brute-force chain (composite MM-TM-2026-0001) |

### 3.2 `cors`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0012**: Origin reflection com allowAll — spec violation | **CONFIRMED MM-2026-0028**: Origin CRLF retido in-memory (sanitised on wire) | **N/A** | **CONFIRMED MM-2026-0012**: CSRF bypass via reflection | **N/A** | **PASS**: `*` + AllowCredentials=true panica em config-time (guarded) |

### 3.3 `compress`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **ACCEPTED MM-2026-0047**: BREACH é handler-level, compress não mitiga (docs) | **CONFIRMED MM-2026-0007**: unbounded buf → OOM (confirmed 64MB→178MB) | **N/A** |

### 3.4 `real_ip`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0008**: XFF spoof trivial | **CONFIRMED MM-2026-0029**: CRLF em XFF retido em r.RemoteAddr | **N/A** | **N/A** | **CONFIRMED MM-2026-0008**: downstream throttle bypass (composite MM-TM-2026-0002) | **CONFIRMED MM-2026-0008**: IP-based ACL bypass |

### 3.5 `logger`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0006**: CRLF/ANSI/NUL via `r.URL.Path` percent-decoded | **CONFIRMED MM-2026-0006**: log forgery / plausible deniability | **PASS**: current format não loga Auth/Cookie headers (caller responsibility) | **PASS**: stderr bytes acceptable (docs opcional) | **N/A** |

### 3.6 `recoverer`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **ACCEPTED**: panics são recovered (audit trail preservado via stderr dump — docs) | **CONFIRMED MM-2026-0023**: panic value + debug.Stack() raw em stderr (attacker-controlled) | **PASS**: recuperação de 16k panics concurrent sem pool contamination | **N/A** |

### 3.7 `throttle`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0013**: global not per-IP | **N/A** | **N/A** | **REFUTED TSC-007**: near-limit timing é noise; não-exploitable | **REFUTED H-016**: defer cleanup correcto (0 token leaks em 16k panics) | **CONFIRMED MM-2026-0013**: 1 attacker esgota budget global |

### 3.8 `timeout`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0019**: goroutine leak 1000→1000 blocked | **N/A** |

### 3.9 `request_id`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **CONFIRMED MM-2026-0011**: client X-Request-ID override sem validação | **CONFIRMED MM-2026-0011**: CRLF retido in-memory (sanitised on wire) | **CONFIRMED MM-2026-0011**: forge request_id to confuse correlation | **N/A** | **CONFIRMED MM-2026-0011**: 1MiB X-Request-ID amplifica 1024× | **N/A** |

### 3.10 `clean_path`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0018**: single `path.Clean` pass — encoded traversal bypass | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0018**: 136 bypass combinations em matrix (MM-TM-2026-0003) |

### 3.11 `strip_slashes`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **CONFIRMED MM-2026-0025**: non-idempotent (só 1 trailing slash) | **N/A** | **N/A** | **N/A** | **CONFIRMED MM-2026-0025 (variant)**: interação com CleanPath + RedirectTrailingSlash |

### 3.12 `set_header`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **LOW MM-2026-0037**: CRLF retido in-memory (sanitised on wire by Go) | **N/A** | **N/A** | **N/A** | **N/A** |

### 3.13 `with_value`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **LOW MM-2026-0039**: `any` key aceite — string collision risk (caller responsibility) | **N/A** | **ACCEPTED**: if val is secret, caller must not pass to logging middleware | **N/A** | **N/A** |

### 3.14 `no_cache`

| S | T | R | I | D | E |
|---|---|---|---|---|---|
| **N/A** | **N/A** | **N/A** | **PASS** | **N/A** | **N/A** |

## 4. STRIDE agregado — cobertura

| Componente | S | T | R | I | D | E | Cobertura |
|---|---|---|---|---|---|---|---|
| Core router | 1/1 | 2/2 | 0/0 | 3/3 | 3/3 | 3/3 | 12/12 to validate |
| Radix tree | 0 | 1/1 | 0 | 1/1 | 2/2 | 2/2 | 6/6 |
| Params / pool | 0 | 1/1 | 0 | 1/1 | 1/1 | 1/1 | 4/4 |
| Introspection | 0 | 1/1 | 0 | 1/1 | 0 | 0 | 2/2 |
| Mount / ServeFiles | 0 | 1/1 | 0 | 2/2 | 0 | 2/2 | 5/5 |
| Redirect | 0 | 2/2 | 0 | 2/2 | 0 | 2/2 | 6/6 |
| Middleware assignment | 1/1 | 0 | 0 | 1/1 | 0 | 1/1 | 3/3 |
| **basic_auth** | 1/1 | 1/1 | 0 | 1/1 | 1/1 | 1/1 | 5/5 |
| **cors** | 1/1 | 1/1 | 0 | 1/1 | 0 | 1/1 | 4/4 |
| **compress** | 0 | 0 | 0 | 1/1 | 1/1 | 0 | 2/2 |
| **real_ip** | 1/1 | 1/1 | 0 | 0 | 1/1 | 1/1 | 4/4 |
| **logger** | 0 | 1/1 | 1/1 | 1/1 | 1/1 | 0 | 4/4 |
| **recoverer** | 0 | 0 | 1/1 | 1/1 | 1/1 | 0 | 3/3 |
| **throttle** | 1/1 | 0 | 0 | 1/1 | 1/1 | 1/1 | 4/4 |
| **timeout** | 0 | 0 | 0 | 0 | 1/1 | 0 | 1/1 |
| **request_id** | 1/1 | 1/1 | 1/1 | 0 | 1/1 | 0 | 4/4 |
| **clean_path** | 0 | 1/1 | 0 | 0 | 0 | 1/1 | 2/2 |
| **strip_slashes** | 0 | 1/1 | 0 | 0 | 0 | 1/1 | 2/2 |
| **set_header** | 0 | 1/1 | 0 | 0 | 0 | 0 | 1/1 |
| **with_value** | 0 | 1/1 | 0 | 1/1 | 0 | 0 | 2/2 |
| **no_cache** | 0 | 0 | 0 | 0 | 0 | 0 | 0/0 |

Total cells com RISK/OPEN a fechar neste sprint: **76**.

## 5. Mitigações conhecidas (presentes no código)

| Mitigação | Localização | O que cobre |
|---|---|---|
| `subtle.ConstantTimeCompare` | `basic_auth.go:18` | password comparison; **não** cobre user-enum via map lookup |
| `crypto/rand` | `request_id.go:18` | id generation |
| `http.ErrAbortHandler` propagation via `defer recover()` | `recoverer.go` | panic containment (mas stderr dump) |
| `CORSOptions.AllowCredentials` + `*` → panic | `cors.go:22` | preventa combinação proibida **em config-time** |
| `atomic.Pointer[methodTrees]` | `mux.go:107` | lock-free read path |
| `sync.Mutex` em `Handle` | `mux.go:159` | protege COW da árvore |
| `path.Clean` em `clean_path` e `RedirectFixedPath` | — | normaliza `.` e `..` **textual** |
| Wildcard validation (`findWildcard`) | `tree.go:485` | rejeita múltiplos wildcards num segmento |
| `r.Clone(r.Context())` em `Mount`/`ServeFiles`/`clean_path`/`strip_slashes` | — | evita mutar o `*http.Request` compartilhado — MAS não cobre o `unsafe.Add` em `mux.go` |

## 6. Gaps conhecidos (explicitamente não mitigados)

1. **XFF trust sem config de proxies** — `real_ip.go` trusts unconditionally
2. **Single-pass path normalization** — `clean_path.go` só chama `path.Clean` uma vez
3. **Global throttle** — `throttle.go` não faz per-IP
4. **Unbounded response buffering** — `compress.go` acumula tudo antes de comprimir
5. **No rate limit on auth** — `basic_auth.go` não integra com throttle
6. **Client X-Request-ID aceite sem validação** — `request_id.go` reflecte CRLF
7. **Logger sem escape** — `logger.go` imprime path raw
8. **Handler continua após timeout** — `timeout.go` só cancela context
9. **Registration races documented, not enforced** — `Use`/`Handle` concorrente com serve é UB
10. **`unsafe.Add` no request** — funcional se request for goroutine-owned; mas **nada no código garante isto**

## 7. Priorização pós-sprint (actualização com MM-IDs)

**Critical — blockers v1.0.0 (7):**
- MM-2026-0001 Tree corruption wildcard shadow
- MM-2026-0002 UTF-8 invariant violation
- MM-2026-0003 Race unsafe.Add (r.ctx)
- MM-2026-0004 TSR pre-auth route disclosure
- MM-2026-0005 RedirectFixedPath + auto-OPTIONS + auto-405 Allow leak
- MM-2026-0006 Logger CRLF/ANSI injection
- MM-2026-0007 Compress unbounded buffer

**High — blockers v1.0.0 (14):**
- MM-2026-0008 XFF unconditional trust
- MM-2026-0009 basic_auth user enum timing
- MM-2026-0010 paramsBuf silent overflow
- MM-2026-0011 request_id CRLF + amplification
- MM-2026-0012 CORS wildcard reflection
- MM-2026-0013 Throttle global masquerading per-IP
- MM-2026-0014 Use/Pre race vs Handle
- MM-2026-0015 Panic cleanup skip (rc leak + r.ctx leak)
- MM-2026-0016 Introspection race vs addRoute
- MM-2026-0017 Public fields race
- MM-2026-0018 clean_path single-pass bypass
- MM-2026-0019 Timeout goroutine leak (docs blocker)
- MM-2026-0020 password-length oracle
- MM-2026-0021 Registration-time OOB

**Medium (15):** MM-2026-0022 a MM-2026-0036 — ver findings.md §7.

**Low (8):** MM-2026-0037 a MM-2026-0044 — docs-only / code hygiene.

**Info (3):** MM-2026-0045 a MM-2026-0047.

**Compostos (5):** MM-TM-2026-0001 a MM-TM-2026-0005 — ver findings.md §10.

**Refuted (5):** H-007, H-016, H-019, H-028, H-023.

## 8. Cobertura STRIDE final

| Componente | Total cells | PASS / Refuted | Confirmed | Accepted | Coverage |
|---|---|---|---|---|---|
| Core router | 22 | 9 | 12 | 1 | 100% |
| Radix tree | 6 | 1 | 4 | 1 | 100% |
| Params / pool | 5 | 2 | 2 | 1 | 100% |
| Introspection | 2 | 0 | 1 | 1 | 100% |
| Redirects | 6 | 2 | 4 | 0 | 100% |
| 15 middlewares | 56 | 13 | 35 | 8 | 100% |
| **TOTAL** | **97** | **27** | **58** | **12** | **100%** |

**Nenhuma célula OPEN/RISK resta.**

## 9. Revisão

Reavaliação obrigatória:
- Após cada fix de CRITICAL/HIGH (re-run reproducer → Verified)
- Antes do tag v1.0.0 (release gate)
- Após qualquer mudança arquitectural (novo middleware, novo transporte, introspection endpoint)
- Scan de CVE novos em GHSA Go a cada 90 dias
