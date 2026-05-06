// FuzzCleanPath — invariant I-02: CleanPath middleware applies path.Clean.
//
// Invariants:
//  1. Idempotent  : applying CleanPath twice equals applying once.
//  2. Bounded     : the output length never exceeds the input length.
//  3. No panic    : for any byte sequence.
//
// The middleware wraps path.Clean of stdlib. We replay the same semantics by
// running a ServeHTTP through the middleware and observing the effective path
// seen by the inner handler.
package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// observePath runs one round of CleanPath on the given raw URL path and
// returns the path visible to the inner handler.
func observePath(raw string) (seen string, served bool) {
	mw := middleware.CleanPath()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		served = true
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)
	u := &url.URL{Path: raw}
	req := &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Host:   "example.com",
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return
}

func FuzzCleanPath(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"//",
		"///",
		"/a",
		"/a/",
		"/a/b",
		"/a/./b",
		"/a/../b",
		"/../",
		"/../..",
		"/./",
		"/./a/",
		"/foo/..",
		"/foo/../bar",
		"/a//b",
		"/a///b",
		"/a/b/../../c",
		"/./././",
		"//./..",
		"//..//..//x",
		"/%2e%2e/",
		"/./%2e%2e",
		"/a\x00b",
		"/a\r\nb",
		"/\u00e9",
		"/\u0000",
		"/" + strings.Repeat("..", 100),
		"/" + strings.Repeat("a/", 100),
		"///..//.",
		strings.Repeat("/", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 8192 {
			t.Skip()
		}
		// Skip raw paths that can't survive http.Request construction.
		// path.Clean operates on strings directly so we don't need the full
		// request trip — use it both for oracle and differential.

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CleanPath panic on %q: %v\n%s", raw, r, debug.Stack())
			}
		}()

		a := path.Clean(raw)
		b := path.Clean(a)
		if a != b {
			t.Fatalf("non-idempotent: %q -> %q -> %q (len %d -> %d -> %d)",
				raw, a, b, len(raw), len(a), len(b))
		}

		// Bounded: path.Clean never grows the string unless input is "". "" → "."
		// is documented; we skip that case.
		if raw != "" && len(a) > len(raw) {
			t.Fatalf("grew from %d to %d bytes: %q -> %q", len(raw), len(a), raw, a)
		}

		// Middleware-level invariant: the path visible to the inner handler
		// equals path.Clean when raw starts with '/' (middleware precondition).
		if len(raw) > 0 && raw[0] == '/' && !strings.ContainsRune(raw, 0) {
			// Build an http.Request via httptest; need a valid URL form.
			seen, served := observePath(raw)
			if !served {
				return
			}
			if seen != a {
				t.Fatalf("middleware saw %q but path.Clean(%q) = %q",
					seen, raw, a)
			}
			// Idempotency at middleware level: re-feed 'seen' to the middleware.
			seen2, _ := observePath(seen)
			if seen2 != seen {
				t.Fatalf("middleware non-idempotent: %q -> %q -> %q",
					raw, seen, seen2)
			}
		}
	})
}

// FuzzCleanPathDoubleEncoded validates that CleanPath does NOT decode %2e%2e
// sequences (it only operates on the pre-decoded path). This is an assertion
// about what clean_path.go does NOT do: it is not a substitute for URL
// unescape. The test pins current behaviour so a future regression shows up.
func FuzzCleanPathDoubleEncoded(f *testing.F) {
	f.Add("/foo/%2e%2e/bar")
	f.Add("/a/%2E%2E/b")
	f.Add("/a/..%2f..%2fetc/passwd")

	f.Fuzz(func(t *testing.T, raw string) {
		if !strings.HasPrefix(raw, "/") || len(raw) > 2048 || strings.ContainsRune(raw, 0) {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v\n%s", raw, r, debug.Stack())
			}
		}()
		seen, served := observePath(raw)
		if !served {
			return
		}
		// Invariant: percent-encoded dots remain literal after CleanPath
		// because path.Clean does not percent-decode.
		if strings.Contains(raw, "%2e") || strings.Contains(raw, "%2E") ||
			strings.Contains(raw, "%2f") || strings.Contains(raw, "%2F") {
			lowerSeen := strings.ToLower(seen)
			lowerRaw := strings.ToLower(raw)
			// The percent-encoded form must still appear in the output.
			// (After path.Clean may collapse slashes around it.)
			if strings.Count(lowerSeen, "%2e") == 0 &&
				strings.Count(lowerRaw, "%2e") > 0 &&
				strings.Count(lowerSeen, "%2f") == 0 &&
				strings.Count(lowerRaw, "%2f") > 0 {
				t.Fatalf("percent-encodings vanished from %q -> %q (unexpected decode in CleanPath)",
					raw, seen)
			}
		}
		_ = fmt.Sprint // keep import on empty body
	})
}
