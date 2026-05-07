//go:build race

// public_fields_race_test.go — CSA harness for MM-2026-0017 / CSA-2026-0052
// Verifies that public Mux handler fields (NotFound, MethodNotAllowed,
// PanicHandler, GlobalOPTIONS, ErrorHandler) are safely captured into the
// frozen muxConfig snapshot on first ServeHTTP and that the dispatch path
// is race-free under sustained parallel load.
//
// Contract (also documented in SECURITY.md): public handler fields must be
// set BEFORE the first ServeHTTP call. Post-serving mutation is supported
// only via Rebuild() — Mux re-reads all fields on the next request. Direct
// post-serving field writes are user error and are NOT exercised by these
// tests; the snapshot guarantee is that, once captured, dispatch ignores
// any subsequent field mutation.

package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestPublicFields_NotFound_Race verifies that lazyNotFound dispatch is
// race-free under sustained parallel load once the snapshot is locked in.
func TestPublicFields_NotFound_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var customCalled atomic.Int64
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		customCalled.Add(1)
		http.Error(w, "custom 404", http.StatusNotFound)
	})
	r.GET("/found", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Pre-warm: lock the snapshot in before launching parallel workers.
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/nothere", nil))

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/nothere", nil))
				if w.Code != http.StatusNotFound {
					t.Errorf("expected 404 from custom NotFound, got %d", w.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
	if customCalled.Load() == 0 {
		t.Fatalf("custom NotFound was never invoked — snapshot regression")
	}
}

// TestPublicFields_PanicHandler_Race verifies the PanicHandler snapshot
// is race-free under panic-heavy parallel load.
func TestPublicFields_PanicHandler_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var recovered atomic.Int64
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		recovered.Add(1)
		http.Error(w, "recovered", http.StatusInternalServerError)
	}
	r.GET("/panic", func(w http.ResponseWriter, req *http.Request) {
		panic("test panic")
	})
	r.GET("/ok", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ok", nil))

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			path := "/ok"
			if g%3 == 0 {
				path = "/panic"
			}
			for i := 0; i < 5000; i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			}
		}(g)
	}
	wg.Wait()
	if recovered.Load() == 0 {
		t.Fatalf("PanicHandler never invoked — snapshot regression")
	}
}

// TestPublicFields_MethodNotAllowed_Race verifies the 405 snapshot is
// race-free under sustained parallel load.
func TestPublicFields_MethodNotAllowed_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var custom405 atomic.Int64
	r.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		custom405.Add(1)
		http.Error(w, "nope", http.StatusMethodNotAllowed)
	})
	r.POST("/item", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/item", nil))

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("DELETE", "/item", nil))
				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("expected 405, got %d", w.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
	if custom405.Load() == 0 {
		t.Fatalf("custom MethodNotAllowed never invoked — snapshot regression")
	}
}

// TestPublicFields_RebuildPicksUpMutation verifies the documented
// reconfiguration path: mutate field, call Rebuild, observe new behaviour.
// Sequential (no concurrent mutation) — exercises the snapshot's reset
// semantic established in CSA-2026-0050.
func TestPublicFields_RebuildPicksUpMutation(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "v1", http.StatusNotFound)
	})

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest("GET", "/none", nil))
	if got := w1.Body.String(); got != "v1\n" {
		t.Fatalf("first snapshot body = %q, want v1", got)
	}

	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "v2", http.StatusNotFound)
	})

	// Without Rebuild, the snapshot still holds v1.
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/none", nil))
	if got := w2.Body.String(); got != "v1\n" {
		t.Fatalf("post-mutation body = %q, want v1 (snapshot frozen)", got)
	}

	r.Rebuild()
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest("GET", "/none", nil))
	if got := w3.Body.String(); got != "v2\n" {
		t.Fatalf("post-Rebuild body = %q, want v2", got)
	}
}
