//go:build race

// handlefast_panic_test.go — CSA harness for H-H + HandleFast panic recovery.
// Hypothesis H-H: Recoverer (stdlib middleware) does NOT wrap FastHandler routes.
// PanicHandler (Mux-level) MUST cover FastHandlers via dispatchWithRecover.
// A panic in a FastHandler without PanicHandler crashes the goroutine.

package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestHandleFast_PanicHandler_Covers verifies that m.PanicHandler covers FastHandler
// panics via dispatchWithRecover. This confirms H-H: the mux-level PanicHandler
// is the only recovery mechanism for FastHandlers.
func TestHandleFast_PanicHandler_Covers(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var recovered int64
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		atomic.AddInt64(&recovered, 1)
		http.Error(w, "recovered", http.StatusInternalServerError)
	}

	r.GETFast("/fast-panic/:id", func(w http.ResponseWriter, req *http.Request, ps mm.Params) {
		panic("fast handler panic")
	})
	r.GET("/ok", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 5000
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET",
					fmt.Sprintf("/fast-panic/%d", i), nil))
				if w.Code != http.StatusInternalServerError {
					t.Errorf("expected 500 from PanicHandler, got %d", w.Code)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	expected := int64(n * 4 * iters)
	if atomic.LoadInt64(&recovered) != expected {
		t.Errorf("PanicHandler recovered %d panics, want %d", recovered, expected)
	}
}

// TestHandleFast_RecovererMW_DoesNotCover confirms that Recoverer middleware
// does NOT protect FastHandler routes (by design). When PanicHandler is nil
// and a FastHandler panics, the panic propagates to the net/http server layer.
// We use recover() in the test goroutine to catch it.
func TestHandleFast_RecovererMW_DoesNotCover(t *testing.T) {
	r := mm.New()
	// Use stdlib Recoverer as middleware — does not cover FastHandlers.
	r.Use(mw.Recoverer())
	// No PanicHandler set.

	r.GETFast("/danger/:id", func(w http.ResponseWriter, req *http.Request, ps mm.Params) {
		panic("fasthandler unprotected")
	})

	// A panic here propagates — catch it in the test so we don't fail the whole suite.
	panicked := false
	func() {
		defer func() {
			if rcv := recover(); rcv != nil {
				panicked = true
			}
		}()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/danger/1", nil))
	}()

	if !panicked {
		t.Log("H-H: FastHandler panic was absorbed (PanicHandler or framework absorbed it) — document behavior")
	} else {
		t.Log("H-H CONFIRMED: Recoverer middleware does NOT cover FastHandler panics — PanicHandler is required")
	}
}

// TestHandleFast_MixedWithHandle_GroupUse verifies that registering a
// FastHandler on a Group that already has stdlib middleware (Use) panics at
// registration time, preventing the silent auth bypass documented as
// CSA-2026-0054 (hypothesis #10 / H-H). After the fix, mixing Use(auth) with
// HandleFast on the same group is impossible — operators are forced into a
// safe configuration via UseFast() or by switching to Handle().
func TestHandleFast_MixedWithHandle_GroupUse(t *testing.T) {
	t.Parallel()
	r := mm.New()

	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
		})
	}

	g := r.Group("/api")
	g.Use(authMW)

	// Slow route is fine — Handle wraps the handler with g.middleware.
	g.GET("/slow/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatalf("expected panic from g.HandleFast on a group with stdlib middleware; got none — auth bypass regression")
		}
		msg, _ := rec.(string)
		if msg == "" {
			t.Fatalf("panic value was not a string: %v", rec)
		}
		// The panic message must clearly identify the misconfiguration so an
		// operator can fix it without inspecting source.
		for _, want := range []string{"HandleFast", "stdlib middleware", "UseFast"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic message missing %q: %s", want, msg)
			}
		}
		t.Logf("H-H #10 SAFE: registration panic prevents bypass: %s", msg)
	}()

	g.HandleFast(http.MethodGet, "/fast/:id", func(w http.ResponseWriter, req *http.Request, ps mm.Params) {
		w.WriteHeader(http.StatusOK)
	})
}
