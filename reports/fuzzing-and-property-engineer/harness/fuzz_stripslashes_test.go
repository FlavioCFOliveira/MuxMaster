// fuzz_stripslashes_test.go — rmp task #274 part 5b (O-14 residual gap
// flagged in fuzz_o14_restored_test.go, part 3/4).
//
// FuzzStripSlashesIdempotency was deleted by commit 5f804fa with no
// restored equivalent — fuzz_o14_restored_test.go catalogued every other
// dropped Fuzz*/TestProp_* target with a current-equivalent mapping and
// flagged this one as a residual gap because none existed.
//
// At the time it was deleted, the original test pinned a KNOWN BUG rather
// than the specified behaviour: StripSlashes stripped only ONE trailing
// slash (FPE-003 / findings.md MM-2026-0025), so the original explicitly
// skipped any input ending in "//" to avoid failing on that (then-current,
// then-accepted) non-idempotent behaviour.
//
// middleware/strip_slashes.go now strips ALL trailing slashes from Path in
// a loop and findings.md records MM-2026-0025 as fixed (2026-09-25),
// backed by the deterministic regression test
// middleware/middleware_test.go::TestStripSlashes_MultipleTrailing. This
// restoration therefore asserts FULL idempotency with no carve-out for
// multi-trailing-slash input — see specification/middleware-stdlib.md §12
// (statements 57-60): StripSlashes removes trailing slashes, never touches
// the root "/", never mutates the original request, and (implicitly, since
// RawPath is documented as "the equivalently stripped value") must keep
// RawPath a valid net/url encoding of the resulting Path.
//
// While building this restoration, fuzzing found a second, previously
// untracked defect in the SAME function: a decoded trailing slash spelled
// as a percent-encoded "%2F"/"%2f" in RawPath (a client sending the raw
// request-target "/a%2f" — net/url decodes this to Path="/a/",
// RawPath="/a%2f", per RFC 3986 §2.1) was left byte-untouched by the
// RawPath-stripping loop, which only recognised a literal '/' byte. Path
// became "/a" while RawPath stayed "/a%2f" (which still decodes to "/a/"),
// desynchronising the two — any RawPath-based consumer downstream
// (UseRawPath=true routing, logging, a reverse proxy) would then disagree
// with Path about the request's actual target. Fixed minimally in
// middleware/strip_slashes.go (stripTrailingPathSeparators: walks RawPath
// from the end, consuming either a literal '/' or a "%2F"/"%2f" triplet per
// stripped Path character) with a dedicated regression test,
// middleware/middleware_test.go::TestStripSlashes_EncodedTrailingSlashKeepsRawPathInSync.
// Reported prominently in this task's final report (not filed under a new
// findings.md ID here — this agent does not edit findings.md).
//
// Invariants (invariants.md I-17):
//   - StripSlashes never panics for any Path/RawPath input;
//   - the root path "/" is never stripped further;
//   - applying StripSlashes once vs twice yields identical
//     (r.URL.Path, r.URL.RawPath) — MM-2026-0025 idempotency, now
//     unconditional;
//   - RawPath stays either empty or a valid net/url encoding of Path
//     (u.EscapedPath() == u.RawPath) after stripping.
package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// validRawPath reports whether rawPath is either empty (use the default
// encoding of path) or a valid net/url encoding of path, i.e. the pair a
// real *http.Request built by net/http's URL parser could produce
// (u.EscapedPath() returns rawPath verbatim).
func validRawPath(path, rawPath string) bool {
	if rawPath == "" {
		return true
	}
	u := &url.URL{Path: path, RawPath: rawPath}
	return u.EscapedPath() == rawPath
}

// stripSlashesOnce builds a request with the given Path/RawPath, runs it
// through StripSlashes, and returns the resulting Path/RawPath observed by
// the wrapped handler.
func stripSlashesOnce(path, rawPath string) (outPath, outRawPath string) {
	mw := middleware.StripSlashes()
	var gotPath, gotRawPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawPath = r.URL.RawPath
		w.WriteHeader(http.StatusOK)
	})
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Path: path, RawPath: rawPath},
		Header:     make(http.Header),
		Body:       http.NoBody,
		Host:       "example.com",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	return gotPath, gotRawPath
}

func FuzzStripSlashesIdempotency(f *testing.F) {
	f.Add("/a/", "")
	f.Add("/", "")
	f.Add("/a", "")
	f.Add("/a///", "")
	f.Add("/a//", "")
	f.Add("//", "")
	f.Add("///", "")
	f.Add("/a/b//", "")
	// Regression seeds for the encoded-trailing-slash desync found in this
	// task (see file header) — kept as permanent corpus entries.
	f.Add("/a/", "/a%2f")
	f.Add("/a/", "/a%2F")
	f.Add("/a/b/", "/a/b%2f")
	f.Add("/a//", "/a%2f%2f")
	f.Add("/a//", "/a%2f/")
	// Regression seed for the original FPE-003 bug class (multi-trailing
	// slash), now expected to be fully idempotent.
	f.Add("/a////////", "")

	f.Fuzz(func(t *testing.T, path, rawPath string) {
		if len(path) > 8192 || len(rawPath) > 8192 {
			return
		}
		if containsControlBytes(path) || containsControlBytes(rawPath) {
			return
		}
		if path == "" || path[0] != '/' {
			return // net/http's URL parser never produces a non-absolute Path
		}
		if !validRawPath(path, rawPath) {
			return // not a Path/RawPath pair net/http could ever produce
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("StripSlashes PANIC: path=%q rawPath=%q r=%v\n%s",
					path, rawPath, r, debug.Stack())
			}
		}()

		path1, rawPath1 := stripSlashesOnce(path, rawPath)

		// I-17a: the root path is never stripped further than "/", and the
		// result is never empty.
		if path == "/" && path1 != "/" {
			t.Fatalf("StripSlashes stripped the root path: %q -> %q", path, path1)
		}
		if path1 == "" {
			t.Fatalf("StripSlashes produced an empty Path from %q", path)
		}

		// I-17b: Path/RawPath stay a valid net/url pair after stripping.
		if !validRawPath(path1, rawPath1) {
			t.Fatalf("StripSlashes desynchronised Path/RawPath: in path=%q rawPath=%q -> out path=%q rawPath=%q",
				path, rawPath, path1, rawPath1)
		}

		// I-17c (MM-2026-0025): idempotency — applying StripSlashes a
		// second time is a no-op.
		path2, rawPath2 := stripSlashesOnce(path1, rawPath1)
		if path1 != path2 || rawPath1 != rawPath2 {
			t.Fatalf("StripSlashes non-idempotent: %q/%q -> %q/%q -> %q/%q",
				path, rawPath, path1, rawPath1, path2, rawPath2)
		}
	})
}
