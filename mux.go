// Package muxmaster is a high-performance HTTP request multiplexer for Go.
//
// Routes are matched with a radix (compressed prefix) tree, giving O(k) lookup
// where k is the path length. Zero external dependencies; pure standard library.
//
// Usage:
//
//	mux := muxmaster.New()
//	mux.Use(logger, auth)          // middleware applied to every route below
//	mux.GET("/users", listUsers)
//	mux.GET("/users/:id", getUser)
//	mux.GET("/static/*filepath", serveFiles)
//
//	api := mux.Group("/api/v1")
//	api.Use(apiKeyCheck)
//	api.POST("/items", createItem)
//
//	http.ListenAndServe(":8080", mux)
//
// Middleware must be registered (via Use) before the routes it should wrap.
// Dynamic route registration after the server starts serving is not supported.
package muxmaster

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// MethodQuery is the HTTP QUERY method, standardized by RFC 10008
// (https://www.rfc-editor.org/rfc/rfc10008.html, June 2026). Per RFC 10008
// section 2, QUERY is safe and idempotent like GET and HEAD, but — like
// POST — it carries request content (a "query") in its body.
//
// As of Go 1.27, the standard library's net/http package does not define a
// MethodQuery constant (tracked by the Go project as golang/go#80058).
// MuxMaster defines this constant so callers do not need to write the
// literal string "QUERY". If a future Go release adds http.MethodQuery,
// its value is guaranteed to be "QUERY" — RFC 10008 defines the method
// token and Go does not redefine HTTP method tokens — so MuxMaster's
// constant remains equal to it and no code using MethodQuery needs to
// change. MuxMaster does not deprecate or remove MethodQuery when that
// happens.
//
// The router performs no validation of a QUERY request's Content-Type or
// body; that responsibility belongs to the registered handler (see the
// QUERY method on *Mux).
const MethodQuery = "QUERY"

// anyMethods is the full set of HTTP methods registered by ANY and Group.ANY.
var anyMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
	http.MethodConnect, http.MethodTrace, MethodQuery,
}

// Method index constants — replace the map[string]*node lookup with an O(1) array access.
//
// idxQUERY is appended AFTER idxTRACE (not inserted among the existing
// indices) so every previously computed Allow-header bitmask keeps its
// original meaning and every existing Allow string stays byte-identical.
// allowed()'s per-index walk (see allowTable below) always skips idxOPTIONS
// and idxWild and always appends OPTIONS last, so QUERY's array position
// relative to OPTIONS does not affect ordering — only its position relative
// to CONNECT/TRACE does, which is why it is placed immediately after
// idxTRACE: routing.md §4.7 rule 61 orders the Allow header as
// "GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, OPTIONS".
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
	idxQUERY    = 9
	idxWild     = 10 // "*" — used by Mount
	methodCount = 11
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
	idxQUERY:   MethodQuery,
	idxWild:    "*",
}

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
	case MethodQuery:
		return idxQUERY
	case "*":
		return idxWild
	default:
		return -1
	}
}

