package middleware

import (
	"net"
	"net/http"
	"sync"
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

// ThrottleAllBacklog is the renamed ThrottleBacklog — limits concurrency globally
// across ALL clients combined. Use ThrottlePerIP for per-client rate limiting.
//
// Deprecated: Use ThrottleAllBacklog. ThrottleBacklog remains for compatibility.
func ThrottleAllBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler {
	return ThrottleBacklog(limit, backlog, timeout)
}

// ThrottlePerIP limits concurrent handler executions per client key.
// keyFn extracts the rate-limit key from the request; if nil, the host part
// of r.RemoteAddr is used. limit is the maximum concurrent requests per key;
// timeout is how long a request waits for a slot before receiving 503.
// Panics if limit <= 0.
func ThrottlePerIP(limit int, timeout time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: ThrottlePerIP limit must be > 0")
	}
	if keyFn == nil {
		keyFn = func(r *http.Request) string {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			return host
		}
	}

	type entry struct {
		tokens chan struct{}
		refs   int
	}
	var mu sync.Mutex
	table := make(map[string]*entry)

	acquire := func(key string) (chan struct{}, func()) {
		mu.Lock()
		e, ok := table[key]
		if !ok {
			e = &entry{tokens: make(chan struct{}, limit)}
			for range limit {
				e.tokens <- struct{}{}
			}
			table[key] = e
		}
		e.refs++
		ch := e.tokens
		mu.Unlock()

		release := func() {
			ch <- struct{}{}
			mu.Lock()
			e.refs--
			if e.refs == 0 {
				delete(table, key)
			}
			mu.Unlock()
		}
		return ch, release
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			ch, release := acquire(key)
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-ch:
				defer release()
				next.ServeHTTP(w, r)
			case <-timer.C:
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			}
		})
	}
}
