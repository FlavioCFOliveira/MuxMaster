package muxmaster

import (
	"net/http"
	"strings"
	"testing"
)

// TestUnclosedRegexParamPanicsWithoutCorruptingTree is the regression test
// for FPE-O14-002 (rmp #274, O-14 fuzz/property scope), found by
// reports/fuzzing-and-property-engineer/harness's FuzzWalkRoutes.
//
// findWildcard's '{' case returned (start=-1, valid=false) when a path
// segment contained an unclosed '{' (no matching '}' before the next '/'
// or end of path — e.g. the malformed pattern "/{" or "/{a"). addRoute's
// walk() loop treats start<0 as "this segment has no wildcard marker at
// all" and, when the current node n has no existing wildChild and no
// matching static index for the byte, calls n.insertChild(path, ...)
// directly on n. insertChild's own top-of-loop check (`if i < 0 { break }`)
// then also took the "no wildcard" exit and fell through to its final,
// unconditional:
//
//	n.path = path
//	n.handler = handler
//	n.fast = fast
//	n.pattern = fullPath
//
// That fallthrough is only correct when n is a freshly allocated node with
// no prior state (the normal caller: a brand-new tree, or a `next` node
// insertChild itself just created). Here n was the SAME node that already
// held a previously-registered, unrelated route — so registering a pattern
// with a stray, unclosed '{' silently discarded that route's handler and
// replaced it with the new one, with NO panic and NO conflict signal.
//
// Concretely: on a Mux with only "/" registered, registering "/{"
// (obviously malformed — no name, no ':', no closing '}') did not panic,
// and afterwards Lookup("/") returned found=false: the "/" route had been
// silently replaced by a literal static route "/{".
//
// Fix (tree.go findWildcard, '{' case): return the wildcard's start
// position (not -1) when no closing '}' is found, keeping valid=false.
// This routes the malformed pattern into insertChild's existing
// `if !valid { panic(...) }` branch — the same branch already used for
// "more than one wildcard marker in a path segment" — instead of the
// silent-overwrite fallthrough. insertChild's panic message is
// disambiguated for the unclosed-brace case specifically.
func TestUnclosedRegexParamPanicsWithoutCorruptingTree(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	cases := []struct {
		name string
		base string
		bad  string // malformed pattern, registered after base
	}{
		{"root_unclosed_brace_bare", "/", "/{"},
		{"root_unclosed_brace_named", "/", "/{a"},
		{"static_sibling_unclosed_brace", "/admin", "/{oops"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			m.Handle(http.MethodGet, tc.base, h)

			if hh, _, ok := m.Lookup(http.MethodGet, tc.base); !ok || hh == nil {
				t.Fatalf("setup: Lookup(%q) failed immediately after registration", tc.base)
			}

			panicMsg := ""
			func() {
				defer func() {
					if r := recover(); r != nil {
						if s, ok := r.(string); ok {
							panicMsg = s
						} else {
							t.Fatalf("panic value is not a string: %v (type %T)", r, r)
						}
					}
				}()
				m.Handle(http.MethodGet, tc.bad, h)
			}()

			if panicMsg == "" {
				t.Fatalf("registering malformed pattern %q did not panic — "+
					"it must be rejected loudly, not silently accepted", tc.bad)
			}
			if !strings.Contains(panicMsg, "missing its closing") {
				t.Fatalf("panic message does not identify the unclosed-brace cause: %q", panicMsg)
			}

			// The critical assertion: the panic must not have corrupted the
			// tree. The base route, registered before the malformed one,
			// must still be reachable exactly as before.
			hh, _, ok := m.Lookup(http.MethodGet, tc.base)
			if !ok || hh == nil {
				t.Fatalf("FPE-O14-002 REGRESSED: Lookup(%q) failed after malformed-pattern panic — "+
					"the existing route was silently corrupted/replaced", tc.base)
			}

			seen := map[string]int{}
			if err := m.Walk(func(method, pattern string, handler http.Handler) error {
				seen[method+" "+pattern]++
				return nil
			}); err != nil {
				t.Fatalf("Walk returned unexpected error: %v", err)
			}
			if seen["GET "+tc.base] != 1 {
				t.Fatalf("Walk does not show exactly one GET %q after the panic: seen=%v", tc.base, seen)
			}
			if seen["GET "+tc.bad] != 0 {
				t.Fatalf("Walk shows the malformed pattern %q as registered despite the panic: seen=%v", tc.bad, seen)
			}
		})
	}
}
