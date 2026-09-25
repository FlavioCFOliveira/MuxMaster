// Regression tests for the fixed HTTP method set (rmp task #262, sprint 19).
//
// MuxMaster recognizes a fixed, closed set of eleven method tokens — the ten
// standard methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT,
// TRACE, QUERY) plus the internal "*" token used by Mount — via an internal
// methodIdx lookup that maps each exact string to a fixed array index.
// MuxMaster provides no mechanism to register a custom or extension method
// (no RegisterMethod or equivalent). See specification/routing.md §2.1 rule
// 31, §2.3 rules 34-36, §2.4 rules 37-38, and specification/out-of-scope.md
// §2.7 for the specification this file pins against the implementation.
package muxmaster_test

import (
	"net/http"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// mustRecover runs fn and returns the value recovered from a panic, or nil
// if fn did not panic.
func mustRecover(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return
}

// registrationEntryPoint exercises one specific public registration API
// (Mux.Handle, Group.HandleFast, Mux.Match, ...) against a freshly created
// Mux (or Group thereof), for a single method string, and reports whatever
// value — if any — was recovered from a panic.
type registrationEntryPoint struct {
	name string
	call func(method string) (recovered any)
}

// methodSetEntryPoints returns one entry per public registration API that
// routes through Mux.Handle or Mux.HandleFast's method validation: the five
// *Mux entry points (Handle, HandleFunc, HandleE, HandleFast, Match) and
// their five *Group counterparts, which delegate to the identical *Mux
// methods (specification/routing.md §2.3 rule 34).
func methodSetEntryPoints() []registrationEntryPoint {
	noopHandler := handler(http.StatusOK, "ok")
	noopFast := func(http.ResponseWriter, *http.Request, muxmaster.Params) {}
	noopE := func(http.ResponseWriter, *http.Request) error { return nil }

	return []registrationEntryPoint{
		{"Mux.Handle", func(method string) any {
			m := muxmaster.New()
			return mustRecover(func() { m.Handle(method, "/x", noopHandler) })
		}},
		{"Mux.HandleFunc", func(method string) any {
			m := muxmaster.New()
			return mustRecover(func() { m.HandleFunc(method, "/x", noopHandler) })
		}},
		{"Mux.HandleE", func(method string) any {
			m := muxmaster.New()
			return mustRecover(func() { m.HandleE(method, "/x", noopE) })
		}},
		{"Mux.HandleFast", func(method string) any {
			m := muxmaster.New()
			return mustRecover(func() { m.HandleFast(method, "/x", noopFast) })
		}},
		{"Mux.Match", func(method string) any {
			m := muxmaster.New()
			return mustRecover(func() { m.Match([]string{method}, "/x", noopHandler) })
		}},
		{"Group.Handle", func(method string) any {
			g := muxmaster.New().Group("/api")
			return mustRecover(func() { g.Handle(method, "/x", noopHandler) })
		}},
		{"Group.HandleFunc", func(method string) any {
			g := muxmaster.New().Group("/api")
			return mustRecover(func() { g.HandleFunc(method, "/x", noopHandler) })
		}},
		{"Group.HandleE", func(method string) any {
			g := muxmaster.New().Group("/api")
			return mustRecover(func() { g.HandleE(method, "/x", noopE) })
		}},
		{"Group.HandleFast", func(method string) any {
			g := muxmaster.New().Group("/api")
			return mustRecover(func() { g.HandleFast(method, "/x", noopFast) })
		}},
		{"Group.Match", func(method string) any {
			g := muxmaster.New().Group("/api")
			return mustRecover(func() { g.Match([]string{method}, "/x", noopHandler) })
		}},
	}
}

// ── 1. Unsupported methods panic with the exact message ──────────────────────

// TestMethodSet_UnsupportedMethod_Panics pins specification/routing.md §2.3
// rule 34: a method string that is not one of the eleven tokens methodIdx
// recognizes panics with "muxmaster: unsupported HTTP method '<method>'"
// across every public registration entry point. Covers a WebDAV-style custom
// method (PROPFIND), an informal caching-proxy method (PURGE), and
// case-sensitivity (rule 38): a lowercase standard method ("get") and a
// mixed-case standard method ("Query") are each distinct, unrecognized
// strings to methodIdx.
func TestMethodSet_UnsupportedMethod_Panics(t *testing.T) {
	t.Parallel()
	unsupported := []string{"PURGE", "PROPFIND", "get", "Query"}

	for _, method := range unsupported {
		for _, ep := range methodSetEntryPoints() {
			t.Run(ep.name+"/"+method, func(t *testing.T) {
				t.Parallel()
				want := "muxmaster: unsupported HTTP method '" + method + "'"

				got := ep.call(method)
				if got == nil {
					t.Fatalf("expected panic %q, got no panic", want)
				}
				msg, ok := got.(string)
				if !ok {
					t.Fatalf("expected panic value to be a string, got %T: %v", got, got)
				}
				if msg != want {
					t.Fatalf("panic message = %q, want %q", msg, want)
				}
			})
		}
	}
}

// ── 2. Empty method panics with the exact message ─────────────────────────────

// TestMethodSet_EmptyMethod_Panics pins specification/routing.md §2.4 rule
// 37: an empty method string panics with "muxmaster: HTTP method must not be
// empty" — a distinct message from the "unsupported HTTP method" panic,
// checked before the method string is looked up in the recognized set —
// across every public registration entry point.
func TestMethodSet_EmptyMethod_Panics(t *testing.T) {
	t.Parallel()
	const want = "muxmaster: HTTP method must not be empty"

	for _, ep := range methodSetEntryPoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()

			got := ep.call("")
			if got == nil {
				t.Fatalf("expected panic %q, got no panic", want)
			}
			msg, ok := got.(string)
			if !ok {
				t.Fatalf("expected panic value to be a string, got %T: %v", got, got)
			}
			if msg != want {
				t.Fatalf("panic message = %q, want %q", msg, want)
			}
		})
	}
}

