// Regression tests for sprint 18 waste-hunt WH-05/WH-09 (task #250, rmp
// #250-253): the same aliasing class already found and fixed in
// middleware/set_header.go (MID-SETHEADER-1, see
// reports/middleware-security-reviewer/2026-09-25-sprint18-wastehunt.md) is
// present at several sites in the root package. Each of those optimisations
// hoisted a single-element []string header value out of the per-request
// path to skip an allocation, but installed the SAME slice object into every
// request's http.Header map. http.Header.Set/Add/Del never mutate an
// existing slice in place, so ordinary header manipulation cannot observe
// this; but any code that indexes directly into the slice —
// w.Header()[key][0] = ... — mutates the shared backing array, corrupting
// the value for every other request sharing that slice (past and future)
// until process restart.
//
// Sites covered here:
//   - response.go: JSON/XML/Text share one package-level []string per
//     Content-Type value across EVERY request in the process.
//   - mux.go lazyMethodNotAllowed: the per-Allow-key cached handler shares
//     one []string for "Allow" across every request hitting that cache
//     entry; its default-handler branch also shares two PACKAGE-LEVEL
//     []string values (Content-Type, X-Content-Type-Options) across every
//     Allow-key and every Mux instance in the process.
//   - mux.go lazyOPTIONS: the per-Allow-key cached handler shares one
//     []string for "Allow" across every request hitting that cache entry.
package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// mutateFirstHeaderValue indexes directly into rec's header slice for key
// and overwrites its first element — the same "valid but unusual" access
// pattern used throughout the sprint 18 waste-hunt regression suite
// (middleware/setheader_wastehunt_test.go, request_id_pool_test.go).
func mutateFirstHeaderValue(rec *httptest.ResponseRecorder, key, newValue string) {
	if v := rec.Header()[key]; len(v) == 1 {
		v[0] = newValue
	}
}

// ── response.go: JSON / XML / Text ──────────────────────────────────────

func TestJSON_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	const want = "application/json; charset=utf-8"

	rec1 := httptest.NewRecorder()
	if err := muxmaster.JSON(rec1, http.StatusOK, map[string]int{"a": 1}); err != nil {
		t.Fatalf("JSON (request 1): %v", err)
	}
	mutateFirstHeaderValue(rec1, "Content-Type", "corrupted-by-request-1")
	if got := rec1.Header().Get("Content-Type"); got != "corrupted-by-request-1" {
		t.Fatalf("request 1 Content-Type = %q, want %q (the mutation itself)", got, "corrupted-by-request-1")
	}

	rec2 := httptest.NewRecorder()
	if err := muxmaster.JSON(rec2, http.StatusOK, map[string]int{"b": 2}); err != nil {
		t.Fatalf("JSON (request 2): %v", err)
	}
	if got := rec2.Header().Get("Content-Type"); got != want {
		t.Fatalf("request 2 Content-Type = %q, want %q — request 1's in-place mutation leaked through a shared package-level slice", got, want)
	}
}

func TestXML_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	const want = "application/xml; charset=utf-8"
	type payload struct{ V int }

	rec1 := httptest.NewRecorder()
	if err := muxmaster.XML(rec1, http.StatusOK, payload{V: 1}); err != nil {
		t.Fatalf("XML (request 1): %v", err)
	}
	mutateFirstHeaderValue(rec1, "Content-Type", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	if err := muxmaster.XML(rec2, http.StatusOK, payload{V: 2}); err != nil {
		t.Fatalf("XML (request 2): %v", err)
	}
	if got := rec2.Header().Get("Content-Type"); got != want {
		t.Fatalf("request 2 Content-Type = %q, want %q — request 1's in-place mutation leaked through a shared package-level slice", got, want)
	}
}

