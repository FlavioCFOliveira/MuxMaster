package middleware

import "net/http"

// Pre-canonicalised header keys and their constant string values. Direct
// map assignment bypasses textproto.CanonicalMIMEHeaderKey on every call
// (which Header.Set otherwise invokes for every key).
//
// MID-NOCACHE-1 (sprint 18 waste-hunt, same class as MID-SETHEADER-1 in
// set_header.go): an earlier version of this optimisation hoisted the
// single-element []string HEADER VALUES themselves into these package-level
// variables — not just the strings — so every NoCache() instance and every
// request in the process shared the exact same slice for a given header.
// http.Header consumers that only read via Get/Set/Values cannot observe
// this, but any code indexing directly into the slice
// (w.Header()["Cache-Control"][0] = ...) mutates the shared backing array,
// corrupting that header for every other request served by ANY NoCache()
// middleware, process-wide, until restart. Only the strings are safe to
// hoist; each request must get a freshly allocated one-element slice.
var (
	noCacheCacheControl     = "no-store, no-cache, must-revalidate"
	noCachePragma           = "no-cache"
	noCacheExpires          = "0"
	noCacheSurrogateControl = "no-store"
	noCacheXAccelExpires    = "0"
)

// NoCache sets response headers to prevent caching at every layer:
// browsers (Cache-Control / Pragma / Expires), CDNs (Surrogate-Control)
// and nginx-style reverse proxies (X-Accel-Expires). All headers are
// harmless to deployments that do not run an intermediate cache.
func NoCache() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Opt L3: direct map assignment using pre-canonical keys —
			// eliminates 5× textproto.CanonicalMIMEHeaderKey calls per
			// request.
			//
			// MID-NOCACHE-2 (waste-hunt gate follow-up to MID-NOCACHE-1):
			// all 5 header values share ONE freshly allocated [5]string
			// backing array per request — 1 allocation total, not 5. Each
			// header's slice is the FULL slice expression vals[i:i+1:i+1],
			// which caps its capacity at 1: an append to any one of these
			// 5 header slots (e.g. a later w.Header().Add on the same key)
			// grows into a NEW backing array rather than overwriting the
			// adjacent slot in vals, so the 5 headers stay isolated from
			// each other despite sharing the array. Isolation BETWEEN
			// requests (MID-NOCACHE-1's original concern) is unaffected:
			// vals is allocated fresh here, every call, exactly as the 5
			// separate slices were before.
			vals := &[5]string{
				noCacheCacheControl,
				noCachePragma,
				noCacheExpires,
				noCacheSurrogateControl,
				noCacheXAccelExpires,
			}
			h["Cache-Control"] = vals[0:1:1]
			h["Pragma"] = vals[1:2:2]
			h["Expires"] = vals[2:3:3]
			h["Surrogate-Control"] = vals[3:4:4] // MSR-2026-0056
			h["X-Accel-Expires"] = vals[4:5:5]   // MSR-2026-0056
			next.ServeHTTP(w, r)
		})
	}
}
