package middleware

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type oauth2CtxKey struct{}

// IntrospectResponse holds the RFC 7662 token introspection response fields.
type IntrospectResponse struct {
	Active    bool
	Subject   string
	Scope     string
	ClientID  string
	Username  string
	TokenType string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	Issuer    string
	Audience  []string
}

// OAuth2Options configures the OAuth2Introspect middleware.
type OAuth2Options struct {
	// Endpoint is the RFC 7662 introspection URL. Required.
	// MUST use the https:// scheme — bearer tokens transmitted over plaintext
	// are exposed to passive observers and MITM attackers (RFC 7662 §4 / RFC
	// 6749 §1.6). Construction panics on a non-HTTPS endpoint unless
	// AllowInsecureEndpoint is explicitly set to true (testing/localhost only).
	Endpoint string
	// AllowInsecureEndpoint disables the HTTPS-only enforcement on Endpoint.
	// Set to true ONLY for testing or trusted-local-loopback deployments —
	// production traffic must always use HTTPS. When true, a one-time slog
	// warning is emitted at construction time. Default: false.
	AllowInsecureEndpoint bool
	// ClientID and ClientSecret authenticate to the introspection endpoint via HTTP Basic.
	ClientID     string
	ClientSecret string
	// CacheTTL is how long active tokens are cached. Default: 60s.
	// Set to -1 (or any negative value) to disable caching entirely — every
	// request hits the introspection endpoint, eliminating the cache-poisoning
	// blast radius (MSR-2026-0063) at the cost of higher IDP load. Use
	// disabled caching for high-security endpoints; the singleflight group
	// (DOS-OAUTH2-001 fix) still coalesces concurrent introspection calls
	// for the same token.
	// Cache respects the token's own exp: effective TTL = min(CacheTTL, token.exp - now).
	// Note: caching means revoked tokens remain valid until TTL expires.
	CacheTTL time.Duration
	// MaxCacheSize caps the number of cached active tokens. Default: 10000.
	MaxCacheSize int
	// HTTPClient is used for introspection requests. Default: 10s timeout.
	HTTPClient *http.Client
	// ExtractFn overrides token extraction. Default: "Authorization: Bearer <token>".
	ExtractFn func(*http.Request) string
}

// oauth2Cache is an RWMutex-protected map keyed by sha256(token) to avoid
// storing raw tokens in memory. Eviction is lazy: on cache-full writes.
//
// CH-09: eviction is backed by a min-heap ordered by expiry (heap), giving
// O(log n) amortised insert/evict instead of the previous O(n) full-map
// scan performed on every cache-full set() call — see evictOneLocked.
type oauth2Cache struct {
	mu      sync.RWMutex
	entries map[[32]byte]*oauth2Entry
	heap    oauth2ExpiryHeap
	maxSize int
}

// oauth2ExpiryHeap is a container/heap min-heap of *oauth2Entry ordered by
// expiry. Each entry tracks its own heap index (idx) so that heap.Fix can
// reposition it in O(log n) when its expiry changes (a token re-cached with
// a new expiry before the old entry was evicted), instead of requiring a
// linear scan to find it.
type oauth2ExpiryHeap []*oauth2Entry

func (h oauth2ExpiryHeap) Len() int { return len(h) }

func (h oauth2ExpiryHeap) Less(i, j int) bool { return h[i].expiry.Before(h[j].expiry) }

func (h oauth2ExpiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *oauth2ExpiryHeap) Push(x any) {
	e, _ := x.(*oauth2Entry)
	e.idx = len(*h)
	*h = append(*h, e)
}

func (h *oauth2ExpiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.idx = -1
	*h = old[:n-1]
	return e
}

// oauth2Inflight is an in-process singleflight group keyed by sha256(token).
// Concurrent requests for the same token coalesce into a single upstream
// introspection call, preventing the cache-stampede DoS amplification
// against the IDP (DOS-OAUTH2-001 / rmp #8). Stdlib-only — no dependency
// on golang.org/x/sync.
type oauth2Inflight struct {
	mu    sync.Mutex
	calls map[[32]byte]*oauth2InflightCall
}

type oauth2InflightCall struct {
	done chan struct{}
	resp *IntrospectResponse
	err  error
}

