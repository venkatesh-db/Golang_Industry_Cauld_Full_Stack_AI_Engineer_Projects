package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// releaseScript deletes the lock only if the caller still owns it (token match),
// so a filler that overran its TTL cannot delete a successor's freshly-acquired
// lock. This is the standard safe unlock for SET NX PX locks.
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`)

// Lock is a held distributed lock with the token needed to release it safely.
type Lock struct {
	client *Client
	key    string
	token  string
}

// Lease is the portion of a held lock needed by the search service.
type Lease interface {
	Release(ctx context.Context) error
}

// Acquire attempts to take the fill-lock for key using SET key token NX PX ttl.
// Returns (lock, true, nil) on success, (nil, false, nil) when someone else
// holds it, or an error on transport failure.
func (c *Client) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	token, err := newToken()
	if err != nil {
		return nil, false, err
	}
	ok, err := c.raw().SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &Lock{client: c, key: key, token: token}, true, nil
}

// Release runs the token-checked delete. Safe to call at most once (defer).
func (l *Lock) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return releaseScript.Run(ctx, l.client.raw(), []string{l.key}, l.token).Err()
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
