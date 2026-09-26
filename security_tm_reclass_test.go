package muxmaster_test

// Sprint 20, rmp #286 — composite (Mux + middleware) assertions that settle
// S9 threat-model hypotheses TM-2026-010 and TM-2026-030. Hypothesis text:
// reports/overview/2026-05-07-sprint-S9.md.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TM-2026-010 — "clean_path + UseRawPath ordering: clean(Path), dispatch(RawPath)".
//
// Model: Mux.UseRawPath=true; Pre(CleanPath, gate) where gate is a path-based
// authorisation check on r.URL.Path (the value CleanPath normalises) that
// denies everything under /admin. A bypass would be any request that the
// gate lets through but that the router dispatches (on RawPath) to /admin.
//
// Invariant enforced by CleanPath (clean_path.go:41-57): after it runs,
// RawPath is empty or percent-decodes to the cleaned Path, so the gate and
// the dispatcher always see the same decoded path.
func TestSec_TM_2026_010_CleanPath_UseRawPath_PreGate_NoBypass(t *testing.T) {
	targets := []string{
		"/admin", "/admin/", "/./admin", "//admin", "/x/../admin",
		"/%61dmin", "/%61%64%6d%69%6e", "/a%2fdmin", "/admin%2f",
		"/public/..%2fadmin", "/public/%2e%2e/admin", "/public/%2E%2E/admin",
		"/public/%2e%2e%2fadmin", "/public/..%252fadmin", "/public/%252e%252e/admin",
		"/public/.%2e/admin", "/public/%2e./admin", "/x/%2e%2e/%2e%2e/admin",
		"/admin/%2e", "/admin/%2e%2e/admin", "/public/..%5cadmin", "/ADMIN",
		"/public/%2e%2e%2f%2e%2e%2fadmin", "/%2fadmin", "/%2e/admin",
		// Admin-prefixed raw paths whose DECODED form cleans out of /admin:
		// the exact shape of the S9 hypothesis (gate sees the cleaned Path,
		// router would dispatch the uncleaned RawPath into /admin/*rest).
		"/admin/%2e%2e/x", "/admin/%2e%2e/public/f", "/admin/sub/%2e%2e/%2e%2e/x",
		"/admin/%2E%2E/x", "/admin/.%2e/x",
	}

	// Negative control: a cleaner that normalises Path but leaves RawPath
	// untouched (the pre-MSR-2026-0061 shape) MUST be caught by this harness.
	naiveClean := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := *r
			u := *r.URL
			u.Path = path.Clean(u.Path)
			r2.URL = &u
			next.ServeHTTP(w, &r2)
		})
	}
	if n := tm010Bypasses(t, naiveClean, targets, false); n == 0 {
		t.Fatal("negative control: naive cleaner produced no bypass — harness cannot detect the TM-2026-010 class")
	}
	if n := tm010Bypasses(t, middleware.CleanPath(), targets, true); n != 0 {
		t.Fatalf("CleanPath: %d bypass(es)", n)
	}
}

// tm010Bypasses builds the UseRawPath mux with Pre(cleaner, gate) and returns
// how many targets reach an /admin route; report=true logs each as an error.
func tm010Bypasses(t *testing.T, cleaner func(http.Handler) http.Handler, targets []string, report bool) int {
	t.Helper()
	m := mm.New()
	m.UseRawPath = true
	m.RedirectTrailingSlash = false

	var gateSawPath, gateSawRaw string
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gateSawPath, gateSawRaw = r.URL.Path, r.URL.RawPath
			if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	m.Pre(cleaner, gate)

	var reached string
	m.GET("/admin", func(w http.ResponseWriter, _ *http.Request) { reached = "admin" })
	m.GET("/admin/*rest", func(w http.ResponseWriter, _ *http.Request) { reached = "admin-subtree" })
	m.GET("/public/*f", func(w http.ResponseWriter, _ *http.Request) { reached = "public" })
	m.GET("/:seg", func(w http.ResponseWriter, _ *http.Request) { reached = "seg" })

	bypasses := 0
	for _, target := range targets {
		reached, gateSawPath, gateSawRaw = "", "", ""
		req := httptest.NewRequest(http.MethodGet, "http://x"+target, nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)

		if strings.HasPrefix(reached, "admin") {
			bypasses++
			if !report {
				continue
			}
			t.Errorf("%s: BYPASS — gate saw Path=%q RawPath=%q, router dispatched to %s (status %d)",
				target, gateSawPath, gateSawRaw, reached, rec.Code)
		}
		if report && gateSawRaw != "" {
			dec, err := url.PathUnescape(gateSawRaw)
			if err != nil || dec != gateSawPath {
				t.Errorf("%s: invariant broken — RawPath %q decodes to %q (err %v), Path %q",
					target, gateSawRaw, dec, err, gateSawPath)
			}
		}
	}
	return bypasses
}

// TM-2026-030 — "set_header overwrites Server header -> fingerprint inconsistency".
//
// net/http emits no Server header and MuxMaster adds none, so SetHeader("Server")
// only adds one. Registered mux-wide (Use or Pre) it is present on every
// router-generated response class: matched route, 404, 405, trailing-slash
// redirect, automatic OPTIONS. Per-scope differences arise only from scoped
// registration (Group.Use, or Use after some routes), which is the documented
// registration-time model.
func TestSec_TM_2026_030_SetHeaderServer_UniformAcrossResponseClasses(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/a/"},     // 200
		{http.MethodGet, "/nope"},   // 404
		{http.MethodPost, "/a/"},    // 405
		{http.MethodHead, "/a/"},    // 405 (no implicit HEAD)
		{http.MethodGet, "/a"},      // 301 trailing-slash redirect
		{http.MethodOptions, "/a/"}, // automatic OPTIONS
	}
	for _, mode := range []string{"Use", "Pre"} {
		m := mm.New()
		if mode == "Use" {
			m.Use(middleware.SetHeader("Server", "edge-1"))
		} else {
			m.Pre(middleware.SetHeader("Server", "edge-1"))
		}
		m.GET("/a/", func(w http.ResponseWriter, _ *http.Request) {})

		seen := map[int]bool{}
		for _, c := range cases {
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(c.method, "http://x"+c.path, nil))
			seen[rec.Code] = true
			if got := rec.Header().Values("Server"); len(got) != 1 || got[0] != "edge-1" {
				t.Errorf("%s: %s %s -> %d: Server=%v, want [edge-1]", mode, c.method, c.path, rec.Code, got)
			}
		}
		for _, code := range []int{200, 404, 405, 301, 204} {
			if !seen[code] {
				t.Errorf("%s: response class %d not exercised (seen %v)", mode, code, seen)
			}
		}
	}
}
