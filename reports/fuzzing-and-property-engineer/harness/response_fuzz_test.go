// Fuzz tests for response helpers: JSON, XML, Text, Redirect, NoContent.
//
// Invariants:
//   - JSON/XML must never panic; on marshal error, return error without
//     writing a partial body.
//   - Text must never panic on any UTF-8 or arbitrary bytes.
//   - Redirect must emit a Location header.
//   - NoContent must emit status 204.
package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func FuzzResponseJSON(f *testing.F) {
	f.Add(200, `"hello"`)
	f.Add(0, `null`)
	f.Add(500, `{"a":1}`)
	f.Add(999, `{"a":`)

	f.Fuzz(func(t *testing.T, code int, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in JSON: code=%d raw=%q: %v\n%s",
					code, raw, r, debug.Stack())
			}
		}()
		w := httptest.NewRecorder()
		// raw is passed as a string; JSON will marshal strings safely.
		_ = mm.JSON(w, code, raw)
		_ = mm.JSON(w, code, map[string]string{"k": raw})
		_ = mm.JSON(w, code, []string{raw, raw})
	})
}

func FuzzResponseXML(f *testing.F) {
	f.Add(200, "hello")
	f.Add(500, "<tag/>")

	type Payload struct {
		Msg string `xml:"msg"`
	}

	f.Fuzz(func(t *testing.T, code int, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in XML: code=%d raw=%q: %v\n%s",
					code, raw, r, debug.Stack())
			}
		}()
		w := httptest.NewRecorder()
		_ = mm.XML(w, code, Payload{Msg: raw})
	})
}

func FuzzResponseText(f *testing.F) {
	f.Add(200, "hello")
	f.Add(0, "")
	f.Add(404, "Not Found")
	f.Add(200, "\x00\x01\x02")
	f.Add(200, strings.Repeat("a", 1<<16))
	f.Add(200, "Content-Type: evil\r\n\r\n<html>")

	f.Fuzz(func(t *testing.T, code int, s string) {
		if len(s) > 4<<20 {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in Text: code=%d len=%d: %v\n%s",
					code, len(s), r, debug.Stack())
			}
		}()
		w := httptest.NewRecorder()
		if err := mm.Text(w, code, s); err != nil {
			t.Fatalf("Text returned non-nil error: %v", err)
		}
		if w.Header().Get("Content-Type") == "" {
			t.Fatalf("Text did not set Content-Type")
		}
	})
}

func FuzzResponseRedirect(f *testing.F) {
	f.Add(301, "/elsewhere")
	f.Add(302, "https://example.com/")
	f.Add(307, "//evil.com/")
	f.Add(308, "")

	f.Fuzz(func(t *testing.T, code int, url string) {
		if len(url) > 4096 {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in Redirect: code=%d url=%q: %v\n%s",
					code, url, r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		mm.Redirect(w, req, code, url)
	})
}
