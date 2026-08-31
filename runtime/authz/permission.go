package authz

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Bounds on the names this package accepts. They are small because these are
// identifiers a person reads in a role definition and a grant table, not free
// text.
const (
	// MaxPermissionBytes bounds a permission name.
	MaxPermissionBytes = 64
	// MaxRoleBytes bounds a role name.
	MaxRoleBytes = 32
	// MaxCatalogPermissions bounds how many permissions one catalog declares.
	MaxCatalogPermissions = 512
	// MaxCatalogRoles bounds how many roles one catalog declares.
	MaxCatalogRoles = 64
	// MaxRolePermissions bounds how many permissions one role bundles.
	MaxRolePermissions = MaxCatalogPermissions
)

// Errors reported by construction.
var (
	// ErrPermissionName reports a permission outside the permitted shape.
	ErrPermissionName = errors.New("permission must be dotted lowercase segments")
	// ErrRoleName reports a role name outside the permitted shape.
	ErrRoleName = errors.New("role must be lowercase alphanumeric with underscores")
	// ErrCatalog reports a catalog that is inconsistent or out of bounds.
	ErrCatalog = errors.New("permission catalog is not usable")
	// ErrUndeclared reports a role bundling a permission the catalog does
	// not declare.
	ErrUndeclared = errors.New("role bundles a permission the catalog does not declare")
)

// Permission is one granular capability.
//
// Its zero value is not a permission, and [Set.Has] never reports it as held,
// so a missing value cannot accidentally authorize anything.
type Permission string

// NewPermission validates one permission name.
//
// The shape is dotted lowercase segments — "invoices.create" — so a permission
// reads as a subject and a verb, sorts usefully, and cannot be confused with a
// role name, which has no dot.
func NewPermission(name string) (Permission, error) {
	if !validDotted(name, MaxPermissionBytes) {
		return "", fmt.Errorf("%w: %q", ErrPermissionName, name)
	}
	return Permission(name), nil
}

// MustPermission is NewPermission for a compile-time constant, panicking on a
// name that cannot be valid. Use it only for a literal in a catalog
// definition, where a bad value is a programming error the process should not
// start with.
func MustPermission(name string) Permission {
	permission, err := NewPermission(name)
	if err != nil {
		panic(err)
	}
	return permission
}

// String returns the permission's name.
func (p Permission) String() string { return string(p) }

// Valid reports whether p is a well-formed permission.
func (p Permission) Valid() bool { return validDotted(string(p), MaxPermissionBytes) }

// Set is an immutable set of permissions.
//
// Its zero value is the empty set, which holds nothing. That is the correct
// default: a caller that forgot to resolve someone's permissions gets a set
// that authorizes nothing rather than one that authorizes everything.
type Set struct {
	held map[Permission]struct{}
}

// NewSet returns the set of the given permissions, refusing any that is not
// well formed.
func NewSet(permissions ...Permission) (Set, error) {
	held := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		if !permission.Valid() {
			return Set{}, fmt.Errorf("%w: %q", ErrPermissionName, permission)
		}
		held[permission] = struct{}{}
	}
	return Set{held: held}, nil
}

// Has reports whether the set holds permission.
//
// An invalid permission is never held, so a zero value or a typo at a call site
// denies rather than matching something unintended.
func (s Set) Has(permission Permission) bool {
	if !permission.Valid() {
		return false
	}
	_, ok := s.held[permission]
	return ok
}

// HasAll reports whether the set holds every one of permissions. An empty
// argument list is true: it asks nothing.
func (s Set) HasAll(permissions ...Permission) bool {
	for _, permission := range permissions {
		if !s.Has(permission) {
			return false
		}
	}
	return true
}

// Len returns how many permissions the set holds.
func (s Set) Len() int { return len(s.held) }

// All returns the set's permissions, sorted, so output and comparisons are
// stable.
func (s Set) All() []Permission {
	return slices.Sorted(maps.Keys(s.held))
}

// Union returns the set holding everything in either set.
func (s Set) Union(other Set) Set {
	held := make(map[Permission]struct{}, len(s.held)+len(other.held))
	maps.Copy(held, s.held)
	maps.Copy(held, other.held)
	return Set{held: held}
}

// Role is a named bundle of permissions.
type Role struct {
	name        string
	permissions Set
}

// NewRole validates one role.
func NewRole(name string, permissions ...Permission) (Role, error) {
	if !validName(name, MaxRoleBytes) {
		return Role{}, fmt.Errorf("%w: %q", ErrRoleName, name)
	}
	if len(permissions) > MaxRolePermissions {
		return Role{}, fmt.Errorf("%w: role %q bundles %d permissions, maximum is %d", ErrCatalog, name, len(permissions), MaxRolePermissions)
	}
	set, err := NewSet(permissions...)
	if err != nil {
		return Role{}, err
	}
	return Role{name: name, permissions: set}, nil
}

// Name returns the role's name.
func (r Role) Name() string { return r.name }

// Permissions returns the permissions the role bundles.
func (r Role) Permissions() Set { return r.permissions }

// IsZero reports whether r is the unconstructed zero value.
func (r Role) IsZero() bool { return r.name == "" }

// validDotted reports whether name is dotted lowercase segments.
func validDotted(name string, limit int) bool {
	if name == "" || len(name) > limit {
		return false
	}
	segments := strings.Split(name, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if !validName(segment, limit) {
			return false
		}
	}
	return true
}

// validName reports whether name is a lowercase alphanumeric identifier with
// underscores, starting with a letter.
func validName(name string, limit int) bool {
	if name == "" || len(name) > limit || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := range len(name) {
		char := name[i]
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_':
		default:
			return false
		}
	}
	return true
}
