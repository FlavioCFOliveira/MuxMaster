# Compatibility & Versioning

MuxMaster follows [Semantic Versioning 2.0.0](https://semver.org/) and the
[Go module compatibility guidelines](https://go.dev/ref/mod#major-version-suffixes).
This document is the contract between the project and its consumers.

## Version scheme

`v<MAJOR>.<MINOR>.<PATCH>[-prerelease]`

| Bump  | When                                                                | Examples                              |
|-------|---------------------------------------------------------------------|---------------------------------------|
| MAJOR | Backward-incompatible changes to a Tier 1 surface                   | Removing `Mux.GET`, changing its signature |
| MINOR | Backward-compatible additions; behavioural fixes that may be observable | New middleware, new option fields, performance improvements |
| PATCH | Bug fixes, security patches, internal refactors                     | Race fix, panic fix, doc-only updates |

Pre-release identifiers (`-rc1`, `-beta1`, `-alpha1`) signal that the version
is not yet covered by the SemVer guarantees below; downstream packagers
should pin exact pre-release versions.

## Compatibility tiers

Every exported symbol falls into exactly one tier. The tier dictates the
guarantee.

### Tier 1 — Core Router (strongest guarantee)

The dispatch contract used by every consumer. Breaking changes require a
MAJOR bump.

- `Mux`, `*Mux` and its constructor `New()`
- `Mux.ServeHTTP` (the `http.Handler` implementation)
- `Mux.Handle`, `Mux.HandleFunc`, `Mux.HandleE`, `Mux.HandleFast`
- All method shortcuts: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`,
  `OPTIONS`, `CONNECT`, `TRACE`, `QUERY` and their `*E` and `*Fast`
  variants (every method has a `*Fast` variant on `*Mux`; there is no
  `CONNECTE` or `TRACEE`)
- `Mux.Use`, `Mux.UseFast`, `Mux.Pre`
- `Mux.Group`, `Mux.Route`, `Mux.With`, `Mux.Mount`, `Mux.ServeFiles`,
  `Mux.Match`, `Mux.ANY`
- `Group` and all of its methods
- `Params`, `Param`, `Params.Get`, `Params.Lookup`, `Params.Map` and the
  typed accessors `Int/Int64/Uint64/Float64/Bool`
- `PathParam`, `ParamsFromContext`, `RoutePattern`
- `HandlerFuncE`, `HTTPError`, `Error`
- `FastHandler`, `FastMiddleware`
- Public `Mux` struct fields documented in the README options section:
  `RedirectTrailingSlash`, `RedirectFixedPath`, `HandleMethodNotAllowed`,
  `HandleOPTIONS`, `CaseInsensitive`, `UseRawPath`, `UnescapePathValues`,
  `RedirectCode`, `NotFound`, `MethodNotAllowed`, `GlobalOPTIONS`,
  `PanicHandler`, `ErrorHandler`

### Tier 2 — Middleware sub-package

`github.com/FlavioCFOliveira/MuxMaster/middleware`. Each middleware is
documented as a constructor function and its options struct.

Breaking changes to a middleware constructor or its `Options` struct
require a MAJOR bump. Adding new fields to an `Options` struct is a MINOR
change — consumers using struct literals without field names will break,
but the recommended idiom is named fields, which is unaffected.

### Tier 3 — Introspection / Debug

`Mux.Lookup`, `Mux.Routes`, `Mux.Walk`, `Mux.WalkFast`, `RouteInfo`,
`Mux.Rebuild`. Used by tooling and tests. Breaking changes are signalled
in the CHANGELOG `### Changed` section and require a MINOR bump (Tier 3
is intentionally less rigid than Tier 1 to allow telemetry and tooling
evolution).

### Tier 4 — Internal (no guarantee)

Symbols exported because Go has no other visibility scope but documented
as "internal" in their GoDoc, plus everything under
`internal/`, `competitor/`, `reports/` and `examples/`. May change at any
time without notice.

## Deprecation policy

1. A symbol that will be removed is first **marked deprecated** with a
   `// Deprecated: <reason>. Use <replacement> instead.` comment in
   GoDoc.
2. The deprecation is announced in the CHANGELOG `### Deprecated`
   section.
3. The deprecated symbol must remain functional for **at least one
   MINOR release**. Removing it is then a MAJOR change.
4. CI runs `staticcheck` (which includes SA1019, use of deprecated
   symbols) on the root module. The `examples/` directories are separate
   modules that CI does not check; contributors run `staticcheck` in an
   example they change (see CONTRIBUTING.md).

Example timeline:

```
v1.4.0  — Mark Foo as // Deprecated: use Bar.
v1.5.0  — Foo still works. Bar is the documented path.
v2.0.0  — Foo removed.
```

## Breaking change detection (CI)

Every pull request runs [`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff)
on the root package against the most recent tagged release. Detected
incompatible changes fail the build unless the pull request carries the
`api-break` label. The `middleware` package is not covered by this check;
changes to it are reviewed against `api.md`, which CI requires to be
regenerated (`make api`) whenever an exported symbol or its doc comment
changes.

## Go version policy

`go.mod` declares the minimum supported Go version. Bumping it is a
MINOR change, not a MAJOR one — consistent with the
[Go core team's published guidance](https://go.dev/wiki/MinimumGoVersion).
The CI matrix tests against the declared minimum (Go 1.27.1) and against
`stable` on Linux, macOS and Windows, plus Linux/arm64; a non-blocking
canary job also builds and tests the root package with Go tip.

## Stability of behaviour, not just types

This project considers the following non-type properties as part of the
public contract:

- HTTP status codes returned by built-in handlers (404 NotFound, 405
  MethodNotAllowed, etc.)
- Header semantics (`Allow`, `WWW-Authenticate`, `Vary: Origin`,
  `Content-Type` defaults)
- Order in which middlewares execute relative to dispatch (`Pre` before
  tree lookup, `Use` after)
- Error sentinel identity (e.g. `errors.Is(err, ErrParamNotFound)` if it
  becomes exported)

Behavioural breaks are subject to the same MAJOR/MINOR rules as type
breaks. They will be enumerated in the CHANGELOG `### Changed` section
with a brief migration note.

## Security patches

Security fixes for supported versions are released as PATCH versions and
announced via GitHub Security Advisories. The CHANGELOG `### Security`
section enumerates each finding with its identifier (e.g.
`CSA-2026-0060`, `HPS-2026-0005`).

## Questions

Open an issue on
[github.com/FlavioCFOliveira/MuxMaster](https://github.com/FlavioCFOliveira/MuxMaster/issues)
if you find an undocumented behaviour you depend on; we will either
formalise it as part of the public contract or document it as Tier 4.
