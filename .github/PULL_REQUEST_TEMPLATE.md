<!--
Thanks for contributing to MuxMaster! Please complete the checklist
below. Items irrelevant to your change may be left unchecked but
should not be removed — reviewers use them as a reading guide.
-->

## Summary

<!-- One paragraph: what this PR changes and why. Link the relevant issue
     if any (Fixes #N, Closes #N, Refs #N). -->

## Type of change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Performance improvement
- [ ] Documentation only
- [ ] Refactor / internal cleanup
- [ ] Breaking change (`api-break` label required)

## Checklist

- [ ] Tests added or updated (`mux_test.go`, `middleware/middleware_test.go`,
      etc.). New exported behaviour has at least one test.
- [ ] `go test -race ./...` passes locally.
- [ ] `go vet`, `golangci-lint run`, and `staticcheck` are clean.
- [ ] `make api` was run and `api.md` is updated (if exported surface
      changed).
- [ ] `CHANGELOG.md` updated under `[Unreleased]`.
- [ ] No external dependencies introduced (zero-deps invariant).
- [ ] Hot-path change is benchmarked with `benchstat` (if relevant).
- [ ] GoDoc on every new exported symbol; deprecation convention
      followed if removing/renaming (see CONTRIBUTING.md).

## Performance impact

<!-- For changes touching mux.go, params.go, tree.go, response.go or any
     middleware: include a short benchstat output (or "n/a"). -->

```
n/a
```

## Security considerations

<!-- For changes touching authentication, request parsing, redirects,
     panics or middleware composition: list the security review applied
     (or "n/a"). -->

n/a
