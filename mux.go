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
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
)

// anyMethods is the full set of HTTP methods registered by ANY and Group.ANY.
var anyMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
	http.MethodConnect, http.MethodTrace,
}

// Method index constants — replace the map[string]*node lookup with an O(1) array access.
const (
	idxGET      = 0
	idxHEAD     = 1
	idxPOST     = 2
	idxPUT      = 3
	idxPATCH    = 4
	idxDELETE   = 5
	idxOPTIONS  = 6
	idxCONNECT  = 7
	idxTRACE    = 8
	idxWild     = 9 // "*" — used by Mount
	methodCount = 10
)

// methodNames maps an index back to the HTTP method string.
var methodNames = [methodCount]string{
	idxGET:     http.MethodGet,
	idxHEAD:    http.MethodHead,
	idxPOST:    http.MethodPost,
	idxPUT:     http.MethodPut,
	idxPATCH:   http.MethodPatch,
	idxDELETE:  http.MethodDelete,
	idxOPTIONS: http.MethodOptions,
	idxCONNECT: http.MethodConnect,
	idxTRACE:   http.MethodTrace,
	idxWild:    "*",
}

// methodTrees holds one radix tree root per HTTP method.
// Loaded atomically from treesPtr on every request — no lock needed.
type methodTrees [methodCount]*node

// methodIdx returns the array index for a standard HTTP method, or -1.
func methodIdx(m string) int {
	switch m {
	case http.MethodGet:
		return idxGET
	case http.MethodHead:
		return idxHEAD
	case http.MethodPost:
		return idxPOST
	case http.MethodPut:
		return idxPUT
	case http.MethodPatch:
		return idxPATCH
	case http.MethodDelete:
		return idxDELETE
	case http.MethodOptions:
		return idxOPTIONS
	case http.MethodConnect:
		return idxCONNECT
	case http.MethodTrace:
		return idxTRACE
	case "*":
		return idxWild
	default:
		return -1
	}
}

// muxConfig is a frozen snapshot of Mux configuration flags, captured on the
// first ServeHTTP call. Changes to Mux fields after first use are ignored.
// Use Rebuild to reset the snapshot.
type muxConfig struct {
	useRawPath             bool
	caseInsensitive        bool
	unescapePathValues     bool
	redirectTrailingSlash  bool
	redirectFixedPath      bool
	handleMethodNotAllowed bool
	handleOPTIONS          bool
	hasPanicHandler        bool
	redirectCode           int

	// Snapshotted public handler fields (CSA-2026-0052). Reads from these
	// frozen copies replace direct m.NotFound / m.MethodNotAllowed /
	// m.GlobalOPTIONS / m.PanicHandler / m.ErrorHandler reads in the
	// dispatch path, eliminating the data race against post-startup
	// mutation.
	notFound         http.Handler
	methodNotAllowed http.Handler
	globalOPTIONS    http.Handler
	panicHandler     func(http.ResponseWriter, *http.Request, any)
	errorHandler     func(http.ResponseWriter, *http.Request, error)
}

