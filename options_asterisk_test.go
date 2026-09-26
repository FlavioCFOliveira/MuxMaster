// Tests for the asterisk-form request-target ("OPTIONS * HTTP/1.1", RFC 9110
// §9.3.7) as observed at MuxMaster.ServeHTTP — both under net/http's default
// behaviour, where its own globalOptionsHandler intercepts the request
// before MuxMaster ever sees it, and with that interception disabled via
// http.Server.DisableGeneralOptionsHandler, which is the only way for "*"
// to reach Mux.ServeHTTP at all. rmp task #278.
//
// Prior evidence (rmp #272): reports/http-protocol-security-auditor/harness/
// hps_o14_wire_test.go::TestHPS_O14_OptionsAsterisk already pins the default-
// server case. This file adds it to the root package (so it runs as part of
// the ordinary `go test ./...`, not only the security-harness module) and
// extends coverage to the DisableGeneralOptionsHandler=true case, which the
// prior evidence did not exercise.
package muxmaster_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

// rawHTTPRequest writes payload to addr over a fresh TCP connection and
// returns every byte read back before the peer closes the connection or the
// deadline expires. payload must request connection closure (e.g. via
// "Connection: close") so this returns promptly.
func rawHTTPRequest(t *testing.T, addr, payload string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close() //nolint:errcheck
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	var sb strings.Builder
	br := bufio.NewReader(conn)
	buf := make([]byte, 4096)
	for {
		n, rerr := br.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return sb.String()
}

// firstLine returns the HTTP status line (everything before the first CR or
// LF) of a raw response.
func firstLine(resp string) string {
	if idx := strings.IndexAny(resp, "\r\n"); idx >= 0 {
		return resp[:idx]
	}
	return resp
}

// newAsteriskServer starts a real httptest server with
// DisableGeneralOptionsHandler set to true, so "OPTIONS *" is routed to m
// like any other request instead of being intercepted by net/http's own
// globalOptionsHandler. The field must be set before Start, hence
// NewUnstartedServer.
func newAsteriskServer(t *testing.T, m *muxmaster.Mux) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(m)
	srv.Config.DisableGeneralOptionsHandler = true
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// ── Case 1: default server — net/http intercepts "OPTIONS *" ────────────────

// TestOptionsAsterisk_DefaultServer_InterceptedByNetHTTP confirms that, with
// http.Server's default configuration (DisableGeneralOptionsHandler=false),
// "OPTIONS * HTTP/1.1" never reaches Mux.ServeHTTP: net/http's own
// globalOptionsHandler (net/http/server.go) answers it directly with 200 OK,
// Content-Length: 0, and no Allow header. Neither mux.Pre nor
// mux.GlobalOPTIONS is invoked.
func TestOptionsAsterisk_DefaultServer_InterceptedByNetHTTP(t *testing.T) {
	var preCalled, globalCalled atomic.Bool

	m := muxmaster.New()
	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			preCalled.Store(true)
			next.ServeHTTP(w, r)
		})
	})
	m.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		globalCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	m.GET("/x", handler(http.StatusOK, "x"))

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	raw := "OPTIONS * HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
	resp := rawHTTPRequest(t, addr, raw)
	statusLine := firstLine(resp)

	if !strings.Contains(statusLine, "200") {
		t.Errorf("status line = %q, want 200 OK (net/http's globalOptionsHandler)", statusLine)
	}
	if !strings.Contains(resp, "Content-Length: 0") {
		t.Errorf("response missing \"Content-Length: 0\": %q", resp)
	}
	if strings.Contains(resp, "Allow:") {
		t.Errorf("response unexpectedly contains an Allow header (globalOptionsHandler never sets one): %q", resp)
	}
	if preCalled.Load() {
		t.Errorf("mux.Pre was invoked for OPTIONS * — net/http's globalOptionsHandler should have " +
			"answered before Mux.ServeHTTP ever ran")
	}
	if globalCalled.Load() {
		t.Errorf("mux.GlobalOPTIONS was invoked for OPTIONS * — net/http's globalOptionsHandler should have " +
			"answered before Mux.ServeHTTP ever ran")
	}

	// The connection must remain healthy for a subsequent, unrelated request.
	raw2 := "GET /x HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
	resp2 := rawHTTPRequest(t, addr, raw2)
	if !strings.Contains(firstLine(resp2), "200") {
		t.Errorf("server unhealthy after OPTIONS *: GET /x returned %q", firstLine(resp2))
	}
	if !preCalled.Load() {
		t.Errorf("mux.Pre did not run for the follow-up GET /x — it should see every request routed through Mux.ServeHTTP")
	}
}

