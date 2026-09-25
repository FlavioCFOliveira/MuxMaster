package muxmaster_test

// ctx_propagation_test.go verifies that cancelling, timing out, or attaching
// values to a request's parent context.Context propagates through
// MuxMaster's per-request context wrapping — requestCtx1 / requestCtx2 /
// requestCtx (params.go) and their fused reqBundle1 / reqBundle2 / reqBundle
// allocations, including the sync.Pool-backed Opt O13 variants — into the
// handler's ctx.
//
// Context: this closes a coverage gap left when commit 5f804fa rewrote
// reports/concurrency-security-auditor/harness/middleware_race_test.go and
// dropped TestContextCancellationPropagation without a replacement. That
// original test asserted only that ctx.Done() fired, for a single 1-param
// route. This file extends the same property across every param tier
// (1 / 2 / 3 / overflow, i.e. > tree.go's maxParams=3), the catch-all
// wildcard, Mount, and the Opt O13 PoolRequestBundle=true fast path — and
// additionally asserts ctx.Err(), ctx.Deadline() and ctx.Value() delegation,
// not just Done() closing.
//
// Every requestCtx* type embeds context.Context directly (params.go):
// Done()/Err()/Deadline() are Go method-promotion forwards to the embedded
// parent, so correctness here is a property of that embedding, not of
// per-call logic. These tests exist to catch a regression that breaks the
// embedding — e.g. a future edit that only forwards Value(), or a pooled
// dispatch path that captures the context before the parent is fully wired.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// ctxPropagationKey is a typed, unexported context key — collision-free by
// construction (CLAUDE.md "no string context keys" convention).
type ctxPropagationKey struct{}

// ctxObservation is what a capturing handler reports about the request
// context it received, once cancellation is observed.
type ctxObservation struct {
	err         error
	value       any
	hasDeadline bool
	deadline    time.Time
	paramCount  int
}

// newCtxObserverHandler returns a handler that blocks until its context is
// cancelled (or a 2s safety timeout elapses — a bug, not the expected path),
// then reports Err()/Value()/Deadline() and the path-param count it observed.
//
// Blocking synchronously inside the handler, and never retaining r past
// return, keeps this compatible with the strict PoolRequestBundle=true
// lifetime contract documented on Mux.PoolRequestBundle (mux.go).
func newCtxObserverHandler() (h http.HandlerFunc, called <-chan struct{}, obsCh <-chan ctxObservation) {
	calledCh := make(chan struct{})
	observed := make(chan ctxObservation, 1)

	h = func(w http.ResponseWriter, r *http.Request) {
		close(calledCh)
		ctx := r.Context()
		ps := muxmaster.ParamsFromContext(ctx)

		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			observed <- ctxObservation{err: ctx.Err(), paramCount: len(ps)}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		dl, hasDL := ctx.Deadline()
		observed <- ctxObservation{
			err:         ctx.Err(),
			value:       ctx.Value(ctxPropagationKey{}),
			hasDeadline: hasDL,
			deadline:    dl,
			paramCount:  len(ps),
		}
		w.WriteHeader(http.StatusRequestTimeout)
	}
	return h, calledCh, observed
}

// runContextPropagationCase fires reqPath through serve carrying a parent
// context built as Background -> WithValue -> WithCancel -> WithDeadline,
// then cancels it and asserts every layer — Err(), Value(), Deadline(), and
// the path params delivered alongside — survived MuxMaster's wrapping.
//
// called/obsCh come from a handler already registered on serve's route
// table by the caller (registration must happen before ServeHTTP runs).
func runContextPropagationCase(t *testing.T, label string, serve http.Handler, reqPath string, wantParamCount int, called <-chan struct{}, obsCh <-chan ctxObservation) {
	t.Helper()

	const sentinel = "ctx-propagation-sentinel"
	deadline := time.Now().Add(time.Hour) // far enough out it never fires on its own

	valueCtx := context.WithValue(context.Background(), ctxPropagationKey{}, sentinel)
	cancelCtx, cancel := context.WithCancel(valueCtx)
	finalCtx, dlCancel := context.WithDeadline(cancelCtx, deadline)
	defer dlCancel()

	req := httptest.NewRequest(http.MethodGet, reqPath, nil).WithContext(finalCtx)

	done := make(chan struct{})
	go func() {
		serve.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: handler was never invoked for %s — route did not match", label, reqPath)
	}

	cancel()

	var obs ctxObservation
	select {
	case obs = <-obsCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: handler never reported an observation after cancel()", label)
	}
	<-done

	if obs.err != context.Canceled {
		t.Errorf("%s: ctx.Err() = %v, want context.Canceled", label, obs.err)
	}
	if obs.value != sentinel {
		t.Errorf("%s: ctx.Value(sentinelKey) = %v, want %q — parent Value() chain broken", label, obs.value, sentinel)
	}
	if !obs.hasDeadline {
		t.Errorf("%s: ctx.Deadline() reported ok=false, want the parent's deadline to propagate", label)
	} else if !obs.deadline.Equal(deadline) {
		t.Errorf("%s: ctx.Deadline() = %v, want %v", label, obs.deadline, deadline)
	}
	if obs.paramCount != wantParamCount {
		t.Errorf("%s: len(ParamsFromContext) = %d, want %d — reqBundle param wiring corrupted under a wrapped context", label, obs.paramCount, wantParamCount)
	}
}

