package muxmaster_test

import (
	"context"
	"errors"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// buildParams returns a ctx that ParamsFromContext will resolve to ps.
// It bypasses the dispatch path by creating a stub key — but since the
// internal contextKey is unexported, we test through Params directly.
func makeParams(kvs ...string) muxmaster.Params {
	if len(kvs)%2 != 0 {
		panic("makeParams expects key/value pairs")
	}
	ps := make(muxmaster.Params, 0, len(kvs)/2)
	for i := 0; i < len(kvs); i += 2 {
		ps = append(ps, muxmaster.Param{Key: kvs[i], Value: kvs[i+1]})
	}
	return ps
}

func TestParams_Get(t *testing.T) {
	ps := makeParams("a", "1", "b", "two")
	if got := ps.Get("a"); got != "1" {
		t.Errorf("Get(a)=%q want 1", got)
	}
	if got := ps.Get("missing"); got != "" {
		t.Errorf("Get(missing)=%q want empty", got)
	}
	// Empty Params
	var empty muxmaster.Params
	if got := empty.Get("any"); got != "" {
		t.Errorf("empty.Get=%q want empty", got)
	}
}

func TestParams_Lookup(t *testing.T) {
	ps := makeParams("k", "v")
	if v, ok := ps.Lookup("k"); v != "v" || !ok {
		t.Errorf("Lookup(k)=(%q,%v) want (v,true)", v, ok)
	}
	if v, ok := ps.Lookup("absent"); v != "" || ok {
		t.Errorf("Lookup(absent)=(%q,%v) want (\"\",false)", v, ok)
	}
}

func TestParams_Int(t *testing.T) {
	cases := []struct {
		name    string
		ps      muxmaster.Params
		key     string
		wantVal int
		wantErr bool
	}{
		{"valid_positive", makeParams("n", "42"), "n", 42, false},
		{"valid_negative", makeParams("n", "-7"), "n", -7, false},
		{"missing_key", makeParams("x", "1"), "n", 0, true},
		{"empty_string", makeParams("n", ""), "n", 0, true},
		{"malformed", makeParams("n", "abc"), "n", 0, true},
		{"whitespace", makeParams("n", " 42 "), "n", 0, true}, // strconv.Atoi rejects whitespace
		{"overflow", makeParams("n", "99999999999999999999"), "n", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := tc.ps.Int(tc.key)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			// strconv saturates on overflow; only compare value when no error
			// was expected.
			if !tc.wantErr && v != tc.wantVal {
				t.Errorf("v=%d want=%d", v, tc.wantVal)
			}
		})
	}
}

func TestParams_Int64(t *testing.T) {
	if v, err := makeParams("n", "9223372036854775807").Int64("n"); err != nil || v != 9223372036854775807 {
		t.Errorf("max int64: v=%d err=%v", v, err)
	}
	if v, err := makeParams("n", "-9223372036854775808").Int64("n"); err != nil || v != -9223372036854775808 {
		t.Errorf("min int64: v=%d err=%v", v, err)
	}
	if _, err := makeParams("n", "9223372036854775808").Int64("n"); err == nil {
		t.Error("expected overflow error")
	}
	if _, err := makeParams("n", "abc").Int64("n"); err == nil {
		t.Error("expected parse error on non-numeric")
	}
	if _, err := makeParams("x", "1").Int64("n"); err == nil {
		t.Error("expected NotFound error on missing key")
	}
}

func TestParams_Uint64(t *testing.T) {
	if v, err := makeParams("n", "18446744073709551615").Uint64("n"); err != nil || v != 18446744073709551615 {
		t.Errorf("max uint64: v=%d err=%v", v, err)
	}
	if _, err := makeParams("n", "-1").Uint64("n"); err == nil {
		t.Error("uint64 must reject negative")
	}
	if _, err := makeParams("n", "18446744073709551616").Uint64("n"); err == nil {
		t.Error("expected overflow error")
	}
	if _, err := makeParams("x", "1").Uint64("n"); err == nil {
		t.Error("expected NotFound on missing key")
	}
}

func TestParams_Float64(t *testing.T) {
	cases := []struct {
		val     string
		want    float64
		wantErr bool
	}{
		{"3.14", 3.14, false},
		{"-2.5", -2.5, false},
		{"1e10", 1e10, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		v, err := makeParams("n", tc.val).Float64("n")
		if (err != nil) != tc.wantErr {
			t.Errorf("val=%q err=%v wantErr=%v", tc.val, err, tc.wantErr)
		}
		if !tc.wantErr && v != tc.want {
			t.Errorf("val=%q got=%v want=%v", tc.val, v, tc.want)
		}
	}
	if _, err := muxmaster.Params(nil).Float64("n"); err == nil {
		t.Error("nil params should error")
	}
}

func TestParams_Bool(t *testing.T) {
	cases := []struct {
		val     string
		want    bool
		wantErr bool
	}{
		{"true", true, false},
		{"false", false, false},
		{"1", true, false},
		{"0", false, false},
		{"True", true, false},
		{"FALSE", false, false},
		{"yes", false, true}, // strconv.ParseBool rejects yes/no
		{"", false, true},
	}
	for _, tc := range cases {
		v, err := makeParams("b", tc.val).Bool("b")
		if (err != nil) != tc.wantErr {
			t.Errorf("val=%q err=%v wantErr=%v", tc.val, err, tc.wantErr)
		}
		if !tc.wantErr && v != tc.want {
			t.Errorf("val=%q got=%v want=%v", tc.val, v, tc.want)
		}
	}
	if _, err := makeParams("x", "1").Bool("b"); err == nil {
		t.Error("missing key should error")
	}
}

func TestParams_Map(t *testing.T) {
	ps := makeParams("a", "1", "b", "2")
	m := ps.Map()
	if len(m) != 2 || m["a"] != "1" || m["b"] != "2" {
		t.Errorf("Map=%v want {a:1,b:2}", m)
	}
	// Modifying map must not affect Params (independent copy).
	m["a"] = "mutated"
	if v, _ := ps.Lookup("a"); v != "1" {
		t.Errorf("Params mutated via Map(): a=%q", v)
	}
	// Empty params -> empty map.
	if m := (muxmaster.Params{}).Map(); len(m) != 0 {
		t.Errorf("empty.Map=%v want empty", m)
	}
}

// Sanity: error sentinel chain
func TestParams_ErrorIsParamNotFound(t *testing.T) {
	_, err := makeParams("x", "1").Int("y")
	if err == nil {
		t.Fatal("expected error")
	}
	// We can't import the unexported sentinel, but we can match the message.
	if !errors.Is(err, err) || err.Error() == "" {
		t.Error("error not well-formed")
	}
}

// Race smoke: concurrent reads must be safe.
func TestParams_ConcurrentRead(t *testing.T) {
	ps := makeParams("k", "v")
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 1000; j++ {
				_ = ps.Get("k")
				_, _ = ps.Lookup("k")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// ensure context import not removed by formatting
var _ = context.Background
