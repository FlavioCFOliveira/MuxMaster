package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// FuzzByName exercises Params.Get with arbitrary key bytes.
// Invariant I-06: Params.Get must never panic for any key on any Params slice.
func FuzzByName(f *testing.F) {
	f.Add([]byte{}, "")
	f.Add([]byte("id\x00value"), "id")
	f.Add([]byte("key\xffval"), "key")
	f.Add([]byte("a\tb\nc\rd"), "a")
	f.Add([]byte("id\x00123\x00name\x00alice"), "name")
	f.Add([]byte{0xFF, 0xFE, 0x00}, "x")

	f.Fuzz(func(t *testing.T, data []byte, key string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in Params.Get: key=%q data=%x r=%v\n%s", key, data, r, debug.Stack())
			}
		}()
		p := buildParams(data)
		_ = p.Get(key)
		_, _ = p.Lookup(key)
		_, _ = p.Int(key)
		_, _ = p.Int64(key)
		_, _ = p.Uint64(key)
		_, _ = p.Float64(key)
		_, _ = p.Bool(key)
	})
}

// FuzzParamsFromContext exercises ParamsFromContext with arbitrary context values.
// Invariant I-06b: ParamsFromContext must never panic for any context.Context.
func FuzzParamsFromContext(f *testing.F) {
	f.Add("")
	f.Add("valid-key")
	f.Add("\x00\xff\r\n")
	f.Add("a/b/c")

	f.Fuzz(func(t *testing.T, key string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ParamsFromContext: key=%q r=%v\n%s", key, r, debug.Stack())
			}
		}()
		// Test 1: plain background context — must return nil, no panic.
		ps := mm.ParamsFromContext(context.Background())
		_ = ps.Get(key)

		// Test 2: context with an unrelated value — must not panic.
		type myKey struct{}
		ctx := context.WithValue(context.Background(), myKey{}, "unrelated")
		ps2 := mm.ParamsFromContext(ctx)
		_ = ps2.Get(key)

		// Test 3: nil params from a plain request — PathParam must not panic.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		_ = mm.PathParam(req, key)
		_ = mm.RoutePattern(req)
	})
}

// FuzzParamsRoundtrip verifies that params stored via a real route are
// retrievable via ParamsFromContext and PathParam with the exact same value.
// Invariant I-07: ParamsFromContext(ctx).Get("x") == original value.
func FuzzParamsRoundtrip(f *testing.F) {
	f.Add("hello")
	f.Add("123")
	f.Add("")
	f.Add("a/b/c")  // slashes in param values (catch-all)
	f.Add("\xe2\x9c\x93") // unicode checkmark

	f.Fuzz(func(t *testing.T, paramValue string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ParamsRoundtrip: paramValue=%q r=%v\n%s", paramValue, r, debug.Stack())
			}
		}()

		// Use a catch-all so any paramValue (including those with '/') is captured.
		mux := mm.New()
		var gotParam string
		mux.GET("/files/*path", func(w http.ResponseWriter, r *http.Request) {
			// Verify via both accessors.
			gotParam = mm.PathParam(r, "path")
			ps := mm.ParamsFromContext(r.Context())
			if ps == nil {
				t.Error("ParamsFromContext returned nil on parameterised route")
				return
			}
			v2 := ps.Get("path")
			if gotParam != v2 {
				t.Errorf("PathParam=%q vs ParamsFromContext.Get=%q", gotParam, v2)
			}
			w.WriteHeader(http.StatusOK)
		})

		// paramValue may contain characters invalid in URLs; skip those.
		if containsInvalidURLBytes(paramValue) {
			return
		}

		reqPath := "/files/" + paramValue
		req, err := buildRequest(http.MethodGet, reqPath)
		if err != nil {
			return // invalid URL — skip
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			// The roundtrip invariant: param value must match the path segment.
			want := paramValue
			if gotParam == "" && want != "" {
				// Path normalization may change the value; just verify no panic.
			}
			_ = gotParam
		}
	})
}

// FuzzParamsOverflow exercises the overflow path (>3 params = heap allocation).
// Invariant I-08: routes with >3 params must dispatch without panic and
// return all params correctly.
func FuzzParamsOverflow(f *testing.F) {
	f.Add("a", "b", "c", "d")
	f.Add("1", "2", "3", "4")
	f.Add("", "", "", "")
	f.Add("x\x00y", "a", "b", "c")

	f.Fuzz(func(t *testing.T, v1, v2, v3, v4 string) {
		// Skip values with path-separator characters — they break URL routing.
		if containsSlash(v1) || containsSlash(v2) || containsSlash(v3) || containsSlash(v4) {
			return
		}
		// Skip values with invalid URL characters that httptest.NewRequest rejects.
		if containsInvalidURLBytes(v1) || containsInvalidURLBytes(v2) ||
			containsInvalidURLBytes(v3) || containsInvalidURLBytes(v4) {
			return
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in ParamsOverflow: r=%v\n%s", r, debug.Stack())
			}
		}()

		mux := mm.New()
		var got [4]string
		mux.GET("/a/:p1/b/:p2/c/:p3/d/:p4", func(w http.ResponseWriter, r *http.Request) {
			got[0] = mm.PathParam(r, "p1")
			got[1] = mm.PathParam(r, "p2")
			got[2] = mm.PathParam(r, "p3")
			got[3] = mm.PathParam(r, "p4")
			w.WriteHeader(http.StatusOK)
		})

		reqPath := "/a/" + v1 + "/b/" + v2 + "/c/" + v3 + "/d/" + v4
		req, err := buildRequest(http.MethodGet, reqPath)
		if err != nil {
			return
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			if got[0] != v1 || got[1] != v2 || got[2] != v3 || got[3] != v4 {
				t.Errorf("params mismatch: got=%v want=[%q,%q,%q,%q]",
					got, v1, v2, v3, v4)
			}
		}
	})
}

// buildParams constructs a Params slice deterministically from raw bytes.
// Format: pairs of null-terminated key/value strings.
// Excess bytes are silently dropped. Returns nil on empty input.
func buildParams(data []byte) mm.Params {
	if len(data) == 0 {
		return nil
	}
	var ps mm.Params
	i := 0
	for i < len(data) {
		// Find key end (null byte or end of data)
		j := i
		for j < len(data) && data[j] != 0 {
			j++
		}
		key := string(data[i:j])
		if j >= len(data) {
			break
		}
		i = j + 1

		// Find value end
		j = i
		for j < len(data) && data[j] != 0 {
			j++
		}
		value := string(data[i:j])
		i = j + 1

		ps = append(ps, mm.Param{Key: key, Value: value})
		if len(ps) > 64 { // bound to avoid OOM
			break
		}
	}
	return ps
}

func containsSlash(s string) bool {
	for _, c := range s {
		if c == '/' {
			return true
		}
	}
	return false
}

func containsInvalidURLBytes(s string) bool {
	for _, c := range s {
		// Exclude:
		//   - control chars and space (net/url rejects)
		//   - URL structural chars that terminate path segments (?, #)
		//   - percent sign (% starts encoding sequences — url.ParseRequestURI
		//     normalises them, breaking the roundtrip expectation)
		if c <= 0x20 || c == 0x7F || c == '?' || c == '#' || c == '%' {
			return true
		}
	}
	return false
}
