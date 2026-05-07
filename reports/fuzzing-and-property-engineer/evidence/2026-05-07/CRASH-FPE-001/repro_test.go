// Package crash_fpe_001 is the regression guard for FPE-2026-001 (rmp #38).
//
// FPE-001: Mux.Handle previously panicked with a message NOT prefixed by
// "muxmaster:" for unnamed wildcard paths such as "/*". The panic originated
// in tree.go's insertChild and produced: "wildcards must be named in path
// '/*'", which violated the project's panic-message convention and made it
// harder for operators to attribute the panic to MuxMaster.
//
// Fixed: every panic emitted from tree.go is now prefixed with "muxmaster:".
// This test asserts both the wildcard-name check and the prefix convention.
package crash_fpe_001

import (
	"net/http"
	"strings"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestRepro_FPE001_UnnamedWildcardPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unnamed wildcard '/*' but got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T %v", r, r)
		}
		t.Logf("panic message: %q", msg)
		if !strings.Contains(msg, "wildcards must be named") {
			t.Errorf("unexpected panic message: %q", msg)
		}
		if !strings.HasPrefix(msg, "muxmaster:") {
			t.Errorf("panic message must be prefixed with \"muxmaster:\" — convention violation: %q", msg)
		}
	}()
	mux := mm.New()
	mux.Handle("POST", "/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
}
