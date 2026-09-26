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

// ─────────────────────────────────────────────────────────────────────────────
// rmp #274 part 5a (O-14 residual gap): literal Location value for
// RedirectFixedPath on "//evil.com"-shaped payloads
// ─────────────────────────────────────────────────────────────────────────────
//
// Restores the intent of the pre-5f804fa TestH007_OpenRedirectViaFixedPath
// (reports/path-routing-fuzzer/harness/hypotheses_test.go — see
// `git show 5f804fa^:reports/path-routing-fuzzer/harness/hypotheses_test.go`),
// which registered "/evil.com/foo" and only asserted a "//" PREFIX check on
// RedirectFixedPath's Location header for five double-slash/dot-segment
// payloads. No current test pins the LITERAL Location value FixedPath
// produces, exercises the full //evil.com-shaped + traversal + backslash +
// CRLF/TAB payload set, or verifies the same property over the raw TCP wire
// as a real client would receive it:
//   - TestS8_TrailingSlash_LocationHeader (path-routing-fuzzer/s8_audit_test.go)
//     covers TSR only, and only a "//" prefix + CRLF check.
//   - TestS9_H13/H14 (path-routing-fuzzer/s9_audit_test.go) cover FixedPath's
//     middleware-bypass and route-existence-disclosure properties, not the
//     Location value itself.
//   - redirect_tsr_safety_test.go's table+fuzz harness (project root) is the
//     most rigorous existing safety net for this class of bug, but
//     buildRedirectSafetyMux hard-codes RedirectFixedPath = false, so it
//     never exercises this code path at all.
//
// buildRedirectTarget/cleanedPath (mux.go) route every FixedPath target
// through stdlib path.Clean, which by construction:
//   - always leaves exactly one leading '/' (never "//") on an absolute
//     path — it collapses repeated slashes instead of preserving them;
//   - never introduces a backslash that was not already present verbatim in
//     the decoded request path.
// Combined with cleanedPath's requirement that path.Clean(p) actually
// resolve to a REGISTERED handler, an anonymous attacker can never make
// FixedPath redirect to an arbitrary "evil.com"-shaped target: the only
// host-looking segment that can ever appear in a FixedPath Location is one
// the operator already registered as a real route. This file proves that
// invariant empirically for the full payload set, including the WHATWG URL
// Standard's backslash-as-slash "authority-establishing" special case (a
// leading "/\" is NOT "//" under RFC 3986, but IS treated as scheme-relative
// by every "special scheme" browser URL parser) — see
// looksAuthorityEstablishing below.
//
// Commit under test: 2169d773068504dd2fcb40559c6e06767c219a16
// Go: go1.27.0 linux/amd64
//
// Run with: go test -race -v -run TestHPS_FixedPath ./reports/http-protocol-security-auditor/harness/

// looksAuthorityEstablishing reports whether loc, interpreted as a relative
// URL reference by a WHATWG-compliant "special scheme" (http/https/ws/wss/
// ftp/file) URL parser, would be resolved as authority-establishing —
// i.e. would switch the parser into "relative slash"/"authority" state and
// so pick up a Host component from loc itself, overriding the base origin.
// Per the WHATWG URL Standard's "special authority slashes state", ANY
// combination of two leading '/' or '\' characters (not just "//") triggers
// this: "//", "/\", "\/", and "\\" are all equivalent. RFC 3986 §4.2 only
// recognises "//"; a plain net/url-based check (u.Host == "") is therefore
// NOT sufficient to rule out this class of open redirect — Go's net/url
// never treats a leading backslash as a path separator, so it will
// (correctly, per the RFC) report Host == "" for "/\\evil.com" even though a
// browser resolves it as scheme-relative to evil.com.
func looksAuthorityEstablishing(loc string) bool {
	isSlashLike := func(b byte) bool { return b == '/' || b == '\\' }
	return len(loc) >= 2 && isSlashLike(loc[0]) && isSlashLike(loc[1])
}

