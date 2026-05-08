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
	mux := muxmaster.New()
	mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		id := muxmaster.PathParam(r, "id")
		fmt.Fprintf(w, "user=%s", id)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// user=42
}

// ExampleMux_Group demonstrates grouping routes under a shared prefix.
func ExampleMux_Group() {
	mux := muxmaster.New()

	api := mux.Group("/api/v1")
	api.GET("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "pong")
	})
	api.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		id := muxmaster.PathParam(r, "id")
		fmt.Fprintf(w, "user=%s", id)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	mux.ServeHTTP(rec, req)

	fmt.Print(rec.Body.String())
	// Output:
	// pong
}

// ExampleMux_Use demonstrates applying middleware to all routes registered after
// the Use call. Middleware must be registered before the routes it should wrap.
func ExampleMux_Use() {
	mux := muxmaster.New()

	// addHeader is a simple middleware that injects a response header.
	addHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Powered-By", "MuxMaster")
			next.ServeHTTP(w, r)
		})
	}

	mux.Use(addHeader)
	mux.GET("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Header().Get("X-Powered-By"))
	// Output:
	// MuxMaster
}

// ExampleMux_Mount demonstrates mounting a sub-router at a path prefix.
// The prefix is stripped before the request reaches the sub-router.
func ExampleMux_Mount() {
	sub := muxmaster.New()
	sub.GET("/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	mux := muxmaster.New()
	mux.Mount("/v2", sub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/status", nil)
	mux.ServeHTTP(rec, req)

	fmt.Print(rec.Body.String())
	// Output:
	// ok
}

// ExampleParamsFromContext demonstrates reading path parameters from a context
// directly, without access to the *http.Request.
func ExampleParamsFromContext() {
	mux := muxmaster.New()
	mux.GET("/posts/:year/:slug", func(w http.ResponseWriter, r *http.Request) {
		ps := muxmaster.ParamsFromContext(r.Context())
		fmt.Fprintf(w, "year=%s slug=%s", ps.Get("year"), ps.Get("slug"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/posts/2024/hello-world", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// year=2024 slug=hello-world
}

// ExampleMux_HandleFast demonstrates the FastHandler API for ultra-low-latency
// routes. Params are passed directly as the third argument, avoiding the
// context allocation cost of stdlib http.Handler routes.
func ExampleMux_HandleFast() {
	mux := muxmaster.New()
	mux.HandleFast(http.MethodGet, "/users/:id", func(w http.ResponseWriter, _ *http.Request, ps muxmaster.Params) {
		fmt.Fprintf(w, "fast user=%s", ps.Get("id"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/99", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// fast user=99
}

// ExamplePathParam demonstrates reading a path parameter from a request.
func ExamplePathParam() {
	mux := muxmaster.New()
	mux.GET("/posts/:slug", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "post=%s", muxmaster.PathParam(r, "slug"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/posts/hello-world", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// post=hello-world
}

// ExampleParams_Int demonstrates parsing a path parameter as an int directly.
func ExampleParams_Int() {
	mux := muxmaster.New()
	mux.GET("/items/:n", func(w http.ResponseWriter, r *http.Request) {
		ps := muxmaster.ParamsFromContext(r.Context())
		n, err := ps.Int("n")
		if err != nil {
			fmt.Fprintf(w, "bad n: %v", err)
			return
		}
		fmt.Fprintf(w, "n=%d", n*2)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items/21", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// n=42
}

// ExampleJSON demonstrates writing a JSON response with a status code.
func ExampleJSON() {
	mux := muxmaster.New()
	mux.GET("/api/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = muxmaster.JSON(w, http.StatusOK, map[string]string{"v": "1"})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output:
	// {"v":"1"}
}

// ExampleMux_Pre demonstrates registering Pre middleware that wraps both
// stdlib (Handle) and fast (HandleFast) routes.
func ExampleMux_Pre() {
	mux := muxmaster.New()
	mux.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Pre", "yes")
			next.ServeHTTP(w, r)
		})
	})
	mux.GET("/x", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	fmt.Println(rec.Header().Get("X-Pre"))
	// Output:
	// yes
}
