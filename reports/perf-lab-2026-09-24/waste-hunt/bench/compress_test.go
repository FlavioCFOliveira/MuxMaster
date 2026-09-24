package bench

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
	"testing"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: Compress allocates, per gzip-accepting request, a fresh
// *gzipResponseWriter and grows its sniff buffer with append from nil
// (8 → 16 → … → 8192 B when the handler writes in small chunks, as
// html/template and fmt.Fprintf do), copying the accumulated bytes at every
// growth step. compressAlt is a replica of middleware/compress.go that
// changes ONLY the storage: the writer and a fixed 8 KiB sniff array are
// recycled through a sync.Pool (the http.ResponseWriter contract already
// forbids use of w after ServeHTTP returns, which is what makes this legal;
// the Logger middleware pools its statusRecorder on the same basis).

const (
	altMinCompressSize = 1024
	altSniffBufSize    = 8192
)

type gzipRWAlt struct {
	http.ResponseWriter
	pool    *sync.Pool
	gz      *gzip.Writer
	arr     [altSniffBufSize]byte
	buf     []byte
	status  int
	decided bool
	skip    bool
}

func (g *gzipRWAlt) WriteHeader(code int) { g.status = code }

func (g *gzipRWAlt) Write(b []byte) (int, error) {
	if g.decided {
		if g.skip {
			return g.ResponseWriter.Write(b)
		}
		return g.gz.Write(b)
	}
	room := altSniffBufSize - len(g.buf)
	if len(b) <= room {
		g.buf = append(g.buf, b...)
		return len(b), nil
	}
	g.buf = append(g.buf, b[:room]...)
	if err := g.commit(); err != nil {
		return 0, err
	}
	rest := b[room:]
	if g.skip {
		n, err := g.ResponseWriter.Write(rest)
		return room + n, err
	}
	n, err := g.gz.Write(rest)
	return room + n, err
}

func (g *gzipRWAlt) commit() error {
	g.decided = true
	hdr := g.Header()
	hdr.Add("Vary", "Accept-Encoding")
	if altIsCompressed(hdr.Get("Content-Type")) || hdr.Get("Content-Encoding") != "" || len(g.buf) < altMinCompressSize {
		g.skip = true
		if g.status != 0 {
			g.ResponseWriter.WriteHeader(g.status)
		}
		_, err := g.ResponseWriter.Write(g.buf)
		g.buf = nil
		return err
	}
	hdr.Set("Content-Encoding", "gzip")
	hdr.Del("Content-Length")
	if g.status != 0 {
		g.ResponseWriter.WriteHeader(g.status)
	}
	g.gz = g.pool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
	_, err := g.gz.Write(g.buf)
	g.buf = nil
	return err
}

func altIsCompressed(ct string) bool {
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch strings.TrimSpace(strings.ToLower(ct)) {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif",
		"image/heic", "image/heif",
		"video/mp4", "video/webm", "video/ogg", "video/quicktime",
		"audio/mpeg", "audio/ogg", "audio/aac", "audio/flac", "audio/webm",
		"application/zip", "application/x-gzip", "application/gzip",
		"application/x-bzip2", "application/x-xz", "application/zstd",
		"application/x-7z-compressed", "application/x-rar-compressed",
		"application/pdf",
		"font/woff", "font/woff2":
		return true
	}
	return false
}

func compressAlt(level int) func(http.Handler) http.Handler {
	gzPool := &sync.Pool{New: func() any {
		gz, err := gzip.NewWriterLevel(nil, level)
		if err != nil {
			panic(err)
		}
		return gz
	}}
	rwPool := &sync.Pool{New: func() any { return new(gzipRWAlt) }}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			g := rwPool.Get().(*gzipRWAlt)
			g.ResponseWriter, g.pool = w, gzPool
			g.buf = g.arr[:0]
			next.ServeHTTP(g, r)
			if !g.decided {
				_ = g.commit()
			}
			if !g.skip && g.gz != nil {
				_ = g.gz.Close()
				gzPool.Put(g.gz)
			}
			*g = gzipRWAlt{} // drop references (the 8 KiB array is re-zeroed too)
			rwPool.Put(g)
		})
	}
}

// Workloads: "small" is a 600 B JSON body in one Write (below the 1 KiB
// threshold → passthrough); "chunked-12KiB" is a 12 KiB HTML page written
// in 64-byte pieces (template-like), which gets compressed.
var (
	smallJSON = []byte(`{"id":1,"title":"The Go Programming Language","author":"Donovan","year":2015,"genre":"tech","summary":"` + strings.Repeat("x", 480) + `"}`)
	htmlChunk = []byte(strings.Repeat("<li>entry</li>", 4) + "\n        ")[:64]
)

var smallHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header()["Content-Type"] = []string{"application/json"}
	_, _ = w.Write(smallJSON)
})

var chunkedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header()["Content-Type"] = []string{"text/html; charset=utf-8"}
	for range 12 * 1024 / 64 {
		_, _ = w.Write(htmlChunk)
	}
})

func BenchmarkCompress(b *testing.B) {
	run := func(b *testing.B, mwf func(http.Handler) http.Handler, h http.Handler) {
		hh := mwf(h)
		r := realisticRequest(http.MethodGet, "/guestbook")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			hh.ServeHTTP(w, r)
			w.reset()
		}
	}
	for _, wl := range []struct {
		name string
		h    http.Handler
	}{{"small-600B", smallHandler}, {"chunked-12KiB", chunkedHandler}} {
		b.Run(wl.name+"/current", func(b *testing.B) { run(b, mw.Compress(gzip.BestSpeed), wl.h) })
		b.Run(wl.name+"/alternative", func(b *testing.B) { run(b, compressAlt(gzip.BestSpeed), wl.h) })
	}
}
