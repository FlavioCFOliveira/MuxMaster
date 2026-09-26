package muxmaster

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteRedirect_ByteIdenticalToNetHTTPRedirect is the differential test
// for rmp task #248: writeRedirect (the direct-write replacement for
// net/http.Redirect on MuxMaster's hot redirect path) must produce output
// byte-identical to net/http.Redirect for every method, every redirect code
// MuxMaster can emit, and a wide range of targets — including targets that
// need HTML escaping, hex escaping of non-ASCII bytes, a query string, and
// targets that are not already in path.Clean-normalised form (exercising
// the safety-net Clean-and-restore-trailing-slash step and the "//" guard's
// fallback to net/http.Redirect itself).
//
// Comparison covers status code, every response header (not just Location
// and Content-Type), and the response body, for both the writeRedirect path
// and net/http.Redirect called directly with the same (method, target,
// code) triple.
func TestWriteRedirect_ByteIdenticalToNetHTTPRedirect(t *testing.T) {
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect,
		MethodQuery,
	}
	codes := []int{
		http.StatusMovedPermanently,  // 301 — GET/HEAD default
		http.StatusTemporaryRedirect, // 307 — non-GET/HEAD default
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusPermanentRedirect, // 308 — Mux.RedirectCode override
	}
	targets := []string{
		"/",
		"/a/",
		"/a",
		"/a/b/c",
		"/a<b>&c\"d'e/",        // every HTML-escaped character
		"/café/",               // non-ASCII byte sequence (hex-escaped in Location)
		"/a/b?q=hello&lang=en", // query string
		"/a<b>/?x=1&y=<2>",     // query string + HTML-escape-needing path
		"/a//b/",               // not path.Clean-normalised — exercises the Clean step
		"/a/../b",              // traversal segment — exercises the Clean step
		"/a/./b/",              // "." segment, trailing slash preserved
		"//evil.com/",          // network-path reference — exercises the fallback guard
		"/%2e%2e",              // literal percent-escape bytes, not decoded by writeRedirect
	}

	for _, method := range methods {
		for _, code := range codes {
			for _, target := range targets {
				t.Run(method+"/"+http.StatusText(code)+target, func(t *testing.T) {
					gotRec := httptest.NewRecorder()
					gotReq := httptest.NewRequest(method, "http://example.com/", nil)
					writeRedirect(gotRec, gotReq, target, code)

					wantRec := httptest.NewRecorder()
					wantReq := httptest.NewRequest(method, "http://example.com/", nil)
					http.Redirect(wantRec, wantReq, target, code) //nolint:bodyclose // recorder, not a real body

					compareRedirectResponses(t, gotRec, wantRec)
				})
			}
		}
	}
}

// TestWriteRedirect_PreexistingContentType verifies that when the response
// already has a Content-Type header, writeRedirect neither overwrites it
// nor writes an HTML body, exactly like net/http.Redirect.
func TestWriteRedirect_PreexistingContentType(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			gotRec := httptest.NewRecorder()
			gotRec.Header().Set("Content-Type", "application/custom")
			gotReq := httptest.NewRequest(method, "http://example.com/", nil)
			writeRedirect(gotRec, gotReq, "/a/b/", http.StatusMovedPermanently)

			wantRec := httptest.NewRecorder()
			wantRec.Header().Set("Content-Type", "application/custom")
			wantReq := httptest.NewRequest(method, "http://example.com/", nil)
			http.Redirect(wantRec, wantReq, "/a/b/", http.StatusMovedPermanently)

			compareRedirectResponses(t, gotRec, wantRec)
		})
	}
}