// Mux is a high-performance HTTP request multiplexer.
//
// Configuration fields (RedirectTrailingSlash, CaseInsensitive, etc.) are
// read once and frozen on the first ServeHTTP call. Use Rebuild to reset
// the snapshot when changing flags after the server has started serving.
type Mux struct {
	// treesPtr is loaded atomically on every request — no lock needed after startup.
	// Written only during route registration under mu (copy-on-write).
	treesPtr atomic.Pointer[methodTrees]

	// cfg is the frozen config snapshot, populated on the first ServeHTTP call.
	// Initialisation is single-shot via atomic CAS — no sync.Once is needed
	// because the CAS is itself the once-guard, and Rebuild() resets the
	// snapshot via a single atomic Store(nil), avoiding the struct-write race
	// that a sync.Once reset would introduce.
	cfg atomic.Pointer[muxConfig]

	// RedirectTrailingSlash redirects /foo/ → /foo (or /foo → /foo/) when a
	// handler exists at the alternate path.
	RedirectTrailingSlash bool

	// RedirectFixedPath redirects requests whose cleaned path has a handler.
	RedirectFixedPath bool

	// HandleMethodNotAllowed returns 405 with an Allow header when the path
	// exists but not for the requested method.
	HandleMethodNotAllowed bool

	// HandleOPTIONS replies to OPTIONS requests with the Allow header set to
	// all registered methods for the matched path.
	HandleOPTIONS bool

	// CaseInsensitive enables case-insensitive route matching for static segments.
	CaseInsensitive bool

	// UseRawPath uses r.URL.RawPath for matching when set and non-empty.
	UseRawPath bool

	// UnescapePathValues percent-decodes path parameter values before storing them.
	// Defaults to false — opt in explicitly if you need it.
	UnescapePathValues bool

	// RedirectCode overrides the default redirect status code (301/307).
	// Zero means use the default.
	RedirectCode int

	// NotFound is called when no route matches (default: http.NotFound).
	NotFound http.Handler

	// MethodNotAllowed is called on 405 (default: plain-text response).
	MethodNotAllowed http.Handler

	// GlobalOPTIONS is called for auto-handled OPTIONS requests instead of
	// the default 204 No Content response.
	GlobalOPTIONS http.Handler

	// ErrorHandler handles errors returned by HandlerFuncE handlers.
	// Default: 500 Internal Server Error.
	ErrorHandler func(http.ResponseWriter, *http.Request, error)

	// PanicHandler recovers from panics in handlers and receives the
	// ResponseWriter, Request, and recovered value.
	PanicHandler func(http.ResponseWriter, *http.Request, any)

	middleware     []func(http.Handler) http.Handler
	pre            []func(http.Handler) http.Handler
	fastMiddleware []FastMiddleware

	// preHandlerPtr stores the pre-dispatch handler chain built by Pre().
	// Stored as an atomic pointer so ServeHTTP can read it without a lock.
	preHandlerPtr atomic.Pointer[http.Handler]

	// lazyNotFoundPtr caches the middleware-wrapped not-found handler after
	// first use. Invalidated by Use() to pick up new middleware.
	lazyNotFoundPtr atomic.Pointer[http.Handler]

	// methodNotAllowedCache caches wrapped 405 handlers keyed by Allow value.
	// The Allow string is determined per-path so we key by it. Invalidated by Use().
	methodNotAllowedCache sync.Map

	// optionsCache caches wrapped OPTIONS handlers keyed by Allow value.
	// Invalidated by Use().
	optionsCache sync.Map

	mu sync.RWMutex // guards Use/Pre/Handle/introspection
}

// New returns a Mux with production-safe defaults enabled.
func New() *Mux {
	return &Mux{
		RedirectTrailingSlash:  true,
		RedirectFixedPath:      false, // security default — path canonicalization can bypass middleware
		HandleMethodNotAllowed: true,
		HandleOPTIONS:          true,
		// UnescapePathValues is false by default — opt in explicitly if needed.
	}
}

// Use appends one or more middleware to the chain. Each middleware wraps all
// handlers registered after this call. The first middleware added is outermost.
func (m *Mux) Use(middleware ...func(http.Handler) http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.middleware = append(m.middleware, middleware...)
	// Invalidate lazy handler caches — they were built with the old middleware slice.
	m.lazyNotFoundPtr.Store(nil)
	m.methodNotAllowedCache.Range(func(k, _ any) bool {
		m.methodNotAllowedCache.Delete(k)
		return true
	})
	m.optionsCache.Range(func(k, _ any) bool {
		m.optionsCache.Delete(k)
		return true
	})
}

// Pre registers middleware that runs before dispatch (e.g. before routing).
// Calling Pre rebuilds the pre-dispatch handler chain.
func (m *Mux) Pre(mw ...func(http.Handler) http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pre = append(m.pre, mw...)
	h := wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.dispatch(w, r, m.config())
	}), m.pre)
	m.preHandlerPtr.Store(&h)
}

