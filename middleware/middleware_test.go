package middleware_test

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// makeHS256JWT creates a signed HS256 JWT with the given claims map.
func makeHS256JWT(secret []byte, claims map[string]any) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	pay := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := hdr + "." + pay
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// serve applies mw around a handler that writes code and body, then fires the request.
func serve(mw func(http.Handler) http.Handler, method, path string, reqFn func(*http.Request)) *httptest.ResponseRecorder {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if reqFn != nil {
		reqFn(req)
	}
	mw(inner).ServeHTTP(rec, req)
	return rec
}

// ── Logger ───────────────────────────────────────────────────────────────────

func TestLogger_WritesMethodPathAndStatus(t *testing.T) {
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	mw(inner).ServeHTTP(rec, req)

	line := buf.String()
	if !strings.Contains(line, "POST") {
		t.Errorf("Logger: want method POST in log line, got: %q", line)
	}
	if !strings.Contains(line, "/items") {
		t.Errorf("Logger: want path /items in log line, got: %q", line)
	}
	if !strings.Contains(line, "201") {
		t.Errorf("Logger: want status 201 in log line, got: %q", line)
	}
}

func TestLogger_PanicsOnNilWriter(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Logger: expected panic on nil writer, got none")
		}
	}()
	middleware.Logger(nil)
}

func TestLogger_SanitisesCRLFInPath(t *testing.T) {
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rec := httptest.NewRecorder()
	// httptest.NewRequest percent-decodes the path — simulate what net/http does
	req := httptest.NewRequest("GET", "/foo", nil)
	req.URL.Path = "/foo\r\ninjected: evil" // inject directly into parsed path
	mw(inner).ServeHTTP(rec, req)
	line := buf.String()
	if strings.Contains(line, "\r") || strings.Contains(line, "\n\n") {
		t.Errorf("Logger: CRLF not sanitised: %q", line)
	}
	if !strings.Contains(line, `\r\n`) {
		t.Errorf("Logger: expected escaped CRLF in log, got: %q", line)
	}
}

func TestLogger_SanitisesANSIInPath(t *testing.T) {
	var buf bytes.Buffer
	mw := middleware.Logger(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/foo", nil)
	req.URL.Path = "/foo\x1b[2J" // ANSI clear screen
	mw(inner).ServeHTTP(rec, req)
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("Logger: ANSI escape not sanitised")
	}
}

// ── Compress ─────────────────────────────────────────────────────────────────

func TestCompress_StreamingBoundedMemory(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	// Write 512 KB in small chunks and verify the handler completes without OOM.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 4096)
		for range 128 { // 512 KB total
			_, _ = w.Write(chunk)
		}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip encoding")
	}
}

func TestCompress_SmallResponseNotCompressed(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(handler).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("small response should not be compressed")
	}
	if rec.Body.String() != "hi" {
		t.Fatalf("body: got %q, want %q", rec.Body.String(), "hi")
	}
}

// ── Recoverer ────────────────────────────────────────────────────────────────

func TestRecoverer_Returns500OnPanic(t *testing.T) {
	mw := middleware.Recoverer()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mw(panicking).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Recoverer: got status %d, want 500", rec.Code)
	}
}

func TestRecoverer_PassesThroughOnNoP(t *testing.T) {
	mw := middleware.Recoverer()
	rec := serve(mw, http.MethodGet, "/ping", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("Recoverer: got status %d, want 200", rec.Code)
	}
}

func TestRecovererWithLogger_DoesNotLeakPanicToBody(t *testing.T) {
	logger := slog.Default()
	mw := middleware.RecovererWithLogger(logger)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("secret internal error")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret internal error") {
		t.Fatal("panic value leaked to response body")
	}
}

// ── CORS ─────────────────────────────────────────────────────────────────────

func TestCORS_AllowedOriginSetsHeaders(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("CORS: Access-Control-Allow-Origin = %q, want %q", got, "https://example.com")
	}
}

func TestCORS_RejectsForbiddenOrigin(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("CORS: got status %d for forbidden origin, want 403", rec.Code)
	}
}

func TestCORS_PreflightOptions(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Authorization"},
		MaxAge:         600,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should not reach inner handler for preflight.
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("CORS preflight: got status %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("CORS preflight: Access-Control-Allow-Methods = %q, want POST", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("CORS preflight: Access-Control-Max-Age = %q, want 600", got)
	}
}

