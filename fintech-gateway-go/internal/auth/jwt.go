// Package auth verifies HS256-signed JWTs at the gateway edge, before a
// request is allowed anywhere near a backend.
//
// This is a minimal, single-algorithm reference implementation, not a
// general-purpose JWT library. Production systems should use a vetted
// library (e.g. golang-jwt/jwt) that supports the full claim set, key
// rotation (kid header + JWKS), and asymmetric algorithms (RS256/ES256)
// for cases where the verifier shouldn't hold the signing secret. This
// implementation exists to make explicit what a correct HS256 verifier
// must do: pin the algorithm, compare signatures in constant time, and
// require and check expiry.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrMalformedToken = errors.New("auth: malformed token")
	ErrBadSignature   = errors.New("auth: signature verification failed")
	ErrUnsupportedAlg = errors.New("auth: unsupported or missing algorithm")
	ErrMissingExpiry  = errors.New("auth: token has no exp claim")
	ErrExpired        = errors.New("auth: token has expired")
	ErrEmptySecret    = errors.New("auth: secret must not be empty")
)

type Claims struct {
	Subject   string
	ExpiresAt time.Time
	IssuedAt  time.Time
	Raw       map[string]any
}

type header struct {
	Alg string `json:"alg"`
}

// Verifier holds the HMAC secret and clock used to validate tokens.
// Constructed once and reused; safe for concurrent use (it holds no
// mutable state).
type Verifier struct {
	secret []byte
	now    func() time.Time
}

func NewVerifier(secret []byte) (*Verifier, error) {
	return newVerifierWithClock(secret, time.Now)
}

func newVerifierWithClock(secret []byte, now func() time.Time) (*Verifier, error) {
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}
	return &Verifier{secret: secret, now: now}, nil
}

// Verify checks the token's structure, algorithm, signature, and
// expiry, and returns its claims if all checks pass.
//
// The algorithm is read from the token's own header and pinned to
// HS256 here — accepting whatever alg the token claims (including
// "none") is the classic JWT algorithm-confusion vulnerability, where an
// attacker crafts an unsigned or differently-signed token and the
// verifier trusts it anyway.
func (v *Verifier) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	headerJSON, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, ErrMalformedToken
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return nil, ErrMalformedToken
	}
	if h.Alg != "HS256" {
		return nil, ErrUnsupportedAlg
	}

	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, ErrMalformedToken
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	wantSig := mac.Sum(nil)

	if !hmac.Equal(gotSig, wantSig) {
		return nil, ErrBadSignature
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrMalformedToken
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadJSON, &raw); err != nil {
		return nil, ErrMalformedToken
	}

	expUnix, ok := raw["exp"].(float64)
	if !ok {
		return nil, ErrMissingExpiry
	}
	expiresAt := time.Unix(int64(expUnix), 0)
	if !v.now().Before(expiresAt) {
		return nil, ErrExpired
	}

	claims := &Claims{ExpiresAt: expiresAt, Raw: raw}
	if sub, ok := raw["sub"].(string); ok {
		claims.Subject = sub
	}
	if iat, ok := raw["iat"].(float64); ok {
		claims.IssuedAt = time.Unix(int64(iat), 0)
	}
	return claims, nil
}
