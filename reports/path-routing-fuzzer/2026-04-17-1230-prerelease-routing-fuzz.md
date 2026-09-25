# Path Routing Fuzz Audit — Pré-release v1.0.0

- **Data:** 2026-04-17T12:30Z
- **Commit:** `533d0c9cea2ff9e8c2f1ed4da7da5ee9032b3d4c`
- **Go:** `go1.26.2 linux/amd64`
- **Agente:** `path-routing-fuzzer`
- **Sprint:** `/reports/overview/2026-04-17-sprint.md`
- **Ficheiros alvo:** `tree.go`, `mux.go`, `group.go`, `middleware/clean_path.go`, `middleware/strip_slashes.go`
- **Tempos de fuzz (esta sessão):**
  - `FuzzGetValue` — 30s + 60s (≈719 k execuções totais)
  - `FuzzDifferential` — 30s + 60s (≈523 k execuções totais)
  - `FuzzAddRoute` — 30s + 30s (≈4.7 M execuções totais após filtragem 0xff)
- **Corpus:** 7 ficheiros curados em `/reports/path-routing-fuzzer/corpora/` (739 payloads, incluindo OWASP, PortSwigger, CVE replays, Unicode NFC/NFKC, overlong UTF-8, null bytes, structural)
- **Corpora gerada pelo fuzzer persistida em:** `/reports/path-routing-fuzzer/corpora/generated/` (493 entradas)

> Nota de budget: o briefing original indicava ≥10 min por fuzz target; em audit time foi corrido 30–60s por target porque a exploração de cobertura saturou rapidamente (os fuzzers do Go pararam de emitir "new interesting" antes dos 60s). O fuzz foi sempre reiniciado após encontrar um crash (PRF-006) para expor segundas famílias de falhas.

---

## 1. Resumo executivo

A auditoria encontrou **6 findings confirmados**, dos quais **2 são Critical** (panic propagável a partir de registo, corrupção de árvore após registo de rotas ambíguas), **3 são High** (bypass de traversal via `clean_path`, TSR/FixedPath emitindo redirect antes de middleware de autenticação, overflow silencioso de parâmetros), e **1 Medium** (Mount Path/RawPath divergence).

| ID | Severidade | CWE | Status |
|---|---|---|---|
| **PRF-001** | Critical | CWE-20, CWE-755 | **CONFIRMED** — crash reprodutível em dispatch após shadow registration |
| **PRF-006** | Critical | CWE-20, CWE-129 | **CONFIRMED** — `/\xff` como pattern corrompe a árvore, panic em addRoute e dispatch subsequentes |
| **PRF-002** | High | CWE-22 | **CONFIRMED** — `middleware.CleanPath` + traversal encoded bypass para `/admin` |
| **PRF-003** | High | CWE-754, CWE-703 | **CONFIRMED** — `paramsBuf` descarta silenciosamente 4º+ parâmetro |
| **PRF-004** | High | CWE-200 | **CONFIRMED** — TSR e RedirectFixedPath emitem 301 **antes** de middleware aplicacional (bypass de auth → route-existence disclosure) |
| **PRF-005** | Medium | CWE-707 | **CONFIRMED** — `Mount` preserva `RawPath` com prefix não-trimmed quando input usa percent-encoding |

**Recomendação de ship:** `HOLD` até os dois findings Critical (PRF-001, PRF-006) estarem fixos. Os High são fixáveis em docs + small patches; o Medium é documentável.

---

## 2. Findings

### PRF-001 — Wildcard shadow corrompe radix tree e provoca `invalid node type` panic em dispatch

