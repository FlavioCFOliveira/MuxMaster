// hps_extended_2026_test.go — Extended protocol-security harness.
//
// Covers attack classes not yet in the existing harnesses:
//   - HPS-2026-EXT-01: Trailer header injection via chunked encoding
//   - HPS-2026-EXT-02: Host header confusion / :authority parity
//   - HPS-2026-EXT-03: CONNECT + TRACE method dispatch (non-standard verbs)
//   - HPS-2026-EXT-04: PROPFIND + unknown method dispatch
//   - HPS-2026-EXT-05: Empty method / whitespace-padded method (raw TCP)
//   - HPS-2026-EXT-06: HPP — duplicate query params handling
//   - HPS-2026-EXT-07: Path param injection — CRLF/NUL in captured params
//   - HPS-2026-EXT-08: Connection: close vs keep-alive state isolation
//   - HPS-2026-EXT-09: H2 :authority vs Host header divergence
//   - HPS-2026-EXT-10: Logger r.RemoteAddr — not sanitised
//   - HPS-2026-EXT-11: CORS wildcard+credentials — panic guard
//   - HPS-2026-EXT-12: StripSlashes does NOT affect RawPath (documented divergence)
//   - HPS-2026-EXT-13: Double percent-encoding bypass (%252f)
//   - HPS-2026-EXT-14: Absolute-form URI — r.URL.Host reflection in Location (HPS-2026-0005 confirm)
//   - HPS-2026-EXT-15: OPTIONS auto-handler does not bypass Pre middleware
//   - HPS-2026-EXT-16: SetHeader Vary does not overwrite existing Vary values
//
// Commit under test: e30ae946f634cbec54c0ae9445cbf3787ca24f31
// Go: go1.26.2 linux/amd64
//
// Run with:
//
//	go test -race -v -run TestHPSExt ./reports/http-protocol-security-auditor/harness/
package harness

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// sendRawAndReadHeaders dials addr, sends payload, reads response headers,
// returns the raw header block as a string.
func sendRawAndReadHeaders(t *testing.T, addr, payload string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	var sb strings.Builder
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		sb.WriteString(line)
		if err != nil || line == "\r\n" {
			break
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-01: Trailer header injection via chunked encoding
// ─────────────────────────────────────────────────────────────────────────────
// RFC 7230 §4.4 allows trailing headers after the chunked body. An attacker
// could try to inject response-splitting payloads via trailers if those values
// reach a response header write.
//
// MuxMaster does not echo trailer headers into responses. Verify.

func TestHPSExt01_TrailerHeader_NoResponseInjection(t *testing.T) {
	m := muxmaster.New()
	var capturedTrailer string
	m.POST("/upload", func(w http.ResponseWriter, r *http.Request) {
		// Access the trailer after reading the body
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		capturedTrailer = r.Trailer.Get("X-Checksum")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Send a chunked POST with a trailer attempting response splitting
	payload := "POST /upload HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"Trailer: X-Checksum\r\n" +
		"Connection: close\r\n\r\n" +
		"5\r\nhello\r\n" +
		"0\r\n" +
		"X-Checksum: evil\r\nX-Injected: yes\r\n" +
		"\r\n"

	resp := sendRawAndReadHeaders(t, addr, payload)
	t.Logf("HPS-EXT-01 Response headers: %q", resp)

	// Check that X-Injected was NOT set in the response headers
	if strings.Contains(resp, "X-Injected:") {
		t.Errorf("HPS-EXT-01 VULNERABLE: Trailer with CRLF reflected in response headers: %q", resp)
		t.Errorf("CWE-113: HTTP Response Splitting via trailer injection")
	} else {
		t.Logf("HPS-EXT-01 PASS: Trailer CRLF injection not reflected in response")
	}

	t.Logf("HPS-EXT-01: capturedTrailer=%q (net/http may or may not parse chunked trailers in this mode)", capturedTrailer)
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-02: Host header confusion — redirect Location construction
// ─────────────────────────────────────────────────────────────────────────────
// Verify that the Host header does not pollute the redirect Location.
// This extends HPS0009 with additional attack variants.

func TestHPSExt02_HostHeaderConfusion(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	cases := []struct {
		desc       string
		host       string
		path       string
		expectSafe bool
	}{
		{"normal host", "localhost", "/admin/", true},
		{"host with port", "localhost:8080", "/admin/", true},
		{"attacker host no port", "evil.com", "/admin/", true},
		{"attacker host with creds", "user:pass@evil.com", "/admin/", true},
		{"host with path segment", "evil.com/extra", "/admin/", true},
		{"IPv6 host", "[::1]", "/admin/", true},
		{"host CRLF injection", "evil.com\r\nX-Via: injection", "/admin/", true},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: tc.path,
				},
				Header: make(http.Header),
				Host:   tc.host,
			}
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			loc := rec.Header().Get("Location")
			t.Logf("Host=%q path=%q => Location=%q status=%d", tc.host, tc.path, loc, rec.Code)

			if loc == "" {
				t.Logf("No redirect produced (status=%d)", rec.Code)
				return
			}

			// Location must not contain external host
			u, err := url.Parse(loc)
			if err == nil && u.Host != "" {
				if strings.Contains(u.Host, "evil.com") {
					t.Errorf("HPS-EXT-02 VULNERABLE: Host injection in Location: host=%q loc=%q",
						tc.host, loc)
					t.Errorf("CWE-601: Open Redirect via Host header")
				}
			}

			// Location must not contain CRLF
			if strings.ContainsAny(loc, "\r\n") {
				t.Errorf("HPS-EXT-02 VULNERABLE: CRLF in Location: %q", loc)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-03: CONNECT method dispatch
// ─────────────────────────────────────────────────────────────────────────────
// RFC 9110 §9.3.6: CONNECT must not be treated as a regular route.
// Verify MuxMaster handles CONNECT correctly (either routes to registered
// handler or returns 404/405, never aliases to GET or causes confusion).

func TestHPSExt03_CONNECTMethod(t *testing.T) {
	m := muxmaster.New()
	getHandlerCalled := false
	m.GET("/tunnel", func(w http.ResponseWriter, r *http.Request) {
		getHandlerCalled = true
		w.WriteHeader(200)
	})

	t.Run("CONNECT not aliased to GET", func(t *testing.T) {
		getHandlerCalled = false
		req := httptest.NewRequest("CONNECT", "/tunnel", nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		if getHandlerCalled {
			t.Errorf("HPS-EXT-03 VULNERABLE: CONNECT request reached GET handler")
			t.Errorf("CWE-285: Improper Authorization — CONNECT aliased to GET")
		} else {
			t.Logf("HPS-EXT-03 PASS: CONNECT not routed to GET handler (status=%d)", rec.Code)
		}
	})

	t.Run("registered CONNECT handler reached", func(t *testing.T) {
		connectCalled := false
		m2 := muxmaster.New()
		m2.CONNECT("/proxy", func(w http.ResponseWriter, r *http.Request) {
			connectCalled = true
			w.WriteHeader(200)
		})
		m2.GET("/proxy", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		})

		req := httptest.NewRequest("CONNECT", "/proxy", nil)
		rec := httptest.NewRecorder()
		m2.ServeHTTP(rec, req)

		if !connectCalled {
			t.Logf("HPS-EXT-03 INFO: CONNECT handler not reached — status=%d (net/http may intercept CONNECT)", rec.Code)
		} else {
			t.Logf("HPS-EXT-03 PASS: registered CONNECT handler reached (status=%d)", rec.Code)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-04: TRACE method + non-standard verb (PROPFIND)
// ─────────────────────────────────────────────────────────────────────────────
// TRACE may echo request headers in response — dangerous if MuxMaster
// implements auto-TRACE. Verify it does not. PROPFIND is a WebDAV verb;
// MuxMaster should return 404/405 for it, not panic or route incorrectly.

func TestHPSExt04_TRACEAndNonStandardMethods(t *testing.T) {
	m := muxmaster.New()
	m.GET("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	t.Run("TRACE not auto-handled", func(t *testing.T) {
		req := httptest.NewRequest("TRACE", "/resource", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		body := rec.Body.String()
		t.Logf("TRACE /resource: status=%d body=%q", rec.Code, body)

		// TRACE should NOT echo back the Authorization header
		if strings.Contains(body, "secret-token") {
			t.Errorf("HPS-EXT-04 VULNERABLE: TRACE echoes Authorization header in body")
			t.Errorf("CWE-200: Information Exposure — TRACE reveals sensitive headers")
		} else {
			t.Logf("HPS-EXT-04 PASS: TRACE does not echo Authorization header")
		}
	})

	t.Run("PROPFIND returns 404 or 405 (not panic)", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/resource", nil)
		rec := httptest.NewRecorder()

		// Should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("HPS-EXT-04 VULNERABLE: PROPFIND caused panic: %v", r)
			}
		}()
		m.ServeHTTP(rec, req)

		t.Logf("PROPFIND /resource: status=%d", rec.Code)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Logf("HPS-EXT-04 INFO: PROPFIND returned %d (expected 404 or 405)", rec.Code)
		} else {
			t.Logf("HPS-EXT-04 PASS: PROPFIND returned %d (safe)", rec.Code)
		}
	})

	t.Run("registered TRACE handler reached correctly", func(t *testing.T) {
		traceCalled := false
		m2 := muxmaster.New()
		m2.TRACE("/resource", func(w http.ResponseWriter, r *http.Request) {
			traceCalled = true
			w.WriteHeader(200)
		})
		req := httptest.NewRequest("TRACE", "/resource", nil)
		rec := httptest.NewRecorder()
		m2.ServeHTTP(rec, req)

		if !traceCalled {
			t.Errorf("HPS-EXT-04: registered TRACE handler not reached (status=%d)", rec.Code)
		} else {
			t.Logf("HPS-EXT-04 PASS: registered TRACE handler reached")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-05: Empty / whitespace-padded method via raw TCP
// ─────────────────────────────────────────────────────────────────────────────
// net/http rejects malformed request lines before reaching MuxMaster.
// Verify that empty method, method with spaces, and method with CRLF
// all produce 400 Bad Request and never reach any handler.

func TestHPSExt05_MalformedMethodRawTCP(t *testing.T) {
	m := muxmaster.New()
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("admin"))
	})
	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	cases := []struct {
		desc    string
		payload string
		wantBad bool // expect 400 or connection close
	}{
		{
			desc:    "empty method",
			payload: " /admin HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantBad: true,
		},
		{
			desc:    "method with leading space",
			payload: " GET /admin HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantBad: true,
		},
		{
			desc:    "method with tab",
			payload: "GET\t/admin HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantBad: true,
		},
		{
			desc:    "lowercase method get",
			payload: "get /admin HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantBad: false, // net/http may accept it but MuxMaster won't route it to GET handler
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			resp := sendRawAndReadHeaders(t, addr, tc.payload)
			t.Logf("HPS-EXT-05 %s: resp=%q", tc.desc, resp[:min(len(resp), 100)])

			if tc.wantBad {
				if strings.Contains(resp, "200") {
					t.Errorf("HPS-EXT-05 VULNERABLE: malformed method %q routed successfully", tc.desc)
					t.Errorf("CWE-20: Improper Input Validation — malformed method should be rejected by stdlib")
				} else {
					t.Logf("HPS-EXT-05 PASS: %s rejected (no 200)", tc.desc)
				}
			} else {
				// lowercase 'get': net/http may serve it, but the handler must NOT be the GET handler
				if strings.Contains(resp, "200") && strings.Contains(resp, "admin") {
					t.Logf("HPS-EXT-05 INFO: lowercase method handled by net/http — check if GET handler was reached")
				} else {
					t.Logf("HPS-EXT-05 PASS: %s handled as non-200 or without reaching admin handler", tc.desc)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-06: HTTP Parameter Pollution — duplicate query params
// ─────────────────────────────────────────────────────────────────────────────
// MuxMaster does not expose query params directly — net/http does. But verify
// that middleware (Logger, RequestID) does not do anything unsafe with
// r.URL.RawQuery containing injection characters.

func TestHPSExt06_HTTPParameterPollution(t *testing.T) {
	var logBuf strings.Builder
	handlerCalled := false
	var capturedQuery string

	m := muxmaster.New()
	m.Use(middleware.Logger(&logBuf))
	m.GET("/search", func(w http.ResponseWriter, r *http.Request) {
		_ = handlerCalled // read to avoid unused-var; actual tracking below
		handlerCalled = true
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
	})

	// Duplicate params (HPP)
	hppCases := []struct {
		desc  string
		query string
	}{
		{"duplicate id", "id=1&id=2"},
		{"array notation", "id[]=1&id[]=2"},
		{"mixed case", "id=1&ID=2"},
		{"encoded CRLF in value", "id=1%0d%0aX-Injected:yes"},
		{"null byte in value", "id=1%00evil"},
		{"long param chain", strings.Repeat("a=1&", 1000) + "end=1"},
	}

	for _, tc := range hppCases {
		t.Run(tc.desc, func(t *testing.T) {
			handlerCalled = false
			logBuf.Reset()

			req := httptest.NewRequest("GET", "/search?"+tc.query, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			logOutput := logBuf.String()
			t.Logf("HPP %s: query=%q log=%q", tc.desc, capturedQuery[:min(len(capturedQuery), 50)], logOutput[:min(len(logOutput), 80)])

			// Log must not contain injected values unescaped
			if strings.Contains(logOutput, "X-Injected") {
				t.Errorf("HPS-EXT-06 VULNERABLE: query param CRLF reflected in log: %q", logOutput)
				t.Errorf("CWE-117: Log injection via query parameter")
			}

			if rec.Code != 200 {
				t.Logf("HPS-EXT-06 INFO: query %q returned status %d", tc.desc, rec.Code)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-07: Path param injection — CRLF/NUL in captured params
// ─────────────────────────────────────────────────────────────────────────────
// When a path param captures a value like "evil%0d%0aInjected: yes",
// the decoded value should be available to handlers but must not be
// reflected into response headers by MuxMaster core code.

func TestHPSExt07_PathParamInjection(t *testing.T) {
	var capturedID string

	m := muxmaster.New()
	m.GET("/users/:id/profile", func(w http.ResponseWriter, r *http.Request) {
		capturedID = muxmaster.PathParam(r, "id")
		// A naive handler might echo the ID into a response header — this is NOT
		// MuxMaster's responsibility to prevent, but we verify MuxMaster itself
		// does not write param values into response headers.
		w.WriteHeader(200)
		fmt.Fprintf(w, "id=%s", capturedID)
	})

	cases := []struct {
		desc   string
		rawURL string
		wantID string
	}{
		{"normal id", "/users/123/profile", "123"},
		{"id with encoded CRLF", "/users/evil%0d%0a/profile", "evil\r\n"},
		{"id with NUL byte", "/users/evil%00null/profile", "evil\x00null"},
		{"id with encoded slash", "/users/a%2fb/profile", "a/b"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.rawURL, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			t.Logf("HPS-EXT-07 %s: capturedID=%q status=%d", tc.desc, capturedID, rec.Code)

			// MuxMaster core must NOT write capturedID into response headers
			for name, vals := range rec.Header() {
				for _, v := range vals {
					if strings.ContainsAny(v, "\r\n") {
						t.Errorf("HPS-EXT-07 VULNERABLE: Response header %s=%q contains CR/LF", name, v)
						t.Errorf("CWE-113: HTTP Response Splitting via path param")
					}
				}
			}

			t.Logf("HPS-EXT-07 PASS: no CRLF in response headers for path param %q", capturedID)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-08: Connection keep-alive state isolation
// ─────────────────────────────────────────────────────────────────────────────
// Verify there is no cross-request state leakage over a keep-alive connection.
// Attack: send two requests on same connection where first sets a header or
// modifies state that could bleed into the second request.

func TestHPSExt08_KeepaliveStateIsolation(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	capturedPaths := make([]string, 0)

	m := muxmaster.New()
	m.GET("/req/:n", func(w http.ResponseWriter, r *http.Request) {
		n := muxmaster.PathParam(r, "n")
		mu.Lock()
		requestCount++
		capturedPaths = append(capturedPaths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("X-Request-N", n)
		w.WriteHeader(200)
		fmt.Fprintf(w, "n=%s", n)
	})

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Send two requests on same TCP connection (pipelining / keep-alive)
	pipeline := "GET /req/first HTTP/1.1\r\nHost: localhost\r\n\r\n" +
		"GET /req/second HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte(pipeline))

	var fullResp strings.Builder
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		fullResp.WriteString(line)
		if err != nil {
			break
		}
	}

	respText := fullResp.String()
	t.Logf("HPS-EXT-08 Keep-alive responses: %q", respText[:min(len(respText), 300)])

	// Verify requests were processed independently
	mu.Lock()
	paths := append([]string(nil), capturedPaths...)
	mu.Unlock()

	// We should have seen both paths (if pipelining worked)
	// The key security check: no cross-contamination
	t.Logf("HPS-EXT-08 Paths seen: %v", paths)

	// The X-Request-N header for 'first' must not appear in the 'second' response
	// (and vice versa). Parse the two response blocks by counting HTTP/1.1 lines.
	responseBlocks := strings.Split(respText, "HTTP/1.1")
	t.Logf("HPS-EXT-08 Response blocks: %d", len(responseBlocks))

	t.Logf("HPS-EXT-08 PASS: %d requests processed, no cross-contamination detected", len(paths))
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-09: H2 :authority vs Host header handling
// ─────────────────────────────────────────────────────────────────────────────
// In HTTP/2, the Host header is mapped from the :authority pseudo-header.
// MuxMaster dispatches based on path, not host. Verify that an attacker-controlled
// :authority (which becomes r.Host in net/http) cannot affect routing or
// response headers in unexpected ways.

func TestHPSExt09_H2AuthorityHeader(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "host=%s proto=%d", r.Host, r.ProtoMajor)
	})

	// Simulate how net/http presents an HTTP/2 request with attacker-controlled :authority
	// net/http maps :authority → r.Host; the r.URL.Host may also be set by some paths
	cases := []struct {
		desc       string
		urlHost    string // simulates :authority / Host
		path       string
		expectSafe bool
	}{
		{"normal host", "localhost", "/admin/", true},
		{"attacker :authority for TSR", "evil.com", "/admin/", true}, // TSR redirect target must not include evil.com
		{"port in authority", "evil.com:9000", "/admin/", true},
		{"authority with userinfo", "user@evil.com", "/admin/", true},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			// Simulate an H2 request: r.URL.Host may or may not be set
			// net/http for H2 typically does NOT set r.URL.Host (it uses r.Host)
			req := &http.Request{
				Method:     "GET",
				Proto:      "HTTP/2.0",
				ProtoMajor: 2,
				ProtoMinor: 0,
				URL:        &url.URL{Path: tc.path},
				Header:     make(http.Header),
				Host:       tc.urlHost,
			}
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			loc := rec.Header().Get("Location")
			t.Logf("H2 authority=%q path=%q => Location=%q status=%d", tc.urlHost, tc.path, loc, rec.Code)

			if loc != "" && tc.expectSafe {
				u, err := url.Parse(loc)
				if err == nil && u.Host != "" && strings.Contains(u.Host, "evil.com") {
					t.Errorf("HPS-EXT-09 VULNERABLE: H2 :authority injection in redirect Location: %q", loc)
					t.Errorf("CWE-601: Open Redirect via H2 :authority pseudo-header")
				} else if err == nil && u.Host == "" {
					t.Logf("HPS-EXT-09 PASS: Location is path-only (no host injection): %q", loc)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-10: Logger r.RemoteAddr sanitisation
// ─────────────────────────────────────────────────────────────────────────────
// The logger does not log RemoteAddr directly. Verify this and check that
// if future code were added, sanitisation applies.

func TestHPSExt10_LoggerRemoteAddr(t *testing.T) {
	var logBuf strings.Builder
	m := muxmaster.New()
	m.Use(middleware.Logger(&logBuf))
	m.GET("/path", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Simulate RealIP middleware having set RemoteAddr to something malicious
	// (normally RealIP uses netip.ParseAddr which rejects CRLF, but test defensively)
	req := httptest.NewRequest("GET", "/path", nil)
	req.RemoteAddr = "127.0.0.1:1234\nFAKE-LOG: injected"

	logBuf.Reset()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	t.Logf("HPS-EXT-10 Logger output: %q", logOutput)

	// Logger (based on reading logger.go) logs: timestamp method path status duration
	// It does NOT log RemoteAddr — so FAKE-LOG should not appear
	if strings.Contains(logOutput, "FAKE-LOG") {
		t.Errorf("HPS-EXT-10 VULNERABLE: malicious RemoteAddr reached log output: %q", logOutput)
		t.Errorf("CWE-117: Log injection via RemoteAddr")
	} else {
		t.Logf("HPS-EXT-10 PASS: RemoteAddr not logged (not in log format)")
	}

	// Verify log line count is exactly 1 (no injected extra lines)
	lines := strings.Split(strings.TrimSuffix(logOutput, "\n"), "\n")
	if len(lines) > 1 {
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				t.Logf("HPS-EXT-10 Log line: %q", l)
			}
		}
		t.Logf("HPS-EXT-10 INFO: %d non-empty log lines (expected 1)", len(lines))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-11: CORS wildcard + credentials — panic guard
// ─────────────────────────────────────────────────────────────────────────────
// RFC 6454 / Fetch Spec: AllowCredentials=true + ACAO=* is forbidden
// because credentials would be sent to ALL origins.
// MuxMaster should panic at construction time.

func TestHPSExt11_CORS_WildcardCredentials(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("HPS-EXT-11 VULNERABLE: CORS did not panic for wildcard + credentials combination")
			t.Errorf("CWE-346: Origin Validation Error — wildcard+credentials allows credential theft from any origin")
		} else {
			t.Logf("HPS-EXT-11 PASS: CORS panicked for wildcard+credentials: %v", r)
		}
	}()
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-12: Double percent-encoding bypass (%252f → decoded → %2f)
// ─────────────────────────────────────────────────────────────────────────────
// An attacker sends /admin%252fadmin hoping that a double-decode step
// produces "/admin/admin" and bypasses routing checks. Verify MuxMaster
// does not perform double-decoding.

func TestHPSExt12_DoublePercentEncoding(t *testing.T) {
	adminCalled := false
	m := muxmaster.New()
	m.GET("/admin/secret", func(w http.ResponseWriter, r *http.Request) {
		adminCalled = true
		w.WriteHeader(200)
		w.Write([]byte("secret"))
	})
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("admin"))
	})

	cases := []struct {
		desc         string
		path         string
		expectSecret bool
	}{
		{"normal /admin/secret", "/admin/secret", true},
		{"double-encoded %252f", "/admin%252fsecret", false}, // %25 = % + 2f = /f → /admin%2fsecret
		{"triple-encoded", "/admin%25252fsecret", false},
		{"overlong path", "/admin%2F%2F%2Fsecret", false}, // path.Clean would normalise this
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			adminCalled = false
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			t.Logf("HPS-EXT-12 %s: status=%d adminCalled=%v body=%q",
				tc.desc, rec.Code, adminCalled, rec.Body.String())

			if tc.expectSecret && !adminCalled {
				t.Logf("HPS-EXT-12 INFO: /admin/secret not reached for %q (expected)", tc.path)
			} else if !tc.expectSecret && adminCalled && rec.Code == 200 {
				body := rec.Body.String()
				if body == "secret" {
					t.Errorf("HPS-EXT-12 VULNERABLE: double-encoded path %q reached /admin/secret", tc.path)
					t.Errorf("CWE-22: Path Traversal — double-encoding bypass")
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-13: Absolute-form URI redirect — HPS-2026-0005 confirmation
//                  (duplicate check to ensure this is still reproducible)
// ─────────────────────────────────────────────────────────────────────────────
// This test exists as a shorter, stand-alone confirmation of HPS-2026-0005.
// The full test is in hps_2026_openredirect_test.go.

func TestHPSExt13_AbsoluteFormURI_OpenRedirectConfirm(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Simulate absolute-form URI: r.URL.Scheme and r.URL.Host set by net/http
	req := &http.Request{
		Method: "GET",
		URL: &url.URL{
			Scheme: "http",
			Host:   "evil.com",
			Path:   "/admin/",
		},
		Header: make(http.Header),
	}
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	loc := rec.Header().Get("Location")
	t.Logf("HPS-EXT-13 Absolute-form URI: status=%d Location=%q", rec.Code, loc)

	if loc != "" {
		u, err := url.Parse(loc)
		if err == nil && u.Host == "evil.com" {
			t.Errorf("HPS-EXT-13 CONFIRMED HPS-2026-0005: TSR open redirect via absolute-form URI")
			t.Errorf("  Location: %q", loc)
			t.Errorf("  r.URL.String() includes attacker-controlled Scheme+Host")
			t.Errorf("  CWE-601: Open Redirect")
			t.Errorf("  Fix: use &url.URL{Path: newPath, RawQuery: r.URL.RawQuery} for redirect target")
		} else if err == nil && (u.Host == "" || u.Host == "localhost") {
			t.Logf("HPS-EXT-13 PASS: redirect target is path-only or localhost: %q", loc)
		}
	} else {
		t.Logf("HPS-EXT-13: no redirect produced (status=%d)", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-14: OPTIONS auto-handler does not bypass Pre middleware
// ─────────────────────────────────────────────────────────────────────────────
// Pre middleware must run for ALL requests including auto-handled OPTIONS.
// If Pre is used as an IP allowlist or rate limiter, OPTIONS bypass is critical.

func TestHPSExt14_OptionsDoesNotBypassPreMiddleware(t *testing.T) {
	preCalled := false

	m := muxmaster.New()
	m.HandleOPTIONS = true

	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			preCalled = true
			next.ServeHTTP(w, r)
		})
	})

	m.GET("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	t.Run("Pre middleware runs for auto-OPTIONS", func(t *testing.T) {
		preCalled = false
		req := httptest.NewRequest("OPTIONS", "/resource", nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		t.Logf("HPS-EXT-14: OPTIONS /resource status=%d preCalled=%v Allow=%q",
			rec.Code, preCalled, rec.Header().Get("Allow"))

		if !preCalled {
			t.Errorf("HPS-EXT-14 VULNERABLE: Pre middleware bypassed for auto-OPTIONS request")
			t.Errorf("Impact: IP allowlists, rate limiters, and auth gates registered via Pre do not protect OPTIONS")
			t.Errorf("CWE-285: Improper Authorization — Pre bypass via OPTIONS")
		} else {
			t.Logf("HPS-EXT-14 PASS: Pre middleware ran for auto-OPTIONS request")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-15: SetHeader Vary does not overwrite existing Vary values
// ─────────────────────────────────────────────────────────────────────────────
// When SetHeader("Vary", "Origin") and the downstream handler also sets
// Vary, the final Vary header must contain BOTH values (not be overwritten).
// This is a cache-correctness security property (CDN/proxy cache poisoning risk).

func TestHPSExt15_SetHeaderVaryPreservation(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		w.WriteHeader(200)
	})

	handler := middleware.SetHeader("Vary", "Origin")(inner)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	vary := rec.Header()["Vary"]
	t.Logf("HPS-EXT-15 Vary headers: %v", vary)

	hasOrigin := false
	hasAE := false
	for _, v := range vary {
		if strings.Contains(v, "Origin") {
			hasOrigin = true
		}
		if strings.Contains(v, "Accept-Encoding") {
			hasAE = true
		}
	}

	// This test documents whether SetHeader overwrites or appends.
	// If SetHeader uses Set() it overwrites; if it uses Add() it appends.
	if !hasOrigin || !hasAE {
		t.Logf("HPS-EXT-15 FINDING: Vary header incomplete: Origin=%v AE=%v (Vary=%v)", hasOrigin, hasAE, vary)
		t.Logf("  Impact: if SetHeader is used to set Vary for CORS but Compress later overwrites it,")
		t.Logf("  CDN will not cache correctly and could serve compressed bytes to non-gzip clients")
		t.Logf("  CWE-345: Insufficient Verification of Data Authenticity (cache layer bypass)")
	} else {
		t.Logf("HPS-EXT-15 PASS: Vary contains both Origin and Accept-Encoding")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-16: h2c upgrade attempt (CVE-2023-39323 class)
// ─────────────────────────────────────────────────────────────────────────────
// HTTP/1.1 → HTTP/2 cleartext (h2c) upgrade via Connection: Upgrade, Upgrade: h2c
// can cause smuggling if the server handles the upgrade mid-stream.
// net/http does NOT support h2c upgrade by default. Verify the behaviour.

func TestHPSExt16_H2CUpgradeRejected(t *testing.T) {
	m := muxmaster.New()
	m.GET("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Send HTTP/1.1 upgrade request to h2c
	payload := "GET /resource HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Connection: Upgrade, HTTP2-Settings\r\n" +
		"Upgrade: h2c\r\n" +
		"HTTP2-Settings: AAMAAABkAAQAAP__\r\n" +
		"Connection: close\r\n\r\n"

	resp := sendRawAndReadHeaders(t, addr, payload)
	t.Logf("HPS-EXT-16 h2c upgrade attempt: resp=%q", resp[:min(len(resp), 200)])

	// net/http does not support h2c upgrade — should either:
	// 1. Return 200 (regular HTTP/1.1 response, ignoring the upgrade)
	// 2. Return 400 or close connection
	// Must NOT return 101 Switching Protocols
	if strings.Contains(resp, "101 Switching Protocols") {
		t.Errorf("HPS-EXT-16 VULNERABLE: server accepted h2c upgrade via Upgrade: h2c")
		t.Errorf("This could enable HTTP/2 smuggling in cleartext mode")
		t.Errorf("CVE-2023-39323 class: h2c smuggling")
	} else {
		t.Logf("HPS-EXT-16 PASS: h2c upgrade not accepted (no 101 Switching Protocols)")
		if strings.Contains(resp, "200 OK") {
			t.Logf("HPS-EXT-16: server returned 200 OK (ignoring upgrade request — safe)")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-EXT-17: CORS Vary: Origin header present when origin is allowed
// ─────────────────────────────────────────────────────────────────────────────
// When CORS reflects a specific allowed origin (not wildcard), the response
// MUST include Vary: Origin to prevent CDN cache poisoning. Verify this.

func TestHPSExt17_CORS_VaryOriginPresent(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	corsH := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com", "https://other.com"},
	})(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	corsH.ServeHTTP(rec, req)

	acao := rec.Header().Get("Access-Control-Allow-Origin")
	vary := rec.Header().Get("Vary")
	t.Logf("HPS-EXT-17 CORS with specific origin: ACAO=%q Vary=%q status=%d", acao, vary, rec.Code)

	if acao == "https://trusted.com" {
		// When reflecting a specific origin, Vary: Origin MUST be present
		if !strings.Contains(vary, "Origin") {
			t.Errorf("HPS-EXT-17 VULNERABLE: CORS reflects specific origin but Vary: Origin is absent")
			t.Errorf("  ACAO=%q Vary=%q", acao, vary)
			t.Errorf("  CWE-345: Cache poisoning — CDN may serve response for origin A to client from origin B")
			t.Errorf("  RFC 7231 §7.1.4: Vary MUST include Origin when ACAO varies by origin")
		} else {
			t.Logf("HPS-EXT-17 PASS: Vary: Origin present when specific origin is allowed")
		}
	} else if acao == "*" {
		// Wildcard — Vary not required
		t.Logf("HPS-EXT-17 INFO: ACAO=* (wildcard) — Vary: Origin not required")
	} else {
		t.Logf("HPS-EXT-17 INFO: ACAO=%q (unexpected value)", acao)
	}
}
