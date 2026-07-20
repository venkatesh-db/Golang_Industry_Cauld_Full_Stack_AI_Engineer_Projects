package rbac

import (
	"context"
	"net/http"
)

// HeaderName carries the caller's role. In production this comes from a verified
// JWT/session claim, not a raw client header — the middleware shape is the same.
const HeaderName = "X-Role"

type ctxKey struct{}

// NewContext returns a copy of ctx carrying the role.
func NewContext(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, ctxKey{}, role)
}

// FromContext extracts the role and whether one was set.
func FromContext(ctx context.Context) (Role, bool) {
	role, ok := ctx.Value(ctxKey{}).(Role)
	return role, ok
}

// Extract pulls the role from the request header into the context.
func Extract(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := Role(r.Header.Get(HeaderName))
		next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), role)))
	})
}

// Require returns middleware that admits the request only if the context role
// holds perm. Missing role -> 401; insufficient/unknown role -> 403.
func Require(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := FromContext(r.Context())
			if !ok || role == "" {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			if err := Authorize(role, perm); err != nil {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
