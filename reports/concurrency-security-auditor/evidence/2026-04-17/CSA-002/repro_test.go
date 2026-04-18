// CSA-002 minimal reproducer: concurrent Use() + Handle() / GET().
//
// Handle() acquires m.mu, but Use() does NOT — m.middleware is appended in
// place. The subsequent Handle() reads m.middleware inside wrapMiddleware
// without synchronisation with the prior Use() → data race.
//
// Run:
//
//	go test -race -run=TestCSA002 -count=1 ./reports/concurrency-security-auditor/evidence/2026-04-17/CSA-002/
package csa002

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestCSA002_UseVsHandle_Race(t *testing.T) {
	r := mm.New()
	var stop atomic.Bool
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		noop := func(h http.Handler) http.Handler { return h }
		for !stop.Load() {
			r.Use(noop)
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		i := 0
		for !stop.Load() {
			func() {
				defer func() { _ = recover() }()
				r.GET(fmt.Sprintf("/p/%d", i), func(w http.ResponseWriter, _ *http.Request) {})
			}()
			i++
		}
	}()

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	<-done
	<-done
}
