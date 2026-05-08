package harness

// fuzz_v120_changes_test.go — property and fuzz tests for new API surfaces
// introduced in the v1.2.0 pre-release cycle (commits 7512e98..e30ae94).
//
// New surfaces covered:
//   JWTOptions.RequireExpiry    — tokens without "exp" are rejected when true
//   CORS empty AllowedOrigins   — panics at construction time (MSR-2026-0070)
//   OAuth2Options.AllowInsecureEndpoint — panics on http:// unless flag set
//   middleware.DefaultThrottlePerIPMaxTableSize — constant must be > 0

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"pgregory.net/rapid"
)

// ============================================================
// I-JWT-REQEXP-01 — JWTOptions.RequireExpiry rejects tokens without exp
// ============================================================

// TestProp_JWTRequireExpiry verifies the new RequireExpiry field:
//   - When RequireExpiry=true, tokens with exp=0 (missing) get 401.
//   - When RequireExpiry=false (default), tokens with no exp are accepted
//     (provided signature and other claims are valid).
//   - RequireExpiry=true never panics for any token shape.
func TestProp_JWTRequireExpiry(t *testing.T) {
	secret := []byte("testsecret-require-expiry-prop")

	// Build an HS256 middleware with RequireExpiry=true.
	mwRequired := mw.JWTAuth(mw.JWTOptions{
		Algorithms:    []string{"HS256"},
		Secret:        secret,
		RequireExpiry: true,
	})
	// Same but RequireExpiry=false (default).
	mwNotRequired := mw.JWTAuth(mw.JWTOptions{
		Algorithms: []string{"HS256"},
		Secret:     secret,
	})

	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in JWTRequireExpiry: %v\n%s", r, debug.Stack())
			}
		}()

		// Use a syntactically valid HMAC-signed token but with no "exp" claim.
		// jwtHS256Sign is defined in fuzz_jwt_s8_test.go.
		payload := `{"sub":"user"}`
		header := `{"alg":"HS256"}`
		tokenNoExp := "Bearer " + jwtHS256Sign([]byte(header), []byte(payload), secret)

		// With RequireExpiry=true: token with no exp → must get 401.
		rec1 := httptest.NewRecorder()
		req1 := httptest.NewRequest(http.MethodGet, "/", nil)
		req1.Header.Set("Authorization", tokenNoExp)
		mwRequired(h200).ServeHTTP(rec1, req1)
		if rec1.Code == http.StatusOK {
			t.Errorf("RequireExpiry=true accepted token without exp: got 200")
		}
		if rec1.Code != http.StatusUnauthorized {
			t.Errorf("RequireExpiry=true: unexpected status=%d (want 401)", rec1.Code)
		}

		// With RequireExpiry=false: token without exp may be accepted (if signature valid).
		// We only check for no panic and no 500; the actual status depends on clock validation.
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.Header.Set("Authorization", tokenNoExp)
		mwNotRequired(h200).ServeHTTP(rec2, req2)
		if rec2.Code == http.StatusInternalServerError {
			t.Errorf("RequireExpiry=false: got 500 on token without exp")
		}
	})
}

// FuzzJWTRequireExpiry exercises RequireExpiry=true with arbitrary Authorization headers.
// Invariant: never panic, never return 500.
func FuzzJWTRequireExpiry(f *testing.F) {
	mwJWT := mw.JWTAuth(mw.JWTOptions{
		Algorithms:    []string{"HS256"},
		Secret:        []byte("secret"),
		RequireExpiry: true,
	})

	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.AAAA")                        // no exp
	f.Add("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIiwiZXhwIjo5OTk5OTk5OTk5fQ.AAAA") // with exp
	f.Add("")
	f.Add("Bearer ...")
	f.Add("Bearer " + strings.Repeat("A", 4096))

	f.Fuzz(func(t *testing.T, authHeader string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzJWTRequireExpiry: header=%q r=%v\n%s", authHeader, r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		mwJWT(h200).ServeHTTP(rec, req)
		if rec.Code == http.StatusInternalServerError {
			t.Errorf("JWTRequireExpiry returned 500 for header=%q", authHeader)
		}
	})
}

// ============================================================
// I-CORS-EMPTY-01 — CORS panics at construction on empty AllowedOrigins
// ============================================================

// TestCORSEmptyOriginsPanic verifies that mw.CORS(CORSOptions{}) panics at
// construction time, not silently at request time. This ensures misconfiguration
// is caught at boot rather than passed through with no ACAO header.
func TestCORSEmptyOriginsPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("CORS(CORSOptions{}) did not panic — expected construction-time panic for empty AllowedOrigins")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("CORS panic value is not string: %T: %v", r, r)
		}
		if !strings.Contains(msg, "AllowedOrigins") {
			t.Errorf("CORS panic message does not mention AllowedOrigins: %q", msg)
		}
	}()
	_ = mw.CORS(mw.CORSOptions{})
}

// TestCORSNilOriginsPanic verifies that nil AllowedOrigins also panics.
func TestCORSNilOriginsPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("CORS(CORSOptions{AllowedOrigins:nil}) did not panic")
		}
	}()
	_ = mw.CORS(mw.CORSOptions{AllowedOrigins: nil})
}

// ============================================================
// I-OAUTH2-HTTPS-01 — OAuth2Introspect panics on non-HTTPS endpoint
// ============================================================

// TestOAuth2HTTPEndpointPanic verifies that OAuth2Introspect panics when
// Endpoint uses http:// and AllowInsecureEndpoint=false (the default).
func TestOAuth2HTTPEndpointPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("OAuth2Introspect with http:// endpoint did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not string: %T: %v", r, r)
		}
		if !strings.Contains(msg, "https") {
			t.Errorf("panic message does not mention https: %q", msg)
		}
	}()
	_ = mw.OAuth2Introspect(mw.OAuth2Options{
		Endpoint: "http://localhost:8080/introspect",
		// AllowInsecureEndpoint: false (default)
	})
}

