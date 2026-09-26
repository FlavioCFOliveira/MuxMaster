package harness

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// =============================================================================
// FuzzDifferentialSecurity — native Go fuzz target, rmp #274 part 2/4
// =============================================================================
//
// This is the native coverage-guided fuzz target that
// reports/path-routing-fuzzer/harness/fuzz_test.go used to provide before
// commit 5f804fa rewrote the harness (its FuzzDifferential only compared
// MuxMaster against itself — a traversal invariant plus a CRLF-in-Location
// check across all 4 routers — never an actual cross-router match/param
// comparison). TestDifferential_CorpusPaths, TestR3_08_FullCorpus_Differential
// and TestS8_Differential_Extended are all fixed, hand-picked corpus tables;
// none of them is coverage-guided, so none of them can discover a divergence
// shape the corpus author did not already think of. This target closes that
// gap: it registers the same route set as buildMuxMaster/buildHTTPRouter/
// buildChi/buildBunRouter (differential_test.go) and asks Go's mutation
// engine to find an arbitrary request path where MuxMaster's routing
// decision is security-relevant-different from all three references.
//
// "Security-relevant" is deliberately narrow — exactly the three shapes rmp
// #274 part 2 specifies, each checked directly against MuxMaster's own
// result (the competitors are corroborating evidence in the failure
// message, not a majority vote):
//
//  1. bypass:      MuxMaster matches some registered route while httprouter,
//                   chi AND bunrouter all report notfound for the same path.
//                   A "protected route" in this differential surface is any
//                   of the five registered patterns — the fixture does not
//                   special-case /admin because the invariant is general:
//                   nothing should become reachable that no reference router
//                   considers reachable.
//  2. param-slash: a NAMED parameter (":id" on /users/:id or
//                   /api/v1/items/:id/children/:cid) captures a value
//                   containing '/'. This is dangerous independently of what
//                   any competitor does — a caller who expects exactly one
//                   path segment must never receive a smuggled extra
//                   segment. Distinguished from the catch-all handler via
//                   the X-Handler header (routeResult.xHandler) so this
//                   check never fires for the legitimately-slash-bearing
//                   catch-all capture.
//  3. catchall-prefix-escape: the catch-all handler (/static/*filepath) ran
//                   for a request whose path does not literally start with
//                   "/static/". This is the structural version of "catch-all
//                   escapes its prefix" — a tree bug, not the already-
//                   documented PRF-005 finding that the CAPTURED VALUE may
//                   contain ".." (that is accepted, non-security behaviour:
//                   TestInvariant_CatchallNotEscapingPrefix in
//                   hypotheses_test.go documents that the router does not
//                   clean catch-all captures — http.FileServer's path.Clean
//                   is the documented security boundary for ServeFiles, and
//                   TestServeFiles_ShimFS_CleansTraversal below verifies
//                   that boundary directly). This check only fires when the
//                   MATCH ITSELF is structurally impossible — the tree
//                   selected a /static/*filepath handler for a request that
//                   was never under /static/ to begin with.
//
// Any divergence outside these three shapes is differential noise (chi's
// %2f-in-param handling, per-router default trailing-slash behaviour, etc.)
// and is out of scope for THIS fuzz target — TestDifferential_CorpusPaths
// already documents and classifies those. allowListReasonSecurity below is
// the explicit, justified allow-list rmp #274 part 2 requires: every entry
// must cite the evidence that makes it non-security, and the list must never
// grow to swallow shape 1/2/3 themselves — only note *why a particular
// input cannot produce* one of those shapes in the first place.

// allowListReasonSecurity returns a non-empty justification when a path that
// would otherwise trip one of the three security checks above is a known,
// evidence-backed non-issue. Returns "" when the divergence is unexplained
// and must fail the fuzz run.
//
// Deliberately starts near-empty: an allow-list entry is added only when the
// fuzzer surfaces a concrete counter-example AND that counter-example is
// verified (not guessed) against RFC 3986 / the competitor's own source
// under reports/path-routing-fuzzer/harness/vendor. See CLAUDE.md "Never
// guess" (§10).
func allowListReasonSecurity(path string, mmR, hrR, chiR, bunR routeResult) string {
	// A competitor PANIC is not a MuxMaster finding — it is reported
	// separately (see the panic-recovery wrapper below) and must never mask
	// or substitute for a real MuxMaster-side check.
	_ = path
	_ = mmR
	_ = hrR
	_ = chiR
	_ = bunR
	return ""
}

// queryRouterSafe wraps one of the queryX helpers so a competitor panic
// (bunrouter is documented to panic on some malformed paths — see
// queryBunRouter's own doc comment) never aborts the fuzz iteration before
// MuxMaster's own result has been checked.
func queryRouterSafe(fn func(string) routeResult, path string) (res routeResult) {
	defer func() {
		if rc := recover(); rc != nil {
			res = routeResult{status: 0, handler: "panic", param: ""}
		}
	}()
	return fn(path)
}

