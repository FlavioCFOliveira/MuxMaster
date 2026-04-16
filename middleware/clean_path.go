package middleware

import (
	"net/http"
	"path"
)

// CleanPath normalises r.URL.Path via path.Clean before routing.
func CleanPath() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := path.Clean(r.URL.Path)
			if p != r.URL.Path {
				r2 := r.Clone(r.Context())
				r2.URL.Path = p
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
