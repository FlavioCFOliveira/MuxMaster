// h2_crlf_test.go — O-14 item 5: HTTP/2 header value containing CR/LF must
// be rejected per RFC 9113 §8.2.1 ("Field validity" — a field value that
// contains a CR, LF, or NUL is malformed).
//
// The deleted h2_test.go's TestH2HeaderCRLFRejected (5f804fa) was evidence-
// only AND tested the wrong layer: it called http.Transport.RoundTrip
// against http://example.com/x (a live external host, not this project's
// own server), which only proves that Go's HTTP/1 *client* rejects CRLF in
// a header before building a request line — it says nothing about how
// MuxMaster's own HTTP/2 SERVER handles a malicious header value, and
// golang.org/x/net/http2.Transport (the h2 client used everywhere else in
// this harness) ALSO validates and refuses to send such a value, so it is
// unusable to reach the server with a genuinely malformed field.
//
// This file instead drives the wire directly with http2.Framer and
// hpack.Encoder, bypassing every client-side header validator, so the
// bytes that reach the server's HPACK decoder are exactly what an
// adversarial or buggy peer could send.
package h2harness

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// rawH2Conn performs the minimal HTTP/2 client-side handshake (connection
// preface + an empty client SETTINGS frame) over a fresh TLS connection to
// srv, ACKing the server's initial SETTINGS frame along the way. It returns
// the live connection and Framer for the caller to drive directly.
func rawH2Conn(t *testing.T, addr string) (net.Conn, *http2.Framer) {
	t.Helper()

	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test-only self-signed cert
		NextProtos:         []string{http2.NextProtoTLS},
	})
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
		t.Fatalf("write preface: %v", err)
	}

	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		t.Fatalf("write client SETTINGS: %v", err)
	}

	// Drain the server's initial SETTINGS (and ACK it) plus any
	// WINDOW_UPDATE it sends before we move on to the actual test frame.
	deadline := time.Now().Add(3 * time.Second)
	ackedServerSettings := false
	for time.Now().Before(deadline) {
		f, err := fr.ReadFrame()
		if err != nil {
			t.Fatalf("handshake ReadFrame: %v", err)
		}
		switch v := f.(type) {
		case *http2.SettingsFrame:
			if !v.IsAck() {
				if err := fr.WriteSettingsAck(); err != nil {
					t.Fatalf("write SETTINGS ack: %v", err)
				}
				ackedServerSettings = true
			}
		case *http2.WindowUpdateFrame:
			// ignore — flow control bookkeeping only
		}
		if ackedServerSettings {
			return conn, fr
		}
	}
	t.Fatalf("handshake did not observe a server SETTINGS frame within deadline")
	return nil, nil
}

// encodeHeaders hpack-encodes the given pseudo + regular header fields with
// NO validation of any kind — hpack.Encoder only performs Huffman/literal
// byte encoding, it never inspects field-value grammar. This is exactly
// what makes this test able to reach the server's own validation logic.
func encodeHeaders(t *testing.T, fields []hpack.HeaderField) []byte {
	t.Helper()
	var buf bytes.Buffer
	henc := hpack.NewEncoder(&buf)
	for _, f := range fields {
		if err := henc.WriteField(f); err != nil {
			t.Fatalf("hpack WriteField(%q=%q): %v", f.Name, f.Value, err)
		}
	}
	return buf.Bytes()
}

