package muxmaster_test

import (
	"net/http"
	"testing"
	"testing/fstest"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file covers specification/groups.md section 11 (requirements 41-44),
// added alongside rmp tasks #282/#283: every place a group prefix, a nested
// group prefix, a route-local path, or a Mount/ServeFiles prefix argument is
// combined with another such string goes through the same join rule instead
// of a plain, unconditional concatenation.

// TestGroupJoin_TrailingSlashPrefix covers requirement 41: a group prefix
// ending in '/' joined with a route-local path beginning with '/' collapses
// to a single '/' at the boundary — the exact example from groups.md §11.
func TestGroupJoin_TrailingSlashPrefix(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/")
	api.GET("/users", handler(http.StatusOK, "users"))

	rec := get(m, "/api/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/users to match, got %d", rec.Code)
	}
	// The double-slash form must NOT have been registered.
	rec2 := get(m, "/api//users")
	if rec2.Code == http.StatusOK {
		t.Errorf("expected /api//users NOT to match a route joined from '/api/' + '/users', got %d", rec2.Code)
	}
}

// TestGroupJoin_NoTrailingSlashPrefix covers requirement 42's plain
// concatenation case: a group prefix with no trailing '/' joined with a
// route-local path that begins with '/'.
func TestGroupJoin_NoTrailingSlashPrefix(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api")
	api.GET("/users", handler(http.StatusOK, "users"))

	rec := get(m, "/api/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/users to match, got %d", rec.Code)
	}
}

// TestGroupJoin_RouteLocalPathWithoutLeadingSlash covers the groups.md §11
// example verbatim: a route-local path with no leading '/' registered under
// a group prefix that already ends in '/' — nothing is inserted (rule 42),
// and the result is correct only because the prefix already supplied the
// boundary slash.
func TestGroupJoin_RouteLocalPathWithoutLeadingSlash(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/")
	api.GET("orders", handler(http.StatusOK, "orders"))

	rec := get(m, "/api/orders")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/orders to match, got %d", rec.Code)
	}
}

// TestGroupJoin_NestedSubGroupsTrailingSlashes covers requirement 14/41-42
// across two nesting levels, each with a trailing-slash prefix.
func TestGroupJoin_NestedSubGroupsTrailingSlashes(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/")
	v1 := api.Group("/v1/")
	v1.GET("/users", handler(http.StatusOK, "users"))

	rec := get(m, "/api/v1/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/v1/users to match, got %d", rec.Code)
	}
	if rec2 := get(m, "/api//v1/users"); rec2.Code == http.StatusOK {
		t.Errorf("unexpected match for /api//v1/users, got %d", rec2.Code)
	}
	if rec3 := get(m, "/api/v1//users"); rec3.Code == http.StatusOK {
		t.Errorf("unexpected match for /api/v1//users, got %d", rec3.Code)
	}
}

// TestGroupJoin_Mount covers requirement 22: Group.Mount joins the group
// prefix with the Mount prefix argument per section 11, not a plain prepend.
func TestGroupJoin_Mount(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/")
	mounted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.URL.Path)) //nolint:errcheck
	})
	api.Mount("/legacy", mounted)

	rec := get(m, "/api/legacy/thing")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/legacy/thing to match, got %d", rec.Code)
	}
	if got, want := rec.Body.String(), "/thing"; got != want {
		t.Errorf("forwarded path = %q, want %q", got, want)
	}
	if rec2 := get(m, "/api//legacy/thing"); rec2.Code == http.StatusOK {
		t.Errorf("unexpected match for /api//legacy/thing, got %d", rec2.Code)
	}
}

// TestGroupJoin_ServeFiles covers static-files.md requirement 2: Group.ServeFiles
// joins the group prefix with its prefix argument per groups.md section 11.
func TestGroupJoin_ServeFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"style.css": {Data: []byte("body{color:red}")},
	}
	m := muxmaster.New()
	assets := m.Group("/assets/")
	assets.ServeFiles("/*filepath", http.FS(fsys))

	rec := get(m, "/assets/style.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /assets/style.css to match, got %d body=%q", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "body{color:red}"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	// Note: a request with an extra '/' INSIDE the captured catch-all
	// segment (e.g. "/assets//style.css") is not a probe of the join rule —
	// http.FileServer applies path.Clean to the captured filepath
	// regardless of how the prefix itself was joined, so it would resolve
	// the same file either way. The join defect this test guards against
	// (requirement 41) is instead exposed at the registration boundary: had
	// "/assets/" and "/*filepath" been concatenated without collapsing the
	// duplicate '/', the pattern would have required a literal "//" before
	// the wildcard, and the single-slash request above would 404 — which is
	// exactly what the first assertion in this test already verifies.
}

// TestGroupJoin_InnerDoubleSlashPreserved covers requirement 43: a repeated
// '/' that already exists strictly inside the group prefix (not at the join
// boundary) remains literal pattern text, matched literally.
func TestGroupJoin_InnerDoubleSlashPreserved(t *testing.T) {
	m := muxmaster.New()
	weird := m.Group("/api//v1")
	weird.GET("/users", handler(http.StatusOK, "users"))

	rec := get(m, "/api//v1/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api//v1/users to match the literal inner '//', got %d", rec.Code)
	}
	// The "cleaned" single-slash form must NOT match — the inner // is
	// literal pattern text, not normalized away.
	if rec2 := get(m, "/api/v1/users"); rec2.Code == http.StatusOK {
		t.Errorf("unexpected match for /api/v1/users against a pattern with a literal inner '//', got %d", rec2.Code)
	}
}

// TestGroupJoin_PathWithoutLeadingSlashStillPanics covers requirement 42's
// closing statement: the join runs BEFORE Handle's own validation, not
// instead of it — a pattern that still does not begin with '/' after the
// join continues to panic exactly as it did before this rule was
// introduced. Handle's own validation is exercised directly here, since a
// *Group's own prefix is always required to begin with '/' (requirement 6)
// and can therefore never itself produce a joined pattern lacking one.
func TestGroupJoin_PathWithoutLeadingSlashStillPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic for a pattern not beginning with '/'")
		}
	}()
	m := muxmaster.New()
	m.Handle(http.MethodGet, "users", handler(http.StatusOK, "users"))
}

// TestGroupJoin_HandleFast covers requirement 9 for the HandleFast
// registration path, which joins the same way as Handle.
func TestGroupJoin_HandleFast(t *testing.T) {
	m := muxmaster.New()
	api := m.Group("/api/")
	api.HandleFast(http.MethodGet, "/fast", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})

	rec := get(m, "/api/fast")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/fast to match, got %d", rec.Code)
	}
	if rec2 := get(m, "/api//fast"); rec2.Code == http.StatusOK {
		t.Errorf("unexpected match for /api//fast, got %d", rec2.Code)
	}
}
