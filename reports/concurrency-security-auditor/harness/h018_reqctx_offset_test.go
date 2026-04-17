package harness

// Targets hypothesis H-018: reflect-based reqCtxOffset staleness across Go
// versions. If a future Go release renames the 'ctx' field, removes it, or
// changes its type, params.go's init() silently yields reqCtxOffset == 0, and
// unsafe.Add writes will corrupt the first word of http.Request.
//
// Detection strategy:
//   1. Go through the public contract: r.WithContext(X) followed by r.Context()
//      must return X. We drive this through MuxMaster's param-route path which
//      exercises the unsafe write, then assert the context field actually
//      contains our *requestCtx.
//   2. Validate that reqCtxOffset points to a context.Context-shaped field by
//      writing a known ctx via r.WithContext and then reading it back via
//      r.Context() after a param-route dispatch.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

type h018Key struct{}

// TestH018_ReqCtxOffsetAgreement asserts the reflect walk in params.go locates
// a field named "ctx" of type context.Context. We cannot import reqCtxOffset
// (unexported), so we infer correctness via observable behaviour:
//   - After the dispatcher writes *requestCtx into r.ctx, PathParam must see it.
//   - The parent context.Value (our sentinel) must still be reachable via
//     c.Context.Value in *requestCtx.Value.
func TestH018_ReqCtxOffsetAgreement(t *testing.T) {
	// Sanity check — also catches the case where ctx was renamed/removed.
	rt := reflect.TypeOf(http.Request{})
	var found bool
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Name == "ctx" {
			if f.Type.String() != "context.Context" {
				t.Fatalf("H-018: http.Request.ctx has unexpected type %s (expected context.Context)", f.Type.String())
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("H-018: http.Request has no field named 'ctx' — reqCtxOffset would be 0 and unsafe writes would corrupt the struct")
	}

	// Behavioural check: the sentinel from the parent context must survive a
	// param-route dispatch. If reqCtxOffset were wrong, PathParam would see an
	// unrelated or nil context.
	r := mm.New()
	r.GET("/x/:id", func(w http.ResponseWriter, req *http.Request) {
		id := mm.PathParam(req, "id")
		if id != "alpha" {
			t.Errorf("H-018: expected id=alpha, got %q", id)
		}
		if got := req.Context().Value(h018Key{}); got != "sentinel" {
			t.Errorf("H-018: expected sentinel through parent context, got %v", got)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/x/alpha", nil).
		WithContext(context.WithValue(context.Background(), h018Key{}, "sentinel"))
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, req)

	// After dispatch, the original req.Context must be restored — not rc.
	// If reqCtxOffset leaks, req.Context() returns a *requestCtx (or nil).
	if got := req.Context().Value(h018Key{}); got != "sentinel" {
		t.Fatalf("H-018: origCtx was NOT restored after dispatch; got %v", got)
	}
}
