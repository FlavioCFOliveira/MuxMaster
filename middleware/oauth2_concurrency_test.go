// Black-box concurrency regression tests for rmp task #246 (sprint 18):
// exercise OAuth2Introspect end-to-end (real HTTP dispatch, real
// singleflight coalescing, real heap-backed cache eviction) under heavy
// concurrent load, at once. Run with -race: these are designed to surface
// any interaction between the singleflight group and the heap/map rewrite
// that a purely internal, single-cache-instance test could miss (e.g. the
// cache being populated by MULTIPLE singleflight followers racing to call
// set() for the same key after the leader's call completes — see the
// MSR-2026-0071 comment in oauth2.go's set()).
package middleware_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// roundTripFunc adapts a function to http.RoundTripper, letting tests
// intercept the outbound introspection request without a real network
// round trip — needed because context VALUES never cross the wire, so
// asserting they reached the outbound request requires inspecting the
// client-side *http.Request.Context() directly, inside the Transport.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestOAuth2Introspect_ConcurrentSameToken_NoRace drives many goroutines
// through OAuth2Introspect with the EXACT SAME bearer token simultaneously.
// This is the scenario that produced the real data race fixed in this
// sprint (get() reading e.resp/e.expiry after releasing its RLock, racing
// set()'s new in-place mutation): every request after the first should be
// served from cache or coalesced via singleflight, and every one of the
// N singleflight followers independently calls cache.set() with the shared
// response once do() returns, all racing to update (not duplicate) the
// SAME cache entry.
func TestOAuth2Introspect_ConcurrentSameToken_NoRace(t *testing.T) {
	var hits int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(2 * time.Millisecond) // widen the singleflight coalescing window
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer idp.Close()

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              idp.URL,
		AllowInsecureEndpoint: true,
		MaxCacheSize:          16,
		CacheTTL:              time.Hour,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const goroutines = 300
	const waves = 20

	var wg sync.WaitGroup
	var failures int64
	for wave := 0; wave < waves; wave++ {
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer same-token-for-all")
				rec := httptest.NewRecorder()
				wrapped.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
				}
			}()
		}
		wg.Wait()
	}

	if failures != 0 {
		t.Fatalf("%d/%d requests failed for the same token", failures, goroutines*waves)
	}
}

// TestOAuth2Introspect_ConcurrentManyTokens_HeapEvictionUnderLoad drives
// concurrent requests across far more distinct tokens than MaxCacheSize —
// forcing continuous heap-backed eviction (evictOneLocked / heap.Pop) on
// every cache-full set() — interleaved with repeated re-requests of an
// already-cached token (heap.Fix) and concurrent reads, at high goroutine
// counts, to catch any heap corruption or map/cache desync under real HTTP
// dispatch (as opposed to the direct internal-API churn already covered by
// the internal test file).
func TestOAuth2Introspect_ConcurrentManyTokens_HeapEvictionUnderLoad(t *testing.T) {
	var hits int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer idp.Close()

	const maxCacheSize = 32
	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              idp.URL,
		AllowInsecureEndpoint: true,
		MaxCacheSize:          maxCacheSize,
		CacheTTL:              time.Hour,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const goroutines = 800
	const distinctTokens = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var failures int64
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				tok := fmt.Sprintf("tok-%d", (g*37+i*13)%distinctTokens)
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				rec := httptest.NewRecorder()
				wrapped.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if failures != 0 {
		t.Fatalf("%d requests failed under heap-eviction load", failures)
	}
}

// oauth2TraceIDCtxKey is a private, collision-resistant context key used
// only by TestOAuth2Introspect_LeaderDetach_ValuesPropagate_CancellationDoesNot
// to simulate a caller-supplied request-scoped value (e.g. a trace or
// correlation ID) that must survive the singleflight leader's detachment
// from its own request context.
type oauth2TraceIDCtxKey struct{}

