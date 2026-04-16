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
	m.mu.RLock()
	root := m.trees[method]
	m.mu.RUnlock()
	if root == nil {
		return nil, nil, false
	}
	ps := acquireParams()
	handler, _, _ := root.getValue(path, ps, false)
	if handler == nil {
		releaseParams(ps)
		return nil, nil, false
	}
	var params Params
	if len(*ps) > 0 {
		params = make(Params, len(*ps))
		copy(params, *ps)
	}
	releaseParams(ps)
	return handler, params, true
}

// Routes returns a slice of RouteInfo for every registered route.
func (m *Mux) Routes() []RouteInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var infos []RouteInfo
	for method, root := range m.trees {
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	for method, root := range m.trees {
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
