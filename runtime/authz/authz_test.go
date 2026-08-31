package authz_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/authz"
)

func testCatalog(t *testing.T) *authz.Catalog {
	t.Helper()

	permissions := []authz.Permission{
		authz.MustPermission("invoices.read"),
		authz.MustPermission("invoices.create"),
		authz.MustPermission("invoices.void"),
		authz.MustPermission("users.read"),
	}
	clerk, err := authz.NewRole("billing_clerk", permissions[0], permissions[1])
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	manager, err := authz.NewRole("billing_manager", permissions[0], permissions[1], permissions[2])
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	reader, err := authz.NewRole("directory_reader", permissions[3])
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	catalog, err := authz.NewCatalog(permissions, []authz.Role{clerk, manager, reader})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return catalog
}

func TestNewPermissionShape(t *testing.T) {
	t.Parallel()

	accepted := []string{"invoices.read", "a.b", "invoices.line_items.create", "x1.y2"}
	for _, name := range accepted {
		permission, err := authz.NewPermission(name)
		if err != nil {
			t.Errorf("NewPermission(%q): %v", name, err)
			continue
		}
		if permission.String() != name || !permission.Valid() {
			t.Errorf("permission %q is not itself", name)
		}
	}

	rejected := []string{
		"", "invoices", "Invoices.read", "invoices.Read", "invoices..read",
		".read", "invoices.", "invoices read", "invoices-read", "invoices.*",
		"1invoices.read", strings.Repeat("a", authz.MaxPermissionBytes) + ".read",
	}
	for _, name := range rejected {
		if _, err := authz.NewPermission(name); !errors.Is(err, authz.ErrPermissionName) {
			t.Errorf("NewPermission(%q) error = %v, want ErrPermissionName", name, err)
		}
		if authz.Permission(name).Valid() {
			t.Errorf("%q reports itself valid", name)
		}
	}

	// A wildcard is refused like any other bad name. A permission pattern is
	// how an accidental grant becomes invisible.
	if _, err := authz.NewPermission("invoices.*"); err == nil {
		t.Error("a wildcard permission was accepted")
	}
}

func TestSetIsEmptyByDefaultAndDeniesTheZeroValue(t *testing.T) {
	t.Parallel()

	// The zero set is the correct default: a caller that forgot to resolve
	// someone's permissions authorizes nothing rather than everything.
	var zero authz.Set
	if zero.Len() != 0 || zero.Has(authz.MustPermission("invoices.read")) {
		t.Fatal("the zero set holds something")
	}
	if len(zero.All()) != 0 {
		t.Error("the zero set enumerates something")
	}

	set, err := authz.NewSet(authz.MustPermission("invoices.read"), authz.MustPermission("invoices.create"))
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	if !set.Has(authz.MustPermission("invoices.read")) {
		t.Error("a held permission is not held")
	}
	if set.Has(authz.MustPermission("invoices.void")) {
		t.Error("an unheld permission is held")
	}
	// An invalid permission is never held, so a zero value or a typo at a
	// call site denies rather than matching something unintended.
	if set.Has("") || set.Has("invoices") || set.Has("INVOICES.READ") {
		t.Error("an invalid permission was reported held")
	}

	if !set.HasAll(authz.MustPermission("invoices.read"), authz.MustPermission("invoices.create")) {
		t.Error("HasAll denied a fully held list")
	}
	if set.HasAll(authz.MustPermission("invoices.read"), authz.MustPermission("invoices.void")) {
		t.Error("HasAll allowed a partially held list")
	}
	if !set.HasAll() {
		t.Error("HasAll of nothing is false")
	}

	if got := set.All(); len(got) != 2 || got[0] != "invoices.create" {
		t.Errorf("All = %v, want a sorted list", got)
	}
	if _, err := authz.NewSet("not a permission"); !errors.Is(err, authz.ErrPermissionName) {
		t.Errorf("NewSet accepted an invalid permission: %v", err)
	}
}

func TestSetUnionDoesNotMutateItsOperands(t *testing.T) {
	t.Parallel()

	first, err := authz.NewSet(authz.MustPermission("invoices.read"))
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	second, err := authz.NewSet(authz.MustPermission("users.read"))
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}

	union := first.Union(second)
	if !union.HasAll(authz.MustPermission("invoices.read"), authz.MustPermission("users.read")) {
		t.Fatalf("union = %v", union.All())
	}
	if first.Len() != 1 || second.Len() != 1 {
		t.Fatal("Union mutated an operand; two grants would then contaminate each other")
	}
}

func TestNewRoleShape(t *testing.T) {
	t.Parallel()

	role, err := authz.NewRole("billing_clerk", authz.MustPermission("invoices.read"))
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	if role.Name() != "billing_clerk" || role.IsZero() {
		t.Fatalf("role = %#v", role)
	}
	if !role.Permissions().Has(authz.MustPermission("invoices.read")) {
		t.Error("the role does not bundle its permission")
	}
	if !(authz.Role{}).IsZero() {
		t.Error("the zero role does not report IsZero")
	}

	// A role name has no dot, so it can never be confused with a permission.
	rejected := []string{"", "Billing", "billing clerk", "billing.clerk", "billing-clerk", "1billing", strings.Repeat("r", authz.MaxRoleBytes+1)}
	for _, name := range rejected {
		if _, err := authz.NewRole(name, authz.MustPermission("invoices.read")); !errors.Is(err, authz.ErrRoleName) {
			t.Errorf("NewRole(%q) error = %v, want ErrRoleName", name, err)
		}
	}
}

