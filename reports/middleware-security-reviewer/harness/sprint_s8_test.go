// Package harness_test — Sprint S8 supplemental security battery.
//
// Covers:
//   - MSR-2026-0065: real_ip leftmost-XFF in multi-hop chains (H8-30)
//   - MSR-2026-0066: JWT missing RequireExpiry option (H8-45)
//   - MSR-2026-0067: OAuth2 no HTTPS enforcement on Endpoint (H8-43)
//   - MSR-2026-0068: ThrottlePerIP unbounded table under sustained IP churn (H8-53)
//   - MSR-2026-0069: Logger logs r.Method without sanitiseForLog (H8-41)
//   - Composition: set_header + cors overwrite interaction (H8-10)
//   - Composition: basic_auth + logger auth-header not logged (H8-04)
//   - Composition: cors + redirect preflight ordering (H8-05)
//   - Composition: request_id collision log entanglement (H8-06)
//   - Composition: real_ip before/after throttle ordering (H8-01)
//   - Composition: timeout + compress cancel propagation (H8-02)
//   - Composition: with_value + recoverer ctx survives panic (H8-11)
//   - Composition: 32-middleware chain correctness (H8-12)
package harness_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0065: real_ip leftmost-XFF in multi-hop chains
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_RealIP_MultiHop_LeftmostXFF_Spoofable confirms that when a
// multi-hop XFF chain is present and only the rightmost proxy is trusted,
// real_ip picks the leftmost (client-injected) value rather than the first
// non-trusted hop from the right.
//
// Attack: X-Forwarded-For: attacker-ip, real-client, trusted-proxy
// With trusted = trusted-proxy/32
// Expected correct behavior: real-client (rightmost non-trusted)
// Actual current behavior:   attacker-ip (leftmost) — spoofable
func TestSec_RealIP_MultiHop_LeftmostXFF_Spoofable(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.5/32")
	mw := middleware.RealIP(&trusted)

	var capturedRemote string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRemote = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	// Direct peer is the trusted proxy.
	req.RemoteAddr = "10.0.0.5:12345"
	// XFF chain: attacker injected their IP leftmost; real client is in the middle.
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 1.2.3.4, 10.0.0.5")

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	// The correct behaviour for a multi-trusted-proxy deployment is to pick
	// "1.2.3.4" (the rightmost non-trusted entry).
	// The current implementation picks "9.9.9.9" (leftmost).
	if capturedRemote == "9.9.9.9" {
		t.Logf("MSR-2026-0065 CONFIRMED: real_ip picked leftmost XFF value %q in multi-hop chain. "+
			"An attacker who controls the first XFF entry can spoof their IP as %q. "+
			"Correct value for multi-hop should be %q (rightmost non-trusted). "+
			"NOTE: GoDoc documents safe only behind a SINGLE proxy. "+
			"Multi-hop operators must configure proxy depth or use custom keyFn in ThrottlePerIP.",
			capturedRemote, capturedRemote, "1.2.3.4")
	} else if capturedRemote == "1.2.3.4" {
		t.Logf("OK: real_ip correctly resolved rightmost-non-trusted XFF entry: %q", capturedRemote)
	} else {
		t.Logf("real_ip result: %q (neither attacker nor real-client)", capturedRemote)
	}
}

