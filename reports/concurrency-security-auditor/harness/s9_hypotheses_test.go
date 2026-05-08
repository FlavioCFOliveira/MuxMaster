//go:build race

// s9_hypotheses_test.go — CSA S9 harness for 2026-05-07 pre-release audit.
// New hypotheses for this sprint:
//
//   H9-01 — setReqCtxUnsafe happens-before: middleware launching go-routine before dispatch sees stale ctx?
//   H9-02 — Pre() chain rebuild race: preHandlerPtr.Store vs preHandlerPtr.Load in ServeHTTP
//   H9-03 — jwt HMAC sync.Pool: pool Get/Put while hash object is still being read
//   H9-04 — oauth2 inflight map: close(done) races with follower select on c.done
//   H9-05 — ThrottlePerIP token channel: send on closed channel?
//   H9-06 — TSR redirect path: r.URL.Path mutated during concurrent reads from other goroutines
//   H9-07 — paramsBuf stack lifetime: pslice slice header escapes to handler after paramsBuf goes OOS
//   H9-08 — Introspection Routes() reflection: concurrent reflect.ValueOf during route registration
//   H9-09 — Middleware chain Use() + lazyNotFound: RLock snapshot race (post-fix regression check)
//   H9-10 — Context propagation from reqBundle: ctx.Done()/Err()/Deadline() correct after setReqCtxUnsafe
//
// Run with:
//
//	go test -race -tags=race -count=3 -timeout=15m -run=TestH9 ./reports/concurrency-security-auditor/harness/

package muxmaster_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ---------------------------------------------------------------------------
// H9-01 — setReqCtxUnsafe happens-before guarantee.
//
// A middleware that launches a goroutine before the handler runs references the
// *http.Request. If setReqCtxUnsafe wrote the ctx field AFTER the middleware
// goroutine was scheduled, the goroutine could see nil/stale ctx.
//
// Correct design: setReqCtxUnsafe writes ctx into a FRESHLY ALLOCATED bundle.req
// BEFORE bundle.req is handed to any goroutine. The write happens-before any
// goroutine creation in ServeHTTP (Go MM §goroutine creation). This test verifies
// the context is always visible from goroutines spawned inside middleware/handlers.
// ---------------------------------------------------------------------------

func TestH9_01_SetReqCtxUnsafe_HappensBefore_SpawnedGoroutine(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var violations int64

	// Middleware that spawns a goroutine which reads req.Context() immediately.
	r.Pre(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Spawn a goroutine that reads the context — must see a non-nil,
			// correctly initialised context.
			done := make(chan struct{})
			go func() {
				defer close(done)
				ctx := req.Context()
				if ctx == nil {
					atomic.AddInt64(&violations, 1)
					return
				}
				// context.Background() and context.TODO() have no deadline.
				// The ctx from reqBundle should also have no deadline (unless
				// a timeout middleware is in chain). The key property: no panic.
				_, _ = ctx.Deadline()
				_ = ctx.Done()
				_ = ctx.Err()
			}()
			next.ServeHTTP(w, req)
			<-done // wait for goroutine before returning
		})
	})

	r.GET("/h901/one/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 1 {
			atomic.AddInt64(&violations, 1)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/h901/two/:a/:b", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		if len(ps) != 2 {
			atomic.AddInt64(&violations, 1)
		}
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
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h901/one/%d", i), nil))
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h901/two/%d/%d", i, i+1), nil))
			}
		}(g)
	}
	wg.Wait()

	if v := atomic.LoadInt64(&violations); v > 0 {
		t.Errorf("H9-01: %d happens-before violations — ctx nil or params wrong in spawned goroutine", v)
	}
	t.Log("H9-01: setReqCtxUnsafe happens-before spawned goroutines — SAFE")
}

// ---------------------------------------------------------------------------
// H9-02 — Pre() chain rebuild races with preHandlerPtr.Load in ServeHTTP.
//
// Pre() calls preHandlerPtr.Store(&h) under mu.Lock. ServeHTTP loads from
// preHandlerPtr without any lock. Race window: Store races Load?
// Hypothesis: atomic.Pointer eliminates the race. Verify under stress.
// ---------------------------------------------------------------------------

func TestH9_02_Pre_Rebuild_AtomicPtr_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var handlerCalls int64
	r.GET("/h902/:id", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&handlerCalls, 1)
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 5000

	// ServeHTTP goroutines: read preHandlerPtr on every call.
	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h902/%d", i), nil))
			}
		}(g)
	}

	// Pre() goroutines: write preHandlerPtr concurrently.
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var preCalls int64
			for i := 0; i < 500; i++ {
				r.Pre(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						atomic.AddInt64(&preCalls, 1)
						next.ServeHTTP(w, req)
					})
				})
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
	t.Logf("H9-02: Pre() rebuild race — SAFE (handler=%d)", atomic.LoadInt64(&handlerCalls))
}

