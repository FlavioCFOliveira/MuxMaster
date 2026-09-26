package harness

// fuzz_o14_restored_test.go — rmp task #274 part 3/4 (O-14, findings.md B.4).
//
// Commit 5f804fa ("docs(reports): consolidate Sprint S9 security audit
// artifacts") rewrote this harness directory wholesale (17 new fuzz_*_test.go
// files replacing 10 deleted ones, plus a rewritten properties_test.go).
// Most of the deleted Fuzz*/TestProp_* functions were superseded by a
// same-invariant renamed target in the new files — see this task's final
// report for the full removed -> current-equivalent mapping. This file
// restores the handful that were dropped with NO current equivalent
// anywhere in this harness:
//
//   FuzzCleanPathDoubleEncoded        — CleanPath must not percent-decode
//                                        (only deterministic routing-level
//                                        pins remain, in path-routing-fuzzer's
//                                        harness: TestS8_Middleware_CleanPath_
//                                        DoubleEncodedSlash, TestS9_H05_...).
//   FuzzParamsMap / TestProp_ParamsMapLengthInvariant
//                                     — Params.Map() length/content
//                                        invariant (only the deterministic
//                                        params_test.go::TestParams_Map
//                                        remains at the root).
//   TestProp_HandleIdempotencyAndConflict
//                                     — duplicate Handle() on the same
//                                        pattern panics with "already
//                                        registered" and does not corrupt
//                                        the tree.
//   TestProp_ServeFilesRegistersTwoRoutes
//                                     — ServeFiles registers BOTH GET and
//                                        HEAD at its pattern (mux.go
//                                        ServeFiles calls m.Handle twice).
//                                        The current fuzz_servefiles_test.go
//                                        (restored separately, rmp #265)
//                                        only exercises escape/panic safety
//                                        via GET; it does not pin the
//                                        two-route registration invariant.
//   TestProp_GroupMiddlewareOrderPreserved
//                                     — top-level mux.Use() middleware runs
//                                        strictly before Group.Use()
//                                        middleware, in registration order
//                                        within each tier. Distinct from
//                                        TestProp_MiddlewareOrderPreserved
//                                        (top-level only) and
//                                        TestProp_WithGroupMiddleware
//                                        (presence/absence via With(), not
//                                        cross-tier ordering).
//   FuzzWalkRoutes                    — Walk()/Routes() agree on the
//                                        registered-route set, and Walk()
//                                        honours callback-error early
//                                        termination. FuzzWalkCorrupted
//                                        (current) targets a different
//                                        invariant (corrupted-tree
//                                        robustness), not normal-tree
//                                        Walk/Routes consistency.
//   FuzzCompressMultipleWrites        — Compress must buffer/concatenate
//                                        multiple w.Write() calls correctly
//                                        before gzip-encoding. The current
//                                        TestProp_CompressRoundtrip only
//                                        exercises a single w.Write() call.
//   TestProp_ErrorStatusCodePreserved — mm.Error(code, err) round-trips
//                                        StatusCode()/Error()/Unwrap()
//                                        correctly for any code/message.
//   FuzzMuxHandleTwice                — two distinct, non-conflicting
//                                        static patterns registered on the
//                                        same Mux are BOTH reachable via
//                                        Lookup after registration (adapted:
//                                        restricted to a bounded ASCII
//                                        charset so the property is tested
//                                        without depending on the separate,
//                                        already-tracked pathological-UTF-8
//                                        growth behaviour the original
//                                        version time-boxed around).
//
// Per the task's rule, no case in this file uses t.Skip(): inputs that
// cannot exercise the invariant (oversized, malformed, or ambiguous) are
// discarded with a plain `return` from the fuzz/property closure instead.

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"pgregory.net/rapid"
)

// ============================================================
// I-02b — CleanPath does not percent-decode (double-encoding pin)
//
// path.Clean operates on the string as-is; it never interprets or removes
// percent-encoded sequences. This is the middleware-level behaviour that
// TestS8_Middleware_CleanPath_DoubleEncodedSlash and
// TestS9_H05_CleanPath_MSR2026_0061_EncodedDotDot (path-routing-fuzzer's
// harness) rely on and pin at the full-router level with fixed cases; this
// fuzzer exercises the underlying middleware invariant directly, over the
// full byte-mutation input space.
// ============================================================

// o14ObservePath runs one round of CleanPath on the given raw URL.Path and
// returns the path visible to the inner handler. RawPath is left empty so
// only the Path-cleaning branch of the middleware is exercised.
func o14ObservePath(raw string) (seen string, served bool) {
	handler := mw.CleanPath()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: raw},
		Host:   "example.com",
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return
}

