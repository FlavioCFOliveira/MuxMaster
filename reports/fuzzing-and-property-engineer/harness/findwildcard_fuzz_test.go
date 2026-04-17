// FuzzFindWildcard / FuzzRegexCompile — stress the wildcard parser and the
// regex compile path inside addRoute.
//
// findWildcard is an internal routine in tree.go. We exercise it indirectly
// via Mux.Handle since the function is unexported. The invariant is simply
// "any pattern either registers or panics with muxmaster: prefix — never a
// runtime error".
//
// FuzzRegexCompile specifically targets the `{name:expr}` path and tries to
// surface ReDoS-prone patterns that could hang the registration phase.
package harness

import (
	"fmt"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// FuzzFindWildcardViaHandle exercises the findWildcard + insertChild paths
// through public Mux.Handle. It complements FuzzMuxHandle with an emphasis on
// the wildcard syntax.
func FuzzFindWildcardViaHandle(f *testing.F) {
	seeds := []string{
		"/:a",
		"/:a/",
		"/:a/:b",
		"/:a/:b/:c",
		"/*f",
		"/a/*f",
		"/{x:[0-9]+}",
		"/{x:.*}",
		"/{x:a+}",
		"/{x:(a|b)+}",
		"/a/{x:[a-z]+}/b",
		"/a/{x:[0-9]+}/c/{y:[a-z]+}",
		// Adversarial regex
		"/{x:(a+)+$}",
		"/{x:(a|a?)+}",
		"/{x:(.*a){1,100}}",
		// Malformed wildcards
		"/:",
		"/::",
		"/*:a",
		"/:a:b",
		"/{",
		"/{x",
		"/{x:",
		"/{x:}",
		"/{x:[}",
		"/{}",
		"/{:expr}",
		"/{a:b}c{d:e}",
		// Nested braces
		"/{a:[{}]}",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		if !strings.HasPrefix(pattern, "/") || len(pattern) > 1024 || strings.ContainsRune(pattern, 0) {
			t.Skip()
		}
		mux := mm.New()
		done := make(chan struct{}, 1)
		var panicVal any

		go func() {
			defer func() {
				if r := recover(); r != nil {
					panicVal = r
				}
				done <- struct{}{}
			}()
			mux.Handle(http.MethodGet, pattern, h200)
		}()

		// Allow up to 3s for registration. If it hangs, the regex engine is
		// likely wedged — that is a resource-exhaustion bug.
		select {
		case <-done:
			if panicVal != nil {
				msg := fmt.Sprint(panicVal)
				if isTrackedRuntimeError(msg) {
					return // FPE-005 — tracked elsewhere
				}
				if !isExpectedMuxMasterPanic(msg) {
					t.Fatalf("unexpected panic on pattern %q: %v\n%s",
						pattern, panicVal, debug.Stack())
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("Handle(%q) hung for 3s — possible ReDoS or infinite loop", pattern)
		}
	})
}

// FuzzRegexCompile specifically targets regexp.Compile timing via the router.
// We register a pattern with a user-controlled regex and time the registration.
func FuzzRegexCompile(f *testing.F) {
	f.Add("[0-9]+")
	f.Add("a+")
	f.Add("(a|b)+")
	f.Add("(a+)+$")       // classical ReDoS
	f.Add("(.*a){1,100}") // polynomial
	f.Add("[a-z]{0,100000}")
	f.Add("(?:[a-z]|[A-Z])*")
	f.Add("a{0,1000000}")
	f.Add("(a|a?)+")
	f.Add(strings.Repeat("(", 500) + "a" + strings.Repeat(")?", 500))

	f.Fuzz(func(t *testing.T, expr string) {
		if len(expr) > 4096 || strings.ContainsRune(expr, 0) {
			t.Skip()
		}
		if strings.ContainsRune(expr, '/') || strings.ContainsRune(expr, '}') {
			t.Skip() // would break the '{name:expr}' token boundary
		}
		pattern := "/x/{id:" + expr + "}"

		mux := mm.New()
		done := make(chan any, 1)

		go func() {
			defer func() {
				r := recover()
				done <- r
			}()
			mux.Handle(http.MethodGet, pattern, h200)
		}()

		select {
		case r := <-done:
			if r != nil {
				msg := fmt.Sprint(r)
				if isTrackedRuntimeError(msg) {
					return
				}
				if !isExpectedMuxMasterPanic(msg) {
					t.Fatalf("unexpected panic on expr %q: %v", expr, r)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("regex compile hung for 2s on expr %q — ReDoS at registration time",
				expr)
		}
	})
}

// FuzzRegexMatchReDoS: even if the regex compiles quickly, matching it against
// a user-controlled input might be exponential. We register a route with a
// fuzzer-selected regex AND drive a request with a fuzzer-selected path.
//
// Go's regexp package uses RE2 (Thompson NFA), which is immune to classical
// catastrophic backtracking — but malformed or extremely long inputs can still
// exhibit pathological behaviour. This test pins that empirically.
func FuzzRegexMatchReDoS(f *testing.F) {
	f.Add("[a-z]+", "aaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	f.Add("(a|b)+", strings.Repeat("a", 100)+"!")
	f.Add(".*", strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, expr, input string) {
		if len(expr) > 512 || len(input) > 4096 {
			t.Skip()
		}
		if strings.ContainsRune(expr, '/') || strings.ContainsRune(expr, '}') {
			t.Skip()
		}
		if strings.ContainsRune(input, '/') || strings.ContainsRune(input, 0) {
			t.Skip()
		}
		// Sanity-compile the regex; skip if invalid.
		if _, err := regexp.Compile("^(?:" + expr + ")$"); err != nil {
			t.Skip()
		}
		pattern := "/x/{id:" + expr + "}"
		path := "/x/" + input

		mux := mm.New()
		func() {
			defer func() { _ = recover() }()
			mux.Handle(http.MethodGet, pattern, h200)
		}()

		done := make(chan struct{}, 1)
		start := time.Now()
		go func() {
			defer func() {
				_ = recover()
				done <- struct{}{}
			}()
			_, _, _ = mux.Lookup(http.MethodGet, path)
		}()
		select {
		case <-done:
			elapsed := time.Since(start)
			if elapsed > 500*time.Millisecond {
				t.Fatalf("Lookup took %v — pathological regex match on expr=%q input.len=%d",
					elapsed, expr, len(input))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Lookup hung for 2s — pathological match expr=%q input.len=%d",
				expr, len(input))
		}
	})
}

func isExpectedMuxMasterPanic(msg string) bool {
	expectedSubstrings := []string{
		"muxmaster:",
		"conflicts with existing",
		"already registered",
		"catch-all",
		"wildcard",
		"only one wildcard per path segment",
		"wildcards must be named",
		"no '/' before catch-all",
		"invalid regexp",
		"regex param must have the form",
		"unclosed {",
		"unsupported HTTP method",
		"HTTP method must not be empty",
		"path must begin with",
		"handler must not be nil",
	}
	for _, s := range expectedSubstrings {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
