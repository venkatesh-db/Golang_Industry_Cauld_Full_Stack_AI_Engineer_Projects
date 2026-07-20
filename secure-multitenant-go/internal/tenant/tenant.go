// Package tenant provides tenant_id propagation and scoping for a multi-tenant
// service. The tenant id enters via an HTTP header, is carried in the request
// context, and every data-access call is scoped by it so cross-tenant reads are
// impossible through the public API.
package tenant

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ID is a tenant identifier.
type ID string

// HeaderName is the request header carrying the tenant id.
const HeaderName = "X-Tenant-ID"

type ctxKey struct{}

// ErrMissingTenant is returned when no tenant id is present in the context.
var ErrMissingTenant = errors.New("tenant: no tenant id in context")

// NewContext returns a copy of ctx carrying the tenant id.
func NewContext(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext extracts the tenant id, or ErrMissingTenant if absent/empty.
func FromContext(ctx context.Context) (ID, error) {
	id, ok := ctx.Value(ctxKey{}).(ID)
	if !ok || id == "" {
		return "", ErrMissingTenant
	}
	return id, nil
}

// Middleware extracts the tenant id from the request header into the context.
// Requests without a tenant id are rejected with 400.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.Header.Get(HeaderName))
		if raw == "" {
			http.Error(w, "missing "+HeaderName, http.StatusBadRequest)
			return
		}
		ctx := NewContext(r.Context(), ID(raw))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
