package bench

import (
	"hash/maphash"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: ThrottlePerIP per-request work that does not depend on the
// request being throttled.
//
// middleware/throttle.go, on EVERY request:
//   (a) time.NewTimer(timeout) + deferred Stop, even when a token is
//       immediately available (the overwhelmingly common case);
//   (b) when the key has no request in flight (refs==0 → entry deleted by
//       decRefs), the next request for that key re-creates the entry:
//       make(chan struct{}, limit) plus `limit` channel sends to fill it —
//       O(limit) work and a limit-sized allocation per non-overlapping
//       request, i.e. for every request of a client that does not pipeline.
//
// throttleAlt is a replica of ThrottlePerIPCapped (same 64-way sharded table,
// same refs/size accounting and maxTableSize cap) that changes only:
//   (a') a non-blocking token receive first; the timer is created only when
//        the request must actually wait;
//   (b') entries whose refs drop to 0 are recycled through a sync.Pool keyed
//        by nothing but the (fixed) limit: at refs==0 every token has been
//        returned, so the channel is full and reusable as-is.

const altShards = 64

type altEntry struct {
	tokens chan struct{}
	refs   int
}

type altShard struct {
	mu sync.Mutex
	m  map[string]*altEntry
}

type altTable struct {
	seed         maphash.Seed
	shards       [altShards]altShard
	size         atomic.Int64
	maxTableSize int
	limit        int
	reuse        bool
	pool         sync.Pool
}

func newAltTable(maxTableSize, limit int, reuse bool) *altTable {
	t := &altTable{seed: maphash.MakeSeed(), maxTableSize: maxTableSize, limit: limit, reuse: reuse}
	for i := range t.shards {
		t.shards[i].m = make(map[string]*altEntry)
	}
	t.pool.New = func() any {
		e := &altEntry{tokens: make(chan struct{}, limit)}
		for range limit {
			e.tokens <- struct{}{}
		}
		return e
	}
	return t
}

func (t *altTable) shardFor(key string) *altShard {
	return &t.shards[maphash.String(t.seed, key)&(altShards-1)]
}

func (t *altTable) acquire(key string) (chan struct{}, *altEntry, bool) {
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
	var e *altEntry
	if t.reuse {
		e = t.pool.Get().(*altEntry)
	} else {
		e = t.pool.New().(*altEntry)
	}
	e.refs = 1
	sh.m[key] = e
	sh.mu.Unlock()
	return e.tokens, e, false
}

func (t *altTable) decRefs(key string, e *altEntry) {
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
		if t.reuse {
			t.pool.Put(e) // full channel: every token was returned before decRefs
		}
	}
}

func throttleAlt(limit int, timeout time.Duration, maxTableSize int, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return throttleVariant(limit, timeout, maxTableSize, keyFn, true, true)
}

// throttleVariant: fastPath applies change (a'); reuse applies (b').
func throttleVariant(limit int, timeout time.Duration, maxTableSize int, keyFn func(*http.Request) string, fastPath, reuse bool) func(http.Handler) http.Handler {
	if keyFn == nil {
		keyFn = func(r *http.Request) string {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			return host
		}
	}
	table := newAltTable(maxTableSize, limit, reuse)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			ch, e, full := table.acquire(key)
			if full {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			gotFast := false
			if fastPath {
				select {
				case <-ch:
					gotFast = true
				default:
				}
			}
			if !gotFast {
				timer := time.NewTimer(timeout)
				select {
				case <-ch:
					timer.Stop()
				case <-timer.C:
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

// peerHost is byte-for-byte the default keyFn ThrottlePerIPCapped installs
// when keyFn is nil; passing it explicitly only avoids the construction-time
// slog warning interleaving with benchmark output.
func peerHost(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func BenchmarkThrottlePerIP(b *testing.B) {
	// rest-api example: ThrottlePerIP(50, 10s, nil) — keyed on the peer host.
	const limit = 50
	run := func(b *testing.B, h http.Handler) {
		r := realisticRequest(http.MethodGet, "/api/v1/books/1")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			h.ServeHTTP(w, r)
		}
	}
	b.Run("sequential-one-client/current", func(b *testing.B) {
		run(b, mw.ThrottlePerIPCapped(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost)(nop))
	})
	b.Run("sequential-one-client/alternative", func(b *testing.B) {
		run(b, throttleAlt(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost)(nop))
	})
	b.Run("sequential-one-client/alt-timer-fastpath-only", func(b *testing.B) {
		run(b, throttleVariant(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost, true, false)(nop))
	})
	b.Run("sequential-one-client/alt-entry-reuse-only", func(b *testing.B) {
		run(b, throttleVariant(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost, false, true)(nop))
	})
	// Many distinct clients, each with requests that never overlap: every
	// request finds refs==0 for its key.
	runMany := func(b *testing.B, h http.Handler) {
		reqs := make([]*http.Request, 256)
		for i := range reqs {
			reqs[i] = realisticRequest(http.MethodGet, "/api/v1/books/1")
			reqs[i].RemoteAddr = "198.51.100." + strconv.Itoa(i%250) + ":40000"
		}
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			h.ServeHTTP(w, reqs[i&255])
		}
	}
	b.Run("many-clients/current", func(b *testing.B) {
		runMany(b, mw.ThrottlePerIPCapped(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost)(nop))
	})
	b.Run("many-clients/alternative", func(b *testing.B) {
		runMany(b, throttleAlt(limit, 10*time.Second, mw.DefaultThrottlePerIPMaxTableSize, peerHost)(nop))
	})
}
