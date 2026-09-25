package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"unsafe"
)

type requestIDKey struct{}

// requestIDHeaderKey is the canonical MIME header key for "X-Request-ID",
// precomputed once at compile time (== textproto.CanonicalMIMEHeaderKey
// ("X-Request-ID")). "ID" is not itself canonical (canonical form
// capitalises only the first letter of each hyphen-separated segment, then
// lowercases the rest — "ID" -> "Id"), so textproto.CanonicalMIMEHeaderKey
// would otherwise rebuild this string from scratch (1 allocation) on EVERY
// call to http.Header.Get/Set with the literal "X-Request-ID". Using this
// constant for DIRECT map access (r.Header[requestIDHeaderKey] / w.Header()
// [requestIDHeaderKey] = ...) reproduces Get/Set's exact semantics (first
// value on read; single-value replace on write) without paying that
// canonicalisation cost on every request. Real incoming requests already
// store every header under its canonical key (net/http canonicalises while
// parsing), and http.Header.Set does the same when a test constructs a
// request by hand, so direct access under the precomputed canonical key is
// always equivalent to Get/Set for any header actually reachable through
// the standard API.
const requestIDHeaderKey = "X-Request-Id"

// requestIDCtx fuses the context node that carries the request ID with its
// storage into a SINGLE heap allocation, replacing the previous
// context.WithValue(...) (1 alloc for the *context.valueCtx node) plus the
// separate heap allocation Go performs when boxing a string into an `any`
// (a string header, unlike a pointer, does not fit in an interface's single
// data word, so converting a string to `any` allocates).
//
// Embedding context.Context (anonymously) promotes Deadline/Done/Err
// straight through to the parent, satisfying context.Context without extra
// code; only Value is overridden, to intercept requestIDKey{} and fall
// through to the parent for every other key — including when some other
// middleware wraps THIS context further before a handler calls
// GetRequestID: the lookup still walks up through any number of standard
// context.WithValue layers until it reaches this node.
//
// Value(requestIDKey{}) returns the *requestIDCtx pointer itself (not the
// id string) so that leg of the boxing is also free: converting a pointer
// to `any` never allocates, because the pointer already fits in the
// interface's data word and the pointee is already heap-allocated.
// GetRequestID recovers it with a single type assertion.
type requestIDCtx struct {
	context.Context
	buf [32]byte // backing storage for a GENERATED id's hex digits; see Value below
	id  string
	// hdr is the backing array for the response header's []string value.
	// Slicing hdr[:] (RequestID does this below) produces a slice HEADER —
	// (pointer, len, cap), 24 bytes copied by value into the http.Header
	// map — with no separate heap allocation for a backing array, because
	// that array is hdr, already part of THIS SAME allocation. This
	// replaces what would otherwise be a fourth allocation
	// ([]string{c.id}, a distinct backing array) with zero extra cost.
	hdr [1]string
}

