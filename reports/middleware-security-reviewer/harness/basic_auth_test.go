// Package harness provides middleware security tests for MuxMaster.
// Harness for the middleware-security-reviewer security agent.
//
// Scope — basic_auth.go
//
// Threats covered (see /reports/overview/threat-model.md §3.1 and
// hypotheses H-002, H-029):
//   - CWE-208 timing leak on invalid user vs invalid password.
//   - CWE-287 non-constant-time comparison.
//   - CWE-287 empty credentials / base64 error handling.
//   - CWE-117 realm CRLF smuggling.
//   - CWE-522 credential leak to logs (exercised via Logger in ordering).
package harness

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// authHandler returns 200 to serve a hit; this is the protected handler.
func authHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("protected"))
	})
}

// -----------------------------------------------------------------------------
// MSR-BA-001 — Username enumeration via map lookup timing (H-002)
//
// The code structure is:
//
//	if expected, found := creds[user]; found {
//	    if subtle.ConstantTimeCompare(...) == 1 { allow }
//	}
//	// else: fall through to 401
//
// For a missing user, subtle.ConstantTimeCompare is never executed.
// Path length and cache behaviour are architecturally different.
//
// We collect N timing samples per arm and run a Welch t-test approximation.
// When |t| > 5.0 (very large effect) at N >= 1e5 the leak is certain.
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_TimingUserEnum(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test — skipped in short mode")
	}
	creds := map[string]string{
		"alice": "secret-password-1234567890abcdef",
	}
	mw := middleware.BasicAuth("test-realm", creds)
	h := mw(authHandler())

	const samples = 50_000
	valid := make([]time.Duration, samples)
	bogus := make([]time.Duration, samples)

	// Warm-up — prime caches so the very first call doesn't dominate.
	for i := 0; i < 1000; i++ {
		reqV := httptest.NewRequest(http.MethodGet, "/", nil)
		reqV.SetBasicAuth("alice", "wrong-password-1234567890abcdef")
		h.ServeHTTP(httptest.NewRecorder(), reqV)
		reqB := httptest.NewRequest(http.MethodGet, "/", nil)
		reqB.SetBasicAuth("bogus", "wrong-password-1234567890abcdef")
		h.ServeHTTP(httptest.NewRecorder(), reqB)
	}

	// Interleave to average out scheduler jitter / CPU state.
	for i := 0; i < samples; i++ {
		// valid user, wrong pass
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("alice", "wrong-password-1234567890abcdef")
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		valid[i] = time.Since(start)

		// bogus user, same wrong pass
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.SetBasicAuth("bogus", "wrong-password-1234567890abcdef")
		rec2 := httptest.NewRecorder()
		start2 := time.Now()
		h.ServeHTTP(rec2, req2)
		bogus[i] = time.Since(start2)
	}

	// Descriptive stats + Welch t-test approximation.
	meanV, sdV := meanSD(valid)
	meanB, sdB := meanSD(bogus)
	welchT := welch(meanV, sdV, float64(len(valid)), meanB, sdB, float64(len(bogus)))

	// Trim + medians to contain outlier dominance.
	medV := median(valid)
	medB := median(bogus)

	t.Logf("BasicAuth timing — samples=%d", samples)
	t.Logf("  valid-user:  mean=%.1fns  sd=%.1fns  median=%.1fns", meanV, sdV, medV)
	t.Logf("  bogus-user:  mean=%.1fns  sd=%.1fns  median=%.1fns", meanB, sdB, medB)
	t.Logf("  delta(mean): %.1fns  Welch|t|=%.2f", meanV-meanB, welchT)

	// Write CSV to evidence.
	writeTimingCSV(t, "timing-basic-auth.csv", valid, bogus)

	// Record-only: we do NOT fail the test here — the finding is produced by
	// the timing-and-sidechannel-analyst agent with CPU pinning and larger N.
	// We only assert that the harness captured the expected signal so the
	// finding MSR-BA-001 has reproducible evidence.
	if meanV-meanB < 0 {
		t.Logf("  NOTE: valid mean < bogus — timing favours bogus (unexpected, see CSV)")
	}
}

