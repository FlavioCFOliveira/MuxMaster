package muxmaster_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file covers specification/groups.md §§8-10 (requirements 31-40),
// added alongside rmp task #281:
//
//   - §8 (31-35): an inner *Mux's own automatic TSR/fixed-path redirects,
//     issued while handling a request forwarded through Mount, have their
//     Location header rewritten by the outer Mux(es) so the target is
//     expressed in the client's original URL space — recursively across
//     nested mounts, and identically whether Mount was called directly or
//     via Group.Mount. This does NOT apply to a redirect application code
//     issues itself, nor to a mounted handler that is not a *muxmaster.Mux.
//   - §9 (36-37): a Mount prefix whose last element is an optional
//     parameter panics at registration time with a dedicated message.
//   - §10 (38-40): RawPath is propagated to a Mount's forwarded request by
//     comparing it, segment-by-segment and decoded, against the prefix
//     actually matched for the request — not the literal pattern text —
//     so it is preserved whenever a captured parameter's raw form decodes
//     cleanly and zeroed otherwise.

// nopHandler200 is a trivial 200 OK handler used where the response body
// and status are not under test.
func nopHandler200(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// ── §8: Location rewriting for automatic redirects through Mount ───────────

// TestMountRedirectTSR_StaticPrefix covers requirement 31 for the simplest
// case: a static Mount prefix, and a TSR issued by the inner *Mux for its
// own registered route.
func TestMountRedirectTSR_StaticPrefix(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/hello/", nopHandler200)

	outer := muxmaster.New()
	outer.Mount("/api", inner)

	rec := get(outer, "/api/hello")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/hello/" {
		t.Fatalf("Location: got %q, want %q", loc, "/api/hello/")
	}

	// Following the redirect must reach the real handler.
	rec2 := get(outer, "/api/hello/")
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect, got %d", rec2.Code)
	}
}

// TestMountRedirectTSR_NamedParamPrefix covers requirement 33: the Mount
// prefix itself contains a named parameter, whose captured value for this
// specific request must appear (not the literal ":id" pattern text) in the
// composed Location.
func TestMountRedirectTSR_NamedParamPrefix(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/info/", nopHandler200)

	outer := muxmaster.New()
	outer.Mount("/users/:id", inner)

	rec := get(outer, "/users/42/info")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/42/info/" {
		t.Fatalf("Location: got %q, want %q", loc, "/users/42/info/")
	}

	rec2 := get(outer, "/users/42/info/")
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect, got %d", rec2.Code)
	}
}

// TestMountRedirectTSR_ViaGroupMount covers requirement 35: the rewriting
// applies identically when Mount is reached via (*Group).Mount, including
// when the group carries its own middleware.
func TestMountRedirectTSR_ViaGroupMount(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/legacy/", nopHandler200)

	var mwCalls int
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mwCalls++
			next.ServeHTTP(w, r)
		})
	}

	m := muxmaster.New()
	g := m.Group("/api")
	g.Use(mw)
	g.Mount("/old", inner)

	rec := get(m, "/api/old/legacy")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/old/legacy/" {
		t.Fatalf("Location: got %q, want %q", loc, "/api/old/legacy/")
	}
	if mwCalls != 1 {
		t.Fatalf("expected the group middleware to run exactly once, got %d", mwCalls)
	}
}

// TestMountRedirectTSR_NestedTwoLevels covers requirement 32: the rewriting
// is recursive across a chain of nested mounts, and the redirect target
// carries every prefix in the chain, in order — regardless of which Mux in
// the chain actually produced the redirect (here, the innermost one, C).
func TestMountRedirectTSR_NestedTwoLevels(t *testing.T) {
	c := muxmaster.New()
	c.GET("/panel/", nopHandler200)

	b := muxmaster.New()
	b.Mount("/admin", c)

	a := muxmaster.New()
	a.Mount("/v2", b)

	rec := get(a, "/v2/admin/panel")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/v2/admin/panel/" {
		t.Fatalf("Location: got %q, want %q", loc, "/v2/admin/panel/")
	}

	rec2 := get(a, "/v2/admin/panel/")
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect, got %d", rec2.Code)
	}
}

// TestMountRedirectTSR_WithQueryString covers requirement 33: the query
// string is preserved unchanged on the composed target, exactly as
// routing.md rule 80 already specifies for any other redirect.
func TestMountRedirectTSR_WithQueryString(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/info/", nopHandler200)

	outer := muxmaster.New()
	outer.Mount("/users/:id", inner)

	rec := get(outer, "/users/42/info?x=1&y=2")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/42/info/?x=1&y=2" {
		t.Fatalf("Location: got %q, want %q", loc, "/users/42/info/?x=1&y=2")
	}
}

