// White-box regression tests for rmp task #252, sprint 18 (WH-06):
// decodeJWTHeader's single-entry memo and bytesFromString's zero-copy view.
// Package middleware because both are unexported.
package middleware

import (
	"sync/atomic"
	"testing"
)

func TestDecodeJWTHeader_MemoHit_ReturnsSameAlgAsFullDecode(t *testing.T) {
	allowed := map[string]struct{}{"HS256": {}}
	var memo atomic.Pointer[jwtHeaderMemo]
	headerB64 := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}

	alg1, ok1 := decodeJWTHeader(headerB64, allowed, &memo)
	if !ok1 || alg1 != "HS256" {
		t.Fatalf("first (miss) decode = (%q, %v), want (\"HS256\", true)", alg1, ok1)
	}
	m := memo.Load()
	if m == nil || m.b64 != headerB64 || m.alg != "HS256" {
		t.Fatalf("memo not populated correctly after a successful decode: %+v", m)
	}

	alg2, ok2 := decodeJWTHeader(headerB64, allowed, &memo)
	if !ok2 || alg2 != "HS256" {
		t.Fatalf("second (hit) decode = (%q, %v), want (\"HS256\", true)", alg2, ok2)
	}
}

func TestDecodeJWTHeader_DisallowedAlg_NeverCached(t *testing.T) {
	allowed := map[string]struct{}{"HS256": {}}
	var memo atomic.Pointer[jwtHeaderMemo]
	// alg "none" — the classic JWT algorithm-confusion header (RFC 8725 §3.1).
	headerB64 := "eyJhbGciOiJub25lIn0" // {"alg":"none"}

	if _, ok := decodeJWTHeader(headerB64, allowed, &memo); ok {
		t.Fatal("disallowed alg was accepted")
	}
	if m := memo.Load(); m != nil {
		t.Fatalf("a REJECTED header must never populate the memo, got %+v", m)
	}
	// Must still be rejected on a second attempt with the identical bytes.
	if _, ok := decodeJWTHeader(headerB64, allowed, &memo); ok {
		t.Fatal("disallowed alg was accepted on a repeat attempt")
	}
}

func TestDecodeJWTHeader_CritHeader_NeverCached(t *testing.T) {
	allowed := map[string]struct{}{"HS256": {}}
	var memo atomic.Pointer[jwtHeaderMemo]
	// {"alg":"HS256","crit":["x"]}
	headerB64 := "eyJhbGciOiJIUzI1NiIsImNyaXQiOlsieCJdfQ"

	if _, ok := decodeJWTHeader(headerB64, allowed, &memo); ok {
		t.Fatal("a header with a non-empty crit list was accepted")
	}
	if m := memo.Load(); m != nil {
		t.Fatalf("a crit-rejected header must never populate the memo, got %+v", m)
	}
	if _, ok := decodeJWTHeader(headerB64, allowed, &memo); ok {
		t.Fatal("crit header was accepted on a repeat attempt (would indicate a stale/incorrect cache entry)")
	}
}

func TestDecodeJWTHeader_MalformedBase64_NeverCachedNorPanics(t *testing.T) {
	allowed := map[string]struct{}{"HS256": {}}
	var memo atomic.Pointer[jwtHeaderMemo]
	if _, ok := decodeJWTHeader("not-valid-base64!!!", allowed, &memo); ok {
		t.Fatal("malformed base64 was accepted")
	}
	if m := memo.Load(); m != nil {
		t.Fatalf("malformed input must never populate the memo, got %+v", m)
	}
}

func TestDecodeJWTHeader_DifferentBytesSameAlg_BothCorrect(t *testing.T) {
	allowed := map[string]struct{}{"HS256": {}}
	var memo atomic.Pointer[jwtHeaderMemo]
	// Two DIFFERENT header byte strings that both decode to alg=HS256.
	h1 := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}
	h2 := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9" // {"typ":"JWT","alg":"HS256"} (field order swapped)

	if alg, ok := decodeJWTHeader(h1, allowed, &memo); !ok || alg != "HS256" {
		t.Fatalf("h1 decode = (%q,%v)", alg, ok)
	}
	// h2 has different bytes, so this MUST miss the memo (which holds h1) and
	// still independently decode correctly.
	if alg, ok := decodeJWTHeader(h2, allowed, &memo); !ok || alg != "HS256" {
		t.Fatalf("h2 decode = (%q,%v)", alg, ok)
	}
	if m := memo.Load(); m == nil || m.b64 != h2 {
		t.Fatalf("memo should now hold the LAST accepted header (h2), got %+v", m)
	}
	// h1 must still decode correctly even though it is no longer memoised.
	if alg, ok := decodeJWTHeader(h1, allowed, &memo); !ok || alg != "HS256" {
		t.Fatalf("h1 re-decode after eviction = (%q,%v)", alg, ok)
	}
}

func TestBytesFromString_EquivalentToByteSliceConversion(t *testing.T) {
	for _, s := range []string{"", "a", "header.payload", "日本語", string([]byte{0, 1, 2, 255})} {
		want := []byte(s)
		got := bytesFromString(s)
		if len(got) != len(want) {
			t.Fatalf("bytesFromString(%q) len = %d, want %d", s, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("bytesFromString(%q)[%d] = %d, want %d", s, i, got[i], want[i])
			}
		}
	}
}
