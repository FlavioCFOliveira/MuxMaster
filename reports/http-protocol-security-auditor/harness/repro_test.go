package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// These tests are MINIMAL REPRODUCERS for each Critical/High finding.
// Each test is self-contained and asserts the vulnerable behaviour.
// They are kept deliberately short so a maintainer can paste them into
// the main test suite once the fix is mergeable, to guard against regression.

// ReproHPS001 — HPS-001: Logger middleware prints percent-decoded r.URL.Path
// verbatim, allowing CRLF and ANSI injection into the log stream.
// CWE-117 / CWE-93 / CWE-150.
// HPS-001 FIXED (MM-2026-0006): logger sanitises CR/LF — single log line only
func TestReproHPS001_LoggerCRLFInjection(t *testing.T) {
	var buf strings.Builder
	mux := muxmaster.New()
	mux.Use(middleware.Logger(&buf))
	mux.GET("/*rest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Attacker percent-encodes CRLF into the path, stdlib decodes it into
	// r.URL.Path, then the logger prints it with %s → log injection.
	raw := fmt.Sprintf("GET /%%0D%%0A2099-01-01T00:00:00Z+EVIL+/fake+200+0s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		stripSchemeHost(srv.URL))
	_, err := rawHTTPExchange(srv, raw)
	if err != nil {
		t.Fatalf("raw exchange: %v", err)
	}

	log := buf.String()
	lineCount := strings.Count(log, "\n")
	// Fixed: logger uses strconv.QuoteToASCII — CR/LF are escaped, so only one line is written.
	if lineCount != 1 {
		t.Errorf("expected exactly 1 log line (sanitised), got %d; log=%q", lineCount, log)
	}
	// The escaped form must appear, not the raw bytes.
	if strings.ContainsAny(log, "\r\n") && strings.Count(log, "\n") > 1 {
		t.Errorf("raw CR/LF present in log output — sanitisation failed; log=%q", log)
	}
	if !strings.Contains(log, `\r\n`) {
		t.Logf("note: escaped \\r\\n not found in log (may depend on exact sanitisation representation); log=%q", log)
	}
	t.Logf("HPS-001 FIXED — log output:\n%s", log)
}

// ReproHPS002 — HPS-002: RealIP middleware writes X-Forwarded-For value into
// r.RemoteAddr without validation, allowing control bytes through.
// CWE-20 / CWE-345.
// HPS-002 FIXED (MM-2026-0008): RealIP rejects invalid IPs — RemoteAddr unchanged
func TestReproHPS002_RealIPControlBytes(t *testing.T) {
	var observed string
	mux := muxmaster.New()
	mux.Use(middleware.RealIP())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		observed = r.RemoteAddr
		w.WriteHeader(204)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:5555"
	// Attacker supplies CRLF in X-Forwarded-For. stdlib ReadRequest
	// parses header values token-by-token via textproto which rejects raw
	// CR/LF in value bytes — this ONLY applies when driving a real wire.
	// In-memory (httptest.NewRequest) the header is passed verbatim,
	// proving the middleware does not defend on its own.
	req.Header.Set("X-Forwarded-For", "1.2.3.4\r\nSet-Cookie: evil=1")
	mux.ServeHTTP(rec, req)

	// Fixed: RealIP now validates with netip.ParseAddr — the CRLF-containing value
	// is rejected, so RemoteAddr is left equal to the original peer address.
	if strings.ContainsAny(observed, "\r\n") {
		t.Errorf("RemoteAddr contains CR/LF — sanitisation failed; got %q", observed)
	}
	if observed != "1.2.3.4:5555" {
		t.Errorf("expected RemoteAddr to remain unchanged at %q, got %q", "1.2.3.4:5555", observed)
	}
	t.Logf("HPS-002 FIXED — r.RemoteAddr = %q (unchanged)", observed)
}

// ReproHPS003 — HPS-003: TSR redirect emitted before user middleware runs,
// enabling route-existence disclosure to unauthenticated clients.
// CWE-200 / CWE-285.
func TestReproHPS003_TSRRouteDisclosureBeforeAuth(t *testing.T) {
	auth := 0
	mux := muxmaster.New()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth++
			http.Error(w, "forbidden", 403)
		})
	})
	mux.GET("/admin/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil) // note: no trailing slash
	mux.ServeHTTP(rec, req)

	// FIXED (MM-2026-0004): TSR redirect now passes through middleware.
	// Auth middleware (denyAll) must run and return 403 — no route disclosure.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (auth ran on TSR), got %d", rec.Code)
	}
	if auth == 0 {
		t.Fatalf("auth middleware did not run on TSR redirect — disclosure still present")
	}
	t.Logf("HPS-003 FIXED — TSR passes through middleware (auth_calls=%d, code=%d)", auth, rec.Code)
}

