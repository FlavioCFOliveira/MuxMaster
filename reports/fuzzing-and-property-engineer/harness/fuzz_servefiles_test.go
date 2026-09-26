package harness

// fuzz_servefiles_test.go — FPE-2026-006 / O-6 (rmp task #265): ServeFiles
// had no fuzz or property target. ServeFiles (mux.go ~line 832) delegates
// to http.FileServer(root) after rewriting the request's URL.Path to the
// captured catch-all param value (see mux.go's CDX-S8-002 comment and
// specification/static-files.md item 6 and 15). http.FileServer applies
// path.Clean to the rewritten path before opening any file, which is the
// documented protection against ".." traversal for the default
// (UseRawPath=false) configuration that ServeFiles supports (a
// UseRawPath=true + UnescapePathValues=true registration already panics at
// startup — see the ServeFiles doc comment — so that combination is out of
// scope here).
//
// This harness fuzzes the requested path with traversal-shaped payloads
// (raw "..", encoded %2e%2e, %2f, backslashes, double-encoding, NUL,
// absolute-looking paths) and asserts that no response ever discloses a
// file that lives outside the served root.
//
// Invariants added:
//   I-SERVEFILES-01 — ServeFiles never discloses a file outside its root,
//                      for any requested path.
//   I-SERVEFILES-02 — ServeFiles never panics for any requested path.
//
// Out of scope / separately documented (not fuzzed, not asserted as a
// MuxMaster invariant): symlinks placed inside the served root that point
// outside it. net/http's http.Dir godoc explicitly warns that it "will
// follow symlinks pointing out of the directory tree" — this is a
// documented, inherited characteristic of the Go standard library, not
// something ServeFiles adds or removes (specification/static-files.md item
// 6: "MuxMaster does not add additional protection beyond what
// http.FileServer provides"). See TestServeFiles_SymlinkFollowsUpstreamBehavior.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"pgregory.net/rapid"
)

// servefilesSecret is planted OUTSIDE the served root; its appearance in
// any response body is the traversal-escape signal.
const servefilesSecret = "TOP-SECRET-OUTSIDE-ROOT-3f9a7c2b"

// servefilesInsideMarker is planted INSIDE the served root as a sanity
// control (proves the mux is actually serving files, not just 404ing
// everything).
const servefilesInsideMarker = "inside-root-ok-9d1e"

// buildServeFilesMux lays out:
//
//	<tmp>/secret.txt          -> servefilesSecret   (sibling of, OUTSIDE, public/)
//	<tmp>/public/hello.txt    -> servefilesInsideMarker
//
// and registers ServeFiles("/static/*filepath", http.Dir(<tmp>/public)) on
// a fresh Mux. Returns the mux; the temp tree is cleaned up by t.TempDir().
func buildServeFilesMux(t testing.TB) *mm.Mux {
	t.Helper()
	base := t.TempDir()
	publicDir := filepath.Join(base, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte(servefilesSecret), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "hello.txt"), []byte(servefilesInsideMarker), 0o644); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	mux := mm.New()
	mux.ServeFiles("/static/*filepath", http.Dir(publicDir))
	return mux
}

// FuzzServeFiles exercises ServeFiles with path-traversal-shaped payloads.
// Invariants I-SERVEFILES-01 (no escape) / I-SERVEFILES-02 (no panic).
func FuzzServeFiles(f *testing.F) {
	// Seed corpus — each value is the segment requested AFTER "/static/"
	// (the Fuzz closure below always forces the "/static/" prefix so the
	// payload actually reaches the file server instead of 404ing at the
	// route tree).
	seeds := []string{
		"hello.txt",                      // control: must serve inside content
		"../secret.txt",                  // classic traversal
		"../../secret.txt",               // over-traversal
		"..%2Fsecret.txt",                // encoded separator
		"%2e%2e/secret.txt",              // encoded dots
		"%2e%2e%2fsecret.txt",            // encoded dots + separator
		"%2E%2E%2Fsecret.txt",            // uppercase encoding
		"%252e%252e/secret.txt",          // double-encoded dots
		"%252e%252e%252fsecret.txt",      // double-encoded, fully
		"%2e%2e%2f%2e%2e%2fetc%2fpasswd", // deep encoded traversal
		"....//secret.txt",               // repeated-dot bypass attempt
		"..\\secret.txt",                 // backslash separator (Windows-style)
		"%5c..%5csecret.txt",             // encoded backslash
		"/etc/passwd",                    // absolute-looking path
		"//etc/passwd",                   // double leading slash
		"./secret.txt",                   // single dot
		"secret.txt",                     // sibling name, no traversal token
		"",                               // directory listing of /static/
		"./../../../../../../../../../../etc/passwd", // excessive over-traversal
		"%00secret.txt",                          // encoded NUL
		"..;/secret.txt",                         // path-parameter-style bypass attempt (Java-ism)
		"..%c0%af secret.txt",                    // overlong UTF-8 slash attempt
		strings.Repeat("../", 64) + "secret.txt", // pathological depth
	}
	for _, s := range seeds {
		f.Add(s)
	}

	mux := buildServeFilesMux(f)

	f.Fuzz(func(t *testing.T, rawPath string) {
		if len(rawPath) > 4096 {
			t.Skip()
		}
		reqPath := "/static/" + strings.TrimPrefix(rawPath, "/")

		req, err := buildRequest(http.MethodGet, reqPath)
		if err != nil {
			return // not even a routable URL — nothing to serve
		}

		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("I-SERVEFILES-02 PANIC: path=%q r=%v\n%s", reqPath, r, debug.Stack())
				}
			}()
			mux.ServeHTTP(rec, req)
		}()

		body, _ := io.ReadAll(rec.Body)
		if strings.Contains(string(body), servefilesSecret) {
			t.Fatalf("I-SERVEFILES-01 VIOLATION: secret disclosed outside served root: path=%q status=%d body=%q",
				reqPath, rec.Code, string(body))
		}
	})
}

