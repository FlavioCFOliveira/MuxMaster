// Package main demonstrates two authentication strategies with MuxMaster:
//
//  1. HTTP Basic Auth via the built-in middleware.BasicAuth — protects /admin routes.
//  2. API key middleware (X-API-Key header) — protects /api routes.
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

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─── API key store ────────────────────────────────────────────────────────────

// apiKeys maps X-API-Key values to the owner's display name.
// In production, query a database or a secrets manager instead.
var apiKeys = map[string]string{
	"key-alice": "alice",
	"key-bob":   "bob",
}

// ownerKey is the context key used by requireAPIKey to carry the owner name.
type ownerKey struct{}

// requireAPIKey is a middleware that reads X-API-Key, looks it up in apiKeys,
// and stores the owner's name in the request context.
// Unknown keys are rejected with 401 before the handler runs.
func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		owner, ok := apiKeys[key]
		if !ok {
			_ = mm.JSON(w, http.StatusUnauthorized, errMsg("missing or invalid X-API-Key"))
			return
		}
		ctx := context.WithValue(r.Context(), ownerKey{}, owner)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ownerFromCtx returns the API key owner stored by requireAPIKey, or "".
func ownerFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(ownerKey{}).(string)
	return v
}

// errMsg builds a one-field JSON error payload.
func errMsg(msg string) map[string]string { return map[string]string{"error": msg} }

// ─── main ─────────────────────────────────────────────────────────────────────

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

	r.GET("/", func(w http.ResponseWriter, r *http.Request) {
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"hint": "try GET /admin/dashboard (Basic Auth) or GET /api/profile (X-API-Key)",
		})
	})

	// ── Admin group — HTTP Basic Auth ─────────────────────────────────────────
	//
	// mw.BasicAuth uses crypto/subtle.ConstantTimeCompare internally, so the
	// comparison time is the same whether the username or the password is wrong.
	//
	// Credentials: admin / s3cr3t  or  viewer / readonly
	admin := r.Group("/admin")
	admin.Use(mw.BasicAuth("Admin Area", map[string]string{
		"admin":  "s3cr3t",
		"viewer": "readonly",
	}))

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

	// ── API group — X-API-Key header ──────────────────────────────────────────
	//
	// requireAPIKey stores the key owner in the context so handlers can
	// personalise responses without re-querying the key store.
	api := r.Group("/api")
	api.Use(requireAPIKey)

	api.GET("/profile", func(w http.ResponseWriter, r *http.Request) {
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"owner":    ownerFromCtx(r),
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

	// ── Start ─────────────────────────────────────────────────────────────────
	log.Info("listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