// ── Case 2: DisableGeneralOptionsHandler=true — "*" reaches Mux.ServeHTTP ───

// TestOptionsAsterisk_DisableGeneral exercises what MuxMaster itself does
// with "OPTIONS *" once net/http's interception is turned off. r.URL.Path is
// then literally "*" (net/url.ParseRequestURI special-cases the string "*"
// into url.URL{Path: "*"} — see RFC 9110 §9.3.7's asterisk-form). Since every
// registered pattern must start with "/" (Mux.Handle panics otherwise — see
// mux.go's "path must begin with '/'" checks), no route can ever match the
// literal path "*". Consequently:
//   - Mux.dispatch's tree lookup (root.getValue / getValueStatic) never
//     matches, and TSR never triggers (no common prefix with "/").
//   - Mux.allowed("*", "OPTIONS") always returns "" (root.hasHandler("*") is
//     always false), so neither the automatic-OPTIONS branch nor the
//     405 branch ever fires, and mux.GlobalOPTIONS is never invoked and the
//     Allow header never appears — regardless of which/how many routes are
//     registered, of HandleOPTIONS, or of GlobalOPTIONS being set.
//   - The request falls through to mux.NotFound (404), same as any other
//     path that matches nothing.
//
// mux.Pre, however, IS invoked in every case: it wraps Mux.ServeHTTP in full
// (see mux.go's ServeHTTP / dispatchWithRecover), and with
// DisableGeneralOptionsHandler=true nothing intercepts the request before
// that point.
//
// This is deliberate, unsurprising behaviour consistent with
// specification/routing.md §4.6 rule 58 (automatic OPTIONS applies only "at
// the matched path"): no match, no automatic OPTIONS, no Allow-header leak,
// no panic, no redirect. No MuxMaster defect was found — no library code
// changed in this task.
func TestOptionsAsterisk_DisableGeneral(t *testing.T) {
	cases := []struct {
		name           string
		handleOPTIONS  bool
		registerRoutes bool
		setGlobalOpts  bool
	}{
		{name: "NoRoutes", handleOPTIONS: true, registerRoutes: false, setGlobalOpts: false},
		{name: "WithRoutesRegistered", handleOPTIONS: true, registerRoutes: true, setGlobalOpts: false},
		{name: "GlobalOPTIONSConfigured", handleOPTIONS: true, registerRoutes: true, setGlobalOpts: true},
		{name: "HandleOPTIONSFalse", handleOPTIONS: false, registerRoutes: true, setGlobalOpts: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var preCalled, globalCalled atomic.Bool

			m := muxmaster.New()
			m.HandleOPTIONS = tc.handleOPTIONS
			m.Pre(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					preCalled.Store(true)
					next.ServeHTTP(w, r)
				})
			})
			if tc.setGlobalOpts {
				m.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					globalCalled.Store(true)
					w.WriteHeader(http.StatusNoContent)
				})
			}
			if tc.registerRoutes {
				m.GET("/x", handler(http.StatusOK, "x"))
				m.POST("/x", handler(http.StatusOK, "x-post"))
			}

			srv := newAsteriskServer(t, m)
			addr := srv.Listener.Addr().String()

			raw := "OPTIONS * HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
			resp := rawHTTPRequest(t, addr, raw)
			statusLine := firstLine(resp)

			if !strings.Contains(statusLine, "404") {
				t.Errorf("status line = %q, want 404 (mux.NotFound — the literal path \"*\" can never match a registered route)", statusLine)
			}
			if strings.Contains(resp, "Allow:") {
				t.Errorf("response unexpectedly contains an Allow header (would leak registered routes' methods for path \"*\"): %q", resp)
			}
			if strings.Contains(resp, "Location:") {
				t.Errorf("response unexpectedly contains a Location header (unwanted redirect for path \"*\"): %q", resp)
			}
			if !preCalled.Load() {
				t.Errorf("mux.Pre was NOT invoked — with DisableGeneralOptionsHandler=true, every request " +
					"(including OPTIONS *) reaches Mux.ServeHTTP")
			}
			if globalCalled.Load() {
				t.Errorf("mux.GlobalOPTIONS was unexpectedly invoked for OPTIONS * (no route can ever match path \"*\")")
			}

			if tc.registerRoutes {
				// The server and its routing tree must remain healthy for a
				// subsequent, unrelated request.
				raw2 := "GET /x HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
				resp2 := rawHTTPRequest(t, addr, raw2)
				if !strings.Contains(firstLine(resp2), "200") {
					t.Errorf("server unhealthy after OPTIONS *: GET /x returned %q", firstLine(resp2))
				}
			}
		})
	}
}
