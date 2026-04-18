package muxmaster

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReqBundleParamAccess verifies that PathParam works correctly
// when the reqBundle optimisation is active.
func TestReqBundleParamAccess(t *testing.T) {
	if !hasReqCtxField {
		t.Skip("reqBundle fast path unavailable on this Go version")
	}
	m := New()
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		id := PathParam(r, "id")
		if id != "42" {
			t.Errorf("want id=42, got %q", id)
		}
		pat := RoutePattern(r)
		if pat != "/users/:id" {
			t.Errorf("want pattern=/users/:id, got %q", pat)
		}
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/42", nil))
}

// TestReqBundleParamsFromContext verifies ParamsFromContext with reqBundle.
func TestReqBundleParamsFromContext(t *testing.T) {
	if !hasReqCtxField {
		t.Skip("reqBundle fast path unavailable on this Go version")
	}
	m := New()
	m.GET("/orgs/:org/repos/:repo", func(w http.ResponseWriter, r *http.Request) {
		ps := ParamsFromContext(r.Context())
		if len(ps) != 2 {
			t.Errorf("want 2 params, got %d", len(ps))
			return
		}
		if ps[0].Key != "org" || ps[0].Value != "acme" {
			t.Errorf("want org=acme, got %v", ps[0])
		}
		if ps[1].Key != "repo" || ps[1].Value != "api" {
			t.Errorf("want repo=api, got %v", ps[1])
		}
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/orgs/acme/repos/api", nil))
}

// TestReqBundleOriginalRequestUnmodified verifies that dispatch (reqBundle path)
// does NOT modify the original *http.Request — the handler receives a copy.
func TestReqBundleOriginalRequestUnmodified(t *testing.T) {
	if !hasReqCtxField {
		t.Skip("reqBundle fast path unavailable on this Go version")
	}
	m := New()
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {})

	orig := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	origCtx := orig.Context()

	m.ServeHTTP(httptest.NewRecorder(), orig)

	if orig.Context() != origCtx {
		t.Error("dispatch (reqBundle): original request context was modified")
	}
}

// TestReqBundleGoroutineSpawn verifies that params are accessible in goroutines
// spawned by the handler.
func TestReqBundleGoroutineSpawn(t *testing.T) {
	if !hasReqCtxField {
		t.Skip("reqBundle fast path unavailable on this Go version")
	}
	done := make(chan string, 1)
	m := New()
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		captured := r // capture the request
		go func() {
			done <- PathParam(captured, "id")
		}()
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/99", nil))
	got := <-done
	if got != "99" {
		t.Errorf("goroutine got id=%q, want 99", got)
	}
}
