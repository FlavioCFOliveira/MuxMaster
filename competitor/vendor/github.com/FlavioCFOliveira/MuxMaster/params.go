package muxmaster

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"unsafe"
)

// Param is a single URL path parameter (key + value).
type Param struct {
	Key   string
	Value string
}

// Params is an ordered list of path parameters extracted from a URL.
type Params []Param

// Get returns the value for the named parameter, or "" if not present.
func (ps Params) Get(name string) string {
	for i := range ps {
		if ps[i].Key == name {
			return ps[i].Value
		}
	}
	return ""
}

// Lookup returns the value and a presence flag for the named parameter.
func (ps Params) Lookup(name string) (value string, ok bool) {
	for i := range ps {
		if ps[i].Key == name {
			return ps[i].Value, true
		}
	}
	return "", false
}

// Int returns the named parameter parsed as int.
// Returns errParamNotFound if the key is absent, or a strconv error on parse failure.
func (ps Params) Int(name string) (int, error) {
	v, ok := ps.Lookup(name)
	if !ok {
		return 0, errParamNotFound
	}
	return strconv.Atoi(v)
}

// Int64 returns the named parameter parsed as int64 (base 10).
func (ps Params) Int64(name string) (int64, error) {
	v, ok := ps.Lookup(name)
	if !ok {
		return 0, errParamNotFound
	}
	return strconv.ParseInt(v, 10, 64)
}

// Uint64 returns the named parameter parsed as uint64 (base 10).
func (ps Params) Uint64(name string) (uint64, error) {
	v, ok := ps.Lookup(name)
	if !ok {
		return 0, errParamNotFound
	}
	return strconv.ParseUint(v, 10, 64)
}

// Float64 returns the named parameter parsed as float64.
func (ps Params) Float64(name string) (float64, error) {
	v, ok := ps.Lookup(name)
	if !ok {
		return 0, errParamNotFound
	}
	return strconv.ParseFloat(v, 64)
}

// Bool returns the named parameter parsed as bool.
func (ps Params) Bool(name string) (bool, error) {
	v, ok := ps.Lookup(name)
	if !ok {
		return false, errParamNotFound
	}
	return strconv.ParseBool(v)
}

// Map returns a copy of the parameters as a string map.
func (ps Params) Map() map[string]string {
	m := make(map[string]string, len(ps))
	for i := range ps {
		m[ps[i].Key] = ps[i].Value
	}
	return m
}

var errParamNotFound = errors.New("muxmaster: parameter not found")

// --------------------------------------------------------------------------
// Tiered requestCtx / reqBundle types
//
// Each tier is sized to the actual param count so that the GC allocation
// size class matches the minimum required memory:
//
//   requestCtx1 + http.Request = 64 + 304 = 368 B → size class 384 B (Opt O12)
//   requestCtx2 + http.Request = 96 + 304 = 400 B → size class 416 B (Opt O12)
//   requestCtx  + http.Request = 152 + 304 = 456 B → size class 480 B
//
// Opt O12: requestCtx1 and requestCtx2 no longer carry a `params Params`
// field — the slice header is derived on access from `small[:N]`. This
// saves 24 bytes per allocation on the 1- and 2-param tiers, dropping
// them into smaller GC size classes (384/416 vs the previous 416/448).
// The slice header is stack-allocated each access (no heap traffic).
// The 3+-param tier retains `params` because it must support overflow.
//
// All three types implement context.Context identically — they intercept
// the route-params context key and forward everything else to the parent.
// --------------------------------------------------------------------------

// requestCtx1 IS the context for routes with exactly 1 parameter.
// Opt O12: no `params Params` field — derived from `small[:1]` on access.
type requestCtx1 struct {
	context.Context
	pattern string
	small   [1]Param
}

