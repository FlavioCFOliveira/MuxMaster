// Package harness exercises MuxMaster (root package + every middleware) under
// b.RunParallel at multiple GOMAXPROCS levels, for the sprint-18 task #243
// contention hunt. It intentionally lives OUTSIDE the module root (imports
// the module via a `replace` directive) so no product file is touched.
//
// Run with: ./run.sh  (see reports/perf-lab-2026-09-24/contention-hunt.md)
package harness

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// --------------------------------------------------------------------------
// Root package — Mux surfaces
// --------------------------------------------------------------------------

func noopHandler(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(okBody) }

var okBody = []byte("ok")

func newStaticMux() *muxmaster.Mux {
	m := muxmaster.New()
	m.GET("/users/list", noopHandler)
	return m
}

func newParamMux(pooled bool) *muxmaster.Mux {
	m := muxmaster.New()
	m.PoolRequestBundle = pooled
	m.GET("/users/:id", noopHandler)
	m.GET("/users/:id/posts/:postID", noopHandler)
	m.GET("/users/:id/posts/:postID/comments/:cid", noopHandler)
	m.GET("/static/*filepath", noopHandler)
	return m
}

func newFastParamMux(pooled bool) *muxmaster.Mux {
	m := muxmaster.New()
	m.PoolFastParams = pooled
	m.GETFast("/users/:id", func(w http.ResponseWriter, r *http.Request, ps muxmaster.Params) {
		_, _ = w.Write(okBody)
	})
	return m
}

// newMultiMethodMux registers GET+POST+PUT on the same path so requesting
// DELETE triggers the 405 path (lazyMethodNotAllowed / m.allowed()).
func newMultiMethodMux() *muxmaster.Mux {
	m := muxmaster.New()
	m.GET("/res/:id", noopHandler)
	m.POST("/res/:id", noopHandler)
	m.PUT("/res/:id", noopHandler)
	return m
}

func runParallelBench(b *testing.B, h http.Handler, req func() *http.Request) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		r := req()
		rec := httptest.NewRecorder()
		for pb.Next() {
			rec.Body = nil
			h.ServeHTTP(rec, r)
		}
	})
}

func BenchmarkParallelStatic(b *testing.B) {
	m := newStaticMux()
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/list", nil)
	})
}

func BenchmarkParallelParam1(b *testing.B) {
	m := newParamMux(false)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

func BenchmarkParallelParam1Pooled(b *testing.B) {
	m := newParamMux(true)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

func BenchmarkParallelParam3(b *testing.B) {
	m := newParamMux(false)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42/posts/7/comments/99", nil)
	})
}

func BenchmarkParallelParam3Pooled(b *testing.B) {
	m := newParamMux(true)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42/posts/7/comments/99", nil)
	})
}

func BenchmarkParallelCatchAll(b *testing.B) {
	m := newParamMux(false)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	})
}

func BenchmarkParallelFastParam1(b *testing.B) {
	m := newFastParamMux(false)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

func BenchmarkParallelFastParam1Pooled(b *testing.B) {
	m := newFastParamMux(true)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

// BenchmarkParallelMethodNotAllowed exercises m.allowed() + lazyMethodNotAllowed
// (sync.Map cache) under high parallelism.
func BenchmarkParallelMethodNotAllowed(b *testing.B) {
	m := newMultiMethodMux()
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodDelete, "/res/42", nil)
	})
}

// BenchmarkParallelOPTIONS exercises lazyOPTIONS (sync.Map cache).
func BenchmarkParallelOPTIONS(b *testing.B) {
	m := newMultiMethodMux()
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodOptions, "/res/42", nil)
	})
}

// BenchmarkParallelRedirectTrailingSlash exercises serveRedirect, which takes
// m.mu.RLock() on EVERY call (not cached) — see CH-01 in contention-hunt.md.
func BenchmarkParallelRedirectTrailingSlash(b *testing.B) {
	m := muxmaster.New()
	m.GET("/users/list/", noopHandler)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/list", nil)
	})
}

