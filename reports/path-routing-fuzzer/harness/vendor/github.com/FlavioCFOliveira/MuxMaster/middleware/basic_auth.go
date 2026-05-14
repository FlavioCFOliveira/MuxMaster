package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// BasicAuth enforces HTTP Basic Authentication using constant-time credential
// comparison. Passwords are SHA-256 hashed at construction time so that
// subtle.ConstantTimeCompare always operates on equal-length inputs,
// eliminating both user-enumeration timing (MM-2026-0009) and password-length
// oracle (MM-2026-0020). Panics if creds is nil.
func BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler {
	if creds == nil {
		panic("middleware: BasicAuth requires non-nil credentials map")
	}
	// Sanitise realm to prevent WWW-Authenticate header injection (MM-2026-0038).
	realm = strings.NewReplacer(`"`, `\"`, "\r", "", "\n", "").Replace(realm)

	// Pre-hash all passwords at construction time — zero per-request hashing overhead
	// for the common case where the user is found and authenticated.
	hashedCreds := make(map[string][32]byte, len(creds))
	for user, pass := range creds {
		hashedCreds[user] = sha256.Sum256([]byte(pass))
	}
	// dummyHash is used when the username is not found, ensuring that
	// subtle.ConstantTimeCompare always runs regardless of user existence.
	dummyHash := sha256.Sum256([]byte("muxmaster:dummy-password-for-constant-time"))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			// Always hash the supplied password — prevents short-circuit on !ok.
			passHash := sha256.Sum256([]byte(pass))
			var expectedHash [32]byte
			found := false
			if ok {
				if h, exists := hashedCreds[user]; exists {
					expectedHash = h
					found = true
				}
			}
			if !found {
				expectedHash = dummyHash
			}
			// ConstantTimeCompare on equal-length SHA-256 digests:
			// eliminates both user-enumeration and password-length oracles.
			match := subtle.ConstantTimeCompare(passHash[:], expectedHash[:]) == 1
			if ok && found && match {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		})
	}
}