// Value intercepts the route-params context key; forwards all other lookups.
func (c *requestCtx1) Value(key any) any {
	if _, ok := key.(contextKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// reqBundle1 fuses requestCtx1 and a cloned http.Request in a single heap
// allocation (368 B, size class 384 B) for routes with exactly 1 parameter.
//
// The fused allocation is safe because:
//   - bundle is freshly allocated — no goroutine has a reference yet
//   - setReqCtxUnsafe writes happen-before any goroutine spawned in ServeHTTP
//   - the original r is never modified
//   - lifetime is GC-managed (never pooled)
type reqBundle1 struct {
	ctx requestCtx1
	req http.Request
}

// requestCtx2 IS the context for routes with exactly 2 parameters.
// Opt O12: no `params Params` field — derived from `small[:2]` on access.
type requestCtx2 struct {
	context.Context
	pattern string
	small   [2]Param
}

// Value intercepts the route-params context key; forwards all other lookups.
func (c *requestCtx2) Value(key any) any {
	if _, ok := key.(contextKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// reqBundle2 fuses requestCtx2 and a cloned http.Request in a single heap
// allocation (400 B, size class 416 B) for routes with exactly 2 parameters.
type reqBundle2 struct {
	ctx requestCtx2
	req http.Request
}

// requestCtx IS the context for routes with 3 or more parameters.
type requestCtx struct {
	context.Context
	params  Params
	pattern string
	// small holds inline param data for routes with ≤3 params, eliminating
	// a separate slice allocation. params points into small[:n].
	small [3]Param
}

// Value intercepts the route-params context key; forwards all other lookups.
func (c *requestCtx) Value(key any) any {
	if _, ok := key.(contextKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// reqBundle fuses requestCtx and a cloned http.Request in a single heap
// allocation (456 B, size class 480 B) for routes with 3 or more parameters.
type reqBundle struct {
	ctx requestCtx
	req http.Request
}

type contextKey struct{}

// reqCtxFieldOffset is the byte offset of the unexported ctx field within
// http.Request. Computed once at init via reflect; hasReqCtxField is false
// if the field is absent (forward-compatible with future Go versions that
// may rename or remove it), falling back to the safe r.WithContext path.
var (
	reqCtxFieldOffset uintptr
	hasReqCtxField    bool
)

func init() {
	t := reflect.TypeOf(http.Request{})
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Name == "ctx" && f.Type == ctxType {
			reqCtxFieldOffset = f.Offset
			hasReqCtxField = true
			break
		}
	}
}

// setReqCtxUnsafe writes ctx into req.ctx via the pre-computed field offset.
// MUST only be called on a freshly-allocated *http.Request that no other
// goroutine can access. The write is happens-before any goroutine that
// ServeHTTP may spawn (Go memory model §goroutine creation).
//
//go:nosplit
func setReqCtxUnsafe(req *http.Request, ctx context.Context) {
	*(*context.Context)(unsafe.Add(unsafe.Pointer(req), reqCtxFieldOffset)) = ctx
}

// getReqCtxUnsafe reads req.ctx directly via the pre-computed field offset.
// Opt O5a: r.Context() is a method call that does a nil check + falls back to
// context.Background(). Inside MuxMaster's dispatch the request was just
// received from net/http (server.go always sets req.ctx before ServeHTTP) or
// httptest.NewRequest (which also sets it). The field is therefore guaranteed
// non-nil and we can skip the method call.
//
// MUST only be called when hasReqCtxField is true (validated by init()).
//
//go:nosplit
func getReqCtxUnsafe(req *http.Request) context.Context {
	return *(*context.Context)(unsafe.Add(unsafe.Pointer(req), reqCtxFieldOffset))
}

// Opt O13: reqBundle pools recycle the fused requestCtx + http.Request copy
// handed to http.Handler routes with path parameters. They are used only
// when Mux.PoolRequestBundle is true (opt-in — see mux.go for the strict
// lifetime contract that handlers must observe).
//
// Each pool corresponds to a tier (1/2/3+ params) so that get/put never
// shuffles bundles between size classes. The 3+-param pool's bundle
// contains a heap-allocated overflow Params slice for routes with more
// than 3 params; that slice is dropped (set to nil) before Put and
// re-allocated on the next Get when needed — this keeps the pool's
// bundle size constant and avoids retaining the overflow slice across
// requests.
//
// SAFETY: handlers MUST NOT retain *http.Request past return. The
// zeroing on Put prevents the *next* request from observing stale state
// in fields the handler may have written, but it does NOT make stale
// references safe in goroutines that survive the handler.
var (
	reqBundle1Pool = sync.Pool{New: func() any { return new(reqBundle1) }}
	reqBundle2Pool = sync.Pool{New: func() any { return new(reqBundle2) }}
	reqBundlePool  = sync.Pool{New: func() any { return new(reqBundle) }}
)

// Opt O9: fastParamsPool recycles the Params slices handed to FastHandler
// routes. Three tiers match the common-case sizes exactly (1/2/3 params),
// avoiding heap allocation on every fast-route dispatch.
//
// SAFETY CONTRACT: FastHandler implementations MUST NOT retain the Params
// slice (or any backing element) after the handler returns. The
// dispatcher zeroes the slice and returns it to the pool the instant
// ServeHTTP completes; a goroutine still holding a reference would race
// with the next request reusing the same backing array. The contract is
// stated in handler.go on the FastHandler type and reiterated in
// SECURITY.md "FastHandler params lifetime". If a handler needs to retain
// params, it MUST copy them first.
// The pool stores pointer-to-array (not pointer-to-slice) so that the slice
// header itself does not need to escape on Put. `(*[N]Param)(slice[:N:N])`
// recovers the array pointer on Put without any allocation.
var (
	fastParams1Pool = sync.Pool{New: func() any { return new([1]Param) }}
	fastParams2Pool = sync.Pool{New: func() any { return new([2]Param) }}
	fastParams3Pool = sync.Pool{New: func() any { return new([3]Param) }}
)

// getFastParams returns a Params slice of exactly `n` elements, drawn from
// the tier-matched pool when n ∈ {1,2,3}. Routes with more than 3 params
// fall back to a fresh allocation — they are rare and already pay extra
// for the overflow slice anyway.
func getFastParams(n int) Params {
	switch n {
	case 1:
		a := fastParams1Pool.Get().(*[1]Param)
		return a[:1:1]
	case 2:
		a := fastParams2Pool.Get().(*[2]Param)
		return a[:2:2]
	case 3:
		a := fastParams3Pool.Get().(*[3]Param)
		return a[:3:3]
	default:
		return make(Params, n)
	}
}

// putFastParams clears the Params slice and returns it to the tier-matched
// pool. Slices outside the {1,2,3} tier are dropped to the GC. The slice
// is converted back to *[N]Param via slice-to-array-pointer conversion
// (Go 1.17+) so no heap traffic happens on the Put.
//
//go:nosplit
func putFastParams(ps Params) {
	// Zero the entries so the next caller never observes stale strings.
	for i := range ps {
		ps[i] = Param{}
	}
	switch len(ps) {
	case 1:
		fastParams1Pool.Put((*[1]Param)(ps))
	case 2:
		fastParams2Pool.Put((*[2]Param)(ps))
	case 3:
		fastParams3Pool.Put((*[3]Param)(ps))
	}
}

// dispatchParams1 allocates a tier-1 bundle and dispatches to h with one param.
//
// Opt O10: the function-pointer indirection that previously routed Fast/Safe
// has been eliminated — a direct call replaces the indirect CALL through a
// register, restoring branch prediction and inlining-budget reasoning at every
// call site. The internal `if hasReqCtxField` is a load from a process-lifetime
// constant — perfectly predicted after the first request and dead-code-removed
// on systems where reflect confirms the field exists at init time.
//
//go:nosplit
func dispatchParams1(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p Param) {
	if hasReqCtxField {
		// Fast path: fused requestCtx1 + http.Request in a single 384 B alloc
		// (Opt O12 — was 416 B before slimming requestCtx1).
		b := &reqBundle1{}
		b.ctx.Context = getReqCtxUnsafe(r) // skip r.Context() method call
		b.ctx.pattern = pattern
		b.ctx.small[0] = p
		b.req = *r
		setReqCtxUnsafe(&b.req, &b.ctx)
		h.ServeHTTP(w, &b.req)
		return
	}
	// Safe fallback for hypothetical future Go versions that rename `ctx`.
	rc := &requestCtx1{Context: r.Context(), pattern: pattern}
	rc.small[0] = p
	h.ServeHTTP(w, r.WithContext(rc))
}

// dispatchParams2 allocates a tier-2 bundle and dispatches to h with two params.
// See dispatchParams1 for the rationale of the merged Fast/Safe branch.
//
//go:nosplit
func dispatchParams2(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p0, p1 Param) {
	if hasReqCtxField {
		// Fused 400 B alloc — size class 416 B (Opt O12, was 448 B).
		b := &reqBundle2{}
		b.ctx.Context = getReqCtxUnsafe(r)
		b.ctx.pattern = pattern
		b.ctx.small[0] = p0
		b.ctx.small[1] = p1
		b.req = *r
		setReqCtxUnsafe(&b.req, &b.ctx)
		h.ServeHTTP(w, &b.req)
		return
	}
	rc := &requestCtx2{Context: r.Context(), pattern: pattern}
	rc.small[0] = p0
	rc.small[1] = p1
	h.ServeHTTP(w, r.WithContext(rc))
}

// dispatchParams1Pooled is the Opt O13 opt-in variant of dispatchParams1.
// It draws the reqBundle1 from sync.Pool and returns it after the handler
// completes, eliminating the per-request 384 B allocation. The strict
// lifetime contract is documented on Mux.PoolRequestBundle.
//
// hasReqCtxField is guaranteed true here (the pool path requires the unsafe
// shortcut for the zeroing to be cheap). Operators on platforms where the
// shortcut is unavailable fall back to dispatchParams1's Safe branch via
// the non-pooled path in dispatch().
//
//go:nosplit
func dispatchParams1Pooled(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p Param) {
	b := reqBundle1Pool.Get().(*reqBundle1)
	b.ctx.Context = getReqCtxUnsafe(r)
	b.ctx.pattern = pattern
	b.ctx.small[0] = p
	b.req = *r
	setReqCtxUnsafe(&b.req, &b.ctx)
	h.ServeHTTP(w, &b.req)
	// Zero before Put to prevent secret/reference leak into the next request.
	// `*b = reqBundle1{}` is the single fastest way to clear: it emits a
	// 368-byte aligned MOVUPS sequence (~5 ns) and clears the embedded
	// http.Request fields, including Body and headers.
	*b = reqBundle1{}
	reqBundle1Pool.Put(b)
}

// dispatchParams2Pooled is the Opt O13 opt-in variant of dispatchParams2.
//
//go:nosplit
func dispatchParams2Pooled(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p0, p1 Param) {
	b := reqBundle2Pool.Get().(*reqBundle2)
	b.ctx.Context = getReqCtxUnsafe(r)
	b.ctx.pattern = pattern
	b.ctx.small[0] = p0
	b.ctx.small[1] = p1
	b.req = *r
	setReqCtxUnsafe(&b.req, &b.ctx)
	h.ServeHTTP(w, &b.req)
	*b = reqBundle2{}
	reqBundle2Pool.Put(b)
}

// dispatchParamsNPooled is the Opt O13 opt-in variant of the 3+-param path
// inside dispatchWithParams. It mirrors the in-place logic from that
// function's `default:` branch but uses a pooled reqBundle.
//
//go:nosplit
func dispatchParamsNPooled(w http.ResponseWriter, r *http.Request, handler http.Handler, pattern string, pslice []Param) {
	bundle := reqBundlePool.Get().(*reqBundle)
	bundle.ctx.Context = getReqCtxUnsafe(r)
	bundle.ctx.pattern = pattern
	n := len(pslice)
	if n <= 3 {
		for i := range n {
			bundle.ctx.small[i] = pslice[i]
		}
		bundle.ctx.params = Params(bundle.ctx.small[:n])
	} else {
		// Overflow: allocate a fresh Params slice. The pooled bundle holds
		// only `params` (slice header) — the backing array is GC-managed.
		overflow := make(Params, n)
		copy(overflow, pslice)
		bundle.ctx.params = overflow
	}
	bundle.req = *r
	setReqCtxUnsafe(&bundle.req, &bundle.ctx)
	handler.ServeHTTP(w, &bundle.req)
	*bundle = reqBundle{}
	reqBundlePool.Put(bundle)
}

// dispatchWithParams dispatches to handler after params have been extracted.
// pslice is the filled portion of the paramsBuf, already unescape-decoded by
// the caller if UnescapePathValues is set. The caller is responsible for
// unescape so that this function stays independent of *Mux.
//
// Case 1 and 2 are delegated to dispatchParams1/dispatchParams2 (Opt O10:
// previously routed through `var doDispatch1/2 func(...)` pointers; the direct
// call eliminates the indirect CALL through a register and lets the compiler
// reason about each call site's inlining budget).
// Case 3+ uses reqBundle (480 B, size class 480 B) directly.
func dispatchWithParams(w http.ResponseWriter, r *http.Request, handler http.Handler, pattern string, pslice []Param) {
	switch len(pslice) {
	case 1:
		dispatchParams1(w, r, handler, pattern, pslice[0])
	case 2:
		dispatchParams2(w, r, handler, pattern, pslice[0], pslice[1])
	default:
		// 3+ params: use reqBundle (480 B, size class 480 B).
		n := len(pslice)
		if hasReqCtxField {
			bundle := &reqBundle{}
			bundle.ctx.Context = getReqCtxUnsafe(r) // Opt O5a
			bundle.ctx.pattern = pattern
			if n <= 3 {
				for i := range n {
					bundle.ctx.small[i] = pslice[i]
				}
				bundle.ctx.params = Params(bundle.ctx.small[:n])
			} else {
				overflow := make(Params, n)
				copy(overflow, pslice)
				bundle.ctx.params = overflow
			}
			bundle.req = *r
			setReqCtxUnsafe(&bundle.req, &bundle.ctx)
			handler.ServeHTTP(w, &bundle.req)
		} else {
			rc := &requestCtx{Context: r.Context(), pattern: pattern}
			if n <= 3 {
				for i := range n {
					rc.small[i] = pslice[i]
				}
				rc.params = Params(rc.small[:n])
			} else {
				overflow := make(Params, n)
				copy(overflow, pslice)
				rc.params = overflow
			}
			handler.ServeHTTP(w, r.WithContext(rc))
		}
	}
}

// --------------------------------------------------------------------------
// Public accessors — use type switches ordered by frequency (1-param is
// most common in REST APIs, then 3-param for deeper paths).
// --------------------------------------------------------------------------

// routeCtxFor extracts the routeCtx interface from ctx, or nil.
// The type switch is ordered so the most common cases (requestCtx1, requestCtx)
// are tested first (fast path — O(1) type assertion).
//
// Fallback (slow path): when a middleware registered via Use() wraps the request
// context with a new context (e.g. context.WithTimeout, context.WithValue), the
// outermost ctx is no longer a *requestCtx* type. In that case we call
// ctx.Value(contextKey{}) which traverses the context chain until it reaches
// the requestCtx layer, which returns itself. We then type-switch on that value.
// This ensures ParamsFromContext and PathParam work correctly even when the
// context has been wrapped by middleware (CSA-2026-0060).
func routeCtxParams(ctx context.Context) Params {
	switch rc := ctx.(type) {
	case *requestCtx1:
		// Opt O12: slice header derived from inline array — no field load.
		return Params(rc.small[:1])
	case *requestCtx:
		return rc.params
	case *requestCtx2:
		return Params(rc.small[:2])
	}
	// Slow path: context has been wrapped by middleware; traverse the chain.
	if v := ctx.Value(contextKey{}); v != nil {
		switch rc := v.(type) {
		case *requestCtx1:
			return Params(rc.small[:1])
		case *requestCtx:
			return rc.params
		case *requestCtx2:
			return Params(rc.small[:2])
		}
	}
	return nil
}

func routeCtxPattern(ctx context.Context) string {
	switch rc := ctx.(type) {
	case *requestCtx1:
		return rc.pattern
	case *requestCtx:
		return rc.pattern
	case *requestCtx2:
		return rc.pattern
	}
	// Slow path: context has been wrapped by middleware; traverse the chain.
	if v := ctx.Value(contextKey{}); v != nil {
		switch rc := v.(type) {
		case *requestCtx1:
			return rc.pattern
		case *requestCtx:
			return rc.pattern
		case *requestCtx2:
			return rc.pattern
		}
	}
	return ""
}

// PathParam returns the value of the named path parameter from the request.
func PathParam(r *http.Request, name string) string {
	return routeCtxParams(r.Context()).Get(name)
}

// ParamsFromContext returns the path parameters stored in ctx.
func ParamsFromContext(ctx context.Context) Params {
	return routeCtxParams(ctx)
}

// RoutePattern returns the registered route pattern that matched the request,
// or "" if none has been stored in the context.
func RoutePattern(r *http.Request) string {
	return routeCtxPattern(r.Context())
}
