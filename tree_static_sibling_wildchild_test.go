package muxmaster

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// This file is the regression suite for rmp #256 / MM-2026-0256: a static
// route may now be registered as a sibling of an existing named-parameter or
// regex-parameter wildchild (spliced in just before the wildchild, which
// must stay last — see the "wildchild is always last" invariant getValue
// relies on via n.children[len(n.children)-1]).
//
// specification/routing.md §4.2 (rules 48-51) requires static > param >
// catch-all matching precedence to hold regardless of registration order.
// Before rmp #256, registering the static sibling AFTER the param/regex
// wildchild panicked; only the reverse order worked. This suite proves both
// orders now succeed and produce IDENTICAL routing behaviour, and that the
// two conditions the diff explicitly keeps as hard, order-independent
// conflicts (catch-all sibling, rule 68; duplicate wildcard at the same
// position, rule 71) still panic in EVERY registration order.

// wildChildIsLast walks every node in the subtree and asserts that whenever
// wildChild is true, the last element of n.children has nType != static —
// i.e. it is the actual param/regexParam/catch-all-wrapper node. This is the
// structural invariant getValue's `n.children[len(n.children)-1]` shortcut
// depends on for correctness; if #256's splice-before-wildchild logic ever
// puts the new static child AFTER the wildchild instead of before it, this
// check catches it even if a same-day lookup test happens not to.
func wildChildIsLast(t *testing.T, n *node, path string) {
	t.Helper()
	if n == nil {
		return
	}
	if n.wildChild {
		if len(n.children) == 0 {
			t.Errorf("node at %q has wildChild=true but no children", path)
			return
		}
		last := n.children[len(n.children)-1]
		if last.nType != param && last.nType != regexParam && last.nType != wildcard &&
			(last.nType != static || !last.wildChild) { // catch-all wrapper node
			t.Errorf("node at %q: wildChild=true but last child has nType=%d (not a wildcard-family node) — "+
				"the 'wildchild is always last' invariant is broken", path, last.nType)
		}
		// Every child up to (but not including) the last must be indexed in
		// n.indices, and n.indices must have exactly len(children)-1 bytes.
		if len(n.indices) != len(n.children)-1 {
			t.Errorf("node at %q: len(indices)=%d but len(children)-1=%d (wildchild excluded) — "+
				"indices/children desynchronised", path, len(n.indices), len(n.children)-1)
		}
	} else if len(n.indices) != len(n.children) {
		t.Errorf("node at %q: wildChild=false but len(indices)=%d != len(children)=%d",
			path, len(n.indices), len(n.children))
	}
	for _, c := range n.children {
		wildChildIsLast(t, c, path+">"+c.path)
	}
}

func newTestMux() *Mux {
	m := New()
	m.RedirectTrailingSlash = false
	return m
}

func idHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route-ID", id)
		w.WriteHeader(http.StatusOK)
	}
}

func routeID(rec *httptest.ResponseRecorder) string {
	if rec.Code != http.StatusOK {
		return ""
	}
	return rec.Header().Get("X-Route-ID")
}

// TestStaticSiblingOfParamWildchild_BothOrders proves /users/list (static)
// and /users/:id (param) resolve identically whether the static route is
// registered before or after the param route.
func TestStaticSiblingOfParamWildchild_BothOrders(t *testing.T) {
	build := func(staticFirst bool) *Mux {
		m := newTestMux()
		reg := func(pattern, id string) { m.GET(pattern, idHandler(id)) }
		if staticFirst {
			reg("/users/list", "static")
			reg("/users/:id", "param")
		} else {
			reg("/users/:id", "param")
			reg("/users/list", "static")
		}
		return m
	}

	// NOTE on "/users/listx" and "/users/lis" (rmp #259 / DIV-001, fixed):
	// MuxMaster's radix tree now performs bounded backtracking — if the
	// static child chosen over an available wildchild sibling ultimately
	// fails to match, getValue resumes at the wildchild instead of
	// returning 404 outright. This aligns with routing.md rule 49 ("static
	// routes always outrank named parameters" implies the param is still
	// considered when static doesn't pan out) and with chi/bunrouter, which
	// both fall back the same way. See tree.go's backtrackStack (with its
	// "why no cap is needed" proof) and getValue's `goto fail` retry loop
	// for the bounded (non-exponential) implementation.
	lookups := map[string]string{
		"/users/list":  "static", // static must win per rule 49
		"/users/42":    "param",
		"/users/listx": "param", // static "list" fails on the rest of the path -> falls back to :id
		"/users/lis":   "param", // same fallback
		"/users/":      "",      // empty param segment: no match (RedirectTrailingSlash off)
	}

	for _, order := range []bool{true, false} {
		m := build(order)
		wildChildIsLast(t, m.treesPtr.Load()[idxGET], "/users")
		for path, want := range lookups {
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			got := routeID(rec)
			if got != want {
				t.Errorf("order(staticFirst=%v) GET %q = %q, want %q", order, path, got, want)
			}
		}
	}
}

