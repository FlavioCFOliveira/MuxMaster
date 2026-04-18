package muxmaster

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
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
//   requestCtx1 + http.Request = 88 + 304 = 392 B → size class 416 B
//   requestCtx2 + http.Request = 120 + 304 = 424 B → size class 448 B
//   requestCtx  + http.Request = 152 + 304 = 456 B → size class 480 B
//
// Using the right tier for 1- and 2-param routes saves 64 B and 32 B per
// allocation respectively, reducing both malloc cost and GC scan pressure.
//
// All three types implement context.Context identically — they intercept
// the route-params context key and forward everything else to the parent.
// --------------------------------------------------------------------------

// requestCtx1 IS the context for routes with exactly 1 parameter.
type requestCtx1 struct {
	context.Context
	params  Params
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
// allocation (392 B, size class 416 B) for routes with exactly 1 parameter.
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
type requestCtx2 struct {
	context.Context
	params  Params
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
// allocation (424 B, size class 448 B) for routes with exactly 2 parameters.
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
func setReqCtxUnsafe(req *http.Request, ctx context.Context) {
	*(*context.Context)(unsafe.Add(unsafe.Pointer(req), reqCtxFieldOffset)) = ctx
}

// dispatchParams1 allocates a reqBundle1 (size class 416 B) and calls h.
// Tiered allocation: saves 64 B vs reqBundle for routes with exactly 1 param.
// Safe: bundle is freshly allocated, no other goroutine can access it before ServeHTTP.
func dispatchParams1(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p Param) {
	if hasReqCtxField {
		b := &reqBundle1{}
		b.ctx.Context = r.Context()
		b.ctx.pattern = pattern
		b.ctx.small[0] = p
		b.ctx.params = Params(b.ctx.small[:1])
		b.req = *r
		setReqCtxUnsafe(&b.req, &b.ctx)
		h.ServeHTTP(w, &b.req)
	} else {
		rc := &requestCtx1{Context: r.Context(), pattern: pattern}
		rc.small[0] = p
		rc.params = Params(rc.small[:1])
		h.ServeHTTP(w, r.WithContext(rc))
	}
}

// dispatchParams2 allocates a reqBundle2 (size class 448 B) and calls h.
// Tiered allocation: saves 32 B vs reqBundle for routes with exactly 2 params.
func dispatchParams2(w http.ResponseWriter, r *http.Request, h http.Handler, pattern string, p0, p1 Param) {
	if hasReqCtxField {
		b := &reqBundle2{}
		b.ctx.Context = r.Context()
		b.ctx.pattern = pattern
		b.ctx.small[0] = p0
		b.ctx.small[1] = p1
		b.ctx.params = Params(b.ctx.small[:2])
		b.req = *r
		setReqCtxUnsafe(&b.req, &b.ctx)
		h.ServeHTTP(w, &b.req)
	} else {
		rc := &requestCtx2{Context: r.Context(), pattern: pattern}
		rc.small[0] = p0
		rc.small[1] = p1
		rc.params = Params(rc.small[:2])
		h.ServeHTTP(w, r.WithContext(rc))
	}
}

// --------------------------------------------------------------------------
// Public accessors — use type switches ordered by frequency (1-param is
// most common in REST APIs, then 3-param for deeper paths).
// --------------------------------------------------------------------------

// routeCtxFor extracts the routeCtx interface from ctx, or nil.
// The type switch is ordered so the most common cases (requestCtx1, requestCtx)
// are tested first.
func routeCtxParams(ctx context.Context) Params {
	switch rc := ctx.(type) {
	case *requestCtx1:
		return rc.params
	case *requestCtx:
		return rc.params
	case *requestCtx2:
		return rc.params
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
