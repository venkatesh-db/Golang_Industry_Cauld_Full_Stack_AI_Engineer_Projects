CREATE TABLE IF NOT EXISTS profiles (
    id BIGINT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    bio TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS posts (
    id BIGSERIAL PRIMARY KEY,
    author_id BIGINT NOT NULL REFERENCES profiles(id),
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS posts_created_at_idx ON posts (created_at DESC);
CREATE INDEX IF NOT EXISTS posts_author_id_idx ON posts (author_id);

INSERT INTO profiles (id, username, bio) VALUES
    (1, 'venkatesh', 'Principal-architect observability lab'),
    (2, 'gopher', 'Distributed systems and Go performance')
ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, bio = EXCLUDED.bio;

INSERT INTO posts (author_id, body)
SELECT seed.author_id, seed.body
FROM (VALUES
    (1::BIGINT, 'Building bounded observability for high-scale Go services'),
    (2::BIGINT, 'Trace exemplars connect latency spikes to distributed traces'),
    (1::BIGINT, 'Low-cardinality metrics survive billion-request workloads')
) AS seed(author_id, body)
WHERE NOT EXISTS (SELECT 1 FROM posts);
