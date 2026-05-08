package harness

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestR2_07_Deep_EmptySegmentRegexBypass
// When regex param {id:[a-z]*} is used, the empty match ([a-z]* matches empty string)
// allows //profile (double-slash → empty segment before /profile) to reach the handler.
// Security implication: if auth middleware distinguishes between /user/profile and
// //profile (e.g., WAF rules, nginx normalisation before forwarding), this could
// allow bypassing WAF rules that only consider /profile.
// Key: does getValue see the empty segment or does the double-slash skip the regex check?
func TestR2_07_Deep_RegexEmptySegmentMechanism(t *testing.T) {
	r := mm.New()
	var capturedID string
	// Note: /admin would conflict with /{id:[a-z]*} since admin matches [a-z]*
	// Register the regex param route only
	r.GET("/{id:[a-z]*}/profile", func(w http.ResponseWriter, req *http.Request) {
		capturedID = mm.PathParam(req, "id")
		w.Header().Set("X-Handler", "profile")
		w.WriteHeader(200)
	})

	// How does getValue handle //profile?
	// URL //profile: net/http parses this; URL.Path = //profile (two slashes preserved)
	// In getValue: path = //profile (starts with /). Tree root has /. Prefix match
	// removes first char. Remaining: /profile. Wait, that's wrong.
	// Actually: tree has "{id:[a-z]*}/profile" registered after "/"
	// Tree structure: root "/" → child "{id:[a-z]*}" → child "/profile"
	// getValue("//profile"): after consuming root's prefix "/", path="/profile"
	// Then regex node: looks for segment end (/) in path="/profile"
	// path[0]='/' so end=0, seg="" → matches [a-z]* (empty)
	// captures id="" then path="/profile" → matches static "/profile" → handler!
	// So: //profile → id="" because '/' is treated as segment boundary.

	capturedID = ""
	req1 := httptest.NewRequest("GET", "http://example.com//profile", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	t.Logf("REGEX-EMPTY: //profile → code=%d handler=%q id=%q", w1.Code, w1.Header().Get("X-Handler"), capturedID)

	// The concern: //profile could bypass auth checks that pattern-match on the
	// specific URL /user/profile (expecting the id segment).
	// Also: the empty segment means id="" — any code that uses id as a user lookup
	// would get an empty string, which might match a default/admin user.

	// What about //admin? Does the empty segment + regex allow //admin → /admin?
	capturedID = ""
	req2 := httptest.NewRequest("GET", "http://example.com//admin", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	t.Logf("REGEX-EMPTY: //admin → code=%d handler=%q id=%q", w2.Code, w2.Header().Get("X-Handler"), capturedID)
	// What about the security impact?
	// Route: /{id:[a-z]*}/profile
	// A WAF might have a rule: deny if path matches /*/profile (where * is non-empty)
	// An attacker sends //profile (empty segment) — the WAF sees two-slash path,
	// but the router matches it with id="".
	// This is a WAF bypass vector if the WAF normalises paths differently from the router.
	if w1.Code == 200 {
		t.Logf("FINDING R2-07: Regex {id:[a-z]*} with empty-match allows //profile to reach handler with id='' — WAF bypass risk if WAF doesn't normalize double-slash before pattern matching")
	}
}
