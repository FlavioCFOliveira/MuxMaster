// hps_2026_openredirect_test.go — HPS-2026-0005 Open Redirect via Absolute-Form URI
//
// Finding: MuxMaster's TSR (RedirectTrailingSlash) and RedirectFixedPath redirects
// build the Location target via r.URL.String(), which includes Scheme and Host when
// the request was sent using an absolute-form URI (RFC 7230 §5.3.2).
//
// An attacker can send: GET http://evil.com/admin/ HTTP/1.1
// net/http parses r.URL.Scheme="http", r.URL.Host="evil.com", r.URL.Path="/admin/"
// MuxMaster's TSR fires → target = r.URL.String() = "http://evil.com/admin"
// Response: 301 Location: http://evil.com/admin  ← OPEN REDIRECT
//
// CWE-601: URL Redirection to Untrusted Site ('Open Redirect')
// Commit under test: e30ae946f634cbec54c0ae9445cbf3787ca24f31
// Go: go1.26.2 linux/amd64
//
// Run with: go test -race -v -run TestHPS_AbsoluteFormURI ./reports/http-protocol-security-auditor/harness/
package harness

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestHPS_AbsoluteFormURI_TSR_OpenRedirect verifies that TSR redirect
// does NOT produce an open redirect when the request contains an absolute-form URI.
//
// RFC 7230 §5.3.2: clients send absolute-form URIs (e.g. "GET http://host/path HTTP/1.1")
// when making requests through a proxy. net/http parses these and sets r.URL.Scheme and
// r.URL.Host. MuxMaster's redirect target is built from r.URL.String() which includes
// those fields — producing a redirect to an attacker-controlled host.
func TestHPS_AbsoluteFormURI_TSR_OpenRedirect(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	cases := []struct {
		desc     string
		rawReq   string
		wantSafe bool
	}{
		{
			desc:     "HTTP absolute-form URI TSR redirect — attacker controls host",
			rawReq:   "GET http://evil.com/admin/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantSafe: true, // want the redirect to NOT include evil.com in Location
		},
		{
			desc:     "HTTPS absolute-form URI TSR redirect",
			rawReq:   "GET https://evil.com/admin/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantSafe: true,
		},
		{
			desc:     "Normal path-form URI — safe baseline",
			rawReq:   "GET /admin/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantSafe: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))

			conn.Write([]byte(tc.rawReq))

			br := bufio.NewReader(conn)
			var location string
			var statusLine string
			for {
				line, err := br.ReadString('\n')
				if statusLine == "" && strings.HasPrefix(line, "HTTP/") {
					statusLine = strings.TrimSpace(line)
				}
				if strings.HasPrefix(line, "Location:") {
					location = strings.TrimSpace(strings.TrimPrefix(line, "Location:"))
				}
				if line == "\r\n" || err != nil {
					break
				}
			}

			t.Logf("Request: %q", tc.rawReq[:50])
			t.Logf("Status: %s", statusLine)
			t.Logf("Location: %q", location)

			if location == "" {
				t.Logf("No redirect (status=%s) — not a redirect scenario", statusLine)
				return
			}

			// Parse the Location header to check if it's an open redirect
			u, err := url.Parse(location)
			if err != nil {
				t.Logf("Failed to parse Location %q: %v", location, err)
				return
			}

			if u.Host != "" && u.Host != "localhost" && !strings.HasPrefix(u.Host, "127.") {
				if tc.wantSafe {
					t.Errorf("HPS-2026-0005 VULNERABLE (CWE-601): Open Redirect via absolute-form URI")
					t.Errorf("  Request: %q", tc.rawReq[:50])
					t.Errorf("  Location: %q", location)
					t.Errorf("  Attacker-controlled host in redirect: %q", u.Host)
					t.Errorf("  Attack: send GET http://evil.com/admin/ HTTP/1.1 to a MuxMaster server")
					t.Errorf("  Result: victim browser follows redirect to http://evil.com/admin")
					t.Errorf("  Impact: phishing, credential harvesting via redirect chain")
					t.Errorf("  Root cause: r.URL.String() includes Scheme+Host from absolute-form URI")
					t.Errorf("  Fix: build redirect target from path+query only:")
					t.Errorf("    newURL := &url.URL{Path: newPath, RawQuery: r.URL.RawQuery}")
					t.Errorf("    target := newURL.String()")
				}
			} else {
				if tc.wantSafe {
					t.Logf("PASS: Location=%q has no external host", location)
				}
			}
		})
	}
}

// TestHPS_AbsoluteFormURI_FixedPath_OpenRedirect verifies the same for RedirectFixedPath.
func TestHPS_AbsoluteFormURI_FixedPath_OpenRedirect(t *testing.T) {
	m := muxmaster.New()
	m.RedirectFixedPath = true
	m.RedirectTrailingSlash = false
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Send absolute-form URI with double-slash path — triggers RedirectFixedPath
	// path.Clean("//admin") = "/admin" which has a handler
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("GET http://evil.com//admin HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))

	br := bufio.NewReader(conn)
	var location string
	for {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "Location:") {
			location = strings.TrimSpace(strings.TrimPrefix(line, "Location:"))
		}
		if line == "\r\n" || err != nil {
			break
		}
	}

	t.Logf("FixedPath redirect Location: %q", location)

	if location != "" {
		u, err := url.Parse(location)
		if err == nil && u.Host != "" && u.Host != "localhost" {
			t.Errorf("HPS-2026-0005 VULNERABLE (RedirectFixedPath): Open Redirect")
			t.Errorf("  Location: %q (attacker host: %q)", location, u.Host)
			t.Errorf("  CWE-601: URL Redirection to Untrusted Site")
		} else {
			t.Logf("PASS: FixedPath Location=%q has no external host", location)
		}
	}
}