// TestStaticSiblingOfRegexParamWildchild_BothOrders is the regex-param
// analogue: /items/all (static) vs /items/{id:[0-9]+} (regexParam).
func TestStaticSiblingOfRegexParamWildchild_BothOrders(t *testing.T) {
	build := func(staticFirst bool) *Mux {
		m := newTestMux()
		reg := func(pattern, id string) { m.GET(pattern, idHandler(id)) }
		if staticFirst {
			reg("/items/all", "static")
			reg("/items/{id:[0-9]+}", "regex")
		} else {
			reg("/items/{id:[0-9]+}", "regex")
			reg("/items/all", "static")
		}
		return m
	}

	lookups := map[string]string{
		"/items/all": "static", // static wins over regex per rule 48/49
		"/items/42":  "regex",
		"/items/abc": "", // regex does not match, no plain-param fallback registered
	}

	for _, order := range []bool{true, false} {
		m := build(order)
		wildChildIsLast(t, m.treesPtr.Load()[idxGET], "/items")
		for path, want := range lookups {
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			got := routeID(rec)
			if got != want {
				t.Errorf("order(staticFirst=%v) GET %q = %q, want %q", order, path, got, want)
			}
		}
	}
}

// TestMultipleStaticSiblingsInterleavedWithWildchild registers several
// static siblings before AND after an existing param wildchild, in mixed
// order, and verifies every one of them remains independently reachable and
// the wildchild remains reachable and last.
func TestMultipleStaticSiblingsInterleavedWithWildchild(t *testing.T) {
	m := newTestMux()
	m.GET("/users/:id", idHandler("param"))
	m.GET("/users/list", idHandler("list"))
	m.GET("/users/info", idHandler("info"))
	m.GET("/users/list2", idHandler("list2")) // shares prefix "list" with an existing sibling
	m.GET("/users/a", idHandler("a"))

	wildChildIsLast(t, m.treesPtr.Load()[idxGET], "/users")

	lookups := map[string]string{
		"/users/list":  "list",
		"/users/info":  "info",
		"/users/list2": "list2",
		"/users/a":     "a",
		"/users/999":   "param",
		// "/users/lis" is a partial prefix of the static children "list"/
		// "list2" — neither matches the rest of the path, so the tree now
		// backtracks to :id (rmp #259 / DIV-001, fixed). See the NOTE in
		// TestStaticSiblingOfParamWildchild_BothOrders.
		"/users/lis": "param",
	}
	for path, want := range lookups {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := routeID(rec); got != want {
			t.Errorf("GET %q = %q, want %q", path, got, want)
		}
	}
}

// TestCatchAllSiblingConflict_PanicsInBothOrders is the negative-space
// counterpart to #256: registering a static route as a sibling of an
// EXISTING CATCH-ALL wildchild is still a hard conflict (rule 68), and so is
// the reverse order (catch-all registered after an existing static
// sibling) — both must panic, keeping the conflict itself order-independent
// even though the static/param case above is now order-independent success.
func TestCatchAllSiblingConflict_PanicsInBothOrders(t *testing.T) {
	mustPanic := func(t *testing.T, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected a panic, got none")
			}
		}()
		fn()
	}

	t.Run("catch-all first, static second", func(t *testing.T) {
		m := newTestMux()
		m.GET("/static/*filepath", idHandler("catchall"))
		mustPanic(t, func() { m.GET("/static/list", idHandler("static")) })
	})

	t.Run("static first, catch-all second", func(t *testing.T) {
		m := newTestMux()
		m.GET("/static/list", idHandler("static"))
		mustPanic(t, func() { m.GET("/static/*filepath", idHandler("catchall")) })
	})
}

// TestDuplicateWildcardConflict_PanicsInBothOrders covers rule 71: two
// different named parameters (or two different regex parameters) at the
// same tree position must panic regardless of which is registered first —
// the path-copying (#253) and splice-before-wildchild (#256) changes must
// not have accidentally relaxed this into a silent shadow.
func TestDuplicateWildcardConflict_PanicsInBothOrders(t *testing.T) {
	mustPanic := func(t *testing.T, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected a panic, got none")
			}
		}()
		fn()
	}

	t.Run("param vs param, :a then :b", func(t *testing.T) {
		m := newTestMux()
		m.GET("/x/:a", idHandler("a"))
		mustPanic(t, func() { m.GET("/x/:b", idHandler("b")) })
	})
	t.Run("param vs param, :b then :a", func(t *testing.T) {
		m := newTestMux()
		m.GET("/x/:b", idHandler("b"))
		mustPanic(t, func() { m.GET("/x/:a", idHandler("a")) })
	})
	t.Run("regex vs regex, different name/expr, both orders", func(t *testing.T) {
		m1 := newTestMux()
		m1.GET("/y/{id:[0-9]+}", idHandler("id"))
		mustPanic(t, func() { m1.GET("/y/{name:[a-z]+}", idHandler("name")) })

		m2 := newTestMux()
		m2.GET("/y/{name:[a-z]+}", idHandler("name"))
		mustPanic(t, func() { m2.GET("/y/{id:[0-9]+}", idHandler("id")) })
	})
}
