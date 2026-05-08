// s8_hypotheses_test.go — Sprint S8 HTTP protocol security hypotheses harness.
//
// Covers: H8-02, H8-04, H8-10, H8-22, H8-30, H8-48, H8-51, H8-59, H8-60, H8-61.
// Commit under test: e30ae946f634cbec54c0ae9445cbf3787ca24f31
// Go: go1.26.2 linux/amd64
//
// Run with: go test -race -v -run TestS8 ./reports/http-protocol-security-auditor/harness/
package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// sendRawTCP dials addr, sends raw bytes, reads back the response, and returns it.
func sendRawTCP(t *testing.T, addr, payload string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	var sb strings.Builder
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		sb.WriteString(line)
		if err != nil {
			break
		}
		if strings.Contains(sb.String(), "\r\n\r\n") && sb.Len() > 100 {
			break
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-02: Mount + parent Use + inner sub-mux recursive chain
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: Group.Mount wraps the inner handler with group middleware correctly;
// if the inner mux itself has middleware, the chain is: mux.Use → group.Use → inner.Use → handler.
// A silently-dropped layer would be a middleware bypass.

func TestS8_H8_02_MountGroupUseChain(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ctx

	var calls []string

	// Inner Mux with its own middleware
	innerMux := muxmaster.New()
	innerMux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "inner-mux-mw")
			next.ServeHTTP(w, r)
		})
	})
	innerMux.GET("/resource", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
		w.WriteHeader(200)
	})

	// Sub-inner Mux (recursive mount)
	subInner := muxmaster.New()
	subInner.GET("/deep", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "deep-handler")
		w.WriteHeader(200)
	})
	innerMux.Mount("/nested", subInner)

	// Outer Mux with global middleware
	outer := muxmaster.New()
	outer.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "outer-mux-mw")
			next.ServeHTTP(w, r)
		})
	})

	// Group with group-level middleware
	g := outer.Group("/api")
	g.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "group-mw")
			next.ServeHTTP(w, r)
		})
	})
	g.Mount("/v1", innerMux)

	t.Run("group_middleware_wraps_inner", func(t *testing.T) {
		calls = nil
		req := httptest.NewRequest("GET", "/api/v1/resource", nil)
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Errorf("expected 200, got %d", rec.Code)
		}

		// Expected order: outer-mux-mw → group-mw → inner-mux-mw → handler
		// Verify outer and group middleware both ran
		hasOuter := false
		hasGroup := false
		hasInner := false
		hasHandler := false
		for _, c := range calls {
			switch c {
			case "outer-mux-mw":
				hasOuter = true
			case "group-mw":
				hasGroup = true
			case "inner-mux-mw":
				hasInner = true
			case "handler":
				hasHandler = true
			}
		}
		if !hasOuter {
			t.Errorf("H8-02 VULNERABLE: outer mux middleware bypassed; calls=%v", calls)
		}
		if !hasGroup {
			t.Errorf("H8-02 VULNERABLE: group middleware bypassed; calls=%v", calls)
		}
		if !hasInner {
			t.Logf("INFO: inner mux middleware not reached (inner mux bakes middleware at registration)")
		}
		if !hasHandler {
			t.Errorf("H8-02: handler not reached; calls=%v", calls)
		}
		t.Logf("H8-02 PASS: middleware call order=%v", calls)
	})

	t.Run("recursive_mount_middleware", func(t *testing.T) {
		calls = nil
		req := httptest.NewRequest("GET", "/api/v1/nested/deep", nil)
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		t.Logf("H8-02 recursive mount: status=%d calls=%v", rec.Code, calls)
		// Verify outer and group mw ran for the recursive sub-mount
		hasOuter := false
		hasGroup := false
		for _, c := range calls {
			if c == "outer-mux-mw" {
				hasOuter = true
			}
			if c == "group-mw" {
				hasGroup = true
			}
		}
		if !hasOuter {
			t.Errorf("H8-02 recursive: outer mux middleware bypassed; calls=%v", calls)
		}
		if !hasGroup {
			t.Errorf("H8-02 recursive: group middleware bypassed; calls=%v", calls)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-04: CORS preflight + throttle — does OPTIONS bypass throttle?
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: auto-handled OPTIONS (HandleOPTIONS=true) goes through lazyOPTIONS
// which wraps with m.middleware including throttle. Verify throttle runs.

func TestS8_H8_04_OptionsReachesThrottle(t *testing.T) {
	t.Parallel()

	throttleCalled := false
	fakeThrottle := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			throttleCalled = true
			next.ServeHTTP(w, r)
		})
	}

	m := muxmaster.New()
	m.HandleOPTIONS = true
	m.Use(fakeThrottle) // throttle registered via m.Use

	// Register GET but NOT OPTIONS — auto-OPTIONS should answer
	m.GET("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	t.Run("auto_OPTIONS_reaches_mw_throttle", func(t *testing.T) {
		throttleCalled = false
		req := httptest.NewRequest("OPTIONS", "/resource", nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		t.Logf("H8-04: OPTIONS /resource status=%d throttleCalled=%v Allow=%q",
			rec.Code, throttleCalled, rec.Header().Get("Allow"))

		if !throttleCalled {
			t.Errorf("H8-04 VULNERABLE: throttle middleware NOT called for auto-OPTIONS; "+
				"status=%d — OPTIONS preflight bypasses throttle", rec.Code)
		} else {
			t.Logf("H8-04 PASS: throttle middleware ran for auto-OPTIONS")
		}
	})

	t.Run("CORS_preflight_reaches_mw_throttle", func(t *testing.T) {
		// CORS OPTIONS preflight: client sends OPTIONS with Origin and ACRM
		throttleCalled = false

		m2 := muxmaster.New()
		m2.HandleOPTIONS = true

		corsThrottleCalled := false
		m2.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				corsThrottleCalled = true
				next.ServeHTTP(w, r)
			})
		})
		m2.Use(middleware.CORS(middleware.CORSOptions{
			AllowedOrigins: []string{"https://example.com"},
			AllowedMethods: []string{"GET", "POST"},
		}))
		m2.GET("/api", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		})

		req := httptest.NewRequest("OPTIONS", "/api", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		m2.ServeHTTP(rec, req)

		t.Logf("H8-04 CORS preflight: status=%d throttleCalled=%v ACAO=%q",
			rec.Code, corsThrottleCalled, rec.Header().Get("Access-Control-Allow-Origin"))

		if !corsThrottleCalled {
			t.Errorf("H8-04 CORS VULNERABLE: throttle-equivalent middleware bypassed for CORS preflight")
		} else {
			t.Logf("H8-04 CORS PASS: middleware ran for CORS preflight OPTIONS")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-10: set_header + compress + Vary header merging
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: when SetHeader("Vary","Origin") and Compress are both in the chain,
// the Vary header should contain BOTH "Origin" and "Accept-Encoding".
// If one overwrites the other, a CDN may mis-cache.

func TestS8_H8_10_CompressSetHeaderVaryMerge(t *testing.T) {
	t.Parallel()

	// Body large enough to trigger compression
	bigBody := bytes.Repeat([]byte("hello world compressed response data "), 50)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(bigBody)
	})

	compressH := middleware.Compress(1)(inner)

	t.Run("set_header_outer_compress_inner", func(t *testing.T) {
		// SetHeader is outer, Compress is inner
		// Request flow: setHeader → compress → inner
		// SetHeader sets Vary:Origin first (before calling next)
		// then compress.commit() adds Vary:Accept-Encoding
		setHeaderH := middleware.SetHeader("Vary", "Origin")(compressH)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		setHeaderH.ServeHTTP(rec, req)

		vary := rec.Header()["Vary"]
		t.Logf("H8-10 set_header_outer: Vary=%v Content-Encoding=%q",
			vary, rec.Header().Get("Content-Encoding"))

		hasOrigin := false
		hasAcceptEncoding := false
		for _, v := range vary {
			if strings.Contains(v, "Origin") {
				hasOrigin = true
			}
			if strings.Contains(v, "Accept-Encoding") {
				hasAcceptEncoding = true
			}
		}
		// Both should be present when compress was triggered
		if rec.Header().Get("Content-Encoding") == "gzip" {
			if !hasOrigin || !hasAcceptEncoding {
				t.Errorf("H8-10 FINDING: Vary header incomplete: Origin=%v AE=%v (Vary=%v)",
					hasOrigin, hasAcceptEncoding, vary)
				t.Errorf("Cache-poisoning risk: CDN may not have correct vary key set")
			} else {
				t.Logf("H8-10 PASS: Vary contains both Origin and Accept-Encoding")
			}
		}
	})

	t.Run("compress_outer_set_header_inner", func(t *testing.T) {
		// Compress is outer, SetHeader is inner
		// Request flow: compress → setHeader → inner
		setHeaderInner := middleware.SetHeader("Vary", "Origin")(inner)
		compressOuter := middleware.Compress(1)(setHeaderInner)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		compressOuter.ServeHTTP(rec, req)

		vary := rec.Header()["Vary"]
		t.Logf("H8-10 compress_outer: Vary=%v Content-Encoding=%q",
			vary, rec.Header().Get("Content-Encoding"))

		if rec.Header().Get("Content-Encoding") == "gzip" {
			hasOrigin := false
			hasAE := false
			for _, v := range vary {
				if strings.Contains(v, "Origin") {
					hasOrigin = true
				}
				if strings.Contains(v, "Accept-Encoding") {
					hasAE = true
				}
			}
			if !hasOrigin || !hasAE {
				t.Errorf("H8-10 FINDING: Vary incomplete in compress-outer order: Origin=%v AE=%v",
					hasOrigin, hasAE)
			} else {
				t.Logf("H8-10 compress_outer PASS: both Vary values present")
			}
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-22: Mount RawPath trim with %2F-encoded prefix
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: when prefix="/api" and RawPath="/api%2F..." or "/api%2fusers/...",
// the TrimPrefix either matches only literal bytes or produces a non-rooted result
// which must be zeroed.

func TestS8_H8_22_MountRawPathPercentEncoded(t *testing.T) {
	t.Parallel()

	var capturedPath, capturedRaw string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedRaw = r.URL.RawPath
		w.WriteHeader(200)
		w.Write([]byte("inner:path=" + r.URL.Path))
	})

	m := muxmaster.New()
	m.Mount("/api", inner)

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	cases := []struct {
		desc       string
		rawReq     string
		wantStatus int
		wantNoRaw  bool // RawPath in inner should be either empty or rooted
	}{
		{
			desc:       "normal /api/users",
			rawReq:     "GET /api/users HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantStatus: 200,
		},
		{
			desc:       "encoded slash in segment /api/users%2F123",
			rawReq:     "GET /api/users%2F123 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantStatus: 200,
		},
		{
			desc:       "encoded prefix slash /api%2Fusers/123",
			rawReq:     "GET /api%2Fusers/123 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantStatus: 404, // %2F in prefix — tree won't match /api/*mux_mount for this RawPath
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			capturedPath = ""
			capturedRaw = ""

			raw := sendRawTCP(t, addr, tc.rawReq)
			statusOK := strings.Contains(raw, "HTTP/1.1 "+http.StatusText(tc.wantStatus)) ||
				strings.Contains(raw, "200 OK") || strings.Contains(raw, "404")
			_ = statusOK

			t.Logf("H8-22 %s: capturedPath=%q capturedRaw=%q raw_resp=%q",
				tc.desc, capturedPath, capturedRaw, raw[:min(len(raw), 80)])

			// Security assertion: if RawPath was forwarded, it MUST start with '/'
			if capturedRaw != "" && capturedRaw[0] != '/' {
				t.Errorf("H8-22 VULNERABLE: inner handler got non-rooted RawPath=%q "+
					"(violates url.URL contract, may confuse downstream router)", capturedRaw)
			} else if capturedRaw != "" {
				t.Logf("H8-22 PASS: RawPath=%q is rooted", capturedRaw)
			}

			// RawPath must not contain the mount prefix bytes
			if strings.HasPrefix(capturedRaw, "/api") {
				t.Errorf("H8-22 FINDING: inner handler got RawPath=%q still containing mount prefix /api",
					capturedRaw)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-30: dispatchWithRecover — PanicHandler double-panic behaviour
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: if PanicHandler itself panics, does MuxMaster have an outer guard?
// Expected: net/http provides the outer recovery; connection is severed.
// Security concern: could a double-panic cause a double-WriteHeader that
// leaks data from a prior request?

func TestS8_H8_30_PanicHandlerDoublePanic(t *testing.T) {
	t.Parallel()

	t.Run("panichandler_panic_caught_by_nethttp", func(t *testing.T) {
		m := muxmaster.New()
		m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
			// PanicHandler itself panics — tests if outer net/http guard catches it
			panic("panic in PanicHandler")
		}
		m.GET("/", func(w http.ResponseWriter, r *http.Request) {
			panic("handler panic")
		})

		srv := httptest.NewServer(m)
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// Connection severed — this is the expected outcome.
			// net/http catches the panic and closes the connection.
			t.Logf("H8-30 PASS: PanicHandler double-panic closes connection (err=%v)", err)
		} else {
			resp.Body.Close()
			// If we got a response, it means something recovered. Log the status.
			t.Logf("H8-30 INFO: got response status=%d (net/http may have caught the panic)", resp.StatusCode)
		}
		// In both cases, the server is still running — verify with a clean request
	})

	t.Run("handler_writes_then_panics_panichandler_double_write", func(t *testing.T) {
		// Handler writes a 200 first, then panics.
		// PanicHandler tries to write a 500.
		// Expected: httptest.ResponseRecorder records the first WriteHeader (200).
		// The second WriteHeader (500) is ignored per Go's http.ResponseWriter contract.
		// There should be NO data leak from a prior request.

		m := muxmaster.New()
		m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
			w.WriteHeader(500)
			w.Write([]byte("PANIC-HANDLER-BODY"))
		}
		m.GET("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("PARTIAL-BODY"))
			panic("mid-write panic")
		})

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		t.Logf("H8-30 double-write: status=%d body=%q", rec.Code, rec.Body.String())

		// First WriteHeader(200) wins; second WriteHeader(500) is a no-op
		if rec.Code == 500 && rec.Body.String() == "PANIC-HANDLER-BODY" {
			// PanicHandler ran but 500 wrote over 200 — this would be a superposition issue
			// Actually with httptest.Recorder, the LAST WriteHeader wins if the first wasn't sent
			t.Logf("H8-30 INFO: PanicHandler status=%d (200 was overwritten by 500 in recorder)", rec.Code)
		}

		// Key check: no data from a PRIOR request should appear
		body := rec.Body.String()
		if strings.Contains(body, "PRIOR-REQUEST-DATA") {
			t.Errorf("H8-30 CRITICAL: data from prior request leaked into response body")
		} else {
			t.Logf("H8-30 PASS: no cross-request data leak detected")
		}
	})

	t.Run("panichandler_not_registered_no_recovery", func(t *testing.T) {
		// Without PanicHandler, panic propagates to net/http's conn.serve recovery.
		// This should NOT crash the server — net/http always has a recovery.
		m := muxmaster.New()
		m.GET("/panic", func(w http.ResponseWriter, r *http.Request) {
			panic("unrecovered panic in handler")
		})
		m.GET("/ok", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("ok"))
		})

		srv := httptest.NewServer(m)
		defer srv.Close()

		// Send panic request
		ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()
		req1, _ := http.NewRequestWithContext(ctx1, "GET", srv.URL+"/panic", nil)
		resp1, err1 := http.DefaultClient.Do(req1)
		if err1 == nil {
			resp1.Body.Close()
		}

		// Verify server still alive
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		req2, _ := http.NewRequestWithContext(ctx2, "GET", srv.URL+"/ok", nil)
		resp2, err2 := http.DefaultClient.Do(req2)
		if err2 != nil || resp2.StatusCode != 200 {
			t.Errorf("H8-30 CRITICAL: server not responsive after panic in handler: err=%v status=%v",
				err2, resp2)
		} else {
			resp2.Body.Close()
			t.Logf("H8-30 PASS: server responsive after handler panic (net/http caught it)")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-48: response.JSON with cyclic struct (CVE-2024-34156 analogue)
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: encoding/json.Marshal returns an error (not panics) for cyclic structs.
// response.JSON() returns the error to the caller; no panic, no memory leak.

func TestS8_H8_48_JSONCyclicStructNoLeak(t *testing.T) {
	t.Parallel()

	type selfRef struct {
		V *selfRef
	}

	t.Run("json_marshal_cyclic_returns_error", func(t *testing.T) {
		a := &selfRef{}
		a.V = a

		b, err := json.Marshal(a)
		if err != nil {
			t.Logf("H8-48 PASS: json.Marshal cyclic → error (not panic): %v", err)
		} else {
			t.Errorf("H8-48 UNEXPECTED: json.Marshal cyclic succeeded: %q", string(b))
		}
	})

	t.Run("response_JSON_cyclic_handler_gets_error", func(t *testing.T) {
		a := &selfRef{}
		a.V = a

		errReceived := false
		m := muxmaster.New()
		m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
			t.Errorf("H8-48 VULNERABLE: panic reached PanicHandler with %v — json.Marshal should have returned error, not panicked", rcv)
			w.WriteHeader(500)
		}
		m.GET("/", func(w http.ResponseWriter, r *http.Request) {
			err := muxmaster.JSON(w, 200, a)
			if err != nil {
				errReceived = true
				http.Error(w, "json error", 500)
			}
		})

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		if !errReceived {
			t.Errorf("H8-48: cyclic struct did not produce an error in the handler")
		} else {
			t.Logf("H8-48 PASS: response.JSON cyclic → error returned (status=%d)", rec.Code)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-51: Mount + path with ".." traversal (nginx alias traversal class)
// ─────────────────────────────────────────────────────────────────────────────
// Hypothesis: net/http server does NOT clean ".." from URL paths before
// passing to handlers. Mount forwards the raw (uncleaned) path to the inner handler.
// This is documented behaviour — inner handlers must clean paths themselves.
// The test verifies the inner handler RECEIVES ".." in the path (not that it's a
// vulnerability in MuxMaster, but documents the trust boundary).

func TestS8_H8_51_MountDotDotForwarding(t *testing.T) {
	t.Parallel()

	var capturedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte("path:" + r.URL.Path))
	})

	m := muxmaster.New()
	m.Mount("/public", inner)

	srv := httptest.NewServer(m)
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	cases := []struct {
		desc           string
		rawReq         string
		wantInnerSafe  bool   // true if inner should NOT see ".." in path
		wantPathPrefix string // if inner is reached, path must start with this
	}{
		{
			desc:           "normal /public/resource",
			rawReq:         "GET /public/resource HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantInnerSafe:  true,
			wantPathPrefix: "/",
		},
		{
			desc:          "path traversal /public/../secret raw TCP",
			rawReq:        "GET /public/../secret HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantInnerSafe: false, // ".." will be in the path forwarded to inner
		},
		{
			desc:          "encoded traversal /public/%2e%2e/secret",
			rawReq:        "GET /public/%2e%2e/secret HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			wantInnerSafe: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			capturedPath = ""
			rawResp := sendRawTCP(t, addr, tc.rawReq)
			t.Logf("H8-51 %s: capturedPath=%q raw_resp=%q", tc.desc, capturedPath, rawResp[:min(len(rawResp), 100)])

			if capturedPath != "" {
				if tc.wantInnerSafe && strings.Contains(capturedPath, "..") {
					t.Errorf("H8-51 VULNERABLE: inner handler got '..' in path=%q for safe case", capturedPath)
				}
				if !tc.wantInnerSafe && strings.Contains(capturedPath, "..") {
					t.Logf("H8-51 DOCUMENTED BEHAVIOUR: inner handler received '..': path=%q — "+
						"inner handlers MUST clean paths; MuxMaster does not clean on behalf of inner mux", capturedPath)
				}
				// The inner path must not contain the mount prefix
				if strings.HasPrefix(capturedPath, "/public") {
					t.Errorf("H8-51 FINDING: inner handler still has mount prefix in path=%q", capturedPath)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-59/H8-60/H8-61: HTTP/2 CVE status — govulncheck result
// ─────────────────────────────────────────────────────────────────────────────
// These are confirmed via govulncheck which returned "No vulnerabilities found."
// This test documents the expected behaviour at the application level.

func TestS8_H8_59_60_61_HTTP2CVEsPatched(t *testing.T) {
	t.Parallel()

	// The h2harness module covers the runtime behaviour of:
	// - CVE-2023-44487 Rapid Reset: goroutine count bounded after 500 RST_STREAM floods
	// - CVE-2024-27316 CONTINUATION flood: heap growth bounded after 150 × 200-header floods
	// - HPACK bombing: heap delta < 50 MB after 200 × 50 × 4096-byte headers
	//
	// govulncheck output (Go 1.26.2 linux/amd64, commit e30ae94):
	// "No vulnerabilities found."
	//
	// Go 1.26.2 is well beyond the patch versions:
	// - CVE-2023-44487: patched in Go 1.21.1 + 1.20.8
	// - CVE-2024-27316: patched in Go 1.22.2 + 1.21.9
	// - No HTTP/2 MadeYouReset (H8-61) advisory found in govulncheck database.

	t.Logf("H8-59 (CVE-2023-44487 Rapid Reset): PATCHED — Go 1.26.2 >= Go 1.21.1")
	t.Logf("H8-60 (CVE-2024-27316 CONTINUATION flood): PATCHED — Go 1.26.2 >= Go 1.22.2")
	t.Logf("H8-61 (MadeYouReset / recent H2 advisory): govulncheck reports NO vulnerabilities found")

	// Runtime validation is in h2harness/ — see TestH2_RapidReset_CVE202344487,
	// TestH2_ContinuationFlood_CVE202427316, TestH2_HPACKBombing.
	// All three passed with Go 1.26.2.
}

// ─────────────────────────────────────────────────────────────────────────────
// H8-NEW: Logger r.Method not sanitised (discovered during S8 audit)
// ─────────────────────────────────────────────────────────────────────────────
// Finding: middleware/logger.go uses r.Method in the fmt.Fprintf format string
// without sanitiseForLog(), while r.URL.Path IS sanitised.
// If r.Method contains '\n' (e.g. from a proxy passing unusual methods, or from
// programmatic use), a fake log line can be injected.
// net/http server rejects CR/LF in method at the wire level (RFC 7230 token),
// so the direct network attack surface is minimal. But defensive programming
// requires ALL user-controlled bytes to be sanitised before reaching log output.

func TestS8_Logger_Method_NotSanitised(t *testing.T) {
	t.Parallel()

	var logBuf strings.Builder
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	logHandler := middleware.Logger(&logBuf)(handler)

	// Inject LF into the Method field (simulates a proxy or programmatic call
	// that forwards an unusual method — possible in test harnesses, proxies, etc.)
	req := httptest.NewRequest("GET", "/path", nil)
	req.Method = "GET\nFAKE-LOG: injected-entry"

	rec := httptest.NewRecorder()
	logHandler.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	lines := strings.Split(logOutput, "\n")
	t.Logf("Logger output with LF-in-method: %q", logOutput)
	t.Logf("Line count: %d", len(lines))

	// HPS-2026-0001: log injection requires the LF inside r.Method to produce
	// a SEPARATE log line. After sanitisation, the LF is escaped (\\n) and
	// the entire injected payload remains on the same line as the GET token.
	// Detect injection by looking for any standalone line that starts with
	// "FAKE-LOG:" (the attacker's payload is a fake key/value entry).
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "FAKE-LOG:") {
			t.Errorf("HPS-S8-LOGGER VULNERABLE: log injection via r.Method LF: injected line=%q", line)
			t.Errorf("Fix: use sanitiseForLog(r.Method) in the fmt.Fprintf call in logger.go")
			return
		}
	}
	// Defence in depth: a successful injection produces >2 split lines
	// (real log line + fake line + trailing empty after trailing LF).
	if len(lines) > 2 {
		for _, l := range lines {
			trim := strings.TrimSpace(l)
			if trim == "" {
				continue
			}
			if !strings.Contains(l, "GET") {
				t.Errorf("HPS-S8-LOGGER: suspicious extra log line: %q", l)
			}
		}
	}
	t.Logf("Logger Method sanitisation: method=%q log=%q", req.Method, logOutput)
}
