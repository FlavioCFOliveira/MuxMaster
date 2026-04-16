// Package muxmaster is a high-performance HTTP request multiplexer for Go.
//
// Routes are matched with a radix (compressed prefix) tree, giving O(k) lookup
// where k is the path length. Zero external dependencies; pure standard library.
//
// Usage:
//
//	r := muxmaster.New()
//	r.Use(logger, auth)          // middleware applied to every route below
//	r.GET("/users", listUsers)
//	r.GET("/users/:id", getUser)
//	r.GET("/static/*filepath", serveFiles)
//
//	api := r.Group("/api/v1")
//	api.Use(apiKeyCheck)
//	api.POST("/items", createItem)
//
//	http.ListenAndServe(":8080", r)
//
// Middleware must be registered (via Use) before the routes it should wrap.
// Dynamic route registration after the server starts serving is not supported.
package muxmaster

import (
	"net/http"
	"path"
	"strings"
	"sync"
)

// Mux is a high-performance HTTP request multiplexer.
type Mux struct {
	trees map[string]*node

	// RedirectTrailingSlash redirects /foo/ → /foo (or /foo → /foo/) when a
	// handler exists at the alternate path. Uses 301 for GET, 307 otherwise.
	RedirectTrailingSlash bool

	// RedirectFixedPath redirects requests whose cleaned path has a handler.
	RedirectFixedPath bool

	// HandleMethodNotAllowed returns 405 with an Allow header when the path
	// exists but not for the requested method.
	HandleMethodNotAllowed bool

	// HandleOPTIONS replies to OPTIONS requests with the Allow header set to
	// all registered methods for the matched path.
	HandleOPTIONS bool

	// NotFound is called when no route matches (default: http.NotFound).
	NotFound http.Handler

	// MethodNotAllowed is called on 405 (default: plain-text response).
	MethodNotAllowed http.Handler

	// PanicHandler, when set, recovers from panics in handlers and receives
	// the ResponseWriter, Request, and recovered value.
	PanicHandler func(http.ResponseWriter, *http.Request, any)

	middleware []func(http.Handler) http.Handler
	mu         sync.RWMutex
}

// New returns a Mux with production-safe defaults enabled.
func New() *Mux {
	return &Mux{
		RedirectTrailingSlash:  true,
		RedirectFixedPath:      true,
		HandleMethodNotAllowed: true,
		HandleOPTIONS:          true,
	}
}

// Use appends one or more middleware to the chain. Each middleware wraps all
// handlers registered after this call. The first middleware added is outermost.
func (m *Mux) Use(middleware ...func(http.Handler) http.Handler) {
	m.middleware = append(m.middleware, middleware...)
}

// Handle registers handler for the given HTTP method and path pattern.
//
// Path parameters use the ':name' syntax (/users/:id).
// Catch-all parameters use '*name' and must end the path (/static/*filepath).
//
// Panics on empty method, non-absolute path, nil handler, or route conflict.
func (m *Mux) Handle(method, pattern string, handler http.Handler) {
	switch {
	case method == "":
		panic("muxmaster: HTTP method must not be empty")
	case len(pattern) == 0 || pattern[0] != '/':
		panic("muxmaster: path must begin with '/' in '" + pattern + "'")
	case handler == nil:
		panic("muxmaster: handler must not be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.trees == nil {
		m.trees = make(map[string]*node)
	}
	root := m.trees[method]
	if root == nil {
		root = new(node)
		m.trees[method] = root
	}

	root.addRoute(pattern, wrapMiddleware(handler, m.middleware))
}

// HandleFunc registers a HandlerFunc for the given method and path.
func (m *Mux) HandleFunc(method, pattern string, h http.HandlerFunc) {
	m.Handle(method, pattern, h)
}

// Shorthand registration methods.

func (m *Mux) GET(pattern string, h http.HandlerFunc)     { m.HandleFunc(http.MethodGet, pattern, h) }
func (m *Mux) HEAD(pattern string, h http.HandlerFunc)    { m.HandleFunc(http.MethodHead, pattern, h) }
func (m *Mux) POST(pattern string, h http.HandlerFunc)    { m.HandleFunc(http.MethodPost, pattern, h) }
func (m *Mux) PUT(pattern string, h http.HandlerFunc)     { m.HandleFunc(http.MethodPut, pattern, h) }
func (m *Mux) PATCH(pattern string, h http.HandlerFunc)   { m.HandleFunc(http.MethodPatch, pattern, h) }
func (m *Mux) DELETE(pattern string, h http.HandlerFunc)  { m.HandleFunc(http.MethodDelete, pattern, h) }
func (m *Mux) OPTIONS(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodOptions, pattern, h) }

