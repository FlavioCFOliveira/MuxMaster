package middleware

import "net/http"

// StripSlashes removes trailing slashes from r.URL.Path before routing.
func StripSlashes() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if len(p) > 1 && p[len(p)-1] == '/' {
				r2 := r.Clone(r.Context())
				r2.URL.Path = p[:len(p)-1]
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
