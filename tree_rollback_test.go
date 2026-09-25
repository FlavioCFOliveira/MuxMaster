package muxmaster

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fingerprintNode returns a deep, deterministic textual snapshot of the
// subtree rooted at n — every field that addRouteInternal/insertChild can
// mutate (path, indices, wildChild, nType, priority, pattern, regexp name
// bounds, handler/fast presence) plus the same fingerprint recursively for
// every child. Two fingerprints are equal iff no reachable node's mutable
// state differs, which is exactly what "the published tree was not mutated
// in place" needs to prove — including for children that path copying
// intentionally leaves shared (as opposed to copied) between two tree
// generations.
func fingerprintNode(n *node) string {
	if n == nil {
		return "nil"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{path:%q indices:%q wildChild:%v nType:%d priority:%d pattern:%q "+
		"regexpNameEnd:%d maxParams:%d handler:%v fast:%v children:[",
		n.path, n.indices, n.wildChild, n.nType, n.priority, n.pattern,
		n.regexpNameEnd, n.maxParams, n.handler != nil, n.fast != nil)
	for i, c := range n.children {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fingerprintNode(c))
	}
	b.WriteString("]}")
	return b.String()
}

// TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched is the
// regression test for rmp task #253 / MM-2026-0033 / performance.md §37: a
// registration that panics partway through mutating the copied insertion
// path must leave the published tree, and any tree snapshot obtained
// before the panic, completely unaffected.
//
// The scenario is deliberately chosen so that, WITHOUT copy-on-write, a
// live, already-published node is mutated before the panic fires — a plain
// "the panic happens before any node write" case (like registering a
// pattern with too many optional segments) would pass even with cloning
// removed entirely, because nothing was touched yet, and would not
// distinguish a correct implementation from a broken one.
//
// Registering "/users" alone makes the root node itself hold path="/users"
// with its own handler (addRouteInternal inserts directly into the root
// when the tree is still empty). Registering "/users:id/posts:x:y" next
// walks into that SAME root (no path split needed — the common prefix is
// exactly "/users"), and its first wildcard segment (":id") is valid and
// gets attached directly to the root — mutating the root's children and
// wildChild fields. Its second segment ("posts:x:y") contains two wildcard
// markers in one path segment, which is invalid: insertChild panics with
// "only one wildcard per path segment is allowed" on the SECOND loop
// iteration, strictly after the root was already mutated in the first.
//
// Under whole-tree cloning or the current path-copying implementation, the
// root touched during this call is always a private copy: the panic
// discards it, and the previously published root is never touched. Under a
// hypothetical no-copy variant (like experiments/E1b — commenting out the
// `trees[idx] = copyNode(trees[idx])` line in Handle/HandleFast), this test
// fails: the root's wildChild/children/priority/maxParams fields are
// mutated in place before the panic fires. Verified manually against such
// a build as part of rmp task #253; see the sprint 18 report for the
// captured before/after fingerprint diff.
func TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched(t *testing.T) {
	m := New()
	m.GET("/users", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler", "users-v1")
		w.WriteHeader(http.StatusOK)
	})

	// Sanity: /users serves before the panicking registration.
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Handler") != "users-v1" {
		t.Fatalf("pre-panic /users: status=%d header=%q, want 200 users-v1", rec.Code, rec.Header().Get("X-Handler"))
	}

	// Capture the published root pointer and a deep fingerprint of it
	// BEFORE the panicking registration — this is the "previously obtained
	// tree snapshot" the task asks to prove is unaffected.
	before := m.treesPtr.Load()
	if before == nil || before[idxGET] == nil {
		t.Fatal("expected a published GET tree before the panicking registration")
	}
	rootBefore := before[idxGET]
	fpBefore := fingerprintNode(rootBefore)

	// Trigger the panic: two wildcard markers in one path segment.
	const badPattern = "/users:id/posts:x:y"
	panicMsg := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicMsg = fmt.Sprint(r)
			}
		}()
		m.GET(badPattern, func(w http.ResponseWriter, _ *http.Request) {})
	}()
	if panicMsg == "" {
		t.Fatal("expected addRouteInternal to panic on a double-wildcard path segment")
	}
	if !strings.Contains(panicMsg, "only one wildcard per path segment is allowed") {
		t.Fatalf("panic message = %q, want it to mention the double-wildcard conflict", panicMsg)
	}

	// The root pointer captured before the panic must be byte-for-byte
	// unmutated: no field of it (or anything reachable from it) may have
	// changed, even though addRouteInternal did mutate a node with this
	// exact path during the failed call.
	if got := fingerprintNode(rootBefore); got != fpBefore {
		t.Errorf("live root node was mutated in place by the panicking registration:\n before: %s\n after:  %s", fpBefore, got)
	}

	// The published tree must still point at the exact same root object —
	// proving treesPtr.Store was never reached for this registration.
	after := m.treesPtr.Load()
	if after == nil || after[idxGET] != rootBefore {
		t.Errorf("treesPtr published a different GET root after the panic — registration was not fully rolled back")
	}

	// End-to-end: /users must still route to its original handler.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Handler") != "users-v1" {
		t.Errorf("post-panic /users: status=%d header=%q, want 200 users-v1 (tree corruption)", rec.Code, rec.Header().Get("X-Handler"))
	}

	// The failed registration's own paths must not have leaked in either.
	for _, p := range []string{"/users/id", "/users/posts", "/users:id", "/users:id/posts"} {
		rec = httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("post-panic %q unexpectedly routes (status 200) — partial registration leaked", p)
		}
	}
}
