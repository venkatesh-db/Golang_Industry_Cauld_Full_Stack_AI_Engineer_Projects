package social

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the write model, read projections, inbox deduplication and the
// transactional outbox. Kafka never becomes the source of truth for a like.
type Store struct{ pool *pgxpool.Pool }

func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnIdleTime = 5 * time.Minute
	config.ConnConfig.RuntimeParams["statement_timeout"] = "3000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

type Post struct {
	ID            string `json:"id"`
	AuthorID      string `json:"author_id"`
	AuthorHandle  string `json:"author_handle"`
	AuthorName    string `json:"author_name"`
	Caption       string `json:"caption"`
	Accent        string `json:"accent"`
	LikesCount    int    `json:"likes_count"`
	CommentsCount int    `json:"comments_count"`
}

type Notification struct {
	ID          string    `json:"id"`
	RecipientID string    `json:"recipient_id"`
	ActorID     string    `json:"actor_id"`
	PostID      string    `json:"post_id"`
	Kind        string    `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
}

type PipelineStats struct {
	OutboxPending          int `json:"outbox_pending"`
	OutboxPublished        int `json:"outbox_published"`
	EngagementsProjected   int `json:"engagements_projected"`
	NotificationsRequested int `json:"notifications_requested"`
	NotificationsDelivered int `json:"notifications_delivered"`
}

type PipelineEvent struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Topic     string     `json:"topic"`
	Key       string     `json:"key"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	Published *time.Time `json:"published_at,omitempty"`
}