// TestMountRedirectTSR_WithEncodedChars covers requirement 33's composed
// target for a request whose RawPath differs from Path (a percent-encoded
// character reaches the router). The Location is built from the decoded
// path, matching routing.md's existing redirect-target rules; the encoding
// in the request itself must not corrupt the composed prefix.
func TestMountRedirectTSR_WithEncodedChars(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/hello/world/", nopHandler200)

	outer := muxmaster.New()
	outer.Mount("/api", inner)

	// "%2f" decodes to '/' in r.URL.Path, so this reaches the same route as
	// "/api/hello/world" — decoded — while r.URL.RawPath still carries the
	// original encoded form.
	rec := get(outer, "/api/hello%2fworld")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/hello/world/" {
		t.Fatalf("Location: got %q, want %q", loc, "/api/hello/world/")
	}
}

// TestMountRedirectFixedPath covers requirement 31's fixed-path branch
// (routing.md §4.5): a redirect produced by the inner Mux's
// RedirectFixedPath cleanup is rewritten exactly like a TSR redirect.
func TestMountRedirectFixedPath(t *testing.T) {
	inner := muxmaster.New()
	inner.RedirectFixedPath = true
	inner.GET("/hello", nopHandler200)

	outer := muxmaster.New()
	outer.Mount("/api", inner)

	rec := get(outer, "/api/./hello")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/hello" {
		t.Fatalf("Location: got %q, want %q", loc, "/api/hello")
	}
}

// TestMountRedirect_AppIssuedNotRewritten covers requirement 34's first
// bullet: a redirect the application handler issues itself (http.Redirect)
// reaches the client completely unmodified, with no mount-prefix rewriting.
func TestMountRedirect_AppIssuedNotRewritten(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/somewhere", http.StatusFound)
	})

	outer := muxmaster.New()
	outer.Mount("/api", inner)

	rec := get(outer, "/api/x")
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/somewhere" {
		t.Fatalf("Location: got %q, want %q (must NOT be rewritten)", loc, "/somewhere")
	}
}

// TestMountRedirect_FileServerUnaffected covers requirement 34's second
// bullet: http.FileServer is not a *muxmaster.Mux, so its own
// directory-redirect behavior is never rewritten by the outer Mux.
func TestMountRedirect_FileServerUnaffected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	outer := muxmaster.New()
	outer.Mount("/static", http.FileServer(http.Dir(dir)))

	rec := get(outer, "/static/sub")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301 from http.FileServer, got %d", rec.Code)
	}
	// http.FileServer's own redirect uses a relative Location (just the
	// final path element + "/"), never an absolute path built from
	// r.URL.Path — if the outer Mux's rewriting ever fired here, it would
	// alter this value. It must not.
	if loc := rec.Header().Get("Location"); loc != "sub/" {
		t.Fatalf("Location: got %q, want %q (must be unmodified http.FileServer output)", loc, "sub/")
	}
}

// ── §9: Mount prefix validation ─────────────────────────────────────────────

// TestMountPrefixOptionalTrailingPanics covers requirement 36: a Mount
// prefix ending in an optional parameter panics with the exact message,
// naming the exact combined prefix, both for (*Mux).Mount directly and for
// (*Group).Mount (group prefix + local prefix already concatenated).
func TestMountPrefixOptionalTrailingPanics(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		m := muxmaster.New()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected a panic, got none")
			}
			want := "muxmaster: Mount prefix '/admin{/:id}' ends with an optional parameter; " +
				"Mount does not support an optional parameter as the last element of its prefix"
			if r != want {
				t.Fatalf("panic message: got %q, want %q", r, want)
			}
		}()
		m.Mount("/admin{/:id}", http.NotFoundHandler())
	})

	t.Run("via group", func(t *testing.T) {
		m := muxmaster.New()
		g := m.Group("/api")
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected a panic, got none")
			}
			want := "muxmaster: Mount prefix '/api/admin{/:id}' ends with an optional parameter; " +
				"Mount does not support an optional parameter as the last element of its prefix"
			if r != want {
				t.Fatalf("panic message: got %q, want %q", r, want)
			}
		}()
		g.Mount("/admin{/:id}", http.NotFoundHandler())
	})

	t.Run("regex optional", func(t *testing.T) {
		m := muxmaster.New()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected a panic, got none")
			}
			want := `muxmaster: Mount prefix '/admin{/:id:\d+}' ends with an optional parameter; ` +
				"Mount does not support an optional parameter as the last element of its prefix"
			if r != want {
				t.Fatalf("panic message: got %q, want %q", r, want)
			}
		}()
		m.Mount(`/admin{/:id:\d+}`, http.NotFoundHandler())
	})

	t.Run("trailing slash on the optional form still panics", func(t *testing.T) {
		m := muxmaster.New()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected a panic, got none")
			}
			// The message reports the exact combined prefix, BEFORE the
			// trailing-'/' normalization (requirement 36) — the trailing
			// slash is still present in the panic text.
			want := "muxmaster: Mount prefix '/admin{/:id}/' ends with an optional parameter; " +
				"Mount does not support an optional parameter as the last element of its prefix"
			if r != want {
				t.Fatalf("panic message: got %q, want %q", r, want)
			}
		}()
		m.Mount("/admin{/:id}/", http.NotFoundHandler())
	})
}

