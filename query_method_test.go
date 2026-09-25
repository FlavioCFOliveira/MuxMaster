// Tests for the HTTP QUERY method (RFC 10008, rmp task #261, sprint 19).
//
// QUERY is a standard HTTP method — safe and idempotent like GET, but it
// carries request content in its body like POST. See specification/routing.md
// §9 (rules 82-89), specification/groups.md §3 (rule 8), specification/
// error-handling.md §5 (rule 25), specification/performance.md §6 (rule 26),
// and specification/compatibility.md §6 (rules 17-19) for the full spec this
// file exercises.
package muxmaster_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// ── Dispatch: static, 1-param, multi-param, catch-all — every registration API ──

func TestQueryMethod_Handle_Static(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Handle(muxmaster.MethodQuery, "/search", handler(http.StatusOK, "handle-static"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "handle-static" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_HandleFunc_Static(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.HandleFunc(muxmaster.MethodQuery, "/search", handler(http.StatusOK, "handlefunc-static"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "handlefunc-static" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_QUERY_Convenience_Static(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search", handler(http.StatusOK, "query-static"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "query-static" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_QUERYE_Static(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERYE("/search", func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("querye-static"))
		return nil
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "querye-static" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_QUERYE_ErrorDelegatesToErrorHandler(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		var he muxmaster.HTTPError
		if errors.As(err, &he) {
			w.WriteHeader(he.StatusCode())
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}
	m.QUERYE("/search", func(http.ResponseWriter, *http.Request) error {
		return muxmaster.Error(http.StatusUnprocessableEntity, errors.New("invalid query"))
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d, want 422", rec.Code)
	}
}

// TestQueryMethod_QUERYE_ErrorWithoutHandler_Defaults500 verifies the
// documented default when no ErrorHandler is configured (error-handling.md
// §24): a plain 500, regardless of the error's own status code.
func TestQueryMethod_QUERYE_ErrorWithoutHandler_Defaults500(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERYE("/search", func(http.ResponseWriter, *http.Request) error {
		return muxmaster.Error(http.StatusUnprocessableEntity, errors.New("invalid query"))
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d, want 500 (default error handler)", rec.Code)
	}
}

func TestQueryMethod_QUERYFast_Static(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	var gotParams muxmaster.Params
	m.QUERYFast("/search", func(w http.ResponseWriter, _ *http.Request, ps muxmaster.Params) {
		gotParams = ps
		w.WriteHeader(http.StatusOK)
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if gotParams != nil {
		t.Errorf("expected nil Params for a static fast route, got %v", gotParams)
	}
}

func TestQueryMethod_OneParam(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/collections/:name", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, muxmaster.PathParam(r, "name"))
	})

	rec := do(m, muxmaster.MethodQuery, "/collections/books")
	if rec.Code != http.StatusOK || rec.Body.String() != "books" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_MultiParam(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/collections/:name/shards/:shard", func(w http.ResponseWriter, r *http.Request) {
		ps := muxmaster.ParamsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, ps.Get("name")+"/"+ps.Get("shard"))
	})

	rec := do(m, muxmaster.MethodQuery, "/collections/books/shards/3")
	if rec.Code != http.StatusOK || rec.Body.String() != "books/3" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_MultiParam_Fast(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	var gotName, gotShard string
	m.QUERYFast("/collections/:name/shards/:shard", func(w http.ResponseWriter, _ *http.Request, ps muxmaster.Params) {
		gotName = ps.Get("name")
		gotShard = ps.Get("shard")
		w.WriteHeader(http.StatusOK)
	})

	rec := do(m, muxmaster.MethodQuery, "/collections/books/shards/3")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if gotName != "books" || gotShard != "3" {
		t.Errorf("name=%q shard=%q", gotName, gotShard)
	}
}

func TestQueryMethod_CatchAll(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search/*rest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, muxmaster.PathParam(r, "rest"))
	})

	rec := do(m, muxmaster.MethodQuery, "/search/a/b/c")
	if rec.Code != http.StatusOK || rec.Body.String() != "/a/b/c" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_Group_QUERY(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	g := m.Group("/api/v1")
	g.QUERY("/search", handler(http.StatusOK, "group-query"))

	rec := do(m, muxmaster.MethodQuery, "/api/v1/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "group-query" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestQueryMethod_Group_QUERYE(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	g := m.Group("/api/v1")
	g.QUERYE("/search", func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("group-querye"))
		return nil
	})

	rec := do(m, muxmaster.MethodQuery, "/api/v1/search")
	if rec.Code != http.StatusOK || rec.Body.String() != "group-querye" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestQueryMethod_Group_HandleFast_NoDedicatedShortcut verifies groups.md §3:
// *Group exposes no QUERYFast convenience method, but a fast QUERY route can
// still be registered via (*Group).HandleFast(muxmaster.MethodQuery, ...).
func TestQueryMethod_Group_HandleFast_NoDedicatedShortcut(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	g := m.Group("/api/v1")
	g.HandleFast(muxmaster.MethodQuery, "/search", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})

	rec := do(m, muxmaster.MethodQuery, "/api/v1/search")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
}

// ── Request body is readable by the handler ──────────────────────────────────

func TestQueryMethod_BodyReadableByHandler(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	var gotBody string
	m.QUERY("/search", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(muxmaster.MethodQuery, "/search", strings.NewReader(`{"select":["title"]}`))
	req.Header.Set("Content-Type", "application/json")
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if gotBody != `{"select":["title"]}` {
		t.Errorf("body=%q", gotBody)
	}
}

func TestQueryMethod_BodyReadableByFastHandler(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	var gotBody string
	m.QUERYFast("/search", func(w http.ResponseWriter, r *http.Request, _ muxmaster.Params) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(muxmaster.MethodQuery, "/search", strings.NewReader("raw-query"))
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if gotBody != "raw-query" {
		t.Errorf("body=%q", gotBody)
	}
}

// ── ANY and Match ─────────────────────────────────────────────────────────────

func TestQueryMethod_ANY_Covers_Query(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.ANY("/any", handler(http.StatusOK, "any"))

	rec := do(m, muxmaster.MethodQuery, "/any")
	if rec.Code != http.StatusOK {
		t.Fatalf("ANY did not register QUERY: code=%d", rec.Code)
	}
}

func TestQueryMethod_Group_ANY_Covers_Query(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	g := m.Group("/api")
	g.ANY("/any", handler(http.StatusOK, "any"))

	rec := do(m, muxmaster.MethodQuery, "/api/any")
	if rec.Code != http.StatusOK {
		t.Fatalf("Group.ANY did not register QUERY: code=%d", rec.Code)
	}
}

func TestQueryMethod_Match(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Match([]string{http.MethodGet, muxmaster.MethodQuery}, "/m", handler(http.StatusOK, "matched"))

	for _, method := range []string{http.MethodGet, muxmaster.MethodQuery} {
		rec := do(m, method, "/m")
		if rec.Code != http.StatusOK {
			t.Errorf("Match %s: code=%d", method, rec.Code)
		}
	}
	rec := do(m, http.MethodPost, "/m")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /m: code=%d, want 405 (not registered via Match)", rec.Code)
	}
}

// ── Allow header (405 and auto-OPTIONS) ──────────────────────────────────────

// TestQueryMethod_AllowHeader_GETOnly_QueryRequested exercises
// error-handling.md §8 / routing.md §4.7 rule 61: a QUERY request against a
// path that only has GET registered gets 405 with Allow "GET, OPTIONS".
func TestQueryMethod_AllowHeader_GETOnly_QueryRequested(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.GET("/resource", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/resource")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, OPTIONS" {
		t.Errorf("Allow=%q, want %q", allow, "GET, OPTIONS")
	}
}

// TestQueryMethod_AllowHeader_GETAndQuery_PostRequested exercises the same
// rule with QUERY present in the registered set: Allow must list QUERY
// between the standard methods and OPTIONS.
func TestQueryMethod_AllowHeader_GETAndQuery_PostRequested(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.GET("/resource", handler(http.StatusOK, "ok"))
	m.QUERY("/resource", handler(http.StatusOK, "ok"))

	rec := do(m, http.MethodPost, "/resource")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, QUERY, OPTIONS" {
		t.Errorf("Allow=%q, want %q", allow, "GET, QUERY, OPTIONS")
	}
}

// TestQueryMethod_AllowHeader_FullOrder registers every standard method on
// one path and asserts the exact Allow order from routing.md §4.7 rule 61:
// "GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, OPTIONS".
func TestQueryMethod_AllowHeader_FullOrder(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	h := handler(http.StatusOK, "ok")
	m.GET("/full", h)
	m.HEAD("/full", h)
	m.POST("/full", h)
	m.PUT("/full", h)
	m.PATCH("/full", h)
	m.DELETE("/full", h)
	m.CONNECT("/full", h)
	m.TRACE("/full", h)
	m.QUERY("/full", h)
	// OPTIONS is deliberately NOT registered — the router adds it to Allow
	// unconditionally (routing.md §4.7 rule 61).

	const want = "GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY, OPTIONS"

	// The automatic OPTIONS response's Allow header lists every registered
	// method for the path plus OPTIONS itself (routing.md §4.6 rule 58),
	// which is the same list the 405 handler uses (§4.7 rule 61) — checking
	// it here exercises the exact same allowed()/allowTable computation.
	rec := do(m, http.MethodOptions, "/full")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS code=%d, want 204", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != want {
		t.Errorf("auto-OPTIONS Allow=%q, want %q", allow, want)
	}
}

// TestQueryMethod_AutoOptions_IncludesQuery is a focused check that the
// automatic OPTIONS response's Allow header includes QUERY when QUERY is
// registered for the path (routing.md §4.6 rule 58, §4.7 rule 61).
func TestQueryMethod_AutoOptions_IncludesQuery(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.GET("/resource", handler(http.StatusOK, "ok"))
	m.QUERY("/resource", handler(http.StatusOK, "ok"))

	rec := do(m, http.MethodOptions, "/resource")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d, want 204", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !contains(allow, muxmaster.MethodQuery) {
		t.Errorf("Allow=%q missing QUERY", allow)
	}
}

// ── Redirects ─────────────────────────────────────────────────────────────────

// TestQueryMethod_RedirectTrailingSlash_DefaultCode307 exercises routing.md
// §4.4 rule 53: QUERY is not GET/HEAD, so the default TSR redirect uses 307
// (preserving both method and body — RFC 10008 §2.5), not 301.
func TestQueryMethod_RedirectTrailingSlash_DefaultCode307(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search/", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("code=%d, want 307", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/search/" {
		t.Errorf("Location=%q, want /search/", loc)
	}
}

// TestQueryMethod_RedirectFixedPath exercises routing.md §4.5 rule 57 for
// QUERY: a non-clean path with a registered handler at its cleaned form
// redirects with 307 by default.
func TestQueryMethod_RedirectFixedPath(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.RedirectFixedPath = true
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/search/.")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("code=%d, want 307", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/search" {
		t.Errorf("Location=%q, want /search", loc)
	}
}

// TestQueryMethod_CustomRedirectCode_Honoured exercises configuration.md
// §4.1 rule 19: a non-zero Mux.RedirectCode overrides the QUERY-specific
// 307 default for every redirect the router issues, including QUERY's.
func TestQueryMethod_CustomRedirectCode_Honoured(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.RedirectCode = http.StatusPermanentRedirect // 308
	m.QUERY("/search/", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("code=%d, want 308 (custom RedirectCode)", rec.Code)
	}
}

// ── Introspection ─────────────────────────────────────────────────────────────

func TestQueryMethod_Routes_ListsQuery(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	found := false
	for _, ri := range m.Routes() {
		if ri.Method == muxmaster.MethodQuery && ri.Pattern == "/search" {
			found = true
		}
	}
	if !found {
		t.Errorf("Routes() did not list the QUERY /search route: %+v", m.Routes())
	}
}

func TestQueryMethod_Lookup(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/collections/:name", handler(http.StatusOK, "ok"))

	h, ps, ok := m.Lookup(muxmaster.MethodQuery, "/collections/books")
	if !ok || h == nil {
		t.Fatalf("Lookup failed: ok=%v h=%v", ok, h)
	}
	if got := ps.Get("name"); got != "books" {
		t.Errorf("param name=%q, want books", got)
	}
}

func TestQueryMethod_Walk(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	seen := false
	err := m.Walk(func(method, pattern string, _ http.Handler) error {
		if method == muxmaster.MethodQuery && pattern == "/search" {
			seen = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}
	if !seen {
		t.Error("Walk did not visit the QUERY /search route")
	}
}

func TestQueryMethod_WalkFast(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERYFast("/search", func(http.ResponseWriter, *http.Request, muxmaster.Params) {})

	seen := false
	err := m.WalkFast(func(method, pattern string, _ muxmaster.FastHandler) error {
		if method == muxmaster.MethodQuery && pattern == "/search" {
			seen = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkFast error: %v", err)
	}
	if !seen {
		t.Error("WalkFast did not visit the QUERY /search fast route")
	}
}

// ── Duplicate registration panics ────────────────────────────────────────────

func TestQueryMethod_DuplicateRegistration_Panics(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate QUERY registration")
		}
	}()
	m.QUERY("/search", handler(http.StatusOK, "ok-2"))
}

func TestQueryMethod_QUERYFast_DuplicateRegistration_Panics(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERYFast("/search", func(http.ResponseWriter, *http.Request, muxmaster.Params) {})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate QUERYFast registration")
		}
	}()
	m.QUERYFast("/search", func(http.ResponseWriter, *http.Request, muxmaster.Params) {})
}

// ── Middleware ────────────────────────────────────────────────────────────────

func TestQueryMethod_UseMiddleware_Wraps(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	})
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Header().Get("X-Wrapped") != "yes" {
		t.Errorf("Use middleware did not wrap the QUERY route")
	}
}

func TestQueryMethod_PreMiddleware_Wraps(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Pre", "yes")
			next.ServeHTTP(w, r)
		})
	})
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Header().Get("X-Pre") != "yes" {
		t.Errorf("Pre middleware did not wrap the QUERY route")
	}
}

func TestQueryMethod_PreMiddleware_WrapsFastRoute(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Pre", "yes")
			next.ServeHTTP(w, r)
		})
	})
	m.QUERYFast("/search", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Header().Get("X-Pre") != "yes" {
		t.Errorf("Pre middleware did not wrap the QUERYFast route")
	}
}

func TestQueryMethod_UseFastMiddleware_WrapsFastRoute(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.UseFast(func(next muxmaster.FastHandler) muxmaster.FastHandler {
		return func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
			w.Header().Set("X-UseFast", "yes")
			next(w, r, ps)
		}
	})
	m.QUERYFast("/search", func(w http.ResponseWriter, _ *http.Request, _ muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})

	rec := do(m, muxmaster.MethodQuery, "/search")
	if rec.Header().Get("X-UseFast") != "yes" {
		t.Errorf("UseFast middleware did not wrap the QUERYFast route")
	}
}

// ── Other methods unaffected ──────────────────────────────────────────────────

// TestQueryMethod_OtherMethodsUnaffected registers QUERY alongside every
// other standard method on the same path (exercising the reindexed
// methodTrees array — idxQUERY sits between idxTRACE and the shifted
// idxWild) and verifies each method still reaches its own, distinct
// handler with no cross-talk.
func TestQueryMethod_OtherMethodsUnaffected(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	register := []struct {
		method string
		body   string
	}{
		{http.MethodGet, "get"},
		{http.MethodHead, "head"},
		{http.MethodPost, "post"},
		{http.MethodPut, "put"},
		{http.MethodPatch, "patch"},
		{http.MethodDelete, "delete"},
		{http.MethodConnect, "connect"},
		{http.MethodTrace, "trace"},
		{muxmaster.MethodQuery, "query"},
	}
	for _, rt := range register {
		m.Handle(rt.method, "/shared", handler(http.StatusOK, rt.body))
	}

	for _, rt := range register {
		rec := do(m, rt.method, "/shared")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: code=%d", rt.method, rec.Code)
			continue
		}
		// httptest.NewRecorder captures exactly what the handler writes
		// (it is not a real net/http transport, so it never strips a HEAD
		// response body the way a real server's writer would); each method
		// must reach its own distinct handler body with no cross-talk.
		if rec.Body.String() != rt.body {
			t.Errorf("%s: body=%q, want %q", rt.method, rec.Body.String(), rt.body)
		}
	}
}

// TestQueryMethod_UnregisteredMethodsStillGetsFallthrough404 verifies that
// registering QUERY on one path does not create phantom routes on other
// methods for that same path.
func TestQueryMethod_UnregisteredMethodsStillGetsFallthrough404(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.QUERY("/search", handler(http.StatusOK, "ok"))

	rec := do(m, http.MethodGet, "/search")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /search: code=%d, want 405 (only QUERY registered)", rec.Code)
	}

	rec2 := do(m, muxmaster.MethodQuery, "/does-not-exist")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("QUERY /does-not-exist: code=%d, want 404", rec2.Code)
	}
}