// assertFixedPathLocationSafe applies the full same-origin invariant to a
// captured Location header value: no authority-establishing prefix (RFC
// 3986 "//" AND the WHATWG backslash variants), no scheme, no host, no raw
// control byte, and path-rooted.
func assertFixedPathLocationSafe(t *testing.T, payload, loc string) {
	t.Helper()
	if loc == "" {
		return // no redirect fired — nothing to check
	}
	if looksAuthorityEstablishing(loc) {
		t.Errorf("OPEN REDIRECT (rmp #274 5a): payload=%q Location=%q is authority-establishing "+
			"(WHATWG backslash-as-slash or RFC 3986 \"//\" prefix) — a browser would navigate to an "+
			"attacker-influenced origin", payload, loc)
	}
	if loc[0] != '/' {
		t.Errorf("OPEN REDIRECT (rmp #274 5a): payload=%q Location=%q is not path-rooted", payload, loc)
	}
	for i := 0; i < len(loc); i++ {
		if b := loc[i]; b < 0x20 || b == 0x7f {
			t.Errorf("CONTROL BYTE LEAK: payload=%q Location=%q contains raw control byte 0x%02x at offset %d",
				payload, loc, b, i)
		}
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Errorf("Location %q for payload %q failed to parse as a URL reference: %v", loc, payload, err)
		return
	}
	if u.IsAbs() {
		t.Errorf("OPEN REDIRECT (rmp #274 5a): payload=%q Location=%q is an absolute URL (scheme=%q)",
			payload, loc, u.Scheme)
	}
	if u.Host != "" {
		t.Errorf("OPEN REDIRECT (rmp #274 5a): payload=%q Location=%q carries a Host component (%q)",
			payload, loc, u.Host)
	}
	// RFC 3986 §5 reference resolution: resolving loc against a fixed
	// same-origin base must stay on that origin.
	base, _ := url.Parse("https://victim.example.test/")
	resolved := base.ResolveReference(u)
	if resolved.Host != "victim.example.test" {
		t.Errorf("OPEN REDIRECT (rmp #274 5a): payload=%q Location=%q resolves (RFC 3986 §5) against "+
			"the same origin to host %q, not victim.example.test", payload, loc, resolved.Host)
	}
}

// buildFixedPathOpenRedirectMux registers the target routes a cleaned
// "//evil.com"-shaped payload could resolve to, mirroring (and extending)
// the pre-5f804fa TestH007 setup: "/evil.com/foo" was the sole target
// there; this adds "/evil.com", "/evil.com/x", "/" (path.Clean("//evil.com/
// ..") == "/"), and CRLF/TAB-bearing routes for the control-byte variants.
func buildFixedPathOpenRedirectMux(rts bool) *muxmaster.Mux {
	m := muxmaster.New()
	m.RedirectFixedPath = true
	m.RedirectTrailingSlash = rts
	m.GET("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/evil.com", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/evil.com/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/evil.com/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	// rmp #260-style CRLF/TAB target: path.Clean does not touch \r, \n or
	// \t, so if a cleaned path carrying one of them is ever dispatched to a
	// matching handler, buildRedirectTarget's output must still pass
	// through percentEncodeControlBytes before it reaches the Location
	// header (or the wire).
	m.GET("/evil.com\r\ninjected/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/evil.com\tinjected/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	return m
}

// TestHPS_FixedPath_LiteralLocation_InProcess pins the exact Location value
// RedirectFixedPath produces for the full "//evil.com"-shaped payload set,
// both with RedirectTrailingSlash off and on, and asserts the full
// same-origin invariant (assertFixedPathLocationSafe) on every result.
func TestHPS_FixedPath_LiteralLocation_InProcess(t *testing.T) {
	cases := []struct {
		payload      string
		wantLocation string // expected literal Location, "" = no redirect (404/200)
	}{
		{"//evil.com", "/evil.com"},
		{"//evil.com/x", "/evil.com/x"},
		{"///evil.com", "/evil.com"},
		{"/\\evil.com", ""}, // path.Clean is a no-op on backslash — FixedPath never fires
		{"/%2f%2fevil.com", "/evil.com"},
		{"/./evil.com", "/evil.com"},
		{"/../evil.com", "/evil.com"},
		{"//evil.com/%2e%2e", "/"},
		{"/%5cevil.com", ""}, // decodes to "/\evil.com" — same as above, no cleaning occurs
		{"//evil.com/foo", "/evil.com/foo"},
		{"///evil.com/foo", "/evil.com/foo"},
		{"//evil.com/foo/", "/evil.com/foo"},
		{"/./evil.com/foo", "/evil.com/foo"},
		{"/./../evil.com/foo", "/evil.com/foo"},
	}

	for _, rts := range []bool{false, true} {
		t.Run(fmt.Sprintf("RedirectTrailingSlash=%v", rts), func(t *testing.T) {
			m := buildFixedPathOpenRedirectMux(rts)
			for _, tc := range cases {
				t.Run(tc.payload, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, "http://example.test"+tc.payload, nil)
					rec := httptest.NewRecorder()
					m.ServeHTTP(rec, req)
					loc := rec.Header().Get("Location")
					t.Logf("payload=%q status=%d Location=%q", tc.payload, rec.Code, loc)

					if loc != tc.wantLocation {
						t.Errorf("payload=%q: Location=%q, want %q (status=%d)", tc.payload, loc, tc.wantLocation, rec.Code)
					}
					assertFixedPathLocationSafe(t, tc.payload, loc)
				})
			}
		})
	}
}

