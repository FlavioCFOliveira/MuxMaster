// Package muxmaster is a high-performance HTTP router for Go that is
// 100% compatible with the standard net/http package, requires zero
// external dependencies, and is built on a radix tree (compressed prefix
// trie) that delivers O(k) route lookup — where k is the length of the
// URL path, not the number of registered routes.
//
// # Quick start
//
//	mux := muxmaster.New()
//	mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
//	    id := muxmaster.PathParam(r, "id")
//	    fmt.Fprintf(w, "user=%s", id)
//	})
//	http.ListenAndServe(":8080", mux)
//
// # Route patterns
//
// Static segments match literally; dynamic segments use one of three
// forms:
//
//   - Named parameter:  /users/:id           — single non-slash segment
//   - Regex parameter:  /items/{id:[0-9]+}   — Go regexp restricted match
//   - Catch-all:        /static/*filepath    — matches the rest of the path
//
// Catch-all values are the unsanitised remainder of the request path
// (decoded r.URL.Path, or r.URL.RawPath when Mux.UseRawPath is set), so they
// may contain dot-dot segments. Handlers that map a catch-all value to files
// must use http.FileServer or ServeFiles, which clean the path, or clean and
// confine the value themselves to prevent directory traversal.
//
// # Middleware
//
// Three orthogonal middleware scopes are supported:
//
//   - Mux.Use: stdlib http.Handler middleware applied at registration time;
//     wraps Mux.Handle routes only. Registering a HandleFast route after
//     Use panics (FPE-2026-010).
//   - Mux.UseFast: FastMiddleware that wraps HandleFast routes only.
//   - Mux.Pre: pre-dispatch middleware that wraps BOTH Handle and
//     HandleFast routes; ideal for cross-cutting policy (auth, logging,
//     request_id).
//
// See the SECURITY.md "Pre vs Use security boundary" section for the full
// matrix.
//
// Use and UseFast middleware must be registered before the routes they should
// wrap. Registering routes after the server has started serving is not
// supported.
//
// # Performance
//
// Root-package benchmarks on AMD Ryzen 9 5900HX (Go 1.27.0, 2026-09-26; see
// docs/performance.md for the method and the full tables):
//
//   - Static route:                                   28.6 ns / 0 allocs
//   - 1-param Handle (default):                      118.5 ns / 1 alloc / 384 B
//   - 1-param HandleFast (default):                   46.2 ns / 1 alloc / 32 B
//   - 1-param Handle + Mux.PoolRequestBundle = true:  48.5 ns / 0 allocs
//
// Mux.PoolFastParams = true also removes the 1-param HandleFast allocation;
// the 2026-09-26 run has no benchmark for it.
//
// HandleFast routes bypass the requestCtx allocation by passing Params
// directly as the third handler argument; they trade off stdlib middleware
// compatibility for raw throughput.
//
// Mux.PoolRequestBundle and Mux.PoolFastParams are opt-in switches that
// recycle the per-request objects via sync.Pool, dropping the entire hot
// path to zero allocations. They require a stricter handler lifetime
// contract: handlers must not retain *http.Request (or the Params slice
// for FastHandler) past return. See docs/max-performance.md for the audit
// checklist and worked recipes.
//
// # Compatibility
//
// See COMPATIBILITY.md for the SemVer scheme, tier classification of the
// public API surface, deprecation policy, and Go version policy.
package muxmaster
