// hps_o14_smuggling_test.go — restores the 13 raw-TCP HTTP/1.1 request-
// smuggling variants from the original smuggle_test.go (deleted in 5f804fa)
// that have no current assertive equivalent, per reports/overview/
// findings.md O-14.
//
// Of the 20 original variants:
//   - 4 already have an assertive equivalent in TestHPS0010_RequestSmuggling_NetHTTPDefence
//     (clte_classic, tecl_classic, te_chunked_identity, te_space_before_colon)
//   - trailer_abuse has an equivalent in TestHPSExt01_TrailerHeader_NoResponseInjection
//   - connect_method has an equivalent in TestHPSExt03_CONNECTMethod
//   - percent_encoded_traversal is routing-layer scope (path-routing-fuzzer), not
//     HTTP-framing scope, and is intentionally not restored here
//   - the remaining 13 are restored below, made assertive (the original
//     smuggle_test.go only recorded evidence transcripts; it never asserted
//     a status code or a documented RFC 9112 rationale per variant)
//
// Every case gets its own *Mux/*httptest.Server/atomic.Bool inside the
// subtest closure, mirroring the isolation fix in TestHPS0010 (rmp #269):
// httptest.Server.Close() blocks until in-flight (including pipelined)
// requests on that server finish, which makes cross-subtest contamination
// of a shared "was /SMUGGLED reached" flag structurally impossible instead
// of merely unlikely.
//
// Expected status per variant is derived from reading Go's net/http source
// (net/http/transfer.go fixLength/parseTransferEncoding, net/textproto
// reader.go validHeaderValueByte/validHeaderFieldByte/readContinuedLineSlice,
// net/url url.go parse/stringContainsCTLByte — go1.27, GOROOT=/usr/local/go)
// and confirmed empirically by running this file. See each subtest's
// t.Logf("RFC ...") line for the exact citation.
//
// Commit under test: 586578644f1cbc6e8c18d95d29bd5b8d0248cb5d
// Go: go1.27.0 linux/amd64
package harness

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

