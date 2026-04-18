// CSA-003 minimal reproducer: Walk/Routes/Lookup vs Handle race.
//
// introspection.Walk and Routes atomically load trees and traverse them, but
// the node tree is mutated in place by addRoute (no COW of nodes). A walker
// observing a node concurrently with addRoute writing n.children, n.indices,
// n.handler, n.path → data race.
//
// Run:
//
//	go test -race -run=TestCSA003 -count=1 ./reports/concurrency-security-auditor/evidence/2026-04-17/CSA-003/
package csa003

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestCSA003_WalkVsHandle_Race(t *testing.T) {
	r := mm.New()
	r.GET("/seed", func(w http.ResponseWriter, _ *http.Request) {})

	var stop atomic.Bool
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		i := 0
		for !stop.Load() {
			func() {
				defer func() { _ = recover() }()
				r.GET(fmt.Sprintf("/dyn/%d/sub/%d", i%16, i), func(w http.ResponseWriter, _ *http.Request) {})
			}()
			i++
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for !stop.Load() {
			_ = r.Walk(func(method, pattern string, h http.Handler) error { return nil })
		}
	}()

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	<-done
	<-done
}