// ReproHPS004 — HPS-004: auto-OPTIONS discloses registered methods before auth.
// CWE-200.
func TestReproHPS004_OPTIONSAllowLeakBeforeAuth(t *testing.T) {
	auth := 0
	mux := muxmaster.New()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth++
			http.Error(w, "forbidden", 403)
		})
	})
	mux.GET("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.POST("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.DELETE("/admin/config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/admin/config", nil)
	mux.ServeHTTP(rec, req)

	// FIXED (MM-2026-0005): auto-OPTIONS now passes through middleware.
	// Auth middleware (denyAll) must run and return 403 — no Allow header leak.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (auth ran on OPTIONS), got %d", rec.Code)
	}
	if auth == 0 {
		t.Fatalf("auth middleware did not run on auto-OPTIONS — disclosure still present")
	}
	t.Logf("HPS-004 FIXED — auto-OPTIONS passes through middleware (auth_calls=%d, code=%d)", auth, rec.Code)
}

// ReproHPS005 — HPS-005: SetHeader accepts raw CR/LF in caller-supplied
// values; while Go's serialiser replaces \r\n with spaces on the wire
// (so no response-splitting occurs), the in-memory value is retained
// verbatim, and any middleware downstream that reads the header sees the
// raw bytes. Severity: Low / defence-in-depth.
func TestReproHPS005_SetHeaderCRLFInMemory(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.SetHeader("X-Inject", "val\r\nX-Other: attacker"))
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		// Within the handler the header is visible as raw bytes.
		v := w.Header().Get("X-Inject")
		if !strings.ContainsAny(v, "\r\n") {
			t.Fatalf("expected raw CR/LF in X-Inject within handler, got %q", v)
		}
		w.WriteHeader(204)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	mux.ServeHTTP(rec, req)
	t.Logf("HPS-005 reproduced — X-Inject = %q (sanitised on wire but visible in-memory)",
		rec.Header().Get("X-Inject"))
}

// ReproHPS006 — HPS-006: RequestID middleware reflects arbitrary bytes
// from client-supplied X-Request-ID — while the wire serialiser masks
// CR/LF, other bytes (NUL, ANSI, DEL, 1MB of data) reach the response.
// This creates an *amplification* channel (client controls response size).
// CWE-20 / CWE-400.
// HPS-006 FIXED (MM-2026-0006): RequestID bounded to 128 bytes
func TestReproHPS006_RequestIDAmplification(t *testing.T) {
	mux := muxmaster.New()
	mux.Use(middleware.RequestID())
	mux.GET("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	big := strings.Repeat("A", 1<<20) // 1 MiB
	req.Header.Set("X-Request-ID", big)
	mux.ServeHTTP(rec, req)
	reply := rec.Header().Get("X-Request-Id")
	// Fixed: RequestID limits client-supplied IDs to 128 bytes; oversized or
	// invalid IDs are discarded and a fresh UUID is generated instead.
	if len(reply) > 128 {
		t.Errorf("expected reply ≤128 bytes (bounded), got %d bytes", len(reply))
	}
	t.Logf("HPS-006 FIXED — response X-Request-ID length: %d bytes (≤128)", len(reply))
}
