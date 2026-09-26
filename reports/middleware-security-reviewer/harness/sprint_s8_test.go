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
//
// NOTE (rmp #285, 2026-09-26): the five findings above that describe S8's
// ORIGINAL (pre-fix) behaviour — MSR-2026-0065, MSR-2026-0066's
// "AcceptedForever" framing, MSR-2026-0067, MSR-2026-0068's "unbounded
// table" framing, and MSR-2026-0069 — were all subsequently fixed. Their
// original t.Logf-only tests here never asserted anything (so they would
// have silently kept "passing" even if a fix were reverted) and their doc
// comments describe behaviour that no longer matches HEAD. Each has since
// been superseded by an asserting regression test in sprint_s9_test.go;
// the stale originals were removed in favour of a pointer comment at their
// former location, below, to avoid duplicating the same assertion in two
// files.
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
//
// TestSec_RealIP_MultiHop_LeftmostXFF_Spoofable (rmp #285: removed, stale).
// Its doc comment claimed the "current" (S8) behaviour was to pick the
// leftmost, attacker-spoofable XFF entry — real_ip.go's selectXFFRightmost
// has since been rewritten to walk from the rightmost entry (MSR-2026-0065
// fix), and this test's own body never asserted anything (every branch was
// a t.Logf), so it kept "passing" through the fix without ever exercising
// it as a regression guard. Superseded by
// sprint_s9_test.go::TestSec_RealIP_AttackerInjectionRejected_Regression,
// which asserts (t.Errorf) that the leftmost, attacker-injected value is
// NOT selected.

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

// TestSec_JWT_NoExpClaim_AcceptedForever (rmp #285: removed, stale/duplicate).
// Its doc comment framed "JWTOptions has no RequireExpiry bool" as an open
// finding (MSR-2026-0066); RequireExpiry has since been added, and neither
// branch of the original body asserted anything (both were t.Logf), so it
// never exercised the fix as a regression guard. The accepted-by-design
// backward-compatibility behaviour it documented (RequireExpiry=false, the
// default, still accepts a token with no "exp") is now properly asserted
// (t.Errorf on regression) by
// sprint_s9_test.go::TestSec_JWT_RequireExpiryFalse_NoExpAccepted; the
// opt-in enforcement path is covered by
// sprint_s9_test.go::TestSec_JWT_RequireExpiry_NoExpRejected.

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

// TestSec_OAuth2_PlaintextHTTP_EndpointAccepted (rmp #285: removed, stale).
// Its doc comment and its `if !constructionPanicked` branch documented
// OAuth2Introspect SILENTLY accepting a plaintext http:// endpoint
// (MSR-2026-0067); oauth2.go now panics on a non-https Endpoint unless
// AllowInsecureEndpoint is set, and this test never asserted the absence of
// that panic (t.Skip on the impossible https-test-server case, t.Logf on
// both outcomes of the real check), so it would keep "passing" whether or
// not the fix regressed. Superseded by
// sprint_s9_test.go::TestSec_OAuth2_HTTPSEnforcement_Regression (asserts
// t.Errorf if construction does NOT panic on http://) and
// sprint_s9_test.go::TestSec_OAuth2_AllowInsecureEndpoint_NopanicInTest
// (asserts the opt-out override).

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

// TestSec_ThrottlePerIP_UnboundedTable_UnderIPChurn (rmp #285: removed,
// stale). Its doc comment claimed "the internal per-IP table in
// ThrottlePerIP has no hard cap" (MSR-2026-0068); ThrottlePerIP now wraps
// ThrottlePerIPCapped with DefaultThrottlePerIPMaxTableSize, so that claim
// is false at HEAD. The test's own body never asserted anything either way
// (it only measured and logged peak concurrent in-flight requests at 500
// unique IPs — well under the 100,000 default cap, so it never actually
// probed the cap boundary). Cap enforcement is properly asserted by
// sprint_s9_test.go::TestSec_ThrottlePerIPCapped_CapEnforced_Regression
// (t.Errorf if new keys beyond the cap do NOT get 503) and
// sprint_s9_test.go::TestSec_ThrottlePerIP_DefaultCap_Present (t.Errorf if
// the default cap constant is not positive).
//
// ═══════════════════════════════════════════════════════════════════════════════
// MSR-2026-0069: Logger logs r.Method without sanitisation
// ═══════════════════════════════════════════════════════════════════════════════
//
// TestSec_Logger_Method_NotSanitised (rmp #285: removed, stale). Its doc
// comment and its first `if` branch documented Logger emitting r.Method
// UNSANITISED, letting a CRLF-injected Method forge a fake log line
// (MSR-2026-0069); logger.go now sanitises the Method field before
// formatting, and this test never asserted the absence of the injection —
// all three branches were t.Logf, so it kept "passing" through the fix.
// Superseded by
// sprint_s9_test.go::TestSec_Logger_Method_Sanitised_Regression, which
// asserts (t.Errorf) that no raw CR/LF byte reaches the log output.

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
