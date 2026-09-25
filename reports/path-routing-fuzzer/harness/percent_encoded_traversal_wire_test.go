package harness

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// =============================================================================
// TestPercentEncodedTraversal_WireLevel — rmp #274 part 2/4, item 2
// =============================================================================
//
// The deleted reports/http-protocol-security-auditor/harness/smuggle_test.go
// (git show 5f804fa^:reports/http-protocol-security-auditor/harness/
// smuggle_test.go) had a "percent_encoded_traversal" case inside
// TestSmugglingVariants:
//
//	{
//		name: "percent_encoded_traversal",
//		raw: "GET /..%2fadmin HTTP/1.1\r\nHost: " + host + "\r\n" +
//			"Connection: close\r\n\r\n",
//	},
//
// That case only fed the payload through the generic hits-map/transcript
// machinery shared with the smuggling suite (CL.TE, obs-fold, etc.) — it
// never asserted, on its own, that the request cannot reach a protected
// route, and it never varied UseRawPath. The whole TestSmugglingVariants
// function (and its harness package) is outside this agent's domain — HTTP
// framing/smuggling belongs to http-protocol-security-auditor — but the
// ROUTING question the case exercises ("does raw-wire percent-encoded
// traversal ever reach a route it shouldn't") is squarely this agent's
// scope, and had no current equivalent using a REAL TCP connection through
// Go's actual net/http request-line + URL parser (every other traversal
// check in this harness, e.g. TestInvariant_AdminNotReachableViaTraversal
// in hypotheses_test.go, goes through httptest.NewRequest, which builds the
// *http.Request directly rather than parsing an HTTP/1.1 request line off
// the wire).
//
// This test restores that coverage as a routing-layer check, and extends it
// per the task: MuxMaster.UseRawPath is exercised in BOTH states, since the
// routing decision for a percent-encoded path is UseRawPath-dependent
// (mux.go: "UseRawPath uses r.URL.RawPath for matching when set and
// non-empty").
func rawHTTPGet(t *testing.T, addr, requestLine string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(requestLine)); err != nil {
		t.Fatalf("write: %v", err)
	}
	var b strings.Builder
	r := bufio.NewReader(conn)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return b.String()
}

// buildProtectedRouteMux mirrors the fixture the deleted smuggle test used
// (an /admin handler standing in for "protected route") plus a catch-all so
// the traversal payload has somewhere plausible to land if the router's
// segment matching were broken.
func buildProtectedRouteMux(useRawPath bool) *mm.Mux {
	r := mm.New()
	r.UseRawPath = useRawPath
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.Header().Set("X-Param-filepath", mm.PathParam(req, "filepath"))
		w.WriteHeader(200)
	})
	return r
}

// TestPercentEncodedTraversal_WireLevel sends the exact payload the deleted
// smuggle_test.go case used — "GET /..%2fadmin HTTP/1.1" — over a real TCP
// connection to an httptest.Server, for both UseRawPath=false (default) and
// UseRawPath=true, and asserts the response never carries X-Handler: admin.
func TestPercentEncodedTraversal_WireLevel(t *testing.T) {
	payloads := []string{
		"/..%2fadmin",
		"/..%2Fadmin",
		"/static/..%2fadmin",
		"/%2e%2e%2fadmin",
		"/%2e%2e/admin",
	}

	for _, useRaw := range []bool{false, true} {
		useRaw := useRaw
		t.Run(fmt.Sprintf("UseRawPath=%v", useRaw), func(t *testing.T) {
			mux := buildProtectedRouteMux(useRaw)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			addr := strings.TrimPrefix(srv.URL, "http://")

			for _, payload := range payloads {
				t.Run(payload, func(t *testing.T) {
					raw := "GET " + payload + " HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n"
					resp := rawHTTPGet(t, addr, raw)

					statusLine := ""
					if i := strings.Index(resp, "\r\n"); i >= 0 {
						statusLine = resp[:i]
					}
					reachedAdmin := strings.Contains(resp, "X-Handler: admin")

					t.Logf("payload=%q useRawPath=%v status=%q reachedAdmin=%v",
						payload, useRaw, statusLine, reachedAdmin)

					if reachedAdmin {
						t.Fatalf("PRF-WIRE-TRAVERSAL: raw-wire request %q (UseRawPath=%v) reached the /admin handler — response:\n%s",
							payload, useRaw, resp)
					}
				})
			}
		})
	}
}