func FuzzCleanPathDoubleEncoded(f *testing.F) {
	f.Add("/foo/%2e%2e/bar")
	f.Add("/a/%2E%2E/b")
	f.Add("/a/..%2f..%2fetc/passwd")
	f.Add("/static/%252e%252e/admin")
	f.Add("/static/%2f../admin")
	f.Add("/%2f/..")      // regression seed (FPE-O14-001): a literal ".." segment
	f.Add("/%2e%2e/..")   // legitimately collapses whatever opaque segment
	f.Add("/a/%2e%2e/..") // precedes it — including one that happens to spell "%2f".

	f.Fuzz(func(t *testing.T, raw string) {
		if !strings.HasPrefix(raw, "/") || len(raw) > 2048 || strings.ContainsRune(raw, 0) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v\n%s", raw, r, debug.Stack())
			}
		}()
		seen, served := o14ObservePath(raw)
		if !served {
			return
		}
		// Ground-truth oracle: middleware/clean_path.go computes
		// `p := path.Clean(r.URL.Path)` with nothing else applied to Path
		// when RawPath is empty (as it is here — o14ObservePath only sets
		// Path). Any divergence from stdlib path.Clean's own output means
		// CleanPath applied extra transformation — most notably, percent-
		// decoding before cleaning, which would let %2e%2e or %2f be
		// interpreted as an actual ".." / "/" and collapse segments that
		// path.Clean's own literal-string semantics would not collapse.
		//
		// NOTE (FPE-O14-001, found running this fuzzer): an earlier version
		// of this test asserted the literal substring "%2f"/"%2e" must
		// always survive verbatim in the output. That is too strong: a
		// literal ".." segment legitimately cancels whatever opaque segment
		// precedes it, even one that happens to spell "%2f" or "%2e" — e.g.
		// path.Clean("/%2f/..") == "/" removes the "%2f" segment via normal
		// dot-dot collapsing, not via decoding. The oracle comparison below
		// does not have this false-positive: it is exactly what CleanPath
		// is specified to do.
		want := path.Clean(raw)
		if seen != want {
			t.Fatalf("CleanPath diverged from path.Clean oracle: raw=%q seen=%q want=%q "+
				"(possible unintended percent-decoding before cleaning)", raw, seen, want)
		}
	})
}

// ============================================================
// I-06c — Params.Map() length/content invariant
//
// len(ps.Map()) == number of DISTINCT keys in ps (duplicates collapse),
// and for every distinct key the map holds the value of its LAST
// occurrence in ps (Map() iterates forward, so later entries overwrite
// earlier ones — this pins that documented last-write-wins order).
// Map() must never be nil (params.go always allocates via make()).
// ============================================================

func FuzzParamsMap(f *testing.F) {
	f.Add([]byte("k\x00v\x00"))
	f.Add([]byte("a\x001\x00b\x002\x00"))
	f.Add([]byte{})
	f.Add([]byte("dup\x00first\x00dup\x00second\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in Params.Map: data=%x r=%v\n%s", data, r, debug.Stack())
			}
		}()
		p := buildParams(data)
		m := p.Map()
		if m == nil {
			t.Fatalf("Map() returned nil (params.go must always make() a map)")
		}

		lastValue := make(map[string]string)
		for _, pp := range p {
			lastValue[pp.Key] = pp.Value
		}
		if len(m) != len(lastValue) {
			t.Fatalf("Map len=%d, distinct keys=%d (data=%x)", len(m), len(lastValue), data)
		}
		for k, want := range lastValue {
			if got := m[k]; got != want {
				t.Fatalf("Map[%q]=%q, want last-write-wins value %q (data=%x)", k, got, want, data)
			}
		}
	})
}

