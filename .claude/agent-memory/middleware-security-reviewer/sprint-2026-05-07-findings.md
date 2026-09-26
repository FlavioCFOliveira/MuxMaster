---
name: Sprint 2026-05-07 JWT/OAuth2/APIKey/Full-16-middleware audit findings
description: Exhaustive pre-release v1.2.0 audit of all 17 middleware files; findings and verdicts MSR-2026-0051 through MSR-2026-0061
type: project
---

Commit f4faa54. Audited all 17 middleware files (basic_auth, api_key, jwt_auth, oauth2, cors, compress, real_ip, recoverer, throttle, timeout, logger, request_id, clean_path, strip_slashes, with_value, set_header, no_cache). Harness: /reports/middleware-security-reviewer/harness/security_harness_test.go — 109 tests, 2 hard failures (both unfixed known findings).

**Confirmed findings (rmp tasks created):**
- MM-2026-0051 (Medium, ID 4): CORS missing Vary:Origin when reflecting specific whitelisted origin. CDN cache-poisoning risk.
- MM-2026-0052 (Low, ID 5): APIKey 401 missing WWW-Authenticate header (RFC 7235).
- MM-2026-0053 (Low, ID 6): Compress does not skip already-compressed MIME types.
- MSR-RE-V2-001 (Medium, ID 7): Recoverer logs r.URL.Path via slog WITHOUT sanitisation. slog TextHandler does NOT apply QuoteToASCII — raw CRLF in URL.Path reaches log output, enabling log injection.
- MSR-2026-0055 (High, ID 40): RealIP() no-args mode trusts ALL peers. XFF spoofable by any client. Only safe behind a single known proxy. Confirmed by test.
- MSR-2026-0056 (Low, ID 41): NoCache missing Surrogate-Control: no-store and X-Accel-Expires: 0 — CDN/nginx-accelerator edges may cache sensitive responses.
- MSR-2026-0057 (Medium, ID 42): Recoverer slog path log (extends MSR-RE-V2-001) — slog.String("path", r.URL.Path) emits raw control chars. Confirmed in test output.
- MSR-2026-0058 (Low, ID 43): CORS empty AllowedOrigins silently passes requests through — operator misconfiguration trap.
- MSR-2026-0059 (Low, ID 44): WithValue allows string context keys — cross-package collision risk (CWE-1021).
- MSR-2026-0060 (Low, ID 45): Compress does not emit Vary: Accept-Encoding for small (uncompressed) responses — CDN cache key gap.
- MSR-2026-0061 (Medium, ID 46): CleanPath does not zero RawPath when %2e%2e traversal present in percent-encoded form. path.Clean does not decode %2e, so RawPath retains encoded traversal while decoded Path is cleaned. Router preferring RawPath bypasses protection.

**Cleared verdicts (NOT exploitable):**
- JWT alg-confusion HS256 vs RS256: NOT exploitable. allowedAlgs gate prevents cross-family dispatch.
- alg=none: NOT exploitable. "none" not in algorithm switch; panics at construction if configured.
- kid injection: NOT exploitable. No JWKS — kid is ignored.
- OAuth2 fail-open: NOT exploitable. All error paths return 401 (fail-closed).
- OAuth2 SSRF: NOT exploitable. Token in POST body only, endpoint fixed at construction.
- BasicAuth credential log leak: PASS. Logger only logs method/path/status/duration.
- RequestID CRLF/oversized: PASS. validRequestID rejects all control chars and >128 byte values.
- JWT RawPayload leak: PASS. 401 response never contains payload claims.
- JWT HMAC pool safety: PASS. mac = h.Sum(nil) is a new slice; hmac.Equal after pool.Put is safe.

**Gap iteration 2026-05-07 (threat-modeler H-G + H-OAuth2 + H-JWT-crit):**
- MSR-2026-0062 (High, ID 61, BUG): Group.Mount() bypasses group-level Use() middleware — auth not applied to inner handler. CWE-306/CWE-284. Tests FAIL confirmed. Fix: wrapMiddleware(h, g.middleware) in group.go Mount().
- MSR-2026-0063 (Medium, ID 62, IMPROVEMENT): OAuth2 introspection cache poisoning blast-radius up to 60s (default CacheTTL). CWE-345/CWE-613. No code defect — documentation + CacheDisabled option needed.
- MSR-2026-0064 (Info, ID 63, TASK): JWT crit header rejection CONFIRMED compliant. jwt_auth.go:271 rejects all non-empty crit arrays. 7-test regression harness added in harness/jwt_crit_header_test.go. All PASS.

**How to apply:** In future sprints, reference these verdicts before re-auditing. Focus fixes on:
1. MSR-2026-0062 (ID 61): Group.Mount() middleware bypass — HIGH, one-line fix in group.go
2. Recoverer slog path sanitisation (ID 7/42) — medium severity, trivial 1-line fix
3. CleanPath %2e%2e bypass (ID 46) — medium severity, needs url.PathUnescape check
4. MSR-2026-0063 (ID 62): OAuth2 GoDoc + CacheDisabled option — medium, docs/API change
5. CORS Vary:Origin (ID 4) — 1-line fix
6. APIKey WWW-Authenticate (ID 5) — 1-line fix
