package repro_test

// FPE-2026-010 — Mux.Use(stdlibMW) + Mux.HandleFast silently bypasses auth
//
// Severity: 6 (Medium-High)
// Task: rmp #181
// Found by: TestCDX_MuxUsePlusHandleFastDoesNotPanic (fuzz_s9_cdx_matrix_test.go)
//
// Description:
//   The Use() docstring in mux.go states:
//     "registering a fast route after Use(authMiddleware) panics at HandleFast call time (CSA-2026-0054)"
//
//   This claim is FALSE for root Mux.HandleFast. The panic guard was added only to
//   Group.HandleFast (commit 65cde88), not to Mux.HandleFast. As a result:
//
//   1. mux.Use(authMW)                   ← stdlib auth gate registered
//   2. mux.HandleFast("GET", "/fast", h) ← DOES NOT PANIC
//   3. GET /fast is served by h with NO auth middleware applied
//
//   An operator who reads the docstring and trusts the documented panic-at-registration
//   protection is silently left with unauthenticated fast routes on the root mux.
//
// Contrast with Group behaviour (correctly panics):
//   g.Use(authMW)
//   g.HandleFast("GET", "/fast", h) ← PANICS (correct — Group.HandleFast guard)
//
// Impact:
//   Any auth, logging, request-ID, or CORS middleware registered via Mux.Use()
//   does not protect HandleFast routes on the root mux. This is a silent security
//   bypass if the operator relies on the (incorrect) documented panic guard.
//
// Recommended fix:
//   In Mux.HandleFast, add the same guard as Group.HandleFast (group.go:68):
//
//     if len(m.middleware) > 0 {
//         panic("muxmaster: HandleFast route registered on a Mux with stdlib middleware (Use) — " +
//               "stdlib middleware does not run on the FastHandler path. Use UseFast() for fast routes, " +
//               "or Handle() for routes that must run through the stdlib chain.")
//     }
//
//   Update the Use() docstring to clarify the scope of the panic guard.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestRepro_FPE010_MuxUsePlusHandleFastNoPanic documents that Mux.Use + Mux.HandleFast
// does NOT panic (contrary to docstring), exposing the auth bypass.
func TestRepro_FPE010_MuxUsePlusHandleFastNoPanic(t *testing.T) {
	var authApplied bool
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authApplied = true
			next.ServeHTTP(w, r)
		})
	}

	didPanic := false
	var mux *mm.Mux
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		mux = mm.New()
		mux.Use(authMW)
		mux.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
			w.WriteHeader(http.StatusOK)
		})
	}()

	if didPanic {
		t.Logf("FPE-2026-010 FIXED: Mux.Use + Mux.HandleFast now panics at registration time")
		return
	}

	// No panic — the auth bypass exists. Demonstrate it:
	t.Logf("FPE-2026-010 OPEN: registration did not panic")

	authApplied = false
	req := httptest.NewRequest(http.MethodGet, "/fast", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("fast route not reachable: got %d", rec.Code)
	}
	if authApplied {
		t.Logf("FPE-2026-010 RESOLVED at dispatch: auth middleware ran (unexpected but safe)")
	} else {
		// Confirmed bypass: auth middleware never ran.
		t.Logf("FPE-2026-010 CONFIRMED: fast route served 200 without authMW running — auth bypassed")
	}
}

// TestRepro_FPE010_GroupHandleFastPanicsCorrectly verifies the Group case IS fixed.
func TestRepro_FPE010_GroupHandleFastPanicsCorrectly(t *testing.T) {
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Group.Use + Group.HandleFast did not panic — CSA-2026-0054 regression")
		}
		t.Logf("Group.Use + Group.HandleFast correctly panics: %v", r)
	}()

	mux := mm.New()
	g := mux.Group("/api")
	g.Use(authMW)
	g.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
}
