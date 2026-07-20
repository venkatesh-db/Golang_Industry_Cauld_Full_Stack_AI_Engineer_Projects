// Package pgstore is a Postgres-backed tenant.Repository. Tenant isolation is
// enforced twice: an explicit WHERE tenant_id predicate on every query, and
// Row-Level Security keyed off a per-transaction GUC (app.current_tenant).
package pgstore

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/secure-multitenant-go/internal/tenant"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store implements tenant.Repository against a pgx connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps a pool in a Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Compile-time assertion that Store implements tenant.Repository.
var _ tenant.Repository = (*Store)(nil)

// Migrate applies every embedded migration in lexical order. Each file is
// expected to be idempotent (IF NOT EXISTS / DROP ... IF EXISTS).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("pgstore: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("pgstore: read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("pgstore: apply %s: %w", name, err)
		}
	}
	return nil
}

// withTenant opens a transaction, binds app.current_tenant for that transaction
// (activating the RLS policy), runs fn, and commits. fn's error rolls back.
func (s *Store) withTenant(ctx context.Context, fn func(tx pgx.Tx, tid tenant.ID) error) error {
	tid, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// set_config(..., is_local=true) scopes the GUC to this transaction, like
	// SET LOCAL but accepting a bind parameter.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", string(tid)); err != nil {
		return fmt.Errorf("pgstore: bind tenant: %w", err)
	}
	if err := fn(tx, tid); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit: %w", err)
	}
	return nil
}

// Put upserts a record owned by the context's tenant.
func (s *Store) Put(ctx context.Context, id, payload string) (tenant.Record, error) {
	var rec tenant.Record
	err := s.withTenant(ctx, func(tx pgx.Tx, tid tenant.ID) error {
		const q = `INSERT INTO records (tenant_id, id, data) VALUES ($1, $2, $3)
		           ON CONFLICT (tenant_id, id) DO UPDATE SET data = EXCLUDED.data
		           RETURNING tenant_id, id, data`
		var t string
		if err := tx.QueryRow(ctx, q, string(tid), id, payload).Scan(&t, &rec.ID, &rec.Data); err != nil {
			return fmt.Errorf("pgstore: put: %w", err)
		}
		rec.TenantID = tenant.ID(t)
		return nil
	})
	if err != nil {
		return tenant.Record{}, err
	}
	return rec, nil
}

// Get returns a record only if it belongs to the context's tenant, else
// tenant.ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (tenant.Record, error) {
	var rec tenant.Record
	err := s.withTenant(ctx, func(tx pgx.Tx, tid tenant.ID) error {
		const q = `SELECT tenant_id, id, data FROM records WHERE tenant_id = $1 AND id = $2`
		var t string
		err := tx.QueryRow(ctx, q, string(tid), id).Scan(&t, &rec.ID, &rec.Data)
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("pgstore: get: %w", err)
		}
		rec.TenantID = tenant.ID(t)
		return nil
	})
	if err != nil {
		return tenant.Record{}, err
	}
	return rec, nil
}

// List returns every record owned by the context's tenant.
func (s *Store) List(ctx context.Context) ([]tenant.Record, error) {
	out := make([]tenant.Record, 0)
	err := s.withTenant(ctx, func(tx pgx.Tx, tid tenant.ID) error {
		const q = `SELECT tenant_id, id, data FROM records WHERE tenant_id = $1 ORDER BY id`
		rows, err := tx.Query(ctx, q, string(tid))
		if err != nil {
			return fmt.Errorf("pgstore: list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rec tenant.Record
			var t string
			if err := rows.Scan(&t, &rec.ID, &rec.Data); err != nil {
				return fmt.Errorf("pgstore: scan: %w", err)
			}
			rec.TenantID = tenant.ID(t)
			out = append(out, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
