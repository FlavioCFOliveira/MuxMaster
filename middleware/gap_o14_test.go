// Regression tests for rmp #273 (sprint 20), O-14 middleware scope
// (reports/overview/findings.md B.4). Commit 5f804fa deleted the
// reports/middleware-security-reviewer/harness/*_test.go files that used to
// cover these properties; none of them had survived, under any name, in the
// current test suite. Each test below pins one property from that gap list
// against the CURRENT specification (specification/middleware-stdlib.md)
// and GoDoc, not against the deleted tests' assumptions.
package middleware_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Compress: stale Content-Length dropped after compression (spec §34) ────

func TestSec_Compress_DropsStaleContentLength(t *testing.T) {
	mw := middleware.Compress(gzip.DefaultCompression)
	body := bytes.Repeat([]byte("a"), 2048) // > 1 KB threshold, so compression is chosen.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler sets Content-Length for the UNCOMPRESSED body, exactly as
		// a naive handler unaware of Compress would. This must not survive.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	mw(inner).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want empty: the stale pre-compression length must be dropped "+
			"(spec §34, middleware/compress.go commit()'s hdr.Del(\"Content-Length\"))", got)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if !bytes.Equal(decompressed, body) {
		t.Fatalf("decompressed body mismatch: got %d bytes, want %d", len(decompressed), len(body))
	}
}

// ── CORS: wildcard at a non-first index + credentials must still panic ─────

func TestSec_CORS_WildcardNonFirstIndexAndCredentialsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic: AllowCredentials=true with \"*\" at a non-first index of " +
				"AllowedOrigins must be rejected exactly like the first-index case (spec §49)")
		}
	}()
	_ = middleware.CORS(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://trusted.example", "*"},
		AllowCredentials: true,
	})
}

// ── CORS: a request with no Origin header gets no Access-Control-Allow-Origin ──
//
// TM-2026-033 (spec rules 71-72, rmp #291): a request with no Origin header
// still gets no Access-Control-* headers (rule 72, unchanged behaviour) but
// now DOES get Vary: Origin (rule 71) — so a cache is aware this response
// varies by Origin before it stores it, closing the CDN cache-poisoning gap
// where a stored no-Origin response was later reused for a CORS request.
func TestSec_CORS_NoOriginHeader_NoACAO(t *testing.T) {
	mw := middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://trusted.example"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil) // deliberately no Origin header
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a same-origin / no-Origin request must reach the handler)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty when the request carries no Origin header", got)
	}
	found := false
	for _, v := range rec.Header().Values("Vary") {
		if strings.EqualFold(strings.TrimSpace(v), "Origin") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Vary = %v, want a value containing the token \"Origin\" (TM-2026-033, spec rule 71) even "+
			"though the request carries no Origin header", rec.Header().Values("Vary"))
	}
}

// ── Logger: an output-writer error does not panic and does not alter the response ──

type gapO14ErroringWriter struct{}

func (gapO14ErroringWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestSec_Logger_WriterErrorDoesNotPanicOrAlterResponse(t *testing.T) {
	mw := middleware.Logger(gapO14ErroringWriter{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Logger panicked when the underlying writer returned an error: %v", r)
		}
	}()
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a log-writer failure must not alter the response", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want %q: a log-writer failure must not alter the response body", rec.Body.String(), "ok")
	}
}

// ── Logger: concurrent requests produce intact (non-interleaved) log lines ──

// gapO14LineCapturingWriter records each Write call's bytes verbatim as one
// slice entry. The mutex only protects the test harness's own bookkeeping —
// per specification/middleware-stdlib.md §12, Logger itself performs no
// synchronization and issues exactly one Write call per request containing
// the complete line, so line-atomicity holds as long as each Write call is
// captured whole, which is what this type guarantees.
type gapO14LineCapturingWriter struct {
	mu    sync.Mutex
	lines [][]byte
}