// ── 3. A failed registration panic leaves the mux usable ──────────────────────

// TestMethodSet_UnsupportedMethodPanic_LeavesMuxUsable is a narrow
// complement to TestRegistrationRollback_PanicMidInsert_LiveTreeUntouched
// (tree_rollback_test.go), which covers a panic from deep inside
// addRoute/insertChild after the two-phase copy-on-write has already copied
// and started mutating nodes. Method validation panics BEFORE that
// copy-on-write begins (mux.go: the method switch runs before m.mu.Lock()),
// so no tree node is ever touched — this test pins that a rejected
// registration is a true no-op: routes registered before the panic keep
// serving, and the mux accepts further valid registrations afterward.
func TestMethodSet_UnsupportedMethodPanic_LeavesMuxUsable(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.GET("/a", handler(http.StatusOK, "a"))

	func() {
		defer func() { recover() }() //nolint:errcheck
		m.Handle("PURGE", "/b", handler(http.StatusOK, "b"))
	}()

	if rec := do(m, http.MethodGet, "/a"); rec.Code != http.StatusOK || rec.Body.String() != "a" {
		t.Fatalf("route registered before the panic was corrupted: code=%d body=%q", rec.Code, rec.Body.String())
	}

	m.GET("/c", handler(http.StatusOK, "c"))
	if rec := do(m, http.MethodGet, "/c"); rec.Code != http.StatusOK || rec.Body.String() != "c" {
		t.Fatalf("mux rejected a valid registration after the panic: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// ── 4. Every supported method registers without panic ─────────────────────────

// TestMethodSet_SupportedMethods_RegisterWithoutPanic pins
// specification/routing.md §2.1 rule 31: all eleven recognized tokens —
// the ten standard methods plus the internal "*" token Mount relies on —
// register successfully via Handle. Passing "*" directly to Handle is
// explicitly documented as accepted (not a panic), even though ANY (not a
// literal "*" method string) is the supported way to register a handler for
// every standard method.
func TestMethodSet_SupportedMethods_RegisterWithoutPanic(t *testing.T) {
	t.Parallel()
	supported := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace, muxmaster.MethodQuery, "*",
	}

	for _, method := range supported {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			m := muxmaster.New()
			if got := mustRecover(func() {
				m.Handle(method, "/supported", handler(http.StatusOK, "ok"))
			}); got != nil {
				t.Fatalf("unexpected panic registering supported method %q: %v", method, got)
			}
		})
	}
}

// ── 5. Dispatch behavior for unrecognized request methods ─────────────────────

