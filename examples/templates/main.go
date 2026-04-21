// Package main demonstrates server-side HTML rendering with MuxMaster and
// Go's html/template package.
//
// It implements a multi-page guestbook website to show:
//
//   - Layout inheritance: base.html defines the page chrome (nav, header,
//     footer); each page template fills {{block "content"}} and {{block "title"}}.
//     Every page is pre-parsed at startup into its own *template.Template
//     together with base.html, so each template set has exactly one definition
//     per block and there is no runtime parsing cost.
//
//   - Template data: a PageData struct is passed to every ExecuteTemplate call,
//     carrying flash messages, form values, validation errors, and domain data.
//
//   - POST–Redirect–GET: submitting the guestbook form POSTs to /guestbook,
//     which validates and stores the entry, then redirects to /guestbook?ok=1.
//     The GET handler reads the query param and renders a success flash message.
//     This prevents the "resubmit on refresh?" browser dialog.
//
//   - Form re-population: when validation fails the form re-renders with the
//     submitted values and per-field error messages — no data loss for the user.
//
//   - Static files: /static/* is served from an embedded sub-filesystem, so
//     the stylesheet lives alongside the dynamic routes with no file-system
//     dependency at runtime.
//
//   - Custom 404: a themed HTML error page via r.NotFound.
//
//   - Embedded assets: all templates and static files are compiled into the
//     binary via embed.FS — deploy a single binary with no external assets.
//
// Start:
//
//	go run .
//
// Then open http://localhost:8080 in your browser.
package main

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
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

//go:embed templates static
var files embed.FS

// ─── Template loader ─────────────────────────────────────────────────────────

// Templates holds one pre-parsed *template.Template per page.
// Each entry is base.html + <page>.html parsed together so that the page's
// {{define "content"}} and {{define "title"}} blocks are the only definitions
// in that template set — no runtime conflicts between pages.
type Templates struct {
	pages    map[string]*template.Template
	notFound *template.Template
}

func loadTemplates() *Templates {
	base := "templates/base.html"
	pages := make(map[string]*template.Template)
	for _, name := range []string{"home", "about", "guestbook"} {
		pages[name] = template.Must(template.ParseFS(
			files, base, "templates/"+name+".html",
		))
	}
	return &Templates{
		pages:    pages,
		notFound: template.Must(template.ParseFS(files, "templates/404.html")),
	}
}

// render executes the named page template with the given data.
// The entry point is always the "base" template defined in base.html.
func (t *Templates) render(w http.ResponseWriter, name string, data PageData) {
	tmpl, ok := t.pages[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		// Headers are already sent — we can only log at this point.
		slog.Error("template render error", "page", name, "err", err)
	}
}

// renderNotFound writes a themed 404 HTML response.
func (t *Templates) renderNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if err := t.notFound.Execute(w, nil); err != nil {
		slog.Error("404 template error", "err", err)
	}
}

// ─── Template data ────────────────────────────────────────────────────────────

// PageData is the value passed to every template execution.
// Templates access its fields with {{.Flash}}, {{.Entries}}, etc.
type PageData struct {
	Flash     string            // transient message shown once
	FlashType string            // CSS class: "success" or "error"
	Entries   []*Entry          // guestbook entries
	Form      map[string]string // re-populated form values after a failed POST
	Errors    map[string]string // per-field validation errors
}

// ─── Domain ───────────────────────────────────────────────────────────────────

// Entry is a single guestbook entry.
type Entry struct {
	ID      int
	Name    string
	Message string
	Date    string // pre-formatted at creation time
}

// ─── Store ────────────────────────────────────────────────────────────────────

type Store struct {
	mu      sync.RWMutex
	entries []*Entry
	seq     atomic.Int64
}

func newStore() *Store {
	now := time.Now()
	s := &Store{
		entries: []*Entry{
			{ID: 1, Name: "Alice", Message: "Great router! Zero allocations is impressive.", Date: now.Add(-2 * time.Hour).Format("2 Jan 2006 at 15:04")},
			{ID: 2, Name: "Bob", Message: "Love the API — very idiomatic Go.", Date: now.Add(-30 * time.Minute).Format("2 Jan 2006 at 15:04")},
		},
	}
	s.seq.Store(2)
	return s
}