// do coalesces concurrent calls for key into a single fn() invocation. The
// leader runs fn(); followers wait on the leader's done channel or on ctx
// cancellation, whichever fires first. The leader's result is shared with
// every follower that does not cancel.
func (g *oauth2Inflight) do(
	ctx context.Context,
	key [32]byte,
	fn func() (*IntrospectResponse, error),
) (*IntrospectResponse, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-c.done:
			return c.resp, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := &oauth2InflightCall{done: make(chan struct{})}
	if g.calls == nil {
		g.calls = make(map[[32]byte]*oauth2InflightCall)
	}
	g.calls[key] = c
	g.mu.Unlock()

	c.resp, c.err = fn()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	close(c.done)
	return c.resp, c.err
}

type oauth2Entry struct {
	key    [32]byte
	resp   *IntrospectResponse
	expiry time.Time
	idx    int // position in the owning oauth2Cache.heap; maintained by heap.Fix/Push/Pop
}

func (c *oauth2Cache) get(key [32]byte) (*IntrospectResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// e.resp/e.expiry MUST be read while still holding the read lock: set()
	// now updates an EXISTING entry in place on a re-cache (heap.Fix reuses
	// the object instead of allocating a new one — see set() below), so an
	// entry is no longer immutable-after-construction the way it was
	// before this rewrite. Reading these fields after releasing the lock
	// (the previous code's shape) would race with that in-place write.
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.resp, true
}

// set inserts or refreshes the cache entry for key. When the cache is full,
// exactly one entry — evictOneLocked's choice — is evicted first to make
// room, keeping the write lock held for O(log n) instead of the O(n) full
// map scan the previous implementation performed on every cache-full write
// (CH-09).
func (c *oauth2Cache) set(key [32]byte, resp *IntrospectResponse, expiry time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok {
		// Re-caching an already-present token (e.g. two concurrent
		// singleflight leaders raced — see MSR-2026-0071): update in place
		// and reposition the heap entry in O(log n) via heap.Fix, instead
		// of removing and reinserting.
		e.resp = resp
		e.expiry = expiry
		heap.Fix(&c.heap, e.idx)
		return
	}

	if len(c.entries) >= c.maxSize {
		if !c.evictOneLocked() {
			// Defence in depth: nothing to evict (only possible when
			// maxSize <= 0) — bail out rather than growing unbounded.
			return
		}
	}

	e := &oauth2Entry{key: key, resp: resp, expiry: expiry}
	heap.Push(&c.heap, e)
	c.entries[key] = e
}

// evictOneLocked removes exactly one entry — the heap root, i.e. the entry
// with the globally earliest expiry — and reports whether an entry was
// evicted. Caller must hold c.mu.Lock().
//
// Because the heap root is always the minimum-expiry entry across the
// WHOLE cache, this single O(log n) pop already implements both preserved
// eviction properties from the previous O(n) two-phase scan:
//   - if ANY entry is expired, the minimum-expiry entry is a fortiori also
//     expired (expiry <= now implies every larger expiry could still be >
//     now, but the minimum cannot be later than an already-expired entry),
//     so the root IS an expired entry — "expired entries evicted first";
//   - if NO entry is expired yet, the root is simply the soonest-to-expire
//     live entry — the DOS-2026-0005 fallback that keeps the cache from
//     being permanently filled with long-lived tokens.
func (c *oauth2Cache) evictOneLocked() bool {
	if c.heap.Len() == 0 {
		return false
	}
	victim, _ := heap.Pop(&c.heap).(*oauth2Entry)
	delete(c.entries, victim.key)
	return true
}

// oauth2UserinfoPattern matches an embedded userinfo component ("user:pass@"
// or "user@") immediately following a scheme separator ("://"), e.g. in
// "https://user:secret@host/path". It is the fallback redaction path used
// by redactedEndpointRaw when the Endpoint string cannot be parsed into a
// structured *url.URL — see redactedEndpointRaw.
var oauth2UserinfoPattern = regexp.MustCompile(`://[^/?#@]*@`)

// redactedEndpointURL returns u's string form with any userinfo component
// stripped ENTIRELY (not merely password-masked) — CWE-532: construction-time
// diagnostics (panics, errors) must never render credentials embedded in the
// Endpoint URL. This mirrors the TM-2026-005 slog redaction policy (host +
// scheme only, no userinfo) so every surface — logs and panics alike —
// applies the same redaction rule.
func redactedEndpointURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	return c.String()
}

// redactedEndpointRaw redacts an embedded userinfo component from a raw,
// NOT-YET-PARSED (or unparseable) Endpoint string. It exists because the
// "malformed Endpoint URL" panic path is reached precisely when url.Parse
// has already failed on opts.Endpoint, so there is no *url.URL to hand to
// redactedEndpointURL. Falls back to a regex-based redaction of the
// "user:pass@" / "user@" substring following the scheme separator.
func redactedEndpointRaw(raw string) string {
	return oauth2UserinfoPattern.ReplaceAllString(raw, "://")
}

