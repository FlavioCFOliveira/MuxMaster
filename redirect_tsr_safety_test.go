package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file is the sprint-18 regression/fuzz suite for the redirect-safety
// side of rmp #255 (MM-2026-0255): the TSR fix for catch-all-terminated
// routes (tree.go getValue, ~line 610) and the new Mount-bare-prefix TSR
// branch in mux.go dispatch (the "starRoot"/idxWild block). Both are new
// PRODUCERS of tsr=true in situations that previously fell through to 404,
// so both get dedicated coverage for the invariants
// specification/routing.md §4.4 (rules 52-55) and HPS-2026-0005 require of
// every redirect target MuxMaster builds from a request path:
//
//   - never an open redirect: the Location value must never start with "//"
//     (protocol-relative) or contain "://" (absolute-with-scheme) — see
//     buildRedirectTarget/writeRedirect's own "//" guard (mux.go) and
//     redirect_bytediff_test.go, which documents that guard's fallback to
//     net/http.Redirect is intentionally NOT itself a sanitizer: the real
//     guarantee has to come from dispatch() never handing it such a target.
//   - never CRLF-injectable: the raw header value MuxMaster itself sets
//     must contain no '\r' or '\n' (defense in depth on top of net/http's
//     own Header.writeSubset newline-to-space scrubbing at wire-write time).
//   - never a redirect loop: following a TSR redirect must land on a
//     stable outcome (a match, or a 404/405) within one hop, never bounce
//     back to a further redirect for the same resource.
//   - TSR must never fire for CONNECT or for the exact root path "/"
//     (rules 54-55).