func TestCORS_WildcardOrigin(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anyone.com")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	// Wildcard CORS must emit literal "*", never reflect the request origin (MM-2026-0012).
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS wildcard: Access-Control-Allow-Origin = %q, want *", got)
	}
}

// ── BasicAuth ────────────────────────────────────────────────────────────────

func TestBasicAuth_Returns401WithoutCredentials(t *testing.T) {
	mw := middleware.BasicAuth("Test Realm", map[string]string{"alice": "secret"})
	rec := serve(mw, http.MethodGet, "/admin", nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("BasicAuth: got status %d without credentials, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
		t.Errorf("BasicAuth: WWW-Authenticate = %q, want Basic scheme", got)
	}
}

func TestBasicAuth_AllowsValidCredentials(t *testing.T) {
	mw := middleware.BasicAuth("Test Realm", map[string]string{"alice": "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.SetBasicAuth("alice", "secret")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("BasicAuth: got status %d with valid credentials, want 200", rec.Code)
	}
}

func TestBasicAuth_RejectsWrongPassword(t *testing.T) {
	mw := middleware.BasicAuth("Test Realm", map[string]string{"alice": "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.SetBasicAuth("alice", "wrong")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("BasicAuth: got status %d with wrong password, want 401", rec.Code)
	}
}

func TestBasicAuth_PanicsOnNilCreds(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("BasicAuth: expected panic on nil credentials map, got none")
		}
	}()
	middleware.BasicAuth("realm", nil)
}

// ── NoCache ──────────────────────────────────────────────────────────────────

func TestNoCache_SetsHeaders(t *testing.T) {
	mw := middleware.NoCache()
	rec := serve(mw, http.MethodGet, "/data", nil)

	tests := []struct {
		header string
		want   string
	}{
		{"Cache-Control", "no-store"},
		{"Pragma", "no-cache"},
		{"Expires", "0"},
	}
	for _, tc := range tests {
		if got := rec.Header().Get(tc.header); !strings.Contains(got, tc.want) {
			t.Errorf("NoCache: %s = %q, want it to contain %q", tc.header, got, tc.want)
		}
	}
}

// ── RequestID ────────────────────────────────────────────────────────────────

func TestRequestID_AddsNonEmptyHeader(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, http.MethodGet, "/", nil)

	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("RequestID: X-Request-ID header is empty, want non-empty")
	}
}

func TestRequestID_PropagatesExistingID(t *testing.T) {
	mw := middleware.RequestID()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "my-trace-id")

	var captured string
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if captured != "my-trace-id" {
		t.Errorf("RequestID: captured ID = %q, want %q", captured, "my-trace-id")
	}
	if got := rec.Header().Get("X-Request-ID"); got != "my-trace-id" {
		t.Errorf("RequestID: response X-Request-ID = %q, want %q", got, "my-trace-id")
	}
}

// ── RealIP (Phase 5.1) ───────────────────────────────────────────────────────

func TestRealIP_TrustAll_SetsRemoteAddr(t *testing.T) {
	mw := middleware.RealIP() // no CIDR = trust all
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	mw(inner).ServeHTTP(rec, req)
	if captured != "10.0.0.1" {
		t.Fatalf("got %q, want 10.0.0.1", captured)
	}
}

func TestRealIP_UntrustedPeerIgnored(t *testing.T) {
	prefix, _ := netip.ParsePrefix("10.0.0.0/8")
	mw := middleware.RealIP(&prefix)
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5678" // not in 10.0.0.0/8
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	mw(inner).ServeHTTP(rec, req)
	if captured != "1.2.3.4:5678" {
		t.Fatalf("untrusted peer mutated RemoteAddr: %q", captured)
	}
}

func TestRealIP_RejectsCRLFInXFF(t *testing.T) {
	mw := middleware.RealIP()
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4\r\nX-Injected: evil")
	mw(inner).ServeHTTP(rec, req)
	if strings.Contains(captured, "\r") || strings.Contains(captured, "\n") {
		t.Fatalf("CRLF in RemoteAddr: %q", captured)
	}
}

