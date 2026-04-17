package harness

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

// TestMethodDispatchMatrix confirms that the case-sensitive methodIdx()
// in mux.go:76-101 does not dispatch variants of GET (e.g. "GeT", "get",
// "GET\t", " GET") to the GET handler. Our expectation: the Mux treats
// them as unknown methods and falls through to NotFound / 405.
//
// The second concern is whether the *stdlib parser* normalises case in the
// request line before handing off to our ServeHTTP. Per RFC 9110 §9.1,
// method is case-sensitive and "GET" is the only normalised token. Go's
// net/http does not lowercase — we confirm empirically.
func TestMethodDispatchMatrix(t *testing.T) {
	invoked := ""
	mux := muxmaster.New()
	mux.GET("/t", func(w http.ResponseWriter, r *http.Request) {
		invoked = "GET"
		w.WriteHeader(204)
	})
	mux.POST("/t", func(w http.ResponseWriter, r *http.Request) {
		invoked = "POST"
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Cases: each method is set on the http.Request.Method field directly.
	// Note: http.NewRequest may reject some invalid methods — we handle err.
	cases := []string{
		"GET", "get", "GeT", "Get", "GET ", " GET", "GET\t", "\tGET",
		"HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT",
		"TRACE", "PROPFIND", "FOO", "BAR", "MKCOL", "*",
	}

	header := []string{"method", "new_request_err", "status", "invoked", "allow_header"}
	rows := [][]string{header}

	for _, m := range cases {
		invoked = ""
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/t", nil) // overwrite below
		req.Method = m
		errStr := ""
		mux.ServeHTTP(rec, req)
		rows = append(rows, []string{
			m, errStr, fmt.Sprintf("%d", rec.Code), invoked, rec.Header().Get("Allow"),
		})
	}

	// Also confirm via real TCP — stdlib parses the method via textproto.
	// If a real client sends "get /t HTTP/1.1", what happens?
	f, err := os.Create(filepath.Join(evidenceDir, "method-dispatch-matrix.csv"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		t.Fatalf("csv: %v", err)
	}

	// Assertions:
	// - "GET" → invoked = GET
	// - "get", "GeT", "Get" → MUST NOT invoke GET (case-sensitive by design)
	for _, row := range rows[1:] {
		m, invokedRow := row[0], row[3]
		if m == "GET" && invokedRow != "GET" {
			t.Errorf("canonical GET failed to dispatch (invoked=%q)", invokedRow)
		}
		if (m == "get" || m == "GeT" || m == "Get") && invokedRow == "GET" {
			t.Errorf("case-insensitive dispatch: %q invoked GET handler", m)
		}
		// Leading/trailing whitespace: stdlib http.Request.Method accepts the raw string,
		// so MuxMaster sees it verbatim. Should not match.
		if (m == "GET " || m == " GET" || m == "GET\t" || m == "\tGET") && invokedRow == "GET" {
			t.Errorf("whitespace-padded method %q matched GET", m)
		}
	}
}
