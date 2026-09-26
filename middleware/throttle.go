package middleware

import (
	"hash/maphash"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// throttleSem is a counting semaphore with a lock-free fast path and a
// bounded backlog for waiters.
//
// CH-02: the previous implementation used a buffered channel pre-filled
// with `limit` tokens as the semaphore. Every acquire/release pair did a
// non-blocking channel receive/send, which internally serialises on the
// channel's hchan.lock regardless of contention — this anti-scaled under
// high core counts even though the configured limit was never close to
// being saturated (the common production case). The fast path below never
// touches a channel: acquiring a permit while under the limit is a single
// atomic CAS loop. Channels are used ONLY for the rare "at capacity"
// case: a bounded "queue" channel enforces the backlog bound (503 the
// instant it is full, exactly as before), and a bounded "wake" channel
// notifies waiters that a slot may have freed so they can retry the CAS
// loop without busy-polling.
type throttleSem struct {
	inUse atomic.Int64
	limit int64
	queue chan struct{} // bounds the number of concurrent waiters (backlog)
	wake  chan struct{} // notifies waiters that a slot may have freed
}

func newThrottleSem(limit, backlog int) *throttleSem {
	return &throttleSem{
		limit: int64(limit),
		queue: make(chan struct{}, backlog),
		wake:  make(chan struct{}, backlog),
	}
}

// tryAcquire is the lock-free fast path: it succeeds immediately, without
// any channel operation, whenever the in-use count is below limit.
func (s *throttleSem) tryAcquire() bool {
	for {
		cur := s.inUse.Load()
		if cur >= s.limit {
			return false
		}
		if s.inUse.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// acquireWait is the slow path, entered only once tryAcquire has already
// failed once. It reserves a backlog slot (bounded by cap(s.queue)) and
// then waits, up to timeout, to be woken and retry the CAS loop. Returns
// false when the backlog is already full (immediate 503) or the timeout
// fires first.
func (s *throttleSem) acquireWait(timeout time.Duration) bool {
	select {
	case s.queue <- struct{}{}:
	default:
		return false // backlog full — reject immediately (503)
	}
	defer func() { <-s.queue }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if s.tryAcquire() {
			return true
		}
		// No lost wake-ups: a waiter that reaches this select is already
		// blocked on it, so any value later sent to `wake` (buffered, one
		// slot per possible waiter) is delivered to a receiver without
		// delay. A dropped send in release() (buffer momentarily full)
		// never starves a waiter — it only means enough other buffered
		// signals are already queued to make every current waiter retry.
		select {
		case <-s.wake:
			continue
		case <-timer.C:
			return false
		}
	}
}

// release returns a permit and wakes at most one waiter (if any).
func (s *throttleSem) release() {
	s.inUse.Add(-1)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ThrottleBacklog limits concurrent handler execution globally, across all
// clients combined, with a backlog queue. At most limit requests run next at
// the same time; up to backlog further requests wait, each for at most
// timeout, for a free slot. A request that finds the backlog full, or whose
// wait times out, receives 503 Service Unavailable. Use ThrottlePerIP for
// per-client limits.
//
// ThrottleBacklog and ThrottleAllBacklog are equivalent names for the same
// middleware. Panics if limit <= 0 or backlog < 0.
func ThrottleBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: ThrottleBacklog limit must be > 0")
	}
	if backlog < 0 {
		panic("middleware: ThrottleBacklog backlog must be >= 0")
	}
	sem := newThrottleSem(limit, backlog)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fast path: acquire immediately if under the limit — no channel,
			// no lock, just an atomic CAS loop.
			if sem.tryAcquire() {
				defer sem.release()
				next.ServeHTTP(w, r)
				return
			}
			// Slow path: queue and wait (bounded by backlog, up to timeout).
			if !sem.acquireWait(timeout) {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			defer sem.release()
			next.ServeHTTP(w, r)
		})
	}
}