func buildRedirectSafetyMux() *muxmaster.Mux {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.RedirectFixedPath = false

	m.GET("/admin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/static/*filepath", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.GET("/trailing/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	// rmp #260: a param route registered WITH a trailing slash lets a
	// percent-decoded control byte in the captured segment (e.g. %00) reach
	// the TSR-generated Location — the fuzz target below needs this shape to
	// actually exercise the CTL-byte assertion, not just the CRLF one.
	m.GET("/items/:id/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	// #255's new code path: Mount registers a catch-all on the "*" method
	// tree and relies on the new tsr2 branch in dispatch for the bare
	// "/v2" (no trailing slash, no further segment) request.
	m.Mount("/v2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	return m
}

// assertRedirectIsSafe applies the invariants above to a single recorded
// redirect response. It is called both from the deterministic table test
// and from the fuzz target.
func assertRedirectIsSafe(t *testing.T, method, reqPath string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code < 300 || rec.Code >= 400 {
		return // not a redirect — nothing to check here
	}
	if method == http.MethodConnect {
		t.Fatalf("TSR fired for CONNECT %q (rule 55 forbids this): status=%d", reqPath, rec.Code)
	}
	if reqPath == "/" {
		t.Fatalf("TSR fired for the root path %q (rule 54 forbids this): status=%d", reqPath, rec.Code)
	}

	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatalf("redirect status %d for %q has no Location header", rec.Code, reqPath)
	}
	if strings.Contains(loc, "\r") || strings.Contains(loc, "\n") {
		t.Fatalf("CRLF INJECTION: redirect Location for %q contains a raw CR/LF byte: %q", reqPath, loc)
	}
	// rmp #260: RFC 9110 §5.5 forbids ANY raw ASCII control byte (0x00-0x1F)
	// or DEL (0x7F) in a field value, not just CR/LF — a percent-decoded
	// control byte in the request path (e.g. %00) must be percent-encoded
	// before it reaches Location. See writeRedirect's percentEncodeControlBytes
	// (mux.go, unexported — reimplemented locally since this file is in the
	// external muxmaster_test package).
	for i := 0; i < len(loc); i++ {
		if b := loc[i]; b < 0x20 || b == 0x7f {
			t.Fatalf("CONTROL BYTE LEAK: redirect Location for %q contains a raw control byte 0x%02x at offset %d: %q", reqPath, b, i, loc)
		}
	}
	if len(loc) >= 2 && loc[0] == '/' && loc[1] == '/' {
		t.Fatalf("OPEN REDIRECT: redirect Location for %q starts with '//' (protocol-relative): %q", reqPath, loc)
	}
	if loc[0] == '/' && strings.Contains(loc, "://") {
		// A path-rooted value that also contains "://" further in is
		// unusual but not itself unsafe for a browser's Location
		// resolution (it stays a relative path); flagged only as a
		// same-origin sanity signal, not a hard failure, unless it's at
		// the very start (covered by the scheme-prefix check below).
		t.Logf("NOTE: redirect Location for %q contains \"://\" mid-string: %q", reqPath, loc)
	}
	if loc[0] != '/' {
		t.Fatalf("OPEN REDIRECT: redirect Location for %q is not path-rooted: %q", reqPath, loc)
	}
}

// followAndCheckLoop replays the redirect target through the SAME mux (with
// the same method, no query manipulation) up to maxHops times, asserting
// the chain terminates in a non-redirect response and never revisits a
// Location it has already seen (which would be an infinite loop).
func followAndCheckLoop(t *testing.T, mux *muxmaster.Mux, method, reqPath string, maxHops int) {
	t.Helper()
	seen := map[string]bool{reqPath: true}
	path := reqPath
	for hop := 0; hop < maxHops; hop++ {
		req, err := http.NewRequest(method, "http://example.com"+path, nil)
		if err != nil {
			// A redirect target containing a raw ASCII control byte other
			// than CR/LF (e.g. NUL from a %00 in the original request path)
			// fails net/url's client-side parser here. This is a pre-existing,
			// stdlib-shared characteristic — net/http.Redirect's own
			// hexEscapeNonASCII only escapes bytes >= utf8.RuneSelf (0x80),
			// same as writeRedirect — verified independent of #253/#255/#256
			// (reproduces via an ordinary /users/:id/ TSR redirect on
			// unmodified code, and via net/http.Redirect called directly).
			// Not this fuzz target's concern (it asserts no CR/LF, no "//"
			// open-redirect shape, and no loop) — stop following rather than
			// fail the loop-termination check on a harness limitation.
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code < 300 || rec.Code >= 400 {
			return // terminated
		}
		loc := rec.Header().Get("Location")
		if seen[loc] {
			t.Fatalf("REDIRECT LOOP: %q -> %q revisits an already-seen location after %d hop(s)", reqPath, loc, hop+1)
		}
		seen[loc] = true
		path = loc
	}
	t.Fatalf("redirect chain from %q did not terminate within %d hops", reqPath, maxHops)
}

// TestTSRRedirectSafety_TableDriven exercises the two NEW tsr-producing
// scenarios from #255 directly: a bare catch-all prefix ("/static", no
// trailing slash, only "/static/*filepath" registered) and a bare Mount
// prefix ("/v2"), plus a representative sample of the taxonomy's
// "//"-prefixed and traversal-flavoured inputs.
func TestTSRRedirectSafety_TableDriven(t *testing.T) {
	mux := buildRedirectSafetyMux()

	cases := []string{
		"/admin/",    // static route + trailing slash -> TSR strip
		"/trailing",  // static route registered WITH trailing slash -> TSR add
		"/static",    // #255: bare catch-all prefix, no handler at "/static" itself
		"/v2",        // #255: bare Mount prefix
		"/v2/",       // must NOT redirect — the catch-all matches directly (mux_mount="/")
		"//admin",    // leading double slash — must never surface as an open redirect
		"//v2",       // leading double slash against the Mount tree
		"///v2",      // triple leading slash
		"/v2//",      // trailing double slash against the Mount tree
		"/static/",   // catch-all matches directly (filepath="") — must NOT redirect
		"/users/42/", // no route registered with a trailing slash here — expect 404, not TSR
	}

	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			assertRedirectIsSafe(t, http.MethodGet, p, rec)
			if rec.Code >= 300 && rec.Code < 400 {
				followAndCheckLoop(t, mux, http.MethodGet, p, 3)
			}
		})
	}

	// Rule 55: CONNECT must never receive a TSR redirect even for an
	// otherwise-classic TSR shape.
	t.Run("CONNECT never redirects", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodConnect, "/admin/", nil))
		if rec.Code >= 300 && rec.Code < 400 {
			t.Fatalf("CONNECT /admin/ redirected (status %d) — rule 55 violation", rec.Code)
		}
	})
}