// TestProp_ParamsMapLengthInvariant is the rapid complement to
// FuzzParamsMap: it draws Params slices with a controlled duplicate-key
// ratio, which explores the "many duplicates" corner of the invariant more
// systematically than byte mutation alone.
func TestProp_ParamsMapLengthInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 32).Draw(t, "n")
		// Small key alphabet on purpose: forces frequent key collisions so
		// the "distinct keys, last write wins" branch of the invariant is
		// exercised, not just the "all keys unique" happy path.
		keyGen := rapid.SampledFrom([]string{"a", "b", "c", "id", "name", ""})

		ps := make(mm.Params, 0, n)
		lastValue := make(map[string]string)
		for range n {
			k := keyGen.Draw(t, "key")
			v := rapid.String().Draw(t, "value")
			ps = append(ps, mm.Param{Key: k, Value: v})
			lastValue[k] = v
		}

		m := ps.Map()
		if m == nil {
			t.Fatalf("Map() returned nil for %d params", n)
		}
		if len(m) != len(lastValue) {
			t.Fatalf("Map len=%d, distinct keys=%d (n=%d)", len(m), len(lastValue), n)
		}
		for k, want := range lastValue {
			if got := m[k]; got != want {
				t.Fatalf("Map[%q]=%q, want %q", k, got, want)
			}
		}
		// Independence: mutating the returned map must not affect ps.
		for k := range m {
			m[k] = "MUTATED"
		}
		for _, pp := range ps {
			if pp.Value == "MUTATED" {
				t.Fatalf("mutating Map() result mutated the underlying Params (key=%q)", pp.Key)
			}
		}
	})
}

// ============================================================
// I-01d — Handle idempotency and conflict safety
//
// Registering the same (method, pattern) twice must panic with a message
// containing "already registered", and the tree must remain usable
// afterwards (the panic must not corrupt previously-registered routes).
// ============================================================

func TestProp_HandleIdempotencyAndConflict(t *testing.T) {
	seg := rapid.StringMatching(`[a-z][a-z0-9]{0,7}`)
	rapid.Check(t, func(t *rapid.T) {
		segs := rapid.SliceOfN(seg, 1, 3).Draw(t, "segs")
		pattern := "/" + strings.Join(segs, "/")

		mux := mm.New()
		mux.Handle(http.MethodGet, pattern, h200)

		if h, _, ok := mux.Lookup(http.MethodGet, pattern); !ok || h == nil {
			t.Fatalf("Lookup failed after first Handle: pattern=%q", pattern)
		}

		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic on duplicate Handle: pattern=%q", pattern)
				}
				msg := fmt.Sprint(r)
				if !strings.Contains(msg, "already registered") {
					t.Fatalf("duplicate-Handle panic has wrong message: pattern=%q msg=%v", pattern, r)
				}
			}()
			mux.Handle(http.MethodGet, pattern, h200)
		}()

		// Post-panic: the tree must not be corrupted — the original
		// registration must still be reachable.
		if h, _, ok := mux.Lookup(http.MethodGet, pattern); !ok || h == nil {
			t.Fatalf("Lookup failed after duplicate-Handle panic: pattern=%q", pattern)
		}
	})
}

// ============================================================
// I-SERVEFILES-03 — ServeFiles registers both GET and HEAD
//
// mux.go's ServeFiles calls m.Handle(http.MethodGet, prefix, h) and
// m.Handle(http.MethodHead, prefix, h) — both methods must resolve via
// Lookup for any path under the registered prefix.
// ============================================================

func TestProp_ServeFilesRegistersTwoRoutes(t *testing.T) {
	// A single shared root suffices: this property only checks Lookup()
	// (route registration), never an actual file read, so no per-iteration
	// isolation is needed — mirrors the original test's use of a constant
	// "/tmp" root.
	root := http.Dir(t.TempDir())
	seg := rapid.StringMatching(`[a-z][a-z0-9]{0,7}`)
	rapid.Check(t, func(t *rapid.T) {
		dir := seg.Draw(t, "dir")
		paramName := seg.Draw(t, "paramName")
		prefix := "/" + dir + "/*" + paramName

		mux := mm.New()
		mux.ServeFiles(prefix, root)

		for _, method := range []string{http.MethodGet, http.MethodHead} {
			h, _, ok := mux.Lookup(method, "/"+dir+"/any.txt")
			if !ok || h == nil {
				t.Fatalf("ServeFiles: %s not registered at prefix=%q", method, prefix)
			}
		}
		// A method ServeFiles does NOT register must not resolve.
		if h, _, ok := mux.Lookup(http.MethodPost, "/"+dir+"/any.txt"); ok || h != nil {
			t.Fatalf("ServeFiles unexpectedly registered POST at prefix=%q", prefix)
		}
	})
}

