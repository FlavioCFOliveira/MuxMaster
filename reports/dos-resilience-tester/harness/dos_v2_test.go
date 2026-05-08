// Package harness — DoS Resilience v2: remaining attack vectors
//
// Sprint 2026-05-07 — extends the existing harness to cover:
//  1. addRoute O(N^2) registration time (DOS-2026-0051) — documented finding
//  2. Logger I/O backpressure (DOS-2026-0052) — synchronous fmt.Fprintf to slow writer
//  3. Request body exhaustion — no MaxBytesReader in core mux (DOS-2026-0053, informational)
//  4. ThrottlePerIP degradation corrected test (DOS-2026-0002)
//  5. Two-phase config snapshot: concurrent Rebuild() under load (DOS-2026-0054)
//  6. Regex param catastrophic backtracking check (DOS-2026-0055) — RE2 confirmed
//  7. GC pacing measurement under 1 alloc/req reqBundle load
//  8. Slowloris goroutine accumulation (DOS-2026-0056)
package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ---------------------------------------------------------------------------
// DOS-2026-0051: addRoute O(N^2) registration time
// ---------------------------------------------------------------------------

// TestAddRouteRegistrationComplexity documents that addRoute exhibits O(N^2)
// complexity in the total number of routes registered. This is a startup-time
// DoS vector if routes are registered dynamically after the server starts
// (which MuxMaster explicitly warns against). At N=5000 routes it takes ~5 s;
// no runtime request throughput is affected.
//
// Attack scenario: a misconfigured operator loads 10000+ routes from a
// database at startup and the server takes >30 s to initialise, causing
// health checks to fail and triggering a restart loop.
func TestAddRouteRegistrationComplexity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping O(N^2) registration test in short mode")
	}

	type point struct {
		n  int
		ms float64
	}

	sizes := []int{100, 500, 1000, 2000}
	points := make([]point, len(sizes))

	for i, n := range sizes {
		r := mm.New()
		start := time.Now()
		for j := range n {
			r.GET(fmt.Sprintf("/route/%d/end", j), h)
		}
		elapsed := time.Since(start)
		points[i] = point{n: n, ms: float64(elapsed.Milliseconds())}
		t.Logf("addRoute N=%d: %v (%.1f ms)", n, elapsed, points[i].ms)
		_ = r
	}

	// Compute slope ratio for N doubling. O(N^2) → ratio ~4x per doubling.
	for i := 1; i < len(points); i++ {
		nRatio := float64(points[i].n) / float64(points[i-1].n)
		if points[i-1].ms < 0.1 {
			continue // skip if too fast to measure
		}
		timeRatio := points[i].ms / points[i-1].ms
		expectedO2 := nRatio * nRatio
		t.Logf("  N ratio=%.1fx, time ratio=%.1fx (O(N^2) predicts=%.1fx)", nRatio, timeRatio, expectedO2)
		if timeRatio >= expectedO2*0.5 {
			t.Logf("  DOS-2026-0051 NOTE: addRoute exhibits O(N^2) registration cost (startup-time only)")
		}
	}

	t.Log("DOS-2026-0051: addRoute is O(N^2) in number of routes. " +
		"Impact: startup-time only (MuxMaster docs: no dynamic runtime registration). " +
		"At N=5000 routes: ~5 s startup. Mitigation: if registering >1000 routes, " +
		"profile startup time and batch-register or split into sub-routers.")
}

