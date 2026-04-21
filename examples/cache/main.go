// Package main demonstrates response caching with MuxMaster using three
// complementary techniques:
//
//  1. In-memory TTL cache — avoids re-computing expensive responses within a
//     configurable window. Write operations explicitly invalidate stale keys.
//     Expired entries are evicted by a background goroutine.
//
//  2. Cache-Control headers — instruct HTTP clients and reverse proxies to
//     store responses and reuse them until max-age expires.
//
//  3. ETag / conditional requests — the server attaches an ETag to each
//     response; clients re-send it as If-None-Match; the server replies 304
//     Not Modified (no body) when the resource has not changed.
//
// Endpoints:
//
//	GET  /health            — public (FastHandler, zero allocs)
//	GET  /articles          — list; in-memory cache 30 s + Cache-Control
//	POST /articles          — create; invalidates list cache
//	GET  /articles/:id      — single item; ETag + 304 support
//	PUT  /articles/:id/done — toggle done flag; invalidates caches
//
// Start:
//
//	go run .
//
// Smoke-test:
//
//	# List — first call: MISS, subsequent calls within 30 s: HIT
//	curl -i http://localhost:8080/articles
//
//	# Create (invalidates list cache)
//	curl -s -X POST http://localhost:8080/articles \
//	  -H 'Content-Type: application/json' \
//	  -d '{"title":"Caching patterns in Go"}' | jq .
//
//	# ETag round-trip
//	ETAG=$(curl -si http://localhost:8080/articles/1 \
//	  | grep -i '^etag' | awk '{print $2}' | tr -d '\r')
//	curl -i -H "If-None-Match: $ETAG" http://localhost:8080/articles/1  # 304
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─── In-memory TTL cache ──────────────────────────────────────────────────────

type entry struct {
	val       any
	expiresAt time.Time
}

// Cache is a thread-safe key-value store with per-entry TTL.
// A background goroutine evicts expired entries every minute.
type Cache struct {
	mu   sync.RWMutex
	data map[string]entry
}

func newCache() *Cache {
	c := &Cache{data: make(map[string]entry)}
	go c.evict()
	return c
}

// Set stores val under key with the given TTL.
func (c *Cache) Set(key string, val any, ttl time.Duration) {
	c.mu.Lock()
	c.data[key] = entry{val: val, expiresAt: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// Get returns the value for key and true when the entry exists and has not expired.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.val, true
}

// Invalidate removes all keys that share the given prefix.
func (c *Cache) Invalidate(prefix string) {
	c.mu.Lock()
	for k := range c.data {
		if strings.HasPrefix(k, prefix) {
			delete(c.data, k)
		}
	}
	c.mu.Unlock()
}

func (c *Cache) evict() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		c.mu.Lock()
		for k, e := range c.data {
			if now.After(e.expiresAt) {
				delete(c.data, k)
			}
		}
		c.mu.Unlock()
	}
}

// ─── ETag helpers ─────────────────────────────────────────────────────────────

// etagFor returns a weak ETag derived from a SHA-256 hash of the given strings.
// Weak ETags (W/"...") indicate semantic equivalence — appropriate for JSON.
func etagFor(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
	}
	return fmt.Sprintf(`W/"%x"`, h.Sum(nil)[:8])
}

// checkNotModified writes 304 and returns true when the client's If-None-Match
// header matches the current ETag. The handler should return immediately after.
func checkNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// ─── Domain ───────────────────────────────────────────────────────────────────

type Article struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Done      bool   `json:"done"`
	CreatedAt int64  `json:"created_at"`
}

// ─── Store ────────────────────────────────────────────────────────────────────

type Store struct {
	mu       sync.RWMutex
	articles map[int]*Article
	seq      atomic.Int64
}

func newStore() *Store {
	s := &Store{articles: make(map[int]*Article)}
	now := time.Now().Unix()
	s.articles[1] = &Article{ID: 1, Title: "Intro to MuxMaster", CreatedAt: now}
	s.articles[2] = &Article{ID: 2, Title: "Radix trees explained", CreatedAt: now}
	s.seq.Store(2)
	return s
}

func (s *Store) list() []*Article {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Article, 0, len(s.articles))
	for _, a := range s.articles {
		out = append(out, a)
	}
	return out
}

func (s *Store) get(id string) (*Article, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.articles {
		if fmt.Sprint(a.ID) == id {
			return a, true
		}
	}
	return nil, false
}

