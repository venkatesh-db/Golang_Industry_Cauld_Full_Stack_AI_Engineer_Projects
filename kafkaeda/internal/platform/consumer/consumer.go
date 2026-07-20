package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/venkatesh/kafkaeda/internal/events"
)

type Handler func(context.Context, pgx.Tx, events.Envelope) error

type Config struct {
	Topic        string
	GroupID      string
	ConsumerName string
}

// Run implements the consume -> idempotency record -> state transition -> commit
// sequence. Kafka offsets advance only after the state transaction commits.
func Run(ctx context.Context, pool *pgxpool.Pool, brokers []string, config Config, handler Handler) error {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		GroupID:        config.GroupID,
		Topic:          config.Topic,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0,
		StartOffset:    kafka.FirstOffset,
		// Topics are provisioned lazily by the outbox writer in this local
		// topology. Rejoin the group when a newly-created topic gains partitions
		// instead of leaving this reader permanently assigned to none.
		WatchPartitionChanges:  true,
		PartitionWatchInterval: time.Second,
	})
	defer reader.Close()

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("fetch %s: %w", config.Topic, err)
		}

		var event events.Envelope
		if err := json.Unmarshal(message.Value, &event); err != nil {
			return fmt.Errorf("decode event on %s at %d: %w", config.Topic, message.Offset, err)
		}
		if event.EventID == "" || event.EventType == "" || event.SchemaVersion != 1 {
			return fmt.Errorf("reject invalid event on %s at %d", config.Topic, message.Offset)
		}

		err = process(ctx, pool, config.ConsumerName, event, handler)
		if err != nil {
			// Do not commit the offset. Retrying is intentional for transient DB and
			// Kafka failures; a production deployment adds retry topics and a DLQ.
			slog.Error("event processing failed", "consumer", config.ConsumerName, "event_id", event.EventID, "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			return fmt.Errorf("commit %s offset %d: %w", config.Topic, message.Offset, err)
		}
	}
}

func process(ctx context.Context, pool *pgxpool.Pool, consumerName string, event events.Envelope, handler Handler) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin consumer transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		INSERT INTO platform.processed_events (consumer_name, event_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, consumerName, event.EventID)
	if err != nil {
		return fmt.Errorf("record processed event: %w", err)
	}
	if result.RowsAffected() == 1 {
		if err := handler(ctx, tx, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit consumer transaction: %w", err)
	}
	return nil
}
