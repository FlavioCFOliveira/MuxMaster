package muxmaster_test

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// This file is the sprint-18 regression/fuzz suite for rmp #256
// (MM-2026-0256): registration order must never change observable routing
// behaviour. It has two halves:
//
//  1. A NON-conflicting pattern set that must register successfully and
//     route identically in every permutation (order-independent SUCCESS).
//  2. A set of intentionally conflicting pattern pairs (catch-all vs static
//     sibling — rule 68; duplicate wildcard at the same position — rule 71)
//     that must panic in EVERY registration order (order-independent
//     CONFLICT — the specific symmetry property #256 introduces for the
//     catch-all case, generalised here to make sure no other conflict class
//     was accidentally made order-DEPENDENT, which would mean a route is
//     silently shadowed in one order and rejected in the other).

type orderIndepRoute struct {
	pattern string
	id      string
}

// orderIndepPatterns is a fixed, deliberately non-conflicting set covering:
// a static sibling of a param wildchild (#256's primary scenario), a static
// sibling of a regex-param wildchild, a deeper param continuation with two
// static children, and an independent subtree — so permuting registration
// order also permutes which subtree is built "first" without ever placing
// two conflicting wildcards at the same tree position.
var orderIndepPatterns = []orderIndepRoute{
	{"/users/list", "users_list"},
	{"/users/:id", "users_id"},
	{"/items/all", "items_all"},
	{"/items/{id:[0-9]+}", "items_regex"},
	{"/a/b/c", "abc"},
	{"/a/:x/c", "a_x_c"},
	{"/a/:x/d", "a_x_d"},
	{"/a/e", "a_e"},
}

var orderIndepLookups = []string{
	"/users/list", "/users/42", "/users/",
	"/items/all", "/items/42", "/items/abc",
	"/a/b/c", "/a/z/c", "/a/z/d", "/a/z/e", "/a/e",
	"/nonexistent", "/users/list/extra",
}

// buildOrderIndepMux registers orderIndepPatterns at the given permutation
// of indices and returns the resulting Mux, or a non-nil panic value if
// registration failed (which the non-conflicting set must never do).
func buildOrderIndepMux(perm []int) (mux *muxmaster.Mux, panicVal any) {
	defer func() { panicVal = recover() }()
	mux = muxmaster.New()
	mux.RedirectTrailingSlash = false
	for _, idx := range perm {
		r := orderIndepPatterns[idx]
		id := r.id
		mux.GET(r.pattern, func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Route-ID", id)
			w.WriteHeader(http.StatusOK)
		})
	}
	return mux, nil
}

func snapshotRouting(mux *muxmaster.Mux, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		out[p] = fmt.Sprintf("%d:%s", rec.Code, rec.Header().Get("X-Route-ID"))
	}
	return out
}

// TestRouteRegistrationOrderIndependence_TableDriven checks a handful of
// concrete, hand-picked permutations of orderIndepPatterns deterministically
// before the fuzz target explores the rest of the permutation space.
func TestRouteRegistrationOrderIndependence_TableDriven(t *testing.T) {
	n := len(orderIndepPatterns)
	identity := make([]int, n)
	for i := range identity {
		identity[i] = i
	}
	reversed := make([]int, n)
	for i := range identity {
		reversed[i] = n - 1 - i
	}

	refMux, panicVal := buildOrderIndepMux(identity)
	if panicVal != nil {
		t.Fatalf("reference (identity order) registration panicked: %v", panicVal)
	}
	want := snapshotRouting(refMux, orderIndepLookups)

	perms := map[string][]int{
		"reversed":      reversed,
		"param-first":   {1, 0, 3, 2, 5, 6, 4, 7}, // wildchild before its static sibling in both groups
		"static-first":  {0, 1, 2, 3, 4, 5, 6, 7}, // == identity, sanity check
		"interleaved":   {0, 4, 1, 5, 2, 6, 3, 7},
		"deepest-first": {6, 5, 4, 3, 2, 1, 0, 7},
	}

	for name, perm := range perms {
		t.Run(name, func(t *testing.T) {
			mux, panicVal := buildOrderIndepMux(perm)
			if panicVal != nil {
				t.Fatalf("permutation %v panicked: %v", perm, panicVal)
			}
			got := snapshotRouting(mux, orderIndepLookups)
			for _, p := range orderIndepLookups {
				if got[p] != want[p] {
					t.Errorf("permutation %v: GET %q = %q, want %q (identity order)", perm, p, got[p], want[p])
				}
			}
		})
	}
}

