package middleware_test

// Sprint 20, rmp #286 — reclassification of S9 threat-model hypotheses that
// were never tested (or were recorded with a status the code contradicts).
// Each test asserts the behaviour at HEAD that decides the hypothesis'
// final status; the hypothesis text is in reports/overview/2026-05-07-sprint-S9.md.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TM-2026-003 — "sub claim spoof in trusted issuer".
//
// JWTAuth authenticates the token (signature, alg allowlist, exp/nbf, iss and
// aud allowlists); it does not validate or authorise the "sub" claim. Any
// token signed with a trusted key is accepted with whatever Subject its
// signer chose, and Subject is not unique across issuers that share key
// material. A tampered sub (payload modified after signing) is rejected.
func TestSec_TM_2026_003_JWT_SubjectIsSignerAssertedNotValidated(t *testing.T) {
	secret := []byte("tm-2026-003-shared-hmac-secret-32b!")
	exp := time.Now().Add(time.Hour).Unix()

	var got []*middleware.JWTClaims
	mw := middleware.JWTAuth(middleware.JWTOptions{
		Secret:        secret,
		Algorithms:    []string{"HS256"},
		Issuers:       []string{"tenant-a", "tenant-b"},
		RequireExpiry: true,
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := middleware.GetJWTClaims(r.Context())
		if !ok {
			t.Fatal("claims missing from context")
		}
		got = append(got, c)
		w.WriteHeader(http.StatusOK)
	}))

	do := func(tok string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Two issuers sharing key material both assert sub=admin: both accepted,
	// distinguishable only by Issuer.
	tokA := makeHS256JWT(secret, map[string]any{"sub": "admin", "iss": "tenant-a", "exp": exp})
	tokB := makeHS256JWT(secret, map[string]any{"sub": "admin", "iss": "tenant-b", "exp": exp})
	// Subject is passed through verbatim, including characters no identity
	// provider would issue — JWTAuth performs no syntactic check on sub.
	tokOdd := makeHS256JWT(secret, map[string]any{"sub": "../admin\n", "iss": "tenant-a", "exp": exp})
	for _, tok := range []string{tokA, tokB, tokOdd} {
		if code := do(tok); code != http.StatusOK {
			t.Fatalf("validly signed token rejected: status %d", code)
		}
	}
	if len(got) != 3 {
		t.Fatalf("handler invocations = %d, want 3", len(got))
	}
	if got[0].Subject != "admin" || got[1].Subject != "admin" {
		t.Fatalf("Subject not passed through: %q %q", got[0].Subject, got[1].Subject)
	}
	if got[0].Issuer == got[1].Issuer {
		t.Fatalf("issuers must differ, both %q", got[0].Issuer)
	}
	if got[2].Subject != "../admin\n" {
		t.Fatalf("Subject altered: %q", got[2].Subject)
	}

	// Spoofing sub without the key: re-encode the payload of tokA with a
	// different sub, keep the original signature -> must be rejected.
	parts := strings.Split(tokA, ".")
	forged, _ := json.Marshal(map[string]any{"sub": "root", "iss": "tenant-a", "exp": exp})
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)
	if code := do(strings.Join(parts, ".")); code != http.StatusUnauthorized {
		t.Fatalf("tampered sub accepted: status %d", code)
	}
	if len(got) != 3 {
		t.Fatalf("handler reached with a tampered token")
	}
}

// TM-2026-024 — "RealIP misconfig slog.Warn leaks IP list".
//
// The only log call in RealIP is the no-CIDR construction warning
// (real_ip.go:46). With CIDRs configured nothing is logged; without CIDRs
// the warning is a constant string that carries no address or prefix.
func TestSec_TM_2026_024_RealIP_WarningCarriesNoCIDRs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	p1 := netip.MustParsePrefix("10.20.30.0/24")
	p2 := netip.MustParsePrefix("2001:db8:abcd::/48")
	_ = middleware.RealIP(&p1, &p2)
	withCIDRs := realIPRecords(t, buf.Bytes())
	if len(withCIDRs) != 0 {
		t.Fatalf("RealIP with CIDRs logged %d record(s): %v", len(withCIDRs), withCIDRs)
	}

	buf.Reset()
	_ = middleware.RealIP()
	recs := realIPRecords(t, buf.Bytes())
	if len(recs) != 1 {
		t.Fatalf("RealIP() without CIDRs: %d RealIP records, want exactly 1", len(recs))
	}
	r := recs[0]
	if r["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", r["level"])
	}
	// Only the standard time/level/msg keys: no attribute can carry a list.
	for k := range r {
		if k != "time" && k != "level" && k != "msg" {
			t.Fatalf("unexpected attribute %q in RealIP warning: %v", k, r)
		}
	}
	msg, _ := r["msg"].(string)
	for _, leak := range []string{"10.20.30", "2001:db8", "/24", "/48"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("warning leaks %q: %s", leak, msg)
		}
	}
}

func realIPRecords(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		// Other tests in the package may log concurrently through the
		// default logger; keep only records emitted by RealIP.
		if msg, _ := m["msg"].(string); strings.HasPrefix(msg, "RealIP") {
			out = append(out, m)
		}
	}
	return out
}

// tmReclassPanickingHandler is referenced by name in the TM-2026-025 test.
func tmReclassPanickingHandler(http.ResponseWriter, *http.Request) {
	panic("tm-2026-025-secret-panic-value")
}

