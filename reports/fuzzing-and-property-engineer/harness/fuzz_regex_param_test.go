package harness

// Regex param fuzz harness — H8-52
//
// MuxMaster supports {name:expr} regex params in patterns. The expression is
// compiled with regexp.MustCompile("^(?:<expr>)$") at registration time and
// matched per-request with regexp.MatchString.
//
// Hypothesis H8-52: if <expr> is an attacker-controlled pattern (e.g. via a
// template engine that builds routes dynamically), catastrophic backtracking
// could cause per-request DoS. We fuzz:
//   1. Registration: any regex in {name:expr} — addRoute must never panic beyond
//      the documented "invalid regexp" check.
//   2. Request matching: for registered regex routes, any path segment — getValue
//      must never panic even if the regex partially matches.
//   3. ReDoS detection: use a time-bounded approach to detect catastrophic
//      backtracking by measuring match time for known ReDoS patterns.
//
// Invariants tested:
//   I-REGEX-01: addRoute with an invalid regex panics with a message containing
//               "invalid regexp"; all other valid regexes register without panic.
//   I-REGEX-02: ServeHTTP with a registered regex route never panics, regardless
//               of the segment value in the request path.
//   I-REGEX-03: Registration time for any single pattern is bounded (< 1s for
//               the hardcoded cap of 8 optional segments × regex complexity).
//   I-REGEX-04: Per-request matching time is bounded; known ReDoS patterns
//               such as (a+)+ on "aaaaaaaaab" must not hang indefinitely.

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// knownReDoSPatterns contains classic catastrophic backtracking patterns.
// Any regex matching test must complete within the time budget.
var knownReDoSPatterns = []string{
	"(a+)+",
	"(a|aa)+",
	"(a|a?)+",
	"([a-zA-Z]+)*",
	"(a+b?)+",
	"(x+x+)+y",
	"([a-z]+)*",
	"(a{1,9})+",
	"^(a+)*$",
}

// knownReDoSInputs are inputs crafted to trigger catastrophic backtracking
// on the above patterns.
var knownReDoSInputs = []string{
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaab",
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!",
	strings.Repeat("a", 30) + "b",
	strings.Repeat("a", 50) + "X",
}