// ============================================================
// I-05b — Group middleware order preserved across tiers
//
// Top-level mux.Use() middleware must run strictly before Group.Use()
// middleware for any request that reaches a route registered through
// that group, and within each tier, execution order equals registration
// order.
// ============================================================

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
		for i := range nTop {
			mux.Use(build(fmt.Sprintf("T%d", i)))
		}
		g := mux.Group("/g")
		for i := range nGrp {
			g.Use(build(fmt.Sprintf("G%d", i)))
		}
		g.GET("/r", h200)

		req := httptest.NewRequest(http.MethodGet, "/g/r", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		expected := make([]string, 0, nTop+nGrp)
		for i := range nTop {
			expected = append(expected, fmt.Sprintf("T%d", i))
		}
		for i := range nGrp {
			expected = append(expected, fmt.Sprintf("G%d", i))
		}
		if !reflect.DeepEqual(order, expected) {
			t.Fatalf("order mismatch: got %v want %v", order, expected)
		}
	})
}

// ============================================================
// I-WALK-05 — Walk()/Routes() agree with the registered set, and Walk()
// honours callback-error early termination. (Renumbered from I-WALK-01,
// which collided with the pre-existing I-WALK-01..04 invariant in
// invariants.md — see that entry, fuzz_walk_corrupted_test.go, for
// I-WALK-01 through I-WALK-04.)
// ============================================================

func FuzzWalkRoutes(f *testing.F) {
	f.Add("/a", "/a/b", "/a/b/c")
	f.Add("/", "/users/x", "/static/y")
	f.Add("/api/v1", "/api/v2", "/api/v3")

	f.Fuzz(func(t *testing.T, p1, p2, p3 string) {
		if containsControlBytes(p1) || containsControlBytes(p2) || containsControlBytes(p3) {
			return
		}
		if len(p1) > 256 || len(p2) > 256 || len(p3) > 256 {
			return
		}

		patterns := []string{p1, p2, p3}
		registered := make(map[string]bool)

		mux := mm.New()
		for _, p := range patterns {
			func() {
				defer func() { _ = recover() }() // conflicting/invalid patterns are expected to panic; only track successes
				if p == "" || p[0] != '/' {
					return
				}
				mux.GET(p, h200)
				registered[p] = true
			}()
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Walk panic patterns=%v: %v\n%s", patterns, r, debug.Stack())
			}
		}()

		walkSeen := make(map[string]int)
		if err := mux.Walk(func(method, pattern string, handler http.Handler) error {
			walkSeen[method+" "+pattern]++
			return nil
		}); err != nil {
			t.Fatalf("Walk returned unexpected error: %v", err)
		}

		for p := range registered {
			key := "GET " + p
			if walkSeen[key] == 0 {
				t.Fatalf("Walk did not surface registered pattern %q (patterns=%v)", p, patterns)
			}
			if walkSeen[key] > 1 {
				t.Fatalf("Walk surfaced %q %d times", p, walkSeen[key])
			}
		}

		// Routes() must agree with Walk() on the full registered set.
		infos := mux.Routes()
		routesSeen := make(map[string]int)
		for _, info := range infos {
			routesSeen[info.Method+" "+info.Pattern]++
		}
		for k, v := range walkSeen {
			if routesSeen[k] != v {
				t.Fatalf("Walk/Routes disagree on %q: walk=%d routes=%d", k, v, routesSeen[k])
			}
		}

		// Early termination: the callback can return an error and Walk
		// must stop after the first call and propagate it.
		if len(infos) > 0 {
			sentinel := errors.New("stop")
			callCount := 0
			err := mux.Walk(func(method, pattern string, handler http.Handler) error {
				callCount++
				return sentinel
			})
			if err == nil {
				t.Fatalf("Walk ignored early-termination error (patterns=%v)", patterns)
			}
			if callCount != 1 {
				t.Fatalf("Walk called cb %d times after sentinel error, want 1", callCount)
			}
		}
	})
}

// ============================================================
// I-03b — Compress buffers multiple w.Write() calls correctly
//
// A handler that calls w.Write() more than once must have the concatenated
// output round-trip through gzip exactly, the same as a single-write body.
// ============================================================

func FuzzCompressMultipleWrites(f *testing.F) {
	f.Add([]byte("hello"), []byte(" world"))
	f.Add(bytes.Repeat([]byte("A"), 600), bytes.Repeat([]byte("B"), 600))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, a, b []byte) {
		if len(a)+len(b) > 8<<20 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v\n%s", r, debug.Stack())
			}
		}()

		handler := mw.Compress(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(a)
			_, _ = w.Write(b)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		enc := rec.Header().Get("Content-Encoding")
		out := rec.Body.Bytes()
		expected := append(append([]byte{}, a...), b...)

		if enc == "gzip" {
			gr, err := gzip.NewReader(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("gzip reader error: %v (a.len=%d b.len=%d)", err, len(a), len(b))
			}
			decoded, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("gzip read error: %v", err)
			}
			if !bytes.Equal(decoded, expected) {
				t.Fatalf("mismatch with two writes: expected.len=%d decoded.len=%d", len(expected), len(decoded))
			}
		} else {
			if !bytes.Equal(out, expected) {
				t.Fatalf("uncompressed mismatch: expected.len=%d out.len=%d", len(expected), len(out))
			}
		}
	})
}

