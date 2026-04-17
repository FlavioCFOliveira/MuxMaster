package fuzz

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestShadowDebug_IdentifyPanicCause inspects which of the two registrations
// causes the panic in the /a/:x + /a/b scenario.
func TestShadowDebug_IdentifyPanicCause(t *testing.T) {
	r := mm.New()

	step1OK := true
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				step1OK = false
				t.Logf("step1 (register /a/:x) panicked: %v", rc)
			}
		}()
		r.GET("/a/:x", handlerTag("param"))
	}()

	step2OK := true
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				step2OK = false
				t.Logf("step2 (register /a/b after /a/:x) panicked: %v", rc)
			}
		}()
		r.GET("/a/b", handlerTag("static"))
	}()

	t.Logf("step1OK=%v step2OK=%v", step1OK, step2OK)

	// Dispatch both paths.
	for _, p := range []string{"/a/b", "/a/c"} {
		req := httptest.NewRequest(http.MethodGet, "http://example.test"+p, nil)
		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if rc := recover(); rc != nil {
					t.Errorf("dispatch %q panicked: %v", p, rc)
				}
			}()
			r.ServeHTTP(rec, req)
		}()
		t.Logf("dispatch %q → status=%d handler=%q", p,
			rec.Code, rec.Header().Get("X-Handler"))
	}
}

// TestShadowDebug_DirectPanic reproduces the exact scenario that ran into
// "invalid node type" in the first test execution: registration apparently
// succeeded but dispatch crashed.
func TestShadowDebug_DirectPanic(t *testing.T) {
	// This is the exact sequence that crashed during the first run.
	defer func() {
		if rc := recover(); rc != nil {
			t.Errorf("scenario panicked (captured): %v", rc)
		}
	}()
	r := mm.New()
	r.GET("/a/:x", handlerTag("param"))
	r.GET("/a/b", handlerTag("static"))

	req := httptest.NewRequest(http.MethodGet, "http://example.test/a/b", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	fmt.Printf("status=%d handler=%s\n", rec.Code, rec.Header().Get("X-Handler"))
}
