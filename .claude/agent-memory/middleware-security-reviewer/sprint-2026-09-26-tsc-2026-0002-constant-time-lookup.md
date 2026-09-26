---
name: sprint-2026-09-26-tsc-2026-0002-constant-time-lookup
description: rmp #290/#29 — BasicAuth constant-time user lookup implemented (removes TSC-2026-0002 map-lookup oracle); O(n) per-user cost measured; not committed
metadata:
  type: project
---

rmp task #290, item #29 (TSC-2026-0002). Decision taken by the user (flaviocfo@gmail.com):
replace `middleware/basic_auth.go`'s `hashedCreds map[string][32]byte` lookup with an
unordered `[]basicAuthEntry{userHash, passHash [32]byte}` slice, scanned in FULL on every
request via `subtle.ConstantTimeCompare` (username match) + `subtle.ConstantTimeCopy`
(password-digest selection, no early exit, no data-dependent branch). Branch
`feature/20-backlog-clearance`. **Not committed** — user explicitly said not to.

## Files changed (uncommitted at handoff)

- `middleware/basic_auth.go` — core fix; GoDoc updated to explain the new design and cite
  TSC-2026-0002
- `middleware/middleware_test.go` — added `TestBasicAuth_ManyUsers_FirstMiddleLastAuthenticate`,
  `TestBasicAuth_ManyUsers_WrongPasswordRejected`, `TestBasicAuth_ManyUsers_UnknownUserRejected`,
  `TestBasicAuth_EmptyUsernameAndPassword` (Basic Og==), `TestBasicAuth_EmptyUsernameRegistered`
- `middleware/bench_test.go` — added `BenchmarkBasicAuth` (1/10/100 users × hit/miss)

## Verification

- `go build ./...`, `go vet ./...`, `staticcheck ./middleware/...`, `golangci-lint run ./...`:
  all clean.
- `go test -race -count=3 ./middleware/...`: PASS, no races.
- All existing BasicAuth tests still pass unmodified — behaviour (realm/WWW-Authenticate,
  401 body, empty-credentials, nil-map panic, malformed base64) preserved exactly.

## Performance cost (benchstat, before=map lookup vs after=constant-time scan, same host)

Zero allocation/byte delta (32 B / 2 allocs hit, 168 B / 8 allocs miss — unchanged). Pure CPU
cost, linear in registered-user count, ~25-27 ns per registered user per request (e.g. 100-user
hit path: 202 ns → 2845 ns, +1307%; 100-user miss: 593 ns → 3227 ns, +444%). This is the
expected, disclosed cost of O(n) scanning — acceptable for small/medium credential sets (tens
to low hundreds of users); operators with very large static credential lists should be told to
prefer a different auth primitive (e.g. hashed-lookup-backed APIKey/JWT if enumeration risk is
accepted, or an external IdP). Full numbers in the task's final report to the user.

## Timing verification (3× independent runs, `-tags timing`, N=200,000, shared/virtualised sandbox)

`TestTiming_BasicAuth_UserExistsVsNotExists` mean-diff across 3 runs: 121.47 ns, 15.18 ns,
103.61 ns — all far under the SECURITY.md TSC-2026-0002 accepted bound (700 ns), and lower than
the pre-fix historical range (61-349 ns) recorded there. `TestTiming_BasicAuth_ValidVsInvalid`
(TSC-2026-0001) and `TestTiming_BasicAuth_PasswordLengthOracle` (TSC-2026-0013) both still pass
comfortably within their bounds — unaffected by this change (different oracle, same
hash-before-compare foundation).

## Outstanding

**SECURITY.md was deliberately NOT edited** — the task instructions said to report the timing
result back to the user instead, since TSC-2026-0002's "accepted oracle" framing (map-lookup is
architectural/out-of-scope) is now stale: the oracle has been fixed at the code level, not just
bounded. The user needs to decide how to update/retire the TSC-2026-0002 entry (and possibly
reconsider whether the analogous `TSC-2026-0004` — `APIKey` — should get the same treatment;
SECURITY.md's own text already floats that as "only worthwhile for very small key sets", which
this sprint's numbers now make concrete: ~26 ns/user).

See also [[sprint-2026-09-26-closed-task-audit-gaps]] (same branch, same day, different task).
