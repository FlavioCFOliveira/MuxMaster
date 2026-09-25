// Security-focused white-box regression tests for the sprint-18 rewrite of
// RequestID (rmp #245): batched crypto/rand via sync.Pool and the fused
// requestIDCtx node.
//
// MSR-2026-0072: the reviewed diff added a fallback to math/rand/v2 on a
// crypto/rand.Read error inside nextRandomID. Direct testing below proves
// that branch can never execute: as of Go 1.24 (https://go.dev/issue/66821,
// confirmed on this project's Go toolchain), crypto/rand.Read never returns
// an error to its caller under ANY Reader implementation — on an
// unrecoverable entropy-source failure it crashes the whole process
// (runtime fatal, not a panic, bypassing defer/recover) before Read itself
// returns. The fallback code was therefore unreachable dead code sitting
// behind a comment that inaccurately described the runtime's actual
// behaviour (a graceful per-request degradation that can never happen) and
// has been removed from request_id.go; see that file's nextRandomID doc
// comment for the production-code side of this fix.
//
// Because triggering the real failure crashes the process, it cannot be
// exercised in-process without taking down the whole test binary (an
// earlier version of this test attempted exactly that and failed the
// entire `go test ./middleware/...` run with "fatal error: crypto/rand:
// failed to read random data" — not a normal test failure). The correct,
// safe way to observe and pin this behaviour is a subprocess: re-exec the
// test binary with an environment flag that installs a failing
// crypto/rand.Reader and calls nextRandomID, then assert on the child
// process's exit status and output from the parent.
package middleware

import (
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// requestIDCryptoRandCrashEnv is the environment variable this test uses to
// re-exec itself as the subprocess that triggers the crypto/rand failure.
const requestIDCryptoRandCrashEnv = "MUXMASTER_TEST_TRIGGER_CRYPTO_RAND_FAILURE"

// failingReader is an io.Reader that always fails, standing in for a broken
// OS entropy source (the exact condition crypto/rand.Reader's docs say
// crashes the process).
type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	return 0, errors.New("injected failure: OS entropy source unavailable")
}

// TestSec_NextRandomID_CryptoRandDefaultFailureModeCrashesProcess_NotFallback
// is both the subprocess driver AND (via the environment-flag branch) the
// subprocess body. Run normally (no env flag), it re-execs itself with the
// flag set, capturing the child's combined output and exit status, and
// asserts:
//   - the child process CRASHES (non-zero, abnormal exit) rather than
//     returning normally from nextRandomID — proving the math/rand/v2
//     fallback this review removed could never have run in production;
//   - the crash carries crypto/rand's own documented fatal message, not a
//     Go panic (confirming this is runtime.fatal, unrecoverable even by a
//     deferred recover(), which is exactly why no in-process fallback is
//     possible);
//   - the child never reaches the sentinel print statement placed
//     immediately after the nextRandomID() call, i.e. control genuinely
//     never returns from rand.Read on failure.
func TestSec_NextRandomID_CryptoRandDefaultFailureModeCrashesProcess_NotFallback(t *testing.T) {
	if os.Getenv(requestIDCryptoRandCrashEnv) == "1" {
		// Subprocess body: install a failing Reader and call the real,
		// production nextRandomID. This line is expected to crash the
		// process (runtime fatal) and never return.
		rand.Reader = failingReader{}
		_ = nextRandomID()
		// If we ever reach this line, crypto/rand.Read returned instead of
		// crashing on a Reader error — the stdlib contract this fix relies
		// on has changed.
		os.Stdout.WriteString("SENTINEL_REACHED_AFTER_RAND_READ_WITHOUT_CRASH\n")
		os.Exit(0)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSec_NextRandomID_CryptoRandDefaultFailureModeCrashesProcess_NotFallback$", "-test.v")
	cmd.Env = append(os.Environ(), requestIDCryptoRandCrashEnv+"=1")
	out, runErr := cmd.CombinedOutput()

	if runErr == nil {
		t.Fatalf("subprocess exited successfully instead of crashing — crypto/rand.Read's "+
			"never-returns-an-error-to-the-caller contract no longer holds on this toolchain; "+
			"nextRandomID's fallback-removal fix (see request_id.go) needs re-review. output:\n%s", out)
	}
	if strings.Contains(string(out), "SENTINEL_REACHED_AFTER_RAND_READ_WITHOUT_CRASH") {
		t.Fatalf("subprocess reached the line after nextRandomID() despite the injected Reader failure — "+
			"rand.Read returned instead of crashing. output:\n%s", out)
	}
	if !strings.Contains(string(out), "crypto/rand: failed to read random data") {
		t.Fatalf("subprocess crashed (exit err=%v) but not with crypto/rand's documented fatal message — "+
			"cannot confirm this was the expected crash. output:\n%s", runErr, out)
	}
}
