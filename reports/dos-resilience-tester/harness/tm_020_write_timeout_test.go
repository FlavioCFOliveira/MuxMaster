// Package harness — TM-2026-020 (closed-task audit #285, row #110).
//
// The threat-modeler's hypothesis (S9 catalogue) was: a slow reader keeps
// the connection to a Compress-wrapped handler alive, and the server's own
// write deadline (http.Server.WriteTimeout) must still fire through the
// middleware — i.e. Compress must not swallow, mask, or otherwise prevent
// the deadline from reaching the underlying connection.
//
// DOS-2026-0062 (TestCompressSlowReadMemoryProfile, s9_dos_test.go) only
// measured HEAP under a fully-synchronous (fast) reader — it never
// exercised WriteTimeout, never used a slow reader, and never observed
// connection lifetime or goroutine cleanup. This file closes that gap
// with a real httptest server that has WriteTimeout configured, a genuine
// slow-reader TCP client (a 1-byte trickle read on a deliberately small
// receive buffer — the classic slow-read/slowloris-on-the-response-body
// shape), and deterministic, bounded assertions:
//
//   - the handler's Write() call through middleware.Compress fails within
//     WriteTimeout + a fixed margin (proving the deadline reaches the
//     compressor, not just the raw connection);
//   - runtime.NumGoroutine() returns to its pre-test baseline afterwards
//     (no goroutine leak from the stalled handler or the slow reader).
package harness

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestCompressSlowReaderWriteTimeoutFires proves that http.Server's
// WriteTimeout still fires for a response written through
// middleware.Compress when the client reads the body as a slow trickle
// (1 byte every readInterval), and that no goroutine is left behind once
// the connection is torn down.
func TestCompressSlowReaderWriteTimeoutFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WriteTimeout slow-reader test in short mode")
	}

	const writeTimeout = 300 * time.Millisecond
	// Deterministic bound: generous relative to writeTimeout so the test
	// is not flaky on a loaded CI machine, but tight enough that an
	// unbounded stall (deadline not propagating) cannot pass by accident.
	const margin = 10 * writeTimeout
	const readInterval = 50 * time.Millisecond // slow trickle: far slower than the handler produces data

	writeErrCh := make(chan error, 1)
	handlerDone := make(chan struct{})

	r := mm.New()
	r.Use(middleware.Compress(1)) // gzip BestSpeed — CPU-cheap, irrelevant to the deadline behaviour under test
	r.GET("/stream", func(w http.ResponseWriter, _ *http.Request) {
		defer close(handlerDone)
		// Incompressible payload: gzip's LZ77 window would otherwise
		// compress a repeated buffer down to almost nothing, and the
		// resulting tiny wire size might never exceed the deliberately
		// small receive buffer below — no backpressure would ever build
		// up and WriteTimeout would never have anything to interrupt.
		// Fresh random bytes on every chunk keep bytes-on-wire
		// proportional to bytes-written regardless of gzip.
		chunk := make([]byte, 64*1024)
		for {
			if _, err := rand.Read(chunk); err != nil {
				writeErrCh <- fmt.Errorf("rand.Read: %w", err)
				return
			}
			if _, err := w.Write(chunk); err != nil {
				select {
				case writeErrCh <- err:
				default:
				}
				return
			}
		}
	})

	srv := httptest.NewUnstartedServer(r)
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	defer srv.Close()

	// Baseline goroutine count BEFORE opening the slow connection, so the
	// end-of-test check is a clean before/after delta for exactly the
	// resources this test itself creates.
	runtime.GC()
	runtime.GC()
	before := runtime.NumGoroutine()

	addr := srv.Listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Deliberately tiny receive buffer: forces TCP flow control to
		// close the advertised window almost immediately once the slow
		// reader falls behind, so the handler's Write() blocks (and the
		// WriteTimeout deadline has something real to interrupt) well
		// within this test's bounded wait, on any platform/CI machine
		// regardless of default OS socket buffer auto-tuning.
		if err := tcpConn.SetReadBuffer(1024); err != nil {
			t.Logf("SetReadBuffer: %v (non-fatal, platform-dependent)", err)
		}
	}

	readerStopped := make(chan struct{})
	go func() {
		defer close(readerStopped)
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
			time.Sleep(readInterval)
		}
	}()

	reqLine := "GET /stream HTTP/1.1\r\nHost: " + addr + "\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n"
	start := time.Now()
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var writeErr error
	select {
	case writeErr = <-writeErrCh:
	case <-time.After(margin):
		t.Fatalf("TM-2026-020 REGRESSION: handler Write() through middleware.Compress did not fail within "+
			"WriteTimeout(%v)+margin(%v) — the server's write deadline is not reaching the compressor "+
			"(or the connection) for a slow reader", writeTimeout, margin)
	}
	elapsed := time.Since(start)

	t.Logf("TM-2026-020: handler Write() failed after %v (WriteTimeout=%v, margin=%v): %v",
		elapsed, writeTimeout, margin, writeErr)

	if writeErr == nil {
		t.Fatalf("TM-2026-020 REGRESSION: expected a non-nil error from Write() once the deadline fired, got nil")
	}
	if elapsed > margin {
		t.Fatalf("TM-2026-020 REGRESSION: Write() failed after %v, exceeding the bound of %v (WriteTimeout=%v)",
			elapsed, margin, writeTimeout)
	}
	if elapsed < writeTimeout/2 {
		// Sanity check the OTHER direction: if the write failed almost
		// instantly, the deadline is not what caused it (e.g. the reader
		// goroutine crashed and the connection was torn down for an
		// unrelated reason) — that would be a false pass, not a genuine
		// proof of WriteTimeout propagation.
		t.Fatalf("TM-2026-020 test invalid: Write() failed after only %v, well under half of "+
			"WriteTimeout(%v) — this failure is not attributable to the write deadline; investigate "+
			"the reader goroutine / connection setup", elapsed, writeTimeout)
	}

	// Tear down and confirm the handler goroutine actually returned (its
	// own deferred close(handlerDone) proves it stopped writing, not just
	// that the connection died).
	conn.Close()
	select {
	case <-handlerDone:
	case <-time.After(margin):
		t.Fatalf("TM-2026-020 REGRESSION: handler goroutine did not return within %v after the write "+
			"failed — the handler for a Compress-wrapped route keeps running/writing past a failed Write()", margin)
	}
	<-readerStopped

	runtime.GC()
	runtime.GC()
	time.Sleep(50 * time.Millisecond) // let the server's own per-connection goroutine unwind
	after := runtime.NumGoroutine()

	const goroutineSlack = 3 // scheduler/runtime noise tolerance, not a leak budget
	if after > before+goroutineSlack {
		t.Fatalf("TM-2026-020 REGRESSION: goroutine count did not return to baseline: before=%d after=%d "+
			"(slack=%d) — the stalled slow-reader connection leaked a goroutine", before, after, goroutineSlack)
	}
	t.Logf("TM-2026-020 PASS: WriteTimeout(%v) fired through middleware.Compress for a slow reader in %v; "+
		"goroutines before=%d after=%d (slack=%d)", writeTimeout, elapsed, before, after, goroutineSlack)
}
