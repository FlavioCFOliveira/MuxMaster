package muxmaster

import (
	"net/http"
	"net/url"
	"strings"
)

// Group is a set of routes sharing a common path prefix and middleware stack.
// Create one via Mux.Group; nest further via Group.Group.
type Group struct {
	mux        *Mux
	prefix     string
	middleware []func(http.Handler) http.Handler
}

// Use appends middleware to this group's chain.
// Must be called before registering routes on the group.
func (g *Group) Use(middleware ...func(http.Handler) http.Handler) {
	g.middleware = append(g.middleware, middleware...)
}

// Handle registers handler under this group with the given method and path.
// The full path is g.prefix + path. Group middleware is applied after mux-level middleware.
func (g *Group) Handle(method, path string, handler http.Handler) {
	g.mux.Handle(method, g.prefix+path, wrapMiddleware(handler, g.middleware))
}

// HandleFunc registers a HandlerFunc under this group.
func (g *Group) HandleFunc(method, path string, h http.HandlerFunc) {
	g.Handle(method, path, h)
}

// HandleE registers a HandlerFuncE under this group.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) HandleE(method, path string, h HandlerFuncE) {
	g.Handle(method, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			if g.mux.ErrorHandler != nil {
				g.mux.ErrorHandler(w, r, err)
			} else {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}
	}))
}

// GET registers a HandlerFunc for GET requests on path.
func (g *Group) GET(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodGet, path, h) }

// HEAD registers a HandlerFunc for HEAD requests on path.
func (g *Group) HEAD(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodHead, path, h) }

// POST registers a HandlerFunc for POST requests on path.
func (g *Group) POST(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodPost, path, h) }

// PUT registers a HandlerFunc for PUT requests on path.
func (g *Group) PUT(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodPut, path, h) }

// PATCH registers a HandlerFunc for PATCH requests on path.
func (g *Group) PATCH(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodPatch, path, h) }

// DELETE registers a HandlerFunc for DELETE requests on path.
func (g *Group) DELETE(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodDelete, path, h) }

// OPTIONS registers a HandlerFunc for OPTIONS requests on path.
func (g *Group) OPTIONS(path string, h http.HandlerFunc) {
	g.HandleFunc(http.MethodOptions, path, h)
}

// CONNECT registers a HandlerFunc for CONNECT requests on path.
func (g *Group) CONNECT(path string, h http.HandlerFunc) {
	g.HandleFunc(http.MethodConnect, path, h)
}

// TRACE registers a HandlerFunc for TRACE requests on path.
func (g *Group) TRACE(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodTrace, path, h) }

// GETE registers a HandlerFuncE for GET requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) GETE(path string, h HandlerFuncE) { g.HandleE(http.MethodGet, path, h) }

// HEADE registers a HandlerFuncE for HEAD requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) HEADE(path string, h HandlerFuncE) { g.HandleE(http.MethodHead, path, h) }

// POSTE registers a HandlerFuncE for POST requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) POSTE(path string, h HandlerFuncE) { g.HandleE(http.MethodPost, path, h) }

// PUTE registers a HandlerFuncE for PUT requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) PUTE(path string, h HandlerFuncE) { g.HandleE(http.MethodPut, path, h) }

// PATCHE registers a HandlerFuncE for PATCH requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) PATCHE(path string, h HandlerFuncE) { g.HandleE(http.MethodPatch, path, h) }

// DELETEE registers a HandlerFuncE for DELETE requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) DELETEE(path string, h HandlerFuncE) { g.HandleE(http.MethodDelete, path, h) }

// OPTIONSE registers a HandlerFuncE for OPTIONS requests on path.
// Errors are passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.
func (g *Group) OPTIONSE(path string, h HandlerFuncE) { g.HandleE(http.MethodOptions, path, h) }

// ANY registers h for all standard HTTP methods on path.
func (g *Group) ANY(path string, h http.HandlerFunc) {
	for _, method := range anyMethods {
		g.HandleFunc(method, path, h)
	}
}

// Match registers handler for each of the listed methods on path.
func (g *Group) Match(methods []string, path string, handler http.Handler) {
	for _, method := range methods {
		g.Handle(method, path, handler)
	}
}

// With returns a copy of this group with additional middleware appended.
func (g *Group) With(mw ...func(http.Handler) http.Handler) *Group {
	mwCopy := make([]func(http.Handler) http.Handler, len(g.middleware)+len(mw))
	copy(mwCopy, g.middleware)
	copy(mwCopy[len(g.middleware):], mw)
	return &Group{mux: g.mux, prefix: g.prefix, middleware: mwCopy}
}

// Group returns a sub-group sharing the same mux with an extended prefix.
// The sub-group starts with a copy of the parent group's middleware.
func (g *Group) Group(prefix string) *Group {
	mw := make([]func(http.Handler) http.Handler, len(g.middleware))
	copy(mw, g.middleware)
	return &Group{
		mux:        g.mux,
		prefix:     g.prefix + prefix,
		middleware: mw,
	}
}

// Route creates a sub-group at prefix and calls fn with it.
func (g *Group) Route(prefix string, fn func(*Group)) {
	sub := g.Group(prefix)
	fn(sub)
}

// Mount attaches h at g.prefix+prefix, stripping the full prefix before forwarding.
func (g *Group) Mount(prefix string, h http.Handler) {
	g.mux.mountAt(g.prefix+prefix, h)
}

// ServeFiles serves static files from root under the given prefix pattern.
// prefix must end with "/*name" (relative to the group prefix).
func (g *Group) ServeFiles(prefix string, root http.FileSystem) {
	if root == nil {
		panic("muxmaster: nil root passed to ServeFiles")
	}
	fullPrefix := g.prefix + prefix
	i := strings.LastIndex(fullPrefix, "/*")
	if i < 0 {
		panic("muxmaster: ServeFiles prefix must end with '/*name': " + fullPrefix)
	}
	paramName := fullPrefix[i+2:]
	if paramName == "" {
		panic("muxmaster: ServeFiles prefix must end with '/*name': " + fullPrefix)
	}
	fs := http.FileServer(root)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = PathParam(r, paramName)
		fs.ServeHTTP(w, r2)
	})
	g.Handle(http.MethodGet, prefix, h)
	g.Handle(http.MethodHead, prefix, h)
}