// Handle registers handler for the given HTTP method and path pattern.
//
// Path parameters use the ':name' syntax (/users/:id).
// Regex params use '{name:expr}' (/users/{id:[0-9]+}).
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

	idx := methodIdx(method)
	if idx < 0 {
		panic("muxmaster: unsupported HTTP method '" + method + "'")
	}

	// Copy-on-write: load current array, clone, mutate, then store atomically.
	// Readers in ServeHTTP/dispatch never need a lock — they just load the pointer.
	var trees methodTrees
	if old := m.treesPtr.Load(); old != nil {
		trees = *old
	}

	root := trees[idx]
	if root == nil {
		root = new(node)
		trees[idx] = root
	}
	root.addRoute(pattern, wrapMiddleware(handler, m.middleware))
	m.treesPtr.Store(&trees)
}

// HandleFunc registers a HandlerFunc for the given method and path.
func (m *Mux) HandleFunc(method, pattern string, h http.HandlerFunc) {
	m.Handle(method, pattern, h)
}

// HandleE registers a HandlerFuncE for the given method and path.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
// The error handler is read from the frozen muxConfig snapshot at request
// time, eliminating the data race against post-startup mutation of
// m.ErrorHandler (CSA-2026-0052).
func (m *Mux) HandleE(method, pattern string, h HandlerFuncE) {
	m.Handle(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			if eh := m.config().errorHandler; eh != nil {
				eh(w, r, err)
			} else {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}
	}))
}

// UseFast appends one or more FastMiddleware to the chain applied to all
// HandleFast routes registered after this call. The first middleware added
// is outermost. Has no effect on routes registered via Handle.
func (m *Mux) UseFast(mw ...FastMiddleware) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fastMiddleware = append(m.fastMiddleware, mw...)
}

// HandleFast registers a FastHandler for the given HTTP method and path.
//
// FastHandler routes bypass the context allocation overhead of http.Handler
// routes. Params are passed as a direct argument — see FastHandler for
// lifetime guarantees.
//
// SECURITY: stdlib middleware (registered via Use) does NOT apply to fast
// routes. This includes the Recoverer middleware — a panic in a FastHandler
// is NOT recovered by middleware.Recoverer, regardless of the order Use was
// called. Set Mux.PanicHandler to recover panics on the FastHandler path:
// PanicHandler is invoked from dispatchWithRecover and covers both
// http.Handler and FastHandler routes. Use UseFast to attach FastMiddleware
// to fast routes; FastMiddleware runs on the FastHandler dispatch path.
//
// Panics on empty method, non-absolute path, nil handler, or route conflict.
func (m *Mux) HandleFast(method, pattern string, h FastHandler) {
	switch {
	case method == "":
		panic("muxmaster: HTTP method must not be empty")
	case len(pattern) == 0 || pattern[0] != '/':
		panic("muxmaster: path must begin with '/' in '" + pattern + "'")
	case h == nil:
		panic("muxmaster: handler must not be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	idx := methodIdx(method)
	if idx < 0 {
		panic("muxmaster: unsupported HTTP method '" + method + "'")
	}

	var trees methodTrees
	if old := m.treesPtr.Load(); old != nil {
		trees = *old
	}

	root := trees[idx]
	if root == nil {
		root = new(node)
		trees[idx] = root
	}
	root.addRouteFast(pattern, wrapFastMiddleware(h, m.fastMiddleware))
	m.treesPtr.Store(&trees)
}

// GETFast registers a FastHandler for GET requests on pattern.
func (m *Mux) GETFast(pattern string, h FastHandler) { m.HandleFast(http.MethodGet, pattern, h) }

// HEADFast registers a FastHandler for HEAD requests on pattern.
func (m *Mux) HEADFast(pattern string, h FastHandler) { m.HandleFast(http.MethodHead, pattern, h) }

// POSTFast registers a FastHandler for POST requests on pattern.
func (m *Mux) POSTFast(pattern string, h FastHandler) { m.HandleFast(http.MethodPost, pattern, h) }

// PUTFast registers a FastHandler for PUT requests on pattern.
func (m *Mux) PUTFast(pattern string, h FastHandler) { m.HandleFast(http.MethodPut, pattern, h) }

// PATCHFast registers a FastHandler for PATCH requests on pattern.
func (m *Mux) PATCHFast(pattern string, h FastHandler) { m.HandleFast(http.MethodPatch, pattern, h) }

// DELETEFast registers a FastHandler for DELETE requests on pattern.
func (m *Mux) DELETEFast(pattern string, h FastHandler) {
	m.HandleFast(http.MethodDelete, pattern, h)
}

// OPTIONSFast registers a FastHandler for OPTIONS requests on pattern.
func (m *Mux) OPTIONSFast(pattern string, h FastHandler) {
	m.HandleFast(http.MethodOptions, pattern, h)
}

// CONNECTFast registers a FastHandler for CONNECT requests on pattern.
func (m *Mux) CONNECTFast(pattern string, h FastHandler) {
	m.HandleFast(http.MethodConnect, pattern, h)
}

// TRACEFast registers a FastHandler for TRACE requests on pattern.
func (m *Mux) TRACEFast(pattern string, h FastHandler) { m.HandleFast(http.MethodTrace, pattern, h) }

// GET registers a HandlerFunc for GET requests on pattern.
func (m *Mux) GET(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodGet, pattern, h) }

// HEAD registers a HandlerFunc for HEAD requests on pattern.
func (m *Mux) HEAD(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodHead, pattern, h) }

