// Package main demonstrates two authentication strategies with MuxMaster
// using the built-in middleware:
//
//  1. HTTP Basic Auth via middleware.BasicAuth — protects /admin routes,
//     composed with middleware.ThrottlePerIP to mitigate online brute-force
//     (per SECURITY.md MM-2026-0027).
//
//  2. API key middleware via middleware.APIKey — protects /api routes.
//     Keys are SHA-256 hashed at construction time, so the per-request cost
//     is one hash plus a [32]byte map lookup.
//
// Public routes need no credentials. Protected routes return 401 when
// credentials are missing or wrong.
//
// Start:
//
//	go run .
//
// Smoke-test:
//
//	# Public
//	curl http://localhost:8080/health
//
//	# Basic Auth (admin / s3cr3t)
//	curl -u admin:s3cr3t http://localhost:8080/admin/dashboard
//	curl -u admin:wrong  http://localhost:8080/admin/dashboard  # 401
//
//	# API key
//	curl -H "X-API-Key: key-alice" http://localhost:8080/api/profile
//	curl -H "X-API-Key: bad"       http://localhost:8080/api/profile  # 401
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// apiKeys maps X-API-Key values to the owner identity passed into the request
// context by middleware.APIKey. In production, populate this from a database
// or a secrets manager and rebuild the middleware on rotation.
var apiKeys = map[string]string{
	"key-alice": "alice",
	"key-bob":   "bob",
}

// errMsg builds a one-field JSON error payload.
func errMsg(msg string) map[string]string { return map[string]string{"error": msg} }

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	r := mm.New()

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(
		mw.RequestID(),
		mw.Logger(os.Stdout),
		mw.RecovererWithLogger(log),
	)

	// ── Custom error handlers ─────────────────────────────────────────────────
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = mm.JSON(w, http.StatusNotFound, errMsg("not found"))
	})
	r.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = mm.JSON(w, http.StatusMethodNotAllowed, errMsg("method not allowed"))
	})
	r.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		code := http.StatusInternalServerError
		var he mm.HTTPError
		if errors.As(err, &he) {
			code = he.StatusCode()
		}
		_ = mm.JSON(w, code, errMsg(err.Error()))
	}

	// ── Public routes — no authentication required ────────────────────────────

	// FastHandler: zero allocation — ideal for high-frequency health probes.
	r.GETFast("/health", func(w http.ResponseWriter, _ *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	r.GET("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"hint": "try GET /admin/dashboard (Basic Auth) or GET /api/profile (X-API-Key)",
		})
	})

	// ── Admin group — HTTP Basic Auth + per-IP rate limit ─────────────────────
	//
	// SECURITY.md MM-2026-0027 — BasicAuth has no built-in rate limiting; an
	// attacker can attempt unlimited credentials. Compose it with ThrottlePerIP
	// (10 concurrent requests per IP, 5 s queue timeout) to mitigate online
	// brute-force. Order matters: throttle is registered first so it sees the
	// request before the auth check.
	//
	// Credentials: admin / s3cr3t  or  viewer / readonly
	admin := r.Group("/admin")
	admin.Use(
		mw.ThrottlePerIP(10, 5*time.Second, nil),
		mw.BasicAuth("Admin Area", map[string]string{
			"admin":  "s3cr3t",
			"viewer": "readonly",
		}),
	)

	admin.GET("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		_ = mm.JSON(w, http.StatusOK, map[string]any{
			"page":     "dashboard",
			"trace_id": mw.GetRequestID(r.Context()),
		})
	})

	// DELETEE: error-returning handler — delegates error formatting to ErrorHandler.
	admin.DELETEE("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := mm.PathParam(r, "id")
		if id == "0" {
			return mm.Error(http.StatusNotFound, fmt.Errorf("user %q not found", id))
		}
		mm.NoContent(w)
		return nil
	})

	// ── API group — X-API-Key header via middleware.APIKey ────────────────────
	//
	// middleware.APIKey hashes every key with SHA-256 at construction time, so
	// per-request cost is one SHA-256 hash plus a [32]byte map lookup — no
	// per-request iteration or string comparison. The identity associated with
	// the matched key is injected into the request context and retrieved via
	// middleware.GetAPIKeyIdentity.
	api := r.Group("/api")
	api.Use(mw.APIKey(mw.APIKeyOptions{
		Keys: apiKeys,
		// Header defaults to "X-API-Key"; override here if you need a different one.
	}))

	api.GET("/profile", func(w http.ResponseWriter, r *http.Request) {
		owner, _ := mw.GetAPIKeyIdentity(r.Context())
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"owner":    owner,
			"trace_id": mw.GetRequestID(r.Context()),
		})
	})

	api.GETE("/items/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := mm.PathParam(r, "id")
		if id == "0" {
			return mm.Error(http.StatusNotFound, fmt.Errorf("item %q not found", id))
		}
		return mm.JSON(w, http.StatusOK, map[string]string{"id": id, "name": "Widget " + id})
	})

	// ── Server with hardened timeouts ─────────────────────────────────────────
	//
	// SECURITY.md MM-2026-0024 — MuxMaster is an http.Handler and does not
	// configure the http.Server timeouts itself. Set them explicitly to mitigate
	// Slowloris and similar slow-read/slow-write attacks.
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}

	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("server stopped")
}
