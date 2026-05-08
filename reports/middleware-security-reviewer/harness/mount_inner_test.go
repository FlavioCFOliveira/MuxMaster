// Package harness_test — Gap A: Mount + auth + inner stdlib router trust boundary.
//
// H-G hypothesis: when Group.Mount() nests an external http.Handler,
// the group-level middleware registered via Group.Use() is NOT applied to
// requests forwarded to the mounted handler.  This breaks the auth trust
// boundary: an operator who puts BasicAuth (or JWTAuth) on a Group and then
// mounts a sub-router under that group gets no authentication on the inner routes.
//
// Threat model row: "auth bypass via Mount — group middleware not applied to
// mounted handler" (CWE-284 / CWE-306).
package harness_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// authFlag is set to true by the test auth middleware when it runs.
type authFlag struct{ ran bool }

func newTestAuthMiddleware(flag *authFlag) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flag.ran = true
			// Simulate a guard: reject if special header absent.
			if r.Header.Get("X-Require-Auth") == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TestSec_Mount_GroupMiddlewareAppliedToMounted verifies that middleware
// registered on a Group via Use() is applied to requests forwarded through
// Group.Mount() to the inner handler.
//
// EXPECTED BEHAVIOUR (secure): the group auth middleware runs and a request
// without credentials receives 401.
//
// ACTUAL BEHAVIOUR (finding): Group.Mount() calls m.mountAt() directly —
// it wraps the handler in a bare http.HandlerFunc and registers it via
// m.Handle("*", ...).  The group's middleware slice is NEVER applied to the
// mountH closure.  Requests reach the inner handler unauthenticated.
func TestSec_Mount_GroupMiddlewareAppliedToMounted(t *testing.T) {
	flag := &authFlag{}

	r := muxmaster.New()
	g := r.Group("/api")
	g.Use(newTestAuthMiddleware(flag))

	// Inner stdlib ServeMux with a sensitive route.
	inner := http.NewServeMux()
	inner.HandleFunc("/secret", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secret-data"))
	})

	// Mount inner under group — should be protected by auth.
	g.Mount("/internal", inner)

	// Request without credentials — should be blocked by auth middleware.
	req := httptest.NewRequest(http.MethodGet, "/api/internal/secret", nil)
	// Intentionally NOT setting X-Require-Auth.
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if !flag.ran {
		// FINDING: auth middleware never ran — complete bypass.
		t.Errorf("MSR-GAP-A: group auth middleware did NOT run for mounted handler — auth bypass confirmed (CWE-306)")
	}

	if rw.Code != http.StatusUnauthorized {
		// FINDING: inner route was served without auth.
		t.Errorf("MSR-GAP-A: expected 401 Unauthorized for unauthenticated request to mounted handler, got %d — trust boundary broken", rw.Code)
	}
}

// TestSec_Mount_GroupMiddlewareAppliedWithCredentials verifies the happy path:
// a request that satisfies the group auth middleware reaches the inner handler.
func TestSec_Mount_GroupMiddlewareAppliedWithCredentials(t *testing.T) {
	flag := &authFlag{}

	r := muxmaster.New()
	g := r.Group("/api")
	g.Use(newTestAuthMiddleware(flag))

	inner := http.NewServeMux()
	inner.HandleFunc("/public", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	g.Mount("/internal", inner)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/public", nil)
	req.Header.Set("X-Require-Auth", "yes")
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 for authenticated request to mounted handler, got %d", rw.Code)
	}
}

// TestSec_Mount_BasicAuthNotAppliedToInner is a concrete reproduction using the
// production BasicAuth middleware to confirm the same bypass.
//
// An attacker who knows (or guesses) a sub-path of the mounted handler can
// reach it without supplying HTTP Basic credentials.
func TestSec_Mount_BasicAuthNotAppliedToInner(t *testing.T) {
	r := muxmaster.New()
	g := r.Group("/admin")
	g.Use(middleware.BasicAuth("test", map[string]string{"alice": "s3cr3t"}))

	inner := http.NewServeMux()
	inner.HandleFunc("/dashboard", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("admin-dashboard"))
	})
	g.Mount("/panel", inner)

	// No Authorization header — should be 401.
	req := httptest.NewRequest(http.MethodGet, "/admin/panel/dashboard", nil)
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Errorf("MSR-GAP-A (BasicAuth): expected 401 for unauthenticated request to /admin/panel/dashboard, got %d — BasicAuth bypass via Mount", rw.Code)
	}
}

// TestSec_Mount_TopLevelUseAppliedToMounted verifies that mux-level Use()
// middleware (not group-level) IS applied to mounted handlers, because those
// middlewares are baked in at m.Handle() registration time.
func TestSec_Mount_TopLevelUseAppliedToMounted(t *testing.T) {
	flag := &authFlag{}

	r := muxmaster.New()
	r.Use(newTestAuthMiddleware(flag))

	inner := http.NewServeMux()
	inner.HandleFunc("/route", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Mount("/service", inner)

	req := httptest.NewRequest(http.MethodGet, "/service/route", nil)
	// No X-Require-Auth — top-level middleware should block.
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	if !flag.ran {
		t.Errorf("top-level Use() middleware unexpectedly did not run for mounted handler")
	}
	if rw.Code != http.StatusUnauthorized {
		t.Errorf("top-level Use() middleware should block unauthenticated request, got %d", rw.Code)
	}
}

// TestSec_Mount_InnerServeMuxRoutingAfterStrip verifies that the path stripping
// in mountAt correctly forwards sub-paths to the inner ServeMux, and that the
// inner mux can match its own routes independently of the outer prefix.
func TestSec_Mount_InnerServeMuxRoutingAfterStrip(t *testing.T) {
	r := muxmaster.New()

	inner := http.NewServeMux()
	inner.HandleFunc("/a", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("route-a"))
	})
	inner.HandleFunc("/b", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("route-b"))
	})
	r.Mount("/v1", inner)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/v1/a", "route-a"},
		{"/v1/b", "route-b"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rw := httptest.NewRecorder()
		r.ServeHTTP(rw, req)
		if rw.Body.String() != tc.want {
			t.Errorf("path %s: expected body %q, got %q", tc.path, tc.want, rw.Body.String())
		}
	}
}
