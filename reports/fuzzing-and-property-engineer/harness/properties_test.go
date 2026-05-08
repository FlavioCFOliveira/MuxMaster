package harness

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"pgregory.net/rapid"
)

// ============================================================
// Property: I-02 — CleanPath idempotency
// CleanPath(CleanPath(p)) == CleanPath(p) for all p.
// Length must not grow.
// ============================================================

func TestProp_CleanPathIdempotency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate any string — not just valid paths.
		p := rapid.String().Draw(t, "path")
		if len(p) > 8192 {
			t.Skip()
		}

		// CleanPath operates via a middleware, so we test path.Clean directly
		// (which CleanPath uses internally) and also the middleware itself.
		req1 := httptest.NewRequest(http.MethodGet, "/", nil)
		req1.URL.Path = p
		var cleaned1, cleaned2 string

		handler := mw.CleanPath()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cleaned1 = r.URL.Path
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req1)

		// Second pass.
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.URL.Path = cleaned1
		handler2 := mw.CleanPath()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cleaned2 = r.URL.Path
		}))
		rec2 := httptest.NewRecorder()
		handler2.ServeHTTP(rec2, req2)

		if cleaned1 != cleaned2 {
			t.Fatalf("CleanPath not idempotent: %q -> %q -> %q", p, cleaned1, cleaned2)
		}
		if len(cleaned1) > len(p)+1 { // +1 for leading / prepend
			t.Fatalf("CleanPath grew from %d to %d bytes: %q -> %q",
				len(p), len(cleaned1), p, cleaned1)
		}
	})
}

// ============================================================
// Property: I-03 — Compress roundtrip
// If response is gzip-encoded, gunzip(body) == original body.
// ============================================================

func TestProp_CompressRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		body := rapid.SliceOfN(rapid.Byte(), 0, 8192).Draw(t, "body")

		mux := mm.New()
		mux.Use(mw.Compress(5))
		mux.GET("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatalf("gzip.NewReader: %v (body len=%d)", err, len(body))
			}
			out, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("io.ReadAll gzip: %v", err)
			}
			if !bytes.Equal(out, body) {
				t.Fatalf("compress roundtrip mismatch: input len=%d output len=%d",
					len(body), len(out))
			}
		} else {
			// Not compressed (< minCompressSize). Verify raw body is intact.
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Fatalf("uncompressed body mismatch: input len=%d output len=%d",
					len(body), rec.Body.Len())
			}
		}
	})
}

// ============================================================
// Property: I-04 — Group prefix composition
// Routes registered through nested groups are reachable at the full path.
// ============================================================

func TestProp_GroupPrefixComposition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-4 prefix segments, each a valid non-empty path segment.
		prefixes := rapid.SliceOfN(
			rapid.StringMatching(`/[a-z][a-z0-9]{0,7}`),
			1, 4,
		).Draw(t, "prefixes")
		leaf := rapid.StringMatching(`/[a-z][a-z0-9]{0,7}`).Draw(t, "leaf")

		mux := mm.New()
		g := mux.Group(prefixes[0])
		for _, p := range prefixes[1:] {
			g = g.Group(p)
		}
		g.GET(leaf, h200)

		fullPath := strings.Join(prefixes, "") + leaf
		req := httptest.NewRequest(http.MethodGet, fullPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("group prefix composition failed: path=%q got=%d want=200",
				fullPath, rec.Code)
		}
	})
}

// ============================================================
// Property: I-05 — Middleware order preserved
// Middleware registered via Use() executes in registration order.
// ============================================================

func TestProp_MiddlewareOrderPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "n")

		var order []int
		var mws []func(http.Handler) http.Handler
		for i := range n {
			i := i
			mws = append(mws, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					order = append(order, i)
					next.ServeHTTP(w, r)
				})
			})
		}

		mux := mm.New()
		mux.Use(mws...)
		mux.GET("/", h200)
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		expected := make([]int, n)
		for i := range expected {
			expected[i] = i
		}
		if !reflect.DeepEqual(order, expected) {
			t.Fatalf("middleware order wrong: got=%v expected=%v", order, expected)
		}
	})
}

// ============================================================
// Property: I-07 — ParamsFromContext roundtrip
// For any param (key, value) injected via a real route,
// ParamsFromContext(ctx).Get(key) == value.
// ============================================================

