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

// TestCORSOriginReflectionAllowAll confirms H-005 happy path:
// when AllowedOrigins=["*"] and AllowCredentials=false, the Origin header
// is blindly echoed into ACAO, with no validation.
func TestCORSOriginReflectionAllowAll(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: false,
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"X-Client"},
		ExposedHeaders:   []string{"X-Response"},
		MaxAge:           86400,
	}))
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	cases := []struct {
		name   string
		origin string
	}{
		{"bare_evil", "https://evil.com"},
		{"scheme_http", "http://evil.com"},
		{"port_variant", "https://evil.com:8443"},
		{"empty_string", ""}, // middleware bails out if origin is empty
		{"null_literal", "null"},
		{"whitespace_only", " "},
		{"with_trailing_comma", "https://evil.com,"},
		{"crlf_in_origin", "https://evil.com\r\nSet-Cookie: evil=1"},
		{"ansi_in_origin", "https://evil.com\x1b[2J"},
		{"nul_in_origin", "https://evil.com\x00/bad"},
		{"absurdly_long", "https://" + strings.Repeat("a", 8192) + ".com"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h005-cors-reflection.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		func() {
			defer func() {
				if rcv := recover(); rcv != nil {
					fmt.Fprintf(f, "[%s] PANIC: %v\n", c.name, rcv)
				}
			}()
			mux.ServeHTTP(rec, req)
		}()

		acao := rec.Header().Get("Access-Control-Allow-Origin")
		acac := rec.Header().Get("Access-Control-Allow-Credentials")
		aceh := rec.Header().Get("Access-Control-Expose-Headers")

		fmt.Fprintf(f, "[%s] origin=%q status=%d acao=%q acac=%q expose=%q\n",
			c.name, c.origin, rec.Code, acao, acac, aceh)

		// NOTE: httptest.NewRecorder stores raw header bytes in-memory;
		// Go's HTTP serialiser on a real wire replaces \r\n with spaces
		// (see TestHeaderSerializationDefence). We log the in-memory state
		// for auditing but do not treat it as a wire-exploitable finding.
		if containsCtl(acao) {
			t.Logf("[%s] in-memory-only: CRLF/NUL present in ACAO: %q (wire response sanitises)",
				c.name, acao)
		}
	}
}

// TestCORSOriginReflectionAllowAllRawTCP drives the Mux through real TCP so
// Go's ResponseWriter validator (net/http.Header.Set) has a chance to reject
// malformed bytes. Confirms the *actual* bytes on the wire.
func TestCORSOriginReflectionAllowAllRawTCP(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	}))
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	payloads := []struct {
		name   string
		origin string
	}{
		{"literal_crlf", "https://evil.com\r\nSet-Cookie: evil=1"},
		{"literal_lf", "https://evil.com\nSet-Cookie: evil=1"},
		{"literal_cr", "https://evil.com\rSet-Cookie: evil=1"},
		{"nul_in_origin", "https://evil.com\x00X"},
		{"ansi_escape", "https://evil.com\x1b[2J"},
	}

	for _, p := range payloads {
		raw := fmt.Sprintf("GET /x HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: close\r\n\r\n",
			host, p.origin)
		resp, err := rawHTTPExchange(srv, raw)
		fname := filepath.Join("cors-" + p.name + ".txt")
		if err != nil {
			// Raw bytes in Origin header may have caused the stdlib parser
			// to fail. Record the request and error.
			ff, cerr := os.Create(filepath.Join(evidenceDir, fname))
			if cerr == nil {
				fmt.Fprintf(ff, "===== REQUEST =====\n%q\n===== ERROR =====\n%v\n",
					raw, err)
				ff.Close()
			}
			continue
		}
		recordTranscript(t, fname, raw, resp)
		if strings.Contains(string(resp), "Set-Cookie: evil") {
			t.Errorf("[%s] CRLF escaped into CORS response", p.name)
		}
	}
}

// TestCORSWildcardAllowCredentials confirms the panic-at-config behaviour.
func TestCORSWildcardAllowCredentials(t *testing.T) {
	defer func() {
		if rcv := recover(); rcv == nil {
			t.Errorf("expected panic when AllowCredentials=true + AllowedOrigins=['*']")
		}
	}()
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}

// TestCORSAllowCredentialsWithReflectedStar tests the specific sub-hypothesis
// from H-005: does the middleware panic only when '*' is LITERALLY in the
// list, or does it also detect dynamic wildcards?
func TestCORSAllowCredentialsWithExactOriginsEchoingAtk(t *testing.T) {
	// Reflect only a narrow allow-list, but include an origin that includes
	// the attacker's host as a substring match? MuxMaster's check is
	// allowedOrigins[origin] — map lookup, exact string.
	mux := muxmaster.New()
	mux.Use(middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://legit.com"},
		AllowCredentials: true,
	}))
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	cases := []struct {
		name   string
		origin string
		expect string // "" means ACAO should be absent or 403
	}{
		{"legit", "https://legit.com", "https://legit.com"},
		{"attacker", "https://evil.com", ""},
		{"null", "null", ""},
		{"empty", "", ""},
		{"substring_prefix", "https://legit.com.evil.com", ""},
		{"substring_suffix", "https://evilhttps://legit.com", ""},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		mux.ServeHTTP(rec, req)

		acao := rec.Header().Get("Access-Control-Allow-Origin")
		if acao != c.expect {
			t.Errorf("[%s] origin=%q: acao=%q, want %q (status=%d)",
				c.name, c.origin, acao, c.expect, rec.Code)
		}
	}
}
