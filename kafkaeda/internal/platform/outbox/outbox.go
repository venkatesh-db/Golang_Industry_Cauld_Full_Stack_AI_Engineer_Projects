package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/venkatesh/kafkaeda/internal/events"
)

func Add(ctx context.Context, tx pgx.Tx, topic, key string, event events.Envelope) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal outbox event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO platform.outbox_events (id, topic, event_key, payload)
		VALUES ($1, $2, $3, $4)`, event.EventID, topic, key, payload)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

type Relay struct {
	pool      *pgxpool.Pool
	writer    *kafka.Writer
	batchSize int
}

func NewRelay(pool *pgxpool.Pool, brokers []string) *Relay {
	return &Relay{
		pool: pool,
		writer: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Balancer:               &kafka.Hash{},
			RequiredAcks:           kafka.RequireAll,
			AllowAutoTopicCreation: true,
			BatchTimeout:           100 * time.Millisecond,
		},
		batchSize: 50,
	}
}

func (r *Relay) Close() error { return r.writer.Close() }

func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := r.publishBatch(ctx); err != nil {
			slog.Error("outbox batch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type row struct {
	id      string
	topic   string
	key     string
	payload []byte
}

// publishBatch deliberately retains the database row lock while publishing. If this
// process stops after Kafka acknowledges but before the commit, it republishes on
// recovery; downstream idempotency makes that at-least-once duplicate harmless.
func (r *Relay) publishBatch(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin outbox transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id::text, topic, event_key, payload
		FROM platform.outbox_events
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, r.batchSize)
	if err != nil {
		return fmt.Errorf("lock outbox rows: %w", err)
	}
	defer rows.Close()

	batch := make([]row, 0, r.batchSize)
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.topic, &item.key, &item.payload); err != nil {
			return fmt.Errorf("scan outbox row: %w", err)
		}
		batch = append(batch, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read outbox rows: %w", err)
	}
	for _, item := range batch {
		err := r.writer.WriteMessages(ctx, kafka.Message{
			Topic:   item.topic,
			Key:     []byte(item.key),
			Value:   item.payload,
			Headers: []kafka.Header{{Key: "event_id", Value: []byte(item.id)}},
		})
		if err != nil {
			// The row lock belongs to this transaction. Release it before recording
			// the failed attempt through a fresh connection, otherwise the error
			// metadata would be rolled back with the failed publishing attempt.
			_ = tx.Rollback(ctx)
			_, recordErr := r.pool.Exec(ctx, `UPDATE platform.outbox_events
				SET publish_attempts = publish_attempts + 1, last_error = $2 WHERE id = $1`, item.id, err.Error())
			if recordErr != nil {
				return fmt.Errorf("publish %s: %w (also record failure: %v)", item.id, err, recordErr)
			}
			return fmt.Errorf("publish %s: %w", item.id, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE platform.outbox_events
			SET published_at = now(), publish_attempts = publish_attempts + 1, last_error = NULL WHERE id = $1`, item.id); err != nil {
			return fmt.Errorf("mark event published: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbox transaction: %w", err)
	}
	if len(batch) > 0 {
		slog.Info("outbox published", "count", len(batch))
	}
	return nil
}
