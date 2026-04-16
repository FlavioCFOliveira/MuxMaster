package middleware

import (
	"net/http"
	"time"
)

// ThrottleBacklog limits concurrent handler execution with a backlog queue.
// Panics if limit <= 0 or backlog < 0.
func ThrottleBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: ThrottleBacklog limit must be > 0")
	}
	if backlog < 0 {
		panic("middleware: ThrottleBacklog backlog must be >= 0")
	}
	tokens := make(chan struct{}, limit)
	for range limit {
		tokens <- struct{}{}
	}
	queue := make(chan struct{}, backlog)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to acquire a token immediately.
			select {
			case t := <-tokens:
				defer func() { tokens <- t }()
				next.ServeHTTP(w, r)
				return
			default:
			}
			// Queue the request.
			select {
			case queue <- struct{}{}:
			default:
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			// Wait for a token with timeout.
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case t := <-tokens:
				<-queue
				defer func() { tokens <- t }()
				next.ServeHTTP(w, r)
			case <-timer.C:
				<-queue
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			}
		})
	}
}
