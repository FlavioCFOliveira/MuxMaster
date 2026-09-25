package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file is the differential fuzz/test rmp #259 requires: MuxMaster's
// actual lookup result is compared, over random route sets and random
// request paths, against a small, independent reference matcher that
// implements specification/routing.md rules 48-51's precedence directly —
// static > param > catch-all, with fallback when a higher-priority segment
// type structurally matches but the rest of the pattern does not (the exact
// DIV-001 shape rmp #259 fixes).
//
// Scope: static, named-parameter (":name"), and catch-all ("*name")
// segments only. Regex parameters ("{name:expr}") are deliberately excluded
// — rule 51's regex-vs-plain-param fallback requires the tree to hold BOTH
// a regex and a plain param at the same position, which conflicts with
// rule 71 ("a wildcard conflicts with an already-registered wildcard at the
// same position") as currently implemented and tested
// (TestDuplicateWildcardConflict_PanicsInBothOrders in
// tree_static_sibling_wildchild_test.go treats a differently-named/valued
// wildcard at the same position as a hard, order-independent conflict, with
// no carve-out for regex-vs-plain). Reconciling that contradiction is a
// separate, reported decision (see the task's final report) — modelling
// regex fallback here would require guessing the resolution instead of
// testing the specification as it is actually enforced today.

// refSegKind classifies one path segment of a reference-model route.
// Lower numeric value = higher matching priority (rule 48).
type refSegKind int

const (
	refStatic refSegKind = iota
	refParam
	refCatchAll
)

type refSeg struct {
	kind refSegKind
	lit  string // literal text, only meaningful for refStatic
}

type refRoute struct {
	pattern string
	segs    []refSeg
}

// parseRefRoute splits a "/"-rooted pattern into typed segments using the
// same token syntax as MuxMaster's own patterns (specification/routing.md
// §1): ":name" is a named parameter, "*name" is a catch-all (always last),
// anything else is a static literal segment.
func parseRefRoute(pattern string) refRoute {
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	segs := make([]refSeg, 0, len(parts))
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, ":"):
			segs = append(segs, refSeg{kind: refParam})
		case strings.HasPrefix(p, "*"):
			segs = append(segs, refSeg{kind: refCatchAll})
		default:
			segs = append(segs, refSeg{kind: refStatic, lit: p})
		}
	}
	return refRoute{pattern: pattern, segs: segs}
}

// refMatches reports whether route structurally matches reqPath (rules
// 8-17): a named parameter matches exactly one non-empty segment; a
// catch-all requires a literal '/' immediately before it in the pattern
// (rule 16) AND in the actual request — i.e. the remaining path after the
// prior segments must be non-empty and start with '/' — and then matches
// everything from that '/' onward, including a bare "/" (empty tail); a
// static segment must match its literal exactly.
//
// Operates on the raw remaining path string rather than a pre-split
// []string so the "must have a literal / present" requirement is exact:
// splitting "/a" and "/a/" by "/" both naively yield a one-element and a
// two-element (with a trailing empty string) slice respectively, but the
// difference between "ends exactly at a static segment" (a TSR candidate,
// NOT a direct catch-all match — MuxMaster returns 404 here when
// RedirectTrailingSlash is off, as this fuzz target sets it) and "ends
// with a trailing slash" (a direct catch-all match with an empty tail) is
// exactly the distinction a slice-based model loses.
func refMatches(route refRoute, reqPath string) bool {
	rest := reqPath // invariant: rest is empty, or starts with '/'
	for _, seg := range route.segs {
		if seg.kind == refCatchAll {
			return rest != "" && rest[0] == '/'
		}
		if rest == "" || rest[0] != '/' {
			return false
		}
		rest = rest[1:]
		var segVal string
		if end := strings.IndexByte(rest, '/'); end < 0 {
			segVal, rest = rest, ""
		} else {
			segVal, rest = rest[:end], rest[end:]
		}
		switch seg.kind {
		case refStatic:
			if segVal != seg.lit {
				return false
			}
		case refParam:
			if segVal == "" {
				return false
			}
		}
	}
	return rest == ""
}

// refPriorityLess reports whether a outranks b per rule 48/49: compare
// segment kinds position by position (static < param < catch-all,
// numerically, so "less" means "higher priority"); the first position
// where they differ decides. This is exactly the precedence a correct
// bounded-backtracking implementation must realise by trying the
// higher-priority branch first and falling back to the next one only when
// the higher-priority branch's OWN continuation fails to match overall
// (DIV-001) — never by picking a partially-matching branch over a fully
// matching lower-priority one.
func refPriorityLess(a, b refRoute) bool {
	n := min(len(a.segs), len(b.segs))
	for i := 0; i < n; i++ {
		if a.segs[i].kind != b.segs[i].kind {
			return a.segs[i].kind < b.segs[i].kind
		}
	}
	return len(a.segs) < len(b.segs)
}

