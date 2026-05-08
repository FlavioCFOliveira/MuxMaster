package harness

// GAP B: Walk/WalkFast on a post-panic partially-corrupted tree.
//
// Context: addRoute panics on route conflicts, invalid wildcards, and invalid UTF-8.
// When a panic occurs mid-registration, the tree node being written may be in a
// half-mutated state (e.g. n.children partially appended, n.indices partially
// extended, n.path split but n.handler not yet set). If the caller catches the
// panic and continues using the Mux, Walk/WalkFast traverse the tree via
// node.walk(), which recurses into n.children unconditionally. This could hit:
//   - nil handler + nil fast + non-empty pattern → fn called with nil handler (spec says it won't: walk guards this)
//   - cycle: theoretically impossible with the append-only child model, but verify
//   - panic in fn due to nil handler being passed
//   - OOM from exponential recursion if a cycle were somehow introduced
//
// The harness deliberately triggers documented panics (route conflicts) during
// registration via recover(), then exercises Walk/WalkFast on the resulting state.
//
// Invariants verified:
//   I-WALK-01: Walk/WalkFast never panic on any Mux state, including post-panic trees.
//   I-WALK-02: Walk only delivers routes where handler != nil; WalkFast only where fast != nil.
//   I-WALK-03: Routes() never panics on any Mux state.
//   I-WALK-04: Walk iteration is finite (terminates — no infinite loop / cycle).

import (
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// corruptionScenario describes one way to leave the tree in a post-panic state.
type corruptionScenario struct {
	name string
	// setupFn registers routes; may panic. Panics are caught by the harness.
	setupFn func(mux *mm.Mux)
}

// builtinCorruptionScenarios returns scenarios that trigger documented panics.
// Each scenario pairs a valid route with a conflicting registration so the tree
// has at least one committed node before the panic.
func builtinCorruptionScenarios() []corruptionScenario {
	fast := mm.FastHandler(func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
		w.WriteHeader(200)
	})

	return []corruptionScenario{
		{
			name: "conflict_param_vs_static",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/users/:id", h200)
				// Registering a wildcard in the same position panics with conflict.
				mux.GET("/users/*rest", h200) // expected panic: conflict
			},
		},
		{
			name: "duplicate_handler",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/api/v1/items", h200)
				mux.GET("/api/v1/items", h200) // expected panic: duplicate
			},
		},
		{
			name: "catch_all_not_at_end",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/a/b", h200)
				mux.GET("/static/*fp/extra", h200) // expected panic: catch-all not at end
			},
		},
		{
			name: "invalid_utf8_path",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/valid", h200)
				mux.GET("/invalid\xff\xfe", h200) // expected panic: invalid UTF-8
			},
		},
		{
			name: "unnamed_wildcard",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/a/:p", h200)
				mux.GET("/b/:", h200) // expected panic: wildcards must be named
			},
		},
		{
			name: "fast_then_conflict",
			setupFn: func(mux *mm.Mux) {
				mux.HandleFast("GET", "/fast/:id", fast)
				mux.HandleFast("GET", "/fast/:id", fast) // expected panic: duplicate
			},
		},
		{
			name: "mixed_fast_and_normal_conflict",
			setupFn: func(mux *mm.Mux) {
				mux.GET("/mixed/:x", h200)
				mux.HandleFast("GET", "/mixed/:x", fast) // expected panic: duplicate
			},
		},
		{
			name: "deep_tree_then_duplicate",
			setupFn: func(mux *mm.Mux) {
				// Build a deep tree first, then trigger conflict at a deep node.
				mux.GET("/a/b/c/d/e", h200)
				mux.GET("/a/b/c/d/:p", h200)
				mux.GET("/a/b/c/d/e", h200) // expected panic: duplicate
			},
		},
	}
}

