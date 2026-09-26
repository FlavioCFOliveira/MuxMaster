package muxmaster_test

import (
	"net/http"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file covers specification/routing.md section 12 (requirements
// 97-101) and params.md rule 37, added alongside rmp task #283: neither a
// named parameter nor a regex parameter ever matches an empty path segment.

// ── Named parameter, mid-path empty segment (rule 97/98) ───────────────────

// TestEmptySegment_NamedParam_MidPath_NoBacktrack covers the direct case:
// no sibling static route exists, so getValue's optimistic walk resolves
// (or fails) without ever needing getValueBacktrack.
func TestEmptySegment_NamedParam_MidPath_NoBacktrack(t *testing.T) {
	m := muxmaster.New()
	m.GET("/:id/posts", handler(http.StatusOK, "posts"))

	// Sanity: a genuine, non-empty segment still matches.
	if rec := get(m, "/42/posts"); rec.Code != http.StatusOK {
		t.Fatalf("expected /42/posts to match, got %d", rec.Code)
	}

	rec := get(m, "//posts")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected //posts to 404 (empty segment rejected), got %d", rec.Code)
	}
}

// TestEmptySegment_NamedParam_MidPath_WithBacktrackAlternative covers rules
// 97/98 combined with the static/wildchild backtracking mechanism (DIV-001):
// a static sibling that shares the empty segment's leading '/' byte forces
// getValue to commit speculatively to the static branch first; when that
// branch fails, getValueBacktrack retries via the named-parameter wildchild
// — which must still reject the empty segment, not silently capture it.
func TestEmptySegment_NamedParam_MidPath_WithBacktrackAlternative(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/:id/posts", handler(http.StatusOK, "posts"))
	// A literal double-slash static route: shares the '/' index byte with
	// the ":id" wildchild at the "/users/" node, forcing a genuine fork.
	m.GET("/users//special", handler(http.StatusOK, "special"))

	// Sanity checks: both registered routes still match as registered.
	if rec := get(m, "/users/42/posts"); rec.Code != http.StatusOK {
		t.Fatalf("expected /users/42/posts to match, got %d", rec.Code)
	}
	if rec := get(m, "/users//special"); rec.Code != http.StatusOK {
		t.Fatalf("expected /users//special to match, got %d", rec.Code)
	}

	rec := get(m, "/users//posts")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected /users//posts to 404 (empty segment rejected even after backtracking), got %d", rec.Code)
	}
}

// ── Named parameter, last-position empty segment (rule 97/98 vs rule 11) ───

// TestEmptySegment_NamedParam_LastPosition covers a named parameter that is
// the pattern's final element, reached with a trailing empty segment — a
// distinct code path from rule 11's terminal case (no path remains at all).
func TestEmptySegment_NamedParam_LastPosition(t *testing.T) {
	m := muxmaster.New()
	m.GET("/:id", handler(http.StatusOK, "id"))

	if rec := get(m, "/42"); rec.Code != http.StatusOK {
		t.Fatalf("expected /42 to match, got %d", rec.Code)
	}
	// Rule 11: no path remains after the leading '/' — terminal case,
	// unrelated to rule 97, already 404 before this fix.
	if rec := get(m, "/"); rec.Code != http.StatusNotFound {
		t.Errorf("expected / to 404 (rule 11 terminal case), got %d", rec.Code)
	}
	// Rule 97/98: a genuine empty mid/last segment via a doubled slash.
	if rec := get(m, "//"); rec.Code != http.StatusNotFound {
		t.Errorf("expected // to 404 (empty segment rejected), got %d", rec.Code)
	}
}

// ── Regex parameter (rule 97/99) ────────────────────────────────────────────

// TestEmptySegment_RegexParam_MidPath covers the exact example from
// routing.md rule 99: an expression that would itself accept the empty
// string ("[a-z]*") never gets the chance to, because the empty segment is
// rejected before the regexp is evaluated at all.
func TestEmptySegment_RegexParam_MidPath(t *testing.T) {
	m := muxmaster.New()
	m.GET("/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))

	if rec := get(m, "/abc/profile"); rec.Code != http.StatusOK {
		t.Fatalf("expected /abc/profile to match, got %d", rec.Code)
	}
	rec := get(m, "//profile")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected //profile to 404 (empty segment rejected before regexp eval), got %d", rec.Code)
	}
}

// TestEmptySegment_RegexParam_MidPath_WithBacktrackAlternative mirrors
// TestEmptySegment_NamedParam_MidPath_WithBacktrackAlternative for a regex
// parameter: a static sibling forces a fork, and the backtracked retry into
// the regex wildchild must still reject the empty segment.
func TestEmptySegment_RegexParam_MidPath_WithBacktrackAlternative(t *testing.T) {
	m := muxmaster.New()
	m.GET("/users/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))
	m.GET("/users//special", handler(http.StatusOK, "special"))

	if rec := get(m, "/users/abc/profile"); rec.Code != http.StatusOK {
		t.Fatalf("expected /users/abc/profile to match, got %d", rec.Code)
	}
	if rec := get(m, "/users//special"); rec.Code != http.StatusOK {
		t.Fatalf("expected /users//special to match, got %d", rec.Code)
	}
	rec := get(m, "/users//profile")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected /users//profile to 404 (empty segment rejected even after backtracking), got %d", rec.Code)
	}
}

