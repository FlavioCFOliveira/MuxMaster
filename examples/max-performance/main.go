// Package main demonstrates how to configure MuxMaster for maximum throughput
// and zero per-request allocations on the routing layer.
//
// What this example shows:
//
//  1. Mux.PoolRequestBundle  — recycle the per-request reqBundle (Opt O13)
//  2. Mux.PoolFastParams      — recycle the Params slice for HandleFast (Opt O9)
//  3. Pre vs Use vs UseFast   — picking the right middleware family
//  4. Handle vs HandleFast    — when each API is appropriate
//  5. Safe and unsafe handler patterns under the pooled lifetime contract
//  6. A built-in /bench endpoint that hits the route set in-process so you
//     can run `go run .` and observe pool wins on your own hardware.
//
// Run:
//
//	go run .
//
// Then:
//
//	curl http://localhost:8080/v1/health
//	curl http://localhost:8080/v1/users/42
//	curl http://localhost:8080/v1/orgs/acme/repos/api/issues/123
//	curl http://localhost:8080/bench         # in-process benchmark
//
// Profile the running server:
//
//	curl http://localhost:8080/debug/pprof/profile?seconds=10 > cpu.prof
//	go tool pprof -top -cum cpu.prof
//
// Read the companion guide: docs/max-performance.md
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // attached to mux below via Mount
	"net/http/httptest"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mux := buildMux(log)

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", srv.Addr,
			"PoolRequestBundle", true, "PoolFastParams", true)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// buildMux constructs the maximum-performance configuration explained in
// docs/max-performance.md. It is a separate function so tests can reuse it.
func buildMux(log *slog.Logger) *mm.Mux {
	mux := mm.New()

	// ── Performance opt-ins ──────────────────────────────────────────────────
	//
	// Both pools eliminate the per-request allocation on the routing layer.
	// The lifetime contract: handlers MUST NOT retain *http.Request (Handle)
	// or the Params slice (HandleFast) past return. See docs/max-performance.md
	// "Lifetime contract — what you must not do" for the full audit.

	// PoolRequestBundle: recycles the fused reqBundle (requestCtx +
	// http.Request copy). Drops 1-param routes from 105 ns / 384 B / 1 alloc
	// to ~45 ns / 0 B / 0 allocs.
	mux.PoolRequestBundle = true

	// PoolFastParams: recycles the Params slice handed to FastHandler
	// routes. Drops FastParam routes from 50 ns / 32 B / 1 alloc to
	// ~44 ns / 0 B / 0 allocs.
	mux.PoolFastParams = true

	// ── Pre middleware: runs ONCE per request, before route lookup ────────────
	//
	// Pre applies uniformly to BOTH Handle and HandleFast routes. Use Pre for
	// cross-cutting policies that must wrap every request: auth gates, ID
	// generation, panic recovery, real-IP resolution.
	mux.Pre(
		mw.RequestID(),                  // X-Request-Id propagation
		mw.RecovererWithLogger(log),     // recover from panics in handlers
	)

	// ── Group with stdlib middleware for the JSON API ─────────────────────────
	//
	// Use applies stdlib middleware (func(http.Handler) http.Handler) at route
	// registration time. It only wraps Handle routes — HandleFast routes
	// registered after Use will panic at registration (a safety check that
	// prevents silently bypassing auth on the fast path).
	v1 := mux.Group("/v1")
	v1.Use(mw.Logger(os.Stdout))

	// JSON REST routes — use Handle (stdlib http.Handler signature).
	v1.GET("/users/:id", getUser)                                  // 1 param, 0 alloc
	v1.GET("/users/:id/orders/:orderID", getUserOrder)             // 2 params, 0 alloc
	v1.GET("/orgs/:org/repos/:repo/issues/:num", getRepoIssue)     // 3 params, 0 alloc
	v1.GET("/static/*filepath", listStaticFile)                    // catch-all, 0 alloc
	v1.POST("/users", createUser)

	// Regex-constrained route on a different prefix so it does not collide
	// with the ":id" wildcard above (a regex param and a `:name` param cannot
	// share the same parent in the radix tree).
	v1.GET("/profiles/{id:[0-9]+}", getUserProfile)                // regex-constrained

	// Background work pattern — copy primitives before spawning a goroutine.
	v1.POST("/events", postEvent)

	// ── Hot routes via HandleFast ─────────────────────────────────────────────
	//
	// HandleFast bypasses the standard context allocation entirely. Params
	// arrive as a 3rd argument. Stdlib Use middleware is NOT applied — use
	// UseFast for the FastMiddleware family, or Pre for cross-cutting policy.
	//
	// Latency: ~25 ns for static, ~44 ns for 1 param with PoolFastParams.
	mux.UseFast(fastTimer(log))
	mux.GETFast("/v1/health", healthFast)
	mux.GETFast("/v1/metrics/:metric", metricsFast)

	// ── Operational endpoints ────────────────────────────────────────────────
	//
	// /bench runs an in-process benchmark and returns JSON — useful when
	// demonstrating pool wins to users without leaving curl.
	mux.GET("/bench", benchHandler)

	// pprof — Mount the standard net/http/pprof handler tree at /debug/pprof/.
	mux.Mount("/debug/pprof", http.DefaultServeMux)

	// ── Diagnostics endpoint: a route that returns the active Mux config ─────
	mux.GET("/config", configHandler)

	return mux
}

