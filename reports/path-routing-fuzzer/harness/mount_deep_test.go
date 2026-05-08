package harness

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestMount_TraversalEscapesPrefix(t *testing.T) {
	// CRITICAL: Mount("/api", inner) — inner path is NOT sanitised.
	// /api/../../etc/passwd has URL.Path=/api/../../etc/passwd (not cleaned by net/url).
	// The tree's catch-all *mux_mount matches everything under /api/.
	// Inner handler receives Path = /../../etc/passwd (stripping /api prefix).
	// If inner is http.FileServer("/var/www"), this would escape to /etc/passwd.

	var innerReceivedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerReceivedPath = r.URL.Path
		w.Header().Set("X-Inner-Path", r.URL.Path)
		w.Header().Set("X-Handler", "inner")
		w.WriteHeader(200)
	})

	outside := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "outside")
		w.WriteHeader(200)
	})

	r := mm.New()
	r.Mount("/api", inner)
	r.GET("/etc/passwd", outside) // registered to detect if traversal escaped mount

	cases := []struct {
		path              string
		innerPathExpected string
		desc              string
		secCritical       bool
	}{
		{"/api/v1/data", "/v1/data", "normal request", false},
		{"/api/../etc/passwd", "N/A", "url traversal: escapes mount prefix", true},
		{"/api/v1/../../../etc/passwd", "N/A", "deep traversal: escapes mount prefix", true},
		{"/api/../../etc/passwd", "N/A", "two levels up", true},
	}

	for _, tc := range cases {
		innerReceivedPath = ""
		req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		t.Logf("MOUNT-TRAVERSAL: %s: %q → code=%d handler=%q innerPath=%q",
			tc.desc, tc.path, w.Code, w.Header().Get("X-Handler"), innerReceivedPath)

		if tc.secCritical {
			if w.Header().Get("X-Handler") == "inner" {
				t.Logf("MOUNT-TRAVERSAL FINDING: %q reached inner handler with innerPath=%q — "+
					"inner handler receives un-sanitised traversal path. "+
					"If inner is a file server, operator MUST sanitise. "+
					"MuxMaster.mountAt does NOT call path.Clean on the inner path.",
					tc.path, innerReceivedPath)
			}
			// The question: did traversal ESCAPE /api prefix to reach /etc/passwd handler?
			if w.Header().Get("X-Handler") == "outside" {
				t.Errorf("MOUNT-TRAVERSAL BYPASS: %q reached /etc/passwd handler (escaped mount prefix) code=%d",
					tc.path, w.Code)
			}
		}
	}
}