// TestHPS_FixedPath_CRLFTabVariant_InProcess exercises the CRLF/TAB payload
// variants explicitly: a cleaned path that resolves to a route whose
// pattern itself contains a raw \r\n or \t must have those bytes
// percent-encoded in the Location header, never emitted raw (CWE-113).
func TestHPS_FixedPath_CRLFTabVariant_InProcess(t *testing.T) {
	cases := []struct {
		name         string
		payload      string
		wantLocation string
	}{
		{"CRLF", "/./evil.com%0d%0ainjected/foo", "/evil.com%0D%0Ainjected/foo"},
		{"TAB", "/./evil.com%09injected/foo", "/evil.com%09injected/foo"},
	}
	for _, rts := range []bool{false, true} {
		m := buildFixedPathOpenRedirectMux(rts)
		for _, tc := range cases {
			t.Run(fmt.Sprintf("RedirectTrailingSlash=%v/%s", rts, tc.name), func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "http://example.test"+tc.payload, nil)
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				loc := rec.Header().Get("Location")
				t.Logf("payload=%q status=%d Location=%q", tc.payload, rec.Code, loc)

				if loc != tc.wantLocation {
					t.Errorf("payload=%q: Location=%q, want %q (status=%d)", tc.payload, loc, tc.wantLocation, rec.Code)
				}
				assertFixedPathLocationSafe(t, tc.payload, loc)
			})
		}
	}
}

// TestHPS_FixedPath_LiteralLocation_RawTCP replays a representative subset
// of the payload set over a real TCP connection, exactly as a browser or
// proxy would send it, and captures the Location header as it appears on
// the wire (net/http's header writer, not httptest.ResponseRecorder).
func TestHPS_FixedPath_LiteralLocation_RawTCP(t *testing.T) {
	cases := []struct {
		payload      string
		wantLocation string
	}{
		{"//evil.com", "/evil.com"},
		{"///evil.com", "/evil.com"},
		{"//evil.com/foo/", "/evil.com/foo"},
		{"/%2f%2fevil.com", "/evil.com"},
		{"//evil.com/%2e%2e", "/"},
		{"/\\evil.com", ""},
		{"/%5cevil.com", ""},
	}

	for _, rts := range []bool{false, true} {
		t.Run(fmt.Sprintf("RedirectTrailingSlash=%v", rts), func(t *testing.T) {
			m := buildFixedPathOpenRedirectMux(rts)
			srv := httptest.NewServer(m)
			defer srv.Close()
			addr := srv.Listener.Addr().String()

			for _, tc := range cases {
				t.Run(tc.payload, func(t *testing.T) {
					conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
					if err != nil {
						t.Fatalf("dial: %v", err)
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(3 * time.Second))

					raw := "GET " + tc.payload + " HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
					if _, err := conn.Write([]byte(raw)); err != nil {
						t.Fatalf("write: %v", err)
					}

					br := bufio.NewReader(conn)
					var statusLine, loc string
					var headerLines []string
					for {
						line, err := br.ReadString('\n')
						if statusLine == "" && strings.HasPrefix(line, "HTTP/") {
							statusLine = strings.TrimSpace(line)
						} else if strings.TrimSpace(line) != "" {
							headerLines = append(headerLines, strings.TrimSpace(line))
						}
						if strings.HasPrefix(line, "Location:") {
							loc = strings.TrimSpace(strings.TrimPrefix(line, "Location:"))
						}
						if line == "\r\n" || err != nil {
							break
						}
					}

					t.Logf("payload=%q status=%q Location=%q headers=%v", tc.payload, statusLine, loc, headerLines)

					if loc != tc.wantLocation {
						t.Errorf("payload=%q: wire Location=%q, want %q (status=%q)", tc.payload, loc, tc.wantLocation, statusLine)
					}
					assertFixedPathLocationSafe(t, tc.payload, loc)

					// No injected header line: exactly one "Location:" line
					// on the wire, never more (would indicate the redirect
					// value itself split the header section).
					count := 0
					for _, hl := range headerLines {
						if strings.HasPrefix(hl, "Location:") {
							count++
						}
					}
					if loc != "" && count != 1 {
						t.Errorf("payload=%q: expected exactly 1 Location header line on the wire, found %d", tc.payload, count)
					}
				})
			}
		})
	}
}

