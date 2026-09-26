// Benchmarks for the six middlewares touched by rmp tasks #251/#252,
// sprint 18 (waste-hunt WH-01, WH-02, WH-03, WH-06, WH-07, WH-11, WH-12).
// Compared against the baseline commit with `benchstat` per
// reports/perf-lab-2026-09-24/waste-hunt/results/fixes/251-252.txt.
package middleware_test

import (
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func benchPrefixes(ss []string) []*netip.Prefix {
	out := make([]*netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p := netip.MustParsePrefix(s)
		out = append(out, &p)
	}
	return out
}

// ── shared benchmark helpers ────────────────────────────────────────────

// discardRW is a minimal http.ResponseWriter with a reusable header map, so
// benchmarks measure the middleware, not httptest.ResponseRecorder's own
// allocations.
type discardRW struct {
	h      http.Header
	status int
	n      int
}

func newDiscardRW() *discardRW { return &discardRW{h: make(http.Header, 16)} }

func (d *discardRW) Header() http.Header         { return d.h }
func (d *discardRW) WriteHeader(code int)        { d.status = code }
func (d *discardRW) Write(b []byte) (int, error) { d.n += len(b); return len(b), nil }
func (d *discardRW) reset() {
	clear(d.h)
	d.status = 0
	d.n = 0
}

var benchNop = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

func realisticRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	r.Header.Set("Accept-Language", "en-GB,en;q=0.9,pt;q=0.8")
	r.Header.Set("Cache-Control", "no-cache")
	r.Header.Set("Connection", "keep-alive")
	r.Header.Set("Cookie", "session=0123456789abcdef0123456789abcdef; theme=dark")
	r.Header.Set("Referer", "https://example.com/index.html")
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	return r
}

// ── WH-01: ThrottlePerIP ────────────────────────────────────────────────

func peerHost(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func BenchmarkThrottlePerIP(b *testing.B) {
	const limit = 50
	run := func(b *testing.B, h http.Handler) {
		r := realisticRequest(http.MethodGet, "/api/v1/books/1")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			h.ServeHTTP(w, r)
		}
	}
	b.Run("sequential-one-client", func(b *testing.B) {
		run(b, middleware.ThrottlePerIPCapped(limit, 10*time.Second, middleware.DefaultThrottlePerIPMaxTableSize, peerHost)(benchNop))
	})
	runMany := func(b *testing.B, h http.Handler) {
		reqs := make([]*http.Request, 256)
		for i := range reqs {
			reqs[i] = realisticRequest(http.MethodGet, "/api/v1/books/1")
			reqs[i].RemoteAddr = "198.51.100." + strconv.Itoa(i%250) + ":40000"
		}
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			h.ServeHTTP(w, reqs[i&255])
		}
	}
	b.Run("many-clients-no-overlap", func(b *testing.B) {
		runMany(b, middleware.ThrottlePerIPCapped(limit, 10*time.Second, middleware.DefaultThrottlePerIPMaxTableSize, peerHost)(benchNop))
	})
}

// ── WH-02 / WH-07: Logger ───────────────────────────────────────────────

func BenchmarkLogger(b *testing.B) {
	h := middleware.Logger(io.Discard)(benchNop)
	r := realisticRequest(http.MethodGet, "/api/v1/books/42/reviews")
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

// ── Recoverer: no-panic hot path (rmp #276, sprint 20) ───────────────────
//
// Measures the cost Recoverer adds to a request that never panics — the
// overwhelming majority of traffic through any deployment that wraps
// routes with Recoverer. This is the path the O-14 fix (started-tracking
// wrapper, pooled via recovererWriterPool) must not regress.

func BenchmarkRecoverer_NoPanic(b *testing.B) {
	h := middleware.Recoverer()(benchNop)
	r := realisticRequest(http.MethodGet, "/api/v1/books/42/reviews")
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

func BenchmarkRecovererWithLogger_NoPanic(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := middleware.RecovererWithLogger(logger)(benchNop)
	r := realisticRequest(http.MethodGet, "/api/v1/books/42/reviews")
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

// ── WH-03: Compress ─────────────────────────────────────────────────────

var (
	compressSmallJSON    = []byte(`{"id":1,"title":"The Go Programming Language","author":"Donovan","year":2015,"genre":"tech","summary":"` + strings.Repeat("x", 480) + `"}`)
	compressHTMLChunk    = []byte(strings.Repeat("<li>entry</li>", 4) + "\n        ")[:64]
	compressSmallHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = []string{"application/json"}
		_, _ = w.Write(compressSmallJSON)
	})
	compressChunkedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = []string{"text/html; charset=utf-8"}
		for range 12 * 1024 / 64 {
			_, _ = w.Write(compressHTMLChunk)
		}
	})
)