// ============================================================
// I-ERROR-01 — mm.Error(code, err) round-trips StatusCode/Error/Unwrap
// ============================================================

func TestProp_ErrorStatusCodePreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		code := rapid.IntRange(100, 599).Draw(t, "code")
		msg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,32}`).Draw(t, "msg")

		inner := fmt.Errorf("%s", msg)
		wrapped := mm.Error(code, inner)

		if wrapped.StatusCode() != code {
			t.Fatalf("StatusCode: want %d got %d", code, wrapped.StatusCode())
		}
		if wrapped.Error() != msg {
			t.Fatalf("Error: want %q got %q", msg, wrapped.Error())
		}
		if !errors.Is(wrapped, inner) {
			t.Fatalf("errors.Is(wrapped, inner) = false; Unwrap() must expose the wrapped error")
		}
	})
}

// TestErrorNilPanics pins the documented precondition (handler.go: "Panics
// if err is nil") as a plain deterministic check — not itself a fuzz/
// property target (there is nothing to vary), but co-located with the
// property test above since both cover mm.Error's full contract.
func TestErrorNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("mm.Error(code, nil) did not panic")
		}
	}()
	mm.Error(500, nil)
}

// ============================================================
// I-HANDLE-02 — distinct, non-conflicting static patterns coexist
//
// Two distinct static patterns registered on the same Mux either both
// succeed (both then reachable via Lookup) or one panics with a documented
// conflict message (tree.go: "conflict"/"already registered") — never a
// silent loss of a previously-registered route.
//
// Adapted from the removed FuzzMuxHandleTwice: restricted to a bounded
// ASCII charset (a-z, 0-9, '/') so the coexistence property is exercised
// directly, without needing the original's goroutine + 2s time-box (which
// existed only to work around a separate, already-tracked pathological
// growth issue on adversarial UTF-8 input, out of scope for this
// invariant).
// ============================================================

func FuzzMuxHandleTwice(f *testing.F) {
	f.Add("/users/a", "/users/b")
	f.Add("/a", "/a/b")
	f.Add("/a/b", "/a")
	f.Add("/v1/users/x", "/v1/users/x/posts")
	f.Add("/x/y", "/x/z")
	f.Add("/a", "/a")

	f.Fuzz(func(t *testing.T, p1, p2 string) {
		if !isBoundedStaticPattern(p1) || !isBoundedStaticPattern(p2) {
			return
		}
		if p1 == p2 {
			return
		}

		mux := mm.New()
		bothRegistered := true

		func() {
			defer func() {
				if r := recover(); r != nil {
					msg := fmt.Sprint(r)
					if strings.Contains(msg, "conflict") || strings.Contains(msg, "already registered") {
						bothRegistered = false
						return
					}
					t.Fatalf("unexpected panic: p1=%q p2=%q panic=%v\n%s", p1, p2, r, debug.Stack())
				}
			}()
			mux.Handle(http.MethodGet, p1, h200)
			mux.Handle(http.MethodGet, p2, h200)
		}()

		if !bothRegistered {
			return
		}
		if h, _, ok := mux.Lookup(http.MethodGet, p1); !ok || h == nil {
			t.Fatalf("p1 unreachable after dual registration: p1=%q p2=%q", p1, p2)
		}
		if h, _, ok := mux.Lookup(http.MethodGet, p2); !ok || h == nil {
			t.Fatalf("p2 unreachable after dual registration: p1=%q p2=%q", p1, p2)
		}
	})
}

// isBoundedStaticPattern restricts fuzz input to a small, safe charset for
// FuzzMuxHandleTwice: absolute paths, lowercase ASCII + digits + '/' only,
// no empty segments, no trailing slash beyond "/", bounded length. This
// keeps the fuzzer inside the "plain static route" invariant space and
// avoids the unrelated adversarial-UTF-8 growth path tracked separately.
func isBoundedStaticPattern(p string) bool {
	if len(p) == 0 || p[0] != '/' || len(p) > 128 {
		return false
	}
	if p != "/" && p[len(p)-1] == '/' {
		return false
	}
	if strings.Contains(p, "//") {
		return false
	}
	for i := range len(p) {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '/':
		default:
			return false
		}
	}
	return true
}
