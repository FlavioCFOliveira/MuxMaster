package middleware

import (
	"net/http"
	"net/url"
)

// StripSlashes removes all trailing slashes from r.URL.Path before routing.
// Idempotent: "/a///" becomes "/a" (not "/a//").
//
// When r.URL.RawPath is set (the original raw form is preserved by net/http
// only when it differs from the decoded Path), StripSlashes also strips
// trailing '/' bytes from RawPath. Without this the dispatch path would
// diverge between Path and RawPath when Mux.UseRawPath is true
// (HPS-2026-0004).
//
// When the path has trailing slashes to strip, next receives a shallow copy
// of the request (see the Terminology section in the MuxMaster
// specification/README.md): a new *http.Request with a new URL, but sharing
// the original's header map and context. The original request passed to
// StripSlashes is never mutated.
func StripSlashes() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			n := 0
			for len(p) > 1 && p[len(p)-1] == '/' {
				p = p[:len(p)-1]
				n++
			}
			if p != r.URL.Path {
				// [waste-hunt WH-04] Shallow request copy (see
				// specification/README.md Terminology) instead of
				// r.Clone's deep copy — see clean_path.go for the
				// rationale.
				r2 := new(http.Request)
				*r2 = *r
				r2.URL = new(url.URL)
				*r2.URL = *r.URL
				r2.URL.Path = p
				if r.URL.RawPath != "" {
					r2.URL.RawPath = stripTrailingPathSeparators(r.URL.RawPath, n)
				}
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// stripTrailingPathSeparators removes up to n trailing path-separator
// tokens from rp, where a token is either a literal '/' byte or a
// percent-encoded slash ("%2F"/"%2f", 3 bytes). n is the number of trailing
// '/' characters already stripped from the decoded r.URL.Path.
//
// A raw request path can spell a trailing slash two ways: literally, or
// percent-encoded (e.g. a client sending "GET /a%2f" — net/url decodes
// this to Path="/a/", RawPath="/a%2f", per RFC 3986 §2.1). Stripping only
// literal '/' bytes from RawPath — as a byte-suffix loop mirroring the
// Path loop would — silently ignores an encoded trailing slash and
// desynchronises RawPath from the newly stripped Path (FPE-O14-003: found
// while restoring FuzzStripSlashesIdempotency, rmp #274 part 5b). This
// walks RawPath from the end, consuming one token per stripped Path
// character, so the two stay a valid net/url pair.
func stripTrailingPathSeparators(rp string, n int) string {
	for i := 0; i < n && len(rp) > 1; i++ {
		switch {
		case rp[len(rp)-1] == '/':
			rp = rp[:len(rp)-1]
		case len(rp) >= 3 && rp[len(rp)-3] == '%' && rp[len(rp)-2] == '2' &&
			(rp[len(rp)-1] == 'F' || rp[len(rp)-1] == 'f'):
			rp = rp[:len(rp)-3]
		default:
			// RawPath does not encode as many trailing separators as Path
			// had — this cannot happen for a Path/RawPath pair net/http's
			// URL parser produces (they are then, by construction, always
			// in sync). Stop rather than strip an unrelated byte.
			return rp
		}
	}
	return rp
}