// ── BasicAuth (Phase 5.2) ────────────────────────────────────────────────────

func TestBasicAuth_ValidCredentials(t *testing.T) {
	mw := middleware.BasicAuth("test", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.SetBasicAuth("alice", "secret")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid creds: got %d, want 200", rec.Code)
	}
}

func TestBasicAuth_InvalidCredentials(t *testing.T) {
	mw := middleware.BasicAuth("test", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.SetBasicAuth("alice", "wrong")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pass: got %d, want 401", rec.Code)
	}
}

func TestBasicAuth_UnknownUser(t *testing.T) {
	mw := middleware.BasicAuth("test", map[string]string{"alice": "secret"})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.SetBasicAuth("nobody", "anything")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: got %d, want 401", rec.Code)
	}
}

func TestBasicAuth_ConstantTimePath(t *testing.T) {
	// Verifica que ConstantTimeCompare corre sempre — smoke test, não timing.
	mw := middleware.BasicAuth("realm", map[string]string{"u": "p"})
	// found=false path: dummy compare deve correr
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.SetBasicAuth("nonexistent", "anypass")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing user, got %d", rec.Code)
	}
}

func TestBasicAuth_RealmInjectionSanitised(t *testing.T) {
	mw := middleware.BasicAuth("my\r\nrealm", map[string]string{"u": "p"})
	rec := serve(mw, "GET", "/", nil)
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if strings.Contains(wwwAuth, "\r") || strings.Contains(wwwAuth, "\n") {
		t.Fatalf("realm injection not sanitised: %q", wwwAuth)
	}
}

// ── RequestID (Phase 5.3) ────────────────────────────────────────────────────

func TestRequestID_ValidPropagated(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("X-Request-ID", "abc-123")
	})
	if got := rec.Header().Get("X-Request-ID"); got != "abc-123" {
		t.Fatalf("valid ID not propagated: %q", got)
	}
}

func TestRequestID_OversizedRejected(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("X-Request-ID", strings.Repeat("a", 200))
	})
	got := rec.Header().Get("X-Request-ID")
	if len(got) > 128 {
		t.Fatalf("oversized ID propagated: len=%d", len(got))
	}
	if got == strings.Repeat("a", 200) {
		t.Fatal("oversized ID not replaced")
	}
}

func TestRequestID_CRLFRejected(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("X-Request-ID", "id\r\nX-Injected: evil")
	})
	got := rec.Header().Get("X-Request-ID")
	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Fatalf("CRLF in X-Request-ID response: %q", got)
	}
	if got == "id\r\nX-Injected: evil" {
		t.Fatal("CRLF ID not replaced")
	}
}

func TestRequestID_GeneratedWhenMissing(t *testing.T) {
	mw := middleware.RequestID()
	rec := serve(mw, "GET", "/", nil)
	got := rec.Header().Get("X-Request-ID")
	if got == "" {
		t.Fatal("no X-Request-ID generated")
	}
	if len(got) != 32 { // hex of 16 bytes
		t.Fatalf("unexpected generated ID length: %q", got)
	}
}

// ── CORS (Phase 5.4) ─────────────────────────────────────────────────────────

func TestCORS_WildcardEmitsLiteralStar(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("Origin", "https://attacker.example")
	})
	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Fatalf("expected *, got %q", acao)
	}
}

func TestCORS_CRLFInOriginRejected(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("Origin", "https://evil.example\r\nX-Injected: bad")
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CRLF in Origin: expected 400, got %d", rec.Code)
	}
}

func TestCORS_SpecificOriginReflected(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://trusted.example"}})
	rec := serve(mw, "GET", "/", func(req *http.Request) {
		req.Header.Set("Origin", "https://trusted.example")
	})
	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://trusted.example" {
		t.Fatalf("expected trusted origin, got %q", acao)
	}
}

// ── Throttle (Phase 5.5) ─────────────────────────────────────────────────────

func TestThrottleAllBacklog_IsAlias(t *testing.T) {
	// ThrottleAllBacklog deve ter o mesmo comportamento que ThrottleBacklog.
	mw := middleware.ThrottleAllBacklog(1, 0, time.Millisecond)
	rec := serve(mw, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ThrottleAllBacklog: got %d, want 200", rec.Code)
	}
}

