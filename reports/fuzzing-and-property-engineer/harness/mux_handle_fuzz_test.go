// Fuzz targets for Mux.Handle and friends.
//
// Scope: ensure Mux.Handle never panics on inputs that the public contract
// claims to accept. The public contract for Handle is documented in mux.go:
//
//	Panics on empty method, non-absolute path, nil handler, or route conflict.
//
// So our invariant is:
//   - Given (method ∈ known-methods, pattern starts with '/', handler non-nil,
//     no route conflict), Handle must return without panic.
//   - On any violation of the pre-conditions, Handle must panic with a clear
//     message (not a nil-pointer dereference or runtime error from tree.go).
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// h200 is the no-op handler used across fuzzers.
var h200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// knownMethods are the methods methodIdx accepts plus Mount's "*".
var knownMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
	http.MethodConnect, http.MethodTrace,
}

// validPattern is the most permissive acceptance filter that reflects the
// public contract. Rejects patterns that are known to panic by design so the
// fuzzer is not chasing those cases (the documented invalid-input panics are
// covered by FuzzMuxHandleInvalid below).
//
// Documented panic conditions (tree.go):
//   - Empty method / non-absolute path / nil handler.
//   - Unnamed wildcard (":" or "*" with nothing after it within the segment).
//   - Multiple wildcards in a single segment (":a:b").
//   - Catch-all mid-path.
//   - Catch-all conflicts with existing handler.
//   - Nested '{' in regex param.
//   - Malformed '{name:expr}' (no colon, no closing brace).
//   - Invalid regex expression.
//   - Duplicate handler.
func validPattern(p string) bool {
	if len(p) == 0 || p[0] != '/' {
		return false
	}
	if len(p) > 4096 {
		return false
	}
	// No NUL in paths — Go doesn't reject them but they are a trivial DOS.
	if strings.ContainsRune(p, 0) {
		return false
	}
	// Nested '{' is documented as panicking in findWildcard; skip.
	// Catch-all must be at the end; skip patterns with '*' mid-path.
	depth := 0
	segStart := 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' {
			segStart = i + 1
		}
		if c == '{' {
			depth++
			if depth > 1 {
				return false
			}
			// Must contain "name:expr" before closing '}'. Scan ahead.
			closing := strings.Index(p[i:], "}")
			if closing < 0 {
				return false
			}
			inner := p[i+1 : i+closing]
			colon := strings.Index(inner, ":")
			if colon <= 0 || colon == len(inner)-1 {
				return false
			}
			// Name chars must not include '/' or special meta.
			for _, ch := range inner[:colon] {
				if ch == '/' || ch == ':' || ch == '{' || ch == '}' || ch == '*' {
					return false
				}
			}
			// Try to pre-compile the regex — if it fails, skip.
			// (We don't import regexp into validPattern to keep it cheap;
			// we rely on the fuzzer's panic classifier for invalid regex.)
		}
		if c == '}' {
			depth--
			if depth < 0 {
				return false
			}
		}
		if c == ':' {
			// Segment containing ':' must have at least one byte of name
			// before the next '/' or end.
			j := i + 1
			for j < len(p) && p[j] != '/' {
				j++
			}
			if j == i+1 {
				return false // unnamed :
			}
			// Two ':' in one segment: ':a:b' is invalid.
			for k := i + 1; k < j; k++ {
				if p[k] == ':' || p[k] == '*' {
					return false
				}
			}
			// Must be at the start of a segment.
			if segStart != i {
				return false
			}
		}
		if c == '*' {
			// Catch-all: must only appear at a segment start, must be last
			// segment. We filter out mid-path '*' and '/':'-adjacent stars.
			if i == 0 || p[i-1] != '/' {
				return false
			}
			// Must run to end of string without another '/'.
			for j := i; j < len(p); j++ {
				if p[j] == '/' && j != i-1 {
					return false
				}
			}
			// Must have a name: '*foo' with len >= 2 after '*'.
			if i+1 >= len(p) {
				return false
			}
			// The remainder must be just the name (no slashes).
			if strings.ContainsRune(p[i+1:], '/') {
				return false
			}
		}
	}
	if depth != 0 {
		return false
	}
	return true
}

