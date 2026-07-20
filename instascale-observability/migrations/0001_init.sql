-- InstaScale schema + seed. Sized so load tests hit realistic cache miss/hit
-- ratios while staying laptop-friendly.

CREATE TABLE IF NOT EXISTS users (
    id         BIGSERIAL PRIMARY KEY,
    handle     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS posts (
    id         BIGSERIAL PRIMARY KEY,
    author_id  BIGINT NOT NULL REFERENCES users(id),
    media_url  TEXT NOT NULL,
    caption    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Feed reads order by author + recency.
CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts(author_id, created_at DESC);

CREATE TABLE IF NOT EXISTS edges (
    follower_id BIGINT NOT NULL REFERENCES users(id),
    followee_id BIGINT NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id)
);
-- Fan-out-on-read walks a viewer's followees.
CREATE INDEX IF NOT EXISTS idx_edges_follower ON edges(follower_id);

CREATE TABLE IF NOT EXISTS counters (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id),
    follower_count BIGINT NOT NULL DEFAULT 0,
    like_count     BIGINT NOT NULL DEFAULT 0
);

-- ---- seed (idempotent) ----
-- 2,000 users, ~40k posts, each user follows ~25 others, counters initialized.
INSERT INTO users (id, handle)
SELECT g, 'user_' || g
FROM generate_series(1, 2000) g
ON CONFLICT DO NOTHING;

INSERT INTO posts (author_id, media_url, caption, created_at)
SELECT (1 + floor(random() * 2000))::bigint,
       'https://cdn.example/img/' || g || '.jpg',
       'caption ' || g,
       now() - (random() * interval '30 days')
FROM generate_series(1, 40000) g
ON CONFLICT DO NOTHING;

INSERT INTO edges (follower_id, followee_id)
SELECT f.id, t.id
FROM users f
CROSS JOIN LATERAL (
    SELECT id FROM users
    WHERE id <> f.id
    ORDER BY random()
    LIMIT 25
) t
ON CONFLICT DO NOTHING;

INSERT INTO counters (user_id, follower_count, like_count)
SELECT id, floor(random() * 5000)::bigint, floor(random() * 20000)::bigint
FROM users
ON CONFLICT DO NOTHING;

-- Reset sequences past the explicit ids we inserted.
SELECT setval('users_id_seq', (SELECT max(id) FROM users));
SELECT setval('posts_id_seq', (SELECT max(id) FROM posts));
