// Shared test helpers for the S10-PreCSA harness.
package s10_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// newMux returns a fresh Mux with production-safe defaults.
func newMux() *mm.Mux {
	return mm.New()
}

// pathParamFromRequest extracts a named path param from the request context.
func pathParamFromRequest(r *http.Request, name string) string {
	return mm.PathParam(r, name)
}

// recoverMiddleware returns a Pre-compatible middleware that recovers panics
// using middleware.RecovererWithLogger (nil logger → slog.Default).
func recoverMiddleware(_ *testing.T) func(http.Handler) http.Handler {
	return middleware.Recoverer()
}

// fakeIntrospectSrv is a minimal OAuth2 introspection endpoint for testing.
type fakeIntrospectSrv struct {
	active bool
}

func (f *fakeIntrospectSrv) Start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/introspect" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{
				"active": f.active,
				"sub":    "test-user",
				"exp":    9999999999, // far future
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
}
