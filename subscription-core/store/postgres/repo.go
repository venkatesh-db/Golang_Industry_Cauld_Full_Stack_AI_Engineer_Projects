// Package postgres is the pgx-backed implementation of the store ports. It is
// the durable source of truth; the in-memory store mirrors its contract.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"subscriptioncore/domain"
	"subscriptioncore/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store implements store.SubscriptionRepo, store.WebhookEventRepo and
// store.PlanRepo over a pgx connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time proof the Store satisfies every port it claims.
var (
	_ store.SubscriptionRepo = (*Store)(nil)
	_ store.WebhookEventRepo = (*Store)(nil)
	_ store.PlanRepo         = (*Store)(nil)
)

// New opens a pool against dsn.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// NewWithPool wraps an existing pool (useful for tests and shared pools).
func NewWithPool(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Migrate applies the embedded SQL files in lexical order. Each file uses
// CREATE ... IF NOT EXISTS, so this is safe to run on every startup.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := s.pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

const subColumns = `id, customer_id, provider_subscription_id, plan_id, status,
	seat_count, current_period_start, current_period_end, cancel_at_period_end,
	trial_end, provider_updated_at`

func scanSubscription(row pgx.Row) (domain.Subscription, error) {
	var s domain.Subscription
	err := row.Scan(
		&s.ID, &s.CustomerID, &s.ProviderSubID, &s.PlanID, &s.Status,
		&s.SeatCount, &s.CurrentPeriodStart, &s.CurrentPeriodEnd, &s.CancelAtPeriodEnd,
		&s.TrialEnd, &s.ProviderUpdatedAt,
	)
	return s, err
}

// GetByProviderID returns the subscription for a provider subscription id.
func (s *Store) GetByProviderID(ctx context.Context, providerSubID string) (domain.Subscription, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subColumns+` FROM subscriptions WHERE provider_subscription_id = $1`, providerSubID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Subscription{}, store.ErrNotFound
	}
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("get subscription by provider id: %w", err)
	}
	return sub, nil
}

// GetBySubject returns the most recently updated subscription for a customer.
func (s *Store) GetBySubject(ctx context.Context, subjectID string) (domain.Subscription, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subColumns+` FROM subscriptions
		 WHERE customer_id = $1 ORDER BY provider_updated_at DESC LIMIT 1`, subjectID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Subscription{}, store.ErrNotFound
	}
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("get subscription by subject: %w", err)
	}
	return sub, nil
}

// Upsert inserts or updates a subscription keyed on provider_subscription_id.
func (s *Store) Upsert(ctx context.Context, sub domain.Subscription) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO subscriptions (id, customer_id, provider_subscription_id, plan_id, status,
			seat_count, current_period_start, current_period_end, cancel_at_period_end,
			trial_end, provider_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (provider_subscription_id) DO UPDATE SET
			customer_id          = EXCLUDED.customer_id,
			plan_id              = EXCLUDED.plan_id,
			status               = EXCLUDED.status,
			seat_count           = EXCLUDED.seat_count,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end   = EXCLUDED.current_period_end,
			cancel_at_period_end = EXCLUDED.cancel_at_period_end,
			trial_end            = EXCLUDED.trial_end,
			provider_updated_at  = EXCLUDED.provider_updated_at`,
		sub.ID, sub.CustomerID, sub.ProviderSubID, sub.PlanID, sub.Status,
		sub.SeatCount, sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd,
		sub.TrialEnd, sub.ProviderUpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert subscription: %w", err)
	}
	return nil
}

// MarkProcessed records a webhook event id. The UNIQUE PK + ON CONFLICT DO
// NOTHING means a redelivery inserts zero rows and reports firstTime=false.
func (s *Store) MarkProcessed(ctx context.Context, providerEventID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO webhook_events (provider_event_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		providerEventID)
	if err != nil {
		return false, fmt.Errorf("mark webhook processed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Get returns a plan by id.
func (s *Store) Get(ctx context.Context, planID string) (domain.Plan, error) {
	var (
		p        domain.Plan
		features []byte
	)
	row := s.pool.QueryRow(ctx,
		`SELECT id, tier, features, seat_included FROM plans WHERE id = $1`, planID)
	if err := row.Scan(&p.ID, &p.Tier, &features, &p.SeatIncluded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Plan{}, store.ErrNotFound
		}
		return domain.Plan{}, fmt.Errorf("get plan: %w", err)
	}
	if len(features) > 0 {
		if err := json.Unmarshal(features, &p.Features); err != nil {
			return domain.Plan{}, fmt.Errorf("decode plan features: %w", err)
		}
	}
	return p, nil
}

// UpsertPlan inserts or updates a plan (setup/admin path).
func (s *Store) UpsertPlan(ctx context.Context, p domain.Plan) error {
	features, err := json.Marshal(p.Features)
	if err != nil {
		return fmt.Errorf("encode plan features: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO plans (id, tier, features, seat_included)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET
			tier          = EXCLUDED.tier,
			features      = EXCLUDED.features,
			seat_included = EXCLUDED.seat_included`,
		p.ID, p.Tier, features, p.SeatIncluded)
	if err != nil {
		return fmt.Errorf("upsert plan: %w", err)
	}
	return nil
}