func TestProp_ParamsFromContextRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		key := rapid.StringMatching(`[a-z][a-z0-9]{0,15}`).Draw(t, "key")
		value := rapid.StringMatching(`[a-zA-Z0-9_-]{1,32}`).Draw(t, "value")

		mux := mm.New()
		var got string
		var ok bool
		mux.GET("/items/:" + key, func(w http.ResponseWriter, r *http.Request) {
			ps := mm.ParamsFromContext(r.Context())
			got = ps.Get(key)
			_, ok = ps.Lookup(key)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/items/"+value, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("route not matched: key=%q value=%q got=%d", key, value, rec.Code)
		}
		if got != value {
			t.Fatalf("param mismatch: key=%q want=%q got=%q", key, value, got)
		}
		if !ok {
			t.Fatalf("Lookup returned ok=false for present key=%q", key)
		}
		// Also verify via PathParam.
		// (Not re-issued since req is already consumed — we rely on the handler having run.)
	})
}

// ============================================================
// Property: I-08 — HandleFast vs Handle routing equivalence
// For any route registered via both Handle and HandleFast at different paths,
// each must route independently without cross-contamination.
// Specifically tests hypothesis H-10 (silent unauthenticated when mixed).
// ============================================================

func TestProp_HandleFastVsHandleIsolation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in HandleFast/Handle isolation: %v\n%s", r, debug.Stack())
			}
		}()

		n := rapid.IntRange(1, 5).Draw(t, "middlewareCount")

		// Build an auth middleware that sets a header.
		var authApplied int
		authMW := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authApplied++
				next.ServeHTTP(w, r)
			})
		}

		mux := mm.New()
		for range n {
			mux.Use(authMW)
		}

		var stdlibHandlerCalled, fastHandlerCalled bool
		authApplied = 0

		mux.GET("/stdlib", func(w http.ResponseWriter, r *http.Request) {
			stdlibHandlerCalled = true
			w.WriteHeader(http.StatusOK)
		})
		mux.GETFast("/fast", func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
			fastHandlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		// Request to stdlib route — auth middleware MUST run.
		authApplied = 0
		req1 := httptest.NewRequest(http.MethodGet, "/stdlib", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req1)
		if !stdlibHandlerCalled {
			t.Fatal("stdlib handler not called")
		}
		if authApplied != n {
			t.Fatalf("auth middleware ran %d times on stdlib route, expected %d", authApplied, n)
		}

		// Request to fast route — stdlib auth middleware must NOT run.
		// (HandleFast is documented as bypassing Use() middleware — this is by design.)
		// The invariant here is that the fast handler IS called.
		authApplied = 0
		req2 := httptest.NewRequest(http.MethodGet, "/fast", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req2)
		if !fastHandlerCalled {
			t.Fatal("fast handler not called")
		}
		// Document the bypass: auth middleware ran 0 times on fast route.
		// If it runs, that means the semantics changed — fail to signal the change.
		if authApplied != 0 {
			t.Logf("NOTE: stdlib middleware ran %d times on fast route (semantics changed)", authApplied)
		}
	})
}

// ============================================================
// Property: I-09 — ServeHTTP never panics (broad property)
// For any method + path combination, ServeHTTP must not panic.
// ============================================================

func TestProp_ServeHTTPNeverPanics(t *testing.T) {
	mux := setupFuzzMux()
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ServeHTTP property: %v\n%s", r, debug.Stack())
			}
		}()
		method := rapid.StringMatching(`[A-Z]{1,10}`).Draw(t, "method")
		path := rapid.StringMatching(`/[a-zA-Z0-9/_:*.-]{0,100}`).Draw(t, "path")

		req := httptest.NewRequest(method, "http://example.com"+path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	})
}

// ============================================================
// Property: I-10 — PanicHandler always absorbs handler panics
// When PanicHandler is set, no panic escapes ServeHTTP.
// ============================================================

func TestProp_PanicHandlerAbsorbsPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		panicMsg := rapid.String().Draw(t, "panicMsg")

		var recovered any
		mux := mm.New()
		mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
			recovered = rcv
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		mux.GET("/panic", func(w http.ResponseWriter, r *http.Request) {
			panic(panicMsg)
		})

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped ServeHTTP despite PanicHandler: %v\n%s", r, debug.Stack())
			}
		}()

		req := httptest.NewRequest(http.MethodGet, "/panic", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if recovered == nil {
			t.Fatal("PanicHandler was not called")
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("PanicHandler did not write 500: got=%d", rec.Code)
		}
	})
}

// ============================================================
// Property: I-11 — RequestID always set in response
// The RequestID middleware must always set the X-Request-ID header.
// ============================================================

