package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// =============================================================================
// TestServeFiles_ShimFS_CleansTraversal — rmp #274 part 2/4, item 3 restoration
// =============================================================================
//
// The deleted catchall_escape_test.go (git show
// 5f804fa^:reports/path-routing-fuzzer/harness/catchall_escape_test.go) had
// TestCatchAllEscape_ServeFiles, which registered the REAL mux.ServeFiles
// wrapper against a shim http.FileSystem and observed exactly which name
// Open() was called with for a set of traversal payloads. No function with
// that name or purpose survived 5f804fa's rewrite: the current equivalent,
// TestServeFiles_FilepathContainment (hypotheses_test.go), only registers a
// plain http.HandlerFunc that captures mm.PathParam(req, "filepath") — it
// never calls r.ServeFiles at all, so it cannot observe what the actual
// ServeFiles → http.FileServer → FileSystem.Open boundary receives. That is
// a real, unrestored coverage gap: mux.go's ServeFiles doc comment
// (mux.go:820-831) explicitly documents that the traversal-safety boundary
// for the catch-all's raw captured value is "http.FileServer's clean step"
// — nothing in the current suite exercises that specific handoff.
//
// This restores it, updated to also assert (not merely log, as the deleted
// version did) the invariant mux.go's own comment promises: for a
// registration WITHOUT UseRawPath+UnescapePathValues (the combination
// mux.go panics on at registration — see TestServeFiles_RawPathUnescape...
// below is intentionally not needed since ServeFiles itself refuses to
// register that combination), the name reaching FileSystem.Open, after
// http.FileServer's internal path.Clean, must never retain a literal ".."
// segment — i.e. must never be able to escape the FileSystem root.
type shimFS struct {
	onOpen func(name string)
}

func (s shimFS) Open(name string) (http.File, error) {
	if s.onOpen != nil {
		s.onOpen(name)
	}
	// http.FileServer only needs an error return to finish the request —
	// we don't need a working http.File to observe the name it asked for.
	// os.ErrNotExist (not http.ErrMissingFile) makes FileServer respond 404
	// instead of 500, matching what a real missing-file lookup would do.
	return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
}

func TestServeFiles_ShimFS_CleansTraversal(t *testing.T) {
	var servedName string
	r := mm.New()
	r.ServeFiles("/static/*filepath", shimFS{
		onOpen: func(name string) { servedName = name },
	})

	cases := []struct {
		input string
		note  string
	}{
		{"/static/../admin", "literal traversal segment"},
		{"/static/../../etc/passwd", "multi-level traversal"},
		{"/static/foo/../../bar", "traversal after a real segment"},
		{"/static/..%2fadmin", "percent-encoded slash — decoded to literal by net/url before reaching the router"},
		{"/static/%2e%2e/admin", "percent-encoded dots — decoded to literal by net/url"},
		{"/static/foo", "control: no traversal, must serve foo"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			servedName = ""
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+tc.input, nil)
			rec := httptest.NewRecorder()

			var panicked any
			func() {
				defer func() { panicked = recover() }()
				r.ServeHTTP(rec, req)
			}()
			if panicked != nil {
				t.Fatalf("PRF-SERVEFILES-PANIC: input=%q panicked: %v", tc.input, panicked)
			}

			t.Logf("input=%q servedName=%q status=%d note=%s", tc.input, servedName, rec.Code, tc.note)

			// The security boundary: whatever name reaches FileSystem.Open
			// must not contain a literal ".." path segment. http.FileServer
			// calls path.Clean on the URL.Path it is handed (mux.go's
			// ServeFiles sets r2.URL.Path = the raw captured filepath
			// param) BEFORE opening — path.Clean collapses ".." against a
			// rooted ("/...") path, so an absolute path can never resolve
			// to something outside the root via this mechanism. Verify
			// that promise directly rather than trusting the doc comment.
			for _, seg := range strings.Split(servedName, "/") {
				if seg == ".." {
					t.Errorf("PRF-SERVEFILES-ESCAPE: input=%q — FileSystem.Open was called with %q, which still contains a literal '..' segment after ServeFiles/http.FileServer's clean step",
						tc.input, servedName)
				}
			}
		})
	}
}
