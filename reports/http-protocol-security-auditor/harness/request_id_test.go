package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestRequestIDCRLFReflection exercises H-004: client-supplied
// X-Request-ID is echoed into the response via w.Header().Set.
// We confirm whether Go's stdlib header validator rejects CRLF /
// NUL / bad bytes, and how the middleware behaves when it does.
func TestRequestIDCRLFReflection(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.RequestID())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	cases := []struct {
		name string
		id   string
	}{
		{"clean_alnum", "abc123"},
		{"crlf_in_id", "abc\r\nSet-Cookie: evil=1"},
		{"lf_only_in_id", "abc\nSet-Cookie: evil=1"},
		{"cr_only_in_id", "abc\rSet-Cookie: evil=1"},
		{"nul_in_id", "abc\x00Set-Cookie: evil=1"},
		{"tab_in_id", "abc\tdef"},
		{"space_in_id", "abc def"},
		{"ansi_in_id", "abc\x1b[2Jdef"},
		{"one_mb_id", strings.Repeat("A", 1<<20)},
		{"del_byte", "abc\x7fdef"},
		{"high_utf8", "abc你好def"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h004-request-id-reflection.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("X-Request-ID", c.id)

		// Invoke handler. Stdlib may panic on invalid headers — recover.
		func() {
			defer func() {
				if rcv := recover(); rcv != nil {
					fmt.Fprintf(f, "[%s] PANIC in handler: %v\n", c.name, rcv)
				}
			}()
			mux.ServeHTTP(rec, req)
		}()

		got := rec.Header().Get("X-Request-ID")
		setCookie := rec.Header().Get("Set-Cookie")

		fmt.Fprintf(f, "[%s] input_id_len=%d input_has_ctl=%t reply_id=%q set_cookie=%q\n",
			c.name, len(c.id), containsCtl(c.id), got, setCookie)

		// Assertion: Set-Cookie must NEVER appear (no splitting ever).
		if setCookie != "" {
			t.Errorf("[%s] SET-COOKIE leaked into response via CRLF: %q", c.name, setCookie)
		}
	}
}

// TestRequestIDCRLFRealTCP goes deeper: it runs an actual httptest.Server
// and confirms the raw response bytes. If Go's Header.Set validates inputs
// it will silently drop the bad value; we want to know what ACTUALLY goes
// on the wire.
func TestRequestIDCRLFRealTCP(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.RequestID())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Craft a raw TCP request.
	payloads := []struct {
		name string
		id   string
	}{
		{"literal_crlf", "abc\r\nSet-Cookie: evil=1"},
		{"encoded_in_value", "abc%0D%0ASet-Cookie:%20evil=1"},
		{"leading_space", " abc"},
		{"long", strings.Repeat("A", 1<<14)},
	}

	out, err := os.Create(filepath.Join(evidenceDir, "h004-request-id-raw-tcp.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer out.Close()

	for _, p := range payloads {
		raw := fmt.Sprintf("GET /x HTTP/1.1\r\nHost: %s\r\nX-Request-ID: %s\r\nConnection: close\r\n\r\n",
			stripSchemeHost(srv.URL), p.id)
		respBytes, err := rawHTTPExchange(srv, raw)
		if err != nil {
			fmt.Fprintf(out, "[%s] DIAL/IO err=%v\n", p.name, err)
			continue
		}
		fmt.Fprintf(out, "[%s] ----- RAW RESPONSE -----\n%q\n----- END -----\n",
			p.name, respBytes)
		// Presence of a REAL Set-Cookie line (after \r\n boundary) = splitting.
		// The percent-encoded `%20` substring "Set-Cookie:" is only a string
		// within the reflected X-Request-Id value and is not a real header.
		for _, line := range strings.Split(string(respBytes), "\r\n") {
			if strings.HasPrefix(line, "Set-Cookie:") {
				t.Errorf("[%s] CONFIRMED response splitting — Set-Cookie header: %q",
					p.name, line)
			}
		}
	}
}

func stripSchemeHost(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
}
