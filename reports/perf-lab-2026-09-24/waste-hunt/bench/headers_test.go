package bench

import (
	"io"
	"net/http"
	"net/textproto"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: SetHeader re-canonicalises the key and allocates a []string
// on every request (w.Header().Set). The key and value are fixed at
// construction time, so both can be computed once — exactly the pattern
// NoCache and CORS already use (Opt L3 in cors.go).

func setHeaderAlt(key, value string) func(http.Handler) http.Handler {
	ck := textproto.CanonicalMIMEHeaderKey(key)
	vs := []string{value}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header()[ck] = vs
			next.ServeHTTP(w, r)
		})
	}
}

// The static-site example stacks four SetHeader middlewares on every route
// plus one more on page routes (and two on each Mount sub-mux): 4 is the
// minimum per request there.
func staticSiteHeaders(set func(k, v string) func(http.Handler) http.Handler) http.Handler {
	h := http.Handler(nop)
	for _, kv := range [][2]string{
		{"Permissions-Policy", "camera=(), microphone=(), geolocation=()"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
		{"X-Frame-Options", "SAMEORIGIN"},
		{"X-Content-Type-Options", "nosniff"},
	} {
		h = set(kv[0], kv[1])(h)
	}
	return h
}

func BenchmarkSetHeaderX4(b *testing.B) {
	run := func(b *testing.B, h http.Handler) {
		r := realisticRequest(http.MethodGet, "/assets/style.css")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			h.ServeHTTP(w, r)
			w.reset()
		}
	}
	b.Run("current", func(b *testing.B) { run(b, staticSiteHeaders(mw.SetHeader)) })
	b.Run("alternative", func(b *testing.B) { run(b, staticSiteHeaders(setHeaderAlt)) })
}

// ── Candidate: response helpers (Text/JSON/XML) call
// w.Header().Set("Content-Type", <constant>) — one []string allocation per
// response for a value that never changes; Text also converts its string
// argument with []byte(s) (a copy + allocation) instead of io.WriteString.

var textContentType = []string{"text/plain; charset=utf-8"}

func textAlt(w http.ResponseWriter, code int, s string) error {
	if code == 0 {
		code = http.StatusOK
	}
	w.Header()["Content-Type"] = textContentType
	w.WriteHeader(code)
	_, _ = io.WriteString(w, s)
	return nil
}

func BenchmarkText(b *testing.B) {
	body := "pong from GET /api/v1/books/ping — a typical short plain-text response body"
	b.Run("current", func(b *testing.B) {
		w := newDiscardRW()
		b.ReportAllocs()
		for range b.N {
			_ = mm.Text(w, 200, body)
			w.reset()
		}
	})
	b.Run("alternative", func(b *testing.B) {
		w := newDiscardRW()
		b.ReportAllocs()
		for range b.N {
			_ = textAlt(w, 200, body)
			w.reset()
		}
	})
}

type jsonBook struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Year   int    `json:"year"`
	Genre  string `json:"genre"`
}

// BenchmarkJSONHelper isolates the fixed header cost inside mm.JSON (the
// marshal itself is the application's payload cost, not waste).
func BenchmarkJSONHelper(b *testing.B) {
	v := jsonBook{1, "The Go Programming Language", "Donovan", 2015, "tech"}
	w := newDiscardRW()
	b.ReportAllocs()
	for range b.N {
		_ = mm.JSON(w, 200, v)
		w.reset()
	}
}

type xmlBook struct {
	ID     int    `xml:"id"`
	Title  string `xml:"title"`
	Author string `xml:"author"`
	Year   int    `xml:"year"`
	Genre  string `xml:"genre"`
}

// BenchmarkXMLHelper mirrors BenchmarkJSONHelper for mm.XML — added
// [waste-hunt task #250, MID-RESPONSE-1 aliasing fix] alongside the existing
// Text/JSONHelper benchmarks so the Content-Type header-slice fix (response.go)
// is measured for all three response helpers, not just two.
func BenchmarkXMLHelper(b *testing.B) {
	v := xmlBook{1, "The Go Programming Language", "Donovan", 2015, "tech"}
	w := newDiscardRW()
	b.ReportAllocs()
	for range b.N {
		_ = mm.XML(w, 200, v)
		w.reset()
	}
}

func BenchmarkHeaderSetConst(b *testing.B) {
	b.Run("Header.Set", func(b *testing.B) {
		h := make(http.Header, 4)
		b.ReportAllocs()
		for range b.N {
			h.Set("Content-Type", "application/json; charset=utf-8")
		}
	})
	b.Run("prealloc-assign", func(b *testing.B) {
		h := make(http.Header, 4)
		v := []string{"application/json; charset=utf-8"}
		b.ReportAllocs()
		for range b.N {
			h["Content-Type"] = v
		}
	})
}
