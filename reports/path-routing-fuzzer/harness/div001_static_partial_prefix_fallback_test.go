package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bunrouter"
)

// TestDIV001_NoBacktrackFromPartialStaticPrefixToParam is the differential
// record for DIV-001 (sprint 18, rmp #256 audit; FIXED by rmp #259).
//
// Originally: registering a static route "/users/list" as a sibling of a
// named-parameter route "/users/:id" exposed a radix-tree limitation in
// MuxMaster — once getValue descended into the static sibling because its
// first byte after the common prefix matched an index entry, it never
// backtracked to try the param sibling if the static subtree then failed to
// match the remainder of the path. chi and bunrouter both fell back to the
// param route in this situation; MuxMaster returned 404.
//
// rmp #259 added bounded backtracking to getValue (tree.go: backtrackStack,
// the `goto fail` retry loop inside getValue): when a static child is
// chosen over an available wildchild sibling and that static branch
// ultimately fails to match, the lookup now resumes at the wildchild
// instead of returning 404. rmp #260 then removed the hard backtrackDepth=8
// cap #259 had placed on that stack — it silently dropped any fork frame
// past the 8th, a correctness bug — so deep forks now resolve at any
// nesting depth, bounded only by tree size (see tree.go's backtrackStack
// doc for the linear-in-tree-size proof). MuxMaster's output below now
// matches chi and bunrouter exactly for all four paths, resolving the
// divergence with specification/routing.md rule 49 ("static routes always
// outrank named parameters" implies the param is still considered when
// static doesn't pan out, not merely when no static child's first byte
// matched at all).
//
// httprouter v1.3.0 (the version vendored here) still cannot be used as an
// oracle for this case — it panics at REGISTRATION time on this exact
// static+param sibling combination, a limitation MuxMaster's #256 change
// specifically lifts.
//
// Kept as a permanent regression guard: the loop below now asserts the
// FIXED behaviour (muxmaster returns 200:param:* for "/users/listx" and
// "/users/lis", matching chi/bunrouter) rather than merely logging the
// prior divergence.
func TestDIV001_NoBacktrackFromPartialStaticPrefixToParam(t *testing.T) {
	mux := mm.New()
	mux.RedirectTrailingSlash = false
	mux.GET("/users/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route-ID", "static")
		w.WriteHeader(200)
	})
	mux.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route-ID", "param:"+mm.PathParam(r, "id"))
		w.WriteHeader(200)
	})

	cx := chi.NewRouter()
	cx.Get("/users/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route-ID", "static")
		w.WriteHeader(200)
	})
	cx.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route-ID", "param:"+chi.URLParam(r, "id"))
		w.WriteHeader(200)
	})

	br := bunrouter.New()
	br.GET("/users/list", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Route-ID", "static")
		w.WriteHeader(200)
		return nil
	})
	br.GET("/users/:id", func(w http.ResponseWriter, req bunrouter.Request) error {
		w.Header().Set("X-Route-ID", "param:"+req.Param("id"))
		w.WriteHeader(200)
		return nil
	})

	routers := map[string]http.Handler{"muxmaster": mux, "chi": cx, "bunrouter": br}
	paths := []string{"/users/list", "/users/listx", "/users/lis", "/users/42"}

	results := make(map[string]map[string]string) // path -> router -> "code:id"
	for _, p := range paths {
		results[p] = map[string]string{}
		for name, h := range routers {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			results[p][name] = fmt.Sprintf("%d:%s", rec.Code, rec.Header().Get("X-Route-ID"))
			t.Logf("DIV-001 %-12s %-14s -> %s", name, p, results[p][name])
		}
	}

	// Regression guard for the FIX (rmp #259): muxmaster must now fall back
	// to :id for "/users/listx" and "/users/lis", matching chi and
	// bunrouter exactly (200:param:<value>), not 404.
	for _, p := range []string{"/users/listx", "/users/lis"} {
		wantID := "param:" + strings.TrimPrefix(p, "/users/")
		want := "200:" + wantID
		if got := results[p]["muxmaster"]; got != want {
			t.Errorf("DIV-001 REGRESSION: muxmaster %q = %q, want %q (bounded backtracking to :id, rmp #259)", p, got, want)
		}
		for _, competitor := range []string{"chi", "bunrouter"} {
			if got := results[p][competitor]; got != want {
				t.Logf("DIV-001: %s behaviour for %q changed (now %q, oracle expected %q) — re-verify the finding is still accurate before citing it", competitor, p, got, want)
			}
		}
	}
}
