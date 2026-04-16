package middleware

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuth enforces HTTP Basic Authentication. Panics if creds is nil.
func BasicAuth(realm string, creds map[string]string) func(http.Handler) http.Handler {
	if creds == nil {
		panic("middleware: BasicAuth requires non-nil credentials map")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if ok {
				if expected, found := creds[user]; found {
					if subtle.ConstantTimeCompare([]byte(pass), []byte(expected)) == 1 {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		})
	}
}
