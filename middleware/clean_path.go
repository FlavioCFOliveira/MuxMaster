package middleware

import (
	"net/http"
	"net/url"
	"path"
)

// CleanPath normalises r.URL.Path via path.Clean before routing.
// When r.URL.RawPath is set, it is also cleaned; if the cleaned RawPath
// differs from what path.Clean produces for the percent-decoded Path,
// RawPath is zeroed to prevent encoded path-traversal bypass (MM-2026-0018).
//
// ORDERING: When composing CleanPath with path-inspecting Pre-gates
// (authorization checks that reject certain prefixes), CleanPath MUST be
// registered first. A gate registered before CleanPath sees the raw,
// unnormalised path and can be bypassed by /admin/../public, //admin, or
// %2e%2e-encoded variants. CleanPath must run first to normalise before
// the gate inspects the path (rmp #284, TM-2026-040).
//
// When the path changes, next receives a shallow copy of the request (see
// the Terminology section in README.md): a new *http.Request with a new
// URL, but sharing the original's header map and context. The original
// request passed to CleanPath is never mutated.
func CleanPath() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := path.Clean(r.URL.Path)
			rawP := r.URL.RawPath

			if p == r.URL.Path && rawP == "" {
				next.ServeHTTP(w, r)
				return
			}

			// [waste-hunt WH-04] Shallow request copy (see
			// specification/README.md Terminology) instead of r.Clone's
			// deep copy of the header map, Trailer, Form and
			// TransferEncoding — only URL.Path (and possibly URL.RawPath
			// below) changes. r2 shares the original's header map and
			// context; the original request is never mutated.
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = p

			if rawP != "" {
				cleanedRaw := path.Clean(rawP)
				if cleanedRaw != rawP {
					// RawPath contained traversal sequences — zero it so the
					// router uses the (already-cleaned) decoded Path.
					r2.URL.RawPath = ""
				} else if decoded, err := url.PathUnescape(rawP); err == nil && decoded != p {
					// MSR-2026-0061: a RawPath like /a/%2e%2e/b is byte-for-byte
					// identical after path.Clean (path.Clean does not decode),
					// but its decoded form (/a/../b) cleans to a different path
					// than r.URL.Path. Zero RawPath so dispatch follows the
					// already-cleaned decoded Path rather than the
					// encoded-traversal RawPath.
					r2.URL.RawPath = ""
				} else {
					r2.URL.RawPath = cleanedRaw
				}
			}

			next.ServeHTTP(w, r2)
		})
	}
}
