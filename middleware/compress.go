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
	hdr := g.ResponseWriter.Header()
	// MSR-2026-0060: Vary: Accept-Encoding must always be emitted when this
	// middleware is in scope, even if the response was too small to compress
	// — otherwise a CDN may serve the small uncompressed body to a client
	// that expected (and would receive) gzip on the next byte-larger response.
	hdr.Add("Vary", "Accept-Encoding")
	// MM-2026-0053: skip compression for already-compressed payload types.
	// The size of the wrapped body can grow slightly under double-compression
	// and the CPU cost is wasted.
	if isAlreadyCompressedMIME(hdr.Get("Content-Type")) || hdr.Get("Content-Encoding") != "" {
		g.skip = true
		if g.status != 0 {
			g.ResponseWriter.WriteHeader(g.status)
		}
		_, err := g.ResponseWriter.Write(g.buf)
		g.buf = nil
		return err
	}
	if len(g.buf) < minCompressSize {
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
	g.gz = g.pool.Get().(*gzip.Writer) //nolint:forcetypeassert // pool.New always returns *gzip.Writer
	g.gz.Reset(g.ResponseWriter)
	_, err := g.gz.Write(g.buf)
	g.buf = nil
	return err
}

// isAlreadyCompressedMIME returns true for content types whose payloads are
// already compressed at rest — applying gzip provides no meaningful size
// reduction and may slightly inflate the body. Match is prefix-based so
// charset suffixes (e.g. "image/png; charset=binary") are handled.
func isAlreadyCompressedMIME(ct string) bool {
	if ct == "" {
		return false
	}
	// Trim parameters (everything after ';').
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch ct {
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
//
// SECURITY (BREACH / DOS-2026-0006): do NOT echo user-controlled input
// alongside a secret (OAuth2 scope, CSRF token, session ID, JWT) inside a
// gzip-compressed response body. Compression amplifies tiny size differences
// that depend on whether the user's input matches a prefix of the secret —
// this is the BREACH oracle (Cohen's d > 10 measured in
// reports/dos-resilience-tester/harness/breach_oracle_test.go), letting an
// attacker recover the secret character-by-character with ~2 requests per
// character.
//
// Mitigations, in order of preference:
//
//  1. Do not compress endpoints that echo user input near secrets — wrap
//     them with a different middleware chain that excludes Compress.
//  2. Move secrets out of the response body (set them in headers, cookies,
//     or separate API endpoints not reachable via attacker-controlled input).
//  3. Add variable-length random padding (>= 256 bytes, length randomised
//     per request) to the response body. Validated by
//     TestBREACHOracleWithRandomPadding (Cohen's d drops below 0.03).
//
// MuxMaster cannot apply these mitigations on the operator's behalf because
// they require knowledge of which fields are secret vs user-controlled.
// See SECURITY.md "BREACH mitigation" for the full pattern.
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