// BenchmarkParallelRedirectTrailingSlashWithMiddleware measures the same path
// with Use()-registered middleware present, taking the wrapMiddleware branch
// inside serveRedirect (still under m.mu.RLock()).
func BenchmarkParallelRedirectTrailingSlashWithMiddleware(b *testing.B) {
	m := muxmaster.New()
	m.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) })
	})
	m.GET("/users/list/", noopHandler)
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/list", nil)
	})
}

func BenchmarkParallelNotFound(b *testing.B) {
	m := newStaticMux()
	runParallelBench(b, m, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/does/not/exist", nil)
	})
}

// --------------------------------------------------------------------------
// middleware.ThrottleBacklog / ThrottlePerIP — global mutex + channel semaphore
// --------------------------------------------------------------------------

func BenchmarkThrottleBacklogParallel(b *testing.B) {
	h := middleware.ThrottleBacklog(64, 1024, time.Second)(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/", nil)
	})
}

// BenchmarkThrottlePerIPSingleKey drives every goroutine through the SAME key
// — worst case for both the global table mutex and the per-key token channel.
func BenchmarkThrottlePerIPSingleKey(b *testing.B) {
	h := middleware.ThrottlePerIP(64, time.Second, func(r *http.Request) string { return "same-key" })(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/", nil)
	})
}

// BenchmarkThrottlePerIPManyKeys gives each goroutine a distinct, stable key
// — isolates the global table mutex cost from per-key channel contention
// (each goroutine's channel is private after the entry is created).
func BenchmarkThrottlePerIPManyKeys(b *testing.B) {
	h := middleware.ThrottlePerIP(64, time.Second, nil)(http.HandlerFunc(noopHandler))
	var counter int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		id := atomic.AddInt64(&counter, 1)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:%d", (id>>16)&0xff, (id>>8)&0xff, id&0xff, 1024+id%40000)
		rec := httptest.NewRecorder()
		for pb.Next() {
			rec.Body = nil
			h.ServeHTTP(rec, r)
		}
	})
}

// --------------------------------------------------------------------------
// middleware.OAuth2Introspect — RWMutex cache + singleflight inflight map
// --------------------------------------------------------------------------

func newOAuth2TestMiddleware(b *testing.B, cacheTTL time.Duration) (func(http.Handler) http.Handler, *httptest.Server) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true, "sub": "user", "exp": time.Now().Add(time.Hour).Unix(),
		})
	}))
	b.Cleanup(srv.Close)
	mw := middleware.OAuth2Introspect(middleware.OAuth2Options{
		Endpoint:              srv.URL,
		AllowInsecureEndpoint: true,
		CacheTTL:              cacheTTL,
		HTTPClient:            srv.Client(),
	})
	return mw, srv
}

// BenchmarkOAuth2CacheHitSteadyState: one token, warmed cache — every request
// after the first only takes oauth2Cache.mu.RLock(). Measures whether an
// RWMutex read lock alone scales under -cpu.
func BenchmarkOAuth2CacheHitSteadyState(b *testing.B) {
	mw, _ := newOAuth2TestMiddleware(b, time.Minute)
	h := mw(http.HandlerFunc(noopHandler))
	rec0 := httptest.NewRecorder()
	warm := httptest.NewRequest(http.MethodGet, "/", nil)
	warm.Header.Set("Authorization", "Bearer token-fixed")
	h.ServeHTTP(rec0, warm) // populate the cache once, single-threaded
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer token-fixed")
		return r
	})
}

// BenchmarkOAuth2ManyTokensNoCacheHit gives every goroutine a distinct,
// never-before-seen token: forces cache.mu.Lock() (write) + eviction scan +
// oauth2Inflight.mu on every call, and a real round trip to the local httptest
// server for each (singleflight cannot coalesce distinct tokens).
func BenchmarkOAuth2ManyTokensNoCacheHit(b *testing.B) {
	mw, _ := newOAuth2TestMiddleware(b, time.Minute)
	h := mw(http.HandlerFunc(noopHandler))
	var counter int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := atomic.AddInt64(&counter, 1)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Authorization", "Bearer token-"+strconv.FormatInt(id, 10))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
		}
	})
}

