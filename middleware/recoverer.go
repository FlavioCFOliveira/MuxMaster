package middleware

import (
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
)

// Recoverer recovers from panics, writes 500 if headers not sent, logs to stderr.
func Recoverer() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rcv := recover(); rcv != nil {
					fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", rcv, debug.Stack())
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
