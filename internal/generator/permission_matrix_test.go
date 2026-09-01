package generator_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// permissionConstant matches a permission declaration in the generated
// catalog: `UsersRead = authz.MustPermission("users.read")`.
var permissionConstant = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*authz\.MustPermission\("([a-z0-9_.]+)"\)`)

// permissionCheck matches an enforcement site: `authorization.UsersRead`
// appearing as an argument to Require or RequireAll.
var permissionCheck = regexp.MustCompile(`authorization\.(?:Require|RequireAll)\(ctx,\s*([^)]+)\)`)

// TestEveryPermissionIsEnforcedSomewhere is the check that turns the
// permission catalog from a list into a control.
//
// A permission that is declared and never checked is worse than one that does
// not exist. It appears in the catalog, a reviewer counts it, an
// administrator grants a role expecting it to mean something, and the
// operation it was supposed to guard is open to everyone who can reach it.
// Nothing about the catalog reveals that — the permission is right there.
//
// So: every permission the generated catalog declares must appear in at least
// one Require or RequireAll call in the generated tree, and every permission
// named in such a call must be one the catalog declares.
func TestEveryPermissionIsEnforcedSomewhere(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())

	catalog := content["internal/platform/authorization/catalog.go"]
	if catalog == "" {
		t.Fatal("a generated project has no permission catalog")
	}

	declared := map[string]string{} // Go identifier -> permission name
	for _, match := range permissionConstant.FindAllStringSubmatch(catalog, -1) {
		declared[match[1]] = match[2]
	}
	if len(declared) == 0 {
		t.Fatal("the catalog declares no permissions; this test is checking nothing")
	}

	enforced := map[string]bool{}
	for path, body := range content {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		if strings.HasPrefix(path, "internal/platform/authorization/") {
			// The authorization package declares the permissions; it does
			// not check them.
			continue
		}
		for _, match := range permissionCheck.FindAllStringSubmatch(body, -1) {
			for _, argument := range strings.Split(match[1], ",") {
				name := strings.TrimSpace(argument)
				identifier, ok := strings.CutPrefix(name, "authorization.")
				if !ok {
					continue
				}
				if _, isPermission := declared[identifier]; !isPermission {
					t.Errorf("%s checks authorization.%s, which the catalog does not declare", path, identifier)
					continue
				}
				enforced[identifier] = true
			}
		}
	}

	unenforced := make([]string, 0, len(declared))
	for identifier, name := range declared {
		if !enforced[identifier] {
			unenforced = append(unenforced, name)
		}
	}
	slices.Sort(unenforced)
	if len(unenforced) != 0 {
		t.Errorf("these permissions are declared and never checked, so granting them means nothing and the operations they name are open: %v", unenforced)
	}
}

// TestTheGeneratedProjectChecksItsOwnMatrix pins that the matrix test ships,
// and that it checks both directions.
//
// One direction alone is the trap. A test asserting "the administrator can
// manage roles" keeps passing after somebody adds roles.manage to the auditor
// bundle — which is the change that actually needs noticing, and the only one
// that widens access.
func TestTheGeneratedProjectChecksItsOwnMatrix(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())
	matrix := content["internal/platform/authorization/matrix_test.go"]
	if matrix == "" {
		t.Fatal("a generated project ships no permission-matrix test")
	}

	for _, name := range []string{
		"TestEveryRoleGrantsExactlyTheMatrix",
		"TestNoRoleGrantsAnythingOutsideTheMatrix",
		"TestEveryRoleInTheCatalogIsInTheMatrix",
		"TestAReadOnlyRoleHoldsNoWritePermission",
		"TestEveryCataloguedPermissionIsHeldBySomeRole",
		"TestPermissionNamesAreStable",
	} {
		if !strings.Contains(matrix, name) {
			t.Errorf("the matrix test lacks %s", name)
		}
	}

	// The read-only assertion must be independent of the table, or a change
	// that widens a bundle and edits the table to match would pass.
	if !strings.Contains(matrix, "var writePermissions = []string{") {
		t.Error("the read-only property is expressed only through the matrix table, so making the table agree with a mistake would pass")
	}
}

// TestTenantIsolationIsCoveredWhereItExists is the other half of M9-004.
//
// The isolation tests themselves are in internal/features/organizations and
// are proved against a real database; this asserts they ship, so a change
// that removed them would not quietly pass as "no tests to run".
func TestTenantIsolationIsCoveredWhereItExists(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())
	for path, names := range map[string][]string{
		"internal/features/organizations/organizations_test.go": {
			"TestOneTenantCannotReadAnother",
			"TestOneTenantCannotWriteIntoAnother",
			"TestOneTenantCannotUpdateOrDeleteAnothers",
			"TestATransactionWithNoTenantSeesNothing",
			"TestRowLevelSecurityIsForcedNotMerelyEnabled",
		},
		"internal/features/organizations/tenant_leak_test.go": {
			"TestConcurrentTenantsOnASmallPoolNeverSeeEachOther",
			"TestAJobWithNoTenantContextSeesNothing",
		},
	} {
		body := content[path]
		if body == "" {
			t.Errorf("%s is missing", path)
			continue
		}
		for _, name := range names {
			if !strings.Contains(body, name) {
				t.Errorf("%s lacks %s", path, name)
			}
		}
	}

	// A project without the module has no tenants to isolate, and must not
	// pretend otherwise.
	without := planContent(t, defaultOptions())
	for path := range without {
		if strings.HasPrefix(path, "internal/features/organizations/") {
			t.Errorf("a project without the organizations module contains %s", path)
		}
	}
}
