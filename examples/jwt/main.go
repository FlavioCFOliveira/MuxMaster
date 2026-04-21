// Package main demonstrates JWT authentication with MuxMaster using only the
// Go standard library (no external JWT package).
//
// The token format is a standard compact JWS: header.payload.signature, where
// the signature is HMAC-SHA256. Constant-time comparison (hmac.Equal) prevents
// timing attacks on the signature.
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
	"strings"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─── JWT ─────────────────────────────────────────────────────────────────────

// Claims is the JWT payload. Only HS256 is supported.
type Claims struct {
	Sub  string `json:"sub"`  // user ID
	Name string `json:"name"` // username
	IAT  int64  `json:"iat"`  // issued at (Unix seconds)
	EXP  int64  `json:"exp"`  // expires at (Unix seconds)
}

var (
	errTokenInvalid = errors.New("token invalid")
	errTokenExpired = errors.New("token expired")
)

// issueToken builds and signs a JWT for the given user with the given TTL.
func issueToken(userID, username string, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()
	return signToken(Claims{
		Sub:  userID,
		Name: username,
		IAT:  now.Unix(),
		EXP:  now.Add(ttl).Unix(),
	}, secret)
}

// signToken encodes Claims as a compact JWT string (header.payload.signature).
func signToken(c Claims, secret []byte) (string, error) {
	// The header is constant for HS256 — no need to encode it dynamically.
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

// validateToken parses a compact JWT string, verifies its HMAC-SHA256 signature
// with constant-time comparison, and checks the expiry claim.
func validateToken(token string, secret []byte) (*Claims, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, errTokenInvalid
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, signingInput)
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	// hmac.Equal uses constant-time comparison — prevents timing attacks.
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return nil, errTokenInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errTokenInvalid
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errTokenInvalid
	}
	if time.Now().Unix() > c.EXP {
		return nil, errTokenExpired
	}
	return &c, nil
}

// ─── JWT middleware ───────────────────────────────────────────────────────────

type claimsKey struct{}

// requireJWT validates the Bearer token in the Authorization header and stores
// the parsed Claims in the request context. Applies to every route in /api.
func requireJWT(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("Authorization")
			if !strings.HasPrefix(raw, "Bearer ") {
				_ = mm.JSON(w, http.StatusUnauthorized, errMsg("missing Bearer token"))
				return
			}
			claims, err := validateToken(strings.TrimPrefix(raw, "Bearer "), secret)
			switch {
			case errors.Is(err, errTokenExpired):
				_ = mm.JSON(w, http.StatusUnauthorized, errMsg("token expired"))
			case err != nil:
				_ = mm.JSON(w, http.StatusUnauthorized, errMsg("token invalid"))
			default:
				ctx := context.WithValue(r.Context(), claimsKey{}, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
			}
		})
	}
}

// claimsFromCtx returns the Claims injected by requireJWT, or nil.
func claimsFromCtx(r *http.Request) *Claims {
	c, _ := r.Context().Value(claimsKey{}).(*Claims)
	return c
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
	// fresh expiry, without requiring the user to log in again.
	r.POSTE("/auth/refresh", func(w http.ResponseWriter, r *http.Request) error {
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			return mm.Error(http.StatusUnauthorized, errors.New("missing Bearer token"))
		}
		claims, err := validateToken(strings.TrimPrefix(raw, "Bearer "), secret)
		if err != nil {
			return mm.Error(http.StatusUnauthorized, err)
		}
		token, err := issueToken(claims.Sub, claims.Name, secret, tokenTTL)
		if err != nil {
			return err
		}
		return mm.JSON(w, http.StatusOK, map[string]string{"token": token})
	})

	// ── Protected /api group ──────────────────────────────────────────────────
	//
	// requireJWT runs before every handler in this group. If the token is
	// missing, invalid, or expired, the handler never runs.

	api := r.Group("/api")
	api.Use(requireJWT(secret))

	// GET /api/me — return the claims extracted from the token.
	api.GET("/me", func(w http.ResponseWriter, r *http.Request) {
		c := claimsFromCtx(r)
		_ = mm.JSON(w, http.StatusOK, map[string]any{
			"user_id":  c.Sub,
			"username": c.Name,
			"issued":   time.Unix(c.IAT, 0).UTC().Format(time.RFC3339),
			"expires":  time.Unix(c.EXP, 0).UTC().Format(time.RFC3339),
		})
	})

	// GET /api/secret — a resource only accessible with a valid token.
	api.GET("/secret", func(w http.ResponseWriter, r *http.Request) {
		c := claimsFromCtx(r)
		_ = mm.JSON(w, http.StatusOK, map[string]string{
			"message":  "you have access, " + c.Name,
			"trace_id": mw.GetRequestID(r.Context()),
		})
	})

	// ── Start ─────────────────────────────────────────────────────────────────
	log.Info("listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
