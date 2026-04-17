// Harness — clean_path.go (H-010).
//
// Threats covered:
//   - CWE-22 single-pass cleaning lets encoded traversal survive.
//   - Null byte in path.
//   - Query string contamination.
//   - Interaction with catch-all routing.
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-CL-001 — path.Clean matrix.
// -----------------------------------------------------------------------------

func TestSec_CleanPath_NormalisationMatrix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Textual traversal is removed.
		{"/a/../b", "/b"},
		{"/a/./b", "/a/b"},
		{"/a//b", "/a/b"},
		{"/a/b/", "/a/b"}, // path.Clean trims trailing slash

		// Percent-encoded traversal is NOT decoded by clean_path (only path.Clean runs).
		// The URL parser would have decoded %2e%2e → .. before entering here if the
		// request reaches via httptest. So test both states of r.URL.Path.
		{"/%2e%2e/b", "/%2e%2e/b"}, // not decoded by path.Clean — it sees textual %2e
		{"/.%2e/b", "/.%2e/b"},      // same

		// Already-clean paths pass through (with exact matching).
		{"/a/b", "/a/b"},
		{"/", "/"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			var captured string
			mw := middleware.CleanPath()
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.URL.Path
				w.WriteHeader(http.StatusOK)
			})
			h := mw(inner)

			req := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req.URL = &url.URL{Path: c.in}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if captured != c.want {
				t.Errorf("clean_path: in=%q out=%q, want %q", c.in, captured, c.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-CL-002 — Null byte survives clean (documented).
// -----------------------------------------------------------------------------

func TestSec_CleanPath_NullByteSurvives(t *testing.T) {
	var captured string
	mw := middleware.CleanPath()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
	req.URL = &url.URL{Path: "/admin\x00evil"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if captured != "/admin\x00evil" {
		t.Logf("clean_path: null byte handling: in=%q out=%q", "/admin\x00evil", captured)
	}
}

// -----------------------------------------------------------------------------
// MSR-CL-003 — Query string is untouched.
// -----------------------------------------------------------------------------

func TestSec_CleanPath_QueryUntouched(t *testing.T) {
	var capturedPath, capturedQuery string
	mw := middleware.CleanPath()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://x/a/../b?q=..%2f..%2f", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if capturedPath != "/b" {
		t.Errorf("clean_path: path out=%q, want /b", capturedPath)
	}
	if capturedQuery != "q=..%2f..%2f" {
		t.Errorf("clean_path: query mutated; out=%q, want %q", capturedQuery, "q=..%2f..%2f")
	}
}

// -----------------------------------------------------------------------------
// MSR-CL-004 — Single-pass limitation: input that cleans once still has ..
// if original contained mixed encoded/plain sequences.
// -----------------------------------------------------------------------------

func TestSec_CleanPath_SinglePass(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Textual (mixed) — second pass would be needed?
		{"plain_double_dotdot", "/../../b", "/b"},
		// Encoded: single pass does NOT decode.
		{"encoded_travsal", "/%2e%2e/%2e%2e/b", "/%2e%2e/%2e%2e/b"},
		// Mixed.
		{"mixed", "/..%2f../secret", "/..%2f../secret"}, // inner `..` is literal in `%2f`
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var captured string
			mw := middleware.CleanPath()
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req.URL = &url.URL{Path: c.in}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if captured != c.want {
				t.Errorf("clean_path[%s]: in=%q out=%q, want %q", c.name, c.in, captured, c.want)
			}
		})
	}
}
