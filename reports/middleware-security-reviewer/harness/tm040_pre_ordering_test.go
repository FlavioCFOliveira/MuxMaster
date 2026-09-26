// Package harness_test — TM-2026-040: Pre(gate, CleanPath()) vs
// Pre(CleanPath(), gate) ordering (rmp #284).
//
// Confirms the ordering hazard identified in
// reports/overview/2026-09-26-closed-task-audit.md (#130 / TM-2026-040): a
// path-inspecting Pre() middleware ("gate") registered BEFORE CleanPath() in
// the Pre chain sees the RAW, unnormalised path and can be bypassed with
// traversal / percent-encoded-traversal / duplicate-slash sequences that
// later clean to a path the gate would have rejected. Registering
// CleanPath() FIRST closes the bypass because dispatch — and every later
// Pre middleware, including the gate — always sees the already-cleaned
// path.
//
// Also documents (TestTM040_UseOrdering_NoHandlerBypass) that the ROUTE
// BYPASS half of the hazard does NOT exist when gate/CleanPath are
// registered via Mux.Use() instead of Mux.Pre(): Use-wrapped middleware is
// baked into the handler for an ALREADY-MATCHED route at Handle()
// registration time (mux.go wrapMiddleware; see CLAUDE.md "Middleware
// applied at registration, not per request") and only runs once the
// radix-tree lookup — performed on the untouched, un-cleaned r.URL.Path —
// has already decided the request does NOT match /admin literally. None of
// the three payloads is byte-for-byte equal to "/admin", so tree lookup
// never selects the real /admin handler for them, in EITHER gate/CleanPath
// order: the protected handler's body ("admin-secret") is never reached.
//
// This is NOT the same as "ordering is irrelevant", however: MuxMaster's
// global Use() middleware chain also wraps the shared NotFound handler
// (mux.go lazyNotFound, invoked from dispatch when tree lookup finds no
// route — mux.go:1619), so gate and CleanPath still both execute on the
// unmatched-route path, just downstream of routing rather than upstream of
// it. Their relative order therefore still changes the RESPONSE STATUS for
// these requests — gate-then-CleanPath sees the raw path and returns 404
// (gate's literal "/admin" prefix check misses on the raw traversal /
// duplicate-slash form, so the request falls through to NotFound
// unmodified); CleanPath-then-gate normalises the path first, so gate then
// recognises it as "/admin" and returns 403 instead of letting it reach
// NotFound. Neither order EVER exposes the real /admin handler's response
// body — the hazard is specific to Pre(), where gate/CleanPath run BEFORE
// the routing decision is made.
package harness_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// tm040Gate rejects any request whose (at-the-time-it-runs) URL.Path lies
// under /admin, matched with a naive literal prefix check — precisely the
// kind of pre-routing authorization gate SECURITY.md's "Pre vs Use security
// boundary" recommends registering via Pre() (auth gates, IP allow lists,
// WAF-style rules).
func tm040Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tm040NewMux() *muxmaster.Mux {
	m := muxmaster.New()
	m.GET("/pub", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("public"))
	})
	m.GET("/admin", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("admin-secret"))
	})
	return m
}

// tm040Payloads are three independent encodings of the same traversal idea
// (dot-dot, percent-encoded dot-dot, duplicate leading slash) — exactly the
// set named in rmp #284's task description.
var tm040Payloads = []string{
	"/pub/../admin",
	"/pub/%2e%2e/admin",
	"//admin",
}

// TestTM040_Pre_GateBeforeCleanPath_Bypass demonstrates the CONFIRMED
// bypass: Pre(gate, CleanPath()) lets every payload reach the /admin
// handler. gate runs first and inspects the RAW path (none of the three
// payloads has a literal "/admin" prefix), then CleanPath normalises the
// path and dispatch matches /admin on the CLEANED value.
func TestTM040_Pre_GateBeforeCleanPath_Bypass(t *testing.T) {
	m := tm040NewMux()
	m.Pre(tm040Gate, middleware.CleanPath())

	for _, target := range tm040Payloads {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || rec.Body.String() != "admin-secret" {
				t.Fatalf("TM-2026-040 wrong-order Pre(gate, CleanPath()): %q got status=%d body=%q, want 200 admin-secret (bypass)",
					target, rec.Code, rec.Body.String())
			}
			t.Logf("TM-2026-040 CONFIRMED bypass: Pre(gate, CleanPath()) — %q reached /admin (status=%d)", target, rec.Code)
		})
	}
}

// TestTM040_Pre_CleanPathBeforeGate_NoBypass confirms the fix: registering
// CleanPath() FIRST in the Pre chain makes gate see the already-normalised
// path, so every payload is correctly rejected before it can reach /admin.
func TestTM040_Pre_CleanPathBeforeGate_NoBypass(t *testing.T) {
	m := tm040NewMux()
	m.Pre(middleware.CleanPath(), tm040Gate)

	for _, target := range tm040Payloads {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("TM-2026-040 correct-order Pre(CleanPath(), gate): %q got status=%d body=%q, want 403 (no bypass)",
					target, rec.Code, rec.Body.String())
			}
			if rec.Body.String() == "admin-secret" {
				t.Errorf("TM-2026-040 correct-order Pre(CleanPath(), gate): %q reached /admin handler", target)
			}
		})
	}
}

// TestTM040_UseOrdering_NoHandlerBypass documents (see package doc comment
// above for the full reasoning) that Use()-registered gate/CleanPath
// ordering cannot reproduce the Pre() HANDLER bypass: routing already
// happened, on the raw path, before either middleware runs, so the real
// /admin handler's body is never observed for any payload in either order.
// The two orders DO differ in the status code returned for the (always
// unmatched) request — 404 for gate-then-CleanPath, 403 for
// CleanPath-then-gate — because Use() middleware also wraps the shared
// NotFound handler; that difference is cosmetic, not a security bypass.
func TestTM040_UseOrdering_NoHandlerBypass(t *testing.T) {
	cases := []struct {
		name       string
		order      [2]func(http.Handler) http.Handler
		wantStatus int
	}{
		// gate runs on the RAW path (no literal "/admin" prefix) and lets
		// the request fall through unmodified to the shared NotFound
		// handler: 404.
		{"gate_then_CleanPath", [2]func(http.Handler) http.Handler{tm040Gate, middleware.CleanPath()}, http.StatusNotFound},
		// CleanPath normalises the path first; gate then recognises the
		// cleaned form as "/admin" and rejects it before NotFound ever
		// runs: 403. Routing itself already failed on the raw path in
		// both cases — this is a downstream status-code difference, not a
		// handler bypass.
		{"CleanPath_then_gate", [2]func(http.Handler) http.Handler{middleware.CleanPath(), tm040Gate}, http.StatusForbidden},
	}
	for _, tc := range cases {
		for _, target := range tm040Payloads {
			t.Run(fmt.Sprintf("%s/%s", tc.name, target), func(t *testing.T) {
				m := tm040NewMux()
				m.Use(tc.order[0], tc.order[1])
				req := httptest.NewRequest(http.MethodGet, target, nil)
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != tc.wantStatus {
					t.Errorf("Use() order %s, %q: got status=%d, want %d", tc.name, target, rec.Code, tc.wantStatus)
				}
				if rec.Body.String() == "admin-secret" {
					t.Errorf("Use() order %s, %q: reached /admin handler — unexpected bypass via Use() (TM-2026-040)",
						tc.name, target)
				}
			})
		}
	}
}
