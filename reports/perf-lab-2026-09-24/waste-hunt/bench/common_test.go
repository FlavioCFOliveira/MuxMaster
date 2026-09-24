// Package bench holds the micro-benchmarks of the sprint-18 waste hunt
// (rmp #249). Every candidate finding is measured as:
//
//	.../current     — the REAL MuxMaster code, imported unmodified;
//	.../alternative — a harness-local rewrite of only the wasteful part.
//
// Alternatives exist solely to quantify the achievable gain; they are not
// product code. Each alternative is paired with an equivalence test
// (equiv_test.go) proving it produces the same observable result.
package bench

import (
	"net/http"
	"net/http/httptest"
)

// discardRW is a minimal http.ResponseWriter with a reusable header map, so
// benchmarks measure the middleware, not httptest.ResponseRecorder.
type discardRW struct {
	h      http.Header
	status int
	n      int
}

func newDiscardRW() *discardRW { return &discardRW{h: make(http.Header, 16)} }

func (d *discardRW) Header() http.Header { return d.h }
func (d *discardRW) WriteHeader(code int) {
	d.status = code
}
func (d *discardRW) Write(b []byte) (int, error) { d.n += len(b); return len(b), nil }
func (d *discardRW) WriteString(s string) (int, error) {
	d.n += len(s)
	return len(s), nil
}

// reset clears per-request state; its cost is identical for every variant.
func (d *discardRW) reset() {
	clear(d.h)
	d.status = 0
	d.n = 0
}

var nop = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

// realisticRequest mimics a browser/API client request as it reaches the
// server after net/http parsing (canonical header keys, 9 headers).
func realisticRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	r.Header.Set("Accept-Language", "en-GB,en;q=0.9,pt;q=0.8")
	r.Header.Set("Cache-Control", "no-cache")
	r.Header.Set("Connection", "keep-alive")
	r.Header.Set("Cookie", "session=0123456789abcdef0123456789abcdef; theme=dark")
	r.Header.Set("Referer", "https://example.com/index.html")
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	return r
}