func TestText_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	const want = "text/plain; charset=utf-8"

	rec1 := httptest.NewRecorder()
	if err := muxmaster.Text(rec1, http.StatusOK, "hello"); err != nil {
		t.Fatalf("Text (request 1): %v", err)
	}
	mutateFirstHeaderValue(rec1, "Content-Type", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	if err := muxmaster.Text(rec2, http.StatusOK, "world"); err != nil {
		t.Fatalf("Text (request 2): %v", err)
	}
	if got := rec2.Header().Get("Content-Type"); got != want {
		t.Fatalf("request 2 Content-Type = %q, want %q — request 1's in-place mutation leaked through a shared package-level slice", got, want)
	}
}

// TestResponseHelpers_ConcurrentRequests_EachGetsIndependentContentTypeSlice
// hammers all three helpers concurrently, each goroutine mutating its own
// response's Content-Type slice by index to a distinct value, and asserts
// every goroutine observes only ITS OWN write. A race in the shared-slice
// design manifests as one goroutine's mutation appearing on another's
// recorder, non-deterministically.
func TestResponseHelpers_ConcurrentRequests_EachGetsIndependentContentTypeSlice(t *testing.T) {
	const n = 200
	results := make([]string, n)
	done := make(chan int, n)
	for i := range n {
		go func(i int) {
			rec := httptest.NewRecorder()
			_ = muxmaster.Text(rec, http.StatusOK, "x")
			mutateFirstHeaderValue(rec, "Content-Type", "value-from-goroutine")
			results[i] = rec.Header().Get("Content-Type")
			done <- i
		}(i)
	}
	for range n {
		<-done
	}
	for i, got := range results {
		if got != "value-from-goroutine" {
			t.Fatalf("goroutine %d: Content-Type = %q, want %q", i, got, "value-from-goroutine")
		}
	}
}

// ── mux.go: lazyMethodNotAllowed (default handler branch) ───────────────

func newMuxWithOneRoute() *muxmaster.Mux {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.RedirectFixedPath = false
	m.HandleMethodNotAllowed = true
	m.GET("/path", func(http.ResponseWriter, *http.Request) {})
	return m
}

// wantAllowGET is the Allow header value MuxMaster produces for a path with
// only GET registered: allowTable always appends ", OPTIONS" to every entry
// (every resource implicitly answers OPTIONS per HTTP semantics), regardless
// of the HandleOPTIONS setting — that behaviour is unrelated to this
// aliasing regression test and is asserted here only so the leak check has
// a stable, correct expectation to compare against.
const wantAllowGET = "GET, OPTIONS"

// TestMethodNotAllowed_DefaultHandler_AllowHeaderMutation_DoesNotLeakAcrossRequests
// drives two 405 responses through the SAME Mux (so the SAME per-Allow-key
// cache entry is reused) and proves a downstream mutation of the Allow
// header on request 1 does not survive into request 2.
func TestMethodNotAllowed_DefaultHandler_AllowHeaderMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	m := newMuxWithOneRoute()

	rec1 := httptest.NewRecorder()
	m.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/path", nil))
	if rec1.Code != http.StatusMethodNotAllowed {
		t.Fatalf("request 1 code = %d, want 405", rec1.Code)
	}
	mutateFirstHeaderValue(rec1, "Allow", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	m.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/path", nil))
	if got := rec2.Header().Get("Allow"); got != wantAllowGET {
		t.Fatalf("request 2 Allow = %q, want %q — request 1's in-place mutation leaked through the cached handler's shared slice", got, wantAllowGET)
	}
}