func (w *gapO14LineCapturingWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	w.mu.Lock()
	w.lines = append(w.lines, cp)
	w.mu.Unlock()
	return len(p), nil
}

func TestSec_Logger_ConcurrentRequestsProduceAtomicLines(t *testing.T) {
	lw := &gapO14LineCapturingWriter{}
	mw := middleware.Logger(lw)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	const n = 300
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/req-%d", i), nil)
			h.ServeHTTP(rec, req)
		}(i)
	}
	wg.Wait()

	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.lines) != n {
		t.Fatalf("got %d captured Write calls, want %d: each request must produce exactly one Write call", len(lw.lines), n)
	}
	seen := make(map[string]bool, n)
	for _, line := range lw.lines {
		s := string(line)
		if strings.Count(s, "\n") != 1 || !strings.HasSuffix(s, "\n") {
			t.Fatalf("log line is not a single well-formed line (possible interleaving/corruption): %q", s)
		}
		fields := strings.Fields(s)
		if len(fields) < 4 || fields[1] != http.MethodGet {
			t.Fatalf("malformed log line (possible interleaving/corruption): %q", s)
		}
		if seen[s] {
			t.Fatalf("duplicate log line observed (possible corruption): %q", s)
		}
		seen[s] = true
	}
}

// ── Logger placed after (outer of) an auth middleware records the 401 ──────

func TestSec_Logger_OuterOfAuth_RecordsFailureStatus(t *testing.T) {
	var buf bytes.Buffer
	logMW := middleware.Logger(&buf)
	authMW := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// mux.Use(Logger, BasicAuth) composition: Logger outermost, BasicAuth
	// innermost — request → Logger → BasicAuth → handler.
	chained := logMW(authMW(inner))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/secure", nil) // no credentials supplied
	chained.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, " /secure 401 ") {
		t.Fatalf("Logger did not record the auth failure's 401 status: %q", logged)
	}
}

// ── NoCache: the handler can still override Cache-Control ──────────────────

func TestSec_NoCache_HandlerCanOverrideCacheControl(t *testing.T) {
	mw := middleware.NoCache()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NoCache sets headers before calling next (middleware/no_cache.go); the
		// wrapped handler runs afterwards and, exactly like a bare
		// http.ResponseWriter, can still overwrite them before WriteHeader
		// commits — the same override contract SetHeader documents explicitly
		// (spec §67: "The next handler may overwrite or delete this header").
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q, want the handler's override %q", got, "public, max-age=3600")
	}
}

// ── Recoverer: survives a panic value whose String()/Error() itself panics ──

type gapO14PanickingStringer struct{}

func (gapO14PanickingStringer) String() string { panic("evil Stringer panic") }

type gapO14PanickingErrorer struct{}

func (gapO14PanickingErrorer) Error() string { panic("evil Errorer panic") }

func TestSec_Recoverer_SurvivesPanicValueWithPanickingStringerOrError(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"panicking Stringer", gapO14PanickingStringer{}},
		{"panicking error", gapO14PanickingErrorer{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			mw := middleware.RecovererWithLogger(logger)
			panicValue := c.value
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic(panicValue)
			})

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Recoverer did not survive a panic value whose String()/Error() itself "+
							"panics (a second, uncaught panic escaped): %v", r)
					}
				}()
				rec := httptest.NewRecorder()
				mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("status = %d, want 500", rec.Code)
				}
				if strings.Contains(rec.Body.String(), "evil") {
					t.Fatalf("panic message leaked into the response body: %q", rec.Body.String())
				}
			}()
		})
	}
}

// ── Recoverer placed INSIDE does not catch a panic raised by an outer ──────
// ── middleware (MM-2026-0034 negative) ──────────────────────────────────────