// TestMethodSet_Dispatch_UnrecognizedMethod_405WhenOtherMethodRegistered
// pins specification/routing.md §2.4 rule 38: at dispatch time, methodIdx
// finds no match for a request whose Method is an unrecognized string
// (PURGE) or a lowercase variant of a recognized one ("get"), exactly as it
// finds none for a recognized-but-unregistered method — the router falls
// through to the path's method-mismatch handling and returns 405 with the
// Allow header listing the methods actually registered at that path, when
// HandleMethodNotAllowed is true (the default).
func TestMethodSet_Dispatch_UnrecognizedMethod_405WhenOtherMethodRegistered(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"PURGE", "get"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			m := muxmaster.New() // HandleMethodNotAllowed defaults to true.
			m.GET("/only-get", handler(http.StatusOK, "ok"))

			rec := do(m, method, "/only-get")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
			if got := rec.Header().Get("Allow"); got != "GET, OPTIONS" {
				t.Fatalf("Allow = %q, want %q", got, "GET, OPTIONS")
			}
		})
	}
}

// TestMethodSet_Dispatch_UnrecognizedMethod_404WhenMethodNotAllowedDisabled
// pins the other half of routing.md §2.4 rule 38 / §4.7: with
// HandleMethodNotAllowed set to false, the same unrecognized-method request
// against a path registered for a different method returns 404 instead of
// 405.
func TestMethodSet_Dispatch_UnrecognizedMethod_404WhenMethodNotAllowedDisabled(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"PURGE", "get"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			m := muxmaster.New()
			m.HandleMethodNotAllowed = false
			m.GET("/only-get", handler(http.StatusOK, "ok"))

			rec := do(m, method, "/only-get")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("code = %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
}

// TestMethodSet_Dispatch_UnrecognizedMethod_UnregisteredPath_404 pins that
// an unrecognized method against a path with no registered handler for any
// method returns a plain 404, not a 405 (there is nothing to list in an
// Allow header).
func TestMethodSet_Dispatch_UnrecognizedMethod_UnregisteredPath_404(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.GET("/only-get", handler(http.StatusOK, "ok"))

	rec := do(m, "PURGE", "/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Allow"); got != "" {
		t.Fatalf("Allow = %q, want empty (no method registered at this path)", got)
	}
}

// TestMethodSet_Dispatch_StarWildcardMatchesAnyRequestMethod documents a
// verified, non-obvious dispatch fact: mux.go's dispatch function checks the
// idxWild tree (trees[idxWild], the internal "*" tree Mount and a direct
// Handle("*", ...) registration both populate) UNCONDITIONALLY whenever the
// primary per-method lookup did not produce a match — it does not gate that
// fallback on whether the request's method was recognized by methodIdx.
// Concretely: methodIdx("PURGE") returns -1, so the primary-tree lookup in
// dispatch is skipped entirely (root stays nil) and the request falls
// straight through to the trees[idxWild] check, where it matches like any
// other request. A path registered via Handle("*", pattern, handler) THEREFORE
// matches a request carrying ANY method string, including one methodIdx does
// not recognize — this is not limited to Mount's own catch-all routes.
// allowed() (used for the 405/OPTIONS Allow header) explicitly skips
// idxWild, so a "*" registration never appears in an Allow header and never
// affects 405 behavior for other methods at overlapping paths.
func TestMethodSet_Dispatch_StarWildcardMatchesAnyRequestMethod(t *testing.T) {
	t.Parallel()
	m := muxmaster.New()
	m.Handle("*", "/star", handler(http.StatusOK, "star"))

	for _, method := range []string{"PURGE", "get", http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			rec := do(m, method, "/star")
			if rec.Code != http.StatusOK || rec.Body.String() != "star" {
				t.Fatalf("method %q did not match the \"*\" route: code=%d body=%q", method, rec.Code, rec.Body.String())
			}
		})
	}
}

// Note: do (mux_test.go) builds requests with httptest.NewRequest. The
// lowercase/mixed-case/custom method strings this file exercises ("get",
// "Query", "PURGE", "PROPFIND") are all valid HTTP tokens per RFC 9110, so
// they pass net/http's request construction unchanged — every 404/405
// observed above comes from MuxMaster's method recognition, not from
// httptest rejecting the method string.
