package bench

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: Logger's statusRecorder embeds http.ResponseWriter, so the
// wrapped writer only exposes Header/Write/WriteHeader. net/http's
// *response implements io.ReaderFrom (sendfile(2) for *os.File sources,
// splice for sockets); once hidden, http.ServeContent's io.CopyN falls back
// to io.Copy with a per-request heap buffer (up to 32 KiB) and a
// read(2)+write(2) pair per 32 KiB instead of one sendfile(2).
//
// loggerRF is a replica of Logger whose recorder ALSO implements
// io.ReaderFrom by delegation (status capture unchanged: ReadFrom writes an
// implicit 200 exactly like Write does).

type statusRecorderRF struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorderRF) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type writerOnly struct{ io.Writer }

func (r *statusRecorderRF) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(writerOnly{r.ResponseWriter}, src)
}

var statusRecorderRFPool = sync.Pool{New: func() any { return new(statusRecorderRF) }}

var loggerRFBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 192); return &b }}

// loggerRF is a VERBATIM replica of middleware.Logger (same three clock
// reads, same QuoteToASCII sanitisation, same pools) except that its
// recorder type also implements io.ReaderFrom. It isolates exactly the
// effect of hiding ReaderFrom.
func loggerRF(out io.Writer) func(http.Handler) http.Handler {
	sanitise := func(s string) string { q := strconv.QuoteToASCII(s); return q[1 : len(q)-1] }
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := statusRecorderRFPool.Get().(*statusRecorderRF)
			rec.ResponseWriter = w
			rec.status = http.StatusOK
			next.ServeHTTP(rec, r)
			status := rec.status
			rec.ResponseWriter = nil
			rec.status = 0
			statusRecorderRFPool.Put(rec)
			bufp := loggerRFBufPool.Get().(*[]byte)
			buf := (*bufp)[:0]
			buf = time.Now().AppendFormat(buf, time.RFC3339)
			buf = append(buf, ' ')
			buf = append(buf, sanitise(r.Method)...)
			buf = append(buf, ' ')
			buf = append(buf, sanitise(r.URL.Path)...)
			buf = append(buf, ' ')
			buf = strconv.AppendInt(buf, int64(status), 10)
			buf = append(buf, ' ')
			buf = append(buf, time.Since(start).String()...)
			buf = append(buf, '\n')
			_, _ = out.Write(buf)
			*bufp = buf[:0]
			loggerRFBufPool.Put(bufp)
		})
	}
}

func fileServeFixture(tb testing.TB) (dir string) {
	dir = tb.TempDir()
	small := bytes.Repeat([]byte("a"), 965)            // static-site assets/style.css size
	large := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	if err := os.WriteFile(filepath.Join(dir, "small.css"), small, 0o644); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.bin"), large, 0o644); err != nil {
		tb.Fatal(err)
	}
	// Intermediate sizes, to locate where delegating ReadFrom starts to pay.
	for name, n := range map[string]int{"16k.js": 16 << 10, "128k.js": 128 << 10} {
		if err := os.WriteFile(filepath.Join(dir, name), large[:n], 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

func BenchmarkFileServe(b *testing.B) {
	dir := fileServeFixture(b)
	variants := []struct {
		name string
		use  func(http.Handler) http.Handler
	}{
		{"no-logger", nil},
		{"Logger-current", mw.Logger(io.Discard)},
		{"Logger-with-ReaderFrom", loggerRF(io.Discard)},
	}
	for _, v := range variants {
		for _, f := range []string{"small.css", "16k.js", "128k.js", "large.bin"} {
			b.Run(v.name+"/"+f, func(b *testing.B) {
				m := mm.New()
				if v.use != nil {
					m.Use(v.use)
				}
				m.ServeFiles("/assets/*filepath", http.Dir(dir))
				srv := httptest.NewServer(m)
				defer srv.Close()
				client := &http.Client{Transport: &http.Transport{DisableCompression: true, MaxIdleConnsPerHost: 4}, Timeout: 10 * time.Second}
				url := srv.URL + "/assets/" + f
				buf := make([]byte, 64<<10)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					resp, err := client.Get(url)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := io.CopyBuffer(io.Discard, resp.Body, buf); err != nil {
						b.Fatal(err)
					}
					_ = resp.Body.Close()
					if resp.StatusCode != 200 {
						b.Fatal("status " + strconv.Itoa(resp.StatusCode))
					}
				}
			})
		}
	}
}