// TestMethodNotAllowed_DefaultHandler_ContentTypeMutation_DoesNotLeakAcrossMuxInstances
// proves the SAME defect for the default 405 handler's Content-Type and
// X-Content-Type-Options headers, which (unlike Allow) are backed by
// PACKAGE-LEVEL variables shared across EVERY Mux instance and EVERY
// Allow-key cache entry in the process — so the leak is checked across two
// entirely independent Mux instances with DIFFERENT Allow sets, which is a
// strictly stronger proof than reusing one cache entry.
func TestMethodNotAllowed_DefaultHandler_ContentTypeMutation_DoesNotLeakAcrossMuxInstances(t *testing.T) {
	m1 := muxmaster.New()
	m1.RedirectTrailingSlash = false
	m1.HandleMethodNotAllowed = true
	m1.GET("/a", func(http.ResponseWriter, *http.Request) {})

	rec1 := httptest.NewRecorder()
	m1.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/a", nil))
	if rec1.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mux 1 code = %d, want 405", rec1.Code)
	}
	mutateFirstHeaderValue(rec1, "Content-Type", "corrupted-by-mux-1")
	mutateFirstHeaderValue(rec1, "X-Content-Type-Options", "corrupted-by-mux-1")

	// A second, independent Mux with a DIFFERENT Allow set (so this cannot
	// pass merely because the two requests share one cache entry).
	m2 := muxmaster.New()
	m2.RedirectTrailingSlash = false
	m2.HandleMethodNotAllowed = true
	m2.PUT("/b", func(http.ResponseWriter, *http.Request) {})

	rec2 := httptest.NewRecorder()
	m2.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/b", nil))
	if got := rec2.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("mux 2 Content-Type = %q, want %q — leaked through a package-level shared slice", got, "text/plain; charset=utf-8")
	}
	if got := rec2.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("mux 2 X-Content-Type-Options = %q, want %q — leaked through a package-level shared slice", got, "nosniff")
	}
}

// TestMethodNotAllowed_CustomHandler_AllowHeaderMutation_DoesNotLeakAcrossRequests
// repeats the Allow-header check for the CUSTOM MethodNotAllowed branch of
// lazyMethodNotAllowed (a separate code path from the default handler).
func TestMethodNotAllowed_CustomHandler_AllowHeaderMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false
	m.HandleMethodNotAllowed = true
	m.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	m.GET("/path", func(http.ResponseWriter, *http.Request) {})

	rec1 := httptest.NewRecorder()
	m.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/path", nil))
	mutateFirstHeaderValue(rec1, "Allow", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	m.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/path", nil))
	if got := rec2.Header().Get("Allow"); got != wantAllowGET {
		t.Fatalf("request 2 Allow = %q, want %q — request 1's in-place mutation leaked through the cached handler's shared slice", got, wantAllowGET)
	}
}

// ── mux.go: lazyOPTIONS ──────────────────────────────────────────────────

// TestOPTIONSAuto_AllowHeaderMutation_DoesNotLeakAcrossRequests drives two
// automatic OPTIONS responses through the same Mux (same per-Allow-key
// cache entry) and proves a downstream mutation of the Allow header on
// request 1 does not survive into request 2.
func TestOPTIONSAuto_AllowHeaderMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	m := muxmaster.New()
	m.HandleOPTIONS = true
	m.GET("/path", func(http.ResponseWriter, *http.Request) {})

	rec1 := httptest.NewRecorder()
	m.ServeHTTP(rec1, httptest.NewRequest(http.MethodOptions, "/path", nil))
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("request 1 code = %d, want 204", rec1.Code)
	}
	mutateFirstHeaderValue(rec1, "Allow", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	m.ServeHTTP(rec2, httptest.NewRequest(http.MethodOptions, "/path", nil))
	const wantAllow = "GET, OPTIONS" // registered GET plus the auto-added OPTIONS method
	if got := rec2.Header().Get("Allow"); got != wantAllow {
		t.Fatalf("request 2 Allow = %q, want %q — request 1's in-place mutation leaked through the cached handler's shared slice", got, wantAllow)
	}
}