// POST registers a HandlerFunc for POST requests on pattern.
func (m *Mux) POST(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodPost, pattern, h) }

// PUT registers a HandlerFunc for PUT requests on pattern.
func (m *Mux) PUT(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodPut, pattern, h) }

// PATCH registers a HandlerFunc for PATCH requests on pattern.
func (m *Mux) PATCH(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodPatch, pattern, h) }

// DELETE registers a HandlerFunc for DELETE requests on pattern.
func (m *Mux) DELETE(pattern string, h http.HandlerFunc) {
	m.HandleFunc(http.MethodDelete, pattern, h)
}

// OPTIONS registers a HandlerFunc for OPTIONS requests on pattern.
func (m *Mux) OPTIONS(pattern string, h http.HandlerFunc) {
	m.HandleFunc(http.MethodOptions, pattern, h)
}

// CONNECT registers a HandlerFunc for CONNECT requests on pattern.
func (m *Mux) CONNECT(pattern string, h http.HandlerFunc) {
	m.HandleFunc(http.MethodConnect, pattern, h)
}

// TRACE registers a HandlerFunc for TRACE requests on pattern.
func (m *Mux) TRACE(pattern string, h http.HandlerFunc) { m.HandleFunc(http.MethodTrace, pattern, h) }

// GETE registers a HandlerFuncE for GET requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) GETE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodGet, pattern, h) }

// HEADE registers a HandlerFuncE for HEAD requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) HEADE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodHead, pattern, h) }

// POSTE registers a HandlerFuncE for POST requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) POSTE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodPost, pattern, h) }

// PUTE registers a HandlerFuncE for PUT requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) PUTE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodPut, pattern, h) }

// PATCHE registers a HandlerFuncE for PATCH requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) PATCHE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodPatch, pattern, h) }

// DELETEE registers a HandlerFuncE for DELETE requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) DELETEE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodDelete, pattern, h) }

// OPTIONSE registers a HandlerFuncE for OPTIONS requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
func (m *Mux) OPTIONSE(pattern string, h HandlerFuncE) { m.HandleE(http.MethodOptions, pattern, h) }

// ANY registers handler for all standard HTTP methods on pattern.
func (m *Mux) ANY(pattern string, h http.HandlerFunc) {
	for _, method := range anyMethods {
		m.HandleFunc(method, pattern, h)
	}
}

// Match registers handler for each of the listed methods on pattern.
func (m *Mux) Match(methods []string, pattern string, handler http.Handler) {
	for _, method := range methods {
		m.Handle(method, pattern, handler)
	}
}

// Group returns a RouteGroup whose routes share the given path prefix.
func (m *Mux) Group(prefix string) *Group {
	return &Group{mux: m, prefix: prefix}
}

