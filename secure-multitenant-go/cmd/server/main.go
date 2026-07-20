// Command server wires the five security building blocks into one HTTP service:
// TLS 1.2+ transport, AES-256 encryption at rest, tenant_id scoping, and RBAC.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/secure-multitenant-go/internal/cryptox"
	"github.com/venkatesh/secure-multitenant-go/internal/httpx"
	"github.com/venkatesh/secure-multitenant-go/internal/pgstore"
	"github.com/venkatesh/secure-multitenant-go/internal/rbac"
	"github.com/venkatesh/secure-multitenant-go/internal/tenant"
)

func main() {
	// In production the key comes from a KMS / secret manager, never generated
	// at boot. Generated here only so the demo runs standalone.
	key, err := cryptox.NewKey()
	if err != nil {
		log.Fatalf("generate key: %v", err)
	}
	cipher, err := cryptox.NewCipher(key)
	if err != nil {
		log.Fatalf("build cipher: %v", err)
	}

	// Persistence: Postgres when DATABASE_URL is set, else in-memory. Both
	// satisfy tenant.Repository, so the handler code is identical.
	store := newStore()

	mux := http.NewServeMux()

	// POST /records — needs billing:manage; stores an AES-256-GCM payload whose
	// AAD is the tenant id, scoped in a tenant-isolated store.
	mux.Handle("/records", rbac.Require(rbac.PermManageBilling)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid, err := tenant.FromContext(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			enc, err := cipher.Encrypt([]byte("sensitive-payload"), []byte(tid))
			if err != nil {
				log.Printf("encrypt: %v", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if _, err := store.Put(r.Context(), "rec-"+string(tid), enc); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("stored (AES-256-GCM, tenant-scoped)\n"))
		})))

	// Middleware chain (outermost first): tenant scoping -> role extraction.
	handler := tenant.Middleware(rbac.Extract(mux))

	srv := httpx.NewServer(":8443", handler)
	log.Printf("listening on https://localhost:8443 (TLS 1.2+)")
	// Provide real certificate paths in production.
	log.Fatal(srv.ListenAndServeTLS("server.crt", "server.key"))
}

// newStore returns a Postgres-backed repository when DATABASE_URL is set
// (running migrations first), otherwise the in-memory store.
func newStore() tenant.Repository {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Printf("DATABASE_URL unset — using in-memory store")
		return tenant.NewStore()
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	if err := pgstore.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("using Postgres store")
	return pgstore.New(pool)
}