func TestSec_Ordering_RecovererInsideDoesNotCatchOuterPanic(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Recoverer wraps ONLY the inner handler here — it is the innermost frame,
	// not the outermost one MM-2026-0034 requires.
	recovered := middleware.Recoverer()(inner)
	outerPanics := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("outer middleware panic — must not be caught by an inner Recoverer")
		})
	}
	chained := outerPanics(recovered)

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		chained.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if !panicked {
		t.Fatal("a Recoverer placed inside an outer, panicking middleware unexpectedly caught that " +
			"outer panic — Recoverer must be outermost to provide coverage (MM-2026-0034)")
	}
}

// ── ThrottleBacklog: invalid configuration panics ───────────────────────────

func TestSec_ThrottleBacklog_InvalidConfigPanics(t *testing.T) {
	cases := []struct {
		name           string
		limit, backlog int
	}{
		{"limit zero", 0, 1},
		{"limit negative", -1, 1},
		{"backlog negative", 5, -1},
		{"limit zero and backlog negative", 0, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("ThrottleBacklog(%d, %d, ...) expected panic (spec §55-56), got none", c.limit, c.backlog)
				}
			}()
			_ = middleware.ThrottleBacklog(c.limit, c.backlog, time.Second)
		})
	}
}

// ── Timeout: nested Timeout middlewares compose to the shortest deadline ───

func TestSec_Timeout_NestedPicksShortestDeadline(t *testing.T) {
	const short = 40 * time.Millisecond
	const long = 2 * time.Second

	t.Run("outer long, inner short", func(t *testing.T) {
		outer := middleware.Timeout(long)
		innerMW := middleware.Timeout(short)
		var deadline time.Time
		var had bool
		start := time.Now()
		h := outer(innerMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, had = r.Context().Deadline()
			w.WriteHeader(http.StatusOK)
		})))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if !had {
			t.Fatal("expected a deadline on the context")
		}
		if got := deadline.Sub(start); got > 500*time.Millisecond {
			t.Fatalf("effective deadline is %v from start, want ~%v (the shortest of the two nested Timeouts)", got, short)
		}
	})

	t.Run("outer short, inner long", func(t *testing.T) {
		outer := middleware.Timeout(short)
		innerMW := middleware.Timeout(long)
		var deadline time.Time
		var had bool
		start := time.Now()
		h := outer(innerMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, had = r.Context().Deadline()
			w.WriteHeader(http.StatusOK)
		})))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if !had {
			t.Fatal("expected a deadline on the context")
		}
		if got := deadline.Sub(start); got > 500*time.Millisecond {
			t.Fatalf("effective deadline is %v from start, want ~%v (the shortest of the two nested Timeouts, "+
				"regardless of registration order)", got, short)
		}
	})
}

// ── RealIP: X-Forwarded-For takes precedence over X-Real-IP (spec §21-24) ──

func TestSec_RealIP_XFFPrecedenceOverXRealIP(t *testing.T) {
	trusted := mustPrefixes(t, []string{"127.0.0.0/8"})
	mw := middleware.RealIP(trusted...)
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "198.51.100.7")
	mw(inner).ServeHTTP(rec, req)

	if captured != "203.0.113.9" {
		t.Fatalf("RemoteAddr = %q, want %q: X-Forwarded-For must take precedence over X-Real-IP when both "+
			"are present and the peer is trusted (spec §24)", captured, "203.0.113.9")
	}
}

func TestSec_RealIP_XRealIPUsedWhenXFFAbsent(t *testing.T) {
	trusted := mustPrefixes(t, []string{"127.0.0.0/8"})
	mw := middleware.RealIP(trusted...)
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	mw(inner).ServeHTTP(rec, req)

	if captured != "198.51.100.7" {
		t.Fatalf("RemoteAddr = %q, want %q: X-Real-IP must be used when X-Forwarded-For is absent (spec §24)",
			captured, "198.51.100.7")
	}
}

// ── SetHeader("", v): observable wire behaviour of an empty key ────────────

