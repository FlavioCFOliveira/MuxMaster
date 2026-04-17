package harness

import (
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rawHTTPExchange opens a TCP connection to srv, writes raw bytes verbatim,
// and returns the response bytes read until EOF or 2s deadline.
func rawHTTPExchange(srv *httptest.Server, raw string) ([]byte, error) {
	u := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", u, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte(raw)); err != nil {
		return nil, err
	}
	var resp []byte
	buf := make([]byte, 8192)
	for {
		n, rerr := conn.Read(buf)
		if n > 0 {
			resp = append(resp, buf[:n]...)
		}
		if rerr == io.EOF {
			return resp, nil
		}
		if rerr != nil {
			if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
				return resp, nil
			}
			return resp, rerr
		}
		if len(resp) > 1<<20 {
			return resp, nil
		}
	}
}

// recordTranscript stores a request+response transcript under evidence/.
func recordTranscript(t *testing.T, name, request string, response []byte) {
	t.Helper()
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(filepath.Join(evidenceDir, name))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "===== REQUEST =====\n%q\n===== RESPONSE =====\n%q\n===== END =====\n",
		request, string(response))
}
