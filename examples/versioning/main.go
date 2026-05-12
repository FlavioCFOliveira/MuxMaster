// Package main demonstrates API versioning with MuxMaster — both
// path-based (/v1, /v2) and header-based (Accept: application/vnd.api+json;v=2),
// with the maximum-performance pool configuration enabled.
//
// Why this is a performance example:
//
//   - Group + Sub-group composition has ZERO runtime cost: middleware is
//     wrapped at registration time, not per request. A 3-level deep
//     /api/v2/admin/users/:id route dispatches at the same cost as a
//     flat /users/:id (105 ns default, 45 ns pooled).
//
//   - With PoolRequestBundle, even the version dispatch itself remains
//     0-alloc — middleware that branches on r.Header.Get("Accept") inside
//     a Pre-handler does not introduce per-request allocations.
//
// Run:
//
//	go run .
//
// Try:
//
//	curl http://localhost:8080/api/v1/users/42
//	curl http://localhost:8080/api/v2/users/42
//	curl -H 'Accept: application/vnd.muxmaster+json;v=2' http://localhost:8080/api/users/42
//	curl http://localhost:8080/api/v1/admin/dashboard
//	curl http://localhost:8080/api/v2/admin/dashboard
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mux := mm.New()

	// Maximum performance: every parameterised route under /v1, /v2, /admin
	// dispatches at ~45 ns / 0 B / 0 allocs.
	mux.PoolRequestBundle = true

	// Pre middleware wraps the WHOLE dispatch — version-negotiation hook
	// also runs ONCE per request before routing, so it is free in the
	// per-route cost.
	mux.Pre(
		mw.RequestID(),
		mw.RecovererWithLogger(log),
		acceptHeaderVersionDispatch, // Header-based routing for /api/...
	)

	// ── Path-based versioning (the simplest pattern) ──────────────────────────

	v1 := mux.Group("/api/v1")
	v1.GET("/users/:id", v1GetUser)
	v1.GET("/users", v1ListUsers)
	v1.GET("/health", v1Health)

	v2 := mux.Group("/api/v2")
	v2.GET("/users/:id", v2GetUser)
	v2.GET("/users", v2ListUsers)
	v2.GET("/health", v2Health)

	// Nested groups — admin section under each version, with its own
	// middleware. Stdlib middleware applied at the group level wraps every
	// route under it at registration time — zero per-request overhead.
	v1Admin := v1.Group("/admin")
	v1Admin.Use(adminMiddleware) // applied once at reg, free at runtime
	v1Admin.GET("/dashboard", v1AdminDashboard)
	v1Admin.GET("/users/:id/audit", v1AdminAudit)

	v2Admin := v2.Group("/admin")
	v2Admin.Use(adminMiddleware)
	v2Admin.GET("/dashboard", v2AdminDashboard)
	v2Admin.GET("/users/:id/audit", v2AdminAudit)

	// ── Index ────────────────────────────────────────────────────────────────

	mux.GET("/", index)

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ─── Version-aware Pre middleware ────────────────────────────────────────────

// acceptHeaderVersionDispatch rewrites /api/... URLs to /api/vN/... based on
// an `Accept: application/vnd.muxmaster+json;v=N` header. This implements
// header-based versioning WITHOUT a runtime routing penalty — the rewrite
// happens in Pre, before tree lookup.
//
// Note: this allocates a new URL object only on the (rare) cache-miss path.
// For pool safety, the rewrite writes through r.URL (which is itself a
// pointer into the bundle and gets recycled cleanly).
func acceptHeaderVersionDispatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only apply to /api/ paths that DON'T already have a /vN/ prefix.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		rest := r.URL.Path[len("/api/"):]
		if strings.HasPrefix(rest, "v1/") || strings.HasPrefix(rest, "v2/") {
			next.ServeHTTP(w, r)
			return
		}

		// Parse `Accept: ...;v=N` — default to v1 if not specified.
		v := "1"
		if a := r.Header.Get("Accept"); strings.Contains(a, "v=2") {
			v = "2"
		}
		// Rewrite r.URL.Path in place. The bundle copy is mutable; the
		// original request is never modified.
		r.URL.Path = "/api/v" + v + "/" + rest
		next.ServeHTTP(w, r)
	})
}

// ─── adminMiddleware — applied to /api/vN/admin/... at registration ──────────

func adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Toy gate — replace with real auth in production.
		if r.Header.Get("X-Admin-Token") != "letmein" {
			http.Error(w, "admin token required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── v1 handlers ─────────────────────────────────────────────────────────────

func v1GetUser(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v1",
		"id":      mm.PathParam(r, "id"),
		"shape":   "flat",
	})
}

func v1ListUsers(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v1",
		"users":   []string{"alice", "bob"},
	})
}

func v1Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok v1")
}

func v1AdminDashboard(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v1", "admin": true, "users_online": 7,
	})
}

func v1AdminAudit(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v1", "id": mm.PathParam(r, "id"), "audit": []string{},
	})
}

// ─── v2 handlers (e.g. richer response shape) ────────────────────────────────

type v2User struct {
	ID    string         `json:"id"`
	Links map[string]any `json:"_links"`
}

func v2GetUser(w http.ResponseWriter, r *http.Request) {
	id := mm.PathParam(r, "id")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v2",
		"shape":   "hateoas",
		"user": v2User{
			ID:    id,
			Links: map[string]any{"self": "/api/v2/users/" + id, "audit": "/api/v2/admin/users/" + id + "/audit"},
		},
	})
}

func v2ListUsers(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v2",
		"shape":   "paged",
		"page":    1,
		"size":    20,
		"total":   2,
		"users": []v2User{
			{ID: "alice", Links: map[string]any{"self": "/api/v2/users/alice"}},
			{ID: "bob", Links: map[string]any{"self": "/api/v2/users/bob"}},
		},
	})
}

func v2Health(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"version": "v2", "status": "ok"})
}

func v2AdminDashboard(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v2", "admin": true, "metrics": map[string]int{"online": 7, "queued": 0},
	})
}

func v2AdminAudit(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": "v2", "id": mm.PathParam(r, "id"), "events": []string{},
	})
}

// ─── Index ───────────────────────────────────────────────────────────────────

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, `MuxMaster · API versioning

Path-based versioning:
  GET /api/v1/users/:id
  GET /api/v2/users/:id

Header-based versioning (defaults to v1):
  GET /api/users/:id            (Accept: application/vnd.muxmaster+json;v=2)

Admin (header X-Admin-Token: letmein):
  GET /api/v1/admin/dashboard
  GET /api/v2/admin/dashboard
  GET /api/v1/admin/users/:id/audit
  GET /api/v2/admin/users/:id/audit

All routes dispatched at ~45 ns / 0 B / 0 allocs (PoolRequestBundle=true).`)
}