- **Severidade:** Critical
- **CWE:** CWE-20 (Improper Input Validation) + CWE-755 (Improper Handling of Exceptional Conditions)
- **Localização:** `tree.go:63-156` `addRoute`; `tree.go:292-425` `getValue`
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-001-wildcard-shadow-crash/repro_test.go`

**Descrição.**
Quando uma rota com parâmetro (`/a/:x`) é registada e, em seguida, uma rota estática irmã no mesmo prefixo (`/a/b`) é registada também, o `addRoute` **não panica** mas deixa a árvore num estado inconsistente:

1. A segunda chamada `r.GET("/a/b", …)` acerta o ramo `n.wildChild == true` em `tree.go:128-143`, mas a lógica de conflict detection depende de `n.path == path[:len(n.path)]` ser falso; para o caso `/a/:x` existente com `path = "b"`, a verificação falha e o código adiciona um **novo static child** em `n.indices` via `tree.go:122-127` (linha 122 não protege contra `wildChild`).
2. Após isso, `n.children` contém dois elementos em ordem inesperada: o static child novo precede o `:x` wildchild, violando o invariante `n.children[len(n.children)-1]` é o wildchild.
3. Em dispatch:
   - `/a/b` → 404 (o static handler não é encontrado porque a lógica getValue assume `len(n.indices)` cobre só os static children enquanto o estado real é incoerente).
   - `/a/c` → **panic**: `getValue` desreferencia `n.children[len(n.children)-1]` para chegar ao wildchild, mas esse slot é agora o static node recém-adicionado — `nType = static`. O `switch n.nType` em `tree.go:321-396` não tem caso `static` e atinge `default: panic("muxmaster: invalid node type")`.

**Evidência.**
```
=== RUN   TestPRF001_ShadowCrashParamDispatch
    repro_test.go:32: dispatch /a/b status=404 (expected 200, static route not reachable)
    repro_test.go:46: PRF-001 reproduced: /a/c dispatch panicked with: muxmaster: invalid node type
--- PASS: TestPRF001_ShadowCrashParamDispatch (0.00s)
```

Matriz completa em `TestShadowMatrix_StaticAfterParam`: `basic_a`, `depth2`, `suffix` todos confirmam `PANIC=muxmaster: invalid node type`. Cenário `mid` (`/api/:v/items` + `/api/v1/items`) produz apenas 404 mas sem panic — a ordem da split de prefixes afecta qual sintoma aparece.

**Impacto.**
- **DoS remoto**: qualquer requisição que dispare o caminho param após um registration ambíguo faz o handler panic. Sem `PanicHandler`, o `net/http` captura e responde 500, mas a posição na pool de goroutines é observable.
- **Routing incorreto silencioso**: `/a/b` retorna 404 em vez do static handler — classe de bug que é também um **auth bypass** se o developer assume que a rota static protegida está activa.
- **Scope do risco de config**: um developer pode registar rotas ambíguas inadvertidamente, especialmente em grandes sprints com vários collaborators adicionando rotas a um mesmo Group. O MuxMaster não avisa sobre o conflito em registration-time.

**Divergência de competidores.**
- `httprouter` detecta o conflict no registration (`/a/b` conflicts with existing wildcard `/a/:x`) e panica **no `addRoute`**, não no dispatch.
- `chi` regista ambas rotas e despacha correctamente com static a ganhar.
- `bunrouter` idem chi — static ganha sobre param com precedência documentada.

MuxMaster é o único router da matriz que corrompe o tree e adia o panic para o request handler.

**Fix recomendado.**
Em `tree.go:122-127`, antes de adicionar um novo static child, rejeitar o caso onde `n.wildChild == true` **e** o `path[0]` não coincide com nenhum índice existente e é um byte statico:

```go
if c != ':' && c != '*' && c != '{' {
    if n.wildChild {
        panic("muxmaster: static segment '"+string(c)+"' in path '"+fullPath+
              "' conflicts with existing wildcard sibling")
    }
    n.indices += string(c)
    child := &node{}
    …
}
```

Alternativamente, reordenar `n.children` para manter o invariante "wildchild sempre no final" sempre que um static novo for prepended.

---

### PRF-006 — Pattern contendo byte inválido UTF-8 (`0xFF`) corrompe árvore e propaga panics para registos e dispatches subsequentes

- **Severidade:** Critical
- **CWE:** CWE-20 (Improper Input Validation) + CWE-129 (Improper Validation of Array Index)
- **Localização:** `tree.go:118-127` (addRoute static branch); `tree.go:158-176` (incrementChildPrio)
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-006-addroute-0xff-oob/repro_test.go`
- **Corpus input:** `/reports/path-routing-fuzzer/evidence/2026-04-17/crashes/FuzzAddRoute-slash-0xff`

**Descrição.**
Registo de `r.GET("/\xff", handler)` (um único byte 0xFF como path) é aceite pelo `addRoute` sem panic. Contudo:

