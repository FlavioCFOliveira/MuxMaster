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
// Params is valid only for the lifetime of the handler call. If a goroutine
// is spawned that outlives the handler, copy the Params slice before the
// handler returns:
//
//	func myHandler(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
//	    ps2 := make(muxmaster.Params, len(ps))
//	    copy(ps2, ps)
//	    go func() { use(ps2) }()
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
