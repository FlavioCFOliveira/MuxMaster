package middleware

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logger logs each request after it completes. Panics if out is nil.
func Logger(out io.Writer) func(http.Handler) http.Handler {
	if out == nil {
		panic("middleware: Logger requires a non-nil writer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			fmt.Fprintf(out, "%s %s %s %d %s\n",
				time.Now().Format(time.RFC3339),
				r.Method,
				r.URL.Path,
				rec.status,
				time.Since(start),
			)
		})
	}
}
