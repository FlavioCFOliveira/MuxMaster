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

// TestURLPathSurface maps what bytes the stdlib actually admits into
// r.URL.Path when a real TCP client sends them, either raw or
// percent-encoded. This delimits the realistic attack surface for
// downstream consumers (logger, Location header construction, etc.).
func TestURLPathSurface(t *testing.T) {
	captured := make(chan string, 100)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- fmt.Sprintf("path=%q raw_path=%q unescape_err=%v",
			r.URL.Path, r.URL.RawPath, nil)
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	cases := []struct {
		name   string
		target string
	}{
		{"literal_cr", "/abc\r"},
		{"literal_lf", "/abc\n"},
		{"literal_crlf", "/abc\r\n"},
		{"literal_nul", "/abc\x00"},
		{"literal_ansi", "/\x1b[2J"},
		{"literal_tab", "/abc\t"},
		{"literal_del", "/abc\x7f"},
		{"literal_space", "/abc "},
		{"literal_utf8", "/abc\xe4\xbd\xa0"}, // 你
		{"literal_backslash", "/abc\\xyz"},
		{"encoded_cr", "/abc%0D"},
		{"encoded_lf", "/abc%0A"},
		{"encoded_crlf", "/abc%0D%0A"},
		{"encoded_nul", "/abc%00"},
		{"encoded_ansi", "/%1B%5B2J"},
		{"encoded_del", "/abc%7F"},
		{"encoded_traversal", "/static/..%2f..%2fsecret"},
		{"double_encoded", "/abc%2525"},
		{"overlong", "/abc%C0%AF"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "url-path-surface.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		raw := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			c.target, host)
		resp, rerr := rawHTTPExchange(srv, raw)
		var observed string
		select {
		case observed = <-captured:
		default:
			observed = "<not-captured>"
		}
		// Extract status line from response
		statusLine := ""
		if idx := strings.Index(string(resp), "\r\n"); idx >= 0 {
			statusLine = string(resp)[:idx]
		}
		fmt.Fprintf(f, "[%s] target=%q rerr=%v status=%q observed=%s\n",
			c.name, c.target, rerr, statusLine, observed)
	}
}
