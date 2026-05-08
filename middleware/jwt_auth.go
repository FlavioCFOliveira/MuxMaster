package middleware

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

type jwtCtxKey struct{}

// JWTClaims holds the standard JWT claims extracted from a validated token.
// Custom claims can be unmarshalled from RawPayload.
type JWTClaims struct {
	Subject   string
	Issuer    string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	// RawPayload is the decoded JSON payload bytes, available for extracting custom claims.
	RawPayload []byte
}

// JWTOptions configures the JWTAuth middleware.
//
// SECURITY (TSC-2026-0003): mixing algorithm families (HS* with RS* or ES*)
// in Algorithms leaks the algorithm path via response latency. HMAC verifies
// in ~1 µs, RSA-2048 verifies in ~300 µs, and an attacker submitting tokens
// with different alg labels can determine which path the server runs from
// the response time alone — narrowing the attack surface for
// algorithm-confusion attacks (RFC 8725 §3.1). Configure each endpoint
// with a single algorithm family. JWTAuth emits a slog.Warn at construction
// time when a mixed-family Algorithms list is detected.
type JWTOptions struct {
	// Secret is the HMAC signing key, required for HS256, HS384, HS512.
	Secret []byte
	// PublicKey is the RSA or ECDSA public key, required for RS*/ES* algorithms.
	PublicKey crypto.PublicKey
	// Algorithms lists accepted signing algorithms. Must be non-empty.
	// Supported: HS256, HS384, HS512, RS256, RS384, RS512, ES256, ES384, ES512.
	// SEE the SECURITY note on JWTOptions about mixing families.
	Algorithms []string
	// Issuers, if non-empty, restricts accepted "iss" claim values.
	Issuers []string
	// Audiences, if non-empty, requires at least one "aud" entry to match.
	Audiences []string
	// ClockSkew is the permitted clock drift applied to exp and nbf checks. Default: 0.
	ClockSkew time.Duration
	// RequireExpiry, when true, rejects any token whose payload has no "exp"
	// claim. RFC 8725 §4.4 recommends rejecting tokens without expiry unless
	// there is a compelling reason: a stolen token without "exp" is valid
	// indefinitely. Default: false (backward compatible).
	RequireExpiry bool
}

// jwtHMACPool reuses HMAC hash objects across requests to avoid per-request
// key-schedule recomputation — the dominant cost for HMAC algorithms.
type jwtHMACPool struct {
	p sync.Pool
}

func newJWTHMACPool(hf func() hash.Hash, key []byte) *jwtHMACPool {
	return &jwtHMACPool{p: sync.Pool{New: func() any { return hmac.New(hf, key) }}}
}

func (p *jwtHMACPool) verify(msg, sig []byte) bool {
	h := p.p.Get().(hash.Hash)
	h.Reset()
	h.Write(msg)
	mac := h.Sum(nil)
	p.p.Put(h)
	return hmac.Equal(mac, sig)
}