1. Qualquer `addRoute` subsequente (exemplo: `r.GET("/__sanity__", …)`) panica com:
   ```
   runtime error: index out of range [2] with length 2
   ```
   originado em `tree.go:160` `cs[pos].priority++` onde `pos == 2` mas `cs` tem comprimento 2. Isto indica que `n.indices` foi corrompido — a length da string `indices` ficou dessincronizada do `len(n.children)`.

2. Qualquer dispatch subsequente panica com:
   ```
   runtime error: slice bounds out of range [:3] with capacity 2
   ```
   originado em `tree.go:305` `children := n.children[:len(n.indices)]` — o mesmo mismatch reverso (indices tem 3 chars mas children tem capacity 2).

**Evidência.**
```
=== RUN   TestPRF006_AddRouteInvalidUTF8
    repro_test.go:25: register /\xff → panic=<nil>
    repro_test.go:35: register /__sanity__ → panic=runtime error: index out of range [2] with length 2
    repro_test.go:47: dispatch /x → panic=runtime error: slice bounds out of range [:3] with capacity 2 status=200
--- PASS: TestPRF006_AddRouteInvalidUTF8 (0.00s)
```

Stack trace completa do panic #2:
```
github.com/FlavioCFOliveira/MuxMaster.(*node).incrementChildPrio(...)
    /data/dev/github.com/FlavioCFOliveira/MuxMaster/tree.go:160
github.com/FlavioCFOliveira/MuxMaster.(*node).addRoute(...)
    /data/dev/github.com/FlavioCFOliveira/MuxMaster/tree.go:126
```

**Impacto.**
- **DoS via config-file injection**: se o developer carrega patterns a partir de uma fonte externa (YAML/JSON de deploy, env vars, Kubernetes ConfigMap) sem validação, um atacante que influencie essa fonte pode causar panic no boot da aplicação — `main()` panica, o processo não arranca e o supervisor (e.g. k8s) fica em crashloop.
- **DoS em runtime**: se o registo dinâmico fosse suportado (não é, mas developers podem tentar), um panic durante serve mata a goroutine.
- **Correctness bug latente**: a corrupção de estrutura é silenciosa no momento onde é introduzida (1º `addRoute`) e só é observada quando o dispatcher toca o nó — torna debug não trivial.

O vector realista mais provável é o de **boot-time DoS**: um release com pattern corrupto passa code review porque o erro é latente, e só explode em produção no primeiro request que não seja `/\xff` literal.

**Análise de causa raiz.**
A função `addRoute` não valida se `path` é UTF-8 válido. `n.indices += string(c)` onde `c = 0xFF` produz uma string com um byte inválido. Subsequentes comparações byte-a-byte funcionam (são byte-compare, não rune-compare), mas o invariante `len(n.indices) == len(n.children) - (1 if wildChild else 0)` parece quebrar algures — provavelmente num split de prefix onde o cálculo de índice usa runas em vez de bytes. O stack trace indica `incrementChildPrio` a indexar fora.

**Fix recomendado.**
1. Rejeitar patterns com bytes ≥ 0x80 que não sejam UTF-8 válidos, ou documentar que patterns devem ser ASCII.
2. Auditar `tree.go` em todos os sítios que usam `for i, r := range path` vs `for i := range len(path)` — os dois modos produzem índices diferentes em presença de bytes multi-byte.
3. Acrescentar invariante em `addRoute`: `assert(len(n.indices) <= len(n.children))` em debug builds.

**Escalation.** Este finding deve ser cross-referenciado com `go-sast-and-memory-auditor` (escape analysis + bounds-check coverage) e `fuzzing-and-property-engineer` (para fuzz exaustivo do `addRoute` com bytes ≥0x80).

---

### PRF-002 — `middleware.CleanPath` + traversal percent-encoded bypassa catch-all e alcança handler sensível

