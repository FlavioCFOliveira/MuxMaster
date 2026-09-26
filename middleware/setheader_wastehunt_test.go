// Regression test for the sprint 18 waste-hunt security review of WH-05
// (SetHeader's prebuilt header value): MID-SETHEADER-1.
//
// The WH-05 optimisation canonicalised the header key once at middleware
// construction time (still safe — it never changes) but also hoisted the
// single-element []string{value} backing the header value into that same
// closure, sharing ONE slice across every request the middleware instance
// ever serves. http.Header.Set/Add/Del always install a brand new slice, so
// they cannot observe this; but any code that touches the slice by index —
// w.Header()[key][0] = ... — mutates the shared backing array in place,
// corrupting the value for every other request (past and future) through
// that middleware instance until process restart. This is a real
// cross-request contamination vector for exactly the kind of header
// SetHeader is normally used to pin (CSP, HSTS, X-Frame-Options, a fixed
// CORS allow-origin, ...).
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestSetHeader_InPlaceSliceMutation_DoesNotLeakAcrossRequests drives one
// request whose handler mutates the header value slice by index, then a
// second, unrelated request through the SAME middleware instance whose
// handler touches nothing. The second request must still see the value
// SetHeader was configured with.
func TestSetHeader_InPlaceSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	const key = "X-Fixed"
	const original = "original-value"
	mw := middleware.SetHeader(key, original)

	mutating := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A valid, if unusual, way to touch http.Header: index directly into
		// the slice rather than going through Set/Add/Del.
		if v := w.Header()[key]; len(v) == 1 {
			v[0] = "corrupted-by-request-1"
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec1 := httptest.NewRecorder()
	mutating.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec1.Header().Get(key); got != "corrupted-by-request-1" {
		t.Fatalf("request 1 (the one doing the mutation) header = %q, want %q", got, "corrupted-by-request-1")
	}

	clean := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec2 := httptest.NewRecorder()
	clean.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec2.Header().Get(key); got != original {
		t.Fatalf("request 2 header = %q, want %q — request 1's in-place mutation leaked through a shared slice (MID-SETHEADER-1)", got, original)
	}

	// A third, fully independent request confirms the middleware has fully
	// recovered — not just "one clean request after the corrupting one".
	rec3 := httptest.NewRecorder()
	clean.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec3.Header().Get(key); got != original {
		t.Fatalf("request 3 header = %q, want %q", got, original)
	}
}

// TestSetHeader_ConcurrentRequests_EachGetsIndependentSlice hammers the same
// SetHeader instance concurrently, each goroutine mutating its own
// response's header slice by index to a distinct value, and asserts every
// goroutine observes only ITS OWN write — proving requests are no longer
// sharing one backing array under concurrency (a race in the shared-slice
// design manifests as one goroutine's mutation showing up on another's
// recorder, non-deterministically).
func TestSetHeader_ConcurrentRequests_EachGetsIndependentSlice(t *testing.T) {
	const key = "X-Fixed"
	mw := middleware.SetHeader(key, "default")

	const n = 200
	results := make([]string, n)
	done := make(chan int, n)
	for i := range n {
		go func(i int) {
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if v := w.Header()[key]; len(v) == 1 {
					v[0] = "value-from-goroutine"
				}
				w.WriteHeader(http.StatusOK)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			results[i] = rec.Header().Get(key)
			done <- i
		}(i)
	}
	for range n {
		<-done
	}
	for i, got := range results {
		if got != "value-from-goroutine" {
			t.Fatalf("goroutine %d: header = %q, want %q", i, got, "value-from-goroutine")
		}
	}
}
