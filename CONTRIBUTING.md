# Contributing to MuxMaster

Thank you for your interest in contributing. This guide covers everything you need to get started.

## Getting started

### Prerequisites

- Go 1.27.1 or later (the minimum declared in `go.mod`)
- `golangci-lint` v2 — `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` (the version CI uses)
- `staticcheck` — `go install honnef.co/go/tools/cmd/staticcheck@latest`

### Clone and verify

```bash
git clone https://github.com/FlavioCFOliveira/MuxMaster.git
cd MuxMaster
make test        # all tests must pass
make test-race   # zero race conditions
make vet         # zero warnings
```

The Makefile targets exclude `reports/`, whose audit harnesses belong to the same module; `go test ./...` also runs them and takes much longer.

## Development workflow

### Branches

The repository follows the [gitflow](https://nvie.com/posts/a-successful-git-branching-model/) branching model:

| Branch | Branches off | Merges into | Purpose |
|---|---|---|---|
| `main` | — | — | Production line. Every release is a commit on `main` tagged `vMAJOR.MINOR.PATCH`. |
| `develop` | `main` | — | Integration line. All new work targets `develop`. |
| `feature/<sprint-id>-<slug>` | `develop` | `develop`, with `git merge --no-ff` | A maintainer sprint's working branch. |
| `release/<version>` | `develop` | `main`, with a version tag | Stabilises a release. `main` is then merged back into `develop`. |
| `hotfix/<version>` | `main` | `main`, with a version tag | Urgent fix on top of a release. `main` is then merged back into `develop`. |

Only `release/*` and `hotfix/*` branches merge into `main`. After every merge into `main`, `main` is merged back into `develop`, so `develop` never falls behind `main`.

### Branch protection

Maintainers apply gitflow without pull requests: they merge `release/*` and `hotfix/*` branches into `main` with `--no-ff` merge commits and push directly. The branch-protection rule for `main` is therefore limited to blocking force pushes and branch deletion, with no bypass for administrators. It does not require pull requests, status checks, or linear history, because each of those would reject the direct gitflow merges. The rule, its rationale, and the exact steps to apply it are in [`.github/branch-protection.md`](.github/branch-protection.md).

The rule is **not currently applied** on GitHub: on 2026-09-26, `gh api repos/FlavioCFOliveira/MuxMaster/branches/main/protection` returned `Branch not protected`. Until a maintainer applies it, GitHub does not block force pushes to `main` or its deletion. Even when it is applied, GitHub cannot make a direct push wait for CI: the maintainer checks that CI is green on the `release/*` or `hotfix/*` branch before merging it into `main`.

### Making a change

1. Fork the repository and create a topic branch from `develop` (for example `fix/<short-description>`).
2. Write or update tests before (or alongside) the implementation.
3. Ensure all checks pass locally (see below).
4. Open a pull request against `develop`.

The CI and commitlint workflows run on every push to `main`, `develop`, `release/**` and `hotfix/**`, and on pull requests that target `main`. CodeQL runs on pushes to `main`, on pull requests that target `main`, and weekly. None of these workflows runs on a pull request that targets `develop`, so run the local checks below before you open one: CI checks your change once it is merged and pushed to `develop`.

In `ci.yml`, the `apidiff` API compatibility check is advisory on push (incompatible changes produce a warning, and the job never fails) and blocking on pull requests against `main`, unless the pull request carries the `api-break` label.

### Local checks (run before every PR)

```bash
make test        # all tests (excluding reports/)
make test-race   # race detector
make vet         # go vet
make lint        # golangci-lint run + staticcheck
make bench       # benchmarks (excluding reports/ and competitor/); compare with benchstat
make api         # regenerate api.md after changing any exported symbol or doc comment (CI fails on a stale api.md)
```

`make check` runs `vet`, `lint` and `test-race` in one step. It does not run the benchmarks.

Each directory under `examples/` is a separate Go module (with a `replace` directive pointing at the repository root) and is not covered by the targets above or by CI. If you change one, check it from its own directory:

```bash
cd examples/<name> && go vet . && go build .
```

### Pre-push lint guard (recommended)

Install the repository's pre-push hook to block any `git push` that
introduces a `golangci-lint` finding:

```bash
make hooks-install
```

This sets `git config core.hooksPath .githooks`. Once installed, every
`git push` runs `golangci-lint run ./...` and aborts the push if any
issue is reported. Bypass in emergencies with `git push --no-verify`
(NOT recommended — CI will still reject the change).

Uninstall any time with `make hooks-uninstall`.

## Code guidelines

### Style

- Follow standard Go conventions (`gofmt`, `goimports`).
- Exported types and functions must have a doc comment (`// TypeName ...`).
- Comments explain *why*, not *what* — the code already says what.
- No external dependencies in the core package or `middleware/`.

### Tests

- Every new exported feature requires a unit test in `mux_test.go` or a dedicated `*_test.go` file.
- Edge cases and error paths must be tested, not just the happy path.
- New code in the hot path (route lookup, dispatch) must have a benchmark in `bench_test.go`.
- Tests must pass under `go test -race`.

### Performance

- The hot path must remain zero-allocation for static routes.
- Measure with `benchstat` before and after any change that touches `tree.go`, `mux.go`, or `params.go`, with at least `-count=6` so `benchstat` can report confidence intervals (see [docs/performance.md](docs/performance.md#running-benchmarks-locally)).
- Never introduce `interface{}` conversions, closures, or `context.WithValue` on the hot path.

### API compatibility

See [COMPATIBILITY.md](COMPATIBILITY.md) for the full SemVer policy and
the Tier 1 / Tier 2 / Tier 3 / Tier 4 classification of every exported
symbol.

- Do not introduce breaking changes in MINOR or PATCH releases.
- Keep compatibility with the minimum Go version declared in `go.mod`.

### Deprecation convention

When a symbol must be removed, follow the staged deprecation:

1. **Mark it.** Add a `// Deprecated:` line to the GoDoc, immediately
   below the existing description. Reference the replacement and the
   target removal version.

   ```go
   // OldThing does X.
   //
   // Deprecated: use NewThing instead. Will be removed in v2.0.0.
   func OldThing() { /* ... */ }
   ```

2. **Announce.** Add an entry to the `### Deprecated` section of the
   next release in `CHANGELOG.md`.

3. **Wait.** The deprecated symbol must remain functional for at least
   one MINOR release before removal (per
   [COMPATIBILITY.md](COMPATIBILITY.md#deprecation-policy)).

4. **Remove.** Removal is a MAJOR change, listed under `### Removed`.

CI runs `staticcheck` (which includes SA1019, use of deprecated
symbols) on the root module. The `examples/` modules are not checked in
CI, so run `staticcheck .` in any example you change.

## Commit messages

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/); the `commitlint` workflow validates every commit pushed to `main`, `develop`, `release/**` or `hotfix/**`, and every commit of a pull request against `main`. Merge commits (commits with more than one parent) are exempt:

```
<type>(<optional scope>)!: <short description>

[optional body]
```

Types: `feat`, `fix`, `perf`, `refactor`, `test`, `docs`, `ci`, `chore`, `build`, `style`, `revert`, `security`. The scope is optional and lowercase; `!` marks a breaking change. The subject is 1–100 characters.

Examples:

```
feat: add Mux.With for inline middleware scoping
perf: eliminate requestCtx heap allocation for ≤3 params
fix(middleware): look up BasicAuth users in constant time
```

## Pull requests

- Keep PRs focused on a single concern.
- Link the relevant issue if one exists.
- Update `CHANGELOG.md` under `[Unreleased]`. CI fails a push to `main`, `develop`, `release/**` or `hotfix/**`, and a pull request against `main`, that changes a non-test `.go` file outside `reports/`, `competitor/` and `examples/` without touching `CHANGELOG.md`. A pull request can be exempted with the `no-changelog` label; a push cannot.
- All CI checks must be green before merging.

## Reporting issues

Use the [GitHub issue tracker](https://github.com/FlavioCFOliveira/MuxMaster/issues).

For security vulnerabilities, see [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contributions will be licensed under the [MIT License](LICENSE).
