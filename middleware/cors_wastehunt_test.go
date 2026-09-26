// Regression tests for the sprint 18 waste-hunt scan of the middleware
// package for the MID-SETHEADER-1 aliasing class (see
// reports/middleware-security-reviewer/2026-09-25-sprint18-wastehunt.md and
// setheader_wastehunt_test.go). CORS's Opt L3 hoisted several single-element
// []string header values into the middleware closure so every request
// reusing that CORS() instance shares the SAME slice for a given header.
// http.Header.Set/Add/Del never mutate an existing slice in place, so
// ordinary header manipulation cannot observe this; but any code that
// indexes directly into the slice — w.Header()[k][0] = ... — mutates the
// shared backing array, corrupting the header for every other request
// through that CORS() instance (past and future) until process restart.
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func mutateFirstCORSHeaderValue(rec *httptest.ResponseRecorder, key, newValue string) {
	if v := rec.Header()[key]; len(v) == 1 {
		v[0] = newValue
	}
}

func nopHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// TestCORS_AllowOriginWildcard_HeaderSliceMutation_DoesNotLeakAcrossRequests
// covers the allowAllVal ("*") slice.
func TestCORS_AllowOriginWildcard_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})
	h := mw(http.HandlerFunc(nopHandler))

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Origin", "https://a.example")
	h.ServeHTTP(rec1, req1)
	if got := rec1.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("request 1 ACAO = %q, want %q", got, "*")
	}
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Allow-Origin", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "https://b.example")
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("request 2 ACAO = %q, want %q — request 1's in-place mutation leaked through a shared slice", got, "*")
	}
}

// TestCORS_Vary_HeaderSliceMutation_DoesNotLeakAcrossRequests covers the
// varyOriginVal ("Origin") slice, installed on the per-origin (non-wildcard) branch.
func TestCORS_Vary_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://a.example", "https://b.example"}})
	h := mw(http.HandlerFunc(nopHandler))

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Origin", "https://a.example")
	h.ServeHTTP(rec1, req1)
	if got := rec1.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("request 1 Vary = %q, want %q", got, "Origin")
	}
	mutateFirstCORSHeaderValue(rec1, "Vary", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "https://b.example")
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("request 2 Vary = %q, want %q — request 1's in-place mutation leaked through a shared slice", got, "Origin")
	}
}

// TestCORS_Credentials_HeaderSliceMutation_DoesNotLeakAcrossRequests covers
// the credTrueVal ("true") slice.
func TestCORS_Credentials_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://a.example"},
		AllowCredentials: true,
	})
	h := mw(http.HandlerFunc(nopHandler))

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", "https://a.example")
		return r
	}

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req())
	if got := rec1.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("request 1 ACAC = %q, want %q", got, "true")
	}
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Allow-Credentials", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req())
	if got := rec2.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("request 2 ACAC = %q, want %q — request 1's in-place mutation leaked through a shared slice", got, "true")
	}
}

// TestCORS_PreflightHeaders_HeaderSliceMutation_DoesNotLeakAcrossRequests
// covers methodsVal, headersVal and maxAgeVal on the OPTIONS preflight branch.
func TestCORS_PreflightHeaders_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://a.example"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"X-Custom"},
		MaxAge:         600,
	})
	h := mw(http.HandlerFunc(nopHandler))

	preflight := func() *http.Request {
		r := httptest.NewRequest(http.MethodOptions, "/", nil)
		r.Header.Set("Origin", "https://a.example")
		return r
	}

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, preflight())
	if got := rec1.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("request 1 ACAM = %q", got)
	}
	if got := rec1.Header().Get("Access-Control-Allow-Headers"); got != "X-Custom" {
		t.Fatalf("request 1 ACAH = %q", got)
	}
	if got := rec1.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("request 1 ACMA = %q", got)
	}
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Allow-Methods", "corrupted")
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Allow-Headers", "corrupted")
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Max-Age", "corrupted")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, preflight())
	if got := rec2.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("request 2 ACAM = %q, want %q — leaked through a shared slice", got, "GET, POST")
	}
	if got := rec2.Header().Get("Access-Control-Allow-Headers"); got != "X-Custom" {
		t.Fatalf("request 2 ACAH = %q, want %q — leaked through a shared slice", got, "X-Custom")
	}
	if got := rec2.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("request 2 ACMA = %q, want %q — leaked through a shared slice", got, "600")
	}
}

// TestCORS_ExposeHeaders_HeaderSliceMutation_DoesNotLeakAcrossRequests
// covers exposeVal.
func TestCORS_ExposeHeaders_HeaderSliceMutation_DoesNotLeakAcrossRequests(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://a.example"},
		ExposedHeaders: []string{"X-Total-Count"},
	})
	h := mw(http.HandlerFunc(nopHandler))

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", "https://a.example")
		return r
	}

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req())
	if got := rec1.Header().Get("Access-Control-Expose-Headers"); got != "X-Total-Count" {
		t.Fatalf("request 1 ACEH = %q", got)
	}
	mutateFirstCORSHeaderValue(rec1, "Access-Control-Expose-Headers", "corrupted-by-request-1")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req())
	if got := rec2.Header().Get("Access-Control-Expose-Headers"); got != "X-Total-Count" {
		t.Fatalf("request 2 ACEH = %q, want %q — request 1's in-place mutation leaked through a shared slice", got, "X-Total-Count")
	}
}

// TestCORS_ConcurrentRequests_EachGetsIndependentAllowOriginSlice hammers a
// single wildcard CORS() instance concurrently, each goroutine mutating its
// own response's ACAO slice by index, and asserts every goroutine observes
// only its own write.
func TestCORS_ConcurrentRequests_EachGetsIndependentAllowOriginSlice(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"*"}})
	h := mw(http.HandlerFunc(nopHandler))

	const n = 200
	results := make([]string, n)
	done := make(chan int, n)
	for i := range n {
		go func(i int) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Origin", "https://example.com")
			h.ServeHTTP(rec, req)
			mutateFirstCORSHeaderValue(rec, "Access-Control-Allow-Origin", "value-from-goroutine")
			results[i] = rec.Header().Get("Access-Control-Allow-Origin")
			done <- i
		}(i)
	}
	for range n {
		<-done
	}
	for i, got := range results {
		if got != "value-from-goroutine" {
			t.Fatalf("goroutine %d: ACAO = %q, want %q", i, got, "value-from-goroutine")
		}
	}
}
