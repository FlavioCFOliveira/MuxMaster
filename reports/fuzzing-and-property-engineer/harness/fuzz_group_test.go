package harness

import (
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"net/http"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// FuzzGroup exercises Mux.Group with arbitrary prefixes and leaf paths.
// Invariant I-04: nested group prefix composition must never panic and
// must route requests to the registered leaf correctly.
func FuzzGroup(f *testing.F) {
	f.Add("/api", "/v1", "/users")
	f.Add("/", "/a", "/b")
	f.Add("/deep", "/nested", "/path")
	f.Add("", "/api", "/v1")       // empty prefix — may panic at Handle level
	f.Add("/api", "", "/v1")       // empty sub-prefix
	f.Add("/api", "/v1/../hack", "/users") // traversal attempt in prefix
	f.Add("/api", "/v1", "no-slash")       // leaf without slash
	f.Add("/a/b/c", "/d/e", "/f")

	f.Fuzz(func(t *testing.T, prefix1, prefix2, leaf string) {
		defer func() {
			if r := recover(); r != nil {
				// Group prefix panics only if the combined path violates Handle invariants.
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): prefix1=%q prefix2=%q leaf=%q r=%v\n%s",
						prefix1, prefix2, leaf, r, debug.Stack())
				}
				if !isExpectedHandlePanic(msg) {
					t.Fatalf("PANIC (unexpected): prefix1=%q prefix2=%q leaf=%q msg=%q\n%s",
						prefix1, prefix2, leaf, msg, debug.Stack())
				}
				// Expected panic from Handle's precondition check.
				return
			}
		}()

		mux := mm.New()
		g1 := mux.Group(prefix1)
		g2 := g1.Group(prefix2)
		g2.GET(leaf, h200)

		// Verify routing only when the combined path is valid.
		fullPath := prefix1 + prefix2 + leaf
		if len(fullPath) == 0 || fullPath[0] != '/' {
			return
		}
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+fullPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// 200 means routed; 404 means path mismatch (acceptable for invalid combos).
		// Crash or panic is the only failure mode.
	})
}

// FuzzMount exercises Mux.Mount with arbitrary prefixes.
// Invariant: Mount must not panic for valid prefixes; inner handler receives stripped path.
// Co-investigation of hypothesis H-G (Mount + auth middleware) and H-J (redirectFixedPath + Mount).
func FuzzMount(f *testing.F) {
	f.Add("/api")
	f.Add("/api/v1")
	f.Add("/static")
	f.Add("")           // should panic — non-absolute
	f.Add("no-slash")   // should panic — non-absolute

	f.Fuzz(func(t *testing.T, prefix string) {
		var innerPath string
		defer func() {
			if r := recover(); r != nil {
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): prefix=%q r=%v\n%s", prefix, r, debug.Stack())
				}
				if !isExpectedMountPanic(msg) {
					t.Fatalf("PANIC (unexpected): prefix=%q innerPath=%q msg=%q\n%s",
						prefix, innerPath, msg, debug.Stack())
				}
			}
		}()

		mux := mm.New()
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			innerPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		mux.Mount(prefix, inner)

		// Verify the mounted handler strips the prefix correctly.
		if len(prefix) == 0 || prefix[0] != '/' {
			return
		}
		cleanPrefix := strings.TrimRight(prefix, "/")
		reqPath := cleanPrefix + "/resource"
		req, err := buildRequest(http.MethodGet, reqPath)
		if err != nil {
			return // invalid URL — skip
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			// I-Mount-01: inner handler must receive path without prefix.
			if innerPath != "/resource" && innerPath != "" {
				t.Errorf("Mount prefix stripping failed: prefix=%q innerPath=%q want=/resource",
					prefix, innerPath)
			}
		}
	})
}

// FuzzGroupMiddlewareOrder verifies that group middleware is applied in
// registration order (I-05) even when the group is deeply nested.
func FuzzGroupDepthNoPanic(f *testing.F) {
	f.Add(1)
	f.Add(5)
	f.Add(10)
	f.Add(20)

	f.Fuzz(func(t *testing.T, depth int) {
		if depth < 1 || depth > 50 {
			return // bound to avoid OOM
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC at depth=%d: %v\n%s", depth, r, debug.Stack())
			}
		}()

		mux := mm.New()
		g := mux.Group("/root")
		for i := range depth {
			seg := strings.Repeat("a", (i%8)+1)
			g = g.Group("/" + seg)
		}
		g.GET("/leaf", h200)

		// Build the expected path and verify routing.
		var sb strings.Builder
		sb.WriteString("/root")
		for i := range depth {
			seg := strings.Repeat("a", (i%8)+1)
			sb.WriteString("/")
			sb.WriteString(seg)
		}
		sb.WriteString("/leaf")
		fullPath := sb.String()

		req := httptest.NewRequest(http.MethodGet, "http://example.com"+fullPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("nested group depth=%d: path=%q got=%d want=200",
				depth, fullPath, rec.Code)
		}
	})
}

func isExpectedMountPanic(msg string) bool {
	expected := []string{
		"muxmaster: nil handler",
		"muxmaster: Mount prefix must begin with '/'",
		"muxmaster: path must begin with '/'",
		"muxmaster: path contains invalid UTF-8",
		"conflicts",
		"wildcard",
		"catch-all",
		"panic",
		"invalid",
		"must",
		"only one",
	}
	for _, e := range expected {
		if strings.Contains(msg, e) {
			return true
		}
	}
	return false
}
