# MuxMaster Knowledge Model

**Status:** Active
**Store:** `rmp graph` (roadmap `muxmaster`)
**Owner:** the `knowledge-authority` skill — no other process may write to the graph.

This file is the authoritative description of the shape of the MuxMaster knowledge graph.
The file and the graph **must mirror each other**. Whenever a new label or edge type is
introduced, this file is updated in the same commit.

The graph is a **Label Property Graph (LPG)**. It is the project's single source of truth
about itself: what exists, where it lives, what it depends on, what proves it works, and in
which commit it appeared.

---

## Provenance

Every node **and** every edge carries provenance:

| Property | Meaning |
|---|---|
| `gitCommit` | Full hash of the commit at which the element was last confirmed. |
| `gitDate` | ISO date (`YYYY-MM-DD`) of that commit. |

Provenance is stamped at write time from `HEAD`. Elements that describe a historical fact
(a `Commit`, a `Release`, a closed `Task`) additionally carry their own dates in dedicated
properties; `gitCommit`/`gitDate` always mean *"when the graph last confirmed this"*, not
*"when the thing happened"*.

---

## Node labels

| Label | Meaning | Key | Other properties |
|---|---|---|---|
| `Package` | A Go package in the module. | `name` | `path`, `importPath` |
| `File` | A tracked Go file. | `path` | `package` (Go package clause, `_test` suffix stripped), `loc`, `kind` (`source` \| `test` \| `harness` \| `example`) |
| `Type` | An exported Go type. | `name` | `package`, `file`, `kind` (`struct` \| `interface` \| `func` \| `slice` …) |
| `Function` | An exported function or method, plus the internal hot-path functions that carry the design. | `name` | `package`, `file`, `receiver`, `exported` (bool), `hotPath` (bool) |
| `Variable` | A package-level `var` that carries a load-bearing design decision (e.g. a table built once at init). | `name` (string) | `package`, `file`, `exported` (bool), `hotPath` (bool) |
| `Feature` | A user-visible capability of the router. | `name` | `summary`, `status` |
| `Middleware` | One of the middlewares in `middleware/`. | `name` | `file`, `constructors` |
| `Test` | A `Test*` function. | `name` + `file` (both strings; see *Identity of test symbols*) | `package` |
| `FuzzTarget` | A `Fuzz*` function. | `name` + `file` (both strings; see *Identity of test symbols*) | `module` (`muxmaster` for the main module, else the harness module directory) |
| `Benchmark` | A `Benchmark*` function. | `name` + `file` (both strings; see *Identity of test symbols*) | `module` (`muxmaster` for the main module, else the harness module directory) |
| `Spec` | A file under `specification/`. | `path` | `title` |
| `Doc` | A user-facing document (`docs/`, `README.md`, `CHANGELOG.md`, …). | `path` | `title` |
| `Example` | A runnable example under `examples/`. | `name` | `path` |
| `Report` | An audit artefact under `reports/`: a narrative report, or a harness or evidence file that is the record of a finding. | `path` | `agent`, `date`, `kind` (`report` \| `harness` \| `evidence`) |
| `Finding` | A security or performance finding raised by an audit. | `id` (string, e.g. `CSA-2026-0050`) | `title`, `severity` (integer), `severityLabel` (string: the qualitative severity exactly as the source states it, e.g. `Critical`; never converted to or from `severity`), `family`, `status` (remediation state), `traceability` (`documented` \| `documented-new` \| `not-documented`: how the finding is traced to a record), `traceabilityReason` (string: the evidence or, for `not-documented`, the justification), `alsoKnownAs` (list of strings: the aliases under which the records name it) |
| `Optimization` | A performance optimisation evaluated by the perf audit. | `name` | `optId`, `summary`, `outcome` (`applied` \| `no-gain`) |
| `Release` | A published SemVer tag. | `version` | `tag`, `commit`, `date` |
| `Commit` | A git commit. | `hash` | `shortHash`, `date`, `subject`, `type` (Conventional Commits type) |
| `Workflow` | A GitHub Actions workflow. | `name` | `path` |
| `Competitor` | An external router benchmarked against MuxMaster. | `name` | `importPath`, `algorithm` |
| `Sprint` | An `rmp` sprint. | `id` | `title`, `status`, `description`, `taskCount`, `closedAt` |
| `Task` | An `rmp` task. | `id` | `title`, `status`, `sprintId`, `priority`, `severity`, `type` |