func (s *Store) ListPosts(ctx context.Context) ([]Post, error) {
	rows, err := s.pool.Query(ctx, `
SELECT p.id, p.author_id, p.author_handle, p.author_name, p.caption, p.accent,
       COALESCE(c.likes_count, 0), COALESCE(c.comments_count, 0)
FROM posts p
LEFT JOIN post_counters c ON c.post_id = p.id
ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list posts: %w", err)
	}
	defer rows.Close()
	posts := make([]Post, 0)
	for rows.Next() {
		var post Post
		if err := rows.Scan(&post.ID, &post.AuthorID, &post.AuthorHandle, &post.AuthorName, &post.Caption, &post.Accent, &post.LikesCount, &post.CommentsCount); err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		posts = append(posts, post)
	}
	return posts, rows.Err()
}

// RecordLike performs the only synchronous business write. Its outbox insert
// is in the same transaction, which makes delivery retryable even if the
// process crashes immediately after the HTTP response is returned.
func (s *Store) RecordLike(ctx context.Context, postID, actorID, correlationID string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin like transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var postAuthorID string
	if err := tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, postID).Scan(&postAuthorID); err != nil {
		if err == pgx.ErrNoRows {
			return false, ErrPostNotFound
		}
		return false, fmt.Errorf("find post: %w", err)
	}

	likeID := newID()
	command, err := tx.Exec(ctx, `
INSERT INTO post_likes (id, post_id, actor_id) VALUES ($1, $2, $3)
ON CONFLICT (post_id, actor_id) DO NOTHING`, likeID, postID, actorID)
	if err != nil {
		return false, fmt.Errorf("insert like: %w", err)
	}
	if command.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit duplicate like: %w", err)
		}
		return false, nil
	}

	event, err := NewEvent(EventEngagementRecorded, postID, correlationID, LikeRecorded{
		LikeID: likeID, PostID: postID, PostAuthorID: postAuthorID, ActorID: actorID,
	})
	if err != nil {
		return false, err
	}
	if err := insertOutbox(ctx, tx, event, TopicEngagement); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit like transaction: %w", err)
	}
	return true, nil
}

var ErrPostNotFound = fmt.Errorf("post not found")

type OutboxEvent struct {
	ID      string
	Topic   string
	Key     string
	Payload []byte
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event Event, topic string) error {
	payload, err := event.JSON()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO outbox_events (id, aggregate_id, topic, partition_key, event_type, payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ID, event.Key, topic, event.Key, event.Type, payload, event.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// ClaimOutbox uses SKIP LOCKED so several relay replicas can safely drain the
// table without coordinating through Kafka or a separate leader election.
func (s *Store) ClaimOutbox(ctx context.Context, workerID string, limit int) ([]OutboxEvent, error) {
	rows, err := s.pool.Query(ctx, `
WITH claimable AS (
  SELECT id FROM outbox_events
  WHERE published_at IS NULL AND (locked_until IS NULL OR locked_until < now())
  ORDER BY occurred_at
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events o
SET locked_by = $2, locked_until = now() + interval '30 seconds', attempts = attempts + 1
FROM claimable c
WHERE o.id = c.id
RETURNING o.id, o.topic, o.partition_key, o.payload`, limit, workerID)
	if err != nil {
		return nil, fmt.Errorf("claim outbox: %w", err)
	}
	defer rows.Close()
	events := make([]OutboxEvent, 0)
	for rows.Next() {
		var event OutboxEvent
		if err := rows.Scan(&event.ID, &event.Topic, &event.Key, &event.Payload); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id, workerID string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE outbox_events
SET published_at = now(), locked_by = NULL, locked_until = NULL, publish_error = NULL
WHERE id = $1 AND locked_by = $2`, id, workerID)
	return err
}

func (s *Store) ReleaseOutbox(ctx context.Context, id, workerID string, cause error) {
	_, _ = s.pool.Exec(ctx, `
UPDATE outbox_events
SET locked_by = NULL, locked_until = NULL, publish_error = $3
WHERE id = $1 AND locked_by = $2`, id, workerID, cause.Error())
}

// ApplyEngagement is the engagement-projection consumer's transaction. The
// inbox row is inserted first; a Kafka redelivery then turns into a harmless
// no-op instead of incrementing a visible counter twice.
func (s *Store) ApplyEngagement(ctx context.Context, event Event) error {
	var like LikeRecorded
	if err := json.Unmarshal(event.Data, &like); err != nil {
		return fmt.Errorf("decode like payload: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	command, err := tx.Exec(ctx, `
INSERT INTO processed_events (consumer_name, event_id) VALUES ('engagement-projector', $1)
ON CONFLICT DO NOTHING`, event.ID)
	if err != nil {
		return fmt.Errorf("insert engagement inbox: %w", err)
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO post_counters (post_id, likes_count, comments_count) VALUES ($1, 1, 0)
ON CONFLICT (post_id) DO UPDATE SET likes_count = post_counters.likes_count + 1, updated_at = now()`, like.PostID)
	if err != nil {
		return fmt.Errorf("project engagement count: %w", err)
	}
	if like.PostAuthorID != like.ActorID {
		notificationID := newID()
		notificationEvent, err := NewEvent(EventNotificationRequested, like.PostAuthorID, event.CorrelationID, NotificationRequested{
			NotificationID: notificationID, RecipientID: like.PostAuthorID, ActorID: like.ActorID,
			PostID: like.PostID, Kind: "like", SourceEventID: event.ID,
		})
		if err != nil {
			return err
		}
		if err := insertOutbox(ctx, tx, notificationEvent, TopicNotification); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) PersistNotification(ctx context.Context, event Event) error {
	var request NotificationRequested
	if err := json.Unmarshal(event.Data, &request); err != nil {
		return fmt.Errorf("decode notification payload: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
INSERT INTO processed_events (consumer_name, event_id) VALUES ('notification-writer', $1)
ON CONFLICT DO NOTHING`, event.ID)
	if err != nil {
		return fmt.Errorf("insert notification inbox: %w", err)
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO notifications (id, recipient_id, actor_id, post_id, kind, source_event_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source_event_id) DO NOTHING`,
		request.NotificationID, request.RecipientID, request.ActorID, request.PostID, request.Kind, request.SourceEventID)
	if err != nil {
		return fmt.Errorf("write notification: %w", err)
	}
	delivery, err := NewEvent(EventNotificationDelivered, request.RecipientID, event.CorrelationID, NotificationDelivered{
		NotificationID: request.NotificationID, RecipientID: request.RecipientID, SourceEventID: request.SourceEventID,
	})
	if err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, delivery, TopicDelivery); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListNotifications(ctx context.Context, recipientID string) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, recipient_id, actor_id, post_id, kind, created_at
FROM notifications WHERE recipient_id = $1 ORDER BY created_at DESC LIMIT 12`, recipientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Notification, 0)
	for rows.Next() {
		var item Notification
		if err := rows.Scan(&item.ID, &item.RecipientID, &item.ActorID, &item.PostID, &item.Kind, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Pipeline(ctx context.Context) (PipelineStats, []PipelineEvent, error) {
	var stats PipelineStats
	err := s.pool.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE published_at IS NULL),
  count(*) FILTER (WHERE published_at IS NOT NULL),
  (SELECT count(*) FROM processed_events WHERE consumer_name = 'engagement-projector'),
  count(*) FILTER (WHERE event_type = $1),
  (SELECT count(*) FROM notifications)
FROM outbox_events`, EventNotificationRequested).Scan(
		&stats.OutboxPending, &stats.OutboxPublished, &stats.EngagementsProjected,
		&stats.NotificationsRequested, &stats.NotificationsDelivered)
	if err != nil {
		return stats, nil, fmt.Errorf("pipeline stats: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, event_type, topic, partition_key,
       CASE WHEN published_at IS NULL THEN 'pending relay' ELSE 'published' END,
       occurred_at, published_at
FROM outbox_events ORDER BY occurred_at DESC LIMIT 10`)
	if err != nil {
		return stats, nil, err
	}
	defer rows.Close()
	events := make([]PipelineEvent, 0)
	for rows.Next() {
		var event PipelineEvent
		if err := rows.Scan(&event.ID, &event.Type, &event.Topic, &event.Key, &event.Status, &event.CreatedAt, &event.Published); err != nil {
			return stats, nil, err
		}
		events = append(events, event)
	}
	return stats, events, rows.Err()
}
