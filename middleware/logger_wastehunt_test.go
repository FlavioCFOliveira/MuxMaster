// Regression tests for rmp task #251, sprint 18: WH-02 (single end-of-
// request clock read, allocation-free sanitisation and duration formatting)
// and WH-07 (statusRecorder delegates io.ReaderFrom so http.ServeContent's
// sendfile(2) fast path is not hidden behind Logger). Package middleware
// (white-box) because appendSanitisedForLog, appendLogDuration and the
// unexported statusRecorder/writerOnly types are not part of the public API.
package middleware

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestEquiv_AppendSanitisedForLog pins appendSanitisedForLog (WH-02) to be
// byte-for-byte identical to the pre-existing sanitiseForLog (still used by
// Recoverer) for every input, across a 200000-string corpus mixing control
// characters, invalid UTF-8, quotes, backslashes and multi-byte runes.
func TestEquiv_AppendSanitisedForLog(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{
		"a", "Z", "/", "%2f", " ", "\"", "\\", "\n", "\r", "\t", "\x00", "\x7f",
		"é", "日本", "\xff", "\xc3", "😀", "..", "?", "#",
	}
	randString := func(n int) string {
		var sb strings.Builder
		for range n {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return sb.String()
	}
	corpus := []string{
		"", "GET", "/api/v1/books/42", "/a b", "/\"q\"", "/back\\slash",
		"/nl\n", "\x00", "/é", "/\xff\xfe", "/日本語",
	}
	for range 200000 {
		corpus = append(corpus, randString(rng.IntN(12)))
	}
	for _, s := range corpus {
		want := sanitiseForLog(s)
		if got := string(appendSanitisedForLog([]byte("prefix"), s)); got != "prefix"+want {
			t.Fatalf("appendSanitisedForLog(%q) = %q, want %q", s, got, "prefix"+want)
		}
	}
}

// TestEquiv_AppendLogDuration pins appendLogDuration (WH-02) to be
// byte-for-byte identical to time.Duration.String() for zero, small,
// negative, fractional and extreme (min/max int64) durations plus 500000
// random values. If a future Go release changes Duration's textual format,
// this test fails immediately rather than silently drifting.
func TestEquiv_AppendLogDuration(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	ds := []time.Duration{
		0, 1, 999, 1000, 1001, 999999, 1000000, 1234567,
		time.Second - 1, time.Second, time.Minute, time.Hour,
		25*time.Hour + 3*time.Second + 7,
		-1, -1234567, -time.Hour,
		1<<63 - 1, -1 << 63,
	}
	for range 500000 {
		ds = append(ds, time.Duration(rng.Int64()>>uint(rng.IntN(63))))
	}
	for _, d := range ds {
		if got, want := string(appendLogDuration(nil, d)), d.String(); got != want {
			t.Fatalf("appendLogDuration(%d) = %q, want %q", int64(d), got, want)
		}
	}
}

// TestLogger_SingleClockRead_StillProducesValidTimestampAndDuration is a
// coarse sanity check that the single end-of-request clock read (WH-02)
// still yields a parseable RFC3339 timestamp and a parseable, non-negative
// duration, on top of the existing method/path/status field tests in
// middleware_test.go.
func TestLogger_SingleClockRead_StillProducesValidTimestampAndDuration(t *testing.T) {
	var buf bytes.Buffer
	h := Logger(&buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	fields := strings.Fields(strings.TrimSuffix(buf.String(), "\n"))
	if len(fields) != 5 {
		t.Fatalf("log line field count = %d, want 5: %q", len(fields), buf.String())
	}
	if _, err := time.Parse(time.RFC3339, fields[0]); err != nil {
		t.Fatalf("timestamp %q does not parse as RFC3339: %v", fields[0], err)
	}
	if fields[3] != strconv.Itoa(http.StatusTeapot) {
		t.Fatalf("status = %q, want %d", fields[3], http.StatusTeapot)
	}
	d, err := time.ParseDuration(fields[4])
	if err != nil {
		t.Fatalf("duration %q does not parse: %v", fields[4], err)
	}
	if d < time.Millisecond {
		t.Fatalf("duration %v is shorter than the handler's own sleep — clock reading is wrong", d)
	}
}

// readFromRecorder wraps httptest.ResponseRecorder with its own ReadFrom,
// so tests can observe whether statusRecorder.ReadFrom (WH-07) actually
// delegates to an underlying io.ReaderFrom instead of falling back to a
// generic io.Copy.
type readFromRecorder struct {
	*httptest.ResponseRecorder
	readFromCalled bool
}

func (rw *readFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	rw.readFromCalled = true
	return io.Copy(rw.ResponseRecorder, src)
}

func TestLogger_ReadFrom_DelegatesToUnderlyingReaderFrom(t *testing.T) {
	var logBuf bytes.Buffer
	inner := &readFromRecorder{ResponseRecorder: httptest.NewRecorder()}

	h := Logger(&logBuf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rf, ok := w.(io.ReaderFrom)
		if !ok {
			t.Fatal("statusRecorder does not implement io.ReaderFrom")
		}
		n, err := rf.ReadFrom(strings.NewReader("hello world"))
		if err != nil || n != int64(len("hello world")) {
			t.Fatalf("ReadFrom: n=%d err=%v", n, err)
		}
	}))
	h.ServeHTTP(inner, httptest.NewRequest(http.MethodGet, "/x", nil))

	if !inner.readFromCalled {
		t.Fatal("underlying ResponseWriter's ReadFrom was never called — the sendfile(2) fast path would be hidden (WH-07 regression)")
	}
	if inner.Body.String() != "hello world" {
		t.Fatalf("body = %q, want %q", inner.Body.String(), "hello world")
	}
	// Status capture is unchanged: an implicit 200 is recorded exactly like
	// a plain Write would, since WriteHeader was never called.
	if inner.Code != http.StatusOK {
		t.Fatalf("implicit status = %d, want %d", inner.Code, http.StatusOK)
	}
	fields := strings.Fields(logBuf.String())
	if fields[3] != "200" {
		t.Fatalf("logged status = %q, want 200", fields[3])
	}
}