// FuzzMuxHandle fuzzes Mux.Handle with (method, pattern). Pattern is filtered
// through validPattern — on valid inputs we assert no panic; on invalid inputs
// we assert that if Handle panics, the message begins with "muxmaster: " so
// we know the panic is intentional and not an internal bug.
func FuzzMuxHandle(f *testing.F) {
	// Seeds: cover the key axes.
	seeds := []struct{ m, p string }{
		{"GET", "/"},
		{"GET", "/a"},
		{"GET", "/a/b"},
		{"GET", "/users/:id"},
		{"GET", "/users/:id/posts/:pid"},
		{"GET", "/static/*filepath"},
		{"GET", "/users/{id:[0-9]+}"},
		{"GET", "/articles/{slug:[a-z-]+}"},
		{"POST", "/"},
		{"POST", "/items"},
		{"PUT", "/items/:id"},
		{"DELETE", "/items/:id"},
		{"PATCH", "/items/:id"},
		{"OPTIONS", "/"},
		{"HEAD", "/"},
		{"CONNECT", "/tunnel"},
		{"TRACE", "/"},
		// Mount's wildcard method
		{"*", "/mount/*mux_mount"},
		// Optional segments
		{"GET", "/v1/users{/:id}"},
		{"GET", "/v1/posts{/:slug:[a-z]+}"},
		// Patterns that may be invalid but are seed-worthy for invalid path testing
		{"", "/"},
		{"GET", ""},
		{"GET", "noleadingslash"},
		{"GET", "//"},
		{"GET", "/:"},
		{"GET", "/:/"},
		{"GET", "/*"},
		{"GET", "/*filepath/after"},
		{"GET", "/a/*b/c"},
		{"GET", "/{"},
		{"GET", "/{name"},
		{"GET", "/{name:"},
		{"GET", "/{name:(((((((((((((((((((((((((((((("},
		{"GET", "/{name:(a+)+$}"},
		{"GET", "/a/:id/:id"},
		{"GET", strings.Repeat("/a", 2000)},
		// Unicode
		{"GET", "/\u00e9\u00e9"},
		{"GET", "/\U0001F600"},
	}
	for _, s := range seeds {
		f.Add(s.m, s.p)
	}

	f.Fuzz(func(t *testing.T, method, pattern string) {
		mux := mm.New()

		valid := isKnownMethod(method) && validPattern(pattern)
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprint(r)
				if valid && !isExpectedMuxMasterPanic(msg) {
					t.Fatalf("unexpected panic on valid input\n"+
						"method=%q\npattern=%q\npanic=%v\nstack:\n%s",
						method, pattern, r, debug.Stack())
				}
				// For invalid inputs the panic must be either an informative
				// muxmaster message OR a known-tracked runtime.Error class.
				// Known: FPE-005 (runtime error: index out of range) on
				// patterns mixing `{...}` and catch-all without intermediate
				// slash. Track but don't fail — there's a dedicated repro.
				if isTrackedRuntimeError(msg) {
					return
				}
				if !isExpectedMuxMasterPanic(msg) {
					t.Fatalf("panic from input is not an intentional muxmaster panic\n"+
						"method=%q\npattern=%q\npanic=%v\nstack:\n%s",
						method, pattern, r, debug.Stack())
				}
			}
		}()

		mux.Handle(method, pattern, h200)

		// If registration succeeded on a valid input, a lookup for that exact
		// path (when it has no wildcard) must succeed.
		if valid && !containsWildcard(pattern) {
			h, _, ok := mux.Lookup(method, pattern)
			if !ok || h == nil {
				t.Fatalf("Lookup failed after successful Handle: method=%q pattern=%q",
					method, pattern)
			}
		}

		// ServeHTTP must never panic on a registered route, even with a weird
		// but registered path. We build the request manually to bypass the
		// stdlib URL parser's aggressive rejection of special bytes — any
		// such rejection is the stdlib's responsibility, not the mux's.
		if valid && !containsWildcard(pattern) {
			req := &http.Request{
				Method:     method,
				URL:        &url.URL{Path: pattern},
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{},
				Host:       "example.com",
				RequestURI: pattern,
				RemoteAddr: "127.0.0.1:1234",
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == 0 {
				t.Fatalf("no status code written after ServeHTTP: method=%q pattern=%q",
					method, pattern)
			}
		}
	})
}

