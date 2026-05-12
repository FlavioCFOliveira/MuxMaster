package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// BenchmarkNotFoundDetailed mede cada componente do NotFound path.

// Sem qualquer redirect, sem handler customizado — apenas o lazyNotFound.
func BenchmarkNotFoundClean(b *testing.B) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = false
	m.HandleOPTIONS = false
	m.GET("/known", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nope", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// Igual mas com NotFound handler customizado simples (no allocs).
func BenchmarkNotFoundCustomHandler(b *testing.B) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = false
	m.HandleOPTIONS = false
	m.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	m.GET("/known", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nope", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// NotFound com HandleMethodNotAllowed=true (causa allowed() lookup).
func BenchmarkNotFoundWithMethodAllowedLookup(b *testing.B) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = true
	m.HandleOPTIONS = false
	m.GET("/known", func(w http.ResponseWriter, r *http.Request) {})
	m.POST("/known", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nope", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// MethodNotAllowed real (path existe, método não).
func BenchmarkMethodNotAllowed(b *testing.B) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = true
	m.GET("/path", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/path", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// OPTIONS handling auto.
func BenchmarkOPTIONSAuto(b *testing.B) {
	m := muxmaster.New()
	m.HandleOPTIONS = true
	m.GET("/path", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/path", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// Group registration overhead (registo NÃO hot path).
func BenchmarkGroupDispatch(b *testing.B) {
	m := muxmaster.New()
	g := m.Group("/api/v1")
	g.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/users/42", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// Nested Group dispatch.
func BenchmarkNestedGroupDispatch(b *testing.B) {
	m := muxmaster.New()
	api := m.Group("/api")
	v1 := api.Group("/v1")
	users := v1.Group("/users")
	users.GET("/:id", func(w http.ResponseWriter, r *http.Request) {})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/users/42", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
}

// RedirectTrailingSlash path (alloc 1: closure + alloc 2: target string).
func BenchmarkRedirectTSL(b *testing.B) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/users", func(w http.ResponseWriter, r *http.Request) {})

	r := httptest.NewRequest(http.MethodGet, "/users/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
	}
}

// PathParam lookup cost.
func BenchmarkPathParamLookup(b *testing.B) {
	m := muxmaster.New()
	var captured string
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		captured = muxmaster.PathParam(r, "id")
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
	_ = captured
}

// PathParam with FastHandler (zero-cost params).
func BenchmarkPathParamFast(b *testing.B) {
	m := muxmaster.New()
	var captured string
	m.GETFast("/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		captured = ps.Get("id")
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
	_ = captured
}

// ParamsFromContext lookup cost.
func BenchmarkParamsFromContext(b *testing.B) {
	m := muxmaster.New()
	var captured muxmaster.Params
	m.GET("/users/:id/posts/:pid", func(w http.ResponseWriter, r *http.Request) {
		captured = muxmaster.ParamsFromContext(r.Context())
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/42/posts/7", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
	}
	_ = captured
}
