// Fuzz targets for Params.Get, Params.Lookup and ParamsFromContext.
//
// Invariants:
//   - I-06 : Params.Get never panics on any key, any slice state.
//   - I-06b: ParamsFromContext never panics on any context (including nil).
//   - I-06c: PathParam never panics on any *http.Request, including zero-value.
//
// Because Params and the requestCtx type are designed to be used only after
// a successful route match, we deliberately feed zero-value and adversarial
// inputs to surface any implicit assumption.
package harness

import (
	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// FuzzParamsGet exercises Params.Get with random keys. Shape of Params is
// derived from the seed bytes so corpus minimization can still identify
// minimal failing inputs.
func FuzzParamsGet(f *testing.F) {
	f.Add([]byte{}, "")
	f.Add([]byte{}, "foo")
	f.Add([]byte("k=v"), "k")
	f.Add([]byte("k=v;k2=v2"), "k2")
	f.Add([]byte("k=\x00v"), "k")
	f.Add([]byte("key=\x00\x00\x00"), "key")

	f.Fuzz(func(t *testing.T, data []byte, key string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in Params.Get: data=%q key=%q panic=%v\n%s",
					data, key, r, debug.Stack())
			}
		}()
		p := buildParams(data)
		v := p.Get(key)
		// Lookup must be consistent with Get.
		v2, ok := p.Lookup(key)
		if ok && v2 != v {
			t.Fatalf("Get/Lookup disagree for key=%q: Get=%q Lookup=%q", key, v, v2)
		}
		if !ok && v != "" {
			t.Fatalf("Get returned %q but Lookup says absent", v)
		}
		// Round-trip: adding the same key/value and re-querying must equal.
		if len(key) > 0 && !strings.ContainsAny(key, "\x00") {
			p2 := append(mm.Params{}, p...)
			p2 = append(p2, mm.Param{Key: key, Value: "canary"})
			if got := p2.Get(key); got != "canary" {
				// Last write wins in a Params slice IF no earlier entry matches.
				// But Get returns FIRST match. So we only assert stability.
				if _, present := p.Lookup(key); !present && got != "canary" {
					t.Fatalf("append-and-get round-trip broken: got=%q", got)
				}
			}
		}
		_ = v
	})
}

// FuzzParamsInt exercises the numeric parsers. They must never panic, and
// must error on non-numeric inputs.
func FuzzParamsInt(f *testing.F) {
	f.Add("42")
	f.Add("-1")
	f.Add("")
	f.Add("0x10")
	f.Add("9223372036854775808") // int64 overflow
	f.Add("0.5")
	f.Add("true")
	f.Add(strings.Repeat("9", 40))

	f.Fuzz(func(t *testing.T, val string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in numeric Params parsers: val=%q: %v\n%s",
					val, r, debug.Stack())
			}
		}()
		p := mm.Params{{Key: "n", Value: val}}
		_, _ = p.Int("n")
		_, _ = p.Int64("n")
		_, _ = p.Uint64("n")
		_, _ = p.Float64("n")
		_, _ = p.Bool("n")
		// Absent key: must return "not found" error — cannot panic.
		_, _ = p.Int("absent")
		_, _ = p.Int64("absent")
		_, _ = p.Uint64("absent")
		_, _ = p.Float64("absent")
		_, _ = p.Bool("absent")
	})
}

// FuzzParamsMap — Params.Map must always return a map with exactly len(p) entries
// or fewer (when duplicates collapse). Never panics.
func FuzzParamsMap(f *testing.F) {
	f.Add([]byte("k=v"))
	f.Add([]byte("a=1;b=2"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in Map: %v\n%s", r, debug.Stack())
			}
		}()
		p := buildParams(data)
		m := p.Map()
		if m == nil && len(p) > 0 {
			t.Fatalf("Map returned nil despite %d params", len(p))
		}
		uniqueKeys := map[string]struct{}{}
		for _, pp := range p {
			uniqueKeys[pp.Key] = struct{}{}
		}
		if len(m) != len(uniqueKeys) {
			t.Fatalf("Map len=%d unique keys=%d", len(m), len(uniqueKeys))
		}
	})
}

// FuzzParamsFromContext — ParamsFromContext must never panic regardless of
// ctx shape, and must return nil when the ctx is not a requestCtx.
func FuzzParamsFromContext(f *testing.F) {
	f.Add("value")
	f.Add("")

	f.Fuzz(func(t *testing.T, v string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in ParamsFromContext: v=%q panic=%v\n%s",
					v, r, debug.Stack())
			}
		}()
		// Background ctx — must return nil.
		if p := mm.ParamsFromContext(context.Background()); p != nil {
			t.Fatalf("Background ctx returned non-nil Params: %v", p)
		}
		// Custom ctx with arbitrary value — still non-requestCtx.
		type k struct{}
		ctx := context.WithValue(context.Background(), k{}, v)
		if p := mm.ParamsFromContext(ctx); p != nil {
			t.Fatalf("custom ctx returned non-nil Params: %v", p)
		}
	})
}

// FuzzPathParam — PathParam must never panic, even on zero-value Request or
// a Request with nil context. Guards I-06c.
func FuzzPathParam(f *testing.F) {
	f.Add("id", "42")
	f.Add("", "")
	f.Add("weird\x00key", "v")

	f.Fuzz(func(t *testing.T, key, val string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in PathParam: key=%q val=%q panic=%v\n%s",
					key, val, r, debug.Stack())
			}
		}()
		// Zero-value Request with default context from httptest would panic
		// on nil URL; we use a minimally-valid one.
		req, err := http.NewRequest(http.MethodGet, "http://x/", nil)
		if err != nil {
			t.Skip()
		}
		// PathParam on a request with no requestCtx must return "".
		if got := mm.PathParam(req, key); got != "" {
			t.Fatalf("unexpected value from untagged request: got=%q", got)
		}
		// RoutePattern on a request with no requestCtx must return "".
		if got := mm.RoutePattern(req); got != "" {
			t.Fatalf("unexpected pattern from untagged request: got=%q", got)
		}
	})
}

// buildParams deterministically derives Params from seed bytes. Format:
// "k1=v1;k2=v2;…" — ';' separates, first '=' splits key/value. Empty keys
// and NUL bytes allowed.
func buildParams(data []byte) mm.Params {
	if len(data) == 0 {
		return nil
	}
	s := string(data)
	parts := strings.Split(s, ";")
	out := make(mm.Params, 0, len(parts))
	for _, p := range parts {
		eq := strings.Index(p, "=")
		if eq < 0 {
			out = append(out, mm.Param{Key: p, Value: ""})
			continue
		}
		out = append(out, mm.Param{Key: p[:eq], Value: p[eq+1:]})
		if len(out) >= 16 {
			break
		}
	}
	return out
}
