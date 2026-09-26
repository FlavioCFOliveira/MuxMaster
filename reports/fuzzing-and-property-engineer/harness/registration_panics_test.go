package harness

import (
	"regexp"
)

// This file is the single catalogue of the registration-time panics that
// MuxMaster raises on purpose. Every fuzz target and property test that
// registers routes from fuzzer-controlled input classifies a recovered panic
// through isExpectedHandlePanic or isExpectedMountPanic, both defined here.
//
// Each entry matches ONE panic site exactly: the regular expression is
// anchored at both ends and reproduces the literal text of the panic call,
// with only the interpolated user input (pattern, method, prefix, regexp
// error) left open. A message that does not match an entry in full is an
// unexpected panic and must fail the fuzz target. In particular, the
// internal invariant panic "muxmaster: invalid node type" (tree.go) is
// deliberately absent: reaching it means the tree is corrupt.
//
// TestRegistrationPanicCatalogue (registration_panics_catalogue_test.go)
// triggers every entry through the public API, so a change to a panic
// message in the product code fails that test instead of silently turning
// a documented panic into a fuzz "crash" (or the reverse).

// registrationPanic is one documented registration-time panic.
type registrationPanic struct {
	// site is the product-code location that raises the panic.
	site string
	// source is the specification rule (or, where the specification has
	// no rule yet, the code comment and finding ID) that makes the panic
	// intended behaviour.
	source string
	// re matches the complete panic message.
	re *regexp.Regexp
}

// anyText matches arbitrary interpolated input, including newlines.
const anyText = `(?s:.*)`

// methodPanics are raised by Mux.Handle, Mux.HandleFast and Mux.Match (and
// the Group methods that delegate to them) before the tree is touched.
var methodPanics = []registrationPanic{
	{
		site:   "mux.go Handle/HandleFast",
		source: "specification/routing.md rules 37, 42, 62",
		re:     regexp.MustCompile(`^muxmaster: HTTP method must not be empty$`),
	},
	{
		site:   "mux.go Handle/HandleFast",
		source: "specification/routing.md rules 2, 41, 63",
		re:     regexp.MustCompile(`^muxmaster: path must begin with '/' in '` + anyText + `'$`),
	},
	{
		site:   "mux.go Handle/HandleFast",
		source: "specification/routing.md rules 40, 64",
		re:     regexp.MustCompile(`^muxmaster: handler must not be nil$`),
	},
	{
		site:   "mux.go Handle/HandleFast",
		source: "specification/routing.md rules 34, 38",
		re:     regexp.MustCompile(`^muxmaster: unsupported HTTP method '` + anyText + `'$`),
	},
	{
		site:   "mux.go HandleFast, group.go Group.HandleFast",
		source: "specification/middleware.md rules 38 and 43 (CSA-2026-0054)",
		re: regexp.MustCompile(`^muxmaster: HandleFast route registered on a (?:Mux|Group) with stdlib middleware \(Use\) — ` +
			`stdlib middleware does not run on the FastHandler path\. ` +
			`Use UseFast\(\) for fast routes, or Handle\(\) for stdlib-middleware-wrapped routes\.$`),
	},
}

