// Package token verifies signed playback tokens. It is verify-only: this
// service authorizes an already-issued identity, it never mints one (a real
// deployment pushes verification to edge KV — see spec §7).
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrMalformed = errors.New("token: malformed")
	ErrBadSig    = errors.New("token: signature mismatch")
	ErrExpired   = errors.New("token: expired")
)

// Claims are the verified contents of a playback token.
type Claims struct {
	AccountID   string
	DeviceLimit int
	Exp         int64 // unix seconds
	AssetScope  string
}

// Format: accountId.deviceLimit.exp.assetScope.hexHMAC
// The HMAC is computed over the dot-joined payload (everything before the sig).
const sep = "."

// Sign builds a token for the given claims. Provided for tests and a token-mint
// helper CLI — the request path only ever calls Verify.
func Sign(key []byte, c Claims) string {
	payload := strings.Join([]string{
		c.AccountID,
		strconv.Itoa(c.DeviceLimit),
		strconv.FormatInt(c.Exp, 10),
		c.AssetScope,
	}, sep)
	return payload + sep + sign(key, payload)
}

// Verify parses and validates a token. It checks the signature with a
// constant-time compare (hmac.Equal) before trusting any field, then expiry.
func Verify(key []byte, tok string, now time.Time) (Claims, error) {
	i := strings.LastIndex(tok, sep)
	if i <= 0 || i == len(tok)-1 {
		return Claims{}, ErrMalformed
	}
	payload, gotSig := tok[:i], tok[i+1:]

	// Constant-time signature check first — never parse untrusted fields
	// before the signature is proven valid.
	if !hmac.Equal([]byte(gotSig), []byte(sign(key, payload))) {
		return Claims{}, ErrBadSig
	}

	parts := strings.Split(payload, sep)
	if len(parts) != 4 {
		return Claims{}, ErrMalformed
	}
	limit, err := strconv.Atoi(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	c := Claims{AccountID: parts[0], DeviceLimit: limit, Exp: exp, AssetScope: parts[3]}
	if c.AccountID == "" || c.DeviceLimit < 1 {
		return Claims{}, ErrMalformed
	}
	if now.Unix() >= c.Exp {
		return Claims{}, ErrExpired
	}
	return c, nil
}

func sign(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}
