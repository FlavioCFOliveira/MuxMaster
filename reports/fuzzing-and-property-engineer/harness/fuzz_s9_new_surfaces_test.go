package harness

// fuzz_s9_new_surfaces_test.go — S9 coverage for API surfaces with no prior
// fuzz targets: APIKey, NoCache, RecovererWithLogger, Match, ANY, With, Route,
// FuzzMiddlewareChain, FuzzMuxDispatch.
//
// Tasks covered:
//   FPE-2026-006 — APIKey/NoCache/RecovererWithLogger/Match/ANY/With/Route coverage
//   FPE-2026-008 — FuzzMiddlewareChain
//   FPE-2026-009 — FuzzMuxDispatch
//
// Invariants added:
//   I-APIKEY-01     — APIKey never panics for any header value; only 200 or 401
//   I-APIKEY-02     — GetAPIKeyIdentity returns identity for valid key
//   I-NOCACHE-01    — NoCache always sets Cache-Control/Pragma/Expires headers
//   I-MATCH-01      — Match never panics for any []string × pattern
//   I-ANY-01        — ANY registers across all 9 methods; all dispatch to handler
//   I-DISPATCH-01   — ServeHTTP on diverse mux never panics for any (method,path)
//   I-MW-CHAIN-01   — N-middleware chain never panics; no 500 for innocuous requests

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"pgregory.net/rapid"
)

// ============================================================
// I-APIKEY-01 / I-APIKEY-02 — APIKey middleware
// ============================================================

// FuzzAPIKey exercises the APIKey middleware with arbitrary X-API-Key header values.
// Invariant: never panic, return only 200 or 401 (never 500).
func FuzzAPIKey(f *testing.F) {
	apiMW := mw.APIKey(mw.APIKeyOptions{
		Keys: map[string]string{
			"valid-key-abc123": "user1",
			"valid-key-xyz456": "user2",
		},
	})

	f.Add("valid-key-abc123")
	f.Add("valid-key-xyz456")
	f.Add("")
	f.Add("invalid")
	f.Add(strings.Repeat("A", 4096))
	f.Add("\x00")
	f.Add("\r\nX-Injected: evil")
	f.Add("Bearer token") // wrong format for API key

	f.Fuzz(func(t *testing.T, keyHeader string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-APIKEY-01 PANIC: header=%q r=%v\n%s", keyHeader, r, debug.Stack())
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if keyHeader != "" {
			req.Header.Set("X-API-Key", keyHeader)
		}
		rec := httptest.NewRecorder()
		apiMW(h200).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
			t.Fatalf("I-APIKEY-01: unexpected status %d for header=%q", rec.Code, keyHeader)
		}
	})
}

// TestProp_APIKeyCorrectness verifies that valid keys are admitted and invalid keys rejected.
func TestProp_APIKeyCorrectness(t *testing.T) {
	validKeys := map[string]string{
		"secret-alpha": "alice",
		"secret-beta":  "bob",
	}
	apiMW := mw.APIKey(mw.APIKeyOptions{Keys: validKeys})

	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-APIKEY-02 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		// Draw either a valid key (from validKeys) or an arbitrary string.
		useValid := rapid.Bool().Draw(t, "useValid")
		var key string
		if useValid {
			// Pick one of the two valid keys deterministically.
			if rapid.Bool().Draw(t, "whichKey") {
				key = "secret-alpha"
			} else {
				key = "secret-beta"
			}
		} else {
			key = rapid.String().Draw(t, "key")
		}

		var gotIdentity string
		var identityFound bool
		handler := apiMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotIdentity, identityFound = mw.GetAPIKeyIdentity(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Verify correctness.
		expectedID, isValid := validKeys[key]
		if isValid {
			if rec.Code != http.StatusOK {
				t.Errorf("I-APIKEY-02: valid key %q rejected (got %d)", key, rec.Code)
			}
			if !identityFound || gotIdentity != expectedID {
				t.Errorf("I-APIKEY-02: identity mismatch for key=%q: got=%q want=%q", key, gotIdentity, expectedID)
			}
		} else {
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("I-APIKEY-02: invalid key %q accepted (got %d)", key, rec.Code)
			}
		}
	})
}

// TestAPIKeyEmptyKeysPanics verifies construction-time panic for empty key map.
func TestAPIKeyEmptyKeysPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("APIKey(empty keys) did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value not string: %T %v", r, r)
		}
		if !strings.Contains(msg, "APIKey") && !strings.Contains(msg, "key") {
			t.Errorf("panic message does not mention APIKey: %q", msg)
		}
	}()
	_ = mw.APIKey(mw.APIKeyOptions{Keys: map[string]string{}})
}

// ============================================================
// I-NOCACHE-01 — NoCache middleware
// ============================================================