// With returns a Group with the given middleware applied and an empty prefix.
func (m *Mux) With(mw ...func(http.Handler) http.Handler) *Group {
	return &Group{
		mux:        m,
		prefix:     "",
		middleware: append([]func(http.Handler) http.Handler{}, mw...),
	}
}

// Route creates a sub-group at prefix and calls fn with it.
func (m *Mux) Route(prefix string, fn func(*Group)) {
	g := m.Group(prefix)
	fn(g)
}

// Mount attaches h at prefix, stripping the prefix before forwarding the request.
// The catch-all parameter is named "mux_mount".
func (m *Mux) Mount(prefix string, h http.Handler) {
	m.mountAt(prefix, h)
}

// mountAt is the shared implementation used by Mount and Group.Mount.
func (m *Mux) mountAt(prefix string, h http.Handler) {
	if h == nil {
		panic("muxmaster: nil handler passed to Mount")
	}
	if len(prefix) == 0 || prefix[0] != '/' {
		panic("muxmaster: Mount prefix must begin with '/'")
	}
	prefix = strings.TrimRight(prefix, "/")

	mountH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := PathParam(r, "mux_mount")
		if p == "" {
			p = "/"
		}
		r2 := r.Clone(r.Context())
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p
		if r.URL.RawPath != "" {
			trimmed := strings.TrimPrefix(r.URL.RawPath, prefix)
			if len(trimmed) == len(r.URL.RawPath) {
				// TrimPrefix didn't match — zero RawPath to prevent stale encoded prefix.
				r2.URL.RawPath = ""
			} else {
				r2.URL.RawPath = trimmed
			}
		}
		h.ServeHTTP(w, r2)
	})

	m.Handle("*", prefix+"/*mux_mount", mountH)
}

// ServeFiles serves static files from root under the given prefix pattern.
// prefix must end with "/*name" (e.g. "/static/*filepath").
func (m *Mux) ServeFiles(prefix string, root http.FileSystem) {
	if root == nil {
		panic("muxmaster: nil root passed to ServeFiles")
	}
	i := strings.LastIndex(prefix, "/*")
	if i < 0 {
		panic("muxmaster: ServeFiles prefix must end with '/*name': " + prefix)
	}
	paramName := prefix[i+2:]
	if paramName == "" {
		panic("muxmaster: ServeFiles prefix must end with '/*name': " + prefix)
	}
	fs := http.FileServer(root)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = PathParam(r, paramName)
		fs.ServeHTTP(w, r2)
	})
	m.Handle(http.MethodGet, prefix, h)
	m.Handle(http.MethodHead, prefix, h)
}

// lazyNotFound returns the middleware-wrapped not-found handler, building and
// caching it on first use. Two concurrent first-callers may both build the
// handler; the second Store simply overwrites with a functionally identical
// value — safe because the mux is fully configured before serving begins.
//
// Reads cfg.notFound (the frozen snapshot) instead of m.NotFound to avoid
// racing concurrent post-startup mutation (CSA-2026-0052).
func (m *Mux) lazyNotFound(cfg *muxConfig) http.Handler {
	if h := m.lazyNotFoundPtr.Load(); h != nil {
		return *h
	}
	notFound := cfg.notFound
	if notFound == nil {
		notFound = http.HandlerFunc(http.NotFound)
	}
	m.mu.RLock()
	mw := m.middleware
	m.mu.RUnlock()
	h := wrapMiddleware(notFound, mw)
	m.lazyNotFoundPtr.Store(&h)
	return h
}

// lazyMethodNotAllowed returns the middleware-wrapped 405 handler for the
// given Allow header value, building and caching it on first use per allow key.
// Reads cfg.methodNotAllowed (frozen snapshot) — see CSA-2026-0052.
func (m *Mux) lazyMethodNotAllowed(cfg *muxConfig, allow string) http.Handler {
	if v, ok := m.methodNotAllowedCache.Load(allow); ok {
		return v.(http.Handler)
	}
	methodNotAllowed := cfg.methodNotAllowed
	m.mu.RLock()
	mw := m.middleware
	m.mu.RUnlock()
	h := wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		if methodNotAllowed != nil {
			methodNotAllowed.ServeHTTP(w, r)
		} else {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	}), mw)
	m.methodNotAllowedCache.Store(allow, h)
	return h
}

