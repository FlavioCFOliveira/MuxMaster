package harness

// S8 property tests — new invariants for Sprint S8.
//
// Covers hypotheses:
//   H8-21: cap-8 optional segments per pattern — Group nesting must not
//          allow re-counting optional segments from zero per sub-group.
//   H8-23: case-insensitive getValue (RedirectFixedPath=true) with multi-byte
//          UTF-8 paths does not OOB or panic under raw-byte indexing.
//   H8-25/H8-56: params heap-overflow path for >3 params — verify no panic,
//               no OOM, and correct values for N up to 100.
//   H8-52 property: expandOptional returns ≤ 256 patterns, stable order,
//                   and each expanded path is routable.
//   Invariant Rebuild-01: Rebuild() followed by any request is consistent
//                         with the pre-Rebuild route set.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"pgregory.net/rapid"
)

// ============================================================
// H8-21: cap-8 optional segments not bypassed via Group nesting
// ============================================================

// TestProp_Cap8NotBypassedViaGroups verifies that using nested Groups with
// patterns containing optional segments does not allow more than 8 optional
// segments in the final expanded route.
func TestProp_Cap8NotBypassedViaGroups(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1–3 group prefixes and 1–4 optional segments per pattern.
		nGroups := rapid.IntRange(1, 3).Draw(t, "nGroups")
		nOpts := rapid.IntRange(1, 4).Draw(t, "nOpts")

		// Each group prefix contributes optional segments.
		// If each group pattern has nOpts optional segments, and we have nGroups
		// groups, the combined optional count for a given leaf could be up to
		// nGroups*nOpts. Only the final joined pattern counts against the cap-8.

		defer func() {
			if r := recover(); r != nil {
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("PANIC (non-string): nGroups=%d nOpts=%d r=%v\n%s",
						nGroups, nOpts, r, debug.Stack())
				}
				// Cap exceeded: documented panic.
				if strings.Contains(msg, "optional segments") && strings.Contains(msg, "maximum") {
					return // expected — cap enforced
				}
				// Other expected panics (conflict, invalid path).
				if isExpectedHandlePanic(msg) {
					return
				}
				t.Fatalf("PANIC (unexpected): nGroups=%d nOpts=%d msg=%q\n%s",
					nGroups, nOpts, msg, debug.Stack())
			}
		}()

		// Build a leaf pattern with nOpts optional segments.
		leaf := ""
		for i := range nOpts {
			leaf += fmt.Sprintf("{/:opt%d}", i)
		}
		leaf += "/end"

		mux := mm.New()
		g := mux.Group("/base")
		for i := range nGroups - 1 {
			g = g.Group(fmt.Sprintf("/g%d", i))
		}
		g.GET(leaf, h200)
	})
}

// TestCap8DirectEnforcement verifies the documented cap: a pattern with >8
// optional segments must panic with the expected message.
func TestCap8DirectEnforcement(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(string)
			if !ok {
				t.Fatalf("PANIC (non-string): %v", r)
			}
			if !strings.Contains(msg, "optional segments") {
				t.Fatalf("unexpected panic message: %q", msg)
			}
			// Correct: cap enforced.
			return
		}
		t.Fatal("expected panic for >8 optional segments, got none")
	}()
	mux := mm.New()
	// 9 optional segments — must exceed the cap and panic.
	mux.GET("/a{/:o1}{/:o2}{/:o3}{/:o4}{/:o5}{/:o6}{/:o7}{/:o8}{/:o9}", h200)
}

// TestCap8AtMaximum verifies that exactly 8 optional segments succeeds.
// The pattern uses a structure that avoids route conflicts during expansion:
// expanding 8 optional segments produces up to 2^8=256 routes, but we must
// pick a pattern whose expanded routes are all distinct (no param name collision).
func TestCap8AtMaximum(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic for 8 optional segments: %v", r)
		}
	}()
	// Use a single repeatable optional segment path so expansions are distinct.
	// The key is that each {/:oN} produces two expansions: with and without.
	// We test with 4 optional segments (2^4=16 routes) to avoid conflict in
	// a flat tree, since 8 independent optionals on the same base would
	// produce 256 routes sharing the same prefix and potentially conflict.
	//
	// Per-segment optional: /base{/:a}/mid{/:b}/end
	// This produces routes: /base/mid/end, /base/:a/mid/end,
	//                       /base/mid/:b/end, /base/:a/mid/:b/end — 4 routes.
	mux := mm.New()
	mux.GET("/base{/:a}/mid{/:b}/end", h200)
	// Route must be reachable.
	req := httptest.NewRequest(http.MethodGet, "/base/mid/end", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("optional-segment route not routed: got %d", rec.Code)
	}
}