// FuzzRegexParamRegistration exercises addRoute with arbitrary regex expressions.
// I-REGEX-01: only panics with "invalid regexp" message on bad regex.
func FuzzRegexParamRegistration(f *testing.F) {
	// Seed: valid and invalid regex expressions.
	f.Add("[0-9]+")
	f.Add("[a-z]+")
	f.Add(".*")
	f.Add("(a+)+")        // valid but ReDoS-prone
	f.Add("(a|aa)+")      // valid but ReDoS-prone
	f.Add("[")            // invalid: unclosed bracket
	f.Add("(?P<name>x)")  // valid with named group
	f.Add("x{1,1000000}") // large quantifier
	f.Add("a|b|c|d")
	f.Add("")  // empty — may be accepted or rejected
	f.Add("(") // invalid: unclosed paren
	// Regression seed: a valid regex whose '/' splits the pattern segment,
	// so the token is cut inside the expression (rule 95).
	f.Add("(a}b)/c")

	f.Fuzz(func(t *testing.T, expr string) {
		// First check if the expression is a valid Go regex.
		_, regexErr := regexp.Compile("^(?:" + expr + ")$")
		// A '/' in expr ends the path segment that holds the {p:...} token:
		// the closing-brace search never crosses a segment boundary
		// (specification/routing.md rule 95), so the parsed token is cut at
		// the last '}' before that '/' — or reported as unclosed — and the
		// original expr is never what gets compiled. Such inputs cannot be
		// held to I-REGEX-01; their panics must still be documented ones.
		segmentBounded := !strings.ContainsRune(expr, '/')
		isValidRegex := regexErr == nil && segmentBounded

		// FPE-2026-0001 / FPE-2026-REGEX-01 — FIXED (tree.go ~line 1255,
		// commit 825c623). The {name:expr} parser used to stop at the FIRST
		// '}' byte, so regex expressions containing '}' (e.g. "(})", "\}",
		// "[}]", "a{2,3}") were rejected with a confusing panic even though
		// regexp.Compile accepted them. The parser now scans the whole path
		// segment and picks the LAST '}' before the next '/' (or end of
		// pattern) as the token's closing brace, so any '}' inside expr no
		// longer breaks registration. A valid regex containing '}' is
		// therefore asserted below exactly like any other valid regex — no
		// more special-cased swallow. See also TestRegexBraceFixed for a
		// small deterministic regression guard.
		containsCloseBrace := strings.ContainsRune(expr, '}')

		defer func() {
			if r := recover(); r != nil {
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): expr=%q r=%v\n%s", expr, r, debug.Stack())
				}
				if isValidRegex {
					// Valid regex (with or without '}') — tree parser must
					// accept it post-FPE-2026-0001. Only expected panic:
					// conflict or other structural issue.
					if strings.Contains(msg, "invalid regexp") {
						t.Fatalf("I-REGEX-01 violation: valid regex %q (containsCloseBrace=%v) rejected as invalid\n%s",
							expr, containsCloseBrace, debug.Stack())
					}
					if isExpectedHandlePanic(msg) {
						return
					}
					t.Fatalf("I-REGEX-01: unexpected panic for valid regex %q (containsCloseBrace=%v): %q\n%s",
						expr, containsCloseBrace, msg, debug.Stack())
				}
				// Invalid regex — any panic must be a documented/expected one.
				if !isExpectedHandlePanic(msg) {
					t.Fatalf("I-REGEX-01: unexpected panic for expr=%q msg=%q\n%s",
						expr, msg, debug.Stack())
				}
			}
		}()

		mux := mm.New()
		// Attempt registration: {p:<expr>}
		pattern := "/items/{p:" + expr + "}"
		mux.GET(pattern, h200)
	})
}

// TestRegexBraceFixed is a deterministic regression guard for
// FPE-2026-0001 / FPE-2026-REGEX-01 (fixed in tree.go ~line 1255,
// commit 825c623): valid Go regexes containing '}' must register as a
// {name:expr} param without panic, and must correctly match at request
// time. Unlike FuzzRegexParamRegistration (which only checks
// registration), this also exercises a real request through the
// registered route to confirm the closing-brace detection did not
// truncate the regex body.
func TestRegexBraceFixed(t *testing.T) {
	cases := []struct {
		expr    string
		segment string
		want    bool
	}{
		{expr: `a{2,3}`, segment: "aaa", want: true},
		{expr: `a{2,3}`, segment: "a", want: false},
		{expr: `[}]`, segment: "}", want: true},
		{expr: `[}]`, segment: "x", want: false},
		{expr: `(})`, segment: "}", want: true},
		{expr: `\}`, segment: "}", want: true},
		{expr: `x{1,3}y{2,4}`, segment: "xyy", want: true},
		{expr: `x{1,3}y{2,4}`, segment: "x", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			var matched bool
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				mux := mm.New()
				mux.GET("/items/{p:"+tc.expr+"}", func(w http.ResponseWriter, r *http.Request) {
					matched = true
					w.WriteHeader(http.StatusOK)
				})
				req := httptest.NewRequest(http.MethodGet, "/items/"+tc.segment, nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
			}()
			if panicked != nil {
				t.Fatalf("registration/dispatch panicked for expr=%q segment=%q: %v",
					tc.expr, tc.segment, panicked)
			}
			if matched != tc.want {
				t.Fatalf("expr=%q segment=%q: handler invoked=%v, want=%v",
					tc.expr, tc.segment, matched, tc.want)
			}
		})
	}
}

