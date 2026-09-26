// Regression tests for a CWE-532 finding against OAuth2Introspect's
// construction-time validation (rmp #280): a panic message rendered the
// full Endpoint URL — including userinfo credentials
// (e.g. "https://user:secret@host/introspect") — in full. The earlier
// TM-2026-005 fix only redacted the slog.Warn/slog.Info construction-time
// log lines; the panic paths (malformed URL, missing host, embedded
// userinfo) were never audited and kept leaking the raw string. These
// tests pin the redaction so the password (and, for consistency with the
// slog policy, the username too) never reaches a panic message.
package middleware_test

import (
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const (
	oauth2TestUser = "svc-account"
	oauth2TestPass = "S3cr3t-P4ssw0rd!"
)

// recoverPanicText constructs the middleware and returns the panic message
// it must have raised, failing the test if it did not panic at all.
func recoverPanicText(t *testing.T, opts middleware.OAuth2Options) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("OAuth2Introspect(%+v) did not panic, want a construction-time panic", opts)
		}
		s, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is %T, want string", r)
		}
		msg = s
	}()
	middleware.OAuth2Introspect(opts)
	return ""
}

// assertNoCredentials fails the test if the panic text contains the raw
// password or username used to construct the malicious Endpoint.
func assertNoCredentials(t *testing.T, msg string) {
	t.Helper()
	if strings.Contains(msg, oauth2TestPass) {
		t.Fatalf("panic message leaks the password: %q", msg)
	}
	if strings.Contains(msg, oauth2TestUser) {
		t.Fatalf("panic message leaks the username: %q", msg)
	}
	if strings.Contains(msg, oauth2TestUser+":"+oauth2TestPass) {
		t.Fatalf("panic message leaks the full userinfo component: %q", msg)
	}
}

// TestSec_OAuth2Introspect_UserinfoEndpointPanic_RedactsCredentials covers
// the exact defect reported: a well-formed URL whose Endpoint embeds
// "user:pass@" is rejected by the TM-2026-004 userinfo check, and the
// resulting panic message must not render either half of the credential.
func TestSec_OAuth2Introspect_UserinfoEndpointPanic_RedactsCredentials(t *testing.T) {
	endpoint := "https://" + oauth2TestUser + ":" + oauth2TestPass + "@idp.example.com/introspect"
	msg := recoverPanicText(t, middleware.OAuth2Options{Endpoint: endpoint})
	assertNoCredentials(t, msg)
	if !strings.Contains(msg, "userinfo") {
		t.Fatalf("panic message %q does not identify the userinfo violation", msg)
	}
	// The redacted host must still be present — only the credentials are
	// stripped, so operators can still tell which endpoint was misconfigured.
	if !strings.Contains(msg, "idp.example.com") {
		t.Fatalf("panic message %q lost the host entirely; only userinfo should be redacted", msg)
	}
}

// TestSec_OAuth2Introspect_UserinfoOnlyNoPasswordEndpointPanic_RedactsUsername
// covers the "user@host" (no password) form: even though there is no
// password to leak, the fix also drops the username for consistency with
// the TM-2026-005 slog policy (host+scheme only, never any userinfo).
func TestSec_OAuth2Introspect_UserinfoOnlyNoPasswordEndpointPanic_RedactsUsername(t *testing.T) {
	endpoint := "https://" + oauth2TestUser + "@idp.example.com/introspect"
	msg := recoverPanicText(t, middleware.OAuth2Options{Endpoint: endpoint})
	if strings.Contains(msg, oauth2TestUser) {
		t.Fatalf("panic message leaks the username even with no password present: %q", msg)
	}
}

// TestSec_OAuth2Introspect_NoHostWithUserinfoPanic_RedactsCredentials covers
// the "https://user:pass@" edge case (empty host, non-nil User): this hits
// the "has no host" panic branch, not the userinfo branch, and must also
// redact.
func TestSec_OAuth2Introspect_NoHostWithUserinfoPanic_RedactsCredentials(t *testing.T) {
	endpoint := "https://" + oauth2TestUser + ":" + oauth2TestPass + "@"
	msg := recoverPanicText(t, middleware.OAuth2Options{Endpoint: endpoint})
	assertNoCredentials(t, msg)
	if !strings.Contains(msg, "no host") {
		t.Fatalf("panic message %q does not identify the missing-host violation", msg)
	}
}

// TestSec_OAuth2Introspect_MalformedURLWithUserinfoPanic_RedactsCredentials
// covers the url.Parse-failure path: an Endpoint string that (a) embeds
// credentials and (b) is otherwise malformed enough that url.Parse itself
// returns an error. url.Error.Error() echoes the raw URL verbatim
// ("parse \"<url>\": <reason>"), so naively wrapping err.Error() in the
// panic — as the code did before this fix in the OTHER two branches —
// would leak the credentials via this branch too, even though it was not
// the specific line named in the report. A literal ASCII control byte
// (0x7f) after the userinfo makes url.Parse reject the string outright.
func TestSec_OAuth2Introspect_MalformedURLWithUserinfoPanic_RedactsCredentials(t *testing.T) {
	endpoint := "https://" + oauth2TestUser + ":" + oauth2TestPass + "@idp.example.com/\x7finvalid"
	msg := recoverPanicText(t, middleware.OAuth2Options{Endpoint: endpoint})
	assertNoCredentials(t, msg)
	if !strings.Contains(msg, "malformed Endpoint URL") {
		t.Fatalf("panic message %q does not identify the malformed-URL violation", msg)
	}
}

// TestSec_OAuth2Introspect_PlainEndpointPanic_Unaffected is a control case:
// an Endpoint with no userinfo at all must still panic for the reason it
// always did (non-HTTPS — this branch never rendered opts.Endpoint at all,
// so it was never part of the CWE-532 defect), completely unaffected by the
// redaction change, proving the fix does not swallow or alter unrelated
// validation paths.
func TestSec_OAuth2Introspect_PlainEndpointPanic_Unaffected(t *testing.T) {
	msg := recoverPanicText(t, middleware.OAuth2Options{Endpoint: "http://idp.example.com/introspect"})
	if !strings.Contains(msg, "https://") {
		t.Fatalf("panic message %q does not describe the HTTPS requirement", msg)
	}
}