func TestSec_SetHeader_EmptyKeyIsSilentlyDroppedOnTheWire(t *testing.T) {
	// SetHeader's construction-time guard only rejects CR/LF in key or value
	// (spec §66); an empty key is not rejected there. net/http's own response
	// writer refuses to serialise a header whose name is not a valid HTTP
	// token — an empty string is not — so the header map entry SetHeader
	// installs never reaches the wire. This pins that observed, safe
	// behaviour end-to-end (real listener, real client), not just the
	// in-process header map httptest.ResponseRecorder would show regardless
	// of wire validity.
	mw := middleware.SetHeader("", "somevalue")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL) //nolint:noctx // test-only, no context needed
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status=%d body=%q, want 200/\"ok\": an empty SetHeader key must not break the response", resp.StatusCode, body)
	}
	if _, present := resp.Header[""]; present {
		t.Fatalf("response carried a header under the empty key %q, want it dropped by net/http's wire serialisation", "")
	}
}

// ── WithValue(k, nil): a nil value is stored and retrievable without panic ──

type gapO14WithValueNilKey struct{}

func TestSec_WithValue_NilValueStoredAndRetrievable(t *testing.T) {
	mw := middleware.WithValue(gapO14WithValueNilKey{}, nil)
	var got any
	seen := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Context().Value(gapO14WithValueNilKey{})
		seen = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !seen {
		t.Fatal("handler was never invoked: WithValue(key, nil) must not panic (consistent with context.WithValue, spec §68-70)")
	}
	if got != nil {
		t.Fatalf("context value = %v, want nil", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// ── BasicAuth: concurrent use is race-free and produces correct results ────

func TestSec_BasicAuth_ConcurrentUseIsRaceFreeAndCorrect(t *testing.T) {
	mw := middleware.BasicAuth("realm", map[string]string{
		"alice": "correct-horse-battery-staple",
		"bob":   "hunter2-hunter2",
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	const n = 500
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			switch i % 3 {
			case 0:
				req.SetBasicAuth("alice", "correct-horse-battery-staple")
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("valid alice creds: got %d, want 200", rec.Code)
				}
			case 1:
				req.SetBasicAuth("bob", "hunter2-hunter2")
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("valid bob creds: got %d, want 200", rec.Code)
				}
			default:
				req.SetBasicAuth("alice", "wrong-password")
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("invalid creds: got %d, want 401", rec.Code)
				}
			}
		}(i)
	}
	wg.Wait()
}

// The tests below were not requested by the original 10-property gap list,
// but were found while cross-referencing the 81 Test* functions 5f804fa
// deleted from reports/middleware-security-reviewer/harness/ against the
// current suite (reports/middleware-security-reviewer/harness/security_harness_test.go
// now covers the large majority under new names). These are the handful
// that had no current equivalent anywhere.

// ── ThrottleBacklog: the budget is GLOBAL, not per-client (MSR-TH-001) ─────

func TestSec_ThrottleBacklog_BudgetIsGlobalNotPerIP(t *testing.T) {
	mw := middleware.ThrottleBacklog(2, 0, 100*time.Millisecond)
	blockCh := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh
		w.WriteHeader(http.StatusOK)
	})
	h := mw(slow)

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/a%d", id), nil)
			req.RemoteAddr = "attacker.example:9999"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("attacker req %d: got %d, want 200", id, rec.Code)
			}
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let both attacker requests occupy the 2 tokens

	// A legitimate client from a DIFFERENT source IP must still be throttled:
	// ThrottleBacklog has no notion of "client" at all, unlike ThrottlePerIP.
	req := httptest.NewRequest(http.MethodGet, "/legit", nil)
	req.RemoteAddr = "legit.example:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("legit client from a different IP: got %d, want 503 (ThrottleBacklog's budget is global, "+
			"not per-client — unlike ThrottlePerIP)", rec.Code)
	}

	close(blockCh)
	wg.Wait()
}

// ── RequestID: the context key is a private, non-string type ───────────────

