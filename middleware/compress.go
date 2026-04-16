package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

const minCompressSize = 1024

type gzipResponseWriter struct {
	http.ResponseWriter
	gz     *gzip.Writer
	buf    []byte
	status int
	done   bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.status = code
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.done {
		g.buf = append(g.buf, b...)
		return len(b), nil
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) flush(w http.ResponseWriter, pool *sync.Pool) {
	if len(g.buf) < minCompressSize {
		// write uncompressed
		if g.status != 0 {
			w.WriteHeader(g.status)
		}
		_, _ = w.Write(g.buf)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Del("Content-Length")
	w.Header().Add("Vary", "Accept-Encoding")
	if g.status != 0 {
		w.WriteHeader(g.status)
	}
	gz := pool.Get().(*gzip.Writer)
	gz.Reset(w)
	_, _ = gz.Write(g.buf)
	_ = gz.Close()
	pool.Put(gz)
}

// Compress compresses responses with gzip when the client accepts it. Panics on invalid level.
func Compress(level int) func(http.Handler) http.Handler {
	// validate level by creating a test writer
	testGz, err := gzip.NewWriterLevel(io.Discard, level)
	if err != nil {
		panic("middleware: invalid gzip level: " + err.Error())
	}
	testGz.Close()

	pool := &sync.Pool{
		New: func() any {
			gz, _ := gzip.NewWriterLevel(io.Discard, level)
			return gz
		},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			grw := &gzipResponseWriter{ResponseWriter: w}
			next.ServeHTTP(grw, r)
			grw.done = true
			grw.flush(w, pool)
		})
	}
}