// TestLogger_ReadFrom_FallsBackWhenUnderlyingLacksReaderFrom verifies the
// generic io.Copy fallback (via writerOnly) still delivers the exact bytes
// when the wrapped ResponseWriter does NOT implement io.ReaderFrom — the
// case httptest.ResponseRecorder itself represents — and that this fallback
// does not recurse (writerOnly strips ReaderFrom from view).
func TestLogger_ReadFrom_FallsBackWhenUnderlyingLacksReaderFrom(t *testing.T) {
	var logBuf bytes.Buffer
	rec := httptest.NewRecorder()

	h := Logger(&logBuf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rf, ok := w.(io.ReaderFrom)
		if !ok {
			t.Fatal("statusRecorder does not implement io.ReaderFrom")
		}
		n, err := rf.ReadFrom(strings.NewReader("fallback body"))
		if err != nil || n != int64(len("fallback body")) {
			t.Fatalf("ReadFrom: n=%d err=%v", n, err)
		}
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/y", nil))

	if rec.Body.String() != "fallback body" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "fallback body")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("implicit status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestLogger_ReadFrom_RespectsExplicitStatus confirms WriteHeader called
// before ReadFrom still wins — ReadFrom must not silently force 200.
func TestLogger_ReadFrom_RespectsExplicitStatus(t *testing.T) {
	var logBuf bytes.Buffer
	rec := httptest.NewRecorder()
	h := Logger(&logBuf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		rf := w.(io.ReaderFrom)
		if _, err := rf.ReadFrom(strings.NewReader("created body")); err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/z", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	fields := strings.Fields(logBuf.String())
	if fields[3] != "201" {
		t.Fatalf("logged status = %q, want 201", fields[3])
	}
}