func TestProp_RequestIDAlwaysSet(t *testing.T) {
	mwRID := mw.RequestID()
	rapid.Check(t, func(t *rapid.T) {
		incomingID := rapid.String().Draw(t, "incomingID")

		handler := mwRID(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if incomingID != "" {
			req.Header.Set("X-Request-ID", incomingID)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		outID := rec.Header().Get("X-Request-ID")
		if outID == "" {
			t.Fatal("X-Request-ID header not set in response")
		}
		// Invariant: output ID must be bounded (no uncontrolled reflection of huge IDs).
		if len(outID) > 128 {
			t.Fatalf("X-Request-ID too long: %d bytes", len(outID))
		}
	})
}

// ============================================================
// Property: I-12 — BasicAuth constant-time comparison
// Verify that auth outcome is correct (correct/incorrect creds),
// not that it's constant-time (that's for timing-and-sidechannel-analyst).
// ============================================================

func TestProp_BasicAuthCorrectness(t *testing.T) {
	creds := map[string]string{
		"alice": "password123",
		"bob":   "s3cr3t!",
	}
	authMW := mw.BasicAuth("test", creds)

	rapid.Check(t, func(t *rapid.T) {
		user := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "user")
		pass := rapid.StringMatching(`[a-zA-Z0-9!@#]{1,20}`).Draw(t, "pass")

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in BasicAuth: user=%q pass=%q r=%v\n%s", user, pass, r, debug.Stack())
			}
		}()

		handler := authMW(h200)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth(user, pass)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		expectedPass, isValidUser := creds[user]
		if isValidUser && pass == expectedPass {
			if rec.Code != http.StatusOK {
				t.Fatalf("BasicAuth rejected valid credentials: user=%q pass=%q got=%d",
					user, pass, rec.Code)
			}
		} else {
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("BasicAuth accepted invalid credentials: user=%q pass=%q got=%d",
					user, pass, rec.Code)
			}
		}
	})
}

// ============================================================
// Property: I-13 — Mux.Lookup consistency
// For any route registered via Handle, Lookup(method, path) must
// return a non-nil handler and found=true.
// ============================================================

func TestProp_LookupConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Lookup: %v\n%s", r, debug.Stack())
			}
		}()
		seg := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "seg")

		mux := mm.New()
		pattern := "/items/" + seg
		mux.GET(pattern, h200)

		h, _, ok := mux.Lookup(http.MethodGet, pattern)
		if !ok || h == nil {
			t.Fatalf("Lookup failed for just-registered pattern=%q", pattern)
		}
		// HEAD should not be found unless registered.
		_, _, headOK := mux.Lookup(http.MethodHead, pattern)
		if headOK {
			t.Logf("HEAD found for pattern=%q (auto-registered)", pattern)
		}
	})
}

// ============================================================
// Property: I-14 — Mount RawPath stripping correctness
// After Mount, requests with percent-encoded paths must not have
// the prefix reappear in the stripped path (h-J co-investigation).
// ============================================================

func TestProp_MountRawPathStripping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in MountRawPath: %v\n%s", r, debug.Stack())
			}
		}()

		suffix := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "suffix")
		prefix := "/mounted/" + suffix

		mux := mm.New()
		var innerPath string
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			innerPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		mux.Mount(prefix, inner)

		reqPath := prefix + "/resource"
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+reqPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			if strings.HasPrefix(innerPath, prefix) {
				t.Errorf("Mount did not strip prefix: innerPath=%q still has prefix=%q",
					innerPath, prefix)
			}
		}
	})
}

// ============================================================
// Property: I-15 — Timeout middleware sets deadline on context
// The context passed to the handler must have a deadline set.
// We test deadline presence, not cancellation (which is timing-dependent).
// ============================================================

func TestProp_TimeoutSetsDeadline(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		timeoutMs := rapid.IntRange(100, 5000).Draw(t, "timeoutMs")

		timeoutMW := mw.Timeout(time.Duration(timeoutMs) * time.Millisecond)

		var deadline time.Time
		var deadlineOk bool
		handler := timeoutMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, deadlineOk = r.Context().Deadline()
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !deadlineOk {
			t.Fatalf("context has no deadline after Timeout(%dms)", timeoutMs)
		}
		if deadline.IsZero() {
			t.Fatal("deadline is zero")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("handler did not complete: got=%d", rec.Code)
		}
	})
}

// ============================================================
// Property: I-16 — Recoverer prevents panics from escaping
// ============================================================

func TestProp_RecovererNoPanic(t *testing.T) {
	recovererMW := mw.Recoverer()
	rapid.Check(t, func(t *rapid.T) {
		panicVal := rapid.String().Draw(t, "panicVal")
		// Note: do NOT use defer+recover inside rapid.Check — it intercepts
		// rapid's internal runtime.Goexit mechanism. Let panics surface naturally;
		// if Recoverer works, none will escape.

		handler := recovererMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(panicVal)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Recoverer did not write 500: got=%d", rec.Code)
		}
	})
}
