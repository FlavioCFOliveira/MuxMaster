package fuzz

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	chi "github.com/go-chi/chi/v5"
	httprouter "github.com/julienschmidt/httprouter"
	bunrouter "github.com/uptrace/bunrouter"
)

// The following patterns are registered on every test router, so any
// divergence is attributable only to the routing algorithm.
//
//	GET  /admin
//	GET  /admin/panel
//	GET  /admin/panel/settings
//	GET  /users/:id
//	GET  /users/:id/posts
//	GET  /users/:id/posts/:postID
//	GET  /static/*filepath
//	GET  /api/v1/items/:id
//	GET  /api/v1/items/:id/children/:cid
//
// Routers that support regex/optional params are not given patterns that
// exercise those features — that would mask the universal tree semantics.

type routerKind int

const (
	rkMuxMaster routerKind = iota
	rkHTTPRouter
	rkChi
	rkBunRouter
	rkCount
)

var routerName = map[routerKind]string{
	rkMuxMaster:  "muxmaster",
	rkHTTPRouter: "httprouter",
	rkChi:        "chi",
	rkBunRouter:  "bunrouter",
}

// handlerTag responds with the symbolic id it was configured with.
func handlerTag(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", id)
		w.WriteHeader(200)
	}
}

// buildMuxMaster builds a MuxMaster with the differential surface and the
// given toggles.
func buildMuxMaster(redirectTS, redirectFP, caseInsensitive, unescape, useRaw bool) *mm.Mux {
	r := mm.New()
	r.RedirectTrailingSlash = redirectTS
	r.RedirectFixedPath = redirectFP
	r.CaseInsensitive = caseInsensitive
	r.UnescapePathValues = unescape
	r.UseRawPath = useRaw

	r.GET("/admin", handlerTag("admin"))
	r.GET("/admin/panel", handlerTag("admin.panel"))
	r.GET("/admin/panel/settings", handlerTag("admin.panel.settings"))
	r.GET("/users/:id", handlerTag("user"))
	r.GET("/users/:id/posts", handlerTag("user.posts"))
	r.GET("/users/:id/posts/:postID", handlerTag("user.post"))
	r.GET("/static/*filepath", handlerTag("catchall.static"))
	r.GET("/api/v1/items/:id", handlerTag("api.item"))
	r.GET("/api/v1/items/:id/children/:cid", handlerTag("api.item.child"))
	return r
}

// buildHTTPRouter mirrors buildMuxMaster for julienschmidt/httprouter.
func buildHTTPRouter() *httprouter.Router {
	r := httprouter.New()
	r.RedirectTrailingSlash = true
	r.RedirectFixedPath = true
	tag := func(id string) httprouter.Handle {
		return func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
			w.Header().Set("X-Handler", id)
			w.WriteHeader(200)
		}
	}
	r.GET("/admin", tag("admin"))
	r.GET("/admin/panel", tag("admin.panel"))
	r.GET("/admin/panel/settings", tag("admin.panel.settings"))
	r.GET("/users/:id", tag("user"))
	r.GET("/users/:id/posts", tag("user.posts"))
	r.GET("/users/:id/posts/:postID", tag("user.post"))
	r.GET("/static/*filepath", tag("catchall.static"))
	r.GET("/api/v1/items/:id", tag("api.item"))
	r.GET("/api/v1/items/:id/children/:cid", tag("api.item.child"))
	return r
}

// buildChi mirrors buildMuxMaster for go-chi/chi.
func buildChi() *chi.Mux {
	r := chi.NewRouter()
	tag := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Handler", id)
			w.WriteHeader(200)
		}
	}
	r.Get("/admin", tag("admin"))
	r.Get("/admin/panel", tag("admin.panel"))
	r.Get("/admin/panel/settings", tag("admin.panel.settings"))
	r.Get("/users/{id}", tag("user"))
	r.Get("/users/{id}/posts", tag("user.posts"))
	r.Get("/users/{id}/posts/{postID}", tag("user.post"))
	r.Get("/static/*", tag("catchall.static"))
	r.Get("/api/v1/items/{id}", tag("api.item"))
	r.Get("/api/v1/items/{id}/children/{cid}", tag("api.item.child"))
	return r
}

// buildBunRouter mirrors buildMuxMaster for uptrace/bunrouter Compat.
func buildBunRouter() *bunrouter.CompatRouter {
	r := bunrouter.New().Compat()
	tag := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Handler", id)
			w.WriteHeader(200)
		}
	}
	r.GET("/admin", tag("admin"))
	r.GET("/admin/panel", tag("admin.panel"))
	r.GET("/admin/panel/settings", tag("admin.panel.settings"))
	r.GET("/users/:id", tag("user"))
	r.GET("/users/:id/posts", tag("user.posts"))
	r.GET("/users/:id/posts/:postID", tag("user.post"))
	r.GET("/static/*filepath", tag("catchall.static"))
	r.GET("/api/v1/items/:id", tag("api.item"))
	r.GET("/api/v1/items/:id/children/:cid", tag("api.item.child"))
	return r
}

// buildRequest constructs an *http.Request without going through
// httptest.NewRequest, which chokes on many payloads (spaces, %u escapes,
// ASCII control bytes) long before the router is reached. We bypass the
// request-line parser and set fields manually.
func buildRequest(rawPath string) *http.Request {
	u, err := url.Parse("http://example.test" + rawPath)
	if err != nil {
		return nil
	}
	return &http.Request{
		Method:     http.MethodGet,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Host:       "example.test",
		Header:     http.Header{},
		RequestURI: rawPath,
	}
}

// runThroughRouter routes the input path through the given handler and
// normalises the response into a routeResult.
func runThroughRouter(h http.Handler, rawPath string) routeResult {
	defer func() { _ = recover() }()
	req := buildRequest(rawPath)
	if req == nil {
		return routeResult{}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	return routeResult{
		matched:   res.StatusCode == 200,
		status:    res.StatusCode,
		location:  res.Header.Get("Location"),
		handlerID: strings.TrimSpace(res.Header.Get("X-Handler")),
	}
}

// routeAll dispatches path through every router configured with safe
// defaults and returns a slice indexed by routerKind.
func routeAll(path string) [rkCount]routeResult {
	var out [rkCount]routeResult
	out[rkMuxMaster] = runThroughRouter(buildMuxMaster(true, true, false, false, false), path)
	out[rkHTTPRouter] = runThroughRouter(buildHTTPRouter(), path)
	out[rkChi] = runThroughRouter(buildChi(), path)
	out[rkBunRouter] = runThroughRouter(buildBunRouter(), path)
	return out
}