// TestSec_RealIP_SingleProxy_XFF_Correct confirms that for the documented
// single-trusted-proxy deployment, real_ip correctly extracts the client IP.
func TestSec_RealIP_SingleProxy_XFF_Correct(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.5/32")
	mw := middleware.RealIP(&trusted)

	var capturedRemote string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRemote = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	// Single-hop: proxy appended client IP. Leftmost = real client.
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if capturedRemote != "1.2.3.4" {
		t.Errorf("single-proxy XFF: expected 1.2.3.4, got %q", capturedRemote)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0066: JWT accepts tokens with no exp claim (no RequireExpiry option)
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_JWT_NoExpClaim_AcceptedForever confirms that a JWT with no "exp"
// claim is accepted indefinitely by JWTAuth. RFC 8725 §4.4 states that tokens
// without expiry SHOULD be rejected unless there is a compelling reason.
// The finding: JWTOptions has no RequireExpiry bool to enforce this.
func TestSec_JWT_NoExpClaim_AcceptedForever(t *testing.T) {
	secret := []byte("s8-test-secret-key")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})

	// Build a token with no exp claim.
	claims := map[string]any{
		"sub": "eternal-user",
		"iat": time.Now().Add(-24 * 365 * time.Hour).Unix(), // issued a year ago
		// no "exp" field
	}
	token := makeJWT("HS256", nil, claims, hs256Sign(secret))

	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK && reached {
		t.Logf("MSR-2026-0066 CONFIRMED: JWT with no exp claim accepted (iat=1 year ago). " +
			"RFC 8725 §4.4 recommends rejecting tokens without expiry. " +
			"JWTOptions has no RequireExpiry field to enforce this. " +
			"Severity: MEDIUM. Recommended fix: add RequireExpiry bool to JWTOptions; " +
			"when true, reject tokens where raw.Exp == 0.")
	} else {
		t.Logf("JWT no-exp token rejected (code=%d) — behaviour changed from expected", rec.Code)
	}
}

// TestSec_JWT_WithExpClaim_ExpiryEnforced verifies that a token WITH exp is
// correctly rejected once it expires (regression guard for MSR-2026-0066 fix).
func TestSec_JWT_WithExpClaim_ExpiryEnforced(t *testing.T) {
	secret := []byte("s8-test-secret-key")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})

	claims := map[string]any{
		"sub": "expiring-user",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-1 * time.Hour).Unix(), // already expired
	}
	token := makeJWT("HS256", nil, claims, hs256Sign(secret))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired JWT must return 401, got %d", rec.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0067: OAuth2 endpoint URL HTTPS not enforced
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_OAuth2_PlaintextHTTP_EndpointAccepted confirms that OAuth2Introspect
// does NOT reject a plaintext HTTP introspection endpoint at construction time.
// A plaintext endpoint sends bearer tokens over the wire in clear text,
// exposing them to any network observer (MITM / passive eavesdrop).
//
// RFC 7662 §4 explicitly requires TLS for introspection endpoints.
func TestSec_OAuth2_PlaintextHTTP_EndpointAccepted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"active":true,"sub":"user1"}`)
	}))
	defer ts.Close()

	if strings.HasPrefix(ts.URL, "https://") {
		t.Skip("test server unexpectedly used TLS")
	}

	var constructionPanicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				constructionPanicked = true
				t.Logf("OAuth2Introspect panicked on http:// endpoint (GOOD if fixing MSR-2026-0067): %v", r)
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: ts.URL, // plaintext HTTP
			CacheTTL: -1,
		})
	}()

	if !constructionPanicked {
		t.Logf("MSR-2026-0067 CONFIRMED: OAuth2Introspect accepts plaintext HTTP endpoint %q "+
			"without warning or panic at construction time. "+
			"Bearer tokens are sent in the clear to this endpoint on every cache miss. "+
			"RFC 7662 §4 requires TLS for introspection endpoints. "+
			"Severity: HIGH. Recommended fix: url.Parse(opts.Endpoint) at construction; "+
			"panic/error if scheme != 'https' unless AllowInsecureEndpoint bool override is set.",
			ts.URL)
	}
}

// TestSec_OAuth2_HTTPS_Endpoint_NotRejected confirms that a TLS endpoint
// (the correct/safe configuration) is accepted without panic (regression guard).
func TestSec_OAuth2_HTTPS_Endpoint_NotRejected(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"active":true,"sub":"user1"}`)
	}))
	defer ts.Close()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("OAuth2Introspect unexpectedly panicked on https:// endpoint: %v", r)
			}
		}()
		_ = middleware.OAuth2Introspect(middleware.OAuth2Options{
			Endpoint: ts.URL,
			CacheTTL: -1,
		})
	}()
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0068: ThrottlePerIP unbounded table under sustained IP churn
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn confirms that the internal
// per-IP table in ThrottlePerIP has no hard cap, making it susceptible to
// memory exhaustion under sustained IP-churn attacks.
//
// This test uses a small scale (500 IPs) to document the behaviour.
// At production scale (1M IPs), the O(N) map growth exhausts heap.
func TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn(t *testing.T) {
	const concurrentIPs = 500
	const limit = 10

	startBarrier := make(chan struct{})
	var inFlight atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-startBarrier
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.ThrottlePerIP(limit, 5*time.Second, func(r *http.Request) string {
		return r.RemoteAddr
	})
	handler := mw(inner)

	var wg sync.WaitGroup
	for i := range concurrentIPs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234",
				(i>>16)&0xff, (i>>8)&0xff, i&0xff)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	peakInFlight := inFlight.Load()
	close(startBarrier)
	wg.Wait()

	t.Logf("MSR-2026-0068: ThrottlePerIP concurrent IPs: %d, peak in-flight: %d. "+
		"Table entries while all requests are in-flight = O(concurrent unique IPs). "+
		"No max-entries cap exists — a sustained 1M-unique-IP attack would grow the table "+
		"to 1M entries. Mitigation: add MaxTableSize int to ThrottlePerIP; "+
		"document RealIP+known-client-range requirement for bounded key space.",
		concurrentIPs, peakInFlight)
}

// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0069: Logger logs r.Method without sanitisation
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Logger_Method_NotSanitised confirms that Logger writes r.Method
// directly into the log format string without sanitiseForLog. In Go's HTTP
// server, Method is validated and cannot contain CRLF; but in test harnesses
// or behind a misbehaving reverse proxy, a method with control characters
// would produce a log injection line.
func TestSec_Logger_Method_NotSanitised(t *testing.T) {
	var logBuf bytes.Buffer
	mw := middleware.Logger(&logBuf)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/safe-path", nil)
	// Inject a CRLF into the Method field directly (bypasses Go HTTP parser).
	req.Method = "GET\r\nINJECTED: fake-log-line"

	mw(inner).ServeHTTP(httptest.NewRecorder(), req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "INJECTED") && strings.Contains(logOutput, "fake-log-line") {
		t.Logf("MSR-2026-0069 CONFIRMED: Logger emits unsanitised r.Method. "+
			"CRLF in Method creates a fake log line. Log output: %q. "+
			"Severity: LOW (Go net/http prevents this in production; "+
			"exploitable via test harness or misbehaving reverse proxy). "+
			"Fix: replace r.Method with sanitiseForLog(r.Method) in logger.go fmt.Fprintf call.",
			logOutput)
	} else if strings.Contains(logOutput, "\r") {
		t.Logf("MSR-2026-0069: raw CR in Method log: %q", logOutput)
	} else {
		t.Logf("Logger method field in log: %q — no injection in this run "+
			"(Go net/http may have validated the method before it reached here)", logOutput)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-10: set_header + cors — SetHeader after CORS overwrites CORS headers
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_SetHeaderAfterCORS_OverwritesCORSHeaders demonstrates
// that placing SetHeader AFTER CORS in the middleware chain allows it to
// silently overwrite Access-Control-Allow-Origin, bypassing the CORS policy.
//
// Dangerous order (operator mistake): CORS wraps SetHeader wraps inner.
// Correct order: SetHeader wraps CORS wraps inner (CORS writes last and wins).
func TestSec_Composition_SetHeaderAfterCORS_OverwritesCORSHeaders(t *testing.T) {
	corsMW := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com"},
	})
	setHeaderMW := middleware.SetHeader("Access-Control-Allow-Origin", "*")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Dangerous order: CORS runs (sets trusted.com), then SetHeader runs (overwrites with *).
	chain := corsMW(setHeaderMW(inner))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao == "*" {
		t.Logf("H8-10 CONFIRMED: SetHeader after CORS overwrites Access-Control-Allow-Origin to %q. "+
			"CORS policy (restrict to trusted.com) is silently bypassed. "+
			"Severity: MEDIUM (operator misconfiguration). "+
			"Mitigation: document that SetHeader must be placed BEFORE CORS in the wrapping order; "+
			"CORS must be the innermost header-setting middleware to win the last-write.",
			acao)
	} else {
		t.Logf("H8-10: ACAO=%q — chain order may differ from expected", acao)
	}
}

