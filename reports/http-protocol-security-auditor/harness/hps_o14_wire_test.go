// hps_o14_wire_test.go — closes the remaining O-14 HTTP-protocol coverage
// gaps (reports/overview/findings.md O-14) that are not raw-TCP smuggling
// variants:
//
//   - OPTIONS * (asterisk-form request-target, RFC 9110 §9.3.7)
//   - a CRLF in a header value set directly by a handler cannot split the
//     response on the wire (net/http's own defence, observed end-to-end)
//   - wire-level X-Forwarded-For admission of CR/LF/NUL/control bytes and
//     RealIP's resulting behaviour
//   - a raw-TCP byte-class map of what reaches r.URL.Path, asserting no
//     panic and no traversal/escape dispatch
//   - regression coverage for specification/middleware.md requirement 11:
//     Use middleware wraps the automatic 404, 405, OPTIONS, and
//     TSR/fixed-path redirect responses (the pre-5f804fa harness tested the
//     OPPOSITE, now-superseded behaviour in TestOPTIONSPreAuth /
//     TestMethodNotAllowedPreAuth / TestRedirectTrailingSlashNoAuthBypass)
//
// Commit under test: 586578644f1cbc6e8c18d95d29bd5b8d0248cb5d
// Go: go1.27.0 linux/amd64
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─────────────────────────────────────────────────────────────────────────────
// O-14 item 2: OPTIONS * (asterisk-form request-target)
// ─────────────────────────────────────────────────────────────────────────────
// RFC 9110 §9.3.7: "OPTIONS * HTTP/1.1" is a server-wide query with no
// implied target resource.
//
// FINDING (not a MuxMaster defect — a Go net/http behaviour every
// net/http-based router shares, recorded here per operating instructions):
// net/http's own serverHandler.ServeHTTP special-cases this exact request
// line BEFORE calling the configured Handler at all:
//
//	if !sh.srv.DisableGeneralOptionsHandler && req.RequestURI == "*" && req.Method == "OPTIONS" {
//		handler = globalOptionsHandler{}
//	}
//
// (net/http/server.go, go1.27). globalOptionsHandler sets
// "Content-Length: 0" and returns — implicit 200 OK, empty body, no Allow
// header. MuxMaster.ServeHTTP is NEVER invoked for this request form, by
// default (DisableGeneralOptionsHandler is false unless an operator
// explicitly sets it on their *http.Server). Consequently:
//   - mux.Pre (documented in specification/middleware.md rule 6 as seeing
//     "every request, including requests that result in 404 or 405") does
//     NOT see "OPTIONS *" — an IP allowlist or rate limiter registered via
//     Pre is silently bypassed for this one request form.
//   - mux.Use, mux.GlobalOPTIONS, and HandleOPTIONS are equally bypassed.
//
// This is not exploitable as a route/method-enumeration or crash vector —
// the stdlib handler always answers the same fixed, empty 200 regardless of
// what routes exist — but it IS a real, documentation-relevant scope gap in
// the "Pre sees every request" claim, worth a SECURITY.md/spec note for
// operators who rely on Pre as a universal gate.
func TestHPS_O14_OptionsAsterisk(t *testing.T) {
	var preCalled atomic.Bool

	m := muxmaster.New()
	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			preCalled.Store(true)
			next.ServeHTTP(w, r)
		})
	})
	m.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	raw := "OPTIONS * HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
	resp := rawHTTP(t, addr, raw)
	statusLine := resp
	if idx := strings.IndexAny(resp, "\r\n"); idx >= 0 {
		statusLine = resp[:idx]
	}
	t.Logf("OPTIONS * response: %q", resp)

	if !strings.Contains(statusLine, "200") {
		t.Errorf("UNEXPECTED: OPTIONS * returned %q, expected 200 OK from net/http's "+
			"globalOptionsHandler (server.go, DisableGeneralOptionsHandler=false by default)", statusLine)
	}
	if !strings.Contains(resp, "Content-Length: 0") {
		t.Errorf("UNEXPECTED: OPTIONS * response missing \"Content-Length: 0\": %q", resp)
	}
	if strings.Contains(resp, "Allow:") {
		t.Errorf("UNEXPECTED: OPTIONS * response contains an Allow header (globalOptionsHandler "+
			"never sets one): %q", resp)
	}
	if strings.ContainsAny(statusLine, "\x00") {
		t.Errorf("VULNERABLE: NUL byte in status line: %q", statusLine)
	}
	if preCalled.Load() {
		t.Errorf("UNEXPECTED: mux.Pre ran for \"OPTIONS *\" — net/http's globalOptionsHandler " +
			"interception must have been disabled or removed; re-verify the documented bypass still holds")
	} else {
		t.Logf("CONFIRMED: mux.Pre did NOT run for \"OPTIONS *\" — net/http's globalOptionsHandler " +
			"answered before MuxMaster.ServeHTTP was ever called")
	}

	// Confirm the connection is still healthy for a subsequent, unrelated
	// request — i.e. handling "*" did not corrupt server state.
	raw2 := "GET /x HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
	resp2 := rawHTTP(t, addr, raw2)
	if !strings.Contains(resp2, "204") {
		t.Errorf("server unhealthy after OPTIONS *: GET /x returned %q", resp2[:min(len(resp2), 80)])
	}
	if !preCalled.Load() {
		t.Errorf("mux.Pre did not run for the follow-up GET /x — Pre should see every request routed through MuxMaster.ServeHTTP")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// O-14 item 3: a CRLF in a header value set by a handler cannot split the
// response, observed on the real wire (not just via httptest.Recorder).
// ─────────────────────────────────────────────────────────────────────────────
// This complements TestHPS0017_SetHeader_PanicsOnCRLF, which only proves
// that MuxMaster's OWN middleware.SetHeader helper guards CRLF at
// construction time. Here we bypass that guard entirely and call
// w.Header().Set directly from a handler — the actual defence in this path
// is net/http's Header.writeSubset (net/http/header.go), which replaces
// every '\n' and '\r' in a header value with a space before writing, and
// drops any header whose FIELD NAME is invalid outright. We assert this
// end-to-end over a real TCP connection.
func TestHPS_O14_HandlerHeaderCRLF_CannotSplitResponseOnWire(t *testing.T) {
	m := muxmaster.New()
	m.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		// Deliberately bypass middleware.SetHeader's construction-time
		// panic guard — simulates a handler author who forgot it exists,
		// or third-party middleware not written for this codebase.
		w.Header().Set("X-Evil", "safe\r\nSet-Cookie: injected=1\r\nX-Second-Header: leak")
		w.WriteHeader(200)
		w.Write([]byte("body"))
	})

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	raw := "GET /x HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
	resp := rawHTTP(t, addr, raw)
	t.Logf("Raw response:\n%q", resp)

	// A successful split would show Set-Cookie and X-Second-Header as their
	// OWN header lines (each terminated by its own \r\n on both sides).
	if strings.Contains(resp, "\r\nSet-Cookie: injected=1\r\n") {
		t.Errorf("VULNERABLE HPS-2026-O14-3: CRLF in handler-set header split the response — " +
			"Set-Cookie appeared as an independent header line")
		t.Errorf("CWE-113: HTTP Response Splitting")
	}
	if strings.Contains(resp, "\r\nX-Second-Header: leak\r\n") {
		t.Errorf("VULNERABLE HPS-2026-O14-3: CRLF in handler-set header split the response — " +
			"X-Second-Header appeared as an independent header line")
	}

	// Positive assertion: net/http neutralises the CR/LF by replacing EACH
	// of the two bytes (\r and \n) with its own space, keeping the whole
	// payload inside ONE header value on the X-Evil line (hence the double
	// spaces below, one per replaced byte).
	const wantFolded = "X-Evil: safe  Set-Cookie: injected=1  X-Second-Header: leak\r\n"
	if !strings.Contains(resp, wantFolded) {
		t.Errorf("EXPECTED net/http's CR/LF-to-space folding not observed.\nwant substring: %q\ngot response: %q",
			wantFolded, resp)
	} else {
		t.Logf("PASS: net/http folded CR/LF into spaces, keeping one header line — no response splitting")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// O-14 item 4: wire-level X-Forwarded-For admission of CR/LF/NUL/control
// bytes, and RealIP's resulting behaviour.
// ─────────────────────────────────────────────────────────────────────────────
// net/textproto validates every header VALUE byte against
// validHeaderValueByte (VCHAR / SP / HTAB / obs-text only) while parsing
// the wire. A byte embedded mid-value that fails that check either (a)
// aborts header parsing with a 400 before RealIP ever runs, or (b) — for
// bytes textproto does accept (HTAB) — reaches RealIP as part of the raw
// XFF string, where RealIP's own defence (netip.ParseAddr, which rejects
// anything that is not a syntactically valid IP address) refuses to adopt
// it into r.RemoteAddr.
func TestHPS_O14_XFF_WireLevelCRLF_And_RealIPBehaviour(t *testing.T) {
	loopback4 := netip.MustParsePrefix("127.0.0.1/32")
	loopback6 := netip.MustParsePrefix("::1/128")

	cases := []struct {
		name          string
		xffLineSuffix string // appended verbatim after "X-Forwarded-For: "
		wantStatus    string // substring expected in the status line
		note          string
	}{
		{
			name:          "normal",
			xffLineSuffix: "1.2.3.4\r\n",
			wantStatus:    "204",
			note:          "baseline — a syntactically valid IP is admitted and adopted into RemoteAddr",
		},
		{
			name:          "literal_crlf_extra_header",
			xffLineSuffix: "1.2.3.4\r\nX-Injected: evil\r\n",
			wantStatus:    "204",
			note: "NOT an injection: the attacker already controls the raw wire bytes in this " +
				"scenario and is simply writing a second, well-formed header directly. The " +
				"X-Forwarded-For value itself parses as the clean string \"1.2.3.4\"; X-Injected " +
				"never reaches or influences the RESPONSE. This case exists to document that a " +
				"literal CRLF in a Go string, once placed on the wire, is just a header " +
				"boundary — not a value-smuggling primitive.",
		},
		{
			name:          "bare_cr_mid_value",
			xffLineSuffix: "1.2.3.4\rSet-Cookie: evil=1\r\n",
			wantStatus:    "400",
			note:          "bare CR (0x0D) fails validHeaderValueByte — rejected before RealIP runs",
		},
		{
			name:          "bare_lf_mid_value",
			xffLineSuffix: "1.2.3.4\nmalicious\r\n",
			wantStatus:    "400",
			note: "the bare LF terminates the header line early at the textproto layer; the next " +
				"line \"malicious\" has no colon, failing mustHaveFieldNameColon — rejected",
		},
		{
			name:          "nul_mid_value",
			xffLineSuffix: "1.2.3.4\x00extra\r\n",
			wantStatus:    "400",
			note:          "NUL (0x00) fails validHeaderValueByte — rejected before RealIP runs",
		},
		{
			name:          "tab_mid_value",
			xffLineSuffix: "1.2.3.4\tsmuggle\r\n",
			wantStatus:    "204",
			note: "HTAB is grammatically valid inside a field value (RFC 9110 §5.5) and IS admitted " +
				"verbatim; netip.ParseAddr(\"1.2.3.4\\tsmuggle\") fails, so RealIP leaves RemoteAddr " +
				"at the real TCP peer address instead of adopting the malformed value",
		},
		{
			name:          "ansi_escape_mid_value",
			xffLineSuffix: "1.2.3.4\x1b[2J\r\n",
			wantStatus:    "400",
			note:          "ESC (0x1B) fails validHeaderValueByte — rejected before RealIP runs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var observedRemoteAddr string
			var observedXFF string
			handled := make(chan struct{}, 1)

			m := muxmaster.New()
			m.Use(middleware.RealIP(&loopback4, &loopback6))
			m.GET("/x", func(w http.ResponseWriter, r *http.Request) {
				observedRemoteAddr = r.RemoteAddr
				observedXFF = r.Header.Get("X-Forwarded-For")
				handled <- struct{}{}
				w.WriteHeader(204)
			})

			srv := httptest.NewServer(m)
			defer srv.Close()
			addr := srv.Listener.Addr().String()

			raw := "GET /x HTTP/1.1\r\nHost: " + addr + "\r\n" +
				"X-Forwarded-For: " + tc.xffLineSuffix +
				"Connection: close\r\n\r\n"
			resp := rawHTTP(t, addr, raw)
			statusLine := resp
			if idx := strings.IndexAny(resp, "\r\n"); idx >= 0 {
				statusLine = resp[:idx]
			}
			t.Logf("[%s] status=%q note=%s", tc.name, statusLine, tc.note)

			if !strings.Contains(statusLine, tc.wantStatus) {
				t.Errorf("UNEXPECTED status for %s: got %q, want substring %q", tc.name, statusLine, tc.wantStatus)
			}

			select {
			case <-handled:
				t.Logf("[%s] handler observed RemoteAddr=%q XFF=%q", tc.name, observedRemoteAddr, observedXFF)
				if strings.ContainsAny(observedRemoteAddr, "\r\n\x00\x1b") {
					t.Errorf("VULNERABLE HPS-2026-O14-4: control byte reached r.RemoteAddr: %q", observedRemoteAddr)
					t.Errorf("CWE-93: Improper Neutralization of CRLF Sequences")
				}
				if strings.Contains(observedXFF, "\r") || strings.Contains(observedXFF, "\n") || strings.Contains(observedXFF, "\x00") {
					t.Errorf("VULNERABLE: control byte reached r.Header.Get(X-Forwarded-For) after admission: %q", observedXFF)
				}
			default:
				if strings.Contains(tc.wantStatus, "204") {
					t.Errorf("expected handler to run for %s (want status 200) but it was never called", tc.name)
				} else {
					t.Logf("[%s] PASS: request rejected before reaching the handler (status=%s)", tc.name, statusLine)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// O-14 item 6: raw-TCP byte-class map of which bytes reach r.URL.Path.
// ─────────────────────────────────────────────────────────────────────────────
// Restores the property from the deleted url_path_surface_test.go
// (TestURLPathSurface), made assertive: for every request-target byte
// class that net/http admits onto the wire, MuxMaster must never panic,
// and must dispatch to the protected /admin/secret route if and only if
// that is what the byte class is documented to legitimately mean —
// crucially, MuxMaster (UseRawPath=false, the default) routes on
// r.URL.Path, which net/url has ALREADY percent-decoded, so a target like
// "/admin%2Fsecret" is expected and correct to reach "/admin/secret"
// (RFC 3986 §2.1: %2F is one way of writing the octet 0x2F, i.e. '/' — not
// a distinct value). The property under test is that no byte class causes
// a dot-segment to be silently collapsed (MuxMaster does not path.Clean by
// default) or otherwise reaches the protected route through a route that
// does NOT decode to the exact same path.
func TestHPS_O14_URLPathByteClassMap(t *testing.T) {
	secretHit := make(chan struct{}, 1)
	var lastPath string

	m := muxmaster.New()
	// RedirectTrailingSlash/RedirectFixedPath both default-relevant; leave
	// at MuxMaster defaults (TSR on, FixedPath off) to match production.
	m.GET("/admin/secret", func(w http.ResponseWriter, r *http.Request) {
		secretHit <- struct{}{}
		w.WriteHeader(200)
	})
	// Mount uses the separate wildcard-method tree (idxWild), so it can
	// coexist with the GET-tree "/admin/secret" registration above without
	// a catch-all/static conflict, while still observing every path that
	// does not hit a more specific registered route.
	m.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.WriteHeader(200)
	}))

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	cases := []struct {
		name          string
		target        string // raw request-target bytes, sent verbatim on the wire
		wantSecretHit bool
		rationale     string
	}{
		{"clean", "/foo/bar", false, "unrelated path"},
		{"dotdot_literal", "/foo/../admin/secret", false,
			"MuxMaster does not path.Clean by default (RedirectFixedPath=false); the literal " +
				"\"..\" segment is just another tree-lookup byte sequence and matches no route"},
		{"dotdot_encoded_lower", "/foo/%2e%2e/admin/secret", false,
			"%2e%2e decodes to the literal two-byte string \"..\", identical to dotdot_literal — " +
				"net/url decoding a dot-segment does not make MuxMaster collapse it"},
		{"dotdot_encoded_upper", "/foo/%2E%2E/admin/secret", false, "same as dotdot_encoded_lower, case-insensitive percent-hex"},
		{"double_percent_slash", "/admin%252Fsecret", false,
			"%25 decodes ONE level to a literal '%', producing the literal path \"/admin%2Fsecret\" " +
				"(percent sign followed by the two characters '2' 'F') — net/url performs exactly one " +
				"decode pass, never a second, so this never becomes \"/admin/secret\""},
		{"percent_encoded_slash", "/admin%2Fsecret", true,
			"EXPECTED, not a bypass: RFC 3986 §2.1 — %2F is simply an alternate encoding of the byte " +
				"0x2F ('/'). net/url decodes it into r.URL.Path=\"/admin/secret\", and MuxMaster's " +
				"default UseRawPath=false routes on the decoded Path, so this legitimately reaches the " +
				"same resource a literal \"/admin/secret\" request would"},
		{"backslash_literal", `/admin\secret`, false, "backslash has no special meaning to net/url or MuxMaster's tree; literal byte, no match"},
		{"null_percent_encoded", "/foo%00bar", false, "decodes to a path containing a literal NUL byte; matches no route, must not panic"},
		{"raw_highbyte_utf8", "/foo\xc0\x80bar", false, "raw non-ASCII bytes (overlong NUL encoding) pass through as opaque bytes; matches no route, must not panic"},
		{"semicolon_matrix_param", "/foo;admin=1/bar", false, "net/url treats ';' as an ordinary path byte (no matrix-parameter parsing); matches no route"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lastPath = ""
			raw := "GET " + tc.target + " HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"

			var resp string
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("PANIC dispatching request-target %q: %v", tc.target, r)
					}
				}()
				resp = rawHTTP(t, addr, raw)
			}()

			statusLine := resp
			if idx := strings.IndexAny(resp, "\r\n"); idx >= 0 {
				statusLine = resp[:idx]
			}

			t.Logf("[%s] target=%q status=%q rationale=%s", tc.name, tc.target, statusLine, tc.rationale)

			select {
			case <-secretHit:
				if !tc.wantSecretHit {
					t.Errorf("VULNERABLE HPS-2026-O14-6: request-target %q reached the protected "+
						"/admin/secret route (path-traversal/escape bypass)", tc.target)
					t.Errorf("CWE-22: Improper Limitation of a Pathname to a Restricted Directory")
				} else {
					t.Logf("PASS (expected): %q correctly routed to /admin/secret", tc.target)
				}
			default:
				if tc.wantSecretHit {
					t.Errorf("EXPECTED request-target %q to reach /admin/secret (decodes to the same "+
						"path) but it did not — observedPath=%q status=%q", tc.target, lastPath, statusLine)
				} else {
					t.Logf("PASS: /admin/secret NOT reached; observedPath=%q", lastPath)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// specification/middleware.md requirement 11: Use middleware wraps the
// automatic 404, 405, OPTIONS, and TSR/fixed-path redirect responses.
// ─────────────────────────────────────────────────────────────────────────────
// The deleted options_test.go (TestOPTIONSPreAuth, TestMethodNotAllowedPreAuth)
// and redirect_test.go (TestRedirectTrailingSlashNoAuthBypass) documented the
// OPPOSITE, now-superseded behaviour (these internal responses used to run
// WITHOUT user middleware, letting an unauthenticated client enumerate
// routes/methods). The current specification explicitly requires the
// opposite: Use middleware wraps all four internally generated responses.
// No test in this harness currently asserts the fixed behaviour: this
// closes that regression-coverage gap.
func TestHPS_O14_UseMiddlewareWrapsInternalResponses(t *testing.T) {
	var calls []string

	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.HandleOPTIONS = true
	m.HandleMethodNotAllowed = true
	m.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			next.ServeHTTP(w, r)
		})
	})
	m.GET("/resource", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/tsr/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"404-NotFound", "GET", "/does-not-exist", 404},
		{"405-MethodNotAllowed", "POST", "/resource", 405},
		{"OPTIONS-auto-response", "OPTIONS", "/resource", 204},
		{"TSR-redirect", "GET", "/tsr", 301},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls = nil
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.wantStatus)
			}
			if len(calls) != 1 {
				t.Errorf("VULNERABLE (spec middleware.md requirement 11 regression): Use middleware "+
					"did not wrap the %s response — calls=%v (want exactly 1 invocation)", tc.name, calls)
				t.Errorf("CWE-285: Improper Authorization — internal response bypasses global middleware")
			} else {
				t.Logf("PASS: Use middleware ran for %s (calls=%v)", tc.name, calls)
			}
		})
	}
}
