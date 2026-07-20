// Package feed implements the feed-service: fan-out-on-read of posts from the
// users a viewer follows, with Redis cache-aside in front of Postgres.
package feed

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Post struct {
	ID        int64     `json:"id"`
	AuthorID  int64     `json:"author_id"`
	MediaURL  string    `json:"media_url"`
	Caption   string    `json:"caption"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

func NewStore(pool *pgxpool.Pool, rdb *redis.Client) *Store {
	return &Store{pool: pool, redis: rdb}
}

// Feed returns up to limit posts authored by users the viewer follows, newest
// first. Cache-aside: serve from Redis on hit; on miss read Postgres (fan-out via
// the edges join) and backfill the cache with a short TTL.
func (s *Store) Feed(ctx context.Context, viewerID int64, limit int) ([]Post, bool, error) {
	ck := "feed:" + itoa(viewerID)

	if cached, err := s.redis.Get(ctx, ck).Result(); err == nil {
		var posts []Post
		if json.Unmarshal([]byte(cached), &posts) == nil {
			return posts, true, nil // cache hit
		}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.author_id, p.media_url, p.caption, p.created_at
		FROM posts p
		JOIN edges e ON e.followee_id = p.author_id
		WHERE e.follower_id = $1
		ORDER BY p.created_at DESC
		LIMIT $2`, viewerID, limit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	posts := make([]Post, 0, limit)
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.AuthorID, &p.MediaURL, &p.Caption, &p.CreatedAt); err != nil {
			return nil, false, err
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	if b, err := json.Marshal(posts); err == nil {
		s.redis.Set(ctx, ck, b, 60*time.Second)
	}
	return posts, false, nil
}
