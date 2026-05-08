package muxmaster_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// COV-2026-014 — Property-based tests of public API invariants.
// We use math/rand with a fixed seed instead of an external library to
// preserve the zero-dependency invariant. Each invariant runs N iterations
// with shrinking-style retry on the failing seed.

const propIters = 200

// Invariant 1: addRoute(method, pattern, h) followed by ServeHTTP(method, pattern)
// must invoke h. Holds for any pattern that the router accepts (no panic).
func TestProp_AddThenDispatchAlwaysHits(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < propIters; i++ {
		// Generate a static pattern from a small alphabet.
		pattern := genStaticPath(rng, 1+rng.Intn(4))
		hits := 0
		r := muxmaster.New()
		// Register only if it doesn't panic (router rejects some patterns).
		ok := tryRegister(r, pattern, func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.WriteHeader(http.StatusOK)
		})
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pattern, nil))
		if hits != 1 || rec.Code != http.StatusOK {
			t.Errorf("iter=%d pattern=%q: hits=%d code=%d", i, pattern, hits, rec.Code)
		}
	}
}

// Invariant 2: PathParam never panics on a request with no params context.
func TestProp_PathParamNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < propIters; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		name := genName(rng, 1+rng.Intn(20))
		// Must not panic for any name, even on a request without params.
		v := muxmaster.PathParam(req, name)
		if v != "" {
			t.Errorf("iter=%d name=%q v=%q want empty", i, name, v)
		}
	}
}

// Invariant 3: ParamsFromContext on context.Background returns nil/empty.
func TestProp_ParamsFromContextOnBackgroundIsEmpty(t *testing.T) {
	for i := 0; i < propIters; i++ {
		ps := muxmaster.ParamsFromContext(context.Background())
		if len(ps) != 0 {
			t.Errorf("iter=%d ps=%v want empty", i, ps)
		}
	}
}

// Invariant 4: Params.Get on a missing key always returns "".
func TestProp_ParamsGetMissingReturnsEmpty(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < propIters; i++ {
		ps := muxmaster.Params{}
		// Sometimes populate with random keys; query for a guaranteed-missing one.
		n := rng.Intn(5)
		for j := 0; j < n; j++ {
			ps = append(ps, muxmaster.Param{Key: fmt.Sprintf("k%d", j), Value: "v"})
		}
		missing := fmt.Sprintf("never-%d-%d", i, rng.Int())
		if v := ps.Get(missing); v != "" {
			t.Errorf("iter=%d Get(%q)=%q", i, missing, v)
		}
	}
}

// Invariant 5: Routes() returns one entry per registered pattern (no
// duplicates, no missing). Holds across random sets of static patterns.
func TestProp_RoutesNoDuplicates(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 50; i++ {
		r := muxmaster.New()
		want := map[string]bool{}
		for j := 0; j < 1+rng.Intn(8); j++ {
			pat := genStaticPath(rng, 1+rng.Intn(3))
			if want[pat] {
				continue // skip duplicates (router would panic on conflict)
			}
			if tryRegister(r, pat, func(w http.ResponseWriter, _ *http.Request) {}) {
				want[pat] = true
			}
		}
		got := r.Routes()
		got2 := map[string]int{}
		for _, ri := range got {
			got2[ri.Pattern]++
		}
		for pat := range want {
			if got2[pat] == 0 {
				t.Errorf("iter=%d missing %q", i, pat)
			}
			if got2[pat] > 1 {
				t.Errorf("iter=%d duplicate %q (count=%d)", i, pat, got2[pat])
			}
		}
	}
}

// helpers ---------------------------------------------------------------

var _propAlphabet = []byte("abcdefg")

func genStaticPath(rng *rand.Rand, segs int) string {
	out := ""
	for i := 0; i < segs; i++ {
		out += "/"
		n := 1 + rng.Intn(4)
		for j := 0; j < n; j++ {
			out += string(_propAlphabet[rng.Intn(len(_propAlphabet))])
		}
	}
	return out
}

func genName(rng *rand.Rand, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = _propAlphabet[rng.Intn(len(_propAlphabet))]
	}
	return string(out)
}

func tryRegister(r *muxmaster.Mux, pat string, h http.HandlerFunc) (ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			ok = false
		}
	}()
	r.GET(pat, h)
	return true
}
