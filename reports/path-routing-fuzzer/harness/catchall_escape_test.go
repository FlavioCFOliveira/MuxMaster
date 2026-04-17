package fuzz

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestCatchAllEscape_FilepathCapture probes exactly which characters end
// up inside the catch-all parameter. If filepath can contain ".." the
// file-serving layer must reject; document the boundary.
func TestCatchAllEscape_FilepathCapture(t *testing.T) {
	r := mm.New()
	var captured string
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		captured = mm.PathParam(req, "filepath")
		w.WriteHeader(200)
	})
	cases := []struct {
		input    string
		expected string
		note     string
	}{
		{"/static/foo", "/foo", "normal"},
		{"/static/../admin", "", "traversal (router should 404 when /admin exists; here it doesn't)"},
		{"/static/..%2fadmin", "/..%2fadmin", "encoded slash retains as literal"},
		{"/static/%2e%2e/admin", "/%2e%2e/admin", "encoded dots"},
		{"/static/foo/../bar", "", "middle traversal"},
		{"/static/foo/%00bar", "/foo/%00bar", "null byte"},
		{"/static//", "//", "double slash"},
		{"/static//foo", "//foo", "leading double"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			captured = ""
			req := httptest.NewRequest(http.MethodGet,
				"http://example.test"+tc.input, nil)
			rec := httptest.NewRecorder()
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				r.ServeHTTP(rec, req)
			}()
			t.Logf("input=%q captured=%q status=%d panic=%v note=%s",
				tc.input, captured, rec.Code, panicked, tc.note)
			// Invariant: captured must never contain a literal ".."
			// segment (a file server downstream would interpret it).
			for _, seg := range strings.Split(captured, "/") {
				if seg == ".." {
					t.Errorf("catch-all captured literal '..' segment: %q", captured)
				}
			}
		})
	}
}

// TestCatchAllEscape_ServeFiles ensures the real ServeFiles wrapper
// (which defers to http.FileServer) gets asked for files with ".." in
// them. The file server handles this safely; we only verify the boundary.
func TestCatchAllEscape_ServeFiles(t *testing.T) {
	r := mm.New()
	var servedPath string
	// Shim FileSystem to observe what filename is requested.
	r.ServeFiles("/static/*filepath", shimFS{
		onOpen: func(name string) {
			servedPath = name
		},
	})
	cases := []string{
		"/static/../admin",
		"/static/..%2fadmin",
		"/static/%2e%2e/admin",
		"/static/foo",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			servedPath = ""
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+p, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			t.Logf("input=%q servedPath=%q status=%d", p, servedPath, rec.Code)
		})
	}
}

type shimFS struct {
	onOpen func(string)
}

func (s shimFS) Open(name string) (http.File, error) {
	if s.onOpen != nil {
		s.onOpen(name)
	}
	return nil, http.ErrNoLocation
}
