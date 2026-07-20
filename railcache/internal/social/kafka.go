package social

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
)

// Broker is deliberately thin: ordering, replay, and ownership remain Kafka's
// job. A key is always supplied so all changes to one post (or one recipient)
// are routed to the same partition.
type Broker struct {
	brokers []string
	log     *slog.Logger
	mu      sync.Mutex
	writers map[string]*kafka.Writer
}

func NewBroker(brokers []string, log *slog.Logger) *Broker {
	return &Broker{brokers: brokers, log: log, writers: make(map[string]*kafka.Writer)}
}

func (b *Broker) Publish(ctx context.Context, topic, key string, payload []byte) error {
	event, err := DecodeEvent(payload)
	if err != nil {
		return err
	}
	writer := b.writer(topic)
	err = writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: payload,
		Headers: []kafka.Header{
			{Key: "event_id", Value: []byte(event.ID)},
			{Key: "event_type", Value: []byte(event.Type)},
			{Key: "schema_version", Value: []byte("1")},
			{Key: "correlation_id", Value: []byte(event.CorrelationID)},
		},
	})
	if err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	return nil
}

func (b *Broker) writer(topic string) *kafka.Writer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if writer := b.writers[topic]; writer != nil {
		return writer
	}
	writer := &kafka.Writer{
		Addr:                   kafka.TCP(b.brokers...),
		Topic:                  topic,
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: true,
		BatchTimeout:           20 * time.Millisecond,
		Async:                  false, // relay marks published only after broker ack
	}
	b.writers[topic] = writer
	return writer
}

func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for topic, writer := range b.writers {
		if err := writer.Close(); err != nil {
			b.log.Warn("close kafka writer", "topic", topic, "err", err)
		}
	}
}

type EventHandler func(context.Context, Event) error

// Consume commits only after the handler's Postgres transaction succeeds.
// Kafka's at-least-once delivery and the database inbox table together provide
// effectively-once observable side effects.
func (b *Broker) Consume(ctx context.Context, topic, group string, handler EventHandler) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        b.brokers,
		Topic:          topic,
		GroupID:        group,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0,
		StartOffset:    kafka.FirstOffset,
	})
	defer func() {
		if err := reader.Close(); err != nil {
			b.log.Warn("close kafka reader", "topic", topic, "err", err)
		}
	}()

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			b.log.Warn("kafka fetch failed; consumer will retry", "topic", topic, "group", group, "err", err)
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		event, err := DecodeEvent(message.Value)
		if err == nil {
			err = handler(ctx, event)
		}
		if err != nil {
			// Deliberately leave the offset uncommitted. The next delivery is safe
			// because every mutating consumer uses processed_events as an inbox.
			b.log.Error("event handling failed; leaving offset for retry", "topic", topic, "offset", message.Offset, "err", err)
			if !wait(ctx, 500*time.Millisecond) {
				return
			}
			continue
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			b.log.Warn("kafka commit failed; event may redeliver", "topic", topic, "offset", message.Offset, "err", err)
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Runtime groups the independently-scalable service roles for this demo. In a
// deployment each loop can be launched as its own process with the same code
// and consumer-group name; running together keeps the local experience simple.
type Runtime struct {
	store  *Store
	broker *Broker
	config Config
	log    *slog.Logger
	wg     sync.WaitGroup
}

func NewRuntime(store *Store, broker *Broker, config Config, log *slog.Logger) *Runtime {
	return &Runtime{store: store, broker: broker, config: config, log: log}
}

func (r *Runtime) Start(ctx context.Context) {
	r.wg.Add(3)
	go func() {
		defer r.wg.Done()
		r.runOutbox(ctx)
	}()
	go func() {
		defer r.wg.Done()
		r.broker.Consume(ctx, TopicEngagement, r.config.ConsumerPrefix+"-engagement-projector", func(ctx context.Context, event Event) error {
			if event.Type != EventEngagementRecorded {
				return fmt.Errorf("unexpected event type %q on %s", event.Type, TopicEngagement)
			}
			return r.store.ApplyEngagement(ctx, event)
		})
	}()
	go func() {
		defer r.wg.Done()
		r.broker.Consume(ctx, TopicNotification, r.config.ConsumerPrefix+"-notification-writer", func(ctx context.Context, event Event) error {
			if event.Type != EventNotificationRequested {
				return fmt.Errorf("unexpected event type %q on %s", event.Type, TopicNotification)
			}
			return r.store.PersistNotification(ctx, event)
		})
	}()
}

// Wait prevents consumers from racing a Postgres pool close during shutdown.
func (r *Runtime) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) runOutbox(ctx context.Context) {
	workerID := "relay-" + newID()
	ticker := time.NewTicker(r.config.OutboxInterval)
	defer ticker.Stop()
	for {
		if err := r.relayBatch(ctx, workerID); err != nil && ctx.Err() == nil {
			r.log.Warn("outbox relay batch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) relayBatch(ctx context.Context, workerID string) error {
	events, err := r.store.ClaimOutbox(ctx, workerID, r.config.OutboxBatchSize)
	if err != nil {
		return err
	}
	for _, event := range events {
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := r.broker.Publish(publishCtx, event.Topic, event.Key, event.Payload)
		cancel()
		if err != nil {
			r.store.ReleaseOutbox(ctx, event.ID, workerID, err)
			return err
		}
		if err := r.store.MarkOutboxPublished(ctx, event.ID, workerID); err != nil {
			// A published event whose acknowledgement is not persisted is expected
			// to be sent again. Consumers handle this exact at-least-once case.
			return fmt.Errorf("mark outbox %s published: %w", event.ID, err)
		}
	}
	return nil
}
