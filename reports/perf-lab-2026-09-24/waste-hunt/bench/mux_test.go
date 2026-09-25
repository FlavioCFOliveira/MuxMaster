package bench

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// Root-package dispatch paths that do per-request work which could be done
// once. These benchmarks measure the CURRENT code; experiments/*.diff apply
// the candidate fix to a scratch copy of the module and re-run this file
// against it (see experiments/run-experiments.sh).

type appVersionKey struct{}

// restAPIMux reproduces the rest-api example's global Use() chain (7
// middlewares, same order and configuration; Logger writes to io.Discard)
// and a representative route set.
func restAPIMux() *mm.Mux {
	m := mm.New()
	m.Use(
		mw.RealIP(restAPITrusted()...),
		mw.RequestID(),
		mw.Logger(io.Discard),
		mw.RecovererWithLogger(slog.New(slog.DiscardHandler)),
		mw.ThrottleBacklog(200, 100, 5*time.Second),
		mw.WithValue(appVersionKey{}, "v1.0.0"),
		mw.Compress(gzip.BestSpeed),
	)
	m.GET("/api/v1/books", nop)
	m.POST("/api/v1/books", nop)
	m.GET("/api/v1/books/:id", nop)
	m.PUT("/api/v1/books/:id", nop)
	m.PATCH("/api/v1/books/:id", nop)
	m.DELETE("/api/v1/books/:id", nop)
	m.GET("/api/v1/categories", nop)
	m.GET("/api/v1/categories/:slug", nop)
	return m
}

func serveLoop(b *testing.B, m http.Handler, r *http.Request) {
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.ServeHTTP(w, r)
		w.reset()
	}
}

// RedirectTrailingSlash with Use() middleware: serveRedirect calls
// wrapMiddleware(...) on EVERY redirect, re-running all 7 middleware
// constructors per request.
func BenchmarkRedirectTrailingSlashWithMiddleware(b *testing.B) {
	serveLoop(b, restAPIMux(), realisticRequest(http.MethodGet, "/api/v1/books/"))
}

// Reference: the same middleware chain on a matched route (wrapped once at
// registration) — what the redirect would cost with a pre-wrapped chain.
func BenchmarkMatchedRouteWithMiddleware(b *testing.B) {
	serveLoop(b, restAPIMux(), realisticRequest(http.MethodGet, "/api/v1/books"))
}

func BenchmarkRedirectTrailingSlashNoMiddleware(b *testing.B) {
	m := mm.New()
	m.GET("/api/v1/books", nop)
	serveLoop(b, m, realisticRequest(http.MethodGet, "/api/v1/books/"))
}

// 405 and automatic OPTIONS: allowed() rebuilds the Allow string with a
// strings.Builder on every request, then boxes it into `any` for the
// sync.Map lookup of the cached wrapped handler.
func BenchmarkMethodNotAllowed(b *testing.B) {
	m := mm.New()
	m.GET("/api/v1/categories", nop)
	m.POST("/api/v1/categories", nop)
	serveLoop(b, m, realisticRequest(http.MethodDelete, "/api/v1/categories"))
}

func BenchmarkAutoOPTIONS(b *testing.B) {
	m := mm.New()
	m.GET("/api/v1/books/:id", nop)
	m.PUT("/api/v1/books/:id", nop)
	m.PATCH("/api/v1/books/:id", nop)
	m.DELETE("/api/v1/books/:id", nop)
	serveLoop(b, m, realisticRequest(http.MethodOptions, "/api/v1/books/1"))
}

// Static route dispatch — the paramsBuf zeroing question (mux.go dispatch).
func BenchmarkStaticDispatch(b *testing.B) {
	m := mm.New()
	m.GET("/", nop)
	m.GET("/users", nop)
	m.GET("/users/list", nop)
	m.GET("/health", nop)
	serveLoop(b, m, realisticRequest(http.MethodGet, "/users/list"))
}

// Static route in a tree that ALSO has param routes (root.maxParams > 0):
// the common real-world shape, which takes the getValue path.
func BenchmarkStaticDispatchMixedTree(b *testing.B) {
	m := mm.New()
	m.GET("/", nop)
	m.GET("/users", nop)
	m.GET("/users/list", nop)
	m.GET("/users/:id", nop)
	m.GET("/health", nop)
	serveLoop(b, m, realisticRequest(http.MethodGet, "/users/list"))
}

// Registration: every Handle() deep-clones the method tree (spec'd,
// performance.md §36) and then recomputes maxParams over the WHOLE tree
// (calcPathMaxParams in a deferred closure) — both O(N) per route, O(N²)
// for N routes.
func BenchmarkRegisterRoutes(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run("N="+strconv.Itoa(n), func(b *testing.B) {
			paths := make([]string, n)
			for i := range n {
				switch i % 3 {
				case 0:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items"
				case 1:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items/:id"
				default:
					paths[i] = "/api/v1/res" + strconv.Itoa(i) + "/items/:id/sub/:sid"
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m := mm.New()
				for _, p := range paths {
					m.GET(p, nop)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/route")
		})
	}
}