func TestSec_RequestID_ContextKeyIsUnexportedTypedNotString(t *testing.T) {
	// Static source check, mirroring the constant-time-compare guard used for
	// BasicAuth (TestSec_BasicAuth_UsesConstantTimeCompare in
	// reports/middleware-security-reviewer/harness/security_harness_test.go):
	// middleware.GetRequestID is the ONLY exported accessor for the stored ID,
	// so the collision-resistance property (CWE-1021) can only be verified by
	// confirming the storage key itself is a private struct type, not a raw
	// string literal that another package could collide with.
	src, err := os.ReadFile("request_id.go")
	if err != nil {
		t.Fatalf("reading request_id.go: %v", err)
	}
	data := string(src)
	if !strings.Contains(data, "type requestIDKey struct{}") {
		t.Error("request_id.go: expected an unexported empty-struct context key type " +
			"(type requestIDKey struct{}) — found none; a string or exported key would be a cross-package " +
			"collision risk (CWE-1021)")
	}
	if !strings.Contains(data, "func GetRequestID(ctx context.Context) string") {
		t.Error("request_id.go: expected the documented GetRequestID(ctx) string accessor")
	}
}

// ── Ordering: cancellation from an outer Timeout propagates through Compress ──

func TestSec_Ordering_TimeoutCancellationPropagatesThroughCompress(t *testing.T) {
	timeoutMW := middleware.Timeout(30 * time.Millisecond)
	compressMW := middleware.Compress(gzip.DefaultCompression)

	handlerStarted := make(chan struct{})
	observedDone := make(chan bool, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		select {
		case <-r.Context().Done():
			observedDone <- true
		case <-time.After(500 * time.Millisecond):
			observedDone <- false
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	// request → Timeout → Compress → inner: Compress's response-writer wrapper
	// must not shield the request context it forwards to next.ServeHTTP.
	chain := timeoutMW(compressMW(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	go chain.ServeHTTP(rec, req)
	<-handlerStarted

	select {
	case ok := <-observedDone:
		if !ok {
			t.Fatal("handler did not observe ctx.Done() within 500ms of a 30ms Timeout, through Compress")
		}
	case <-time.After(600 * time.Millisecond):
		t.Fatal("handler goroutine never reported back")
	}
}

// ── BasicAuth: unbounded brute force — documentation regression marker ─────

func TestSec_BasicAuth_BruteForceIsUnboundedByDesign(t *testing.T) {
	// BasicAuth intentionally does not rate-limit; SECURITY.md documents that
	// operators MUST compose it with a throttle. This pins that documented
	// behaviour so a future change does not silently start rate-limiting (or
	// silently stop being safe to compose with ThrottleBacklog/ThrottlePerIP).
	mw := middleware.BasicAuth("realm", map[string]string{"alice": "secret"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const attempts = 2000
	n401 := 0
	for i := 0; i < attempts; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("alice", fmt.Sprintf("wrong-%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			n401++
		}
	}
	if n401 != attempts {
		t.Errorf("BasicAuth processed %d/%d failed attempts as 401; want all %d — any deviation means "+
			"undocumented rate-limiting was added (compose with ThrottleBacklog/ThrottlePerIP instead)", n401, attempts, attempts)
	}
}

// ── Ordering: Logger placed OUTER of (before) RealIP sees the raw peer addr ──

func TestSec_Ordering_LoggerOuterOfRealIP_SeesRawRemoteAddr(t *testing.T) {
	trusted := mustPrefixes(t, []string{"10.0.0.0/8"})
	realIPMW := middleware.RealIP(trusted...)
	var capturedAtLogTime string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAtLogTime = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	})
	// Logger wraps the OUTER frame here via a hand-rolled recorder proxy: we
	// capture RemoteAddr exactly as Logger would see it — BEFORE RealIP (the
	// inner middleware) rewrites it. request → [logger's observation point] →
	// RealIP → inner.
	var loggerSawRemoteAddr string
	loggerObserve := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loggerSawRemoteAddr = r.RemoteAddr
			next.ServeHTTP(w, r)
		})
	}
	chain := loggerObserve(realIPMW(inner))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:8080" // the raw TCP peer, a trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.55")
	chain.ServeHTTP(rec, req)

	if loggerSawRemoteAddr != "10.0.0.5:8080" {
		t.Fatalf("outer-of-RealIP observation point saw RemoteAddr = %q, want the raw peer %q "+
			"(Logger registered before/outer of RealIP logs the proxy's address, not the client's)",
			loggerSawRemoteAddr, "10.0.0.5:8080")
	}
	if capturedAtLogTime != "203.0.113.55" {
		t.Fatalf("handler (inner of RealIP) saw RemoteAddr = %q, want the rewritten client IP %q",
			capturedAtLogTime, "203.0.113.55")
	}
}

