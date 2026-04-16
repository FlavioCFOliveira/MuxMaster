package muxmaster

import (
	"fmt"
	"net/http"
	"reflect"
	"runtime"
)

// RouteInfo describes a single registered route.
type RouteInfo struct {
	Method  string
	Pattern string
	Handler string
}

// Lookup performs a route lookup without dispatching a request.
// Returns (nil, nil, false) if path is empty or does not begin with '/'.
func (m *Mux) Lookup(method, path string) (http.Handler, Params, bool) {
	if path == "" || path[0] != '/' {
		return nil, nil, false
	}
	trees := m.treesPtr.Load()
	if trees == nil {
		return nil, nil, false
	}
	idx := methodIdx(method)
	if idx < 0 {
		return nil, nil, false
	}
	root := trees[idx]
	if root == nil {
		return nil, nil, false
	}
	var pb paramsBuf
	handler, _, _ := root.getValue(path, &pb, false)
	if handler == nil {
		return nil, nil, false
	}
	var params Params
	if pb.count > 0 {
		params = make(Params, pb.count)
		copy(params, pb.params())
	}
	return handler, params, true
}

// Routes returns a slice of RouteInfo for every registered route.
func (m *Mux) Routes() []RouteInfo {
	trees := m.treesPtr.Load()
	if trees == nil {
		return nil
	}
	var infos []RouteInfo
	for i, root := range trees {
		if root == nil {
			continue
		}
		method := methodNames[i]
		root.walk(func(pattern string, handler http.Handler) {
			infos = append(infos, RouteInfo{
				Method:  method,
				Pattern: pattern,
				Handler: handlerName(handler),
			})
		})
	}
	return infos
}

// Walk calls fn for each registered route.
// Stops iteration and returns the error if fn returns non-nil.
func (m *Mux) Walk(fn func(method, pattern string, handler http.Handler) error) error {
	trees := m.treesPtr.Load()
	if trees == nil {
		return nil
	}
	for i, root := range trees {
		if root == nil {
			continue
		}
		method := methodNames[i]
		var walkErr error
		root.walk(func(pattern string, handler http.Handler) {
			if walkErr != nil {
				return
			}
			walkErr = fn(method, pattern, handler)
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

// handlerName returns a human-readable name for the handler.
func handlerName(h http.Handler) string {
	v := reflect.ValueOf(h)
	if v.Kind() == reflect.Func {
		if f := runtime.FuncForPC(v.Pointer()); f != nil {
			return f.Name()
		}
	}
	return fmt.Sprintf("%T", h)
}