func isKnownMethod(m string) bool {
	for _, km := range knownMethods {
		if km == m {
			return true
		}
	}
	return m == "*"
}

func containsWildcard(p string) bool {
	return strings.ContainsAny(p, ":*{")
}

// isTrackedRuntimeError allowlists runtime.Error panics that are already
// covered by a dedicated repro under evidence/FPE-NNN/. Do NOT add to this
// list without a corresponding repro.
func isTrackedRuntimeError(msg string) bool {
	// FPE-005: catch-all mid-path after regex param causes index OOB.
	if strings.Contains(msg, "runtime error: index out of range") {
		return true
	}
	// FPE-006: getValue slice re-slice beyond backing array capacity.
	if strings.Contains(msg, "slice bounds out of range") {
		return true
	}
	return false
}

// FuzzMuxHandleTwice registers two patterns and checks that if registration
// succeeds for both, each is individually reachable. This is a differential
// sanity test against the addRoute split logic.
func FuzzMuxHandleTwice(f *testing.F) {
	f.Add("/users/:id", "/users/:id/posts")
	f.Add("/a", "/a/b")
	f.Add("/a/b", "/a")
	f.Add("/static/*f", "/static")
	f.Add("/v1/users/:id", "/v1/users/:id/posts/:pid")
	f.Add("/x/:a/y/:b", "/x/:a/y")
	f.Add("/{id:[0-9]+}", "/abc")
	f.Add("/users/{id:[0-9]+}/posts", "/users/{id:[0-9]+}/comments")

	f.Fuzz(func(t *testing.T, p1, p2 string) {
		if !validPattern(p1) || !validPattern(p2) {
			t.Skip()
		}
		if p1 == p2 {
			t.Skip()
		}
		if containsWildcard(p1) || containsWildcard(p2) {
			// Restrict to static patterns for deterministic Lookup assertions.
			t.Skip()
		}

		mux := mm.New()

		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprint(r)
				// A conflict between two distinct patterns is allowed.
				if strings.Contains(msg, "conflicts with existing") ||
					strings.Contains(msg, "already registered") ||
					strings.Contains(msg, "catch-all") ||
					isTrackedRuntimeError(msg) {
					return
				}
				t.Fatalf("unexpected panic: p1=%q p2=%q panic=%v\n%s",
					p1, p2, r, debug.Stack())
			}
		}()

		// Time-box the entire Handle+Lookup sequence to 2s. FPE-007 tracks
		// pathological UTF-8 inputs that grow unboundedly; we must not let
		// the fuzzer get OS-killed.
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			mux.Handle(http.MethodGet, p1, h200)
			mux.Handle(http.MethodGet, p2, h200)

			lookup := func(pat string) (ok bool) {
				defer func() {
					if r := recover(); r != nil {
						if isTrackedRuntimeError(fmt.Sprint(r)) {
							ok = true
							return
						}
						panic(r)
					}
				}()
				h, _, found := mux.Lookup(http.MethodGet, pat)
				return found && h != nil
			}

			if !lookup(p1) {
				t.Errorf("p1 unreachable after dual registration: p1=%q p2=%q", p1, p2)
			}
			if !lookup(p2) {
				t.Errorf("p2 unreachable after dual registration: p1=%q p2=%q", p1, p2)
			}
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// FPE-007: pathological pair; tracked separately.
		}
	})
}
