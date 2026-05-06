package fuzz

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestShadowMatrix_StaticAfterParam systematically explores register-order
// cases where a static child is added *after* a sibling :param on the same
// parent segment. The expected behaviour is either:
//   - addRoute panics (reject ambiguity up-front), OR
//   - both routes are reachable at dispatch time without panic.
//
// Anything else — in particular: the static registration silently "succeeds"
// yet the :param dispatch subsequently panics — is a correctness bug that
// corrupts the tree and leaks an "invalid node type" panic into responses
// generated from attacker-controlled requests.
func TestShadowMatrix_StaticAfterParam(t *testing.T) {
	scenarios := []struct {
		name       string
		firstParam string // registered first
		secondStat string // registered after
		dispatchPs []string
	}{
		{"basic_a", "/a/:x", "/a/b", []string{"/a/b", "/a/c"}},
		{"depth2", "/u/:id", "/u/me", []string{"/u/me", "/u/42"}},
		{"mid", "/api/:v/items", "/api/v1/items", []string{"/api/v1/items", "/api/v2/items"}},
		{"suffix", "/u/:id/profile", "/u/me/profile", []string{"/u/me/profile", "/u/42/profile"}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			r := mm.New()
			r.GET(sc.firstParam, handlerTag("param"))

			reg2Panicked := false
			func() {
				defer func() {
					if rc := recover(); rc != nil {
						reg2Panicked = true
						t.Logf("addRoute second reg panicked (safer): %v", rc)
					}
				}()
				r.GET(sc.secondStat, handlerTag("static"))
			}()

			for _, p := range sc.dispatchPs {
				req := httptest.NewRequest(http.MethodGet, "http://example.test"+p, nil)
				rec := httptest.NewRecorder()
				var dispPanic any
				func() {
					defer func() { dispPanic = recover() }()
					r.ServeHTTP(rec, req)
				}()
				if dispPanic != nil {
					t.Errorf("FINDING reg2Panicked=%v dispatch=%q PANIC=%v (corrupt tree)",
						reg2Panicked, p, dispPanic)
				} else {
					t.Logf("reg2Panicked=%v dispatch=%q status=%d handler=%q",
						reg2Panicked, p, rec.Code, rec.Header().Get("X-Handler"))
				}
			}
		})
	}
}

// TestShadowMatrix_CatchAllAfterParam adds a catch-all sibling after a
// :param; same invariant.
func TestShadowMatrix_CatchAllAfterParam(t *testing.T) {
	r := mm.New()
	r.GET("/a/:x", handlerTag("param"))
	panicked := false
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				panicked = true
				t.Logf("catch-all after param panicked at registration (safer): %v", rc)
			}
		}()
		r.GET("/a/*rest", handlerTag("catchall"))
	}()
	fmt.Printf("catchall-after-param registration panicked? %v\n", panicked)
	for _, p := range []string{"/a/1", "/a/1/2"} {
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
		t.Logf("dispatch %q → %d %q", p, rec.Code, rec.Header().Get("X-Handler"))
	}
}
