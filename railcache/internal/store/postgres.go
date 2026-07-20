// Package store is the Postgres source-of-truth layer.
package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"railcache/internal/search"
)

// Options tune the pool and its server-side guardrails.
type Options struct {
	MaxConns         int32
	StatementTimeout time.Duration // server-side kill for a slow query
	ConnectTimeout   time.Duration
}

// DB wraps a pgx pool and exposes the read query RailCache shields behind Redis.
type DB struct {
	pool *pgxpool.Pool
}

// Open creates a pgx pool from a connection string with production guardrails:
// a bounded pool, a per-statement server-side timeout (so one pathological plan
// can't hold a connection indefinitely under burst), and a connect timeout.
func Open(ctx context.Context, url string, opts Options) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = opts.MaxConns
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	// statement_timeout is enforced by Postgres itself, independent of the Go
	// context — belt and suspenders against a slow fill query.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] =
		strconv.Itoa(int(opts.StatementTimeout.Milliseconds()))

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close releases the pool.
func (db *DB) Close() { db.pool.Close() }

// Ping verifies connectivity (used by readiness).
func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// Stat exposes pool statistics for metrics gauges.
func (db *DB) Stat() *pgxpool.Stat { return db.pool.Stat() }

// ListStations returns every known station code (for the validation whitelist).
func (db *DB) ListStations(ctx context.Context) ([]string, error) {
	rows, err := db.pool.Query(ctx, `SELECT code FROM stations`)
	if err != nil {
		return nil, fmt.Errorf("list stations: %w", err)
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan station: %w", err)
		}
		codes = append(codes, c)
	}
	return codes, rows.Err()
}

// searchSQL finds trains that stop at the origin before the destination on the
// same route, running on the requested date. It LEFT JOINs availability so a
// train that runs the route but has no inventory row for the class still
// appears (as "not available") rather than silently vanishing — hiding a train
// is a worse UX than showing it unavailable. DISTINCT ON collapses the earliest
// origin departure for routes that touch a station more than once.
const searchSQL = `
SELECT number, name, dep_from, arr_to, class, available, total
FROM (
    SELECT DISTINCT ON (t.id)
           t.id,
           t.number,
           t.name,
           s_from.dep AS dep_from,
           s_to.arr   AS arr_to,
           $4::text   AS class,
           a.available,
           a.total
    FROM train_stops s_from
    JOIN train_stops s_to
           ON s_to.train_id = s_from.train_id
          AND s_to.seq > s_from.seq
    JOIN trains t
           ON t.id = s_from.train_id
    LEFT JOIN seat_availability a
           ON a.train_id = t.id
          AND a.travel_date = $3::date
          AND a.class = $4
    WHERE s_from.station_code = $1
      AND s_to.station_code   = $2
    ORDER BY t.id, s_from.seq
) trains
ORDER BY dep_from NULLS LAST;
`

// Search runs the SoT query for one normalized request.
func (db *DB) Search(ctx context.Context, q search.Query) (search.SearchResult, error) {
	res := search.SearchResult{Query: q, Trains: []search.Train{}}

	rows, err := db.pool.Query(ctx, searchSQL, q.From, q.To, q.Date, q.Class)
	if err != nil {
		return res, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tr search.Train
		var dep, arr *string
		var avail, total *int
		if err := rows.Scan(&tr.Number, &tr.Name, &dep, &arr, &tr.Class, &avail, &total); err != nil {
			return res, fmt.Errorf("scan row: %w", err)
		}
		tr.DepFrom = derefStr(dep)
		tr.ArrTo = derefStr(arr)
		tr.HasClass = avail != nil && total != nil
		tr.Available = derefInt(avail)
		tr.Total = derefInt(total)
		res.Trains = append(res.Trains, tr)
	}
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("iterate rows: %w", err)
	}
	return res, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}
