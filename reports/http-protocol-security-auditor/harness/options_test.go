package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestOPTIONSPreAuth confirms that automatic OPTIONS handling runs in
// mux.go:546-555 — BEFORE any user middleware. This means an unauthenticated
// client can enumerate the set of HTTP methods available at a path without
// passing through auth. Complements H-025 (TSR pre-auth).
func TestOPTIONSPreAuth(t *testing.T) {
	authCount := 0
	mux := muxmaster.New()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCount++
			http.Error(w, "forbidden", 403)
		})
	})
	mux.GET("/secret", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.POST("/secret", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/secret", nil)
	mux.ServeHTTP(rec, req)

	f, err := os.Create(filepath.Join(evidenceDir, "options-pre-auth.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "OPTIONS /secret (auth middleware should deny)\n")
	fmt.Fprintf(f, "  status=%d allow=%q body=%q auth_invocations=%d\n",
		rec.Code, rec.Header().Get("Allow"), rec.Body.String(), authCount)
	if authCount == 0 && rec.Header().Get("Allow") != "" {
		t.Logf("CONFIRMED: OPTIONS handler ran without auth (allow=%q)", rec.Header().Get("Allow"))
	}
}

// TestMethodNotAllowedPreAuth confirms 405 responses for known routes also
// run without user middleware.
func TestMethodNotAllowedPreAuth(t *testing.T) {
	authCount := 0
	mux := muxmaster.New()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCount++
			http.Error(w, "forbidden", 403)
		})
	})
	mux.GET("/secret", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/secret", nil) // method not registered
	mux.ServeHTTP(rec, req)

	f, err := os.Create(filepath.Join(evidenceDir, "method-not-allowed-pre-auth.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "POST /secret (only GET registered; auth middleware should deny)\n")
	fmt.Fprintf(f, "  status=%d allow=%q body=%q auth_invocations=%d\n",
		rec.Code, rec.Header().Get("Allow"), rec.Body.String(), authCount)
	if authCount == 0 && rec.Code == http.StatusMethodNotAllowed {
		t.Logf("CONFIRMED: 405 returned without running user middleware")
	}
}

// TestAllowHeaderLeakage confirms that the Allow header leaks the full
// list of registered methods for an existing path.
func TestAllowHeaderLeakage(t *testing.T) {
	mux := muxmaster.New()
	// Imagine many admin endpoints guarded by auth middleware.
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", 403)
		})
	})
	mux.GET("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.POST("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.DELETE("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.PUT("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/admin/config", nil)
	mux.ServeHTTP(rec, req)

	f, err := os.Create(filepath.Join(evidenceDir, "allow-header-leak.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "OPTIONS /admin/config → Allow: %q status=%d\n",
		rec.Header().Get("Allow"), rec.Code)

	if rec.Header().Get("Allow") != "" {
		t.Logf("CONFIRMED: OPTIONS discloses registered methods (%s) before auth",
			rec.Header().Get("Allow"))
	}
}
