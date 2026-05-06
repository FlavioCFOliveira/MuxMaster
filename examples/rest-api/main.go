// Package main implements a bookstore REST API that exercises every MuxMaster
// feature: all HTTP methods, path/regex/catch-all parameters, groups,
// middleware composition, FastHandler, error handling, introspection and more.
//
// Start the server:
//
//	go run .
//
// Quick smoke-test:
//
//	curl http://localhost:8080/health
//	curl http://localhost:8080/api/v1/books
//	curl -X POST http://localhost:8080/api/v1/books \
//	     -H 'Content-Type: application/json' \
//	     -d '{"title":"The Go Programming Language","author":"Donovan","year":2015}'
//	curl http://localhost:8080/debug/routes
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─── Domain types ────────────────────────────────────────────────────────────

type Book struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Year   int    `json:"year"`
	Genre  string `json:"genre"`
}

type Author struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Bio  string `json:"bio"`
}

type Review struct {
	ID     int    `json:"id"`
	BookID int    `json:"book_id"`
	Rating int    `json:"rating"`
	Text   string `json:"text"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

// ─── In-memory store ─────────────────────────────────────────────────────────

type Store struct {
	mu      sync.RWMutex
	books   map[int]*Book
	authors map[int]*Author
	reviews map[int][]Review
	seq     int
}

func newStore() *Store {
	s := &Store{
		books:   make(map[int]*Book),
		authors: make(map[int]*Author),
		reviews: make(map[int][]Review),
	}
	s.seq = 3
	s.books[1] = &Book{ID: 1, Title: "The Go Programming Language", Author: "Donovan", Year: 2015, Genre: "tech"}
	s.books[2] = &Book{ID: 2, Title: "Clean Code", Author: "Martin", Year: 2008, Genre: "tech"}
	s.books[3] = &Book{ID: 3, Title: "Dune", Author: "Herbert", Year: 1965, Genre: "sci-fi"}
	s.authors[1] = &Author{ID: 1, Name: "Alan Donovan", Bio: "Go contributor and author."}
	s.authors[2] = &Author{ID: 2, Name: "Robert Martin", Bio: "Software craftsmanship advocate."}
	return s
}

func (s *Store) nextID() int {
	s.seq++
	return s.seq
}

// ─── Context key for WithValue demo ──────────────────────────────────────────

type appVersionKey struct{}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	store := newStore()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// ── Router ───────────────────────────────────────────────────────────────
	r := mm.New()

	// Configuration flags (explicit for documentation purposes).
	r.RedirectTrailingSlash = true  // /books/ → /books
	r.RedirectFixedPath = false     // security default — keeps middleware auth intact
	r.HandleMethodNotAllowed = true // 405 with Allow header
	r.HandleOPTIONS = true          // automatic OPTIONS + Allow header
	r.CaseInsensitive = false       // strict case matching
	r.UseRawPath = false            // use decoded path for matching
	r.UnescapePathValues = false    // raw param values; opt-in if you need %2F decoded

	// ── Custom error / fallback handlers ─────────────────────────────────────

	// PanicHandler: recover from panics without crashing the server.
	r.PanicHandler = func(w http.ResponseWriter, req *http.Request, rcv any) {
		log.Error("panic recovered", "path", req.URL.Path, "panic", rcv)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "internal server error", Code: 500})
	}

	// ErrorHandler: central handler for HandlerFuncE errors.
	r.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		traceID := mw.GetRequestID(req.Context())
		code := http.StatusInternalServerError
		var he mm.HTTPError
		if errors.As(err, &he) {
			code = he.StatusCode()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Error:   err.Error(),
			Code:    code,
			TraceID: traceID,
		})
	}

	// NotFound: JSON 404 instead of the default plain-text response.
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = mm.JSON(w, http.StatusNotFound, ErrorResponse{
			Error: fmt.Sprintf("route not found: %s %s", req.Method, req.URL.Path),
			Code:  404,
		})
	})

	// MethodNotAllowed: JSON 405 (the Allow header is already set by the router).
	r.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = mm.JSON(w, http.StatusMethodNotAllowed, ErrorResponse{
			Error: "method not allowed",
			Code:  405,
		})
	})

	// GlobalOPTIONS: custom preflight response (adds CORS headers globally).
	r.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.WriteHeader(http.StatusNoContent)
	})

	// ── Pre-middleware: runs before route matching ────────────────────────────

	// CleanPath normalises double slashes and dot segments before routing, so
	// /api//v1/books/../books is matched as /api/v1/books. StripSlashes would
	// be an alternative, but CleanPath is safer for REST APIs.
	r.Pre(mw.CleanPath())

	// ── Global middleware (applied to every registered route below) ───────────

	// Trusted proxy CIDRs for RealIP — localhost + RFC-1918 private ranges.
	localhost, _ := netip.ParsePrefix("127.0.0.0/8")
	private10, _ := netip.ParsePrefix("10.0.0.0/8")
	private172, _ := netip.ParsePrefix("172.16.0.0/12")
	private192, _ := netip.ParsePrefix("192.168.0.0/16")

	r.Use(
		// Overwrite r.RemoteAddr with X-Forwarded-For when peer is a trusted proxy.
		mw.RealIP(&localhost, &private10, &private172, &private192),

		// Assign or propagate X-Request-ID for distributed tracing.
		mw.RequestID(),

		// Structured request logging (method, path, status, latency).
		mw.Logger(os.Stdout),

		// Recover from panics — complements r.PanicHandler for middleware panics.
		mw.RecovererWithLogger(log),

		// Global rate limiting: max 200 concurrent requests, backlog 100, 5s timeout.
		mw.ThrottleBacklog(200, 100, 5*time.Second),

		// Inject a value into every request context — accessible via ctx.Value.
		mw.WithValue(appVersionKey{}, "v1.0.0"),

		// Compress responses >= 1 kB with gzip when the client accepts it.
		mw.Compress(gzip.BestSpeed),
	)

	// ── Public convenience routes ─────────────────────────────────────────────

	// FastHandler: bypasses context allocation — zero allocs for static routes.
	// Ideal for health-check endpoints that are hit thousands of times per second.
	r.GETFast("/health", func(w http.ResponseWriter, req *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	// Standard HandlerFunc: version endpoint reads the injected app version.
	r.GET("/version", func(w http.ResponseWriter, req *http.Request) {
		ver, _ := req.Context().Value(appVersionKey{}).(string)
		_ = mm.JSON(w, http.StatusOK, map[string]string{"version": ver})
	})

	// Redirect old URL to new one (demonstrates mm.Redirect helper).
	r.GET("/books", func(w http.ResponseWriter, req *http.Request) {
		mm.Redirect(w, req, http.StatusMovedPermanently, "/api/v1/books")
	})

	// ── Introspection endpoints ───────────────────────────────────────────────

	// List every registered route via Routes().
	r.GET("/debug/routes", func(w http.ResponseWriter, req *http.Request) {
		_ = mm.JSON(w, http.StatusOK, r.Routes())
	})

	// Route lookup: demonstrates Lookup().
	r.GET("/debug/lookup", func(w http.ResponseWriter, req *http.Request) {
		method := req.URL.Query().Get("method")
		path := req.URL.Query().Get("path")
		if method == "" || path == "" {
			_ = mm.Text(w, http.StatusBadRequest, "provide ?method=GET&path=/api/v1/books/1")
			return
		}
		handler, params, found := r.Lookup(method, path)
		type result struct {
			Found   bool      `json:"found"`
			Handler string    `json:"handler,omitempty"`
			Params  mm.Params `json:"params,omitempty"`
		}
		name := ""
		if handler != nil {
			name = fmt.Sprintf("%T", handler)
		}
		_ = mm.JSON(w, http.StatusOK, result{Found: found, Handler: name, Params: params})
	})

	// ── API v1 ────────────────────────────────────────────────────────────────

	v1 := r.Group("/api/v1")
	v1.Use(
		// Per-request timeout: handlers that exceed 30 s get a cancelled context.
		mw.Timeout(30*time.Second),

		// Per-IP throttle: max 50 concurrent requests per client IP.
		mw.ThrottlePerIP(50, 10*time.Second, nil),
	)

	// ── Books ─────────────────────────────────────────────────────────────────

	books := v1.Group("/books")

	// GET /api/v1/books — list with pagination.
	books.GET("", store.listBooks)

	// POST /api/v1/books — create; uses POSTE (error-returning handler).
	books.POSTE("", store.createBook)

	// HEAD /api/v1/books/:id — check existence without transferring the body.
	books.HEAD("/:id", store.headBook)

	// GET /api/v1/books/:id — get by id; PathParam demo.
	books.GETE("/:id", store.getBook)

	// GET /api/v1/books/{id:[0-9]+}/details — regex param: only numeric IDs.
	// PathParam still returns the matched segment (e.g. "42") under key "id".
	books.GET("/{id:[0-9]+}/details", store.getBookDetails)

	// PUT /api/v1/books/:id — full replacement; PUTE demo.
	books.PUTE("/:id", store.replaceBook)

	// PATCH /api/v1/books/:id — partial update.
	books.PATCH("/:id", store.patchBook)

	// DELETE /api/v1/books/:id — delete; DELETEE demo.
	books.DELETEE("/:id", store.deleteBook)

	// Nested resource: /api/v1/books/:id/reviews
	// Two path parameters in a single route — uses ParamsFromContext.
	books.GET("/:id/reviews", store.listReviews)
	books.POSTE("/:id/reviews", store.createReview)

	// Three path parameters: book → reviews → review.
	// Demonstrates Params.Map() and RoutePattern().
	books.GETE("/:id/reviews/:rid", store.getReview)

	// Match registers one handler for multiple methods (GET + HEAD share logic).
	books.Match(
		[]string{http.MethodGet, http.MethodHead},
		"/featured",
		http.HandlerFunc(store.featuredBooks),
	)

	// ANY registers a handler for every standard HTTP method.
	books.ANY("/ping", func(w http.ResponseWriter, req *http.Request) {
		_ = mm.Text(w, http.StatusOK, fmt.Sprintf("pong from %s /api/v1/books/ping", req.Method))
	})

	// ServeFiles from the local filesystem. The catch-all param is "filepath".
	// HEAD requests are automatically registered alongside GET by ServeFiles.
	books.ServeFiles("/files/*filepath", http.Dir("./static/books"))

	// ── Authors (With: scoped CORS + XML demo) ────────────────────────────────

	// With() returns a new Group with extra middleware applied to its routes only.
	// This CORS configuration is independent of the global one.
	corsOpts := mw.CORSOptions{
		AllowedOrigins: []string{"https://bookshelf.example.com", "http://localhost:3000"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
		MaxAge:         3600,
	}
	authors := v1.With(mw.CORS(corsOpts))
	// XML endpoint: demonstrates mm.XML response helper + With() scoped CORS.
	authors.GET("/authors/:id/xml", store.getAuthorXML)

	// ── Categories (Route: inline sub-group definition) ───────────────────────

	v1.Route("/categories", func(g *mm.Group) {
		g.Use(mw.NoCache()) // categories change frequently — never cache
		g.GET("", listCategories)
		g.GET("/:slug", getCategoryBySlug)
	})

	// ── Admin (BasicAuth + NoCache) ───────────────────────────────────────────

	admin := v1.Group("/admin")
	admin.Use(
		mw.BasicAuth("Bookstore Admin", map[string]string{
			"admin": "s3cr3t!",
		}),
		mw.NoCache(),
		mw.SetHeader("X-Admin-Zone", "true"),
	)
	admin.GET("/dashboard", adminDashboard)
	admin.DELETEE("/books/:id", store.adminDeleteBook)
	admin.GET("/routes", func(w http.ResponseWriter, req *http.Request) {
		_ = mm.JSON(w, http.StatusOK, r.Routes())
	})

	// ── Fast routes with FastMiddleware ──────────────────────────────────────

	// UseFast registers middleware that applies only to HandleFast routes below.
	r.UseFast(fastTimer)

	// GETFast: metrics endpoint — no context allocation, direct Params argument.
	r.GETFast("/metrics", metricsHandler)

	// POSTFast via HandleFast: demonstrates HandleFast with explicit method.
	r.HandleFast(http.MethodPost, "/api/v1/fast/echo", fastEcho)

	// ── Mount: attach a sub-handler at a prefix ───────────────────────────────

	// Mount strips the prefix before forwarding. legacyMux handles /legacy/* paths
	// as if they were rooted at /. Useful for migrating old APIs incrementally.
	legacyMux := buildLegacyMux()
	r.Mount("/legacy", legacyMux)

	// ── Walk: enumerate routes at startup ────────────────────────────────────

	var stdCount, fastCount int
	_ = r.Walk(func(method, pattern string, _ http.Handler) error {
		stdCount++
		return nil
	})
	_ = r.WalkFast(func(method, pattern string, _ mm.FastHandler) error {
		fastCount++
		return nil
	})
	log.Info("routes registered", "std", stdCount, "fast", fastCount)

	// ── Server ────────────────────────────────────────────────────────────────

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
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

// ─── FastMiddleware ───────────────────────────────────────────────────────────

// fastTimer is a FastMiddleware that logs the elapsed time for every fast route.
// FastMiddleware wraps FastHandler the same way stdlib middleware wraps http.Handler.
func fastTimer(next mm.FastHandler) mm.FastHandler {
	return func(w http.ResponseWriter, r *http.Request, ps mm.Params) {
		start := time.Now()
		next(w, r, ps)
		slog.Debug("fast", "path", r.URL.Path, "elapsed", time.Since(start))
	}
}

// ─── Fast handlers ────────────────────────────────────────────────────────────

// metricsHandler is a FastHandler. Params is nil for static routes (0 allocs).
func metricsHandler(w http.ResponseWriter, r *http.Request, _ mm.Params) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = io.WriteString(w, "# HELP go_goroutines Current number of goroutines.\n")
	_, _ = fmt.Fprintf(w, "go_info{version=\"go1.26\"} 1\n")
}

// fastEcho returns the body and path params (demonstrates Params with FastHandler).
func fastEcho(w http.ResponseWriter, r *http.Request, ps mm.Params) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"method": r.Method,
		"path":   r.URL.Path,
		"params": ps.Map(),
		"body":   string(body),
	})
}

// ─── Book handlers ────────────────────────────────────────────────────────────

func (s *Store) listBooks(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	list := make([]*Book, 0, len(s.books))
	for _, b := range s.books {
		list = append(list, b)
	}
	s.mu.RUnlock()
	_ = mm.JSON(w, http.StatusOK, list)
}

// createBook uses the HandlerFuncE pattern: returning an error delegates to ErrorHandler.
func (s *Store) createBook(w http.ResponseWriter, r *http.Request) error {
	var b Book
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		return mm.Error(http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
	}
	if b.Title == "" {
		return mm.Error(http.StatusUnprocessableEntity, errors.New("title is required"))
	}
	s.mu.Lock()
	b.ID = s.nextID()
	s.books[b.ID] = &b
	s.mu.Unlock()

	w.Header().Set("Location", fmt.Sprintf("/api/v1/books/%d", b.ID))
	return mm.JSON(w, http.StatusCreated, b)
}

// headBook checks existence via HEAD (no response body).
func (s *Store) headBook(w http.ResponseWriter, r *http.Request) {
	// PathParam extracts a named path parameter from the request context.
	id := mm.PathParam(r, "id")
	s.mu.RLock()
	_, ok := findBookByID(s, id)
	s.mu.RUnlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// getBook uses HandlerFuncE and PathParam to retrieve a book by id.
func (s *Store) getBook(w http.ResponseWriter, r *http.Request) error {
	id := mm.PathParam(r, "id")
	s.mu.RLock()
	b, ok := findBookByID(s, id)
	s.mu.RUnlock()
	if !ok {
		return mm.Error(http.StatusNotFound, fmt.Errorf("book %q not found", id))
	}

	// Propagate request trace ID in the response.
	traceID := mw.GetRequestID(r.Context())
	w.Header().Set("X-Trace-ID", traceID)
	return mm.JSON(w, http.StatusOK, b)
}

// getBookDetails uses a regex param {id:[0-9]+}. PathParam returns the raw
// segment value (e.g. "42") under the key "id" — same API as :id params.
func (s *Store) getBookDetails(w http.ResponseWriter, r *http.Request) {
	id := mm.PathParam(r, "id") // key is the part before ":" in {id:[0-9]+}

	s.mu.RLock()
	b, ok := findBookByID(s, id)
	s.mu.RUnlock()
	if !ok {
		_ = mm.JSON(w, http.StatusNotFound, ErrorResponse{Error: "not found", Code: 404})
		return
	}
	_ = mm.JSON(w, http.StatusOK, map[string]any{
		"book":    b,
		"pattern": mm.RoutePattern(r), // "/api/v1/books/{id:[0-9]+}/details"
	})
}

func (s *Store) replaceBook(w http.ResponseWriter, r *http.Request) error {
	id := mm.PathParam(r, "id")
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := findBookByID(s, id)
	if !ok {
		return mm.Error(http.StatusNotFound, fmt.Errorf("book %q not found", id))
	}
	var update Book
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		return mm.Error(http.StatusBadRequest, err)
	}
	update.ID = b.ID
	s.books[b.ID] = &update
	return mm.JSON(w, http.StatusOK, &update)
}

func (s *Store) patchBook(w http.ResponseWriter, r *http.Request) {
	id := mm.PathParam(r, "id")
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := findBookByID(s, id)
	if !ok {
		_ = mm.JSON(w, http.StatusNotFound, ErrorResponse{Error: "not found", Code: 404})
		return
	}
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		_ = mm.JSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: 400})
		return
	}
	if v, ok := patch["title"].(string); ok {
		b.Title = v
	}
	if v, ok := patch["genre"].(string); ok {
		b.Genre = v
	}
	_ = mm.JSON(w, http.StatusOK, b)
}

func (s *Store) deleteBook(w http.ResponseWriter, r *http.Request) error {
	id := mm.PathParam(r, "id")
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := findBookByID(s, id)
	if !ok {
		return mm.Error(http.StatusNotFound, fmt.Errorf("book %q not found", id))
	}
	delete(s.books, b.ID)
	delete(s.reviews, b.ID)
	mm.NoContent(w)
	return nil
}

func (s *Store) featuredBooks(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	var list []*Book
	for _, b := range s.books {
		if b.Genre == "tech" {
			list = append(list, b)
		}
	}
	s.mu.RUnlock()
	if r.Method == http.MethodHead {
		w.Header().Set("X-Count", fmt.Sprint(len(list)))
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = mm.JSON(w, http.StatusOK, list)
}

// ─── Review handlers ──────────────────────────────────────────────────────────

// listReviews demonstrates ParamsFromContext to read path params.
func (s *Store) listReviews(w http.ResponseWriter, r *http.Request) {
	// ParamsFromContext is the lower-level accessor — equivalent to PathParam but
	// returns all params at once. Useful when you need more than one param.
	ps := mm.ParamsFromContext(r.Context())
	id := ps.Get("id")

	s.mu.RLock()
	b, ok := findBookByID(s, id)
	s.mu.RUnlock()
	if !ok {
		_ = mm.JSON(w, http.StatusNotFound, ErrorResponse{Error: "book not found", Code: 404})
		return
	}

	s.mu.RLock()
	reviews := s.reviews[b.ID]
	s.mu.RUnlock()
	if reviews == nil {
		reviews = []Review{}
	}
	_ = mm.JSON(w, http.StatusOK, reviews)
}

func (s *Store) createReview(w http.ResponseWriter, r *http.Request) error {
	// Lookup uses Params.Get — identical to PathParam(r, "id").
	id := mm.PathParam(r, "id")

	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := findBookByID(s, id)
	if !ok {
		return mm.Error(http.StatusNotFound, fmt.Errorf("book %q not found", id))
	}

	var rev Review
	if err := json.NewDecoder(r.Body).Decode(&rev); err != nil {
		return mm.Error(http.StatusBadRequest, err)
	}
	rev.ID = s.nextID()
	rev.BookID = b.ID
	s.reviews[b.ID] = append(s.reviews[b.ID], rev)

	w.Header().Set("Location", fmt.Sprintf("/api/v1/books/%d/reviews/%d", b.ID, rev.ID))
	return mm.JSON(w, http.StatusCreated, rev)
}

// getReview demonstrates Params.Int() (typed param parsing) for nested resources.
func (s *Store) getReview(w http.ResponseWriter, r *http.Request) error {
	ps := mm.ParamsFromContext(r.Context())

	// Params.Int() — parse param as int; returns error if absent or non-numeric.
	bookID, err := ps.Int("id")
	if err != nil {
		return mm.Error(http.StatusBadRequest, fmt.Errorf("invalid book id: %w", err))
	}
	reviewID, err := ps.Int("rid")
	if err != nil {
		return mm.Error(http.StatusBadRequest, fmt.Errorf("invalid review id: %w", err))
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rev := range s.reviews[bookID] {
		if rev.ID == reviewID {
			return mm.JSON(w, http.StatusOK, rev)
		}
	}
	return mm.Error(http.StatusNotFound, errors.New("review not found"))
}

// ─── Author handlers ──────────────────────────────────────────────────────────

// getAuthorXML demonstrates mm.XML (XML response helper).
func (s *Store) getAuthorXML(w http.ResponseWriter, r *http.Request) {
	id := mm.PathParam(r, "id")
	s.mu.RLock()
	a, ok := findAuthorByID(s, id)
	s.mu.RUnlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = mm.XML(w, http.StatusOK, a)
}

// ─── Admin handlers ───────────────────────────────────────────────────────────

func adminDashboard(w http.ResponseWriter, r *http.Request) {
	_ = mm.JSON(w, http.StatusOK, map[string]any{
		"admin":    true,
		"trace_id": mw.GetRequestID(r.Context()),
		"pattern":  mm.RoutePattern(r),
	})
}

// adminDeleteBook: admin-only hard delete (bypasses soft-delete logic).
func (s *Store) adminDeleteBook(w http.ResponseWriter, r *http.Request) error {
	id := mm.PathParam(r, "id")
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := findBookByID(s, id)
	if !ok {
		return mm.Error(http.StatusNotFound, fmt.Errorf("book %q not found", id))
	}
	delete(s.books, b.ID)
	mm.NoContent(w)
	return nil
}

// ─── Category handlers (registered via Route) ─────────────────────────────────

var categories = []map[string]string{
	{"slug": "tech", "label": "Technology"},
	{"slug": "sci-fi", "label": "Science Fiction"},
	{"slug": "history", "label": "History"},
}

func listCategories(w http.ResponseWriter, r *http.Request) {
	_ = mm.JSON(w, http.StatusOK, categories)
}

func getCategoryBySlug(w http.ResponseWriter, r *http.Request) {
	slug := mm.PathParam(r, "slug")
	for _, c := range categories {
		if c["slug"] == slug {
			_ = mm.JSON(w, http.StatusOK, c)
			return
		}
	}
	_ = mm.JSON(w, http.StatusNotFound, ErrorResponse{Error: "category not found", Code: 404})
}

// ─── Legacy sub-muxer (for Mount demo) ───────────────────────────────────────

// buildLegacyMux returns a sub-mux for old API routes mounted at /legacy.
// After Mount strips the prefix, handlers see paths rooted at /.
func buildLegacyMux() http.Handler {
	sub := mm.New()
	sub.GET("/api/books", func(w http.ResponseWriter, r *http.Request) {
		mm.Redirect(w, r, http.StatusPermanentRedirect, "/api/v1/books")
	})
	sub.GET("/api/books/:id", func(w http.ResponseWriter, r *http.Request) {
		id := mm.PathParam(r, "id")
		mm.Redirect(w, r, http.StatusPermanentRedirect, "/api/v1/books/"+id)
	})
	return sub
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func findBookByID(s *Store, id string) (*Book, bool) {
	for _, b := range s.books {
		if fmt.Sprint(b.ID) == id {
			return b, true
		}
	}
	return nil, false
}

func findAuthorByID(s *Store, id string) (*Author, bool) {
	for _, a := range s.authors {
		if fmt.Sprint(a.ID) == id {
			return a, true
		}
	}
	return nil, false
}
