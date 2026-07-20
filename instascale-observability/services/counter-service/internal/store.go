// Package counter implements the counter-service: like/follower counts backed by
// Redis hot counters with Postgres as source of truth. It also implements
// chaos.PoolExhauster so the db-pool-exhaust mode can starve the pgx pool.
package counter

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	pool  *pgxpool.Pool
	redis *redis.Client

	// held connections for the db-pool-exhaust chaos mode.
	holdMu sync.Mutex
	held   []*pgxpool.Conn
}

func NewStore(pool *pgxpool.Pool, rdb *redis.Client) *Store {
	return &Store{pool: pool, redis: rdb}
}

// Counts returns (likes, followers) for a user, reading Redis first (cache-aside)
// and falling back to Postgres on a miss.
func (s *Store) Counts(ctx context.Context, userID int64) (likes, followers int64, err error) {
	lk := key(userID, "likes")
	fk := key(userID, "followers")

	if vals, rerr := s.redis.MGet(ctx, lk, fk).Result(); rerr == nil && vals[0] != nil && vals[1] != nil {
		return toInt(vals[0]), toInt(vals[1]), nil
	}

	row := s.pool.QueryRow(ctx,
		`SELECT like_count, follower_count FROM counters WHERE user_id=$1`, userID)
	if err = row.Scan(&likes, &followers); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	// Warm the cache with a short TTL.
	s.redis.Set(ctx, lk, likes, 60*time.Second)
	s.redis.Set(ctx, fk, followers, 60*time.Second)
	return likes, followers, nil
}

// Like increments the like counter: write-through to Redis, best-effort async to
// Postgres. Returns the new Redis value.
func (s *Store) Like(ctx context.Context, userID int64) (int64, error) {
	n, err := s.redis.Incr(ctx, key(userID, "likes")).Result()
	if err != nil {
		return 0, err
	}
	// Reconcile source of truth; upsert so a new user is created lazily.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO counters(user_id, like_count, follower_count)
		 VALUES($1, 1, 0)
		 ON CONFLICT (user_id) DO UPDATE SET like_count = counters.like_count + 1`, userID)
	return n, err
}

func key(userID int64, kind string) string {
	return "counts:" + itoa(userID) + ":" + kind
}

// --- chaos.PoolExhauster ---

// HoldConns acquires n pool connections and holds them, driving acquire-waits.
func (s *Store) HoldConns(ctx context.Context, n int) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	for i := 0; i < n; i++ {
		// Short acquire timeout so we don't block the trigger call forever once
		// the pool is drained — the point is to leave it drained.
		acqCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		conn, err := s.pool.Acquire(acqCtx)
		cancel()
		if err != nil {
			break
		}
		s.held = append(s.held, conn)
	}
}

// ReleaseConns returns every held connection to the pool.
func (s *Store) ReleaseConns() {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	for _, c := range s.held {
		c.Release()
	}
	s.held = nil
}
