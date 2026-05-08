// Package main demonstrates the hardened OAuth2 introspection stack.
//
// This example pairs OAuth2Introspect with the four invariants required by
// SECURITY.md "Composite token-handling stack" (CDX-S8-001):
//
//  1. HTTPS-only introspection endpoint (OAuth2Introspect rejects http://
//     unless AllowInsecureEndpoint is explicitly set — testing only).
//  2. Per-IP throttle with a bounded table (DefaultThrottlePerIPMaxTableSize)
//     so an attacker cannot exhaust memory by churning unique IPs.
//  3. RealIP configured with explicit trusted-proxy CIDRs so the rightmost
//     non-trusted XFF entry is selected (defends against attacker-injected
//     leftmost values, MSR-2026-0065).
//  4. Recoverer + PanicHandler to keep the process alive on a misbehaving
//     handler.
//
// Run: go run . (talks to a fake https introspection server in this demo).
package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Mock IDP — in production this is your auth server. We use TLS because
	// OAuth2Introspect rejects plaintext endpoints (CDX-S8-001 invariant 1).
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		token := r.FormValue("token")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": token == "good-token",
			"sub":    "user-1",
			"exp":    time.Now().Add(60 * time.Second).Unix(),
		})
	}))
	defer idp.Close()

	// Trusted reverse-proxy CIDR — MUST be the actual edge proxy network.
	trustedProxy, err := netip.ParsePrefix("127.0.0.0/8")
	if err != nil {
		log.Error("invalid CIDR", "err", err)
		os.Exit(1)
	}

	r := mm.New()

	// ── Invariant 3: rightmost-walk XFF (RealIP). Pre wraps every route. ─
	r.Pre(mw.RealIP(&trustedProxy))

	// ── Invariant 4: bounded per-IP throttle. ─
	r.Use(mw.ThrottlePerIP(50, 2*time.Second, nil))

	// ── Invariant 1: HTTPS-only introspection endpoint. ─
	r.Use(mw.OAuth2Introspect(mw.OAuth2Options{
		Endpoint:   idp.URL, // https:// from httptest.NewTLSServer
		HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		CacheTTL:   30 * time.Second,
	}))

	r.GET("/api/me", func(w http.ResponseWriter, req *http.Request) {
		claims, ok := mw.GetOAuth2Claims(req.Context())
		if !ok {
			http.Error(w, "missing claims", http.StatusInternalServerError)
			return
		}
		_ = mm.JSON(w, http.StatusOK, map[string]any{
			"subject": claims.Subject,
			"active":  claims.Active,
		})
	})

	srv := &http.Server{Addr: ":8080", Handler: r}
	log.Info("oauth2 hardened stack listening — see SECURITY.md CDX-S8-001",
		"addr", srv.Addr,
		"try", fmt.Sprintf("curl -H 'Authorization: Bearer good-token' http://localhost:8080/api/me"))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server error", "err", err)
	}
}
