package middleware

import "net/http"

// StripSlashes removes all trailing slashes from r.URL.Path before routing.
// Idempotent: "/a///" becomes "/a" (not "/a//").
func StripSlashes() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			for len(p) > 1 && p[len(p)-1] == '/' {
				p = p[:len(p)-1]
			}
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