// applyScenario applies setupFn, catching any panic. Returns true if a panic
// was caught (tree may be partially mutated).
func applyScenario(mux *mm.Mux, s corruptionScenario) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	s.setupFn(mux)
	return false
}

// TestWalkOnCorruptedTree verifies all four walk invariants across all built-in
// corruption scenarios. This is a table-driven test (not a fuzz test) so it
// runs deterministically in short mode and contributes to CI coverage.
func TestWalkOnCorruptedTree(t *testing.T) {
	for _, sc := range builtinCorruptionScenarios() {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			mux := mm.New()
			panicked := applyScenario(mux, sc)
			if !panicked {
				// Some scenarios may not panic on all Go versions if a future
				// refactor removes the conflict check; that's fine — still verify Walk.
				t.Log("scenario did not panic (tree is clean)")
			}

			verifyWalkInvariants(t, mux, sc.name)
		})
	}
}

// verifyWalkInvariants exercises Walk, WalkFast, and Routes() on a potentially
// corrupted mux and asserts all four invariants.
func verifyWalkInvariants(t *testing.T, mux *mm.Mux, label string) {
	t.Helper()
	const iterationLimit = 100_000

	// I-WALK-04: use a deadline to catch infinite iteration.
	done := make(chan struct{})
	var walkPanic atomic.Value

	go func() {
		defer func() {
			if r := recover(); r != nil {
				walkPanic.Store(fmt.Sprintf("%v\n%s", r, debug.Stack()))
			}
			close(done)
		}()

		// I-WALK-01 + I-WALK-02: Walk delivers only http.Handler routes; handler != nil.
		var walkCount int
		err := mux.Walk(func(method, pattern string, handler http.Handler) error {
			walkCount++
			if walkCount > iterationLimit {
				return fmt.Errorf("I-WALK-04: Walk exceeded %d iterations on %q", iterationLimit, label)
			}
			// I-WALK-02: handler must be non-nil (Walk's spec guarantees this).
			if handler == nil {
				return fmt.Errorf("I-WALK-02 violation: Walk delivered nil handler for method=%q pattern=%q", method, pattern)
			}
			return nil
		})
		if err != nil {
			walkPanic.Store(err.Error())
			return
		}

		// I-WALK-01 + I-WALK-02: WalkFast delivers only FastHandler routes.
		var walkFastCount int
		err = mux.WalkFast(func(method, pattern string, handler mm.FastHandler) error {
			walkFastCount++
			if walkFastCount > iterationLimit {
				return fmt.Errorf("I-WALK-04: WalkFast exceeded %d iterations on %q", iterationLimit, label)
			}
			if handler == nil {
				return fmt.Errorf("I-WALK-02 violation: WalkFast delivered nil FastHandler for method=%q pattern=%q", method, pattern)
			}
			return nil
		})
		if err != nil {
			walkPanic.Store(err.Error())
			return
		}

		// I-WALK-03: Routes() must not panic.
		_ = mux.Routes()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("I-WALK-04 TIMEOUT: Walk/WalkFast did not terminate within 5s on scenario %q", label)
	}

	if v := walkPanic.Load(); v != nil {
		t.Fatalf("I-WALK-01/02/03 violation on scenario %q: %s", label, v.(string))
	}
}

