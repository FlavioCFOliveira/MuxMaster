package dosharness

import (
	"net/http"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestRegexCompileNoExponentialBlowup validates H-019:
// The Go regexp package uses RE2 which is linear in both match and compile.
// We attempt several classic "ReDoS" patterns and assert that compilation
// (via addRoute) does NOT blow up.
//
// RE2 bounds the state count via the MaxCap / MaxInstances limits.
func TestRegexCompileNoExponentialBlowup(t *testing.T) {
	// Classic catastrophic backtracking patterns — would kill a PCRE engine.
	// RE2 should handle them in linear time.
	cases := []struct {
		name    string
		expr    string
		timeout time.Duration
	}{
		{"nested_star", "(a*)*", 1 * time.Second},
		{"nested_alternation", "(a|a|a|a)+", 1 * time.Second},
		{"nested_optional", "(a?){20}a{20}", 1 * time.Second},
		{"large_alternation_10k", "a" + strings.Repeat("|a", 10000), 10 * time.Second},
		{"long_literal", strings.Repeat("a", 5000), 1 * time.Second},
	}

	dummy := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			var panicMsg string
			start := time.Now()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						panicMsg = shortStack(r)
					}
					close(done)
				}()
				m := mm.New()
				m.GET("/x/{id:"+tc.expr+"}", dummy)
			}()
			select {
			case <-done:
				elapsed := time.Since(start)
				if panicMsg != "" {
					t.Logf("pattern %q rejected at compile after %v: %s", tc.name, elapsed, panicMsg)
				} else {
					t.Logf("pattern %q compiled successfully in %v (RE2 bounded)", tc.name, elapsed)
				}
			case <-time.After(tc.timeout):
				t.Errorf("pattern %q did NOT compile within %v — possible blowup", tc.name, tc.timeout)
			}
		})
	}
}

func shortStack(r any) string {
	s := string(debug.Stack())
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

// TestRegexMatchNoBlowup validates that MATCH time (at request time) is
// bounded even when attacker controls the path (which RE2 matches against).
func TestRegexMatchNoBlowup(t *testing.T) {
	r := mm.New()
	// A simple regex route.
	r.GET("/x/{id:(a|ab)+}", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	// An attacker-controlled long matching candidate.
	path := "/x/" + strings.Repeat("ab", 5000)

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// We need to ServeHTTP on a request; the test focuses on time-to-return.
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		w := dummyRW{}
		r.ServeHTTP(&w, req)
	}()

	select {
	case <-done:
		t.Logf("long-match request returned in %v (RE2 linear)", time.Since(start))
	case <-time.After(2 * time.Second):
		t.Errorf("long-match took > 2s — possible quadratic/exponential behaviour")
	}
}

type dummyRW struct {
	hdr    http.Header
	status int
}

func (d *dummyRW) Header() http.Header {
	if d.hdr == nil {
		d.hdr = http.Header{}
	}
	return d.hdr
}
func (d *dummyRW) Write(p []byte) (int, error) { return len(p), nil }
func (d *dummyRW) WriteHeader(status int)      { d.status = status }
