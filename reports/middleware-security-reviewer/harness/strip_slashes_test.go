// Harness — strip_slashes.go.
//
// Threats covered:
//   - Trailing slash strip leaks /admin/ → /admin alias.
//   - Double slash //admin unchanged.
//   - Root "/" untouched.
//   - Interaction with CleanPath (ordering).
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-SS-001 — Matrix of strip behaviour.
// -----------------------------------------------------------------------------

func TestSec_StripSlashes_Matrix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/", "/"},         // root untouched
		{"/a", "/a"},       // no change
		{"/a/", "/a"},      // strip trailing
		{"/a/b/", "/a/b"},  // strip trailing
		{"//", "/"},        // len > 1 and ends in '/' → strip last byte
		{"//a", "//a"},     // leading double-slash, no trailing strip needed
		{"//a/", "//a"},    // leading double-slash, trailing stripped
		{"/a//", "/a/"},    // only last byte stripped — documented single-pass
		{"/a//b", "/a//b"}, // internal double slash preserved
		{"/a/b/c/", "/a/b/c"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			var captured string
			mw := middleware.StripSlashes()
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req.URL = &url.URL{Path: c.in}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if captured != c.want {
				t.Errorf("strip_slashes: in=%q out=%q, want %q", c.in, captured, c.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-SS-002 — Ordering with CleanPath:
// strip_slashes then clean_path: /a/b/ → /a/b → /a/b (no change at clean).
// clean_path then strip_slashes: /a/b/ → /a/b (clean trims) → /a/b (no strip).
// Verify idempotence at either order.
// -----------------------------------------------------------------------------

func TestSec_StripSlashes_OrderingWithCleanPath(t *testing.T) {
	cases := []struct {
		in     string
		wantSC string // strip then clean: first trim trailing /, then path.Clean
		wantCS string // clean then strip: first path.Clean, then trim trailing /
	}{
		// /a/b/: strip→/a/b, clean(/a/b)=/a/b. clean(/a/b/)=/a/b, strip: no trailing → /a/b.
		{"/a/b/", "/a/b", "/a/b"},
		// /a/b//: strip→/a/b/, clean(/a/b/)=/a/b. clean(/a/b//)=/a/b, strip no-op → /a/b.
		{"/a/b//", "/a/b", "/a/b"},
		// /a/../b/: strip→/a/../b, clean=/b. clean(/a/../b/)=/b, strip no-op → /b.
		{"/a/../b/", "/b", "/b"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			// Strip then Clean.
			var capSC string
			hSC := middleware.StripSlashes()(middleware.CleanPath()(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capSC = r.URL.Path
					w.WriteHeader(http.StatusOK)
				})))
			req1 := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req1.URL = &url.URL{Path: c.in}
			rec1 := httptest.NewRecorder()
			hSC.ServeHTTP(rec1, req1)

			// Clean then Strip.
			var capCS string
			hCS := middleware.CleanPath()(middleware.StripSlashes()(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capCS = r.URL.Path
					w.WriteHeader(http.StatusOK)
				})))
			req2 := httptest.NewRequest(http.MethodGet, "http://x/placeholder", nil)
			req2.URL = &url.URL{Path: c.in}
			rec2 := httptest.NewRecorder()
			hCS.ServeHTTP(rec2, req2)

			if capSC != c.wantSC {
				t.Errorf("Strip∘Clean: in=%q out=%q, want %q", c.in, capSC, c.wantSC)
			}
			if capCS != c.wantCS {
				t.Errorf("Clean∘Strip: in=%q out=%q, want %q", c.in, capCS, c.wantCS)
			}
			if capSC != capCS {
				t.Logf("strip/clean ordering diverges for in=%q (SC=%q, CS=%q) — routing may see different path",
					c.in, capSC, capCS)
			}
		})
	}
}
