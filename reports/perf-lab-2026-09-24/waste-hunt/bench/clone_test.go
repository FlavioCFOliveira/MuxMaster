package bench

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: Mount / ServeFiles / Group.ServeFiles per-request request copy.
//
// mux.go mountAt (and ServeFiles, group.go ServeFiles) do, on EVERY request:
//
//	r2 := r.Clone(r.Context())   // deep copy: *Request, URL, Header map (+ every
//	                             // []string), Trailer, TransferEncoding, Form...
//	r2.URL = new(url.URL)        // a SECOND url.URL allocation ...
//	*r2.URL = *r.URL             // ... overwriting the one Clone just made
//
// Two separable wastes:
//   (1) redundant: Clone already deep-copied the URL; the new(url.URL) +
//       copy throws that copy away (1 alloc + 144 B per request, zero effect);
//   (2) oversized: only r.URL is modified, but Clone also deep-copies the
//       header map. net/http's own http.StripPrefix — the stdlib equivalent
//       of Mount — does a shallow *Request copy plus a fresh URL.

const mountParam = "mux_mount"

// mountNoRedundantURL removes waste (1) only; it keeps the deep Clone.
func mountNoRedundantURL(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := mm.PathParam(r, mountParam)
		if p == "" {
			p = "/"
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = p
		if r.URL.RawPath != "" {
			trimmed := strings.TrimPrefix(r.URL.RawPath, prefix)
			switch {
			case len(trimmed) == len(r.URL.RawPath):
				r2.URL.RawPath = ""
			case trimmed != "" && trimmed[0] != '/':
				r2.URL.RawPath = ""
			default:
				r2.URL.RawPath = trimmed
			}
		}
		h.ServeHTTP(w, r2)
	})
}

// mountShallow removes (1) and (2): the http.StripPrefix copy strategy.
func mountShallow(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := mm.PathParam(r, mountParam)
		if p == "" {
			p = "/"
		}
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p
		if r.URL.RawPath != "" {
			trimmed := strings.TrimPrefix(r.URL.RawPath, prefix)
			switch {
			case len(trimmed) == len(r.URL.RawPath):
				r2.URL.RawPath = ""
			case trimmed != "" && trimmed[0] != '/':
				r2.URL.RawPath = ""
			default:
				r2.URL.RawPath = trimmed
			}
		}
		h.ServeHTTP(w, r2)
	})
}

func BenchmarkMount(b *testing.B) {
	run := func(b *testing.B, m *mm.Mux) {
		r := realisticRequest(http.MethodGet, "/docs/v2/guide/index.html")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			m.ServeHTTP(w, r)
			w.reset()
		}
	}
	b.Run("current", func(b *testing.B) {
		m := mm.New()
		m.Mount("/docs/v2", nop)
		run(b, m)
	})
	b.Run("no-redundant-url", func(b *testing.B) {
		m := mm.New()
		m.Handle("*", "/docs/v2/*"+mountParam, mountNoRedundantURL("/docs/v2", nop))
		run(b, m)
	})
	b.Run("shallow-copy", func(b *testing.B) {
		m := mm.New()
		m.Handle("*", "/docs/v2/*"+mountParam, mountShallow("/docs/v2", nop))
		run(b, m)
	})
	// Reference: the same catch-all dispatch with a no-op handler, i.e. the
	// cost that is NOT attributable to the per-request copy.
	b.Run("dispatch-only", func(b *testing.B) {
		m := mm.New()
		m.Handle("*", "/docs/v2/*"+mountParam, nop)
		run(b, m)
	})
}

// ServeFiles closure: exact replica of mux.go ServeFiles' handler with the
// file server replaced by a no-op, so only the copy strategy is measured.
func serveFilesCurrentReplica(paramName string, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = mm.PathParam(r, paramName)
		fs.ServeHTTP(w, r2)
	})
}

func serveFilesShallow(paramName string, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = mm.PathParam(r, paramName)
		fs.ServeHTTP(w, r2)
	})
}

func BenchmarkServeFilesCopy(b *testing.B) {
	run := func(b *testing.B, h http.Handler) {
		m := mm.New()
		m.GET("/assets/*filepath", h.ServeHTTP)
		r := realisticRequest(http.MethodGet, "/assets/css/style.css")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			m.ServeHTTP(w, r)
			w.reset()
		}
	}
	b.Run("current-replica", func(b *testing.B) { run(b, serveFilesCurrentReplica("filepath", nop)) })
	b.Run("shallow-copy", func(b *testing.B) { run(b, serveFilesShallow("filepath", nop)) })
}

// ── Same mechanism in middleware/clean_path.go (and strip_slashes.go): when
// the path changes, r.Clone deep-copies the whole request (header map
// included) although only r.URL is modified. cleanPathShallow replicates
// CleanPath exactly, with the http.StripPrefix copy strategy.
func cleanPathShallow() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := path.Clean(r.URL.Path)
			rawP := r.URL.RawPath
			if p == r.URL.Path && rawP == "" {
				next.ServeHTTP(w, r)
				return
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = p
			if rawP != "" {
				cleanedRaw := path.Clean(rawP)
				if cleanedRaw != rawP {
					r2.URL.RawPath = ""
				} else if decoded, err := url.PathUnescape(rawP); err == nil && decoded != p {
					r2.URL.RawPath = ""
				} else {
					r2.URL.RawPath = cleanedRaw
				}
			}
			next.ServeHTTP(w, r2)
		})
	}
}

func BenchmarkCleanPathChanged(b *testing.B) {
	run := func(b *testing.B, h http.Handler) {
		r := realisticRequest(http.MethodGet, "/docs/v2/")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			h.ServeHTTP(w, r)
		}
	}
	b.Run("current", func(b *testing.B) { run(b, mw.CleanPath()(nop)) })
	b.Run("shallow-copy", func(b *testing.B) { run(b, cleanPathShallow()(nop)) })
}
