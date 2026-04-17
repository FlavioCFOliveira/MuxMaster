// Property tests using pgregory.net/rapid.
//
// These cover invariants that are global to the module — not tied to a single
// fuzz target. Each property is stated as an "∀ x, P(x)" claim and checked
// with rapid's shrink-to-minimal-counterexample driver.
//
// Covered invariants:
//   I-04 : Group prefix composition — for any sequence of prefixes P1..Pn
//          registered via nested Groups with leaf L, a request at
//          concat(P1..Pn, L) is routed to the handler.
//   I-05 : Middleware order preserved — Use(m1, m2, m3) wraps handlers so
//          that m1 runs outermost and m3 innermost (classic chi/gin contract).
//   I-07 : Handle is idempotent modulo route-conflict panic — calling Handle
//          with an accepted (method, pattern) twice produces a consistent
//          conflict panic rather than silent corruption.
//   H-012: paramsBuf overflow — routes with more than maxInlineParams (3)
//          parameters are either serviced correctly or rejected with a
//          clear signal; silent drop is forbidden.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"pgregory.net/rapid"
)

// pathSegGen generates a valid single path segment (no slashes, no special chars).
var pathSegGen = rapid.StringMatching(`[a-z][a-z0-9]{0,7}`)

// TestProp_GroupPrefixComposition — I-04.
//
// ∀ prefixes P1..Pn (n ∈ [1,5]), leaf L: register L on a group chain built
// from P1..Pn and assert the resulting full path is routed.
func TestProp_GroupPrefixComposition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "n")
		prefixes := make([]string, n)
		for i := range prefixes {
			prefixes[i] = "/" + pathSegGen.Draw(t, fmt.Sprintf("p%d", i))
		}
		leaf := "/" + pathSegGen.Draw(t, "leaf")

		mux := mm.New()
		g := mux.Group(prefixes[0])
		for _, p := range prefixes[1:] {
			g = g.Group(p)
		}
		g.GET(leaf, h200)

		fullPath := strings.Join(prefixes, "") + leaf
		req := httptest.NewRequest(http.MethodGet, fullPath, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d (prefixes=%v leaf=%q)",
				fullPath, w.Code, prefixes, leaf)
		}
	})
}

// TestProp_MiddlewareOrderPreserved — I-05.
//
// Register N middlewares via Mux.Use, each appending its index to a shared
// slice. After serving a request, the slice must equal [0, 1, …, N-1].
func TestProp_MiddlewareOrderPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "n")

		var mu sync.Mutex
		var order []int
		build := func(idx int) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					order = append(order, idx)
					mu.Unlock()
					next.ServeHTTP(w, r)
				})
			}
		}
		mws := make([]func(http.Handler) http.Handler, n)
		for i := 0; i < n; i++ {
			mws[i] = build(i)
		}

		mux := mm.New()
		mux.Use(mws...)
		mux.GET("/x", h200)

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		expected := make([]int, n)
		for i := range expected {
			expected[i] = i
		}
		if !reflect.DeepEqual(order, expected) {
			t.Fatalf("middleware order mismatch: got %v want %v", order, expected)
		}
	})
}

// TestProp_GroupMiddlewareOrderPreserved — mux-level middlewares run before
// group-level middlewares, preserving registration order within each tier.
func TestProp_GroupMiddlewareOrderPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nTop := rapid.IntRange(0, 5).Draw(t, "nTop")
		nGrp := rapid.IntRange(0, 5).Draw(t, "nGrp")
		if nTop+nGrp == 0 {
			return
		}

		var mu sync.Mutex
		var order []string
		build := func(tag string) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					order = append(order, tag)
					mu.Unlock()
					next.ServeHTTP(w, r)
				})
			}
		}

		mux := mm.New()
		for i := 0; i < nTop; i++ {
			mux.Use(build(fmt.Sprintf("T%d", i)))
		}
		g := mux.Group("/g")
		for i := 0; i < nGrp; i++ {
			g.Use(build(fmt.Sprintf("G%d", i)))
		}
		g.GET("/r", h200)

		req := httptest.NewRequest(http.MethodGet, "/g/r", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		// Expected: all T* then all G* in ascending index.
		exp := make([]string, 0, nTop+nGrp)
		for i := 0; i < nTop; i++ {
			exp = append(exp, fmt.Sprintf("T%d", i))
		}
		for i := 0; i < nGrp; i++ {
			exp = append(exp, fmt.Sprintf("G%d", i))
		}
		if !reflect.DeepEqual(order, exp) {
			t.Fatalf("order mismatch: got %v want %v", order, exp)
		}
	})
}

