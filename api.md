# Public API

Auto-generated from `go doc`. Do not edit by hand —
regenerate with: `make api`.

See [COMPATIBILITY.md](./COMPATIBILITY.md) for the SemVer tier policy.

## github.com/FlavioCFOliveira/MuxMaster

```go
package muxmaster // import "github.com/FlavioCFOliveira/MuxMaster"

Package muxmaster is a high-performance HTTP router for Go that is 100%
compatible with the standard net/http package, requires zero external
dependencies, and is built on a radix tree (compressed prefix trie) that
delivers O(k) route lookup — where k is the length of the URL path, not the
number of registered routes.

# Quick start

    mux := muxmaster.New()
    mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
        id := muxmaster.PathParam(r, "id")
        fmt.Fprintf(w, "user=%s", id)
    })
    http.ListenAndServe(":8080", mux)

# Route patterns

Static segments match literally; dynamic segments use one of three forms:

  - Named parameter: /users/:id — single non-slash segment
  - Regex parameter: /items/{id:[0-9]+} — Go regexp restricted match
  - Catch-all: /static/*filepath — matches the rest of the path

# Middleware

Three orthogonal middleware scopes are supported:

  - Mux.Use: stdlib http.Handler middleware applied at registration time;
    wraps Mux.Handle routes only. Registering a HandleFast route after Use
    panics (FPE-2026-010).
  - Mux.UseFast: FastMiddleware that wraps HandleFast routes only.
  - Mux.Pre: pre-dispatch middleware that wraps BOTH Handle and HandleFast
    routes; ideal for cross-cutting policy (auth, logging, request_id).

See the SECURITY.md "Pre vs Use security boundary" section for the full matrix.

# Performance

On AMD Ryzen 9 5900HX (Go 1.26.2):

  - Static route: ~27 ns / 0 alloc
  - 1-param route: ~128 ns / 1 alloc / 416 B
  - HandleFast 1-p: ~54 ns / 1 alloc / 32 B (parity with httprouter)

HandleFast routes bypass the requestCtx allocation by passing Params directly as
the third handler argument; they trade off stdlib middleware compatibility for
raw throughput.

# Compatibility

See COMPATIBILITY.md for the SemVer scheme, tier classification of the public
API surface, deprecation policy, and Go version policy.

Package muxmaster is a high-performance HTTP request multiplexer for Go.

Routes are matched with a radix (compressed prefix) tree, giving O(k) lookup
where k is the path length. Zero external dependencies; pure standard library.

Usage:

    mux := muxmaster.New()
    mux.Use(logger, auth)          // middleware applied to every route below
    mux.GET("/users", listUsers)
    mux.GET("/users/:id", getUser)
    mux.GET("/static/*filepath", serveFiles)

    api := mux.Group("/api/v1")
    api.Use(apiKeyCheck)
    api.POST("/items", createItem)

    http.ListenAndServe(":8080", mux)

Middleware must be registered (via Use) before the routes it should wrap.
Dynamic route registration after the server starts serving is not supported.

FUNCTIONS

func JSON(w http.ResponseWriter, code int, v any) error
    JSON marshals v to JSON and writes it with the given status code. Returns
    any marshalling or write error.

func NoContent(w http.ResponseWriter)
    NoContent writes a 204 No Content response.

func PathParam(r *http.Request, name string) string
    PathParam returns the value of the named path parameter from the request.

func Redirect(w http.ResponseWriter, r *http.Request, code int, url string)
    Redirect sends an HTTP redirect to url with the given status code.

func RoutePattern(r *http.Request) string
    RoutePattern returns the registered route pattern that matched the request,
    or "" if none has been stored in the context.

func Text(w http.ResponseWriter, code int, s string) error
    Text writes s as plain text with the given status code.

func XML(w http.ResponseWriter, code int, v any) error
    XML marshals v to XML and writes it with the given status code. Returns any
    marshalling or write error.


TYPES

type FastHandler func(http.ResponseWriter, *http.Request, Params)
    FastHandler is a high-performance request handler that receives path
    parameters as a direct argument, bypassing the context allocation overhead
    of http.Handler routes.

    Params is valid only for the lifetime of the handler call. If a goroutine is
    spawned that outlives the handler, copy the Params slice before the handler
    returns:

        func myHandler(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
            ps2 := make(muxmaster.Params, len(ps))
            copy(ps2, ps)
            go func() { use(ps2) }()
        }

    FastHandler routes do not support stdlib middleware (func(http.Handler)
    http.Handler). Use FastMiddleware instead, or register the route with Handle
    for full stdlib compatibility.

type FastMiddleware func(FastHandler) FastHandler
    FastMiddleware wraps a FastHandler, following the same composition model as
    stdlib middleware but for FastHandler routes only.

type Group struct {
	// Has unexported fields.
}
    Group is a set of routes sharing a common path prefix and middleware stack.
    Create one via Mux.Group; nest further via Group.Group.

func (g *Group) ANY(path string, h http.HandlerFunc)
    ANY registers h for all standard HTTP methods on path.

func (g *Group) CONNECT(path string, h http.HandlerFunc)
    CONNECT registers a HandlerFunc for CONNECT requests on path.

func (g *Group) DELETE(path string, h http.HandlerFunc)
    DELETE registers a HandlerFunc for DELETE requests on path.

func (g *Group) DELETEE(path string, h HandlerFuncE)
    DELETEE registers a HandlerFuncE for DELETE requests on path. Errors are
    passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) GET(path string, h http.HandlerFunc)
    GET registers a HandlerFunc for GET requests on path.

func (g *Group) GETE(path string, h HandlerFuncE)
    GETE registers a HandlerFuncE for GET requests on path. Errors are passed to
    g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) Group(prefix string) *Group
    Group returns a sub-group sharing the same mux with an extended prefix.
    The sub-group starts with a copy of the parent group's middleware stacks.

func (g *Group) HEAD(path string, h http.HandlerFunc)
    HEAD registers a HandlerFunc for HEAD requests on path.

func (g *Group) HEADE(path string, h HandlerFuncE)
    HEADE registers a HandlerFuncE for HEAD requests on path. Errors are passed
    to g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) Handle(method, path string, handler http.Handler)
    Handle registers handler under this group with the given method and path.
    The full path is g.prefix + path. Group middleware is applied after
    mux-level middleware.

func (g *Group) HandleE(method, path string, h HandlerFuncE)
    HandleE registers a HandlerFuncE under this group. Errors are passed to
    g.mux.ErrorHandler if set, otherwise a 500 is returned. The error handler
    is read from the frozen muxConfig at request time — see Mux.HandleE for the
    rationale (CSA-2026-0052).

func (g *Group) HandleFast(method, path string, h FastHandler)
    HandleFast registers a FastHandler under this group with the given method
    and path. The full path is g.prefix + path. Group FastMiddleware is applied
    before dispatch.

    SECURITY: panics if the group has stdlib middleware registered via Use().
    Stdlib middleware is incompatible with the FastHandler dispatch path —
    silently mixing them would let HandleFast routes bypass authentication,
    authorisation, logging or any other Use()-registered middleware. Operators
    must use UseFast() for FastHandler routes, or Handle() for routes that
    should run through the stdlib middleware chain.

func (g *Group) HandleFunc(method, path string, h http.HandlerFunc)
    HandleFunc registers a HandlerFunc under this group.

func (g *Group) Match(methods []string, path string, handler http.Handler)
    Match registers handler for each of the listed methods on path.

func (g *Group) Mount(prefix string, h http.Handler)
    Mount attaches h at g.prefix+prefix, stripping the full prefix before
    forwarding. The group's stdlib middleware (registered via Use) wraps the
    mounted handler so authentication, logging, etc. apply to every request
    reaching h — without this wrapping a Group with BasicAuth/JWTAuth would
    silently leave the mounted handler unprotected (MSR-2026-0062).

func (g *Group) OPTIONS(path string, h http.HandlerFunc)
    OPTIONS registers a HandlerFunc for OPTIONS requests on path.

func (g *Group) OPTIONSE(path string, h HandlerFuncE)
    OPTIONSE registers a HandlerFuncE for OPTIONS requests on path. Errors are
    passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) PATCH(path string, h http.HandlerFunc)
    PATCH registers a HandlerFunc for PATCH requests on path.

func (g *Group) PATCHE(path string, h HandlerFuncE)
    PATCHE registers a HandlerFuncE for PATCH requests on path. Errors are
    passed to g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) POST(path string, h http.HandlerFunc)
    POST registers a HandlerFunc for POST requests on path.

func (g *Group) POSTE(path string, h HandlerFuncE)
    POSTE registers a HandlerFuncE for POST requests on path. Errors are passed
    to g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) PUT(path string, h http.HandlerFunc)
    PUT registers a HandlerFunc for PUT requests on path.

func (g *Group) PUTE(path string, h HandlerFuncE)
    PUTE registers a HandlerFuncE for PUT requests on path. Errors are passed to
    g.mux.ErrorHandler if set, otherwise a 500 is returned.

func (g *Group) Route(prefix string, fn func(*Group))
    Route creates a sub-group at prefix and calls fn with it.

func (g *Group) ServeFiles(prefix string, root http.FileSystem)
    ServeFiles serves static files from root under the given prefix pattern.
    prefix must end with "/*name" (relative to the group prefix).

func (g *Group) TRACE(path string, h http.HandlerFunc)
    TRACE registers a HandlerFunc for TRACE requests on path.

func (g *Group) Use(middleware ...func(http.Handler) http.Handler)
    Use appends middleware to this group's chain. Must be called before
    registering routes on the group.

func (g *Group) UseFast(mw ...FastMiddleware)
    UseFast appends FastMiddleware to this group's fast-route chain. Must be
    called before registering HandleFast routes on the group.

func (g *Group) With(mw ...func(http.Handler) http.Handler) *Group
    With returns a copy of this group with additional middleware appended.

type HTTPError interface {
	error
	StatusCode() int
}
    HTTPError is an error that carries an HTTP status code.

func Error(code int, err error) HTTPError
    Error constructs an HTTPError wrapping err with the given HTTP status code.
    Panics if err is nil.

type HandlerFuncE func(http.ResponseWriter, *http.Request) error
    HandlerFuncE is a handler that returns an error, allowing centralised error
    handling.

type Mux struct {

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

	// Has unexported fields.
}
    Mux is a high-performance HTTP request multiplexer.

    Configuration fields (RedirectTrailingSlash, CaseInsensitive, etc.) are
    read once and frozen on the first ServeHTTP call. Use Rebuild to reset the
    snapshot when changing flags after the server has started serving.

func New() *Mux
    New returns a Mux with production-safe defaults enabled.

func (m *Mux) ANY(pattern string, h http.HandlerFunc)
    ANY registers handler for all standard HTTP methods on pattern.

func (m *Mux) CONNECT(pattern string, h http.HandlerFunc)
    CONNECT registers a HandlerFunc for CONNECT requests on pattern.

func (m *Mux) CONNECTFast(pattern string, h FastHandler)
    CONNECTFast registers a FastHandler for CONNECT requests on pattern.

func (m *Mux) DELETE(pattern string, h http.HandlerFunc)
    DELETE registers a HandlerFunc for DELETE requests on pattern.

func (m *Mux) DELETEE(pattern string, h HandlerFuncE)
    DELETEE registers a HandlerFuncE for DELETE requests on pattern. Errors are
    passed to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) DELETEFast(pattern string, h FastHandler)
    DELETEFast registers a FastHandler for DELETE requests on pattern.

func (m *Mux) GET(pattern string, h http.HandlerFunc)
    GET registers a HandlerFunc for GET requests on pattern.

func (m *Mux) GETE(pattern string, h HandlerFuncE)
    GETE registers a HandlerFuncE for GET requests on pattern. Errors are passed
    to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) GETFast(pattern string, h FastHandler)
    GETFast registers a FastHandler for GET requests on pattern.

func (m *Mux) Group(prefix string) *Group
    Group returns a RouteGroup whose routes share the given path prefix.

func (m *Mux) HEAD(pattern string, h http.HandlerFunc)
    HEAD registers a HandlerFunc for HEAD requests on pattern.

func (m *Mux) HEADE(pattern string, h HandlerFuncE)
    HEADE registers a HandlerFuncE for HEAD requests on pattern. Errors are
    passed to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) HEADFast(pattern string, h FastHandler)
    HEADFast registers a FastHandler for HEAD requests on pattern.

func (m *Mux) Handle(method, pattern string, handler http.Handler)
    Handle registers handler for the given HTTP method and path pattern.

    Path parameters use the ':name' syntax (/users/:id). Regex params use
    '{name:expr}' (/users/{id:[0-9]+}). Catch-all parameters use '*name' and
    must end the path (/static/*filepath).

    Panics on empty method, non-absolute path, nil handler, or route conflict.

func (m *Mux) HandleE(method, pattern string, h HandlerFuncE)
    HandleE registers a HandlerFuncE for the given method and path. Errors
    are passed to m.ErrorHandler if set, otherwise a 500 is returned. The
    error handler is read from the frozen muxConfig snapshot at request time,
    eliminating the data race against post-startup mutation of m.ErrorHandler
    (CSA-2026-0052).

func (m *Mux) HandleFast(method, pattern string, h FastHandler)
    HandleFast registers a FastHandler for the given HTTP method and path.

    FastHandler routes bypass the context allocation overhead of http.Handler
    routes. Params are passed as a direct argument — see FastHandler for
    lifetime guarantees.

    SECURITY: stdlib middleware (registered via Use) does NOT apply to fast
    routes. This includes the Recoverer middleware — a panic in a FastHandler
    is NOT recovered by middleware.Recoverer, regardless of the order Use was
    called. Set Mux.PanicHandler to recover panics on the FastHandler path:
    PanicHandler is invoked from dispatchWithRecover and covers both
    http.Handler and FastHandler routes. Use UseFast to attach FastMiddleware to
    fast routes; FastMiddleware runs on the FastHandler dispatch path.

    Panics on empty method, non-absolute path, nil handler, or route conflict.

func (m *Mux) HandleFunc(method, pattern string, h http.HandlerFunc)
    HandleFunc registers a HandlerFunc for the given method and path.

func (m *Mux) Lookup(method, path string) (http.Handler, Params, bool)
    Lookup performs a route lookup without dispatching a request. Returns (nil,
    nil, false) if path is empty or does not begin with '/'. For FastHandler
    routes the returned http.Handler is nil; use LookupFast when you need to
    distinguish fast routes.

func (m *Mux) Match(methods []string, pattern string, handler http.Handler)
    Match registers handler for each of the listed methods on pattern.

func (m *Mux) Mount(prefix string, h http.Handler)
    Mount attaches h at prefix, stripping the prefix before forwarding the
    request. The catch-all parameter is named "mux_mount".

func (m *Mux) OPTIONS(pattern string, h http.HandlerFunc)
    OPTIONS registers a HandlerFunc for OPTIONS requests on pattern.

func (m *Mux) OPTIONSE(pattern string, h HandlerFuncE)
    OPTIONSE registers a HandlerFuncE for OPTIONS requests on pattern. Errors
    are passed to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) OPTIONSFast(pattern string, h FastHandler)
    OPTIONSFast registers a FastHandler for OPTIONS requests on pattern.

func (m *Mux) PATCH(pattern string, h http.HandlerFunc)
    PATCH registers a HandlerFunc for PATCH requests on pattern.

func (m *Mux) PATCHE(pattern string, h HandlerFuncE)
    PATCHE registers a HandlerFuncE for PATCH requests on pattern. Errors are
    passed to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) PATCHFast(pattern string, h FastHandler)
    PATCHFast registers a FastHandler for PATCH requests on pattern.

func (m *Mux) POST(pattern string, h http.HandlerFunc)
    POST registers a HandlerFunc for POST requests on pattern.

func (m *Mux) POSTE(pattern string, h HandlerFuncE)
    POSTE registers a HandlerFuncE for POST requests on pattern. Errors are
    passed to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) POSTFast(pattern string, h FastHandler)
    POSTFast registers a FastHandler for POST requests on pattern.

func (m *Mux) PUT(pattern string, h http.HandlerFunc)
    PUT registers a HandlerFunc for PUT requests on pattern.

func (m *Mux) PUTE(pattern string, h HandlerFuncE)
    PUTE registers a HandlerFuncE for PUT requests on pattern. Errors are passed
    to m.ErrorHandler if set, otherwise a 500 is returned.

func (m *Mux) PUTFast(pattern string, h FastHandler)
    PUTFast registers a FastHandler for PUT requests on pattern.

func (m *Mux) Pre(mw ...func(http.Handler) http.Handler)
    Pre registers middleware that runs before dispatch (e.g. before routing).
    Calling Pre rebuilds the pre-dispatch handler chain.

    SECURITY (CSA-2026-0059): Pre wraps the entire ServeHTTP dispatch and
    covers BOTH Handle (stdlib) and HandleFast routes. This makes Pre the
    correct registration point for cross-cutting policies that must apply
    uniformly — auth gates, CleanPath, RealIP, RecovererWithLogger, request IDs.
    See SECURITY.md "Pre vs Use security boundary".

func (m *Mux) Rebuild()
    Rebuild resets the frozen configuration snapshot and the lazy NotFound
    / MethodNotAllowed / OPTIONS handler caches so the next ServeHTTP call
    re-reads every configuration field and rebuilds the wrapped handlers.

    Safe to call concurrently with ServeHTTP: every reset is a single atomic
    operation, and the next config() / lazyNotFound() / lazyMethodNotAllowed()
    / lazyOPTIONS() call re-initialises via CompareAndSwap or sync.Map
    re-population. Intended for tests and dynamic reconfiguration scenarios.

func (m *Mux) Route(prefix string, fn func(*Group))
    Route creates a sub-group at prefix and calls fn with it.

func (m *Mux) Routes() []RouteInfo
    Routes returns a slice of RouteInfo for every registered route, including
    both http.Handler and FastHandler routes.

func (m *Mux) ServeFiles(prefix string, root http.FileSystem)
    ServeFiles serves static files from root under the given prefix pattern.
    prefix must end with "/*name" (e.g. "/static/*filepath").

    SECURITY (CDX-S8-002): http.FileServer applies path.Clean internally,
    so a request like /static/../etc/passwd cannot escape root. However,
    when the Mux is configured with UseRawPath=true AND UnescapePathValues=true
    the captured filepath param contains decoded slashes (PRF-2026-0002)
    and http.FileServer's clean step happens AFTER the param has already
    been re-set as r2.URL.Path — the decoded slashes act as path separators
    inside FileServer's tree. Registration with that combination panics
    so the misconfiguration is caught at boot. Disable one of UseRawPath /
    UnescapePathValues, or write a custom handler that calls path.Clean on the
    captured value and rejects ".." segments before dispatch.

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request)
    ServeHTTP implements http.Handler, dispatching through pre-middleware if
    set.

    The fast path (no PanicHandler, no pre-middleware) avoids the defer frame
    overhead entirely by going straight to dispatch. Deferred paths are isolated
    in dispatchWithRecover to keep this function inlineable.

func (m *Mux) TRACE(pattern string, h http.HandlerFunc)
    TRACE registers a HandlerFunc for TRACE requests on pattern.

func (m *Mux) TRACEFast(pattern string, h FastHandler)
    TRACEFast registers a FastHandler for TRACE requests on pattern.

func (m *Mux) Use(middleware ...func(http.Handler) http.Handler)
    Use appends one or more middleware to the chain. Each middleware wraps
    all handlers registered after this call. The first middleware added is
    outermost.

    SECURITY (CSA-2026-0059): Use does NOT wrap HandleFast routes — registering
    a fast route after Use(authMiddleware) panics at HandleFast call time on
    BOTH the root Mux (FPE-2026-010) and Groups (CSA-2026-0054), so the bypass
    cannot occur silently regardless of where the operator places the route.
    To apply policy to both stdlib and fast routes, use Pre(...) (outermost,
    route-type agnostic) or UseFast(...) for FastMiddleware. See SECURITY.md
    "Pre vs Use security boundary".

func (m *Mux) UseFast(mw ...FastMiddleware)
    UseFast appends one or more FastMiddleware to the chain applied to all
    HandleFast routes registered after this call. The first middleware added is
    outermost. Has no effect on routes registered via Handle.

    SECURITY (CSA-2026-0059): UseFast is the FastHandler counterpart of Use;
    together with Pre (which covers BOTH route types) it forms the route-type
    matrix documented in SECURITY.md "Pre vs Use security boundary". An auth
    gate applied only via Use(...) does NOT cover HandleFast routes.

func (m *Mux) Walk(fn func(method, pattern string, handler http.Handler) error) error
    Walk calls fn for each registered http.Handler route. FastHandler routes are
    skipped — use WalkFast to visit them. Stops iteration and returns the error
    if fn returns non-nil.

func (m *Mux) WalkFast(fn func(method, pattern string, handler FastHandler) error) error
    WalkFast calls fn for each registered FastHandler route. http.Handler routes
    are skipped — use Walk to visit them. Stops iteration and returns the error
    if fn returns non-nil.

func (m *Mux) With(mw ...func(http.Handler) http.Handler) *Group
    With returns a Group with the given middleware applied and an empty prefix.

type Param struct {
	Key   string
	Value string
}
    Param is a single URL path parameter (key + value).

type Params []Param
    Params is an ordered list of path parameters extracted from a URL.

func ParamsFromContext(ctx context.Context) Params
    ParamsFromContext returns the path parameters stored in ctx.

func (ps Params) Bool(name string) (bool, error)
    Bool returns the named parameter parsed as bool.

func (ps Params) Float64(name string) (float64, error)
    Float64 returns the named parameter parsed as float64.

func (ps Params) Get(name string) string
    Get returns the value for the named parameter, or "" if not present.

func (ps Params) Int(name string) (int, error)
    Int returns the named parameter parsed as int. Returns errParamNotFound if
    the key is absent, or a strconv error on parse failure.

func (ps Params) Int64(name string) (int64, error)
    Int64 returns the named parameter parsed as int64 (base 10).

func (ps Params) Lookup(name string) (value string, ok bool)
    Lookup returns the value and a presence flag for the named parameter.

func (ps Params) Map() map[string]string
    Map returns a copy of the parameters as a string map.

func (ps Params) Uint64(name string) (uint64, error)
    Uint64 returns the named parameter parsed as uint64 (base 10).

type RouteInfo struct {
	Method  string
	Pattern string
	Handler string
}
    RouteInfo describes a single registered route.

```

