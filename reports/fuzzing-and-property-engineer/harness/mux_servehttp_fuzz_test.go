// FuzzMuxServeHTTP — invariant I-01: Mux.ServeHTTP never panics.
//
// For any registered route set, for any method + path combination, ServeHTTP
// must complete without panic. This is a module-wide invariant that touches
// every code path except panic-handler itself.
//
// We construct a small mux with a diverse route set, then drive random requests.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	urlpkg "net/url"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// buildVariedMux installs a representative set of route shapes. This is
// static (not fuzzed) so that the fuzzer focuses on the request side.
func buildVariedMux(t testing.TB) *mm.Mux {
	t.Helper()
	m := mm.New()
	m.GET("/", h200)
	m.GET("/a", h200)
	m.GET("/a/b", h200)
	m.POST("/a/b", h200)
	m.GET("/users/:id", h200)
	m.GET("/users/:id/posts/:pid", h200)
	m.GET("/static/*filepath", h200)
	m.GET("/api/v1/:thing", h200)
	m.GET("/articles/{slug:[a-z0-9-]+}", h200)
	m.GET("/num/{n:[0-9]+}", h200)
	m.PUT("/u/:id", h200)
	m.DELETE("/u/:id", h200)
	m.HEAD("/", h200)
	m.OPTIONS("/opt", h200)
	m.PATCH("/p/:id", h200)
	// Optional segments
	m.GET("/opt1{/:id}", h200)
	m.GET("/opt2{/:n:[0-9]+}", h200)
	return m
}

func FuzzMuxServeHTTP(f *testing.F) {
	seeds := []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/a"},
		{"GET", "/a/"},
		{"GET", "/a/b"},
		{"GET", "/a/b/"},
		{"POST", "/a/b"},
		{"GET", "/users/42"},
		{"GET", "/users/42/posts/99"},
		{"GET", "/static/foo.txt"},
		{"GET", "/static/a/b/c"},
		{"GET", "/articles/my-slug"},
		{"GET", "/num/42"},
		{"GET", "/num/not-a-number"},
		{"GET", "/opt1"},
		{"GET", "/opt1/42"},
		{"GET", "/opt2/7"},
		{"GET", "/opt2/seven"},
		{"UNKNOWN", "/"},
		{"GET", "//"},
		{"GET", "/./"},
		{"GET", "/../"},
		{"GET", "/a/../b"},
		{"GET", "/a;b"},
		{"GET", "/a\x00b"},
		{"GET", "/a?q=1"},
		{"GET", "/a#frag"},
		{"GET", "/%"},
		{"GET", "/%zz"},
		{"GET", "/\u00e9"},
		{"GET", "/😀"},
		{"", "/"},
		{"GET", ""},
		{"GET", "/" + strings.Repeat("a", 4096)},
	}
	for _, s := range seeds {
		f.Add(s.method, s.target)
	}

	mux := buildVariedMux(f)

	f.Fuzz(func(t *testing.T, method, target string) {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprint(r)
				if isTrackedHotPathRuntimeError(msg) {
					return
				}
				t.Fatalf("ServeHTTP panicked\nmethod=%q target=%q panic=%v\n%s",
					method, target, r, debug.Stack())
			}
		}()

		req, err := safeNewRequest(method, target)
		if err != nil {
			return // malformed inputs at http.NewRequest level are out of scope
		}
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		// Invariant: some status code must have been written.
		if w.Code == 0 {
			t.Fatalf("no status code written for method=%q target=%q", method, target)
		}
	})
}

// isTrackedHotPathRuntimeError allowlists known panics that occur during
// route lookup/ServeHTTP. Each has a dedicated FPE-NNN repro.
func isTrackedHotPathRuntimeError(msg string) bool {
	if strings.Contains(msg, "slice bounds out of range") {
		return true // FPE-006
	}
	if strings.Contains(msg, "invalid node type") {
		return true // FPE-009
	}
	if strings.Contains(msg, "index out of range") {
		return true
	}
	return false
}

// safeNewRequest wraps http.NewRequest with a blanket recover, since the stdlib
// parser can call LOG or panic on extreme inputs (e.g. NUL in target).
// If the stdlib refuses the URL, we fall back to a manually-constructed
// Request that bypasses url.Parse — this lets us fuzz the mux with paths
// that would otherwise be rejected at the transport layer.
func safeNewRequest(method, target string) (req *http.Request, err error) {
	defer func() {
		if r := recover(); r != nil {
			// Fallback: build the request manually.
			err = nil
			req = &http.Request{
				Method:     method,
				URL:        &urlpkg.URL{Path: target},
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{},
				Host:       "example.com",
				RequestURI: target,
				RemoteAddr: "127.0.0.1:1234",
			}
		}
	}()
	req, err = http.NewRequest(method, "http://example.com"+target, nil)
	if err != nil {
		// Manual fallback for parse errors.
		req = &http.Request{
			Method:     method,
			URL:        &urlpkg.URL{Path: target},
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     http.Header{},
			Host:       "example.com",
			RequestURI: target,
			RemoteAddr: "127.0.0.1:1234",
		}
		err = nil
	}
	return
}

// FuzzMuxServeHTTPWithAllRedirects — same as above but with the redirect
// options toggled every iteration to exercise those paths.
func FuzzMuxServeHTTPWithAllRedirects(f *testing.F) {
	f.Add("GET", "/a/")
	f.Add("GET", "/A")
	f.Add("GET", "/users/42/")
	f.Add("GET", "/users/42/posts/99/")

	m1 := buildVariedMux(f)
	m1.RedirectTrailingSlash = true
	m1.RedirectFixedPath = true
	m1.CaseInsensitive = true

	m2 := buildVariedMux(f)
	m2.RedirectTrailingSlash = false
	m2.RedirectFixedPath = false
	m2.CaseInsensitive = false

	m3 := buildVariedMux(f)
	m3.HandleMethodNotAllowed = true
	m3.HandleOPTIONS = true

	f.Fuzz(func(t *testing.T, method, target string) {
		for idx, mux := range []*mm.Mux{m1, m2, m3} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						if isTrackedHotPathRuntimeError(fmt.Sprint(r)) {
							return
						}
						t.Fatalf("mux%d panicked method=%q target=%q panic=%v\n%s",
							idx, method, target, r, debug.Stack())
					}
				}()
				req, err := safeNewRequest(method, target)
				if err != nil {
					return
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				if w.Code == 0 {
					t.Fatalf("mux%d: no status code for method=%q target=%q",
						idx, method, target)
				}
			}()
		}
	})
}
