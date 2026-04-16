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
