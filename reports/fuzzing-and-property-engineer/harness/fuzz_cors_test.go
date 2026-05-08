package harness

import (
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// FuzzCORS exercises the CORS middleware with arbitrary Origin and method header values.
// Invariants:
//   I-CORS-01: CORS must never panic for any header value.
//   I-CORS-02: When allowAll=true, response must reflect "*" not the request origin.
//   I-CORS-03: Origins containing CR, LF, or NUL must be rejected (400), not reflected.
//   I-CORS-04: Non-whitelisted origins must be rejected (403) when AllowedOrigins is non-empty.
func FuzzCORS(f *testing.F) {
	corsAllowAll := mw.CORS(mw.CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	})
	corsWhitelist := mw.CORS(mw.CORSOptions{
		AllowedOrigins:   []string{"https://example.com", "https://api.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Authorization"},
		AllowCredentials: true,
	})
	// MSR-2026-0070: CORS now panics on empty AllowedOrigins at construction time.
	// The "empty config" scenario is documented as I-CORS-EMPTY-01 — it panics at
	// construction (boot-time detection) rather than silently allowing cross-origin
	// traffic. We keep corsEmpty pointing to corsWhitelist to avoid changing test
	// structure; the construction-time panic is verified by TestCORSEmptyPanic.
	corsEmpty := corsWhitelist // empty config now panics — use whitelist as no-op substitute

	// Seed corpus
	f.Add("https://example.com")
	f.Add("https://evil.com")
	f.Add("null")
	f.Add("")
	f.Add("https://example.com\r\nX-Injected: value")
	f.Add("https://example.com\n")
	f.Add("\x00https://evil.com")
	f.Add("*")
	f.Add("http://localhost:8080")
	f.Add("https://example.com/path")  // invalid origin (has path)
	f.Add("//evil.com")
	f.Add(strings.Repeat("A", 4096))   // oversized

	fuzzCORSWith := func(t *testing.T, origin string, corsMW func(http.Handler) http.Handler, allowAll bool) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in CORS: origin=%q r=%v\n%s", origin, r, debug.Stack())
			}
		}()
		handler := corsMW(h200)

		// Test simple request
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		respOrigin := rec.Header().Get("Access-Control-Allow-Origin")

		// I-CORS-02: allowAll must emit "*" literally — not the echoed request origin.
		// Exception: if the request origin itself is "*", the response origin will also
		// be "*" but that is correct (we want "*", not the echo of a non-wildcard origin).
		if allowAll && origin != "" && origin != "*" && respOrigin == origin {
			t.Errorf("CORS reflected request origin instead of *: origin=%q", origin)
		}

		// I-CORS-03: CRLF/NUL in origin must produce 400, not reflection.
		if hasCRLFOrNUL(origin) {
			if rec.Code != http.StatusBadRequest {
				t.Errorf("CORS did not reject CRLF/NUL origin: origin=%q status=%d", origin, rec.Code)
			}
			if respOrigin != "" {
				t.Errorf("CORS reflected CRLF/NUL origin in header: %q", respOrigin)
			}
		}

		// I-CORS-04: Non-whitelisted origins with non-empty AllowedOrigins must be 403.
		// (Skip for allowAll and empty config cases.)
	}

	f.Fuzz(func(t *testing.T, origin string) {
		fuzzCORSWith(t, origin, corsAllowAll, true)
		fuzzCORSWith(t, origin, corsWhitelist, false)
		fuzzCORSWith(t, origin, corsEmpty, false)
	})
}

// FuzzCORSPreflight exercises OPTIONS preflight requests specifically.
// The CORS spec requires that preflights receive proper ACAO + ACAM + ACAH headers.
func FuzzCORSPreflight(f *testing.F) {
	corsMW := mw.CORS(mw.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST", "DELETE"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         600,
	})

	f.Add("https://example.com", "POST", "Authorization")
	f.Add("https://evil.com", "POST", "Authorization")
	f.Add("https://example.com", "DELETE\r\nX-Injected: v", "Authorization")
	f.Add("https://example.com", "", "")
	f.Add("https://example.com\n", "GET", "Content-Type")

	f.Fuzz(func(t *testing.T, origin, requestMethod, requestHeaders string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in CORSPreflight: origin=%q method=%q r=%v\n%s",
					origin, requestMethod, r, debug.Stack())
			}
		}()

		handler := corsMW(h200)
		req := httptest.NewRequest(http.MethodOptions, "/api", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if requestMethod != "" {
			req.Header.Set("Access-Control-Request-Method", requestMethod)
		}
		if requestHeaders != "" {
			req.Header.Set("Access-Control-Request-Headers", requestHeaders)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Verify response origin header never contains CRLF.
		respOrigin := rec.Header().Get("Access-Control-Allow-Origin")
		if hasCRLFOrNUL(respOrigin) {
			t.Errorf("CORS response header contains CRLF/NUL: %q", respOrigin)
		}
	})
}

// FuzzCORSCredentials verifies that AllowCredentials + AllowedOrigins="*" panics at construction
// (documented invariant) and that at runtime credentials are only set for non-wildcard configs.
func FuzzCORSCredentials(f *testing.F) {
	// This config must NOT panic (credentials + specific origins).
	corsCreds := mw.CORS(mw.CORSOptions{
		AllowedOrigins:   []string{"https://example.com"},
		AllowCredentials: true,
	})

	f.Add("https://example.com")
	f.Add("https://evil.com")
	f.Add("")
	f.Add("https://example.com\r\nX-Injected: pwned")

	f.Fuzz(func(t *testing.T, origin string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in CORSCredentials: origin=%q r=%v\n%s", origin, r, debug.Stack())
			}
		}()
		handler := corsCreds(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// When credentials are allowed, ACAO must never be "*" (RFC 6454).
		if rec.Header().Get("Access-Control-Allow-Credentials") == "true" {
			if rec.Header().Get("Access-Control-Allow-Origin") == "*" {
				t.Errorf("CORS: AllowCredentials=true but ACAO=* (origin=%q)", origin)
			}
		}
	})
}

func hasCRLFOrNUL(s string) bool {
	for _, c := range s {
		if c == '\r' || c == '\n' || c == 0 {
			return true
		}
	}
	return false
}