### Granularity decisions

- **`Function`** covers every exported package-level function, every exported method of `Mux`
  and `Group`, and the unexported functions that carry a load-bearing design decision
  (`getValue`, `getValueStatic`, `addRoute`, `insertChild`, `expandOptional`,
  `dispatchParams1`, `dispatchParams2`, `setReqCtxUnsafe`, `wrapMiddleware`, …), flagged with
  `hotPath: true`.
- **`Finding`** and **`Task`** are distinct nodes. A finding is *what an auditor found*; a task
  is *the unit of work that closed it*. They are joined by `ADDRESSES`.
- **`FuzzTarget`** nodes live either in the main module (root package test files, corpus under
  `testdata/fuzz/`) or in the `reports/*/harness` modules, which are separate Go modules excluded
  from the main build. Harness `File` nodes carry `kind: harness`; `module` on the symbol names
  the harness module directory.
- **Identity of test symbols.** Go function names are unique per package, not per repository:
  the same `Benchmark*`/`Test*`/`Fuzz*` name can exist in the main module and in a harness
  module. `Test`, `Benchmark` and `FuzzTarget` are therefore identified by `name` **and** `file`;
  always `MERGE` on both. A lookup by `name` alone may bind several nodes.
- **`Variable`** covers only package-level variables that carry a design decision, flagged with
  `hotPath: true` like the internal `Function` nodes.
- **`File` with `kind: example`** is a Go file under `examples/`; the runnable program itself is
  the `Example` node whose `path` is the file's directory.
- **`Finding` identifiers.** The `id` is the identifier the source assigns. When a report lists
  defects without identifiers, the id is `<report finding prefix>-OOS-<nn>`, where `nn` is the
  item number in that report's out-of-scope defect list (e.g. `WH-OOS-03` is item 3 of the
  waste-hunt report's list); the `REPORTED_IN` edge names that report.

## Known coverage limits (state at bootstrap)

These are honest gaps, not defects in the model. They are the graph telling the truth about
the repository:

- 34 of the 166 `Finding` nodes have no `REPORTED_IN` edge: their identifier appears in the
  `rmp` task title but nowhere in the report prose (naming drift, e.g. `TSC-REVAL-2026-001`).
- 20 `Report` nodes cite no finding — they are benchmark and analysis reports, not audits.
- `Sprint` 10 has no tasks; it is an empty container whose work was split into S11–S13.
- Only 11 `Finding` nodes have a `FIXED_BY` edge: most fix commits close findings in batches
  and name only some of them in the subject line.

---

## Edge types

| Edge | From → To | Meaning |
|---|---|---|
| `CONTAINS` | `Package` → `File` | The file belongs to the package. |
| `DECLARES` | `File` → `Type` \| `Function` \| `Variable` | The symbol is declared in this file. |
| `IMPLEMENTS` | `File` → `Feature` \| `Middleware` | The file carries the implementation. |
| `SPECIFIED_IN` | `Feature` → `Spec` | The feature's behaviour is specified there. |
| `DOCUMENTED_IN` | `Feature` \| `Middleware` → `Doc` | The feature is documented for users there. |
| `TESTS` | `Test` → `Feature` \| `Middleware` | The test exercises it. |
| `DEFINED_IN` | `Test` \| `FuzzTarget` \| `Benchmark` → `File` | Where the function lives. |
| `FUZZES` | `FuzzTarget` → `Feature` \| `Middleware` | The fuzz target attacks it. |
| `BENCHMARKS` | `Benchmark` → `Feature` \| `Middleware` | The benchmark measures it. |
| `DEPENDS_ON` | `Feature` → `Feature`, `Package` → `Package` | Hard dependency. |
| `INTRODUCED_IN` | `Feature` \| `Middleware` → `Commit` | The commit that first delivered it. |
| `FIXED_BY` | `Finding` → `Commit` | The commit that closed the finding. |
| `AFFECTS` | `Finding` → `File` \| `Middleware` \| `Feature` | What the finding compromises. |
| `REPORTED_IN` | `Finding` → `Report` | The report that raised it. |
| `REPORTED_IN` | `Finding` → `Doc` | No report raised it; the security advisory in the document (`SECURITY.md`) is its primary source, or the document is a secondary record of it. |
| `DUPLICATES` | `Finding` → `Finding` | The finding is not distinct: it re-validates or repeats the target finding. |
| `ADDRESSES` | `Task` → `Finding` | The task that closed the finding. |
| `OPTIMIZES` | `Optimization` → `Feature` \| `File` | What the optimisation targets. |
| `DELIVERED_BY` | `Optimization` → `Commit`, `Task` → `Commit` | The commit that carried the work. |
| `RELEASED_IN` | `Commit` → `Release` | The commit is an ancestor of the tag and postdates the previous one. |
| `DEMONSTRATES` | `Example` → `Feature` \| `Middleware` | The example shows it in use. |
| `VALIDATES` | `Workflow` → `Feature` \| `Package` | The CI job gates it. |
| `COMPETES_WITH` | `Package` → `Competitor` | Benchmarked head-to-head. |
| `BELONGS_TO` | `Task` → `Sprint` | Sprint membership. |
| `MODIFIES` | `Commit` → `File` \| `Spec` \| `Doc` \| `Workflow` \| `Report` | The commit touched the file. On a `Spec` target the edge may carry `sections` (list of strings): the headings of the sections whose lines the commit changed. |

