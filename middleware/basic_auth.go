package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// basicAuthEntry pairs the SHA-256 digest of a registered username with the
// SHA-256 digest of its password. Storing digests — not raw values — keeps
// every comparison BasicAuth performs operating on fixed-length 32-byte
// inputs, regardless of the length of the caller-supplied username or
// password.
type basicAuthEntry struct {
	userHash [32]byte
	passHash [32]byte
}

// BasicAuth enforces HTTP Basic Authentication using constant-time credential
// comparison. Usernames and passwords are SHA-256 hashed at construction
// time and stored as an unordered slice of {userHash, passHash} entries —
// not a map — so that authenticating a request never performs a
// data-dependent map lookup. Every request scans the ENTIRE entry slice
// unconditionally with subtle.ConstantTimeCompare / subtle.ConstantTimeCopy:
// there is no early exit and no branch whose outcome depends on whether the
// supplied username matches a registered one. This removes the
// user-enumeration timing oracle inherent to Go's `map[string]V` lookup
// (`runtime.mapaccess2_faststr`, whose running time depends on hash-bucket
// occupancy and key comparison), tracked as TSC-2026-0002 in SECURITY.md.
// Cost scales linearly with the number of registered users (O(n) per
// request, always — matched or not); see BenchmarkBasicAuth for the
// per-user overhead. The historical password-length oracle (MM-2026-0020)
// and user-enumeration oracle (MM-2026-0009) remain fixed by the same
// hash-before-compare technique this function has always used. Panics if
// creds is nil.
func BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler {
	if creds == nil {
		panic("middleware: BasicAuth requires non-nil credentials map")
	}
	// Sanitise realm to prevent WWW-Authenticate header injection (MM-2026-0038).
	realm = strings.NewReplacer(`"`, `\"`, "\r", "", "\n", "").Replace(realm)

	// Pre-hash every username and password at construction time — zero
	// per-request hashing overhead beyond the two hashes of the supplied
	// credentials themselves. Map iteration order is randomised by Go at
	// runtime and irrelevant here: the per-request scan below visits every
	// entry regardless of order or match position, so entry order carries
	// no timing signal. creds keys are unique (map), so at most one entry
	// can ever match a given username.
	entries := make([]basicAuthEntry, 0, len(creds))
	for user, pass := range creds {
		entries = append(entries, basicAuthEntry{
			userHash: sha256.Sum256([]byte(user)),
			passHash: sha256.Sum256([]byte(pass)),
		})
	}
	// dummyHash is selected when no entry's username matches, ensuring the
	// final password compare always runs against a well-defined 32-byte
	// digest regardless of whether the supplied username exists.
	dummyHash := sha256.Sum256([]byte("muxmaster:dummy-password-for-constant-time"))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			// Always hash the supplied username and password — prevents
			// short-circuiting on !ok and keeps every compare below
			// operating on fixed-length digests.
			userHash := sha256.Sum256([]byte(user))
			passHash := sha256.Sum256([]byte(pass))

			// Scan every registered entry unconditionally: no early exit, no
			// data-dependent branch. selectedHash accumulates the password
			// digest of whichever entry's username matches (there is at
			// most one); anyMatch records whether any entry matched, as a
			// 0/1 int (not a bool) so the fallback below stays branchless
			// via ConstantTimeCopy.
			var selectedHash [32]byte
			anyMatch := 0
			for i := range entries {
				userMatch := subtle.ConstantTimeCompare(entries[i].userHash[:], userHash[:])
				subtle.ConstantTimeCopy(userMatch, selectedHash[:], entries[i].passHash[:])
				anyMatch |= userMatch
			}
			// No entry matched the username: copy in dummyHash instead of
			// leaving selectedHash at its zero value, so the compare below
			// always runs on a real 32-byte digest either way.
			subtle.ConstantTimeCopy(1-anyMatch, selectedHash[:], dummyHash[:])

			found := anyMatch == 1
			// ConstantTimeCompare on equal-length SHA-256 digests:
			// eliminates both user-enumeration and password-length oracles.
			match := subtle.ConstantTimeCompare(passHash[:], selectedHash[:]) == 1
			if ok && found && match {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		})
	}
}