func (s *Store) create(title string) *Article {
	a := &Article{
		ID:        int(s.seq.Add(1)),
		Title:     title,
		CreatedAt: time.Now().Unix(),
	}
	s.mu.Lock()
	s.articles[a.ID] = a
	s.mu.Unlock()
	return a
}

func (s *Store) toggleDone(id string) (*Article, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.articles {
		if fmt.Sprint(a.ID) == id {
			a.Done = !a.Done
			return a, true
		}
	}
	return nil, false
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func errMsg(msg string) map[string]string { return map[string]string{"error": msg} }

// decodeJSON decodes the request body into v, limited to 1 MB.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	store := newStore()
	cache := newCache()

	r := mm.New()

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(
		mw.RequestID(),
		mw.Logger(os.Stdout),
		mw.RecovererWithLogger(log),
	)

	// ── Custom error handlers ─────────────────────────────────────────────────
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = mm.JSON(w, http.StatusNotFound, errMsg("not found"))
	})
	r.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		code := http.StatusInternalServerError
		var he mm.HTTPError
		if errors.As(err, &he) {
			code = he.StatusCode()
		}
		_ = mm.JSON(w, code, errMsg(err.Error()))
	}

	// ── Public routes ─────────────────────────────────────────────────────────

	// FastHandler: static route — zero allocations per request.
	r.GETFast("/health", func(w http.ResponseWriter, _ *http.Request, _ mm.Params) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	// ── GET /articles — list with in-memory cache + Cache-Control ────────────
	//
	// First request within each 30 s window: store miss → query store, populate
	// cache, respond with X-Cache: MISS.
	// Subsequent requests: cache hit → return stored value, X-Cache: HIT.
	// Cache-Control tells clients and reverse proxies to keep their own copy.
	r.GET("/articles", func(w http.ResponseWriter, r *http.Request) {
		const (
			ttl = 30 * time.Second
			key = "articles:list"
		)
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(ttl.Seconds())))

		if cached, ok := cache.Get(key); ok {
			w.Header().Set("X-Cache", "HIT")
			_ = mm.JSON(w, http.StatusOK, cached)
			return
		}

		list := store.list()
		cache.Set(key, list, ttl)
		w.Header().Set("X-Cache", "MISS")
		_ = mm.JSON(w, http.StatusOK, list)
	})

	// ── POST /articles — create and invalidate the list cache ────────────────
	r.POSTE("/articles", func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Title string `json:"title"`
		}
		if err := decodeJSON(r, &body); err != nil {
			return mm.Error(http.StatusBadRequest, errors.New("invalid JSON"))
		}
		if strings.TrimSpace(body.Title) == "" {
			return mm.Error(http.StatusUnprocessableEntity, errors.New("title is required"))
		}

		article := store.create(body.Title)

		// A new article was added — the cached list is stale.
		cache.Invalidate("articles:")

		w.Header().Set("Location", fmt.Sprintf("/articles/%d", article.ID))
		return mm.JSON(w, http.StatusCreated, article)
	})

	// ── GET /articles/:id — single item with ETag + conditional requests ──────
	//
	// The ETag is derived from the article's current state. If the client
	// includes If-None-Match with the same ETag, the server responds 304 Not
	// Modified — no body is sent, saving bandwidth on unchanged resources.
	r.GETE("/articles/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := mm.PathParam(r, "id")
		article, ok := store.get(id)
		if !ok {
			return mm.Error(http.StatusNotFound, fmt.Errorf("article %q not found", id))
		}

		etag := etagFor(fmt.Sprint(article.ID), article.Title, fmt.Sprint(article.Done))
		w.Header().Set("Cache-Control", "public, max-age=60")

		if checkNotModified(w, r, etag) {
			return nil // 304 already written — return early
		}

		w.Header().Set("ETag", etag)
		return mm.JSON(w, http.StatusOK, article)
	})

	// ── PUT /articles/:id/done — toggle done flag, invalidate caches ──────────
	//
	// After mutating the resource, cached ETags are no longer valid. Clients
	// that retry with the old ETag will receive a fresh 200 on the next GET.
	r.PUTE("/articles/:id/done", func(w http.ResponseWriter, r *http.Request) error {
		id := mm.PathParam(r, "id")
		article, ok := store.toggleDone(id)
		if !ok {
			return mm.Error(http.StatusNotFound, fmt.Errorf("article %q not found", id))
		}

		// Both the list and the item are now stale.
		cache.Invalidate("articles:")

		return mm.JSON(w, http.StatusOK, article)
	})

	// ── Start ─────────────────────────────────────────────────────────────────
	log.Info("listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
