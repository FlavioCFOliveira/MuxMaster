package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// recoverPanic runs fn and returns the recovered panic value formatted with
// %v, or "" when fn returns normally.
func recoverPanic(fn func()) (msg string) {
	defer func() {
		if rec := recover(); rec != nil {
			msg = fmt.Sprintf("%v", rec)
		}
	}()
	fn()
	return ""
}

// TestGroupServeFiles_RawPathUnescapeGuard is the regression test for rmp
// #302: (*Group).ServeFiles must enforce the same CDX-S8-002 / PRF-2026-0002
// registration guard as (*Mux).ServeFiles and panic with the identical
// message when the owning Mux has both UseRawPath and UnescapePathValues set.
// The Group reads the options from its Mux at ServeFiles time, so the order
// of Group creation relative to setting the options must not matter.
func TestGroupServeFiles_RawPathUnescapeGuard(t *testing.T) {
	muxMsg := recoverPanic(func() {
		m := muxmaster.New()
		m.UseRawPath = true
		m.UnescapePathValues = true
		m.ServeFiles("/s/*fp", http.Dir("."))
	})
	if muxMsg == "" {
		t.Fatal("(*Mux).ServeFiles did not panic with UseRawPath+UnescapePathValues")
	}

	t.Run("options set before Group creation", func(t *testing.T) {
		m := muxmaster.New()
		m.UseRawPath = true
		m.UnescapePathValues = true
		g := m.Group("/g")
		got := recoverPanic(func() { g.ServeFiles("/s/*fp", http.Dir(".")) })
		if got != muxMsg {
			t.Fatalf("panic message mismatch:\n got: %q\nwant: %q", got, muxMsg)
		}
	})

	t.Run("options set after Group creation", func(t *testing.T) {
		m := muxmaster.New()
		g := m.Group("/g")
		m.UseRawPath = true
		m.UnescapePathValues = true
		got := recoverPanic(func() { g.ServeFiles("/s/*fp", http.Dir(".")) })
		if got != muxMsg {
			t.Fatalf("panic message mismatch:\n got: %q\nwant: %q", got, muxMsg)
		}
	})

	t.Run("nested sub-group", func(t *testing.T) {
		m := muxmaster.New()
		m.UseRawPath = true
		m.UnescapePathValues = true
		g := m.Group("/g").Group("/sub")
		got := recoverPanic(func() { g.ServeFiles("/s/*fp", http.Dir(".")) })
		if got != muxMsg {
			t.Fatalf("panic message mismatch:\n got: %q\nwant: %q", got, muxMsg)
		}
	})

	t.Run("no route registered after refusal", func(t *testing.T) {
		m := muxmaster.New()
		m.UseRawPath = true
		m.UnescapePathValues = true
		_ = recoverPanic(func() { m.Group("/g").ServeFiles("/s/*fp", http.Dir(".")) })
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/g/s/go.mod", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d (route must not be registered)", rec.Code, http.StatusNotFound)
		}
	})

	for _, tc := range []struct {
		name               string
		useRawPath         bool
		unescapePathValues bool
	}{
		{"defaults", false, false},
		{"UseRawPath only", true, false},
		{"UnescapePathValues only", false, true},
	} {
		t.Run("no panic: "+tc.name, func(t *testing.T) {
			m := muxmaster.New()
			m.UseRawPath = tc.useRawPath
			m.UnescapePathValues = tc.unescapePathValues
			g := m.Group("/g")
			if got := recoverPanic(func() { g.ServeFiles("/s/*fp", http.Dir(".")) }); got != "" {
				t.Fatalf("unexpected panic: %s", got)
			}
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/g/s/go.mod", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /g/s/go.mod status = %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}