// lazyOPTIONS returns the middleware-wrapped OPTIONS handler for the given
// Allow header value, building and caching it on first use per allow key.
// Reads cfg.globalOPTIONS (frozen snapshot) — see CSA-2026-0052.
func (m *Mux) lazyOPTIONS(cfg *muxConfig, allow string) http.Handler {
	if v, ok := m.optionsCache.Load(allow); ok {
		return v.(http.Handler)
	}
	globalOPTS := cfg.globalOPTIONS
	m.mu.RLock()
	mw := m.middleware
	m.mu.RUnlock()
	h := wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		if globalOPTS != nil {
			globalOPTS.ServeHTTP(w, r)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}), mw)
	m.optionsCache.Store(allow, h)
	return h
}

// config returns the frozen Mux configuration, building it on the first call.
// The fast path (atomic load of a non-nil pointer) is inlineable; the slow
// initialisation path is split into frozenConfigSlow to keep this function
// within the compiler's inline budget.
func (m *Mux) config() *muxConfig {
	if c := m.cfg.Load(); c != nil {
		return c
	}
	return m.frozenConfigSlow()
}

// frozenConfigSlow builds and stores the frozen config snapshot on the first
// call. Marked noinline so that config() stays within the inline budget.
//
// The atomic CompareAndSwap is itself the once-guard: every concurrent caller
// constructs a candidate from the same (pre-serving) field values, but only
// the winner's copy is stored. Losers discard their candidate and Load the
// winner's copy, guaranteeing all callers observe the same snapshot.
//
//go:noinline
func (m *Mux) frozenConfigSlow() *muxConfig {
	c := &muxConfig{
		useRawPath:             m.UseRawPath,
		caseInsensitive:        m.CaseInsensitive,
		unescapePathValues:     m.UnescapePathValues,
		redirectTrailingSlash:  m.RedirectTrailingSlash,
		redirectFixedPath:      m.RedirectFixedPath,
		handleMethodNotAllowed: m.HandleMethodNotAllowed,
		handleOPTIONS:          m.HandleOPTIONS,
		hasPanicHandler:        m.PanicHandler != nil,
		redirectCode:           m.RedirectCode,
		notFound:               m.NotFound,
		methodNotAllowed:       m.MethodNotAllowed,
		globalOPTIONS:          m.GlobalOPTIONS,
		panicHandler:           m.PanicHandler,
		errorHandler:           m.ErrorHandler,
	}
	// Retry loop guards against a concurrent Rebuild() racing with our CAS:
	// Rebuild may Store(nil) between a losing CAS and the subsequent Load,
	// which would otherwise return nil and crash the dispatch path.
	for {
		if m.cfg.CompareAndSwap(nil, c) {
			return c
		}
		if existing := m.cfg.Load(); existing != nil {
			return existing
		}
	}
}

// Rebuild resets the frozen configuration snapshot and the lazy NotFound /
// MethodNotAllowed / OPTIONS handler caches so the next ServeHTTP call
// re-reads every configuration field and rebuilds the wrapped handlers.
//
// Safe to call concurrently with ServeHTTP: every reset is a single atomic
// operation, and the next config() / lazyNotFound() / lazyMethodNotAllowed()
// / lazyOPTIONS() call re-initialises via CompareAndSwap or sync.Map
// re-population. Intended for tests and dynamic reconfiguration scenarios.
func (m *Mux) Rebuild() {
	m.cfg.Store(nil)
	m.lazyNotFoundPtr.Store(nil)
	m.methodNotAllowedCache.Range(func(k, _ any) bool {
		m.methodNotAllowedCache.Delete(k)
		return true
	})
	m.optionsCache.Range(func(k, _ any) bool {
		m.optionsCache.Delete(k)
		return true
	})
}

