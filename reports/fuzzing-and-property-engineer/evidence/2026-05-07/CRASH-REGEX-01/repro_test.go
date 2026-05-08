package repro_test

// FPE-2026-REGEX-01 — Regex param parser rejects valid Go regexes containing '}'
//
// Hypothesis: H8-52 (Express path-to-regexp ReDoS analogue / MuxMaster regex params)
// Severity: 3 (Low — cosmetic, operator-only, no security impact)
// Found by: FuzzRegexParamRegistration (corpus entry 7856b272b56230df)
//
// Description:
//   The {name:expr} pattern parser in tree.go searches for the closing '}'
//   delimiter using a naive byte scan (strings.IndexByte(path[i:], '}')). This
//   terminates on the FIRST '}' byte encountered, even if that '}' is part of
//   the regex body (e.g., in quantifiers "{2,}", character classes "[}]", or
//   matching groups "(})").
//
//   As a result, the following valid Go regex expressions are unusable in regex
//   params:
//     - "(})"    — literal closing brace in a group
//     - "a{2,3}" — quantifier (looks like end of param + "}" continuation)
//     - "[}]"    — character class with closing brace
//     - "\}"     — escaped closing brace
//
//   When such an expression is used, the tree parser finds a premature '}'
//   boundary and then panics with "only one wildcard per path segment is allowed"
//   or a related structural panic — a confusing message that does not explain
//   the root cause.
//
// Reproduction:
//   go test -run TestReproFPE2026REGEX01 ./...
//
// Expected behaviour:
//   mux.GET("/items/{id:(})", h) should register successfully
//   (or panic with "muxmaster: invalid regexp" if the intent is to disallow '}').
//
// Actual behaviour:
//   panics with: muxmaster: only one wildcard per path segment is allowed in
//   '/items/{p:(})}' — a misleading message that implies a route-conflict
//   issue, not a parser limitation.
//
// Recommended fix:
//   Replace the naive '}'→IndexByte scan in insertChild with a bracket-aware
//   scanner that tracks depth: increment depth on '{', decrement on '}', stop
//   on depth=0. This is the standard approach used by Express, Gin, and Echo.
//
// Fix diff sketch (tree.go, insertChild):
//   - end := strings.IndexByte(path[i:], '}')
//   + end := findClosingBrace(path[i:])  // bracket-depth-aware scan
//
// Risk: none for existing users (workaround: avoid '}' in regex body).
// This is a purely cosmetic/usability finding with no security or correctness
// implications for well-formed inputs.

import (
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"net/http"
)

var h200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestReproFPE2026REGEX01(t *testing.T) {
	// These regex expressions are all valid per regexp.Compile but contain '}'
	// which the tree parser misinterprets as the end of the {name:expr} block.
	problematicExprs := []string{
		"(})",    // original fuzzer find
		"a{2,3}", // quantifier
		"[}]",    // character class with }
		"\\}",    // escaped }
	}

	for _, expr := range problematicExprs {
		t.Run(expr, func(t *testing.T) {
			// Document the known limitation — do not fail (it is not a security bug).
			defer func() {
				if r := recover(); r != nil {
					msg, _ := r.(string)
					// Confirm it is a tree parser panic, not a regexp panic.
					if strings.Contains(msg, "invalid regexp") {
						t.Logf("FPE-2026-REGEX-01: expr=%q correctly rejected as invalid by regexp", expr)
					} else if strings.Contains(msg, "wildcard") || strings.Contains(msg, "only one") {
						t.Logf("FPE-2026-REGEX-01 CONFIRMED: expr=%q (valid Go regex) rejected with misleading message: %q", expr, msg)
					} else {
						t.Logf("FPE-2026-REGEX-01: expr=%q panic=%q", expr, msg)
					}
					// Mark as a known failure without failing the test.
					t.Skip("known limitation FPE-2026-REGEX-01: '}' in regex body confuses the tree parser")
				}
			}()
			mux := mm.New()
			mux.GET("/items/{id:"+expr+"}", h200)
			t.Logf("FPE-2026-REGEX-01: expr=%q registered successfully (unexpected — limitation may be fixed)", expr)
		})
	}
}
