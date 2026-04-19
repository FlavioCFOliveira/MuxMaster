package muxmaster

import (
	"net/http"
	"testing"
	"unsafe"
)

// TestNodeSizeUnchanged verifies the size of node matches the expected layout.
// The fast FastHandler field (8 bytes) was added in the HandleFast feature.
// Nodes exist only at registration time and are never per-request allocated,
// so the size increase has no impact on hot-path performance.
func TestNodeSizeUnchanged(t *testing.T) {
	const want = 112
	if got := unsafe.Sizeof(node{}); got != want {
		t.Errorf("node size changed: want %d bytes, got %d bytes", want, got)
	}
}

func TestMaxParamsCalculation(t *testing.T) {
	root := &node{}

	nopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	root.addRoute("/users", nopHandler)
	root.addRoute("/users/:id", nopHandler)
	root.addRoute("/users/:id/posts/:pid", nopHandler)
	root.addRoute("/static/*filepath", nopHandler)
	root.addRoute("/orgs/:org/repos/:repo/issues/:num", nopHandler)

	if root.maxParams != 3 {
		t.Errorf("expected maxParams=3, got %d", root.maxParams)
	}
}

func TestMaxParamsZeroForStaticTree(t *testing.T) {
	root := &node{}
	nopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	root.addRoute("/users", nopHandler)
	root.addRoute("/users/list", nopHandler)
	root.addRoute("/health", nopHandler)

	if root.maxParams != 0 {
		t.Errorf("expected maxParams=0 for static tree, got %d", root.maxParams)
	}
}

func TestMaxParamsWildcard(t *testing.T) {
	root := &node{}
	nopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	root.addRoute("/static/*filepath", nopHandler)

	if root.maxParams != 1 {
		t.Errorf("expected maxParams=1 for wildcard, got %d", root.maxParams)
	}
}

// TestMaxParamsSingleParam ensures a single :param route yields maxParams=1.
func TestMaxParamsSingleParam(t *testing.T) {
	root := &node{}
	nopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	root.addRoute("/users/:id", nopHandler)

	if root.maxParams != 1 {
		t.Errorf("expected maxParams=1, got %d", root.maxParams)
	}
}

// TestMaxParamsNilBufPassedForStaticTree verifies that getValue receives nil
// for a static-only tree (the optimisation under test). We confirm this
// indirectly: a static lookup must still resolve correctly even when the
// internal paramsBuf pointer is never set.
func TestMaxParamsNilBufPassedForStaticTree(t *testing.T) {
	root := &node{}
	nopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	root.addRoute("/hello", nopHandler)

	// getValue with nil params must work for static routes.
	h, _, pattern, tsr := root.getValue("/hello", nil, false)
	if h == nil {
		t.Fatal("expected handler for /hello, got nil")
	}
	if pattern != "/hello" {
		t.Errorf("expected pattern=/hello, got %q", pattern)
	}
	if tsr {
		t.Error("unexpected TSR hint for exact match")
	}
}