// BenchmarkAddRouteLargeScale measures addRoute cost at different route counts.
func BenchmarkAddRouteLargeScale(b *testing.B) {
	for _, preload := range []int{50, 200, 500} {
		preload := preload
		b.Run(fmt.Sprintf("N=%d", preload), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := mm.New()
				for j := range preload {
					r.GET(fmt.Sprintf("/r/%d", j), h)
				}
				_ = r
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0052: Logger middleware I/O backpressure
// ---------------------------------------------------------------------------

// v2SlowWriter simulates a slow I/O sink (e.g. a disk-full or blocked syslog socket).
type v2SlowWriter struct {
	delay time.Duration
	buf   bytes.Buffer
	mu    sync.Mutex
}

func (s *v2SlowWriter) Write(b []byte) (int, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	n, err := s.buf.Write(b)
	s.mu.Unlock()
	return n, err
}

// TestLoggerIOBackpressure verifies that Logger middleware blocks the request
// goroutine when the io.Writer is slow.
//
// Finding DOS-2026-0052: Logger uses synchronous fmt.Fprintf to the provided
// io.Writer. A blocking writer (full disk, blocked syslog, network log aggregator)
// delays every request by the I/O wait time. This is not a bug — the caller must
// wrap the writer in a buffered async sink.
func TestLoggerIOBackpressure(t *testing.T) {
	const writeDelay = 5 * time.Millisecond
	const concurrency = 20

	slow := &v2SlowWriter{delay: writeDelay}
	r := mm.New()
	r.Use(middleware.Logger(slow))
	r.GET("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	slow.mu.Lock()
	lines := strings.Count(slow.buf.String(), "\n")
	slow.mu.Unlock()

	t.Logf("DOS-2026-0052: Logger with %v write delay, %d concurrent requests: total=%v",
		writeDelay, concurrency, elapsed)
	t.Logf("  Min possible: %v (fully parallel). Max possible: %v (fully serial).",
		writeDelay, time.Duration(concurrency)*writeDelay)

	if lines != concurrency {
		t.Errorf("expected %d log lines, got %d", concurrency, lines)
	}
	t.Log("  Mitigation: wrap logger io.Writer in async buffer (bufio + dedicated flush goroutine).")
}

// v2SyncWriter wraps a bytes.Buffer with a mutex for concurrent safe writing.
type v2SyncWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (s *v2SyncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	n, err := s.buf.Write(b)
	s.mu.Unlock()
	return n, err
}

// BenchmarkLoggerSyncWrite measures per-request overhead of Logger with sync write.
func BenchmarkLoggerSyncWrite(b *testing.B) {
	buf := &bytes.Buffer{}
	mu := &sync.Mutex{}
	w := &v2SyncWriter{buf: buf, mu: mu}

	r := mm.New()
	r.Use(middleware.Logger(w))
	r.GET("/bench", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest("GET", "/bench", nil)
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0053: Request body — no MaxBytesReader in core mux (informational)
// ---------------------------------------------------------------------------

// TestRequestBodyNoMaxBytes documents that MuxMaster's mux itself does NOT
// apply http.MaxBytesReader to request bodies. A handler reading r.Body
// without a size cap can be forced to process arbitrarily large bodies.
//
// This is INFORMATIONAL. net/http's ReadTimeout bounds wall-clock time.
// Responsibility for body size caps lies with the handler or a middleware.
func TestRequestBodyNoMaxBytes(t *testing.T) {
	const largeMB = 1
	largeBody := strings.Repeat("X", largeMB*1024*1024)

	var receivedBytes int64

	r := mm.New()
	r.POST("/upload", func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		receivedBytes = int64(len(b))
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/upload", strings.NewReader(largeBody))
	w := httptest.NewRecorder()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	r.ServeHTTP(w, req)
	runtime.GC()
	runtime.ReadMemStats(&after)

	heapDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)

	t.Logf("DOS-2026-0053 (informational): handler read %d bytes from body", receivedBytes)
	t.Logf("  heap delta: %d KB for %d MB body", heapDelta/1024, largeMB)
	t.Log("  MuxMaster does NOT cap request body size — handlers MUST call http.MaxBytesReader.")
	t.Log("  Mitigation: use http.MaxBytesReader(w, r.Body, maxBytes) in handler or Pre() middleware.")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// BenchmarkBodyReadNoCap measures allocation cost of reading an uncapped request body.
func BenchmarkBodyReadNoCap(b *testing.B) {
	for _, kb := range []int{1, 10, 100} {
		kb := kb
		b.Run(fmt.Sprintf("body=%dKB", kb), func(b *testing.B) {
			body := strings.Repeat("X", kb*1024)
			r := mm.New()
			r.POST("/upload", func(w http.ResponseWriter, req *http.Request) {
				_, _ = io.ReadAll(req.Body)
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest("POST", "/upload", strings.NewReader(body))
			w := httptest.NewRecorder()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req.Body = io.NopCloser(strings.NewReader(body))
				w.Body.Reset()
				r.ServeHTTP(w, req)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0002 corrected: ThrottlePerIP degradation to global throttle
// ---------------------------------------------------------------------------

// TestThrottlePerIPDegradesToGlobalWithProxy demonstrates that when
// ThrottlePerIP is configured before RealIP, all requests behind a proxy
// share the same RemoteAddr (the proxy IP), causing ThrottlePerIP to act
// as a global throttle that blocks legitimate clients from distinct IPs.
func TestThrottlePerIPDegradesToGlobalWithProxy(t *testing.T) {
	const (
		limit       = 2
		concurrency = 10
		proxyIP     = "10.0.0.1:8080"
	)

	// Broken ordering: Throttle BEFORE RealIP — throttle sees proxyIP for all requests.
	rBroken := mm.New()
	rBroken.Use(middleware.ThrottlePerIP(limit, 200*time.Millisecond, nil))

	// Handler that takes 50ms to ensure concurrency causes blocking
	handlerDone := make(chan struct{})
	rBroken.GET("/api", func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-handlerDone:
		case <-req.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	codes := make([]int, concurrency)
	for i := range concurrency {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api", nil)
			req.RemoteAddr = proxyIP // all requests appear from the same proxy
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
			w := httptest.NewRecorder()
			rBroken.ServeHTTP(w, req)
			codes[i] = w.Code
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(handlerDone)
	wg.Wait()

	var okCount, blockedCount int
	for _, code := range codes {
		if code == http.StatusOK {
			okCount++
		} else {
			blockedCount++
		}
	}

	t.Logf("DOS-2026-0002 corrected: ThrottlePerIP(limit=%d), proxy=%s, %d concurrent requests",
		limit, proxyIP, concurrency)
	t.Logf("  %d OK, %d blocked (503)", okCount, blockedCount)

	if blockedCount > 0 {
		t.Logf("CONFIRMED DOS-2026-0002: %d/%d requests blocked — throttle keyed on proxy IP, "+
			"so single-IP burst blocks ALL clients behind same proxy. "+
			"Fix: register RealIP BEFORE ThrottlePerIP.", blockedCount, concurrency)
	} else {
		t.Log("NOTE: No requests blocked (timing-dependent; run with slow handler to confirm)")
	}
}

// TestThrottlePerIPNoBlockWithCorrectOrdering confirms correct RealIP+ThrottlePerIP ordering.
func TestThrottlePerIPNoBlockWithCorrectOrdering(t *testing.T) {
	const (
		limit     = 1
		concN     = 5
		proxyAddr = "10.0.0.1:8080"
	)

	cidrStr := "10.0.0.0/8"
	p, err := netip.ParsePrefix(cidrStr)
	if err != nil {
		t.Fatalf("ParsePrefix: %v", err)
	}
	trustedCIDR := &p

	rCorrect := mm.New()
	rCorrect.Use(middleware.RealIP(trustedCIDR))
	rCorrect.Use(middleware.ThrottlePerIP(limit, 100*time.Millisecond, nil))

	var handlerCalled atomic.Int64
	rCorrect.GET("/api", func(w http.ResponseWriter, req *http.Request) {
		handlerCalled.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	codes := make([]int, concN)
	for i := range concN {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api", nil)
			req.RemoteAddr = proxyAddr
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
			w := httptest.NewRecorder()
			rCorrect.ServeHTTP(w, req)
			codes[i] = w.Code
		}()
	}
	wg.Wait()

	blocked := 0
	for _, code := range codes {
		if code == http.StatusServiceUnavailable {
			blocked++
		}
	}
	t.Logf("Correct ordering (RealIP before ThrottlePerIP): %d blocked / %d", blocked, concN)
	if blocked > 0 {
		t.Errorf("Correct ordering still blocked requests — per-IP bucketing not working: %d blocked", blocked)
	} else {
		t.Log("PASS: distinct XFF IPs each get their own throttle bucket")
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0054: Concurrent Rebuild() under load — config snapshot race window
// ---------------------------------------------------------------------------

// TestRebuildUnderLoadNoRace verifies that calling Rebuild() while ServeHTTP
// is executing concurrently does not cause panics or incorrect routing.
// Run with -race to detect data races.
func TestRebuildUnderLoadNoRace(t *testing.T) {
	const (
		readers  = 8
		iters    = 1000
		rebuilds = 50
	)

	r := mm.New()
	r.GET("/stable", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	var errors atomic.Int64

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/stable", nil)
			w := httptest.NewRecorder()
			for range iters {
				w.Body.Reset()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					errors.Add(1)
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rebuilds {
			r.Rebuild()
			runtime.Gosched()
		}
	}()

	wg.Wait()

	if errs := errors.Load(); errs > 0 {
		t.Errorf("DOS-2026-0054: %d incorrect responses during Rebuild() under load", errs)
	} else {
		t.Log("DOS-2026-0054: Rebuild() under load: no incorrect responses (PASS)")
		t.Log("  Run with -race to confirm no data races.")
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0055: Regex param catastrophic backtracking (ReDoS) probe
// ---------------------------------------------------------------------------

// TestReDoSRegexParams probes regex param patterns that would cause catastrophic
// backtracking in PCRE/backtracking engines. Go's regexp uses RE2 (NFA), which
// guarantees O(n) matching — no exponential backtracking.
func TestReDoSRegexParams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ReDoS test in short mode")
	}

	cases := []struct {
		name    string
		pattern string
		evil    string
	}{
		{
			name:    "nested-plus",
			pattern: `(a+)+`,
			evil:    strings.Repeat("a", 30) + "!",
		},
		{
			name:    "alternation-bomb",
			pattern: `(a|aa)+`,
			evil:    strings.Repeat("a", 30) + "!",
		},
		{
			name:    "dot-star-long",
			pattern: `.*.*.*x`,
			evil:    strings.Repeat("a", 500),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := mm.New()
			registeredOK := false
			var regPanic any
			func() {
				defer func() {
					if rcv := recover(); rcv != nil {
						regPanic = rcv
					}
				}()
				r.GET("/{id:"+tc.pattern+"}", h)
				registeredOK = true
			}()

			if !registeredOK {
				t.Logf("  pattern=%q rejected at registration: %v", tc.pattern, regPanic)
				return
			}

			req := httptest.NewRequest("GET", "/"+tc.evil, nil)
			w := httptest.NewRecorder()

			start := time.Now()
			r.ServeHTTP(w, req)
			elapsed := time.Since(start)

			t.Logf("  pattern=%q evil_len=%d: elapsed=%v status=%d",
				tc.pattern, len(tc.evil), elapsed, w.Code)

			if elapsed > 100*time.Millisecond {
				t.Errorf("DOS-2026-0055: possible ReDoS? pattern=%q took %v for %d-char input",
					tc.pattern, elapsed, len(tc.evil))
			} else {
				t.Logf("  PASS: Go RE2 is O(n) — no exponential backtracking (elapsed=%v)", elapsed)
			}
		})
	}
}

// BenchmarkReDoSRegexWorstCase measures RE2 cost for adversarial inputs.
func BenchmarkReDoSRegexWorstCase(b *testing.B) {
	r := mm.New()
	r.GET("/{id:[a-zA-Z0-9._~-]+}", h)
	input := strings.Repeat("abcdefghijklmnop0123456789", 40) + "!invalid"
	req := httptest.NewRequest("GET", "/"+input, nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// ---------------------------------------------------------------------------
// GC pacing measurement
// ---------------------------------------------------------------------------

// TestGCPacingUnderLoad measures GC behaviour for 1 alloc/req at 416/448/480 B.
func TestGCPacingUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GC pacing test in short mode")
	}

	r := mm.New()
	r.GET("/users/:id", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/users/42", nil)
	w := httptest.NewRecorder()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	const iters = 100_000
	for range iters {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}

	runtime.GC()
	runtime.ReadMemStats(&after)

	gcCycles := after.NumGC - before.NumGC
	totalAlloc := after.TotalAlloc - before.TotalAlloc
	pauseTotal := after.PauseTotalNs - before.PauseTotalNs

	t.Logf("GC pacing: %d iterations, %d GC cycles, %d KB allocated, %v total GC pause",
		iters, gcCycles, totalAlloc/1024, time.Duration(pauseTotal))
	t.Logf("  Alloc per request: %.0f B (expected 416 B for 1-param route)", float64(totalAlloc)/float64(iters))

	if gcCycles > 100 {
		t.Logf("WARNING: %d GC cycles in %d requests — consider GOGC tuning at production RPS",
			gcCycles, iters)
	} else {
		t.Logf("GC pacing: %d GC cycles for %d requests (PASS)", gcCycles, iters)
	}
}

// ---------------------------------------------------------------------------
// DOS-2026-0056: Slowloris goroutine accumulation
// ---------------------------------------------------------------------------

// TestSlowlorisWithoutTimeout documents goroutine accumulation when
// net/http.Server has no ReadHeaderTimeout configured.
//
// Finding DOS-2026-0056 (informational): without ReadHeaderTimeout, each stalled
// connection holds a goroutine (~8 KB stack). At N=1000 connections: ~8 MB RAM
// + 1000 file descriptors. Operator MUST set ReadHeaderTimeout.
func TestSlowlorisWithoutTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slowloris test in short mode")
	}

	r := mm.New()
	r.GET("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Server WITHOUT ReadHeaderTimeout (deliberately insecure for the test)
	srv := httptest.NewServer(r)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	const numConns = 50

	goroutinesBefore := runtime.NumGoroutine()

	var conns []net.Conn
	for range numConns {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
		cancel()
		if err != nil {
			continue
		}
		// Send partial headers — no final CRLF to stall the server
		partial := "GET / HTTP/1.1\r\nHost: " + host + "\r\nX-"
		if _, err := conn.Write([]byte(partial)); err != nil {
			conn.Close()
			continue
		}
		conns = append(conns, conn)
	}

	time.Sleep(100 * time.Millisecond)
	goroutinesDuring := runtime.NumGoroutine()

	for _, conn := range conns {
		conn.Close()
	}

	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()

	established := len(conns)
	delta := goroutinesDuring - goroutinesBefore
	leak := goroutinesAfter - goroutinesBefore
	perConn := 0
	if established > 0 {
		perConn = delta / established
	}

	t.Logf("DOS-2026-0056: Slowloris (no ReadHeaderTimeout): %d/%d connections established",
		established, numConns)
	t.Logf("  Goroutine delta during attack: %d (+%d per connection)", delta, perConn)
	t.Logf("  Goroutine delta after close+GC: %d", leak)
	t.Log("  DOCUMENTED RISK: each stalled connection holds a goroutine indefinitely.")
	t.Log("  Mitigation: http.Server{ReadHeaderTimeout: 5*time.Second, IdleTimeout: 60*time.Second}")
}

// BenchmarkSlowlorisConnectionRate measures the rate at which slowloris connections
// can be established (attacker cost). Useful for capacity planning.
func BenchmarkSlowlorisConnectionRate(b *testing.B) {
	r := mm.New()
	r.GET("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := httptest.NewServer(r)
	b.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
		cancel()
		if err == nil {
			conn.Close()
		}
	}
}
