package middleware

import (
	"context"
	"crypto/sha256"
	"net/http"
)

type apiKeyCtxKey struct{}

// APIKeyOptions configures the APIKey middleware.
type APIKeyOptions struct {
	// Keys maps raw API key values to identity strings injected into the request context.
	// Panics if nil or empty.
	Keys map[string]string
	// Header is the request header name to read when ExtractFn is nil. Default: "X-API-Key".
	Header string
	// ExtractFn overrides key extraction. If nil, the Header field is used.
	ExtractFn func(*http.Request) string
}

// APIKey authenticates requests by matching an extracted key against a pre-hashed
// set of valid keys. All keys are SHA-256 hashed at construction time; per-request
// cost is one SHA-256 hash plus a [32]byte map lookup — no iteration, no string comparison.
//
// On success, the identity string associated with the key is injected into the request
// context and retrievable via GetAPIKeyIdentity.
// Panics if opts.Keys is nil or empty.
func APIKey(opts APIKeyOptions) func(http.Handler) http.Handler {
	if len(opts.Keys) == 0 {
		panic("middleware: APIKey requires at least one key in opts.Keys")
	}
	header := opts.Header
	if header == "" {
		header = "X-API-Key"
	}
	extract := opts.ExtractFn
	if extract == nil {
		extract = func(r *http.Request) string { return r.Header.Get(header) }
	}
	// Pre-hash all keys at construction time: zero per-request key-schedule overhead.
	hashed := make(map[[32]byte]string, len(opts.Keys))
	for k, id := range opts.Keys {
		hashed[sha256.Sum256([]byte(k))] = id
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extract(r)
			if raw == "" {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			id, ok := hashed[sha256.Sum256([]byte(raw))]
			if !ok {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey{}, id)))
		})
	}
}

// GetAPIKeyIdentity returns the identity string associated with the validated API key,
// as injected by the APIKey middleware.
func GetAPIKeyIdentity(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(apiKeyCtxKey{}).(string)
	return id, ok
}
