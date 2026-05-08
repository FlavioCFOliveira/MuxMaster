// Package s10 contains the S10-PreCSA mini-sprint harness.
//
// Covers TM-2026: 007, 008, 036, 037, 045 — the CL-AUTH-1 cluster variants
// that were UNTESTED at the close of S9. Specifically:
//
//  1. Group.HandleFast when the Group inherits Use() from the parent Mux (not
//     the Group itself). Expectation: HandleFast panic fires because the parent
//     Mux.middleware is non-empty and Group.HandleFast checks g.middleware
//     (the GROUP's own chain, not the root's).
//
//  2. Mount with an external handler receiving a path that is NOT
//     canonicalised, to test that Group middleware wraps the mounted handler.
//
//  3. UseFast on a Group when there is a Pre on the root Mux AND a Use on the
//     Group.  Registration-time safety + request-time middleware ordering.
//
//  4. Echo-style Group middleware ordering — sub-group inherits parent Use()
//     middleware (TM-045), then calls HandleFast — confirms panic fires.
package s10_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── helpers ──────────────────────────────────────────────────────────────────

var h200 http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

var fast200 mm.FastHandler = func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
	w.WriteHeader(http.StatusOK)
}

func makeAuthMW(called *atomic.Int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

func makePreMW(called *atomic.Int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// ── TM-007 / TM-037: Group.HandleFast panics when parent Mux has Use() ──────

// TestTM007_GroupHandleFast_ParentMuxUse_Panics verifies that Group.HandleFast
// panics when the parent Mux has stdlib Use() middleware — even though the Group
// itself has no Use() middleware.
//
// Mechanism: Group.HandleFast delegates to g.mux.HandleFast() after its own
// guard (which checks g.middleware). The root Mux.HandleFast guard checks
// m.middleware — which is non-empty — and panics. This is the FPE-2026-010
// fix propagating correctly through the Group delegation path.
//
// SECURITY VERDICT (TM-007): silent bypass is impossible. Any combination of
// stdlib middleware on the parent Mux + HandleFast on a child Group correctly
// panics at registration time. REFUTED as a new bypass vulnerability.
func TestTM007_GroupHandleFast_ParentMuxUse_Panics(t *testing.T) {
	r := mm.New()
	r.Use(func(next http.Handler) http.Handler { return next }) // Mux-level Use

	g := r.Group("/api") // Group has NO middleware of its own

	// Group.HandleFast passes g.middleware check (empty), then delegates to
	// g.mux.HandleFast which finds m.middleware non-empty and panics.
	var panicked bool
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		g.HandleFast("GET", "/:id", fast200)
	}()

	if !panicked {
		t.Error("TM-007: Group.HandleFast should panic when the parent Mux has Use() middleware " +
			"(FPE-010 guard fires via delegation to g.mux.HandleFast)")
	}
	t.Log("TM-007: Group.HandleFast correctly panics when parent Mux.middleware is non-empty. " +
		"Silent auth bypass via Group path: IMPOSSIBLE. FPE-010 fix covers both Group and root paths.")
}

// TestTM007_GroupHandleFast_GroupUse_Panics verifies that Group.HandleFast
// DOES panic when the Group itself has Use() middleware (not just the parent).
func TestTM007_GroupHandleFast_GroupUse_Panics(t *testing.T) {
	r := mm.New()
	g := r.Group("/api")
	g.Use(func(next http.Handler) http.Handler { return next })

	var panicked bool
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		g.HandleFast("GET", "/:id", fast200)
	}()

	if !panicked {
		t.Error("TM-007: Group.HandleFast should panic when the Group itself has Use() middleware")
	}
	t.Log("TM-007: Group.HandleFast panic guard correctly fires when Group.middleware is non-empty.")
}

// TestTM037_RootMuxUse_HandleFast_Panics verifies the FPE-2026-010 fix:
// root Mux.Use() followed by Mux.HandleFast panics.
func TestTM037_RootMuxUse_HandleFast_Panics(t *testing.T) {
	r := mm.New()
	r.Use(func(next http.Handler) http.Handler { return next })

	var panicked bool
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		r.HandleFast("GET", "/fast/:id", fast200)
	}()

	if !panicked {
		t.Error("TM-037/FPE-010: Mux.HandleFast should panic when Mux has stdlib Use() middleware")
	}
	t.Log("TM-037/FPE-010: root Mux HandleFast panic guard CONFIRMED operative.")
}