// refPick returns the highest-priority route (if any) among routes that
// structurally match reqPath, per rules 48-51 (regex excluded — see file
// doc comment).
func refPick(routes []refRoute, reqPath string) (refRoute, bool) {
	var best refRoute
	found := false
	for _, r := range routes {
		if !refMatches(r, reqPath) {
			continue
		}
		if !found || refPriorityLess(r, best) {
			best = r
			found = true
		}
	}
	return best, found
}

// differentialPRNG is a tiny, deterministic byte-consuming generator over
// fuzz-provided data — native Go fuzzing only supports a fixed set of
// corpus argument types, so route-set and path generation is driven by
// consuming bytes from a single []byte argument rather than by requesting
// arbitrarily many typed arguments.
type differentialPRNG struct {
	data []byte
	pos  int
}

func (p *differentialPRNG) byte() byte {
	if p.pos >= len(p.data) {
		return 0
	}
	b := p.data[p.pos]
	p.pos++
	return b
}

func (p *differentialPRNG) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(p.byte()) % n
}

// literalVocab is the small alphabet of static segment literals used to
// generate route sets and request paths. Deliberately tiny and overlapping
// (some literals also appear as request segments) so collisions between
// static and param/catch-all branches at the same tree position — the
// DIV-001 shape — are common, not rare, in the generated corpus.
var literalVocab = []string{"a", "b", "list", "x", "id", "static"}

// genSegment produces one pattern segment: a static literal, a named
// parameter, or (only ever as the last segment, handled by the caller) a
// catch-all.
func genSegment(p *differentialPRNG, paramN int) string {
	switch p.intn(3) {
	case 0:
		return literalVocab[p.intn(len(literalVocab))]
	case 1:
		return ":p" + string(rune('0'+paramN))
	default:
		return literalVocab[p.intn(len(literalVocab))] // extra static weight
	}
}

// genPattern builds one random "/"-rooted pattern of 1-3 segments, with a
// roughly 1-in-4 chance the last segment is a catch-all instead.
func genPattern(p *differentialPRNG) string {
	n := 1 + p.intn(3)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('/')
		if i == n-1 && p.intn(4) == 0 {
			b.WriteString("*rest")
		} else {
			b.WriteString(genSegment(p, i))
		}
	}
	return b.String()
}

// genRequestPath builds a random "/"-rooted request path of 1-3 literal
// segments drawn from the same vocabulary the patterns use, so it has a
// realistic chance of matching (fully or partially) several patterns at
// once.
func genRequestPath(p *differentialPRNG) string {
	n := 1 + p.intn(3)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('/')
		b.WriteString(literalVocab[p.intn(len(literalVocab))])
	}
	return b.String()
}

// FuzzRoutingDifferentialAgainstReferenceMatcher is the rmp #259
// differential fuzz target: build a random route set (skipping any
// registration that panics — a structural conflict such as rule 68/71 is
// not this target's concern and must simply mean the route is absent from
// BOTH the real mux and the reference model), pick a random request path,
// and assert MuxMaster's matched pattern (or absence of one) agrees with
// the reference matcher's independent rules-48-51 precedence computation.
func FuzzRoutingDifferentialAgainstReferenceMatcher(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add([]byte{2, 2, 2, 2, 0, 0, 0, 0, 1, 1, 1, 1})
	f.Add([]byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 || len(data) > 512 {
			t.Skip()
		}
		p := &differentialPRNG{data: data}

		mux := muxmaster.New()
		mux.RedirectTrailingSlash = false

		const numRoutes = 6
		var refRoutes []refRoute
		seenPatterns := map[string]bool{}
		for i := 0; i < numRoutes; i++ {
			pattern := genPattern(p)
			if seenPatterns[pattern] {
				continue // exact duplicate — both sides would reject/ignore identically
			}
			seenPatterns[pattern] = true

			pat := pattern // capture per-iteration value for the closure below
			registered := func() (ok bool) {
				defer func() {
					if recover() != nil {
						ok = false
					}
				}()
				mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Matched-Pattern", pat)
					w.WriteHeader(http.StatusOK)
				})
				return true
			}()
			if registered {
				refRoutes = append(refRoutes, parseRefRoute(pattern))
			}
		}

		reqPath := genRequestPath(p)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))

		gotPattern := ""
		gotMatched := rec.Code == http.StatusOK
		if gotMatched {
			gotPattern = rec.Header().Get("X-Matched-Pattern")
		}

		wantRoute, wantMatched := refPick(refRoutes, reqPath)

		if gotMatched != wantMatched {
			t.Fatalf("route set %v, path %q: muxmaster matched=%v (pattern=%q), reference matched=%v (pattern=%q)",
				patternList(refRoutes), reqPath, gotMatched, gotPattern, wantMatched, wantRoute.pattern)
		}
		if gotMatched && gotPattern != wantRoute.pattern {
			t.Fatalf("route set %v, path %q: muxmaster picked %q, reference picked %q (rules 48-51 precedence divergence)",
				patternList(refRoutes), reqPath, gotPattern, wantRoute.pattern)
		}
	})
}

func patternList(routes []refRoute) []string {
	out := make([]string, len(routes))
	for i, r := range routes {
		out[i] = r.pattern
	}
	return out
}
