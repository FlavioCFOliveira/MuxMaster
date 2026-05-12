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
// # Performance
//
// On AMD Ryzen 9 5900HX (Go 1.26.2):
//
//   - Static route:                                    ~25 ns / 0 alloc
//   - 1-param Handle (default):                       ~105 ns / 1 alloc / 384 B
//   - 1-param HandleFast (default):                    ~50 ns / 1 alloc / 32 B
//   - 1-param Handle + Mux.PoolRequestBundle = true:   ~45 ns / 0 alloc / 0 B
//   - 1-param HandleFast + Mux.PoolFastParams = true:  ~44 ns / 0 alloc / 0 B
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
