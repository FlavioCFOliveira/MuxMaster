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
// of the request (see the Terminology section in README.md): a new
// *http.Request with a new URL, but sharing the original's header map and
// context. The original request passed to StripSlashes is never mutated.
func StripSlashes() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			for len(p) > 1 && p[len(p)-1] == '/' {
				p = p[:len(p)-1]
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
