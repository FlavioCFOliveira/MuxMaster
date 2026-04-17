package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestRealIPXFFRealTCP verifies whether stdlib's HTTP/1.1 parser admits
// control bytes into a header value. If it does, RealIP propagates them
// into r.RemoteAddr; if it rejects, the in-memory finding is moot on wire.
func TestRealIPXFFRealTCP(t *testing.T) {
	observed := make(chan string, 32)
	mux := muxmaster.New()
	mux.Use(middleware.RealIP())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		observed <- r.RemoteAddr
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := stripSchemeHost(srv.URL)

	cases := []struct {
		name string
		xff  string
	}{
		{"normal", "1.2.3.4"},
		{"crlf_in_value", "1.2.3.4\r\nSet-Cookie: evil=1"}, // should be rejected at wire
		{"lf_in_value", "1.2.3.4\nmalicious"},              // should be rejected
		{"nul_in_value", "1.2.3.4\x00extra"},               // should be rejected
		{"tab_in_value", "1.2.3.4\tsmuggle"},               // tab is allowed
		{"ansi_in_value", "1.2.3.4\x1b[2J"},                // should be rejected
	}

	f, err := os.Create(filepath.Join(evidenceDir, "real-ip-raw-tcp.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		raw := fmt.Sprintf("GET /x HTTP/1.1\r\nHost: %s\r\nX-Forwarded-For: %s\r\nConnection: close\r\n\r\n",
			host, c.xff)
		resp, rerr := rawHTTPExchange(srv, raw)
		var obs string
		select {
		case obs = <-observed:
		default:
			obs = "<not-observed>"
		}

		fmt.Fprintf(f, "[%s] xff=%q err=%v obs_remote_addr=%q status=%q\n",
			c.name, c.xff, rerr, obs, firstLine(string(resp)))
	}
}