func TestH2_HeaderValueCRLF_Rejected(t *testing.T) {
	m := muxmaster.New()
	var handlerHit bool
	var observedValue string
	m.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		handlerHit = true
		observedValue = r.Header.Get("X-Evil")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	srv := httptest.NewUnstartedServer(m)
	srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	addr := srv.Listener.Addr().String()
	conn, fr := rawH2Conn(t, addr)
	defer conn.Close()

	host := addr
	block := encodeHeaders(t, []hpack.HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":path", Value: "/x"},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: host},
		// The malformed field: RFC 9113 §8.2.1 forbids CR, LF, and NUL in a
		// field value. hpack itself has no opinion on this — it's the
		// server's meta-frame assembly (httpguts.ValidHeaderFieldValue,
		// per net/http/internal/http2/frame.go) that must catch it.
		{Name: "x-evil", Value: "a\r\nSet-Cookie: evil=1"},
	})

	if err := fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      1,
		BlockFragment: block,
		EndStream:     true,
		EndHeaders:    true,
	}); err != nil {
		t.Fatalf("WriteHeaders: %v", err)
	}

	// Read frames until we see how the server responded to stream 1.
	var gotStreamError, gotConnError bool
	var streamErrCode, connErrCode http2.ErrCode
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f, err := fr.ReadFrame()
		if err != nil {
			// Connection torn down without an explicit GOAWAY frame (e.g.
			// TLS close) is also an acceptable rejection outcome — record
			// and stop.
			t.Logf("ReadFrame terminated the loop: %v", err)
			break
		}
		switch v := f.(type) {
		case *http2.RSTStreamFrame:
			if v.StreamID == 1 {
				gotStreamError = true
				streamErrCode = v.ErrCode
			}
		case *http2.GoAwayFrame:
			gotConnError = true
			connErrCode = v.ErrCode
		case *http2.HeadersFrame:
			t.Errorf("VULNERABLE HPS-2026-O14-5: server sent a HEADERS response frame for a "+
				"malformed (CRLF-containing) request header instead of rejecting it: %+v", v)
		case *http2.DataFrame:
			// A 200 response body would show up here — also a failure mode.
			t.Errorf("VULNERABLE HPS-2026-O14-5: server sent response DATA for a malformed request: %q", v.Data())
		}
		if gotStreamError || gotConnError {
			break
		}
	}

	t.Logf("gotStreamError=%v (code=%v) gotConnError=%v (code=%v) handlerHit=%v observedXEvil=%q",
		gotStreamError, streamErrCode, gotConnError, connErrCode, handlerHit, observedValue)

	if !gotStreamError && !gotConnError {
		t.Errorf("VULNERABLE HPS-2026-O14-5: neither RST_STREAM nor GOAWAY observed for a CRLF-containing " +
			"header value — server may have silently accepted a malformed field (RFC 9113 §8.2.1 violation)")
	}
	if handlerHit {
		t.Errorf("VULNERABLE HPS-2026-O14-5: MuxMaster's handler ran despite the malformed header " +
			"(the h2 layer should reject the frame before it ever reaches net/http's Handler)")
		t.Errorf("Observed X-Evil header value in handler: %q", observedValue)
	}
	if gotStreamError && streamErrCode != http2.ErrCodeProtocol {
		t.Logf("INFO: RST_STREAM error code was %v, not the commonly expected PROTOCOL_ERROR — "+
			"still a rejection, just documenting the exact code", streamErrCode)
	}

	// If only a stream-level error was raised (not a full GOAWAY), the
	// connection should still be usable for a subsequent, well-formed
	// request on a new stream — confirming the server isolates the bad
	// stream instead of degrading the whole connection.
	if gotStreamError && !gotConnError {
		block2 := encodeHeaders(t, []hpack.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":path", Value: "/x"},
			{Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: host},
		})
		if err := fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      3,
			BlockFragment: block2,
			EndStream:     true,
			EndHeaders:    true,
		}); err != nil {
			t.Fatalf("WriteHeaders (follow-up stream 3): %v", err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		gotOK := false
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				break
			}
			if hf, ok := f.(*http2.HeadersFrame); ok && hf.StreamID == 3 {
				gotOK = true
				break
			}
		}
		if !gotOK {
			t.Errorf("server did not answer a well-formed follow-up request on stream 3 after " +
				"rejecting the malformed stream 1 — connection may have been left unusable")
		} else {
			t.Logf("PASS: connection remained usable for a clean request on stream 3 after the " +
				"malformed stream 1 was rejected in isolation")
		}
	}
}
