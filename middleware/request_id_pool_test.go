// Regression tests for rmp task #245 (sprint 18): batching RequestID's
// crypto/rand reads into pooled, per-refill buffers instead of one
// rand.Read(16 bytes) call per request (CH-05), and the follow-up allocation
// rewrite (canonical header constant + fused context node) that removes the
// remaining context.WithValue / string-boxing / hex.EncodeToString / header
// canonicalisation allocations.
package middleware_test

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestRequestID_GeneratedFormat_PooledBuffers confirms the generated ID is
// still exactly 32 lowercase hex characters (16 random bytes) regardless of
// which offset within a pooled buffer it was drawn from.
func TestRequestID_GeneratedFormat_PooledBuffers(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	for i := 0; i < 300; i++ { // > 256 (one buffer refill) to cross a refill boundary
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(rec, req)
		id := rec.Header().Get("X-Request-ID")
		if len(id) != 32 {
			t.Fatalf("iteration %d: len(id)=%d, want 32", i, len(id))
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("iteration %d: id %q is not valid hex: %v", i, id, err)
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("iteration %d: id %q contains non-lowercase-hex char %q", i, id, c)
			}
		}
	}
}

// TestRequestID_NoDuplicates_ConcurrentGeneration generates a large number
// of IDs across many goroutines (forcing many concurrent buffer refills and
// pool Get/Put cycles) and asserts none collide — the batching must never
// cause a byte range to be reissued to two different IDs.
func TestRequestID_NoDuplicates_ConcurrentGeneration(t *testing.T) {
	const total = 200_000 // kept well under 1e6 for test wall-clock time; the
	// buffer-boundary and refill logic is exercised identically at any scale
	// (each buffer serves exactly 256 IDs before a fresh crypto/rand.Read).
	const goroutines = 64

	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	ids := make(chan string, total)
	perGoroutine := total / goroutines
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				h.ServeHTTP(rec, req)
				ids <- rec.Header().Get("X-Request-ID")
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, total)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("collected %d unique IDs, want %d", len(seen), goroutines*perGoroutine)
	}
}

// TestRequestID_ValidInboundHeaderPropagated_Pooled confirms a valid inbound
// X-Request-ID is still propagated unchanged (no random generation, no pool
// interaction) after the batching rewrite.
func TestRequestID_ValidInboundHeaderPropagated_Pooled(t *testing.T) {
	mw := middleware.RequestID()
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-supplied-id-123")
	h.ServeHTTP(rec, req)

	if got != "client-supplied-id-123" {
		t.Fatalf("context id = %q, want %q", got, "client-supplied-id-123")
	}
	if hdr := rec.Header().Get("X-Request-ID"); hdr != "client-supplied-id-123" {
		t.Fatalf("response header = %q, want %q", hdr, "client-supplied-id-123")
	}
}

// TestRequestID_InvalidInboundHeaderReplaced_Pooled confirms an invalid
// inbound X-Request-ID (containing CRLF-unsafe or otherwise disallowed
// characters) is still replaced with a freshly generated pooled ID.
func TestRequestID_InvalidInboundHeaderReplaced_Pooled(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "not valid!!")
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-ID")
	if got == "not valid!!" {
		t.Fatalf("invalid inbound id was propagated unchanged")
	}
	if len(got) != 32 {
		t.Fatalf("generated replacement id len=%d, want 32 (got %q)", len(got), got)
	}
}

// ── Allocation-reduction regression tests (follow-up to CH-05) ─────────────

// TestRequestID_AllocsPerRun_GeneratedPath pins the exact allocation count
// on the generated-id path (no inbound X-Request-ID) via
// testing.AllocsPerRun. Expected: 2 — (1) the fused *requestIDCtx node,
// which carries the context-node allocation, the hex-encoded id storage
// (buf), AND the response header's []string backing array (hdr) all in
// ONE allocation, and (2) r.WithContext's copy of *http.Request
// (unavoidable for a stdlib middleware — the spec requires the id to be
// reachable via r.Context()). This is down from 7 before this task's
// allocation rewrite (context.WithValue's *context.valueCtx node, boxing
// the id string into an `any`, hex.EncodeToString's separate string,
// header-key canonicalisation on both the inbound Get and the outbound
// Set, and the []string{id} header-value slice). A regression that
// reintroduces any of the removed allocations will fail this test
// immediately.
func TestRequestID_AllocsPerRun_GeneratedPath(t *testing.T) {
	h := middleware.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no X-Request-ID -> generated path
	rec := httptest.NewRecorder()

	got := testing.AllocsPerRun(200, func() {
		rec.Body = nil
		h.ServeHTTP(rec, req)
	})
	if got != 2 {
		t.Fatalf("allocs/op (generated path) = %v, want exactly 2", got)
	}
}