// compareRedirectResponses asserts that two recorded responses are
// byte-identical: same status code, same header set (not just a subset),
// and the same body bytes.
func compareRedirectResponses(t *testing.T, got, want *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != want.Code {
		t.Errorf("status code: got %d, want %d", got.Code, want.Code)
	}
	for k, wantVals := range want.Header() {
		gotVals := got.Header()[k]
		if !equalStringSlices(gotVals, wantVals) {
			t.Errorf("header %q: got %v, want %v", k, gotVals, wantVals)
		}
	}
	for k, gotVals := range got.Header() {
		if _, ok := want.Header()[k]; !ok {
			t.Errorf("header %q present in writeRedirect output but not in net/http.Redirect output: %v", k, gotVals)
		}
	}
	if !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
		t.Errorf("body: got %q, want %q", got.Body.String(), want.Body.String())
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWriteRedirect_ControlBytesPercentEncoded is the regression test for
// rmp #260: a percent-decoded ASCII control byte in the request path (e.g.
// a %00 in r.URL.Path) must never reach the Location header, or the HTML
// redirect body's href, unescaped — RFC 9110 §5.5 forbids raw CTL bytes in
// HTTP field values, and net/http.Redirect shares this flaw (its own
// hexEscapeNonASCII only escapes bytes >= 0x80).
//
// Repro: register "/users/:id/" (trailing slash) and request
// "/users/abc%00" (no trailing slash). net/url decodes %00 into a raw NUL
// byte in r.URL.Path ("/users/abc\x00"), which legitimately matches the
// :id capture (a path segment may contain any byte except '/'), so
// RedirectTrailingSlash fires and builds "/users/abc\x00/" as the redirect
// target — carrying the raw NUL straight into buildRedirectTarget unless
// writeRedirect sanitises it.
func TestWriteRedirect_ControlBytesPercentEncoded(t *testing.T) {
	mux := New()
	mux.RedirectTrailingSlash = true
	mux.GET("/users/:id/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/users/abc%00", nil)
	if req.URL.Path != "/users/abc\x00" {
		t.Fatalf("test setup invariant broken: decoded path = %q, want a raw NUL byte", req.URL.Path)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d (TSR redirect)", rec.Code, http.StatusMovedPermanently)
	}
	loc := rec.Header().Get("Location")
	for i := 0; i < len(loc); i++ {
		if isRedirectControlByte(loc[i]) {
			t.Fatalf("Location %q contains a raw control byte 0x%02x at offset %d — RFC 9110 §5.5 violation", loc, loc[i], i)
		}
	}
	const want = "/users/abc%00/"
	if loc != want {
		t.Fatalf("Location = %q, want %q", loc, want)
	}
	body := rec.Body.String()
	if bytes.IndexByte(rec.Body.Bytes(), 0x00) != -1 {
		t.Fatalf("HTML redirect body contains a raw NUL byte: %q", body)
	}
}

// TestWriteRedirect_FallbackPathAlsoEscapesControlBytes covers the
// http.Redirect fallback branch (a target not starting with a single '/',
// e.g. a relative or "//"-prefixed target) — rmp #260 requires the same
// control-byte guarantee there, achieved by pre-encoding the target before
// handing it to net/http.Redirect (which does not escape control bytes
// itself).
func TestWriteRedirect_FallbackPathAlsoEscapesControlBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	writeRedirect(rec, req, "relative\x00path", http.StatusFound)

	loc := rec.Header().Get("Location")
	for i := 0; i < len(loc); i++ {
		if isRedirectControlByte(loc[i]) {
			t.Fatalf("Location %q (fallback path) contains a raw control byte 0x%02x at offset %d", loc, loc[i], i)
		}
	}
}

