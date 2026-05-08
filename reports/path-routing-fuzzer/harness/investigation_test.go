package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestS9_PRF001_ChangeAnalysis — deep analysis of PRF-001 behaviour change
// In the PREVIOUS audit (Sprint S1-S8, pre-32d3c77), /static/../admin with
// CleanPath Pre() re-routed to /admin handler. Now the test shows code=403, handler="".
// The auth middleware (which returns 403 and does NOT call next) is blocking it.
// This means CleanPath IS still re-routing to /admin, but auth IS running and blocking.
// That's the MITIGATION — auth runs BEFORE /admin handler.
// But the question is: does the router actually reach /admin now?
func TestInvestigation_PRF001_RouteDestination(t *testing.T) {
	// Without auth — just CleanPath and two handlers.
	r := mm.New()
	r.Pre(middleware.CleanPath())
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	paths := []string{
		"/static/../admin",
		"/static/..%2fadmin",
		"/static/%2e%2e/admin",
		"/static/./../../admin",
	}
	for _, p := range paths {
		req := httptest.NewRequest("GET", "http://example.com"+p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		t.Logf("PRF001-ANALYSIS: %q → code=%d handler=%q", p, w.Code, w.Header().Get("X-Handler"))
	}
	// PRF-001 behaviour: does CleanPath still re-route these to /admin?
	// If yes (handler=admin), this confirms PRF-001 is still present (documented).
	// If no (handler=static), PRF-001 has been fixed.
}

// TestInvestigation_PRF001_WithAuth_AuthOrderCheck
// With auth middleware via Use() — does auth run before or after handler dispatch?
func TestInvestigation_PRF001_AuthOrder(t *testing.T) {
	// Scenario: auth middleware that PASSES (calls next)
	sequence := []string{}
	passingAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sequence = append(sequence, "auth")
			next.ServeHTTP(w, r)
		})
	}

	r := mm.New()
	r.Pre(middleware.CleanPath())
	r.Use(passingAuth)
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		sequence = append(sequence, "admin-handler")
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		sequence = append(sequence, "static-handler")
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	sequence = nil
	code, handler := func() (int, string) {
		req := httptest.NewRequest("GET", "http://example.com/static/../admin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Handler")
	}()
	t.Logf("PRF001-AUTH-ORDER: /static/../admin → code=%d handler=%q sequence=%v", code, handler, sequence)
	// If sequence contains both "auth" and "admin-handler" — auth ran for /admin (mitigated)
	// If sequence only contains "static-handler" — CleanPath didn't re-route (fixed?)
	// If sequence is ["auth", "admin-handler"] — PRF-001 still present but auth-guarded
}

// TestInvestigation_S9H01_FullRevalidation — comprehensive PRF-001 revalidation
func TestInvestigation_S9H01_FullRevalidation(t *testing.T) {
	// Without any middleware — baseline traversal handling
	r := mm.New()
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})
	r.GET("/static/*filepath", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "static")
		w.WriteHeader(200)
	})

	// Without CleanPath — traversal paths should NOT reach /admin
	noCleanPaths := []struct{ path, expected string }{
		{"/static/../admin", "static"},       // literal traversal stays in catch-all
		{"/static/..%2fadmin", "static"},     // decoded by net/url → /static/../admin → stays in catch-all
		{"/static/%2e%2e/admin", "static"},   // decoded by net/url → /static/../admin → stays in catch-all
	}
	for _, tc := range noCleanPaths {
		req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		handler := w.Header().Get("X-Handler")
		t.Logf("NO-CLEANPATH: %q → code=%d handler=%q (expected %q)", tc.path, w.Code, handler, tc.expected)
		if handler == "admin" {
			t.Errorf("NO-CLEANPATH BYPASS: %q reached /admin without CleanPath (should stay in catch-all)", tc.path)
		}
	}
}