// treePanics are raised by the radix tree (tree.go) while a pattern is
// expanded and inserted. They are reachable from every registration entry
// point, including Mount, which registers prefix + "/*mux_mount".
var treePanics = []registrationPanic{
	{
		site:   "tree.go addRouteInternal",
		source: "specification/routing.md rule 103",
		re: regexp.MustCompile(`^muxmaster: pattern '` + anyText + `' has [0-9]+ optional segments; ` +
			`the maximum is 8 to prevent exponential addRoute time complexity \(DoS\)\.$`),
	},
	{
		site:   "tree.go expandOptional",
		source: "specification/routing.md rule 110",
		re:     regexp.MustCompile(`^muxmaster: unclosed \{ in path '` + anyText + `'$`),
	},
	{
		site:   "tree.go expandOptional",
		source: "specification/routing.md rule 104",
		re: regexp.MustCompile(`^muxmaster: consecutive optional segments not supported in path '` + anyText +
			`' — separate optional segments with a literal segment, e\.g\. /users\{/:id\}/posts\{/:post\}$`),
	},
	{
		site:   "tree.go addRouteInternal",
		source: "specification/routing.md rule 111",
		re:     regexp.MustCompile(`^muxmaster: path contains invalid UTF-8: ` + anyText + `$`),
	},
	{
		site:   "tree.go addRouteInternal (two sites)",
		source: "specification/routing.md rules 51, 71, 79",
		re:     regexp.MustCompile(`^muxmaster: '` + anyText + `' in path '` + anyText + `' conflicts with existing wildcard '` + anyText + `'$`),
	},
	{
		site:   "tree.go addRouteInternal",
		source: "specification/routing.md rules 26, 32, 46, 65",
		re:     regexp.MustCompile(`^muxmaster: a handler is already registered for path '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rule 95 (FPE-O14-002)",
		re:     regexp.MustCompile(`^muxmaster: regex param '\{' in path '` + anyText + `' is missing its closing '\}'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rule 69",
		re:     regexp.MustCompile(`^muxmaster: only one wildcard per path segment is allowed in '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rules 27, 66",
		re:     regexp.MustCompile(`^muxmaster: wildcards must be named in path '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rule 112",
		re:     regexp.MustCompile(`^muxmaster: regex param must have the form \{name:expr\} in '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rules 21, 70",
		re:     regexp.MustCompile(`^muxmaster: invalid regexp in path '` + anyText + `': ` + anyText + `$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rule 106",
		re:     regexp.MustCompile(`^muxmaster: regex param name must be at most 254 bytes in '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rules 14, 67",
		re:     regexp.MustCompile(`^muxmaster: catch-all routes are only allowed at the end of the path in '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rules 68, 79",
		re:     regexp.MustCompile(`^muxmaster: catch-all conflicts with existing handler for the path root in '` + anyText + `'$`),
	},
	{
		site:   "tree.go insertChild",
		source: "specification/routing.md rule 113",
		re:     regexp.MustCompile(`^muxmaster: catch-all requires a '/' prefix in path '` + anyText + `'$`),
	},
}

// mountPanics are raised by Mux.Mount and Group.Mount (mux.go mountAt)
// before the internal catch-all route is registered.
var mountPanics = []registrationPanic{
	{
		site:   "mux.go mountAt",
		source: "specification/groups.md rule 29",
		re:     regexp.MustCompile(`^muxmaster: nil handler passed to Mount$`),
	},
	{
		site:   "mux.go mountAt",
		source: "specification/groups.md rule 30",
		re:     regexp.MustCompile(`^muxmaster: Mount prefix must begin with '/'$`),
	},
	{
		site:   "mux.go mountAt",
		source: "specification/groups.md rule 45 (FPE-2026-002)",
		re:     regexp.MustCompile(`^muxmaster: Mount prefix contains invalid UTF-8$`),
	},
	{
		site:   "mux.go mountAt",
		source: "specification/groups.md rules 36-37",
		re: regexp.MustCompile(`^muxmaster: Mount prefix '` + anyText + `' ends with an optional parameter; ` +
			`Mount does not support an optional parameter as the last element of its prefix$`),
	},
}

// matchesAny reports whether msg matches one entry of any catalogue in full.
func matchesAny(msg string, catalogues ...[]registrationPanic) bool {
	for _, c := range catalogues {
		for _, p := range c {
			if p.re.MatchString(msg) {
				return true
			}
		}
	}
	return false
}

// isExpectedHandlePanic reports whether msg is a documented panic of
// Mux.Handle, Mux.HandleFast, Mux.Match or the equivalent Group methods.
func isExpectedHandlePanic(msg string) bool {
	return matchesAny(msg, methodPanics, treePanics)
}

// isExpectedMountPanic reports whether msg is a documented panic of
// Mux.Mount or Group.Mount. Mount validates its own preconditions and then
// registers prefix + "/*mux_mount" under the internal "*" method, so the
// tree panics apply but the method and handler preconditions of Handle
// cannot be reached.
func isExpectedMountPanic(msg string) bool {
	return matchesAny(msg, mountPanics, treePanics)
}
