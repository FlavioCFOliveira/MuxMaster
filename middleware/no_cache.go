package middleware

import "net/http"

// NoCache sets response headers to prevent caching at every layer:
// browsers (Cache-Control / Pragma / Expires), CDNs (Surrogate-Control)
// and nginx-style reverse proxies (X-Accel-Expires). All headers are
// harmless to deployments that do not run an intermediate cache.
func NoCache() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
			h.Set("Pragma", "no-cache")
			h.Set("Expires", "0")
			h.Set("Surrogate-Control", "no-store") // MSR-2026-0056: CDN-level no-store
			h.Set("X-Accel-Expires", "0")          // MSR-2026-0056: nginx X-Accel-Expires
			next.ServeHTTP(w, r)
		})
	}
}
