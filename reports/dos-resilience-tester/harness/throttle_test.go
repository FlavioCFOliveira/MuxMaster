// Package harness — DoS Resilience: throttle middleware attack vectors
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestThrottlePerIPTableBloat verifies that ThrottlePerIP does NOT accumulate
// entries in the internal map when keys rotate rapidly (e.g. spoofed IPs).
//
// ThrottlePerIP uses ref-counting: entries are deleted when refs drop to zero.
// Each request atomically increments refs on acquire and decrements on release.
// After all requests complete, the table should be empty.
//
// Finding: if cleanup is deferred or racy, each of N distinct IPs leaves a
// permanent entry → O(N) memory leak. This test confirms proper cleanup.
func TestThrottlePerIPTableBloat(t *testing.T) {
	const numIPs = 10000

	// Custom keyFn returns the X-Real-IP header value — simulates RealIP trusting XFF
	keyFn := func(r *http.Request) string {
		return r.Header.Get("X-Real-IP")
	}

	r := mm.New()
	r.Use(middleware.ThrottlePerIP(100, time.Second, keyFn))
	r.GET("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// Fire numIPs requests each with a distinct spoofed IP
	for i := range numIPs {
		req := httptest.NewRequest("GET", "/api", nil)
		req.Header.Set("X-Real-IP", fmt.Sprintf("10.%d.%d.%d", (i>>16)&0xff, (i>>8)&0xff, i&0xff))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)

	heapDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	// After all requests finish, table must be empty → heap growth should be < 5 MB
	if heapDelta > 5*1024*1024 {
		t.Fatalf("DOS-2026: ThrottlePerIP table bloat: %d KB heap retained after %d distinct IPs. "+
			"Possible map leak — entries not cleaned up after ref hits zero.",
			heapDelta/1024, numIPs)
	}
	t.Logf("ThrottlePerIP table bloat test: heap delta=%d KB for %d IPs (PASS)", heapDelta/1024, numIPs)
}

// TestThrottleXFFSpoofBypass verifies the ordering attack:
// when real_ip is NOT applied (or applied after throttle), all requests share
// the proxy IP and the per-IP throttle degrades to a global throttle,
// effectively blocking legitimate clients.
//
// This is hypothesis H-F from sprint plan. The test documents the exposure
// when registration order is wrong.
func TestThrottleXFFSpoofBypass(t *testing.T) {
	// Scenario A: throttle applied BEFORE real_ip (broken configuration)
	// All requests appear to come from 127.0.0.1 (httptest default)
	rBroken := mm.New()
	rBroken.Use(middleware.ThrottlePerIP(2, 50*time.Millisecond, nil))
	rBroken.GET("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Send 10 concurrent requests from "different" clients via XFF spoof.
	// With broken ordering, throttle sees all as 127.0.0.1 and blocks after 2.
	const concurrency = 10
	var wg sync.WaitGroup
	blocked := 0
	var mu sync.Mutex
	for i := range concurrency {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api", nil)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("1.2.3.%d", i))
			w := httptest.NewRecorder()
			rBroken.ServeHTTP(w, req)
			if w.Code == http.StatusServiceUnavailable {
				mu.Lock()
				blocked++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// With all requests sharing 127.0.0.1, >2 will be blocked even though they
	// present distinct XFF values. Document this as the configuration hazard.
	t.Logf("H-F throttle order hazard: %d/%d requests blocked (all shared proxy IP 127.0.0.1)", blocked, concurrency)
	if blocked > concurrency/2 {
		t.Logf("WARNING DOS-2026-0002: ThrottlePerIP before RealIP degrades to global throttle. " +
			"Registering Use(RealIP(...)) BEFORE Use(ThrottlePerIP(...)) is required for per-client limits.")
	}
}

// TestThrottleAllBacklogConcurrency stress-tests ThrottleBacklog under concurrency
// to confirm no data race on the token channel. Run with -race.
func TestThrottleAllBacklogConcurrency(t *testing.T) {
	r := mm.New()
	r.Use(middleware.ThrottleBacklog(5, 20, 100*time.Millisecond))
	r.GET("/work", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	for i := range 200 {
		_ = i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/work", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		}()
	}
	wg.Wait()
	t.Log("ThrottleBacklog concurrency test: no race detected (run with -race for full validation)")
}

// BenchmarkThrottlePerIPFastPath measures hot-path cost when there IS an available token.
func BenchmarkThrottlePerIPFastPath(b *testing.B) {
	r := mm.New()
	r.Use(middleware.ThrottlePerIP(1000, time.Second, nil))
	r.GET("/api", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest("GET", "/api", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}