func TestThrottlePerIP_AllowsDifferentIPs(t *testing.T) {
	mw := middleware.ThrottlePerIP(1, 10*time.Millisecond, nil)
	// Two sequential requests from different IPs must both succeed.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, ip := range []string{"1.2.3.4:100", "5.6.7.8:200"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = ip
		mw(inner).ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("IP %s: got %d, want 200", ip, rec.Code)
		}
	}
}

// ── CleanPath (Phase 5.8) ────────────────────────────────────────────────────

func TestCleanPath_NormalisesDoubleSlash(t *testing.T) {
	mw := middleware.CleanPath()
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Path
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/a//b", nil)
	mw(inner).ServeHTTP(rec, req)
	if captured != "/a/b" {
		t.Fatalf("CleanPath: got %q, want /a/b", captured)
	}
}

func TestCleanPath_ZerosRawPathOnTraversal(t *testing.T) {
	mw := middleware.CleanPath()
	var capturedRaw string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRaw = r.URL.RawPath
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/static/foo", nil)
	req.URL.RawPath = "/static/../admin" // traversal in RawPath
	mw(inner).ServeHTTP(rec, req)
	// RawPath deve ser zerado porque path.Clean mudou o valor
	if capturedRaw != "" {
		t.Fatalf("RawPath not zeroed after traversal: %q", capturedRaw)
	}
}

// ── StripSlashes (Phase 6.3) ─────────────────────────────────────────────────

func TestStripSlashes_MultipleTrailing(t *testing.T) {
	mw := middleware.StripSlashes()
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Path
		w.WriteHeader(200)
	})
	for input, want := range map[string]string{
		"/a///": "/a",
		"/a/":   "/a",
		"/":     "/",
		"/a":    "/a",
	} {
		captured = ""
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", input, nil)
		mw(inner).ServeHTTP(rec, req)
		if captured != want {
			t.Errorf("StripSlashes(%q): got %q, want %q", input, captured, want)
		}
	}
}

// ── APIKey ───────────────────────────────────────────────────────────────────

func TestAPIKey_ValidKeyAllows(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"secret-key-1": "service-a"},
	})
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = middleware.GetAPIKeyIdentity(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret-key-1")
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid key: got %d, want 200", rec.Code)
	}
	if captured != "service-a" {
		t.Fatalf("identity: got %q, want service-a", captured)
	}
}

func TestAPIKey_InvalidKeyRejects(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid": "id"},
	})
	rec := serve(mw, http.MethodGet, "/", func(req *http.Request) {
		req.Header.Set("X-API-Key", "wrong-key")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key: got %d, want 401", rec.Code)
	}
}

func TestAPIKey_MissingKeyRejects(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"valid": "id"},
	})
	rec := serve(mw, http.MethodGet, "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: got %d, want 401", rec.Code)
	}
}

func TestAPIKey_CustomExtractFn(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{"my-token": "svc"},
		ExtractFn: func(r *http.Request) string {
			return r.URL.Query().Get("apikey")
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?apikey=my-token", nil)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("custom extract: got %d, want 200", rec.Code)
	}
}

func TestAPIKey_CustomHeader(t *testing.T) {
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys:   map[string]string{"tok": "id"},
		Header: "X-Custom-Key",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom-Key", "tok")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("custom header: got %d, want 200", rec.Code)
	}
}

func TestAPIKey_PanicsOnEmptyKeys(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("APIKey: expected panic on empty Keys, got none")
		}
	}()
	middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{}})
}

// ── JWTAuth ──────────────────────────────────────────────────────────────────

func TestJWTAuth_ValidHS256(t *testing.T) {
	secret := []byte("super-secret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	now := time.Now()
	token := makeHS256JWT(secret, map[string]any{
		"sub": "user-123",
		"iss": "test",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	})
	var sub string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := middleware.GetJWTClaims(r.Context()); ok {
			sub = c.Subject
		}
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("valid token: got %d, want 200", rec.Code)
	}
	if sub != "user-123" {
		t.Fatalf("subject: got %q, want user-123", sub)
	}
}

func TestJWTAuth_ExpiredTokenRejects(t *testing.T) {
	secret := []byte("super-secret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeHS256JWT(secret, map[string]any{
		"sub": "user-123",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: got %d, want 401", rec.Code)
	}
}