// ─── Handlers — stdlib http.Handler with PoolRequestBundle = true ────────────

// getUser is the canonical example of a pool-safe handler: it reads `r` only
// during the function body and never spawns a goroutine that outlives the
// handler. With PoolRequestBundle = true this runs at ~45 ns / 0 allocs.
func getUser(w http.ResponseWriter, r *http.Request) {
	id := mm.PathParam(r, "id")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":     id,
		"method": r.Method,
		"path":   r.URL.Path,
	})
}

// getUserOrder reads two path params via ParamsFromContext when you need
// more than one — it returns the whole slice with a single accessor call.
func getUserOrder(w http.ResponseWriter, r *http.Request) {
	ps := mm.ParamsFromContext(r.Context())
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user_id":  ps.Get("id"),
		"order_id": ps.Get("orderID"),
	})
}

func getRepoIssue(w http.ResponseWriter, r *http.Request) {
	ps := mm.ParamsFromContext(r.Context())
	num, _ := strconv.Atoi(ps.Get("num"))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"org":    ps.Get("org"),
		"repo":   ps.Get("repo"),
		"number": num,
	})
}

func listStaticFile(w http.ResponseWriter, r *http.Request) {
	// PathParam returns a string — safe to capture, copy, or send to a goroutine.
	path := mm.PathParam(r, "filepath")
	_, _ = io.WriteString(w, "would serve: "+path)
}

func getUserProfile(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      mm.PathParam(r, "id"),
		"pattern": mm.RoutePattern(r), // e.g. "/v1/users/{id:[0-9]+}/profile"
	})
}

func createUser(w http.ResponseWriter, r *http.Request) {
	var u struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(u)
}

// postEvent demonstrates the background-work pattern under PoolRequestBundle.
//
// 1. Read everything you need into local values (strings, byte slices).
// 2. Then spawn the goroutine.
//
// Capturing `r` directly would be a use-after-free against the recycled bundle.
func postEvent(w http.ResponseWriter, r *http.Request) {
	// Snapshot primitives BEFORE spawning anything async.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	requestID := mw.GetRequestID(r.Context())
	remoteAddr := r.RemoteAddr

	// Now we are safe to fan out — `body`, `requestID`, `remoteAddr` are all
	// values; the bundle can be recycled the moment we return.
	go func() {
		fmt.Fprintf(os.Stderr,
			"event accepted req=%s peer=%s bytes=%d\n",
			requestID, remoteAddr, len(body))
	}()

	w.WriteHeader(http.StatusAccepted)
}