// TestWriteRedirect_BackslashAuthorityShape_Neutralised is the regression
// test for rmp #279 (sprint 20), consolidating the hardening recommendation
// filed by the http-protocol-security-auditor as rmp #274 part 5a /
// TestHPS_FixedPath_BackslashAuthority_NotAttackerReachable
// (reports/http-protocol-security-auditor/harness/hps_2026_openredirect_test.go)
// into a first-class test in the root package.
//
// Per the WHATWG URL Standard's "special authority slashes state", a
// browser's URL parser treats ANY two-byte combination of '/' and '\' at
// the start of a relative reference — "//", "/\", "\/", "\\" — as
// authority-establishing, not just RFC 3986's "//". writeRedirect now
// percent-encodes every '\' in target to "%5C" (percentEncodeBackslash)
// before anything else runs, so a "/\"-prefixed Location can never be
// resolved by a browser as a network-path reference: the encoded form is
// unambiguously path-rooted and single-origin.
//
// This is defense-in-depth, NOT a fix for an attacker-reachable bug: a "/\"
// prefixed target can only ever reach writeRedirect if the operator
// registers that exact literal route themselves (path.Clean, used by both
// the TSR and FixedPath redirect paths, never introduces a backslash that
// was not already present verbatim in the registered pattern or the
// decoded request path — see cleanedPath/buildRedirectTarget in mux.go). An
// anonymous attacker cannot choose the host in this shape. The round trip
// still works for the operator: net/http's own request-target decoding
// turns "%5C" back into a literal '\' byte in r.URL.Path on the follow-up
// request, so the registered route is still reached — verified end-to-end
// below.
//
// '\/' and '\\' (target[0] == '\\') are deliberately not exercised here:
// every writeRedirect caller derives target from a Handle()-registered
// pattern, and Handle() panics unless a pattern starts with '/', so
// target[0] can never be '\' in this package — that shape is already
// excluded by the pre-existing target[0] != '/' check (which still routes
// to the net/http.Redirect fallback, itself now also backslash-neutralised
// since percentEncodeBackslash runs before that branch is chosen).
func TestWriteRedirect_BackslashAuthorityShape_Neutralised(t *testing.T) {
	t.Run("RedirectFixedPath", func(t *testing.T) {
		// Mirrors TestHPS_FixedPath_BackslashAuthority_NotAttackerReachable:
		// the operator registers a literal backslash-shaped route, and a
		// doubled leading slash in the request is what triggers FixedPath.
		mux := New()
		mux.RedirectFixedPath = true
		mux.GET("/\\evil.com/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "http://example.com//\\evil.com/foo", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		const want = "/%5Cevil.com/foo"
		loc := rec.Header().Get("Location")
		if loc != want {
			t.Fatalf("Location = %q, want %q (backslash must be percent-encoded, neutralising the "+
				"WHATWG authority-establishing shape)", loc, want)
		}
		if looksAuthorityEstablishing(loc) {
			t.Fatalf("Location = %q is still authority-establishing after neutralisation", loc)
		}
	})

	t.Run("RedirectTrailingSlash", func(t *testing.T) {
		// buildRedirectTarget's TSR toggle (mux.go) appends "/" to the
		// decoded request path when a slash-suffixed sibling route is
		// registered — a backslash-prefixed pattern reaches writeRedirect
		// here too, independent of the FixedPath code path above.
		mux := New()
		mux.GET("/\\evil.com/foo/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "http://example.com/\\evil.com/foo", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		const want = "/%5Cevil.com/foo/"
		loc := rec.Header().Get("Location")
		if loc != want {
			t.Fatalf("Location = %q, want %q (backslash must be percent-encoded, neutralising the "+
				"WHATWG authority-establishing shape)", loc, want)
		}
		if looksAuthorityEstablishing(loc) {
			t.Fatalf("Location = %q is still authority-establishing after neutralisation", loc)
		}
	})
}

// looksAuthorityEstablishing mirrors the helper of the same name in
// reports/http-protocol-security-auditor/harness/hps_2026_openredirect_test.go:
// per the WHATWG URL Standard's "special authority slashes state", ANY
// combination of two leading '/' or '\' characters — "//", "/\", "\/", "\\"
// — is authority-establishing to a browser, even though RFC 3986 (and
// net/url) only recognise "//".
func looksAuthorityEstablishing(loc string) bool {
	isSlashLike := func(b byte) bool { return b == '/' || b == '\\' }
	return len(loc) >= 2 && isSlashLike(loc[0]) && isSlashLike(loc[1])
}

// TestWriteRedirect_BackslashAuthorityShape_NeutralisesEndToEnd proves the
// neutralisation round-trips correctly for a real client, not just that the
// Location header looks safe: a genuine http.Client, following the redirect
// exactly as a browser would, must land on the SAME origin (the test
// server) and reach the operator's registered backslash-shaped handler —
// never a different host, and never a 404.
func TestWriteRedirect_BackslashAuthorityShape_NeutralisesEndToEnd(t *testing.T) {
	const marker = "reached-backslash-route"

	mux := New()
	mux.RedirectFixedPath = true
	mux.GET("/\\evil.com/foo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Marker", marker)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := srv.Client() // follows redirects by default, resolving Location against the request URL

	resp, err := client.Get(srv.URL + "//\\evil.com/foo")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final status = %d, want 200 (redirect must land on the registered handler)", resp.StatusCode)
	}
	if resp.Header.Get("X-Marker") != marker {
		t.Fatalf("X-Marker = %q, want %q — the final request did not reach the operator's registered "+
			"backslash-shaped handler", resp.Header.Get("X-Marker"), marker)
	}
	if got, want := resp.Request.URL.Host, srv.Listener.Addr().String(); got != want {
		t.Fatalf("final request host = %q, want %q (same origin as the test server) — a mismatch here "+
			"would mean the client followed the redirect off-origin", got, want)
	}
}
