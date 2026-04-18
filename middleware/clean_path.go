package middleware

import (
	"net/http"
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
				} else {
					r2.URL.RawPath = cleanedRaw
				}
			}

			next.ServeHTTP(w, r2)
		})
	}
}