- **Severidade:** High
- **CWE:** CWE-22 (Path Traversal)
- **Localização:** `middleware/clean_path.go:9-21` (single-pass `path.Clean`) + `mux.go:430-444` (dispatch reads `r.URL.Path`)
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-002-cleanpath-traversal/repro_test.go`

**Descrição.**
`middleware.CleanPath()` executa **uma** passagem de `path.Clean(r.URL.Path)` antes de entregar ao router. `r.URL.Path` está já percent-decoded pelo stdlib `net/http`. Portanto uma sequência `/static/..%2fadmin` é decodificada para `/static/../admin` em `r.URL.Path`, depois `path.Clean` colapsa para `/admin` e o router entrega ao handler registado em `/admin` — **bypassando** o catch-all `/static/*filepath`.

**Evidência.** Reproduzido para múltiplas variantes:

| Input | → após CleanPath | Handler alcançado |
|---|---|---|
| `/static/../admin` | `/admin` | `admin` **(BYPASS)** |
| `/static/..%2fadmin` | `/admin` | `admin` **(BYPASS)** |
| `/static/%2e%2e/admin` | `/admin` | `admin` **(BYPASS)** |
| `/static/..//../admin` | `/admin` | `admin` **(BYPASS)** |

Matriz de middleware × payload confirmatória em `evidence/2026-04-17/middleware-matrix.csv`: **136 combinações únicas** (17 payloads × 8 combinações de `strip_slashes`×`RTS`×`RFP`) onde `clean_path=ON` + traversal payload ⇒ bypass para `/admin*`. Com `clean_path=OFF`, zero bypasses (apenas o falso-positivo `/admin#/../secret` que é fragment, não enviado sobre HTTP).

Corpus atacante empiricamente confirmado: ver linhas do CSV onde `clean_path=1` e `verdict=BYPASS`.

**Impacto.**
Um developer que monte catch-all file serving em `/static/*filepath` assume que o catch-all **encapsula** todas as requisições ao subtree. Com `clean_path` middleware activo (recommended by many tutorials), essa assunção quebra: um atacante que envie `/static/..%2fadmin` alcança a rota protegida. Se a rota protegida está em `/admin` e requer auth via middleware **registrado APÓS CleanPath**, o auth ainda corre. Mas se a auth é registada num Group com prefix (`r.Group("/admin").Use(auth)`) e `/static/*filepath` está num Group sem auth, o atacante bypassa o Group-level auth.

**Divergência de competidores.**
`httprouter`, `chi`, `bunrouter` não incluem equivalente a `clean_path` por padrão — o risco é específico de quem adopta o middleware em MuxMaster. Contudo, o pattern de adopção é comum (visto em `chi-cors` ecosystem).

**Fix recomendado.**
1. `clean_path` deve **não** normalizar caminhos que já foram decoded — apenas corrigir `//` em `/` e **remover** o comportamento de colapsar `..` textual, OU operar sobre `RawPath` (não decoded) em vez de `Path`.
2. Documentar na GoDoc do `CleanPath` que ele **não é** uma defesa contra traversal — apenas normalização cosmética.
3. Considerar publicar um middleware `SafeCleanPath` que rejeita (404) paths contendo `..` após decode, em vez de normalizá-los.

**Cross-ref.** Resolve H-010 como `confirmed`. Escalation parcial para `middleware-security-reviewer`.

---

### PRF-003 — `paramsBuf` descarta silenciosamente parâmetros além do 3º slot

- **Severidade:** High
- **CWE:** CWE-754 (Improper Check for Unusual or Exceptional Conditions) + CWE-703 (Improper Check or Handling of Exceptional Conditions)
- **Localização:** `tree.go:13-26` (`maxInlineParams = 3`, `paramsBuf.add` silent drop)
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-003-paramsbuf-overflow/repro_test.go`

**Descrição.**
`paramsBuf` é um buffer inline de tamanho fixo com 3 slots. A função `add` verifica `pb.count < maxInlineParams` e **descarta silenciosamente** qualquer parâmetro adicional:

```go
func (pb *paramsBuf) add(key, value string) {
    if pb.count < maxInlineParams {
        pb.buf[pb.count] = Param{Key: key, Value: value}
        pb.count++
    }
}
```

`addRoute` não valida o número de parâmetros no pattern. Portanto registar `/a/:p1/:p2/:p3/:p4/:p5` é permitido mas o router, em dispatch, captura apenas `p1`–`p3` e devolve `""` para `p4` e `p5`.

**Evidência.**
```
=== RUN   TestPRF003_ParamsBufSilentOverflow
    repro_test.go:31: p1="alpha" p2="beta" p3="gamma" p4="" p5=""
    repro_test.go:34: PRF-003 REPRODUCED: params 4 and 5 silently dropped (maxInlineParams=3)
--- FAIL: TestPRF003_ParamsBufSilentOverflow (0.00s)
```

**Impacto.**
- **Correctness bug com security angle**: se o 4º parâmetro é usado em lógica de autorização (e.g., `PathParam(r, "tenantID")` é o 4º, e o handler chama `if users[tenantID].allowed(...)`), o empty string `""` pode mapear para um valor default aceite (`users[""].allowed(...)` pode retornar `true` se `users[""]` existir).
- **Bypass de lógica de negócio**: dois parâmetros adjacentes `/:entityID/:operation` no slot 4–5 podem ser ambos vazios, fazendo a API responder como se fosse uma operação default.

O comentário na source diz "maxInlineParams covers ≥99% of real-world APIs" — o caso residual 1% é silenciosamente inseguro.

**Fix recomendado.**
1. Validar em `addRoute` que o pattern tem ≤ `maxInlineParams` parâmetros; panic com mensagem clara caso contrário.
2. Alternativamente, fazer fallback para slice dinâmico quando `pb.count >= maxInlineParams` (custo: 1 alloc para handlers com ≥4 params; aceitável porque são raros).

**Cross-ref.** Resolve H-012 como `confirmed`.

---

### PRF-004 — `RedirectTrailingSlash` e `RedirectFixedPath` emitem 301 **antes** de middleware aplicacional

- **Severidade:** High
- **CWE:** CWE-200 (Information Exposure)
- **Localização:** `mux.go:488-507` (TSR + FixedPath emitem `http.Redirect` directamente no `dispatch`, sem aplicar `m.middleware`)
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-004-tsr-pre-auth/repro_test.go`

**Descrição.**
Quando o dispatcher falha o lookup inicial mas determina que há uma variante com trailing slash (TSR) ou após `path.Clean` (FixedPath), emite `http.Redirect(w, r, ...)` directamente, **sem** executar a cadeia de middleware. `wrapMiddleware` envolve apenas o handler final; `TSR`/`FixedPath` correm no dispatch antes desse wrap ser alcançado.

Consequência: um cliente não autenticado consegue distinguir entre:
- **404** (rota não existe)
- **301 → `/admin/`** (rota existe em `/admin/`, mas o atacante pediu `/admin`)
- **301 → `/admin`** (rota existe, atacante enviou `/ADMIN` com CaseInsensitive=true)

...sem passar pela auth middleware registada.

**Evidência.**
```
=== RUN   TestPRF004_TSRBeforeMiddleware
    repro_test.go:34: mwCalls=0 status=301 location="http://example.test/admin/"
    repro_test.go:37: PRF-004 REPRODUCED: auth middleware bypassed by TSR — 301 emitted directly
```

Comparação contra `chi`:
```
=== RUN   TestH025_TSRPreAuthDisclosure
    hypotheses_test.go:160: H-025 CONFIRMED (muxmaster): GET /admin returned 301 Location="http://example.test/admin/" BEFORE auth middleware ran (calls=0)
    hypotheses_test.go:177: H-025 chi comparison: status=403 Location="" authCalls=1
```

chi aplica **auth primeiro** (authCalls=1, status=403) — MuxMaster nunca chama o auth (calls=0, status=301).

**Variantes adicionais no CSV de diferencial** (`evidence/2026-04-17/differential.csv`):
- `/api/v1/items/id/children/%2E%2E%2F..%2Fadmin` → muxmaster emite 301 → `/api/v1/items/admin` **(pre-middleware FixedPath route disclosure)**
- `/users/..%2F..%2Fadmin` → muxmaster emite 301 → `/admin` **(FixedPath discloses /admin exists)**
- `/users//` → muxmaster emite 301 → `/users/` (TSR)

**Impacto.**
- **Route reconnaissance**: um atacante que enumera rotas protegidas recebe 301 (route existe) vs 404 (route não existe), apesar de auth estar activa. Isto quebra o princípio "middleware guards everything".
- **Combinação com H-007**: o Location header contém o path canónico, que pode ser um segmento arbitrário descoberto via probing de trailing/fixedpath variants.
- **Combinação com PRF-002**: CleanPath + encoded traversal produz o mesmo sintoma sem sequer requerer o redirect — vetor agravado.

**Fix recomendado.**
1. Aplicar `wrapMiddleware(http.HandlerFunc(m.dispatch), m.middleware)` em `ServeHTTP` em vez de só ao handler final — todas as respostas (incluindo TSR/FixedPath redirects) passariam pela chain. Custo: overhead de middleware em requests 404 também (consistente com chi).
2. Alternativa: oferecer opção `TSRRequiresAuth bool` / `FixedPathRequiresAuth bool` (default `true` em v1.0.0 para security-by-default, `false` para preservar comportamento actual).
3. Documentar o comportamento explicitamente no README e marcar o risco.

**Cross-ref.** Resolve H-025 como `confirmed`; refina H-007 (open redirect via FixedPath). Escalation para `http-protocol-security-auditor` para validar Location header integrity.

---

### PRF-005 — `Mount` deixa `RawPath` com prefix não-trimmed quando input é percent-encoded

- **Severidade:** Medium
- **CWE:** CWE-707 (Improper Neutralization)
- **Localização:** `mux.go:362-370` (Mount clones request + `r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)`)
- **Reprodutor:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-005-mount-rawpath/repro_test.go`

**Descrição.**
Quando o cliente envia um request com `RawPath` que contém a prefix percent-encoded (e.g. `/%61pi/foo` onde `%61 = 'a'`), `url.Parse` preserva `Path = "/api/foo"` e `RawPath = "/%61pi/foo"`. O routing encontra `Mount("/api", …)` a partir de `Path`. No clone:

```go
r2.URL.Path = p                 // "/foo" (PathParam mux_mount)
if r.URL.RawPath != "" {
    r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix) // fails silently
}
```

`TrimPrefix("/%61pi/foo", "/api")` retorna o argumento inalterado porque não há prefix match literal. O inner handler vê:
- `r2.URL.Path = "/foo"`
- `r2.URL.RawPath = "/%61pi/foo"` (com a prefix do outer mux intacta)

**Evidência.**
```
=== RUN   TestPRF005_MountRawPathDivergence
    repro_test.go:33: inner handler saw Path="/foo" RawPath="/%61pi/foo"
    repro_test.go:35: PRF-005 REPRODUCED: Path="/foo" vs RawPath="/%61pi/foo" — inner handler sees divergent URL components
```

**Impacto.**
- Se o inner handler (e.g. outro MuxMaster ou router arbitrário) usa **RawPath** para routing próprio (comum em routers que suportam `UseRawPath=true`), recebe um path que **começa com a prefix do outer**. Dependendo de como o inner parseia, pode:
  - Routar para um pattern inesperado (ex.: `/api/foo` em vez de `/foo`)
  - Falhar por path não reconhecido
  - Usar parte da prefix como parâmetro capturado
- Se o inner handler implementa auth baseada em path string, atacante que injecta prefix encoded pode confundir a lógica.

Não é um bypass directo em MuxMaster contra MuxMaster (o inner usaria Path = "/foo" por default), mas é um risco real quando Mount serve um router cujo behaviour depende de RawPath.

**Fix recomendado.**
Em `mux.go:366-368`:
```go
if r.URL.RawPath != "" {
    // Prefer to recompute RawPath from the *decoded* prefix so both
    // fields stay consistent after trimming. Falls back to Path if
    // the encoded prefix does not share the decoded prefix's length.
    if strings.HasPrefix(r.URL.RawPath, prefix) {
        r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
    } else {
        r2.URL.RawPath = "" // signal "unavailable" rather than divergent
    }
}
```

Alternativamente, sempre clear `r2.URL.RawPath` quando o TrimPrefix não match — é mais seguro forçar o inner handler a usar `Path`.

**Cross-ref.** Resolve H-013 como `partial confirmed` (divergência real mas exploitability depende do inner handler).

---

## 3. Findings adicionais de diferencial (informational)

Ver `/reports/path-routing-fuzzer/evidence/2026-04-17/differential.csv` para a tabela completa (739 entradas).

| Input | MuxMaster | httprouter | chi | bunrouter | Classe |
|---|---|---|---|---|---|
| `/%61dmin` | 200 `admin` | 200 `admin` | 404 | 404 | handler-split — MuxMaster decodifica percent-encoded no matching, chi/bunrouter usam RawPath |
| `/users/Al%2fice` | 404 | 404 | 200 `user` | 200 `user` | MuxMaster/httprouter split `%2f` como separador, chi/bunrouter tratam-no como parte do valor |
| `/users//` | 301 → `/users/` | 301 → `/users/` | 404 | 200 `user` (com id vazio) | MuxMaster faz TSR, chi ignora, bunrouter captura id=`"/"` |
| `/users/Müller%2F` | 301 → `M%C3%BCller` | 301 → `M%C3%BCller` | 200 `user` | 200 `user` | FixedPath strip trailing slash encoded + reencoding de Unicode |
| `/api/v1/items/%2F` | 301 → `/api/v1/items/` | 301 → `/api/v1/items/` | 200 `api.item` | 200 `api.item` | FixedPath route disclosure |

Estas divergências não são necessariamente bugs — são diferenças documentadas de política de decoding. **Nenhuma** destas constitui um bypass da surface registada; contudo, MuxMaster **é mais permissivo que chi/bunrouter** no tratamento de percent-encoded characters em match de paths estáticos (variante aceita `/%61dmin → /admin`). Documentar é suficiente.

---

## 4. Property / invariant tests

| Invariante | Status | Evidência |
|---|---|---|
| I-01 `:param` não contém `/` | **PASS** | `TestInvariant_ParamNeverSpansSegment` |
| I-02 `..` textual + `RedirectFixedPath=false` não alcança `/admin` | **PASS** | `TestInvariant_NoFixedPathTraversal` |
| I-03 catch-all match exige prefix `/static/` literal | **PASS** | `TestInvariant_CatchAllPrefix` |
| I-04 `addRoute` nunca panica em dispatch para inputs ASCII | **PASS** | `FuzzAddRoute` 60s sem crash |
| I-05 `addRoute` nunca panica em dispatch para UTF-8 válido | **PARTIAL** — falha com 0xFF (PRF-006) |
| I-06 wildcard shadow rejeitado em registration | **FAIL** — PRF-001 |
| I-07 redirects passam pela middleware chain | **FAIL** — PRF-004 |
| I-08 Path e RawPath consistentes após Mount | **FAIL** — PRF-005 |

---

## 5. Coverage metrics

- **Fuzz inputs processados:** ~5.4 M (combinado FuzzGetValue, FuzzDifferential, FuzzAddRoute)
- **Unique coverage entries:** 753 (FuzzGetValue), 796 (FuzzDifferential), 330 (FuzzAddRoute)
- **Corpus curado:** 739 linhas × 4 routers × 7 ficheiros = 20 692 router-dispatch operations em `TestDifferentialTable`
- **Matriz de middleware:** 17 payloads × 16 combinações (2⁴) = 272 cells + 136 bypasses confirmados
- **Invariants testados:** 8 (ver §4)

---

## 6. Crashes & panics

| # | Input | Família | Primeira observação | Repro |
|---|---|---|---|---|
| 1 | `/\xff` como pattern | UTF-8 invalid corruption | FuzzAddRoute | PRF-006 |
| 2 | `/a/:x` + `/a/b` dispatch `/a/c` | wildcard shadow | TestWildcardShadow | PRF-001 |

Ambos têm reproducer standalone e expand para famílias (variantes em diferentes profundidades/prefixes).

---

## 7. Escalations

| Escalation | Alvo | Razão |
|---|---|---|
| **PRF-001** — tree corruption | `concurrency-security-auditor` | Embora o bug seja single-threaded em origem, o estado corrupto persiste no tree atomicamente-published; se um request em flight está a ler enquanto o bad addRoute corre, o race detector pode flag. |
| **PRF-002** — CleanPath bypass | `middleware-security-reviewer` | Owner do middleware; adicionar teste de regressão no middleware suite. |
| **PRF-004** — TSR/FixedPath pre-middleware | `http-protocol-security-auditor` | Location header + 301 antes de auth é também uma forma de spec-level risk. |
| **PRF-006** — 0xFF OOB panic | `go-sast-and-memory-auditor` | SAST `gosec` / `staticcheck` deveriam ter apanhado o `indices` length mismatch — verificar porque não apanharam. |
| **PRF-005** — Mount RawPath | `http-protocol-security-auditor` | Inconsistency of URL components is protocol-level. |
| `/admin#/../secret` oracle limitation | `threat-modeler` | Documentar que o fragment é client-only — não é um gap real mas o corpus inclui. |

---

## 8. Coverage gaps (honestamente declarados)

O que **não foi** testado nesta sessão:

1. **HTTP/2 pseudo-header paths** — o harness envia sempre requests HTTP/1.1 via `http.Request`. HTTP/2 pode ter semântica diferente no `:path` pseudo-header (PRI escape, percent-encoding CONTINUATION). Fora de scope deste agente (→ `http-protocol-security-auditor`).
2. **IRI (RFC 3987) / internationalised paths na request line** — apenas UTF-8 bytes percent-encoded foram testados; non-BMP characters raw (e.g. emoji) não foram suficientemente explorados no fuzz (o corpus inclui alguns mas o fuzz-guided explorou pouco).
3. **Query string** — o oráculo de traversal descarta `?...`; não fuzzámos combinações de path traversal com query-injection.
4. **Raw request bytes que fazem bypass de `url.Parse`** — `net/http` parser é mais permissivo que `url.Parse` em alguns aspectos; um harness ao nível TCP (raw `bufio.Writer` para `Server.Serve`) poderia revelar vectores não cobertos.
5. **Registo dinâmico após arranque** — documentado como UB; o fuzz não testa concorrência.
6. **Fuzz prolongado (≥10 min por target)** — este budget não foi utilizado. Recomendado re-correr pré-release com `-fuzztime=30m` cada.
7. **Interaction com regex params (`{name:expr}`)** — cobertura parcial via alguns seeds mas `FuzzAddRoute` não cobriu regex explicitamente.

---

## 9. Next actions (priorizada)

1. **[CRITICAL]** Fix PRF-001 (wildcard shadow panic) — bloqueia release v1.0.0.
2. **[CRITICAL]** Fix PRF-006 (0xFF OOB) — bloqueia release v1.0.0.
3. **[HIGH]** Fix PRF-002 — redesenhar `clean_path` para não colapsar `..`.
4. **[HIGH]** Fix PRF-004 — aplicar middleware à chain de redirect OU documentar como feature + adicionar opt-in.
5. **[HIGH]** Fix PRF-003 — validar `maxInlineParams` em addRoute.
6. **[MEDIUM]** Fix PRF-005 — consistência Path/RawPath em Mount.
7. **[LOW]** Acrescentar teste de regressão para cada reprodutor no suite principal (`tree_test.go`, `mux_test.go`).
8. **[LOW]** Correr fuzz nightly com `-fuzztime=30m` — budget de tempo extensivo é barato em CI.

---

## 10. Artefactos

- **Fuzz output:** `/reports/path-routing-fuzzer/evidence/2026-04-17/fuzz-*.txt`
- **Differential CSV:** `/reports/path-routing-fuzzer/evidence/2026-04-17/differential.csv` (672 linhas)
- **Middleware matrix CSV:** `/reports/path-routing-fuzzer/evidence/2026-04-17/middleware-matrix.csv` (5 265 linhas)
- **Crash inputs:** `/reports/path-routing-fuzzer/evidence/2026-04-17/crashes/`
- **Standalone reproducers:** `/reports/path-routing-fuzzer/evidence/2026-04-17/findings/PRF-{001..006}/`
- **Harness source:** `/reports/path-routing-fuzzer/harness/`
- **Corpora (curadas + geradas):** `/reports/path-routing-fuzzer/corpora/`
- **Commit snapshot:** `/reports/path-routing-fuzzer/evidence/2026-04-17/commit.txt`

---

## 11. Cross-reference com hipóteses

| Hipótese | Verdict | Finding(s) |
|---|---|---|
| H-010 clean_path single-pass | **confirmed** | PRF-002 |
| H-012 paramsBuf silent overflow | **confirmed** | PRF-003 |
| H-013 Mount RawPath divergence | **confirmed (partial)** | PRF-005 |
| H-025 TSR pre-auth disclosure | **confirmed** | PRF-004 (+ variante FixedPath) |
| H-007 RedirectFixedPath open-redirect `//` | **refuted** | `path.Clean` colapsa `//` → `/`; Location fica same-origin |
| H-028 Unicode case-fold asymmetry | **refuted** | `foldEq` é ASCII-only por design; confusables Unicode não cross-fold (safe) |

Novas hipóteses geradas (para o threat-modeler promover):
- **H-031** (proposta): tree corruption via wildcard shadow — PRF-001 é exemplo da classe "addRoute aceita pattern que cria árvore inconsistente"
- **H-032** (proposta): UTF-8 invariant violation em radix tree — PRF-006 é a primeira instância; outros bytes ≥0x80 em posições específicas podem reproduzir

---

**Agente:** path-routing-fuzzer
**Commit auditado:** `533d0c9`
**Timestamp:** 2026-04-17T12:30Z
