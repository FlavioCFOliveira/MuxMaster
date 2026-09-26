# MuxMaster — Security Reports

This directory is the single destination for all reports, evidence, harnesses, and corpora produced by specialized security agents. Each agent is the sole author of its subdirectory; the `threat-modeler-and-zero-day-researcher` is the sole author of `/reports/overview/`.

## Structure

```
reports/
├── README.md                                   ← this file
├── .gitignore                                  ← ignores raw evidence; .md reports are versioned
├── overview/                                   ← threat-modeler (consolidates everything)
│   ├── threat-model.md                         ← live STRIDE matrix
│   ├── attack-trees.md                         ← attack trees
│   ├── transposition.md                        ← vectors from other ecosystems
│   ├── hypotheses.md                           ← open zero-day hypotheses
│   ├── findings.md                             ← single ledger of findings
│   ├── system-model.md                         ← DFD + trust boundaries
│   └── YYYY-MM-DD-sprint.md / -posture.md      ← per sprint
│
├── http-protocol-security-auditor/             ← smuggling, HTTP/2, CRLF, redirects
├── path-routing-fuzzer/                        ← bypass, traversal, Unicode, differential
├── dos-resilience-tester/                      ← complexity, slowloris, bombs
├── concurrency-security-auditor/               ← races, pool, TOCTOU, panic recovery
├── middleware-security-reviewer/               ← audit per middleware
├── go-sast-and-memory-auditor/                 ← SAST, CVE, supply chain
├── timing-and-sidechannel-analyst/             ← timing leaks, constant-time
└── fuzzing-and-property-engineer/              ← continuous fuzz + invariants
```

Each agent subdirectory follows the same convention:
```
<agent>/
├── YYYY-MM-DD-HHMM-<topic>.md                  ← dated reports (versioned)
├── evidence/<date>/                            ← raw artefacts (gitignored)
├── harness/                                    ← reusable Go tests (versioned)
└── corpora/                                    ← fuzz corpus persisted (versioned)
```

## Classification

All 9 agents have in their respective frontmatter:
- `category: security`
- `tags: [security, ...]` (first tag always `security`)
- prefix `[SECURITY AGENT]` at the start of `description`

This enables:
- Natural group invocation: "run all security agents" / "complete security audit" / "execute security sprint"
- Quick filtering via grep: `grep -l '^category: security$' .claude/agents/*.md`
- Immediate visual identification when agents appear in lists

### How to invoke as a group

**Complete audit (sprint):** ask the `threat-modeler-and-zero-day-researcher` to coordinate. It creates the sprint plan in `/reports/overview/<date>-sprint.md` and dispatches the 8 specialists in parallel.

**Prompts that activate the group:**
- "run all security agents"
- "complete security sprint"
- "pre-release security audit"
- "I want evidence that there are no vulnerabilities before tagging"
- "dispatch all agents with `category: security`"

**Individual invocation:** use the agent's exact name (see table below). Useful when the change touches only one area.

## The 9 security agents

| # | Agent | Primary focus | Reports in |
|---|---|---|---|
| 1 | `http-protocol-security-auditor` | HTTP/1.1 + HTTP/2 framing, smuggling, CRLF, redirects | `/reports/http-protocol-security-auditor/` |
| 2 | `path-routing-fuzzer` | Routing bypass (traversal, Unicode, encoding), differential vs competitors | `/reports/path-routing-fuzzer/` |
| 3 | `dos-resilience-tester` | Complexity attacks, slowloris, gzip bombs, throttle bypass | `/reports/dos-resilience-tester/` |
| 4 | `concurrency-security-auditor` | Data races, sync.Pool contamination, TOCTOU, goroutine leaks | `/reports/concurrency-security-auditor/` |
| 5 | `middleware-security-reviewer` | Threat model per middleware (basic_auth, cors, compress, real_ip, etc.) | `/reports/middleware-security-reviewer/` |
| 6 | `go-sast-and-memory-auditor` | gosec + staticcheck + govulncheck + escape analysis + SBOM | `/reports/go-sast-and-memory-auditor/` |
| 7 | `timing-and-sidechannel-analyst` | Statistical timing leaks (Welch/KS/MWU), constant-time verification | `/reports/timing-and-sidechannel-analyst/` |
| 8 | `fuzzing-and-property-engineer` | Continuous fuzz of entire public API + property tests (rapid) | `/reports/fuzzing-and-property-engineer/` |
| 9 | `threat-modeler-and-zero-day-researcher` | Orchestrates the 8 specialists; STRIDE; attack trees; zero-day hypotheses | `/reports/overview/` |

## When and how to invoke

### Complete pre-release sprint
```
Ask the threat-modeler-and-zero-day-researcher for a complete sprint plan.
It dispatches the 8 specialists in parallel and consolidates in /reports/overview/.
```

### Targeted change
Invoke directly the agent whose area the change touches. If the change crosses domains, start with `threat-modeler` who will redistribute.

### Automatic activation matrix

| File changed | Agents to activate |
|---|---|
| `mux.go` | http-protocol + path-routing + concurrency + sast |
| `tree.go` | path-routing + dos-resilience + fuzzing-and-property |
| `params.go` | concurrency + dos-resilience + sast |
| `middleware/*.go` | middleware-reviewer + (timing if auth) + sast |
| `introspection.go` | concurrency + http-protocol + middleware-reviewer |
| `response.go` | http-protocol + sast |
| `go.mod` | sast |

## Contracts between agents

1. **Findings flow:** each specialist writes findings in its local report; the `threat-modeler` copies them to `/reports/overview/findings.md` with canonical ID.
2. **Canonical severity:** Critical / High / Medium / Low / Info. Every severity must be justified.
3. **CWE mandatory:** each finding cites a CWE (https://cwe.mitre.org).
4. **Reproducer:** every Critical/High finding must have a minimal repro_test.go.
5. **Evidence path:** every finding cites a concrete evidence path (not "see attached").
6. **Cross-agent escalation:** CRLF → http-protocol; race → concurrency; timing → timing; traversal → path-routing.

## Versioning policy

Reports (`*.md`) are **versioned** in git — they are historical documentation.
Evidence (`evidence/**`), profiles (`*.pprof`), SARIF (`*.sarif`), large CSVs, crashers, are **ignored** by git via local `.gitignore` — available on the audit machine but not polluting releases.

Harnesses (`harness/**`) and corpora (`corpora/**`) are **versioned** — they are reusable and reproducible infrastructure.

## SLA / recommended minimum cadence

| Trigger | Agents | Typical duration |
|---|---|---|
| Pull-request PR | sast (fast), fuzzing 30s/target | < 3 min |
| Merge to main | sast + routing-fuzzer 10min + race | < 30 min |
| Nightly | fuzzing 2h/target + dos sustained 30min | ~4h |
| Pre-release | complete sprint (9 agents) | ~24h aggregate |
| Incident | relevant agent + threat-modeler | hours |

## Contact / handoff

The `threat-modeler-and-zero-day-researcher` is the single point of contact for the maintainer. It consolidates, prioritises, and recommends a release decision (ship / hold / conditional).
