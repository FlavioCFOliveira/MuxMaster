package muxmaster_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// buildDeepForkBacktrackMux is a variant of buildAdversarialBacktrackMux
// (bench_test.go) where the SOLE real target is reachable only by taking
// the param alternative at the DEEPEST level (depth-1) of the all-static
// decoy spine, rather than at level 0 — the worst case for a LIFO
// backtrack stack, since the deepest fork is the LAST one pushed and so
// the FIRST one popped, but it is still only found by unwinding through
// every shallower dead-end fork first.
//
// Before the fix validated by this file, a fixed-size backtrackStack
// capped at backtrackDepth (8) silently dropped any fork frame past the
// 8th, so a target requiring the deepest fork was only reachable while
// depth-1 < 8; deeper trees 404'd incorrectly even though the target was
// genuinely registered and reachable by construction — the DIV-001-class
// bug this whole backtracking mechanism exists to fix, just reappearing
// past an arbitrary depth. See backtrackStack's "why no cap is needed"
// proof in tree.go: since each node is visited at most once, there is no
// need for — and no correctness-preserving way to justify — a cap at all.
func buildDeepForkBacktrackMux(depth int) (*muxmaster.Mux, string) {
	m := muxmaster.New()
	m.RedirectTrailingSlash = false

	spine := "/a" + strings.Repeat("/x", depth) + "/STATICEND"
	m.GET(spine, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	for k := 0; k < depth; k++ {
		paramName := fmt.Sprintf("r%d", k)
		if k == 0 {
			paramName = "p1"
		}
		pattern := "/a" + strings.Repeat("/x", k) + fmt.Sprintf("/:%s/NEVERMATCH%d", paramName, k)
		m.GET(pattern, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	}

	// The real target requires going param at the LAST (deepest) decoy
	// level instead of level 0. Mirror the loop's own k==0 naming
	// convention ("p1") so depth==1 (deepest level IS level 0) reuses the
	// same wildchild instead of registering a conflicting second name at
	// the same tree position.
	lastParam := fmt.Sprintf("r%d", depth-1)
	if depth-1 == 0 {
		lastParam = "p1"
	}
	target := "/a" + strings.Repeat("/x", depth-1) + "/:" + lastParam + "/REALEND"
	m.GET(target, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	return m, "/a" + strings.Repeat("/x", depth) + "/REALEND"
}

// TestDeepForkBacktracking_LinearNotExponential is the correctness-side
// companion to BenchmarkAdversarialBacktracking and
// BenchmarkQuadraticBacktracking (bench_test.go): it proves getValue's
// backtracking has NO depth ceiling — every one of these depths, including
// several well past the OLD (removed) backtrackDepth=8 ceiling, must find
// the real, registered target. A 404 at any of these depths would mean a
// legitimately registered, reachable route is unreachable — exactly the
// DIV-001-class correctness bug bounded backtracking exists to fix — and
// would contradict backtrackStack's "why no cap is needed" proof (tree.go),
// which shows total work is bounded by tree size, not by an arbitrary
// depth constant.
//
// This also proves ServeHTTP never panics or blocks at any of these
// depths, directly addressing the DoS concern the earlier, capped design
// was (mistakenly) thought to require: an attacker cannot force unbounded
// work by crafting a deeply forking route set, because each node is
// visited at most once (the proof), not because retrying beyond some
// point is refused.
func TestDeepForkBacktracking_LinearNotExponential(t *testing.T) {
	// 2 and 3 straddle backtrackInline (2): depth=2 never touches the
	// heap-allocated overflow path, depth=3 forces exactly one overflow
	// frame. 8 and 9 straddle the OLD backtrackDepth cap that this test
	// used to document as a correctness boundary — both must now succeed
	// identically. 50 is far beyond anything the old cap ever allowed,
	// proving there is no new, larger hidden ceiling either.
	for _, depth := range []int{1, 2, 3, 8, 9, 50} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			m, reqPath := buildDeepForkBacktrackMux(depth)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("depth=%d: ServeHTTP panicked: %v", depth, r)
				}
			}()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("depth=%d: GET %q = %d, want 200 (the deepest fork must always be "+
					"reachable — backtracking has no depth ceiling)", depth, reqPath, rec.Code)
			}
		})
	}
}

// TestDeepForkBacktracking_NoPanicAtExtremeDepth is a pure safety check,
// separate from the correctness check above: at a depth two orders of
// magnitude past the old cap, ServeHTTP must still neither panic nor hang.
// It intentionally does not assert on ns/op or allocs/op (that is
// BenchmarkAdversarialBacktracking / BenchmarkQuadraticBacktracking's job)
// — only that the process survives and returns.
func TestDeepForkBacktracking_NoPanicAtExtremeDepth(t *testing.T) {
	const depth = 500
	m, reqPath := buildDeepForkBacktrackMux(depth)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("depth=%d: ServeHTTP panicked: %v", depth, r)
		}
	}()

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("depth=%d: GET %q = %d, want 200", depth, reqPath, rec.Code)
	}
}