// ── CleanPath: a NUL byte in the path does not panic and is not stripped ───

func TestSec_CleanPath_NullByteDoesNotPanicAndSurvivesCleaning(t *testing.T) {
	// path.Clean treats '\x00' as an ordinary byte (not a separator or a
	// component to collapse), so a path containing one is already "clean" and
	// passes through completely unmodified. This pins that observed,
	// panic-free behaviour; it is the router's/handler's responsibility to
	// reject NUL bytes if that is desired, not CleanPath's.
	mw := middleware.CleanPath()
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CleanPath panicked on a NUL byte in the path: %v", r)
		}
	}()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/placeholder", nil)
	req.URL.Path = "/admin\x00evil"
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if captured != "/admin\x00evil" {
		t.Fatalf("path = %q, want the NUL byte preserved unchanged: %q", captured, "/admin\x00evil")
	}
}

// ── Recoverer: a panic after the handler's own WriteHeader/Write does not ──
// ── crash, and the response is left EXACTLY as the handler committed it   ──

// O-14 FIX (rmp #276, sprint 20): spec item 14 reads "writes a 500 response
// (if headers have not already been sent)". The prior implementation called
// http.Error(w, ..., 500) unconditionally on every recovered panic — on a
// live connection this did not rewrite an already-sent status line
// (net/http silently discards a superfluous WriteHeader), but it still
// APPENDED "Internal Server Error\n" to whatever body bytes the handler had
// already streamed, corrupting the response.
//
// RecovererWithLogger now wraps the ResponseWriter it hands to the next
// handler with recovererWriter (recoverer.go), which tracks whether the
// response has already been committed — a WriteHeader call with a final
// (non-1xx) status, or the first byte written (which implies an implicit
// 200) — exactly the same "first commit wins" tracking statusRecorder
// (logger.go) and gzipResponseWriter (compress.go) already use for their
// own purposes. Recoverer's panic handler now calls http.Error only when
// the wrapper says the response has NOT started, matching the spec's
// parenthetical literally instead of only in spirit for the status line.
func recovererVariants(t *testing.T) []struct {
	name   string
	wrap   func(http.Handler) http.Handler
	logBuf *bytes.Buffer // nil for the bare Recoverer() variant (logs to slog.Default())
} {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	return []struct {
		name   string
		wrap   func(http.Handler) http.Handler
		logBuf *bytes.Buffer
	}{
		{"Recoverer", middleware.Recoverer(), nil},
		{"RecovererWithLogger", middleware.RecovererWithLogger(logger), &buf},
	}
}

