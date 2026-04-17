package fuzz

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mmmw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestDifferentialTable walks every corpus line through the four routers
// and writes a CSV evidence file documenting the (router -> result) map.
// Security-material divergences (traversal reaching /admin*) cause the
// test to fail; everything else is logged as an informational row.
func TestDifferentialTable(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "corpora", "*.txt"))
	if len(files) == 0 {
		t.Skip("no corpora")
	}
	outDir := filepath.Join("..", "evidence", "2026-04-17")
	_ = os.MkdirAll(outDir, 0o755)
	out, err := os.Create(filepath.Join(outDir, "differential.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	w := csv.NewWriter(out)
	defer w.Flush()
	_ = w.Write([]string{
		"corpus", "path",
		"muxmaster_status", "muxmaster_handler", "muxmaster_loc",
		"httprouter_status", "httprouter_handler", "httprouter_loc",
		"chi_status", "chi_handler", "chi_loc",
		"bunrouter_status", "bunrouter_handler", "bunrouter_loc",
		"divergence_class",
	})

	var bypasses []string
	var crlfLocs []string
	var divCount int
	var totalLines int

	for _, file := range files {
		name := filepath.Base(file)
		lines := loadSeedsFromPath(t, file)
		for _, raw := range lines {
			totalLines++
			if !isAcceptablePath(raw) {
				continue
			}
			res := routeAll(raw)
			cls := classifyDivergence(raw, res)
			if cls != "equivalent" && cls != "all-404" && cls != "all-matched-same" {
				divCount++
			}
			_ = w.Write([]string{
				name, raw,
				itoa(res[rkMuxMaster].status), res[rkMuxMaster].handlerID, res[rkMuxMaster].location,
				itoa(res[rkHTTPRouter].status), res[rkHTTPRouter].handlerID, res[rkHTTPRouter].location,
				itoa(res[rkChi].status), res[rkChi].handlerID, res[rkChi].location,
				itoa(res[rkBunRouter].status), res[rkBunRouter].handlerID, res[rkBunRouter].location,
				cls,
			})
			for _, r := range res {
				if strings.ContainsAny(r.location, "\r\n") {
					crlfLocs = append(crlfLocs, raw)
				}
			}
			// Classify muxmaster-only traversal acceptance.
			if containsDotDotSegment(raw) {
				h := res[rkMuxMaster].handlerID
				if h == "admin" || h == "admin.panel" || h == "admin.panel.settings" {
					bypasses = append(bypasses, fmt.Sprintf("path=%q handler=%q", raw, h))
				}
			}
		}
	}
	t.Logf("differential: %d total lines processed, %d divergences", totalLines, divCount)
	if len(crlfLocs) > 0 {
		t.Errorf("%d inputs produced CRLF in Location: %v", len(crlfLocs), crlfLocs[:min(5, len(crlfLocs))])
	}
	if len(bypasses) > 0 {
		t.Errorf("muxmaster accepted %d traversal paths onto /admin*: %v",
			len(bypasses), bypasses[:min(10, len(bypasses))])
	}
}

// classifyDivergence labels a result-tuple with one of:
//   - equivalent: every router reached the same handler
//   - all-404: all routers rejected
//   - all-matched-same: all routed to same symbolic handler
//   - status-split: status codes differ
//   - handler-split: different handlers matched
//   - location-split: redirect target disagreements
//   - muxmaster-unique-match: only muxmaster accepted
//   - muxmaster-unique-reject: muxmaster is the only reject
func classifyDivergence(path string, res [rkCount]routeResult) string {
	var allRejected = true
	var uniqueMatch = false
	for _, r := range res {
		if r.status >= 200 && r.status < 300 {
			allRejected = false
		}
	}
	if allRejected {
		return "all-404"
	}

	// Same handler for all matched routers?
	sameHandler := true
	ref := res[rkMuxMaster].handlerID
	for _, r := range res {
		if r.handlerID != ref {
			sameHandler = false
			break
		}
	}
	if sameHandler {
		return "all-matched-same"
	}

	mmMatched := res[rkMuxMaster].matched
	othersMatched := 0
	for i, r := range res {
		if i == int(rkMuxMaster) {
			continue
		}
		if r.matched {
			othersMatched++
		}
	}
	if mmMatched && othersMatched == 0 {
		return "muxmaster-unique-match"
	}
	if !mmMatched && othersMatched >= 2 {
		return "muxmaster-unique-reject"
	}
	_ = uniqueMatch
	return "handler-split"
}

func loadSeedsFromPath(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, decodeEscapes(line))
	}
	return out
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

// TestMiddlewareInteractionMatrix builds a 2×2×2×2 matrix of
// (clean_path, strip_slashes, RedirectTrailingSlash, RedirectFixedPath)
// and for each combination records how every corpus payload is routed.
// Output is a CSV at evidence/2026-04-17/middleware-matrix.csv. The test
// asserts that no combination allows a ".."-bearing payload to reach
// /admin*.
func TestMiddlewareInteractionMatrix(t *testing.T) {
	outDir := filepath.Join("..", "evidence", "2026-04-17")
	_ = os.MkdirAll(outDir, 0o755)
	out, err := os.Create(filepath.Join(outDir, "middleware-matrix.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	w := csv.NewWriter(out)
	defer w.Flush()
	_ = w.Write([]string{
		"clean_path", "strip_slashes", "redirectTS", "redirectFP",
		"path", "status", "handler", "location", "verdict",
	})

	files, _ := filepath.Glob(filepath.Join("..", "corpora", "*.txt"))
	var corpus []string
	for _, f := range files {
		lines := loadSeedsFromPath(t, f)
		// Bound the corpus so the matrix stays in a tolerable size.
		// We pick the first N from every file and rely on curation.
		if len(lines) > 60 {
			lines = lines[:60]
		}
		corpus = append(corpus, lines...)
	}

	var bypassCount int
	for _, cp := range []bool{false, true} {
		for _, ss := range []bool{false, true} {
			for _, rts := range []bool{false, true} {
				for _, rfp := range []bool{false, true} {
					m := buildMuxMaster(rts, rfp, false, false, false)
					var h http.Handler = m
					if ss {
						h = mmmw.StripSlashes()(h)
					}
					if cp {
						h = mmmw.CleanPath()(h)
					}
					for _, p := range corpus {
						if !isAcceptablePath(p) {
							continue
						}
						req := buildRequest(p)
						if req == nil {
							continue
						}
						rec := httptest.NewRecorder()
						func() {
							defer func() { _ = recover() }()
							h.ServeHTTP(rec, req)
						}()
						res := rec.Result()
						handlerID := res.Header.Get("X-Handler")
						loc := res.Header.Get("Location")
						verdict := "ok"
						if containsDotDotSegment(p) && (handlerID == "admin" || handlerID == "admin.panel" || handlerID == "admin.panel.settings") {
							verdict = "BYPASS"
							bypassCount++
						}
						if strings.ContainsAny(loc, "\r\n") {
							verdict = "CRLF-LOC"
						}
						_ = w.Write([]string{
							btoa(cp), btoa(ss), btoa(rts), btoa(rfp),
							p, fmt.Sprintf("%d", res.StatusCode), handlerID, loc, verdict,
						})
						res.Body.Close()
					}
				}
			}
		}
	}
	if bypassCount > 0 {
		t.Errorf("%d middleware combinations allowed a traversal bypass onto /admin*", bypassCount)
	}
}

func btoa(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