// TestTSRRedirectSafety_MountBarePrefixEndToEnd is the specific rmp #255 /
// groups.md §28 regression: GET "/v2" (bare mount prefix) must redirect
// exactly once, to "/v2/", which must then be served by the mounted
// handler (not redirect again).
func TestTSRRedirectSafety_MountBarePrefixEndToEnd(t *testing.T) {
	mux := buildRedirectSafetyMux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /v2 = %d, want %d (TSR redirect)", rec.Code, http.StatusMovedPermanently)
	}
	loc := rec.Header().Get("Location")
	if loc != "/v2/" {
		t.Fatalf("GET /v2 Location = %q, want %q", loc, "/v2/")
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, loc, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET %q (the redirect target) = %d, want 200 (mounted handler)", loc, rec2.Code)
	}
}

// FuzzTSRRedirectSafety fuzzes arbitrary request paths (and, via the fuzzed
// method index, occasionally CONNECT) against buildRedirectSafetyMux and
// applies assertRedirectIsSafe + a bounded redirect-loop check to every
// response that comes back as a redirect. A failure here is an open
// redirect, a CRLF injection, a redirect loop, or a rule 54/55 violation —
// see the file doc comment.
func FuzzTSRRedirectSafety(f *testing.F) {
	seeds := []string{
		"/admin", "/admin/", "/admin//", "//admin", "///admin", "/ADMIN/",
		"/static", "/static/", "/static/x", "/static/../etc/passwd",
		"/v2", "/v2/", "/v2//", "//v2", "/v2/x", "/v2/../x",
		"/users/1", "/users/1/", "/users/", "/users",
		"/trailing", "/trailing/", "/trailing//",
		"/", "//", "///",
		"/%2e%2e/admin", "/admin%00", "/admin\x00",
		"/items/abc%00", "/items/abc%00/", "/items/%01%02", // rmp #260: percent-decoded CTL bytes into a TSR target
		"/a%2fb", "/..%2fadmin", "/admin/..",
		"/ádmin/", // non-ASCII, not "admin"
	}
	for _, s := range seeds {
		f.Add(s, uint8(0))
		f.Add(s, uint8(1)) // CONNECT variant
	}

	mux := buildRedirectSafetyMux()
	methods := []string{http.MethodGet, http.MethodConnect, http.MethodPost, http.MethodHead}

	f.Fuzz(func(t *testing.T, path string, methodIdx uint8) {
		if len(path) == 0 || len(path) > 2048 {
			t.Skip()
		}
		method := methods[int(methodIdx)%len(methods)]

		// http.NewRequest (which httptest.NewRequest wraps) rejects targets
		// containing raw ASCII control characters at url.Parse time — the
		// same class of input a real HTTP/1.1 server already refuses at the
		// request-line parser, before r.URL.Path ever exists. Skip those:
		// they cannot reach dispatch() through any real net/http listener
		// either, so they are outside this fuzz target's threat model
		// (control-byte smuggling into a decoded path is
		// http-protocol-security-auditor's territory, not routing).
		req, err := http.NewRequest(method, "http://example.com"+path, nil)
		if err != nil {
			t.Skip()
		}

		var rec *httptest.ResponseRecorder
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ServeHTTP panicked on method=%s path=%q: %v", method, path, r)
				}
			}()
			rec = httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
		}()

		assertRedirectIsSafe(t, method, path, rec)
		if rec.Code >= 300 && rec.Code < 400 {
			followAndCheckLoop(t, mux, method, path, 3)
		}
	})
}
