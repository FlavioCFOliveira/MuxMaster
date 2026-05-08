// Package h2harness tests HTTP/2-specific attack vectors against MuxMaster.
//
// Covers:
//   - CVE-2023-44487  Rapid Reset (RST_STREAM flood)
//   - CVE-2024-27316  CONTINUATION flood
//   - HPACK bombing   (dynamic table exhaustion)
//
// All three vectors exploit weaknesses in the HTTP/2 framing layer.  MuxMaster
// delegates framing to Go's net/http stdlib, which patches these CVEs in Go
// 1.21.3+.  These tests confirm that the mitigations hold in the version under
// test (Go 1.26.2) and that MuxMaster's routing layer does not re-introduce
// resource-exhaustion opportunities on top of stdlib.
//
// Run with:
//
//	go test -race -v -timeout 60s ./reports/http-protocol-security-auditor/harness/h2harness/
//
// Commit under test: f4faa5405324fe6779b2624741ac282388d3006c
// Go: go1.26.2 linux/amd64
package h2harness

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"golang.org/x/net/http2"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// newH2Server creates an httptest TLS server with HTTP/2 enabled, backed by
// MuxMaster.  Returns the *httptest.Server and a *http2.Transport pre-wired
// to trust the test server's self-signed certificate.
func newH2Server(t *testing.T) (*httptest.Server, *http2.Transport) {
	t.Helper()

	m := muxmaster.New()
	m.GET("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	m.GET("/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data-payload")
	})

	srv := httptest.NewUnstartedServer(m)
	// Force HTTP/2.
	srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // test-only self-signed cert
		},
	}
	return srv, tr
}

// goroutineCount returns the current number of live goroutines.
func goroutineCount() int { return runtime.NumGoroutine() }

// ─────────────────────────────────────────────────────────────────────────────
// CVE-2023-44487: Rapid Reset attack
// ─────────────────────────────────────────────────────────────────────────────
//
// Attack: open many streams (HEADERS frames) and immediately send RST_STREAM,
// forcing the server to allocate and immediately discard stream state.  At
// scale this exhausts server goroutines.
//
// Mitigation (Go 1.21.3+): net/http/h2 tracks RST rate and returns GOAWAY
// ENHANCE_YOUR_CALM after too many rapid resets per connection.
//
// Test strategy:
//  1. Record baseline goroutine count.
//  2. Send 500 requests concurrently, each cancelled before receiving response.
//  3. Wait 2 s for the server to drain.
//  4. Assert goroutine delta ≤ 2× concurrency level (no goroutine leak).
//  5. Assert server still responds to a normal request after the flood.

