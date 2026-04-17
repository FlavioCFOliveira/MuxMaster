package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// ExampleMux_GET demonstrates registering a GET route with a path parameter
// and reading it with PathParam.
func ExampleMux_GET() {
	r := muxmaster.New()
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		id := muxmaster.PathParam(req, "id")
		fmt.Fprintf(w, "user=%s", id)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// user=42
}

// ExampleMux_Group demonstrates grouping routes under a shared prefix.
func ExampleMux_Group() {
	r := muxmaster.New()

	api := r.Group("/api/v1")
	api.GET("/ping", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "pong")
	})
	api.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		id := muxmaster.PathParam(req, "id")
		fmt.Fprintf(w, "user=%s", id)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	r.ServeHTTP(rec, req)

	fmt.Print(rec.Body.String())
	// Output:
	// pong
}

// ExampleMux_Use demonstrates applying middleware to all routes registered after
// the Use call. Middleware must be registered before the routes it should wrap.
func ExampleMux_Use() {
	r := muxmaster.New()

	// addHeader is a simple middleware that injects a response header.
	addHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Powered-By", "MuxMaster")
			next.ServeHTTP(w, req)
		})
	}

	r.Use(addHeader)
	r.GET("/hello", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "hello")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Header().Get("X-Powered-By"))
	// Output:
	// MuxMaster
}

// ExampleMux_Mount demonstrates mounting a sub-router at a path prefix.
// The prefix is stripped before the request reaches the sub-router.
func ExampleMux_Mount() {
	sub := muxmaster.New()
	sub.GET("/status", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	r := muxmaster.New()
	r.Mount("/v2", sub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/status", nil)
	r.ServeHTTP(rec, req)

	fmt.Print(rec.Body.String())
	// Output:
	// ok
}

// ExampleParamsFromContext demonstrates reading path parameters from a context
// directly, without access to the *http.Request.
func ExampleParamsFromContext() {
	r := muxmaster.New()
	r.GET("/posts/:year/:slug", func(w http.ResponseWriter, req *http.Request) {
		ps := muxmaster.ParamsFromContext(req.Context())
		fmt.Fprintf(w, "year=%s slug=%s", ps.Get("year"), ps.Get("slug"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/posts/2024/hello-world", nil)
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// year=2024 slug=hello-world
}