## github.com/FlavioCFOliveira/MuxMaster/middleware

```go
package middleware // import "github.com/FlavioCFOliveira/MuxMaster/middleware"

Package middleware provides common HTTP middleware handlers for use with
MuxMaster or any net/http-compatible router.

Each middleware is a function that takes an http.Handler and returns an
http.Handler, following the standard Go middleware pattern:

    func(next http.Handler) http.Handler

Usage with MuxMaster:

    import "github.com/FlavioCFOliveira/MuxMaster/middleware"

    mux := muxmaster.New()
    mux.Use(middleware.Logger(os.Stdout))
    mux.Use(middleware.Recoverer)
    mux.Use(middleware.CORS(middleware.CORSOptions{
        AllowedOrigins: []string{"https://example.com"},
    }))

CONSTANTS

const DefaultThrottlePerIPMaxTableSize = 100_000
    DefaultThrottlePerIPMaxTableSize is the default upper bound on the number of
    distinct keys ThrottlePerIP will track concurrently. When the table is full,
    requests for NEW keys are rejected with 503 to bound memory under IP-churn
    attacks (MSR-2026-0068). Existing keys keep working.


FUNCTIONS

func APIKey(opts APIKeyOptions) func(http.Handler) http.Handler
    APIKey authenticates requests by matching an extracted key against a
    pre-hashed set of valid keys. All keys are SHA-256 hashed at construction
    time; per-request cost is one SHA-256 hash plus a [32]byte map lookup — no
    iteration, no string comparison.

    On success, the identity string associated with the key is injected into the
    request context and retrievable via GetAPIKeyIdentity. Panics if opts.Keys
    is nil or empty.

func BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler
    BasicAuth enforces HTTP Basic Authentication using constant-time credential
    comparison. Passwords are SHA-256 hashed at construction time so that
    subtle.ConstantTimeCompare always operates on equal-length inputs,
    eliminating both user-enumeration timing (MM-2026-0009) and password-length
    oracle (MM-2026-0020). Panics if creds is nil.

func CORS(opts CORSOptions) func(http.Handler) http.Handler
    CORS handles Cross-Origin Resource Sharing. Panics on invalid configuration.

    SECURITY: AllowedOrigins must be set explicitly. Passing nil or an
    empty slice is a misconfiguration trap (HPS-2026-0003): the middleware
    would silently let cross-origin requests through with no ACAO header,
    hiding the issue from the operator. We panic at construction time so the
    misconfiguration is caught at boot.

    ORDERING (MSR-2026-0070): CORS sets `Access-Control-Allow-Origin` (and
    related Access-Control-* + Vary headers) when its frame runs. If another
    middleware that calls `Header().Set(...)` runs AFTER CORS in the request
    flow (innermost in the Use() chain), the late Set will OVERWRITE the
    CORS-managed values, silently bypassing the configured whitelist. To keep
    CORS authoritative, register CORS as the INNERMOST middleware that touches
    these headers (i.e. last in the Use() chain that handles them) or avoid
    calling SetHeader on CORS-managed names. See SetHeader for the composition
    rule.

func CleanPath() func(http.Handler) http.Handler
    CleanPath normalises r.URL.Path via path.Clean before routing. When
    r.URL.RawPath is set, it is also cleaned; if the cleaned RawPath differs
    from what path.Clean produces for the percent-decoded Path, RawPath is
    zeroed to prevent encoded path-traversal bypass (MM-2026-0018).

func Compress(level int) func(http.Handler) http.Handler
    Compress compresses responses with gzip when the client accepts it.
    Responses smaller than 1024 bytes are passed through uncompressed. Uses
    streaming compression — memory usage is bounded regardless of response size.
    Panics on invalid compression level.

    OPERATIONAL (DOS-2026-0007): the compress middleware buffers up to 8 KiB
    per stalled connection while sniffing whether the response is large enough
    to compress. Operators MUST configure http.Server.ReadHeaderTimeout and
    http.Server.WriteTimeout (and a connection cap via a Listener limit) to
    bound the total memory held by N stalled connections; the middleware itself
    does not enforce a per-connection timeout.

    SECURITY (BREACH / DOS-2026-0006): do NOT echo user-controlled input
    alongside a secret (OAuth2 scope, CSRF token, session ID, JWT) inside
    a gzip-compressed response body. Compression amplifies tiny size
    differences that depend on whether the user's input matches a prefix
    of the secret — this is the BREACH oracle (Cohen's d > 10 measured in
    reports/dos-resilience-tester/harness/breach_oracle_test.go), letting an
    attacker recover the secret character-by-character with ~2 requests per
    character.

    Mitigations, in order of preference:

     1. Do not compress endpoints that echo user input near secrets — wrap them
        with a different middleware chain that excludes Compress.
     2. Move secrets out of the response body (set them in headers, cookies,
        or separate API endpoints not reachable via attacker-controlled input).
     3. Add variable-length random padding (>= 256 bytes, length
        randomised per request) to the response body. Validated by
        TestBREACHOracleWithRandomPadding (Cohen's d drops below 0.03).

    MuxMaster cannot apply these mitigations on the operator's behalf because
    they require knowledge of which fields are secret vs user-controlled.
    See SECURITY.md "BREACH mitigation" for the full pattern.

func GetAPIKeyIdentity(ctx context.Context) (string, bool)
    GetAPIKeyIdentity returns the identity string associated with the validated
    API key, as injected by the APIKey middleware.

func GetRequestID(ctx context.Context) string
    GetRequestID returns the request ID stored in ctx, or "" if absent.

func JWTAuth(opts JWTOptions) func(http.Handler) http.Handler
    JWTAuth validates JWT Bearer tokens from the Authorization header. Signature
    is always verified before claims are parsed to prevent payload manipulation.
    On success, claims are injected into the request context via GetJWTClaims.

    Panics if Algorithms is empty or if the required key material is missing for
    any listed algorithm.

func Logger(out io.Writer) func(http.Handler) http.Handler
    Logger logs each request after it completes. Panics if out is nil.

func NoCache() func(http.Handler) http.Handler
    NoCache sets response headers to prevent caching at every layer: browsers
    (Cache-Control / Pragma / Expires), CDNs (Surrogate-Control) and nginx-style
    reverse proxies (X-Accel-Expires). All headers are harmless to deployments
    that do not run an intermediate cache.

func OAuth2Introspect(opts OAuth2Options) func(http.Handler) http.Handler
    OAuth2Introspect validates Bearer tokens via RFC 7662 token introspection.
    Active tokens are cached (keyed by sha256(token)) to avoid per-request
    network calls. On success, the IntrospectResponse is available via
    GetOAuth2Claims.

    Panics if opts.Endpoint is empty, malformed, or non-HTTPS (unless
    opts.AllowInsecureEndpoint is true). Bearer tokens transmitted over
    plaintext are exposed to passive observers (MSR-2026-0067 / RFC 7662 §4).

func RealIP(trustedCIDRs ...*netip.Prefix) func(http.Handler) http.Handler
    RealIP overwrites r.RemoteAddr with the client IP derived from the
    X-Forwarded-For or X-Real-IP header. Only mutates RemoteAddr when the direct
    peer is within one of the trusted CIDR prefixes.

    XFF selection (MSR-2026-0065): the header is parsed as a comma-separated
    list and walked from RIGHTMOST toward leftmost, skipping entries that
    lie inside any trustedCIDRs. The first entry NOT inside a trusted CIDR
    is the real client IP. This rejects attacker-injected leftmost values:
    in a multi-hop chain (proxy1 + proxy2), if only proxy2 is trusted,
    the leftmost-XFF approach would pick a forged `client_ip` injected by the
    attacker, but the rightmost walk stops at proxy1 (the first untrusted hop)
    and falls back to its address. When the entire chain consists of trusted
    CIDRs, the leftmost entry is used as a last resort. With a single trusted
    proxy stripping inbound XFF (the documented baseline) the behaviour is
    identical to picking the leftmost.

    SECURITY (MSR-2026-0055): calling RealIP() with no CIDRs trusts every
    peer — any client can spoof the X-Forwarded-For / X-Real-IP header and the
    router will accept it as the real client IP. This is only safe behind a
    single trusted proxy that strips inbound XFF; in any other deployment it
    is a trivial spoofing primitive that defeats ThrottlePerIP and IP-based
    access controls. The middleware emits a slog.Warn at construction time when
    called without CIDRs so the misconfiguration is visible in startup logs.
    ALWAYS pass the proxy CIDR list explicitly in production.

    Proxy depth: each additional trusted proxy in the forwarding chain MUST
    be covered by a trustedCIDRs entry, otherwise the rightmost-walk stops too
    early and the proxy IP (rather than the real client) becomes RemoteAddr.

    IP values are validated via netip.ParseAddr, which rejects CRLF injection
    and malformed addresses, and IPv6 zone IDs are stripped via WithZone("")
    (FPE-2026-003 / FPE-2026-003b).

func Recoverer() func(http.Handler) http.Handler
    Recoverer recovers from panics and writes a 500 response. Logs via
    slog.Default() — use RecovererWithLogger for a custom logger.

    Deprecated: use RecovererWithLogger(slog.Default()) for explicit control.

func RecovererWithLogger(logger *slog.Logger) func(http.Handler) http.Handler
    RecovererWithLogger recovers from panics, logs the panic value and stack
    trace at Error level via logger, and writes a plain 500 response. The panic
    value is never written to the response body, preventing information leakage
    to clients (MM-2026-0023).

func RequestID() func(http.Handler) http.Handler
    RequestID generates or propagates a request ID via X-Request-ID header.
    Incoming X-Request-ID values are validated; invalid or oversized values are
    replaced with a freshly generated random ID (MM-2026-0011).

func SetHeader(key, value string) func(http.Handler) http.Handler
    SetHeader sets a fixed response header before calling the next handler.
    Panics at construction time if key or value contains CR or LF, as those
    bytes can reach downstream middleware in raw form even though Go's wire
    serialiser strips them before writing to the network.

    ORDERING (MSR-2026-0070): SetHeader runs Header().Set on the response at the
    START of its frame, BEFORE calling next. Middleware composition in MuxMaster
    wraps from outermost to innermost — the first Use() call is outermost. So if
    Use(CORS, SetHeader(...)) is registered, SetHeader runs LAST in the request
    flow (innermost) and its Set() will OVERWRITE any header CORS already wrote.
    Specifically, Use(CORS, SetHeader( "Access-Control-Allow-Origin", "*"))
    silently bypasses the CORS allowed origins whitelist for every request.

    To preserve CORS guarantees, register SetHeader BEFORE CORS in the Use()
    chain (so SetHeader is the outermost frame and CORS overwrites it for the
    headers it manages), or omit any SetHeader call that targets a CORS-managed
    header (Access-Control-Allow-Origin, -Methods, -Headers, -Credentials,
    -Max-Age, -Expose-Headers, Vary).

func StripSlashes() func(http.Handler) http.Handler
    StripSlashes removes all trailing slashes from r.URL.Path before routing.
    Idempotent: "/a///" becomes "/a" (not "/a//").

    When r.URL.RawPath is set (the original raw form is preserved by
    net/http only when it differs from the decoded Path), StripSlashes
    also strips trailing '/' bytes from RawPath. Without this the dispatch
    path would diverge between Path and RawPath when Mux.UseRawPath is true
    (HPS-2026-0004).

func ThrottleAllBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler
    ThrottleAllBacklog is the renamed ThrottleBacklog — limits concurrency
    globally across ALL clients combined. Use ThrottlePerIP for per-client rate
    limiting.

    Deprecated: Use ThrottleAllBacklog. ThrottleBacklog remains for
    compatibility.

func ThrottleBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler
    ThrottleBacklog limits concurrent handler execution with a backlog queue.
    Panics if limit <= 0 or backlog < 0.

func ThrottlePerIP(limit int, timeout time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler
    ThrottlePerIP limits concurrent handler executions per client key.
    keyFn extracts the rate-limit key from the request; if nil, the host part
    of r.RemoteAddr is used. limit is the maximum concurrent requests per key;
    timeout is how long a request waits for a slot before receiving 503.

    SECURITY (DOS-2026-0002): when keyFn is nil, ThrottlePerIP keys on
    r.RemoteAddr — which is whatever the TCP peer's address is unless RealIP
    has previously rewritten it. Behind a load balancer that does not strip the
    LB's own address from RemoteAddr, every request appears to come from the LB
    and the per-IP limit degrades to a global rate limit. RealIP (with explicit
    trusted-proxy CIDRs) MUST be registered BEFORE ThrottlePerIP for per-client
    limits to be effective. See SECURITY.md "RealIP + ThrottlePerIP ordering".

    SECURITY (MSR-2026-0068): the internal per-key table is capped at
    DefaultThrottlePerIPMaxTableSize. When full, requests for NEW keys (those
    not already in the table) receive 503 immediately to bound memory under
    IP-churn attacks. Use ThrottlePerIPCapped to override the cap.

    Panics if limit <= 0.

func ThrottlePerIPCapped(limit int, timeout time.Duration, maxTableSize int, keyFn func(*http.Request) string) func(http.Handler) http.Handler
    ThrottlePerIPCapped is ThrottlePerIP with an explicit cap on the number of
    distinct keys tracked. maxTableSize <= 0 disables the cap (legacy unbounded
    behaviour, NOT recommended in production).

    SATURATION BEHAVIOUR (TM-2026-013, DOS-2026-0057): when the table reaches
    maxTableSize and every slot has refs > 0 (i.e. all entries are in active
    use), new client IPs receive 503 immediately until at least one slot drains.
    An attacker controlling N >= maxTableSize distinct IPs can sustain this
    state for as long as their requests stay open. Mitigations:
      - deploy upstream DDoS scrubbing (Cloudflare, AWS Shield, etc.);
      - configure http.Server{ReadHeaderTimeout, IdleTimeout, ReadTimeout} so
        slow-handler connections cannot hold slots indefinitely;
      - lower maxTableSize for sensitive endpoints.

    Panics if limit <= 0.

func Timeout(d time.Duration) func(http.Handler) http.Handler
    Timeout applies a context deadline to each request. Panics if d <= 0.

    SECURITY (DOS-2026-0003): Timeout cancels the request context after d,
    but it does NOT preempt the handler goroutine — Go has no preemption
    primitive for blocked syscalls. Handlers MUST observe ctx.Done() on every
    blocking call (DB, network, file I/O); a handler that ignores ctx.Done()
    will run to completion regardless of the timeout, accumulating goroutines
    under load and exhausting memory or upstream connections. Co-design Timeout
    with handler-level cooperation (use the *Context variants of the stdlib —
    sql.DB.QueryContext, net/http with http.Request, etc.).

func WithValue(key, val any) func(http.Handler) http.Handler
    WithValue injects a value into the request context. To avoid context key
    collisions between packages, always use an unexported type as the key:

        type ctxKey struct{}
        mux.Use(middleware.WithValue(ctxKey{}, myValue))

    MSR-2026-0059: passing a string (or any built-in type) as the key is a
    CWE-1021 cross-package collision risk — any package that uses the same
    string literal can read or overwrite this value. WithValue emits a slog.Warn
    at construction time when called with a string-kind key.


TYPES

type APIKeyOptions struct {
	// Keys maps raw API key values to identity strings injected into the request context.
	// Panics if nil or empty.
	Keys map[string]string
	// Header is the request header name to read when ExtractFn is nil. Default: "X-API-Key".
	Header string
	// ExtractFn overrides key extraction. If nil, the Header field is used.
	ExtractFn func(*http.Request) string
}
    APIKeyOptions configures the APIKey middleware.

type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}
    CORSOptions configures CORS behaviour.

type IntrospectResponse struct {
	Active    bool
	Subject   string
	Scope     string
	ClientID  string
	Username  string
	TokenType string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	Issuer    string
	Audience  []string
}
    IntrospectResponse holds the RFC 7662 token introspection response fields.

func GetOAuth2Claims(ctx context.Context) (*IntrospectResponse, bool)
    GetOAuth2Claims returns the IntrospectResponse injected by the
    OAuth2Introspect middleware.

type JWTClaims struct {
	Subject   string
	Issuer    string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	// RawPayload is the decoded JSON payload bytes, available for extracting custom claims.
	RawPayload []byte
}
    JWTClaims holds the standard JWT claims extracted from a validated token.
    Custom claims can be unmarshalled from RawPayload.

func GetJWTClaims(ctx context.Context) (*JWTClaims, bool)
    GetJWTClaims returns the JWT claims injected by the JWTAuth middleware.

type JWTOptions struct {
	// Secret is the HMAC signing key, required for HS256, HS384, HS512.
	Secret []byte
	// PublicKey is the RSA or ECDSA public key, required for RS*/ES* algorithms.
	PublicKey crypto.PublicKey
	// Algorithms lists accepted signing algorithms. Must be non-empty.
	// Supported: HS256, HS384, HS512, RS256, RS384, RS512, ES256, ES384, ES512.
	// SEE the SECURITY note on JWTOptions about mixing families.
	Algorithms []string
	// Issuers, if non-empty, restricts accepted "iss" claim values.
	Issuers []string
	// Audiences, if non-empty, requires at least one "aud" entry to match.
	Audiences []string
	// ClockSkew is the permitted clock drift applied to exp and nbf checks. Default: 0.
	ClockSkew time.Duration
	// RequireExpiry, when true, rejects any token whose payload has no "exp"
	// claim. RFC 8725 §4.4 recommends rejecting tokens without expiry unless
	// there is a compelling reason: a stolen token without "exp" is valid
	// indefinitely (TM-2026-001).
	//
	// SECURITY: production deployments SHOULD set RequireExpiry: true. The
	// default is false ONLY for backward compatibility with code written
	// before this option existed. JWTAuth emits a slog.Warn at construction
	// time when this option is left at the unsafe default so the
	// misconfiguration is visible in startup logs.
	//
	// Default: false (backward compatible — DO NOT use in production).
	RequireExpiry bool
}
    JWTOptions configures the JWTAuth middleware.

    SECURITY (TSC-2026-0003): mixing algorithm families (HS* with RS* or ES*)
    in Algorithms leaks the algorithm path via response latency. HMAC verifies
    in ~1 µs, RSA-2048 verifies in ~300 µs, and an attacker submitting tokens
    with different alg labels can determine which path the server runs from the
    response time alone — narrowing the attack surface for algorithm-confusion
    attacks (RFC 8725 §3.1). Configure each endpoint with a single algorithm
    family. JWTAuth emits a slog.Warn at construction time when a mixed-family
    Algorithms list is detected.

type OAuth2Options struct {
	// Endpoint is the RFC 7662 introspection URL. Required.
	// MUST use the https:// scheme — bearer tokens transmitted over plaintext
	// are exposed to passive observers and MITM attackers (RFC 7662 §4 / RFC
	// 6749 §1.6). Construction panics on a non-HTTPS endpoint unless
	// AllowInsecureEndpoint is explicitly set to true (testing/localhost only).
	Endpoint string
	// AllowInsecureEndpoint disables the HTTPS-only enforcement on Endpoint.
	// Set to true ONLY for testing or trusted-local-loopback deployments —
	// production traffic must always use HTTPS. When true, a one-time slog
	// warning is emitted at construction time. Default: false.
	AllowInsecureEndpoint bool
	// ClientID and ClientSecret authenticate to the introspection endpoint via HTTP Basic.
	ClientID     string
	ClientSecret string
	// CacheTTL is how long active tokens are cached. Default: 60s.
	// Set to -1 (or any negative value) to disable caching entirely — every
	// request hits the introspection endpoint, eliminating the cache-poisoning
	// blast radius (MSR-2026-0063) at the cost of higher IDP load. Use
	// disabled caching for high-security endpoints; the singleflight group
	// (DOS-OAUTH2-001 fix) still coalesces concurrent introspection calls
	// for the same token.
	// Cache respects the token's own exp: effective TTL = min(CacheTTL, token.exp - now).
	// Note: caching means revoked tokens remain valid until TTL expires.
	CacheTTL time.Duration
	// MaxCacheSize caps the number of cached active tokens. Default: 10000.
	MaxCacheSize int
	// HTTPClient is used for introspection requests. Default: 10s timeout.
	HTTPClient *http.Client
	// ExtractFn overrides token extraction. Default: "Authorization: Bearer <token>".
	ExtractFn func(*http.Request) string
}
    OAuth2Options configures the OAuth2Introspect middleware.

```
