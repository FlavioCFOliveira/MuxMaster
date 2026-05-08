// Package main implements a documentation static website served with MuxMaster.
//
// It demonstrates the full HTTP vocabulary for static file serving:
//   - Conditional GET (304 Not Modified via ETag / Last-Modified)
//   - Range requests (partial content, 206)
//   - Content-Type detection and charset negotiation
//   - HEAD requests for metadata without body transfer
//   - OPTIONS for CORS preflight
//   - Cache-Control strategy (long TTL for versioned assets, no-cache for HTML)
//   - Gzip compression via Accept-Encoding
//   - Security headers (X-Frame-Options, X-Content-Type-Options, CSP)
//   - CORS for cross-origin asset requests (e.g. web fonts, CDN)
//   - Request IDs for distributed tracing
//   - Rate limiting via ThrottleBacklog
//   - RealIP resolution behind a reverse proxy
//   - Clean-path normalisation before routing
//   - Custom HTML 404 and method-not-allowed pages
//   - Redirect chains for canonical URLs
//   - Sub-handler mount for versioned documentation
//
// Start the server:
//
//	go run .
//
// Try it:
//
//	curl -I http://localhost:8080/assets/style.css            # HEAD + ETag
//	curl -H 'Accept-Encoding: gzip' http://localhost:8080/   # gzip body
//	curl -r 0-499 http://localhost:8080/assets/style.css      # Range request
//	curl -H 'Origin: https://example.com' -X OPTIONS http://localhost:8080/assets/style.css
package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Root of the static file tree (relative to the working directory).
	// Run the server from the examples/static-site directory.
	staticRoot := http.Dir("./static")

	// ── Router ───────────────────────────────────────────────────────────────
	r := mm.New()

	// RedirectTrailingSlash handles /docs/v1/ → /docs/v1 and vice-versa
	// automatically; http.FileServer also needs the trailing slash on directories,
	// so we keep both enabled and let each layer handle its own case.
	r.RedirectTrailingSlash = true
	r.RedirectFixedPath = false
	r.HandleMethodNotAllowed = true
	r.HandleOPTIONS = true

	// ── Custom fallback handlers ──────────────────────────────────────────────

	// NotFound: serve the custom HTML 404 page instead of Go's default plain text.
	r.NotFound = http.HandlerFunc(serveNotFound(staticRoot))

	// MethodNotAllowed: return a simple HTML message for non-GET/HEAD requests on
	// static routes (browsers never POST to a static file, but robots might).
	r.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Allow", w.Header().Get("Allow")) // already set by the router
		http.Error(w, fmt.Sprintf("method %s not allowed", req.Method), http.StatusMethodNotAllowed)
	})

	// GlobalOPTIONS: respond to preflight requests for all registered routes.
	r.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Accept-Encoding, Range")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
	})

	// ── Pre-middleware: before route matching ─────────────────────────────────

	// CleanPath normalises paths like /docs//v1/../v2/ → /docs/v2 before the
	// radix tree sees them, preventing traversal attacks and double-hit caching.
	r.Pre(mw.CleanPath())

	// ── Global middleware ─────────────────────────────────────────────────────

	// Trusted upstream CIDRs for RealIP — localhost + common private ranges.
	loopback, _ := netip.ParsePrefix("127.0.0.0/8")
	private10, _ := netip.ParsePrefix("10.0.0.0/8")
	private172, _ := netip.ParsePrefix("172.16.0.0/12")
	private192, _ := netip.ParsePrefix("192.168.0.0/16")

	r.Use(
		// Overwrite r.RemoteAddr with X-Forwarded-For when behind nginx/Caddy.
		mw.RealIP(&loopback, &private10, &private172, &private192),

		// Assign or propagate X-Request-ID for access log correlation.
		mw.RequestID(),

		// Structured access log: "2026-04-21T... GET /assets/style.css 200 1.2ms"
		mw.Logger(os.Stdout),

		// Recover from panics so a bad handler does not bring down the whole server.
		mw.RecovererWithLogger(log),

		// Global rate limit: max 500 concurrent requests, backlog 200, 8s timeout.
		// Protects against accidental DDoS from scrapers or CI load tests.
		mw.ThrottleBacklog(500, 200, 8*time.Second),

		// Gzip: compress text/* and application/* responses >= 1 kB.
		// http.FileServer sets Content-Type before writing — Compress picks it up.
		mw.Compress(gzip.BestSpeed),

		// Security headers applied to every response.
		// X-Content-Type-Options prevents MIME sniffing.
		mw.SetHeader("X-Content-Type-Options", "nosniff"),
		// X-Frame-Options prevents clickjacking.
		mw.SetHeader("X-Frame-Options", "SAMEORIGIN"),
		// Referrer-Policy controls how much info is sent in Referer headers.
		mw.SetHeader("Referrer-Policy", "strict-origin-when-cross-origin"),
		// Permissions-Policy restricts access to browser APIs.
		mw.SetHeader("Permissions-Policy", "camera=(), microphone=(), geolocation=()"),
	)

	// ── Health check ─────────────────────────────────────────────────────────

	// FastHandler: no context allocation, 0 allocs for this static route.
	// Ideal for load-balancer probes called thousands of times per second.
	r.GETFast("/health", func(w http.ResponseWriter, _ *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	// HEAD /health for tools that only probe with HEAD.
	r.HEADFast("/health", func(w http.ResponseWriter, _ *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})

	// ── Dynamic API endpoint (JSON, no-cache) ─────────────────────────────────

	// With() returns a Group with extra middleware applied only to its routes.
	// NoCache ensures these endpoints are never stored by proxies or browsers.
	api := r.With(mw.NoCache(), mw.Timeout(10*time.Second))
	api.GET("/api/config", serveConfig)

	// ── HTML pages ────────────────────────────────────────────────────────────

	// HTML pages use NoCache so changes are picked up immediately.
	// Cache-Control is set per-route via SetHeader.
	pages := r.With(
		mw.SetHeader("Cache-Control", "no-cache, must-revalidate"),
	)

	// Serve the site root — http.FileServer handles:
	//   • ETag generation and If-None-Match → 304 Not Modified
	//   • Last-Modified and If-Modified-Since → 304 Not Modified
	//   • Range requests → 206 Partial Content
	//   • HEAD (automatically — no body but all headers present)
	//   • Directory index (index.html auto-served for directories)
	pages.GET("/", serveFile(staticRoot, "/index.html"))
	pages.HEAD("/", serveFile(staticRoot, "/index.html"))

	// Redirect old URL shape to canonical docs path.
	r.GET("/doc", func(w http.ResponseWriter, req *http.Request) {
		mm.Redirect(w, req, http.StatusMovedPermanently, "/docs/v2/")
	})
	r.GET("/documentation", func(w http.ResponseWriter, req *http.Request) {
		mm.Redirect(w, req, http.StatusMovedPermanently, "/docs/v2/")
	})

	// ── Versioned documentation (Mount) ──────────────────────────────────────

	// Mount attaches a sub-handler at a prefix, stripping the prefix before
	// forwarding. Each versioned docs sub-mux handles its own file tree.
	//
	// After mount, requests to /docs/v1/index.html arrive at docsV1 as /index.html.
	docsV1 := buildDocsMux(http.Dir("./static/docs/v1"))
	docsV2 := buildDocsMux(http.Dir("./static/docs/v2"))
	r.Mount("/docs/v1", docsV1)
	r.Mount("/docs/v2", docsV2)

	// ── Versioned static assets ───────────────────────────────────────────────

	// Assets use immutable cache: long TTL + immutable directive.
	// The asset URL encodes a content hash, e.g. /assets/style.css.
	// http.FileServer handles ETag / Last-Modified / Range / HEAD automatically.
	assetsGroup := r.With(
		// Long-lived cache for fingerprinted assets.
		mw.SetHeader("Cache-Control", "public, max-age=31536000, immutable"),
		// Allow CDNs and cross-origin pages to load fonts and scripts.
		mw.CORS(mw.CORSOptions{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{http.MethodGet, http.MethodHead},
			AllowedHeaders: []string{"Accept-Encoding", "Range"},
			ExposedHeaders: []string{"Content-Length", "Content-Range", "ETag"},
			MaxAge:         86400,
		}),
	)

	// ServeFiles registers GET and HEAD for the catch-all pattern.
	// http.FileServer underneath handles:
	//   GET  /assets/style.css                → 200 with body
	//   HEAD /assets/style.css                → 200 headers only
	//   GET  /assets/style.css (with ETag)    → 304 Not Modified
	//   GET  /assets/large.js (Range: 0-1023) → 206 Partial Content
	//
	// SECURITY (CDX-S8-002): ServeFiles refuses to register when this Mux is
	// configured with both UseRawPath=true AND UnescapePathValues=true. We
	// keep both at default (false) to let net/http canonicalise the path
	// before dispatch, and http.FileServer applies path.Clean internally.
	// See SECURITY.md "UseRawPath traversal".
	assetsGroup.ServeFiles("/assets/*filepath", staticRoot)

	// ── Route inspection ──────────────────────────────────────────────────────

	// List all registered routes — useful for debugging the routing tree.
	r.GET("/debug/routes", func(w http.ResponseWriter, req *http.Request) {
		traceID := mw.GetRequestID(req.Context())
		w.Header().Set("X-Trace-ID", traceID)
		routes := r.Routes()
		type entry struct {
			Method  string `json:"method"`
			Pattern string `json:"pattern"`
		}
		list := make([]entry, 0, len(routes))
		for _, ri := range routes {
			list = append(list, entry{Method: ri.Method, Pattern: ri.Pattern})
		}
		_ = mm.JSON(w, http.StatusOK, list)
	})

	// ── Server ────────────────────────────────────────────────────────────────

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("static site listening", "addr", srv.Addr,
			"tip", "curl -I http://localhost:8080/assets/style.css")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("server stopped")
}

// ─── Sub-muxer for versioned docs (Mount demo) ────────────────────────────────

// buildDocsMux returns a Mux that serves a versioned documentation directory.
// All routes use an HTML-appropriate cache policy and moderate rate limiting.
func buildDocsMux(root http.FileSystem) http.Handler {
	sub := mm.New()
	sub.Use(
		mw.SetHeader("Cache-Control", "no-cache, must-revalidate"),
		mw.SetHeader("X-Robots-Tag", "index, follow"),
		mw.ThrottleBacklog(100, 50, 5*time.Second),
	)

	// Serve directory index — http.FileServer auto-redirects /docs/v1 → /docs/v1/
	// (the trailing slash) and then serves index.html for the directory.
	sub.ServeFiles("/*filepath", root)
	return sub
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// serveFile returns a HandlerFunc that serves a single file from root.
// http.ServeContent / http.ServeFile honours:
//   - If-Modified-Since → 304
//   - If-None-Match (ETag) → 304
//   - Range → 206 Partial Content
//   - HEAD → headers only, no body
func serveFile(root http.FileSystem, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := root.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// http.ServeContent handles ETag, Last-Modified, Range and HEAD transparently.
		http.ServeContent(w, r, name, stat.ModTime(), f.(io.ReadSeeker))
	}
}

// serveNotFound returns a HandlerFunc that serves the custom 404 HTML page.
func serveNotFound(root http.FileSystem) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := root.Open("/404.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		// http.ServeContent would send 200; use manual copy for the 404 body.
		http.ServeContent(w, r, "404.html", stat.ModTime(), f.(io.ReadSeeker))
	}
}

// serveConfig returns a JSON payload of public site configuration.
// Registered with NoCache + Timeout via With().
func serveConfig(w http.ResponseWriter, r *http.Request) {
	traceID := mw.GetRequestID(r.Context())
	_ = mm.JSON(w, http.StatusOK, map[string]any{
		"site":     "MuxMaster Docs",
		"version":  "v1.0.0",
		"trace_id": traceID,
		"routes": []string{
			"/",
			"/docs/v1/",
			"/docs/v2/",
			"/assets/*",
			"/health",
		},
	})
}