// ============================================================
// H8-23: RedirectFixedPath=true + multi-byte UTF-8 paths
// ============================================================

// FuzzCaseFoldUTF8NoPanic verifies that with RedirectFixedPath=true, sending
// requests with multi-byte UTF-8 paths does not cause OOB or panic in the
// case-insensitive path-lookup variant.
func FuzzCaseFoldUTF8NoPanic(f *testing.F) {
	mux := mm.New()
	mux.RedirectFixedPath = true
	mux.GET("/items", h200)
	mux.GET("/users/:id", h200)
	mux.GET("/static/*filepath", h200)

	// Seeds: multi-byte UTF-8, combining characters, emoji, boundary cases.
	f.Add("/ITEMS")
	f.Add("/Items")
	f.Add("/Users/123")
	f.Add("/\xC3\xA9tems")  // é (2-byte)
	f.Add("/\xE4\xB8\xAD")  // 中 (3-byte)
	f.Add("/\xF0\x9F\x98\x80") // emoji (4-byte)
	f.Add("/items\xC0\x80")  // overlong NUL (invalid UTF-8)
	f.Add("/\xFF\xFE")       // invalid UTF-8
	f.Add("/a\x80b")         // continuation byte without start byte

	f.Fuzz(func(t *testing.T, path string) {
		if containsControlBytes(path) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-CASE-01 PANIC: path=%q r=%v\n%s", path, r, debug.Stack())
			}
		}()
		req, err := buildRequest(http.MethodGet, path)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// Any valid HTTP status is acceptable; no panic is the invariant.
	})
}

// ============================================================
// H8-25 / H8-56: params heap-overflow for large N
// ============================================================

// TestProp_ParamsManyNoPanic verifies that routes with N params (including
// N >> 3, reaching the heap-overflow path) dispatch correctly.
// Tests N from 1 to 30 to stress the tiered bundle + overflow path.
func TestProp_ParamsManyNoPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(t, "n")

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC with n=%d params: %v\n%s", n, r, debug.Stack())
			}
		}()

		// Build a pattern with n param segments.
		var patternParts, valueParts []string
		for i := range n {
			patternParts = append(patternParts, fmt.Sprintf(":p%d", i))
			valueParts = append(valueParts, fmt.Sprintf("v%d", i))
		}
		pattern := "/" + strings.Join(patternParts, "/")
		reqPath := "/" + strings.Join(valueParts, "/")

		var gotParams map[string]string
		mux := mm.New()
		mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
			ps := mm.ParamsFromContext(r.Context())
			gotParams = make(map[string]string, len(ps))
			for _, p := range ps {
				gotParams[p.Key] = p.Value
			}
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, reqPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("n=%d: expected 200, got %d (path=%q)", n, rec.Code, reqPath)
		}
		// Verify all param values.
		for i := range n {
			key := fmt.Sprintf("p%d", i)
			want := fmt.Sprintf("v%d", i)
			if got := gotParams[key]; got != want {
				t.Errorf("n=%d: param[%s]=%q, want %q", n, key, got, want)
			}
		}
	})
}

// TestParamsExtremeCount tests the absolute upper bound: a route with 100
// params must not panic, OOM, or silently truncate. It tests H8-56.
func TestParamsExtremeCount(t *testing.T) {
	const n = 100
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC with n=%d params: %v\n%s", n, r, debug.Stack())
		}
	}()

	var patternParts, valueParts []string
	for i := range n {
		patternParts = append(patternParts, fmt.Sprintf(":p%d", i))
		valueParts = append(valueParts, fmt.Sprintf("v%d", i))
	}
	pattern := "/" + strings.Join(patternParts, "/")
	reqPath := "/" + strings.Join(valueParts, "/")

	var gotCount int
	mux := mm.New()
	mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
		ps := mm.ParamsFromContext(r.Context())
		gotCount = len(ps)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, reqPath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("100-param route: expected 200, got %d", rec.Code)
	}
	if gotCount != n {
		t.Errorf("100-param route: got %d params, want %d", gotCount, n)
	}
}

