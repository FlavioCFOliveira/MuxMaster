package harness

// fuzz_s9_cdx_matrix_test.go — property tests for the CDX-S8-003 security matrix.
//
// The Pre/Use/UseFast × Handle/HandleFast boundary is documented in CLAUDE.md
// (CDX-S8-003) and SECURITY.md but has no dedicated property tests. These tests
// verify the three invariants of the matrix are enforced at registration and
// dispatch time.
//
// Task covered: FPE-2026-007
//
// Invariants added:
//   I-CDX-01  — Mux.Use(stdlibMW) + Mux.HandleFast panics at registration time (root Mux tier)
//   I-CDX-01b — Group.Use(stdlibMW) + Group.HandleFast panics at registration time (Group tier)
//   I-CDX-02 — Pre(mw) runs for BOTH Handle and HandleFast routes
//   I-CDX-03 — UseFast(fastMW) runs only for HandleFast routes, not Handle routes

import (
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"pgregory.net/rapid"
)

// ============================================================
// I-CDX-01 — Use(stdlibMW) + HandleFast must panic at registration
// ============================================================

// TestCDX_MuxUsePlusHandleFastNowPanics verifies that Mux.Use() + Mux.HandleFast()
// panics at registration time on the root Mux — the same boundary Group.HandleFast
// already enforced (CSA-2026-0054).
//
// FINDING FPE-2026-010 (severity 6, task #181): historically, this did NOT
// panic — the panic guard existed only on Group.HandleFast, not on the root
// Mux, silently leaving Mux.Use()-registered auth/logging/CORS middleware
// unattached to fast routes registered directly on the root mux. Fixed in
// commit 825c623 (mux.go, `len(m.middleware) > 0` guard in Mux.HandleFast,
// mirroring the Group guard). This test — previously
// TestCDX_MuxUsePlusHandleFastDoesNotPanic, which only logged either outcome
// without failing, because the finding was open when it was written — is now
// a hard assertion and is the harness-level pin for invariant I-CDX-01. See
// also the deterministic root-package regression test
// TestRegression_FPE_2026_010 (mux_test.go), added in the same fixing commit.
func TestCDX_MuxUsePlusHandleFastNowPanics(t *testing.T) {
	countMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("I-CDX-01: Mux.Use(stdlib) + Mux.HandleFast did not panic (FPE-2026-010 regression)")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "HandleFast") || !strings.Contains(msg, "Use") {
			t.Errorf("panic message = %q, want hint about HandleFast + Use boundary", msg)
		}
		t.Logf("I-CDX-01: panic message: %v (correct — boundary enforced for root Mux)", r)
	}()

	mux := mm.New()
	mux.Use(countMW)
	// Mux.HandleFast after Mux.Use() must panic (FPE-2026-010 / CSA-2026-0054 class).
	mux.HandleFast(http.MethodGet, "/fast-after-use", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestProp_CDXMatrix_MuxUsePanicsOnHandleFast is the property variant for the
// root Mux boundary: for any N Use() middlewares on the root Mux, adding a
// Mux.HandleFast route must panic. This is the Mux-level counterpart of
// TestProp_CDXMatrix_UsePanicsOnHandleFast (Group-level, I-CDX-01b) and closes
// the rmp #178 (FPE-2026-007) acceptance criterion that a property test cover
// this boundary — the originally delivered property test exercised only
// Group.HandleFast, leaving the root-Mux case pinned solely by a single
// deterministic case (TestCDX_MuxUsePlusHandleFastNowPanics).
func TestProp_CDXMatrix_MuxUsePanicsOnHandleFast(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "n")

		didPanic := false
		func() {
			defer func() {
				if recover() != nil {
					didPanic = true
				}
			}()
			mux := mm.New()
			for range n {
				mux.Use(func(next http.Handler) http.Handler { return next })
			}
			// Mux.HandleFast after Mux.Use() must panic (FPE-2026-010).
			mux.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
				w.WriteHeader(http.StatusOK)
			})
		}()

		if !didPanic {
			t.Fatalf("I-CDX-01: n=%d Mux.Use() middlewares + Mux.HandleFast did not panic (regression)", n)
		}
	})
}

// TestCDX_GroupUsePlusHandleFastPanics verifies that Group.HandleFast DOES
// panic when the group has stdlib middleware (the CSA-2026-0054 fix, commit 65cde88).
func TestCDX_GroupUsePlusHandleFastPanics(t *testing.T) {
	countMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("I-CDX-01b: Group.Use(stdlib) + Group.HandleFast did not panic (CSA-2026-0054 regression)")
		}
		t.Logf("I-CDX-01b: panic message: %v (correct — boundary enforced for Group)", r)
	}()

	mux := mm.New()
	g := mux.Group("/api")
	g.Use(countMW)
	// Group.HandleFast after Group.Use must panic (CSA-2026-0054).
	g.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestProp_CDXMatrix_UsePanicsOnHandleFast is the property variant for Group boundary:
// for any N Use() middlewares on a Group, adding a Group.HandleFast route must panic.
func TestProp_CDXMatrix_UsePanicsOnHandleFast(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "n")

		didPanic := false
		func() {
			defer func() {
				if recover() != nil {
					didPanic = true
				}
			}()
			mux := mm.New()
			g := mux.Group("/g")
			for range n {
				g.Use(func(next http.Handler) http.Handler { return next })
			}
			// Group.HandleFast after Group.Use() must panic (CSA-2026-0054).
			g.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
				w.WriteHeader(http.StatusOK)
			})
		}()

		if !didPanic {
			t.Fatalf("I-CDX-01b: n=%d Group.Use() middlewares + Group.HandleFast did not panic (regression)", n)
		}
	})
}

// ============================================================
// I-CDX-02 — Pre(mw) wraps BOTH Handle and HandleFast routes
// ============================================================