// TestRequestID_AllocsPerRun_PropagatedPath pins the same allocation budget
// for the propagated-id path (valid inbound X-Request-ID) — the fused
// context node still costs exactly one allocation even though no
// hex-encoding happens on this path, because the id string is simply copied
// (by reference) from the header's existing backing array into the node.
func TestRequestID_AllocsPerRun_PropagatedPath(t *testing.T) {
	h := middleware.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-supplied-id-123")
	rec := httptest.NewRecorder()

	got := testing.AllocsPerRun(200, func() {
		rec.Body = nil
		h.ServeHTTP(rec, req)
	})
	if got != 2 {
		t.Fatalf("allocs/op (propagated path) = %v, want exactly 2", got)
	}
}

// TestRequestID_GetRequestID_ThroughFurtherWrappedContext confirms that a
// later middleware wrapping the context (via a plain context.WithValue, as
// any downstream middleware might) does not break GetRequestID's lookup —
// the fused *requestIDCtx's Value method must be reached by walking up
// through the extra layer.
func TestRequestID_GetRequestID_ThroughFurtherWrappedContext(t *testing.T) {
	type otherKey struct{}
	var gotID string

	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a downstream middleware wrapping the context further.
		wrapped := context.WithValue(r.Context(), otherKey{}, "unrelated-value")
		wrapped = context.WithValue(wrapped, otherKey{}, "unrelated-value-2") // two layers
		gotID = middleware.GetRequestID(wrapped)
		if v, _ := wrapped.Value(otherKey{}).(string); v != "unrelated-value-2" {
			t.Errorf("outer context.WithValue layer did not shadow correctly: got %q", v)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "propagated-through-wraps")
	h.ServeHTTP(rec, req)

	if gotID != "propagated-through-wraps" {
		t.Fatalf("GetRequestID through wrapped context = %q, want %q", gotID, "propagated-through-wraps")
	}
}

// TestRequestID_GetRequestID_NoID_ReturnsEmpty confirms GetRequestID returns
// "" for a context that never passed through the RequestID middleware,
// including one that carries unrelated values.
func TestRequestID_GetRequestID_NoID_ReturnsEmpty(t *testing.T) {
	if got := middleware.GetRequestID(context.Background()); got != "" {
		t.Fatalf("GetRequestID(Background()) = %q, want \"\"", got)
	}

	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "something")
	if got := middleware.GetRequestID(ctx); got != "" {
		t.Fatalf("GetRequestID(ctx with unrelated value) = %q, want \"\"", got)
	}
}

// TestRequestID_HeaderCanonicalForm_ObservedByClient confirms the response
// header is stored (and therefore observed on the wire by any real HTTP
// client) under the canonical key "X-Request-Id", not the literal
// "X-Request-ID" spelling used in source code and documentation.
func TestRequestID_HeaderCanonicalForm_ObservedByClient(t *testing.T) {
	h := middleware.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	resp := rec.Result()
	if _, ok := resp.Header["X-Request-Id"]; !ok {
		t.Fatalf("response header map does not contain the canonical key %q; got keys %v", "X-Request-Id", keysOf(resp.Header))
	}
	//lint:ignore SA1008 intentional: proving the NON-canonical spelling is
	// absent from the map is exactly what this assertion checks.
	if _, ok := resp.Header["X-Request-ID"]; ok { //nolint:staticcheck // see lint:ignore above
		t.Fatalf("response header map unexpectedly contains the NON-canonical key %q", "X-Request-ID")
	}
	// http.Header.Get canonicalises its argument, so both spellings must
	// still resolve to the same value for any client using the standard API.
	if resp.Header.Get("X-Request-ID") == "" || resp.Header.Get("x-request-id") == "" {
		t.Fatalf("Header.Get did not find the id via a non-canonical spelling")
	}
}

func keysOf(h http.Header) []string {
	ks := make([]string, 0, len(h))
	for k := range h {
		ks = append(ks, k)
	}
	return ks
}

