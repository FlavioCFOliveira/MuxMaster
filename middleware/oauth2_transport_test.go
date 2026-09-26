package middleware_test

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// oauth2GlobalTransportHelperEnv selects the helper mode of
// TestOAuth2Introspect_DefaultClient_DoesNotInitialiseDefaultTransport when
// the test binary re-executes itself. The helper must run in a FRESH process:
// http.DefaultTransport's lazy HTTP/2 initialisation happens at most once per
// process, so once any earlier test in this binary has used the global
// transport, the side effect under test can no longer be observed.
const oauth2GlobalTransportHelperEnv = "MUXMASTER_OAUTH2_GLOBAL_TRANSPORT_HELPER"

// TestOAuth2Introspect_DefaultClient_DoesNotInitialiseDefaultTransport is the
// regression test for the rmp #300 follow-up found by the middleware security
// review. Building the default introspection client with
// http.DefaultTransport.Clone() ran the GLOBAL transport's one-time HTTP/2
// initialisation at middleware construction. An application that then
// replaced http.DefaultTransport.TLSClientConfig (for example to trust a
// private CA) lost HTTP/2 for every http.DefaultClient request: the
// replacement config no longer advertised "h2" through ALPN, and the global
// transport never re-runs its initialisation.
//
// The test re-executes the test binary twice, in fresh processes:
//   - "control": no middleware is built; the application replaces the
//     global TLSClientConfig and must negotiate HTTP/2 (proves the
//     environment supports HTTP/2, so the next check is meaningful);
//   - "middleware": OAuth2Introspect is built with the default client FIRST,
//     then the application does exactly the same; it must still negotiate
//     HTTP/2.
func TestOAuth2Introspect_DefaultClient_DoesNotInitialiseDefaultTransport(t *testing.T) {
	if mode := os.Getenv(oauth2GlobalTransportHelperEnv); mode != "" {
		runOAuth2GlobalTransportHelper(t, mode)
		return
	}

	for _, mode := range []string{"control", "middleware"} {
		out := runOAuth2HelperProcess(t, "TestOAuth2Introspect_DefaultClient_DoesNotInitialiseDefaultTransport", mode)
		const want = "OAUTH2_GLOBAL_PROTO=HTTP/2.0"
		if !strings.Contains(out, want) {
			t.Fatalf("%s: http.DefaultClient did not negotiate HTTP/2 after the application replaced http.DefaultTransport.TLSClientConfig (want %q in helper output)\n%s",
				mode, want, out)
		}
	}
}

// runOAuth2HelperProcess re-executes the test binary, running only the named
// test with the helper environment variable set to mode, and returns the
// combined output. The helper process fails the parent test if it fails.
func runOAuth2HelperProcess(t *testing.T, testName, mode string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), oauth2GlobalTransportHelperEnv+"="+mode)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s helper process failed: %v\n%s", mode, err, out.String())
	}
	return out.String()
}

// runOAuth2GlobalTransportHelper is the body executed in the re-executed
// helper process. It prints the protocol http.DefaultClient negotiated.
func runOAuth2GlobalTransportHelper(t *testing.T, mode string) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	if mode == "middleware" {
		// Construction only: the endpoint is never contacted.
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: "https://idp.example.invalid/introspect",
		})
	}

	// The application configures its private CA on the global transport
	// AFTER building its middleware.
	global, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}
	srvTransport, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest client transport is %T, want *http.Transport", srv.Client().Transport)
	}
	global.TLSClientConfig = &tls.Config{
		RootCAs:    srvTransport.TLSClientConfig.RootCAs,
		MinVersion: tls.VersionTLS12,
	}

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET via http.DefaultClient: %v", err)
	}
	_ = resp.Body.Close()
	fmt.Printf("OAUTH2_GLOBAL_PROTO=%s\n", resp.Proto)
}

// TestOAuth2Introspect_DefaultClient_HonoursDefaultTransportSettings checks
// that the default introspection client still honours proxy and TLS
// customisations an application makes to http.DefaultTransport BEFORE
// building the middleware (the default transport copies them at
// construction). Each case runs in a fresh helper process because it
// modifies the process-global http.DefaultTransport.
func TestOAuth2Introspect_DefaultClient_HonoursDefaultTransportSettings(t *testing.T) {
	if mode := os.Getenv(oauth2GlobalTransportHelperEnv); mode != "" {
		runOAuth2SettingsHelper(t, mode)
		return
	}
	for _, mode := range []string{"tls", "proxy"} {
		out := runOAuth2HelperProcess(t, "TestOAuth2Introspect_DefaultClient_HonoursDefaultTransportSettings", mode)
		want := "OAUTH2_SETTINGS_RESULT=" + mode + " status=200"
		if !strings.Contains(out, want) {
			t.Fatalf("%s customisation of http.DefaultTransport was not honoured (want %q in helper output)\n%s", mode, want, out)
		}
	}
}

// runOAuth2SettingsHelper is the body executed in the re-executed helper
// process for TestOAuth2Introspect_DefaultClient_HonoursDefaultTransportSettings.
func runOAuth2SettingsHelper(t *testing.T, mode string) {
	introspect := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
	})
	global, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}

	var opts middleware.OAuth2Options
	switch mode {
	case "tls":
		// The IdP presents a certificate from a private CA; the application
		// trusts that CA on the global transport before building the
		// middleware. Without the copied TLS configuration the handshake
		// fails and the request is rejected.
		idp := httptest.NewUnstartedServer(introspect)
		idp.EnableHTTP2 = true
		idp.StartTLS()
		defer idp.Close()
		idpTransport, ok := idp.Client().Transport.(*http.Transport)
		if !ok {
			t.Fatalf("httptest client transport is %T, want *http.Transport", idp.Client().Transport)
		}
		global.TLSClientConfig = &tls.Config{
			RootCAs:    idpTransport.TLSClientConfig.RootCAs,
			MinVersion: tls.VersionTLS12,
		}
		opts = middleware.OAuth2Options{Endpoint: idp.URL + "/introspect"}
	case "proxy":
		// The endpoint host does not resolve; the request only succeeds if
		// it is sent through the proxy the application configured on the
		// global transport before building the middleware.
		proxy := httptest.NewServer(introspect)
		defer proxy.Close()
		proxyURL, err := url.Parse(proxy.URL)
		if err != nil {
			t.Fatalf("parse proxy URL: %v", err)
		}
		global.Proxy = http.ProxyURL(proxyURL)
		opts = middleware.OAuth2Options{
			Endpoint:              "http://idp.example.invalid/introspect",
			AllowInsecureEndpoint: true,
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}

	mw := middleware.OAuth2Introspect(opts)
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer settings-token")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	fmt.Printf("OAUTH2_SETTINGS_RESULT=%s status=%d\n", mode, rec.Code)
}
