package middleware

import "net/http"

// StripSlashes removes all trailing slashes from r.URL.Path before routing.
// Idempotent: "/a///" becomes "/a" (not "/a//").
//
// When r.URL.RawPath is set (the original raw form is preserved by net/http
// only when it differs from the decoded Path), StripSlashes also strips
// trailing '/' bytes from RawPath. Without this the dispatch path would
// diverge between Path and RawPath when Mux.UseRawPath is true
// (HPS-2026-0004).
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
				if r.URL.RawPath != "" {
					rp := r.URL.RawPath
					for len(rp) > 1 && rp[len(rp)-1] == '/' {
						rp = rp[:len(rp)-1]
					}
					r2.URL.RawPath = rp
				}
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
