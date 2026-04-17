package middleware_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

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

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anyone.com" {
		t.Errorf("CORS wildcard: Access-Control-Allow-Origin = %q, want origin echoed", got)
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
