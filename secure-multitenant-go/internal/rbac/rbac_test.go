package rbac

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name    string
		role    Role
		perm    Permission
		wantErr error
	}{
		{"owner deletes tenant", RoleOwner, PermDeleteTenant, nil},
		{"owner manages billing", RoleOwner, PermManageBilling, nil},
		{"admin manages users", RoleAdmin, PermManageUsers, nil},
		{"admin cannot delete tenant", RoleAdmin, PermDeleteTenant, ErrForbidden},
		{"viewer views billing", RoleBillingViewer, PermViewBilling, nil},
		{"viewer cannot manage billing", RoleBillingViewer, PermManageBilling, ErrForbidden},
		{"viewer cannot manage users", RoleBillingViewer, PermManageUsers, ErrForbidden},
		{"unknown role", Role("intern"), PermViewBilling, ErrUnknownRole},
		{"empty role", Role(""), PermViewBilling, ErrUnknownRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Authorize(tt.role, tt.perm); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authorize(%q, %q) = %v, want %v", tt.role, tt.perm, err, tt.wantErr)
			}
		})
	}
}

func TestRequire_Middleware(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		perm       Permission
		wantStatus int
	}{
		{"authorized admin", string(RoleAdmin), PermManageUsers, http.StatusOK},
		{"forbidden viewer", string(RoleBillingViewer), PermManageUsers, http.StatusForbidden},
		{"unknown role forbidden", "intern", PermViewBilling, http.StatusForbidden},
		{"missing role unauthorized", "", PermViewBilling, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			h := Extract(Require(tt.perm)(final))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.role != "" {
				req.Header.Set(HeaderName, tt.role)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
