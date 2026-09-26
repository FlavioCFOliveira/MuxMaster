// Sprint 20, rmp #286 — reclassification of S9 hypotheses TM-2026-033
// (cors + CDN + Vary cache-key smuggling) and TM-2026-046 (HEAD/GET
// divergence). Hypothesis text: reports/overview/2026-05-07-sprint-S9.md.
//
// Run with:
//
//	go test -race -count=1 -run 'TestHPS_TM_2026_0(33|46)' ./reports/http-protocol-security-auditor/harness/
package harness

import (
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// tm033Vary returns the Vary field lines of a raw HTTP/1.1 response and the
// set of tokens they carry (RFC 9110 §12.5.5: Vary is a list-based field).
func tm033Vary(raw string) (lines []string, tokens map[string]bool) {
	tokens = map[string]bool{}
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	for _, l := range strings.Split(head, "\r\n") {
		name, val, ok := strings.Cut(l, ":")
		if !ok || !strings.EqualFold(name, "Vary") {
			continue
		}
		lines = append(lines, l)
		for _, tok := range strings.Split(val, ",") {
			tokens[strings.ToLower(strings.TrimSpace(tok))] = true
		}
	}
	return lines, tokens
}

// TestHPS_TM_2026_033_CORSCompress_VaryOnTheWire pins the headers MuxMaster
// controls for a CORS response that a CDN would key on: for an allowed
// origin under an explicit allowlist, Vary carries Origin, and with Compress
// in either order also Accept-Encoding. Go serialises the two values as two
// separate "Vary:" field lines — valid (RFC 9110 §5.3), but a cache that
// reads only the first Vary line would drop one key. That parsing divergence
// is on the CDN side (deployment residual), not something MuxMaster emits
// incorrectly.
func TestHPS_TM_2026_033_CORSCompress_VaryOnTheWire(t *testing.T) {
	cors := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://app.example"},
		AllowedMethods: []string{"GET"},
	})
	compress := middleware.Compress(gzip.DefaultCompression)
	body := strings.Repeat("x", 4096)

	for _, order := range []string{"cors-then-compress", "compress-then-cors"} {
		m := muxmaster.New()
		if order == "cors-then-compress" {
			m.Use(cors, compress)
		} else {
			m.Use(compress, cors)
		}
		m.GET("/r", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(body))
		})
		srv := httptest.NewServer(m)
		raw := sendRawTCP(t, srv.Listener.Addr().String(),
			"GET /r HTTP/1.1\r\nHost: x\r\nOrigin: https://app.example\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n")
		srv.Close()

		if !strings.Contains(raw, "Access-Control-Allow-Origin: https://app.example\r\n") {
			t.Fatalf("%s: ACAO missing: %q", order, raw)
		}
		lines, tokens := tm033Vary(raw)
		if !tokens["origin"] || !tokens["accept-encoding"] {
			t.Fatalf("%s: Vary tokens %v, want origin and accept-encoding (lines %q)", order, tokens, lines)
		}
		if len(lines) != 2 {
			t.Fatalf("%s: %d Vary field lines, want 2 (one per middleware): %q", order, len(lines), lines)
		}
	}
}