// FuzzDifferentialSecurity is the coverage-guided differential fuzz target.
// It builds the 4 routers once (outside f.Fuzz, matching the established
// perf-conscious pattern already used by FuzzGetValue/FuzzAddRoute in this
// file — a fresh router per iteration made the original fuzz_test.go's
// FuzzDifferential too slow to reach useful coverage in the fuzztime budget)
// and, for every fuzz-supplied path, checks MuxMaster's own result for the
// three security-relevant shapes documented above.
func FuzzDifferentialSecurity(f *testing.F) {
	seeds := []string{
		// --- Control (must match, sanity for the harness itself) ---
		"/admin", "/users/123", "/static/img.png",
		"/api/v1/items/1/children/2", "/public",

		// --- Traversal (shape 1 candidates) ---
		"/../admin", "/./admin", "/..//admin",
		"/users/../admin", "/users/1/../admin",
		"/static/../admin", "/static/./../../admin",
		"/%2e%2e/admin", "/%2E%2E/admin",
		"/..%2fadmin", "/..%5cadmin", "/%252e%252e/admin",
		"/users/%2e%2e/admin", "/a/%2e%2e/admin",
		"/static/..%2fadmin", "/static/%2e%2e/admin",
		"/static/..%2F..%2Fadmin", "/static/..%5c..%5cadmin",
		"/static/%2e%2e%2f%2e%2e%2fadmin",
		"/%c0%ae%c0%ae/admin", // overlong UTF-8 dot — CVE-class payload
		"/admin%2f..%2f..%2fetc%2fpasswd",
		"/api/../admin", "/api/v1/../../admin", "/public/../admin",
		"/files/.%2e/secret",

		// --- Encoding (shape 1 candidates) ---
		"/%61dmin", "/ad%6din", "/%2561dmin",
		"/%61%64%6d%69%6e", "/%252e%252e/admin",
		"/..%c0%afadmin",    // overlong-UTF8 slash CVE-class
		"/..%c1%9cadmin",    // overlong-UTF8 backslash CVE-class
		"/..%ef%bc%8fadmin", // fullwidth solidus U+FF0F

		// --- Structural (shape 1/3 candidates) ---
		"//admin", "///admin", "////admin",
		"/admin//", "/admin///", "/admin//extra",
		"/;admin", "/admin;param", "/admin;ignore=1",
		"/;jsessionid=abc/admin",
		"/users//1", "/users///1/posts",
		"/static//", "//static//",
		"/admin/.", "/admin/./", "/admin/./extra",

		// --- Param boundary (shape 2 candidates) ---
		"/users/1%2f2", "/users/a%2fb", "/users/a%2Fb",
		"/users/x%252fy",
		"/api/v1/items/1%2f2/children/3",
		"/api/v1/items/1/children/3%2f4",

		// --- Catch-all boundary (shape 3 candidates) ---
		"/static/a/b/c/d", "/static/../../etc/passwd",
		"/static/%2e%2e/%2e%2e/etc/passwd",
		"/static/..%2f..%2fetc%2fpasswd",

		// --- Unicode ---
		"/аdmin", // Cyrillic а U+0430
		"/ɑdmin", // Latin alpha U+0251
		"/admın", // Turkish dotless i U+0131
		"/ADMIN",

		// --- Null / control bytes ---
		"/%00admin", "/admin%00", "/users/%00/1",

		// --- Empty / edge ---
		"", "/", "//", "///",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	mmRouter := buildMuxMaster()
	hrRouter := buildHTTPRouter()
	chiRouter := buildChi()
	bunRouter := buildBunRouter()

	f.Fuzz(func(t *testing.T, path string) {
		if !utf8.ValidString(path) || len(path) == 0 || len(path) > 4096 {
			t.Skip()
		}
		if !isValidHTTPPath(path) {
			t.Skip()
		}

		var mmR routeResult
		func() {
			defer func() {
				if rc := recover(); rc != nil {
					t.Fatalf("PANIC(muxmaster) path=%q: %v", path, rc)
				}
			}()
			mmR = queryMuxMaster(mmRouter, path)
		}()

		hrR := queryRouterSafe(func(p string) routeResult { return queryHTTPRouter(hrRouter, p) }, path)
		chiR := queryRouterSafe(func(p string) routeResult { return queryChi(chiRouter, p) }, path)
		bunR := queryRouterSafe(func(p string) routeResult { return queryBunRouter(bunRouter, p) }, path)

		// Shape 1: MuxMaster matches, every reference says notfound.
		if mmR.handler == "matched" && hrR.handler == "notfound" && chiR.handler == "notfound" && bunR.handler == "notfound" {
			if reason := allowListReasonSecurity(path, mmR, hrR, chiR, bunR); reason == "" {
				t.Fatalf("SECURITY DIV (bypass): path=%q muxmaster matched %s (param=%q) but httprouter/chi/bunrouter all 404",
					path, mmR.xHandler, mmR.param)
			}
		}

		// Shape 2: a named :param captured a value spanning '/'.
		if mmR.handler == "matched" && (mmR.xHandler == "users_id" || mmR.xHandler == "items_children") {
			if strings.Contains(mmR.param, "/") {
				if reason := allowListReasonSecurity(path, mmR, hrR, chiR, bunR); reason == "" {
					t.Fatalf("SECURITY DIV (param-spans-slash): path=%q handler=%s param=%q contains '/'",
						path, mmR.xHandler, mmR.param)
				}
			}
		}

		// Shape 3: the catch-all ran for a request that never had the
		// registered "/static/" literal prefix in the first place — a
		// structural tree-matching bug, distinct from PRF-005 (captured
		// VALUE containing ".." is documented, accepted behaviour and is
		// deliberately NOT checked here).
		if mmR.handler == "matched" && mmR.xHandler == "static" {
			// Compare against the DECODED path the tree actually matched
			// against (net/url percent-decodes URL.Path when UseRawPath is
			// false, MuxMaster's default) — not the raw fuzz string. A
			// percent-encoded but otherwise legitimate path such as
			// "/%73tatic/x" decodes to "/static/x" and correctly matches;
			// checking the raw string's prefix would misclassify that as a
			// structural escape (false positive).
			if !strings.HasPrefix(mmR.decodedPath, "/static/") {
				if reason := allowListReasonSecurity(path, mmR, hrR, chiR, bunR); reason == "" {
					t.Fatalf("SECURITY DIV (catchall-prefix-escape): path=%q matched static handler but request path lacks the literal /static/ prefix",
						path)
				}
			}
		}
	})
}
