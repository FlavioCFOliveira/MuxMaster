package harness

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// panicTrigger performs one registration that must panic with a documented
// message.
type panicTrigger struct {
	name     string
	classify func(string) bool
	run      func()
}

// recoverMessage runs fn and returns the string it panicked with. It fails
// the test when fn does not panic or panics with a non-string value.
func recoverMessage(t *testing.T, name string, fn func()) (msg string) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				s, ok := r.(string)
				if !ok {
					t.Fatalf("%s: panicked with non-string value %T: %v", name, r, r)
				}
				msg = s
			}
		}()
		fn()
	}()
	if !panicked {
		t.Fatalf("%s: registration did not panic", name)
	}
	return msg
}

var fastOK = mm.FastHandler(func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
	w.WriteHeader(http.StatusOK)
})

func passThrough(next http.Handler) http.Handler { return next }

// registrationPanicTriggers reaches every entry of the catalogue in
// registration_panics_test.go through the public API.
func registrationPanicTriggers() []panicTrigger {
	nineOptional := ""
	for i := range 9 {
		nineOptional += fmt.Sprintf("/s%d{/:p%d}", i, i)
	}
	longName := strings.Repeat("a", 255)

	handle := func(name, method, pattern string, h http.Handler) panicTrigger {
		return panicTrigger{name, isExpectedHandlePanic, func() { mm.New().Handle(method, pattern, h) }}
	}
	twice := func(name, first, second string) panicTrigger {
		return panicTrigger{name, isExpectedHandlePanic, func() {
			mux := mm.New()
			mux.GET(first, h200)
			mux.GET(second, h200)
		}}
	}
	mount := func(name, prefix string, h http.Handler) panicTrigger {
		return panicTrigger{name, isExpectedMountPanic, func() { mm.New().Mount(prefix, h) }}
	}

	return []panicTrigger{
		// methodPanics
		handle("empty method", "", "/", h200),
		handle("pattern without leading slash", http.MethodGet, "x", h200),
		handle("nil handler", http.MethodGet, "/", nil),
		handle("unsupported method", "get", "/", h200),
		{"Mux.HandleFast after Mux.Use", isExpectedHandlePanic, func() {
			mux := mm.New()
			mux.Use(passThrough)
			mux.HandleFast(http.MethodGet, "/", fastOK)
		}},
		{"Group.HandleFast after Group.Use", isExpectedHandlePanic, func() {
			g := mm.New().Group("/g")
			g.Use(passThrough)
			g.HandleFast(http.MethodGet, "/", fastOK)
		}},
		// treePanics
		handle("more than 8 optional segments", http.MethodGet, nineOptional, h200),
		handle("unclosed optional segment", http.MethodGet, "/a{/:b", h200),
		handle("consecutive optional segments", http.MethodGet, "/a{/:b}{/:c}", h200),
		handle("invalid UTF-8 pattern", http.MethodGet, "/\xff", h200),
		twice("conflicting wildcards", "/x/:a", "/x/:b"),
		twice("duplicate route", "/a", "/a"),
		handle("unclosed regex parameter brace", http.MethodGet, "/{", h200),
		handle("two wildcards in one segment", http.MethodGet, "/:a:b", h200),
		handle("unnamed wildcard", http.MethodGet, "/:", h200),
		handle("regex parameter without colon", http.MethodGet, "/{abc}", h200),
		handle("invalid regexp", http.MethodGet, "/{a:[}", h200),
		handle("regex parameter name over 254 bytes", http.MethodGet, "/{"+longName+":x}", h200),
		handle("catch-all not at the end", http.MethodGet, "/*a/b", h200),
		twice("catch-all under a registered segment root", "/src/", "/src/*filepath"),
		handle("catch-all without a leading slash", http.MethodGet, "/a*b", h200),
		// mountPanics
		mount("Mount nil handler", "/a", nil),
		mount("Mount prefix without leading slash", "a", h200),
		mount("Mount prefix with invalid UTF-8", "/\xff", h200),
		mount("Mount prefix ending in an optional parameter", "/a{/:b}", h200),
		// Tree panics reached through Mount (the FuzzMount crashers).
		mount("Mount prefix with unclosed regex brace", "/{", h200),
		mount("Mount prefix with unclosed optional segment", "/{/:", h200),
	}
}

// TestRegistrationPanicCatalogue pins the catalogue to the product code:
// every trigger must panic with a message its classifier accepts, and every
// catalogue entry must be reached by at least one trigger. A reworded panic
// message in tree.go, mux.go or group.go therefore fails here, with the
// offending message, instead of surfacing as a nightly fuzz crash.
func TestRegistrationPanicCatalogue(t *testing.T) {
	all := [][]registrationPanic{methodPanics, treePanics, mountPanics}
	covered := make(map[*registrationPanic]bool)

	for _, tr := range registrationPanicTriggers() {
		msg := recoverMessage(t, tr.name, tr.run)
		if !tr.classify(msg) {
			t.Errorf("%s: documented panic classified as unexpected: %q", tr.name, msg)
			continue
		}
		for ci := range all {
			for ei := range all[ci] {
				if all[ci][ei].re.MatchString(msg) {
					covered[&all[ci][ei]] = true
				}
			}
		}
	}

	for ci := range all {
		for ei := range all[ci] {
			e := &all[ci][ei]
			if !covered[e] {
				t.Errorf("catalogue entry %q (%s) is not reached by any trigger", e.re, e.site)
			}
		}
	}
}

// TestRegistrationPanicCatalogueRejectsOthers checks that the classifiers
// stay strict: internal invariant panics, runtime errors, partial matches
// and messages that belong to the other entry point are all unexpected.
func TestRegistrationPanicCatalogueRejectsOthers(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		handle bool // expected result of isExpectedHandlePanic
		mount  bool // expected result of isExpectedMountPanic
	}{
		{"internal tree invariant", "muxmaster: invalid node type", false, false},
		{"runtime error text", "runtime error: index out of range [3] with length 3", false, false},
		{"empty message", "", false, false},
		{"bare package prefix", "muxmaster:", false, false},
		{"documented message with trailing text", "muxmaster: HTTP method must not be empty (extra)", false, false},
		{"documented message with leading text", "wrapped: muxmaster: handler must not be nil", false, false},
		{"Mount-only message on Handle", "muxmaster: nil handler passed to Mount", false, true},
		{"Handle-only message on Mount", "muxmaster: HTTP method must not be empty", true, false},
		{"tree message on both", "muxmaster: regex param '{' in path '/{/*mux_mount' is missing its closing '}'", true, true},
		{"optional brace message on both", "muxmaster: unclosed { in path '/{/:/*mux_mount'", true, true},
	}
	for _, c := range cases {
		if got := isExpectedHandlePanic(c.msg); got != c.handle {
			t.Errorf("%s: isExpectedHandlePanic(%q) = %v, want %v", c.name, c.msg, got, c.handle)
		}
		if got := isExpectedMountPanic(c.msg); got != c.mount {
			t.Errorf("%s: isExpectedMountPanic(%q) = %v, want %v", c.name, c.msg, got, c.mount)
		}
	}
}
