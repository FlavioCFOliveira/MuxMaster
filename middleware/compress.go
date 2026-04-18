package middleware

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

const (
	minCompressSize = 1024
	sniffBufSize    = 8192
)

type gzipResponseWriter struct {
	http.ResponseWriter
	pool    *sync.Pool
	gz      *gzip.Writer // non-nil once compression is committed
	buf     []byte       // bounded sniff buffer (at most sniffBufSize bytes)
	status  int
	decided bool // true once compress/skip decision is made
	skip    bool // true = pass through uncompressed
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.status = code
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if g.decided {
		if g.skip {
			return g.ResponseWriter.Write(b)
		}
		return g.gz.Write(b)
	}
	// Still accumulating the sniff buffer.
	room := sniffBufSize - len(g.buf)
	if len(b) <= room {
		g.buf = append(g.buf, b...)
		return len(b), nil
	}
	// Enough data — make the compress/skip decision now.
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

// commit flushes the sniff buffer and sets decided=true.
func (g *gzipResponseWriter) commit() error {
	g.decided = true
	if len(g.buf) < minCompressSize {
		g.skip = true
		if g.status != 0 {
			g.ResponseWriter.WriteHeader(g.status)
		}
		_, err := g.ResponseWriter.Write(g.buf)
		g.buf = nil
		return err
	}
	g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.Header().Del("Content-Length")
	g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
	if g.status != 0 {
		g.ResponseWriter.WriteHeader(g.status)
	}
	g.gz = g.pool.Get().(*gzip.Writer) //nolint:forcetypeassert // pool.New always returns *gzip.Writer
	g.gz.Reset(g.ResponseWriter)
	_, err := g.gz.Write(g.buf)
	g.buf = nil
	return err
}

// close is called after the handler returns.
func (g *gzipResponseWriter) close() {
	if !g.decided {
		// Handler returned without writing sniffBufSize bytes — decide now.
		_ = g.commit()
	}
	if !g.skip && g.gz != nil {
		_ = g.gz.Close()
		g.pool.Put(g.gz)
		g.gz = nil
	}
}

// Compress compresses responses with gzip when the client accepts it.
// Responses smaller than 1024 bytes are passed through uncompressed.
// Uses streaming compression — memory usage is bounded regardless of response size.
// Panics on invalid compression level.
func Compress(level int) func(http.Handler) http.Handler {
	pool := &sync.Pool{
		New: func() any {
			gz, err := gzip.NewWriterLevel(nil, level)
			if err != nil {
				panic("middleware: invalid gzip level: " + err.Error())
			}
			return gz
		},
	}
	// Validate level eagerly.
	_ = pool.Get().(*gzip.Writer) //nolint:forcetypeassert // pool.New always returns *gzip.Writer; errcheck not applicable to discarded value

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			grw := &gzipResponseWriter{ResponseWriter: w, pool: pool}
			defer grw.close()
			next.ServeHTTP(grw, r)
		})
	}
}