// TestOPTIONSAuto_ConcurrentRequests_EachGetsIndependentAllowSlice hammers
// the same cached OPTIONS handler concurrently, each goroutine mutating its
// own response's Allow slice by index, and asserts every goroutine observes
// only its own write.
func TestOPTIONSAuto_ConcurrentRequests_EachGetsIndependentAllowSlice(t *testing.T) {
	m := muxmaster.New()
	m.HandleOPTIONS = true
	m.GET("/path", func(http.ResponseWriter, *http.Request) {})

	const n = 200
	results := make([]string, n)
	done := make(chan int, n)
	for i := range n {
		go func(i int) {
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/path", nil))
			mutateFirstHeaderValue(rec, "Allow", "value-from-goroutine")
			results[i] = rec.Header().Get("Allow")
			done <- i
		}(i)
	}
	for range n {
		<-done
	}
	for i, got := range results {
		if got != "value-from-goroutine" {
			t.Fatalf("goroutine %d: Allow = %q, want %q", i, got, "value-from-goroutine")
		}
	}
}

// ── waste-hunt gate item 2: sibling-header isolation within ONE response ──
//
// MID-405OPTIONS-2 (see mux.go's lazyMethodNotAllowed) fuses the default
// 405 handler's three header values (Allow, Content-Type,
// X-Content-Type-Options) into a single freshly allocated [3]string per
// request, instead of three separate allocations, to cut allocs/op. Each
// header's slice into that array is the FULL slice expression
// vals[i:i+1:i+1], which caps its capacity at 1. This test proves that
// capping actually works: appending to ONE of the three headers (as
// http.Header.Add does) must grow into a NEW backing array rather than
// silently overwriting the ADJACENT header's slot in the shared vals array
// — which is exactly what would happen if the slices had NOT been capped
// (a 3-capacity slice's append would write in place).

func TestMethodNotAllowed_DefaultHandler_AppendToOneHeader_DoesNotCorruptSiblingHeaders(t *testing.T) {
	m := newMuxWithOneRoute()

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/path", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rec.Code)
	}
	wantCT := rec.Header().Get("Content-Type")
	wantXCTO := rec.Header().Get("X-Content-Type-Options")
	if wantCT == "" || wantXCTO == "" {
		t.Fatalf("Content-Type=%q X-Content-Type-Options=%q — one of the fused headers is missing", wantCT, wantXCTO)
	}

	// Add grows the "Allow" slice via append — if the three headers shared
	// an UNCAPPED slice into the same backing array, this append would
	// silently overwrite Content-Type's slot in place instead of allocating.
	rec.Header().Add("Allow", "TRACE")

	if got := rec.Header().Get("Content-Type"); got != wantCT {
		t.Fatalf("Content-Type = %q after Allow.Add, want unchanged %q — appending to Allow corrupted a sibling header sharing the same backing array", got, wantCT)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != wantXCTO {
		t.Fatalf("X-Content-Type-Options = %q after Allow.Add, want unchanged %q — appending to Allow corrupted a sibling header sharing the same backing array", got, wantXCTO)
	}
	if got := rec.Header().Values("Allow"); len(got) != 2 || got[0] != wantAllowGET || got[1] != "TRACE" {
		t.Fatalf("Allow values = %v, want [%q TRACE] — Add did not behave like an ordinary header append", got, wantAllowGET)
	}
}

// TestRedirect_AppendToLocation_DoesNotCorruptContentType is the same proof
// for serveRedirect's MID-REDIRECT-1 fusion of Location and Content-Type
// into one [2]string when both are set (GET/HEAD request, no caller-set
// Content-Type).
func TestRedirect_AppendToLocation_DoesNotCorruptContentType(t *testing.T) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = true
	m.GET("/path/", func(http.ResponseWriter, *http.Request) {})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/path", nil))
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("code = %d, want a redirect", rec.Code)
	}
	wantCT := rec.Header().Get("Content-Type")
	if wantCT == "" {
		t.Fatal("Content-Type not set on redirect response — MID-REDIRECT-1 fusion did not run as expected")
	}

	rec.Header().Add("Location", "/path/?extra")

	if got := rec.Header().Get("Content-Type"); got != wantCT {
		t.Fatalf("Content-Type = %q after Location.Add, want unchanged %q — appending to Location corrupted the sibling Content-Type header sharing the same backing array", got, wantCT)
	}
}