// FuzzRouteRegistrationOrderIndependence explores the permutation space of
// orderIndepPatterns using the fuzz input as a Fisher-Yates shuffle seed,
// and asserts every permutation (a) registers without panicking and (b)
// produces routing results identical to the canonical (identity-order)
// registration, for every path in orderIndepLookups.
//
// A failure here means registration order changes observable routing
// behaviour for a pattern set with no genuine conflict — a route-shadowing
// bug in addRouteInternal/insertChild (tree.go), most likely in the #256
// splice-before-wildchild logic or the #253 path-copying/copyNode wrapping
// around it.
func FuzzRouteRegistrationOrderIndependence(f *testing.F) {
	for _, seed := range []uint64{0, 1, 2, 3, 42, 12345, 0xdeadbeef} {
		f.Add(seed)
	}
	n := len(orderIndepPatterns)
	identity := make([]int, n)
	for i := range identity {
		identity[i] = i
	}
	refMux, panicVal := buildOrderIndepMux(identity)
	if panicVal != nil {
		f.Fatalf("reference (identity order) registration panicked: %v", panicVal)
	}
	want := snapshotRouting(refMux, orderIndepLookups)

	f.Fuzz(func(t *testing.T, seed uint64) {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		perm := rng.Perm(n)

		mux, panicVal := buildOrderIndepMux(perm)
		if panicVal != nil {
			t.Fatalf("permutation %v (seed %d) panicked on a non-conflicting pattern set: %v", perm, seed, panicVal)
		}
		got := snapshotRouting(mux, orderIndepLookups)
		for _, p := range orderIndepLookups {
			if got[p] != want[p] {
				t.Fatalf("order-dependent routing bypass: permutation %v (seed %d): GET %q = %q, want %q (identity order)",
					perm, seed, p, got[p], want[p])
			}
		}
	})
}

// conflictPair is a pair of patterns that MUST conflict at registration time
// (rule 68 or rule 71) regardless of which is registered first.
type conflictPair struct {
	name string
	a, b string
}

var wildcardConflictPairs = []conflictPair{
	{"catch-all vs static sibling (rule 68)", "/static/*filepath", "/static/list"},
	{"two different param names at same position (rule 71)", "/x/:a", "/x/:b"},
	{"two different regex params at same position (rule 71)", "/y/{id:[0-9]+}", "/y/{name:[a-z]+}"},
	{"param vs regex-param at same position", "/z/:a", "/z/{b:[0-9]+}"},
}

// TestWildcardConflictPairs_PanicInBothOrders is the deterministic
// counterpart to FuzzWildcardConflictOrderIndependence: every conflict pair
// must panic whether a or b is registered first, so a route can never be
// silently shadowed in one order while being (correctly) rejected in the
// other.
func TestWildcardConflictPairs_PanicInBothOrders(t *testing.T) {
	for _, cp := range wildcardConflictPairs {
		t.Run(cp.name, func(t *testing.T) {
			for _, order := range []struct {
				name   string
				first  string
				second string
			}{
				{"a-then-b", cp.a, cp.b},
				{"b-then-a", cp.b, cp.a},
			} {
				t.Run(order.name, func(t *testing.T) {
					mux := muxmaster.New()
					func() {
						defer func() { recover() }() //nolint:errcheck // first registration must succeed
						mux.GET(order.first, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
					}()
					var panicked bool
					func() {
						defer func() {
							if recover() != nil {
								panicked = true
							}
						}()
						mux.GET(order.second, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
					}()
					if !panicked {
						t.Errorf("%s: registering %q after %q did not panic — potential shadowed/ambiguous route",
							order.name, order.second, order.first)
					}
				})
			}
		})
	}
}

// FuzzWildcardConflictOrderIndependence is the fuzz counterpart: the fuzz
// input selects one of the known conflict pairs and a coin flip for order,
// asserting the panic always fires. This is intentionally a small,
// deterministic search space dressed as a fuzz target so it participates in
// coverage-guided corpus minimisation and long fuzz runs alongside the
// other targets in this file, in case a future edit to tree.go introduces a
// data-dependent path that only manifests for specific byte patterns in the
// pattern strings themselves.
func FuzzWildcardConflictOrderIndependence(f *testing.F) {
	for i := range wildcardConflictPairs {
		f.Add(i, false)
		f.Add(i, true)
	}
	f.Fuzz(func(t *testing.T, idx int, swap bool) {
		if idx < 0 {
			idx = -idx
		}
		if len(wildcardConflictPairs) == 0 {
			t.Skip()
		}
		cp := wildcardConflictPairs[idx%len(wildcardConflictPairs)]
		first, second := cp.a, cp.b
		if swap {
			first, second = second, first
		}
		mux := muxmaster.New()
		func() {
			defer func() { recover() }() //nolint:errcheck
			mux.GET(first, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		}()
		var panicked bool
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			mux.GET(second, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		}()
		if !panicked {
			t.Fatalf("%s: registering %q after %q did not panic (idx=%d swap=%v)", cp.name, second, first, idx, swap)
		}
	})
}
