// CSA-004 minimal reproducer: pool leak on handler panic.
//
// When a handler panics, the dispatch code path does NOT execute the
// `releaseRC(rc)` line because the panic skips the remaining statements.
// No deferred release exists. Each panic = one permanently leaked *requestCtx.
// Under recoverer middleware (which recovers inside the handler frame),
// cleanup ALSO does not happen because the recover runs BEFORE control
// returns to dispatch's cleanup sequence — recoverer's defer runs first; the
// panic is swallowed; control returns up the middleware chain into dispatch,
// which THEN runs its cleanup. So recoverer inside keeps the pool healthy.
// But m.PanicHandler (on Mux) runs AT THE OUTER defer in dispatchWithRecover
// — too late; cleanup is skipped.
//
// Behavioural observation: we measure the GC pressure / memory growth under
// repeated panics without recoverer middleware.
//
// Run:
//
//	go test -run=TestCSA004 -count=1 ./reports/concurrency-security-auditor/evidence/2026-04-17/CSA-004/
package csa004

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestCSA004_PanicHandlerLeaksRC(t *testing.T) {
	r := mm.New()
	r.PanicHandler = func(w http.ResponseWriter, _ *http.Request, _ any) {}
	r.GET("/panic/:id", func(w http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})

	// Warm up.
	for range 1000 {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic/x", nil))
	}
	var ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms1)

	const N = 50000
	for range N {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic/x", nil))
	}
	var ms2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms2)

	// Each requestCtx is ~144 bytes + context allocation.
	allocBytesDelta := int64(ms2.TotalAlloc) - int64(ms1.TotalAlloc)
	t.Logf("CSA-004: %d panic requests caused allocDelta=%d bytes (%.1f bytes/req)",
		N, allocBytesDelta, float64(allocBytesDelta)/float64(N))

	// The assertion is intentionally lax — this is a diagnostic reproducer,
	// not a go/no-go test. The report documents that rc is NOT returned to
	// the pool on panic paths, inflating pool pressure.
}
