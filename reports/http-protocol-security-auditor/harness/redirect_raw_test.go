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
)

// TestRedirectLocationRawBytes captures the raw Location header bytes for
// each redirect scenario via real TCP, so we can definitively say whether
// a browser would interpret the Location as same-origin or off-origin.
func TestRedirectLocationRawBytes(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/admin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.GET("/evil.com/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	cases := []struct {
		name   string
		target string
	}{
		{"admin_trailing_slash", "/admin/"},
		{"admin_trailing_slash_slash", "/admin//"},
		{"evil_double_slash", "//evil.com/foo"},
		{"evil_dot_dot", "/x/../evil.com/foo"},
		{"evil_triple_slash", "///evil.com/foo"},
		{"admin_percent_encoded_crlf", "/admin/%0D%0AX-Inject:%20y"},
		{"admin_percent_encoded_cr", "/admin/%0D"},
		{"admin_percent_encoded_lf", "/admin/%0A"},
		{"admin_nul_encoded", "/admin/%00"},
		{"admin_ansi_encoded", "/%1B%5B2Jadmin/"},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "redirect-raw-bytes.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		raw := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			c.target, host)
		resp, rerr := rawHTTPExchange(srv, raw)
		fmt.Fprintf(f, "[%s] target=%q\n  raw_err=%v\n  raw_response=%q\n\n",
			c.name, c.target, rerr, string(resp))

		// Pull out Location from the response headers, preserving literal bytes.
		lines := strings.Split(string(resp), "\r\n")
		loc := ""
		for _, line := range lines {
			if strings.HasPrefix(strings.ToLower(line), "location:") {
				loc = strings.TrimSpace(line[len("location:"):])
				break
			}
		}

		// A browser interprets Location starting with "//" or "\\" as an
		// authority-less URL, which maps to the current scheme + the
		// string after. That is an open redirect if the attacker controls
		// the string after //.
		if strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "\\\\") {
			t.Errorf("[%s] OPEN REDIRECT: Location %q", c.name, loc)
		}
		if strings.ContainsAny(loc, "\r\n") {
			t.Errorf("[%s] CRLF IN LOCATION: %q", c.name, loc)
		}
	}
}

// TestRedirectEncodedCRLFInjection drives the percent-decoded CRLF path
// into the redirect Location. If stdlib's http.Redirect does not sanitise,
// we can split the response.
func TestRedirectEncodedCRLFInjection(t *testing.T) {
	mux := muxmaster.New()
	// Register the "fixed" target so RedirectFixedPath will redirect to it
	// after path.Clean.
	mux.GET("/admin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	// Craft a request that, when percent-decoded and path-cleaned, produces
	// a literal CRLF in the path. Go's url.Parse decodes %0D %0A → raw CR/LF
	// in r.URL.Path, so m.dispatch's http.Redirect call sees a path
	// containing control bytes. Does http.Redirect sanitise?
	raw := fmt.Sprintf("GET /admin/%%0D%%0AX-Inject:%%20yes HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		host)
	resp, rerr := rawHTTPExchange(srv, raw)

	f, err := os.Create(filepath.Join(evidenceDir, "redirect-encoded-crlf.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "request_target=%q err=%v\nraw_response=%q\n",
		"/admin/%0D%0AX-Inject:%20yes", rerr, string(resp))

	// An X-Inject header in the response would mean splitting succeeded.
	if strings.Contains(string(resp), "X-Inject") {
		t.Errorf("CRLF injection succeeded — X-Inject header appeared in response")
	}
}

// TestHostHeaderInRedirect captures what http.Redirect does when r.URL.Host
// or r.Host is influenced by the attacker. Specifically: does the Location
// absolutize based on r.Host?
func TestHostHeaderInRedirect(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/admin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	cases := []struct {
		name string
		host string
	}{
		{"normal", host},
		{"evil_port", host + ".evil.com:80"},
		{"with_crlf", host + "\r\nX-Inject: y"},
		{"whitespace", "evil.com "},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "host-header-in-redirect.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		raw := fmt.Sprintf("GET /admin/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			c.host)
		resp, rerr := rawHTTPExchange(srv, raw)
		fmt.Fprintf(f, "[%s] host=%q err=%v\nraw=%q\n\n",
			c.name, c.host, rerr, string(resp))
	}
}