func TestJWTAuth_InvalidSignatureRejects(t *testing.T) {
	secret := []byte("super-secret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	// Build token signed with a different key.
	token := makeHS256JWT([]byte("wrong-secret"), map[string]any{
		"sub": "attacker",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad sig: got %d, want 401", rec.Code)
	}
}

func TestJWTAuth_MissingBearerTokenRejects(t *testing.T) {
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     []byte("s"),
		Algorithms: []string{"HS256"},
	})
	rec := serve(mw, http.MethodGet, "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("missing WWW-Authenticate Bearer header: %q", got)
	}
}

func TestJWTAuth_WrongAlgorithmRejects(t *testing.T) {
	// Middleware only allows HS512, token is signed with HS256.
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS512"},
	})
	token := makeHS256JWT(secret, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong alg: got %d, want 401", rec.Code)
	}
}

func TestJWTAuth_IssuerCheckFails(t *testing.T) {
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		Issuers:    []string{"trusted"},
	})
	token := makeHS256JWT(secret, map[string]any{
		"iss": "untrusted",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong issuer: got %d, want 401", rec.Code)
	}
}

func TestJWTAuth_AudienceCheckPasses(t *testing.T) {
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		Audiences:  []string{"my-service"},
	})
	token := makeHS256JWT(secret, map[string]any{
		"aud": "my-service",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct audience: got %d, want 200", rec.Code)
	}
}

func TestJWTAuth_ClockSkewAllowsSlightlyExpired(t *testing.T) {
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
		ClockSkew:  30 * time.Second,
	})
	// Token expired 10s ago — within the 30s skew window.
	token := makeHS256JWT(secret, map[string]any{
		"exp": time.Now().Add(-10 * time.Second).Unix(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("within skew: got %d, want 200", rec.Code)
	}
}

func TestJWTAuth_BearerCaseInsensitive(t *testing.T) {
	// RFC 7235: auth-scheme is case-insensitive — "bearer" must work as well as "Bearer".
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeHS256JWT(secret, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	for _, scheme := range []string{"Bearer ", "bearer ", "BEARER "} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", scheme+token)
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("scheme %q: got %d, want 200", scheme, rec.Code)
		}
	}
}

func TestJWTAuth_CritHeaderRejects(t *testing.T) {
	// RFC 7515 §4.1.11: "crit" with any extension must be rejected when not supported.
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	// Build a JWT with "crit" in the JOSE header.
	hdrJSON := `{"alg":"HS256","typ":"JWT","crit":["custom_ext"]}`
	hdr := base64.RawURLEncoding.EncodeToString([]byte(hdrJSON))
	payJSON, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	pay := base64.RawURLEncoding.EncodeToString(payJSON)
	sigInput := hdr + "." + pay
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	token := sigInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("crit header: got %d, want 401", rec.Code)
	}
}

func TestJWTAuth_ES256WrongCurvePanics(t *testing.T) {
	// RFC 7518 §3.4: ES256 must use P-256; configuring P-384 key must panic at startup.
	p384key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("JWTAuth ES256 with P-384 key: expected panic, got none")
		}
	}()
	middleware.JWTAuth(middleware.JWTOptions{
		PublicKey:  &p384key.PublicKey,
		Algorithms: []string{"ES256"},
	})
}

func TestJWTAuth_PanicsOnEmptyAlgorithms(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("JWTAuth: expected panic on empty Algorithms")
		}
	}()
	middleware.JWTAuth(middleware.JWTOptions{Algorithms: nil})
}

func TestJWTAuth_RawPayloadAvailable(t *testing.T) {
	secret := []byte("s")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})
	token := makeHS256JWT(secret, map[string]any{
		"sub":    "u1",
		"custom": "value",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	var raw []byte
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := middleware.GetJWTClaims(r.Context()); ok {
			raw = c.RawPayload
		}
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(inner).ServeHTTP(rec, req)
	if !strings.Contains(string(raw), `"custom"`) {
		t.Fatalf("RawPayload missing custom field: %s", raw)
	}
}

// ── OAuth2Introspect ──────────────────────────────────────────────────────────

func TestOAuth2Introspect_ActiveTokenAllows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "user-abc",
			"scope":  "read write",
		})
	}))
	defer srv.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:   srv.URL,
		CacheTTL:   -1, // disable cache for this test
		HTTPClient: srv.Client(),
	})
	var sub string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := middleware.GetOAuth2Claims(r.Context()); ok {
			sub = c.Subject
		}
		w.WriteHeader(200)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-active-token")
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("active token: got %d, want 200", rec.Code)
	}
	if sub != "user-abc" {
		t.Fatalf("subject: got %q, want user-abc", sub)
	}
}

