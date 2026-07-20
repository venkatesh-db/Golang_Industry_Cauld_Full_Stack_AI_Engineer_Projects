package pgstore

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/secure-multitenant-go/internal/tenant"
)

// testStore spins up a Store against DATABASE_URL, migrates, and truncates the
// table so each test starts clean. It skips when DATABASE_URL is unset so the
// unit suite stays hermetic; run it with e.g.
//
//	DATABASE_URL=postgres://localhost:5432/app_test go test ./internal/pgstore
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE records"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return New(pool)
}

func ctxFor(id tenant.ID) context.Context {
	return tenant.NewContext(context.Background(), id)
}

func TestPGStore_TenantIsolation(t *testing.T) {
	s := testStore(t)
	ctxA := ctxFor("tenant-a")
	ctxB := ctxFor("tenant-b")

	if _, err := s.Put(ctxA, "rec1", "a-data"); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if _, err := s.Put(ctxB, "rec1", "b-data"); err != nil { // same id, different tenant
		t.Fatalf("Put B: %v", err)
	}

	tests := []struct {
		name     string
		ctx      context.Context
		id       string
		wantErr  error
		wantData string
	}{
		{"A reads own rec1", ctxA, "rec1", nil, "a-data"},
		{"B reads own rec1", ctxB, "rec1", nil, "b-data"},
		{"A cannot see B-only id", ctxA, "missing", tenant.ErrNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := s.Get(tt.ctx, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v want %v", err, tt.wantErr)
			}
			if rec.Data != tt.wantData {
				t.Fatalf("data = %q want %q", rec.Data, tt.wantData)
			}
		})
	}
}

func TestPGStore_ListScoped(t *testing.T) {
	s := testStore(t)
	ctxA := ctxFor("tenant-a")
	ctxB := ctxFor("tenant-b")
	_, _ = s.Put(ctxA, "1", "x")
	_, _ = s.Put(ctxA, "2", "y")
	_, _ = s.Put(ctxB, "3", "z")

	got, err := s.List(ctxA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d want 2", len(got))
	}
	for _, r := range got {
		if r.TenantID != "tenant-a" {
			t.Fatalf("leaked record from tenant %q", r.TenantID)
		}
	}
}

func TestPGStore_RequiresTenant(t *testing.T) {
	s := testStore(t)
	if _, err := s.Put(context.Background(), "1", "x"); !errors.Is(err, tenant.ErrMissingTenant) {
		t.Fatalf("got %v want ErrMissingTenant", err)
	}
}
