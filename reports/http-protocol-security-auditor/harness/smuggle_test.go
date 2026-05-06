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

// TestSmugglingVariants exercises HTTP/1.1 request-smuggling families over
// a real httptest.Server. We are defensively testing *stdlib* behaviour —
// MuxMaster does not re-parse framing — but we want to know:
//
//  1. Does stdlib reject conflicting CL/TE?  (expected: yes)
//  2. Does stdlib reject "Transfer-Encoding : chunked"? (expected: yes)
//  3. Does any of these payloads EVER reach the MuxMaster handler?
//
// Evidence for every case is serialised to evidence/smuggling-2026-04-17/.
func TestSmugglingVariants(t *testing.T) {
	hits := make(map[string]int)
	mux := muxmaster.New()
	mux.ANY("/", func(w http.ResponseWriter, r *http.Request) {
		hits[r.Method] = hits[r.Method] + 1
		hits["__target_"+r.URL.Path] = hits["__target_"+r.URL.Path] + 1
		fmt.Fprintf(w, "HANDLED %s %s\n", r.Method, r.URL.Path)
	})
	mux.ANY("/smuggled", func(w http.ResponseWriter, r *http.Request) {
		hits["SMUGGLED_REACHED"] = hits["SMUGGLED_REACHED"] + 1
		fmt.Fprintf(w, "SMUGGLED %s %s\n", r.Method, r.URL.Path)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "clte_classic",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Content-Length: 5\r\n" +
				"Transfer-Encoding: chunked\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "tecl_classic",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding: chunked\r\n" +
				"Content-Length: 3\r\n" +
				"Connection: close\r\n\r\n" +
				"8\r\nSMUGGLED\r\n0\r\n\r\n",
		},
		{
			name: "te_chunked_chunked",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding: chunked, chunked\r\n" +
				"Content-Length: 3\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\n",
		},
		{
			name: "te_chunked_identity",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding: chunked\r\n" +
				"Transfer-Encoding: identity\r\n" +
				"Content-Length: 3\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\n",
		},
		{
			name: "te_space_before_colon",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding : chunked\r\n" +
				"Content-Length: 5\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "te_tab_before_value",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding:\tchunked\r\n" +
				"Content-Length: 5\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "obs_fold_headers",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"X-Folded: line1\r\n lineContinuation\r\n" +
				"Connection: close\r\n\r\n",
		},
		{
			name: "bare_lf_line_term",
			raw:  "POST / HTTP/1.1\nHost: " + host + "\nConnection: close\n\n",
		},
		{
			name: "bare_cr_in_headers",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"X-Bad: val\rinjected\r\n" +
				"Connection: close\r\n\r\n",
		},
		{
			name: "nul_in_header_value",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"X-Nul: a\x00b\r\n" +
				"Connection: close\r\n\r\n",
		},
		{
			name: "nul_in_request_target",
			raw: "GET /\x00admin HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Connection: close\r\n\r\n",
		},
		{
			name: "crlf_in_request_target",
			raw:  "GET /\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "percent_encoded_traversal",
			raw: "GET /..%2fadmin HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Connection: close\r\n\r\n",
		},
		{
			name: "te_case_mixed",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-ENCODING: Chunked\r\n" +
				"Content-Length: 5\r\n" +
				"Connection: close\r\n\r\n" +
				"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "cl_duplicate_diff",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Content-Length: 5\r\n" +
				"Content-Length: 6\r\n" +
				"Connection: close\r\n\r\n" +
				"hello\r\n\r\n",
		},
		{
			name: "cl_duplicate_same",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Content-Length: 5\r\n" +
				"Content-Length: 5\r\n" +
				"Connection: close\r\n\r\n" +
				"hello",
		},
		{
			name: "trailer_abuse",
			raw: "POST / HTTP/1.1\r\nHost: " + host + "\r\n" +
				"Transfer-Encoding: chunked\r\n" +
				"Trailer: X-After\r\n" +
				"Connection: close\r\n\r\n" +
				"5\r\nhello\r\n0\r\nX-After: injected\r\n\r\n",
		},
		{
			name: "oversize_method",
			raw:  strings.Repeat("A", 1024) + " / HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "oversize_target",
			raw:  "GET /" + strings.Repeat("a", 1<<15) + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
		{
			name: "connect_method",
			raw:  "CONNECT evil.com:443 HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
		},
	}

	evDir := filepath.Join(evidenceDir, "smuggling")
	_ = os.MkdirAll(evDir, 0o755)

	for _, c := range cases {
		resp, err := rawHTTPExchange(srv, c.raw)
		fname := filepath.Join("smuggling", c.name+".txt")
		if err != nil {
			f, ferr := os.Create(filepath.Join(evidenceDir, fname))
			if ferr == nil {
				fmt.Fprintf(f, "===== REQUEST =====\n%q\n===== ERROR =====\n%v\n",
					c.raw, err)
				f.Close()
			}
			continue
		}
		recordTranscript(t, fname, c.raw, resp)
	}

	// Finally: write a summary of handler hits.
	f, err := os.Create(filepath.Join(evidenceDir, "smuggling", "_summary.txt"))
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "===== MuxMaster handler hits =====\n")
	for k, v := range hits {
		fmt.Fprintf(f, "%s = %d\n", k, v)
	}

	if hits["SMUGGLED_REACHED"] > 0 {
		t.Errorf("SMUGGLED /smuggled handler reached — smuggling successful!")
	}
}