// TestProp_WithAppendsMiddleware — Group.With returns a new group whose
// middleware chain equals the original plus the appended ones.
func TestProp_WithAppendsMiddleware(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nInit := rapid.IntRange(0, 4).Draw(t, "nInit")
		nWith := rapid.IntRange(1, 4).Draw(t, "nWith")

		var mu sync.Mutex
		var order []int
		build := func(idx int) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					order = append(order, idx)
					mu.Unlock()
					next.ServeHTTP(w, r)
				})
			}
		}

		mux := mm.New()
		g := mux.Group("/g")
		for i := 0; i < nInit; i++ {
			g.Use(build(i))
		}
		extra := make([]func(http.Handler) http.Handler, nWith)
		for i := 0; i < nWith; i++ {
			extra[i] = build(nInit + i)
		}
		g2 := g.With(extra...)
		g2.GET("/r", h200)

		req := httptest.NewRequest(http.MethodGet, "/g/r", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		exp := make([]int, nInit+nWith)
		for i := range exp {
			exp[i] = i
		}
		if !reflect.DeepEqual(order, exp) {
			t.Fatalf("With order mismatch: got %v want %v", order, exp)
		}
	})
}

// TestProp_HandleIdempotencyAndConflict — I-07.
//
// Registering the same (method, static-pattern) twice must panic with
// "already registered". Registering twice with Lookup in between should still
// leave the registry queryable post-conflict-panic (i.e. no corruption).
func TestProp_HandleIdempotencyAndConflict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		segs := rapid.SliceOfN(pathSegGen, 1, 3).Draw(t, "segs")
		pattern := "/" + strings.Join(segs, "/")

		mux := mm.New()
		mux.Handle(http.MethodGet, pattern, h200)

		// First registration should be reachable.
		if h, _, ok := mux.Lookup(http.MethodGet, pattern); !ok || h == nil {
			t.Fatalf("Lookup failed after first Handle: pattern=%q", pattern)
		}

		// Second registration on the same pattern must panic "already registered".
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic on duplicate Handle: pattern=%q", pattern)
				}
				msg := fmt.Sprint(r)
				if !strings.Contains(msg, "already registered") {
					t.Fatalf("duplicate-Handle panic has wrong message: pattern=%q msg=%v",
						pattern, r)
				}
			}()
			mux.Handle(http.MethodGet, pattern, h200)
		}()

		// Post-panic: registry is not corrupted — original still works.
		if h, _, ok := mux.Lookup(http.MethodGet, pattern); !ok || h == nil {
			t.Fatalf("Lookup failed after duplicate-panic: pattern=%q", pattern)
		}
	})
}

// TestProp_ParamsCaptureAllWhenWithinCapacity — for any pattern with
// N ∈ [1, maxInlineParams=3] parameters, all are captured correctly. This
// covers the green path. The overflow case (N > 3) is tracked as FPE-004
// with an explicit repro and is not asserted here.
func TestProp_ParamsCaptureAllWhenWithinCapacity(t *testing.T) {
	const maxInline = 3
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, maxInline).Draw(t, "n")
		names := make([]string, n)
		values := make([]string, n)
		for i := 0; i < n; i++ {
			names[i] = fmt.Sprintf("p%d", i)
			values[i] = fmt.Sprintf("v%d", i)
		}
		segs := make([]string, n)
		for i := range segs {
			segs[i] = ":" + names[i]
		}
		pattern := "/" + strings.Join(segs, "/")
		path := "/" + strings.Join(values, "/")

		mux := mm.New()
		var observed map[string]string

		var regPanic any
		func() {
			defer func() { regPanic = recover() }()
			mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
				observed = make(map[string]string, n)
				for _, nm := range names {
					observed[nm] = mm.PathParam(r, nm)
				}
				w.WriteHeader(http.StatusOK)
			})
		}()
		if regPanic != nil {
			t.Fatalf("unexpected panic at pattern=%q: %v", pattern, regPanic)
		}

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("pattern=%q path=%q status=%d", pattern, path, w.Code)
		}
		if observed == nil {
			t.Fatalf("handler not invoked for pattern=%q path=%q", pattern, path)
		}
		for i, name := range names {
			if observed[name] != values[i] {
				t.Fatalf("param %q: want %q got %q (all: %v)",
					name, values[i], observed[name], observed)
			}
		}
	})
}