func (c *requestIDCtx) Value(key any) any {
	if _, ok := key.(requestIDKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// validRequestID returns true if id is safe to propagate as a request ID.
// Allows ASCII alphanumeric characters plus hyphen, underscore, and dot,
// with a maximum length of 128 characters.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
			(c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// requestIDBufSize is the size of each pooled buffer of pre-generated
// random bytes, refilled from a single crypto/rand.Read call. 4 KiB / 16
// bytes per ID = 256 IDs served per refill.
//
// CH-05: Go's CSPRNG synchronises its internal state through a shared lock
// (the FIPS-140 ChaCha8 DRBG backing crypto/rand since recent Go versions),
// so concurrent rand.Read(16 bytes) calls — one per request — contend on
// that lock under high concurrency, unlike a per-goroutine PRNG. Batching
// reads into much larger buffers amortises that lock over 256 IDs instead
// of paying for it on every single request. The spec (middleware-stdlib.md
// §4.16) mandates crypto/rand as the source of the random bytes themselves
// — only the granularity of the underlying Read calls changes here.
const requestIDBufSize = 4096

// requestIDBuf is one pooled buffer of pre-generated random bytes. off is
// the offset of the next unconsumed byte. Every byte is served to exactly
// one request ID and is never reused: off is advanced BEFORE the buffer is
// returned to the pool, and the buffer is refilled from crypto/rand once
// fully consumed.
type requestIDBuf struct {
	b   [requestIDBufSize]byte
	off int
}

// newExhaustedRequestIDBuf returns a buffer with off == len(b) — i.e.
// already "fully consumed" — so that nextRandomID's refill check triggers
// on the VERY FIRST use. A freshly zero-valued requestIDBuf would otherwise
// have off == 0, which is indistinguishable from "just refilled": without
// this, the first draw from every newly allocated buffer would silently
// serve 16 zero bytes (uninitialised memory) as if they were random.
func newExhaustedRequestIDBuf() *requestIDBuf {
	return &requestIDBuf{off: requestIDBufSize}
}

var requestIDBufPool = sync.Pool{New: func() any { return newExhaustedRequestIDBuf() }}

// nextRandomID draws the next 16 random bytes from a pooled, batch-refilled
// buffer.
//
// SECURITY (sprint-18 review, request_id_security_test.go): crypto/rand.Read
// is documented, as of Go 1.24 (see https://go.dev/issue/66821), to NEVER
// return an error to its caller — on an unrecoverable entropy-source
// failure it crashes the whole process (runtime fatal, not a recoverable
// panic, bypassing defer/recover) BEFORE Read itself ever returns. There is
// therefore no reachable "rand.Read failed" branch for this function, or
// anything else running in the same process, to handle: an in-process
// fallback (e.g. to math/rand/v2) can never execute, because the crash
// happens upstream of any error check this function could perform. An
// earlier version of this function attempted exactly such a fallback; it
// was unreachable dead code masking a comment that inaccurately described
// runtime behaviour, confirmed empirically by
// TestSec_NextRandomID_CryptoRandDefaultFailureModeCrashesProcess_NotFallback
// (which observes the actual crash), and has been removed. If a future Go
// release ever loosens crypto/rand.Read's never-returns-an-error contract,
// this simplification would need revisiting.
func nextRandomID() [16]byte {
	buf, _ := requestIDBufPool.Get().(*requestIDBuf)
	if buf == nil {
		buf = newExhaustedRequestIDBuf()
	}
	if buf.off+16 > len(buf.b) {
		_, _ = rand.Read(buf.b[:]) // never returns a non-nil error — see doc comment above
		buf.off = 0
	}
	var id [16]byte
	copy(id[:], buf.b[buf.off:buf.off+16])
	buf.off += 16
	requestIDBufPool.Put(buf)
	return id
}

// RequestID generates or propagates a request ID via X-Request-ID header.
// Incoming X-Request-ID values are validated; invalid or oversized values
// are replaced with a freshly generated random ID (MM-2026-0011).
//
// Allocation budget: exactly 2 allocations per request, on either path
// (generated or propagated) — (1) the fused *requestIDCtx node, which also
// carries the hex-encoded id storage (buf) and the response header's
// []string backing array (hdr), and (2) r.WithContext's copy of
// *http.Request, required for a stdlib-compatible middleware so the id is
// reachable via r.Context() inside next.ServeHTTP. Down from 7 before
// rmp #245 (context.WithValue's *context.valueCtx node; boxing the id
// string into an `any`; hex.EncodeToString's separate string;
// canonicalising "X-Request-ID" on both the inbound Get and the outbound
// Set; and the []string{id} header-value slice). See
// reports/perf-lab-2026-09-24/results/fixes/245.txt for the measured gain.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var inbound string
			if v := r.Header[requestIDHeaderKey]; len(v) > 0 {
				inbound = v[0]
			}

			c := &requestIDCtx{Context: r.Context()}
			if validRequestID(inbound) {
				// Propagated: `inbound` already exists as a string (owned
				// by r.Header's backing array) — assigning it just copies
				// the string header (ptr+len), no new allocation.
				c.id = inbound
			} else {
				raw := nextRandomID()
				hex.Encode(c.buf[:], raw[:])
				// SAFETY (unsafe.String): c.buf is a [32]byte FIELD of c —
				// i.e. the same single heap allocation as the *requestIDCtx
				// itself, not a separately-escaping local array. It is
				// written exactly once, right here, before c is ever
				// exposed to another goroutine (r.WithContext(c) below is
				// the first point c becomes reachable from the request
				// that flows to next.ServeHTTP), and nothing mutates c.buf
				// afterwards — so aliasing it as a string is data-race-free
				// and the string's data pointer keeps c (the whole
				// allocation, buf included) alive via Go's interior-pointer
				// GC semantics for exactly as long as the string is
				// reachable. This avoids the copy that `string(c.buf[:])`
				// (a safe conversion, but one that always allocates a new
				// backing array) would otherwise perform — the last
				// allocation on the generated-id path. Measured gain: see
				// reports/perf-lab-2026-09-24/results/fixes/245.txt.
				c.id = unsafe.String(&c.buf[0], len(c.buf))
			}

			c.hdr[0] = c.id
			w.Header()[requestIDHeaderKey] = c.hdr[:]
			next.ServeHTTP(w, r.WithContext(c))
		})
	}
}

// GetRequestID returns the request ID stored in ctx, or "" if absent.
func GetRequestID(ctx context.Context) string {
	if c, ok := ctx.Value(requestIDKey{}).(*requestIDCtx); ok {
		return c.id
	}
	return ""
}
