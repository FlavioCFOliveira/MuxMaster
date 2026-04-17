package harness

// Public-field assignment race: Mux exposes NotFound, MethodNotAllowed,
// GlobalOPTIONS, ErrorHandler, PanicHandler, and several boolean flags
// (RedirectTrailingSlash, RedirectFixedPath, HandleMethodNotAllowed,
// HandleOPTIONS, CaseInsensitive, UseRawPath, UnescapePathValues,
// RedirectCode) as plain fields.
//
// Assignment to any of them while ServeHTTP is reading them is a data race.
// Documentation currently implies these should be set before serving.
// This test confirms whether -race catches misuse. If so, either:
//   (a) enforce "do not assign after Start" via atomic.Pointer + accessor
//   (b) document the constraint prominently

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestPublicFieldAssignment_Race toggles PanicHandler while ServeHTTP reads
// it. MuxMaster's ServeHTTP checks `m.PanicHandler != nil` as its branch
// selector (mux.go:407). This is a direct data race per the Go memory model.
func TestPublicFieldAssignment_PanicHandler_Race(t *testing.T) {
	r := mm.New()
	r.GET("/x", func(w http.ResponseWriter, _ *http.Request) {})

	var stop atomic.Bool
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		toggle := false
		for !stop.Load() {
			if toggle {
				r.PanicHandler = func(http.ResponseWriter, *http.Request, any) {}
			} else {
				r.PanicHandler = nil
			}
			toggle = !toggle
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for !stop.Load() {
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		}
	}()

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	<-done
	<-done
}

// TestPublicFieldAssignment_NotFound_Race toggles NotFound during dispatch.
func TestPublicFieldAssignment_NotFound_Race(t *testing.T) {
	r := mm.New()
	// Seed one route; all others return NotFound path.
	r.GET("/seed", func(w http.ResponseWriter, _ *http.Request) {})

	var stop atomic.Bool
	done := make(chan struct{}, 2)

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	go func() {
		defer func() { done <- struct{}{} }()
		toggle := false
		for !stop.Load() {
			if toggle {
				r.NotFound = h
			} else {
				r.NotFound = nil
			}
			toggle = !toggle
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for !stop.Load() {
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))
		}
	}()

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	<-done
	<-done
}

// TestPublicFieldAssignment_BoolFlag_Race toggles RedirectTrailingSlash.
// Go memory model: a racy read of a bool is a data race regardless of value.
func TestPublicFieldAssignment_BoolFlag_Race(t *testing.T) {
	r := mm.New()
	r.GET("/a/", func(w http.ResponseWriter, _ *http.Request) {})

	var stop atomic.Bool
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		toggle := false
		for !stop.Load() {
			r.RedirectTrailingSlash = toggle
			toggle = !toggle
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		i := 0
		for !stop.Load() {
			// Path without trailing slash — tree lookup will yield tsr=true.
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/a?%d", i), nil)
			r.ServeHTTP(httptest.NewRecorder(), req)
			i++
		}
	}()

	time.Sleep(150 * time.Millisecond)
	stop.Store(true)
	<-done
	<-done
}
