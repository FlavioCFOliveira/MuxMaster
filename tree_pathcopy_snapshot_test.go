package muxmaster

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file is the sprint-18 regression/fuzz suite for rmp #253
// (MM-2026-0033, performance.md §36-37): registration by path copying
// (copyNode) instead of whole-tree cloning must still give every tree
// pointer obtained before a registration full, permanent immutability —
// not just across a panic (already covered by
// TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched in
// tree_rollback_test.go), but across ANY subsequent successful
// registration, including the #256 static-sibling-of-wildchild splice,
// which is exactly the code path that copies an EXISTING published node
// (via copyNode in the `n.wildChild` descend branches) rather than only
// ever allocating brand new ones.
//
// reuses fingerprintNode from tree_rollback_test.go (same package).

// pathcopySnapshotRoutes is a base route set that includes a static route,
// a param route, and a regex-param route sharing a segment position with a
// param (on different subtrees, to stay conflict-free) — enough surface for
// later registrations to exercise every copyNode call site in
// addRouteInternal: the param-continuation descend (line ~241), the
// static-child-insert-before-wildchild splice (#256, line ~278-296), the
// wildchild descend for extension (line ~302), and incrementChildPrio's own
// copy-before-mutate (line ~342).
func buildPathcopyBaseMux() *Mux {
	m := New()
	m.RedirectTrailingSlash = false
	m.GET("/users/:id", idHandler("users_id"))
	m.GET("/users/:id/posts", idHandler("users_id_posts"))
	m.GET("/items/{id:[0-9]+}", idHandler("items_regex"))
	m.GET("/a/b", idHandler("a_b"))
	return m
}

var pathcopyLookups = []string{
	"/users/42", "/users/42/posts", "/users/list", "/users/list/posts",
	"/items/7", "/items/x", "/a/b", "/a/c",
}

// TestPathCopying_OldSnapshotUnaffectedByLaterStaticSiblingRegistration is
// the deterministic core of the property: capture the published root
// pointer, fingerprint it, register NEW routes that specifically add static
// siblings to the EXISTING param/regex wildchildren (the #256 code path,
// which copies those existing nodes via copyNode as it descends), and
// assert the OLD root pointer's fingerprint is byte-for-byte identical
// afterwards, AND that looking up the original routes directly against the
// OLD root pointer still gives the original answers.
func TestPathCopying_OldSnapshotUnaffectedByLaterStaticSiblingRegistration(t *testing.T) {
	m := buildPathcopyBaseMux()

	before := m.treesPtr.Load()
	if before == nil || before[idxGET] == nil {
		t.Fatal("expected a published GET tree")
	}
	rootBefore := before[idxGET]
	fpBefore := fingerprintNode(rootBefore)
	oldResults := map[string]string{}
	for _, p := range pathcopyLookups {
		var ps paramsBuf
		h, f, _, _ := rootBefore.getValue(p, &ps, false)
		oldResults[p] = fmt.Sprintf("%v/%v", h != nil, f != nil)
	}

	// New registrations: static siblings of the EXISTING wildchildren —
	// exactly the #256 splice-before-wildchild path, and exactly the
	// scenario where copyNode is applied to nodes reachable from the
	// published tree (rootBefore) rather than to brand-new nodes only.
	m.GET("/users/list", idHandler("users_list"))         // static sibling of :id
	m.GET("/items/all", idHandler("items_all"))           // static sibling of {id:[0-9]+}
	m.GET("/users/:id/comments", idHandler("users_id_c")) // second static child under the SAME param continuation node
	m.GET("/a/d", idHandler("a_d"))                       // static sibling under a plain static node

	// The root pointer captured BEFORE must be unmutated.
	if got := fingerprintNode(rootBefore); got != fpBefore {
		t.Errorf("published root node (captured before later registrations) was mutated in place:\n before: %s\n after:  %s", fpBefore, got)
	}
	// treesPtr must now point at a DIFFERENT root (a new tree was published).
	after := m.treesPtr.Load()
	if after == nil || after[idxGET] == rootBefore {
		t.Fatal("expected treesPtr to publish a new GET root after registration")
	}

	// The OLD root pointer, queried directly (bypassing dispatch/treesPtr —
	// this is what "a tree snapshot obtained before" means operationally),
	// must still answer exactly as it did before.
	for _, p := range pathcopyLookups {
		var ps paramsBuf
		h, f, _, _ := rootBefore.getValue(p, &ps, false)
		got := fmt.Sprintf("%v/%v", h != nil, f != nil)
		if got != oldResults[p] {
			t.Errorf("old snapshot lookup for %q changed after later registrations: before=%s after=%s", p, oldResults[p], got)
		}
	}

	// The new routes must be reachable through the NEW published tree, and
	// the old routes must still be reachable and unaffected through it too
	// (both trees agree on the pre-existing routes; only the new tree knows
	// about the new ones).
	newLookups := map[string]string{
		"/users/list":        "users_list",
		"/items/all":         "items_all",
		"/users/42/comments": "users_id_c",
		"/a/d":               "a_d",
		"/users/42":          "users_id",
		"/users/42/posts":    "users_id_posts",
		"/items/7":           "items_regex",
		"/a/b":               "a_b",
	}
	for p, want := range newLookups {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if got := routeID(rec); got != want {
			t.Errorf("post-registration GET %q = %q, want %q", p, got, want)
		}
	}
}

