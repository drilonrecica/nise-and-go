// Generated once by nise; owned by this application. Nise will not overwrite it.

package authorization

import (
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/authz"
)

// The permission matrix.
//
// This table is the authorization policy written out in full, and the tests
// below check the catalog against it in **both** directions: nothing in the
// table is missing from a role, and nothing in a role is missing from the
// table.
//
// Both directions matter, and only one of them is obvious. A test that
// checked "the administrator can manage roles" would keep passing after
// somebody added `roles.manage` to the auditor bundle — which is the change
// that actually needs noticing. Widening a role is a one-line diff in
// catalog.go and a completely silent behavioural change; here it is a failing
// test that names the permission and the role.
//
// So: adding a permission to a role means editing this table, deliberately,
// in the same commit. That is the point.
var permissionMatrix = map[string][]string{
	// Everybody who uses the equipment. Note what is *not* here: no
	// reservations.manage, so a member can act on their own reservation and
	// nobody else's — an asymmetry the use case enforces by checking "holder
	// or ReservationsManage" rather than a permission alone.
	RoleMember: {
		"reservations.create",
		"reservations.read",
		"resources.read",
	},
	// Runs the shared equipment day to day. Gains resources.manage and
	// reservations.manage, and deliberately not resources.retire: taking a
	// resource out of service cancels other people's plans, and belongs with
	// the role that can also explain itself to them.
	RoleSteward: {
		"reservations.create",
		"reservations.manage",
		"reservations.read",
		"resources.manage",
		"resources.read",
	},
	RoleAdministrator: {
		"audit.read",
		"reservations.create",
		"reservations.manage",
		"reservations.read",
		"resources.manage",
		"resources.read",
		"resources.retire",
		"roles.manage",
		"roles.read",
		"sessions.revoke",
		"system.read",
		"users.manage",
		"users.read",
	},
	// The auditor reads and changes nothing. It exists so that "look, do
	// not touch" is expressible, which is what makes an audit role safe to
	// grant widely — a safety officer reviewing who used which instrument
	// needs the log and the calendar and no ability to alter either. Any
	// write permission appearing here is a serious finding rather than a
	// preference.
	RoleAuditor: {
		"audit.read",
		"reservations.read",
		"resources.read",
		"roles.read",
		"users.read",
	},
}

// writePermissions are the permissions that change something. A role that is
// supposed to be read-only having any of these is the failure this file
// exists to make loud.
//
// reservations.create is a write permission even though it sounds mild: a
// reservation takes a resource away from everybody else for its window, which
// is a change to what other people can do.
var writePermissions = []string{
	"reservations.create",
	"reservations.manage",
	"resources.manage",
	"resources.retire",
	"roles.manage",
	"sessions.revoke",
	"users.manage",
}

// readOnlyRoles are the roles that must hold no write permission, ever.
var readOnlyRoles = []string{RoleAuditor}

func testCatalog(t *testing.T) *authz.Catalog {
	t.Helper()

	catalog, err := Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	return catalog
}

// grants reports whether a named role holds a permission.
func grants(t *testing.T, catalog *authz.Catalog, role string, permission authz.Permission) bool {
	t.Helper()

	bundle, ok := catalog.Role(role)
	if !ok {
		t.Fatalf("the catalog has no role %q", role)
	}
	return bundle.Permissions().Has(permission)
}

// TestEveryRoleGrantsExactlyTheMatrix is the first direction: what the table
// says a role has, it has.
func TestEveryRoleGrantsExactlyTheMatrix(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	for role, granted := range permissionMatrix {
		t.Run(role, func(t *testing.T) {
			for _, name := range granted {
				permission, err := authz.NewPermission(name)
				if err != nil {
					t.Fatalf("the matrix names %q, which is not a well-formed permission: %v", name, err)
				}
				if !grants(t, catalog, role, permission) {
					t.Errorf("the matrix says %s grants %s, and the catalog does not", role, name)
				}
			}
		})
	}
}