// TestProp_ServeFilesNoEscape is a structured (rapid) complement to
// FuzzServeFiles: it systematically composes traversal depth × encoding
// style × separator style × target, which explores the combinatorial space
// more evenly than byte-mutation alone. Same invariant (I-SERVEFILES-01).
func TestProp_ServeFilesNoEscape(t *testing.T) {
	mux := buildServeFilesMux(t)

	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 10).Draw(t, "depth")
		dotStyle := rapid.SampledFrom([]string{
			"..", "%2e%2e", "%2E%2E", "%2e.", ".%2e", "....", "%252e%252e",
		}).Draw(t, "dotStyle")
		sepStyle := rapid.SampledFrom([]string{
			"/", "%2f", "%2F", "\\", "%5c", "%c0%af", "//",
		}).Draw(t, "sepStyle")
		target := rapid.SampledFrom([]string{
			"secret.txt", "etc/passwd", "hello.txt", "", "..",
		}).Draw(t, "target")

		var b strings.Builder
		b.WriteString("/static/")
		for range depth {
			b.WriteString(dotStyle)
			b.WriteString(sepStyle)
		}
		b.WriteString(target)
		reqPath := b.String()

		req, err := buildRequest(http.MethodGet, reqPath)
		if err != nil {
			return
		}

		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("I-SERVEFILES-02 PANIC: path=%q r=%v\n%s", reqPath, r, debug.Stack())
				}
			}()
			mux.ServeHTTP(rec, req)
		}()

		body, _ := io.ReadAll(rec.Body)
		if strings.Contains(string(body), servefilesSecret) {
			t.Fatalf("I-SERVEFILES-01 VIOLATION: secret disclosed outside served root: path=%q status=%d",
				reqPath, rec.Code)
		}
	})
}

// TestServeFiles_InsideRootIsServed is a sanity control for
// FuzzServeFiles/TestProp_ServeFilesNoEscape: proves the fixture actually
// serves content from inside the root, so an all-404 fuzz run cannot be
// mistaken for "no escape found" by construction.
func TestServeFiles_InsideRootIsServed(t *testing.T) {
	mux := buildServeFilesMux(t)
	req := httptest.NewRequest(http.MethodGet, "/static/hello.txt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for in-root file, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != servefilesInsideMarker {
		t.Fatalf("unexpected body: %q", body)
	}
}

// TestServeFiles_SymlinkFollowsUpstreamBehavior documents (does not treat
// as a MuxMaster defect) that a symlink placed inside the served root and
// pointing outside it is followed by http.FileServer/http.Dir, exactly as
// net/http's own godoc for Dir warns: "Dir will follow symlinks pointing
// out of the directory tree, which can be especially dangerous if serving
// from a directory in which users are able to create arbitrary symlinks."
// ServeFiles adds no protection beyond http.FileServer
// (specification/static-files.md item 6), so this is expected, current,
// upstream (net/http) behaviour — not a new finding for this task. The
// assertion pins that expectation: if it ever flips (e.g. a future Go
// stdlib adds symlink containment, or ServeFiles starts wrapping root in
// something that blocks it), this test fails and prompts a spec/SECURITY.md
// review rather than a silent, undetected behaviour change either way.
func TestServeFiles_SymlinkFollowsUpstreamBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	base := t.TempDir()
	publicDir := filepath.Join(base, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	secretPath := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secretPath, []byte(servefilesSecret), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	linkPath := filepath.Join(publicDir, "escape-link")
	if err := os.Symlink(filepath.Join("..", "secret.txt"), linkPath); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	mux := mm.New()
	mux.ServeFiles("/static/*filepath", http.Dir(publicDir))

	req := httptest.NewRequest(http.MethodGet, "/static/escape-link", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || string(body) != servefilesSecret {
		t.Fatalf("expected documented upstream symlink-follow behaviour (200, secret content); got status=%d body=%q — "+
			"if this is a deliberate improvement, update specification/static-files.md item 6 and SECURITY.md to match",
			rec.Code, body)
	}
}
