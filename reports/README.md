# MuxMaster — Security Reports

Esta pasta é o destino único de todos os relatórios, evidências, harnesses e corpora produzidos pelos agentes especializados de segurança. Cada agente é o único autor da sua subpasta; o `threat-modeler-and-zero-day-researcher` é o único autor de `/reports/overview/`.

## Estrutura

```
reports/
├── README.md                                   ← este ficheiro
├── .gitignore                                  ← ignora evidências brutas; relatórios .md são versionados
├── overview/                                   ← threat-modeler (consolida tudo)
│   ├── threat-model.md                         ← matriz STRIDE viva
│   ├── attack-trees.md                         ← árvores de ataque
│   ├── transposition.md                        ← vectores de outros ecossistemas
│   ├── hypotheses.md                           ← zero-day hypotheses abertas
│   ├── findings.md                             ← ledger único de findings
│   ├── system-model.md                         ← DFD + trust boundaries
│   └── YYYY-MM-DD-sprint.md / -posture.md      ← por sprint
│
├── http-protocol-security-auditor/             ← smuggling, HTTP/2, CRLF, redirects
├── path-routing-fuzzer/                        ← bypass, traversal, Unicode, differential
├── dos-resilience-tester/                      ← complexity, slowloris, bombs
├── concurrency-security-auditor/               ← races, pool, TOCTOU, panic recovery
├── middleware-security-reviewer/               ← audit por middleware
├── go-sast-and-memory-auditor/                 ← SAST, CVE, supply chain
├── timing-and-sidechannel-analyst/             ← timing leaks, constant-time
└── fuzzing-and-property-engineer/              ← fuzz contínuo + invariants
```

Cada subpasta de agente segue a mesma convenção:
```
<agent>/
├── YYYY-MM-DD-HHMM-<topic>.md                  ← relatórios datados (versionados)
├── evidence/<date>/                            ← artefactos brutos (gitignored)
├── harness/                                    ← testes Go reusáveis (versionados)
└── corpora/                                    ← corpus de fuzz persistido (versionado)
```

## Classificação

Todos os 9 agentes têm no respectivo frontmatter:
- `category: security`
- `tags: [security, ...]` (primeira tag sempre `security`)
- prefixo `[SECURITY AGENT]` no início da `description`

Isto permite:
- Invocação de grupo natural: "corre todos os agentes de segurança" / "faz auditoria de segurança completa" / "executa o sprint de segurança"
- Filtragem rápida via grep: `grep -l '^category: security$' .claude/agents/*.md`
- Identificação visual imediata quando os agentes aparecem em listas

### Como invocar como grupo

**Auditoria completa (sprint):** pede ao `threat-modeler-and-zero-day-researcher` para coordenar. Ele cria o sprint plan em `/reports/overview/<data>-sprint.md` e dispatcha os 8 especialistas em paralelo.

**Exemplos de prompt que activam o grupo:**
- "corre todos os agentes de segurança"
- "faz um sprint de segurança completo"
- "auditoria de segurança pré-release"
- "quero ter evidência de que não há vulnerabilidades antes de taggar"
- "dispatcha todos os agentes com `category: security`"

**Invocação individual:** usa o nome exacto do agente (ver tabela abaixo). Útil quando a mudança toca apenas uma área.

## Os 9 agentes de segurança

| # | Agente | Foco principal | Reports em |
|---|---|---|---|
| 1 | `http-protocol-security-auditor` | HTTP/1.1 + HTTP/2 framing, smuggling, CRLF, redirects | `/reports/http-protocol-security-auditor/` |
| 2 | `path-routing-fuzzer` | Routing bypass (traversal, Unicode, encoding), differential com competitors | `/reports/path-routing-fuzzer/` |
| 3 | `dos-resilience-tester` | Complexity attacks, slowloris, gzip bombs, throttle bypass | `/reports/dos-resilience-tester/` |
| 4 | `concurrency-security-auditor` | Data races, sync.Pool contamination, TOCTOU, goroutine leaks | `/reports/concurrency-security-auditor/` |
| 5 | `middleware-security-reviewer` | Threat model por middleware (basic_auth, cors, compress, real_ip, etc.) | `/reports/middleware-security-reviewer/` |
| 6 | `go-sast-and-memory-auditor` | gosec + staticcheck + govulncheck + escape analysis + SBOM | `/reports/go-sast-and-memory-auditor/` |
| 7 | `timing-and-sidechannel-analyst` | Timing leaks estatísticos (Welch/KS/MWU), constant-time verification | `/reports/timing-and-sidechannel-analyst/` |
| 8 | `fuzzing-and-property-engineer` | Fuzz contínuo de toda a API pública + property tests (rapid) | `/reports/fuzzing-and-property-engineer/` |
| 9 | `threat-modeler-and-zero-day-researcher` | Orquestra os 8 anteriores; STRIDE; attack trees; hipóteses zero-day | `/reports/overview/` |

## Quando e como invocar

### Sprint completo pré-release
```
Pede ao threat-modeler-and-zero-day-researcher um sprint plan completo.
Ele dispatcha os 8 especialistas em paralelo e consolida em /reports/overview/.
```

### Mudança pontual
Invoca directamente o agente cuja área toca a mudança. Se a mudança cruza domínios, começa pelo `threat-modeler` que redistribui.

### Matriz de activação automática

| Ficheiro alterado | Agentes a activar |
|---|---|
| `mux.go` | http-protocol + path-routing + concurrency + sast |
| `tree.go` | path-routing + dos-resilience + fuzzing-and-property |
| `params.go` | concurrency + dos-resilience + sast |
| `middleware/*.go` | middleware-reviewer + (timing se for auth) + sast |
| `introspection.go` | concurrency + http-protocol + middleware-reviewer |
| `response.go` | http-protocol + sast |
| `go.mod` | sast |

## Contratos entre agentes

1. **Findings flow:** cada especialista escreve findings no seu relatório local; o `threat-modeler` copia-os para `/reports/overview/findings.md` com ID canónico.
2. **Severity canónica:** Critical / High / Medium / Low / Info. Toda severidade tem que estar justificada.
3. **CWE obrigatório:** cada finding cita um CWE (https://cwe.mitre.org).
4. **Reproducer:** toda finding Critical/High tem que ter um repro_test.go minimal.
5. **Evidence path:** toda finding cita um caminho de evidência concreto (não "see attached").
6. **Cross-agent escalation:** CRLF → http-protocol; race → concurrency; timing → timing; traversal → path-routing.

## Política de versionamento

Relatórios (`*.md`) são **versionados** em git — são documentação histórica.
Evidências (`evidence/**`), profiles (`*.pprof`), SARIF (`*.sarif`), CSVs grandes, crashers, são **ignorados** pelo git via `.gitignore` local — estão disponíveis na máquina de auditoria mas não poluem releases.

Harnesses (`harness/**`) e corpora (`corpora/**`) são **versionados** — são infraestrutura reutilizável e reprodutível.

## SLA / cadência mínima recomendada

| Trigger | Agentes | Duração típica |
|---|---|---|
| Pull-request PR | sast (rápido), fuzzing 30s/target | < 3 min |
| Merge para main | sast + routing-fuzzer 10min + race | < 30 min |
| Nightly | fuzzing 2h/target + dos sustained 30min | ~4h |
| Pré-release | sprint completo (9 agentes) | ~24h agregadas |
| Incidente | agente relevante + threat-modeler | horas |

## Contacto / handoff

O `threat-modeler-and-zero-day-researcher` é o ponto único de contacto para o maintainer. Ele consolida, prioriza, recomenda decisão de release (ship / hold / conditional).