// TestPathCopying_MultipleGenerationsAllRemainValid registers three
// successive generations of routes, keeping a snapshot pointer + end-to-end
// lookup expectations after each generation, and asserts ALL THREE
// generations' snapshots remain independently correct after the final
// registration — path copying must never let a later registration corrupt
// an earlier, still-referenced generation, however many generations back.
func TestPathCopying_MultipleGenerationsAllRemainValid(t *testing.T) {
	m := New()
	m.RedirectTrailingSlash = false

	type generation struct {
		root     *node
		fp       string
		expected map[string]string // path -> route id, "" for no match
	}
	var gens []generation

	snapshot := func(expected map[string]string) generation {
		root := m.treesPtr.Load()[idxGET]
		return generation{root: root, fp: fingerprintNode(root), expected: expected}
	}

	m.GET("/p/:id", idHandler("g1_param"))
	gens = append(gens, snapshot(map[string]string{"/p/1": "g1_param", "/p/list": "g1_param"}))

	m.GET("/p/list", idHandler("g2_static")) // #256 splice into the g1 wildchild
	gens = append(gens, snapshot(map[string]string{"/p/1": "g1_param", "/p/list": "g2_static"}))

	m.GET("/p/info", idHandler("g3_static")) // another splice, now with two static siblings
	gens = append(gens, snapshot(map[string]string{"/p/1": "g1_param", "/p/list": "g2_static", "/p/info": "g3_static"}))

	for i, g := range gens {
		if got := fingerprintNode(g.root); got != g.fp {
			t.Errorf("generation %d root mutated after later registrations:\n before: %s\n after:  %s", i, g.fp, got)
		}
		for p, want := range g.expected {
			var ps paramsBuf
			h, _, _, _ := g.root.getValue(p, &ps, false)
			var got string
			if h != nil {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
				got = rec.Header().Get("X-Route-ID")
			}
			if got != want {
				t.Errorf("generation %d snapshot lookup %q = %q, want %q", i, p, got, want)
			}
		}
	}
}

