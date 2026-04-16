package muxmaster

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
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

// routeCtx stores the matched params and registered route pattern together
// in a single context write per request, avoiding two separate allocations.
type routeCtx struct {
	params  Params
	pattern string
}

type contextKey struct{}

const maxParams = 16

var paramsPool = sync.Pool{
	New: func() any {
		ps := make(Params, 0, maxParams)
		return &ps
	},
}

func acquireParams() *Params {
	return paramsPool.Get().(*Params)
}

func releaseParams(ps *Params) {
	*ps = (*ps)[:0]
	paramsPool.Put(ps)
}

// PathParam returns the value of the named path parameter from the request.
func PathParam(r *http.Request, name string) string {
	return ParamsFromContext(r.Context()).Get(name)
}

// ParamsFromContext returns the path parameters stored in ctx.
func ParamsFromContext(ctx context.Context) Params {
	rc, _ := ctx.Value(contextKey{}).(*routeCtx)
	if rc == nil {
		return nil
	}
	return rc.params
}

// RoutePattern returns the registered route pattern that matched the request,
// or "" if none has been stored in the context.
func RoutePattern(r *http.Request) string {
	rc, _ := r.Context().Value(contextKey{}).(*routeCtx)
	if rc == nil {
		return ""
	}
	return rc.pattern
}

// withRoute stores the matched params and route pattern in the request context.
func withRoute(r *http.Request, ps Params, pattern string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, &routeCtx{
		params:  ps,
		pattern: pattern,
	}))
}