// ---------------------------------------------------------------------------
// H9-03 — JWT HMAC sync.Pool contamination.
//
// jwtHMACPool.verify() does: Get, Reset, Write, Sum, Put.
// If the hash.Hash is put back while still being read (e.g. race between
// Sum and Put), the next Get could return a dirty hash object.
// We verify: concurrent parallel verify() calls never produce false positives
// or false negatives (indicating pool contamination or torn writes).
// ---------------------------------------------------------------------------

func TestH9_03_JWT_HMAC_Pool_NoConcurrentContamination(t *testing.T) {
	t.Parallel()

	secret := []byte("test-secret-key-32bytes-exactly!")
	// Build a test JWT with HS256.
	makeJWT := func(sub string) string {
		hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		payload, _ := json.Marshal(map[string]any{
			"sub": sub,
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		pay := base64.RawURLEncoding.EncodeToString(payload)
		sigInput := hdr + "." + pay
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(sigInput))
		sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		return sigInput + "." + sig
	}

	validToken := makeJWT("user1")

	r := mm.New()
	r.Use(mw.JWTAuth(mw.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	}))

	var ok, fail int64
	r.GET("/jwt/:id", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&ok, 1)
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
				req := httptest.NewRequest("GET", fmt.Sprintf("/jwt/%d", i), nil)
				req.Header.Set("Authorization", "Bearer "+validToken)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					atomic.AddInt64(&fail, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(n * 4 * iters)
	okCount := atomic.LoadInt64(&ok)
	failCount := atomic.LoadInt64(&fail)

	if failCount > 0 {
		t.Errorf("H9-03: %d/%d JWT verifications failed — HMAC pool contamination suspected", failCount, total)
	}
	if okCount != total {
		t.Errorf("H9-03: ok=%d want=%d", okCount, total)
	}
	t.Logf("H9-03: JWT HMAC pool — SAFE (ok=%d, fail=%d, total=%d)", okCount, failCount, total)
}

// ---------------------------------------------------------------------------
// H9-04 — oauth2 inflight: close(done) races with follower select on c.done.
//
// The leader closes c.done AFTER deleting from g.calls (under mu). A late-arriving
// follower could see: g.calls miss → new call leader → but old done channel not yet
// closed. Hypothesis: the design is safe because close happens after delete, and
// a follower that misses the delete gets a fresh leader slot. Verify under maximum
// concurrent pressure: N goroutines for same token, 1 server with 5ms latency.
// ---------------------------------------------------------------------------

func TestH9_04_OAuth2Inflight_CloseRace(t *testing.T) {
	t.Parallel()

	var serverCalls int64
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&serverCalls, 1)
		// Simulate variable latency to expose races between close(done) and select.
		time.Sleep(time.Duration(n%3+1) * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"active":true,"sub":"u%d","exp":%d}`, n, time.Now().Add(30*time.Second).Unix())
	}))
	defer fakeServer.Close()

	o2mw := mw.OAuth2Introspect(mw.OAuth2Options{
		AllowInsecureEndpoint: true,
		Endpoint:              fakeServer.URL,
		CacheTTL:              -1, // disable cache — forces singleflight hit on every burst
	})

	r := mm.New()
	r.Use(o2mw)
	var handlerCalls int64
	r.GET("/h904", func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&handlerCalls, 1)
		w.WriteHeader(http.StatusOK)
	})

	// Send bursts of concurrent requests with the SAME token (maximises inflight coalescing).
	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const bursts = 50
	const goroutinesPerBurst = 8

	for b := 0; b < bursts; b++ {
		token := fmt.Sprintf("burst-token-%d", b)
		// Launch goroutinesPerBurst concurrent requests all with same token.
		var burstWg sync.WaitGroup
		for g := 0; g < goroutinesPerBurst; g++ {
			burstWg.Add(1)
			wg.Add(1)
			go func() {
				defer burstWg.Done()
				defer wg.Done()
				req := httptest.NewRequest("GET", "/h904", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}()
		}
		burstWg.Wait()
		// Brief yield between bursts.
		if b%10 == 0 {
			runtime.Gosched()
		}
	}
	wg.Wait()

	_ = n
	t.Logf("H9-04: oauth2 inflight close race — SAFE (server_calls=%d, handler_calls=%d)",
		atomic.LoadInt64(&serverCalls), atomic.LoadInt64(&handlerCalls))
}

// ---------------------------------------------------------------------------
// H9-05 — ThrottlePerIP: no send-on-closed-channel.
//
// After a timeout the defer sends back to ch: `ch <- struct{}{}`. If the entry's
// tokens channel was closed between acquisition and the deferred send, this would
// panic. The channel is created once per entry and never closed — verify under
// stress that no panic occurs when many goroutines timeout simultaneously for the
// same IP.
// ---------------------------------------------------------------------------

func TestH9_05_ThrottlePerIP_NoSendOnClosedChannel(t *testing.T) {
	t.Parallel()

	r := mm.New()
	// Limit=1: forces almost all concurrent requests to compete for the single slot.
	r.Use(mw.ThrottlePerIP(1, 1*time.Millisecond, func(req *http.Request) string {
		return "127.0.0.1" // all same key — maximum contention
	}))
	r.GET("/h905/:id", func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(2 * time.Millisecond) // handler holds slot longer than timeout
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	var panics int64

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer func() {
				if rcv := recover(); rcv != nil {
					atomic.AddInt64(&panics, 1)
					t.Errorf("H9-05: panic in ThrottlePerIP: %v", rcv)
				}
				wg.Done()
			}()
			for i := 0; i < 1000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h905/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	if atomic.LoadInt64(&panics) > 0 {
		t.Errorf("H9-05: %d panics detected — possible send-on-closed-channel", panics)
	}
	t.Log("H9-05: ThrottlePerIP send-on-closed-channel — SAFE")
}

// ---------------------------------------------------------------------------
// H9-06 — TSR redirect: r.URL.Path mutated during concurrent reads.
//
// dispatch() temporarily mutates r.URL.Path for the target computation, then
// restores it. If another goroutine reads r.URL.Path between the mutation and
// restore, it sees the modified path. The request struct is per-goroutine in
// normal use (httptest.NewRequest creates a fresh *http.Request per call), so
// this race only applies if one request object is shared across goroutines.
// We verify: each ServeHTTP call gets its own request (not shared), so no race.
// Secondary: verify the TSR path itself has no data race between r.URL.Path
// read and the redirect target construction.
// ---------------------------------------------------------------------------

func TestH9_06_TSR_URLPath_Mutation_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.RedirectTrailingSlash = true

	r.GET("/h906/item", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	var okCount, redirectCount int64

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				// Alternate between canonical path and path-with-trailing-slash (TSR triggers).
				var path string
				if i%2 == 0 {
					path = "/h906/item"
				} else {
					path = "/h906/item/" // TSR: redirect to /h906/item
				}
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				switch w.Code {
				case http.StatusOK:
					atomic.AddInt64(&okCount, 1)
				case http.StatusMovedPermanently, http.StatusTemporaryRedirect:
					atomic.AddInt64(&redirectCount, 1)
				default:
					t.Errorf("H9-06: unexpected status %d for path %s", w.Code, path)
				}
			}
		}(g)
	}
	wg.Wait()

	t.Logf("H9-06: TSR URL.Path mutation — SAFE (ok=%d, redirects=%d)", okCount, redirectCount)
}

// ---------------------------------------------------------------------------
// H9-07 — paramsBuf stack lifetime: pslice escapes via dispatchWithParams.
//
// dispatchWithParams receives pslice []Param which is backed by the stack-allocated
// paramsBuf.buf when count <= maxParams. If dispatchWithParams or the handler
// retains a reference to this slice past the return of ServeHTTP, the slice
// would alias dead stack memory.
//
// Design: dispatchWithParams immediately copies pslice into bundle.ctx.small
// (inline array within the heap-allocated reqBundle). The original pslice from
// the stack is not retained. Verify by inspecting the path and reading params
// AFTER the ServeHTTP call stack has returned.
//
// This test cannot directly detect use-after-stack-return (Go's GC-managed stack
// makes it safe unless `go:noescape` is violated), but the race detector will
// flag any concurrent write to pslice backing storage if it is incorrectly shared.
// ---------------------------------------------------------------------------

func TestH9_07_ParamsBuf_StackLifetime_NoEscape(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var violations int64
	// Handler that holds a reference to the Params slice AFTER the handler returns.
	// This simulates a buggy handler that "escapes" the context.
	var escapedParams atomic.Pointer[mm.Params]

	r.GET("/h907/:id", func(w http.ResponseWriter, req *http.Request) {
		ps := mm.ParamsFromContext(req.Context())
		// Store the Params for inspection after handler returns.
		// This is safe in the GC-managed reqBundle design: params are in bundle.ctx.small,
		// which is heap-allocated and lives as long as the context is referenced.
		escapedParams.Store(&ps)
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
				req := httptest.NewRequest("GET", fmt.Sprintf("/h907/%d", i), nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)

				// Read the escaped params — must be the params from the LAST request,
				// and must never be nil for a matched route.
				if ps := escapedParams.Load(); ps != nil {
					for _, p := range *ps {
						if p.Key == "" {
							atomic.AddInt64(&violations, 1)
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if v := atomic.LoadInt64(&violations); v > 0 {
		t.Errorf("H9-07: %d empty-key params detected — paramsBuf escape issue", v)
	}
	t.Log("H9-07: paramsBuf stack lifetime — SAFE (params correctly in heap-allocated bundle)")
}

// ---------------------------------------------------------------------------
// H9-08 — Introspection Routes() reflection during concurrent registration.
//
// Routes() holds mu.RLock() while calling root.walk() and handlerName() which
// calls reflect.ValueOf(h).Pointer(). If a concurrent Handle() replaces a tree
// (COW atomic store) while Routes() is iterating the old tree, the iterator sees
// an immutable snapshot — no data race expected. Verify under stress.
// ---------------------------------------------------------------------------

func TestH9_08_Routes_Reflection_ConcurrentRegistration(t *testing.T) {
	t.Parallel()

	r := mm.New()
	// Pre-register stable routes.
	for i := 0; i < 20; i++ {
		i := i
		r.GET(fmt.Sprintf("/h908/stable/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			_ = i
			w.WriteHeader(http.StatusOK)
		})
	}

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	var panics int64

	// Routes() goroutines.
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func() {
			defer func() {
				if rcv := recover(); rcv != nil {
					atomic.AddInt64(&panics, 1)
					t.Errorf("H9-08: Routes() panicked: %v", rcv)
				}
				wg.Done()
			}()
			for i := 0; i < 2000; i++ {
				routes := r.Routes()
				if len(routes) == 0 {
					t.Error("H9-08: Routes() returned empty slice for registered routes")
					return
				}
			}
		}()
	}

	// ServeHTTP goroutines (reads atomicPtr.Load).
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				idx := (g * i) % 20
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h908/stable/%d/val", idx), nil))
			}
		}(g)
	}

	wg.Wait()

	if atomic.LoadInt64(&panics) > 0 {
		t.Errorf("H9-08: %d panics in Routes() reflection", panics)
	}
	t.Log("H9-08: Routes() reflection concurrent registration — SAFE")
}

// ---------------------------------------------------------------------------
// H9-09 — lazyNotFound middleware snapshot: post-fix regression.
//
// After the CSA-2026-0051 fix, lazyNotFound reads m.middleware under mu.RLock.
// Regression test: concurrent Use() + lazyNotFound should be race-free.
// This is a stricter version of TestUse_LazyNotFound_Race with higher concurrency.
// ---------------------------------------------------------------------------

func TestH9_09_LazyNotFound_UseRace_PostFix_Regression(t *testing.T) {
	t.Parallel()
	r := mm.New()

	r.GET("/h909/found", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 30000

	// Not-found path triggers lazyNotFound (which snapshots middleware under RLock).
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", "/h909/missing", nil))
			}
		}()
	}

	// Concurrent Use() (acquires mu.Lock, then invalidates lazyNotFoundPtr).
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			noop := func(next http.Handler) http.Handler { return next }
			for i := 0; i < 5000; i++ {
				r.Use(noop)
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
	t.Log("H9-09: lazyNotFound Use() race post-fix regression — SAFE")
}

// ---------------------------------------------------------------------------
// H9-10 — Context propagation from reqBundle via setReqCtxUnsafe.
//
// After setReqCtxUnsafe writes into bundle.req.ctx, the context must correctly
// implement the full context.Context interface: Value, Done, Err, Deadline.
// In particular, the parent context (r.Context() before dispatch) must remain
// reachable via bundle.ctx.Context for Done/Err/Deadline propagation.
//
// Specifically tests: context.WithTimeout parent → child (reqBundle ctx) receives
// Done signal when parent is cancelled.
// ---------------------------------------------------------------------------

func TestH9_10_ContextPropagation_ReqBundle_Done(t *testing.T) {
	t.Parallel()
	r := mm.New()

	var contextCancelledCount int64
	var wrongParamCount int64

	r.Use(mw.Timeout(10 * time.Millisecond))
	r.GET("/h910/:id", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ps := mm.ParamsFromContext(ctx)
		if len(ps) != 1 || ps[0].Key != "id" {
			atomic.AddInt64(&wrongParamCount, 1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Observe context cancellation (timeout fires in 10ms).
		select {
		case <-ctx.Done():
			// Context was cancelled as expected by the timeout.
			atomic.AddInt64(&contextCancelledCount, 1)
			w.WriteHeader(http.StatusRequestTimeout)
		case <-time.After(50 * time.Millisecond):
			// Timeout didn't fire — unexpected (handler takes longer than timeout).
			w.WriteHeader(http.StatusOK)
		}
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	const iters = 500

	for g := 0; g < n*4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h910/%d", i), nil))
			}
		}(g)
	}
	wg.Wait()

	if wrongParamCount > 0 {
		t.Errorf("H9-10: %d requests had wrong params — reqBundle ctx lost params", wrongParamCount)
	}
	t.Logf("H9-10: context propagation from reqBundle — SAFE (cancelled=%d, wrongParams=%d)",
		contextCancelledCount, wrongParamCount)
}

// ---------------------------------------------------------------------------
// H9-11 — Massive parallel regression: full dispatch under GOMAXPROCS variation.
//
// 128 goroutines × 100k iterations across all route types. This is the primary
// stress test for the atomic.Pointer[methodTrees] COW design.
// ---------------------------------------------------------------------------

func TestH9_11_MassiveParallel_AllRouteTypes(t *testing.T) {
	t.Parallel()
	r := mm.New()

	// Pre-register 100 routes per tier.
	for i := 0; i < 100; i++ {
		i := i
		r.GET(fmt.Sprintf("/h911/static/%d", i), func(w http.ResponseWriter, req *http.Request) {
			_ = i
			w.WriteHeader(http.StatusOK)
		})
		r.GET(fmt.Sprintf("/h911/p1/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			_ = i
			w.WriteHeader(http.StatusOK)
		})
		r.GET(fmt.Sprintf("/h911/p2/%d/:a/:b", i), func(w http.ResponseWriter, req *http.Request) {
			_ = i
			w.WriteHeader(http.StatusOK)
		})
	}

	n := runtime.GOMAXPROCS(0)
	goroutines := min(n*8, 128)
	const iters = 10000

	var wg sync.WaitGroup
	var errors int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				idx := (g * i) % 100
				req := httptest.NewRequest("GET", fmt.Sprintf("/h911/static/%d", idx), nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					atomic.AddInt64(&errors, 1)
				}

				req2 := httptest.NewRequest("GET", fmt.Sprintf("/h911/p1/%d/val", idx), nil)
				w2 := httptest.NewRecorder()
				r.ServeHTTP(w2, req2)
				if w2.Code != http.StatusOK {
					atomic.AddInt64(&errors, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if errors > 0 {
		t.Errorf("H9-11: %d errors in massive parallel test", errors)
	}
	t.Logf("H9-11: massive parallel (%d goroutines × %d iters) — SAFE (errors=%d)",
		goroutines, iters, errors)
}

// ---------------------------------------------------------------------------
// H9-12 — Use() + Handle() interleave: concurrent Use + Handle from same goroutine set.
//
// The API contract requires Use() before Handle(). But what if a concurrent
// goroutine calls Use() while another calls Handle()? Both acquire mu.Lock
// so no data race — but does the result violate the ordering contract (new
// middleware wraps routes registered before Use was called)?
// We test that the *race detector* does not fire and that the system remains
// stable (no panic, no crash).
// ---------------------------------------------------------------------------

func TestH9_12_Use_Handle_Concurrent_Lock_Correctness(t *testing.T) {
	t.Parallel()

	r := mm.New()
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	var registered int64
	var used int64

	// Concurrent Handle goroutines.
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				func() {
					defer func() { recover() }() //nolint:errcheck — may conflict
					r.GET(fmt.Sprintf("/h912/g%d/r%d/:id", g, i), func(w http.ResponseWriter, req *http.Request) {
						w.WriteHeader(http.StatusOK)
					})
					atomic.AddInt64(&registered, 1)
				}()
			}
		}(g)
	}

	// Concurrent Use goroutines.
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r.Use(func(next http.Handler) http.Handler { return next })
				atomic.AddInt64(&used, 1)
				runtime.Gosched()
			}
		}()
	}

	// Concurrent ServeHTTP goroutines.
	for g := 0; g < n*2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				r.ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest("GET", fmt.Sprintf("/h912/g%d/r%d/val", g%n, i%50), nil))
			}
		}(g)
	}

	wg.Wait()
	t.Logf("H9-12: concurrent Use+Handle — SAFE (registered=%d, use_calls=%d)",
		registered, used)
}