// Group returns a RouteGroup whose routes share the given path prefix.
func (m *Mux) Group(prefix string) *Group {
	return &Group{mux: m, prefix: prefix}
}

// ServeHTTP implements http.Handler.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.PanicHandler != nil {
		defer m.recoverPanic(w, r)
	}

	urlPath := r.URL.Path

	m.mu.RLock()
	root := m.trees[r.Method]
	m.mu.RUnlock()

	if root != nil {
		ps := acquireParams()
		handler, tsr := root.getValue(urlPath, ps)

		if handler != nil {
			if len(*ps) > 0 {
				paramsCopy := make(Params, len(*ps))
				copy(paramsCopy, *ps)
				releaseParams(ps)
				r = withParams(r, paramsCopy)
			} else {
				releaseParams(ps)
			}
			handler.ServeHTTP(w, r)
			return
		}
		releaseParams(ps)

		if r.Method != http.MethodConnect && urlPath != "/" {
			code := http.StatusMovedPermanently
			if r.Method != http.MethodGet {
				code = http.StatusTemporaryRedirect
			}

			if tsr && m.RedirectTrailingSlash {
				if len(urlPath) > 1 && urlPath[len(urlPath)-1] == '/' {
					r.URL.Path = urlPath[:len(urlPath)-1]
				} else {
					r.URL.Path = urlPath + "/"
				}
				http.Redirect(w, r, r.URL.String(), code)
				return
			}

			if m.RedirectFixedPath {
				if fixed, ok := m.cleanedPath(root, urlPath); ok {
					r.URL.Path = fixed
					http.Redirect(w, r, r.URL.String(), code)
					return
				}
			}
		}
	}

	if r.Method == http.MethodOptions && m.HandleOPTIONS {
		if allow := m.allowed(urlPath, r.Method); allow != "" {
			w.Header().Set("Allow", allow)
			if m.MethodNotAllowed != nil {
				m.MethodNotAllowed.ServeHTTP(w, r)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
	} else if m.HandleMethodNotAllowed {
		if allow := m.allowed(urlPath, r.Method); allow != "" {
			w.Header().Set("Allow", allow)
			if m.MethodNotAllowed != nil {
				m.MethodNotAllowed.ServeHTTP(w, r)
			} else {
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			}
			return
		}
	}

	if m.NotFound != nil {
		m.NotFound.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}

func (m *Mux) recoverPanic(w http.ResponseWriter, r *http.Request) {
	if rcv := recover(); rcv != nil {
		m.PanicHandler(w, r, rcv)
	}
}

// allowed returns a comma-separated Allow header value for urlPath.
// Returns "" when no methods are registered at that path.
func (m *Mux) allowed(urlPath, reqMethod string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var b strings.Builder
	for method, root := range m.trees {
		if method == reqMethod || method == http.MethodOptions {
			continue
		}
		if root.hasHandler(urlPath) {
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString(method)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteString(", ")
	b.WriteString(http.MethodOptions)
	return b.String()
}

// cleanedPath checks whether path.Clean(p) has a registered handler.
func (m *Mux) cleanedPath(root *node, p string) (string, bool) {
	cleaned := path.Clean(p)
	if cleaned == p {
		return "", false
	}
	ps := acquireParams()
	h, _ := root.getValue(cleaned, ps)
	releaseParams(ps)
	if h != nil {
		return cleaned, true
	}
	return "", false
}

// wrapMiddleware wraps h with each middleware in order (index 0 is outermost).
func wrapMiddleware(h http.Handler, middleware []func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