// TestInvestigation_StripSlashes_RawPath_EncodedSlash
// S9-07 found that /api/users%2f (with encoded trailing slash) returns 404.
// This is because StripSlashes only strips LITERAL '/', not %2f.
// Verify this is the correct behaviour.
func TestInvestigation_StripSlashes_EncodedSlash(t *testing.T) {
	r := mm.New()
	r.UseRawPath = true
	r.Pre(middleware.StripSlashes())
	r.GET("/api/users", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "users")
		w.Header().Set("X-RawPath", req.URL.RawPath)
		w.WriteHeader(200)
	})

	// /api/users%2f — encoded slash at end
	// StripSlashes checks for literal '/' at end of r.URL.Path.
	// After net/url decode: Path=/api/users/, RawPath=/api/users%2f
	// StripSlashes strips trailing '/' from Path → /api/users
	// StripSlashes also strips trailing '/' from RawPath... but RawPath ends with %2f, not '/'
	// So Path becomes /api/users, RawPath stays /api/users%2f
	// UseRawPath=true → dispatch uses RawPath=/api/users%2f → no match for /api/users
	req := httptest.NewRequest("GET", "http://example.com/api/users/", nil)
	req.URL.Path = "/api/users/"
	req.URL.RawPath = "/api/users%2f"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	t.Logf("STRIPSLASHES-ENC: Path=/api/users/, RawPath=/api/users%%2f → code=%d handler=%q rawAfter=%q",
		w.Code, w.Header().Get("X-Handler"), w.Header().Get("X-RawPath"))
	// Expected: 404 because RawPath=/api/users%2f doesn't match /api/users
	// This is a subtle: %2f at end of RawPath is NOT treated as trailing slash.
	// Could cause user confusion: they might expect this to hit /api/users.
	if w.Code == 200 {
		t.Logf("Note: /api/users%%2f matched (StripSlashes stripped encoded slash?)")
	}
}

// TestInvestigation_CRLFCarriageReturn — CRLF in :param value
func TestInvestigation_CRLF_ParamValue(t *testing.T) {
	r := mm.New()
	var capturedID string
	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "users")
		// Try to set a header using the param value
		w.Header().Set("X-User-ID", capturedID)
		w.WriteHeader(200)
	})

	// CRLF in param — net/url.Parse sanitises control chars but let's verify
	testCases := []struct {
		desc string
		path string
	}{
		{"%0d%0a in id", "/users/evil%0d%0aX-Injected:%20hacked"},
		{"%0a only in id", "/users/abc%0aX-Injected:%20hacked"},
	}
	for _, tc := range testCases {
		capturedID = ""
		req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		// net/http sanitises headers — X-Injected should NOT appear
		injected := w.Header().Get("X-Injected")
		t.Logf("CRLF-PARAM: %s → code=%d id=%q injected=%q", tc.desc, w.Code, capturedID, injected)
		if injected == "hacked" {
			t.Errorf("CRLF-INJECT: CRLF in param value %q leaked into response header X-Injected", tc.path)
		}
	}
}

// TestInvestigation_UnicodeNFD_CFD_Attacks
// NFD vs NFC normalisation: é can be encoded as U+00E9 (NFC) or e+U+0301 (NFD).
// The tree stores routes as raw bytes — no normalisation. Test both forms.
func TestInvestigation_UnicodeNormalisation(t *testing.T) {
	r := mm.New()
	// Register /café (NFC: U+00E9 = \xc3\xa9)
	nfcPath := "/caf\xc3\xa9"
	r.GET(nfcPath, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "cafe-nfc")
		w.WriteHeader(200)
	})
	r.GET("/admin", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Handler", "admin")
		w.WriteHeader(200)
	})

	// NFD form of é: e (0x65) + combining acute (U+0301 = \xcc\x81)
	nfdPath := "/cafe\xcc\x81"
	req1 := httptest.NewRequest("GET", "http://example.com"+nfcPath, nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	t.Logf("UNICODE-NFC: %q → code=%d handler=%q", nfcPath, w1.Code, w1.Header().Get("X-Handler"))

	req2 := &http.Request{
		Method: "GET",
		URL:    &url.URL{Path: nfdPath},
		Header: make(http.Header),
	}
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	t.Logf("UNICODE-NFD: %q → code=%d handler=%q", nfdPath, w2.Code, w2.Header().Get("X-Handler"))
	// NFD must NOT match NFC-registered route (no normalisation in tree)
	if w2.Header().Get("X-Handler") == "cafe-nfc" {
		t.Errorf("UNICODE-NFD-MATCH: NFD form %q matched NFC-registered route %q — Unicode normalisation bypass", nfdPath, nfcPath)
	}
}