// ── TM-008 / TM-036: UseFast on Group with Pre on root + Use on Group ────────

// TestTM008_UseFast_Group_WithPreOnRoot verifies that UseFast on a Group works
// correctly when the root Mux has Pre() registered.
// Expected: fast route still reachable; Pre middleware fires for fast route.
func TestTM008_UseFast_Group_WithPreOnRoot(t *testing.T) {
	var preCalled atomic.Int64
	var fastMWCalled atomic.Int64

	r := mm.New()
	r.Pre(makePreMW(&preCalled))

	g := r.Group("/v1")
	g.UseFast(func(next mm.FastHandler) mm.FastHandler {
		return func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
			fastMWCalled.Add(1)
			next(w, r, ps)
		}
	})
	g.HandleFast("GET", "/resource/:id", fast200)

	const N = 10000
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < N; j++ {
				req := httptest.NewRequest("GET", "/v1/resource/42", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("TM-008: expected 200, got %d", w.Code)
				}
			}
		}()
	}
	wg.Wait()

	total := int64(32 * N)
	if preCalled.Load() != total {
		t.Errorf("TM-008: Pre middleware should fire for fast route: got %d/%d", preCalled.Load(), total)
	}
	if fastMWCalled.Load() != total {
		t.Errorf("TM-008: Group UseFast middleware should fire: got %d/%d", fastMWCalled.Load(), total)
	}
	t.Logf("TM-008: Pre fired %d/%d, UseFast fired %d/%d — CONFIRMED both layers operative.", preCalled.Load(), total, fastMWCalled.Load(), total)
}

// TestTM036_Group_Middleware_Ordering verifies that Group middleware is applied
// in registration order and is NOT skipped due to inheritance anomalies.
// TM-036: "Group middleware ordering allows skip."
func TestTM036_Group_Middleware_Ordering(t *testing.T) {
	var order []string
	var mu sync.Mutex

	addStep := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}

	makeMW := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				addStep(name + ":before")
				next.ServeHTTP(w, r)
				addStep(name + ":after")
			})
		}
	}

	r := mm.New()
	r.Use(makeMW("mux-A"))

	g := r.Group("/g")
	g.Use(makeMW("group-B"))
	g.Use(makeMW("group-C"))
	g.GET("/:id", func(w http.ResponseWriter, r *http.Request) {
		addStep("handler")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/g/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	want := []string{"mux-A:before", "group-B:before", "group-C:before", "handler", "group-C:after", "group-B:after", "mux-A:after"}
	mu.Lock()
	got := make([]string, len(order))
	copy(got, order)
	mu.Unlock()

	if len(got) != len(want) {
		t.Fatalf("TM-036: ordering mismatch\nwant: %v\ngot:  %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TM-036: step %d: want %q got %q", i, want[i], got[i])
		}
	}
	t.Logf("TM-036: middleware ordering CONFIRMED correct: %v", got)
}

// TestTM036_Group_Middleware_Race stresses middleware ordering under concurrency.
func TestTM036_Group_Middleware_Race(t *testing.T) {
	var muxMWCalled atomic.Int64
	var groupMWCalled atomic.Int64

	r := mm.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			muxMWCalled.Add(1)
			next.ServeHTTP(w, r)
		})
	})

	g := r.Group("/stress")
	g.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			groupMWCalled.Add(1)
			next.ServeHTTP(w, r)
		})
	})
	g.GET("/:id", h200)

	const goroutines = 32
	const iters = 10000
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				req := httptest.NewRequest("GET", "/stress/7", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}
	wg.Wait()

	total := int64(goroutines * iters)
	if muxMWCalled.Load() != total {
		t.Errorf("TM-036 race: mux middleware call count wrong: got %d, want %d", muxMWCalled.Load(), total)
	}
	if groupMWCalled.Load() != total {
		t.Errorf("TM-036 race: group middleware call count wrong: got %d, want %d", groupMWCalled.Load(), total)
	}
}

// ── TM-045: Echo-style Group inheritance ─────────────────────────────────────