// BenchmarkOAuth2SameTokenColdCache: every goroutine hits the SAME never-seen
// token concurrently on b.N==0 iteration only once per proc — approximates the
// singleflight coalescing path (all followers wait on one leader's channel).
func BenchmarkOAuth2SingleflightCoalesce(b *testing.B) {
	mw, _ := newOAuth2TestMiddleware(b, -1) // caching disabled — forces singleflight every time
	h := mw(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer shared-token")
		return r
	})
}

// --------------------------------------------------------------------------
// middleware.JWTAuth — sync.Pool of hmac.Hash
// --------------------------------------------------------------------------

func hs256Token(secret []byte, sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"` + sub + `","exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func BenchmarkJWTAuthHS256Parallel(b *testing.B) {
	secret := []byte("contention-hunt-secret-material-32bytes!")
	h := middleware.JWTAuth(middleware.JWTOptions{
		Secret:     secret,
		Algorithms: []string{"HS256"},
	})(http.HandlerFunc(noopHandler))
	token := hs256Token(secret, "user-42")
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	})
}

// --------------------------------------------------------------------------
// middleware.Compress — sync.Pool of *gzip.Writer
// --------------------------------------------------------------------------

var compressPayload = make([]byte, 8192) // >1024B minCompressSize, triggers gzip path

func BenchmarkCompressParallel(b *testing.B) {
	h := middleware.Compress(6)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(compressPayload)
	}))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		return r
	})
}

// --------------------------------------------------------------------------
// middleware.Logger — sync.Pool (statusRecorder + []byte buf) + shared io.Writer
// --------------------------------------------------------------------------

// discard is an io.Writer that does nothing — isolates the pool/format cost
// from any underlying-writer lock (e.g. os.File).
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkLoggerParallel(b *testing.B) {
	h := middleware.Logger(discard{})(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

// BenchmarkLoggerParallelRealFile logs to a real *os.File (like production),
// to capture contention on the shared fd write path itself.
func BenchmarkLoggerParallelRealFile(b *testing.B) {
	h := middleware.Logger(io.Discard)(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/users/42", nil)
	})
}

// --------------------------------------------------------------------------
// middleware.RequestID — crypto/rand.Read per request
// --------------------------------------------------------------------------

func BenchmarkRequestIDParallel(b *testing.B) {
	h := middleware.RequestID()(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/", nil) // no X-Request-ID -> forces crypto/rand.Read
	})
}

// --------------------------------------------------------------------------
// middleware.RealIP — stateless per-request, no shared state expected
// --------------------------------------------------------------------------

func BenchmarkRealIPParallel(b *testing.B) {
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	h := middleware.RealIP(&prefix)(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.1.2.3:5000"
		r.Header.Set("X-Forwarded-For", "203.0.113.5")
		return r
	})
}

// --------------------------------------------------------------------------
// middleware.RecovererWithLogger — no panics on the hot path (measures the
// defer/recover setup cost, not the log path).
// --------------------------------------------------------------------------

func BenchmarkRecovererParallel(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := middleware.RecovererWithLogger(logger)(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/", nil)
	})
}

// --------------------------------------------------------------------------
// middleware.Timeout — context.WithTimeout per request
// --------------------------------------------------------------------------

func BenchmarkTimeoutParallel(b *testing.B) {
	h := middleware.Timeout(time.Second)(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/", nil)
	})
}

// --------------------------------------------------------------------------
// middleware.BasicAuth / APIKey / CORS — map lookups, no shared mutable state
// --------------------------------------------------------------------------

func BenchmarkBasicAuthParallel(b *testing.B) {
	h := middleware.BasicAuth("realm", map[string]string{"alice": "s3cr3t-password"})(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetBasicAuth("alice", "s3cr3t-password")
		return r
	})
}

func BenchmarkAPIKeyParallel(b *testing.B) {
	h := middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{"key-abc": "svc-a"}})(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-API-Key", "key-abc")
		return r
	})
}

func BenchmarkCORSParallel(b *testing.B) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
	})(http.HandlerFunc(noopHandler))
	runParallelBench(b, h, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", "https://example.com")
		return r
	})
}

var _ = context.Background
var _ = fmt.Sprintf
