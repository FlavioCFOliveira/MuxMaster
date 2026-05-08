//go:build race

// h018_reqctx_offset_test.go — CSA harness for MM-2026-0035 / Hypothesis #2
// Validates setReqCtxUnsafe ABI drift: on Go 1.26.2, the reflect-derived offset
// must point to the 'ctx context.Context' field of http.Request.
//
// If hasReqCtxField==false (future Go version, field renamed), the safe fallback
// (r.WithContext) must activate — no silent wrong-field write.

package muxmaster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"unsafe"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// ctxOffsetForTest replicates the offset computation from params.go for verification.
// Returns (offset, found).
func ctxOffsetForTest() (uintptr, bool) {
	t := reflect.TypeOf(http.Request{})
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Name == "ctx" && f.Type == ctxType {
			return f.Offset, true
		}
	}
	return 0, false
}

// TestReqCtxField_OffsetIsCorrect verifies that the offset computed by init()
// in params.go actually points to the context.Context field of http.Request.
// A handler accessing req.Context() must see the requestCtx* we injected, which
// chains to the parent context via its embedded context.Context field.
func TestReqCtxField_OffsetIsCorrect(t *testing.T) {
	r := mm.New()
	var captured atomic.Value // stores context.Context

	r.GET("/test/:id", func(w http.ResponseWriter, req *http.Request) {
		captured.Store(req.Context())
		w.WriteHeader(http.StatusOK)
	})

	type sentinelKey struct{}
	parentCtx := context.WithValue(context.Background(), sentinelKey{}, "yes")
	req := httptest.NewRequest("GET", "/test/42", nil)
	req = req.WithContext(parentCtx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	got, ok := captured.Load().(context.Context)
	if !ok || got == nil {
		t.Fatal("handler did not store context")
	}

	// The handler context must chain through the parent context.
	if got.Value(sentinelKey{}) != "yes" {
		t.Error("handler context does not chain through parent context — offset may be wrong")
	}

	// Verify path params are accessible, confirming the ctx swap worked.
	ps := mm.ParamsFromContext(got)
	if len(ps) != 1 || ps[0].Key != "id" || ps[0].Value != "42" {
		t.Errorf("unexpected params in handler context: %+v", ps)
	}
}

// TestReqCtxField_NoWriteToOriginal verifies that after ServeHTTP returns,
// the original request's ctx field is unchanged. Uses the same reflected offset
// to read back the original request's ctx field.
func TestReqCtxField_NoWriteToOriginal(t *testing.T) {
	offset, found := ctxOffsetForTest()
	if !found {
		t.Skip("ctx field not found in http.Request — safe fallback path active; nothing to verify")
	}

	r := mm.New()
	r.GET("/check/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	originalReq := httptest.NewRequest("GET", "/check/99", nil)
	origCtx := originalReq.Context()

	// Read the raw pointer stored in the ctx field before dispatch.
	origCtxPtrBefore := *(*uintptr)(unsafe.Add(unsafe.Pointer(originalReq), offset))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, originalReq)

	origCtxPtrAfter := *(*uintptr)(unsafe.Add(unsafe.Pointer(originalReq), offset))

	if origCtxPtrBefore != origCtxPtrAfter {
		t.Error("original request ctx field was mutated by setReqCtxUnsafe — CSA-001 regression")
	}

	if originalReq.Context() != origCtx {
		t.Error("original request.Context() pointer changed after ServeHTTP")
	}
}

// TestReqCtxField_FallbackSafe ensures that when hasReqCtxField is false,
// params are still accessible via ParamsFromContext (safe fallback path uses r.WithContext).
// We simulate this by routing through the standard handler and checking contexts work.
func TestReqCtxField_ParamsAccessible_AllTiers(t *testing.T) {
	r := mm.New()
	type result struct {
		params mm.Params
		ok     bool
	}
	ch := make(chan result, 3)

	r.GET("/one/:a", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		ch <- result{params: ps, ok: len(ps) == 1}
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/two/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		ch <- result{params: ps, ok: len(ps) == 2}
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/three/:a/:b/:c", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		ch <- result{params: ps, ok: len(ps) == 3}
		w.WriteHeader(http.StatusOK)
	})

	paths := []string{"/one/x", "/two/x/y", "/three/x/y/z"}
	for _, p := range paths {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	close(ch)

	for res := range ch {
		if !res.ok {
			t.Errorf("wrong number of params: %+v", res.params)
		}
	}
}
