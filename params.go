package muxmaster

import (
	"context"
	"net/http"
	"sync"
)

// Param is a single URL path parameter consisting of a key and value.
type Param struct {
	Key   string
	Value string
}

// Params is an ordered slice of path parameters extracted from the URL.
type Params []Param

// Get returns the value of the first Param whose key matches name.
// Returns an empty string if the key is not found.
func (ps Params) Get(name string) string {
	for i := range ps {
		if ps[i].Key == name {
			return ps[i].Value
		}
	}
	return ""
}

type contextKey struct{}

// maxParams is the pre-allocated capacity for path parameter slices.
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

// PathParam returns the value of the named URL path parameter from r.
func PathParam(r *http.Request, name string) string {
	ps, _ := r.Context().Value(contextKey{}).(Params)
	return ps.Get(name)
}

// ParamsFromContext returns the Params stored in ctx, if any.
func ParamsFromContext(ctx context.Context) Params {
	ps, _ := ctx.Value(contextKey{}).(Params)
	return ps
}

func withParams(r *http.Request, ps Params) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, ps))
}