// TestOAuth2Introspect_LeaderDetach_ValuesPropagate_CancellationDoesNot is
// the regression test for MSR-2026-0071's follow-up fix (rmp #280): the
// singleflight leader used to detach the outbound introspection call via
// context.Background(), which correctly shielded the shared call from the
// leader's own cancellation (client disconnect) but ALSO discarded every
// context VALUE the leader's request carried (trace/correlation IDs, etc.),
// so the outbound IdP request could never observe them.
//
// This test drives a genuine two-goroutine singleflight race — a "leader"
// whose request context carries a value and gets canceled mid-flight, and
// a "follower" for the SAME token that joins before the leader's call
// completes — through a custom http.RoundTripper (not httptest.NewServer:
// context values are process-local and never cross an actual HTTP wire, so
// asserting propagation requires inspecting the client-side request
// context directly). It asserts BOTH properties the fix must preserve
// together:
//  1. the value set on the leader's request context reaches the outbound
//     introspection request's context (the defect being fixed), and
//  2. the leader canceling its own request context never aborts the
//     shared upstream call (the original MSR-2026-0071 guarantee), so
//     both the leader and the follower still receive the legitimate
//     result instead of a cancellation-derived 401.
func TestOAuth2Introspect_LeaderDetach_ValuesPropagate_CancellationDoesNot(t *testing.T) {
	const traceValue = "trace-abc-123"

	started := make(chan struct{})
	release := make(chan struct{})
	var capturedTraceID atomic.Value
	var sawCancel atomic.Bool

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if v, ok := req.Context().Value(oauth2TraceIDCtxKey{}).(string); ok {
			capturedTraceID.Store(v)
		}
		close(started)
		select {
		case <-release:
		case <-req.Context().Done():
			sawCancel.Store(true)
			return nil, req.Context().Err()
		case <-time.After(5 * time.Second):
		}
		body := fmt.Sprintf(`{"active":true,"sub":"u","exp":%d}`, time.Now().Add(time.Hour).Unix())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              "https://idp.example.invalid/introspect",
		AllowInsecureEndpoint: true,
		HTTPClient:            &http.Client{Transport: transport},
		MaxCacheSize:          16,
		CacheTTL:              time.Hour,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	leaderCtx, leaderCancel := context.WithCancel(
		context.WithValue(context.Background(), oauth2TraceIDCtxKey{}, traceValue))
	leaderReq := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(leaderCtx)
	leaderReq.Header.Set("Authorization", "Bearer shared-token")

	followerReq := httptest.NewRequest(http.MethodGet, "/", nil)
	followerReq.Header.Set("Authorization", "Bearer shared-token")

	var leaderRec, followerRec *httptest.ResponseRecorder
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		leaderRec = httptest.NewRecorder()
		wrapped.ServeHTTP(leaderRec, leaderReq)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never reached the outbound RoundTrip — singleflight leadership was not acquired")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		followerRec = httptest.NewRecorder()
		wrapped.ServeHTTP(followerRec, followerReq)
	}()
	time.Sleep(20 * time.Millisecond) // let the follower join the in-flight singleflight call
	leaderCancel()                    // simulate the leader's client disconnecting
	time.Sleep(20 * time.Millisecond)
	close(release) // let the (detached) upstream call complete

	wg.Wait()

	if sawCancel.Load() {
		t.Fatal("the outbound introspection request observed context cancellation from the leader's disconnected request — MSR-2026-0071 guarantee violated")
	}
	if leaderRec.Code != http.StatusOK {
		t.Fatalf("leader got %d, want 200 despite its own request being canceled (the shared call's real result must still be delivered)", leaderRec.Code)
	}
	if followerRec.Code != http.StatusOK {
		t.Fatalf("follower got %d, want 200", followerRec.Code)
	}
	got, _ := capturedTraceID.Load().(string)
	if got != traceValue {
		t.Fatalf("outbound introspection request context lost the leader's request-scoped value: got %q, want %q", got, traceValue)
	}
}