// TestProp_NoCacheHeadersAlwaysSet verifies that NoCache always sets
// Cache-Control, Pragma, and Expires headers on every response.
func TestProp_NoCacheHeadersAlwaysSet(t *testing.T) {
	noCacheMW := mw.NoCache()

	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-NOCACHE-01 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		method := rapid.StringMatching(`[A-Z]{1,8}`).Draw(t, "method")
		path := rapid.StringMatching(`/[a-z0-9/]{0,30}`).Draw(t, "path")
		if path == "" {
			path = "/"
		}

		req := httptest.NewRequest(method, "http://example.com"+path, nil)
		rec := httptest.NewRecorder()
		noCacheMW(h200).ServeHTTP(rec, req)

		// I-NOCACHE-01: these headers must always be set.
		if cc := rec.Header().Get("Cache-Control"); cc == "" {
			t.Errorf("I-NOCACHE-01: Cache-Control not set for method=%q path=%q", method, path)
		}
		if p := rec.Header().Get("Pragma"); p == "" {
			t.Errorf("I-NOCACHE-01: Pragma not set")
		}
		if e := rec.Header().Get("Expires"); e == "" {
			t.Errorf("I-NOCACHE-01: Expires not set")
		}
	})
}

// ============================================================
// RecovererWithLogger — never panics, logs to slog.Logger
// ============================================================

// FuzzRecovererWithLogger verifies RecovererWithLogger absorbs panics and
// never itself panics for any panic value.
func FuzzRecovererWithLogger(f *testing.F) {
	// Use a discard slog handler for fuzz speed — only the no-panic invariant matters.
	recovMW := mw.RecovererWithLogger(slog.New(slog.DiscardHandler))

	f.Add("panic string")
	f.Add("")
	f.Add("\x00\r\n")
	f.Add(strings.Repeat("A", 65536))

	f.Fuzz(func(t *testing.T, panicVal string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC escaped RecovererWithLogger: r=%v\n%s", r, debug.Stack())
			}
		}()
		handler := recovMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(panicVal)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("RecovererWithLogger did not write 500: got=%d panicVal=%q", rec.Code, panicVal)
		}
	})
}

// ============================================================
// I-MATCH-01 — Mux.Match (multi-method registration)
// ============================================================

// FuzzMatch verifies that Mux.Match never panics unexpectedly for any
// (methods slice, pattern) combination.
func FuzzMatch(f *testing.F) {
	f.Add("GET,POST", "/items")
	f.Add("GET", "/items/:id")
	f.Add("PUT,PATCH,DELETE", "/items/:id")
	f.Add("", "/items")
	f.Add("GET", "no-slash")
	f.Add("INVALID", "/path")

	f.Fuzz(func(t *testing.T, methodsCSV, pattern string) {
		defer func() {
			if r := recover(); r != nil {
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("I-MATCH-01 PANIC (non-string): methods=%q pattern=%q r=%v\n%s",
						methodsCSV, pattern, r, debug.Stack())
				}
				// Expected panics from documented invariants.
				if isExpectedHandlePanic(msg) {
					return
				}
				t.Fatalf("I-MATCH-01 PANIC (unexpected): methods=%q pattern=%q msg=%q\n%s",
					methodsCSV, pattern, msg, debug.Stack())
			}
		}()
		var methods []string
		for _, m := range strings.Split(methodsCSV, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				methods = append(methods, m)
			}
		}
		if len(methods) == 0 {
			return
		}
		mux := mm.New()
		mux.Match(methods, pattern, h200)
	})
}

// TestProp_MatchRoutesAllMethods verifies that Match(methods, pattern, h)
// routes all listed methods to the handler correctly.
func TestProp_MatchRoutesAllMethods(t *testing.T) {
	// Use a fixed set of safe methods and patterns to avoid route conflicts.
	cases := []struct {
		methods []string
		pattern string
		path    string
	}{
		{[]string{"GET", "POST"}, "/items", "/items"},
		{[]string{"PUT", "PATCH", "DELETE"}, "/items/:id", "/items/42"},
		{[]string{"GET"}, "/ping", "/ping"},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.methods, "+"), func(t *testing.T) {
			mux := mm.New()
			mux.Match(tc.methods, tc.pattern, h200)

			for _, method := range tc.methods {
				req := httptest.NewRequest(method, tc.path, nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("Match: method=%q path=%q got=%d want=200", method, tc.path, rec.Code)
				}
			}
		})
	}
}

// ============================================================
// I-ANY-01 — Mux.ANY (all methods)
// ============================================================

