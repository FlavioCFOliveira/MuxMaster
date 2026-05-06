package dosharness

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// BenchmarkNotFoundAllocationCost measures the per-request allocation count
// on the 404 path. This is relevant because an attacker can flood 404s
// cheaper than real routes if they cost less; or more expensive if alloc
// churn is higher.
//
// Baseline (established from root bench_test.go):
//
//	StaticRoute — 24 ns, 0 allocs
//	NotFound    — 250 ns, 3 allocs, 105 B
//
// So a 404 costs 10x the ns AND 3 allocs where a static route is zero.
// Under a 404 flood from an attacker, GC pressure grows while legit routes
// are starved. This quantifies the amplification.
func BenchmarkNotFoundAmplification(b *testing.B) {
	r := mm.New()
	// Register a handful of routes so allowed() has work to do.
	r.GET("/users/:id", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r.POST("/users/:id", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r.PUT("/users/:id", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist-"+strconv.Itoa(0), nil)
	w := httptest.NewRecorder()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		r.ServeHTTP(w, req)
	}
}

// BenchmarkMethodNotAllowedAmplification: attacker targets an existing path
// with the wrong method. This forces allowed() to walk every tree — cost
// scales with number of registered methods + route depth.
func BenchmarkMethodNotAllowedAmplification(b *testing.B) {
	r := mm.New()
	// 9 methods all registered for /target — worst case for allowed().
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace,
	}
	for _, m := range methods {
		r.Handle(m, "/target", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}

	// Custom method to trigger the 405 path.
	req := httptest.NewRequest("FROBNICATE", "/target", nil)
	w := httptest.NewRecorder()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		r.ServeHTTP(w, req)
	}
}

// BenchmarkTSRRedirectCost measures the cost of RedirectTrailingSlash path
// (this emits 301 BEFORE any middleware runs — important for H-025).
func BenchmarkTSRRedirectCost(b *testing.B) {
	r := mm.New()
	r.GET("/admin/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil) // missing trailing /
	w := httptest.NewRecorder()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		// Reset w between iterations to avoid header bloat from previous runs.
		if b.N < 2 {
			r.ServeHTTP(w, req)
		} else {
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}
	}
}
