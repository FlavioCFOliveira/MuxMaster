package muxmaster

import "net/http"

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
// The full path is g.prefix + path. Group middleware is applied innermost
// (after mux-level middleware).
func (g *Group) Handle(method, path string, handler http.Handler) {
	g.mux.Handle(method, g.prefix+path, wrapMiddleware(handler, g.middleware))
}

// HandleFunc registers a HandlerFunc under this group.
func (g *Group) HandleFunc(method, path string, h http.HandlerFunc) {
	g.Handle(method, path, h)
}

// Shorthand registration methods.

func (g *Group) GET(path string, h http.HandlerFunc)     { g.HandleFunc(http.MethodGet, path, h) }
func (g *Group) HEAD(path string, h http.HandlerFunc)    { g.HandleFunc(http.MethodHead, path, h) }
func (g *Group) POST(path string, h http.HandlerFunc)    { g.HandleFunc(http.MethodPost, path, h) }
func (g *Group) PUT(path string, h http.HandlerFunc)     { g.HandleFunc(http.MethodPut, path, h) }
func (g *Group) PATCH(path string, h http.HandlerFunc)   { g.HandleFunc(http.MethodPatch, path, h) }
func (g *Group) DELETE(path string, h http.HandlerFunc)  { g.HandleFunc(http.MethodDelete, path, h) }
func (g *Group) OPTIONS(path string, h http.HandlerFunc) { g.HandleFunc(http.MethodOptions, path, h) }

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