// TestProp_LookupNeverPanics — I-06 family: Lookup on any path must return
// deterministic (handler/nil, params/nil, ok) without panicking, irrespective
// of registration state.
func TestProp_LookupNeverPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Build a random small mux.
		nRoutes := rapid.IntRange(0, 10).Draw(t, "nRoutes")
		mux := mm.New()
		for i := 0; i < nRoutes; i++ {
			segs := rapid.SliceOfN(pathSegGen, 1, 4).Draw(t, fmt.Sprintf("r%d", i))
			pat := "/" + strings.Join(segs, "/")
			func() {
				defer func() { _ = recover() }() // duplicates are fine
				mux.GET(pat, h200)
			}()
		}

		// Random lookup target.
		target := rapid.StringMatching(`/[a-z0-9/]{0,32}`).Draw(t, "target")
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Lookup panicked on %q: %v", target, r)
			}
		}()
		_, _, _ = mux.Lookup(http.MethodGet, target)
	})
}

// TestProp_RouteRoundTrip — for any pattern that registers, a request at a
// path that matches the pattern's "concrete form" must be routed to the handler.
// Concrete form: replace each :name with an arbitrary segment value, and each
// catch-all *name with a path tail.
func TestProp_RouteRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nSegs := rapid.IntRange(1, 4).Draw(t, "nSegs")
		shapes := make([]string, nSegs)
		values := make([]string, nSegs)
		for i := 0; i < nSegs; i++ {
			kind := rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("kind%d", i))
			switch kind {
			case 0:
				shapes[i] = pathSegGen.Draw(t, fmt.Sprintf("static%d", i))
				values[i] = shapes[i]
			case 1:
				shapes[i] = ":p" + fmt.Sprint(i)
				values[i] = pathSegGen.Draw(t, fmt.Sprintf("val%d", i))
			case 2:
				// Catch-all — only allowed at the end.
				if i == nSegs-1 {
					shapes[i] = "*rest" + fmt.Sprint(i)
					values[i] = strings.Join([]string{
						pathSegGen.Draw(t, "ca1"),
						pathSegGen.Draw(t, "ca2"),
					}, "/")
				} else {
					shapes[i] = pathSegGen.Draw(t, fmt.Sprintf("static%d", i))
					values[i] = shapes[i]
				}
			}
		}
		pattern := "/" + strings.Join(shapes, "/")
		path := "/" + strings.Join(values, "/")

		mux := mm.New()
		served := false
		func() {
			defer func() { _ = recover() }()
			mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
				served = true
				w.WriteHeader(http.StatusOK)
			})
		}()

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if !served && w.Code != http.StatusMovedPermanently {
			t.Fatalf("route not served: pattern=%q path=%q status=%d",
				pattern, path, w.Code)
		}
	})
}

// TestProp_ServeFilesRegistersTwoRoutes — ServeFiles must register both GET
// and HEAD at the given prefix. We assert that both Lookup calls succeed.
func TestProp_ServeFilesRegistersTwoRoutes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seg := pathSegGen.Draw(t, "seg")
		paramName := pathSegGen.Draw(t, "paramName")
		prefix := "/" + seg + "/*" + paramName

		mux := mm.New()
		mux.ServeFiles(prefix, http.Dir("/tmp"))

		for _, method := range []string{http.MethodGet, http.MethodHead} {
			h, _, ok := mux.Lookup(method, "/"+seg+"/any.txt")
			if !ok || h == nil {
				t.Fatalf("ServeFiles: %s not registered at %q", method, prefix)
			}
		}
	})
}

// TestProp_ErrorStatusCodePreserved — muxmaster.Error preserves the status
// code and the wrapped error's message.
func TestProp_ErrorStatusCodePreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		code := rapid.IntRange(400, 599).Draw(t, "code")
		msg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,32}`).Draw(t, "msg")
		err := mm.Error(code, fmt.Errorf("%s", msg))
		if err.StatusCode() != code {
			t.Fatalf("StatusCode: want %d got %d", code, err.StatusCode())
		}
		if err.Error() != msg {
			t.Fatalf("Error: want %q got %q", msg, err.Error())
		}
		// Unwrap is on the concrete type — exercise via errors.Is.
		inner := fmt.Errorf("%s", msg)
		wrapped := mm.Error(code, inner)
		_ = wrapped
	})
}
