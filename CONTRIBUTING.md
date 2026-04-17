# Contributing to MuxMaster

Thank you for your interest in contributing. This guide covers everything you need to get started.

## Getting started

### Prerequisites

- Go 1.22 or later
- `golangci-lint` — `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
- `staticcheck` — `go install honnef.co/go/tools/cmd/staticcheck@latest`

### Clone and verify

```bash
git clone https://github.com/FlavioCFOliveira/MuxMaster.git
cd MuxMaster
go test ./...          # all tests must pass
go test -race ./...    # zero race conditions
go vet ./...           # zero warnings
```

## Development workflow

### Branches

- `main` — stable, always passing CI. Direct pushes are not allowed.
- Feature branches: `feat/<short-description>`
- Bug fix branches: `fix/<short-description>`
- Performance branches: `perf/<short-description>`

### Making a change

1. Fork the repository and create a branch from `main`.
2. Write or update tests before (or alongside) the implementation.
3. Ensure all checks pass locally (see below).
4. Open a pull request against `main`.

### Local checks (run before every PR)

```bash
go test ./...                          # all tests
go test -race ./...                    # race detector
go test -bench=. -benchmem ./...       # benchmarks (no regressions)
go vet ./...                           # static analysis
golangci-lint run                      # linter suite
staticcheck ./...                      # advanced analysis
```

Or use the Makefile:

```bash
make check    # runs all of the above
```

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
- Measure with `benchstat` before and after any change that touches `tree.go`, `mux.go`, or `params.go`.
- Never introduce `interface{}` conversions, closures, or `context.WithValue` on the hot path.

### API compatibility

- Do not introduce breaking changes in MINOR or PATCH releases.
- Deprecate before removing: add `// Deprecated: use X instead.` to the GoDoc.
- Keep compatibility with the minimum Go version declared in `go.mod`.

## Commit messages

```
<type>: <short description>

[optional body]
```

Types: `feat`, `fix`, `perf`, `refactor`, `test`, `docs`, `ci`, `chore`.

Examples:

```
feat: add Mux.With for inline middleware scoping
perf: eliminate requestCtx heap allocation for ≤3 params
fix: allow static children on param nodes
```

## Pull requests

- Keep PRs focused on a single concern.
- Link the relevant issue if one exists.
- Update `CHANGELOG.md` under `[Unreleased]`.
- All CI checks must be green before merging.

## Reporting issues

Use the [GitHub issue tracker](https://github.com/FlavioCFOliveira/MuxMaster/issues).

For security vulnerabilities, see [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contributions will be licensed under the [MIT License](LICENSE).