// JWTAuth validates JWT Bearer tokens from the Authorization header.
// Signature is always verified before claims are parsed to prevent payload manipulation.
// On success, claims are injected into the request context via GetJWTClaims.
//
// Panics if Algorithms is empty or if the required key material is missing for any
// listed algorithm.
func JWTAuth(opts JWTOptions) func(http.Handler) http.Handler {
	if len(opts.Algorithms) == 0 {
		panic("middleware: JWTAuth requires at least one algorithm in opts.Algorithms")
	}

	allowedAlgs := make(map[string]struct{}, len(opts.Algorithms))
	var hasHMAC, hasRSA, hasECDSA bool
	for _, alg := range opts.Algorithms {
		allowedAlgs[alg] = struct{}{}
		switch {
		case strings.HasPrefix(alg, "HS"):
			hasHMAC = true
		case strings.HasPrefix(alg, "RS"):
			hasRSA = true
		case strings.HasPrefix(alg, "ES"):
			hasECDSA = true
		}
	}
	families := 0
	for _, b := range []bool{hasHMAC, hasRSA, hasECDSA} {
		if b {
			families++
		}
	}
	if families > 1 {
		slog.Default().Warn("JWTAuth: Algorithms mixes algorithm families — "+
			"timing oracle (TSC-2026-0003) leaks alg path via response latency; "+
			"configure one family per endpoint.",
			slog.Any("algorithms", opts.Algorithms))
	}
	// TM-2026-001: warn when RequireExpiry is left at the unsafe default.
	// RFC 8725 §4.4 recommends rejecting tokens without "exp"; otherwise a
	// stolen token without expiry is valid forever (replay).
	if !opts.RequireExpiry {
		slog.Default().Warn("JWTAuth: RequireExpiry=false — tokens without an \"exp\" claim are accepted. " +
			"RFC 8725 §4.4 recommends RequireExpiry=true so that tokens cannot be replayed indefinitely.")
	}
	issuers := make(map[string]struct{}, len(opts.Issuers))
	for _, iss := range opts.Issuers {
		issuers[iss] = struct{}{}
	}
	audiences := make(map[string]struct{}, len(opts.Audiences))
	for _, aud := range opts.Audiences {
		audiences[aud] = struct{}{}
	}

	// Build HMAC pools and validate key material at construction time.
	hmacPools := make(map[string]*jwtHMACPool)
	for _, alg := range opts.Algorithms {
		switch alg {
		case "HS256":
			if opts.Secret == nil {
				panic("middleware: JWTAuth HS256 requires opts.Secret")
			}
			hmacPools["HS256"] = newJWTHMACPool(sha256.New, opts.Secret)
		case "HS384":
			if opts.Secret == nil {
				panic("middleware: JWTAuth HS384 requires opts.Secret")
			}
			hmacPools["HS384"] = newJWTHMACPool(sha512.New384, opts.Secret)
		case "HS512":
			if opts.Secret == nil {
				panic("middleware: JWTAuth HS512 requires opts.Secret")
			}
			hmacPools["HS512"] = newJWTHMACPool(sha512.New, opts.Secret)
		case "RS256", "RS384", "RS512":
			if _, ok := opts.PublicKey.(*rsa.PublicKey); !ok {
				panic("middleware: JWTAuth " + alg + " requires opts.PublicKey to be *rsa.PublicKey")
			}
		case "ES256":
			pub, ok := opts.PublicKey.(*ecdsa.PublicKey)
			if !ok {
				panic("middleware: JWTAuth ES256 requires opts.PublicKey to be *ecdsa.PublicKey")
			}
			if pub.Curve != elliptic.P256() {
				panic("middleware: JWTAuth ES256 requires a P-256 public key (RFC 7518 §3.4)")
			}
		case "ES384":
			pub, ok := opts.PublicKey.(*ecdsa.PublicKey)
			if !ok {
				panic("middleware: JWTAuth ES384 requires opts.PublicKey to be *ecdsa.PublicKey")
			}
			if pub.Curve != elliptic.P384() {
				panic("middleware: JWTAuth ES384 requires a P-384 public key (RFC 7518 §3.4)")
			}
		case "ES512":
			pub, ok := opts.PublicKey.(*ecdsa.PublicKey)
			if !ok {
				panic("middleware: JWTAuth ES512 requires opts.PublicKey to be *ecdsa.PublicKey")
			}
			if pub.Curve != elliptic.P521() {
				panic("middleware: JWTAuth ES512 requires a P-521 public key (RFC 7518 §3.4)")
			}
		default:
			panic("middleware: JWTAuth unsupported algorithm: " + alg)
		}
	}

	// verifyFn dispatches to the appropriate signing algorithm.
	// signingInput is the raw "header.payload" string bytes (no alloc: substring of the token).
	verifyFn := func(alg string, signingInput, sig []byte) bool {
		if p, ok := hmacPools[alg]; ok {
			return p.verify(signingInput, sig)
		}
		switch alg {
		case "RS256":
			h := sha256.Sum256(signingInput)
			return rsa.VerifyPKCS1v15(opts.PublicKey.(*rsa.PublicKey), crypto.SHA256, h[:], sig) == nil
		case "RS384":
			h := sha512.Sum384(signingInput)
			return rsa.VerifyPKCS1v15(opts.PublicKey.(*rsa.PublicKey), crypto.SHA384, h[:], sig) == nil
		case "RS512":
			h := sha512.Sum512(signingInput)
			return rsa.VerifyPKCS1v15(opts.PublicKey.(*rsa.PublicKey), crypto.SHA512, h[:], sig) == nil
		case "ES256":
			h := sha256.Sum256(signingInput)
			return verifyECDSAJWT(opts.PublicKey.(*ecdsa.PublicKey), h[:], sig, 32)
		case "ES384":
			h := sha512.Sum384(signingInput)
			return verifyECDSAJWT(opts.PublicKey.(*ecdsa.PublicKey), h[:], sig, 48)
		case "ES512":
			h := sha512.Sum512(signingInput)
			return verifyECDSAJWT(opts.PublicKey.(*ecdsa.PublicKey), h[:], sig, 66)
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			claims, err := parseAndValidateJWT(token, allowedAlgs, issuers, audiences, opts.ClockSkew, opts.RequireExpiry, verifyFn)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), jwtCtxKey{}, claims)))
		})
	}
}

// GetJWTClaims returns the JWT claims injected by the JWTAuth middleware.
func GetJWTClaims(ctx context.Context) (*JWTClaims, bool) {
	c, ok := ctx.Value(jwtCtxKey{}).(*JWTClaims)
	return c, ok
}

// ── internal helpers ──────────────────────────────────────────────────────────

var (
	errJWTInvalid     = errors.New("invalid jwt")
	errJWTExpired     = errors.New("jwt expired")
	errJWTNotYetValid = errors.New("jwt not yet valid")
)

type rawJWTHeader struct {
	Alg string `json:"alg"`
	// Crit lists critical extensions the recipient must understand (RFC 7515 §4.1.11).
	Crit []string `json:"crit"`
}

