package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// nopHandler is a zero-cost http.Handler used in benchmarks.
var nopHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {}

// newBenchMux builds a realistic mux for benchmarks.
func newBenchMux() *muxmaster.Mux {
	m := muxmaster.New()

	// Static routes
	m.GET("/", nopHandler)
	m.GET("/users", nopHandler)
	m.GET("/users/list", nopHandler)
	m.GET("/users/search", nopHandler)
	m.POST("/users", nopHandler)
	m.GET("/products", nopHandler)
	m.GET("/products/featured", nopHandler)
	m.POST("/products", nopHandler)
	m.GET("/health", nopHandler)
	m.GET("/metrics", nopHandler)

	// Param routes
	m.GET("/users/:id", nopHandler)
	m.PUT("/users/:id", nopHandler)
	m.DELETE("/users/:id", nopHandler)
	m.GET("/users/:id/posts", nopHandler)
	m.GET("/users/:id/posts/:pid", nopHandler)
	m.GET("/products/:id", nopHandler)
	m.PUT("/products/:id", nopHandler)
	m.GET("/orgs/:org/repos/:repo/issues/:num", nopHandler)

	// Wildcard routes
	m.GET("/static/*filepath", nopHandler)
	m.GET("/docs/*path", nopHandler)

	return m
}

var benchReq = func(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

// BenchmarkStaticRoute measures lookup of a route with no parameters.
func BenchmarkStaticRoute(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/list")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute1 measures lookup with one path parameter.
func BenchmarkParamRoute1(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute2 measures lookup with two path parameters.
func BenchmarkParamRoute2(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42/posts/7")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParamRoute3 measures lookup with three path parameters.
func BenchmarkParamRoute3(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/orgs/acme/repos/api/issues/123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkWildcardRoute measures lookup with a catch-all parameter.
func BenchmarkWildcardRoute(b *testing.B) {
	m := newBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/static/css/main.min.css")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkNotFound measures 404 path.
func BenchmarkNotFound(b *testing.B) {
	m := newBenchMux()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = false
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/this/path/does/not/exist")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkParallelStaticRoute measures concurrent static route lookup.
func BenchmarkParallelStaticRoute(b *testing.B) {
	m := newBenchMux()
	r := benchReq(http.MethodGet, "/products/featured")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// BenchmarkParallelParamRoute measures concurrent param route lookup.
func BenchmarkParallelParamRoute(b *testing.B) {
	m := newBenchMux()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}

// newFastBenchMux builds a Mux using HandleFast for the benchmarked routes.
func newFastBenchMux() *muxmaster.Mux {
	nopFast := func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {}
	m := muxmaster.New()

	// Static routes — same as newBenchMux
	m.GET("/", nopHandler)
	m.GET("/users", nopHandler)
	m.GET("/users/list", nopHandler)
	m.GET("/users/search", nopHandler)
	m.POST("/users", nopHandler)
	m.GET("/products", nopHandler)
	m.GET("/products/featured", nopHandler)
	m.POST("/products", nopHandler)
	m.GET("/health", nopHandler)
	m.GET("/metrics", nopHandler)

	// Param routes registered as FastHandler
	m.GETFast("/users/:id", nopFast)
	m.PUTFast("/users/:id", nopFast)
	m.DELETEFast("/users/:id", nopFast)
	m.GETFast("/users/:id/posts", nopFast)
	m.GETFast("/users/:id/posts/:pid", nopFast)
	m.GETFast("/products/:id", nopFast)
	m.PUTFast("/products/:id", nopFast)
	m.GETFast("/orgs/:org/repos/:repo/issues/:num", nopFast)

	// Wildcard routes as FastHandler
	m.GETFast("/static/*filepath", nopFast)
	m.GETFast("/docs/*path", nopFast)

	return m
}

// BenchmarkFastStaticRoute measures ServeHTTP for a static fast route.
// Static routes use the same path as regular Handle routes — 0 allocs.
func BenchmarkFastStaticRoute(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/list")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute1 measures HandleFast dispatch with one path parameter.
// Target: ≤58 ns, 1 alloc ~64 B.
func BenchmarkFastParamRoute1(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute2 measures HandleFast dispatch with two path parameters.
func BenchmarkFastParamRoute2(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/users/42/posts/7")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParamRoute3 measures HandleFast dispatch with three path parameters.
func BenchmarkFastParamRoute3(b *testing.B) {
	m := newFastBenchMux()
	w := httptest.NewRecorder()
	r := benchReq(http.MethodGet, "/orgs/acme/repos/api/issues/123")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// BenchmarkFastParallelParamRoute measures concurrent HandleFast param dispatch.
func BenchmarkFastParallelParamRoute(b *testing.B) {
	m := newFastBenchMux()
	r := benchReq(http.MethodGet, "/users/42")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := httptest.NewRecorder()
		for pb.Next() {
			m.ServeHTTP(w, r)
		}
	})
}