// FuzzPathCopySnapshotImmutability drives a random sequence of route
// registrations against a single Mux, taking a tree snapshot before each
// registration, and after all registrations complete, re-verifies every
// snapshot's fingerprint is still exactly what it was when captured. The
// fuzz input selects, at each step, one of a small fixed set of
// non-conflicting pattern "moves" designed so every step is always legal —
// the property under test is snapshot immutability, not conflict detection
// (that is FuzzWildcardConflictOrderIndependence's job).
func FuzzPathCopySnapshotImmutability(f *testing.F) {
	for _, seed := range []uint64{0, 1, 7, 99, 0xC0FFEE} {
		f.Add(seed, uint8(6))
	}
	// Each move must be registrable no matter which subset of the PRIOR
	// moves has already run (moves target disjoint top-level segments, or a
	// static sibling of a wildchild already introduced by an earlier move
	// in the same disjoint segment) so the fuzz loop never needs to
	// recover() a legitimate conflict and can treat any panic as a finding.
	type move struct {
		pattern string
		id      string
	}
	moves := []move{
		{"/g1/:id", "g1_param"},
		{"/g1/list", "g1_static"},
		{"/g2/{id:[0-9]+}", "g2_regex"},
		{"/g2/all", "g2_static"},
		{"/g3/a", "g3_a"},
		{"/g3/b", "g3_b"},
		{"/g4/:id/x", "g4_x"},
		{"/g4/:id/y", "g4_y"},
	}

	f.Fuzz(func(t *testing.T, seed uint64, stepsRaw uint8) {
		steps := int(stepsRaw)%len(moves) + 1
		rng := rand.New(rand.NewPCG(seed, seed+1))
		perm := rng.Perm(len(moves))[:steps]

		m := New()
		m.RedirectTrailingSlash = false

		type snap struct {
			root *node
			fp   string
		}
		var snaps []snap

		for _, idx := range perm {
			mv := moves[idx]
			// Snapshot BEFORE this registration (nil root is valid: fingerprintNode(nil) == "nil").
			var root *node
			if tp := m.treesPtr.Load(); tp != nil {
				root = tp[idxGET]
			}
			snaps = append(snaps, snap{root: root, fp: fingerprintNode(root)})

			id := mv.id
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("registration of %q panicked unexpectedly (moves are designed to be pairwise non-conflicting): %v", mv.pattern, r)
					}
				}()
				m.GET(mv.pattern, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Route-ID", id)
					w.WriteHeader(http.StatusOK)
				})
			}()
		}

		for i, s := range snaps {
			if got := fingerprintNode(s.root); got != s.fp {
				t.Fatalf("snapshot #%d (root=%p) mutated by a later registration:\n before: %s\n after:  %s",
					i, s.root, s.fp, got)
			}
		}
	})
}

// TestPathCopying_ConcurrentReadersDuringStaticSiblingRegistration is a
// -race stress test: readers hammer GET while a writer goroutine registers
// static siblings into existing param/regex wildchildren (#256's splice
// path, which copies live nodes reachable from the currently published
// tree). It asserts (a) go test -race finds nothing, (b) readers never
// observe a torn/partial tree (every response is either a pre-registration
// or post-registration answer, never a panic or a 5xx), and (c) it
// eventually observes the new route once the writer publishes it.
func TestPathCopying_ConcurrentReadersDuringStaticSiblingRegistration(t *testing.T) {
	m := New()
	m.RedirectTrailingSlash = false
	m.GET("/users/:id", idHandler("param"))

	const readers = 8
	var stop atomic.Bool
	var wg sync.WaitGroup
	var sawStatic atomic.Bool
	var panicked atomic.Bool

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recover() != nil {
					panicked.Store(true)
				}
			}()
			for !stop.Load() {
				for _, p := range []string{"/users/42", "/users/list", "/users/other"} {
					rec := httptest.NewRecorder()
					m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
					if p == "/users/list" && routeID(rec) == "static" {
						sawStatic.Store(true)
					}
					if rec.Code >= 500 {
						panicked.Store(true)
					}
				}
			}
		}()
	}

	m.GET("/users/list", idHandler("static")) // #256 splice under concurrent read load
	m.GET("/users/other", idHandler("other")) // a second splice into the (now regenerated) wildchild

	// Give the reader goroutines a bounded window to actually get scheduled
	// and observe the post-registration tree before stopping them — without
	// -race (or under a busy CI runner), registration can complete before
	// any reader goroutine has run even once, which would make sawStatic's
	// absence a scheduling artefact rather than evidence of anything.
	deadline := time.Now().Add(2 * time.Second)
	for !sawStatic.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	stop.Store(true)
	wg.Wait()

	if panicked.Load() {
		t.Fatal("a reader goroutine panicked or observed a 5xx during concurrent registration")
	}
	if !sawStatic.Load() {
		t.Error("no reader ever observed the newly registered /users/list static route (registration may not have published)")
	}

	// Final state must be fully correct post-registration.
	for path, want := range map[string]string{"/users/42": "param", "/users/list": "static", "/users/other": "other"} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := routeID(rec); got != want {
			t.Errorf("final GET %q = %q, want %q", path, got, want)
		}
	}
}