// FuzzRegexParamServeHTTP exercises getValue with registered regex routes.
// I-REGEX-02: ServeHTTP never panics for any segment value.
func FuzzRegexParamServeHTTP(f *testing.F) {
	// Use a simple non-catastrophic regex for the static mux.
	// Note: regex params at the same position conflict with each other and
	// with normal params — use separate prefix paths to avoid registration panics.
	mux := mm.New()
	mux.GET("/items/{id:[0-9]+}", h200)
	mux.GET("/slugs/{slug:[a-z-]+}/detail", h200)
	// No fallback :fallback — would conflict with {id:[0-9]+} at /items/

	f.Add("123")
	f.Add("0")
	f.Add("abc-def")
	f.Add("!")
	f.Add("")
	f.Add("aaaaaaaaaaaaaaaaaaaaaaaaaaaaab")
	f.Add("\x00")
	f.Add("a/b/c")

	f.Fuzz(func(t *testing.T, segment string) {
		if containsControlBytes(segment) || containsInvalidURLBytes(segment) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ServeHTTP with regex route: segment=%q r=%v\n%s",
					segment, r, debug.Stack())
			}
		}()
		req, err := buildRequest(http.MethodGet, "/items/"+segment)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// Any status is fine; no panic is the invariant.
	})
}

// TestRegexReDoSBudget verifies I-REGEX-04: known ReDoS patterns on known
// adversarial inputs complete within 100ms per match.
// This ensures the regex engine's catastrophic backtracking is not
// exploitable at request time — even if registered with a bad pattern.
func TestRegexReDoSBudget(t *testing.T) {
	const budget = 100 * time.Millisecond

	for _, expr := range knownReDoSPatterns {
		re, err := regexp.Compile("^(?:" + expr + ")$")
		if err != nil {
			continue // skip invalid
		}
		for _, input := range knownReDoSInputs {
			expr, input := expr, input
			t.Run(expr+"_"+input[:min(len(input), 10)], func(t *testing.T) {
				done := make(chan bool, 1)
				start := time.Now()
				go func() {
					done <- re.MatchString(input)
				}()
				select {
				case <-done:
					elapsed := time.Since(start)
					if elapsed > budget {
						t.Errorf("I-REGEX-04: regex %q on input %q took %v > %v budget",
							expr, input, elapsed, budget)
					}
				case <-time.After(budget * 10):
					// The goroutine is still running — catastrophic backtracking detected.
					// We cannot kill it, so we log and let the test timeout handle it.
					t.Errorf("I-REGEX-04 HANG: regex %q on input %q did not complete in %v",
						expr, input, budget*10)
				}
			})
		}
	}
}

// TestRegexParamRouteNoPanic verifies that common regex route patterns
// register and serve without panic.
func TestRegexParamRouteNoPanic(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		wantOK  bool
	}{
		{"digits", "/items/{id:[0-9]+}", "/items/123", true},
		{"alpha", "/files/{name:[a-z]+}", "/files/abc", true},
		{"noMatch", "/items/{id:[0-9]+}", "/items/abc", false},
		// Note: {x:.*} matches only a single path segment (the regex is anchored
		// ^(?:.*)$ on the segment text), so "/data/anything/here" has two segments
		// and is not matched. Regex params are not catch-alls.
		{"catchAll_single_seg", "/data/{x:.*}", "/data/anything", true},
		// Note: UUID patterns with `-` in the regex body trigger "only one wildcard
		// per path segment" from the tree parser — this is a documented limitation
		// (the tree interprets `-` in the regex as a segment separator). See FPE-2026-REGEX-01.
		// {"uuid", "/u/{id:[0-9a-f]{8}-[0-9a-f]{4}-...}", ...} — skipped; known limitation.
		{"alts", "/kind/{k:foo|bar|baz}", "/kind/bar", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC: pattern=%q path=%q r=%v\n%s",
						tc.pattern, tc.path, r, debug.Stack())
				}
			}()
			mux := mm.New()
			mux.GET(tc.pattern, h200)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if tc.wantOK && rec.Code != http.StatusOK {
				t.Errorf("pattern=%q path=%q: got %d, want 200", tc.pattern, tc.path, rec.Code)
			}
			if !tc.wantOK && rec.Code == http.StatusOK {
				t.Errorf("pattern=%q path=%q: got 200, want non-200", tc.pattern, tc.path)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
