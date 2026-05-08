// Package main demonstrates JWT authentication with MuxMaster using the
// built-in middleware.JWTAuth for token validation.
//
// MuxMaster validates tokens but intentionally does not issue them — token
// issuance is application-specific (which user-store, which TTL, which
// custom claims). This example issues compact HS256 JWTs by hand using
// only crypto/hmac + crypto/sha256, then delegates validation to
// middleware.JWTAuth on every protected route.
//
// Endpoints:
//
//	POST /auth/login    — validate credentials, issue a JWT
//	POST /auth/refresh  — exchange a valid token for a fresh one
//	GET  /health        — public (FastHandler, zero allocs)
//	GET  /api/me        — authenticated: return token claims
//	GET  /api/secret    — authenticated: protected resource
//
// Start:
//
//	go run .
//
// Smoke-test (requires jq):
//
//	TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
//	  -H 'Content-Type: application/json' \
//	  -d '{"username":"alice","password":"secret"}' | jq -r .token)
//
//	curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/me
//	curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/secret
//
//	# Refresh
//	NEW=$(curl -s -X POST http://localhost:8080/auth/refresh \
//	  -H "Authorization: Bearer $TOKEN" | jq -r .token)
//	curl -H "Authorization: Bearer $NEW" http://localhost:8080/api/me
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// ─── JWT issuance (token signing) ─────────────────────────────────────────────
//
// The standard library does not ship a JWT issuer; the application owns
// issuance because it owns the user store, TTL policy, and custom claims.
// Validation is delegated to middleware.JWTAuth (see /api group below).

// tokenPayload is the JWT payload. Standard claims (`sub`, `iat`, `exp`) are
// validated by middleware.JWTAuth; the non-standard `name` claim is read by
// handlers via the RawPayload field of mw.JWTClaims.
type tokenPayload struct {
	Sub  string `json:"sub"`  // subject — user ID
	Name string `json:"name"` // custom claim — username
	IAT  int64  `json:"iat"`  // issued at (Unix seconds)
	EXP  int64  `json:"exp"`  // expires at (Unix seconds)
}

// issueToken builds and signs an HS256 JWT for the given user with the given TTL.
func issueToken(userID, username string, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()
	return signToken(tokenPayload{
		Sub:  userID,
		Name: username,
		IAT:  now.Unix(),
		EXP:  now.Add(ttl).Unix(),
	}, secret)
}

// signToken encodes a payload as a compact JWT string (header.payload.signature).
func signToken(c tokenPayload, secret []byte) (string, error) {
	const rawHeader = `{"alg":"HS256","typ":"JWT"}`
	hdr := base64.RawURLEncoding.EncodeToString([]byte(rawHeader))

	payloadJSON, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	pld := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := hdr + "." + pld
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, signingInput)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig, nil
}

// ─── User store ───────────────────────────────────────────────────────────────

type user struct{ id, password string }

// users maps username → user. In production: query a database with hashed passwords.
var users = map[string]user{
	"alice": {id: "u1", password: "secret"},
	"bob":   {id: "u2", password: "hunter2"},
}

func findUser(username, password string) (user, bool) {
	u, ok := users[username]
	if !ok || u.password != password {
		return user{}, false
	}
	return u, true
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func errMsg(msg string) map[string]string { return map[string]string{"error": msg} }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// usernameFromClaims extracts the non-standard "name" claim from the validated
// token payload exposed by middleware.JWTAuth via JWTClaims.RawPayload.
func usernameFromClaims(c *mw.JWTClaims) string {
	var custom struct {
		Name string `json:"name"`
	}
	if c == nil || len(c.RawPayload) == 0 {
		return ""
	}
	_ = json.Unmarshal(c.RawPayload, &custom)
	return custom.Name
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	// JWT_SECRET should be a long random string set via environment variable.
	// The default is intentionally weak and only suitable for local development.
	secret := []byte(envOr("JWT_SECRET", "dev-secret-change-in-production"))
	const tokenTTL = 24 * time.Hour

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
	r.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		code := http.StatusInternalServerError
		var he mm.HTTPError
		if errors.As(err, &he) {
			code = he.StatusCode()
		}
		_ = mm.JSON(w, code, errMsg(err.Error()))
	}

	// ── Public routes ─────────────────────────────────────────────────────────

	// FastHandler: static route — zero allocations per request.
	r.GETFast("/health", func(w http.ResponseWriter, _ *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	// POST /auth/login — validate credentials and return a signed JWT.
	r.POSTE("/auth/login", func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return mm.Error(http.StatusBadRequest, errors.New("invalid JSON"))
		}
		u, ok := findUser(body.Username, body.Password)
		if !ok {
			return mm.Error(http.StatusUnauthorized, errors.New("invalid credentials"))
		}
		token, err := issueToken(u.id, body.Username, secret, tokenTTL)
		if err != nil {
			return err
		}
		return mm.JSON(w, http.StatusOK, map[string]string{"token": token})
	})

	// POST /auth/refresh — accept a valid token and return a new one with a
	// fresh expiry. The incoming token must validate via the same JWTAuth
	// middleware used by /api routes; we apply it inline to this single route
	// rather than to a /auth group so /auth/login stays public.
	refresh := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := mw.GetJWTClaims(r.Context())
		if !ok {
			_ = mm.JSON(w, http.StatusUnauthorized, errMsg("missing claims"))
			return
		}
		token, err := issueToken(claims.Subject, usernameFromClaims(claims), secret, tokenTTL)
		if err != nil {
			_ = mm.JSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		_ = mm.JSON(w, http.StatusOK, map[string]string{"token": token})
	})
	// Hardened JWT configuration — see SECURITY.md "Composite token-handling
	// stack" (CDX-S8-001). RequireExpiry rejects tokens without `exp`, which
	// would otherwise be valid forever once stolen (RFC 8725 §4.4).
	jwtAuth := mw.JWTAuth(mw.JWTOptions{
		Secret:        secret,
		Algorithms:    []string{"HS256"},
		RequireExpiry: true,
	})
	r.Handle(http.MethodPost, "/auth/refresh", jwtAuth(refresh))

	// ── Protected /api group ──────────────────────────────────────────────────
	//
	// middleware.JWTAuth validates the Bearer token before any handler runs:
	//   - signature verified with constant-time HMAC comparison
	//   - exp / nbf checked against time.Now() with optional ClockSkew
	//   - alg whitelist enforced (only HS256 here)
	//   - RFC 7515 §4.1.11 "crit" header rejected
	// On success, JWTClaims is injected into the request context.

	api := r.Group("/api")
	api.Use(jwtAuth)

	// GET /api/me — return the claims extracted from the token.
	api.GET("/me", func(w http.ResponseWriter, r *http.Request) {
		c, _ := mw.GetJWTClaims(r.Context())
		_ = mm.JSON(w, http.StatusOK, map[string]any{
			"user_id":  c.Subject,
			"username": usernameFromClaims(c),
			"issued":   c.IssuedAt.UTC().Format(time.RFC3339),
			"expires":  c.ExpiresAt.UTC().Format(time.RFC3339),
		})
	})

	// GET /api/secret — a resource only accessible with a valid token.
	api.GET("/secret", func(w http.ResponseWriter, r *http.Request) {
		c, _ := mw.GetJWTClaims(r.Context())
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"message":  "you have access, " + usernameFromClaims(c),
			"trace_id": mw.GetRequestID(r.Context()),
		})
	})

	// ── Server with hardened timeouts (SECURITY.md MM-2026-0024) ─────────────
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

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