// TestEmptySegment_RegexParam_LastPosition covers a regex parameter as the
// pattern's final element with a trailing empty segment.
func TestEmptySegment_RegexParam_LastPosition(t *testing.T) {
	m := muxmaster.New()
	m.GET("/{id:[a-z]*}", handler(http.StatusOK, "id"))

	if rec := get(m, "/abc"); rec.Code != http.StatusOK {
		t.Fatalf("expected /abc to match, got %d", rec.Code)
	}
	if rec := get(m, "//"); rec.Code != http.StatusNotFound {
		t.Errorf("expected // to 404 (empty segment rejected), got %d", rec.Code)
	}
}

// ── Fast routes (params.md / performance.md HandleFast path) ───────────────

// TestEmptySegment_NamedParam_FastRoute covers the same rejection for a
// HandleFast-registered route: getValue/getValueBacktrack are shared by
// both dispatch paths, so the fix must apply uniformly.
func TestEmptySegment_NamedParam_FastRoute(t *testing.T) {
	m := muxmaster.New()
	var called bool
	var gotID string
	m.GETFast("/:id/posts", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		called = true
		gotID = ps.Get("id")
		w.WriteHeader(http.StatusOK)
	})

	rec := get(m, "//posts")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected //posts to 404 on a fast route, got %d", rec.Code)
	}
	if called {
		t.Errorf("fast handler must not be invoked for an empty segment, got id=%q", gotID)
	}
}

// TestEmptySegment_RegexParam_FastRoute is the regex-parameter counterpart
// of TestEmptySegment_NamedParam_FastRoute.
func TestEmptySegment_RegexParam_FastRoute(t *testing.T) {
	m := muxmaster.New()
	var called bool
	m.GETFast("/{id:[a-z]*}/profile", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := get(m, "//profile")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected //profile to 404 on a fast route, got %d", rec.Code)
	}
	if called {
		t.Errorf("fast handler must not be invoked for an empty segment")
	}
}

// ── Case-insensitive lookup path ────────────────────────────────────────────

// TestEmptySegment_CaseInsensitive covers the case-insensitive static-prefix
// matching variant of getValue (Mux.CaseInsensitive): the empty-segment
// check is independent of, and runs identically under, case folding.
func TestEmptySegment_CaseInsensitive(t *testing.T) {
	m := muxmaster.New()
	m.CaseInsensitive = true
	m.GET("/Users/:id/Posts", handler(http.StatusOK, "posts"))

	if rec := get(m, "/users/42/posts"); rec.Code != http.StatusOK {
		t.Fatalf("expected case-insensitive match, got %d", rec.Code)
	}
	rec := get(m, "/USERS//POSTS")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected /USERS//POSTS to 404 (empty segment rejected), got %d", rec.Code)
	}
}

// ── Rule 101: the outcomes when rule 97 rejects a candidate ─────────────────

// TestEmptySegment_Rule101_DefaultConfig_PlainNotFound covers rule 101's
// closing statement: with the default configuration (RedirectTrailingSlash
// true, RedirectFixedPath false) and no separate route registered at the
// cleaned path, a request rule 97 rejects ends in a plain 404 with no
// redirect of any kind.
func TestEmptySegment_Rule101_DefaultConfig_PlainNotFound(t *testing.T) {
	m := muxmaster.New()
	m.GET("/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))

	rec := get(m, "//profile")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected //profile to 404, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("expected no Location header, got %q", loc)
	}
}

// TestEmptySegment_Rule101_NoTrailingSlashRedirect covers rule 101's first
// bullet: a trailing-slash redirect never fires merely because rule 97
// rejected a candidate — it requires its own, independent condition (a
// registered handler at the request path plus/minus a trailing '/').
func TestEmptySegment_Rule101_NoTrailingSlashRedirect(t *testing.T) {
	m := muxmaster.New() // RedirectTrailingSlash defaults to true
	m.GET("/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))

	rec := get(m, "//profile")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected //profile to 404 with no TSR redirect, got %d", rec.Code)
	}
}

// TestEmptySegment_Rule101_FixedPathRedirect_NoSeparateRoute covers rule
// 101's second bullet, first half: RedirectFixedPath true, but no separate
// route registered at path.Clean("//profile") ("/profile") — still a plain
// 404, because no handler exists at the cleaned path either.
func TestEmptySegment_Rule101_FixedPathRedirect_NoSeparateRoute(t *testing.T) {
	m := muxmaster.New()
	m.RedirectFixedPath = true
	m.GET("/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))

	rec := get(m, "//profile")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected //profile to 404 (no route at /profile), got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("expected no Location header, got %q", loc)
	}
}

// TestEmptySegment_Rule101_FixedPathRedirect_WithSeparateRoute covers rule
// 101's second bullet, second half: with RedirectFixedPath true AND a route
// additionally registered directly at "/profile", the same "//profile"
// request DOES redirect to "/profile" — the ordinary fixed-path mechanism,
// not a special case introduced by empty-segment rejection.
func TestEmptySegment_Rule101_FixedPathRedirect_WithSeparateRoute(t *testing.T) {
	m := muxmaster.New()
	m.RedirectFixedPath = true
	m.GET("/{id:[a-z]*}/profile", handler(http.StatusOK, "profile"))
	m.GET("/profile", handler(http.StatusOK, "profile-direct"))

	rec := get(m, "//profile")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected //profile to 301-redirect to /profile, got %d body=%q", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/profile" {
		t.Errorf("Location = %q, want %q", loc, "/profile")
	}
}