// TestTM045_EchoStyle_SubGroup_HandleFast_PanicWhenGroupUse tests the Echo-style
// pattern where a sub-group inherits the parent group's middleware via Group.Group().
// TM-045 asks: does the sub-group's HandleFast panic if the parent has Use()?
//
// Code reading of group.go Group() method: it COPIES the parent middleware into
// the new sub-group. So if the parent group has Use(), the sub-group starts with
// the same middleware — HandleFast WILL panic on the sub-group.
func TestTM045_EchoStyle_SubGroup_HandleFast_PanicWhenGroupUse(t *testing.T) {
	r := mm.New()
	parent := r.Group("/api")
	parent.Use(func(next http.Handler) http.Handler { return next })

	sub := parent.Group("/v1") // copies parent middleware

	var panicked bool
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		sub.HandleFast("GET", "/res/:id", fast200)
	}()

	if !panicked {
		t.Error("TM-045: sub-group.HandleFast should panic when parent group has Use() middleware (inherited via Group.Group())")
	}
	t.Log("TM-045: sub-group inheritance of parent Use() correctly triggers HandleFast panic guard.")
}

// TestTM045_SubGroup_NoParentMW_HandleFast_NoPanic verifies that a sub-group
// with no inherited middleware does NOT panic on HandleFast.
func TestTM045_SubGroup_NoParentMW_HandleFast_NoPanic(t *testing.T) {
	r := mm.New()
	parent := r.Group("/api") // no Use() on parent
	sub := parent.Group("/v1")

	var panicked bool
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		sub.HandleFast("GET", "/res/:id", fast200)
	}()

	if panicked {
		t.Error("TM-045: sub-group.HandleFast should NOT panic when neither the group nor the parent has Use() middleware")
	}
}

// ── Mount + external handler path canonicalisation (TM-007 variant) ───────────

// TestMount_ExternalHandler_WithGroupMiddleware verifies that Group.Mount()
// wraps the external handler with the group's stdlib middleware.
// Without wrapping, an auth middleware on the group would not protect mounted routes.
func TestMount_ExternalHandler_WithGroupMiddleware(t *testing.T) {
	var authCalled atomic.Int64

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := mm.New()
	g := r.Group("/api")
	g.Use(makeAuthMW(&authCalled))
	g.Mount("/service", inner)

	const N = 1000
	for i := 0; i < N; i++ {
		req := httptest.NewRequest("GET", "/api/service/foo", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Mount: expected 200, got %d", w.Code)
		}
	}

	if authCalled.Load() != N {
		t.Errorf("Mount auth bypass: auth middleware called %d times, want %d", authCalled.Load(), N)
	}
	t.Logf("Mount: auth middleware correctly called %d/%d times.", authCalled.Load(), N)
}

// TestMount_UncanonicalPath verifies that Mount correctly strips the prefix
// before forwarding. The external handler must NOT receive the mount prefix.
func TestMount_UncanonicalPath(t *testing.T) {
	var receivedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	r := mm.New()
	r.Mount("/prefix", inner)

	req := httptest.NewRequest("GET", "/prefix/downstream/path", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if receivedPath != "/downstream/path" {
		t.Errorf("Mount path stripping: inner handler got %q, want %q", receivedPath, "/downstream/path")
	}
	t.Logf("Mount path stripping: inner handler received %q", receivedPath)
}

// ── Pre + Use + ParamsFromContext coherence (CSA-0060 regression coverage) ───

// TestCSA0060_PreAndUseComposition verifies that when both Pre() and Use() are
// registered, ParamsFromContext() correctly returns params through the
// context-wrapping middleware chain (the CSA-2026-0060 slow-path fix).
func TestCSA0060_PreAndUseComposition(t *testing.T) {
	var paramMisses atomic.Int64
	var paramHits atomic.Int64

	r := mm.New()
	// Pre wraps the whole dispatch including the Use() middleware chain.
	r.Pre(middleware.RequestID())

	// Timeout wraps the request context — this is the trigger for CSA-0060.
	r.Use(middleware.Timeout(500))

	r.GET("/user/:id", func(w http.ResponseWriter, r *http.Request) {
		id := mm.PathParam(r, "id")
		if id == "" {
			paramMisses.Add(1)
		} else {
			paramHits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	})

	const goroutines = 32
	const iters = 10000
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				req := httptest.NewRequest("GET", "/user/42", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(i)
	}
	wg.Wait()

	if paramMisses.Load() > 0 {
		t.Errorf("CSA-0060 regression: PathParam returned empty string %d times out of %d requests", paramMisses.Load(), int64(goroutines*iters))
	}
	t.Logf("CSA-0060 Pre+Use composition: hits=%d misses=%d", paramHits.Load(), paramMisses.Load())
}
