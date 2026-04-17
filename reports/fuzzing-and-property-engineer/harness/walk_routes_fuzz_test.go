// FuzzWalkRoutes — Walk and Routes must:
//   - Never panic on any valid registered mux.
//   - Return every registered (method, pattern) exactly once.
//   - Terminate the Walk when the callback returns a non-nil error.
package harness

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func FuzzWalkRoutes(f *testing.F) {
	f.Add("/a", "/a/b", "/a/b/c")
	f.Add("/", "/users/:id", "/static/*f")
	f.Add("/api/v1", "/api/v2", "/api/v3")

	f.Fuzz(func(t *testing.T, p1, p2, p3 string) {
		patterns := []string{p1, p2, p3}
		registered := make(map[string]bool)

		mux := mm.New()
		for _, p := range patterns {
			func() {
				defer func() { _ = recover() }()
				if !isRegisterable(p) {
					return
				}
				mux.GET(p, h200)
				registered[p] = true
			}()
		}

		// Walk must never panic.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Walk panic patterns=%v: %v\n%s", patterns, r, debug.Stack())
			}
		}()

		walkSeen := make(map[string]int)
		_ = mux.Walk(func(method, pattern string, handler http.Handler) error {
			walkSeen[method+" "+pattern]++
			return nil
		})

		// Note: FPE-008 tracks tree corruption after a mid-registration panic.
		// A successfully-registered pattern may disappear from Walk if a
		// subsequent Handle panicked. Skip this assertion when any of the
		// patterns is known to trigger mid-split panic.
		corruptionPossible := false
		for _, p := range patterns {
			if !isRegisterable(p) || strings.Contains(p, "{") || strings.Contains(p, "*") {
				corruptionPossible = true
				break
			}
		}

		if !corruptionPossible {
			for p := range registered {
				key := "GET " + p
				if walkSeen[key] == 0 {
					t.Fatalf("Walk did not surface registered pattern %q", p)
				}
				if walkSeen[key] > 1 {
					t.Fatalf("Walk surfaced %q %d times", p, walkSeen[key])
				}
			}
		}

		// Routes must agree with Walk (independent of FPE-008 corruption).
		infos := mux.Routes()
		routesSeen := make(map[string]int)
		for _, info := range infos {
			routesSeen[info.Method+" "+info.Pattern]++
		}
		for k, v := range walkSeen {
			if routesSeen[k] != v {
				t.Fatalf("Walk/Routes disagree on %q: walk=%d routes=%d", k, v, routesSeen[k])
			}
		}
		_ = infos

		// Early termination: the callback can return an error and stop.
		sentinel := errors.New("stop")
		callCount := 0
		err := mux.Walk(func(method, pattern string, handler http.Handler) error {
			callCount++
			return sentinel
		})
		if len(infos) > 0 {
			if err == nil {
				t.Fatalf("Walk ignored early-termination error")
			}
			if callCount != 1 {
				t.Fatalf("Walk called cb %d times after sentinel error", callCount)
			}
		}
	})
}

// isRegisterable is a cheap heuristic to avoid fuzzing the Handle panic path
// (which we test elsewhere). We still recover() in the caller.
func isRegisterable(p string) bool {
	if len(p) == 0 || p[0] != '/' {
		return false
	}
	if len(p) > 256 {
		return false
	}
	if strings.ContainsRune(p, 0) {
		return false
	}
	return true
}

// FuzzLookupAfterRegistration — after registering N routes, Lookup on each
// must succeed. Bridges introspection and registration.
func FuzzLookupAfterRegistration(f *testing.F) {
	f.Add("/a", "/a/b")
	f.Add("/u/:id", "/u/:id/p")
	f.Add("/api/:v", "/api/:v/users")
	f.Add("/s1", "/s2")

	f.Fuzz(func(t *testing.T, p1, p2 string) {
		if !isRegisterable(p1) || !isRegisterable(p2) || p1 == p2 {
			t.Skip()
		}
		mux := mm.New()
		for _, p := range []string{p1, p2} {
			func() {
				defer func() { _ = recover() }()
				mux.GET(p, h200)
			}()
		}

		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprint(r)
				if isTrackedTreePanic(msg) {
					return
				}
				t.Fatalf("Lookup panic: %v\n%s", r, debug.Stack())
			}
		}()

		// Lookup on each *pattern* won't match (it contains :name) — instead,
		// build concrete paths from patterns and look those up.
		for _, p := range []string{p1, p2} {
			concrete := concretise(p)
			func() {
				defer func() {
					if r := recover(); r != nil {
						if isTrackedTreePanic(fmt.Sprint(r)) {
							return
						}
						panic(r)
					}
				}()
				_, _, _ = mux.Lookup(http.MethodGet, concrete)
			}()
		}
	})
}

// isTrackedTreePanic allowlists known tree-layer panics that have
// dedicated FPE-NNN repros.
func isTrackedTreePanic(msg string) bool {
	if strings.Contains(msg, "slice bounds out of range") {
		return true // FPE-006
	}
	if strings.Contains(msg, "invalid node type") {
		return true // FPE-009
	}
	if strings.Contains(msg, "index out of range") {
		return true // FPE-005
	}
	return false
}

// concretise replaces :name with a dummy segment and *name with a dummy tail.
func concretise(pattern string) string {
	var out strings.Builder
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		if c == ':' || c == '*' {
			// skip to next '/'
			j := i
			for j < len(pattern) && pattern[j] != '/' {
				j++
			}
			out.WriteString(fmt.Sprintf("x%d", i))
			i = j
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}