// TestHPS_TM_2026_033_Regression_NonCORSResponseHasVaryOrigin is the
// regression test for the FIXED TM-2026-033 defect (formerly
// TestHPS_TM_2026_033_Reproducer_NonCORSResponseLacksVaryOrigin, which
// asserted the defect was present; converted per spec section 16 / rules
// 71-75, rmp #291).
//
// Fetch Standard, "CORS protocol and HTTP caches": when
// Access-Control-Allow-Origin is sent only in response to CORS requests,
// "Vary: Origin" must also be sent on responses to non-CORS requests;
// when ACAO is "*" it must be sent on non-CORS responses too. CORS() now
// calls addVaryOrigin unconditionally, before the early return for a
// request without an Origin header (cors.go), so a cache (browser or
// shared) that stores the non-CORS response also stores the fact that it
// varies by Origin, and will not reuse it for a later CORS request from a
// different origin.
func TestHPS_TM_2026_033_Regression_NonCORSResponseHasVaryOrigin(t *testing.T) {
	for _, origins := range [][]string{{"https://app.example"}, {"*"}} {
		h := middleware.CORS(middleware.CORSOptions{AllowedOrigins: origins})(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Cache-Control", "public, max-age=60")
				_, _ = w.Write([]byte("resource"))
			}))

		// 1) Non-CORS request (navigation, curl, cache warmer): stored by the cache.
		stored := httptest.NewRecorder()
		h.ServeHTTP(stored, httptest.NewRequest(http.MethodGet, "/r", nil))

		// The response the cache stores must itself declare that it varies
		// by Origin, so a cache that respects Vary (RFC 9111 §4.1) never
		// reuses it for a request bearing a different Origin.
		hasVaryOrigin := false
		for _, v := range stored.Header().Values("Vary") {
			if strings.Contains(strings.ToLower(v), "origin") {
				hasVaryOrigin = true
			}
		}
		if !hasVaryOrigin {
			t.Fatalf("origins=%v: non-CORS response lacks Vary: Origin — TM-2026-033 regression", origins)
		}
		// ACAO itself must remain unset for a request with no Origin
		// header (spec rule 72 — this fix changes only Vary).
		if stored.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origins=%v: non-CORS response unexpectedly carries ACAO %q — rule 72 violated",
				origins, stored.Header().Get("Access-Control-Allow-Origin"))
		}

		// 2) What the origin server WOULD have answered to a CORS request
		// for the same resource from an allowed origin.
		fresh := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/r", nil)
		req.Header.Set("Origin", "https://app.example")
		h.ServeHTTP(fresh, req)
		if fresh.Header().Get("Access-Control-Allow-Origin") == "" {
			t.Fatalf("origins=%v: fresh CORS response lacks ACAO — reproducer precondition failed", origins)
		}

		// 3) Because `stored` now carries Vary: Origin, a spec-compliant
		// cache keys it separately from the CORS request above (whose
		// Origin differs from the no-Origin request that produced
		// `stored`) and will not serve `stored` in its place.
	}
}

// TestHPS_TM_2026_046_HEADOnGETOnlyRoute asserts the facts that make the
// S9 premise ("MuxMaster reuses the GET handler for HEAD, as fasthttp does")
// false: there is no implicit HEAD — a GET-only route answers HEAD with
// 405 and an Allow list without HEAD, and the GET handler never runs. An
// explicitly registered HEAD handler (including the one ServeFiles
// registers, mux.go:1107) that writes a body puts no body bytes on the
// wire: net/http suppresses HEAD bodies.
func TestHPS_TM_2026_046_HEADOnGETOnlyRoute(t *testing.T) {
	getRan := false
	m := muxmaster.New()
	m.GET("/items", func(w http.ResponseWriter, _ *http.Request) {
		getRan = true
		_, _ = w.Write([]byte("GET-BODY"))
	})
	m.HEAD("/explicit", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("HEAD-HANDLER-BODY"))
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("FILE-CONTENT-0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.ServeFiles("/static/*filepath", http.Dir(dir))

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/items", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD on GET-only route: status %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || strings.Contains(allow, "HEAD") {
		t.Fatalf("Allow = %q, want GET listed and HEAD absent", allow)
	}
	if getRan {
		t.Fatal("GET handler ran for a HEAD request")
	}

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Control: the reader below does capture bodies (GET returns the file).
	if raw := tm046ReadToEOF(t, addr, "GET /static/f.txt HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); !strings.HasSuffix(raw, "\r\n\r\nFILE-CONTENT-0123456789") {
		t.Fatalf("control GET: body not captured: %q", raw)
	}
	for _, p := range []string{"/explicit", "/static/f.txt"} {
		raw := tm046ReadToEOF(t, addr, "HEAD "+p+" HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
		if !strings.HasPrefix(raw, "HTTP/1.1 200") {
			t.Fatalf("HEAD %s: %q", p, raw)
		}
		_, after, ok := strings.Cut(raw, "\r\n\r\n")
		if !ok || after != "" {
			t.Fatalf("HEAD %s: %d body byte(s) on the wire: %q", p, len(after), after)
		}
	}
}

// tm046ReadToEOF sends payload and returns every byte the server writes until
// it closes the connection (the request carries Connection: close). Unlike
// sendRawTCP it does not stop at the end of the header block, so body bytes
// following a HEAD response would be observed.
func tm046ReadToEOF(t *testing.T, addr, payload string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}
