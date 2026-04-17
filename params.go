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

// requestCtx IS the context — it embeds the parent and adds route data inline.
// By implementing context.Context directly, we bypass context.WithValue entirely,
// eliminating the valueCtx heap allocation that WithValue would cause.
type requestCtx struct {
	context.Context
	params  Params
	pattern string
	// small holds param data for routes with ≤3 params without a separate heap alloc.
	// params points into small[:n] in the common case, so the slice header and
	// the backing array share a single allocation (the requestCtx itself).
	small [3]Param
}

// Value intercepts the route-params key and falls through to the parent for everything else.
func (c *requestCtx) Value(key any) any {
	if _, ok := key.(contextKey); ok {
		return c
	}
	return c.Context.Value(key)
}

type contextKey struct{}

const maxParams = 16

// rcPool reuses requestCtx objects — avoids one heap alloc per param request.
// sync.Pool's per-P design wins under real parallel load: attempts at user-space
// atomic rings (swap / CAS on global slots) regressed parallel param routes by
// ~20% due to cache line bouncing across cores, even with per-slot padding.
var rcPool = sync.Pool{New: func() any { return new(requestCtx) }}

// acquireRC / releaseRC: indirection layer — ready to swap in an alternative
// pool strategy if a future design beats sync.Pool in both serial and parallel.
func acquireRC() *requestCtx  { return rcPool.Get().(*requestCtx) }
func releaseRC(rc *requestCtx) { rcPool.Put(rc) }

// reqCtxOffset is the byte offset of the unexported 'ctx context.Context' field
// inside http.Request. Determined at init via reflect — safe under -race and checkptr.
var reqCtxOffset uintptr

func init() {
	t := reflect.TypeOf(http.Request{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name == "ctx" {
			reqCtxOffset = f.Offset
			break
		}
	}
}

// setReqCtx writes ctx into r's unexported ctx field.
// Safe when r is exclusively owned by the current goroutine (pool get → put pattern).
// Uses unsafe.Add which is checkptr-safe: the base pointer is a valid allocation and
// reqCtxOffset is within that allocation's bounds.
func setReqCtx(r *http.Request, ctx context.Context) {
	*(*context.Context)(unsafe.Add(unsafe.Pointer(r), reqCtxOffset)) = ctx
}

// PathParam returns the value of the named path parameter from the request.
func PathParam(r *http.Request, name string) string {
	rc, _ := r.Context().(*requestCtx)
	if rc == nil {
		return ""
	}
	return rc.params.Get(name)
}

// ParamsFromContext returns the path parameters stored in ctx.
func ParamsFromContext(ctx context.Context) Params {
	rc, _ := ctx.(*requestCtx)
	if rc == nil {
		return nil
	}
	return rc.params
}

// RoutePattern returns the registered route pattern that matched the request,
// or "" if none has been stored in the context.
func RoutePattern(r *http.Request) string {
	rc, _ := r.Context().(*requestCtx)
	if rc == nil {
		return ""
	}
	return rc.pattern
}

