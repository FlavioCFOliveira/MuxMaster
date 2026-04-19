package muxmaster_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestFastHandlerParamAccess verifies that path parameters are delivered as the
// third argument and that their values are correct.
func TestFastHandlerParamAccess(t *testing.T) {
	m := muxmaster.New()
	var gotID, gotPID string
	m.GETFast("/users/:id/posts/:pid", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		gotID = ps.Get("id")
		gotPID = ps.Get("pid")
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/42/posts/7", nil)
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotID != "42" {
		t.Errorf("id: want 42, got %q", gotID)
	}
	if gotPID != "7" {
		t.Errorf("pid: want 7, got %q", gotPID)
	}
}

// TestFastHandlerStaticRoute verifies that a static fast route receives nil Params.
func TestFastHandlerStaticRoute(t *testing.T) {
	m := muxmaster.New()
	var gotParams muxmaster.Params
	m.GETFast("/health", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		gotParams = ps
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotParams != nil {
		t.Errorf("expected nil Params for static route, got %v", gotParams)
	}
}

// TestFastHandlerGoroutineSafe verifies that Params remain valid after the
// handler returns when copied to a goroutine. The implementation allocates a
// fresh slice per request, so goroutines can safely hold references.
func TestFastHandlerGoroutineSafe(t *testing.T) {
	m := muxmaster.New()
	var wg sync.WaitGroup
	results := make(chan string, 1)

	m.GETFast("/items/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		// Capture ps into a goroutine that outlives the handler.
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- ps.Get("id")
		}()
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items/99", nil)
	m.ServeHTTP(rec, req)

	wg.Wait()
	close(results)
	got := <-results
	if got != "99" {
		t.Errorf("goroutine got stale/wrong value: want 99, got %q", got)
	}
}

// TestFastHandlerOriginalRequestUnmodified verifies that the original *http.Request
// is not modified by fast dispatch (no ctx mutation, URL intact).
func TestFastHandlerOriginalRequestUnmodified(t *testing.T) {
	m := muxmaster.New()
	m.GETFast("/ping/:name", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
	})

	orig := httptest.NewRequest(http.MethodGet, "/ping/world", nil)
	origCtx := orig.Context()
	origURL := orig.URL.String()

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, orig)

	if orig.Context() != origCtx {
		t.Error("original request context was modified")
	}
	if orig.URL.String() != origURL {
		t.Errorf("original request URL was modified: want %s, got %s", origURL, orig.URL.String())
	}
}

// TestFastHandlerAndHandlerCoexist verifies that Handle and HandleFast routes
// can coexist on the same Mux without interfering with each other.
func TestFastHandlerAndHandlerCoexist(t *testing.T) {
	m := muxmaster.New()

	m.GET("/normal/:id", func(w http.ResponseWriter, r *http.Request) {
		id := muxmaster.PathParam(r, "id")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("normal:" + id)) //nolint:errcheck
	})
	m.GETFast("/fast/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fast:" + ps.Get("id"))) //nolint:errcheck
	})

	// Normal route must use context-based params.
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/normal/abc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("normal route: want 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "normal:abc" {
		t.Errorf("normal route: want normal:abc, got %s", got)
	}

	// Fast route must deliver params as third argument.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fast/xyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fast route: want 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "fast:xyz" {
		t.Errorf("fast route: want fast:xyz, got %s", got)
	}
}

// TestFastHandlerFastMiddleware verifies that UseFast applies middleware to
// HandleFast routes in the correct order (outermost first).
func TestFastHandlerFastMiddleware(t *testing.T) {
	m := muxmaster.New()
	var order []string

	mw1 := func(next muxmaster.FastHandler) muxmaster.FastHandler {
		return func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
			order = append(order, "mw1-before")
			next(w, r, ps)
			order = append(order, "mw1-after")
		}
	}
	mw2 := func(next muxmaster.FastHandler) muxmaster.FastHandler {
		return func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
			order = append(order, "mw2-before")
			next(w, r, ps)
			order = append(order, "mw2-after")
		}
	}

	m.UseFast(mw1, mw2)
	m.GETFast("/mw-test", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mw-test", nil))

	want := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(want) {
		t.Fatalf("middleware call order: want %v, got %v", want, order)
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("order[%d]: want %q, got %q", i, v, order[i])
		}
	}
}

// TestFastHandlerPanicSafe verifies that a panic in a FastHandler is recovered
// by the Mux's PanicHandler.
func TestFastHandlerPanicSafe(t *testing.T) {
	m := muxmaster.New()
	var recovered any
	m.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		recovered = rcv
		w.WriteHeader(http.StatusInternalServerError)
	}
	m.GETFast("/panic", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		panic("fast-handler-panic")
	})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if recovered != "fast-handler-panic" {
		t.Errorf("expected recovered value %q, got %v", "fast-handler-panic", recovered)
	}
}