// TM-2026-025 — "Recoverer logs handler name".
//
// Confirmed by design: RecovererWithLogger logs the panic value and the full
// debug.Stack() — handler function names and source paths — at Error level
// (recoverer.go:104-109). The client receives only the generic 500 body.
func TestSec_TM_2026_025_Recoverer_LogsStackServerSideOnly(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	h := middleware.RecovererWithLogger(logger)(http.HandlerFunc(tmReclassPanickingHandler))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); body != "Internal Server Error\n" {
		t.Fatalf("client body = %q, want the generic 500 text only", body)
	}

	var entry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &entry); err != nil {
		t.Fatalf("log is not one JSON record: %v: %q", err, logBuf.String())
	}
	if entry["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR", entry["level"])
	}
	if entry["panic"] != "tm-2026-025-secret-panic-value" {
		t.Fatalf("panic value not logged verbatim: %v", entry["panic"])
	}
	stack, _ := entry["stack"].(string)
	for _, want := range []string{"tmReclassPanickingHandler", "tm_reclass_test.go", "runtime/debug.Stack"} {
		if !strings.Contains(stack, want) {
			t.Fatalf("stack does not contain %q (function names / source paths are logged by design)", want)
		}
	}
}

// TM-2026-026 — "SetHeader CRLF panic exposes header value via panic message".
//
// The panic happens only at construction (set_header.go:29-34), from
// operator-supplied configuration, never per request. The value IS included
// in the message, escaped with strconv.QuoteToASCII (no raw CR/LF reaches a
// log sink).
func TestSec_TM_2026_026_SetHeader_PanicIsConstructionTimeAndEscaped(t *testing.T) {
	const val = "tm026-value\r\nX-Injected: 1"
	msg := func() (m string) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("SetHeader accepted a CRLF value")
			}
			m = fmt.Sprint(r)
		}()
		_ = middleware.SetHeader("X-Test", val)
		return ""
	}()
	if !strings.Contains(msg, strconv.QuoteToASCII(val)) {
		t.Fatalf("panic message does not carry the escaped value: %q", msg)
	}
	if strings.ContainsAny(msg, "\r\n") {
		t.Fatalf("panic message contains raw CR/LF: %q", msg)
	}

	// A valid SetHeader never panics on the request path, whatever the request.
	h := middleware.SetHeader("X-Test", "ok")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "/\r\n\x00\x1b[31m"
	req.Header.Set("X-Test", "attacker")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Test"); got != "ok" {
		t.Fatalf("X-Test = %q, want ok", got)
	}
}

// TM-2026-029 — "BREACH oracle survives compress variable-padding fix".
//
// Premise false: Compress has no padding. The compressed length is a pure
// function of the plaintext (same input -> same length), which is exactly
// why operator-side random padding (SECURITY.md "BREACH mitigation") is the
// documented mitigation.
func TestSec_TM_2026_029_Compress_HasNoPadding(t *testing.T) {
	body := strings.Repeat("csrf=0123456789abcdef&reflected=", 128)
	size := func() int {
		h := middleware.Compress(gzip.DefaultCompression)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, body)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("response not compressed")
		}
		zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		if string(plain) != body {
			t.Fatalf("decompressed body differs from handler output (padding or rewriting present)")
		}
		return rec.Body.Len()
	}
	first := size()
	for i := range 32 {
		if n := size(); n != first {
			t.Fatalf("run %d: compressed length %d != %d — length is not deterministic", i, n, first)
		}
	}
}

// TM-2026-035 — "HTTP/2 trailers carrying unsanitised value reach Logger".
//
// Logger formats only time, method, URL.Path, status and duration
// (logger.go:297-307); it never reads r.Trailer. Tested with trailers set
// directly on the request (the HTTP/1.1 and HTTP/2 servers both deliver
// trailers through r.Trailer) and with a real chunked HTTP/1.1 request.
func TestSec_TM_2026_035_Logger_IgnoresTrailers(t *testing.T) {
	const marker = "TM035-TRAILER-MARKER"

	var buf bytes.Buffer
	h := middleware.Logger(&buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("x"))
	req.Trailer = http.Header{"X-Evil": {"\x1b[31m" + marker + "\nFAKE 200"}}
	h.ServeHTTP(httptest.NewRecorder(), req)
	assertOneCleanLogLine(t, buf.String(), marker)

	// Real HTTP/1.1 chunked request with a trailer field.
	buf.Reset()
	var sawTrailer string
	srv := httptest.NewServer(middleware.Logger(&buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		sawTrailer = r.Trailer.Get("X-Evil")
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()
	conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	raw := "POST /chunked HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nTrailer: X-Evil\r\nConnection: close\r\n\r\n" +
		"1\r\nx\r\n0\r\nX-Evil: " + marker + "\r\n\r\n"
	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	srv.Close() // waits for the handler (and the Logger write) to finish
	if sawTrailer != marker {
		t.Fatalf("trailer not delivered to the handler (got %q) — test precondition failed", sawTrailer)
	}
	assertOneCleanLogLine(t, buf.String(), marker)
}

func assertOneCleanLogLine(t *testing.T, out, marker string) {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want exactly one log line, got %q", out)
	}
	if strings.Contains(out, marker) || strings.Contains(out, "\x1b") || strings.Contains(out, "FAKE") {
		t.Fatalf("trailer content reached the log: %q", out)
	}
}