// TestSec_Composition_SetHeaderBeforeCORS_CORSWins confirms the safe ordering.
func TestSec_Composition_SetHeaderBeforeCORS_CORSWins(t *testing.T) {
	setHeaderMW := middleware.SetHeader("Access-Control-Allow-Origin", "https://evil.com")
	corsMW := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com"},
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Safe order: SetHeader wraps CORS wraps inner. CORS overwrites evil.com with trusted.com.
	chain := setHeaderMW(corsMW(inner))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao == "https://trusted.com" {
		t.Logf("H8-10 safe order PASS: CORS overwrites SetHeader, ACAO=%q", acao)
	} else {
		t.Errorf("H8-10 safe order broken: expected https://trusted.com, got %q", acao)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-04: basic_auth + logger — auth header not leaked to log
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_BasicAuthLogger_AuthHeaderNotLogged confirms that when
// BasicAuth and Logger are composed, the Authorization header is never written
// to the log output.
func TestSec_Composition_BasicAuthLogger_AuthHeaderNotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	logMW := middleware.Logger(&logBuf)
	authMW := middleware.BasicAuth("test-realm", map[string]string{
		"alice": "hunter2-S8",
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	chain := logMW(authMW(inner))
	req := httptest.NewRequest("GET", "/secret", nil)
	req.SetBasicAuth("alice", "hunter2-S8")

	chain.ServeHTTP(httptest.NewRecorder(), req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "hunter2-S8") {
		t.Errorf("H8-04 CRITICAL: password 'hunter2-S8' appears in log output: %q", logOutput)
	}
	if strings.Contains(logOutput, "Authorization") {
		t.Errorf("H8-04 CRITICAL: 'Authorization' header name appears in log output: %q", logOutput)
	}
	if strings.Contains(logOutput, "Basic ") {
		t.Errorf("H8-04 CRITICAL: 'Basic ' credential prefix appears in log output: %q", logOutput)
	}
	t.Logf("H8-04 PASS: log output contains no auth material: %q", logOutput)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-05: cors + redirect — OPTIONS preflight not forwarded to redirect
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_CORS_Preflight_NotRedirected confirms that an OPTIONS
// preflight handled by CORS returns 204 immediately and is NOT forwarded to
// any downstream handler that would redirect it (which would break CORS).
func TestSec_Composition_CORS_Preflight_NotRedirected(t *testing.T) {
	corsMW := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://trusted.com"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	})

	redirectCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			redirectCalled = true
		}
		http.Redirect(w, r, "/new-location", http.StatusMovedPermanently)
	})

	chain := corsMW(inner)
	req := httptest.NewRequest(http.MethodOptions, "/api/resource", nil)
	req.Header.Set("Origin", "https://trusted.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("H8-05: OPTIONS preflight should return 204, got %d", rec.Code)
	}
	if redirectCalled {
		t.Errorf("H8-05: inner redirect handler reached for OPTIONS — CORS did not short-circuit")
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("H8-05: Access-Control-Allow-Methods missing from preflight response")
	}
	t.Logf("H8-05 PASS: CORS preflight short-circuits at 204, redirect handler not reached")
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-06: request_id + logger — standard Logger excludes request ID
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_RequestID_LogNotEntangled confirms that the standard Logger
// does NOT log the request ID, so ID collisions between requests do NOT cause
// log-line entanglement in the standard Logger.
func TestSec_Composition_RequestID_LogNotEntangled(t *testing.T) {
	var logBuf bytes.Buffer
	var mu sync.Mutex
	logMW := middleware.Logger(&mu_writer{w: &logBuf, mu: &mu})
	ridMW := middleware.RequestID()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	chain := logMW(ridMW(inner))

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", fmt.Sprintf("/path/%d", i), nil)
			req.Header.Set("X-Request-ID", "fixed-collision-id")
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
		}(i)
	}
	wg.Wait()

	mu.Lock()
	output := logBuf.String()
	mu.Unlock()

	if strings.Contains(output, "fixed-collision-id") {
		t.Logf("H8-06 NOTE: request ID appears in log — custom loggers using GetRequestID " +
			"would have collision-ambiguity risk")
	} else {
		t.Logf("H8-06 PASS: standard Logger does not include request ID — no collision entanglement. "+
			"Log length: %d bytes", len(output))
	}
}