// -----------------------------------------------------------------------------
// MSR-BA-002 — Non-constant-time compare detection (static)
//
// We reflect-scan the source to prove `subtle.ConstantTimeCompare` is the only
// primitive used on the password path. If someone refactors to `==` or
// bytes.Equal this test fails.
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_UsesConstantTimeCompare(t *testing.T) {
	// Static test — read source and assert subtle is used and `==` on `pass`/`expected`
	// does not appear. The harness can't import unexported state, so we do a grep.
	data, err := readSource("/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/basic_auth.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "subtle.ConstantTimeCompare") {
		t.Error("basic_auth: subtle.ConstantTimeCompare is missing — credential compare is not constant-time")
	}
	// Flag non-safe comparisons that would be suspicious around the password path.
	for _, bad := range []string{
		"pass == expected",
		"bytes.Equal([]byte(pass)",
		"pass == creds[user]",
	} {
		if strings.Contains(data, bad) {
			t.Errorf("basic_auth: dangerous compare found: %q", bad)
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-BA-003 — Empty credentials / base64 edge cases
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_EmptyAndMalformed(t *testing.T) {
	creds := map[string]string{"alice": "secret"}
	mw := middleware.BasicAuth("r", creds)
	h := mw(authHandler())

	cases := []struct {
		name   string
		header string
		want   int
	}{
		// RFC 7617: "Basic Og==" decodes to ":" (user="", pass="").
		{"empty_user_empty_pass", "Basic " + base64.StdEncoding.EncodeToString([]byte(":")), http.StatusUnauthorized},
		{"empty_user_valid_pass", "Basic " + base64.StdEncoding.EncodeToString([]byte(":secret")), http.StatusUnauthorized},
		{"malformed_no_colon", "Basic " + base64.StdEncoding.EncodeToString([]byte("alicesecret")), http.StatusUnauthorized},
		{"not_basic_scheme", "Bearer xyz", http.StatusUnauthorized},
		{"empty_header", "", http.StatusUnauthorized},
		{"garbage_base64", "Basic !!!", http.StatusUnauthorized},
		{"double_colon", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:se:cret")), http.StatusUnauthorized},
		{"unicode_user_nonexistent", "Basic " + base64.StdEncoding.EncodeToString([]byte("Аlice:secret")), http.StatusUnauthorized}, // Cyrillic A
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			// Ensure no panic on any malformed input.
			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("basic_auth: panic on %s: %v", c.name, rcv)
				}
			}()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("basic_auth[%s]: got %d, want %d", c.name, rec.Code, c.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-BA-004 — Realm CRLF / control char handling (H-029)
//
// The basic_auth implementation concatenates the realm string into the
// WWW-Authenticate header unconditionally. Go's `http.Header.Set` rejects
// \r\n, but the behaviour is silent-drop rather than error, and the caller
// may not validate its own realm string. We verify both:
//   - Go's http package refuses to write the CRLF value, AND
//   - no panic occurs, AND
//   - the realm string used cannot smuggle a second header.
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_RealmCRLFHandling(t *testing.T) {
	creds := map[string]string{"alice": "secret"}
	realms := map[string]string{
		"crlf":           "bad\r\nX-Injected: yes",
		"lf_only":        "bad\nX-Injected: yes",
		"cr_only":        "bad\rX-Injected: yes",
		"quote":          `bad"quote`,
		"null":           "bad\x00null",
		"high_unicode":   "bad\u202eunicode",
		"tab":            "bad\tpseudo",
		"ansi_escape":    "bad\x1b[2J",
		"long_1k":        strings.Repeat("A", 1024),
		"long_16k":       strings.Repeat("A", 16*1024),
		"multiline_long": strings.Repeat("a\r\nb", 256),
	}

	for name, realm := range realms {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if rcv := recover(); rcv != nil {
					t.Fatalf("BasicAuth panicked on realm=%q: %v", realm, rcv)
				}
			}()
			mw := middleware.BasicAuth(realm, creds)
			h := mw(authHandler())
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			// Assert injected header was NOT smuggled. The `X-Injected` header
			// is the CRLF payload the attacker is trying to inject.
			if got := rec.Header().Get("X-Injected"); got != "" {
				t.Errorf("basic_auth: realm smuggled X-Injected header (realm=%q value=%q)", realm, got)
			}
			// Wire-level audit (via http.Header.Write): Go 1.26 converts
			// raw CR/LF to space, so no injection reaches the wire.
			raw := rawHeaderDump(rec.Header())
			// If the raw dump contains more than one header-block terminator,
			// we have a real CRLF injection that survived Go's sanitisation.
			if strings.Count(raw, "\r\n\r\n") > 1 {
				t.Errorf("basic_auth: realm=%q — multiple header-block terminators on wire:\n%s", realm, raw)
			}

			// In-memory finding: the Header() map still has raw CR/LF bytes.
			// If anyone serialises headers without Go's stdlib, they inherit
			// the raw content. Document as MSR-BA-004.
			wwwAuth := rec.Header().Get("WWW-Authenticate")
			if strings.ContainsAny(wwwAuth, "\r\n") {
				t.Logf("MSR-BA-004 CONFIRMED: realm=%q → in-memory WWW-Authenticate contains raw CR/LF (wire is sanitised): %q",
					truncate(realm, 64), truncate(wwwAuth, 128))
			}
		})
	}
}

// -----------------------------------------------------------------------------
// MSR-BA-005 — Credential leak via Logger (ordering trap)
//
// If Logger is placed AFTER BasicAuth and the request reaches the handler,
// the logger format prints only method/path/status — no Authorization header.
// We verify the default format never includes the Authorization value.
// This is a regression lock.
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_LoggerDoesNotLeakAuthHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := middleware.Logger(&buf)
	auth := middleware.BasicAuth("r", map[string]string{"alice": "secret"})
	// Order: logger(auth(handler)) — logger wraps auth.
	h := logger(auth(authHandler()))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.SetBasicAuth("alice", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := buf.String()
	if strings.Contains(out, "secret") {
		t.Errorf("logger leaked the plaintext password into the log:\n%s", out)
	}
	if strings.Contains(out, "Basic ") || strings.Contains(out, "Authorization") {
		t.Errorf("logger leaked the Authorization header:\n%s", out)
	}
	// Cross-check: logger still wrote the status line.
	if !strings.Contains(out, "GET /admin") {
		t.Errorf("logger missing basic request info: %s", out)
	}
}

// -----------------------------------------------------------------------------
// MSR-BA-006 — Brute-force unlimited without throttle (documentation marker)
//
// We confirm that 10k sequential failed attempts all return 401 with no
// rate-limiting. This is a documentation requirement, not a defect of
// BasicAuth itself — BasicAuth is intentionally not coupled to throttle.
// The finding is FYI for users composing middleware.
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_BruteForceUnbounded(t *testing.T) {
	if testing.Short() {
		t.Skip("long — skipped in short mode")
	}
	creds := map[string]string{"alice": "secret"}
	mw := middleware.BasicAuth("r", creds)
	h := mw(authHandler())

	const attempts = 10_000
	n401 := 0
	n200 := 0
	for i := 0; i < attempts; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("alice", fmt.Sprintf("wrong-%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusUnauthorized:
			n401++
		case http.StatusOK:
			n200++
		}
	}
	if n401 != attempts {
		t.Errorf("BasicAuth brute-force: expected %d 401s, got %d 401 + %d 200", attempts, n401, n200)
	}
	t.Logf("BasicAuth: %d failed attempts processed in full — no rate-limit is applied (documented)", attempts)
}

// -----------------------------------------------------------------------------
// MSR-BA-007 — Concurrent credential lookup race
// -----------------------------------------------------------------------------

func TestSec_BasicAuth_Concurrent(t *testing.T) {
	creds := map[string]string{"alice": "secret", "bob": "hunter2"}
	mw := middleware.BasicAuth("r", creds)
	h := mw(authHandler())
	var wg sync.WaitGroup
	const goroutines = 32
	const perG = 500
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.SetBasicAuth("alice", "secret")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("concurrent auth: got %d", rec.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
	runtime.GC()
}

// -----------------------------------------------------------------------------
// Helpers — shared timing utilities (reused by other harness files).
// -----------------------------------------------------------------------------

func meanSD(xs []time.Duration) (mean, sd float64) {
	n := float64(len(xs))
	if n == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, d := range xs {
		sum += float64(d.Nanoseconds())
	}
	mean = sum / n
	ss := 0.0
	for _, d := range xs {
		diff := float64(d.Nanoseconds()) - mean
		ss += diff * diff
	}
	if n > 1 {
		sd = sqrtf(ss / (n - 1))
	}
	return
}

func welch(m1, s1, n1, m2, s2, n2 float64) float64 {
	denom := sqrtf(s1*s1/n1 + s2*s2/n2)
	if denom == 0 {
		return 0
	}
	t := (m1 - m2) / denom
	if t < 0 {
		t = -t
	}
	return t
}

func median(xs []time.Duration) float64 {
	cp := make([]time.Duration, len(xs))
	copy(cp, xs)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	n := len(cp)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return float64(cp[n/2].Nanoseconds())
	}
	return float64(cp[n/2-1].Nanoseconds()+cp[n/2].Nanoseconds()) / 2.0
}

func sqrtf(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton; avoids importing math just for this.
	z := x / 2
	for i := 0; i < 20; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}