func BenchmarkCompress(b *testing.B) {
	run := func(b *testing.B, h http.Handler) {
		hh := middleware.Compress(gzip.BestSpeed)(h)
		r := realisticRequest(http.MethodGet, "/guestbook")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			hh.ServeHTTP(w, r)
			w.reset()
		}
	}
	b.Run("small-600B", func(b *testing.B) { run(b, compressSmallHandler) })
	b.Run("chunked-12KiB", func(b *testing.B) { run(b, compressChunkedHandler) })
}

// ── WH-11: RealIP ───────────────────────────────────────────────────────

func BenchmarkRealIP(b *testing.B) {
	trusted := benchPrefixes([]string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})
	for _, xff := range []struct{ name, v string }{
		{"1-hop", "203.0.113.9"},
		{"3-hops", "198.51.100.23, 203.0.113.9, 10.0.0.7"},
	} {
		h := middleware.RealIP(trusted...)(benchNop)
		r := realisticRequest(http.MethodGet, "/api/v1/books/1")
		r.Header.Set("X-Forwarded-For", xff.v)
		w := newDiscardRW()
		b.Run(xff.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				r.RemoteAddr = "127.0.0.1:54321"
				h.ServeHTTP(w, r)
			}
		})
	}
}

// ── WH-12: APIKey ───────────────────────────────────────────────────────

func BenchmarkAPIKeyHit(b *testing.B) {
	keys := map[string]string{"key-alice": "alice", "key-bob": "bob"}
	h := middleware.APIKey(middleware.APIKeyOptions{Keys: keys})(benchNop)
	r := realisticRequest(http.MethodGet, "/api/profile")
	r.Header.Set("X-API-Key", "key-alice")
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

// ── TSC-2026-0002: BasicAuth constant-time user lookup ─────────────────
//
// BasicAuth (rmp #290 / TSC-2026-0002) replaced its map[string][32]byte
// credential lookup with an unordered []basicAuthEntry scanned in FULL on
// every request, to remove the user-enumeration timing oracle inherent to
// Go's map lookup. This is O(n) in the number of registered users, always —
// this benchmark quantifies the per-user cost of that scan at 1, 10 and 100
// registered users so a regression (or an unexpectedly steep slope) is
// visible in benchstat, not just in the timing harness.
func BenchmarkBasicAuth(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		creds := make(map[string]string, n)
		for i := range n {
			creds[strconv.Itoa(i)] = "password-" + strconv.Itoa(i)
		}
		// Authenticate as the LAST registered user on every iteration: since
		// the scan never short-circuits, this is not expected to be worse
		// than authenticating as the first — the benchmark exists to catch
		// a regression that reintroduces early-exit behaviour as much as to
		// measure the steady-state cost.
		lastUser := strconv.Itoa(n - 1)
		lastPass := "password-" + lastUser
		h := middleware.BasicAuth("realm", creds)(benchNop)
		r := realisticRequest(http.MethodGet, "/admin")
		r.SetBasicAuth(lastUser, lastPass)
		w := newDiscardRW()
		b.Run(strconv.Itoa(n)+"-users/hit", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				h.ServeHTTP(w, r)
				w.reset()
			}
		})
	}

	for _, n := range []int{1, 10, 100} {
		creds := make(map[string]string, n)
		for i := range n {
			creds[strconv.Itoa(i)] = "password-" + strconv.Itoa(i)
		}
		h := middleware.BasicAuth("realm", creds)(benchNop)
		r := realisticRequest(http.MethodGet, "/admin")
		r.SetBasicAuth("nonexistent-user", "anything")
		w := newDiscardRW()
		b.Run(strconv.Itoa(n)+"-users/miss", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				h.ServeHTTP(w, r)
				w.reset()
			}
		})
	}
}

// ── WH-06: JWTAuth ──────────────────────────────────────────────────────

func hs256TokenForBench(secret []byte) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pl, _ := json.Marshal(map[string]any{"sub": "u1", "name": "alice", "iat": time.Now().Unix(), "exp": time.Now().Add(24 * time.Hour).Unix()})
	p := base64.RawURLEncoding.EncodeToString(pl)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(hdr + "." + p))
	return hdr + "." + p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func BenchmarkJWTAuthHS256(b *testing.B) {
	secret := []byte("dev-secret-change-in-production")
	h := middleware.JWTAuth(middleware.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}, RequireExpiry: true})(benchNop)
	r := realisticRequest(http.MethodGet, "/api/me")
	r.Header.Set("Authorization", "Bearer "+hs256TokenForBench(secret))
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}
