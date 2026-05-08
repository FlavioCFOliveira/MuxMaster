package middleware

import (
	"log/slog"
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

// DefaultThrottlePerIPMaxTableSize is the default upper bound on the number
// of distinct keys ThrottlePerIP will track concurrently. When the table is
// full, requests for NEW keys are rejected with 503 to bound memory under
// IP-churn attacks (MSR-2026-0068). Existing keys keep working.
const DefaultThrottlePerIPMaxTableSize = 100_000

// ThrottlePerIP limits concurrent handler executions per client key.
// keyFn extracts the rate-limit key from the request; if nil, the host part
// of r.RemoteAddr is used. limit is the maximum concurrent requests per key;
// timeout is how long a request waits for a slot before receiving 503.
//
// SECURITY (DOS-2026-0002): when keyFn is nil, ThrottlePerIP keys on
// r.RemoteAddr — which is whatever the TCP peer's address is unless RealIP
// has previously rewritten it. Behind a load balancer that does not strip
// the LB's own address from RemoteAddr, every request appears to come from
// the LB and the per-IP limit degrades to a global rate limit. RealIP
// (with explicit trusted-proxy CIDRs) MUST be registered BEFORE
// ThrottlePerIP for per-client limits to be effective. See SECURITY.md
// "RealIP + ThrottlePerIP ordering".
//
// SECURITY (MSR-2026-0068): the internal per-key table is capped at
// DefaultThrottlePerIPMaxTableSize. When full, requests for NEW keys
// (those not already in the table) receive 503 immediately to bound
// memory under IP-churn attacks. Use ThrottlePerIPCapped to override
// the cap.
//
// Panics if limit <= 0.
func ThrottlePerIP(limit int, timeout time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return ThrottlePerIPCapped(limit, timeout, DefaultThrottlePerIPMaxTableSize, keyFn)
}

// ThrottlePerIPCapped is ThrottlePerIP with an explicit cap on the number
// of distinct keys tracked. maxTableSize <= 0 disables the cap (legacy
// unbounded behaviour, NOT recommended in production).
//
// SATURATION BEHAVIOUR (TM-2026-013, DOS-2026-0057): when the table reaches
// maxTableSize and every slot has refs > 0 (i.e. all entries are in active
// use), new client IPs receive 503 immediately until at least one slot
// drains. An attacker controlling N >= maxTableSize distinct IPs can sustain
// this state for as long as their requests stay open. Mitigations:
//   - deploy upstream DDoS scrubbing (Cloudflare, AWS Shield, etc.);
//   - configure http.Server{ReadHeaderTimeout, IdleTimeout, ReadTimeout} so
//     slow-handler connections cannot hold slots indefinitely;
//   - lower maxTableSize for sensitive endpoints.
//
// Panics if limit <= 0.
func ThrottlePerIPCapped(limit int, timeout time.Duration, maxTableSize int, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: ThrottlePerIP limit must be > 0")
	}
	if keyFn == nil {
		slog.Default().Warn("ThrottlePerIP: nil keyFn — keying on r.RemoteAddr. Ensure RealIP is " +
			"registered BEFORE ThrottlePerIP (with explicit trusted-proxy CIDRs); otherwise " +
			"the per-IP limit degrades to a global rate limit behind any reverse proxy. " +
			"See SECURITY.md \"RealIP + ThrottlePerIP ordering\".")
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

	// decRefs decrements the refs counter and removes an empty entry.
	decRefs := func(key string, e *entry) {
		mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(table, key)
		}
		mu.Unlock()
	}

	// acquire returns (tokens chan, entry, full bool). When the table is at
	// capacity and key is not present, full=true so the caller can reject
	// the request with 503 immediately (MSR-2026-0068).
	acquire := func(key string) (chan struct{}, *entry, bool) {
		mu.Lock()
		e, ok := table[key]
		if !ok {
			if maxTableSize > 0 && len(table) >= maxTableSize {
				mu.Unlock()
				return nil, nil, true
			}
			e = &entry{tokens: make(chan struct{}, limit)}
			for range limit {
				e.tokens <- struct{}{}
			}
			table[key] = e
		}
		e.refs++
		ch := e.tokens
		mu.Unlock()
		return ch, e, false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			ch, e, full := acquire(key)
			if full {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-ch:
				defer func() {
					ch <- struct{}{}
					decRefs(key, e)
				}()
				next.ServeHTTP(w, r)
			case <-timer.C:
				// Token was never consumed — only decrement refs so the
				// entry can be reaped when no callers remain. Avoids the
				// silent refs leak that previously kept the entry alive
				// in the table even after a timeout (MSR-2026-0068).
				decRefs(key, e)
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			}
		})
	}
}
