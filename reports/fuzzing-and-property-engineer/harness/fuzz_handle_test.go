// Package harness contains fuzz tests and property tests for the MuxMaster public API.
// Build tag "fuzz" is used only to gate longer-running targets in CI short mode.
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// containsControlBytes returns true if s contains any ASCII control character (< 0x20 or 0x7F).
// httptest.NewRequest panics on URLs containing control characters; we skip those inputs.
func containsControlBytes(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c < 0x20 || c == 0x7F {
			return true
		}
	}
	return false
}

// buildRequest constructs an httptest request without panicking on invalid URLs.
// Returns (nil, err) if the URL cannot be parsed.
func buildRequest(method, urlPath string) (*http.Request, error) {
	if len(urlPath) == 0 || urlPath[0] != '/' {
		urlPath = "/" + urlPath
	}
	u, err := url.ParseRequestURI("http://example.com" + urlPath)
	if err != nil {
		return nil, err
	}
	req := &http.Request{
		Method:     method,
		URL:        u,
		Header:     make(http.Header),
		Body:       http.NoBody,
		RequestURI: urlPath,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	return req, nil
}

var h200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// FuzzHandle exercises Mux.Handle with arbitrary (method, pattern) pairs.
// Invariant I-01: must not panic for any non-nil handler.
// Expected panics (empty method, non-absolute path, unsupported method) are
// caught and swallowed; only unexpected panics are fatal.
func FuzzHandle(f *testing.F) {
	// Seed corpus — valid combinations
	f.Add("GET", "/")
	f.Add("POST", "/users/:id")
	f.Add("DELETE", "/static/*filepath")
	f.Add("PUT", "/api/v1/items/:id/tags/:tag")
	f.Add("PATCH", "/a/b/c/d/e")
	f.Add("GET", "/{id:[0-9]+}")
	f.Add("OPTIONS", "/")
	f.Add("TRACE", "/trace")
	f.Add("CONNECT", "/connect")
	f.Add("HEAD", "/healthz")
	// Seed — known-invalid to exercise panic-safety
	f.Add("", "/")
	f.Add("GET", "no-slash")
	f.Add("CUSTOM", "/custom")
	f.Add("GET", "")

	f.Fuzz(func(t *testing.T, method, pattern string) {
		defer func() {
			if r := recover(); r != nil {
				// Expected panics from documented invariants: empty method, bad path, unsupported method.
				// Re-panic only on truly unexpected crashes.
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): method=%q pattern=%q r=%v\n%s",
						method, pattern, r, debug.Stack())
				}
				expected := isExpectedHandlePanic(msg)
				if !expected {
					t.Fatalf("PANIC (unexpected): method=%q pattern=%q msg=%q\n%s",
						method, pattern, msg, debug.Stack())
				}
				// Expected panic — test passes.
			}
		}()
		mux := mm.New()
		mux.Handle(method, pattern, h200)
	})
}

// FuzzHandleFast exercises Mux.HandleFast with arbitrary (method, pattern) pairs.
// Invariant I-01b: HandleFast must not panic for any FastHandler.
func FuzzHandleFast(f *testing.F) {
	fast := mm.FastHandler(func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
	f.Add("GET", "/")
	f.Add("POST", "/items/:id")
	f.Add("GET", "/static/*filepath")
	f.Add("", "/")
	f.Add("GET", "no-slash")

	f.Fuzz(func(t *testing.T, method, pattern string) {
		defer func() {
			if r := recover(); r != nil {
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): method=%q pattern=%q r=%v\n%s",
						method, pattern, r, debug.Stack())
				}
				if !isExpectedHandlePanic(msg) {
					t.Fatalf("PANIC (unexpected): method=%q pattern=%q msg=%q\n%s",
						method, pattern, msg, debug.Stack())
				}
			}
		}()
		mux := mm.New()
		mux.HandleFast(method, pattern, fast)
	})
}

// FuzzServeHTTP exercises Mux.ServeHTTP with an arbitrary URL path.
// Invariant I-01c: ServeHTTP must never panic for any *http.Request.
// This covers dispatch, TSR, allowed(), lazyNotFound chains.
func FuzzServeHTTP(f *testing.F) {
	mux := setupFuzzMux()

	f.Add("/")
	f.Add("/users/123")
	f.Add("/users/123/posts")
	f.Add("/static/img/logo.png")
	f.Add("/api/v1/items")
	f.Add("/../etc/passwd")
	f.Add("//double-slash")
	f.Add("/users/%2F/posts")
	f.Add("/users/injection")

	f.Fuzz(func(t *testing.T, urlPath string) {
		// httptest.NewRequest panics on URLs containing control characters.
		// Filter these before reaching ServeHTTP — we want to test the router,
		// not httptest.NewRequest's URL parser.
		if containsControlBytes(urlPath) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ServeHTTP: path=%q r=%v\n%s", urlPath, r, debug.Stack())
			}
		}()
		req, err := buildRequest(http.MethodGet, urlPath)
		if err != nil {
			return // invalid URL per net/url — skip
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// Any response status is acceptable; no panic is the invariant.
	})
}

// FuzzServeHTTPAllMethods exercises ServeHTTP with arbitrary (method, path) combos.
// Ensures the allowed() / lazyMethodNotAllowed path never panics.
func FuzzServeHTTPAllMethods(f *testing.F) {
	mux := setupFuzzMux()

	f.Add("GET", "/users/123")
	f.Add("POST", "/users/123")
	f.Add("DELETE", "/users/123")
	f.Add("CUSTOM", "/users/123")
	f.Add("OPTIONS", "/users/123")
	f.Add("", "/")
	f.Add("GET", "/")

	f.Fuzz(func(t *testing.T, method, urlPath string) {
		if containsControlBytes(urlPath) || containsControlBytes(method) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC: method=%q path=%q r=%v\n%s", method, urlPath, r, debug.Stack())
			}
		}()
		req, err := buildRequest(method, urlPath)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	})
}

// setupFuzzMux returns a pre-configured Mux with representative routes.
func setupFuzzMux() *mm.Mux {
	mux := mm.New()
	mux.GET("/", h200)
	mux.GET("/users/:id", h200)
	mux.GET("/users/:id/posts", h200)
	mux.POST("/users", h200)
	mux.PUT("/users/:id", h200)
	mux.DELETE("/users/:id", h200)
	mux.GET("/static/*filepath", h200)
	mux.GET("/api/v1/items", h200)
	mux.POST("/api/v1/items", h200)
	mux.GET("/api/v1/items/:id", h200)
	return mux
}

// isExpectedHandlePanic returns true for panic messages that document
// intentional pre-condition violations in Handle/HandleFast.
// These are all panics emitted by tree.go (addRoute, insertChild, findWildcard)
// or mux.go for documented invalid inputs.
func isExpectedHandlePanic(msg string) bool {
	expected := []string{
		// mux.go precondition panics
		"muxmaster: HTTP method must not be empty",
		"muxmaster: path must begin with '/'",
		"muxmaster: handler must not be nil",
		"muxmaster: unsupported HTTP method",
		// tree.go structural panics — invalid path syntax or route conflicts
		"wildcards must be named",
		"wildcard",
		"catch-all",
		"conflict",
		"conflicts",
		"new path",
		"route",
		"child",
		"param",
		"existing",
		"duplicate",
		"invalid",
		"path segment",
		// Generic fallback for any tree-level panic
		"muxmaster:",
		"only one",
		"must",
	}
	for _, e := range expected {
		if containsSubstr(msg, e) {
			return true
		}
	}
	return false
}

func containsSubstr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
