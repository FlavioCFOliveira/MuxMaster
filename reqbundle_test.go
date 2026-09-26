package muxmaster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"testing"
)

// The tests in this file assert the observable contract of param dispatch
// (PathParam, ParamsFromContext, original request untouched, params visible
// from spawned goroutines). That contract holds on both dispatch paths — the
// reqBundle fast path (hasReqCtxField == true) and the r.WithContext
// fallback — so no test is conditional on the active path.
// TestReqCtxFieldDetected pins which path is active on supported toolchains.

// TestReqCtxFieldDetected asserts that init() in params.go locates the
// unexported ctx field of http.Request on every Go version go.mod supports,
// so the reqBundle fast path (and the PoolRequestBundle opt-in, which
// requires it) is active. It also cross-checks the recorded offset against
// an independent reflect lookup. A failure means a new Go release renamed,
// retyped or removed the field: dispatch stays correct through the
// r.WithContext fallback, but performance regresses and the supported-version
// claim in go.mod / COMPATIBILITY.md must be revisited.
func TestReqCtxFieldDetected(t *testing.T) {
	if !hasReqCtxField {
		t.Fatalf("http.Request has no 'ctx context.Context' field on %s: reqBundle fast path inactive", runtime.Version())
	}
	f, ok := reflect.TypeOf(http.Request{}).FieldByName("ctx")
	if !ok {
		t.Fatal("reflect: http.Request.ctx not found")
	}
	if f.Type != reflect.TypeOf((*context.Context)(nil)).Elem() {
		t.Fatalf("http.Request.ctx has type %v, want context.Context", f.Type)
	}
	if f.Offset != reqCtxFieldOffset {
		t.Fatalf("reqCtxFieldOffset = %d, reflect offset = %d", reqCtxFieldOffset, f.Offset)
	}
}

// TestReqBundleParamAccess verifies that PathParam works correctly on the
// active param-dispatch path (reqBundle on supported toolchains).
func TestReqBundleParamAccess(t *testing.T) {
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
