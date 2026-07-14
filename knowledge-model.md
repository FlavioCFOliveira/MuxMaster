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
| `File` | A tracked Go file. | `path` | `package`, `loc`, `kind` (`source` \| `test` \| `harness`) |
| `Type` | An exported Go type. | `name` | `package`, `file`, `kind` (`struct` \| `interface` \| `func` \| `slice` …) |
| `Function` | An exported function or method, plus the internal hot-path functions that carry the design. | `name` | `package`, `file`, `receiver`, `exported` (bool), `hotPath` (bool) |
| `Feature` | A user-visible capability of the router. | `name` | `summary`, `status` |
| `Middleware` | One of the middlewares in `middleware/`. | `name` | `file`, `constructors` |
| `Test` | A `Test*` function. | `name` | `file`, `package` |
| `FuzzTarget` | A `Fuzz*` function. | `name` | `file`, `module` (main module or a `reports/*/harness` module) |
| `Benchmark` | A `Benchmark*` function. | `name` | `file`, `module` |
| `Spec` | A file under `specification/`. | `path` | `title` |
| `Doc` | A user-facing document (`docs/`, `README.md`, `CHANGELOG.md`, …). | `path` | `title` |
| `Example` | A runnable example under `examples/`. | `name` | `path` |
| `Report` | An audit/benchmark report under `reports/`. | `path` | `agent`, `date` |
| `Finding` | A security or performance finding raised by an audit. | `id` (e.g. `CSA-2026-0050`) | `title`, `severity`, `family`, `status` |
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
- **`FuzzTarget`** nodes all live in the `reports/*/harness` modules, which are separate Go
  modules excluded from the main build. Their `File` nodes carry `kind: harness`, and
  `package` names the harness module rather than a package of the main module.

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
| `DECLARES` | `File` → `Type` \| `Function` | The symbol is declared in this file. |
| `IMPLEMENTS` | `File` → `Feature` \| `Middleware` | The file carries the implementation. |
| `SPECIFIED_IN` | `Feature` → `Spec` | The feature's behaviour is specified there. |
| `DOCUMENTED_IN` | `Feature` \| `Middleware` → `Doc` | The feature is documented for users there. |
| `TESTS` | `Test` → `Feature` \| `Middleware` | The test exercises it. |
| `DEFINED_IN` | `Test` \| `FuzzTarget` \| `Benchmark` → `File` | Where the function lives. |
| `FUZZES` | `FuzzTarget` → `Feature` \| `Middleware` | The fuzz target attacks it. |
| `BENCHMARKS` | `Benchmark` → `Feature` | The benchmark measures it. |
| `DEPENDS_ON` | `Feature` → `Feature`, `Package` → `Package` | Hard dependency. |
| `INTRODUCED_IN` | `Feature` \| `Middleware` → `Commit` | The commit that first delivered it. |
| `FIXED_BY` | `Finding` → `Commit` | The commit that closed the finding. |
| `AFFECTS` | `Finding` → `File` \| `Middleware` \| `Feature` | What the finding compromises. |
| `REPORTED_IN` | `Finding` → `Report` | The report that raised it. |
| `ADDRESSES` | `Task` → `Finding` | The task that closed the finding. |
| `OPTIMIZES` | `Optimization` → `Feature` \| `File` | What the optimisation targets. |
| `DELIVERED_BY` | `Optimization` → `Commit`, `Task` → `Commit` | The commit that carried the work. |
| `RELEASED_IN` | `Commit` → `Release` | The commit is an ancestor of the tag and postdates the previous one. |
| `DEMONSTRATES` | `Example` → `Feature` \| `Middleware` | The example shows it in use. |
| `VALIDATES` | `Workflow` → `Feature` \| `Package` | The CI job gates it. |
| `COMPETES_WITH` | `Package` → `Competitor` | Benchmarked head-to-head. |
| `BELONGS_TO` | `Task` → `Sprint` | Sprint membership. |
| `MODIFIES` | `Commit` → `File` | The commit touched the file. |

---

## Evidence policy

An edge is written **only** when its evidence was verified against the repository at write
time:

- `SPECIFIED_IN` / `DOCUMENTED_IN` require the target file to actually mention the feature
  (keyword match, checked at generation).
- `IMPLEMENTS` requires the defining symbol to be present in the source file.
- `MODIFIES` comes from `git log --name-only`.
- `ADDRESSES` comes from the finding identifier embedded in the `rmp` task title.
- `FIXED_BY` / `DELIVERED_BY` come from the finding or task identifier embedded in the commit
  message.

Nothing is inferred. When evidence is missing, no edge is written and the gap is reported.

---

## Maintenance contract

- **Every `git commit` updates the graph** (CLAUDE.md §5). Reconcile only what changed:
  `git diff --name-only HEAD~1 HEAD`, then bump the provenance of the touched `File`,
  `Feature`, `Middleware` and `Package` nodes, and add the new `Commit` node with its
  `MODIFIES` edges.
- New label or edge type → update this file in the same commit.
- Orphan and label-less nodes are data-quality bugs; surface and clean them.