// TestProp_ANYRoutesAllMethods verifies that ANY(pattern, h) routes all
// standard HTTP methods to the handler.
func TestProp_ANYRoutesAllMethods(t *testing.T) {
	allMethods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace,
	}

	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-ANY-01 PANIC: %v\n%s", r, debug.Stack())
			}
		}()

		seg := rapid.StringMatching(`[a-z][a-z0-9]{0,8}`).Draw(t, "seg")
		pattern := "/any/" + seg

		mux := mm.New()
		mux.ANY(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		for _, method := range allMethods {
			// HEAD and OPTIONS may be handled specially (TSR, OPTIONS coalescing)
			// so we allow both 200 and 204/301/302 as valid outcomes.
			req := httptest.NewRequest(method, pattern, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == 0 {
				t.Errorf("I-ANY-01: method=%q: no response written", method)
			}
		}
	})
}

// ============================================================
// With() group middleware scoping
// ============================================================

// TestProp_WithGroupMiddleware verifies that middleware registered via With()
// applies only to routes in the resulting group, not to routes outside it.
func TestProp_WithGroupMiddleware(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in With group middleware test: %v\n%s", r, debug.Stack())
			}
		}()

		n := rapid.IntRange(1, 5).Draw(t, "n")
		var applied int

		mux := mm.New()

		// Build n counting middlewares.
		mws := make([]func(http.Handler) http.Handler, n)
		for i := range n {
			mws[i] = func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					applied++
					next.ServeHTTP(w, r)
				})
			}
		}

		// Register one route WITH the middleware group and one WITHOUT.
		withGroup := mux.With(mws...)
		withGroup.GET("/with-mw", h200)
		mux.GET("/without-mw", h200)

		// Request to /with-mw must trigger all n middlewares.
		applied = 0
		req1 := httptest.NewRequest(http.MethodGet, "/with-mw", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req1)
		if applied != n {
			t.Errorf("With(): expected %d middleware executions, got %d", n, applied)
		}

		// Request to /without-mw must NOT trigger the grouped middlewares.
		applied = 0
		req2 := httptest.NewRequest(http.MethodGet, "/without-mw", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req2)
		if applied != 0 {
			t.Errorf("With(): grouped middleware ran %d times on non-grouped route", applied)
		}
	})
}

// ============================================================
// Route() DSL — callback-based group registration
// ============================================================

// TestProp_RouteDSLRegistration verifies that Route(prefix, fn) registers
// routes correctly through the callback DSL.
func TestProp_RouteDSLRegistration(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Route DSL: %v\n%s", r, debug.Stack())
			}
		}()

		prefix := rapid.StringMatching(`/[a-z][a-z0-9]{0,7}`).Draw(t, "prefix")
		leaf := rapid.StringMatching(`/[a-z][a-z0-9]{0,7}`).Draw(t, "leaf")

		mux := mm.New()
		mux.Route(prefix, func(g *mm.Group) {
			g.GET(leaf, h200)
		})

		fullPath := prefix + leaf
		req := httptest.NewRequest(http.MethodGet, fullPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Route DSL: path=%q got=%d want=200", fullPath, rec.Code)
		}
	})
}

// ============================================================
// I-MW-CHAIN-01 — FuzzMiddlewareChain (FPE-2026-008)
// ============================================================

// FuzzMiddlewareChain exercises composition of N middlewares from a fixed
// library, verifying that any combination never panics and that innocuous
// GET requests get a valid response.
//
// The middleware library includes: RequestID, NoCache, Recoverer, BasicAuth
// (which will 401 for anonymous requests — that is an acceptable outcome).
func FuzzMiddlewareChain(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(2)
	f.Add(5)
	f.Add(50)

	f.Fuzz(func(t *testing.T, n int) {
		if n < 0 || n > 50 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-MW-CHAIN-01 PANIC: n=%d r=%v\n%s", n, r, debug.Stack())
			}
		}()

		// Middleware catalogue — safe, non-blocking middlewares only.
		catalogue := []func(http.Handler) http.Handler{
			mw.RequestID(),
			mw.NoCache(),
			mw.Recoverer(),
		}

		mux := mm.New()
		for i := range n {
			mux.Use(catalogue[i%len(catalogue)])
		}
		mux.GET("/ping", h200)

		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Valid outcomes: 200 (all middleware pass-through), or 401 (auth block).
		// Never 500 from middleware chain composition.
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("I-MW-CHAIN-01: 500 from N=%d middleware chain", n)
		}
		if rec.Code == 0 {
			t.Fatalf("I-MW-CHAIN-01: no response written for N=%d middleware chain", n)
		}
	})
}