// all returns a copy of the entries slice (newest first).
func (s *Store) all() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// add prepends a new entry so the list is newest-first.
func (s *Store) add(name, message string) *Entry {
	e := &Entry{
		ID:      int(s.seq.Add(1)),
		Name:    name,
		Message: message,
		Date:    time.Now().Format("2 Jan 2006 at 15:04"),
	}
	s.mu.Lock()
	s.entries = append([]*Entry{e}, s.entries...)
	s.mu.Unlock()
	return e
}

// ─── Validation ───────────────────────────────────────────────────────────────

type formErrors map[string]string

func validateGuestbook(name, message string) (formErrors, bool) {
	errs := make(formErrors)
	if strings.TrimSpace(name) == "" {
		errs["name"] = "Name is required."
	} else if len(name) > 100 {
		errs["name"] = "Name must be 100 characters or fewer."
	}
	if strings.TrimSpace(message) == "" {
		errs["message"] = "Message is required."
	} else if len(message) > 1000 {
		errs["message"] = "Message must be 1000 characters or fewer."
	}
	return errs, len(errs) == 0
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tmpl := loadTemplates()
	store := newStore()

	r := mm.New()

	// ── Custom 404 — themed HTML page ────────────────────────────────────────
	r.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tmpl.renderNotFound(w)
	})

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(
		mw.RequestID(),
		mw.Logger(os.Stdout),
		mw.RecovererWithLogger(log),
	)

	// Pre-routing: normalise double-slashes and dot segments before matching.
	r.Pre(mw.CleanPath())

	// ── Static files — embedded CSS served from the "static/" sub-FS ────────
	//
	// fs.Sub roots the embedded FS at "static/", so ServeFiles receives paths
	// like "/style.css" (the catch-all param value) and finds the file correctly.
	staticFS, err := fs.Sub(files, "static")
	if err != nil {
		log.Error("cannot create static sub-FS", "err", err)
		os.Exit(1)
	}
	r.ServeFiles("/static/*filepath", http.FS(staticFS))

	// ── GET / — home page ─────────────────────────────────────────────────────
	r.GET("/", func(w http.ResponseWriter, _ *http.Request) {
		tmpl.render(w, "home", PageData{})
	})

	// ── GET /about — about page ───────────────────────────────────────────────
	r.GET("/about", func(w http.ResponseWriter, _ *http.Request) {
		tmpl.render(w, "about", PageData{})
	})

	// ── GET /guestbook — list entries ─────────────────────────────────────────
	//
	// Reads the ?ok=1 query parameter written by the POST redirect and converts
	// it into a flash message shown once. No server-side session is needed.
	r.GET("/guestbook", func(w http.ResponseWriter, r *http.Request) {
		data := PageData{Entries: store.all()}
		if r.URL.Query().Get("ok") == "1" {
			data.Flash = "Your entry has been added — thank you!"
			data.FlashType = "success"
		}
		tmpl.render(w, "guestbook", data)
	})

	// ── POST /guestbook — submit entry ────────────────────────────────────────
	//
	// Validates the form, stores the entry, then redirects to GET /guestbook
	// (POST–Redirect–GET pattern). On validation failure, the page is re-rendered
	// with the submitted values and per-field errors so the user loses no input.
	r.POST("/guestbook", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		message := r.FormValue("message")

		errs, ok := validateGuestbook(name, message)
		if !ok {
			// Re-render the form with submitted values and error messages.
			tmpl.render(w, "guestbook", PageData{
				Entries: store.all(),
				Form:    map[string]string{"name": name, "message": message},
				Errors:  errs,
			})
			return
		}

		store.add(name, message)

		// Redirect to GET to prevent duplicate submissions on browser refresh.
		http.Redirect(w, r, "/guestbook?ok=1", http.StatusSeeOther)
	})

	// ── Start ─────────────────────────────────────────────────────────────────
	log.Info("listening", "addr", ":8080", "url", "http://localhost:8080")
	if err := http.ListenAndServe(":8080", r); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