// TestNoRoleGrantsAnythingOutsideTheMatrix is the second direction, and the
// one that catches a silent widening. Adding a permission to a role in
// catalog.go without editing the table above fails here, naming both.
func TestNoRoleGrantsAnythingOutsideTheMatrix(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	for role, expected := range permissionMatrix {
		t.Run(role, func(t *testing.T) {
			for _, permission := range catalog.Permissions() {
				name := permission.String()
				granted := grants(t, catalog, role, permission)
				inMatrix := slices.Contains(expected, name)
				switch {
				case granted && !inMatrix:
					t.Errorf("%s grants %s, which the matrix does not list — if that is intended, add it to the matrix in the same commit", role, name)
				case !granted && inMatrix:
					t.Errorf("the matrix lists %s for %s, and the catalog does not grant it", name, role)
				}
			}
		})
	}
}

// TestEveryRoleInTheCatalogIsInTheMatrix keeps a new role from arriving with
// no policy written for it. A role nobody enumerated is a bundle nobody
// reviewed.
func TestEveryRoleInTheCatalogIsInTheMatrix(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	for _, name := range catalog.RoleNames() {
		if _, described := permissionMatrix[name]; !described {
			t.Errorf("the catalog defines the role %q, which the matrix does not describe", name)
		}
	}
	for role := range permissionMatrix {
		if !slices.Contains(catalog.RoleNames(), role) {
			t.Errorf("the matrix describes the role %q, which the catalog does not define", role)
		}
	}
}

// TestAReadOnlyRoleHoldsNoWritePermission states the property the auditor
// role exists for, separately from the table — so that a change which edits
// the table to match a widened bundle still fails.
//
// That is the whole reason this test is not redundant with the two above. A
// matrix keeps the policy honest against accident; this keeps it honest
// against somebody making the table agree with a mistake.
func TestAReadOnlyRoleHoldsNoWritePermission(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	for _, role := range readOnlyRoles {
		for _, name := range writePermissions {
			permission, err := authz.NewPermission(name)
			if err != nil {
				t.Fatalf("NewPermission(%q): %v", name, err)
			}
			if grants(t, catalog, role, permission) {
				t.Errorf("the read-only role %s grants the write permission %s", role, name)
			}
			if slices.Contains(permissionMatrix[role], name) {
				t.Errorf("the matrix gives the read-only role %s the write permission %s", role, name)
			}
		}
	}
}

// TestEveryCataloguedPermissionIsHeldBySomeRole catches the permission that
// exists and nobody can ever have.
//
// Such a permission is not harmless: it reads as a control in the catalog, a
// reviewer counts it, and every check against it denies everybody — which
// looks like correct default-deny right up until somebody grants the role
// they think covers it.
func TestEveryCataloguedPermissionIsHeldBySomeRole(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	for _, permission := range catalog.Permissions() {
		held := slices.ContainsFunc(catalog.RoleNames(), func(name string) bool {
			return grants(t, catalog, name, permission)
		})
		if !held {
			t.Errorf("no role grants %s, so every check against it denies everybody", permission)
		}
	}
}

// TestPermissionNamesAreStable pins the strings themselves.
//
// A permission name is stored in the database against a role, so renaming the
// Go constant is free and renaming the string is a migration. Listing them
// here makes the difference visible in a diff.
func TestPermissionNamesAreStable(t *testing.T) {
	t.Parallel()

	expected := []string{
		"audit.read",
		"reservations.create",
		"reservations.manage",
		"reservations.read",
		"resources.manage",
		"resources.read",
		"resources.retire",
		"roles.manage",
		"roles.read",
		"sessions.revoke",
		"system.read",
		"users.manage",
		"users.read",
	}

	catalog := testCatalog(t)
	actual := make([]string, 0, len(catalog.Permissions()))
	for _, permission := range catalog.Permissions() {
		actual = append(actual, permission.String())
	}
	slices.Sort(actual)

	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Errorf("the permission set changed.\n got: %v\nwant: %v\n\nA permission name is stored against a role in the database, so a rename here is a migration.", actual, expected)
	}
}
