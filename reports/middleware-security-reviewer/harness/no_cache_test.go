// Harness — no_cache.go.
//
// Threats covered:
//   - All cache-inhibiting headers present (Cache-Control, Pragma, Expires).
//   - Headers set before handler runs (handler sees them in outgoing list).
//   - Handler can override if it needs to.
package harness

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-NC-001 — All expected headers present.
// -----------------------------------------------------------------------------

func TestSec_NoCache_HeadersPresent(t *testing.T) {
	mw := middleware.NoCache()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	cases := map[string]string{
		"Cache-Control": "no-store",
		"Pragma":        "no-cache",
		"Expires":       "0",
	}
	for header, wantSub := range cases {
		if got := rec.Header().Get(header); !strings.Contains(got, wantSub) {
			t.Errorf("no_cache: %s=%q, want to contain %q", header, got, wantSub)
		}
	}
	// Bonus: verify full Cache-Control directive set.
	cc := rec.Header().Get("Cache-Control")
	for _, d := range []string{"no-store", "no-cache", "must-revalidate"} {
		if !strings.Contains(cc, d) {
			t.Errorf("no_cache: Cache-Control missing directive %q; got %q", d, cc)
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-NC-002 — Handler can override.
// -----------------------------------------------------------------------------

func TestSec_NoCache_HandlerCanOverride(t *testing.T) {
	mw := middleware.NoCache()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("no_cache: handler override Cache-Control=%q, want public, max-age=60", got)
	}
}