// ServeHTTP implements http.Handler, dispatching through pre-middleware if set.
//
// The fast path (no PanicHandler, no pre-middleware) avoids the defer frame
// overhead entirely by going straight to dispatch. Deferred paths are isolated
// in dispatchWithRecover to keep this function inlineable.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := m.config()
	if cfg.hasPanicHandler {
		m.dispatchWithRecover(w, r, cfg)
		return
	}
	if ph := m.preHandlerPtr.Load(); ph != nil {
		(*ph).ServeHTTP(w, r)
		return
	}
	m.dispatch(w, r, cfg)
}

// dispatchWithRecover is the slow-path variant used only when PanicHandler is
// configured. Kept in a separate function so the common path avoids defer setup.
func (m *Mux) dispatchWithRecover(w http.ResponseWriter, r *http.Request, cfg *muxConfig) {
	defer m.recoverPanic(cfg, w, r)
	if ph := m.preHandlerPtr.Load(); ph != nil {
		(*ph).ServeHTTP(w, r)
		return
	}
	m.dispatch(w, r, cfg)
}

// dispatch performs route lookup and dispatches to the matched handler.
func (m *Mux) dispatch(w http.ResponseWriter, r *http.Request, cfg *muxConfig) {
	// Determine the effective URL path.
	urlPath := r.URL.Path
	if cfg.useRawPath && r.URL.RawPath != "" {
		urlPath = r.URL.RawPath
	}

	// Load the trees pointer atomically — no lock needed after registration.
	trees := m.treesPtr.Load()
	var root *node
	if trees != nil {
		if idx := methodIdx(r.Method); idx >= 0 {
			root = trees[idx]
		}
	}

	if root != nil {
		// paramsBuf is a fixed-size struct — no slice header, no append, no heap escape.
		// Zero allocs for static routes; 1 alloc (reqBundle) for param routes.
		// When the tree has no wildcard routes, pass nil to skip zeroing 264 B of stack.
		var ps paramsBuf
		var psBuf *paramsBuf
		if root.maxParams > 0 {
			psBuf = &ps
		}
		handler, fast, pattern, tsr := root.getValue(urlPath, psBuf, cfg.caseInsensitive)

		if handler != nil || fast != nil {
			if ps.count > 0 {
				if fast != nil {
					// FastHandler path: allocate exact-sized Params (count * 32B)
					// and copy from ps.buf. This keeps ps on the stack — ps.buf is
					// only read via copy() (a builtin), never passed to a function pointer.
					fps := make(Params, ps.count)
					if len(ps.overflow) == 0 {
						copy(fps, ps.buf[:ps.count])
					} else {
						copy(fps, ps.buf[:maxParams])
						copy(fps[maxParams:], ps.overflow)
					}
					if cfg.unescapePathValues {
						for i := range fps {
							if v, err := url.QueryUnescape(fps[i].Value); err == nil {
								fps[i].Value = v
							}
						}
					}
					fast(w, r, fps)
				} else {
					pslice := ps.params()
					if cfg.unescapePathValues {
						for i := range pslice {
							if v, err := url.QueryUnescape(pslice[i].Value); err == nil {
								pslice[i].Value = v
							}
						}
					}
					dispatchWithParams(w, r, handler, pattern, pslice)
				}
			} else {
				// static route — 0 allocs
				if fast != nil {
					fast(w, r, nil)
				} else {
					handler.ServeHTTP(w, r)
				}
			}
			return
		}

		if r.Method != http.MethodConnect && urlPath != "/" {
			code := m.resolveRedirectCode(cfg, r.Method)

			if tsr && cfg.redirectTrailingSlash {
				if len(urlPath) > 1 && urlPath[len(urlPath)-1] == '/' {
					r.URL.Path = urlPath[:len(urlPath)-1]
				} else {
					r.URL.Path = urlPath + "/"
				}
				target := r.URL.String()
				r.URL.Path = urlPath // restore before passing to middleware
				m.mu.RLock()
				mw := m.middleware
				m.mu.RUnlock()
				wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target, code) //#nosec G710 -- target is a same-origin path (TSR canonicalisation only mutates path; host/scheme untouched). Audited as H-007 (refuted) in /reports/overview/findings.md.
				}), mw).ServeHTTP(w, r)
				return
			}

			if cfg.redirectFixedPath {
				if fixed, ok := m.cleanedPath(root, urlPath); ok {
					r.URL.Path = fixed
					target := r.URL.String()
					r.URL.Path = urlPath // restore before passing to middleware
					m.mu.RLock()
					mw := m.middleware
					m.mu.RUnlock()
					wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, target, code) //#nosec G710 -- target is the cleaned same-origin path; path.Clean reduces leading "//evil" to "/evil" producing a relative same-origin Location. Audited as H-007 (refuted) in /reports/overview/findings.md.
					}), mw).ServeHTTP(w, r)
					return
				}
			}
		}
	}

	// Check the wildcard method tree (used by Mount).
	var starRoot *node
	if trees != nil {
		starRoot = trees[idxWild]
	}
	if starRoot != nil {
		var ps2 paramsBuf
		var ps2Buf *paramsBuf
		if starRoot.maxParams > 0 {
			ps2Buf = &ps2
		}
		h2, f2, pat2, _ := starRoot.getValue(urlPath, ps2Buf, cfg.caseInsensitive)
		if h2 != nil || f2 != nil {
			if ps2.count > 0 {
				if f2 != nil {
					fps2 := make(Params, ps2.count)
					if len(ps2.overflow) == 0 {
						copy(fps2, ps2.buf[:ps2.count])
					} else {
						copy(fps2, ps2.buf[:maxParams])
						copy(fps2[maxParams:], ps2.overflow)
					}
					if cfg.unescapePathValues {
						for i := range fps2 {
							if v, err := url.QueryUnescape(fps2[i].Value); err == nil {
								fps2[i].Value = v
							}
						}
					}
					f2(w, r, fps2)
				} else {
					pslice2 := ps2.params()
					if cfg.unescapePathValues {
						for i := range pslice2 {
							if v, err := url.QueryUnescape(pslice2[i].Value); err == nil {
								pslice2[i].Value = v
							}
						}
					}
					dispatchWithParams(w, r, h2, pat2, pslice2)
				}
			} else {
				if f2 != nil {
					f2(w, r, nil)
				} else {
					h2.ServeHTTP(w, r)
				}
			}
			return
		}
	}

	if r.Method == http.MethodOptions && cfg.handleOPTIONS {
		if allow := m.allowed(urlPath, r.Method); allow != "" {
			m.lazyOPTIONS(cfg, allow).ServeHTTP(w, r)
			return
		}
	} else if cfg.handleMethodNotAllowed {
		if allow := m.allowed(urlPath, r.Method); allow != "" {
			m.lazyMethodNotAllowed(cfg, allow).ServeHTTP(w, r)
			return
		}
	}

	m.lazyNotFound(cfg).ServeHTTP(w, r)
}

func (m *Mux) recoverPanic(cfg *muxConfig, w http.ResponseWriter, r *http.Request) {
	if rcv := recover(); rcv != nil {
		cfg.panicHandler(w, r, rcv)
	}
}

// resolveRedirectCode returns the appropriate redirect status code for the given method.
// cfg.redirectCode is used when non-zero, otherwise the default per-method code is returned.
func (m *Mux) resolveRedirectCode(cfg *muxConfig, method string) int {
	if cfg.redirectCode != 0 {
		return cfg.redirectCode
	}
	if method == http.MethodGet || method == http.MethodHead {
		return http.StatusMovedPermanently
	}
	return http.StatusTemporaryRedirect
}

// allowed returns a comma-separated Allow header value for urlPath.
// Returns "" when no other methods are registered at that path.
func (m *Mux) allowed(urlPath, reqMethod string) string {
	trees := m.treesPtr.Load()
	if trees == nil {
		return ""
	}

	var b strings.Builder
	for i, root := range trees {
		if root == nil {
			continue
		}
		method := methodNames[i]
		if method == reqMethod || method == http.MethodOptions || method == "*" {
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
	h, f, _, _ := root.getValue(cleaned, nil, false)
	if h != nil || f != nil {
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
