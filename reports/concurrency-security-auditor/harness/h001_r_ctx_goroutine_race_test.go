//go:build race

// h001_r_ctx_goroutine_race_test.go — CSA harness for CSA-001 regression
// Verifies that setReqCtxUnsafe is called only on freshly-allocated reqBundle.req
// and NEVER on the original *http.Request that ServeHTTP received.
//
// Regression guard for the vendored CSA-001 finding (setReqCtx on original r).

package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestSetReqCtxUnsafe_OnlyFreshBundle verifies that the original *http.Request
// passed into ServeHTTP is never mutated by setReqCtxUnsafe. We do this by
// capturing the context pointer from the original r before ServeHTTP and
// asserting it is unchanged after — if the unsafe write touches the original,
// the pointer will differ.
func TestSetReqCtxUnsafe_OnlyFreshBundle(t *testing.T) {
	t.Parallel()
	r := mm.New()
	var violations int64

	r.GET("/users/:id", func(w http.ResponseWriter, req *http.Request) {
		// Handler sees the bundle's request — just respond OK.
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/users/:id/posts/:postID", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.GET("/a/:x/:y/:z", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			paths := []string{
				fmt.Sprintf("/users/%d", g),
				fmt.Sprintf("/users/%d/posts/%d", g, g+1),
				fmt.Sprintf("/a/%d/%d/%d", g, g+1, g+2),
			}
			for i := 0; i < 20000; i++ {
				path := paths[i%len(paths)]
				orig := httptest.NewRequest("GET", path, nil)
				ctxBefore := orig.Context()

				w := httptest.NewRecorder()
				r.ServeHTTP(w, orig)

				// The original request's context must not have been mutated.
				if orig.Context() != ctxBefore {
					atomic.AddInt64(&violations, 1)
				}
			}
		}(g)
	}

	wg.Wait()
	if v := atomic.LoadInt64(&violations); v > 0 {
		t.Errorf("CSA-001 regression: original *http.Request ctx was mutated in %d cases", v)
	}
}

// TestSetReqCtxUnsafe_MassiveParallel is a high-concurrency race-detector stress
// for all dispatch paths (1-param, 2-param, 3-param, static).
func TestSetReqCtxUnsafe_MassiveParallel(t *testing.T) {
	t.Parallel()
	r := mm.New()

	for i := range 200 {
		i := i
		_ = i
		r.GET(fmt.Sprintf("/static/%d", i), func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.GET(fmt.Sprintf("/p1/%d/:id", i), func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.GET(fmt.Sprintf("/p2/%d/:id/:sub", i), func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.GET(fmt.Sprintf("/p3/%d/:a/:b/:c", i), func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	for g := 0; g < n*16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				idx := (g * i) % 200
				paths := []string{
					fmt.Sprintf("/static/%d", idx),
					fmt.Sprintf("/p1/%d/val", idx),
					fmt.Sprintf("/p2/%d/val/sub", idx),
					fmt.Sprintf("/p3/%d/a/b/c", idx),
				}
				path := paths[i%len(paths)]
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}
	wg.Wait()
}