func TestHPS_O14_SmugglingVariants(t *testing.T) {
	cases := []struct {
		name    string
		payload func(host string) string
		// wantStatus, when non-zero, is the exact leading status code expected
		// on the response's status line (e.g. 400, 501). When zero, the case
		// checks other properties instead (see wantContains/wantSmuggled).
		wantStatus int
		// wantSmuggled records whether reaching /SMUGGLED is expected and,
		// if so, whether that is a documented non-vulnerability (valid
		// HTTP/1.1 pipelining) or would be a genuine finding.
		wantSmuggled   bool
		smuggledIsSafe bool // true = reaching /SMUGGLED is NOT a vulnerability (pipelining)
		rfc            string
		rationale      string
	}{
		{
			name: "te_chunked_chunked",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"Transfer-Encoding: chunked, chunked\r\n" +
					"Content-Length: 3\r\n" +
					"Connection: close\r\n\r\n" +
					"0\r\n\r\n"
			},
			wantStatus: 501,
			rfc:        "RFC 9112 §6.1; net/http/transfer.go parseTransferEncoding",
			rationale: "A single 'Transfer-Encoding: chunked, chunked' header line yields " +
				"one slice entry with value \"chunked, chunked\" (readMIMEHeader does not split " +
				"on comma). ascii.EqualFold(raw[0], \"chunked\") is false, so Go treats this as an " +
				"unsupported transfer coding and returns 501 before ever reading a body — the " +
				"connection is never desynchronised.",
		},
		{
			name: "te_tab_before_value",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"Transfer-Encoding:\tchunked\r\n" +
					"Content-Length: 5\r\n" +
					"Connection: close\r\n\r\n" +
					"0\r\n\r\nGET /SMUGGLED HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
			},
			wantStatus: 200,
			rfc:        "RFC 9112 §5.1; net/textproto reader.go readMIMEHeader (bytes.TrimLeft(v, \" \\t\"))",
			rationale: "The tab after the colon is stripped by textproto's own value trimming " +
				"before the header value ever reaches http.Header, so the server sees a clean " +
				"'Transfer-Encoding: chunked'. Per RFC 9112 §6.1, TE wins over CL; the chunked body " +
				"terminates cleanly at '0\\r\\n\\r\\n' and the request is answered 200. The payload also " +
				"sends 'Connection: close' and a pipelined GET /SMUGGLED — because the server honours " +
				"Connection: close and tears the connection down after this one response, the trailing " +
				"bytes are never read as a second request; /SMUGGLED is correctly never reached.",
		},
		{
			name: "te_case_mixed",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"Transfer-ENCODING: Chunked\r\n" +
					"Content-Length: 5\r\n" +
					"Connection: close\r\n\r\n" +
					"0\r\n\r\nGET /SMUGGLED HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
			},
			wantStatus: 200,
			rfc:        "RFC 9110 §5.1 (field names case-insensitive); net/http/transfer.go ascii.EqualFold",
			rationale: "Header field names are canonicalised case-insensitively by textproto " +
				"(canonicalMIMEHeaderKey), and the TE value comparison uses ascii.EqualFold, so " +
				"'Transfer-ENCODING: Chunked' is fully equivalent to 'Transfer-Encoding: chunked'; the " +
				"request is answered 200. Same Connection: close outcome as te_tab_before_value — the " +
				"pipelined 'GET /SMUGGLED' is never read because the connection closes first.",
		},
		{
			name: "obs_fold_headers",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"X-Folded: line1\r\n lineContinuation\r\n" +
					"Connection: close\r\n\r\n"
			},
			wantStatus: 200,
			rfc:        "RFC 9112 §5.2 (obs-fold); net/textproto reader.go readContinuedLineSlice/skipSpace",
			rationale: "RFC 9112 deprecates obs-fold for senders but does not forbid recipients from " +
				"accepting it. Go's textproto reader still implements the RFC 2616-style line-folding " +
				"algorithm: a continuation line (leading SP/HTAB) is joined to the previous line with " +
				"a single space, producing ONE header value 'line1 lineContinuation' — never two " +
				"headers and never a false line-boundary that could desynchronise framing.",
		},
		{
			name: "bare_lf_line_term",
			payload: func(host string) string {
				return "POST / HTTP/1.1\nHost: " + host + "\nConnection: close\n\n"
			},
			wantStatus: 200,
			rfc:        "RFC 9112 §2.2 (recipients SHOULD accept bare LF); bufio.Reader.ReadLine",
			rationale: "Go's bufio.Reader.ReadLine() treats a bare '\\n' as a valid line terminator " +
				"(stripping a preceding '\\r' only if present), so an entirely bare-LF request parses " +
				"identically to a CRLF one. This is tolerant-receiver behaviour explicitly permitted " +
				"by RFC 9112 §2.2, not a parsing divergence: MuxMaster never sees raw bytes, only the " +
				"already-normalised *http.Request.",
		},
		{
			name: "bare_cr_in_headers",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"X-Bad: val\rinjected\r\n" +
					"Connection: close\r\n\r\n"
			},
			wantStatus: 400,
			rfc:        "RFC 9112 §5.5 (field-vchar excludes CTL); net/textproto validHeaderValueByte",
			rationale: "A bare CR (0x0D) mid-value is not itself a line terminator (ReadLine looks " +
				"for '\\n'), so it survives into the header value bytes, where validHeaderValueByte " +
				"rejects it (CR is outside VCHAR/SP/HTAB/obs-text) — readMIMEHeader returns a " +
				"ProtocolError, and net/http's server maps any non-distinguished error to a generic " +
				"400 Bad Request (server.go default branch) before the request ever reaches MuxMaster.",
		},
		{
			name: "nul_in_header_value",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"X-Nul: a\x00b\r\n" +
					"Connection: close\r\n\r\n"
			},
			wantStatus: 400,
			rfc:        "RFC 9112 §5.5; net/textproto validHeaderValueByte",
			rationale: "NUL (0x00) is outside the allowed field-vchar/SP/HTAB/obs-text set, identical " +
				"reasoning to bare_cr_in_headers — 400 Bad Request at the textproto layer.",
		},
		{
			name: "nul_in_request_target",
			payload: func(host string) string {
				return "GET /\x00admin HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
			},
			wantStatus: 400,
			rfc:        "RFC 9112 §3.2; net/url url.go parse/stringContainsCTLByte",
			rationale: "NUL is an ASCII control byte. url.ParseRequestURI rejects any request-target " +
				"containing a control byte with \"net/url: invalid control character in URL\" before " +
				"req.URL is ever populated — readRequestLimit returns that error, mapped to a generic " +
				"400 Bad Request. The router never sees a NUL byte in r.URL.Path.",
		},
		{
			name: "crlf_in_request_target",
			payload: func(host string) string {
				return "GET /\r\n\r\nGET /SMUGGLED HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
			},
			wantStatus:     400,
			wantSmuggled:   false,
			smuggledIsSafe: false,
			rfc:            "RFC 9112 §3; net/http request.go parseRequestLine",
			rationale: "The CRLF terminates the request line after just \"GET /\", which has only " +
				"two space-separated tokens (method, target) and is missing the HTTP-version token. " +
				"parseRequestLine's 3-token check fails, producing a 'malformed HTTP request' error " +
				"→ 400 Bad Request, and the connection is not left in a state where the trailing " +
				"'GET /SMUGGLED ...' bytes get dispatched as a second request.",
		},
		{
			name: "cl_duplicate_same",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"Content-Length: 5\r\n" +
					"Content-Length: 5\r\n" +
					"Connection: close\r\n\r\n" +
					"hello"
			},
			wantStatus: 200,
			rfc:        "RFC 9112 §6.3.5 (duplicate identical CL permitted); net/http/transfer.go fixLength",
			rationale: "fixLength explicitly deduplicates repeated Content-Length headers when every " +
				"value is textproto.TrimString-identical, per RFC 9112 §6.3.5's exception for " +
				"duplicate-but-consistent Content-Length. The request is framed as an ordinary 5-byte " +
				"body and handled normally — this is not a smuggling primitive.",
		},
		{
			name: "cl_duplicate_diff",
			payload: func(host string) string {
				return "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
					"Content-Length: 5\r\n" +
					"Content-Length: 6\r\n" +
					"Connection: close\r\n\r\n" +
					"hello\r\n\r\n"
			},
			wantStatus: 400,
			rfc:        "RFC 9112 §6.3.5 (conflicting CL MUST be rejected); net/http/transfer.go fixLength",
			rationale: "fixLength finds the two Content-Length values differ and returns a hard error " +
				"(\"message cannot contain multiple Content-Length headers\") — exactly the CL.CL " +
				"smuggling hardening RFC 9112 §6.3.5 mandates. Mapped to a generic 400 Bad Request.",
		},
		{
			name: "oversize_method",
			payload: func(host string) string {
				return strings.Repeat("A", 1024) + " / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
			},
			wantStatus: 405,
			rfc:        "RFC 9110 §9.1 (method is an opaque token); MuxMaster mux.go methodIdx/allowed",
			rationale: "1024 'A' characters are all valid tchar bytes, so validMethod() accepts the " +
				"token and it is well under the 1 MiB DefaultMaxHeaderBytes limit, so no 431 is " +
				"triggered either. The router receives it as an unrecognised method (methodIdx " +
				"returns -1 for anything outside the fixed 10-method array), finds GET registered at " +
				"'/', and returns 405 with a populated Allow header — the oversized token itself never " +
				"causes a panic, a hang, or an unbounded allocation.",
		},
		{
			name: "oversize_target",
			payload: func(host string) string {
				return "GET /" + strings.Repeat("a", 1<<15) + " HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
			},
			wantStatus: 404,
			rfc:        "net/http server.go DefaultMaxHeaderBytes (1 MiB)",
			rationale: "A 32 KiB request-target is well under the 1 MiB header-size ceiling, parses " +
				"as a normal (if enormous) path, and — since no route matches it — the router returns " +
				"a plain 404, with no panic and no unbounded allocation proportional to anything " +
				"beyond the path length itself.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh Mux/server/flag per subtest — see file doc comment.
			m := muxmaster.New()
			var smuggledHit atomic.Bool
			m.GET("/SMUGGLED", func(w http.ResponseWriter, r *http.Request) {
				smuggledHit.Store(true)
				w.WriteHeader(200)
				w.Write([]byte("smuggled-reached"))
			})
			m.GET("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.Write([]byte("root"))
			})
			m.POST("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.Write([]byte("root-post"))
			})

			srv := httptest.NewServer(m)
			defer srv.Close()
			addr := srv.Listener.Addr().String()
			host := addr

			resp := rawHTTP(t, addr, tc.payload(host))
			statusLine := resp
			if idx := strings.IndexAny(resp, "\r\n"); idx >= 0 {
				statusLine = resp[:idx]
			}
			t.Logf("Response status line: %q", statusLine)
			t.Logf("RFC: %s", tc.rfc)
			t.Logf("Rationale: %s", tc.rationale)

			if tc.wantStatus != 0 {
				want := strconv.Itoa(tc.wantStatus)
				if !strings.Contains(statusLine, want) {
					t.Errorf("VULNERABLE / UNEXPECTED HPS-2026-O14-%s: expected status %d, got status line %q",
						tc.name, tc.wantStatus, statusLine)
					t.Errorf("Full response (truncated): %q", resp[:min(len(resp), 400)])
				}
			}

			hit := smuggledHit.Load()
			switch {
			case tc.wantSmuggled && tc.smuggledIsSafe:
				if !hit {
					t.Errorf("EXPECTED pipelining not observed for %s — /SMUGGLED was not reached; "+
						"this may indicate a behavioural regression in TE/CL precedence", tc.name)
				} else {
					t.Logf("INFO: /SMUGGLED reached via valid HTTP/1.1 pipelining (documented, not a vulnerability): %s", tc.name)
				}
			case !tc.wantSmuggled && hit:
				t.Errorf("CRITICAL HPS-2026-O14-%s: /SMUGGLED handler was reached unexpectedly!", tc.name)
				t.Errorf("CWE-444: Inconsistent Interpretation of HTTP Requests (request smuggling)")
			default:
				if hit {
					t.Errorf("CRITICAL HPS-2026-O14-%s: /SMUGGLED handler was reached — smuggling risk", tc.name)
				} else {
					t.Logf("PASS: /SMUGGLED not reached for %s", tc.name)
				}
			}
		})
	}
}