// ─── FastHandler — Mux.PoolFastParams = true ────────────────────────────────

func healthFast(w http.ResponseWriter, r *http.Request, _ mm.Params) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// metricsFast reads ps.Get("metric") and writes a Prometheus-style line.
// Note: do NOT keep `ps` alive past return — when PoolFastParams is on,
// the slice is recycled to a pool the instant this function exits.
func metricsFast(w http.ResponseWriter, r *http.Request, ps mm.Params) {
	name := ps.Get("metric")
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "%s 42\n", name)
}

// fastTimer is a FastMiddleware — it wraps FastHandler the way stdlib
// middleware wraps http.Handler. It runs ONLY on HandleFast routes.
func fastTimer(log *slog.Logger) mm.FastMiddleware {
	return func(next mm.FastHandler) mm.FastHandler {
		return func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
			start := time.Now()
			next(w, r, ps)
			log.Debug("fast", "path", r.URL.Path, "elapsed", time.Since(start))
		}
	}
}

// ─── /bench — in-process benchmark to demonstrate the pool wins ──────────────

func benchHandler(w http.ResponseWriter, r *http.Request) {
	const iterations = 200_000

	// Build two muxes — one default, one with PoolRequestBundle enabled — and
	// hit the same route on each, measuring nanoseconds per request.
	mux := mm.New()
	mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {})

	muxPool := mm.New()
	muxPool.PoolRequestBundle = true
	muxPool.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	rec := httptest.NewRecorder()

	// Warm-up so first-call costs do not skew the result.
	for range 1000 {
		mux.ServeHTTP(rec, req)
		muxPool.ServeHTTP(rec, req)
	}

	// Snapshot allocations before each block and divide by iteration count.
	startDefault := time.Now()
	allocsDefault := runMeasured(iterations, func() { mux.ServeHTTP(rec, req) })
	nsDefault := time.Since(startDefault).Nanoseconds() / int64(iterations)

	startPool := time.Now()
	allocsPool := runMeasured(iterations, func() { muxPool.ServeHTTP(rec, req) })
	nsPool := time.Since(startPool).Nanoseconds() / int64(iterations)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"route":         "/users/:id",
		"iterations":    iterations,
		"default":       benchResult{NsPerOp: nsDefault, AllocsPerOp: allocsDefault},
		"pooled":        benchResult{NsPerOp: nsPool, AllocsPerOp: allocsPool},
		"speedup_ratio": fmt.Sprintf("%.2fx", float64(nsDefault)/float64(nsPool)),
		"go":            runtime.Version(),
	})
}

type benchResult struct {
	NsPerOp     int64   `json:"ns_per_op"`
	AllocsPerOp float64 `json:"allocs_per_op"`
}

// runMeasured runs fn N times and returns allocs/op as a float64.
func runMeasured(n int, fn func()) float64 {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	for range n {
		fn()
	}
	runtime.ReadMemStats(&m2)
	return float64(m2.Mallocs-m1.Mallocs) / float64(n)
}

// ─── /config — surface the live Mux configuration ──────────────────────────────

func configHandler(w http.ResponseWriter, r *http.Request) {
	// The mux that wired up this route owns the field — but we hand the config
	// back in a serialisable form so users can verify the pool opt-ins are on.
	_ = json.NewEncoder(w).Encode(map[string]any{
		"PoolRequestBundle":      true,
		"PoolFastParams":         true,
		"RedirectTrailingSlash":  true,
		"HandleMethodNotAllowed": true,
		"HandleOPTIONS":          true,
		"go":                     runtime.Version(),
		"goarch":                 runtime.GOARCH,
		"goos":                   runtime.GOOS,
		"maxprocs":               runtime.GOMAXPROCS(0),
	})
}
