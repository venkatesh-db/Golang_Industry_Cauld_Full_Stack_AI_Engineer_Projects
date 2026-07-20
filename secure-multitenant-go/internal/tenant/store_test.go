package tenant

import (
	"context"
	"errors"
	"testing"
)

func ctxFor(id ID) context.Context {
	return NewContext(context.Background(), id)
}

func TestStore_TenantIsolation(t *testing.T) {
	s := NewStore()
	ctxA := ctxFor("tenant-a")
	ctxB := ctxFor("tenant-b")

	if _, err := s.Put(ctxA, "rec1", "a-data"); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if _, err := s.Put(ctxB, "rec2", "b-data"); err != nil {
		t.Fatalf("Put B: %v", err)
	}

	tests := []struct {
		name     string
		ctx      context.Context
		id       string
		wantErr  error
		wantData string
	}{
		{"owner reads own", ctxA, "rec1", nil, "a-data"},
		{"cross-tenant blocked", ctxB, "rec1", ErrNotFound, ""},
		{"other owner reads own", ctxB, "rec2", nil, "b-data"},
		{"missing record", ctxA, "does-not-exist", ErrNotFound, ""},
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

func TestStore_ListScoped(t *testing.T) {
	s := NewStore()
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

func TestStore_RequiresTenant(t *testing.T) {
	s := NewStore()
	tests := []struct {
		name string
		call func() error
	}{
		{"put", func() error { _, e := s.Put(context.Background(), "1", "x"); return e }},
		{"get", func() error { _, e := s.Get(context.Background(), "1"); return e }},
		{"list", func() error { _, e := s.List(context.Background()); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrMissingTenant) {
				t.Fatalf("got %v want ErrMissingTenant", err)
			}
		})
	}
}
