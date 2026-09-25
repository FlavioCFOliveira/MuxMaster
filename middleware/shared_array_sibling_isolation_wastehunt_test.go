// Regression tests for waste-hunt gate item 2 (sprint 18 follow-up to
// MID-NOCACHE-1/MID-CORS-1): NoCache and CORS now fuse all the single-value
// header slices they set in one request into ONE freshly allocated backing
// array (MID-NOCACHE-2, MID-CORS-2), instead of one allocation per header,
// to cut allocs/op. Each header's slice into that array is the FULL slice
// expression vals[i:i+1:i+1], which caps its capacity at 1.
//
// These tests prove that capping actually prevents SIBLING-header
// corruption within a SINGLE response: appending to one header (as
// http.Header.Add does) must grow into a fresh backing array rather than
// silently overwriting an ADJACENT header's slot in the shared array. This
// is a different property from the existing cross-request/cross-instance
// leak tests in nocache_wastehunt_test.go and cors_wastehunt_test.go, which
// check that MUTATING one request's slice does not leak into ANOTHER
// request; here a single request's own headers must not corrupt each
// other.
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func TestNoCache_AppendToOneHeader_DoesNotCorruptSiblingHeaders(t *testing.T) {
	h := middleware.NoCache()(http.HandlerFunc(nopHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Pragma":            rec.Header().Get("Pragma"),
		"Expires":           rec.Header().Get("Expires"),
		"Surrogate-Control": rec.Header().Get("Surrogate-Control"),
		"X-Accel-Expires":   rec.Header().Get("X-Accel-Expires"),
	}

	// Cache-Control is vals[0:1:1] — its neighbour in the shared [5]string
	// array is Pragma at index 1. If the slices were not capped, this
	// append would silently overwrite Pragma's slot instead of allocating.
	rec.Header().Add("Cache-Control", "extra-directive")

	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Fatalf("%s = %q after Cache-Control.Add, want unchanged %q — appending to Cache-Control corrupted a sibling header sharing the same backing array", k, got, v)
		}
	}
	if got := rec.Header().Values("Cache-Control"); len(got) != 2 || got[1] != "extra-directive" {
		t.Fatalf("Cache-Control values = %v, want a 2-element slice ending in %q", got, "extra-directive")
	}
}

// TestCORS_AppendToOneHeader_DoesNotCorruptSiblingHeaders drives a preflight
// request that sets every CORS header MID-CORS-2 can fuse into the shared
// [7]string array (Access-Control-Allow-Origin, Vary, -Credentials,
// -Expose-Headers, -Allow-Methods, -Allow-Headers, -Max-Age) and confirms
// appending to the FIRST one does not corrupt any of the others.
func TestCORS_AppendToOneHeader_DoesNotCorruptSiblingHeaders(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"X-Custom"},
		ExposedHeaders:   []string{"X-Total-Count"},
		AllowCredentials: true,
		MaxAge:           600,
	})(http.HandlerFunc(nopHandler))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	keys := []string{
		"Vary",
		"Access-Control-Allow-Credentials",
		"Access-Control-Expose-Headers",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Max-Age",
	}
	want := make(map[string]string, len(keys))
	for _, k := range keys {
		v := rec.Header().Get(k)
		if v == "" {
			t.Fatalf("%s not set — MID-CORS-2 fusion did not run as expected for this configuration", k)
		}
		want[k] = v
	}

	// Access-Control-Allow-Origin is vals[0:1:1], the first slot in the
	// shared [7]string array. If the slices were not capped, this append
	// would silently overwrite whichever header occupies vals[1] (Vary, for
	// a non-wildcard origin) instead of allocating a new backing array.
	rec.Header().Add("Access-Control-Allow-Origin", "https://another.example")

	for _, k := range keys {
		if got := rec.Header().Get(k); got != want[k] {
			t.Fatalf("%s = %q after Access-Control-Allow-Origin.Add, want unchanged %q — appending corrupted a sibling header sharing the same backing array", k, got, want[k])
		}
	}
}