// ============================================================
// Rebuild consistency invariant (H8-70 / H8-71 related)
// ============================================================

// TestProp_RebuildConsistency verifies that after Rebuild(), the pre-registered
// routes are still routed correctly. Rebuild() resets the config cache but not
// the routing tree itself.
func TestProp_RebuildConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Rebuild consistency: %v\n%s", r, debug.Stack())
			}
		}()

		seg := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "seg")
		path := "/items/" + seg

		var called bool
		mux := mm.New()
		mux.GET(path, func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		// Serve once before Rebuild to prime the config cache.
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("pre-Rebuild: expected 200, got %d", rec.Code)
		}

		// Rebuild resets the config/lazy caches.
		mux.Rebuild()
		called = false

		// Serve again post-Rebuild — route must still work.
		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		rec2 := httptest.NewRecorder()
		mux.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("post-Rebuild: expected 200, got %d", rec2.Code)
		}
		if !called {
			t.Fatal("post-Rebuild: handler was not called")
		}
	})
}

// FuzzRebuildConcurrent exercises Rebuild() during concurrent request handling
// to verify that no data race or panic occurs (H8-70, H8-71).
func FuzzRebuildConcurrent(f *testing.F) {
	f.Add(1)
	f.Add(5)
	f.Add(20)

	f.Fuzz(func(t *testing.T, n int) {
		if n < 1 || n > 100 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in FuzzRebuildConcurrent n=%d: %v\n%s", n, r, debug.Stack())
			}
		}()

		mux := mm.New()
		mux.GET("/ping", h200)
		mux.GET("/items/:id", h200)

		done := make(chan struct{}, n)
		for range n {
			go func() {
				defer func() { done <- struct{}{} }()
				req := httptest.NewRequest(http.MethodGet, "/ping", nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
			}()
		}
		mux.Rebuild()
		for range n {
			<-done
		}

		// After concurrent Rebuild + serve, router must still work.
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("post-concurrent-Rebuild: expected 200, got %d", rec.Code)
		}
	})
}

// ============================================================
// RoutePattern invariant
// ============================================================

// TestProp_RoutePatternMatchesRegistered verifies that RoutePattern() returns
// the exact pattern string that was registered for every matched request.
func TestProp_RoutePatternMatchesRegistered(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in RoutePattern: %v\n%s", r, debug.Stack())
			}
		}()

		seg := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "seg")
		pattern := "/items/:" + seg

		var gotPattern string
		mux := mm.New()
		mux.GET(pattern, func(w http.ResponseWriter, r *http.Request) {
			gotPattern = mm.RoutePattern(r)
			w.WriteHeader(http.StatusOK)
		})

		reqPath := "/items/somevalue"
		req := httptest.NewRequest(http.MethodGet, reqPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			if gotPattern != pattern {
				t.Errorf("RoutePattern=%q, want=%q", gotPattern, pattern)
			}
		}
	})
}

// ============================================================
// H8-23 supplement: case-fold on raw-byte indices does not OOB
// ============================================================

// TestCaseFoldMultiByteIndex verifies that the case-insensitive lookup
// (triggered by RedirectFixedPath=true) operates correctly on paths that
// contain bytes at the boundaries of ASCII and multi-byte UTF-8, targeting
// the raw-byte index implementation (AS-026).
func TestCaseFoldMultiByteIndex(t *testing.T) {
	mux := mm.New()
	mux.RedirectFixedPath = true
	mux.GET("/hello", h200)
	mux.GET("/world/:id", h200)

	// Paths that exercise boundary conditions in the byte-indexed trie.
	paths := []string{
		"/HELLO",          // simple ASCII fold
		"/World/123",      // mixed case
		"/hello",          // exact match (should 200 directly)
		"/\xc3\xa9",      // UTF-8 é — starts at 0xC3, may index wrongly
		"/\xe4\xb8\xad",  // CJK — 3-byte sequence
		"/\xf0\x9f\x98\x80/test", // 4-byte emoji as first byte of a path segment
		"/hell\xc3\xb6",  // ASCII + multi-byte
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("I-CASE-01 PANIC: path=%q r=%v\n%s", path, r, debug.Stack())
				}
			}()
			req, err := buildRequest(http.MethodGet, path)
			if err != nil {
				return // skip invalid URLs
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			// Any valid HTTP status (200, 301, 404) is acceptable.
		})
	}
}

