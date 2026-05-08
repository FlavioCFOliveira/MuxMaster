//go:build race

// rebuild_race_test.go — CSA harness for MM-2026-0048
// Tests Rebuild() racing with frozenConfigSlow() via cfgOnce struct write.
//
// Expected outcome: DATA RACE between Rebuild (write to cfgOnce) and
// frozenConfigSlow (read/execute on cfgOnce).

package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestRebuild_CfgOnce_Race(t *testing.T) {
	t.Parallel()
	r := mm.New()
	r.GET("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	n := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	// Goroutines A: drive ServeHTTP, which calls config() -> frozenConfigSlow() -> cfgOnce.Do()
	for i := 0; i < n*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				req := httptest.NewRequest("GET", "/", nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}()
	}

	// Goroutine B: calls Rebuild() which writes cfgOnce = sync.Once{} (plain struct assignment)
	// concurrent with the Do() reads above.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 5000; j++ {
			r.Rebuild()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRebuild_NilCfgPtr_Race(t *testing.T) {
	t.Parallel()
	// Verify that cfg.Store(nil) in Rebuild races with cfg.Load() in config().
	// This tests the atomic path (should be safe) vs the cfgOnce struct write (race).
	r := mm.New()
	for i := range 100 {
		r.GET(fmt.Sprintf("/p%d", i), func(w http.ResponseWriter, req *http.Request) {})
	}

	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)

	// Saturate ServeHTTP from many goroutines.
	for g := 0; g < n*8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				path := fmt.Sprintf("/p%d", (g*i)%100)
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
			}
		}(g)
	}

	// Repeatedly Rebuild from another goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			r.Rebuild()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}
