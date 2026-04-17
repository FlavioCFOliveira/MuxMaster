package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestOptionsAsterisk: per RFC 9110 §9.3.7, "OPTIONS *" is a server-wide
// query. Go's stdlib converts "*" into r.URL.Path = "*". We check that
// MuxMaster's radix tree does not treat "*" as a catch-all.
func TestOptionsAsterisk(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)
	raw := fmt.Sprintf("OPTIONS * HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	resp, rerr := rawHTTPExchange(srv, raw)

	f, err := os.Create(filepath.Join(evidenceDir, "options-asterisk.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "request=%q\nerr=%v\nresponse=%q\n", raw, rerr, string(resp))
}

// TestTraceMethod: many servers refuse TRACE for XST (cross-site tracing).
// Confirm MuxMaster's behaviour when no TRACE is registered.
func TestTraceMethod(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)
	raw := fmt.Sprintf("TRACE /x HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	resp, rerr := rawHTTPExchange(srv, raw)

	f, err := os.Create(filepath.Join(evidenceDir, "trace-method.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "request=%q\nerr=%v\nresponse=%q\n", raw, rerr, string(resp))
}

// Keep import references if individual test files are reordered.
var _ = httptest.NewServer
var _ = http.MethodOptions