// ThrottleAllBacklog limits concurrent handler execution globally, across all
// clients combined, with a backlog queue. It is an equivalent name for
// ThrottleBacklog: both return the same middleware with the same behaviour
// and panics. Use ThrottlePerIP for per-client limits.
func ThrottleAllBacklog(limit int, backlog int, timeout time.Duration) func(http.Handler) http.Handler {
	return ThrottleBacklog(limit, backlog, timeout)
}

// DefaultThrottlePerIPMaxTableSize is the default upper bound on the number
// of distinct keys ThrottlePerIP will track concurrently. When the table is
// full, requests for NEW keys are rejected with 503 to bound memory under
// IP-churn attacks (MSR-2026-0068). Existing keys keep working.
const DefaultThrottlePerIPMaxTableSize = 100_000

// throttleShardCount is the number of independent shards in the per-key
// table used by ThrottlePerIPCapped. A power of two allows a cheap mask
// instead of a modulo. 64 shards keeps per-shard contention negligible
// even at very high core counts, at a trivial fixed memory cost (64 x
// sync.Mutex + map header) compared to the per-key channel storage the
// table already holds.
const throttleShardCount = 64

// throttleEntry is a per-key rate-limit slot: a channel-based token
// semaphore plus a reference count of requests currently holding (or
// waiting to acquire) one of its tokens.
//
// WH-01: entries are recycled through throttleTable.entryPool instead of
// being discarded when refs reaches zero. This is safe because, by
// construction, every successful acquire() is matched by exactly one
// decRefs() call, and the token it received is ALWAYS sent back to
// e.tokens (in the caller's deferred release) strictly before that
// matching decRefs() call runs — never after. So when the LAST
// outstanding reference for a key is released and refs reaches zero,
// every token ever taken from e.tokens has already been returned: the
// channel is guaranteed to be full (exactly cap(e.tokens) items) and the
// entry can be handed to a brand-new key with only e.refs reset to 1 —
// no channel refill, no allocation. A request that times out waiting
// (never received a token) decrements refs WITHOUT sending, which is
// correctly symmetric: it never took one either.
type throttleEntry struct {
	tokens chan struct{}
	refs   int
}

// throttleShard is one independent lock-and-map pair of the sharded table.
type throttleShard struct {
	mu sync.Mutex
	m  map[string]*throttleEntry
}

// throttleTable is a sharded, capacity-bounded map of per-key rate-limit
// entries.
//
// CH-01: the previous implementation guarded the ENTIRE table with a single
// global sync.Mutex, taken on every request regardless of which key it
// carried — this anti-scaled past 4 cores under a many-distinct-keys
// workload even though per-key limiting itself uses independent channels.
// Sharding removes that global lock from the request path entirely: a key
// is routed to exactly one shard by a seeded hash (hash/maphash with a
// per-instance random seed, so an attacker who can influence key values —
// e.g. spoofed IPs — cannot force every key onto the same shard by
// engineering hash collisions), and unrelated keys contend on independent
// mutexes.
//
// size is an atomic, exact count of live entries across every shard, used
// to enforce maxTableSize via reserve-then-insert: capacity for a new key is
// reserved with a single atomic increment (rolled back immediately if it
// would overshoot the cap) BEFORE the entry is inserted, and released with
// a matching decrement when the entry's refs count reaches zero and it is
// deleted. Because a given key always hashes to the same shard, and every
// lookup/insert/delete for that key happens while holding that shard's
// mutex, there is no time-of-check-to-time-of-use window for a single key:
// two concurrent first-time acquires for the SAME key are fully serialised
// by the shard lock, so at most one reservation is ever consumed per key —
// the cap stays exact under concurrent churn.
type throttleTable struct {
	seed         maphash.Seed
	shards       [throttleShardCount]throttleShard
	size         atomic.Int64
	maxTableSize int
	// entryPool recycles *throttleEntry values whose refs reached zero
	// (WH-01), avoiding the make(chan struct{}, limit) allocation plus the
	// `limit` channel sends that filling a fresh entry costs on every
	// request from a client with no overlapping in-flight request. Left at
	// its zero value deliberately (no New func): Get returns nil on an
	// empty pool, which acquire() below treats as "allocate fresh" — the
	// exact same fallback used when a pooled entry's channel capacity does
	// not match the requested limit (defensive; acquire's limit argument
	// is constant in every current call site, but nothing on this type
	// enforces that).
	entryPool sync.Pool
}

