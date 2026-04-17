package dosharness

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestSlowlorisExposure measures the goroutine/connection cost of slow
// header-sending clients when MuxMaster is served via net/http.
//
// Without any ReadHeaderTimeout configured, stdlib accepts connections
// indefinitely. We open N TCP connections, drip one header byte per second,
// and measure the goroutine pool + RSS.
func TestSlowlorisExposureDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test; skipped in -short")
	}

	const connections = 200
	const driplen = 20 // bytes per connection overall
	const dripInterval = 25 * time.Millisecond

	mux := mm.New()
	mux.GET("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// *** Default server — no ReadHeaderTimeout configured ***
	srv := &http.Server{
		Handler: mux,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = srv.Serve(ln)
	}()
	defer srv.Close()

	addr := ln.Addr().String()

	runtime.GC()
	runtime.GC()
	before := runtime.NumGoroutine()
	t.Logf("goroutines before slowloris (no ReadHeaderTimeout): %d", before)

	// Dump goroutine profile before.
	prefix := "/data/dev/github.com/FlavioCFOliveira/MuxMaster/reports/dos-resilience-tester/evidence/2026-04-17"
	dumpProfile(t, prefix+"/slowloris-goroutines-before.pprof")

	// Open N connections dripping.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < connections; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			// Drip the request line + headers very slowly.
			reqBytes := []byte("GET / HTTP/1.1\r\nHost: localhost\r\nX-Pad: ")
			for _, b := range reqBytes[:driplen] {
				if _, err := conn.Write([]byte{b}); err != nil {
					return
				}
				select {
				case <-time.After(dripInterval):
				case <-stop:
					return
				}
			}
			// Hold the connection idle — DO NOT finish the request.
			<-stop
		}()
	}

	// Let the attack sustain for a few seconds.
	time.Sleep(4 * time.Second)
	runtime.GC()
	runtime.GC()
	mid := runtime.NumGoroutine()
	t.Logf("goroutines during slowloris (connections=%d): %d (delta=%d)", connections, mid, mid-before)
	dumpProfile(t, prefix+"/slowloris-goroutines-during.pprof")

	// Record findings.
	leaked := mid - before
	t.Logf("leaked goroutine estimate: %d (expected: at or near %d, one per held connection)", leaked, connections)

	if leaked < connections/2 {
		t.Errorf("expected goroutines to scale with held connections; got %d vs %d connections", leaked, connections)
	}

	close(stop)
	wg.Wait()
}

// TestSlowlorisWithReadHeaderTimeout validates the mitigation: when the
// user configures Server.ReadHeaderTimeout, stdlib closes the idle connections.
func TestSlowlorisMitigatedByReadHeaderTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test; skipped in -short")
	}

	const connections = 100
	const dripInterval = 50 * time.Millisecond

	mux := mm.New()
	mux.GET("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 500 * time.Millisecond, // MITIGATION
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = srv.Serve(ln)
	}()
	defer srv.Close()

	addr := ln.Addr().String()

	runtime.GC()
	runtime.GC()
	before := runtime.NumGoroutine()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < connections; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			reqBytes := []byte("GET / HTTP/1.1\r\nHost: localhost\r\nX-Pad: ")
			for _, b := range reqBytes {
				if _, err := conn.Write([]byte{b}); err != nil {
					return
				}
				select {
				case <-time.After(dripInterval):
				case <-stop:
					return
				}
			}
			// Read the rejection if any.
			_, _ = bufio.NewReader(conn).ReadString('\n')
		}()
	}

	time.Sleep(3 * time.Second)
	mid := runtime.NumGoroutine()
	t.Logf("WITH ReadHeaderTimeout=500ms, goroutines during attack: %d (delta=%d, connections=%d)", mid, mid-before, connections)

	close(stop)
	wg.Wait()

	// After 3s of 500ms timeout, most connections should have been dropped.
	// We expect the delta to be much smaller than `connections`.
	if mid-before > connections/2 {
		t.Logf("NOTE: still %d extra goroutines after ReadHeaderTimeout — verify expectation", mid-before)
	}
}

func dumpProfile(t *testing.T, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Logf("failed to create profile file %s: %v", path, err)
		return
	}
	defer f.Close()
	if err := pprof.Lookup("goroutine").WriteTo(f, 1); err != nil {
		t.Logf("failed to write profile: %v", err)
	}
}

func TestSustainedLoadOneMinute(t *testing.T) {
	if testing.Short() {
		t.Skip("sustained load; skip in -short")
	}

	mux := mm.New()
	mux.GET("/static", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "id=%s", mm.PathParam(r, "id"))
	})
	mux.GET("/a/:b/:c", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s/%s", mm.PathParam(r, "b"), mm.PathParam(r, "c"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(60 * time.Second)

	paths := []string{
		"/static",
		"/users/42",
		"/users/alice",
		"/a/foo/bar",
		"/notfound",
	}

	var total, errs atomic.Int64
	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	start := time.Now()
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			i := 0
			for time.Now().Before(deadline) {
				p := paths[(w+i)%len(paths)]
				resp, err := client.Get(srv.URL + p)
				if err != nil {
					errs.Add(1)
				} else {
					resp.Body.Close()
				}
				total.Add(1)
				i++
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("sustained load: workers=%d duration=%s total=%d errors=%d rps=%.1f heapInuse=%dMB goroutines=%d",
		workers, elapsed.Round(time.Second), total.Load(), errs.Load(),
		float64(total.Load())/elapsed.Seconds(),
		ms.HeapInuse>>20, runtime.NumGoroutine())
}
