package bench

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ── Candidate: APIKey injects the identity with
// context.WithValue(ctx, apiKeyCtxKey{}, id) — one *valueCtx allocation plus
// a second allocation to box the id string into `any`. RequestID already
// fuses node+payload into ONE allocation (requestIDCtx); apiKeyAlt applies
// the same technique. The TSC-2026-0008 Set+Del header equalisation on the
// hit path is security-mandated and is kept verbatim.

type apiKeyCtxAlt struct {
	context.Context
	id string
}

type apiKeyAltKey struct{}

func (c *apiKeyCtxAlt) Value(key any) any {
	if _, ok := key.(apiKeyAltKey); ok {
		return c
	}
	return c.Context.Value(key)
}

func apiKeyAlt(keys map[string]string) func(http.Handler) http.Handler {
	hashed := make(map[[32]byte]string, len(keys))
	for k, id := range keys {
		hashed[sha256.Sum256([]byte(k))] = id
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-API-Key")
			if raw == "" {
				w.Header().Set("WWW-Authenticate", `ApiKey realm="api"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			id, ok := hashed[sha256.Sum256([]byte(raw))]
			if !ok {
				w.Header().Set("WWW-Authenticate", `ApiKey realm="api", error="invalid_key"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			w.Header().Set("WWW-Authenticate", `ApiKey realm="api"`)
			w.Header().Del("WWW-Authenticate")
			next.ServeHTTP(w, r.WithContext(&apiKeyCtxAlt{Context: r.Context(), id: id}))
		})
	}
}

func BenchmarkAPIKeyHit(b *testing.B) {
	keys := map[string]string{"key-alice": "alice", "key-bob": "bob"}
	run := func(b *testing.B, h http.Handler) {
		r := realisticRequest(http.MethodGet, "/api/profile")
		r.Header.Set("X-API-Key", "key-alice")
		w := newDiscardRW()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			h.ServeHTTP(w, r)
			w.reset()
		}
	}
	b.Run("current", func(b *testing.B) { run(b, mw.APIKey(mw.APIKeyOptions{Keys: keys})(nop)) })
	b.Run("alternative", func(b *testing.B) { run(b, apiKeyAlt(keys)(nop)) })
}

// ── JWTAuth: whole-middleware cost on the jwt example's token shape, and the
// isolated sub-costs that the profile attributes to it.

func hs256Token(secret []byte) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pl, _ := json.Marshal(map[string]any{"sub": "u1", "name": "alice", "iat": time.Now().Unix(), "exp": time.Now().Add(24 * time.Hour).Unix()})
	p := base64.RawURLEncoding.EncodeToString(pl)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(hdr + "." + p))
	return hdr + "." + p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func BenchmarkJWTAuthHS256(b *testing.B) {
	secret := []byte("dev-secret-change-in-production")
	h := mw.JWTAuth(mw.JWTOptions{Secret: secret, Algorithms: []string{"HS256"}, RequireExpiry: true})(nop)
	r := realisticRequest(http.MethodGet, "/api/me")
	r.Header.Set("Authorization", "Bearer "+hs256Token(secret))
	w := newDiscardRW()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
		w.reset()
	}
}

var sinkBytes []byte

func BenchmarkJWTParts(b *testing.B) {
	secret := []byte("dev-secret-change-in-production")
	tok := hs256Token(secret)
	signingInput := tok[:len(tok)-44]
	mac := hmac.New(sha256.New, secret)
	b.Run("[]byte(signingInput)-escaping", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			sinkBytes = []byte(signingInput)
		}
	})
	b.Run("hmac.Sum(nil)", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			mac.Reset()
			mac.Write([]byte(signingInput))
			sinkBytes = mac.Sum(nil)
		}
	})
	b.Run("hmac.Sum(stack)", func(b *testing.B) {
		b.ReportAllocs()
		var out [sha256.Size]byte
		for range b.N {
			mac.Reset()
			mac.Write([]byte(signingInput))
			_ = mac.Sum(out[:0])
		}
	})
}

// ── Candidate: JWTAuth base64-decodes and json.Unmarshals the JOSE header on
// every request, although every token minted by one issuer carries the
// byte-identical header segment (e.g. eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9).
// A one-entry memo keyed by the exact header segment string (stored only
// after the header passed the alg allow-list and the crit check) returns the
// same decision for the same input bytes; any other header takes the full
// path. This measures only the header step.

type rawJWTHeaderReplica struct {
	Alg  string   `json:"alg"`
	Crit []string `json:"crit"`
}

type jwtHdrMemo struct {
	b64 string
	alg string
}

var sinkAlg string

func BenchmarkJWTHeaderStep(b *testing.B) {
	tok := hs256Token([]byte("dev-secret-change-in-production"))
	headerB64, _, _ := strings.Cut(tok, ".")
	allowed := map[string]struct{}{"HS256": {}}
	full := func(h string) (string, bool) {
		hb, err := base64.RawURLEncoding.DecodeString(h)
		if err != nil {
			return "", false
		}
		var hdr rawJWTHeaderReplica
		if err := json.Unmarshal(hb, &hdr); err != nil {
			return "", false
		}
		if _, ok := allowed[hdr.Alg]; !ok || len(hdr.Crit) > 0 {
			return "", false
		}
		return hdr.Alg, true
	}
	b.Run("current(decode+unmarshal)", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			sinkAlg, _ = full(headerB64)
		}
	})
	b.Run("alternative(memo-hit)", func(b *testing.B) {
		var memo atomic.Pointer[jwtHdrMemo]
		b.ReportAllocs()
		for range b.N {
			if m := memo.Load(); m != nil && m.b64 == headerB64 {
				sinkAlg = m.alg
				continue
			}
			if alg, ok := full(headerB64); ok {
				memo.Store(&jwtHdrMemo{b64: strings.Clone(headerB64), alg: alg})
				sinkAlg = alg
			}
		}
	})
}
