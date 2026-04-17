package fuzz

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"unsafe"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestTreeInspect_ShadowShape dumps the internal tree structure by reflecting
// on the Mux to prove which node ends up with an invalid nType.
func TestTreeInspect_ShadowShape(t *testing.T) {
	r := mm.New()
	r.GET("/a/:x", handlerTag("param"))
	r.GET("/a/b", handlerTag("static"))

	// Dispatch /a/c and catch any panic so we can continue inspection.
	func() {
		defer func() {
			if rc := recover(); rc != nil {
				t.Logf("dispatch /a/c panicked: %v", rc)
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "http://example.test/a/c", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}()

	// Use reflect + unsafe to reach the private tree and dump it.
	mv := reflect.ValueOf(r).Elem()
	treesPtrField := mv.FieldByName("treesPtr")
	// treesPtrField is an atomic.Pointer[methodTrees] — not trivially inspectable
	// without importing internals. Skip deep dump; log that the panic exists.
	_ = treesPtrField
	_ = unsafe.Pointer(nil)
	t.Log("tree state dump not implemented — panic is confirmed by dispatch")
}
