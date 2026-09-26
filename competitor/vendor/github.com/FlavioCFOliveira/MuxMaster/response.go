package muxmaster

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
)

// [waste-hunt WH-05] Each Content-Type value is constant for its helper, so
// the STRING is computed once at package init instead of being rebuilt by
// Header().Set on every call.
//
// MID-RESPONSE-1 (sprint 18 waste-hunt, same class as MID-SETHEADER-1 in
// middleware/set_header.go): an earlier version of this optimisation hoisted
// the single-element []string HEADER VALUE itself (not just the string)
// into these package-level variables, so every request in the process
// shared the exact same slice for a given helper. http.Header.Set/Add/Del
// never mutate an existing slice in place, so ordinary header manipulation
// could not observe this; but any code that indexes directly into the slice
// (w.Header()["Content-Type"][0] = ...) mutated the shared backing array,
// corrupting the Content-Type for every other request through that helper,
// process-wide, until restart. The slice must be allocated fresh per
// request; only the string is safe to hoist. See
// specification/response-helpers.md §6, §12, §18.
var (
	jsonContentType = "application/json; charset=utf-8"
	xmlContentType  = "application/xml; charset=utf-8"
	textContentType = "text/plain; charset=utf-8"
)

// JSON marshals v to JSON and writes it with the given status code.
// Returns any marshalling or write error.
func JSON(w http.ResponseWriter, code int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if code == 0 {
		code = http.StatusOK
	}
	w.Header()["Content-Type"] = []string{jsonContentType}
	w.WriteHeader(code)
	_, err = w.Write(b)
	return err
}

// XML marshals v to XML and writes it with the given status code.
// Returns any marshalling or write error.
func XML(w http.ResponseWriter, code int, v any) error {
	b, err := xml.Marshal(v)
	if err != nil {
		return err
	}
	if code == 0 {
		code = http.StatusOK
	}
	w.Header()["Content-Type"] = []string{xmlContentType}
	w.WriteHeader(code)
	_, err = w.Write(b)
	return err
}

// Text writes s as plain text with the given status code.
func Text(w http.ResponseWriter, code int, s string) error {
	if code == 0 {
		code = http.StatusOK
	}
	w.Header()["Content-Type"] = []string{textContentType}
	w.WriteHeader(code)
	_, _ = io.WriteString(w, s)
	return nil
}

// Redirect sends an HTTP redirect to url with the given status code.
func Redirect(w http.ResponseWriter, r *http.Request, code int, url string) {
	http.Redirect(w, r, url, code)
}

// NoContent writes a 204 No Content response.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
