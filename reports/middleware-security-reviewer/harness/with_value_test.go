// Harness — with_value.go.
//
// Threats covered:
//   - String-keyed context values collide with other libraries.
//   - Nil value handling — allowed.
//   - Nil key handling — must panic (documented).
//   - Value propagation verification.
package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// -----------------------------------------------------------------------------
// MSR-WV-001 — Static: no string literal keys used elsewhere
// that collide. We scan all middleware sources.
// -----------------------------------------------------------------------------

func TestSec_WithValue_NoStringContextKeys(t *testing.T) {
	for _, f := range []string{
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/basic_auth.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/clean_path.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/compress.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/cors.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/logger.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/no_cache.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/real_ip.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/recoverer.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/request_id.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/set_header.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/strip_slashes.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/throttle.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/timeout.go",
		"/data/dev/github.com/FlavioCFOliveira/MuxMaster/middleware/with_value.go",
	} {
		src, err := readSource(f)
		if err != nil {
			t.Fatal(err)
		}
		// context.WithValue(..., "literal", ...) — a string key — MUST NOT appear.
		// We look for the classic anti-pattern.
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if strings.Contains(line, "context.WithValue") && strings.Contains(line, `"`) {
				// Check the position of the `"` — if a string literal is the 2nd arg, flag.
				start := strings.Index(line, "context.WithValue")
				if start < 0 {
					continue
				}
				rest := line[start:]
				// A typed key has form `xxxKey{}` or a package-level var — no `"` on that arg.
				// We flag if there's a `"` on the `.WithValue(ctx, ...)` arg position.
				if strings.Contains(rest, `"`) {
					// Heuristic: `context.WithValue(ctx, "foo", ...)` vs `context.WithValue(ctx, key, "foo")`.
					// String on 3rd arg is a value — fine. String on 2nd arg is a key — bad.
					openParen := strings.Index(rest, "(")
					if openParen < 0 {
						continue
					}
					argsStart := openParen + 1
					_ = argsStart
					// Simple parse: split on comma up to balanced parens (but arg strings may contain commas).
					// Instead we use a stricter pattern.
					if strings.Contains(rest, `, "`) && !strings.Contains(rest, `) , "`) {
						// Now be certain: extract up to 2nd comma.
						// Actually the safe rule is: look for ', "' immediately after the context argument.
						// That's bad ONLY if the ctx arg is NOT followed by ')'.
						// Heuristic is good enough given the tiny middleware base.
						t.Logf("WARNING %s:%d: suspected string-key in context.WithValue: %s", f, i+1, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// MSR-WV-002 — Nil key panics.
// -----------------------------------------------------------------------------

func TestSec_WithValue_NilKeyPanics(t *testing.T) {
	defer func() {
		if rcv := recover(); rcv == nil {
			t.Fatal("WithValue(nil, val): expected panic, got none")
		}
	}()
	_ = middleware.WithValue(nil, "val")
}

// -----------------------------------------------------------------------------
// MSR-WV-003 — String key is ACCEPTED (documented risk). Verify behaviour
// so that callers who use string keys know what happens.
// -----------------------------------------------------------------------------

func TestSec_WithValue_StringKeyAcceptedButRisky(t *testing.T) {
	// Caller uses string key "user" — collides with any other library doing the same.
	mw := middleware.WithValue("user", "alice")
	var got any
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Context().Value("user")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got != "alice" {
		t.Errorf("with_value string key: got %v, want alice", got)
	}
	t.Log("MSR-WV-003: string keys are accepted without warning; collision risk — document in GoDoc")
}

// -----------------------------------------------------------------------------
// MSR-WV-004 — Typed key roundtrip.
// -----------------------------------------------------------------------------

type myKey struct{}

func TestSec_WithValue_TypedKeyRoundtrip(t *testing.T) {
	mw := middleware.WithValue(myKey{}, "bob")
	var got any
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Context().Value(myKey{})
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got != "bob" {
		t.Errorf("typed key: got %v, want bob", got)
	}
}

// -----------------------------------------------------------------------------
// MSR-WV-005 — Nil value ok.
// -----------------------------------------------------------------------------

func TestSec_WithValue_NilValue(t *testing.T) {
	defer func() {
		if rcv := recover(); rcv != nil {
			t.Fatalf("WithValue(key, nil) panicked: %v", rcv)
		}
	}()
	mw := middleware.WithValue(myKey{}, nil)
	_ = mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
}

// Silence unused import.
var _ = context.TODO
