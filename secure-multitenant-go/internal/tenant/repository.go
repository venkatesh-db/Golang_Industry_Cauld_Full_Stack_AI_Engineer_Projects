package tenant

import "context"

// Repository is the tenant-scoped persistence contract. Every method derives its
// tenant from the context, so no caller can address another tenant's rows. Both
// the in-memory Store and the Postgres-backed store satisfy this interface.
type Repository interface {
	Put(ctx context.Context, id, payload string) (Record, error)
	Get(ctx context.Context, id string) (Record, error)
	List(ctx context.Context) ([]Record, error)
}

// Compile-time assertion that the in-memory Store implements Repository.
var _ Repository = (*Store)(nil)
