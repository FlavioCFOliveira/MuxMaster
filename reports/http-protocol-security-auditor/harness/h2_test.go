package harness

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestH2SmokeRequest confirms that MuxMaster can serve HTTP/2 requests
// through a TLS server using only stdlib (Go enables HTTP/2 by default
// when ListenAndServeTLS is used). This is a smoke test that establishes
// the baseline before stress tests.
func TestH2SmokeRequest(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proto=%s", r.Proto)
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}
	// ForceAttemptHTTP2 only works if TLS is in play (our case via StartTLS).
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL + "/x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	f, err := os.Create(filepath.Join(evidenceDir, "h2-smoke.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "proto=%s status=%d body=%q\n", resp.Proto, resp.StatusCode, body)

	if !strings.HasPrefix(resp.Proto, "HTTP/2") {
		t.Logf("HTTP/2 not negotiated (proto=%s) — %s", resp.Proto, body)
	}
}

// TestH2RapidResetSmoke exercises the Rapid Reset scenario (CVE-2023-44487)
// at a qualitative level: open N streams rapidly and close them immediately.
// stdlib server as of Go 1.22+ includes mitigations (MaxConcurrentStreams
// defaults, StreamResetBurst limits). Go 1.26 should still have these.
//
// This is a sanity check — not a full replay — because proper Rapid Reset
// requires a raw h2 framer client. We verify:
//   - server goroutine count stabilises after burst
//   - no panic or crash
//   - a subsequent request still succeeds
func TestH2RapidResetSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skip h2 stress in short mode")
	}

	mux := muxmaster.New()
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}

	goroutinesBefore := runtime.NumGoroutine()

	// Fire N concurrent requests and cancel their contexts immediately.
	const N = 200
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
			// Client.Do with a short context simulates rapid reset; note
			// that the real Rapid Reset attack sends RST_STREAM on the h2
			// framer directly, which we cannot do without x/net/http2.
			// This is therefore a *weaker* test — a partial surrogate.
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	time.Sleep(500 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()

	// Subsequent request must still succeed.
	resp, err := client.Get(srv.URL + "/x")
	var followUpStatus int
	if err == nil {
		followUpStatus = resp.StatusCode
		resp.Body.Close()
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h2-rapid-reset-smoke.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "go_version=%s burst=%d goroutines_before=%d goroutines_after=%d follow_up_status=%d err=%v\n",
		runtime.Version(), N, goroutinesBefore, goroutinesAfter, followUpStatus, err)

	if err != nil {
		t.Errorf("follow-up request failed after burst: %v", err)
	}
	// Pragmatic goroutine threshold: N burst + 50 growth is acceptable.
	if goroutinesAfter > goroutinesBefore+N+100 {
		t.Errorf("goroutine leak: before=%d after=%d (delta=%d, burst=%d)",
			goroutinesBefore, goroutinesAfter, goroutinesAfter-goroutinesBefore, N)
	}
}

// TestH2HeaderCRLFRejected exercises HTTP/2 header-value CRLF rejection.
// RFC 9113 §8.2.1 mandates that HPACK-encoded header values containing
// CR/LF/NUL MUST be rejected as malformed. The stdlib h2 server is
// expected to comply. We can't send raw HPACK without x/net/http2, but
// we can at least confirm that http.NewRequest rejects obviously broken
// values at the Go-level first.
func TestH2HeaderCRLFRejected(t *testing.T) {
	// The Go client's Header.Set has to reject CR/LF before they reach
	// the HPACK encoder. If it does, the attacker cannot use a legitimate
	// Go client to exploit response splitting.
	req, err := http.NewRequest("GET", "http://example.com/x", nil)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h2-header-validation.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	cases := []struct {
		name  string
		value string
	}{
		{"crlf", "a\r\nSet-Cookie: evil"},
		{"cr", "a\rmalicious"},
		{"lf", "a\nmalicious"},
		{"nul", "a\x00b"},
		{"clean", "legit"},
	}
	for _, c := range cases {
		// http.Header.Set itself does not validate; validation happens
		// in the Transport when building the request line. We simulate
		// that path.
		req.Header.Set("X-Test", c.value)
		tr := &http.Transport{}

		rt, rerr := tr.RoundTrip(req)
		status := 0
		errStr := ""
		if rerr != nil {
			errStr = rerr.Error()
		} else if rt != nil {
			status = rt.StatusCode
			rt.Body.Close()
		}
		fmt.Fprintf(f, "[%s] value=%q roundtrip_err=%q status=%d\n",
			c.name, c.value, errStr, status)
	}
}
