package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHeaderSerializationDefence confirms that Go's net/http ResponseWriter
// serialisation layer sanitises CR/LF in header values before they reach
// the wire. This determines whether any of our middleware-echo paths can
// actually achieve response splitting on a real HTTP/1.1 connection.
//
// We build a minimal handler that calls w.Header().Set("X-Test", value)
// with varied byte sequences and inspect the raw response.
func TestHeaderSerializationDefence(t *testing.T) {
	values := []struct {
		name  string
		value string
	}{
		{"cr_lf", "a\r\nb"},
		{"lf_only", "a\nb"},
		{"cr_only", "a\rb"},
		{"nul", "a\x00b"},
		{"ansi", "a\x1b[31mb"},
		{"tab", "a\tb"},
		{"del", "a\x7fb"},
		{"utf8", "a\xe4\xbd\xa0b"}, // UTF-8 "你" in header value
		{"space", "a b"},
		{"valid_alnum", "abc123"},
		{"empty", ""},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "header-serialisation.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, v := range values {
		v := v
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test", v.value)
			w.WriteHeader(204)
		})
		srv := httptest.NewServer(h)
		raw := "GET / HTTP/1.1\r\nHost: " + stripSchemeHost(srv.URL) + "\r\nConnection: close\r\n\r\n"
		resp, rerr := rawHTTPExchange(srv, raw)
		srv.Close()
		rstr := string(resp)

		// Count \r\n sequences in the header block
		headerEnd := strings.Index(rstr, "\r\n\r\n")
		headerBlock := rstr
		if headerEnd > 0 {
			headerBlock = rstr[:headerEnd]
		}
		lineCount := strings.Count(headerBlock, "\r\n") + 1

		fmt.Fprintf(f, "[%s] input_value=%q err=%v status_line=%q line_count=%d has_x_test=%v raw_response=%q\n",
			v.name, v.value, rerr,
			firstLine(rstr),
			lineCount,
			strings.Contains(rstr, "X-Test:"),
			rstr,
		)

		// If the raw response contains an unexpected header like "Set-Cookie"
		// from the value, that's a splitting success.
		if strings.Contains(strings.ToLower(rstr), "set-cookie") {
			t.Errorf("[%s] splitting achieved — Set-Cookie appeared in raw response", v.name)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