// methodTrees holds one radix tree root per HTTP method.
// Loaded atomically from treesPtr on every request — no lock needed.
type methodTrees [methodCount]*node

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
	poolFastParams         bool
	poolRequestBundle      bool
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

	// UnescapePathValues percent-decodes path parameter values before storing
	// them. Only takes effect when UseRawPath is also true: when UseRawPath is
	// false (the default) net/http already decodes the URL path during parsing
	// and a second decode would corrupt values containing literal '%XX' (the
	// PRF-2026-0006 double-decode that let %2520 bypass space-blocking input
	// validators). Set both UseRawPath and UnescapePathValues to retrieve
	// decoded values from the original raw path bytes.
	//
	// SECURITY (PRF-2026-0002): when UseRawPath=true AND UnescapePathValues=true,
	// `%2f` inside a single segment is matched as one path segment by the radix
	// tree (because `/` is preserved as separator only via literal slash) and
	// then DECODED in the captured param value. A request such as
	// `/files/..%2fetc%2fpasswd` binds `:filepath` to the literal string
	// `..\x2fetc\x2fpasswd` — i.e. the captured value contains a real slash.
	// Handlers that pass `ParamsFromContext(...).ByName("filepath")` to
	// `os.Open`, `http.FileServer`, or any URL/file API WITHOUT calling
	// `path.Clean` (and rejecting values that contain `..`) are vulnerable to
	// directory traversal. The `clean_path` middleware does NOT normalise
	// post-decode values; it only canonicalises the request path before
	// dispatch. See SECURITY.md "UseRawPath traversal" and
	// examples/static-site/ for the safe pattern.
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
	//
	// SECURITY (CSA-2026-0058 / H8-30): PanicHandler implementations MUST
	// NOT themselves panic. MuxMaster's recover frame catches the FIRST
	// panic and dispatches into PanicHandler; if PanicHandler panics again
	// the secondary panic is NOT recovered by MuxMaster. It propagates up
	// to the per-connection recover in net/http (server.go), which logs
	// "http: panic serving ..." and closes the TCP connection. There is
	// no goroutine leak and no process crash, but the connection is
	// terminated mid-response, which can confuse clients and HTTP/2
	// stream multiplexing. See SECURITY.md "Layered panic recovery".
	PanicHandler func(http.ResponseWriter, *http.Request, any)

	// PoolFastParams, when true, recycles the Params slice handed to
	// FastHandler routes via a sync.Pool tier (1/2/3). It eliminates the
	// per-request allocation but enforces a strict lifetime contract:
	// handlers MUST NOT retain the Params slice (or any backing element)
	// past their return. Goroutines that capture ps and outlive the handler
	// see zeroed values at best, or another request's values at worst —
	// effectively a use-after-free.
	//
	// Default is FALSE for backward compatibility with handlers that rely
	// on the goroutine-safe lifetime previously documented (verified by
	// TestFastHandlerGoroutineSafe). Operators who audit their FastHandler
	// implementations and confirm they do not retain ps may opt in for the
	// allocation/variance reduction.
	PoolFastParams bool

	// PoolRequestBundle, when true, recycles the per-request reqBundle (the
	// fused requestCtx + http.Request copy) handed to http.Handler routes
	// with path parameters via a tiered sync.Pool. It eliminates the
	// 368/400/480-byte allocation on every param-route request and is the
	// single largest performance lever for stdlib-style handlers — but it
	// enforces a strict lifetime contract:
	//
	//   Handlers MUST NOT retain the *http.Request (the one passed to
	//   ServeHTTP) past their return. Goroutines that capture r and outlive
	//   the handler observe a recycled request bound to an unrelated route —
	//   effectively a use-after-free against the bundle storage.
	//
	// This contract is stricter than the Go stdlib's documented invariant
	// (net/http itself recycles request structs internally, but only via the
	// per-connection serve loop, which guarantees the handler has returned
	// before recycling). With PoolRequestBundle the recycling happens at
	// MuxMaster's dispatch boundary, which is finer-grained.
	//
	// Default is FALSE for full stdlib semantics. Operators who audit their
	// handlers and confirm they do not retain r past return may opt in to
	// drive ParamRoute1 from ~106 ns / 384 B / 1 alloc down to roughly
	// 40-50 ns / 0 B / 0 allocs on the hot path.
	//
	// SECURITY (Opt O13): the bundle is fully zeroed before returning to
	// the pool, so secrets accidentally stored in request fields by a
	// handler cannot leak across requests. The zeroing cost (~10 ns) is
	// already included in the projected savings.
	PoolRequestBundle bool

	middleware     []func(http.Handler) http.Handler
	pre            []func(http.Handler) http.Handler
	fastMiddleware []FastMiddleware

	// preHandlerPtr stores the pre-dispatch handler chain built by Pre().
	// Stored as an atomic pointer so ServeHTTP can read it without a lock.
	preHandlerPtr atomic.Pointer[http.Handler]

	// lazyNotFoundPtr caches the middleware-wrapped not-found handler after
	// first use. Invalidated by Use() to pick up new middleware.
	lazyNotFoundPtr atomic.Pointer[http.Handler]

	// redirectMWPtr is a lock-free snapshot of m.middleware, refreshed by
	// Use() (CH-06). serveRedirect reads it directly instead of taking
	// m.mu.RLock() on every redirect — the last unconditional RWMutex
	// operation remaining in the request-dispatch path. A nil pointer (the
	// initial state, before any Use() call) means "no middleware". Each
	// snapshot carries a strictly increasing generation number (assigned
	// under m.mu by Use()) so that lazyRedirect's cache (below) can be
	// published with a CAS that always converges to the newest generation,
	// never an ABA-stale one, under concurrent Use() calls.
	redirectMWPtr atomic.Pointer[mwSnapshot]

	// redirectGen is the last generation number assigned to a redirectMWPtr
	// snapshot. Mutated only under m.mu (inside Use()), so plain increment
	// is safe; readers only ever see it embedded, already-published, inside
	// an *mwSnapshot.
	redirectGen uint64

	// lazyRedirectPtr caches the middleware-wrapped redirect handler after
	// first use, mirroring lazyNotFoundPtr ([waste-hunt WH-10]). Without this
	// cache, serveRedirect re-ran wrapMiddleware — re-instantiating one
	// closure per registered middleware plus the redirect closure itself —
	// on every single redirect. The cached entry is tagged with the
	// mwSnapshot generation it was built from; lazyRedirect uses that tag
	// (not Store-order) to decide whether to publish, so a build that started
	// against a stale, superseded snapshot can never clobber a fresher one
	// published by a concurrent Use() + redirect race. The per-request
	// redirect target/code are carried to the cached handler via the request
	// context (redirectCtxKey) rather than closed over, since the handler is
	// built once and reused.
	lazyRedirectPtr atomic.Pointer[lazyRedirectEntry]

	// methodNotAllowedCache caches wrapped 405 handlers keyed by Allow value.
	// The Allow string is determined per-path so we key by it. Invalidated by Use().
	methodNotAllowedCache sync.Map

	// optionsCache caches wrapped OPTIONS handlers keyed by Allow value.
	// Invalidated by Use().
	optionsCache sync.Map

	mu sync.RWMutex // guards Use/Pre/Handle/introspection

	// rawPathDecodeWarnOnce emits a one-time slog warning when the operator
	// enables UseRawPath+UnescapePathValues — the combination decodes %2f
	// inside captured params and exposes handlers to path traversal unless
	// the operator sanitises ParamsFromContext values (PRF-2026-0002).
	rawPathDecodeWarnOnce sync.Once
}

// warnRawPathDecodeIfEnabled emits a one-time slog.Warn whenever the operator
// has enabled the UseRawPath+UnescapePathValues combination, which decodes
// %2f inside captured params and exposes handlers to path traversal unless
// they sanitise the value (PRF-2026-0002 / CDX-S8-002).
func (m *Mux) warnRawPathDecodeIfEnabled() {
	if !m.UseRawPath || !m.UnescapePathValues {
		return
	}
	m.rawPathDecodeWarnOnce.Do(func() {
		slog.Warn("muxmaster: UseRawPath+UnescapePathValues enabled — captured path params may contain literal '/' from %2f decode. Handlers using params as filesystem/URL components MUST call path.Clean and reject values containing '..'. See SECURITY.md \"UseRawPath traversal\" (PRF-2026-0002).")
	})
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
//
// SECURITY (CSA-2026-0059): Use does NOT wrap HandleFast routes — registering
// a fast route after Use(authMiddleware) panics at HandleFast call time on
// BOTH the root Mux (FPE-2026-010) and Groups (CSA-2026-0054), so the bypass
// cannot occur silently regardless of where the operator places the route.
// To apply policy to both stdlib and fast routes, use Pre(...) (outermost,
// route-type agnostic) or UseFast(...) for FastMiddleware. See SECURITY.md
// "Pre vs Use security boundary".
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
	// Refresh the lock-free redirect-middleware snapshot (CH-06) so
	// serveRedirect observes the new chain without ever taking m.mu. The
	// generation number is assigned here, under m.mu, so it is strictly
	// increasing in real Use() call order — lazyRedirect's cache (WH-10)
	// uses it to invalidate itself without a Store(nil) race window: see
	// the lazyRedirectPtr field comment.
	m.redirectGen++
	m.redirectMWPtr.Store(&mwSnapshot{
		gen: m.redirectGen,
		mw:  append([]func(http.Handler) http.Handler(nil), m.middleware...),
	})
}

