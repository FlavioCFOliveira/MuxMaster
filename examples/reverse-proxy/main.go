// Package main demonstrates reverse-proxy routing through MuxMaster using
// the stdlib `httputil.ReverseProxy`. Path-based and round-robin patterns
// are both shown, with maximum-performance pool configuration.
//
// Why this is pool-safe:
//
//   - httputil.ReverseProxy synchronously forwards the request and waits
//     for the upstream response before returning. The proxy never spawns
//     a goroutine that survives ServeHTTP, so r is not captured past return.
//
//   - The Director function rewrites the request's URL to point at the
//     upstream. The rewrite happens inline within ServeHTTP — pool-safe.
//
//   - The handler signature is plain http.Handler, so it can be used in
//     either pool-mode or default-mode without changes.
//
// Run two upstream services and the gateway:
//
//	# Terminal 1: start a backend on :9001
//	go run . backend 9001
//	# Terminal 2: start another backend on :9002
//	go run . backend 9002
//	# Terminal 3: the gateway on :8080 fans out to both
//	go run .
//
// Then:
//
//	curl http://localhost:8080/api/users     # → load-balanced to 9001/9002
//	curl http://localhost:8080/static/x.png  # → 9001 only (path prefix)
//	curl http://localhost:8080/admin/        # → 9002 only
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
	// Allow running as a fake backend for local testing:
	//   go run . backend <port>
	if len(os.Args) == 3 && os.Args[1] == "backend" {
		runBackend(os.Args[2])
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mux := mm.New()
	mux.PoolRequestBundle = true
	mux.Pre(mw.RequestID(), mw.RecovererWithLogger(log))

	// Build proxies for two upstreams.
	upstream1 := mustURL("http://127.0.0.1:9001")
	upstream2 := mustURL("http://127.0.0.1:9002")

	staticProxy := newProxy("static", log, upstream1)
	adminProxy := newProxy("admin", log, upstream2)
	apiBalanced := newRoundRobin("api", log, upstream1, upstream2)

	// /api/* is a catch-all that fans out across upstreams round-robin.
	// catch-all routes are 0-alloc with PoolRequestBundle.
	mux.GET("/api/*path", apiBalanced)
	mux.POST("/api/*path", apiBalanced)
	mux.PUT("/api/*path", apiBalanced)
	mux.DELETE("/api/*path", apiBalanced)

	// /static/* always goes to upstream1.
	mux.GET("/static/*path", staticProxy)
	mux.HEAD("/static/*path", staticProxy)

	// /admin/* always goes to upstream2 — gated behind a token.
	admin := mux.Group("/admin")
	admin.Use(adminAuth)
	admin.GET("/*path", adminProxy)

	mux.GET("/", indexPage)

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Info("gateway listening", "addr", srv.Addr,
			"upstream1", upstream1.String(), "upstream2", upstream2.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ─── Proxy construction ──────────────────────────────────────────────────────

// newProxy returns an http.HandlerFunc that proxies to the given target.
// It rewrites the URL host inline (pool-safe) and strips the gateway path
// prefix that MuxMaster captured via a *catch-all wildcard.
func newProxy(name string, log *slog.Logger, target *url.URL) http.HandlerFunc {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// pr.Out is a fresh request the proxy will send upstream. We
			// retarget its URL to the upstream's scheme/host, preserve
			// the captured wildcard path, and propagate X-Forwarded-* headers.
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			// The catch-all param "path" contains the captured suffix; if
			// the route registered "/static/*path", a request to
			// "/static/css/app.css" sets path = "/css/app.css".
			pr.Out.URL.Path = mm.PathParam(pr.In, "path")
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
			pr.Out.Host = target.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("proxy error", "name", name, "path", r.URL.Path, "err", err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		},
	}
	return rp.ServeHTTP
}

// newRoundRobin returns a handler that distributes requests across N
// upstreams using a lock-free atomic counter (no contention).
func newRoundRobin(name string, log *slog.Logger, targets ...*url.URL) http.HandlerFunc {
	proxies := make([]http.HandlerFunc, len(targets))
	for i, t := range targets {
		proxies[i] = newProxy(fmt.Sprintf("%s[%d]", name, i), log, t)
	}
	var counter atomic.Uint64
	return func(w http.ResponseWriter, r *http.Request) {
		idx := counter.Add(1) % uint64(len(proxies))
		proxies[idx](w, r)
	}
}

// ─── adminAuth — gate /admin/* behind a header ───────────────────────────────

func adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Token") != "letmein" {
			http.Error(w, "admin token required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Fake backend used for local testing ─────────────────────────────────────

func runBackend(port string) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mux := mm.New()
	mux.PoolRequestBundle = true
	mux.GET("/*path", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "backend :%s reached path=%s headers=%v\n",
			port, r.URL.Path, r.Header.Get("X-Forwarded-For"))
	})
	mux.POST("/*path", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		_, _ = fmt.Fprintf(w, "backend :%s POST path=%s body=%q\n", port, r.URL.Path, body)
	})
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	log.Info("backend listening", "addr", srv.Addr)
	_ = srv.ListenAndServe()
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func indexPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, `MuxMaster · Reverse Proxy gateway

  /api/*   → round-robin to :9001 + :9002
  /static/* → :9001
  /admin/*  → :9002  (requires X-Admin-Token: letmein)

Start the two demo backends in separate shells:
  go run . backend 9001
  go run . backend 9002

Then send some traffic through the gateway:
  curl http://localhost:8080/api/users
  curl http://localhost:8080/static/x.png
  curl -H 'X-Admin-Token: letmein' http://localhost:8080/admin/dashboard

Pool-safe: httputil.ReverseProxy returns before ServeHTTP exits, so r is
never captured by a goroutine that outlives the handler.`)
}