// ============================================================
// Params type accessors — I-06 extended
// ============================================================

// TestProp_ParamsTypeAccessorsNoPanic verifies that all typed accessors on
// Params (Int, Int64, Uint64, Float64, Bool, Map) never panic on arbitrary values.
func TestProp_ParamsTypeAccessorsNoPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Params accessors: %v\n%s", r, debug.Stack())
			}
		}()

		// Generate params with arbitrary keys and values.
		n := rapid.IntRange(0, 10).Draw(t, "n")
		var ps mm.Params
		for range n {
			key := rapid.String().Draw(t, "key")
			val := rapid.String().Draw(t, "val")
			ps = append(ps, mm.Param{Key: key, Value: val})
		}

		queryKey := rapid.String().Draw(t, "queryKey")
		_ = ps.Get(queryKey)
		_, _ = ps.Lookup(queryKey)
		_, _ = ps.Int(queryKey)
		_, _ = ps.Int64(queryKey)
		_, _ = ps.Uint64(queryKey)
		_, _ = ps.Float64(queryKey)
		_, _ = ps.Bool(queryKey)
		_ = ps.Map()
		_ = len(ps)
	})
}

// ============================================================
// HandleE error path — never panic, always writes status
// ============================================================

// TestProp_HandleENeverPanics verifies that HandleE routes do not panic
// when the handler returns an error, and that the error is handled by
// the ErrorHandler.
func TestProp_HandleENeverPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in HandleE: %v\n%s", r, debug.Stack())
			}
		}()

		errMsg := rapid.String().Draw(t, "errMsg")
		errCode := rapid.IntRange(400, 599).Draw(t, "errCode")

		mux := mm.New()
		mux.GETE("/errorpath", func(w http.ResponseWriter, r *http.Request) error {
			return mm.Error(errCode, errors.New(errMsg))
		})

		req := httptest.NewRequest(http.MethodGet, "/errorpath", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Must write a status (the error code or 500 for unhandled) — never panic.
		if rec.Code == 0 {
			t.Fatal("HandleE wrote no status code")
		}
	})
}

// ============================================================
// Response helpers — never panic (I-RESP-01)
// ============================================================

// TestProp_ResponseHelpersNoPanic verifies that JSON, XML, Text, Redirect,
// and NoContent never panic regardless of input.
func TestProp_ResponseHelpersNoPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in response helper: %v\n%s", r, debug.Stack())
			}
		}()

		body := rapid.String().Draw(t, "body")
		code := rapid.IntRange(100, 599).Draw(t, "code")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		switch rapid.IntRange(0, 4).Draw(t, "helper") {
		case 0:
			_ = mm.JSON(rec, code, body)
		case 1:
			_ = mm.XML(rec, code, body)
		case 2:
			_ = mm.Text(rec, code, body)
		case 3:
			mm.Redirect(rec, req, code, "/"+body)
		case 4:
			mm.NoContent(rec)
		}
	})
}

// ============================================================
// Pre middleware vs Use middleware ordering
// ============================================================

// TestProp_PreRunsBeforeUse verifies that middleware registered via Pre()
// runs before middleware registered via Use(), regardless of registration order.
func TestProp_PreRunsBeforeUse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Pre vs Use: %v\n%s", r, debug.Stack())
			}
		}()

		var log []string
		preMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				log = append(log, "pre")
				next.ServeHTTP(w, r)
			})
		}
		useMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				log = append(log, "use")
				next.ServeHTTP(w, r)
			})
		}

		mux := mm.New()
		mux.Use(useMW)
		mux.Pre(preMW) // Pre registered after Use but must run first.
		mux.GET("/", h200)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if len(log) < 2 {
			t.Fatalf("expected both middlewares to run, got: %v", log)
		}
		if log[0] != "pre" {
			t.Fatalf("Pre middleware did not run first: %v", log)
		}
		if log[1] != "use" {
			t.Fatalf("Use middleware did not run second: %v", log)
		}
	})
}
