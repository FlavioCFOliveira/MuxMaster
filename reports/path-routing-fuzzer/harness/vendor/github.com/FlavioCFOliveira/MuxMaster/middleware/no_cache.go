package middleware

import "net/http"

// Pre-canonicalised header keys and their value slices, allocated once.
// Direct map assignment bypasses textproto.CanonicalMIMEHeaderKey on every
// call (which Header.Set otherwise invokes for every key). The value slices
// must NEVER be mutated by callers — http.Header consumers only read them.
var (
	noCacheCacheControlVal     = []string{"no-store, no-cache, must-revalidate"}
	noCachePragmaVal           = []string{"no-cache"}
	noCacheExpiresVal          = []string{"0"}
	noCacheSurrogateControlVal = []string{"no-store"}
	noCacheXAccelExpiresVal    = []string{"0"}
)

// NoCache sets response headers to prevent caching at every layer:
// browsers (Cache-Control / Pragma / Expires), CDNs (Surrogate-Control)
// and nginx-style reverse proxies (X-Accel-Expires). All headers are
// harmless to deployments that do not run an intermediate cache.
func NoCache() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Opt L3: direct map assignment using pre-canonical keys and shared
			// value slices — eliminates 5× textproto.CanonicalMIMEHeaderKey calls
			// and 5× []string{...} allocations per request. The slices are
			// read-only by net/http consumers, so sharing is safe.
			h["Cache-Control"] = noCacheCacheControlVal
			h["Pragma"] = noCachePragmaVal
			h["Expires"] = noCacheExpiresVal
			h["Surrogate-Control"] = noCacheSurrogateControlVal // MSR-2026-0056
			h["X-Accel-Expires"] = noCacheXAccelExpiresVal      // MSR-2026-0056
			next.ServeHTTP(w, r)
		})
	}
}