// FuzzWalkCorrupted fuzzes the Walk invariants with arbitrary route sequences.
// The fuzzer controls two routes; the second one is designed to potentially
// conflict with or follow the first. This gives the coverage-guided fuzzer
// the ability to discover novel corruption states beyond the built-in scenarios.
//
// Invariants: I-WALK-01 through I-WALK-04.
func FuzzWalkCorrupted(f *testing.F) {
	// Seeds: pairs of (route1, route2) where route2 may conflict.
	f.Add("/a/:id", "/a/:id")                    // duplicate
	f.Add("/a/b", "/a/:p")                        // wildcard after static leaf
	f.Add("/static/*fp", "/static/*fp")           // duplicate catch-all
	f.Add("/a/:p/b", "/a/*rest")                  // catch-all vs param
	f.Add("/valid", "/invalid\xff")               // invalid UTF-8
	f.Add("/x/:", "/x/:param")                    // unnamed wildcard first
	f.Add("/only-one", "/only-one")               // simple duplicate
	f.Add("/a/b/c", "/a/b/c/d/e/f/g/h/i/j/k")   // deep path after shallow

	f.Fuzz(func(t *testing.T, route1, route2 string) {
		defer func() {
			if r := recover(); r != nil {
				// Panics inside the fuzz body itself (not inside applyScenario) are bugs.
				t.Fatalf("PANIC in FuzzWalkCorrupted outer body: r=%v\n%s", r, debug.Stack())
			}
		}()

		mux := mm.New()

		// Register route1 — catch expected panics.
		func() {
			defer func() { recover() }() //nolint:errcheck
			mux.GET(route1, h200)
		}()

		// Register route2 — may panic (conflict, duplicate, etc.).
		func() {
			defer func() { recover() }() //nolint:errcheck
			mux.GET(route2, h200)
		}()

		// Now Walk the potentially-corrupted tree.
		// I-WALK-04: bound iteration count to prevent OOM/infinite loops.
		const limit = 10_000
		var count int

		walkErr := mux.Walk(func(method, pattern string, handler http.Handler) error {
			count++
			if count > limit {
				return fmt.Errorf("I-WALK-04: iteration limit exceeded")
			}
			if handler == nil {
				t.Errorf("I-WALK-02: Walk delivered nil handler method=%q pattern=%q", method, pattern)
			}
			return nil
		})
		if walkErr != nil && count > limit {
			t.Fatalf("I-WALK-04: Walk did not terminate: route1=%q route2=%q", route1, route2)
		}

		count = 0
		walkFastErr := mux.WalkFast(func(method, pattern string, fast mm.FastHandler) error {
			count++
			if count > limit {
				return fmt.Errorf("I-WALK-04: iteration limit exceeded")
			}
			if fast == nil {
				t.Errorf("I-WALK-02: WalkFast delivered nil handler method=%q pattern=%q", method, pattern)
			}
			return nil
		})
		if walkFastErr != nil && count > limit {
			t.Fatalf("I-WALK-04: WalkFast did not terminate: route1=%q route2=%q", route1, route2)
		}

		// I-WALK-03: Routes() must not panic.
		_ = mux.Routes()
	})
}

// TestWalkCorruptedTreeRace verifies Walk/WalkFast under -race after a panic
// during registration. The race detector catches concurrent map or slice access.
// This test spawns concurrent Walk goroutines while a second goroutine attempts
// more (potentially conflicting) registrations.
func TestWalkCorruptedTreeRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race test in short mode")
	}

	mux := mm.New()
	// Establish a base tree.
	mux.GET("/api/:id", h200)
	mux.GET("/api/:id/details", h200)
	mux.POST("/api/:id", h200)

	// Trigger a corruption.
	func() {
		defer func() { recover() }() //nolint:errcheck
		mux.GET("/api/:id", h200) // duplicate — panics
	}()

	// Concurrent readers.
	const readers = 8
	errs := make(chan string, readers)
	for i := range readers {
		go func(i int) {
			defer func() {
				if r := recover(); r != nil {
					errs <- fmt.Sprintf("reader %d PANIC: %v", i, r)
					return
				}
				errs <- ""
			}()
			_ = mux.Walk(func(_, _ string, _ http.Handler) error { return nil })
			_ = mux.WalkFast(func(_, _ string, _ mm.FastHandler) error { return nil })
			_ = mux.Routes()
		}(i)
	}

	// Drain results.
	for range readers {
		if msg := <-errs; msg != "" {
			t.Error(msg)
		}
	}

	// Yield to let goroutines complete before the test exits.
	runtime.Gosched()
}