func TestCatalogRefusesUndeclaredPermissionsInRoles(t *testing.T) {
	t.Parallel()

	declared := []authz.Permission{authz.MustPermission("invoices.read")}
	typo, err := authz.NewRole("billing_clerk", authz.MustPermission("invoice.read"))
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	// This is the whole point of the catalog: without it the grant reads
	// correctly, the check reads correctly, and the two never meet.
	if _, err := authz.NewCatalog(declared, []authz.Role{typo}); !errors.Is(err, authz.ErrUndeclared) {
		t.Fatalf("NewCatalog error = %v, want ErrUndeclared", err)
	}
}

func TestNewCatalogRefusesInconsistentInput(t *testing.T) {
	t.Parallel()

	read := authz.MustPermission("invoices.read")
	role, err := authz.NewRole("billing_clerk", read)
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}

	if _, err := authz.NewCatalog(nil, nil); !errors.Is(err, authz.ErrCatalog) {
		t.Errorf("NewCatalog accepted an empty catalog: %v", err)
	}
	if _, err := authz.NewCatalog([]authz.Permission{read, read}, nil); !errors.Is(err, authz.ErrCatalog) {
		t.Errorf("NewCatalog accepted a repeated permission: %v", err)
	}
	if _, err := authz.NewCatalog([]authz.Permission{read}, []authz.Role{role, role}); !errors.Is(err, authz.ErrCatalog) {
		t.Errorf("NewCatalog accepted a repeated role: %v", err)
	}
	if _, err := authz.NewCatalog([]authz.Permission{read}, []authz.Role{{}}); !errors.Is(err, authz.ErrCatalog) {
		t.Errorf("NewCatalog accepted an unconstructed role: %v", err)
	}
	if _, err := authz.NewCatalog([]authz.Permission{"invoices"}, nil); !errors.Is(err, authz.ErrPermissionName) {
		t.Errorf("NewCatalog accepted an invalid permission: %v", err)
	}
}

func TestCatalogGrants(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)

	granted, unknown := catalog.Grants("billing_clerk")
	if len(unknown) != 0 {
		t.Fatalf("unknown = %v", unknown)
	}
	if !granted.HasAll(authz.MustPermission("invoices.read"), authz.MustPermission("invoices.create")) {
		t.Fatalf("granted = %v", granted.All())
	}
	if granted.Has(authz.MustPermission("invoices.void")) {
		t.Error("a clerk was granted the manager's permission")
	}

	// Several roles combine.
	combined, _ := catalog.Grants("billing_clerk", "directory_reader")
	if !combined.HasAll(authz.MustPermission("invoices.create"), authz.MustPermission("users.read")) {
		t.Fatalf("combined = %v", combined.All())
	}

	// A grant row naming a role the catalog no longer declares contributes
	// nothing and is reported, rather than failing the request or being
	// interpreted as something else.
	stale, unknown := catalog.Grants("billing_clerk", "retired_role", "retired_role")
	if !stale.Has(authz.MustPermission("invoices.read")) {
		t.Error("a stale grant discarded the valid ones with it")
	}
	if len(unknown) != 1 || unknown[0] != "retired_role" {
		t.Errorf("unknown = %v, want the stale name once", unknown)
	}

	// No roles at all grants nothing. Default deny is the shape of the empty
	// case, not a rule applied on top of it.
	empty, _ := catalog.Grants()
	if empty.Len() != 0 {
		t.Errorf("granting no roles produced %v", empty.All())
	}
}

func TestCatalogDeclaresAndEnumerates(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	if !catalog.Declares(authz.MustPermission("invoices.void")) {
		t.Error("a declared permission is not declared")
	}
	if catalog.Declares(authz.MustPermission("invoices.delete")) {
		t.Error("an undeclared permission is declared")
	}
	if catalog.Declares("") {
		t.Error("the zero permission is declared")
	}

	permissions := catalog.Permissions()
	if len(permissions) != 4 || permissions[0] != "invoices.create" {
		t.Errorf("Permissions = %v, want a sorted list of four", permissions)
	}
	names := catalog.RoleNames()
	if len(names) != 3 || names[0] != "billing_clerk" {
		t.Errorf("RoleNames = %v, want a sorted list of three", names)
	}

	role, ok := catalog.Role("billing_manager")
	if !ok || role.Permissions().Len() != 3 {
		t.Errorf("Role = %#v, %t", role, ok)
	}
	if _, ok := catalog.Role("nobody"); ok {
		t.Error("an undeclared role was returned")
	}
}

func TestMustPermissionPanicsOnlyOnABadName(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("MustPermission accepted an invalid name")
		}
	}()
	_ = authz.MustPermission("invoices")
}