// OAuth2Introspect validates Bearer tokens via RFC 7662 token introspection.
// Active tokens are cached (keyed by sha256(token)) to avoid per-request network calls.
// On success, the IntrospectResponse is available via GetOAuth2Claims.
//
// Panics if opts.Endpoint is empty, malformed, or non-HTTPS (unless
// opts.AllowInsecureEndpoint is true). Bearer tokens transmitted over plaintext
// are exposed to passive observers (MSR-2026-0067 / RFC 7662 §4).
func OAuth2Introspect(opts OAuth2Options) func(http.Handler) http.Handler {
	if opts.Endpoint == "" {
		panic("middleware: OAuth2Introspect requires a non-empty opts.Endpoint")
	}
	parsedEndpoint, err := url.Parse(opts.Endpoint)
	if err != nil {
		// CWE-532: url.Error.Error() echoes the raw, unparsed URL verbatim
		// (`parse "<url>": <reason>`) — if opts.Endpoint carries embedded
		// userinfo (e.g. "https://user:secret@host/x"), naively including
		// err.Error() in this panic would render the credentials in full.
		// Extract only the underlying reason (never the raw URL) and pair
		// it with a separately redacted copy of opts.Endpoint.
		reason := err.Error()
		var uerr *url.Error
		if errors.As(err, &uerr) && uerr.Err != nil {
			reason = uerr.Err.Error()
		}
		panic("middleware: OAuth2Introspect: malformed Endpoint URL (" + redactedEndpointRaw(opts.Endpoint) + "): " + reason)
	}
	// TM-2026-004: tighten Endpoint validation. url.Parse is permissive —
	// "https://attacker@evil/x" parses with Scheme=https, Host=evil, User=attacker.
	// We must:
	//   (1) require non-empty Host (catches "https:///foo" and "https:?q=x"),
	//   (2) reject embedded userinfo (catches "https://a@b/x" exfil tricks),
	//   (3) only THEN check the scheme,
	// otherwise a misconfigured Endpoint silently routes bearer tokens to an
	// attacker-controlled host that happens to use https.
	if parsedEndpoint.Host == "" {
		// CWE-532: render the REDACTED form, not opts.Endpoint — a URL with
		// no discernible host can still carry userinfo (e.g. "https://user:
		// secret@" parses with Host=="" and User set), and printing the raw
		// string here would leak those credentials in the panic message.
		panic("middleware: OAuth2Introspect: Endpoint URL has no host: " + redactedEndpointURL(parsedEndpoint))
	}
	if parsedEndpoint.User != nil {
		// CWE-532 (originally flagged: construction-time panic rendered the
		// full URL, including userinfo credentials, in the panic message).
		// Redact before printing — this is exactly the case being rejected,
		// so the raw opts.Endpoint is guaranteed to contain credentials here.
		panic("middleware: OAuth2Introspect: Endpoint URL must not contain userinfo (RFC 3986 §3.2.1) — credentials in URL are an exfiltration vector: " + redactedEndpointURL(parsedEndpoint))
	}
	if parsedEndpoint.Scheme != "https" {
		if !opts.AllowInsecureEndpoint {
			panic("middleware: OAuth2Introspect: Endpoint must use https:// — bearer tokens over plaintext leak to passive observers (RFC 7662 §4). Set OAuth2Options.AllowInsecureEndpoint=true ONLY for testing.")
		}
		// TM-2026-005: log only the resolved host (no userinfo, no path, no
		// query) so operators can see where their tokens are being sent
		// without leaking credentials embedded in the URL via slog sinks.
		slog.Warn("OAuth2Introspect: insecure plaintext endpoint accepted via AllowInsecureEndpoint — bearer tokens transmitted in clear",
			"host", parsedEndpoint.Host, "scheme", parsedEndpoint.Scheme)
	}
	// TM-2026-005: same redaction policy for the construction-time info log.
	slog.Info("OAuth2Introspect: configured", "host", parsedEndpoint.Host, "scheme", parsedEndpoint.Scheme)
	cacheTTL := opts.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = 60 * time.Second
	}
	maxCacheSize := opts.MaxCacheSize
	if maxCacheSize <= 0 {
		maxCacheSize = 10000
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	extract := opts.ExtractFn
	if extract == nil {
		extract = extractBearerToken
	}

	var cache *oauth2Cache
	if cacheTTL > 0 {
		cache = &oauth2Cache{
			entries: make(map[[32]byte]*oauth2Entry, 64),
			maxSize: maxCacheSize,
		}
	}

	// Singleflight coalesces concurrent introspection calls for the same
	// token into a single upstream request — defends against cache-stampede
	// DoS amplification against the IDP (DOS-OAUTH2-001).
	inflight := &oauth2Inflight{}

	doIntrospect := func(ctx context.Context, token string) (*IntrospectResponse, error) {
		body := url.Values{
			"token":           {token},
			"token_type_hint": {"access_token"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Endpoint,
			strings.NewReader(body.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if opts.ClientID != "" {
			req.SetBasicAuth(opts.ClientID, opts.ClientSecret)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("introspection endpoint returned %d", resp.StatusCode)
		}
		var raw struct {
			Active    bool        `json:"active"`
			Sub       string      `json:"sub"`
			Scope     string      `json:"scope"`
			ClientID  string      `json:"client_id"`
			Username  string      `json:"username"`
			TokenType string      `json:"token_type"`
			Exp       int64       `json:"exp"`
			Iat       int64       `json:"iat"`
			Nbf       int64       `json:"nbf"`
			Iss       string      `json:"iss"`
			Aud       jwtAudClaim `json:"aud"`
		}
		// Limit to 64 KB to prevent memory exhaustion from oversized responses.
		if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&raw); err != nil {
			return nil, err
		}
		ir := &IntrospectResponse{
			Active:    raw.Active,
			Subject:   raw.Sub,
			Scope:     raw.Scope,
			ClientID:  raw.ClientID,
			Username:  raw.Username,
			TokenType: raw.TokenType,
			Issuer:    raw.Iss,
			Audience:  []string(raw.Aud),
		}
		if raw.Exp != 0 {
			ir.ExpiresAt = time.Unix(raw.Exp, 0)
		}
		if raw.Iat != 0 {
			ir.IssuedAt = time.Unix(raw.Iat, 0)
		}
		if raw.Nbf != 0 {
			ir.NotBefore = time.Unix(raw.Nbf, 0)
		}
		return ir, nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extract(r)
			if token == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}

			tokenKey := sha256.Sum256([]byte(token))

			if cache != nil {
				if resp, ok := cache.get(tokenKey); ok {
					if !resp.Active {
						w.Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token"`)
						http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
						return
					}
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), oauth2CtxKey{}, resp)))
					return
				}
			}

			resp, err := inflight.do(r.Context(), tokenKey, func() (*IntrospectResponse, error) {
				// Re-check the cache under the singleflight leader: another
				// concurrent leader for the same token may have populated
				// the cache between our miss above and acquiring leadership.
				if cache != nil {
					if cached, ok := cache.get(tokenKey); ok {
						return cached, nil
					}
				}
				// MSR-2026-0071: detach the introspection call from the
				// leader's request CANCELLATION. If the leader cancels
				// (client disconnect) while followers are still waiting,
				// completing the call lets every follower receive the
				// legitimate result instead of being poisoned with a 401
				// derived from context.Canceled. A previous fix used
				// context.Background() to achieve this, but that also
				// discarded every request-scoped VALUE (trace/correlation
				// IDs, etc.), so the outbound IdP request could never
				// observe them.
				// context.WithoutCancel(r.Context()) keeps ctx.Value()
				// working (Value() still delegates to r.Context()) while
				// guaranteeing the returned context's Done() never fires
				// because of r's own cancellation — exactly the property
				// this coalesced call needs. A fresh, independent 30s
				// timeout is layered on top so the call still cannot run
				// forever if the IdP hangs (the HTTP client's own
				// opts.HTTPClient.Timeout is an additional bound).
				detached, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
				defer cancel()
				return doIntrospect(detached, token)
			})
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			if !resp.Active {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}

			if cache != nil {
				expiry := time.Now().Add(cacheTTL)
				if !resp.ExpiresAt.IsZero() && resp.ExpiresAt.Before(expiry) {
					expiry = resp.ExpiresAt
				}
				cache.set(tokenKey, resp, expiry)
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), oauth2CtxKey{}, resp)))
		})
	}
}

// GetOAuth2Claims returns the IntrospectResponse injected by the OAuth2Introspect middleware.
func GetOAuth2Claims(ctx context.Context) (*IntrospectResponse, bool) {
	c, ok := ctx.Value(oauth2CtxKey{}).(*IntrospectResponse)
	return c, ok
}