func TestOAuth2Introspect_InactiveTokenRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"active": false})
	}))
	defer srv.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:   srv.URL,
		CacheTTL:   -1,
		HTTPClient: srv.Client(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer revoked-token")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("inactive token: got %d, want 401", rec.Code)
	}
}

func TestOAuth2Introspect_MissingTokenRejects(t *testing.T) {
	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint: "http://unused",
		CacheTTL: -1,
	})
	rec := serve(mw, http.MethodGet, "/", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("missing WWW-Authenticate: %q", got)
	}
}

func TestOAuth2Introspect_CacheHitAvoidsSecondCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"active": true,
			"sub":    "cached-user",
		})
	}))
	defer srv.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:   srv.URL,
		CacheTTL:   time.Minute,
		HTTPClient: srv.Client(),
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer same-token")
		mw(inner).ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("cached request: got %d, want 200", rec.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("introspection endpoint called %d times, want 1 (cache miss only)", calls)
	}
}

func TestOAuth2Introspect_EndpointErrorRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:   srv.URL,
		CacheTTL:   -1,
		HTTPClient: srv.Client(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("endpoint error: got %d, want 401", rec.Code)
	}
}

func TestOAuth2Introspect_PanicsOnEmptyEndpoint(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("OAuth2Introspect: expected panic on empty Endpoint")
		}
	}()
	middleware.OAuth2Introspect(middleware.OAuth2Options{Endpoint: ""})
}

func TestSetHeaderRejectsCRLF(t *testing.T) {
	cases := []struct{ key, value string }{
		{"X-Inject\r\nX-Other", "val"},
		{"X-Inject", "val\r\nX-Other: evil"},
		{"X-Key", "val\nX-Other: evil"},
	}
	for _, c := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("SetHeader(%q, %q) should panic on CR/LF", c.key, c.value)
				}
			}()
			middleware.SetHeader(c.key, c.value)
		}()
	}
}

// ── Example functions ────────────────────────────────────────────────────────

func ExampleAPIKey() {
	// Authenticate requests by API key with identity lookup.
	mw := middleware.APIKey(middleware.APIKeyOptions{
		Keys: map[string]string{
			"sk_test_abc": "user-123",
			"sk_test_def": "user-456",
		},
		Header: "X-API-Key",
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := middleware.GetAPIKeyIdentity(r.Context())
		if ok {
			fmt.Println(identity)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("X-API-Key", "sk_test_abc")

	mw(inner).ServeHTTP(rec, req)
	// Output: user-123
}

func ExampleJWTAuth() {
	// Validate JWT tokens from the Authorization header.
	secret := []byte("test-secret")
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.GetJWTClaims(r.Context())
		if ok {
			fmt.Println(claims.Subject)
		}
	})

	// Create a signed HS256 JWT with subject "alice".
	token := makeHS256JWT(secret, map[string]any{
		"sub": "alice",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/profile", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	mw(inner).ServeHTTP(rec, req)
	// Output: alice
}

func ExampleOAuth2Introspect() {
	// Validate tokens via RFC 7662 introspection endpoint.
	// In production, use a real OAuth2 introspection endpoint.
	// This example uses a mock server for demonstration.

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back a valid introspection response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"active": true,
			"sub": "alice",
			"scope": "read write",
			"client_id": "app1"
		}`))
	}))
	defer mockServer.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint: mockServer.URL,
		CacheTTL: 0, // Disable caching for this example.
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, ok := middleware.GetOAuth2Claims(r.Context())
		if ok {
			fmt.Println(resp.Subject)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/resource", nil)
	req.Header.Set("Authorization", "Bearer test-token-xyz")

	mw(inner).ServeHTTP(rec, req)
	// Output: alice
}