---

## Evidence policy

An edge is written **only** when its evidence was verified against the repository at write
time:

- `SPECIFIED_IN` / `DOCUMENTED_IN` require the target file to actually mention the feature
  (keyword match, checked at generation).
- `IMPLEMENTS` requires the defining symbol to be present in the source file.
- `MODIFIES` comes from `git log --name-only`.
- `ADDRESSES` comes from the finding identifier embedded in the `rmp` task title, description or
  completion summary; for a finding without a source identifier (`*-OOS-*`), from the task
  description citing the same defect and evidence file.
- `REPORTED_IN` requires the finding identifier (or, for `*-OOS-*`, the listed defect) to appear
  in the target's prose, or — for a finding recorded under another name — one of its
  `alsoKnownAs` aliases, verified by content (behaviour, affected file, fix commit, task).
  A plain string match in a harness whose identifiers are reused for unrelated checks is not
  evidence (see `reports/overview/findings.md` §5).
- `DUPLICATES` requires a written justification in `traceabilityReason` and in
  `reports/overview/findings.md` §3.
- Every `Finding` without a `REPORTED_IN` edge carries `traceability = 'not-documented'` and a
  `traceabilityReason`; `MATCH (f:Finding) WHERE NOT (f)-[:REPORTED_IN]->() AND f.traceability
  IS NULL RETURN f.id` must return no rows.
- `TESTS` / `FUZZES` / `BENCHMARKS` require evidence in the function body: a call to the
  middleware constructor for a `Middleware` target, a feature-specific API or keyword for a
  `Feature` target. A symbol with no such evidence gets no edge.
- `FIXED_BY` / `DELIVERED_BY` come from the finding or task identifier embedded in the commit
  message.

Nothing is inferred. When evidence is missing, no edge is written and the gap is reported.

---

## Constraints

The engine enforces only single-property `IS UNIQUE` and `IS NOT NULL` constraints. Read what the
engine currently holds with `SHOW CONSTRAINTS`.

| Rule | DDL | Note |
|---|---|---|
| `Variable.name` is unique | `CREATE CONSTRAINT variable_name_uniq IF NOT EXISTS FOR (n:Variable) REQUIRE n.name IS UNIQUE` | Single-property key; expressible. |
| `(name, file)` is unique on `Test`, `Benchmark`, `FuzzTarget` | not expressible (composite keys are unsupported) | Measure with `MATCH (n:Benchmark) WITH n.name AS a, n.file AS b, count(*) AS c WHERE c > 1 RETURN count(*)` (same for `Test`, `FuzzTarget`). `name` alone is **not** unique and must not be declared `UNIQUE`. |

---

## Maintenance contract

- **Every `git commit` updates the graph** (CLAUDE.md §9). Reconcile only what changed:
  `git diff --name-only HEAD~1 HEAD`, then bump the provenance of the touched `File`,
  `Feature`, `Middleware` and `Package` nodes, and add the new `Commit` node with its
  `MODIFIES` edges.
- New label or edge type → update this file in the same commit.
- Orphan and label-less nodes are data-quality bugs; surface and clean them.
