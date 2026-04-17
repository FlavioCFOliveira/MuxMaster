// Package harness contains regression tests owned by go-sast-and-memory-auditor.
//
// h018_ctx_field_type_test verifies Hypothesis H-018: that the offset resolved
// at init() truly points to a field of type context.Context in http.Request.
// If a future Go toolchain renames the field, changes its type, or relocates it,
// this test fails early — preventing silent corruption via unsafe.Add in
// mux.go and params.go.
package harness

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// TestH018_RequestCtxFieldType asserts the invariants required for the
// unsafe.Add pattern in params.go and mux.go to remain safe:
//  1. http.Request still has an unexported 'ctx' field.
//  2. That field's type is context.Context (interface).
//  3. That field's offset is > 0 (not a header, which is safer to detect misconfig).
func TestH018_RequestCtxFieldType(t *testing.T) {
	rt := reflect.TypeOf(http.Request{})
	var (
		found bool
		fType reflect.Type
		fOff  uintptr
	)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Name == "ctx" {
			found = true
			fType = f.Type
			fOff = f.Offset
			break
		}
	}
	if !found {
		t.Fatalf("H-018 BROKEN: http.Request has no field named 'ctx'. " +
			"The unsafe.Add pattern in mux.go/params.go will corrupt data. " +
			"Investigate Go stdlib changes immediately.")
	}
	ctxIface := reflect.TypeOf((*context.Context)(nil)).Elem()
	if !fType.Implements(ctxIface) && fType != ctxIface {
		t.Fatalf("H-018 BROKEN: http.Request.ctx is of type %v, expected context.Context. "+
			"The unsafe.Add pattern will corrupt data.", fType)
	}
	if fOff == 0 {
		t.Fatalf("H-018 SUSPICIOUS: http.Request.ctx is at offset 0 — this is legitimate "+
			"only if context.Context is the first field. Verify by inspecting the " +
			"http.Request struct layout.")
	}
}