// TestContextCancellationPropagation is the direct replacement for the
// removed harness test of the same name (see file header). Each subtest
// exercises a distinct dispatch path so that a regression confined to one
// tier (e.g. only the pooled 2-param path) is pinpointed by name.
func TestContextCancellationPropagation(t *testing.T) {
	t.Run("1 param", func(t *testing.T) {
		mux := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/p1/:a", h)
		runContextPropagationCase(t, "1 param", mux, "/p1/abc", 1, called, obsCh)
	})

	t.Run("2 params", func(t *testing.T) {
		mux := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/p2/:a/:b", h)
		runContextPropagationCase(t, "2 params", mux, "/p2/abc/def", 2, called, obsCh)
	})

	t.Run("3 params", func(t *testing.T) {
		mux := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/p3/:a/:b/:c", h)
		runContextPropagationCase(t, "3 params", mux, "/p3/a/b/c", 3, called, obsCh)
	})

	t.Run("overflow params (5, beyond tree.go maxParams=3)", func(t *testing.T) {
		mux := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/p5/:a/:b/:c/:d/:e", h)
		runContextPropagationCase(t, "overflow params", mux, "/p5/a/b/c/d/e", 5, called, obsCh)
	})

	t.Run("catch-all", func(t *testing.T) {
		mux := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/files/*filepath", h)
		runContextPropagationCase(t, "catch-all", mux, "/files/a/b/c.txt", 1, called, obsCh)
	})

	t.Run("mount", func(t *testing.T) {
		inner := muxmaster.New()
		h, called, obsCh := newCtxObserverHandler()
		inner.GET("/inner/:id", h)

		outer := muxmaster.New()
		outer.Mount("/api", inner)

		runContextPropagationCase(t, "mount", outer, "/api/inner/xyz", 1, called, obsCh)
	})

	t.Run("PoolRequestBundle=true, 1 param", func(t *testing.T) {
		mux := muxmaster.New()
		mux.PoolRequestBundle = true
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/pool1/:a", h)
		runContextPropagationCase(t, "pooled 1 param", mux, "/pool1/abc", 1, called, obsCh)
	})

	t.Run("PoolRequestBundle=true, 2 params", func(t *testing.T) {
		mux := muxmaster.New()
		mux.PoolRequestBundle = true
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/pool2/:a/:b", h)
		runContextPropagationCase(t, "pooled 2 params", mux, "/pool2/abc/def", 2, called, obsCh)
	})

	t.Run("PoolRequestBundle=true, 3 params", func(t *testing.T) {
		mux := muxmaster.New()
		mux.PoolRequestBundle = true
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/pool3/:a/:b/:c", h)
		runContextPropagationCase(t, "pooled 3 params", mux, "/pool3/a/b/c", 3, called, obsCh)
	})

	t.Run("PoolRequestBundle=true, overflow params", func(t *testing.T) {
		mux := muxmaster.New()
		mux.PoolRequestBundle = true
		h, called, obsCh := newCtxObserverHandler()
		mux.GET("/pool5/:a/:b/:c/:d/:e", h)
		runContextPropagationCase(t, "pooled overflow params", mux, "/pool5/a/b/c/d/e", 5, called, obsCh)
	})
}