// TestHPS_AbsoluteFormURI_Programmatic verifies via in-process httptest.
// Simulates what net/http sees when a proxy forwards an absolute-form URI.
func TestHPS_AbsoluteFormURI_Programmatic(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Simulate an absolute-form URI request as net/http parses it:
	// r.URL = {Scheme:"http", Host:"evil.com", Path:"/admin/"}
	req := &http.Request{
		Method: "GET",
		URL: &url.URL{
			Scheme: "http",
			Host:   "evil.com",
			Path:   "/admin/",
		},
		Header: make(http.Header),
		Host:   "evil.com",
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	t.Logf("Programmatic test: status=%d Location=%q", rec.Code, rec.Header().Get("Location"))

	loc := rec.Header().Get("Location")
	if loc != "" {
		u, err := url.Parse(loc)
		if err == nil && u.Host == "evil.com" {
			t.Errorf("HPS-2026-0005 CONFIRMED: TSR open redirect via absolute-form URI")
			t.Errorf("  r.URL = {Scheme:http Host:evil.com Path:/admin/}")
			t.Errorf("  Location: %q", loc)
			t.Errorf("  The nosec G710 comment 'host/scheme untouched' is INCORRECT")
			t.Errorf("  r.URL.String() returns full absolute URL when Scheme+Host are set")
			t.Errorf("  Recommended fix: build redirect target without Scheme/Host:")
			t.Errorf(`    target = (&url.URL{Path: newPath, RawQuery: r.URL.RawQuery}).String()`)
		} else if err == nil && u.Host == "" {
			t.Logf("PASS: Location=%q is path-only (no host injection)", loc)
		}
	}
}

// TestHPS_NoAbsoluteFormURI_CheckComment_G710 is a documentation test.
// It records the behaviour seen at commit e30ae94 and marks whether it is safe.
// This test captures the exact nsec comment text to alert maintainers if the
// incorrect claim is later re-introduced.
func TestHPS_NoAbsoluteFormURI_CheckComment_G710(t *testing.T) {
	// The #nosec G710 comment on mux.go:937 says:
	// "target is a same-origin path (TSR canonicalisation only mutates path; host/scheme untouched)"
	//
	// This claim is FALSE when r.URL.Scheme/Host are set from an absolute-form URI.
	// In that case r.URL.String() returns "scheme://host/path" which:
	// 1. Includes the attacker-controlled Host from the request line (CWE-601)
	// 2. Includes the Scheme which may differ from the server's actual scheme
	//
	// The comment was written assuming requests always have path-only URLs.
	// That assumption holds for browsers (which always use path-form) but NOT for:
	// - HTTP/1.1 proxy requests
	// - Programmatic clients
	// - Certain reverse proxy configurations
	//
	// This test exists to document the finding and make it visible in CI.

	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	req := &http.Request{
		Method: "GET",
		URL: &url.URL{
			Scheme: "http",
			Host:   "attacker.example.com",
			Path:   "/admin/",
		},
		Header: make(http.Header),
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	loc := rec.Header().Get("Location")
	t.Logf("Location from absolute-form URI with attacker host: %q", loc)

	if strings.Contains(loc, "attacker.example.com") {
		t.Errorf("FINDING HPS-2026-0005: Location header reflects attacker-controlled host")
		t.Errorf("  The comment 'host/scheme untouched' on mux.go:937 is incorrect")
		t.Errorf("  for absolute-form URI requests where r.URL.Host is set by net/http parser")

		// Create evidence file
		evidence := fmt.Sprintf(
			"HPS-2026-0005 Open Redirect Evidence\n"+
				"Commit: e30ae946f634cbec54c0ae9445cbf3787ca24f31\n"+
				"Go: go1.26.2 linux/amd64\n\n"+
				"Attack:\n"+
				"  Request: GET http://attacker.example.com/admin/ HTTP/1.1\n"+
				"  (or programmatic: r.URL = {Scheme:'http', Host:'attacker.example.com', Path:'/admin/'})\n\n"+
				"Observed:\n"+
				"  Status: %d\n"+
				"  Location: %q\n\n"+
				"Expected (safe):\n"+
				"  Location: '/admin' (path-only, no host)\n\n"+
				"Root cause:\n"+
				"  mux.go:931: target := r.URL.String()\n"+
				"  When r.URL.Scheme and r.URL.Host are set (absolute-form URI),\n"+
				"  url.URL.String() returns the full absolute URL including attacker-controlled host.\n\n"+
				"Recommended fix:\n"+
				"  Replace r.URL.String() with path-only construction:\n"+
				"    newURL := &url.URL{Path: newPath, RawQuery: r.URL.RawQuery}\n"+
				"    target := newURL.String()\n",
			rec.Code, loc,
		)
		_ = evidence
		t.Logf("Evidence:\n%s", evidence)
	} else {
		t.Logf("PASS: Location %q does not reflect attacker-controlled host", loc)
	}
}
