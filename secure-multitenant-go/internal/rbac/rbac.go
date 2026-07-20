// Package rbac implements a static Role-Based Access Control matrix and HTTP
// middleware that enforces a required permission per route.
package rbac

import "errors"

// Permission is a granular capability checked at an access boundary.
type Permission string

const (
	PermViewBilling   Permission = "billing:view"
	PermManageBilling Permission = "billing:manage"
	PermManageUsers   Permission = "users:manage"
	PermDeleteTenant  Permission = "tenant:delete"
)

// Role is a named bundle of permissions assigned to a principal.
type Role string

const (
	RoleOwner         Role = "owner"
	RoleAdmin         Role = "admin"
	RoleBillingViewer Role = "billing_viewer"
)

var (
	// ErrForbidden means the role is known but lacks the permission.
	ErrForbidden = errors.New("rbac: permission denied")
	// ErrUnknownRole means the role is not defined in the matrix.
	ErrUnknownRole = errors.New("rbac: unknown role")
)

// rolePermissions is the authoritative RBAC matrix. Membership in the inner set
// grants the permission; absence denies it.
var rolePermissions = map[Role]map[Permission]struct{}{
	RoleOwner: {
		PermViewBilling:   {},
		PermManageBilling: {},
		PermManageUsers:   {},
		PermDeleteTenant:  {},
	},
	RoleAdmin: {
		PermViewBilling:   {},
		PermManageBilling: {},
		PermManageUsers:   {},
	},
	RoleBillingViewer: {
		PermViewBilling: {},
	},
}

// Can reports whether role holds perm.
func Can(role Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	_, ok = perms[perm]
	return ok
}

// Authorize returns nil if role holds perm, ErrUnknownRole if the role is not
// defined, or ErrForbidden if it is defined but lacks perm.
func Authorize(role Role, perm Permission) error {
	if _, ok := rolePermissions[role]; !ok {
		return ErrUnknownRole
	}
	if !Can(role, perm) {
		return ErrForbidden
	}
	return nil
}