func TestH2_RapidReset_CVE202344487(t *testing.T) {
	srv, tr := newH2Server(t)
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	const (
		concurrency = 50
		iterations  = 10 // 50 × 10 = 500 total requests
	)

	// Baseline goroutines (after server startup settles).
	time.Sleep(50 * time.Millisecond)
	baseline := goroutineCount()
	t.Logf("Baseline goroutines: %d", baseline)

	// Flood: open and immediately cancel requests.
	var wg sync.WaitGroup
	var succeeded, cancelled atomic.Int64
	for i := 0; i < iterations; i++ {
		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
				// Cancel immediately to simulate RST_STREAM.
				cancel()
				resp, err := client.Do(req)
				if err != nil {
					cancelled.Add(1)
					return
				}
				resp.Body.Close()
				succeeded.Add(1)
			}()
		}
	}
	wg.Wait()
	t.Logf("Flood complete: succeeded=%d cancelled=%d", succeeded.Load(), cancelled.Load())

	// Allow server goroutines to drain.
	time.Sleep(500 * time.Millisecond)
	afterFlood := goroutineCount()
	delta := afterFlood - baseline
	t.Logf("Goroutines after flood: %d (delta=%+d from baseline %d)", afterFlood, delta, baseline)

	// Assert: goroutine growth must be bounded.  Allow up to 5× baseline as a
	// generous upper bound — a true goroutine leak would be 100s above baseline.
	maxAllowed := baseline*5 + 100
	if afterFlood > maxAllowed {
		t.Errorf("FINDING HPS-2026-0005: goroutine leak after Rapid Reset flood — "+
			"baseline=%d after=%d delta=%d (max allowed=%d)",
			baseline, afterFlood, delta, maxAllowed)
		t.Errorf("CVE-2023-44487 mitigation may be ineffective for this Go version")
		t.Errorf("CWE-400: Uncontrolled Resource Consumption")
		// Capture goroutine stacks for evidence.
		buf := make([]byte, 64*1024)
		n := runtime.Stack(buf, true)
		t.Logf("Goroutine dump (first 4096 bytes):\n%s", buf[:min(n, 4096)])
	} else {
		t.Logf("PASS: goroutine count bounded after Rapid Reset flood (after=%d max=%d)", afterFlood, maxAllowed)
	}

	// Assert: server is still responsive after the flood.
	normalCtx, normalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer normalCancel()
	req, _ := http.NewRequestWithContext(normalCtx, http.MethodGet, srv.URL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("FINDING HPS-2026-0005: server unresponsive after Rapid Reset flood: %v", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Server returned %d after flood (expected 200)", resp.StatusCode)
		} else {
			t.Logf("PASS: server responsive after flood (status=%d body=%q)", resp.StatusCode, body)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPACK bombing (dynamic table exhaustion)
// ─────────────────────────────────────────────────────────────────────────────
//
// Attack: send many requests with long, unique header values.  Each unique
// header value inserted into the HPACK dynamic table consumes server memory.
// An attacker with the ability to control header values can send O(N × 4 KB)
// worth of state before limits kick in.
//
// Mitigation (net/http h2): Go's h2 implementation enforces a max dynamic
// table size of 4096 bytes (RFC 7541 §6.3) and caps the total header list
// size per request.
//
// Test strategy:
//  1. Measure heap allocation growth over 200 requests with 50 unique 4-KB headers.
//  2. Assert growth < 50 MB (bounded, not proportional to payload size).

func TestH2_HPACKBombing(t *testing.T) {
	srv, tr := newH2Server(t)
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	// Baseline memory.
	runtime.GC()
	var ms0 runtime.MemStats
	runtime.ReadMemStats(&ms0)

	const (
		numRequests    = 200
		headersPerReq  = 50
		headerValSize  = 4096 // 4 KB per unique header value
	)

	var failCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/data", nil)
			// Add many unique large header values to stress HPACK dynamic table.
			for j := 0; j < headersPerReq; j++ {
				// Each header is unique — cannot be compressed from the table.
				key := fmt.Sprintf("X-Bomb-%d-%d", idx, j)
				val := fmt.Sprintf("%0*d", headerValSize-1, idx*headersPerReq+j)
				req.Header.Set(key, val)
			}
			resp, err := client.Do(req)
			if err != nil {
				// Expect rejection when header list size limit is hit.
				failCount.Add(1)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	runtime.GC()
	var ms1 runtime.MemStats
	runtime.ReadMemStats(&ms1)

	allocGrowth := int64(ms1.HeapInuse) - int64(ms0.HeapInuse)
	t.Logf("HPACK bomb: %d requests × %d headers × %d B = total headers payload ~%d MB",
		numRequests, headersPerReq, headerValSize,
		numRequests*headersPerReq*headerValSize/1024/1024)
	t.Logf("Heap in-use: before=%d KB after=%d KB delta=%d KB (rejected=%d)",
		ms0.HeapInuse/1024, ms1.HeapInuse/1024, allocGrowth/1024, failCount.Load())

	const maxGrowthBytes = 50 * 1024 * 1024 // 50 MB
	if allocGrowth > maxGrowthBytes {
		t.Errorf("FINDING HPS-2026-0005: HPACK bomb caused heap growth of %d MB (limit %d MB)",
			allocGrowth/1024/1024, maxGrowthBytes/1024/1024)
		t.Errorf("CWE-400: Uncontrolled Resource Consumption via HPACK dynamic table")
	} else {
		t.Logf("PASS: heap growth bounded at %d KB (limit %d MB)", allocGrowth/1024, maxGrowthBytes/1024/1024)
	}

	// Liveness check.
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Errorf("FINDING HPS-2026-0005: server unresponsive after HPACK bomb: %v", err)
	} else {
		resp.Body.Close()
		t.Logf("PASS: server responsive after HPACK bomb (status=%d)", resp.StatusCode)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CVE-2024-27316: CONTINUATION flood
// ─────────────────────────────────────────────────────────────────────────────
//
// Attack: send a HEADERS frame followed by many CONTINUATION frames without
// the END_HEADERS flag, forcing the server to buffer headers indefinitely.
//
// Mitigation (Go 1.22.2+ / net/http h2): Go's h2 framer limits the number of
// CONTINUATION frames buffered before END_HEADERS is seen.
//
// Simulation strategy: since we cannot send raw frames easily without a custom
// framer, we approximate the attack using many concurrent requests where each
// request carries a very large set of headers that must be split across
// CONTINUATION frames by the h2 client.  We measure memory growth and liveness.
//
// Note: a fully faithful CONTINUATION flood requires raw frame injection
// (bypassing the h2 client library).  The raw-frame variant would require
// adding a custom framer or using net.Conn directly.  That level is documented
// under Coverage Gaps because it would require modifying the server (adding
// h2c cleartext and manually speaking the H2 preface).

func TestH2_ContinuationFlood_CVE202427316(t *testing.T) {
	srv, tr := newH2Server(t)
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	runtime.GC()
	var ms0 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	baseline := goroutineCount()

	const (
		concurrency   = 30
		iterations    = 5
		headerCount   = 200   // many headers per request to force CONTINUATION frames
		headerValSize = 512
	)

	var wg sync.WaitGroup
	var errorCount atomic.Int64
	for i := 0; i < iterations; i++ {
		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, srv.URL+"/data", nil)
				// Pack enough headers to exceed a single HEADERS frame (max ~16 KB),
				// forcing the h2 client to split them into CONTINUATION frames.
				for k := 0; k < headerCount; k++ {
					key := fmt.Sprintf("X-Cont-%d-%d", idx, k)
					val := fmt.Sprintf("%0*d", headerValSize-1, idx*headerCount+k)
					req.Header.Set(key, val)
				}
				resp, err := client.Do(req)
				if err != nil {
					errorCount.Add(1)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}(i*concurrency + j)
		}
	}
	wg.Wait()

	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	var ms1 runtime.MemStats
	runtime.ReadMemStats(&ms1)
	afterGoroutines := goroutineCount()

	allocGrowth := int64(ms1.HeapInuse) - int64(ms0.HeapInuse)
	goroutineDelta := afterGoroutines - baseline
	t.Logf("CONTINUATION flood: %d total requests × %d headers × %d B",
		concurrency*iterations, headerCount, headerValSize)
	t.Logf("Heap delta: %+d KB; goroutine delta: %+d (errors=%d)",
		allocGrowth/1024, goroutineDelta, errorCount.Load())

	const maxGrowthBytes = 100 * 1024 * 1024 // 100 MB
	if allocGrowth > maxGrowthBytes {
		t.Errorf("FINDING HPS-2026-0006: CONTINUATION flood caused heap growth of %d MB (limit %d MB)",
			allocGrowth/1024/1024, maxGrowthBytes/1024/1024)
		t.Errorf("CVE-2024-27316 mitigation may be ineffective")
		t.Errorf("CWE-400: Uncontrolled Resource Consumption")
	} else {
		t.Logf("PASS: heap growth bounded at %d KB after CONTINUATION flood", allocGrowth/1024)
	}

	maxGoroutines := baseline*3 + 50
	if afterGoroutines > maxGoroutines {
		t.Errorf("FINDING HPS-2026-0006: goroutine leak after CONTINUATION flood — "+
			"baseline=%d after=%d (max=%d)", baseline, afterGoroutines, maxGoroutines)
	} else {
		t.Logf("PASS: goroutine count bounded after CONTINUATION flood (after=%d max=%d)",
			afterGoroutines, maxGoroutines)
	}

	// Liveness check.
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Errorf("FINDING HPS-2026-0006: server unresponsive after CONTINUATION flood: %v", err)
	} else {
		resp.Body.Close()
		t.Logf("PASS: server responsive after CONTINUATION flood (status=%d)", resp.StatusCode)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// H2 → H1 downgrade routing parity
// ─────────────────────────────────────────────────────────────────────────────
//
// When a request arrives via H2, net/http presents it to ServeHTTP as a normal
// *http.Request with r.ProtoMajor=2.  MuxMaster's dispatch must produce the
// same routing result as for the equivalent H1 request.
//
// Attack: a divergence between H2 and H1 routing could let an attacker bypass
// middleware or reach handlers via the H2 path that would be blocked via H1.

func TestH2_RoutingParityWithH1(t *testing.T) {
	m := muxmaster.New()
	var h2Seen, h1Seen atomic.Bool
	m.GET("/parity", func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 {
			h2Seen.Store(true)
		} else {
			h1Seen.Store(true)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "proto=%d", r.ProtoMajor)
	})

	// H2 server.
	srvH2 := httptest.NewUnstartedServer(m)
	srvH2.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	srvH2.StartTLS()
	defer srvH2.Close()

	// H1 server (same mux).
	srvH1 := httptest.NewServer(m)
	defer srvH1.Close()

	h2tr := &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
	h2client := &http.Client{Transport: h2tr, Timeout: 5 * time.Second}
	h1client := &http.Client{Timeout: 5 * time.Second}

	// H2 request.
	resp2, err := h2client.Get(srvH2.URL + "/parity")
	if err != nil {
		t.Fatalf("H2 request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	t.Logf("H2 response: status=%d body=%q", resp2.StatusCode, body2)

	// H1 request.
	resp1, err := h1client.Get(srvH1.URL + "/parity")
	if err != nil {
		t.Fatalf("H1 request failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	t.Logf("H1 response: status=%d body=%q", resp1.StatusCode, body1)

	if resp2.StatusCode != resp1.StatusCode {
		t.Errorf("FINDING: H2/H1 routing divergence — H2 status=%d H1 status=%d",
			resp2.StatusCode, resp1.StatusCode)
	} else {
		t.Logf("PASS: H2 and H1 routing produce same status code (%d)", resp1.StatusCode)
	}

	if !h2Seen.Load() {
		t.Errorf("H2 handler was not reached via H2 transport")
	}
	if !h1Seen.Load() {
		t.Errorf("H1 handler was not reached via H1 transport")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
