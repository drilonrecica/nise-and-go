package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDeclaresAClosedPermissionCatalog(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	wants := map[string][]string{
		"db/migrations/00005_authorization.sql": {
			"CREATE TABLE user_roles",
			"CONSTRAINT user_roles_pkey PRIMARY KEY (user_id, role)",
			"REFERENCES users (id) ON DELETE CASCADE",
			"-- +goose Down",
		},
		"internal/platform/authorization/catalog.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/authz"`,
			"func Catalog() (*authz.Catalog, error)",
			`RoleAdministrator = "administrator"`,
			`RoleAuditor = "auditor"`,
			`AuditRead = authz.MustPermission("audit.read")`,
		},
		"internal/features/auth/roles.go": {
			"func (r *Roles) Grant(ctx context.Context, userID, role, grantedBy string) error",
			"func (r *Roles) Permissions(ctx context.Context, userID string) (authz.Set, []string, error)",
			"func (r *Roles) Holders(ctx context.Context, role string, limit int) ([]Holder, error)",
			"r.catalog.Role(role); !declared",
		},
		"internal/features/auth/queries/users.sql": {
			"-- name: GrantRole :execrows",
			"ON CONFLICT (user_id, role) DO NOTHING",
			"-- name: RevokeRole :execrows",
			"-- name: ListUserRoles :many",
			"-- name: ListRoleHolders :many",
		},
		"internal/features/auth/roles_test.go": {
			"TestGrantAndRevokeRoles",
			"TestGrantRefusesUndeclaredRolesAndUnknownAccounts",
			"TestPermissionsResolveThroughTheCatalog",
			"TestAStaleGrantContributesNothingAndIsReported",
		},
		"internal/platform/authorization/catalog_test.go": {
			"TestEveryDeclaredPermissionIsBundledBySomeRole",
			"TestAuditorCanSeeButNotChange",
			"TestGrantingNoRolesGrantsNothing",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The role's meaning is code. A grant row carries a name, never a
	// permission list, or two replicas could disagree about what a role means.
	migration := content["db/migrations/00005_authorization.sql"]
	if strings.Contains(migration, "permission") {
		t.Error("the role assignment table stores permissions; role meaning must be code")
	}

	// No wildcard may appear in a declared permission: a pattern is how an
	// accidental grant becomes invisible.
	catalog := content["internal/platform/authorization/catalog.go"]
	if strings.Contains(catalog, `MustPermission("*`) || strings.Contains(catalog, `.*")`) {
		t.Error("the catalog declares a wildcard permission")
	}
}
