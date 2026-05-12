package muxmaster

import "net/http"

// HandlerFuncE is a handler that returns an error, allowing centralised error handling.
type HandlerFuncE func(http.ResponseWriter, *http.Request) error

// HTTPError is an error that carries an HTTP status code.
type HTTPError interface {
	error
	StatusCode() int
}

type httpError struct {
	code int
	err  error
}

func (e *httpError) Error() string   { return e.err.Error() }
func (e *httpError) StatusCode() int { return e.code }
func (e *httpError) Unwrap() error   { return e.err }

// Error constructs an HTTPError wrapping err with the given HTTP status code.
// Panics if err is nil.
func Error(code int, err error) HTTPError {
	if err == nil {
		panic("muxmaster: Error called with nil error")
	}
	return &httpError{code: code, err: err}
}

// FastHandler is a high-performance request handler that receives path
// parameters as a direct argument, bypassing the context allocation overhead
// of http.Handler routes.
//
// LIFETIME — default mode (Mux.PoolFastParams == false): the dispatcher
// allocates a fresh Params slice per request. The slice (and its backing
// array) remain valid even after the handler returns — goroutines spawned
// from the handler may safely capture and use ps.
//
// LIFETIME — pooled mode (Mux.PoolFastParams == true, Opt O9): the Params
// slice is drawn from a sync.Pool tier (1/2/3 params) and is RETURNED to
// the pool the instant the handler returns. Handlers in pooled mode MUST
// NOT retain ps (or any backing element) past return. Goroutines that
// capture ps would observe zeroed values at best, or values from an
// unrelated request at worst (indistinguishable from a use-after-free).
//
// If a handler in pooled mode must retain params past return, copy them
// first:
//
//	func myHandler(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
//	    ps2 := make(muxmaster.Params, len(ps))
//	    copy(ps2, ps)
//	    go func() { use(ps2) }() // safe — ps2 owns the data
//	}
//
// FastHandler routes do not support stdlib middleware
// (func(http.Handler) http.Handler). Use FastMiddleware instead, or register
// the route with Handle for full stdlib compatibility.
type FastHandler func(http.ResponseWriter, *http.Request, Params)

// FastMiddleware wraps a FastHandler, following the same composition model
// as stdlib middleware but for FastHandler routes only.
type FastMiddleware func(FastHandler) FastHandler

// wrapFastMiddleware wraps h with each FastMiddleware in order (index 0 is outermost).
func wrapFastMiddleware(h FastHandler, middleware []FastMiddleware) FastHandler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
