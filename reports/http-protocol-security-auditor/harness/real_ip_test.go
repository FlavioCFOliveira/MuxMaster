package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestRealIPXFFTrust confirms the behaviour of real_ip.go — does it
// accept arbitrary bytes (including CR/LF) into r.RemoteAddr?
// The value ends up in downstream systems (throttle key, log, ACL).
func TestRealIPXFFTrust(t *testing.T) {
	observed := make(map[string]string)
	mux := muxmaster.New()
	mux.Use(middleware.RealIP())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		observed[r.Header.Get("X-Label")] = r.RemoteAddr
		w.WriteHeader(204)
	})

	cases := []struct {
		label string
		xff   string
	}{
		{"normal", "1.2.3.4"},
		{"first_of_list", "1.2.3.4, 5.6.7.8"},
		{"literal_crlf", "1.2.3.4\r\nSet-Cookie: evil=1"},
		{"literal_lf", "1.2.3.4\nmalicious"},
		{"ansi", "1.2.3.4\x1b[2J"},
		{"nul", "1.2.3.4\x00extra"},
		{"empty", ""},
		{"only_comma", ","},
		{"whitespace", "   "},
		{"obs_host", "host\texample.com"},
		{"port_injection", "127.0.0.1:99999"},
		{"ipv6", "::1"},
		{"huge", "1.2.3.4" + "\n" + "SIZE_" + "padpadpad"},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "192.0.2.10:55555" // simulate real client IP
		req.Header.Set("X-Label", c.label)
		req.Header.Set("X-Forwarded-For", c.xff)
		mux.ServeHTTP(rec, req)
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h009-real-ip-xff.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for _, c := range cases {
		ra := observed[c.label]
		fmt.Fprintf(f, "[%s] xff=%q -> r.RemoteAddr=%q has_ctl=%t\n",
			c.label, c.xff, ra, containsCtl(ra))

		if containsCtl(ra) {
			t.Logf("[%s] CONFIRMED: RemoteAddr contains control bytes: %q",
				c.label, ra)
		}
	}
}

// TestRealIPThrottleBypassScenario is a defence-in-depth confirmation of
// H-009 outcome: if a downstream middleware uses r.RemoteAddr as a rate
// limit key, XFF spoofing leaks through. We do NOT claim this is a
// finding against real_ip alone (it's documented), but the surface
// area matters for the posture assessment.
func TestRealIPThrottleBypassScenario(t *testing.T) {
	counter := make(map[string]int)
	mux := muxmaster.New()
	mux.Use(middleware.RealIP())
	// A naive rate limit keyed on r.RemoteAddr
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter[r.RemoteAddr]++
			if counter[r.RemoteAddr] > 3 {
				http.Error(w, "rate-limited", 429)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	// Attacker opens 10 connections, each with a different XFF.
	blocked := 0
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "1.2.3.4:5555"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		mux.ServeHTTP(rec, req)
		if rec.Code == 429 {
			blocked++
		}
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h009-throttle-bypass-scenario.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "attacker_attempts=10 blocked=%d counters_by_remote=%+v\n",
		blocked, counter)
	if blocked == 0 {
		t.Logf("CONFIRMED: all 10 attacker requests bypassed throttle via XFF rotation")
	}
}