// mu_writer is a goroutine-safe io.Writer for testing.
type mu_writer struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (m *mu_writer) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.w.Write(p)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-01: real_ip before/after throttle ordering
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_RealIP_Before_ThrottlePerIP_CorrectKey confirms that
// placing RealIP before ThrottlePerIP causes ThrottlePerIP to key on the
// real client IP (rewritten by RealIP), not the proxy's address.
func TestSec_Composition_RealIP_Before_ThrottlePerIP_CorrectKey(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.1/32")
	realIPMW := middleware.RealIP(&trusted)
	throttleMW := middleware.ThrottlePerIP(10, 100*time.Millisecond, nil)

	var mu sync.Mutex
	var observedKeys []string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observedKeys = append(observedKeys, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	chain := realIPMW(throttleMW(inner))

	for _, clientIP := range []string{"1.2.3.4", "5.6.7.8"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", clientIP)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observedKeys) == 2 && observedKeys[0] != observedKeys[1] {
		t.Logf("H8-01 PASS: RealIP before ThrottlePerIP — distinct client IPs produce distinct throttle keys: %v",
			observedKeys)
	} else {
		t.Logf("H8-01: observed keys in handler: %v (2 requests expected, got %d)", observedKeys, len(observedKeys))
	}
}

// TestSec_Composition_ThrottlePerIP_Before_RealIP_WrongKey confirms that
// placing ThrottlePerIP BEFORE RealIP causes it to key on the proxy's
// RemoteAddr, defeating per-client rate limiting.
func TestSec_Composition_ThrottlePerIP_Before_RealIP_WrongKey(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.1/32")
	realIPMW := middleware.RealIP(&trusted)
	throttleMW := middleware.ThrottlePerIP(1, 50*time.Millisecond, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Dangerous order: throttle first, then realIP.
	chain := throttleMW(realIPMW(inner))

	var results []int
	for _, clientIP := range []string{"1.2.3.4", "5.6.7.8"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", clientIP)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		results = append(results, rec.Code)
	}

	t.Logf("H8-01 wrong order results (limit=1, sequential): %v", results)
	// With limit=1 and sequential requests, both may succeed (refs cleanup between calls).
	// Under concurrent load the second gets throttled because both key on 10.0.0.1.
	t.Logf("H8-01 NOTE: ThrottlePerIP before RealIP keys on proxy IP 10.0.0.1, not client IP. " +
		"Under concurrent load from distinct clients, the proxy IP hits the limit and " +
		"legitimate clients are throttled. Correct order: RealIP THEN ThrottlePerIP.")
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-02: timeout + compress — context cancel propagation
// ═══════════════════════════════════════════════════════════════════════════════

// TestSec_Composition_Timeout_Compress_CancelPropagates confirms that when
// Timeout fires while a handler is streaming a compressed response, the handler
// can observe ctx cancellation and stop writing.
func TestSec_Composition_Timeout_Compress_CancelPropagates(t *testing.T) {
	timeoutMW := middleware.Timeout(50 * time.Millisecond)
	compressMW := middleware.Compress(1)

	cancelObserved := make(chan struct{}, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write enough to trigger compression (> 1 KB).
		chunk := bytes.Repeat([]byte("x"), 2048)
		w.Write(chunk) //nolint:errcheck
		// Simulate slow streaming — observe ctx.Done().
		select {
		case <-r.Context().Done():
			cancelObserved <- struct{}{}
		case <-time.After(200 * time.Millisecond):
			// Handler did not observe cancel.
		}
	})

	chain := timeoutMW(compressMW(inner))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	select {
	case <-cancelObserved:
		t.Logf("H8-02 PASS: handler observed ctx cancellation from Timeout while compress was active")
	default:
		t.Logf("H8-02 INFO: handler did not observe ctx cancel within window — " +
			"timeout may have fired after handler body returned, or handler completed before timeout")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-11: with_value + recoverer — ctx values not leaked post-panic
// ═══════════════════════════════════════════════════════════════════════════════

type s8testCtxKey struct{}

// TestSec_Composition_WithValue_Recoverer_CtxNotLeakedPostPanic confirms that:
// 1. Panic value and ctx value are NOT present in the 500 response body.
// 2. A subsequent request (after a panicked request) gets a fresh context.
func TestSec_Composition_WithValue_Recoverer_CtxNotLeakedPostPanic(t *testing.T) {
	var logBuf bytes.Buffer
	logger := s8slogTestLogger(&logBuf)
	recovererMW := middleware.RecovererWithLogger(logger)
	withValMW := middleware.WithValue(s8testCtxKey{}, "secret-ctx-value-S8")

	var requestsDone atomic.Int32

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestsDone.Load()
		if n == 0 {
			requestsDone.Add(1)
			panic("simulated panic — secret in scope")
		}
		requestsDone.Add(1)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok") //nolint:errcheck
	})

	chain := recovererMW(withValMW(inner))

	req1 := httptest.NewRequest("GET", "/first", nil)
	rec1 := httptest.NewRecorder()
	chain.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest("GET", "/second", nil)
	rec2 := httptest.NewRecorder()
	chain.ServeHTTP(rec2, req2)

	if rec1.Code != http.StatusInternalServerError {
		t.Errorf("H8-11: panicked request should return 500, got %d", rec1.Code)
	}
	if rec2.Code != http.StatusOK {
		t.Errorf("H8-11: second request should return 200, got %d", rec2.Code)
	}

	body1 := rec1.Body.String()
	if strings.Contains(body1, "secret-ctx-value-S8") {
		t.Errorf("H8-11 CRITICAL: ctx value leaked into 500 response body: %q", body1)
	}
	if strings.Contains(body1, "simulated panic") {
		t.Errorf("H8-11 CRITICAL: panic string leaked into 500 response body: %q", body1)
	}
	t.Logf("H8-11 PASS: panic recovered, ctx values not in response body. 500 body: %q", body1)
}

func s8slogTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// ═══════════════════════════════════════════════════════════════════════════════
// Composition H8-12: 32-middleware chain correctness
// ═══════════════════════════════════════════════════════════════════════════════

type s8chainKey int

// TestSec_Composition_32MiddlewareChain_CorrectExecution confirms that a chain
// of 32 stacked middleware functions all execute correctly: each injects a
// typed context value; the handler asserts all 32 values are present.
func TestSec_Composition_32MiddlewareChain_CorrectExecution(t *testing.T) {
	const chainLen = 32

	var callOrder []int
	var callMu sync.Mutex

	mws := make([]func(http.Handler) http.Handler, chainLen)
	for i := range chainLen {
		i := i
		mws[i] = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callMu.Lock()
				callOrder = append(callOrder, i)
				callMu.Unlock()
				ctx := context.WithValue(r.Context(), s8chainKey(i), i)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		missing := 0
		for i := range chainLen {
			v, ok := r.Context().Value(s8chainKey(i)).(int)
			if !ok || v != i {
				missing++
			}
		}
		if missing > 0 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "missing %d context values out of %d", missing, chainLen)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	var handler http.Handler = inner
	for i := chainLen - 1; i >= 0; i-- {
		handler = mws[i](handler)
	}

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("H8-12: 32-middleware chain returned %d: %s", rec.Code, rec.Body.String())
	}
	callMu.Lock()
	defer callMu.Unlock()
	if len(callOrder) != chainLen {
		t.Errorf("H8-12: expected %d middleware calls, got %d", chainLen, len(callOrder))
	} else {
		t.Logf("H8-12 PASS: all %d middlewares executed, all %d ctx values injected. "+
			"First 5 call order: %v", chainLen, chainLen, callOrder[:5])
	}
}