// TestHPS_FixedPath_BackslashAuthority_NotAttackerReachable documents, with
// a deliberately contrived route registration, why the backslash-authority
// trick (looksAuthorityEstablishing) could never be triggered by an
// anonymous attacker through RedirectFixedPath or RedirectTrailingSlash:
// path.Clean never introduces a backslash, and cleanedPath/TSR both require
// an EXISTING registered handler at the resulting path. The only way to
// make FixedPath emit a Location shaped like "/\..." is for the operator to
// register that exact literal route themselves — at which point the
// "attacker" is choosing among the operator's own routes, not an arbitrary
// external host.
//
// UPDATE (rmp #279, sprint 20): the hardening recommendation this test
// originally filed as a HOLD ("writeRedirect's guard could additionally
// reject target[1] == '\\'... pure defense-in-depth... NOT as an
// exploitable open redirect") has since been implemented, and implemented
// more strongly than the original recommendation: writeRedirect now
// percent-encodes every '\' in target to "%5C" (percentEncodeBackslash,
// mux.go) before any other processing, rather than merely routing the
// backslash-prefixed shape through the same fallback branch as "//" (an
// earlier iteration of this change did only that — routing alone — and was
// found insufficient: net/http.Redirect itself does not neutralise "/\"
// either, so routing to it left the byte value, and therefore the browser
// hazard, unchanged). The Location this test observes is consequently no
// longer authority-establishing: this test now asserts the neutralised
// value and that looksAuthorityEstablishing is FALSE for it, in addition to
// the original "not attacker reachable" invariant (which remains true and
// valuable independently of the neutralisation — the mitigation is
// defense-in-depth on top of a shape that was never attacker-reachable, not
// a fix for one that was). See also
// TestWriteRedirect_BackslashAuthorityShape_Neutralised and
// TestWriteRedirect_BackslashAuthorityShape_NeutralisesEndToEnd in the root
// package's redirect_bytediff_test.go, which exercise the same mechanism as
// first-class regression tests.
func TestHPS_FixedPath_BackslashAuthority_NotAttackerReachable(t *testing.T) {
	m := muxmaster.New()
	m.RedirectFixedPath = true
	// The operator registers a literal backslash-shaped route themselves —
	// this is the ONLY way path.Clean's no-op-on-backslash behaviour can
	// ever surface such a path as a FixedPath target.
	m.GET("/\\evil.com/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	// path.Clean("//\\evil.com/foo") == "/\\evil.com/foo" — the double
	// leading slash is collapsed, backslash is untouched by Clean itself;
	// writeRedirect's own percentEncodeBackslash step is what neutralises
	// the backslash afterwards.
	req := httptest.NewRequest(http.MethodGet, "http://example.test//\\evil.com/foo", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	t.Logf("payload=%q status=%d Location=%q", "//\\evil.com/foo", rec.Code, loc)

	const wantLoc = "/%5Cevil.com/foo"
	if loc != wantLoc {
		t.Fatalf("expected Location=%q (operator-registered route, backslash neutralised per rmp #279), got %q", wantLoc, loc)
	}
	if looksAuthorityEstablishing(loc) {
		t.Fatalf("expected looksAuthorityEstablishing(%q) == false — rmp #279 neutralised this shape", loc)
	}
	// Confirm the mechanism requires operator registration: the identical
	// payload against a mux that never registered that literal route
	// produces no such Location for any host an anonymous attacker could
	// choose (cleanedPath finds no match, cleanedPath returns ok=false).
	// This invariant is unaffected by the rmp #279 neutralisation — it was
	// true before and remains true after.
	m2 := muxmaster.New()
	m2.RedirectFixedPath = true
	m2.GET("/legit", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req2 := httptest.NewRequest(http.MethodGet, "http://example.test//\\attacker-chosen-host.evil/foo", nil)
	rec2 := httptest.NewRecorder()
	m2.ServeHTTP(rec2, req2)
	if loc2 := rec2.Header().Get("Location"); loc2 != "" {
		t.Fatalf("NOT ATTACKER-REACHABLE INVARIANT VIOLATED: an unregistered backslash target produced Location=%q", loc2)
	}
	t.Logf("PASS: an anonymous attacker cannot choose the host in the backslash-authority shape — "+
		"it is bound to an operator-registered route (status=%d for the unregistered case)", rec2.Code)
}