// TestMountPrefixOptionalNonTrailingWorks covers requirement 37: an
// optional parameter that is NOT the prefix's last element continues to
// work exactly as before — both the with-segment and without-segment forms
// of the request reach the mounted handler.
func TestMountPrefixOptionalNonTrailingWorks(t *testing.T) {
	inner := muxmaster.New()
	inner.GET("/panel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.URL.Path))
	})

	m := muxmaster.New()
	m.Mount("/v2{/:id}/admin", inner)

	rec := get(m, "/v2/admin/panel")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the without-segment form, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "/panel" {
		t.Fatalf("body: got %q, want %q", body, "/panel")
	}

	rec2 := get(m, "/v2/42/admin/panel")
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for the with-segment form, got %d", rec2.Code)
	}
	if body := rec2.Body.String(); body != "/panel" {
		t.Fatalf("body: got %q, want %q", body, "/panel")
	}
}

// ── §10: RawPath propagation through a parameterized Mount prefix ──────────

// TestMountRawPath_NamedParamPrefix_DecodeConsistent covers requirements 38
// and 40: a Mount prefix containing a named parameter whose captured raw
// segment decodes cleanly to the same text the router matched on
// (r.URL.Path) preserves RawPath for the remainder of the request.
func TestMountRawPath_NamedParamPrefix_DecodeConsistent(t *testing.T) {
	var gotPath, gotRawPath string
	inner := muxmaster.New()
	inner.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRawPath = r.URL.Path, r.URL.RawPath
		w.WriteHeader(http.StatusOK)
	})

	m := muxmaster.New()
	m.Mount("/users/:id", inner)

	// "%32" decodes to '2': the raw ":id" segment "4%32" decodes cleanly to
	// "42", matching the segment the router actually captured.
	rec := get(m, "/users/4%32/info")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/info" {
		t.Fatalf("inner Path: got %q, want %q", gotPath, "/info")
	}
	if gotRawPath != "/info" {
		t.Fatalf("inner RawPath: got %q, want %q (decode-consistent, must be preserved)", gotRawPath, "/info")
	}
}

// TestMountRawPath_NamedParamPrefix_ZeroedOnEncodedSlash covers
// requirements 38.3 and 40: a percent-encoded '/' inside the captured
// segment's raw form shifts the raw segment boundaries out of alignment
// with the decoded ones — the match is not decode-consistent, so RawPath is
// zeroed on the forwarded request rather than propagating a misaligned
// encoded prefix.
func TestMountRawPath_NamedParamPrefix_ZeroedOnEncodedSlash(t *testing.T) {
	var gotRawPath string
	var sawRawPath bool
	inner := muxmaster.New()
	inner.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		gotRawPath, sawRawPath = r.URL.RawPath, true
		w.WriteHeader(http.StatusOK)
	})

	m := muxmaster.New()
	m.Mount("/users/:id", inner)

	// "%2F" decodes to '/': r.URL.Path becomes "/users/4/2/info" (three
	// segments), so :id captures only "4" — the raw form's decoded content
	// ("4/2") no longer matches the matched segment ("4").
	rec := get(m, "/users/4%2F2/info")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !sawRawPath {
		t.Fatal("inner handler was never called")
	}
	if gotRawPath != "" {
		t.Fatalf("inner RawPath: got %q, want %q (must be zeroed — not decode-consistent)", gotRawPath, "")
	}
}

// TestMountRawPath_StaticPrefix_Unchanged is a regression guard for
// specification/groups.md requirement 39: a static Mount prefix's RawPath
// behavior is byte-identical to the pre-existing (MM-2026-0022) behavior —
// a literal TrimPrefix against the registered prefix text, zeroed on any
// divergence, even when a percent-encoded rendering of the static segment
// itself would otherwise decode-match it. This must keep passing exactly
// as TestMountRawPathNormalisedOnMismatch (security_test.go) already
// requires.
func TestMountRawPath_StaticPrefix_Unchanged(t *testing.T) {
	var gotPath, gotRawPath string
	inner := muxmaster.New()
	inner.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRawPath = r.URL.Path, r.URL.RawPath
		w.WriteHeader(http.StatusOK)
	})

	m := muxmaster.New()
	m.Mount("/api", inner)

	rec := get(m, "/api/hello%2fworld")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/hello/world" {
		t.Fatalf("inner Path: got %q, want %q", gotPath, "/hello/world")
	}
	if gotRawPath != "/hello%2fworld" {
		t.Fatalf("inner RawPath: got %q, want %q", gotRawPath, "/hello%2fworld")
	}
}
