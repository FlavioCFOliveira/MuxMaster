// Package harness contains pre-release HTTP protocol security harnesses for
// MuxMaster. All tests in this file run via the standard go test toolchain
// against a live httptest.Server serving the production Mux.
//
// They exercise the RedirectTrailingSlash and RedirectFixedPath surfaces for
// CRLF injection, open-redirect and TSR-before-auth disclosure (H-007, H-020,
// H-025). Raw Location-header captures are written to evidence/2026-04-17/.
package harness

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// evidenceDir is resolved once relative to this test file.
const evidenceDir = "../evidence/2026-04-17"

// writeCSV appends a matrix of results to a named CSV under evidence/.
func writeCSV(t *testing.T, name string, rows [][]string) {
	t.Helper()
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	f, err := os.Create(filepath.Join(evidenceDir, name))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		t.Fatalf("write csv: %v", err)
	}
}

// containsCtl returns true if s contains CR, LF or NUL.
func containsCtl(s string) bool {
	return strings.ContainsAny(s, "\r\n\x00")
}

// TestRedirectMatrix sweeps the matrix of (request-target × redirect option)
// combinations against a Mux registered with both "/admin" and "/admin/"
// siblings and static routes. Captures the Location header for every case.
//
// Finding template: any Location header that contains \r, \n, NUL, an
// externally-controlled authority (//evil, /\evil, /../evil.com) or an
// encoded CRLF sequence that survives is recorded as a violation.
func TestRedirectMatrix(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/admin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.GET("/admin/subpage", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.GET("/evil.com/foo", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.GET("/a/b/c", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Path list — a mix of legitimate and hostile inputs.
	// The first element of the pair is a label for the CSV.
	paths := [][2]string{
		{"tsr_basic", "/admin/"},                                           // TSR to /admin
		{"tsr_a_b_c_slash", "/a/b/c/"},                                     // TSR to /a/b/c
		{"fixed_dot_slash", "/./admin"},                                    // RedirectFixedPath
		{"double_slash_admin", "//admin"},                                  // ambiguous — path.Clean → /admin
		{"slash_slash_evil", "//evil.com/foo"},                             // H-007 open redirect
		{"dot_dot_evil", "/x/../evil.com/foo"},                             // path.Clean reveals /evil.com/foo
		{"triple_slash", "///admin"},                                       // path.Clean → /admin
		{"encoded_crlf_in_path", "/admin%0D%0ASet-Cookie:%20evil=1%0D%0A"}, // CRLF injection attempt
		{"literal_crlf_in_path", "/admin\r\n"},                             // stdlib should reject at request line
		{"ansi_in_path", "/\x1b[2Jadmin"},                                  // ANSI escape
		{"backslash_host", "/\\evil.com/"},                                 // backslash confusion
		{"admin_mixed_case", "/ADMIN"},                                     // mixed-case for RedirectFixedPath
		{"tsr_with_slash_slash", "/admin//"},                               // trailing double slash
		{"fragment_in_target", "/./admin#x"},                               // fragment handling
		{"query_encoded_crlf", "/admin?x=%0D%0AFoo:%20bar"},                // CRLF in query
		{"tab_in_path", "/admin\t"},                                        // horizontal tab
		{"null_in_path", "/admin\x00"},                                     // null byte
		{"slash_dot_dot_slash", "/../admin"},                               // climb + clean
		{"very_long_path", "/admin/" + strings.Repeat("A", 4096)},          // long path
		{"percent_encoded_slash", "/admin%2f"},                             // encoded slash
	}

	// Matrix columns.
	header := []string{
		"case", "request_target", "client_get_err",
		"status", "location", "location_has_ctl", "location_external_auth",
		"full_response_hex",
	}
	rows := [][]string{header}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, p := range paths {
		label, target := p[0], p[1]

		// http.NewRequest cannot always be trusted to preserve control bytes,
		// so we use srv.URL + target concatenation and let the stdlib parse it.
		// The result is exactly what a real attacker could issue.
		reqURL := srv.URL + target

		req, err := http.NewRequest("GET", reqURL, nil)
		errStr := ""
		status := 0
		location := ""
		locHasCtl := ""
		locExt := ""

		if err != nil {
			errStr = err.Error()
		} else {
			resp, gerr := client.Do(req)
			if gerr != nil {
				errStr = gerr.Error()
			} else {
				status = resp.StatusCode
				location = resp.Header.Get("Location")
				locHasCtl = fmt.Sprintf("%t", containsCtl(location))
				locExt = fmt.Sprintf("%t", externalishLocation(location))
				resp.Body.Close()
			}
		}

		rows = append(rows, []string{
			label, target, errStr,
			fmt.Sprintf("%d", status), location, locHasCtl, locExt, "",
		})

		// Fail-fast assertions: never permit CRLF/NUL in Location.
		if containsCtl(location) {
			t.Errorf("[%s] CRLF/NUL present in Location header: %q", label, location)
		}
	}

	writeCSV(t, "redirect-matrix.csv", rows)
}

// externalishLocation returns true when the Location string could be
// interpreted by a user-agent as pointing off-origin. This is a conservative
// heuristic: we flag strings starting with "//", "\\", or containing "://"
// of an http-ish scheme that is *not* the same origin (we can't easily know
// the same origin at test time, so we just flag any absolute URL).
func externalishLocation(loc string) bool {
	if loc == "" {
		return false
	}
	if strings.HasPrefix(loc, "//") {
		return true
	}
	if strings.HasPrefix(loc, "\\") {
		return true
	}
	if strings.Contains(loc, "://") {
		return true
	}
	return false
}

// TestRedirectTrailingSlashNoAuthBypass directly reproduces H-025:
// a request that would need auth middleware to be applied first, but the
// TSR redirect is emitted by ServeHTTP before any user middleware runs,
// because the trailing-slash redirect happens when no handler is matched.
func TestRedirectTrailingSlashNoAuthBypass(t *testing.T) {
	mux := muxmaster.New()

	// Auth middleware that would deny everything.
	authInvoked := 0
	denyAll := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authInvoked++
			http.Error(w, "forbidden", 403)
		})
	}
	mux.Use(denyAll)
	mux.GET("/admin/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	// Register a decoy route that does not exist.
	// Request to /admin should hit the TSR and emit a 301 to /admin/ *before*
	// any middleware check, revealing route existence.
	req := httptest.NewRequest("GET", "/admin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	f, err := os.Create(filepath.Join(evidenceDir, "h025-tsr-pre-auth.txt"))
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "# H-025 evidence\n")
	fmt.Fprintf(f, "request: GET /admin\n")
	fmt.Fprintf(f, "registered route: /admin/ (behind denyAll middleware)\n")
	fmt.Fprintf(f, "status: %d\n", rec.Code)
	fmt.Fprintf(f, "location: %s\n", rec.Header().Get("Location"))
	fmt.Fprintf(f, "auth_middleware_invocations: %d\n", authInvoked)
	fmt.Fprintf(f, "body: %q\n", rec.Body.String())

	// CONFIRM finding: status 301, Location /admin/, auth_invoked = 0.
	if rec.Code == http.StatusMovedPermanently && authInvoked == 0 {
		t.Logf("H-025 CONFIRMED: TSR emits %d redirect to %q before auth runs (invocations=%d)",
			rec.Code, rec.Header().Get("Location"), authInvoked)
	} else {
		t.Logf("H-025 NOT REPRODUCED in this configuration (code=%d, auth=%d)",
			rec.Code, authInvoked)
	}
}

// TestRedirectFixedPathOpenRedirect drills H-007 / H-020: RedirectFixedPath
// via path.Clean on "//evil.com/foo" → "/evil.com/foo". The concern is
// whether r.URL.String() produces a Location header that a browser treats as
// cross-origin.
func TestRedirectFixedPathOpenRedirect(t *testing.T) {
	mux := muxmaster.New()
	mux.GET("/evil.com/foo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	tests := []struct {
		name   string
		target string
	}{
		{"double_slash_host", "//evil.com/foo"},
		{"triple_slash_host", "///evil.com/foo"},
		{"backslash_host", "/\\evil.com/foo"},
		{"dot_dot_host", "/x/../evil.com/foo"},
		{"tsr_form", "/evil.com/foo/"},
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	f, err := os.Create(filepath.Join(evidenceDir, "h007-h020-redirect-fixedpath.txt"))
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	defer f.Close()

	for _, tc := range tests {
		resp, err := client.Get(srv.URL + tc.target)
		if err != nil {
			fmt.Fprintf(f, "[%s] %s err=%v\n", tc.name, tc.target, err)
			continue
		}
		loc := resp.Header.Get("Location")
		fmt.Fprintf(f, "[%s] target=%q status=%d location=%q externalish=%t\n",
			tc.name, tc.target, resp.StatusCode, loc, externalishLocation(loc))
		resp.Body.Close()

		if externalishLocation(loc) {
			t.Errorf("[%s] OPEN REDIRECT: Location %q appears off-origin", tc.name, loc)
		}
	}
}