// jwtAudClaim handles JWT "aud" which may be a single string or an array.
type jwtAudClaim []string

func (a *jwtAudClaim) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*a = jwtAudClaim{s}
		return nil
	}
	return json.Unmarshal(data, (*[]string)(a))
}

type rawJWTPayload struct {
	Sub string      `json:"sub"`
	Iss string      `json:"iss"`
	Aud jwtAudClaim `json:"aud"`
	Exp int64       `json:"exp"`
	Nbf int64       `json:"nbf"`
	Iat int64       `json:"iat"`
}

func parseAndValidateJWT(
	token string,
	allowedAlgs, issuers, audiences map[string]struct{},
	clockSkew time.Duration,
	requireExpiry bool,
	verifyFn func(alg string, signingInput, sig []byte) bool,
) (*JWTClaims, error) {
	headerB64, rest, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errJWTInvalid
	}
	payloadB64, sigB64, ok := strings.Cut(rest, ".")
	if !ok {
		return nil, errJWTInvalid
	}

	// Decode and parse the header to extract alg.
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, errJWTInvalid
	}
	var hdr rawJWTHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, errJWTInvalid
	}
	if _, ok := allowedAlgs[hdr.Alg]; !ok {
		return nil, errJWTInvalid
	}
	// RFC 7515 §4.1.11: if "crit" is present, every listed extension must be understood.
	// We support none, so any "crit" entry mandates rejection.
	if len(hdr.Crit) > 0 {
		return nil, errJWTInvalid
	}

	// Decode signature.
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, errJWTInvalid
	}

	// Verify signature over "header.payload" — substring of token, no allocation.
	signingInput := token[:len(headerB64)+1+len(payloadB64)]
	if !verifyFn(hdr.Alg, []byte(signingInput), sigBytes) {
		return nil, errJWTInvalid
	}

	// Parse payload only after signature is verified.
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, errJWTInvalid
	}
	var raw rawJWTPayload
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, errJWTInvalid
	}

	// TM-2026-002: reject negative exp/nbf/iat. RFC 7519 §2 defines NumericDate
	// as a non-negative seconds-since-epoch integer; any negative value is
	// either a forged claim or a malformed client. We reject up-front so the
	// downstream comparisons cannot silently treat an ancient Unix epoch as
	// valid (e.g. exp=-1 → time.Unix(-1, 0) → 1969 → expired path is taken,
	// which is correct, but we tighten the contract for future-proofing).
	if raw.Exp < 0 || raw.Nbf < 0 || raw.Iat < 0 {
		return nil, errJWTInvalid
	}
	if requireExpiry && raw.Exp == 0 {
		return nil, errJWTInvalid
	}
	now := time.Now()
	if raw.Exp != 0 && now.After(time.Unix(raw.Exp, 0).Add(clockSkew)) {
		return nil, errJWTExpired
	}
	if raw.Nbf != 0 && now.Add(clockSkew).Before(time.Unix(raw.Nbf, 0)) {
		return nil, errJWTNotYetValid
	}
	if len(issuers) > 0 {
		if _, ok := issuers[raw.Iss]; !ok {
			return nil, errJWTInvalid
		}
	}
	if len(audiences) > 0 {
		matched := false
		for _, aud := range raw.Aud {
			if _, ok := audiences[aud]; ok {
				matched = true
				break
			}
		}
		if !matched {
			return nil, errJWTInvalid
		}
	}

	claims := &JWTClaims{
		Subject:    raw.Sub,
		Issuer:     raw.Iss,
		Audience:   []string(raw.Aud),
		RawPayload: payloadBytes,
	}
	if raw.Exp != 0 {
		claims.ExpiresAt = time.Unix(raw.Exp, 0)
	}
	if raw.Iat != 0 {
		claims.IssuedAt = time.Unix(raw.Iat, 0)
	}
	if raw.Nbf != 0 {
		claims.NotBefore = time.Unix(raw.Nbf, 0)
	}
	return claims, nil
}

// verifyECDSAJWT verifies a JWT ECDSA signature in IEEE P1363 format (r||s, fixed width).
func verifyECDSAJWT(pub *ecdsa.PublicKey, digest, sig []byte, keySize int) bool {
	if len(sig) != 2*keySize {
		return false
	}
	r := new(big.Int).SetBytes(sig[:keySize])
	s := new(big.Int).SetBytes(sig[keySize:])
	return ecdsa.Verify(pub, digest, r, s)
}

// extractBearerToken returns the token from "Authorization: Bearer <token>".
// The scheme is matched case-insensitively per RFC 7235 (auth-scheme is case-insensitive).
// Used by JWTAuth and OAuth2Introspect.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	// Minimum: "bearer" (6) + " " (1) + at least 1 token char = 8.
	if len(auth) < 8 || auth[6] != ' ' || !strings.EqualFold(auth[:6], "bearer") {
		return ""
	}
	return auth[7:]
}
