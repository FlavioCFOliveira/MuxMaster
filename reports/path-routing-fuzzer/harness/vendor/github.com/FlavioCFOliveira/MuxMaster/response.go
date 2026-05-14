package muxmaster

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
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
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(code)
	_, err = w.Write(b)
	return err
}

// Text writes s as plain text with the given status code.
func Text(w http.ResponseWriter, code int, s string) error {
	if code == 0 {
		code = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(s))
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
