// Black-box security regression tests for RequestID's inbound-header
// handling after the sprint-18 direct-map-access rewrite (rmp #245): the
// middleware now reads r.Header[requestIDHeaderKey] directly instead of
// calling r.Header.Get("X-Request-ID"), and writes
// w.Header()[requestIDHeaderKey] directly instead of calling
// w.Header().Set(...). These tests confirm that rewrite did not change
// observable semantics for cases the allocation-focused
// request_id_pool_test.go suite does not already cover:
//
//   - multiple inbound X-Request-ID header values (Get() semantics: first
//     value wins) — MM-2026-0011's validation must still apply to that
//     first value only, matching the pre-rewrite behaviour byte for byte;
//   - defense-in-depth against CRLF/control characters and oversized
//     values reaching the response header, even when constructed directly
//     via Header.Set in a test (bypassing the wire-level parser that would
//     normally reject a raw CRLF in a header value before it ever reaches
//     the middleware).
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestSec_RequestID_MultipleInboundValues_FirstValueWins_MatchesHeaderGet
// confirms that when a client sends X-Request-ID more than once (legal per
// RFC 9110 §5.3 — a server MAY combine or reject, but net/http always
// exposes them as a slice and Header.Get always returns the FIRST), the
// direct map-access rewrite preserves exactly that "first value" semantic
// instead of, e.g., silently picking the last value or concatenating them.
func TestSec_RequestID_MultipleInboundValues_FirstValueWins_MatchesHeaderGet(t *testing.T) {
	mw := middleware.RequestID()
	var gotCtx string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCtx = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("X-Request-Id", "first-value")
	req.Header.Add("X-Request-Id", "second-value")
	if got := req.Header.Get("X-Request-Id"); got != "first-value" {
		t.Fatalf("sanity check failed: http.Header.Get returned %q, want %q", got, "first-value")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if gotCtx != "first-value" {
		t.Fatalf("context request id = %q, want %q (first of multiple inbound values, matching http.Header.Get semantics)", gotCtx, "first-value")
	}
	if hdr := rec.Header().Get("X-Request-Id"); hdr != "first-value" {
		t.Fatalf("response header = %q, want %q", hdr, "first-value")
	}
}

// TestSec_RequestID_CRLFInValue_NeverReachesValidation is defence-in-depth:
// even though a real HTTP/1.1 or HTTP/2 parser would never deliver a raw
// CRLF inside a single header VALUE to r.Header (the wire format uses CRLF
// to terminate the header line itself), a test — or a future code path that
// constructs *http.Request by hand — can set one directly. validRequestID's
// restrictive character set (alphanumeric + '-' '_' '.') must reject it, so
// no CRLF-bearing value is ever propagated onto the OUTBOUND response header
// via the fast, unchecked direct-map-write path this rewrite introduced.
func TestSec_RequestID_CRLFInValue_NeverReachesValidation(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	payloads := []string{
		"evil\r\nSet-Cookie: pwned=1",
		"evil\r\n",
		"\r\nX-Injected: yes",
		"evil\nline2",
		"evil\x00null",
	}
	for _, p := range payloads {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-Id", p)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		got := rec.Header().Get("X-Request-Id")
		if strings.ContainsAny(got, "\r\n\x00") {
			t.Fatalf("payload %q: response header id %q contains a raw control character — injection reached the wire-facing header", p, got)
		}
		if got == p {
			t.Fatalf("payload %q was propagated unchanged instead of being replaced by a generated id", p)
		}
		if len(got) != 32 {
			t.Fatalf("payload %q: replacement id %q has len=%d, want 32", p, got, len(got))
		}
	}
}

// TestSec_RequestID_OversizedInboundValue_Rejected confirms a very large
// (well beyond the 128-character bound documented on validRequestID)
// inbound X-Request-ID is replaced with a generated id rather than being
// propagated verbatim — both as a correctness check on MM-2026-0011's
// length bound and as a guard against using an attacker-controlled,
// unbounded string as a response header value (memory/response-size
// amplification).
func TestSec_RequestID_OversizedInboundValue_Rejected(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	huge := strings.Repeat("a", 10*1024) // 10 KiB, all otherwise-valid characters
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", huge)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got == huge {
		t.Fatalf("10 KiB inbound id was propagated verbatim — the 128-character bound was not enforced")
	}
	if len(got) != 32 {
		t.Fatalf("replacement id has len=%d, want 32", len(got))
	}
}

// TestSec_RequestID_EmptyInboundValue_ReplacedWithGenerated confirms an
// empty X-Request-Id header (a client sending the header with no value —
// distinct from omitting it entirely) is treated as invalid and replaced,
// not propagated as an empty response header.
func TestSec_RequestID_EmptyInboundValue_ReplacedWithGenerated(t *testing.T) {
	mw := middleware.RequestID()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got == "" {
		t.Fatalf("response X-Request-Id header is empty — an empty inbound value must be replaced with a generated id")
	}
	if len(got) != 32 {
		t.Fatalf("replacement id has len=%d, want 32", len(got))
	}
}
