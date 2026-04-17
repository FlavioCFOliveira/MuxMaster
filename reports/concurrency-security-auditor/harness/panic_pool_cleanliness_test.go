package harness

// Step 3 of auditor method: panic recovery + pool cleanliness.
//
// A handler panic must NOT prevent releaseRC from being called. If the rc
// object leaks (never Put back), the pool grows; if it IS Put back but not
// zeroed, the next request sees stale params. MuxMaster's current layout
// uses *origCtxPtr = origCtx + releaseRC(rc) AFTER handler.ServeHTTP. A panic
// therefore skips the cleanup unless PanicHandler is configured.
//
// This test mines two concerning surfaces:
//
//   1. m.PanicHandler is nil: panic propagates out of ServeHTTP. No cleanup
//      runs. The next request should NOT observe the previous request's rc
//      contents — this is currently ONLY safe because the panic propagates
//      past httptest, but the origCtx of the aborted request IS NOT restored
//      (r.ctx is left pointing at rc after the panic). This is a documented
//      concern if the user recovers at a higher level and reuses r.
//
//   2. m.PanicHandler is set: dispatchWithRecover wraps with `defer
//      m.recoverPanic`. Between the panic and the deferred recover, any
//      accumulated unclosed work runs on goroutine stack. Cleanup still does
//      NOT occur — we need a defer in the param-route code path to guarantee
//      release.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestPanicWithoutHandler_PoolRelease exercises the case where PanicHandler
// is NOT set. The panic must propagate out of ServeHTTP; the test recovers it
// itself, then issues a subsequent non-panicking request to the same pattern
// and asserts the response is correct — i.e. the previous panic did not leave
// the router in a broken state.
func TestPanicWithoutHandler_PoolRelease(t *testing.T) {
	r := mm.New()

	var panics int64
	r.GET("/panic/:id", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&panics, 1)
		panic("boom-" + mm.PathParam(req, "id"))
	})
	r.GET("/check/:id", func(w http.ResponseWriter, req *http.Request) {
		id := mm.PathParam(req, "id")
		if id == "" {
			t.Errorf("check got empty id")
		}
	})

	const iters = 20000
	for i := range iters {
		p := fmt.Sprintf("/panic/p%d", i)
		// A panic out of ServeHTTP must be recoverable by the caller.
		func() {
			defer func() { _ = recover() }()
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
		}()
		c := fmt.Sprintf("/check/c%d", i)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c, nil))
	}
	t.Logf("panics triggered: %d", atomic.LoadInt64(&panics))
}

// TestPanicWithHandler_PoolRelease exercises the case where PanicHandler IS
// set. dispatchWithRecover catches the panic. The test verifies subsequent
// requests observe clean params.
func TestPanicWithHandler_PoolRelease(t *testing.T) {
	r := mm.New()
	var recovered int64
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		atomic.AddInt64(&recovered, 1)
		http.Error(w, "panic", http.StatusInternalServerError)
	}

	r.GET("/panic/:id", func(w http.ResponseWriter, req *http.Request) {
		panic("boom-" + mm.PathParam(req, "id"))
	})
	var badLen int64
	var badKey int64
	r.GET("/check/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 {
			atomic.AddInt64(&badLen, 1)
			return
		}
		if ps[0].Key != "id" {
			atomic.AddInt64(&badKey, 1)
		}
	})

	const iters = 50000
	for i := range iters {
		p := fmt.Sprintf("/panic/p%d", i)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
		c := fmt.Sprintf("/check/c%d", i)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c, nil))
	}
	if bl := atomic.LoadInt64(&badLen); bl > 0 {
		t.Fatalf("panic+handler: %d requests saw wrong Params length", bl)
	}
	if bk := atomic.LoadInt64(&badKey); bk > 0 {
		t.Fatalf("panic+handler: %d requests saw wrong Param.Key", bk)
	}
	if atomic.LoadInt64(&recovered) < int64(iters) {
		t.Fatalf("panic+handler: only %d/%d panics recovered", recovered, iters)
	}
	t.Logf("panic+handler: recovered %d, len=OK, key=OK", recovered)
}

// TestPanicConcurrent_NoCrossLeak combines panic with concurrent load to stress
// the combination of (a) handler panicking, (b) rc NOT being released on that
// path, (c) concurrent goroutines requesting the pool. We verify that the
// NEXT request on any goroutine continues to see correct params.
func TestPanicConcurrent_NoCrossLeak(t *testing.T) {
	r := mm.New()
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {}

	var bad int64
	r.GET("/p/:a", func(w http.ResponseWriter, req *http.Request) {
		panic(mm.PathParam(req, "a"))
	})
	r.GET("/c/:a", func(w http.ResponseWriter, req *http.Request) {
		want := req.URL.Path[len("/c/"):]
		got := mm.PathParam(req, "a")
		if got != want {
			atomic.AddInt64(&bad, 1)
		}
	})

	const workers, perWorker = 32, 2000
	var done = make(chan struct{}, workers)
	for g := range workers {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := range perWorker {
				p := fmt.Sprintf("/p/w%d-i%d", g, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
				c := fmt.Sprintf("/c/w%d-i%d", g, i)
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c, nil))
			}
		}(g)
	}
	for range workers {
		<-done
	}
	if b := atomic.LoadInt64(&bad); b > 0 {
		t.Fatalf("panic-concurrent: %d cross-request param leaks", b)
	}
}