// TestRequestID_DownstreamHeaderMutation_DoesNotCorruptContextID is a
// regression test for the header/context aliasing hazard introduced by
// fusing the response header's []string backing array (hdr) into the same
// allocation as the context node and the id string (requestIDCtx). A
// downstream handler mutating w.Header()["X-Request-Id"][0] directly (bypassing
// the http.Header.Set API, which always allocates a fresh slice) writes into
// c.hdr[0] — a FIELD of the SAME struct that also holds c.id and c.buf.
// This test proves that mutation only ever changes what THAT slot of the
// header map points to; it must never retroactively corrupt c.id (a
// separate string field, copied by value at construction) or the id
// previously observed by GetRequestID.
func TestRequestID_DownstreamHeaderMutation_DoesNotCorruptContextID(t *testing.T) {
	mw := middleware.RequestID()
	var idBeforeMutation, idAfterMutation string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idBeforeMutation = middleware.GetRequestID(r.Context())

		// Directly index into the header map's backing slice — this is the
		// SAME backing array as requestIDCtx.hdr (c.hdr[:] was stored
		// verbatim into the map by RequestID), so this write lands on the
		// fused allocation, not a copy.
		w.Header()["X-Request-Id"][0] = "TAMPERED-BY-DOWNSTREAM-HANDLER"

		idAfterMutation = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if len(idBeforeMutation) != 32 {
		t.Fatalf("idBeforeMutation = %q, want a 32-hex-char generated id", idBeforeMutation)
	}
	if idAfterMutation != idBeforeMutation {
		t.Fatalf("GetRequestID changed from %q to %q after the handler mutated the response header slot directly — "+
			"context/header aliasing corrupted the request id", idBeforeMutation, idAfterMutation)
	}
	// The response header itself legitimately reflects the handler's
	// mutation (this is expected — the handler is entitled to overwrite its
	// own response headers); only the CONTEXT's id must remain untouched.
	if got := rec.Header().Get("X-Request-Id"); got != "TAMPERED-BY-DOWNSTREAM-HANDLER" {
		t.Fatalf("response header = %q, want the handler's direct mutation to be observed on the wire", got)
	}
}

// TestRequestID_DownstreamHeaderMutation_PropagatedPath repeats the above
// for the propagated-id path (valid inbound X-Request-ID), where c.buf is
// never written and c.id aliases the inbound header's own string — a
// different allocation from c.hdr, but worth confirming independently since
// this path skips the unsafe.String call entirely.
func TestRequestID_DownstreamHeaderMutation_PropagatedPath(t *testing.T) {
	mw := middleware.RequestID()
	var idAfterMutation string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["X-Request-Id"][0] = "TAMPERED"
		idAfterMutation = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "propagated-id-untouched")
	h.ServeHTTP(rec, req)

	if idAfterMutation != "propagated-id-untouched" {
		t.Fatalf("GetRequestID = %q after header tampering, want the original propagated id unaffected", idAfterMutation)
	}
}

// TestRequestID_ConcurrentRequests_ContextNeverCrossesRequests is a canary
// test in the spirit of the audit brief: run a large number of concurrent
// requests, each planting its OWN generated id, and confirm every single
// response's X-Request-ID header and context id are internally consistent
// AND globally unique — i.e. no two concurrently in-flight requests ever
// observe each other's requestIDCtx (whether via the pooled random-byte
// buffer or any other shared state).
func TestRequestID_ConcurrentRequests_ContextNeverCrossesRequests(t *testing.T) {
	const goroutines = 128
	const itersPerGoroutine = 2000 // 256,000 total requests

	mw := middleware.RequestID()
	var mismatches int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID := middleware.GetRequestID(r.Context())
		hdrID := w.Header().Get("X-Request-Id")
		if ctxID != hdrID || len(ctxID) != 32 {
			atomic.AddInt64(&mismatches, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	ids := make(chan string, goroutines*itersPerGoroutine)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				h.ServeHTTP(rec, req)
				ids <- rec.Header().Get("X-Request-Id")
			}
		}()
	}
	wg.Wait()
	close(ids)

	if got := atomic.LoadInt64(&mismatches); got != 0 {
		t.Fatalf("%d requests had a context/header id mismatch or malformed id — cross-request contamination", got)
	}

	seen := make(map[string]struct{}, goroutines*itersPerGoroutine)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request id %q observed across concurrent requests", id)
		}
		seen[id] = struct{}{}
	}
}