func TestSec_Recoverer_PanicAfterOwnWriteHeaderAndWrite_StatusAndBodyPreserved(t *testing.T) {
	for _, tc := range recovererVariants(t) {
		t.Run(tc.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK) // headers already committed...
				_, _ = w.Write([]byte("partial"))
				panic("boom after WriteHeader") // ...then the handler panics
			})
			srv := httptest.NewServer(tc.wrap(inner)) // a REAL connection, not httptest.ResponseRecorder
			defer srv.Close()

			resp, err := http.Get(srv.URL) //nolint:noctx // test-only
			if err != nil {
				t.Fatalf("GET %s: %v", srv.URL, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (the handler's own committed status; Recoverer must not "+
					"write its own 500 once the response has started)", resp.StatusCode)
			}
			// The body is EXACTLY what the handler wrote — no "Internal
			// Server Error" text appended, no panic value or stack trace
			// substituted or prepended.
			if string(body) != "partial" {
				t.Fatalf("body = %q, want exactly %q (Recoverer must not write anything once the response "+
					"has started)", body, "partial")
			}
			if tc.logBuf != nil && !strings.Contains(tc.logBuf.String(), "panic recovered") {
				t.Fatalf("log = %q, want the panic to still be logged even though no 500 was written", tc.logBuf.String())
			}
		})
	}
}

// TestSec_Recoverer_PanicBeforeAnyWrite_Returns500WithGenericBody verifies
// the OTHER half of the spec parenthetical: when the handler panics before
// writing anything, Recoverer's 500 response is written exactly as before.
func TestSec_Recoverer_PanicBeforeAnyWrite_Returns500WithGenericBody(t *testing.T) {
	for _, tc := range recovererVariants(t) {
		t.Run(tc.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("boom before any write")
			})
			srv := httptest.NewServer(tc.wrap(inner))
			defer srv.Close()

			resp, err := http.Get(srv.URL) //nolint:noctx // test-only
			if err != nil {
				t.Fatalf("GET %s: %v", srv.URL, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", resp.StatusCode)
			}
			want := http.StatusText(http.StatusInternalServerError) + "\n"
			if string(body) != want {
				t.Fatalf("body = %q, want %q (the generic http.Error body)", body, want)
			}
			if strings.Contains(string(body), "boom") {
				t.Fatalf("body = %q, panic value leaked to the response", body)
			}
		})
	}
}

// TestSec_Recoverer_WriteHeaderOnlyThenPanic_NoExtraBody covers the case
// where the handler commits a final status but never writes a body byte
// before panicking — WriteHeader alone must be enough to mark the response
// as started, so Recoverer's 500 body is not appended after an
// otherwise-empty body.
func TestSec_Recoverer_WriteHeaderOnlyThenPanic_NoExtraBody(t *testing.T) {
	for _, tc := range recovererVariants(t) {
		t.Run(tc.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK) // commits the status, writes no body
				panic("boom after WriteHeader, no body")
			})
			srv := httptest.NewServer(tc.wrap(inner))
			defer srv.Close()

			resp, err := http.Get(srv.URL) //nolint:noctx // test-only
			if err != nil {
				t.Fatalf("GET %s: %v", srv.URL, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if len(body) != 0 {
				t.Fatalf("body = %q, want empty (Recoverer must not append its own 500 body once "+
					"WriteHeader alone has committed the response)", body)
			}
		})
	}
}

// TestSec_Recoverer_Hijack_WorksThroughUnwrap verifies recovererWriter's
// Unwrap() lets http.ResponseController(w).Hijack() reach the underlying
// connection's http.Hijacker, exactly as it already does behind Logger and
// Compress (recoverer.go does not implement Hijacker directly).
func TestSec_Recoverer_Hijack_WorksThroughUnwrap(t *testing.T) {
	mw := middleware.Recoverer()
	const raw = "HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nhijack"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, bufrw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := bufrw.WriteString(raw); err != nil {
			t.Errorf("write hijacked response: %v", err)
			return
		}
		if err := bufrw.Flush(); err != nil {
			t.Errorf("flush hijacked response: %v", err)
		}
	})
	srv := httptest.NewServer(mw(inner))
	defer srv.Close()

	resp, err := http.Get(srv.URL) //nolint:noctx // test-only
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (raw hijacked response)", resp.StatusCode)
	}
	if string(body) != "hijack" {
		t.Fatalf("body = %q, want %q", body, "hijack")
	}
}