// Pre registers middleware that runs before dispatch (e.g. before routing).
// Calling Pre rebuilds the pre-dispatch handler chain.
//
// SECURITY (CSA-2026-0059): Pre wraps the entire ServeHTTP dispatch and
// covers BOTH Handle (stdlib) and HandleFast routes. This makes Pre the
// correct registration point for cross-cutting policies that must apply
// uniformly — auth gates, CleanPath, RealIP, RecovererWithLogger, request
// IDs. See SECURITY.md "Pre vs Use security boundary".
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

	m.warnRawPathDecodeIfEnabled()

	m.mu.Lock()
	defer m.mu.Unlock()

	idx := methodIdx(method)
	if idx < 0 {
		panic("muxmaster: unsupported HTTP method '" + method + "'")
	}

	// Two-phase copy-on-write (MM-2026-0033): copy only the nodes on the
	// insertion path into a new methodTrees array, mutate the copies, then
	// publish via atomic.Pointer.Store. If addRoute panics mid-mutation, the
	// copied nodes are discarded and the previous live tree remains intact —
	// eliminating the "tree corruption after registration panic" class of
	// bugs, at O(depth) copies per registration instead of O(tree size)
	// (performance.md §36-37, [waste-hunt WH-08]).
	var trees methodTrees
	if old := m.treesPtr.Load(); old != nil {
		trees = *old
	}
	if trees[idx] != nil {
		trees[idx] = copyNode(trees[idx])
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
//
// SECURITY (CSA-2026-0059): UseFast is the FastHandler counterpart of
// Use; together with Pre (which covers BOTH route types) it forms the
// route-type matrix documented in SECURITY.md "Pre vs Use security
// boundary". An auth gate applied only via Use(...) does NOT cover
// HandleFast routes.
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

	m.warnRawPathDecodeIfEnabled()

	m.mu.Lock()
	defer m.mu.Unlock()

	// FPE-2026-010: panic if stdlib middleware (registered via Use) is present.
	// Stdlib middleware is incompatible with the FastHandler dispatch path —
	// silently mixing them would let HandleFast routes bypass authentication,
	// authorisation, logging or any other Use()-registered middleware. This
	// panic mirrors Group.HandleFast (CSA-2026-0054) and closes the gap where
	// the same operator mistake on the root Mux silently succeeded.
	if len(m.middleware) > 0 {
		panic("muxmaster: HandleFast route registered on a Mux with stdlib middleware (Use) — " +
			"stdlib middleware does not run on the FastHandler path. " +
			"Use UseFast() for fast routes, or Handle() for stdlib-middleware-wrapped routes.")
	}

	idx := methodIdx(method)
	if idx < 0 {
		panic("muxmaster: unsupported HTTP method '" + method + "'")
	}

	// Two-phase copy-on-write (MM-2026-0033) — path copying, see Handle.
	var trees methodTrees
	if old := m.treesPtr.Load(); old != nil {
		trees = *old
	}
	if trees[idx] != nil {
		trees[idx] = copyNode(trees[idx])
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

// QUERYFast registers a FastHandler for QUERY requests on pattern.
// QUERY is a standard HTTP method (RFC 10008); see MethodQuery.
func (m *Mux) QUERYFast(pattern string, h FastHandler) { m.HandleFast(MethodQuery, pattern, h) }

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

// QUERY registers a HandlerFunc for QUERY requests on pattern.
//
// QUERY is a standard HTTP method, standardized by RFC 10008. Per RFC 10008
// section 2, it is safe and idempotent but — unlike GET — carries request
// content in its body; see MethodQuery. The router performs no validation
// of the Content-Type header or body of a QUERY request: RFC 10008 section
// 2.1 requires servers to fail the request (400, 415, or 422) when the
// Content-Type field is missing or inconsistent with the request content,
// and RFC 10008 section 3 defines the Accept-Query response header for
// advertising supported query formats — implementing both is the
// responsibility of the registered handler.
func (m *Mux) QUERY(pattern string, h http.HandlerFunc) { m.HandleFunc(MethodQuery, pattern, h) }

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

// QUERYE registers a HandlerFuncE for QUERY requests on pattern.
// Errors are passed to m.ErrorHandler if set, otherwise a 500 is returned.
// QUERY is a standard HTTP method (RFC 10008); see MethodQuery.
func (m *Mux) QUERYE(pattern string, h HandlerFuncE) { m.HandleE(MethodQuery, pattern, h) }

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
//
// h receives a shallow copy of the request (see the Terminology section in
// README.md): a new *http.Request with a new URL, but sharing the original's
// header map, Trailer, Form and context. h may read the original request's
// headers, but must not mutate them in place — such a mutation would be
// visible to the caller's original request and to any outer middleware that
// runs after Mount returns.
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
	if !utf8.ValidString(prefix) {
		// FPE-2026-002: a tree.go panic on invalid UTF-8 leaks the internal
		// "*mux_mount" param name. Validate up-front with a clean message.
		panic("muxmaster: Mount prefix contains invalid UTF-8")
	}
	prefix = strings.TrimRight(prefix, "/")

	mountH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := PathParam(r, "mux_mount")
		if p == "" {
			p = "/"
		}
		// [waste-hunt WH-04] Shallow request copy (see specification/README.md
		// Terminology): net/http.StripPrefix's strategy — a shallow struct
		// copy plus a fresh *url.URL — instead of r.Clone, which deep-copies
		// the header map, Trailer, Form and TransferEncoding even though
		// only URL.Path (and possibly URL.RawPath) changes below. r2 shares
		// the original's header map and context; the original request is
		// never mutated.
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p
		if r.URL.RawPath != "" {
			trimmed := strings.TrimPrefix(r.URL.RawPath, prefix)
			switch {
			case len(trimmed) == len(r.URL.RawPath):
				// TrimPrefix didn't match — zero RawPath to prevent stale encoded prefix.
				r2.URL.RawPath = ""
			case trimmed != "" && trimmed[0] != '/':
				// HPS-2026-0001: prefix matched a leading byte of an encoded
				// segment (e.g. /api%2fusers trimmed against /api leaves
				// %2fusers — not a rooted path). Zero RawPath rather than
				// publish a non-rooted URL that would violate the url.URL
				// contract and confuse downstream handlers.
				r2.URL.RawPath = ""
			default:
				r2.URL.RawPath = trimmed
			}
		}
		h.ServeHTTP(w, r2)
	})

	m.Handle("*", prefix+"/*mux_mount", mountH)
}

