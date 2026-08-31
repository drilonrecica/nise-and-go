package authz

import (
	"fmt"
	"slices"
)

// Catalog is the closed set of permissions and roles one application defines.
//
// The zero value is unusable; construct one with [NewCatalog].
type Catalog struct {
	permissions map[Permission]struct{}
	roles       map[string]Role
}

// NewCatalog declares every permission the application checks and every role it
// grants.
//
// A role may only bundle declared permissions. Without that rule a typo in a
// role definition grants a permission nothing ever checks: the grant reads
// correctly, the check reads correctly, and the two never meet. Refusing at
// construction turns that into a startup failure instead.
func NewCatalog(permissions []Permission, roles []Role) (*Catalog, error) {
	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: no permissions are declared", ErrCatalog)
	}
	if len(permissions) > MaxCatalogPermissions {
		return nil, fmt.Errorf("%w: %d permissions, maximum is %d", ErrCatalog, len(permissions), MaxCatalogPermissions)
	}
	if len(roles) > MaxCatalogRoles {
		return nil, fmt.Errorf("%w: %d roles, maximum is %d", ErrCatalog, len(roles), MaxCatalogRoles)
	}

	declared := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		if !permission.Valid() {
			return nil, fmt.Errorf("%w: %q", ErrPermissionName, permission)
		}
		if _, repeat := declared[permission]; repeat {
			return nil, fmt.Errorf("%w: permission %q is declared twice", ErrCatalog, permission)
		}
		declared[permission] = struct{}{}
	}

	bundles := make(map[string]Role, len(roles))
	for _, role := range roles {
		if role.IsZero() {
			return nil, fmt.Errorf("%w: an unconstructed role", ErrCatalog)
		}
		if _, repeat := bundles[role.name]; repeat {
			return nil, fmt.Errorf("%w: role %q is declared twice", ErrCatalog, role.name)
		}
		for _, permission := range role.permissions.All() {
			if _, ok := declared[permission]; !ok {
				return nil, fmt.Errorf("%w: role %q bundles %q", ErrUndeclared, role.name, permission)
			}
		}
		bundles[role.name] = role
	}
	return &Catalog{permissions: declared, roles: bundles}, nil
}

// Declares reports whether the catalog declares permission.
//
// A use case checking a permission the catalog does not declare is asking about
// something that can never be granted, which is a programming error worth
// surfacing rather than a denial worth returning.
func (c *Catalog) Declares(permission Permission) bool {
	_, ok := c.permissions[permission]
	return ok
}

// Permissions returns every declared permission, sorted.
func (c *Catalog) Permissions() []Permission {
	out := make([]Permission, 0, len(c.permissions))
	for permission := range c.permissions {
		out = append(out, permission)
	}
	slices.Sort(out)
	return out
}

// Role returns the named role, and whether the catalog declares it.
func (c *Catalog) Role(name string) (Role, bool) {
	role, ok := c.roles[name]
	return role, ok
}

// RoleNames returns every declared role name, sorted.
func (c *Catalog) RoleNames() []string {
	out := make([]string, 0, len(c.roles))
	for name := range c.roles {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Grants returns the permissions the named roles bundle together.
//
// A name the catalog does not declare contributes nothing. That is the correct
// behavior for a grant row naming a role that has since been removed from the
// catalog: the stale row grants nothing rather than failing the request or,
// worse, being interpreted as something else. The second return value reports
// which names were unknown, so a caller can log the stale grants without
// refusing the request.
func (c *Catalog) Grants(names ...string) (Set, []string) {
	granted := Set{held: map[Permission]struct{}{}}
	var unknown []string
	for _, name := range names {
		role, ok := c.roles[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		granted = granted.Union(role.permissions)
	}
	slices.Sort(unknown)
	return granted, slices.Compact(unknown)
}
