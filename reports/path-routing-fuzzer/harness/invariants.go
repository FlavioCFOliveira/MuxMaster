// Package fuzz contains differential/invariant harnesses used by the
// path-routing-fuzzer security agent.
//
// The harness is a separate Go module so it can depend on competitor
// routers (httprouter, chi, bunrouter) without polluting the production
// module's dependency closure.
package fuzz

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// routeResult is the normalised outcome of a routing decision.
// It collapses the 4 routers' native result shapes into a single type that
// can be compared for divergence.
type routeResult struct {
	// matched is true when a request-handler was selected.
	matched bool
	// pattern is the canonical registered pattern (or "" for wildcards we
	// cannot extract cheaply).
	pattern string
	// status is the HTTP status code returned on the recorder (after the
	// handler, if any, runs).
	status int
	// location is the Location response header when the router emits a
	// redirect, or "" otherwise. Used to flag TSR/FixedPath disclosures.
	location string
	// paramCount is the number of captured path params (best effort; some
	// routers expose them only via context).
	paramCount int
	// sawParamValueWithSlash is a post-condition check — a :param slot
	// should never hold a '/'. This flips true when we detect a router
	// violating this invariant.
	sawParamValueWithSlash bool
	// handlerID is the symbolic id of the handler that ran (e.g. "admin",
	// "user", "catchall"). Used to spot "wrong handler" bypass.
	handlerID string
}

// containsDotDotSegment reports whether path contains a ".." (dot-dot)
// segment after best-effort percent-decoding. Used as the traversal oracle.
// A '#' in the input marks a fragment boundary — everything after '#' is
// client-side, so traversal in the fragment is not reachable over HTTP.
func containsDotDotSegment(path string) bool {
	if path == "" {
		return false
	}
	// Strip fragment — '#' is not sent over HTTP.
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	// Strip query — '?' does not affect the routed path segment.
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// First pass: literal ".." inside slash-delimited segment.
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return true
		}
	}
	// Second pass: recursively percent-decode until stable or bounded.
	cur := path
	for i := 0; i < 3; i++ {
		next := percentDecodeOnce(cur)
		if next == cur {
			break
		}
		for _, seg := range strings.Split(next, "/") {
			if seg == ".." {
				return true
			}
		}
		cur = next
	}
	return false
}

// percentDecodeOnce decodes %HH sequences once, returning the original string
// on decode error. Unlike net/url.QueryUnescape this does not treat '+' as
// space, and it tolerates partial percent sequences by leaving them intact.
func percentDecodeOnce(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(hexToByte(s[i+1])<<4 | hexToByte(s[i+2]))
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexToByte(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

// containsCRLF reports whether s contains a raw CR, LF, or %0d/%0a.
func containsCRLF(s string) bool {
	if strings.ContainsAny(s, "\r\n") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "%0d") || strings.Contains(lower, "%0a")
}

// containsNullByte reports whether s contains a raw NUL or %00.
func containsNullByte(s string) bool {
	if strings.ContainsRune(s, 0) {
		return true
	}
	return strings.Contains(s, "%00")
}

// isAcceptablePath rejects inputs that cannot be exercised through the
// fuzz harness without triggering spurious panics in net/http's request
// constructor. These aren't "bypass" candidates — they just crash the
// harness before the router is even reached.
func isAcceptablePath(s string) bool {
	if len(s) == 0 || len(s) > 8192 {
		return false
	}
	if s[0] != '/' {
		return false
	}
	if !utf8.ValidString(s) {
		return false
	}
	// Raw CR/LF in the request target cannot even reach the router —
	// httptest.NewRequest panics at the http.ReadRequest stage.
	if strings.ContainsAny(s, "\r\n") {
		return false
	}
	// httptest.NewRequest calls url.Parse internally and panics on
	// malformed percent-escapes (e.g. %u002e or bare %). Those payloads
	// are caught by net/http long before the router is reached, so they
	// are not useful bypass candidates.
	if _, err := url.Parse("http://example.test" + s); err != nil {
		return false
	}
	return true
}
