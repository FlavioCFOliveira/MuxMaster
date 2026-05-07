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
func CleanPath() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := path.Clean(r.URL.Path)
			rawP := r.URL.RawPath

			if p == r.URL.Path && rawP == "" {
				next.ServeHTTP(w, r)
				return
			}

			r2 := r.Clone(r.Context())
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
