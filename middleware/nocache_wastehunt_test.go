// Regression tests for the sprint 18 waste-hunt scan of the middleware
// package for the MID-SETHEADER-1 aliasing class (see
// reports/middleware-security-reviewer/2026-09-25-sprint18-wastehunt.md and
// cors_wastehunt_test.go). NoCache's Opt L3 hoisted five single-element
// []string header values into PACKAGE-LEVEL variables, shared across EVERY
// NoCache() instance and EVERY request in the process — an even wider blast
// radius than CORS's per-instance closures. Any code that indexes directly
// into one of these slices — w.Header()[k][0] = ... — corrupts that header
// for every other request served by ANY NoCache() middleware, anywhere in
// the process, until restart.
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestNoCache_HeaderSliceMutation_DoesNotLeakAcrossInstances drives request 1
// through one NoCache() instance, mutates every one of its five headers by
// index, then drives request 2 through a SEPARATE NoCache() instance and
// confirms none of the corruption is visible — proving the vars are not
// just instance-shared but process-wide-shared, and the fix must not rely on
// per-instance isolation.
func TestNoCache_HeaderSliceMutation_DoesNotLeakAcrossInstances(t *testing.T) {
	want := map[string]string{
		"Cache-Control":     "no-store, no-cache, must-revalidate",
		"Pragma":            "no-cache",
		"Expires":           "0",
		"Surrogate-Control": "no-store",
		"X-Accel-Expires":   "0",
	}

	h1 := middleware.NoCache()(http.HandlerFunc(nopHandler))
	rec1 := httptest.NewRecorder()
	h1.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	for k, v := range want {
		if got := rec1.Header().Get(k); got != v {
			t.Fatalf("request 1 %s = %q, want %q", k, got, v)
		}
		mutateFirstCORSHeaderValue(rec1, k, "corrupted-"+k)
	}

	h2 := middleware.NoCache()(http.HandlerFunc(nopHandler))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	for k, v := range want {
		if got := rec2.Header().Get(k); got != v {
			t.Fatalf("request 2 (separate NoCache() instance) %s = %q, want %q — request 1's in-place mutation leaked through a package-level shared slice", k, got, v)
		}
	}
}

// TestNoCache_ConcurrentRequests_EachGetsIndependentCacheControlSlice
// hammers NoCache() concurrently, each goroutine mutating its own response's
// Cache-Control slice by index, and asserts every goroutine observes only
// its own write.
func TestNoCache_ConcurrentRequests_EachGetsIndependentCacheControlSlice(t *testing.T) {
	h := middleware.NoCache()(http.HandlerFunc(nopHandler))

	const n = 200
	results := make([]string, n)
	done := make(chan int, n)
	for i := range n {
		go func(i int) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			mutateFirstCORSHeaderValue(rec, "Cache-Control", "value-from-goroutine")
			results[i] = rec.Header().Get("Cache-Control")
			done <- i
		}(i)
	}
	for range n {
		<-done
	}
	for i, got := range results {
		if got != "value-from-goroutine" {
			t.Fatalf("goroutine %d: Cache-Control = %q, want %q", i, got, "value-from-goroutine")
		}
	}
}
