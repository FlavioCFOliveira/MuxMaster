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
// For FastHandler routes the returned http.Handler is nil; use LookupFast
// when you need to distinguish fast routes.
func (m *Mux) Lookup(method, path string) (http.Handler, Params, bool) {
	if path == "" || path[0] != '/' {
		return nil, nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	handler, fast, _, _ := root.getValue(path, &pb, false)
	if handler == nil && fast == nil {
		return nil, nil, false
	}
	var params Params
	if pb.count > 0 {
		params = make(Params, pb.count)
		copy(params, pb.params())
	}
	return handler, params, true
}

// Routes returns a slice of RouteInfo for every registered route,
// including both http.Handler and FastHandler routes.
func (m *Mux) Routes() []RouteInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
		root.walk(func(pattern string, handler http.Handler, fast FastHandler) {
			name := ""
			if handler != nil {
				name = handlerName(handler)
			} else if fast != nil {
				name = fastHandlerName(fast)
			}
			infos = append(infos, RouteInfo{
				Method:  method,
				Pattern: pattern,
				Handler: name,
			})
		})
	}
	return infos
}

// Walk calls fn for each registered http.Handler route.
// FastHandler routes are skipped — use WalkFast to visit them.
// Stops iteration and returns the error if fn returns non-nil.
func (m *Mux) Walk(fn func(method, pattern string, handler http.Handler) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
		root.walk(func(pattern string, handler http.Handler, _ FastHandler) {
			if walkErr != nil || handler == nil {
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

// WalkFast calls fn for each registered FastHandler route.
// http.Handler routes are skipped — use Walk to visit them.
// Stops iteration and returns the error if fn returns non-nil.
func (m *Mux) WalkFast(fn func(method, pattern string, handler FastHandler) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
		root.walk(func(pattern string, _ http.Handler, fast FastHandler) {
			if walkErr != nil || fast == nil {
				return
			}
			walkErr = fn(method, pattern, fast)
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

// handlerName returns a human-readable name for an http.Handler.
func handlerName(h http.Handler) string {
	v := reflect.ValueOf(h)
	if v.Kind() == reflect.Func {
		if f := runtime.FuncForPC(v.Pointer()); f != nil {
			return f.Name()
		}
	}
	return fmt.Sprintf("%T", h)
}

// fastHandlerName returns a human-readable name for a FastHandler.
func fastHandlerName(h FastHandler) string {
	if f := runtime.FuncForPC(reflect.ValueOf(h).Pointer()); f != nil {
		return f.Name()
	}
	return fmt.Sprintf("%T", h)
}