func newThrottleTable(maxTableSize int) *throttleTable {
	t := &throttleTable{
		seed:         maphash.MakeSeed(),
		maxTableSize: maxTableSize,
	}
	for i := range t.shards {
		t.shards[i].m = make(map[string]*throttleEntry)
	}
	return t
}

func (t *throttleTable) shardFor(key string) *throttleShard {
	h := maphash.String(t.seed, key)
	return &t.shards[h&(throttleShardCount-1)]
}

// acquire returns (tokens chan, entry, full bool). When the table is at
// capacity and key is not present, full=true so the caller can reject
// the request with 503 immediately (MSR-2026-0068). Only the shard that
// key hashes to is ever locked.
func (t *throttleTable) acquire(key string, limit int) (chan struct{}, *throttleEntry, bool) {
	sh := t.shardFor(key)
	sh.mu.Lock()
	if e, ok := sh.m[key]; ok {
		e.refs++
		ch := e.tokens
		sh.mu.Unlock()
		return ch, e, false
	}
	if t.maxTableSize > 0 {
		if t.size.Add(1) > int64(t.maxTableSize) {
			t.size.Add(-1)
			sh.mu.Unlock()
			return nil, nil, true
		}
	}
	// WH-01(b): reuse a recycled entry when one is available and its
	// channel was built for this exact limit; a full channel needs no
	// refill. Otherwise allocate and fill a fresh one, as before.
	e, _ := t.entryPool.Get().(*throttleEntry)
	if e == nil || cap(e.tokens) != limit {
		e = &throttleEntry{tokens: make(chan struct{}, limit)}
		for range limit {
			e.tokens <- struct{}{}
		}
	}
	e.refs = 1
	sh.m[key] = e
	sh.mu.Unlock()
	return e.tokens, e, false
}

// decRefs decrements the refs counter and removes an empty entry, releasing
// its capacity reservation. A removed entry is returned to entryPool for
// reuse by a future first-time acquire() (WH-01(b)) — safe per the
// throttleEntry doc comment: its token channel is guaranteed full at this
// point.
func (t *throttleTable) decRefs(key string, e *throttleEntry) {
	sh := t.shardFor(key)
	sh.mu.Lock()
	e.refs--
	removed := e.refs == 0
	if removed {
		delete(sh.m, key)
	}
	sh.mu.Unlock()
	if removed {
		if t.maxTableSize > 0 {
			t.size.Add(-1)
		}
		t.entryPool.Put(e)
	}
}

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

	table := newThrottleTable(maxTableSize)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			ch, e, full := table.acquire(key, limit)
			if full {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			// WH-01(a): try the non-blocking fast path first. A
			// time.NewTimer is only created when the request must
			// actually wait — the overwhelmingly common case is a token
			// being immediately available, and time.NewTimer plus its
			// deferred Stop cost a clock read and a heap allocation that
			// this path never needs. Timeout semantics are unchanged:
			// the timer still starts the instant a request begins
			// waiting.
			select {
			case <-ch:
				// Fast path: got a token without waiting.
			default:
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				select {
				case <-ch:
				case <-timer.C:
					// Token was never consumed — only decrement refs so
					// the entry can be reaped when no callers remain.
					// Avoids the silent refs leak that previously kept
					// the entry alive in the table even after a timeout
					// (MSR-2026-0068).
					table.decRefs(key, e)
					http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
					return
				}
			}
			defer func() {
				ch <- struct{}{}
				table.decRefs(key, e)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
