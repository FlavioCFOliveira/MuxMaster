package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Endpoint string
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
type oauth2Cache struct {
	mu      sync.RWMutex
	entries map[[32]byte]*oauth2Entry
	maxSize int
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
	resp   *IntrospectResponse
	expiry time.Time
}

func (c *oauth2Cache) get(key [32]byte) (*IntrospectResponse, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.resp, true
}

func (c *oauth2Cache) set(key [32]byte, resp *IntrospectResponse, expiry time.Time) {
	c.mu.Lock()
	if len(c.entries) >= c.maxSize {
		c.evictExpiredLocked()
		// DOS-2026-0005: when no expired entries are evictable, fall back to
		// evicting the entry with the soonest expiry so the cache cannot be
		// permanently filled with long-lived tokens. This is approximate LRU
		// (oldest-by-expiry) but bounded and stdlib-only — a true LRU would
		// require a doubly-linked list per access.
		if len(c.entries) >= c.maxSize {
			c.evictSoonestExpiryLocked()
		}
		// Defence in depth: if we still cannot make room (impossible with
		// the eviction above unless maxSize is 0), bail out rather than
		// growing unbounded.
		if len(c.entries) >= c.maxSize {
			c.mu.Unlock()
			return
		}
	}
	c.entries[key] = &oauth2Entry{resp: resp, expiry: expiry}
	c.mu.Unlock()
}

// evictSoonestExpiryLocked removes the entry with the earliest expiry time —
// the closest analogue to LRU we can compute without per-access timestamps.
// Caller must hold c.mu.Lock().
func (c *oauth2Cache) evictSoonestExpiryLocked() {
	var (
		victim     [32]byte
		earliest   time.Time
		hasVictim  bool
	)
	for k, e := range c.entries {
		if !hasVictim || e.expiry.Before(earliest) {
			victim = k
			earliest = e.expiry
			hasVictim = true
		}
	}
	if hasVictim {
		delete(c.entries, victim)
	}
}

func (c *oauth2Cache) evictExpiredLocked() {
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expiry) {
			delete(c.entries, k)
		}
	}
}

// OAuth2Introspect validates Bearer tokens via RFC 7662 token introspection.
// Active tokens are cached (keyed by sha256(token)) to avoid per-request network calls.
// On success, the IntrospectResponse is available via GetOAuth2Claims.
//
// Panics if opts.Endpoint is empty.
func OAuth2Introspect(opts OAuth2Options) func(http.Handler) http.Handler {
	if opts.Endpoint == "" {
		panic("middleware: OAuth2Introspect requires a non-empty opts.Endpoint")
	}
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
				return doIntrospect(r.Context(), token)
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
