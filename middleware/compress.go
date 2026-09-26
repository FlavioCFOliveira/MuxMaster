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

// gzipResponseWriter accumulates up to sniffBufSize bytes in arr — a fixed
// array that is part of THIS struct's own allocation — before deciding
// whether to compress. buf is a slice into arr; it never grows beyond arr's
// capacity, so accumulating the sniff buffer never reallocates.
//
// WH-03: the writer itself, together with arr, is recycled through a
// sync.Pool (Compress's rwPool, below) instead of being allocated fresh
// (`&gzipResponseWriter{...}`, escaping to the heap) on every
// gzip-accepting request. Pooling a
// ResponseWriter wrapper is safe under the same contract statusRecorder
// (logger.go) already relies on: net/http guarantees a ResponseWriter is
// never used after the handler that received it returns, and this
// middleware only returns the pooled value to the pool from its own
// deferred cleanup, which runs after next.ServeHTTP(grw, r) has returned.
type gzipResponseWriter struct {
	http.ResponseWriter
	pool        *sync.Pool   // *gzip.Writer pool, shared across requests
	gz          *gzip.Writer // non-nil once compression is committed
	arr         [sniffBufSize]byte
	buf         []byte // bounded sniff buffer, backed by arr (at most sniffBufSize bytes)
	status      int
	decided     bool // true once compress/skip decision is made
	skip        bool // true = pass through uncompressed
	wroteHeader bool // true once the status has been fixed by WriteHeader or Write
}

// WriteHeader records the response status. As in net/http, the FIRST call
// wins: a later WriteHeader call (e.g. a handler that writes an error page
// after already writing its real status) is a no-op, matching what a bare
// http.ResponseWriter does. Without this guard, a handler such as
// http.ServeContent — which calls WriteHeader(200) internally after a
// NotFound handler already wrote WriteHeader(404) — would let the second
// call silently overwrite the first, serving 200 to a gzip-accepting
// client while every other client correctly saw 404 (MM-2026-0254).
//
// 1xx informational codes (RFC 8297 Early Hints, 100 Continue) are exempt
// from the first-wins lock, exactly like net/http's own *response.WriteHeader
// (net/http/server.go: "if code >= 100 && code <= 199 && code !=
// StatusSwitchingProtocols"). Without this exemption, a handler emitting a
// 103 Early Hints response before its real final status (e.g. 401, 403)
// would have that final status silently discarded: commit() only ever
// forwards ONE status to the wrapped ResponseWriter, so the real status
// would never reach it and the response would fall back to an implicit 200
// OK once bytes are written — an access-control result downgraded to
// success purely because the client advertised gzip support
// (MID-COMPRESS-1).
func (g *gzipResponseWriter) WriteHeader(code int) {
	if code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols {
		return
	}
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.status = code
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	// A Write with no prior WriteHeader implicitly sends 200, exactly like a
	// bare http.ResponseWriter — and, per the same first-wins rule as
	// WriteHeader itself, fixes the status so a WriteHeader call arriving
	// after bytes have already started flowing is a no-op too.
	if !g.wroteHeader {
		g.wroteHeader = true
		g.status = http.StatusOK
	}
	if g.decided {
		if g.skip {
			return g.ResponseWriter.Write(b)
		}
		return g.gz.Write(b)
	}
	// Still accumulating the sniff buffer.
	room := sniffBufSize - len(g.buf)
	if len(b) <= room {
		g.buf = append(g.buf, b...) // nosemgrep: muxmaster-unbounded-buffer-append — bounded by sniffBufSize check above
		return len(b), nil
	}
	// Enough data — make the compress/skip decision now.
	g.buf = append(g.buf, b[:room]...) // nosemgrep: muxmaster-unbounded-buffer-append — only room bytes, bounded by sniffBufSize
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

// Flush implements http.Flusher so streaming responses (e.g. Server-Sent
// Events) written through Compress can reach the client without waiting for
// the handler to return. Without this method, http.ResponseController's
// Flush walks past Unwrap all the way to the real connection and flushes
// RAW, unbuffered bytes from a compressor that has not written its own
// framing yet — or, worse, bypasses gzip entirely, corrupting the stream
// for a client that already saw Content-Encoding: gzip.
//
// If the compress/skip decision has not been made yet (the sniff buffer has
// not filled), Flush forces it now using whatever has been written so far —
// a stalled SSE stream cannot wait for sniffBufSize bytes to accumulate
// before its first event reaches the client. This picks the same headers
// commit always would (Vary, and Content-Encoding/Content-Length only when
// the accumulated bytes already clear minCompressSize); a stream whose
// first flush happens before 1024 bytes have been written is served
// uncompressed for its entire remaining lifetime, exactly as a short
// non-streamed response would be — gzip framing cannot be switched on
// retroactively once bytes have reached the client.
func (g *gzipResponseWriter) Flush() {
	if !g.decided {
		_ = g.commit()
	}
	if !g.skip && g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the wrapped ResponseWriter so http.ResponseController (and
// any other Unwrap-aware caller) can reach optional interfaces this wrapper
// does not itself implement (http.Hijacker, http.Pusher). Flush is
// implemented directly above rather than left to Unwrap alone, because
// compressed output must be flushed through the gzip.Writer — not the raw
// connection — to reach the client uncorrupted.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

// commit flushes the sniff buffer and sets decided=true.
func (g *gzipResponseWriter) commit() error {
	g.decided = true
	hdr := g.Header()
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
		_, err := g.ResponseWriter.Write(g.buf) // #nosec G705 — buf is the application's own response body, not user input
		g.buf = nil
		return err
	}
	if len(g.buf) < minCompressSize {
		g.skip = true
		if g.status != 0 {
			g.ResponseWriter.WriteHeader(g.status)
		}
		_, err := g.ResponseWriter.Write(g.buf) // #nosec G705 — buf is the application's own response body, not user input
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
// OPERATIONAL (DOS-2026-0007): the compress middleware buffers up to 8 KiB
// per stalled connection while sniffing whether the response is large
// enough to compress. Operators MUST configure http.Server.ReadHeaderTimeout
// and http.Server.WriteTimeout (and a connection cap via a Listener limit)
// to bound the total memory held by N stalled connections; the middleware
// itself does not enforce a per-connection timeout.
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

	// WH-03: rwPool recycles *gzipResponseWriter values — including their
	// embedded 8 KiB sniff array — across requests, so a gzip-accepting
	// request no longer allocates a fresh wrapper (it previously escaped
	// to the heap on every call) nor grows its sniff buffer by repeated
	// append-from-nil reallocation.
	rwPool := &sync.Pool{
		New: func() any { return new(gzipResponseWriter) },
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			grw := rwPool.Get().(*gzipResponseWriter) //nolint:forcetypeassert // pool.New always returns *gzipResponseWriter
			grw.ResponseWriter = w
			grw.pool = pool
			grw.buf = grw.arr[:0]
			defer func() {
				grw.close()
				// Reset every field — including re-zeroing the 8 KiB
				// array — before returning to the pool, so no header,
				// writer or body byte from this request is reachable
				// from the next one that gets this value (pool hygiene).
				// This runs even if the handler panicked: the defer
				// still fires while the panic unwinds this frame, before
				// any Recoverer further up the chain regains control.
				*grw = gzipResponseWriter{}
				rwPool.Put(grw)
			}()
			next.ServeHTTP(grw, r)
		})
	}
}
