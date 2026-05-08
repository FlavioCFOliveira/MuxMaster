package repro_test

// FPE-2026-SAN-01 — sanitiseForLog is not idempotent
//
// Hypothesis: H8-07 / H8-74 (sanitiser exhaustive testing)
// Severity: 3 (Low / cosmetic — one-pass logger, no security implication)
// Found by: TestProp_SanitiserAllControlChars (rapid property test)
//
// Description:
//   `sanitiseForLog` (middleware/logger.go) is implemented as:
//
//       func sanitiseForLog(s string) string {
//           q := strconv.QuoteToASCII(s)
//           return q[1 : len(q)-1]
//       }
//
//   `strconv.QuoteToASCII` escapes the input and wraps it in double-quotes.
//   Stripping the outer quotes gives the log-safe representation. However,
//   if the OUTPUT of a first application is passed back in (a second pass),
//   the backslash sequences are re-escaped:
//
//       s       = `"`          (a single double-quote)
//       pass 1  = `\"`         (backslash + double-quote)
//       pass 2  = `\\\"`       (double-backslash + backslash + double-quote)
//
//   Subsequent passes double the escaping. This means the function is not
//   idempotent: f(f(x)) != f(x) for any input containing `"`, `\`, or any
//   other character that QuoteToASCII escapes.
//
// Security impact:
//   None. The logger applies sanitisation exactly once per log line. There is
//   no code path where sanitiseForLog is called twice on the same value. The
//   safety invariants (no raw control bytes) hold on every pass.
//
// Reproduction:
//   go test -run TestReproFPE2026SAN01 ./...
//
// Recommended action:
//   Document the non-idempotency in a comment next to sanitiseForLog. No code
//   change is required; the function correctly meets its single-use contract.

import (
	"strconv"
	"testing"
)

func sanitiseForLog(s string) string {
	q := strconv.QuoteToASCII(s)
	return q[1 : len(q)-1]
}

func TestReproFPE2026SAN01(t *testing.T) {
	inputs := []string{
		`"`,
		`\"`,
		`hello "world"`,
		`\`,
		`\\`,
	}
	for _, s := range inputs {
		pass1 := sanitiseForLog(s)
		pass2 := sanitiseForLog(pass1)
		if pass1 == pass2 {
			continue // idempotent for this input (not expected for the cases above)
		}
		t.Logf("FPE-2026-SAN-01: s=%q -> pass1=%q -> pass2=%q (not idempotent, as documented)",
			s, pass1, pass2)
		// Verify safety invariant still holds on both passes.
		for i := range len(pass1) {
			if pass1[i] < 0x20 || pass1[i] == 0x7F {
				t.Errorf("control byte 0x%02X in pass1 output: %q", pass1[i], pass1)
			}
		}
		for i := range len(pass2) {
			if pass2[i] < 0x20 || pass2[i] == 0x7F {
				t.Errorf("control byte 0x%02X in pass2 output: %q", pass2[i], pass2)
			}
		}
	}
	t.Log("FPE-2026-SAN-01: safety invariant holds on both passes; non-idempotency is cosmetic only")
}