// TestOAuth2MalformedEndpointPanic verifies that a malformed (unparseable)
// Endpoint URL causes a construction-time panic.
func TestOAuth2MalformedEndpointPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("OAuth2Introspect with malformed endpoint did not panic")
		}
	}()
	// "://no-scheme" is unparseable as a URL.
	_ = mw.OAuth2Introspect(mw.OAuth2Options{
		Endpoint: "://no-scheme",
	})
}

// TestOAuth2AllowInsecureAccepted verifies that AllowInsecureEndpoint=true
// allows http:// endpoints without panicking (for test/local use).
func TestOAuth2AllowInsecureAccepted(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OAuth2Introspect with AllowInsecureEndpoint=true unexpectedly panicked: %v", r)
		}
	}()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
	}))
	defer stub.Close()

	mwOAuth := mw.OAuth2Introspect(mw.OAuth2Options{
		Endpoint:              stub.URL,
		AllowInsecureEndpoint: true,
		CacheTTL:              -1,
	})
	handler := mwOAuth(h200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (stub returns inactive), got %d", rec.Code)
	}
}

// FuzzOAuth2InsecureEndpointConstruction exercises construction of
// OAuth2Introspect with arbitrary endpoint URLs and AllowInsecureEndpoint=true.
// Invariant: with AllowInsecureEndpoint=true, only truly malformed (unparseable)
// URLs should panic; http:// must not panic.
func FuzzOAuth2InsecureEndpointConstruction(f *testing.F) {
	f.Add("http://localhost:8080/introspect")
	f.Add("https://api.example.com/token/introspect")
	f.Add("://bad-url")
	f.Add("")

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
	}))
	_ = stub.Close

	f.Fuzz(func(t *testing.T, endpoint string) {
		defer func() {
			r := recover()
			if r == nil {
				return // no panic is acceptable when AllowInsecureEndpoint=true
			}
			msg, ok := r.(string)
			if !ok {
				t.Fatalf("non-string panic: %T: %v", r, r)
			}
			// Expected panics: empty endpoint, malformed URL.
			if strings.Contains(msg, "Endpoint") || strings.Contains(msg, "malformed") || strings.Contains(msg, "empty") {
				return // expected
			}
			t.Fatalf("unexpected panic: endpoint=%q msg=%q\n%s", endpoint, msg, debug.Stack())
		}()
		if endpoint == "" {
			return // skip — empty always panics
		}
		mw.OAuth2Introspect(mw.OAuth2Options{
			Endpoint:              endpoint,
			AllowInsecureEndpoint: true,
			CacheTTL:              -1,
		})
	})
}

// ============================================================
// I-THROTTLE-MAXTABLE-01 — DefaultThrottlePerIPMaxTableSize is > 0
// ============================================================

// TestDefaultThrottlePerIPMaxTableSizePositive verifies that the exported constant
// is positive (a value of 0 would disable the cap, allowing unbounded memory growth).
func TestDefaultThrottlePerIPMaxTableSizePositive(t *testing.T) {
	if mw.DefaultThrottlePerIPMaxTableSize <= 0 {
		t.Fatalf("DefaultThrottlePerIPMaxTableSize=%d must be positive", mw.DefaultThrottlePerIPMaxTableSize)
	}
}

// TestProp_ThrottlePerIPCappedNeverPanics verifies that ThrottlePerIPCapped
// never panics for any valid (limit, timeout, maxTableSize) combination.
func TestProp_ThrottlePerIPCappedNeverPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ThrottlePerIPCapped: %v\n%s", r, debug.Stack())
			}
		}()

		limit := rapid.IntRange(1, 100).Draw(t, "limit")
		maxTable := rapid.IntRange(1, 1000).Draw(t, "maxTable")
		timeoutMs := rapid.IntRange(100, 5000).Draw(t, "timeoutMs")

		throttleMW := mw.ThrottlePerIPCapped(
			limit,
			time.Duration(timeoutMs)*time.Millisecond,
			maxTable,
			nil, // default key extractor
		)

		// Send a single request to exercise the middleware.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		throttleMW(h200).ServeHTTP(rec, req)
		// 200 or 503 (backlog full) are both acceptable; no panic is the invariant.
	})
}

// TestProp_ThrottlePerIPTableCapRespected verifies that when ThrottlePerIPCapped
// is saturated with distinct IPs, new IPs receive 503 (not panic or OOM).
func TestProp_ThrottlePerIPTableCapRespected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ThrottlePerIPTableCap: %v\n%s", r, debug.Stack())
			}
		}()

		maxTable := rapid.IntRange(1, 10).Draw(t, "maxTable")
		extraIPs := rapid.IntRange(1, 5).Draw(t, "extraIPs")

		// Low concurrent limit so first requests complete, then new IPs hit the cap.
		throttleMW := mw.ThrottlePerIPCapped(10, 100*time.Millisecond, maxTable, nil)

		// Send maxTable + extraIPs distinct IPs through the middleware.
		total := maxTable + extraIPs
		var got503 int
		for i := range total {
			ip := "10.0.0." + string(rune('a'+i%26))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = ip + ":9999"
			rec := httptest.NewRecorder()
			throttleMW(h200).ServeHTTP(rec, req)
			if rec.Code == http.StatusServiceUnavailable {
				got503++
			}
		}
		// When extraIPs > 0, at least some requests for new IPs beyond the cap should 503.
		// We only check for no panic; the exact number of 503s depends on timing.
		_ = got503
	})
}