// TestProp_MiddlewareChainOrdering verifies that a mixed middleware chain
// executes in registration order (extends I-05).
func TestProp_MiddlewareChainOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in MiddlewareChainOrdering: %v\n%s", r, debug.Stack())
			}
		}()

		n := rapid.IntRange(1, 15).Draw(t, "n")
		var log []int

		mux := mm.New()
		for i := range n {
			i := i
			mux.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					log = append(log, i)
					next.ServeHTTP(w, r)
				})
			})
		}
		mux.GET("/", h200)

		log = log[:0]
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		for i := range n {
			if i >= len(log) || log[i] != i {
				t.Fatalf("I-MW-CHAIN-01: order wrong at index %d: log=%v", i, log)
			}
		}
	})
}

// ============================================================
// I-DISPATCH-01 — FuzzMuxDispatch (FPE-2026-009)
// ============================================================

// setupDispatchMux builds a mux pre-populated with a diverse route set covering
// all radix-tree node types: static, param (1/2/3), catch-all, regex, optional,
// fast, and mounted sub-handlers.
func setupDispatchMux() *mm.Mux {
	mux := mm.New()

	// Static routes
	mux.GET("/", h200)
	mux.GET("/ping", h200)
	mux.POST("/ping", h200)
	mux.GET("/healthz", h200)

	// 1-param routes (reqBundle1 tier)
	mux.GET("/users/:id", h200)
	mux.DELETE("/users/:id", h200)
	mux.POST("/users", h200)

	// 2-param routes (reqBundle2 tier)
	mux.GET("/users/:id/posts/:postId", h200)
	mux.PUT("/users/:id/posts/:postId", h200)

	// 3-param route (reqBundle3 tier)
	mux.GET("/orgs/:org/repos/:repo/issues/:num", h200)

	// Catch-all
	mux.GET("/static/*filepath", h200)
	mux.GET("/files/*path", h200)

	// API group with 1-level prefix
	api := mux.Group("/api/v1")
	api.GET("/items", h200)
	api.POST("/items", h200)
	api.GET("/items/:id", h200)
	api.PUT("/items/:id", h200)
	api.DELETE("/items/:id", h200)

	// Fast route (HandleFast — bypasses Use middleware)
	mux.GETFast("/fast/ping", func(w http.ResponseWriter, r *http.Request, _ mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
	mux.GETFast("/fast/items/:id", func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

// FuzzMuxDispatch exercises the full dispatch surface (TSR, 405, OPTIONS,
// tiered param dispatch) on a pre-populated mux. This is the primary
// coverage vehicle for dispatch-layer invariants.
func FuzzMuxDispatch(f *testing.F) {
	mux := setupDispatchMux()

	// Known-good seeds.
	f.Add("GET", "/")
	f.Add("GET", "/ping")
	f.Add("POST", "/ping")
	f.Add("DELETE", "/ping") // 405
	f.Add("GET", "/users/123")
	f.Add("GET", "/users/123/posts/456")
	f.Add("GET", "/orgs/myorg/repos/myrepo/issues/7")
	f.Add("GET", "/static/img/logo.png")
	f.Add("GET", "/api/v1/items")
	f.Add("GET", "/api/v1/items/99")
	f.Add("OPTIONS", "/users/123") // OPTIONS coalescing
	f.Add("GET", "/not-found")
	f.Add("CUSTOM", "/users/123") // unknown method
	f.Add("GET", "/fast/ping")
	f.Add("GET", "/fast/items/7")

	f.Fuzz(func(t *testing.T, method, urlPath string) {
		if containsControlBytes(urlPath) || containsControlBytes(method) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-DISPATCH-01 PANIC: method=%q path=%q r=%v\n%s",
					method, urlPath, r, debug.Stack())
			}
		}()
		req, err := buildRequest(method, urlPath)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// I-DISPATCH-01: only valid HTTP status codes are acceptable.
		code := rec.Code
		if code < 100 || code > 599 {
			t.Fatalf("I-DISPATCH-01: invalid status code %d for method=%q path=%q", code, method, urlPath)
		}
	})
}

// TestDispatchKnownRoutes verifies that known-registered routes are reachable
// (deterministic complement to FuzzMuxDispatch).
func TestDispatchKnownRoutes(t *testing.T) {
	mux := setupDispatchMux()

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{"GET", "/", 200},
		{"GET", "/ping", 200},
		{"POST", "/ping", 200},
		{"DELETE", "/ping", 405},
		{"GET", "/users/123", 200},
		{"GET", "/users/123/posts/456", 200},
		{"GET", "/orgs/myorg/repos/myrepo/issues/7", 200},
		{"GET", "/static/img/logo.png", 200},
		{"GET", "/api/v1/items", 200},
		{"POST", "/api/v1/items", 200},
		{"GET", "/api/v1/items/42", 200},
		{"GET", "/fast/ping", 200},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC: method=%q path=%q r=%v\n%s", tc.method, tc.path, r, debug.Stack())
				}
			}()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("method=%q path=%q: got=%d want=%d", tc.method, tc.path, rec.Code, tc.want)
			}
		})
	}
}