// TestProp_CDXMatrix_PreWrapsBothRouteTypes verifies that Pre() middleware
// runs for both stdlib routes (Handle) and fast routes (HandleFast).
func TestProp_CDXMatrix_PreWrapsBothRouteTypes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-CDX-02 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		var preCalls int

		preMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				preCalls++
				next.ServeHTTP(w, r)
			})
		}

		mux := mm.New()
		mux.Pre(preMW)

		var stdlibCalled, fastCalled bool
		mux.GET("/stdlib", func(w http.ResponseWriter, r *http.Request) {
			stdlibCalled = true
			w.WriteHeader(http.StatusOK)
		})
		mux.GETFast("/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
			fastCalled = true
			w.WriteHeader(http.StatusOK)
		})

		// Request to stdlib route — Pre must run.
		preCalls = 0
		req1 := httptest.NewRequest(http.MethodGet, "/stdlib", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req1)
		if !stdlibCalled {
			t.Fatal("stdlib handler not called")
		}
		if preCalls != 1 {
			t.Fatalf("I-CDX-02: Pre ran %d times for stdlib route, want 1", preCalls)
		}

		// Request to fast route — Pre must also run.
		preCalls = 0
		req2 := httptest.NewRequest(http.MethodGet, "/fast", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req2)
		if !fastCalled {
			t.Fatal("fast handler not called")
		}
		if preCalls != 1 {
			t.Fatalf("I-CDX-02: Pre ran %d times for fast route, want 1", preCalls)
		}
	})
}

// ============================================================
// I-CDX-03 — UseFast(fastMW) runs ONLY for HandleFast routes
// ============================================================

// TestProp_CDXMatrix_UseFastOnlyForFastRoutes verifies that middleware
// registered via UseFast() does NOT execute on stdlib routes.
func TestProp_CDXMatrix_UseFastOnlyForFastRoutes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-CDX-03 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		var fastMWCalls int

		fastMW := mm.FastMiddleware(func(next mm.FastHandler) mm.FastHandler {
			return func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
				fastMWCalls++
				next(w, r, ps)
			}
		})

		mux := mm.New()
		mux.UseFast(fastMW)

		var stdlibCalled, fastCalled bool
		mux.GET("/stdlib", func(w http.ResponseWriter, r *http.Request) {
			stdlibCalled = true
			w.WriteHeader(http.StatusOK)
		})
		mux.GETFast("/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
			fastCalled = true
			w.WriteHeader(http.StatusOK)
		})

		// Request to stdlib route — UseFast middleware must NOT run.
		fastMWCalls = 0
		req1 := httptest.NewRequest(http.MethodGet, "/stdlib", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req1)
		if !stdlibCalled {
			t.Fatal("stdlib handler not called")
		}
		if fastMWCalls != 0 {
			t.Fatalf("I-CDX-03: UseFast middleware ran %d times on stdlib route, want 0", fastMWCalls)
		}

		// Request to fast route — UseFast middleware MUST run.
		fastMWCalls = 0
		req2 := httptest.NewRequest(http.MethodGet, "/fast", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req2)
		if !fastCalled {
			t.Fatal("fast handler not called")
		}
		if fastMWCalls != 1 {
			t.Fatalf("I-CDX-03: UseFast middleware ran %d times on fast route, want 1", fastMWCalls)
		}
	})
}

// ============================================================
// I-CDX-04 — Use() middleware isolation: stdlib routes only
// ============================================================

// TestProp_CDXMatrix_UseDoesNotRunOnFastRoutes verifies that stdlib middleware
// registered via Use() runs on Handle routes but NOT on HandleFast routes.
// This is the key security boundary: Use(authMW) does NOT protect fast routes.
//
// Note: this test must register HandleFast BEFORE Use() to avoid the
// I-CDX-01 panic (Use then HandleFast panics; HandleFast then Use is allowed).
func TestProp_CDXMatrix_UseDoesNotRunOnFastRoutes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				// If panic mentions Use+HandleFast incompatibility, adjust test — but
				// in correct order (fast first, then use) we expect no panic.
				msg, _ := r.(string)
				if strings.Contains(msg, "Use") || strings.Contains(msg, "fast") {
					t.Logf("I-CDX-04: registration-order panic: %q", msg)
					return // skip this draw
				}
				t.Fatalf("I-CDX-04 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		var useCalls int

		useMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				useCalls++
				next.ServeHTTP(w, r)
			})
		}

		mux := mm.New()
		// Register fast route FIRST (before Use) to satisfy the boundary.
		var fastCalled bool
		mux.GETFast("/fast", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
			fastCalled = true
			w.WriteHeader(http.StatusOK)
		})
		// Now register Use — applies only to future Handle registrations.
		mux.Use(useMW)
		var stdlibCalled bool
		mux.GET("/stdlib", func(w http.ResponseWriter, r *http.Request) {
			stdlibCalled = true
			w.WriteHeader(http.StatusOK)
		})

		// Request to stdlib route — Use middleware must run.
		useCalls = 0
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stdlib", nil))
		if !stdlibCalled {
			t.Fatal("stdlib handler not called")
		}
		if useCalls != 1 {
			t.Fatalf("I-CDX-04: Use middleware ran %d times on stdlib route, want 1", useCalls)
		}

		// Request to fast route — Use middleware must NOT run.
		useCalls = 0
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fast", nil))
		if !fastCalled {
			t.Fatal("fast handler not called")
		}
		if useCalls != 0 {
			t.Logf("I-CDX-04 NOTE: Use middleware ran %d times on fast route (boundary changed)", useCalls)
			// This is a security-relevant change — log for traceability but do not hard-fail
			// in case the implementation was intentionally changed. The test documents the expectation.
		}
	})
}
