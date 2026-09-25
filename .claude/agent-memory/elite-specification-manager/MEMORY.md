# Agent Memory Index

- [project_muxmaster.md](project_muxmaster.md) — MuxMaster project identity, principles, Go version, package layout, and spec structure
- [project_spec_structure.md](project_spec_structure.md) — Specification file inventory, terminology decisions, and numbering conventions (updated 2026-07-14)
- [project_implementation_status.md](project_implementation_status.md) — What is implemented in code as of 2026-07-14; supersedes the old 2026-04-16 snapshot, which was found to be badly stale
- [task_kg2026001_fastpath_spec.md](task_kg2026001_fastpath_spec.md) — 2026-07-14: specified FastHandler/FastMiddleware/pooling/Rebuild; found and fixed pre-existing spec/code drift in params.md and out-of-scope.md
- [task_rmp250_253_perflab_spec.md](task_rmp250_253_perflab_spec.md) — 2026-09-24: path-copying registration (WH-08) + shallow request copy (WH-04) spec amendments; new README term "Shallow request copy"; flagged middleware.md §11 vs serveRedirect drift, unfixed
- [task_rmp258_sprint18_reconciliation.md](task_rmp258_sprint18_reconciliation.md) — 2026-09-25: fixed rule 51/71 (regex/plain conflict), DIV-001 lookup fallback, sibling registration order, redirect CTL encoding, Mount TSR, middleware.md §11 (redirects + NotFound/405/OPTIONS all wrapped by Use(), dynamic binding), error-handling.md §5/§10/new §21, middleware-stdlib.md RealIP/Logger/Compress/SetHeader rewrite (full renumbering 4→70), performance.md §36 maxParams (root-only), out-of-scope.md JWT/OAuth2, README.md design principle 6 (per-request header isolation)