// ServeFiles serves static files from root under the given prefix pattern.
// prefix must end with "/*name" (e.g. "/static/*filepath").
//
// http.FileServer receives a shallow copy of the request (see the
// Terminology section in README.md): a new *http.Request with a new URL,
// but sharing the original's header map and context.
//
// SECURITY (CDX-S8-002): http.FileServer applies path.Clean internally,
// so a request like /static/../etc/passwd cannot escape root. However,
// when the Mux is configured with UseRawPath=true AND UnescapePathValues=true
// the captured filepath param contains decoded slashes (PRF-2026-0002) and
// http.FileServer's clean step happens AFTER the param has already been
// re-set as r2.URL.Path — the decoded slashes act as path separators
// inside FileServer's tree. Registration with that combination panics so
// the misconfiguration is caught at boot. Disable one of UseRawPath /
// UnescapePathValues, or write a custom handler that calls path.Clean on
// the captured value and rejects ".." segments before dispatch.
func (m *Mux) ServeFiles(prefix string, root http.FileSystem) {
	if root == nil {
		panic("muxmaster: nil root passed to ServeFiles")
	}
	if m.UseRawPath && m.UnescapePathValues {
		panic("muxmaster: ServeFiles refuses to register with UseRawPath=true AND " +
			"UnescapePathValues=true — captured filepath would contain decoded '/' " +
			"and http.FileServer would treat them as separators (CDX-S8-002 / PRF-2026-0002). " +
			"Disable one of the two, or implement a custom handler that path.Clean's " +
			"the captured value before dispatch. See SECURITY.md \"UseRawPath traversal\".")
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
		// [waste-hunt WH-04] Shallow request copy — see mountAt above.
		r2 := new(http.Request)
		*r2 = *r
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

// Opt M1: pre-built response constants used by the default lazyMethodNotAllowed
// handler so that 405 responses skip http.Error's per-call Header().Set +
// fmt.Fprintln overhead.
//
// MID-405OPTIONS-1 (sprint 18 waste-hunt, same class as MID-SETHEADER-1 in
// middleware/set_header.go): method405ContentType and method405XContentOption
// hold only the STRING value here — never the header's []string slice.
// An earlier version hoisted the slices themselves into these package-level
// variables, so every 405 response from EVERY Mux instance in the process
// shared the exact same slice for Content-Type and X-Content-Type-Options.
// Any downstream code indexing directly into the slice
// (w.Header()["Content-Type"][0] = ...) corrupted that header for every
// other 405 response process-wide until restart. Each request must get a
// freshly allocated one-element slice; see lazyMethodNotAllowed and
// lazyOPTIONS below for the same fix applied to the per-Allow-key cached
// "Allow" value.
var (
	method405Body           = []byte(http.StatusText(http.StatusMethodNotAllowed) + "\n")
	method405ContentType    = "text/plain; charset=utf-8"
	method405XContentOption = "nosniff"
)

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

	// MID-405OPTIONS-1 fix: `allow` (the string) is safe to capture in the
	// closure — it never changes for this cache entry — but the header
	// value's []string MUST be allocated fresh on every request. A slice
	// hoisted here, like the one previously hoisted, would be installed into
	// every request's Header map simultaneously; any downstream code
	// indexing into it directly (w.Header()["Allow"][0] = ...) would mutate
	// the shared backing array in place, corrupting the Allow header for
	// every other request through this cache entry until process restart.
	var inner http.HandlerFunc
	if methodNotAllowed != nil {
		inner = func(w http.ResponseWriter, r *http.Request) {
			w.Header()["Allow"] = []string{allow}
			methodNotAllowed.ServeHTTP(w, r)
		}
	} else {
		// Default branch: emit the plain-text 405 response with the same byte
		// sequence as net/http's http.Error would, but without paying
		// Header().Set's canonicalisation cost (the keys are already
		// canonical compile-time constants).
		//
		// MID-405OPTIONS-2 (waste-hunt gate follow-up): the 3 header values
		// share ONE freshly allocated [3]string backing array per request
		// instead of 3 separate one-element slices — 1 allocation, not 3.
		// Each header's slice is the full slice expression vals[i:i+1:i+1],
		// capped at length 1, so appending to any one of the 3 (e.g. a
		// later Header().Add on the same key) grows into a fresh backing
		// array rather than overwriting an adjacent header's slot.
		inner = func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			vals := &[3]string{allow, method405ContentType, method405XContentOption}
			h["Allow"] = vals[0:1:1]
			h["Content-Type"] = vals[1:2:2]
			h["X-Content-Type-Options"] = vals[2:3:3]
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write(method405Body)
		}
	}
	h := wrapMiddleware(inner, mw)
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
	// [waste-hunt WH-09] `allow` is captured once per cache entry — it never
	// changes — so direct map assignment skips Header().Set's per-request
	// canonicalisation. MID-405OPTIONS-1 fix: the []string header VALUE
	// itself must still be allocated fresh per request (see
	// lazyMethodNotAllowed above for why a shared slice here is unsafe).
	h := wrapMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Allow"] = []string{allow}
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
		poolFastParams:         m.PoolFastParams,
		poolRequestBundle:      m.PoolRequestBundle,
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
// MethodNotAllowed / OPTIONS / redirect handler caches so the next ServeHTTP
// call re-reads every configuration field and rebuilds the wrapped handlers.
//
// Safe to call concurrently with ServeHTTP: every reset is a single atomic
// operation, and the next config() / lazyNotFound() / lazyMethodNotAllowed()
// / lazyOPTIONS() / lazyRedirect() call re-initialises via CompareAndSwap or
// sync.Map re-population. Intended for tests and dynamic reconfiguration
// scenarios.
func (m *Mux) Rebuild() {
	m.cfg.Store(nil)
	m.lazyNotFoundPtr.Store(nil)
	m.lazyRedirectPtr.Store(nil)
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
		var ps paramsBuf
		var (
			handler http.Handler
			fast    FastHandler
			pattern string
			tsr     bool
		)
		if root.maxParams == 0 {
			// Opt O1: when the entire subtree is static (no param/regex/wildcard),
			// dispatch through the dedicated getValueStatic that omits the param
			// switch, the params buffer dereferences, and the wildchild branches.
			// [waste-hunt WH-13] `ps` is still zeroed above regardless of this
			// branch — the compiler emits the zeroing unconditionally at the
			// `var ps paramsBuf` declaration, since it cannot prove getValueStatic
			// never receives a pointer to it. This branch's saving is the params
			// switch/wildchild logic it skips, not the zeroing.
			handler, fast, pattern, tsr = root.getValueStatic(urlPath, cfg.caseInsensitive)
		} else {
			handler, fast, pattern, tsr = root.getValue(urlPath, &ps, cfg.caseInsensitive)
		}

		if handler != nil || fast != nil {
			if ps.count > 0 {
				if fast != nil {
					// Opt O9 (opt-in via Mux.PoolFastParams): pull a tier-matched
					// Params slice from sync.Pool. Default (PoolFastParams=false)
					// allocates fresh each call to preserve the original
					// goroutine-safe lifetime documented in handler.go.
					var fps Params
					if cfg.poolFastParams {
						fps = getFastParams(ps.count)
					} else {
						fps = make(Params, ps.count)
					}
					if len(ps.overflow) == 0 {
						copy(fps, ps.buf[:ps.count])
					} else {
						copy(fps, ps.buf[:maxParams])
						copy(fps[maxParams:], ps.overflow)
					}
					if cfg.unescapePathValues && cfg.useRawPath {
						for i := range fps {
							if v, err := url.PathUnescape(fps[i].Value); err == nil {
								fps[i].Value = v
							}
						}
					}
					fast(w, r, fps)
					if cfg.poolFastParams {
						putFastParams(fps)
					}
				} else {
					// Inline 1-param dispatch (Opt O5): bypass the dispatchWithParams
					// wrapper + switch for the most common REST case (single :id).
					// Saves one non-inlineable function call and one switch.
					//
					// Opt O13: when poolRequestBundle is enabled and the unsafe
					// ctx field shortcut is available, dispatch via the pooled
					// path which recycles the reqBundle1 across requests and
					// eliminates the per-request allocation.
					if ps.count == 1 {
						p0 := ps.buf[0]
						if cfg.unescapePathValues && cfg.useRawPath {
							if v, err := url.PathUnescape(p0.Value); err == nil {
								p0.Value = v
							}
						}
						if cfg.poolRequestBundle && hasReqCtxField {
							dispatchParams1Pooled(w, r, handler, pattern, p0)
						} else {
							dispatchParams1(w, r, handler, pattern, p0)
						}
						return
					}
					if ps.count == 2 {
						p0 := ps.buf[0]
						p1 := ps.buf[1]
						if cfg.unescapePathValues && cfg.useRawPath {
							if v, err := url.PathUnescape(p0.Value); err == nil {
								p0.Value = v
							}
							if v, err := url.PathUnescape(p1.Value); err == nil {
								p1.Value = v
							}
						}
						if cfg.poolRequestBundle && hasReqCtxField {
							dispatchParams2Pooled(w, r, handler, pattern, p0, p1)
						} else {
							dispatchParams2(w, r, handler, pattern, p0, p1)
						}
						return
					}
					pslice := ps.params()
					if cfg.unescapePathValues && cfg.useRawPath {
						for i := range pslice {
							if v, err := url.PathUnescape(pslice[i].Value); err == nil {
								pslice[i].Value = v
							}
						}
					}
					// Opt O13: 3+-param pooled path mirrors the 1/2 path.
					if cfg.poolRequestBundle && hasReqCtxField {
						dispatchParamsNPooled(w, r, handler, pattern, pslice)
					} else {
						dispatchWithParams(w, r, handler, pattern, pslice)
					}
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
				var newPath string
				if len(urlPath) > 1 && urlPath[len(urlPath)-1] == '/' {
					newPath = urlPath[:len(urlPath)-1]
				} else {
					newPath = urlPath + "/"
				}
				// HPS-2026-0005: path-only Location — requests in absolute-form
				// (RFC 7230 §5.3.2) cannot inject scheme+host into the redirect
				// because we never serialise r.URL.Scheme/r.URL.Host.
				// Opt R1: manual concatenation avoids the url.URL{}.String() pair
				// of allocations (url.URL struct + serialised string).
				m.serveRedirect(w, r, buildRedirectTarget(newPath, r.URL.RawQuery), code)
				return
			}

			if cfg.redirectFixedPath {
				if fixed, ok := m.cleanedPath(root, urlPath); ok {
					// HPS-2026-0005: same-origin path-only Location.
					m.serveRedirect(w, r, buildRedirectTarget(fixed, r.URL.RawQuery), code)
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
		h2, f2, pat2, tsr2 := starRoot.getValue(urlPath, ps2Buf, cfg.caseInsensitive)
		if h2 != nil || f2 != nil {
			if ps2.count > 0 {
				if f2 != nil {
					// Opt O9 (opt-in via Mux.PoolFastParams).
					var fps2 Params
					if cfg.poolFastParams {
						fps2 = getFastParams(ps2.count)
					} else {
						fps2 = make(Params, ps2.count)
					}
					if len(ps2.overflow) == 0 {
						copy(fps2, ps2.buf[:ps2.count])
					} else {
						copy(fps2, ps2.buf[:maxParams])
						copy(fps2[maxParams:], ps2.overflow)
					}
					if cfg.unescapePathValues && cfg.useRawPath {
						for i := range fps2 {
							if v, err := url.PathUnescape(fps2[i].Value); err == nil {
								fps2[i].Value = v
							}
						}
					}
					f2(w, r, fps2)
					if cfg.poolFastParams {
						putFastParams(fps2)
					}
				} else {
					pslice2 := ps2.params()
					if cfg.unescapePathValues && cfg.useRawPath {
						for i := range pslice2 {
							if v, err := url.PathUnescape(pslice2[i].Value); err == nil {
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

		// TSR for Mount's internal catch-all (specification/groups.md §28: a
		// mount at "/v2" handles "/v2", "/v2/", and "/v2/anything"). Without
		// this, a request for the bare mount prefix — no further segment and
		// no trailing slash — fell straight through to 404 instead of
		// redirecting to the trailing-slash form, unlike every other route
		// type. Mirrors the primary-tree TSR block above.
		if tsr2 && cfg.redirectTrailingSlash && r.Method != http.MethodConnect && urlPath != "/" {
			code := m.resolveRedirectCode(cfg, r.Method)
			var newPath string
			if len(urlPath) > 1 && urlPath[len(urlPath)-1] == '/' {
				newPath = urlPath[:len(urlPath)-1]
			} else {
				newPath = urlPath + "/"
			}
			m.serveRedirect(w, r, buildRedirectTarget(newPath, r.URL.RawQuery), code)
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

// buildRedirectTarget produces the path-only Location header value for an
// internal redirect. It avoids the &url.URL{...}.String() pair of allocations
// in the hot redirect path: instead of constructing a temporary url.URL struct
// (104B) and asking it to serialise itself (another string allocation), we
// concatenate the path with `?` + raw query when one is present.
//
// SECURITY (HPS-2026-0005): the path is always taken from the request path
// (already validated by net/http) and never carries a scheme or host. The
// raw query is appended verbatim, matching url.URL{Path, RawQuery}.String()
// behaviour — net/http does not validate the query for control characters
// either, so a downstream client may receive arbitrary query bytes (same
// posture as before the refactor).
func buildRedirectTarget(newPath, rawQuery string) string {
	if rawQuery == "" {
		return newPath
	}
	return newPath + "?" + rawQuery
}

// redirectHTMLReplacer mirrors net/http's private htmlReplacer, used to
// escape the redirect target into the HTML body net/http.Redirect writes for
// GET requests. Duplicated here because that symbol is unexported.
var redirectHTMLReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	// "&#34;" is shorter than "&quot;".
	`"`, "&#34;",
	// "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
	"'", "&#39;",
)

// isRedirectControlByte reports whether b is an ASCII control byte (0x00-
// 0x1F) or DEL (0x7F). RFC 9110 §5.5 forbids raw CTL bytes in HTTP field
// values; a percent-decoded control byte in the request path (e.g. a %00 in
// r.URL.Path) must never reach the Location header, or the HTML redirect
// body's href, unescaped (rmp #260 / CSA-2026-00xx).
func isRedirectControlByte(b byte) bool {
	return b < 0x20 || b == 0x7f
}

// percentEncodeControlBytes percent-encodes every ASCII control byte and DEL
// in s, leaving every other byte — including non-ASCII bytes, which
// hexEscapeNonASCII handles separately for the Location header — untouched.
// Returns s unchanged (no allocation) when it contains no such byte, so
// output for a control-byte-free target stays byte-identical to
// net/http.Redirect (TestWriteRedirect_ByteIdenticalToNetHTTPRedirect).
func percentEncodeControlBytes(s string) string {
	needsEscape := false
	for i := 0; i < len(s); i++ {
		if isRedirectControlByte(s[i]) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return s
	}
	const hexDigits = "0123456789ABCDEF"
	b := make([]byte, 0, len(s)+8)
	var pos int
	for i := 0; i < len(s); i++ {
		if isRedirectControlByte(s[i]) {
			if pos < i {
				b = append(b, s[pos:i]...)
			}
			c := s[i]
			b = append(b, '%', hexDigits[c>>4], hexDigits[c&0xf])
			pos = i + 1
		}
	}
	if pos < len(s) {
		b = append(b, s[pos:]...)
	}
	return string(b)
}

// percentEncodeBackslash replaces every '\' (0x5C) byte in s with its
// percent-encoded form "%5C". See writeRedirect's doc comment for why: a
// literal backslash reaching the Location header lets a WHATWG-compliant
// browser resolve the redirect to a different origin (the "special
// authority slashes state") even though RFC 3986 / net/url see no host
// component at all. strings.ReplaceAll already returns s unchanged, with no
// allocation, when s contains no '\' (Count == 0 short-circuits Replace), so
// output stays byte-identical to net/http.Redirect for every target that
// never contains a backslash — the overwhelming majority.
func percentEncodeBackslash(s string) string {
	return strings.ReplaceAll(s, "\\", "%5C")
}

// hexEscapeNonASCII mirrors net/http's private helper of the same name, used
// by net/http.Redirect to escape the Location header value. Duplicated here
// because that symbol is unexported.
func hexEscapeNonASCII(s string) string {
	newLen := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			newLen += 3
		} else {
			newLen++
		}
	}
	if newLen == len(s) {
		return s
	}
	b := make([]byte, 0, newLen)
	var pos int
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			if pos < i {
				b = append(b, s[pos:i]...)
			}
			b = append(b, '%')
			b = strconv.AppendInt(b, int64(s[i]), 16)
			pos = i + 1
		}
	}
	if pos < len(s) {
		b = append(b, s[pos:]...)
	}
	return string(b)
}

// writeRedirect emits an HTTP redirect response byte-identical to
// net/http.Redirect(w, r, target, code) — rmp #248 — for every method and
// redirect code, specialised for MuxMaster's own redirect targets:
// buildRedirectTarget's trailing-slash toggle and (*Mux).cleanedPath's
// path.Clean result, both always a path-only, already-clean value
// (HPS-2026-0005). This skips net/http.Redirect's url.Parse and
// fmt.Fprintln allocations on the hot redirect path while producing the
// exact same headers and body — including the extra trailing newline
// fmt.Fprintln appends after a body that already ends in "\n".
//
// Correctness does not rely on "already clean" as an unchecked assumption:
// the same Clean-and-restore-trailing-slash step net/http.Redirect performs
// is still applied below. It costs nothing extra for an input that is
// already clean — path.Clean returns its argument unchanged, with no
// allocation, when nothing needs rewriting.
//
// The one thing this function does not re-derive is net/http.Redirect's
// scheme/host detection: it requires target to start with exactly one '/'
// — never "//", which url.Parse would read as a network-path reference with
// a non-empty Host (the classic protocol-relative open-redirect shape).
// Every caller in this package satisfies that (Handle() panics on any
// pattern that does not start with a single '/'). The guard below falls
// back to net/http.Redirect itself for anything that doesn't, so the "//"
// output stays byte-identical to net/http.Redirect regardless — "//" is
// deliberately left unneutralised, matching net/http.Redirect exactly,
// because HPS-2026-0005 already guarantees no attacker-controlled
// scheme/host ever reaches this function via that shape, and
// redirect_bytediff_test.go pins exact parity with net/http.Redirect for
// it.
//
// rmp #279 / rmp #274 part 5a — DELIBERATE DIVERGENCE from net/http.Redirect
// for any target containing '\' (0x5C): every '\' is percent-encoded to
// "%5C" (percentEncodeBackslash, below) before anything else runs. RFC 3986
// gives '\' no meaning (url.Parse reports no Host for a target starting
// "/\", unlike "//"), but the WHATWG URL Standard's "special authority
// slashes state" treats ANY two-byte combination of '/' and '\' at the
// start of a relative reference — "//", "/\", "\/", "\\" — as
// authority-establishing: a browser resolves a Location of "/\evil.com" to
// a DIFFERENT origin. net/http.Redirect does not defend against this (its
// own path.Clean is a no-op on backslash — confirmed empirically), so
// reproducing its behaviour byte-for-byte here would reproduce the flaw.
// Encoding is applied to every backslash in target, not only a leading "/\"
// pair: this is simpler than special-casing position 1, stays byte-identical
// to net/http.Redirect for every backslash-free target (the overwhelming
// majority — percentEncodeBackslash is then a true no-op), and additionally
// neutralises components (some reverse proxies, WAFs, and legacy browsers)
// that fold ANY backslash in a path to a forward slash, not only a leading
// pair. A target with '\' can only ever reach writeRedirect if the operator
// registers that literal backslash-shaped route themselves — path.Clean
// never introduces a backslash that was not already present verbatim in a
// registered pattern or the decoded request path — so an anonymous attacker
// can never choose which backslash-shaped target appears (rmp #274 part 5a's
// "not attacker reachable" invariant); this is defense-in-depth against that
// operator-registered shape reaching a real browser unneutralised, not a fix
// for an attacker-reachable bug. The percent-encoded '\' round-trips
// correctly: net/http's own request-target decoding turns "%5C" back into a
// literal '\' byte in the follow-up request's r.URL.Path, so the operator's
// registered route is still reached, on the same origin (verified
// end-to-end in TestWriteRedirect_BackslashAuthorityShape_NeutralisesEndToEnd).
//
// rmp #260: net/http.Redirect's own hexEscapeNonASCII only escapes bytes
// >= 0x80 — a percent-decoded ASCII control byte (e.g. a %00 in
// r.URL.Path) reaches the Location header and the HTML body's href
// unescaped, violating RFC 9110 §5.5. Both this fast path and the
// http.Redirect fallback above now percent-encode CTL/DEL bytes in the
// target before use; percentEncodeControlBytes is a no-op (returns its
// input unchanged) for any target without one, so output stays
// byte-identical to net/http.Redirect for every control-free target.
func writeRedirect(w http.ResponseWriter, r *http.Request, target string, code int) {
	target = percentEncodeBackslash(target)

	if len(target) == 0 || target[0] != '/' || (len(target) > 1 && target[1] == '/') {
		http.Redirect(w, r, percentEncodeControlBytes(target), code) // #nosec G710 — see callers' HPS-2026-0005 guarantees
		return
	}

	loc := target
	var query string
	if i := strings.IndexByte(loc, '?'); i != -1 {
		loc, query = loc[:i], loc[i:]
	}
	trailing := strings.HasSuffix(loc, "/")
	cleaned := path.Clean(loc)
	if trailing && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	loc = percentEncodeControlBytes(cleaned + query)

	// [perf-lab-2026-09-24] Opt R5: direct map assignment instead of
	// Header.Set, mirroring the WH-09 pattern already used for the Allow
	// header (lazyMethodNotAllowed/lazyOPTIONS) — "Location" and
	// "Content-Type" are compile-time-constant, already-canonical keys, so
	// textproto.MIMEHeader.Set's CanonicalMIMEHeaderKey lookup on every call
	// is pure overhead. Semantics are identical to Set for an
	// already-canonical key.
	//
	// MID-REDIRECT-1 (waste-hunt gate follow-up): when both Location and
	// Content-Type are set this call, they share ONE freshly allocated
	// [2]string backing array — 1 allocation instead of 2 — via the full
	// slice expression vals[i:i+1:i+1], capped at length 1 so appending to
	// either header cannot overwrite the other's slot. When Content-Type
	// is not set here (a caller already set it, or the method is neither
	// GET nor HEAD), Location alone keeps its own single-element slice —
	// no [2]string is allocated for a header that will not be used.
	h := w.Header()
	_, hadCT := h["Content-Type"]
	locVal := hexEscapeNonASCII(loc)
	method := r.Method
	if !hadCT && (method == http.MethodGet || method == http.MethodHead) {
		vals := &[2]string{locVal, "text/html; charset=utf-8"}
		h["Location"] = vals[0:1:1]
		h["Content-Type"] = vals[1:2:2]
	} else {
		h["Location"] = []string{locVal}
	}
	w.WriteHeader(code)

	if !hadCT && method == http.MethodGet {
		// fmt.Fprintln(w, body) in net/http.Redirect appends its own "\n"
		// unconditionally, on top of the one already in body — reproduced
		// here so the byte stream matches exactly. [perf-lab-2026-09-24]
		// Opt R3: a single string-concatenation expression (one
		// runtime.concatstrings call, one allocation) replaces the previous
		// two-step `body := ...; body+"\n"`, which allocated the
		// intermediate string and then the final one.
		body := "<a href=\"" + redirectHTMLReplacer.Replace(loc) + "\">" + http.StatusText(code) + "</a>.\n\n"
		_, _ = io.WriteString(w, body)
	}
}

// mwSnapshot pairs a middleware slice snapshot with a strictly increasing
// generation number, both assigned together by Use() under m.mu ([waste-hunt
// WH-10]). The generation lets lazyRedirect's cache publish with a CAS that
// always converges toward the newest snapshot, so a cache build that started
// against an already-superseded snapshot (raced by a concurrent Use() call)
// can never clobber a fresher one — see lazyRedirect.
type mwSnapshot struct {
	gen uint64
	mw  []func(http.Handler) http.Handler
}

// lazyRedirectEntry is the cached, middleware-wrapped redirect handler,
// tagged with the mwSnapshot generation it was built from.
type lazyRedirectEntry struct {
	gen uint64
	h   http.Handler
}

// redirectTarget carries a redirect's per-request target and status code to
// the cached handler built by lazyRedirect, via the request context
// ([waste-hunt WH-10]).
type redirectTarget struct {
	target string
	code   int
}

type redirectCtxKey struct{}

// redirectCtx IS the context that carries a redirect's per-request target and
// status code to the cached, middleware-wrapped handler built by lazyRedirect
// ([perf-lab-2026-09-24] fix for the rmp #248/#250 parallel regression).
// Modeled on requestCtx1/requestCtx2 (params.go): a fixed-shape type
// embedding the parent context.Context and intercepting exactly one key.
//
// This replaces the previous context.WithValue(r.Context(), redirectCtxKey{},
// redirectTarget{...}) call, which cost TWO allocations under Go's
// interface-boxing rules: one for the *context.valueCtx node itself, and a
// second to box the 24-byte redirectTarget value into that node's `val any`
// field (a value that size does not fit in an interface's single data word,
// so the runtime must heap-copy it). redirectCtx stores rt as a plain,
// unboxed struct field — reading it back is a direct field load, not an
// interface unwrap.
type redirectCtx struct {
	context.Context
	rt redirectTarget
}

// Value intercepts redirectCtxKey (used only by the rare fallback path in
// redirectTargetFrom, when a Use()-registered middleware has wrapped the
// context with a further layer before the cached handler runs); every other
// key is forwarded to the parent. This existed purely for that slow-path
// correctness — the hot path never calls Value(): see redirectTargetFrom.
func (c *redirectCtx) Value(key any) any {
	if _, ok := key.(redirectCtxKey); ok {
		return c.rt
	}
	return c.Context.Value(key)
}

// redirectBundle fuses redirectCtx and a shallow copy of *http.Request into a
// single heap allocation, mirroring reqBundle1/reqBundle2 (params.go). It
// replaces serveRedirect's previous pair of allocations — context.WithValue
// (2 allocations: see redirectCtx) plus r.WithContext (a separate cloned
// Request) — with exactly one.
//
// Safe for the same reasons as reqBundle1/reqBundle2: the bundle is freshly
// allocated and no goroutine holds a reference to it before ServeHTTP is
// called; the setReqCtxUnsafe write happens-before any goroutine the handler
// may spawn (Go memory model §goroutine creation); the original r is never
// mutated. Unlike the Opt O13 reqBundle pools, this bundle is NEVER pooled —
// a redirect is a low-traffic path (documented as ~0.33% of CPU in the
// waste-hunt report) and imposing PoolRequestBundle's "handlers/middleware
// must not retain r past return" contract here for a negligible additional
// gain is not worth the risk of a silent use-after-free for operators who
// have Use()-registered middleware that logs or forwards the redirect
// request asynchronously.
type redirectBundle struct {
	ctx redirectCtx
	req http.Request
}

// redirectTargetFrom extracts the redirect target/code carried via
// redirectCtx (serveRedirect). Mirrors routeCtxParams's fast/slow-path split
// (params.go): a direct type assertion when the context is exactly
// *redirectCtx — the common case, a type-descriptor compare with no
// allocation — falling back to the generic ctx.Value(redirectCtxKey{})
// traversal when a Use()-registered middleware has wrapped the context with
// its own layer (e.g. context.WithTimeout) before the redirect handler runs.
func redirectTargetFrom(ctx context.Context) redirectTarget {
	if rc, ok := ctx.(*redirectCtx); ok {
		return rc.rt
	}
	if v := ctx.Value(redirectCtxKey{}); v != nil {
		if rt, ok := v.(redirectTarget); ok {
			return rt
		}
	}
	return redirectTarget{}
}

// lazyRedirect returns the middleware-wrapped redirect handler for snap,
// building and caching it on first use ([waste-hunt WH-10]). Without this
// cache, serveRedirect called wrapMiddleware — re-instantiating one closure
// per registered middleware plus the redirect closure itself — on every
// single redirect.
//
// Publication is a CAS loop keyed on snap.gen rather than a plain Store,
// specifically to avoid an ABA race: serveRedirect reads redirectMWPtr
// without a lock, so a goroutine can read an old snapshot, get descheduled,
// and only reach this function after several concurrent Use() calls have
// already published newer snapshots and rebuilt the cache for them. A plain
// Store from that goroutine would silently overwrite the newer, correct
// cache entry with a stale one built from fewer middleware. Comparing
// generations (strictly increasing, assigned under m.mu by Use()) instead
// of relying on call-completion order makes that impossible: this function
// only ever publishes a strictly newer generation than what is currently
// cached, and returns a locally-built handler without publishing it when a
// fresher generation already won the race.
func (m *Mux) lazyRedirect(snap *mwSnapshot) http.Handler {
	if e := m.lazyRedirectPtr.Load(); e != nil && e.gen == snap.gen {
		return e.h
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt := redirectTargetFrom(r.Context())
		writeRedirect(w, r, rt.target, rt.code)
	})
	h := wrapMiddleware(inner, snap.mw)
	entry := &lazyRedirectEntry{gen: snap.gen, h: h}
	for {
		cur := m.lazyRedirectPtr.Load()
		if cur != nil {
			if cur.gen == snap.gen {
				return cur.h // another goroutine already published this exact generation
			}
			if cur.gen > snap.gen {
				return h // a newer generation already won; use ours locally, don't publish
			}
		}
		if m.lazyRedirectPtr.CompareAndSwap(cur, entry) {
			return h
		}
	}
}

// serveRedirect emits a redirect response. Opt R1: when no Use()-registered
// middleware wraps the redirect (the common case), writeRedirect is invoked
// directly — no middleware chain, no context allocation.
//
// Opt R2 (CH-06): reads the lock-free redirectMWPtr snapshot (refreshed by
// Use()) instead of m.mu.RLock() — the last unconditional RWMutex operation
// on the request-dispatch path, mirroring the lazyNotFound/lazyMethodNotAllowed
// / lazyOPTIONS pattern already used elsewhere in this file.
//
// [waste-hunt WH-10] When middleware IS present, the wrapped handler is
// built once per middleware generation (lazyRedirect) instead of on every
// redirect. The target and code are carried to it via the request context,
// on a shallow copy of r — middleware sees the same request (option b in the
// waste-hunt report: semantics unchanged), and the original *http.Request is
// never mutated.
//
// [perf-lab-2026-09-24] Opt R4: the shallow copy + context are fused into a
// single redirectBundle allocation (see its doc comment) instead of the
// previous context.WithValue(...) + r.WithContext(...) pair, which cost 3
// allocations (2 for context.WithValue's node + boxed value, 1 for the
// cloned Request) versus this path's 1. This fixes a measured regression:
// BenchmarkParallelRedirectTrailingSlash's ns/op and B/op got WORSE under
// the WH-10 cache (despite fewer allocs/op than the pre-cache baseline)
// because the two extra, separately-boxed heap objects it replaced were
// individually small (closures) while context.WithValue's valueCtx node,
// its boxed 24-byte redirectTarget, and the cloned ~200+-byte Request are
// each large enough that 3 separate mallocgc calls (with their own size-class
// lookups and GC scanning) cost more wall-clock time under high parallel
// allocation churn than the fixed cost saved by not rebuilding N middleware
// closures. Fusing them into one allocation keeps the byte-count-reduction
// while cutting the per-redirect allocation *count* further still.
func (m *Mux) serveRedirect(w http.ResponseWriter, r *http.Request, target string, code int) {
	snap := m.redirectMWPtr.Load()
	if snap == nil || len(snap.mw) == 0 {
		writeRedirect(w, r, target, code)
		return
	}
	rt := redirectTarget{target: target, code: code}
	if hasReqCtxField {
		b := &redirectBundle{}
		b.ctx.Context = getReqCtxUnsafe(r) // skip r.Context() method call (Opt O5a)
		b.ctx.rt = rt
		b.req = *r
		setReqCtxUnsafe(&b.req, &b.ctx)
		m.lazyRedirect(snap).ServeHTTP(w, &b.req)
		return
	}
	// Safe fallback for hypothetical future Go versions that rename `ctx`.
	ctx := context.WithValue(r.Context(), redirectCtxKey{}, rt)
	m.lazyRedirect(snap).ServeHTTP(w, r.WithContext(ctx))
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

// allowTable holds the Allow header value for every possible set of
// registered methods, indexed by a bitmask over methodNames positions
// ([waste-hunt WH-09]). Built once at init: allowed() then returns a
// constant string instead of rebuilding it with a strings.Builder on every
// 405 / auto-OPTIONS request. Output and method order are unchanged from
// the previous per-request construction.
var allowTable = func() (t [1 << methodCount]string) {
	for mask := 1; mask < len(t); mask++ {
		if mask&(1<<idxOPTIONS) != 0 || mask&(1<<idxWild) != 0 {
			continue // never produced by allowed() — OPTIONS/"*" are always excluded
		}
		var b strings.Builder
		for i := range methodCount {
			if mask&(1<<i) != 0 {
				if b.Len() > 0 {
					b.WriteString(", ")
				}
				b.WriteString(methodNames[i])
			}
		}
		b.WriteString(", ")
		b.WriteString(http.MethodOptions)
		t[mask] = b.String()
	}
	return t
}()

// allowed returns a comma-separated Allow header value for urlPath.
// Returns "" when no other methods are registered at that path.
func (m *Mux) allowed(urlPath, reqMethod string) string {
	trees := m.treesPtr.Load()
	if trees == nil {
		return ""
	}

	var mask int
	for i, root := range trees {
		if root == nil || i == idxOPTIONS || i == idxWild {
			continue
		}
		if methodNames[i] == reqMethod {
			continue
		}
		if root.hasHandler(urlPath) {
			mask |= 1 << i
		}
	}
	return allowTable[mask]
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
