// Shared helpers for the middleware security harness.
package harness

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// readSource reads a file from disk (used for static checks against middleware source).
func readSource(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// rawHeaderDump serialises headers into canonical wire form for CRLF auditing.
func rawHeaderDump(h http.Header) string {
	var buf bytes.Buffer
	_ = h.Write(&buf)
	return buf.String()
}

// evidenceDir is the canonical path for evidence artefacts produced by the harness.
func evidenceDir() string {
	return "/data/dev/github.com/FlavioCFOliveira/MuxMaster/reports/middleware-security-reviewer/evidence/2026-04-17"
}

// writeTimingCSV dumps valid/bogus timing samples to the evidence CSV.
func writeTimingCSV(t *testing.T, name string, valid, bogus []time.Duration) {
	t.Helper()
	if err := os.MkdirAll(evidenceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(evidenceDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"sample_index", "valid_user_ns", "bogus_user_ns"})
	n := len(valid)
	if len(bogus) < n {
		n = len(bogus)
	}
	for i := 0; i < n; i++ {
		_ = w.Write([]string{
			strconv.Itoa(i),
			strconv.FormatInt(valid[i].Nanoseconds(), 10),
			strconv.FormatInt(bogus[i].Nanoseconds(), 10),
		})
	}
}

// writeCSV writes arbitrary rows to the evidence directory.
func writeCSV(t *testing.T, name string, rows [][]string) {
	t.Helper()
	if err := os.MkdirAll(evidenceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(evidenceDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	defer w.Flush()
	for _, row := range rows {
		_ = w.Write(row)
	}
}

// writeFile writes a verbatim blob to evidence.
func writeFile(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(evidenceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(evidenceDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
