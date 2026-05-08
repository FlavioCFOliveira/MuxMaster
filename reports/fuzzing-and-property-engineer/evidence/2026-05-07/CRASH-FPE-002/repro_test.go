// Package crash_fpe_002 is a minimal reproduction for FPE-002.
//
// FPE-002: Mux.Mount panics with "muxmaster: path contains invalid UTF-8: '/\xd1/*mux_mount'"
// when the prefix contains invalid UTF-8 bytes. The panic originates in tree.go's
// addRouteInternal (line 133) and leaks the internal catch-all name "mux_mount".
//
// Input: Mount("/\xd1", inner)  — prefix containing truncated UTF-8 continuation byte.
//
// Severity: Medium — production code calling Mount with user-supplied prefix can
// be caused to panic. mount() should validate prefix UTF-8 before registering.
// Information disclosure: panic message reveals internal "*mux_mount" parameter name.
//
// Recommended fix: add utf8.ValidString(prefix) check in mountAt(), returning or
// panicking with a clean "muxmaster: Mount prefix contains invalid UTF-8" message
// before concatenating "*mux_mount".
package crash_fpe_002

import (
	"net/http"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

func TestRepro_FPE002_InvalidUTF8MountPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid-UTF-8 Mount prefix but got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T %v", r, r)
		}
		t.Logf("panic message: %q", msg)

		// Confirm: message must mention invalid UTF-8.
		expected := "invalid UTF-8"
		found := false
		for i := 0; i <= len(msg)-len(expected); i++ {
			if msg[i:i+len(expected)] == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected panic message: %q", msg)
		}
		// Security: message leaks internal "*mux_mount" name — confirm presence.
		if containsSubstr(msg, "mux_mount") {
			t.Logf("DISCLOSURE: panic message leaks internal param name 'mux_mount': %q", msg)
		}
	}()

	mux := mm.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	// This panics with invalid UTF-8 in the combined path "/\xd1/*mux_mount".
	mux.Mount("/\xd1", inner)
}

func containsSubstr(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
